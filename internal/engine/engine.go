package engine

import (
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
	"github.com/MarcoColomb0/rightsizer/internal/report"
	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

type Phase string

const (
	Idle         Phase = "idle"
	NeedPassword Phase = "need-password"
	Running      Phase = "running"
	Done         Phase = "done"
)

const (
	maxAuthFails = 3
	MinDuration  = 24 * time.Hour
	MaxDuration  = 14 * 24 * time.Hour
)

var (
	pollEvery      = 5 * time.Minute
	inventoryEvery = time.Hour
)

type Config struct {
	Host        string
	User        string
	Fingerprint string
	Duration    time.Duration
	Profile     string
	Clusters    []string
}

type state struct {
	Config    Config
	Phase     Phase
	Started   time.Time
	Ends      time.Time
	Finished  time.Time
	About     string
	Inventory *vc.Inventory
	Store     *analysis.Store
	Report    string
}

type Status struct {
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
	Result     *analysis.Result
	Share      *report.Share
	ReportFile string
}

type Engine struct {
	dir    string
	web    *report.Server
	mu     sync.Mutex
	st     state
	client *vc.Client
	cancel context.CancelFunc
	wg     sync.WaitGroup

	lastPoll, nextPoll time.Time
	lastErr            string
	authFails          int
	result             *analysis.Result
}

func New(dir string, web *report.Server) (*Engine, error) {
	e := &Engine{dir: dir, web: web, st: state{Phase: Idle}}
	if err := e.load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("could not load previous state, starting fresh", "err", err)
		e.st = state{Phase: Idle}
	}
	if e.st.Phase == Running {
		e.st.Phase = NeedPassword
	}
	e.refreshResult()
	return e, nil
}

func (e *Engine) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := Status{
		Phase: e.st.Phase, Config: e.st.Config, Started: e.st.Started, Ends: e.st.Ends,
		Finished: e.st.Finished, About: e.st.About, LastPoll: e.lastPoll, NextPoll: e.nextPoll,
		LastError: e.lastErr, Share: e.web.Current(), ReportFile: e.st.Report,
	}
	if e.result != nil {
		r := *e.result
		r.VMs = nil
		r.Clusters = slices.Clone(r.Clusters)
		for i := range r.Clusters {
			if n := len(r.Clusters[i].Points); n > 72 {
				r.Clusters[i].Points = r.Clusters[i].Points[n-72:]
			}
		}
		s.Result = &r
	}
	if e.st.Store != nil {
		s.Polls = e.st.Store.Polls
	}
	return s
}

func (e *Engine) Start(ctx context.Context, cfg Config, password string) error {
	if cfg.Duration < MinDuration || cfg.Duration > MaxDuration {
		return fmt.Errorf("duration must be between %s and %s", MinDuration, MaxDuration)
	}
	e.mu.Lock()
	if e.st.Phase == Running || e.st.Phase == NeedPassword {
		e.mu.Unlock()
		return errors.New("an analysis is already in progress; finish or cancel it first")
	}
	e.mu.Unlock()

	cl, err := vc.Connect(ctx, vc.Credentials{Host: cfg.Host, User: cfg.User, Password: password, Fingerprint: cfg.Fingerprint})
	if err != nil {
		return err
	}
	now := time.Now()
	e.web.Stop()
	e.mu.Lock()
	e.st = state{
		Config: cfg, Phase: Running, Started: now, Ends: now.Add(cfg.Duration),
		About: cl.About(), Store: analysis.NewStore(),
	}
	e.client = cl
	e.lastErr, e.authFails = "", 0
	e.result = nil
	e.mu.Unlock()
	e.run()
	return nil
}

func (e *Engine) Resume(ctx context.Context, password string) error {
	e.mu.Lock()
	if e.st.Phase != NeedPassword {
		e.mu.Unlock()
		return errors.New("nothing to resume")
	}
	cfg := e.st.Config
	e.mu.Unlock()
	cl, err := vc.Connect(ctx, vc.Credentials{Host: cfg.Host, User: cfg.User, Password: password, Fingerprint: cfg.Fingerprint})
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.client = cl
	e.st.Phase = Running
	e.lastErr, e.authFails = "", 0
	e.mu.Unlock()
	e.run()
	return nil
}

func (e *Engine) Finish() error {
	e.mu.Lock()
	ph := e.st.Phase
	e.mu.Unlock()
	if ph != Running && ph != NeedPassword {
		return errors.New("no analysis in progress")
	}
	e.stop()
	return e.finalize()
}

func (e *Engine) Cancel() error {
	e.stop()
	e.web.Stop()
	e.mu.Lock()
	defer e.mu.Unlock()
	e.st = state{Phase: Idle}
	e.result = nil
	e.lastErr = ""
	if err := os.Remove(e.statePath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Publish renders the current results to PDF and exposes them on the
// temporary HTTPS download server.
func (e *Engine) Publish() (*report.Share, error) {
	e.mu.Lock()
	res := e.result
	e.mu.Unlock()
	if res == nil {
		return nil, errors.New("no results yet; wait for the first samples")
	}
	path, err := e.writePDF(res)
	if err != nil {
		return nil, err
	}
	return e.web.Serve(path)
}

func (e *Engine) Unpublish() { e.web.Stop() }

func (e *Engine) Shutdown() {
	e.stop()
	e.web.Stop()
	e.mu.Lock()
	defer e.mu.Unlock()
	_ = e.save()
}

func (e *Engine) run() {
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.loop(ctx)
	}()
}

func (e *Engine) stop() {
	e.mu.Lock()
	cancel, cl := e.cancel, e.client
	e.cancel, e.client = nil, nil
	e.mu.Unlock()
	if cancel != nil {
		cancel()
		e.wg.Wait()
	}
	if cl != nil {
		ctx, c := context.WithTimeout(context.Background(), 10*time.Second)
		cl.Close(ctx)
		c()
	}
}

func (e *Engine) loop(ctx context.Context) {
	var lastInv time.Time
	for {
		e.mu.Lock()
		ends := e.st.Ends
		e.mu.Unlock()
		if !time.Now().Before(ends) {
			go func() {
				e.stop()
				if err := e.finalize(); err != nil {
					slog.Error("finalize", "err", err)
				}
			}()
			return
		}
		if time.Since(lastInv) >= inventoryEvery {
			if err := e.refreshInventory(ctx); err != nil {
				if e.fail(err) {
					return
				}
			} else {
				lastInv = time.Now()
			}
		}
		if err := e.poll(ctx); err != nil {
			if e.fail(err) {
				return
			}
		}
		next := time.Now().Add(pollEvery)
		if next.After(ends) {
			next = ends
		}
		e.mu.Lock()
		e.nextPoll = next
		e.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
	}
}

// fail records err and reports whether the loop must stop. Repeated login
// failures pause the analysis so a changed password cannot lock the account.
func (e *Engine) fail(err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	slog.Warn("collection error", "err", err)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastErr = fmt.Sprintf("%s: %v", time.Now().Format("Jan 02 15:04"), err)
	if !vc.IsAuthError(err) {
		return false
	}
	e.authFails++
	if e.authFails < maxAuthFails {
		return false
	}
	e.st.Phase = NeedPassword
	e.client = nil
	e.cancel = nil
	_ = e.save()
	return true
}

func (e *Engine) refreshInventory(ctx context.Context) error {
	e.mu.Lock()
	cl, filter := e.client, e.st.Config.Clusters
	e.mu.Unlock()
	inv, err := cl.Inventory(ctx)
	if err != nil {
		return err
	}
	if len(filter) > 0 {
		inv = filterClusters(inv, filter)
	}
	e.mu.Lock()
	e.st.Inventory = inv
	e.mu.Unlock()
	return nil
}

func (e *Engine) poll(ctx context.Context) error {
	e.mu.Lock()
	cl, inv, store := e.client, e.st.Inventory, e.st.Store
	e.mu.Unlock()
	if inv == nil {
		return errors.New("inventory not loaded yet")
	}
	now, err := cl.Now(ctx)
	if err != nil {
		return err
	}
	e.mu.Lock()
	since := store.Newest()
	e.mu.Unlock()
	if !since.IsZero() {
		since = since.Add(-time.Minute)
		if now.Sub(since) > 55*time.Minute {
			since = now.Add(-55 * time.Minute)
		}
	}

	var vms []string
	vcpu := map[string]int{}
	for _, v := range inv.VMs {
		if v.PowerOn && !v.Template {
			vms = append(vms, v.Ref)
			vcpu[v.Ref] = v.VCPU
		}
	}
	var hosts []string
	for _, h := range inv.Hosts {
		if h.Connected {
			hosts = append(hosts, h.Ref)
		}
	}
	vs, err := cl.Sample(ctx, "VirtualMachine", vms, vc.VMMetrics, since)
	if err != nil {
		return err
	}
	hs, err := cl.Sample(ctx, "HostSystem", hosts, vc.HostMetrics, since)
	if err != nil {
		return err
	}
	hc, cp := analysis.Capacity(inv)

	e.mu.Lock()
	for _, s := range vs {
		store.AddVM(s, vcpu[s.Ref])
	}
	store.AddHosts(hs, hc, cp)
	store.Polls++
	e.lastPoll = time.Now()
	e.lastErr = ""
	e.authFails = 0
	err = e.save()
	e.mu.Unlock()
	e.refreshResult()
	return err
}

func (e *Engine) finalize() error {
	e.mu.Lock()
	if e.st.Phase == Idle || e.st.Phase == Done {
		e.mu.Unlock()
		return nil
	}
	e.st.Phase = Done
	e.st.Finished = time.Now()
	e.nextPoll = time.Time{}
	e.mu.Unlock()
	e.refreshResult()
	e.mu.Lock()
	res := e.result
	e.mu.Unlock()
	if res == nil {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.save()
	}
	path, err := e.writePDF(res)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.st.Report = path
	err = e.save()
	e.mu.Unlock()
	if err != nil {
		return err
	}
	_, err = e.web.Serve(path)
	return err
}

func (e *Engine) refreshResult() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.st.Inventory == nil || e.st.Store == nil {
		return
	}
	end := time.Now()
	if !e.st.Finished.IsZero() {
		end = e.st.Finished
	}
	r := analysis.Analyze(e.st.Inventory, e.st.Store, analysis.ProfileByName(e.st.Config.Profile), e.st.Started, end, e.st.Config.Duration)
	r.Final = e.st.Phase == Done
	r.VCenter = fmt.Sprintf("%s — %s", e.st.Config.Host, e.st.About)
	e.result = r
}

func (e *Engine) writePDF(res *analysis.Result) (string, error) {
	dir := filepath.Join(e.dir, "reports")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	kind := "interim"
	if res.Final {
		kind = "final"
	}
	path := filepath.Join(dir, fmt.Sprintf("rightsizer-%s-%s.pdf", kind, res.Generated.Format("20060102-1504")))
	if err := report.WritePDF(res, path); err != nil {
		return "", err
	}
	return path, nil
}

func (e *Engine) statePath() string { return filepath.Join(e.dir, "state.gob") }

func (e *Engine) save() error {
	tmp := e.statePath() + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := gob.NewEncoder(f).Encode(&e.st); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, e.statePath())
}

func (e *Engine) load() error {
	f, err := os.Open(e.statePath())
	if err != nil {
		return err
	}
	defer f.Close()
	return gob.NewDecoder(f).Decode(&e.st)
}

func filterClusters(inv *vc.Inventory, keep []string) *vc.Inventory {
	out := &vc.Inventory{Taken: inv.Taken}
	for _, h := range inv.Hosts {
		if slices.Contains(keep, h.Cluster) {
			out.Hosts = append(out.Hosts, h)
		}
	}
	for _, v := range inv.VMs {
		if slices.Contains(keep, v.Cluster) {
			out.VMs = append(out.VMs, v)
		}
	}
	return out
}
