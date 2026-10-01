package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	ltable "charm.land/lipgloss/v2/table"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
	"github.com/MarcoColomb0/rightsizer/internal/engine"
	"github.com/MarcoColomb0/rightsizer/internal/report"
)

func (m Model) srcFrac() float64 {
	if m.src == nil {
		return 0
	}
	return progressFrac(*m.src)
}

func (m Model) viewSource() (string, []zone) {
	if m.src == nil {
		return m.spin.View() + m.th.mute.Render(" Loading…"), nil
	}
	var s stack
	head, hz := m.sourceHead()
	s.addZoned(head, hz, 0)
	if m.tab == tabFindings {
		body, bz := m.findingsView()
		s.addZoned(body, bz, 0)
	} else {
		s.add(lipgloss.JoinHorizontal(lipgloss.Top, m.vp.View(), " ", m.scrollbar()))
	}
	return s.String(), s.zones
}

func (m Model) sourceHeadH() int {
	if m.scr != scrSource || m.src == nil {
		return 0
	}
	h, _ := m.sourceHead()
	return lipgloss.Height(h)
}

// sourceHead is the part of the source screen above the tab content:
// collection progress and the tabs.
func (m Model) sourceHead() (string, []zone) {
	t, s := m.th, m.src
	bw := m.bodyW()
	frac := m.srcFrac()
	var st stack
	st.add(m.miniBar(frac, s.Phase, bw-6) + "  " + t.bold.Render(fmt.Sprintf("%3.0f%%", frac*100)))

	total := s.Ends.Sub(s.Started)
	end := time.Now()
	if !s.Finished.IsZero() {
		end = s.Finished
	}
	sub := []string{fmt.Sprintf("%s of %s", human(end.Sub(s.Started)), human(total))}
	switch s.Phase {
	case engine.Running:
		sub = append(sub, "ends "+s.Ends.Format("Mon Jan 02 15:04"))
		if !s.LastPoll.IsZero() {
			sub = append(sub, "last sample "+s.LastPoll.Format("15:04:05"))
		}
		if m.poll.Running() {
			sub = append(sub, "next in "+m.poll.View())
		}
	case engine.NeedPassword:
		sub = append(sub, "paused, press r to resume")
	case engine.Done:
		sub = append(sub, "finished "+s.Finished.Format("Mon Jan 02 15:04"))
	}
	sub = append(sub, report.Num(float64(s.Polls))+" polls", s.Config.Profile+" profile")
	st.add(t.mute.Render(strings.Join(sub, " · ")))
	if s.LastError != "" {
		st.add(t.wrn.Render("⚠ " + clip(s.LastError, bw-2)))
	}
	st.add("")
	tabs, tz := m.tabBar()
	st.addZoned(tabs, tz, 0)
	return st.String(), st.zones
}

func (m Model) tabBar() (string, []zone) {
	t := m.th
	var parts []string
	var zones []zone
	x := 0
	for i, name := range tabNames {
		label := fmt.Sprintf(" %d %s ", i+1, name)
		if i == tabFindings && m.src.Findings > 0 {
			label = fmt.Sprintf(" %d %s · %d ", i+1, name, m.src.Findings)
		}
		r := t.mute.Render(label)
		if i == m.tab {
			r = t.fill(label)
		}
		w := lipgloss.Width(r)
		zones = append(zones, zone{"key:" + strconv.Itoa(i+1), x, 0, x + w - 1, 0})
		parts = append(parts, r)
		x += w + 1
	}
	line := strings.Join(parts, " ")
	var right string
	if m.tab != tabFindings && m.vp.TotalLineCount() > m.vp.Height() {
		right = t.faint.Render(fmt.Sprintf("%3.0f%% ↕", m.vp.ScrollPercent()*100))
	}
	gap := max(m.bodyW()-lipgloss.Width(line)-lipgloss.Width(right), 1)
	return line + strings.Repeat(" ", gap) + right + "\n" + t.faint.Render(strings.Repeat("─", m.bodyW())), zones
}

func (m Model) scrollbar() string {
	h, total := m.vp.Height(), m.vp.TotalLineCount()
	if total <= h || h <= 0 {
		return strings.Repeat(" \n", max(h-1, 0)) + " "
	}
	thumb := max(h*h/total, 1)
	pos := int(float64(h-thumb)*m.vp.ScrollPercent() + 0.5)
	out := make([]string, h)
	for i := range out {
		if i >= pos && i < pos+thumb {
			out[i] = m.th.acc.Render("┃")
		} else {
			out[i] = m.th.faint.Render("│")
		}
	}
	return strings.Join(out, "\n")
}

// syncViewport refreshes the content of the scrolling tabs.
func (m *Model) syncViewport() {
	if m.src == nil {
		m.vp.SetContent("")
		return
	}
	switch m.tab {
	case tabClusters:
		m.vp.SetContent(m.overview())
	case tabPeaks:
		m.vp.SetContent(m.peaksView(m.src.Result))
	case tabSizing:
		m.vp.SetContent(m.sizingView())
	}
}

func (m Model) overview() string {
	t, s := m.th, m.src
	r := s.Result
	w := m.vp.Width()
	var parts []string
	if r == nil {
		parts = append(parts, t.mute.Render("⋯ Waiting for the first samples…"))
	} else {
		parts = append(parts, m.kpis(r, w))
		if r.Preview {
			parts = append(parts, t.callout(t.wrn.Render("Preview based on vCenter history (5-minute to 2-hour averages): peaks are smoothed and read low.")+"\n"+
				t.mute.Render("Each VM switches to 20-second data once it has 24 hours of it."), t.warn, w))
		}
	}
	if sh := m.shares(s.ID); len(sh) > 0 {
		parts = append(parts, m.sharesBox(sh, []engine.Status{*s}))
	}
	parts = append(parts, m.clustersTable(r, w))
	if ins := m.insights(r, w); ins != "" {
		parts = append(parts, ins)
	}
	var notes []string
	if s.History != "" {
		notes = append(notes, t.mute.Render("vCenter history: "+s.History))
	}
	if r != nil && r.WasteNote != "" {
		notes = append(notes, t.wrn.Render(r.WasteNote))
	}
	if len(notes) > 0 {
		parts = append(parts, lipgloss.NewStyle().Width(w).Render(strings.Join(notes, "\n")))
	}
	return strings.Join(parts, "\n\n")
}

func (m Model) kpis(r *analysis.Result, w int) string {
	t, tt := m.th, r.Totals
	cols := 4
	if w < 100 {
		cols = 2
	}
	tw := (w - (cols - 1)) / cols
	value := func(a, b string) string {
		return t.bold.Render(a) + t.faint.Render(" → ") + t.acc.Bold(true).Render(b)
	}
	tile := func(label, val, sub string) string {
		return t.panel(tw, false).Render(t.mute.Render(label) + "\n" + val + "\n" + sub)
	}
	ratio := func(a, b int) string {
		if a == 0 {
			return ""
		}
		return m.deltaText(a, b) + " " + t.ratioBar(float64(b)/float64(a), tw-12)
	}
	tiles := []string{
		tile("vCPU", value(strconv.Itoa(tt.VCPU), strconv.Itoa(tt.RecVCPU)), ratio(tt.VCPU, tt.RecVCPU)),
		tile("Memory", value(analysis.GiB(tt.MemMB), analysis.GiB(tt.RecMemMB)), ratio(tt.MemMB, tt.RecMemMB)),
		tile("Hosts (N+1)", value(strconv.Itoa(tt.Hosts), strconv.Itoa(tt.HostsNeeded)), t.mute.Render(fmt.Sprintf("cores %d → %d", tt.Cores, tt.NeedCores))),
		tile("Reclaimable disk", t.acc.Bold(true).Render(analysis.Human(tt.Reclaim)), t.mute.Render(fmt.Sprintf("%d VMs analysed", tt.Analyzed))),
	}
	var rows []string
	for i := 0; i < len(tiles); i += cols {
		rows = append(rows, joinGap(tiles[i:min(i+cols, len(tiles))], " ")...)
	}
	return strings.Join(rows, "\n")
}

// ratioBar shows how much of today's allocation the recommendation keeps.
func (t theme) ratioBar(f float64, w int) string {
	if w < 4 {
		return ""
	}
	n := int(min(max(f, 0), 1)*float64(w) + 0.5)
	return t.gradient(strings.Repeat("━", n), false) + t.faint.Render(strings.Repeat("━", w-n))
}

var sparks = []rune("▁▂▃▄▅▆▇█")

func (t theme) spark(pts []analysis.Point, capMHz float64) string {
	if len(pts) == 0 || capMHz == 0 {
		return ""
	}
	if len(pts) > 24 {
		pts = analysis.Downsample(pts, 24)
	}
	var b strings.Builder
	for _, p := range pts {
		v := min(p.CPUMHz/capMHz, 0.999)
		b.WriteString(lipgloss.NewStyle().Foreground(t.heat(v)).Render(string(sparks[max(int(v*float64(len(sparks))), 0)])))
	}
	return b.String()
}

func (m Model) clustersTable(r *analysis.Result, w int) string {
	t := m.th
	if r == nil || len(r.Clusters) == 0 {
		return t.mute.Render("No cluster data yet.")
	}
	tb := ltable.New().Border(lipgloss.RoundedBorder()).BorderStyle(t.faint).BorderColumn(false).
		Headers("Cluster", "Hosts", "CPU p/peak", "Mem p", "vCPU", "Memory", "Need", "Div", "CPU trend").
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().Padding(0, 1).Foreground(t.text)
			if row == ltable.HeaderRow {
				return s.Foreground(t.muted).Bold(true)
			}
			if col >= 1 && col <= 7 {
				s = s.Align(lipgloss.Right)
			}
			return s
		})
	for _, c := range r.Clusters {
		need := strconv.Itoa(c.HostsNeeded)
		switch {
		case c.HostsNeeded < c.Hosts:
			need = t.ok.Bold(true).Render("▼ " + need)
		case c.HostsNeeded > c.Hosts:
			need = t.wrn.Bold(true).Render("▲ " + need)
		}
		div := "–"
		if c.Peaks != nil && c.Peaks.Diversity > 0 {
			div = fmt.Sprintf("%.1f×", c.Peaks.Diversity)
		}
		tb.Row(t.bold.Render(clip(c.Name, 28)), strconv.Itoa(c.Hosts),
			t.load(c.CPUP).Render(fmt.Sprintf("%.0f%%", c.CPUP))+t.faint.Render("/")+t.load(c.CPUPeak).Render(fmt.Sprintf("%.0f%%", c.CPUPeak)),
			t.load(c.MemP).Render(fmt.Sprintf("%.0f%%", c.MemP)),
			fmt.Sprintf("%d→%d", c.VCPU, c.RecVCPU),
			fmt.Sprintf("%s→%s", analysis.GiB(c.MemMB), analysis.GiB(c.RecMemMB)),
			need, div, t.spark(c.Points, c.CapMHz))
	}
	out := tb.String()
	if lipgloss.Width(out) > w {
		out = tb.Width(w).String()
	}
	return out
}

func ghz(mhz float64) string { return fmt.Sprintf("%.0f GHz", mhz/1000) }

func (m Model) peaksView(r *analysis.Result) string {
	t := m.th
	if r == nil {
		return t.mute.Render("No data yet.")
	}
	var b strings.Builder
	for _, c := range r.Clusters {
		pk := c.Peaks
		if pk == nil {
			continue
		}
		b.WriteString(t.h2.Render("◆ "+c.Name) + "\n")
		fmt.Fprintf(&b, "  Sum of VM peaks %s · combined peak %s · diversity %s\n",
			t.bold.Render(ghz(pk.SumPeakMHz)), t.bold.Render(ghz(pk.CombinedPeakMHz)), t.acc.Bold(true).Render(fmt.Sprintf("%.2f×", pk.Diversity)))
		fmt.Fprintf(&b, "  Hosts for CPU: %s if sized on the sum of peaks, %s peak-aware\n\n",
			t.wrn.Render(strconv.Itoa(pk.NaiveHosts)), t.ok.Bold(true).Render(strconv.Itoa(pk.AwareHosts)))
		b.WriteString(t.faint.Render("        00    03    06    09    12    15    18    21") + "\n")
		for d, day := range []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"} {
			var row strings.Builder
			for h := range 24 {
				if pk.HeatmapN[d][h] == 0 {
					row.WriteString(t.faint.Render("··"))
					continue
				}
				row.WriteString(lipgloss.NewStyle().Foreground(t.heat(pk.Heatmap[d][h] / 100)).Render("██"))
			}
			b.WriteString("  " + t.mute.Render(day) + "   " + row.String() + "\n")
		}
		b.WriteString("        " + t.faint.Render("idle ") + t.heatLegend(16) + t.faint.Render(" busy") + "\n")
		for _, g := range pk.CoPeak {
			b.WriteString("  " + t.wrn.Render(fmt.Sprintf("▲ Peak together (r %.2f): ", g.R)) + strings.Join(g.VMs, ", ") + "\n")
		}
		for i, p := range pk.Complementary {
			if i == 3 {
				break
			}
			b.WriteString("  " + t.ok.Render(fmt.Sprintf("◇ Peak at different times (r %.2f): ", p.R)) + p.A + " + " + p.B + "\n")
		}
		b.WriteString("\n")
	}
	if b.Len() == 0 {
		return t.mute.Render("Peak analysis needs at least an hour of data.")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (t theme) heatLegend(w int) string {
	var b strings.Builder
	for i := range w {
		b.WriteString(lipgloss.NewStyle().Foreground(t.heat(float64(i) / float64(w-1))).Render("█"))
	}
	return b.String()
}

func (m Model) insights(r *analysis.Result, w int) string {
	t := m.th
	if r == nil {
		return ""
	}
	var parts []string
	for _, c := range r.Clusters {
		if ep := c.EarlierPeak; ep != nil {
			parts = append(parts, t.callout(fmt.Sprintf("%s: a higher peak (%.0f%% of capacity) happened on %s, before this analysis; the window peaked at %.0f%%.",
				c.Name, ep.Pct, ep.At.Local().Format("Mon Jan 02 15:04"), ep.WindowPct), t.warn, w))
		}
	}
	if a := r.Accuracy; a != nil {
		lines := []string{fmt.Sprintf("vCenter history vs 20-second data (%d VMs): CPU p%.0f reads %.0f%% lower, memory %.0f%% lower.",
			a.VMs, a.Percentile, (1-a.CPUMedian)*100, (1-a.MemMedian)*100)}
		for _, x := range a.Bursty[:min(len(a.Bursty), 3)] {
			lines = append(lines, t.mute.Render(fmt.Sprintf("bursty: %s %.0f%% in 20-second data, %.0f%% in vCenter averages", x.VM, x.RealtimeP, x.HistoryP)))
		}
		parts = append(parts, t.callout(strings.Join(lines, "\n"), t.info, w))
	}
	return strings.Join(parts, "\n")
}
