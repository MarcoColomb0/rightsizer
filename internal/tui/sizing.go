package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"charm.land/lipgloss/v2"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
	"github.com/MarcoColomb0/rightsizer/internal/report"
)

type (
	sizingMsg struct {
		id  string
		sz  *analysis.Sizing
		err error
	}
	sizingParamsMsg struct {
		p   *analysis.SizingParams
		ret screen
		err error
	}
)

const sizingEvery = 10 * time.Second

// Sizing options form fields. Text fields map to szIn by szInput.
const (
	soBasis = iota
	soOff
	soGrowth
	soRatio
	soCPU
	soMem
	soSpares
	soSockets
	soUplift
	soFree
	soGroups
	soSave
	soCount
)

// paramField maps a SizingParams field to its form field.
var paramField = map[string]int{"Basis": soBasis, "PoweredOff": soOff, "Growth": soGrowth, "Ratio": soRatio, "CPUTarget": soCPU,
	"MemTarget": soMem, "Spares": soSpares, "Sockets": soSockets, "Uplift": soUplift, "FreeSpace": soFree, "Groups": soGroups}

var szInput = map[int]int{soGrowth: 0, soRatio: 1, soCPU: 2, soMem: 3, soSpares: 4, soUplift: 5, soFree: 6, soGroups: 7}

type sizingForm struct {
	ret   screen
	p     analysis.SizingParams
	focus int
}

func (m Model) fetchSizing() tea.Cmd {
	b, id := m.b, m.cur
	return func() tea.Msg {
		sz, err := b.Sizing(id)
		return sizingMsg{id, sz, err}
	}
}

func (m Model) openSizingOptions(ret screen) (tea.Model, tea.Cmd) {
	b := m.b
	return m, func() tea.Msg {
		p, err := b.SizingParams()
		return sizingParamsMsg{p, ret, err}
	}
}

func fmtNum(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func (m Model) sizingParams(msg sizingParamsMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.err = msg.err.Error()
		return m, nil
	}
	p := *msg.p
	m.szf = sizingForm{ret: msg.ret, p: p}
	vals := []string{fmtNum(p.Growth), fmtNum(p.Ratio), fmtNum(p.CPUTarget), fmtNum(p.MemTarget), strconv.Itoa(p.Spares), fmtNum(p.Uplift), fmtNum(p.FreeSpace), p.Groups}
	for i := range m.szIn {
		m.szIn[i].SetValue(vals[i])
	}
	m.szFocus(soBasis)
	m.scr, m.err = scrSizing, ""
	return m, nil
}

func (m *Model) szFocus(f int) {
	m.szf.focus = f
	for i := range m.szIn {
		m.szIn[i].Blur()
	}
	if i, ok := szInput[f]; ok {
		m.szIn[i].Focus()
	}
}

func (m Model) keySizing(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := &m.szf
	switch k.String() {
	case "esc":
		m.scr, m.err = f.ret, ""
		return m, nil
	case "tab", "down":
		m.szFocus((f.focus + 1) % soCount)
		return m, nil
	case "shift+tab", "up":
		m.szFocus((f.focus + soCount - 1) % soCount)
		return m, nil
	case "left", "right", "space":
		switch f.focus {
		case soBasis:
			if f.p.Basis == analysis.BasisRightsized {
				f.p.Basis = analysis.BasisProvisioned
			} else {
				f.p.Basis = analysis.BasisRightsized
			}
			return m, nil
		case soOff:
			f.p.PoweredOff = !f.p.PoweredOff
			return m, nil
		case soSockets:
			f.p.Sockets = 3 - f.p.Sockets
			return m, nil
		}
	case "enter":
		if f.focus != soSave {
			m.szFocus(f.focus + 1)
			return m, nil
		}
		p, field, err := m.readSizingForm()
		if err != nil {
			m.err = err.Error()
			m.szFocus(field)
			return m, nil
		}
		b := m.b
		return m.busyCmd("Saving the sizing options", "sizing-params", "", func() error { return b.SetSizingParams(p) })
	}
	var cmd tea.Cmd
	if i, ok := szInput[f.focus]; ok {
		m.szIn[i], cmd = m.szIn[i].Update(k)
	}
	return m, cmd
}

func (m Model) readSizingForm() (analysis.SizingParams, int, error) {
	p := m.szf.p
	floats := []struct {
		field int
		dst   *float64
		name  string
	}{{soGrowth, &p.Growth, "growth"}, {soRatio, &p.Ratio, "vCPU per core"}, {soCPU, &p.CPUTarget, "CPU target"}, {soMem, &p.MemTarget, "memory target"}, {soUplift, &p.Uplift, "per-core uplift"}, {soFree, &p.FreeSpace, "free space"}}
	for _, x := range floats {
		v, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(m.szIn[szInput[x.field]].Value()), "%"), 64)
		if err != nil {
			return p, x.field, fmt.Errorf("%s must be a number", x.name)
		}
		*x.dst = v
	}
	n, err := strconv.Atoi(strings.TrimSpace(m.szIn[szInput[soSpares]].Value()))
	if err != nil {
		return p, soSpares, fmt.Errorf("HA spares must be a whole number")
	}
	p.Spares = n
	p.Groups = strings.TrimSpace(m.szIn[szInput[soGroups]].Value())
	if err := p.Validate(); err != nil {
		field := soSave
		if pe, ok := errors.AsType[*analysis.ParamError](err); ok {
			if f, ok := paramField[pe.Field]; ok {
				field = f
			}
		}
		return p, field, err
	}
	return p, 0, nil
}

func (m Model) viewSizingOpts() (string, []zone) {
	t := m.th
	f := m.szf
	var s stack
	s.add(t.h1.Render("Sizing options"), t.mute.Render("Used for every vCenter's sizing and kept across runs and upgrades."), "")
	row := func(fi int, label, val, help string) {
		if help != "" {
			val += t.faint.Render("  " + help)
		}
		s.add(m.field(f.focus == fi, label, val))
	}
	in := func(fi int) string { return m.szIn[szInput[fi]].View() }
	row(soBasis, "Compute basis", t.toggle(f.p.Basis == analysis.BasisRightsized, "As provisioned", "Rightsized", f.focus == soBasis), "")
	s.add(m.hint("VMs as configured today, or after rightsizing"))
	row(soOff, "Powered-off VMs", t.toggle(f.p.PoweredOff, "Leave out", "Include", f.focus == soOff), "")
	s.add(m.hint("in compute; their storage always counts"), "")
	row(soGrowth, "Growth %", in(soGrowth), "")
	row(soRatio, "vCPU per core", in(soRatio), "0 = today's ratio, at least 4")
	row(soCPU, "CPU target %", in(soCPU), "with the HA spares out")
	row(soMem, "Memory target %", in(soMem), "")
	row(soSpares, "HA spares per cluster", in(soSpares), "")
	row(soSockets, "Sockets per node", t.segmented([]string{"1", "2"}, f.p.Sockets-1, f.focus == soSockets), "")
	row(soUplift, "Per-core uplift %", in(soUplift), "how much faster the new cores are")
	row(soFree, "Storage kept free %", in(soFree), "")
	row(soGroups, "Workload groups", in(soGroups), "")
	s.add(m.hint("name=pattern,pattern; … e.g. databases=sql*,*ora*; vdi=vdi-*"), "")
	focus := -1
	if f.focus == soSave {
		focus = 0
	}
	btn, bz := t.buttons(focus, action{"Save options", "submit"})
	s.addZoned(btn, submitZones(bz), 0)
	return m.card(&s, true)
}

// section starts a block of the sizing tab.
func (t theme) section(title, sub string) string {
	out := t.h2.Render("◆ " + title)
	if sub != "" {
		out += t.faint.Render("  " + sub)
	}
	return out + "\n"
}

func (m Model) sizingView() string {
	t := m.th
	sz := m.sz
	if sz == nil || m.szID != m.cur {
		if m.szErr != "" {
			return t.mute.Render(m.szErr)
		}
		return t.mute.Render("⋯ Computing the sizing…")
	}
	p := sz.Params
	var b strings.Builder
	ratio := "auto"
	if p.Ratio > 0 {
		ratio = fmtNum(p.Ratio) + ":1"
	}
	chips := []string{analysis.BasisLabel(p.Basis), "growth " + fmtNum(p.Growth) + "%", "vCPU/core " + ratio, "CPU ≤ " + fmtNum(p.CPUTarget) + "%",
		"RAM ≤ " + fmtNum(p.MemTarget) + "%", fmt.Sprintf("N+%d", p.Spares), fmt.Sprintf("%d-socket nodes", p.Sockets)}
	for i, c := range chips {
		chips[i] = lipgloss.NewStyle().Foreground(t.muted).Background(t.surface).Padding(0, 1).Render(c)
	}
	b.WriteString(strings.Join(chips, " ") + t.faint.Render("  o to change") + "\n\n")

	bi := 0
	if p.Basis == analysis.BasisRightsized {
		bi = 1
	}
	w := 12
	for _, c := range sz.Clusters {
		w = max(w, min(len([]rune(c.Name)), 24))
	}
	pad := strings.Repeat(" ", w+3)
	b.WriteString(t.section("Recommended nodes", ""))
	for _, c := range sz.Clusters {
		n := c.Needs[bi]
		o, ok := n.Picked()
		if !ok {
			if n.Unsized() {
				fmt.Fprintf(&b, "  %-*s %s\n", w, clip(c.Name, w), t.wrn.Render("no node shape fits; left out of the totals, see the notes"))
			}
			continue
		}
		fmt.Fprintf(&b, "  %-*s %s  %s\n", w, clip(c.Name, w), t.acc.Bold(true).Render(o.String()), t.mute.Render(fmt.Sprintf("%d cores, today %d", o.TotalCores, c.Cores)))
		b.WriteString(pad + t.utilBar("CPU", o.CPUUtil) + "  " + t.utilBar("RAM", o.MemUtil) + t.faint.Render("  with spares out · "+n.Ports.String()) + "\n")
	}
	tt := sz.Totals
	nt, alt := tt.For(p.Basis), tt.For(analysis.OtherBasis(p.Basis))
	fmt.Fprintf(&b, "  %-*s %s  %s\n", w, "Total", t.gradient(fmt.Sprintf("%d nodes · %d cores · %s RAM", nt.Nodes, nt.Cores, analysis.GBLabel(nt.MemGB)), true),
		t.mute.Render(fmt.Sprintf("today %d hosts · %d cores · %s", tt.Hosts, tt.Cores, analysis.Human(tt.MemB))))
	other := "Rightsized"
	if p.Basis == analysis.BasisRightsized {
		other = "As provisioned"
	}
	b.WriteString(pad + t.faint.Render(fmt.Sprintf("%s instead: %d nodes · %d cores · %s RAM", other, alt.Nodes, alt.Cores, analysis.GBLabel(alt.MemGB))) + "\n\n")

	b.WriteString(t.section("Compute today", ""))
	for _, c := range sz.Clusters {
		fmt.Fprintf(&b, "  %-*s %d hosts · %d cores · %s RAM · %d vCPU (%.1f:1) · %s vRAM\n", w, clip(c.Name, w),
			c.Hosts, c.Cores, analysis.Human(c.MemB), c.VCPU, c.Ratio, analysis.GiB(c.MemMB))
		b.WriteString(pad + t.faint.Render(fmt.Sprintf("%d VMs on, %d off · CPU p%.0f %s, peak %s", c.VMs, c.VMsOff, sz.Percentile, ghz(c.DemandMHz), ghz(c.PeakMHz))) + "\n")
	}

	st := sz.Storage
	b.WriteString("\n" + t.section("Storage", "raw used, before data reduction"))
	fmt.Fprintf(&b, "  Raw used %s  %s\n", t.acc.Bold(true).Render(analysis.Human(st.RawUsed)),
		t.mute.Render(fmt.Sprintf("disks %s · snapshots %s · other %s · templates %s · RDM %s", analysis.Human(st.VMDisks), analysis.Human(st.Snapshots), analysis.Human(st.Other), analysis.Human(st.Templates), analysis.Human(st.RDM))))
	fmt.Fprintf(&b, "  Plan %s usable  %s\n", t.acc.Bold(true).Render(analysis.Human(st.Plan)),
		t.mute.Render(fmt.Sprintf("+%s%% growth, %s%% free · provisioned %s", fmtNum(p.Growth), fmtNum(p.FreeSpace), analysis.Human(st.Provisioned))))
	b.WriteString(t.faint.Render(fmt.Sprintf("  Not included: swap %s, orphaned disks %s", analysis.Human(st.Swap), analysis.Human(st.Orphans))) + "\n")
	var types []string
	for _, u := range st.ByType {
		types = append(types, fmt.Sprintf("%s %s %s", u.Type, u.Protocol, analysis.Human(u.Used)))
	}
	used := 0.0
	if st.Capacity > 0 {
		used = float64(st.Used) / float64(st.Capacity) * 100
	}
	fmt.Fprintf(&b, "  Datastores %s used of %s %s  %s\n", analysis.Human(st.Used), analysis.Human(st.Capacity), t.utilBar("", used), t.mute.Render(strings.Join(types, " · ")))
	if io := st.IO; io.Available {
		src := ""
		if io.Preview {
			src = " (vCenter history)"
		}
		parts := []string{fmt.Sprintf("%.0f%% reads, peak %s", io.ReadPct, report.Num(io.IOPSPeak))}
		if io.Throughput {
			parts = append(parts, fmt.Sprintf("%.0f MB/s, peak %.0f", io.MBps, io.MBpsPeak), fmt.Sprintf("%.0f KB per I/O", io.IOSizeKB))
		} else {
			parts = append(parts, "no throughput in the data yet")
		}
		if io.Latency {
			parts = append(parts, fmt.Sprintf("%.1f ms", io.LatencyMs))
		}
		fmt.Fprintf(&b, "  IOPS p%.0f %s  %s\n", sz.Percentile, t.acc.Bold(true).Render(report.Num(io.IOPS)), t.mute.Render(strings.Join(parts, " · ")+src))
	} else {
		b.WriteString(t.faint.Render("  Storage performance: waiting for the first samples.") + "\n")
	}

	b.WriteString("\n" + t.section("Connectivity today", ""))
	for _, c := range sz.Clusters {
		parts := []string{}
		if c.Links.NICs != "" {
			nics := c.Links.NICs
			if c.Links.NICsDown > 0 {
				nics += t.wrn.Render(fmt.Sprintf(" (+%d down)", c.Links.NICsDown))
			}
			parts = append(parts, nics)
		}
		if c.Links.HBAs != "" {
			parts = append(parts, c.Links.HBAs)
		}
		if len(c.Protocols) > 0 {
			parts = append(parts, strings.Join(c.Protocols, ", "))
		}
		if c.Links.StorageMTU != "" {
			parts = append(parts, "storage MTU "+c.Links.StorageMTU)
		}
		if len(parts) == 0 {
			parts = append(parts, t.faint.Render("hardware details not read yet"))
		}
		fmt.Fprintf(&b, "  %-*s %s\n", w, clip(c.Name, w), strings.Join(parts, t.faint.Render(" · ")))
	}
	if len(sz.Notes) > 0 {
		b.WriteString("\n" + t.section("Before you order", "all notes are in the PDF"))
		for _, n := range sz.Notes {
			b.WriteString(lipgloss.NewStyle().Width(m.vp.Width()).PaddingLeft(4).Render(t.wrn.Render("• ")+t.mute.Render(n)) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// utilBar shows a utilisation percentage as a short coloured gauge.
func (t theme) utilBar(label string, pct float64) string {
	const w = 10
	n := int(min(max(pct, 0), 100)/100*w + 0.5)
	s := t.load(pct)
	out := s.Render(strings.Repeat("■", n)) + t.faint.Render(strings.Repeat("·", w-n)) + " " + s.Render(fmt.Sprintf("%.0f%%", pct))
	if label != "" {
		out = t.mute.Render(label+" ") + out
	}
	return out
}
