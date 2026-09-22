package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/marcocolombo/rightsizer/internal/analysis"
	"github.com/marcocolombo/rightsizer/internal/engine"
	"github.com/marcocolombo/rightsizer/internal/report"
)

func render(t *testing.T, m Model) string {
	t.Helper()
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	v := nm.(Model).View()
	if os.Getenv("RIGHTSIZER_TUI_DUMP") != "" {
		t.Log("\n" + v)
	}
	return v
}

func TestSetupView(t *testing.T) {
	m := New(nil)
	m.scr = scrSetup
	v := render(t, m)
	for _, s := range []string{"New analysis", "vCenter", "14 days", "balanced", "Start analysis"} {
		if !strings.Contains(v, s) {
			t.Fatalf("setup view missing %q", s)
		}
	}
}

func TestDashboardView(t *testing.T) {
	now := time.Now()
	res := &analysis.Result{
		Totals: analysis.Totals{VMs: 120, On: 110, VCPU: 480, RecVCPU: 260, MemMB: 1 << 20, RecMemMB: 600 << 10, Hosts: 8, HostsNeeded: 6, Cores: 256, NeedCores: 150, Reclaim: 2 << 40, Analyzed: 108},
		Clusters: []analysis.ClusterResult{{Name: "prod-cl01", Hosts: 6, CPUP: 41, CPUPeak: 63, MemP: 58, VCPU: 300, RecVCPU: 170, MemMB: 600 << 10, RecMemMB: 380 << 10, HostsNeeded: 4, CapMHz: 1000,
			Points: []analysis.Point{{T: now, CPUMHz: 200}, {T: now.Add(time.Minute), CPUMHz: 600}, {T: now.Add(2 * time.Minute), CPUMHz: 350}}}},
		Findings: []analysis.Finding{{VM: "sql-01", Kind: analysis.CPUOver, Severity: analysis.High, Current: "16 vCPU", Suggested: "6 vCPU", Confidence: "high"}},
	}
	m := New(nil)
	m.scr = scrDash
	m.st = &engine.Status{
		Phase: engine.Done, Config: engine.Config{Host: "vcsa01.corp.local", Profile: "balanced"},
		Started: now.Add(-14 * 24 * time.Hour), Ends: now, Finished: now, Polls: 4032, Result: res,
		Share: &report.Share{URL: "https://10.0.0.5:8443/abc/rightsizer-final.pdf", Fingerprint: "AA:BB", Expires: now.Add(24 * time.Hour), File: "rightsizer-final.pdf"},
	}
	v := render(t, m)
	for _, s := range []string{"complete", "480 → 260", "prod-cl01", "Report ready", "https://10.0.0.5:8443"} {
		if !strings.Contains(v, s) {
			t.Fatalf("dashboard missing %q", s)
		}
	}
	m.tab = 1
	m.fillTable()
	if v := render(t, m); !strings.Contains(v, "sql-01") {
		t.Fatal("findings tab missing row")
	}
}
