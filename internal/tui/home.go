package tui

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/lipgloss/v2"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
	"github.com/MarcoColomb0/rightsizer/internal/engine"
	"github.com/MarcoColomb0/rightsizer/internal/report"
)

// cardH is the height of a source card on the home screen.
const cardH = 5

var logoLines = []string{
	"█▀█ █ █▀▀ █ █ ▀█▀ █▀ █ ▀█ █▀▀ █▀█",
	"█▀▄ █ █▄█ █▀█  █  ▄█ █ █▄ ██▄ █▀▄",
}

func (t theme) logo() string {
	out := make([]string, len(logoLines))
	for i, l := range logoLines {
		out[i] = t.gradient(l, true)
	}
	return strings.Join(out, "\n")
}

func (m Model) viewLoading() string {
	t := m.th
	s := t.logo() + "\n\n" + m.spin.View() + t.mute.Render(" Connecting to the rightsizer engine…")
	return lipgloss.Place(m.bodyW(), m.bodyH(), lipgloss.Center, lipgloss.Center, s)
}

func (m Model) viewHome() (string, []zone) {
	t := m.th
	if m.sum == nil {
		return m.viewLoading(), nil
	}
	if len(m.sum.Sources) == 0 {
		var s stack
		s.add(t.logo(), "", t.h1.Render("No vCenter sources yet"),
			t.mute.Render("Add one to start collecting. A vCenter account with the Read-only role is enough."), "")
		btn, bz := t.buttons(0, action{"Add vCenter", "a"})
		s.addZoned(btn, bz, 0)
		s.add("", t.faint.Render("rightsizer never changes vCenter: it only reads inventory and performance data."))
		box := lipgloss.Place(m.bodyW(), m.bodyH(), lipgloss.Center, lipgloss.Center, s.String())
		x := (m.bodyW() - lipgloss.Width(s.String())) / 2
		y := (m.bodyH() - lipgloss.Height(s.String())) / 2
		return box, shift(s.zones, x, y)
	}
	var s stack
	s.add(t.h1.Render("vCenter sources") + "  " + t.mute.Render(m.sourceCounts()))
	s.add("")
	start, end := m.pager.GetSliceBounds(len(m.sum.Sources))
	for i := start; i < end; i++ {
		c := m.sourceCard(m.sum.Sources[i], i == m.sel)
		s.addZoned(c, []zone{{"src:" + strconv.Itoa(i), 0, 0, lipgloss.Width(c) - 1, cardH - 1}}, 0)
	}
	if m.pager.TotalPages > 1 {
		s.add(lipgloss.PlaceHorizontal(m.bodyW(), lipgloss.Center, m.pager.View()+t.faint.Render(fmt.Sprintf("  page %d of %d", m.pager.Page+1, m.pager.TotalPages))))
	}
	if x := m.homeExtras(); x != "" {
		s.add("", x)
	}
	s.add("", t.faint.Render("Leaving the console does not stop collection."))
	return s.String(), s.zones
}

func (m Model) sourceCounts() string {
	n := map[engine.Phase]int{}
	for _, s := range m.sum.Sources {
		n[s.Phase]++
	}
	parts := []string{plural(len(m.sum.Sources), "source", "sources")}
	for _, p := range []struct {
		ph   engine.Phase
		name string
	}{{engine.Running, "collecting"}, {engine.NeedPassword, "paused"}, {engine.Done, "complete"}} {
		if n[p.ph] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n[p.ph], p.name))
		}
	}
	return strings.Join(parts, " · ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// homeExtras renders what sits below the cards: shares and notices.
func (m Model) homeExtras() string {
	t := m.th
	var parts []string
	if len(m.sum.Shares) > 0 {
		parts = append(parts, m.sharesBox(m.sum.Shares, m.sum.Sources))
	}
	if u := m.sum.Upgrade; u != nil && time.Since(u.Time) < 24*time.Hour {
		msg := fmt.Sprintf("Upgrade to %s: %s", u.Version, u.State)
		if u.Message != "" {
			msg += " (" + u.Message + ")"
		}
		c := t.info
		if u.State == "failed" || u.State == "rolled back" {
			c = t.bad
		}
		parts = append(parts, t.callout(msg, c, m.bodyW()))
	}
	if len(m.sum.Reboot) > 0 {
		body := t.wrn.Bold(true).Render("Restart required") + "\n" + strings.Join(m.sum.Reboot, "\n")
		if m.opt.Appliance {
			body += "\n" + t.mute.Render("Press R to restart now. Collection pauses until an administrator logs in again.")
		}
		parts = append(parts, t.callout(body, t.warn, m.bodyW()))
	}
	return strings.Join(parts, "\n\n")
}

func (m Model) homeExtrasH() int {
	if m.sum == nil || len(m.sum.Sources) == 0 {
		return 0
	}
	x := m.homeExtras()
	if x == "" {
		return 2
	}
	return lipgloss.Height(x) + 3
}

func (m Model) sourceCard(s engine.Status, selected bool) string {
	t := m.th
	w := m.bodyW()
	inner := w - 4
	name := t.bold.Render(clip(s.Config.Host, inner-20))
	pill := m.phasePill(s.Phase)
	top := name + strings.Repeat(" ", max(inner-lipgloss.Width(name)-lipgloss.Width(pill), 1)) + pill

	frac := progressFrac(s)
	text := progressText(s)
	bar := m.miniBar(frac, s.Phase, max(inner-lipgloss.Width(text)-2, 10))
	mid := bar + "  " + t.mute.Render(text)

	var stats []string
	if tt := s.Totals; tt != nil {
		stats = append(stats,
			t.mute.Render("vCPU ")+t.bold.Render(fmt.Sprintf("%d → %d", tt.VCPU, tt.RecVCPU))+" "+m.deltaText(tt.VCPU, tt.RecVCPU),
			t.mute.Render("Memory ")+t.bold.Render(fmt.Sprintf("%s → %s", analysis.GiB(tt.MemMB), analysis.GiB(tt.RecMemMB)))+" "+m.deltaText(tt.MemMB, tt.RecMemMB),
			t.mute.Render("Hosts ")+t.bold.Render(fmt.Sprintf("%d → %d", tt.Hosts, tt.HostsNeeded)))
	} else {
		stats = append(stats, t.faint.Render("waiting for the first samples"))
	}
	stats = append(stats, findingsBadge(t, s.Findings))
	bottom := strings.Join(stats, t.faint.Render("  ·  "))
	return t.panel(w, selected).Render(top + "\n" + mid + "\n" + bottom)
}

func findingsBadge(t theme, n int) string {
	if n == 0 {
		return t.mute.Render("no findings")
	}
	return t.wrn.Bold(true).Render(strconv.Itoa(n)) + t.mute.Render(" findings")
}

// deltaText colours a relative change: a reduction is good news here.
func (m Model) deltaText(a, b int) string {
	if a == 0 {
		return ""
	}
	d := float64(b-a) / float64(a) * 100
	switch {
	case d < -0.5:
		return m.th.ok.Render(fmt.Sprintf("▼%.0f%%", -d))
	case d > 0.5:
		return m.th.wrn.Render(fmt.Sprintf("▲%.0f%%", d))
	}
	return m.th.mute.Render("=")
}

func (m Model) miniBar(frac float64, ph engine.Phase, w int) string {
	t := m.th
	cs := t.grad
	switch ph {
	case engine.NeedPassword:
		cs = []color.Color{t.warn, t.bad}
	case engine.Done:
		cs = []color.Color{t.good, t.accent}
	case engine.Running:
	}
	p := progress.New(progress.WithColors(cs...), progress.WithoutPercentage(), progress.WithFillCharacters('━', '━'), progress.WithWidth(w))
	p.EmptyColor = t.subtle
	return p.ViewAs(frac)
}

func progressFrac(s engine.Status) float64 {
	if s.Phase == engine.Done {
		return 1
	}
	total := s.Ends.Sub(s.Started)
	if total <= 0 {
		return 0
	}
	return min(max(float64(time.Since(s.Started))/float64(total), 0), 1)
}

func progressText(s engine.Status) string {
	switch s.Phase {
	case engine.Done:
		return "finished " + s.Finished.Format("Jan 02")
	case engine.NeedPassword:
		return fmt.Sprintf("%3.0f%% · needs password", progressFrac(s)*100)
	case engine.Running:
	}
	if s.Ends.Sub(s.Started) <= 0 {
		return ""
	}
	if s.Preview {
		return fmt.Sprintf("%3.0f%% · preview", progressFrac(s)*100)
	}
	return fmt.Sprintf("%3.0f%% · %s left", progressFrac(s)*100, human(time.Until(s.Ends)))
}

func (m Model) sharesBox(shares []report.Share, sources []engine.Status) string {
	t := m.th
	names := map[string]string{"all": "All sources"}
	for _, s := range sources {
		names[s.ID] = s.Config.Host
	}
	var b strings.Builder
	b.WriteString(t.h2.Render("⇪ Shared reports") + t.faint.Render("  download from any browser on the network"))
	for _, sh := range shares {
		name := names[sh.Source]
		if id, ok := strings.CutPrefix(sh.Source, "sizing:"); ok {
			name = "Sizing · " + names[id]
		}
		fmt.Fprintf(&b, "\n%s  %s\n%s", t.bold.Render(name),
			t.mute.Render(fmt.Sprintf("expires %s · %d downloads", sh.Expires.Format("Jan 02 15:04"), sh.Downloads)),
			t.link(sh.URL, sh.URL))
		for _, x := range sh.Extra {
			b.WriteString("\n" + t.link(x.URL, x.URL) + t.mute.Render("  spreadsheet data (CSV)"))
		}
	}
	b.WriteString("\n" + t.faint.Render("Self-signed TLS, SHA-256 "+shares[0].Fingerprint))
	return t.panel(m.bodyW(), false).Render(b.String())
}

func (m Model) shares(id string) []report.Share {
	var out []report.Share
	if m.sum != nil {
		for _, sh := range m.sum.Shares {
			if sh.Source == id || sh.Source == "sizing:"+id {
				out = append(out, sh)
			}
		}
	}
	return out
}

func human(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	h := int(d.Hours())
	switch {
	case h >= 24:
		return fmt.Sprintf("%dd %dh", h/24, h%24)
	case h >= 1:
		return fmt.Sprintf("%dh %dm", h, int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

func clip(s string, n int) string {
	r := []rune(s)
	if n < 2 || len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
