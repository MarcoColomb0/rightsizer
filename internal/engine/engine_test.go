package engine

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
	"github.com/MarcoColomb0/rightsizer/internal/report"
	"github.com/MarcoColomb0/rightsizer/internal/vault"
	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

func TestMain(m *testing.M) {
	pollEvery = 100 * time.Millisecond
	historyEvery = 300 * time.Millisecond
	backgroundTick = 200 * time.Millisecond
	os.Exit(m.Run())
}

type sim struct {
	host, user, pass, fp string
	close                func()
}

func newSim(t *testing.T) sim {
	t.Helper()
	m := simulator.VPX()
	m.Host, m.Cluster, m.Machine = 3, 2, 4
	if err := m.Create(); err != nil {
		t.Fatal(err)
	}
	m.Service.TLS = new(tls.Config)
	s := m.Service.NewServer()
	pw, _ := s.URL.User.Password()
	ci, err := vc.Probe(context.Background(), s.URL.Host)
	if err != nil {
		t.Fatal(err)
	}
	return sim{s.URL.Host, s.URL.User.Username(), pw, ci.Fingerprint, func() { s.Close(); m.Remove() }}
}

func (v sim) cfg() Config {
	return Config{Host: v.host, User: v.user, Fingerprint: v.fp, Duration: 24 * time.Hour, Profile: "balanced"}
}

type fetched struct {
	StatusCode int
	Header     http.Header
	TLS        *tls.ConnectionState
}

// download fetches a shared report, trusting the server's self-signed
// certificate.
func download(t *testing.T, url string) (fetched, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	cl := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	res, err := cl.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return fetched{res.StatusCode, res.Header, res.TLS}, body
}

func waitPolls(t *testing.T, e *Engine, id string, n uint64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		st, err := e.Source(id)
		if err == nil && st.Polls >= n && st.Result != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no polls: %+v %v", st, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestMultiSourceWithVault(t *testing.T) {
	a, b := newSim(t), newSim(t)
	defer a.close()
	defer b.close()

	dir := t.TempDir()
	web := &report.Server{Listen: "127.0.0.1:0", PublicHost: "127.0.0.1", TTL: time.Hour}
	v := vault.Open(dir)
	admin := "appliance admin password"
	if err := v.Reset(admin); err != nil {
		t.Fatal(err)
	}
	e, err := New(dir, web, v)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := e.Add(ctx, Config{Duration: time.Hour}, "x"); err == nil {
		t.Fatal("short duration must be rejected")
	}
	ida, err := e.Add(ctx, a.cfg(), a.pass)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Add(ctx, a.cfg(), a.pass); err == nil {
		t.Fatal("same vCenter twice must be rejected")
	}
	idb, err := e.Add(ctx, b.cfg(), b.pass)
	if err != nil {
		t.Fatal(err)
	}
	waitPolls(t, e, ida, 2)
	waitPolls(t, e, idb, 2)
	deadline := time.Now().Add(20 * time.Second)
	for {
		st, _ := e.Source(ida)
		if strings.HasPrefix(st.History, "imported") && st.Result != nil && st.Result.Preview {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("history not imported: %q preview=%v", st.History, st.Result != nil && st.Result.Preview)
		}
		time.Sleep(100 * time.Millisecond)
	}
	e.mu.Lock()
	src := e.sources[ida]
	e.mu.Unlock()
	var hist *analysis.Store
	for {
		src.mu.Lock()
		scanned := src.st.WasteScanned
		hist = src.st.History
		src.mu.Unlock()
		if !scanned.IsZero() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("datastore scan did not run")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if hist == nil || len(hist.VMs) == 0 {
		t.Fatal("history store empty")
	}
	for {
		src.mu.Lock()
		live := src.st.HistoryLive
		n := 0
		if live != nil {
			n = len(live.VMs)
		}
		src.mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("history is not synced during the window")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if st, _ := e.Source(ida); !strings.Contains(st.History, "synced") {
		t.Fatalf("status must show the history sync: %q", st.History)
	}
	for _, v := range hist.VMs {
		if h := v.Hours(); h < 13.9*24 {
			t.Fatalf("history must cover about 14 days per VM, got %.1f h", h)
		}
	}
	if n := len(e.Summary().Sources); n != 2 {
		t.Fatalf("want 2 sources, got %d", n)
	}

	sh, err := e.Publish("")
	if err != nil {
		t.Fatal(err)
	}
	res, body := download(t, sh.URL)
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(string(body), "%PDF") {
		t.Fatalf("combined download failed: %d", res.StatusCode)
	}
	sum := sha256.Sum256(res.TLS.PeerCertificates[0].Raw)
	if strings.ReplaceAll(sh.Fingerprint, ":", "") != fmt.Sprintf("%X", sum) {
		t.Fatal("served certificate does not match advertised fingerprint")
	}
	bad, _ := download(t, strings.Replace(sh.URL, "/rightsizer-", "/x-", 1))
	if bad.StatusCode != http.StatusNotFound {
		t.Fatalf("wrong file name must 404, got %d", bad.StatusCode)
	}

	if err := e.Finish(idb); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.Source(idb); st.Phase != Done || st.ReportFile == "" {
		t.Fatalf("not finalized: %+v", st.Phase)
	}
	if len(e.Summary().Shares) != 2 {
		t.Fatal("combined and final report should both be shared")
	}
	e.Shutdown()

	// Restart: sources come back paused and the vault is locked until the
	// administrator logs in, which resumes collection with stored credentials.
	v2 := vault.Open(dir)
	e2, err := New(dir, web, v2)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := e2.Source(ida); st.Phase != NeedPassword {
		t.Fatalf("want paused after restart, got %s", st.Phase)
	}
	if !e2.VaultState().Locked {
		t.Fatal("vault must start locked")
	}
	if err := e2.Unlock("wrong password!!"); err == nil {
		t.Fatal("wrong admin password must fail")
	}
	if err := e2.Unlock(admin); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for {
		if st, _ := e2.Source(ida); st.Phase == Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("source not resumed after unlock")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st, _ := e2.Source(idb); st.Phase != Done {
		t.Fatal("finished source must stay done")
	}
	if err := e2.Remove(ida); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sources", ida)); !os.IsNotExist(err) {
		t.Fatal("removed source data must be deleted")
	}
	if _, err := v2.Get(ida); err == nil {
		t.Fatal("removed source credentials must be deleted")
	}
	e2.Shutdown()
}

func TestMemoryOnlyWithoutVault(t *testing.T) {
	a := newSim(t)
	defer a.close()
	dir := t.TempDir()
	web := &report.Server{Listen: "127.0.0.1:0", PublicHost: "127.0.0.1", TTL: time.Hour}
	e, _ := New(dir, web, nil)
	id, err := e.Add(context.Background(), a.cfg(), a.pass)
	if err != nil {
		t.Fatal(err)
	}
	waitPolls(t, e, id, 1)
	e.Shutdown()
	b, _ := os.ReadFile(filepath.Join(dir, "sources", id, "state.gob"))
	if strings.Contains(string(b), a.pass) {
		t.Fatal("password must never be persisted")
	}
	e2, _ := New(dir, web, nil)
	if err := e2.Resume(context.Background(), id, ""); err == nil {
		t.Fatal("resume without vault needs a password")
	}
	if err := e2.Resume(context.Background(), id, a.pass); err != nil {
		t.Fatal(err)
	}
	e2.Shutdown()
}

func TestLegacyMigration(t *testing.T) {
	dir := t.TempDir()
	st := state{Version: 1, Config: Config{Host: "vc-old", Profile: "balanced", Duration: 24 * time.Hour}, Phase: Done, Report: filepath.Join(dir, "reports", "r.pdf")}
	if err := writeState(filepath.Join(dir, "state.gob"), &st); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(dir, "reports"), 0o700)
	_ = os.WriteFile(filepath.Join(dir, "reports", "r.pdf"), []byte("%PDF"), 0o600)
	e, err := New(dir, &report.Server{TTL: time.Hour}, nil)
	if err != nil {
		t.Fatal(err)
	}
	srcs := e.Summary().Sources
	if len(srcs) != 1 || srcs[0].Config.Host != "vc-old" || srcs[0].Phase != Done {
		t.Fatalf("legacy analysis not migrated: %+v", srcs)
	}
	if _, err := os.Stat(srcs[0].ReportFile); err != nil {
		t.Fatalf("legacy report not moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state.gob")); !os.IsNotExist(err) {
		t.Fatal("legacy state must be removed after migration")
	}
}

func TestUnreadableStateIsPreserved(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "sources", "0123abcd"), 0o700)
	if err := os.WriteFile(filepath.Join(dir, "sources", "0123abcd", "state.gob"), []byte("not gob"), 0o600); err != nil {
		t.Fatal(err)
	}
	e, err := New(dir, &report.Server{TTL: time.Hour}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Summary().Sources) != 0 {
		t.Fatal("unreadable source must be skipped")
	}
	m, _ := filepath.Glob(filepath.Join(dir, "sources", "0123abcd", "state.gob.unreadable-*"))
	if len(m) != 1 {
		t.Fatal("unreadable state must be kept")
	}
}

func TestExclusionsPersistAndApply(t *testing.T) {
	a := newSim(t)
	defer a.close()
	dir := t.TempDir()
	web := &report.Server{Listen: "127.0.0.1:0", PublicHost: "127.0.0.1", TTL: time.Hour}
	e, _ := New(dir, web, nil)
	if _, err := e.Exclude(analysis.Exclusion{Name: "DC0_H0_VM*", Note: ""}); err == nil {
		t.Fatal("exclusion without a note must be rejected")
	}
	id, err := e.Exclude(analysis.Exclusion{VCenter: a.host, Name: "DC0_C0_RP0_VM*", Reason: "Vendor requirement", Note: "appliance"})
	if err != nil {
		t.Fatal(err)
	}
	e.Shutdown()

	e2, _ := New(dir, web, nil)
	if xs := e2.Exclusions(); len(xs) != 1 || xs[0].ID != id {
		t.Fatalf("exclusions must survive a restart: %+v", xs)
	}
	src, err := e2.Add(context.Background(), a.cfg(), a.pass)
	if err != nil {
		t.Fatal(err)
	}
	waitPolls(t, e2, src, 1)
	st, _ := e2.Source(src)
	for _, f := range st.Result.Findings {
		if strings.HasPrefix(f.VM, "DC0_C0_RP0_VM") {
			t.Fatalf("excluded VM still has findings: %+v", f)
		}
	}
	if len(st.Result.Excluded) != 1 || len(st.Result.Excluded[0].Matched) == 0 {
		t.Fatalf("new analyses must apply stored exclusions: %+v", st.Result.Excluded)
	}
	if err := e2.Unexclude(id); err != nil || len(e2.Exclusions()) != 0 {
		t.Fatal("unexclude failed")
	}
	e2.Shutdown()
}

func TestSizing(t *testing.T) {
	a, b := newSim(t), newSim(t)
	defer a.close()
	defer b.close()
	dir := t.TempDir()
	web := &report.Server{Listen: "127.0.0.1:0", PublicHost: "127.0.0.1", TTL: time.Hour}
	e, err := New(dir, web, nil)
	if err != nil {
		t.Fatal(err)
	}
	if e.SizingParams() != analysis.DefaultSizing() {
		t.Fatal("defaults expected")
	}
	p := analysis.DefaultSizing()
	p.Growth, p.Groups = 35, "web=DC0_C0*"
	if err := e.SetSizingParams(p); err != nil {
		t.Fatal(err)
	}
	bad := p
	bad.Sockets = 9
	if e.SetSizingParams(bad) == nil {
		t.Fatal("invalid parameters must be rejected")
	}
	ctx := context.Background()
	ida, err := e.Add(ctx, a.cfg(), a.pass)
	if err != nil {
		t.Fatal(err)
	}
	cfg := b.cfg()
	cfg.Clusters = []string{"DC0_C1"}
	idb, err := e.Add(ctx, cfg, b.pass)
	if err != nil {
		t.Fatal(err)
	}
	waitPolls(t, e, ida, 1)
	waitPolls(t, e, idb, 1)
	var sz *analysis.Sizing
	deadline := time.Now().Add(10 * time.Second)
	for {
		sz, err = e.Sizing(ida)
		if err == nil && !sz.EstateTaken.IsZero() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no estate: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if sz.Params.Growth != 35 || len(sz.Clusters) == 0 || len(sz.Hosts) == 0 || sz.VMs != nil || sz.Storage.RawUsed == 0 {
		t.Fatalf("sizing incomplete: %+v", sz)
	}
	if o, ok := sz.Clusters[0].Needs[0].Picked(); !ok || o.Nodes < 2 {
		t.Fatalf("no node recommendation: %+v", sz.Clusters[0].Needs[0])
	}
	sb, err := e.Sizing(idb)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range sb.Hosts {
		if h.Cluster != "DC0_C1" {
			t.Fatalf("cluster filter must apply to the estate: %s", h.Cluster)
		}
	}

	sh, err := e.PublishSizing(ida)
	if err != nil {
		t.Fatal(err)
	}
	if sh.Source != "sizing:"+ida || len(sh.Extra) != 1 || !strings.HasSuffix(sh.Extra[0].URL, "-data.zip") {
		t.Fatalf("share %+v", sh)
	}
	for _, u := range []string{sh.URL, sh.Extra[0].URL} {
		res, body := download(t, u)
		if res.StatusCode != http.StatusOK || len(body) < 100 {
			t.Fatalf("%s: %d", u, res.StatusCode)
		}
		if strings.HasSuffix(u, ".zip") && res.Header.Get("Content-Type") != "application/zip" {
			t.Fatalf("content type %q", res.Header.Get("Content-Type"))
		}
	}
	if _, err := e.Publish(ida); err != nil {
		t.Fatal(err)
	}
	if n := len(e.web.Shares()); n != 2 {
		t.Fatalf("the sizing and the rightsizing report are shared side by side, got %d shares", n)
	}
	all, err := e.PublishSizing("")
	if err != nil || all.Source != "sizing:all" {
		t.Fatalf("combined sizing: %+v %v", all, err)
	}
	merged, err := e.Sizing("")
	if err != nil || !strings.HasPrefix(merged.VCenter, "2 vCenters") {
		t.Fatalf("merged %v %v", merged, err)
	}
	if err := e.Remove(ida); err != nil {
		t.Fatal(err)
	}
	for _, s := range e.web.Shares() {
		if strings.HasSuffix(s.Source, ida) {
			t.Fatalf("share of a removed source still running: %+v", s)
		}
	}
	e.Shutdown()

	e2, err := New(dir, web, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := e2.SizingParams(); got != p {
		t.Fatalf("parameters must survive a restart: %+v", got)
	}
	e2.mu.Lock()
	src := e2.sources[idb]
	e2.mu.Unlock()
	src.mu.Lock()
	kept := src.st.Estate != nil && len(src.st.Estate.Hosts) > 0
	src.mu.Unlock()
	if !kept {
		t.Fatal("the estate must be saved with the source")
	}
	e2.Shutdown()
}
