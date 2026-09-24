package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
	"github.com/MarcoColomb0/rightsizer/internal/report"
	"github.com/MarcoColomb0/rightsizer/internal/vault"
	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

type Phase string

const (
	NeedPassword Phase = "need-password"
	Running      Phase = "running"
	Done         Phase = "done"
)

const (
	maxAuthFails = 3
	maxSources   = 32
	MinDuration  = 24 * time.Hour
	MaxDuration  = 14 * 24 * time.Hour
)

var (
	pollEvery      = 5 * time.Minute
	inventoryEvery = time.Hour
	wasteEvery     = 24 * time.Hour
	historyEvery   = time.Hour
	backgroundTick = time.Minute
	historyBack    = 14 * 24 * time.Hour
)

type Config struct {
	Host        string
	User        string
	Fingerprint string
	Duration    time.Duration
	Profile     string
	Clusters    []string
}

// stateVersion is bumped whenever state changes incompatibly, so an older
// release never overwrites data written by a newer one.
const stateVersion = 1

type state struct {
	Version   int
	Config    Config
	Phase     Phase
	Started   time.Time
	Ends      time.Time
	Finished  time.Time
	About     string
	Inventory *vc.Inventory
	Store     *analysis.Store
	Report    string

	History      *analysis.Store
	HistoryState string
	HistoryLive  *analysis.Store
	HistoryStep  int32
	HistorySync  time.Time
	Orphans      []vc.OrphanDisk
	WasteNote    string
	WasteScanned time.Time
}

type Status struct {
	ID         string
	Phase      Phase
	Config     Config
	Started    time.Time
	Ends       time.Time
	Finished   time.Time
	About      string
	Polls      uint64
	LastPoll   time.Time
	NextPoll   time.Time
	LastError  string
	Totals     *analysis.Totals
	Findings   int
	Preview    bool
	History    string
	Result     *analysis.Result
	ReportFile string
}

type VaultState struct {
	Enabled bool
	Locked  bool
}

type UpgradeInfo struct {
	Version string
	State   string
	Message string
	Time    time.Time
}

type Summary struct {
	Sources []Status
	Shares  []report.Share
	Vault   VaultState
	Upgrade *UpgradeInfo
	// Reboot lists why the appliance needs a restart (appliance only).
	Reboot []string
}

// Engine runs one analysis per vCenter source. With a vault, vCenter
// credentials are stored encrypted and collection resumes after a restart as
// soon as the administrator unlocks it; without one they live in memory only.
type Engine struct {
	dir   string
	web   *report.Server
	vault *vault.Vault

	mu      sync.Mutex
	sources map[string]*Source
	excl    []analysis.Exclusion
}

func New(dir string, web *report.Server, v *vault.Vault) (*Engine, error) {
	e := &Engine{dir: dir, web: web, vault: v, sources: map[string]*Source{}}
	if err := e.migrateLegacy(); err != nil {
		return nil, err
	}
	if err := e.loadExclusions(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(e.sourcesDir())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, d := range entries {
		if !d.IsDir() || !validID(d.Name()) {
			continue
		}
		s := &Source{id: d.Name(), dir: filepath.Join(e.sourcesDir(), d.Name()), eng: e}
		st, err := readState(s.statePath())
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err := preserve(s.statePath(), err); err != nil {
				return nil, err
			}
			continue
		}
		s.st = st
		if s.st.Phase == Running {
			s.st.Phase = NeedPassword
		}
		s.refreshResult()
		e.sources[s.id] = s
	}
	return e, nil
}

// migrateLegacy moves a single-analysis v0.1 state into its own source.
func (e *Engine) migrateLegacy() error {
	old := filepath.Join(e.dir, "state.gob")
	st, err := readState(old)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return preserve(old, err)
	}
	if st.Phase == "idle" || st.Config.Host == "" {
		return os.Remove(old)
	}
	id := newID()
	dir := filepath.Join(e.sourcesDir(), id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(e.dir, "reports")); err == nil {
		if err := os.Rename(filepath.Join(e.dir, "reports"), filepath.Join(dir, "reports")); err != nil {
			return err
		}
		if st.Report != "" {
			st.Report = filepath.Join(dir, "reports", filepath.Base(st.Report))
		}
	}
	if err := writeState(filepath.Join(dir, "state.gob"), &st); err != nil {
		return err
	}
	slog.Info("migrated previous analysis", "source", id, "vcenter", st.Config.Host)
	return os.Remove(old)
}

func preserve(path string, cause error) error {
	kept := fmt.Sprintf("%s.unreadable-%s", path, time.Now().Format("20060102-150405"))
	if err := os.Rename(path, kept); err != nil {
		return fmt.Errorf("cannot load %s (%v) and cannot preserve it: %w", path, cause, err)
	}
	slog.Error("state unreadable, kept a copy", "err", cause, "copy", kept)
	return nil
}

func (e *Engine) sourcesDir() string { return filepath.Join(e.dir, "sources") }

func (e *Engine) VaultState() VaultState {
	if e.vault == nil {
		return VaultState{}
	}
	return VaultState{Enabled: true, Locked: e.vault.Locked()}
}

func (e *Engine) Summary() Summary {
	sum := Summary{Shares: e.web.Shares(), Vault: e.VaultState()}
	for _, s := range e.list() {
		sum.Sources = append(sum.Sources, s.status(false))
	}
	return sum
}

func (e *Engine) Source(id string) (Status, error) {
	s, err := e.get(id)
	if err != nil {
		return Status{}, err
	}
	return s.status(true), nil
}

func (e *Engine) list() []*Source {
	e.mu.Lock()
	out := make([]*Source, 0, len(e.sources))
	for _, s := range e.sources {
		out = append(out, s)
	}
	e.mu.Unlock()
	started := map[*Source]time.Time{}
	for _, s := range out {
		s.mu.Lock()
		started[s] = s.st.Started
		s.mu.Unlock()
	}
	slices.SortFunc(out, func(a, b *Source) int {
		if c := started[a].Compare(started[b]); c != 0 {
			return c
		}
		return strings.Compare(a.id, b.id)
	})
	return out
}

func (e *Engine) get(id string) (*Source, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.sources[id]
	if s == nil {
		return nil, fmt.Errorf("unknown source %q", id)
	}
	return s, nil
}

// Add validates the vCenter connection and starts a new analysis.
func (e *Engine) Add(ctx context.Context, cfg Config, password string) (string, error) {
	if cfg.Duration < MinDuration || cfg.Duration > MaxDuration {
		return "", fmt.Errorf("duration must be between %s and %s", MinDuration, MaxDuration)
	}
	if e.vault != nil && e.vault.Locked() {
		return "", vault.ErrLocked
	}
	existing := e.list()
	for _, s := range existing {
		if st := s.status(false); st.Config.Host == cfg.Host && st.Phase != Done {
			return "", fmt.Errorf("%s is already being analysed", cfg.Host)
		}
	}
	if len(existing) >= maxSources {
		return "", fmt.Errorf("at most %d sources", maxSources)
	}
	id := newID()
	s := &Source{id: id, dir: filepath.Join(e.sourcesDir(), id), eng: e, st: state{Config: cfg}}
	cl, err := s.connect(ctx, password)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		cl.Close(ctx)
		return "", err
	}
	if e.vault != nil {
		if err := e.vault.Put(id, vault.Secret{User: cfg.User, Password: password}); err != nil {
			cl.Close(ctx)
			os.RemoveAll(s.dir)
			return "", err
		}
	}
	e.mu.Lock()
	e.sources[id] = s
	e.mu.Unlock()
	s.begin(cl)
	slog.Info("source added", "source", id, "vcenter", cfg.Host)
	return id, nil
}

// Resume restarts a paused source. An empty password uses the vault.
func (e *Engine) Resume(ctx context.Context, id, password string) error {
	s, err := e.get(id)
	if err != nil {
		return err
	}
	if s.phase() != NeedPassword {
		return errors.New("source is not paused")
	}
	stored := password == ""
	if stored {
		if e.vault == nil {
			return errors.New("password required")
		}
		sec, err := e.vault.Get(id)
		if err != nil {
			return err
		}
		password = sec.Password
	}
	cl, err := s.connect(ctx, password)
	if err != nil {
		return err
	}
	if !stored && e.vault != nil && !e.vault.Locked() {
		if err := e.vault.Put(id, vault.Secret{User: s.status(false).Config.User, Password: password}); err != nil {
			slog.Warn("could not store updated credentials", "source", id, "err", err)
		}
	}
	s.resume(cl)
	return nil
}

// Unlock opens the vault and resumes every paused source in the background.
func (e *Engine) Unlock(password string) error {
	if e.vault == nil {
		return nil
	}
	wasLocked := e.vault.Locked()
	if err := e.vault.Unlock(password); err != nil {
		return err
	}
	if wasLocked {
		go e.resumeAll()
	}
	return nil
}

func (e *Engine) resumeAll() {
	for _, s := range e.list() {
		if s.phase() != NeedPassword {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		if err := e.Resume(ctx, s.id, ""); err != nil {
			slog.Warn("automatic resume failed", "source", s.id, "err", err)
			s.mu.Lock()
			s.lastErr = fmt.Sprintf("%s: resume failed: %v", time.Now().Format("Jan 02 15:04"), err)
			s.mu.Unlock()
		}
		cancel()
	}
}

func (e *Engine) Finish(id string) error {
	s, err := e.get(id)
	if err != nil {
		return err
	}
	if ph := s.phase(); ph != Running && ph != NeedPassword {
		return errors.New("analysis already finished")
	}
	s.stop()
	return s.finalize()
}

func (e *Engine) Remove(id string) error {
	s, err := e.get(id)
	if err != nil {
		return err
	}
	s.stop()
	for _, sh := range e.web.Shares() {
		if sh.Source == id {
			e.web.Stop(sh.ID)
		}
	}
	e.mu.Lock()
	delete(e.sources, id)
	e.mu.Unlock()
	if e.vault != nil && !e.vault.Locked() {
		_ = e.vault.Delete(id)
	}
	slog.Info("source removed", "source", id)
	return os.RemoveAll(s.dir)
}

// Publish renders a source's current results, or all sources combined when id
// is empty, and shares the PDF on the download server.
func (e *Engine) Publish(id string) (*report.Share, error) {
	var res *analysis.Result
	dir := filepath.Join(e.dir, "reports")
	if id == "" {
		var rs []*analysis.Result
		for _, s := range e.list() {
			if r := s.currentResult(); r != nil {
				rs = append(rs, r)
			}
		}
		if len(rs) == 0 {
			return nil, errors.New("no results yet")
		}
		res = analysis.Merge(rs)
		id = "all"
	} else {
		s, err := e.get(id)
		if err != nil {
			return nil, err
		}
		res = s.currentResult()
		dir = filepath.Join(s.dir, "reports")
	}
	if res == nil {
		return nil, errors.New("no results yet; wait for the first samples")
	}
	path, err := writePDF(dir, res)
	if err != nil {
		return nil, err
	}
	return e.web.Serve(id, path)
}

func (e *Engine) StopShare(id string) { e.web.Stop(id) }

func (e *Engine) ChangePassword(old, next string) error {
	if e.vault == nil {
		return errors.New("no administrator password in this installation")
	}
	return e.vault.ChangePassword(old, next)
}

// LatestReport returns the newest PDF on disk.
func (e *Engine) LatestReport() (string, error) {
	var best string
	var bt time.Time
	_ = filepath.WalkDir(e.dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".pdf") {
			return nil
		}
		if fi, err := d.Info(); err == nil && fi.ModTime().After(bt) {
			best, bt = p, fi.ModTime()
		}
		return nil
	})
	if best == "" {
		return "", errors.New("no report yet: finish an analysis or publish one from the console")
	}
	return best, nil
}

func (e *Engine) Shutdown() {
	for _, s := range e.list() {
		s.stop()
		s.mu.Lock()
		_ = s.save()
		s.mu.Unlock()
	}
	e.web.Stop("")
}

func newID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func validID(s string) bool {
	if len(s) != 8 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
