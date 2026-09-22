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
	"strings"
	"sync"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
	"github.com/MarcoColomb0/rightsizer/internal/report"
	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

// Source is one vCenter being analysed.
type Source struct {
	id  string
	dir string
	eng *Engine

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

func (s *Source) status(full bool) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{
		ID: s.id, Phase: s.st.Phase, Config: s.st.Config, Started: s.st.Started, Ends: s.st.Ends,
		Finished: s.st.Finished, About: s.st.About, LastPoll: s.lastPoll, NextPoll: s.nextPoll,
		LastError: s.lastErr, ReportFile: s.st.Report, History: s.st.HistoryState,
	}
	if !s.st.HistorySync.IsZero() && strings.HasPrefix(st.History, "imported") {
		st.History += ", synced " + s.st.HistorySync.Local().Format("Jan 02 15:04")
	}
	if s.st.Store != nil {
		st.Polls = s.st.Store.Polls
	}
	if s.result != nil {
		t := s.result.Totals
		st.Totals = &t
		st.Findings = len(s.result.Findings)
		st.Preview = s.result.Preview
		if full {
			r := *s.result
			r.VMs = nil
			r.Clusters = slices.Clone(r.Clusters)
			for i := range r.Clusters {
				if n := len(r.Clusters[i].Points); n > 72 {
					r.Clusters[i].Points = r.Clusters[i].Points[n-72:]
				}
			}
			st.Result = &r
		}
	}
	return st
}

func (s *Source) connect(ctx context.Context, password string) (*vc.Client, error) {
	s.mu.Lock()
	cfg := s.st.Config
	s.mu.Unlock()
	return vc.Connect(ctx, vc.Credentials{Host: cfg.Host, User: cfg.User, Password: password, Fingerprint: cfg.Fingerprint})
}

func (s *Source) begin(cl *vc.Client) {
	now := time.Now()
	s.mu.Lock()
	s.st.Phase, s.st.Started, s.st.Ends = Running, now, now.Add(s.st.Config.Duration)
	s.st.About, s.st.Store = cl.About(), analysis.NewStore(now)
	s.client = cl
	_ = s.save()
	s.mu.Unlock()
	s.run()
}

func (s *Source) resume(cl *vc.Client) {
	s.mu.Lock()
	s.client = cl
	s.st.Phase = Running
	s.lastErr, s.authFails = "", 0
	s.mu.Unlock()
	s.run()
}

func (s *Source) phase() Phase {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Phase
}

func (s *Source) run() {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	s.wg.Add(2)
	go func() {
		defer s.wg.Done()
		s.loop(ctx)
	}()
	go func() {
		defer s.wg.Done()
		s.background(ctx)
	}()
}

// background imports vCenter's history once and scans datastores for
// orphaned disks every day, without delaying the 5-minute polls.
func (s *Source) background(ctx context.Context) {
	for {
		s.mu.Lock()
		cl, inv, hist := s.client, s.st.Inventory, s.st.HistoryState
		scanned, synced := s.st.WasteScanned, s.st.HistorySync
		s.mu.Unlock()
		wait := backgroundTick
		if cl == nil || inv == nil {
			wait = 2 * time.Second
		} else {
			switch {
			case hist == "" || strings.HasPrefix(hist, "loading"):
				s.importHistory(ctx, cl, inv)
			case strings.HasPrefix(hist, "imported") && time.Since(synced) >= historyEvery:
				s.syncHistory(ctx, cl, inv)
			}
			if time.Since(scanned) >= wasteEvery {
				s.scanWaste(ctx, cl, inv)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (s *Source) setHistory(state string) {
	s.mu.Lock()
	s.st.HistoryState = state
	s.mu.Unlock()
}

func (s *Source) importHistory(ctx context.Context, cl *vc.Client, inv *vc.Inventory) {
	s.setHistory("loading")
	plan, err := cl.HistoryPlan(ctx, historyBack)
	if err != nil || len(plan) == 0 {
		if ctx.Err() == nil {
			slog.Warn("vCenter history unavailable", "source", s.id, "err", err)
			s.setHistory("unavailable")
		}
		return
	}
	oldest := plan[len(plan)-1].Start
	hist := analysis.NewStore(oldest)
	var vms, hosts []string
	vcpu := map[string]int{}
	for _, v := range inv.VMs {
		if !v.Template {
			vms = append(vms, v.Ref)
			vcpu[v.Ref] = v.VCPU
		}
	}
	for _, h := range inv.Hosts {
		if h.Connected {
			hosts = append(hosts, h.Ref)
		}
	}
	hc, cp := analysis.Capacity(inv)
	// Oldest window first: samples older than the last one seen are ignored.
	for i := len(plan) - 1; i >= 0; i-- {
		w := plan[i]
		s.setHistory(fmt.Sprintf("loading %d%%", (len(plan)-1-i)*100/len(plan)))
		vs, err := cl.History(ctx, "VirtualMachine", vms, vc.HistoryVMMetrics, w)
		if err == nil {
			var hs []vc.Series
			hs, err = cl.History(ctx, "HostSystem", hosts, vc.HostMetrics, w)
			for _, v := range vs {
				hist.AddVM(v, vcpu[v.Ref])
			}
			hist.AddHosts(hs, hc, cp)
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("vCenter history import failed", "source", s.id, "interval", w.Interval, "err", err)
			s.setHistory("unavailable")
			return
		}
	}
	for _, c := range hist.Clusters {
		c.Flush()
	}
	s.mu.Lock()
	s.st.History = hist
	s.st.HistoryStep = plan[0].Interval
	s.st.HistorySync = time.Now()
	s.st.HistoryState = fmt.Sprintf("imported from %s", oldest.Local().Format("Jan 02"))
	_ = s.save()
	s.mu.Unlock()
	slog.Info("vCenter history imported", "source", s.id, "from", oldest)
	s.refreshResult()
}

// syncHistory keeps reading vCenter's finest historical interval during the
// window. Samples inside the window also go to HistoryLive, which is compared
// with the 20-second data to show how much averaging hides.
func (s *Source) syncHistory(ctx context.Context, cl *vc.Client, inv *vc.Inventory) {
	s.mu.Lock()
	hist, step, started := s.st.History, s.st.HistoryStep, s.st.Started
	if s.st.HistoryLive == nil {
		s.st.HistoryLive = analysis.NewStore(started)
	}
	live := s.st.HistoryLive
	since := hist.Newest()
	s.mu.Unlock()
	now, err := cl.Now(ctx)
	if err != nil || step == 0 {
		return
	}
	if since.IsZero() || since.Before(started) {
		since = started
	}
	w := vc.Window{Interval: step, Start: since, End: now}
	var vms, hosts []string
	vcpu := map[string]int{}
	for _, v := range inv.VMs {
		if !v.Template {
			vms = append(vms, v.Ref)
			vcpu[v.Ref] = v.VCPU
		}
	}
	for _, h := range inv.Hosts {
		if h.Connected {
			hosts = append(hosts, h.Ref)
		}
	}
	vs, err := cl.History(ctx, "VirtualMachine", vms, vc.HistoryVMMetrics, w)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("vCenter history sync failed", "source", s.id, "err", err)
		}
		return
	}
	hs, err := cl.History(ctx, "HostSystem", hosts, vc.HostMetrics, w)
	if err != nil {
		return
	}
	hc, cp := analysis.Capacity(inv)
	s.mu.Lock()
	for _, v := range vs {
		hist.AddVM(v, vcpu[v.Ref])
		live.AddVM(v, vcpu[v.Ref])
	}
	hist.AddHosts(hs, hc, cp)
	live.AddHosts(hs, hc, cp)
	for _, c := range hist.Clusters {
		c.Flush()
	}
	s.st.HistorySync = time.Now()
	_ = s.save()
	s.mu.Unlock()
	s.refreshResult()
}

func (s *Source) scanWaste(ctx context.Context, cl *vc.Client, inv *vc.Inventory) {
	orphans, err := cl.OrphanedDisks(ctx, inv)
	if ctx.Err() != nil {
		return
	}
	note := ""
	switch {
	case errors.Is(err, vc.ErrNoBrowse):
		note = "Orphaned disk check skipped: " + err.Error() + "."
	case err != nil:
		note = "Orphaned disk check incomplete: " + err.Error()
		slog.Warn("datastore scan", "source", s.id, "err", err)
	}
	s.mu.Lock()
	s.st.Orphans, s.st.WasteNote, s.st.WasteScanned = orphans, note, time.Now()
	_ = s.save()
	s.mu.Unlock()
	s.refreshResult()
}

func (s *Source) stop() {
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
	s.mu.Lock()
	cl := s.client
	s.client = nil
	s.mu.Unlock()
	if cl != nil {
		ctx, c := context.WithTimeout(context.Background(), 10*time.Second)
		cl.Close(ctx)
		c()
	}
}

func (s *Source) loop(ctx context.Context) {
	var lastInv time.Time
	for {
		s.mu.Lock()
		ends := s.st.Ends
		s.mu.Unlock()
		if !time.Now().Before(ends) {
			go func() {
				s.stop()
				if err := s.finalize(); err != nil {
					slog.Error("finalize", "source", s.id, "err", err)
				}
			}()
			return
		}
		if time.Since(lastInv) >= inventoryEvery {
			if err := s.refreshInventory(ctx); err != nil {
				if s.fail(err) {
					return
				}
			} else {
				lastInv = time.Now()
			}
		}
		if err := s.poll(ctx); err != nil {
			if s.fail(err) {
				return
			}
		}
		next := time.Now().Add(pollEvery)
		if next.After(ends) {
			next = ends
		}
		s.mu.Lock()
		s.nextPoll = next
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
	}
}

// fail records err and reports whether the loop must stop. Repeated login
// failures pause the analysis so a changed password cannot lock the account.
func (s *Source) fail(err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	slog.Warn("collection error", "source", s.id, "err", err)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastErr = fmt.Sprintf("%s: %v", time.Now().Format("Jan 02 15:04"), err)
	if !vc.IsAuthError(err) {
		return false
	}
	s.authFails++
	if s.authFails < maxAuthFails {
		return false
	}
	s.st.Phase = NeedPassword
	_ = s.save()
	return true
}

func (s *Source) refreshInventory(ctx context.Context) error {
	s.mu.Lock()
	cl, filter := s.client, s.st.Config.Clusters
	s.mu.Unlock()
	inv, err := cl.Inventory(ctx)
	if err != nil {
		return err
	}
	if len(filter) > 0 {
		inv = filterClusters(inv, filter)
	}
	s.mu.Lock()
	s.st.Inventory = inv
	s.mu.Unlock()
	return nil
}

func (s *Source) poll(ctx context.Context) error {
	s.mu.Lock()
	cl, inv, store := s.client, s.st.Inventory, s.st.Store
	s.mu.Unlock()
	if inv == nil {
		return errors.New("inventory not loaded yet")
	}
	now, err := cl.Now(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	since := store.Newest()
	s.mu.Unlock()
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

	s.mu.Lock()
	for _, v := range vs {
		store.AddVM(v, vcpu[v.Ref])
	}
	store.AddHosts(hs, hc, cp)
	store.Polls++
	s.lastPoll = time.Now()
	s.lastErr = ""
	s.authFails = 0
	err = s.save()
	s.mu.Unlock()
	s.refreshResult()
	return err
}

func (s *Source) finalize() error {
	s.mu.Lock()
	if s.st.Phase == Done {
		s.mu.Unlock()
		return nil
	}
	s.st.Phase = Done
	s.st.Finished = time.Now()
	s.nextPoll = time.Time{}
	s.mu.Unlock()
	s.refreshResult()
	s.mu.Lock()
	res := s.result
	s.mu.Unlock()
	if res == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.save()
	}
	path, err := writePDF(filepath.Join(s.dir, "reports"), res)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.st.Report = path
	err = s.save()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	_, err = s.eng.web.Serve(s.id, path)
	return err
}

func (s *Source) refreshResult() {
	s.mu.Lock()
	host := s.st.Config.Host
	s.mu.Unlock()
	excl := s.eng.exclusionsFor(host)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.Inventory == nil || s.st.Store == nil {
		return
	}
	end := time.Now()
	if !s.st.Finished.IsZero() {
		end = s.st.Finished
	}
	r := analysis.Analyze(analysis.Input{
		Inv: s.st.Inventory, RT: s.st.Store, History: s.st.History, Live: s.st.HistoryLive,
		Orphans: s.st.Orphans, WasteNote: s.st.WasteNote, Exclusions: excl,
		Profile: analysis.ProfileByName(s.st.Config.Profile),
		Start:   s.st.Started, End: end, Planned: s.st.Config.Duration,
	})
	r.Final = s.st.Phase == Done
	r.VCenter = fmt.Sprintf("%s — %s", s.st.Config.Host, s.st.About)
	s.result = r
}

func (s *Source) currentResult() *analysis.Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result
}

func writePDF(dir string, res *analysis.Result) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	kind := "interim"
	if res.Final {
		kind = "final"
	}
	path := filepath.Join(dir, fmt.Sprintf("rightsizer-%s-%s.pdf", kind, res.Generated.Format("20060102-150405")))
	if err := report.WritePDF(res, path); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Source) statePath() string { return filepath.Join(s.dir, "state.gob") }

func (s *Source) save() error {
	s.st.Version = stateVersion
	return writeState(s.statePath(), &s.st)
}

func writeState(path string, st *state) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := gob.NewEncoder(f).Encode(st); err != nil {
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
	return os.Rename(tmp, path)
}

func readState(path string) (state, error) {
	var st state
	f, err := os.Open(path)
	if err != nil {
		return st, err
	}
	defer f.Close()
	if err := gob.NewDecoder(f).Decode(&st); err != nil {
		return st, err
	}
	if st.Version > stateVersion {
		return st, fmt.Errorf("state written by a newer rightsizer (format %d > %d)", st.Version, stateVersion)
	}
	return st, nil
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
