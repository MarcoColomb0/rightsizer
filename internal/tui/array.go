package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	ltable "charm.land/lipgloss/v2/table"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
	"github.com/MarcoColomb0/rightsizer/internal/engine"
	"github.com/MarcoColomb0/rightsizer/internal/report"
)

// kindName names the kind chosen in the add form, in running text.
func (m Model) kindName() string {
	if m.kind == 1 {
		return "ONTAP"
	}
	return "vCenter"
}

// kindLabel is the address field's label for the chosen kind.
func (m Model) kindLabel() string {
	if m.kind == 1 {
		return "ONTAP cluster"
	}
	return "vCenter"
}

// srcKind names a source's kind in running text.
func srcKind(s *engine.Status) string {
	if s != nil && s.Config.IsONTAP() {
		return "ONTAP"
	}
	return "vCenter"
}

func (m Model) isArray() bool { return m.src != nil && m.src.Config.IsONTAP() }

var hostPlaceholders = [][2]string{
	{"vcenter.example.local", "readonly@vsphere.local"},
	{"cluster-mgmt.example.local", "monitor (readonly role)"},
}

// setKind switches the add form between vCenter and ONTAP.
func (m *Model) setKind(k int) {
	m.kind = k
	m.in[0].Placeholder, m.in[1].Placeholder = hostPlaceholders[k][0], hostPlaceholders[k][1]
}

// setupOrder lists the add form's fields from top to bottom; an ONTAP
// cluster has no profile or cluster filter.
func (m Model) setupOrder() []int {
	if m.kind == 1 {
		return []int{fKind, fHost, fUser, fPass, fDuration, fStart}
	}
	return []int{fKind, fHost, fUser, fPass, fDuration, fProfile, fClusters, fStart}
}

func (m Model) nextField(d int) int {
	order := m.setupOrder()
	i := 0
	for j, f := range order {
		if f == m.focus {
			i = j
		}
	}
	return order[(i+d+len(order))%len(order)]
}

func ratioText(r float64) string {
	if r <= 0 {
		return "–"
	}
	return fmt.Sprintf("%.1f:1", r)
}

// arrayStats is the bottom line of an ONTAP card.
func (m Model) arrayStats(s engine.Status) []string {
	t, a := m.th, s.Array
	if a == nil {
		return []string{t.faint.Render("reading the cluster")}
	}
	out := []string{
		t.mute.Render("Reduction ") + t.bold.Render(ratioText(a.Ratio)),
		t.mute.Render("Written ") + t.bold.Render(analysis.Human(a.Physical)) + t.mute.Render(" of "+analysis.Human(a.Size)),
	}
	if a.CPU > 0 {
		out = append(out, t.mute.Render("CPU ")+t.load(a.CPU).Render(fmt.Sprintf("%.0f%%", a.CPU)))
	}
	if a.Matched > 0 {
		out = append(out, t.mute.Render(plural(a.Matched, "datastore", "datastores")))
	} else {
		out = append(out, t.wrn.Render("no datastore matched yet"))
	}
	return out
}

// arrayView is the content of an ONTAP source's screen.
func (m Model) arrayView() string {
	t, s := m.th, m.src
	a := s.Array
	w := m.vp.Width()
	if a == nil {
		return t.mute.Render("⋯ Reading the cluster's nodes, aggregates and volumes…")
	}
	cols := 4
	if w < 100 {
		cols = 2
	}
	tw := (w - (cols - 1)) / cols
	tile := func(label, val, sub string) string {
		return t.panel(tw, false).Render(t.mute.Render(label) + "\n" + val + "\n" + sub)
	}
	used := 0.0
	if a.Size > 0 {
		used = float64(a.Physical) / float64(a.Size) * 100
	}
	cpu, cpuSub := t.faint.Render("–"), t.faint.Render("waiting for samples")
	if a.CPU > 0 {
		cpu = t.load(a.CPU).Bold(true).Render(fmt.Sprintf("%.0f%%", a.CPU))
		cpuSub = t.mute.Render(fmt.Sprintf("p%.0f · peak %.0f%%", m.arrayPct(), a.CPUPeak))
	}
	tiles := []string{
		tile("Data reduction", t.acc.Bold(true).Render(ratioText(a.Ratio)), t.mute.Render(analysis.Human(a.Logical)+" → "+analysis.Human(a.Physical))),
		tile("Written", t.bold.Render(analysis.Human(a.Physical))+t.mute.Render(" of "+analysis.Human(a.Size)), t.utilBar("", used)),
		tile("Snapshot copies", t.bold.Render(analysis.Human(a.Snapshots)), t.mute.Render("not seen by vSphere")),
		tile("Controllers CPU", cpu, cpuSub),
	}
	var parts []string
	var rows []string
	for i := 0; i < len(tiles); i += cols {
		rows = append(rows, joinGap(tiles[i:min(i+cols, len(tiles))], " ")...)
	}
	parts = append(parts, strings.Join(rows, "\n"))
	if sh := m.shares(s.ID); len(sh) > 0 {
		parts = append(parts, m.sharesBox(sh, []engine.Status{*s}))
	}

	if a.Matched == 0 {
		parts = append(parts, t.callout(t.wrn.Render("No vCenter datastore uses this cluster yet.")+"\n"+
			t.mute.Render("Add the vCenter whose datastores it serves: they are matched by LUN serial number and NFS export. The role must be able to read LUNs and network interfaces."), t.warn, w))
	} else {
		line := fmt.Sprintf("Serves %s on %s · %s of datastore data", plural(a.Matched, "datastore", "datastores"), plural(a.Volumes, "volume", "volumes"), analysis.Human(a.MatchedData))
		if a.OtherData > 0 {
			line += fmt.Sprintf(" · %s in other volumes", analysis.Human(a.OtherData))
		}
		parts = append(parts, t.bold.Render(a.Models)+t.mute.Render(" · ONTAP "+a.Version)+"\n"+t.mute.Render(line))
	}

	if io := a.IO; io.Available {
		src := ""
		if io.Preview {
			src = " (ONTAP archive)"
		}
		perf := []string{fmt.Sprintf("peak %s", report.Num(io.IOPSPeak)), fmt.Sprintf("%.0f%% reads", io.ReadPct)}
		if io.Throughput {
			perf = append(perf, fmt.Sprintf("%.0f MB/s", io.MBps))
		}
		if io.Latency {
			perf = append(perf, fmt.Sprintf("%.2f ms", io.LatencyMs))
		}
		parts = append(parts, t.section("Performance", fmt.Sprintf("%.0f hours", io.Hours))+
			fmt.Sprintf("  IOPS p%.0f %s  %s", m.arrayPct(), t.acc.Bold(true).Render(report.Num(io.IOPS)), t.mute.Render(strings.Join(perf, " · ")+src)))
	}

	if len(a.Aggregates) > 0 {
		tb := ltable.New().Border(lipgloss.RoundedBorder()).BorderStyle(t.faint).BorderColumn(false).
			Headers("Aggregate", "Node", "Media", "Usable", "Written", "Logical", "Reduction", "Cold").
			StyleFunc(func(row, col int) lipgloss.Style {
				st := lipgloss.NewStyle().Padding(0, 1).Foreground(t.text)
				if row == ltable.HeaderRow {
					return st.Foreground(t.muted).Bold(true)
				}
				if col >= 3 {
					st = st.Align(lipgloss.Right)
				}
				return st
			})
		for _, g := range a.Aggregates {
			tb.Row(t.bold.Render(clip(g.Name, 28)), g.Node, fmt.Sprintf("%s %s", g.Type, g.RAID), analysis.Human(g.Size), analysis.Human(g.Physical),
				analysis.Human(g.Logical), ratioText(g.Ratio), analysis.Human(g.Inactive))
		}
		out := tb.String()
		if lipgloss.Width(out) > w {
			out = tb.Width(w).String()
		}
		parts = append(parts, out)
	}
	notes := []string{t.mute.Render("Data reduction and load are reported in the sizing of the vCenters whose datastores this cluster serves.")}
	if s.History != "" {
		notes = append(notes, t.mute.Render("ONTAP archive: "+s.History))
	}
	parts = append(parts, lipgloss.NewStyle().Width(w).Render(strings.Join(notes, "\n")))
	return strings.Join(parts, "\n\n")
}

func (m Model) arrayPct() float64 {
	if m.src == nil {
		return 95
	}
	return analysis.ProfileByName(m.src.Config.Profile).Percentile
}

// arraySizing adds what ONTAP measured to the sizing tab's storage section.
func (m Model) arraySizing(b *strings.Builder, st analysis.StorageSizing, pct float64) {
	t := m.th
	r := st.Array
	switch {
	case len(st.Arrays) == 0 && st.NetAppDatastores > 0:
		b.WriteString(t.faint.Render(fmt.Sprintf("  %d datastore(s) on NetApp LUNs: add their ONTAP cluster with a to measure data reduction.", st.NetAppDatastores)) + "\n")
		return
	case len(st.Arrays) == 0:
		return
	case r.Datastores > 0:
		fmt.Fprintf(b, "  ONTAP %s  %s\n", t.acc.Bold(true).Render(ratioText(r.Ratio())),
			t.mute.Render(fmt.Sprintf("measured on %d of %d datastores (%.0f%% of data) · plan ≈ %s physical · array snapshots %s",
				r.Datastores, r.Of, r.Coverage()*100, analysis.Human(r.PlanPhysical), analysis.Human(r.Snapshots))))
	default:
		b.WriteString(t.wrn.Render("  ONTAP: no datastore matched a volume or LUN of the added clusters") + "\n")
	}
	for _, a := range st.Arrays {
		parts := []string{a.Models, analysis.Human(a.Physical) + " written of " + analysis.Human(a.Size)}
		if a.IO.Available {
			parts = append(parts, fmt.Sprintf("IOPS p%.0f %s", pct, report.Num(a.IO.IOPS)))
			if a.IO.Latency {
				parts = append(parts, fmt.Sprintf("%.2f ms", a.IO.LatencyMs))
			}
		}
		if a.CPU > 0 {
			parts = append(parts, fmt.Sprintf("CPU %.0f%%", a.CPU))
		}
		b.WriteString(t.faint.Render("  "+a.Name+": "+strings.Join(parts, " · ")) + "\n")
	}
}
