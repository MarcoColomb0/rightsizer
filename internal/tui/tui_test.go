package tui

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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
	lockedPw bool
}

func (f *fake) Summary() (*engine.Summary, error)     { return &f.sum, nil }
func (f *fake) Source(string) (*engine.Status, error) { return &f.src, nil }
func (f *fake) Probe(string) (*vc.CertInfo, error)    { return &vc.CertInfo{Trusted: true}, nil }
func (f *fake) Finish(string) error                   { return nil }
func (f *fake) Remove(string) error                   { return nil }
func (f *fake) Publish(string) (*report.Share, error) { return &report.Share{}, nil }
func (f *fake) StopShare(string) error                { return nil }
func (f *fake) Resume(id, pw string) error            { f.resumed = id + ":" + pw; return nil }
func (f *fake) Add(c engine.Config, _ string) (string, error) {
	f.added = c
	return "abcd1234", nil
}
func (f *fake) RequestUpgrade(tag string) error { f.upgraded = tag; return nil }

func (f *fake) ChangePassword(o, n string) error {
	if o != "old password 123" {
		return errors.New("wrong password")
	}
	f.changed = [2]string{o, n}
	return nil
}

func demo() *fake {
	now := time.Now()
	res := &analysis.Result{
		Totals: analysis.Totals{VMs: 120, On: 110, VCPU: 480, RecVCPU: 260, MemMB: 1 << 20, RecMemMB: 600 << 10, Hosts: 8, HostsNeeded: 6, Cores: 256, NeedCores: 150, Reclaim: 2 << 40, Analyzed: 108},
		Clusters: []analysis.ClusterResult{{Name: "prod-cl01", Hosts: 6, CPUP: 41, CPUPeak: 63, MemP: 58, VCPU: 300, RecVCPU: 170, MemMB: 600 << 10, RecMemMB: 380 << 10, HostsNeeded: 4, CapMHz: 1000,
			Points: []analysis.Point{{T: now, CPUMHz: 200}, {T: now.Add(time.Minute), CPUMHz: 600}}}},
		Findings: []analysis.Finding{{VM: "sql-01", Kind: analysis.CPUOver, Severity: analysis.High, Current: "16 vCPU", Suggested: "6 vCPU", Confidence: "high"}},
	}
	t := res.Totals
	a := engine.Status{ID: "aaaa0001", Phase: engine.Running, Config: engine.Config{Host: "vcsa01.corp.local", Profile: "balanced"}, Started: now.Add(-24 * time.Hour), Ends: now.Add(13 * 24 * time.Hour), Totals: &t, Findings: 1}
	b := engine.Status{ID: "bbbb0002", Phase: engine.NeedPassword, Config: engine.Config{Host: "vcsa02.corp.local", User: "ro@vsphere.local", Profile: "balanced"}, Started: now.Add(-48 * time.Hour), Ends: now.Add(24 * time.Hour)}
	src := a
	src.Result = res
	return &fake{
		sum: engine.Summary{Sources: []engine.Status{a, b}, Shares: []report.Share{{ID: "s1", Source: "all", URL: "https://10.0.0.5:8443/abc/rightsizer-interim.pdf", Fingerprint: "AA:BB", Expires: now.Add(24 * time.Hour)}}, Vault: engine.VaultState{Enabled: true}},
		src: src,
	}
}

func send(t *testing.T, m Model, msgs ...tea.Msg) Model {
	t.Helper()
	for _, msg := range msgs {
		nm, cmd := m.Update(msg)
		m = run(nm.(Model), cmd)
	}
	return m
}

// run executes commands that talk to the backend, skipping timers.
func run(m Model, cmd tea.Cmd) Model {
	if cmd == nil {
		return m
	}
	switch out := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range out {
			m = run(m, c)
		}
	case summaryMsg, sourceMsg, doneMsg, probeMsg:
		nm, next := m.Update(out)
		m = run(nm.(Model), next)
	}
	return m
}

func keys1(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func view(t *testing.T, m Model, want ...string) string {
	t.Helper()
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 40})
	v := nm.(Model).View()
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
	f := demo()
	m := New(f, Options{Version: "v1.0.0", AdminSettings: true})
	m = send(t, m, m.fetch()())
	view(t, m, "vCenter sources", "vcsa01.corp.local", "vcsa02.corp.local", "needs password", "480 → 260", "Shared reports", "All sources", "change password")

	m = send(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.scr != scrSource || m.cur != "aaaa0001" {
		t.Fatalf("enter must open the selected source, got %v %q", m.scr, m.cur)
	}
	view(t, m, "collecting", "480 → 260", "prod-cl01", "finish now")
	m = send(t, m, keys1("2"))
	view(t, m, "sql-01")
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.scr != scrHome {
		t.Fatal("esc must go back home")
	}

	m = send(t, m, tea.KeyMsg{Type: tea.KeyDown}, tea.KeyMsg{Type: tea.KeyEnter})
	f.src = f.sum.Sources[1]
	m = send(t, m, m.fetch()(), keys1("r"))
	if f.resumed != "bbbb0002:" {
		t.Fatalf("with an unlocked vault, resume must use stored credentials, got %q", f.resumed)
	}
}

func TestAddSource(t *testing.T) {
	f := demo()
	m := New(f, Options{Version: "v1.0.0"})
	m = send(t, m, m.fetch()(), keys1("a"))
	view(t, m, "Add a vCenter source", "stored encrypted", "14 days")
	for _, s := range []string{"vc3.corp.local", "\t", "ro@vsphere.local", "\t", "secret"} {
		if s == "\t" {
			m = send(t, m, tea.KeyMsg{Type: tea.KeyTab})
		} else {
			m = send(t, m, keys1(s))
		}
	}
	for m.focus != fStart {
		m = send(t, m, tea.KeyMsg{Type: tea.KeyTab})
	}
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if f.added.Host != "vc3.corp.local" || f.added.Duration != 14*24*time.Hour {
		t.Fatalf("source not added: %+v", f.added)
	}
	if m.scr != scrSource || m.cur != "abcd1234" {
		t.Fatal("new source must open")
	}
}

func TestChangePassword(t *testing.T) {
	f := demo()
	m := New(f, Options{Version: "v1.0.0", AdminSettings: true})
	m = send(t, m, m.fetch()(), keys1("c"))
	view(t, m, "Change administrator password")
	m = send(t, m, keys1("old password 123"), tea.KeyMsg{Type: tea.KeyEnter}, keys1("brand new password"), tea.KeyMsg{Type: tea.KeyEnter}, keys1("brand new passwordX"), tea.KeyMsg{Type: tea.KeyEnter})
	view(t, m, "do not match")
	m = send(t, m, tea.KeyMsg{Type: tea.KeyBackspace}, tea.KeyMsg{Type: tea.KeyEnter})
	if f.changed[1] != "brand new password" {
		t.Fatalf("password not changed: %+v (%s)", f.changed, m.err)
	}

	m2 := New(f, Options{Version: "v1.0.0"})
	m2 = send(t, m2, m2.fetch()(), keys1("c"))
	if m2.scr == scrSettings {
		t.Fatal("settings must be hidden without admin settings")
	}
}

func TestUpdatePrompt(t *testing.T) {
	f := demo()
	m := New(f, Options{Version: "v1.0.0", Latest: "v1.1.0", ReleaseURL: "https://example/v1.1.0", CanUpgrade: true})
	m = send(t, m, m.fetch()())
	if m.scr != scrUpdate {
		t.Fatal("update prompt not shown")
	}
	view(t, m, "Update available", "v1.1.0", "[Y/n]", "Running analyses continue")
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !nm.(Model).UpgradeRequested() || cmd == nil {
		t.Fatal("enter must accept the default Y")
	}
	m = New(f, Options{Version: "v1.0.0", Latest: "v1.1.0", CanUpgrade: true})
	m = send(t, m, m.fetch()(), keys1("n"), m.fetch()())
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
	view(t, m, "Log in again afterwards")
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if f.upgraded != "v1.1.0" || m.UpgradeRequested() || m.scr != scrHome {
		t.Fatalf("appliance must request the upgrade from the host, got %q", f.upgraded)
	}
	view(t, m, "Upgrade to v1.1.0 started")
}
