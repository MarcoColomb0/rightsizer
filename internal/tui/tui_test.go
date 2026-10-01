package tui

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"charm.land/bubbles/v2/list"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
	"github.com/MarcoColomb0/rightsizer/internal/engine"
	"github.com/MarcoColomb0/rightsizer/internal/report"
	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

type fake struct {
	sum      engine.Summary
	src      engine.Status
	added    engine.Config
	changed  [2]string
	resumed  string
	upgraded string
	excl     []analysis.Exclusion
	rebooted bool
	sz       *analysis.Sizing
	params   analysis.SizingParams
	sized    []string
	stopped  []string
}

func (f *fake) Sizing(string) (*analysis.Sizing, error) {
	if f.sz == nil {
		return nil, errors.New("inventory not loaded yet")
	}
	return f.sz, nil
}
func (f *fake) SizingParams() (*analysis.SizingParams, error) {
	p := f.params
	return &p, nil
}
func (f *fake) SetSizingParams(p analysis.SizingParams) error {
	if err := p.Validate(); err != nil {
		return err
	}
	f.params = p
	return nil
}
func (f *fake) PublishSizing(id string) (*report.Share, error) {
	f.sized = append(f.sized, id)
	return &report.Share{}, nil
}

func (f *fake) Summary() (*engine.Summary, error)     { return &f.sum, nil }
func (f *fake) Source(string) (*engine.Status, error) { return &f.src, nil }
func (f *fake) Probe(string) (*vc.CertInfo, error)    { return &vc.CertInfo{Trusted: true}, nil }
func (f *fake) Finish(string) error                   { return nil }
func (f *fake) Remove(string) error                   { return nil }
func (f *fake) Publish(string) (*report.Share, error) { return &report.Share{}, nil }
func (f *fake) StopShare(id string) error             { f.stopped = append(f.stopped, id); return nil }
func (f *fake) Resume(id, pw string) error            { f.resumed = id + ":" + pw; return nil }
func (f *fake) Add(c engine.Config, _ string) (string, error) {
	f.added = c
	return "abcd1234", nil
}
func (f *fake) RequestUpgrade(tag string) error { f.upgraded = tag; return nil }

func (f *fake) Exclusions() ([]analysis.Exclusion, error) { return f.excl, nil }
func (f *fake) Exclude(x analysis.Exclusion) error {
	x.ID = "ex1"
	f.excl = append(f.excl, x)
	return nil
}
func (f *fake) Unexclude(id string) error { f.excl = nil; return nil }
func (f *fake) RequestReboot() error      { f.rebooted = true; return nil }

func (f *fake) ChangePassword(o, n string) error {
	if o != "old password 123" {
		return errors.New("wrong password")
	}
	f.changed = [2]string{o, n}
	return nil
}

func peaksDemo() *analysis.Peaks {
	pk := &analysis.Peaks{SumPeakMHz: 420000, CombinedPeakMHz: 262000, Diversity: 1.6, NaiveHosts: 7, AwareHosts: 4,
		CoPeak:        []analysis.Group{{VMs: []string{"sql-01", "sql-02"}, R: 0.93}},
		Complementary: []analysis.Pair{{A: "batch-01", B: "web-01", R: -0.71}}}
	for d := range 7 {
		for h := range 24 {
			pk.Heatmap[d][h], pk.HeatmapN[d][h] = float64(h*4), 1
		}
	}
	return pk
}

func fakeDemo() *fake {
	now := time.Now()
	res := &analysis.Result{
		Totals:  analysis.Totals{VMs: 120, On: 110, VCPU: 480, RecVCPU: 260, MemMB: 1 << 20, RecMemMB: 600 << 10, Hosts: 8, HostsNeeded: 6, Cores: 256, NeedCores: 150, Reclaim: 2 << 40, Analyzed: 108},
		Preview: true,
		Clusters: []analysis.ClusterResult{{Peaks: peaksDemo(), Name: "prod-cl01", Hosts: 6, CPUP: 41, CPUPeak: 63, MemP: 58, VCPU: 300, RecVCPU: 170, MemMB: 600 << 10, RecMemMB: 380 << 10, HostsNeeded: 4, CapMHz: 1000,
			Points: []analysis.Point{{T: now, CPUMHz: 200}, {T: now.Add(time.Minute), CPUMHz: 600}}}},
		Findings: []analysis.Finding{{UUID: "uuid-sql-01", VM: "sql-01", Kind: analysis.CPUOver, Severity: analysis.High, Current: "16 vCPU", Suggested: "6 vCPU", Confidence: "high"}},
	}
	t := res.Totals
	a := engine.Status{ID: "aaaa0001", Phase: engine.Running, Config: engine.Config{Host: "vcsa01.corp.local", Profile: "balanced"}, Started: now.Add(-24 * time.Hour), Ends: now.Add(13 * 24 * time.Hour), Totals: &t, Findings: 1}
	b := engine.Status{ID: "bbbb0002", Phase: engine.NeedPassword, Config: engine.Config{Host: "vcsa02.corp.local", User: "ro@vsphere.local", Profile: "balanced"}, Started: now.Add(-48 * time.Hour), Ends: now.Add(24 * time.Hour)}
	src := a
	src.Result = res
	opt := analysis.NodeOption{Nodes: 5, Sockets: 2, CoresPerSocket: 16, MemGB: 768, TotalCores: 160, CPUUtil: 36, MemUtil: 62}
	need := analysis.Need{Basis: analysis.BasisProvisioned, Options: []analysis.NodeOption{opt}, Pick: 0,
		Ports: analysis.PortPlan{DataPorts: 2, DataGb: 25, StoragePorts: 2, StorageKind: "FC", StorageGb: 32, OOBPorts: 1}}
	sz := &analysis.Sizing{Params: analysis.DefaultSizing(), Percentile: 95,
		Clusters: []analysis.ClusterSizing{{Name: "prod-cl01", Hosts: 4, Cores: 128, VMs: 40, VMsOff: 2, VCPU: 344, Ratio: 2.7,
			Protocols: []string{"FC", "NFS"}, Links: analysis.Connectivity{NICs: "8 × 10 GbE", NICsDown: 4, HBAs: "8 × FC 16G", StorageMTU: "9000"},
			Needs: []analysis.Need{need, {Basis: analysis.BasisRightsized, Pick: -1}}}},
		Totals: analysis.SizingTotals{Hosts: 4, Cores: 128, MemB: 2 << 40, New: []analysis.NewTotals{{Basis: analysis.BasisProvisioned, Nodes: 5, Cores: 160, MemGB: 3840}, {Basis: analysis.BasisRightsized, Nodes: 4, Cores: 128, MemGB: 2048}}},
		Storage: analysis.StorageSizing{RawUsed: 5 << 40, Plan: 7 << 40, Used: 8 << 40, Capacity: 15 << 40,
			IO: analysis.IOSummary{Available: true, IOPS: 37608, IOPSPeak: 38000, ReadPct: 70, MBps: 890, IOSizeKB: 24, LatencyMs: 2.8}},
		Notes: []string{"Hosts run Intel CPUs."}}
	return &fake{
		sz:     sz,
		params: analysis.DefaultSizing(),
		sum:    engine.Summary{Sources: []engine.Status{a, b}, Shares: []report.Share{{ID: "s1", Source: "all", URL: "https://10.0.0.5:8443/abc/rightsizer-interim.pdf", Fingerprint: "AA:BB", Expires: now.Add(24 * time.Hour)}}, Vault: engine.VaultState{Enabled: true}},
		src:    src,
	}
}

func send(t *testing.T, m Model, msgs ...tea.Msg) Model {
	t.Helper()
	for _, msg := range msgs {
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				m = run(m, c)
			}
			continue
		}
		nm, cmd := m.Update(msg)
		m = run(asModel(nm), cmd)
	}
	return m
}

// run executes commands that talk to the backend; timers and animation
// ticks are left out.
func run(m Model, cmd tea.Cmd) Model {
	if cmd == nil {
		return m
	}
	out := make(chan tea.Msg, 1)
	go func() { out <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-out:
	case <-time.After(50 * time.Millisecond):
		return m
	}
	switch msg := msg.(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			m = run(m, c)
		}
	case summaryMsg, sourceMsg, doneMsg, probeMsg, exclusionsMsg, sizingMsg, sizingParamsMsg, list.FilterMatchesMsg:
		nm, next := m.Update(msg)
		m = run(asModel(nm), next)
	}
	return m
}

func asModel(m tea.Model) Model {
	mm, ok := m.(Model)
	if !ok {
		panic(fmt.Sprintf("unexpected model %T", m))
	}
	return mm
}

// typ types s as one key event, like a paste of plain text.
func typ(s string) tea.KeyPressMsg {
	r := []rune(s)
	return tea.KeyPressMsg{Code: r[0], Text: s}
}

var (
	enter     = tea.KeyPressMsg{Code: tea.KeyEnter}
	esc       = tea.KeyPressMsg{Code: tea.KeyEscape}
	tab       = tea.KeyPressMsg{Code: tea.KeyTab}
	down      = tea.KeyPressMsg{Code: tea.KeyDown}
	right     = tea.KeyPressMsg{Code: tea.KeyRight}
	backspace = tea.KeyPressMsg{Code: tea.KeyBackspace}
	pgdown    = tea.KeyPressMsg{Code: tea.KeyPgDown}
)

func view(t *testing.T, m Model, want ...string) string {
	t.Helper()
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 40})
	v := ansi.Strip(asModel(nm).View().Content)
	if os.Getenv("RIGHTSIZER_TUI_DUMP") != "" {
		t.Log("\n" + v)
	}
	for _, s := range want {
		if !strings.Contains(v, s) {
			t.Fatalf("view missing %q", s)
		}
	}
	return v
}

func TestHomeAndSource(t *testing.T) {
	f := fakeDemo()
	m := New(f, Options{Version: "v1.0.0", AdminSettings: true})
	m = send(t, m, m.fetch()())
	view(t, m, "vCenter sources", "vcsa01.corp.local", "vcsa02.corp.local", "needs password", "480 → 260", "Shared reports", "All sources", "? more")
	view(t, send(t, m, typ("?")), "change password", "sizing PDF + data")

	m = send(t, m, enter)
	if m.scr != scrSource || m.cur != "aaaa0001" {
		t.Fatalf("enter must open the selected source, got %v %q", m.scr, m.cur)
	}
	view(t, m, "collecting", "480 → 260", "prod-cl01", "1 Overview")
	view(t, send(t, m, typ("?")), "finish now")
	view(t, m, "Preview based on vCenter history", "1.6×")
	m = send(t, m, typ("2"))
	view(t, m, "sql-01")
	m = send(t, m, typ("3"))
	view(t, m, "diversity", "1.60×", "if sized on the sum of peaks", "Peak together (r 0.93)", "batch-01 + web-01", "Mon")
	m = send(t, m, esc)
	if m.scr != scrHome {
		t.Fatal("esc must go back home")
	}

	m = send(t, m, down, enter)
	f.src = f.sum.Sources[1]
	m = send(t, m, m.fetch()(), typ("r"))
	if f.resumed != "bbbb0002:" {
		t.Fatalf("with an unlocked vault, resume must use stored credentials, got %q", f.resumed)
	}
}

func TestAddSource(t *testing.T) {
	f := fakeDemo()
	m := New(f, Options{Version: "v1.0.0"})
	m = send(t, m, m.fetch()(), typ("a"))
	view(t, m, "Add a vCenter source", "stored encrypted", "14 days")
	for _, s := range []string{"vc3.corp.local", "\t", "ro@vsphere.local", "\t", "secret"} {
		if s == "\t" {
			m = send(t, m, tab)
		} else {
			m = send(t, m, typ(s))
		}
	}
	for m.focus != fStart {
		m = send(t, m, tab)
	}
	m = send(t, m, enter)
	if f.added.Host != "vc3.corp.local" || f.added.Duration != 14*24*time.Hour {
		t.Fatalf("source not added: %+v", f.added)
	}
	if m.scr != scrSource || m.cur != "abcd1234" {
		t.Fatal("new source must open")
	}
}

func TestChangePassword(t *testing.T) {
	f := fakeDemo()
	m := New(f, Options{Version: "v1.0.0", AdminSettings: true})
	m = send(t, m, m.fetch()(), typ("c"))
	view(t, m, "Change administrator password")
	m = send(t, m, typ("old password 123"), enter, typ("brand new password"), enter, typ("brand new passwordX"), enter)
	view(t, m, "do not match")
	m = send(t, m, backspace, enter)
	if f.changed[1] != "brand new password" {
		t.Fatalf("password not changed: %+v (%s)", f.changed, m.err)
	}

	m2 := New(f, Options{Version: "v1.0.0"})
	m2 = send(t, m2, m2.fetch()(), typ("c"))
	if m2.scr == scrSettings {
		t.Fatal("settings must be hidden without admin settings")
	}
}

func TestUpdatePrompt(t *testing.T) {
	f := fakeDemo()
	m := New(f, Options{Version: "v1.0.0", Latest: "v1.1.0", ReleaseURL: "https://example/v1.1.0", CanUpgrade: true})
	m = send(t, m, m.fetch()())
	if m.scr != scrUpdate {
		t.Fatal("update prompt not shown")
	}
	view(t, m, "Update available", "v1.1.0", "Upgrade now", "Running analyses continue")
	nm, cmd := m.Update(enter)
	if !asModel(nm).UpgradeRequested() || cmd == nil {
		t.Fatal("enter must accept the default Y")
	}
	m = New(f, Options{Version: "v1.0.0", Latest: "v1.1.0", CanUpgrade: true})
	m = send(t, m, m.fetch()(), typ("n"), m.fetch()())
	if m.scr != scrHome || m.UpgradeRequested() {
		t.Fatal("n must skip and the prompt must appear once")
	}
	m = New(f, Options{Version: "v1.0.0", Latest: "v1.1.0"})
	m = send(t, m, m.fetch()())
	if m.scr == scrUpdate {
		t.Fatal("no prompt when upgrades are not possible")
	}

	m = New(f, Options{Version: "v1.0.0", Latest: "v1.1.0", CanUpgrade: true, Appliance: true})
	m = send(t, m, m.fetch()())
	view(t, m, "Log in again")
	m = send(t, m, enter)
	if f.upgraded != "v1.1.0" || m.UpgradeRequested() || m.scr != scrHome {
		t.Fatalf("appliance must request the upgrade from the host, got %q", f.upgraded)
	}
	view(t, m, "Upgrade to v1.1.0 started")
}

func TestExcludeFromFinding(t *testing.T) {
	f := fakeDemo()
	m := New(f, Options{Version: "v1.0.0"})
	m = send(t, m, m.fetch()(), enter)
	m = send(t, m, m.fetch()(), typ("2"), typ("e"))
	if m.scr != scrExclude {
		t.Fatalf("e on a finding must open the form, got %v (%s)", m.scr, m.err)
	}
	view(t, m, "Exclude from recommendations", "sql-01", "follows the VM if it is renamed", "Vendor requirement")
	m = send(t, m, tab, right, tab, right,
		tab, tab, enter)
	if len(f.excl) != 0 || !strings.Contains(m.err, "note") {
		t.Fatalf("a note must be required: %q", m.err)
	}
	m = send(t, m, typ("Vendor requires 16 vCPU"), enter)
	if len(f.excl) != 1 {
		t.Fatalf("exclusion not saved: %s", m.err)
	}
	x := f.excl[0]
	if x.UUID != "uuid-sql-01" || x.VCenter != "vcsa01.corp.local" || len(x.Kinds) != 1 || x.Kinds[0] != analysis.CPUOver || x.ReviewBy.IsZero() || x.Note != "Vendor requires 16 vCPU" {
		t.Fatalf("unexpected exclusion %+v", x)
	}
	if m.scr != scrSource {
		t.Fatal("must return to the source")
	}

	m = send(t, m, esc, typ("x"))
	view(t, m, "Exclusions", "sql-01", "Vendor requires 16 vCPU", "Oversized vCPU")
	m = send(t, m, typ("a"))
	m = send(t, m, typ("citrix-*"), tab, tab, tab, tab, typ("PVS cache"), enter)
	if len(f.excl) != 2 || f.excl[1].Name != "citrix-*" || f.excl[1].VCenter != "" {
		t.Fatalf("pattern exclusion not saved: %+v %s", f.excl, m.err)
	}
	m = send(t, m, typ("d"), typ("y"))
	if len(f.excl) != 0 {
		t.Fatal("exclusion not removed")
	}
}

func TestRestartRequired(t *testing.T) {
	f := fakeDemo()
	f.sum.Reboot = []string{"Appliance updated: kernel settings changed"}

	m := New(f, Options{Version: "v1.0.0"})
	m = send(t, m, m.fetch()(), typ("R"))
	view(t, m, "Restart required", "kernel settings changed")
	if m.confirm != "" {
		t.Fatal("the Docker install cannot restart the host")
	}

	m = New(f, Options{Version: "v1.0.0", Appliance: true})
	m = send(t, m, m.fetch()())
	view(t, m, "Restart required", "Press R to restart now")
	m = send(t, m, typ("R"))
	view(t, m, "Restart the appliance now?")
	m = send(t, m, typ("n"))
	if f.rebooted {
		t.Fatal("n must cancel")
	}
	m = send(t, m, typ("R"), typ("y"))
	if !f.rebooted {
		t.Fatal("y must request the restart")
	}
	view(t, m, "The appliance is restarting")
}

func TestFindingsSearch(t *testing.T) {
	f := fakeDemo()
	r := *f.src.Result
	r.Findings = []analysis.Finding{
		{VM: "web-01", Kind: analysis.CPUOver, Severity: analysis.High, Current: "8 vCPU", Suggested: "2 vCPU"},
		{VM: "sql-01", Kind: analysis.MemOver, Severity: analysis.High, Current: "64 GB", Suggested: "24 GB"},
		{VM: "web-02", Kind: analysis.CPUOver, Severity: analysis.Medium, Current: "4 vCPU", Suggested: "2 vCPU"},
		{VM: "SQL-02", Kind: analysis.Idle, Severity: analysis.Low, Current: "2 vCPU / 8 GB", Suggested: "Decommission"},
	}
	f.src.Result = &r
	m := New(f, Options{Version: "v1.0.0"})
	m = send(t, m, m.fetch()(), enter)
	m = send(t, m, m.fetch()(), typ("2"))

	// incremental: the cursor follows while typing, esc restores it
	m = send(t, m, typ("/"), typ("s"), typ("q"))
	if m.tbl.Cursor() != 1 {
		t.Fatalf("incremental search must preview the first match, cursor %d", m.tbl.Cursor())
	}
	m = send(t, m, esc)
	if m.tbl.Cursor() != 0 || m.srch.query != "" {
		t.Fatal("esc must cancel and restore the cursor")
	}

	// smartcase: lowercase matches both sql-01 and SQL-02
	m = send(t, m, typ("/"), typ("sql"), enter)
	if m.tbl.Cursor() != 1 || len(m.srch.matches) != 2 {
		t.Fatalf("want 2 matches starting at row 1, got cursor %d matches %v", m.tbl.Cursor(), m.srch.matches)
	}
	view(t, m, "/sql", "1/2", "» sql-01")
	m = send(t, m, typ("n"))
	if m.tbl.Cursor() != 3 {
		t.Fatalf("n must go to the next match, cursor %d", m.tbl.Cursor())
	}
	m = send(t, m, typ("n"))
	if m.tbl.Cursor() != 1 || !strings.Contains(m.note, "BOTTOM") {
		t.Fatalf("n must wrap with a note, cursor %d note %q", m.tbl.Cursor(), m.note)
	}
	m = send(t, m, typ("N"))
	if m.tbl.Cursor() != 3 {
		t.Fatalf("N must go backwards, cursor %d", m.tbl.Cursor())
	}

	// uppercase in the pattern makes it case-sensitive
	m = send(t, m, typ("/"), typ("SQL"), enter)
	if len(m.srch.matches) != 1 || m.tbl.Cursor() != 3 {
		t.Fatalf("smartcase: SQL must only match SQL-02, got %v", m.srch.matches)
	}

	// ? searches backwards; no match reports like vim
	m = send(t, m, typ("?"), typ("web"), enter)
	if m.tbl.Cursor() != 2 {
		t.Fatalf("? must find the previous match, cursor %d", m.tbl.Cursor())
	}
	m = send(t, m, typ("/"), typ("oracle"), enter)
	view(t, m, "Pattern not found: oracle")

	// the search survives the periodic refresh, esc clears it, next esc leaves
	m = send(t, m, typ("/"), typ("web"), enter, m.fetch()())
	view(t, m, "» web-01", "» web-02")
	m = send(t, m, esc)
	if m.scr != scrSource || m.srch.query != "" {
		t.Fatal("first esc must clear the search and stay on the findings")
	}
	m = send(t, m, esc)
	if m.scr != scrHome {
		t.Fatal("second esc must go back")
	}
}

func TestSizingTab(t *testing.T) {
	f := fakeDemo()
	m := New(f, Options{Version: "v1.0.0"})
	m = send(t, m, m.fetch()(), enter)
	m = send(t, m, m.fetch()(), typ("4"))
	view(t, m, "4 Sizing", "Recommended nodes", "5 × 2 × 16-core CPU, 768 GB", "160 cores, today 128", "2 × 32G FC", "Rightsized instead: 4 nodes",
		"Raw used", "5.0 TB", "IOPS p95", "37,608", "8 × FC 16G", "storage MTU 9000", "Hosts run Intel CPUs", "sizing PDF + data")
	small := send(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	if v := ansi.Strip(small.View().Content); small.vp.TotalLineCount() <= small.vp.Height() || strings.Contains(v, "Hosts run Intel CPUs") {
		t.Fatalf("a short terminal must scroll the sizing:\n%s", v)
	}
	small = send(t, small, down, pgdown, pgdown)
	if small.vp.YOffset() == 0 || !strings.Contains(ansi.Strip(small.View().Content), "Hosts run Intel CPUs") {
		t.Fatalf("scroll offset %d", small.vp.YOffset())
	}
	m = send(t, m, typ("p"))
	if len(f.sized) != 1 || f.sized[0] != "aaaa0001" {
		t.Fatalf("p on the sizing tab must publish the sizing, got %v", f.sized)
	}

	m = send(t, m, typ("o"))
	if m.scr != scrSizing {
		t.Fatalf("o must open the sizing options, got %v", m.scr)
	}
	view(t, m, "Sizing options", "As provisioned", "Workload groups")
	m = send(t, m, right, tab, tab,
		backspace, backspace, typ("x"))
	for m.szf.focus != soSave {
		m = send(t, m, tab)
	}
	m = send(t, m, enter)
	if !strings.Contains(m.err, "growth") || m.szf.focus != soGrowth {
		t.Fatalf("a bad number must be reported on its field: %q", m.err)
	}
	m = send(t, m, backspace, typ("35"))
	for m.szf.focus != soGroups {
		m = send(t, m, tab)
	}
	m = send(t, m, typ("db=sql*"), enter, enter)
	if f.params.Growth != 35 || f.params.Basis != analysis.BasisRightsized || f.params.Groups != "db=sql*" {
		t.Fatalf("options not saved: %+v (%s)", f.params, m.err)
	}
	if m.scr != scrSource {
		t.Fatal("saving must return to the source")
	}
	view(t, m, "Sizing options saved")

	m = send(t, m, esc, typ("z"))
	if len(f.sized) != 2 || f.sized[1] != "" {
		t.Fatalf("z must publish the combined sizing, got %v", f.sized)
	}
}

func TestSizingFixes(t *testing.T) {
	f := fakeDemo()
	f.sum.Shares = []report.Share{{ID: "r1", Source: "aaaa0001", URL: "https://x/a/r.pdf"}, {ID: "z1", Source: "sizing:aaaa0001", URL: "https://x/b/s.pdf",
		Extra: []report.Link{{Name: "s-data.zip", URL: "https://x/b/s-data.zip"}}}, {ID: "o1", Source: "bbbb0002"}}
	m := New(f, Options{Version: "v1.0.0"})
	m = send(t, m, m.fetch()(), enter)
	m = send(t, m, m.fetch()())
	view(t, m, "Sizing · vcsa01.corp.local", "s-data.zip", "spreadsheet data")
	m = send(t, m, typ("s"))
	if strings.Join(f.stopped, ",") != "r1,z1" {
		t.Fatalf("s must stop every share of the source and only those, stopped %v", f.stopped)
	}
	if m.scr != scrSource {
		t.Fatalf("stopping shares must stay on the source, got screen %v", m.scr)
	}

	m = send(t, m, typ("4"))
	at := m.szAt
	m = send(t, m, sizingMsg{id: "bbbb0002", err: errors.New("late")})
	if m.szAt != at || m.szErr != "" {
		t.Fatal("a reply for another source must be ignored")
	}

	m = send(t, m, typ("o"))
	for m.szf.focus != soCPU {
		m = send(t, m, tab)
	}
	m = send(t, m, backspace, backspace, typ("150"))
	for m.szf.focus != soSave {
		m = send(t, m, tab)
	}
	m = send(t, m, enter)
	if m.szf.focus != soCPU || !strings.Contains(m.err, "CPU target") {
		t.Fatalf("an out-of-range value must focus its field, focus %d err %q", m.szf.focus, m.err)
	}

	f.sz.Clusters[0].Needs[0] = analysis.Need{Basis: analysis.BasisProvisioned, Cores: 300, Pick: -1}
	m = send(t, m, esc, typ("4"))
	view(t, m, "no node shape fits")
}

// zoneText returns the text under a clickable zone.
func zoneText(t *testing.T, m Model, id string) (zone, string) {
	t.Helper()
	content, zones := m.compose()
	lines := strings.Split(ansi.Strip(content), "\n")
	for _, z := range zones {
		if z.id == id {
			var b strings.Builder
			for y := z.y0; y <= z.y1 && y < len(lines); y++ {
				r := []rune(lines[y])
				b.WriteString(string(r[min(z.x0, len(r)):min(z.x1+1, len(r))]))
			}
			return z, b.String()
		}
	}
	t.Fatalf("no zone %q", id)
	return zone{}, ""
}

func click(z zone) tea.MouseClickMsg {
	return tea.MouseClickMsg{X: z.x0 + 1, Y: z.y0, Button: tea.MouseLeft}
}

func TestMouse(t *testing.T) {
	f := fakeDemo()
	m := New(f, Options{Version: "v1.0.0"})
	m = send(t, m, tea.WindowSizeMsg{Width: 130, Height: 40}, m.fetch()())

	z, text := zoneText(t, m, "src:1")
	if !strings.Contains(text, "vcsa02.corp.local") {
		t.Fatalf("card zone covers %q", text)
	}
	m = send(t, m, click(z))
	if m.sel != 1 || m.scr != scrHome {
		t.Fatal("a click must select the card")
	}
	m = send(t, m, click(z))
	if m.scr != scrSource || m.cur != "bbbb0002" {
		t.Fatal("a click on the selected card must open it")
	}

	f.src = f.sum.Sources[0]
	r := *fakeDemo().src.Result
	r.Findings = append(r.Findings, analysis.Finding{VM: "web-01", Kind: analysis.Idle, Current: "2 vCPU", Suggested: "Decommission"})
	f.src.Result = &r
	m = send(t, m, m.fetch()())
	z, text = zoneText(t, m, "key:2")
	if !strings.Contains(text, "2 Findings") {
		t.Fatalf("tab zone covers %q", text)
	}
	m = send(t, m, click(z))
	if m.tab != tabFindings {
		t.Fatal("a click on a tab must open it")
	}
	z, text = zoneText(t, m, "row:1")
	if !strings.Contains(text, "web-01") {
		t.Fatalf("row zone covers %q", text)
	}
	m = send(t, m, click(z))
	if m.tbl.Cursor() != 1 {
		t.Fatal("a click on a finding must select it")
	}
	m = send(t, m, tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if m.tbl.Cursor() != 0 {
		t.Fatal("the wheel must move through the findings")
	}

	m = send(t, m, typ("x"))
	z, text = zoneText(t, m, "key:y")
	if !strings.Contains(text, "Remove") {
		t.Fatalf("dialog button covers %q", text)
	}
	m = send(t, m, click(z))
	if m.scr != scrHome {
		t.Fatal("clicking Remove must confirm")
	}
}
