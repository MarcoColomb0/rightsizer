package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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

func (m Model) keySizing(k tea.KeyMsg) (tea.Model, tea.Cmd) {
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
	case "left", "right", " ":
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
		return m.busyCmd("Saving the sizing options…", "sizing-params", "", func() error { return b.SetSizingParams(p) })
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
		var pe *analysis.ParamError
		if errors.As(err, &pe) {
			if f, ok := paramField[pe.Field]; ok {
				field = f
			}
		}
		return p, field, err
	}
	return p, 0, nil
}

func (m Model) viewSizingOpts() string {
	f := m.szf
	var b strings.Builder
	b.WriteString(sBold.Render("Sizing options") + "\n")
	b.WriteString(sMuted.Render("Used for every vCenter's sizing and kept across runs and upgrades.") + "\n\n")
	row := func(fi int, label, val, help string) {
		l := sLabel.Width(22).Render(label)
		if f.focus == fi {
			l = sFocus.Width(22).Render("› " + label)
		}
		if help != "" {
			val += sMuted.Render("  " + help)
		}
		b.WriteString(l + val + "\n")
	}
	in := func(fi int) string { return m.szIn[szInput[fi]].View() }
	basis := "As provisioned"
	if f.p.Basis == analysis.BasisRightsized {
		basis = "Rightsized"
	}
	off := "Leave out"
	if f.p.PoweredOff {
		off = "Include"
	}
	row(soBasis, "Compute basis", choice(basis, f.focus == soBasis), "VMs as configured today, or after rightsizing")
	row(soOff, "Powered-off VMs", choice(off, f.focus == soOff), "in compute; their storage always counts")
	row(soGrowth, "Growth %", in(soGrowth), "")
	row(soRatio, "vCPU per core", in(soRatio), "0 = today's ratio, at least 4")
	row(soCPU, "CPU target %", in(soCPU), "with the HA spares out")
	row(soMem, "Memory target %", in(soMem), "")
	row(soSpares, "HA spares per cluster", in(soSpares), "")
	row(soSockets, "Sockets per node", choice(strconv.Itoa(f.p.Sockets), f.focus == soSockets), "")
	row(soUplift, "Per-core uplift %", in(soUplift), "how much faster the new cores are")
	row(soFree, "Storage kept free %", in(soFree), "")
	row(soGroups, "Workload groups", in(soGroups), "")
	b.WriteString(sLabel.Width(22).Render("") + sMuted.Render("name=pattern,pattern; … e.g. databases=sql*,*ora*; vdi=vdi-*") + "\n\n")
	btn := sBox
	if f.focus == soSave {
		btn = btn.BorderForeground(accent).Foreground(accent).Bold(true)
	}
	b.WriteString(btn.Render("Save options") + "\n\n")
	b.WriteString(keys("↑/↓", "move", "←/→", "change", "enter", "next/save", "esc", "cancel"))
	return b.String()
}

func (m Model) sizingView() string {
	sz := m.sz
	if sz == nil || m.szID != m.cur {
		if m.szErr != "" {
			return sMuted.Render(m.szErr)
		}
		return m.spin.View() + sMuted.Render(" Computing the sizing…")
	}
	p := sz.Params
	var b strings.Builder
	ratio := "auto"
	if p.Ratio > 0 {
		ratio = fmtNum(p.Ratio) + ":1"
	}
	b.WriteString(sMuted.Render(fmt.Sprintf("Sized %s · growth %s%% · vCPU/core %s · CPU ≤ %s%% · RAM ≤ %s%% · N+%d · %d-socket nodes",
		analysis.BasisLabel(p.Basis), fmtNum(p.Growth), ratio, fmtNum(p.CPUTarget), fmtNum(p.MemTarget), p.Spares, p.Sockets)) + "\n\n")

	bi := 0
	if p.Basis == analysis.BasisRightsized {
		bi = 1
	}
	w := 12
	for _, c := range sz.Clusters {
		w = max(w, min(len([]rune(c.Name)), 24))
	}
	pad := strings.Repeat(" ", w+3)
	b.WriteString(sBold.Render("Recommended nodes") + "\n")
	for _, c := range sz.Clusters {
		n := c.Needs[bi]
		o, ok := n.Picked()
		if !ok {
			if n.Unsized() {
				b.WriteString(fmt.Sprintf("  %-*s %s\n", w, clip(c.Name, w), sWarn.Render("no node shape fits; left out of the totals, see the notes")))
			}
			continue
		}
		b.WriteString(fmt.Sprintf("  %-*s %s  %s\n", w, clip(c.Name, w), sAccent.Render(o.String()), fmt.Sprintf("%d cores, today %d", o.TotalCores, c.Cores)))
		b.WriteString(pad + sMuted.Render(fmt.Sprintf("CPU %.0f%% · RAM %.0f%% with spares out · %s", o.CPUUtil, o.MemUtil, n.Ports)) + "\n")
	}
	t := sz.Totals
	nt, alt := t.For(p.Basis), t.For(analysis.OtherBasis(p.Basis))
	b.WriteString(fmt.Sprintf("  %-*s %s  %s\n", w, "Total", sBold.Render(fmt.Sprintf("%d nodes · %d cores · %s RAM", nt.Nodes, nt.Cores, analysis.GBLabel(nt.MemGB))),
		sMuted.Render(fmt.Sprintf("today %d hosts · %d cores · %s", t.Hosts, t.Cores, analysis.Human(t.MemB)))))
	other := "Rightsized"
	if p.Basis == analysis.BasisRightsized {
		other = "As provisioned"
	}
	b.WriteString(pad + sMuted.Render(fmt.Sprintf("%s instead: %d nodes · %d cores · %s RAM", other, alt.Nodes, alt.Cores, analysis.GBLabel(alt.MemGB))) + "\n\n")

	b.WriteString(sBold.Render("Compute today") + "\n")
	for _, c := range sz.Clusters {
		b.WriteString(fmt.Sprintf("  %-*s %d hosts · %d cores · %s RAM · %d vCPU (%.1f:1) · %s vRAM\n", w, clip(c.Name, w),
			c.Hosts, c.Cores, analysis.Human(c.MemB), c.VCPU, c.Ratio, analysis.GiB(c.MemMB)))
		b.WriteString(pad + sMuted.Render(fmt.Sprintf("%d VMs on, %d off · CPU p%.0f %s, peak %s", c.VMs, c.VMsOff, sz.Percentile, ghz(c.DemandMHz), ghz(c.PeakMHz))) + "\n")
	}

	st := sz.Storage
	b.WriteString("\n" + sBold.Render("Storage") + sMuted.Render("  raw used, before data reduction") + "\n")
	b.WriteString(fmt.Sprintf("  Raw used %s  %s\n", sAccent.Render(analysis.Human(st.RawUsed)),
		sMuted.Render(fmt.Sprintf("disks %s · snapshots %s · other %s · templates %s · RDM %s", analysis.Human(st.VMDisks), analysis.Human(st.Snapshots), analysis.Human(st.Other), analysis.Human(st.Templates), analysis.Human(st.RDM)))))
	b.WriteString(fmt.Sprintf("  Plan %s usable  %s\n", sAccent.Render(analysis.Human(st.Plan)),
		sMuted.Render(fmt.Sprintf("+%s%% growth, %s%% free · provisioned %s", fmtNum(p.Growth), fmtNum(p.FreeSpace), analysis.Human(st.Provisioned)))))
	b.WriteString(sMuted.Render(fmt.Sprintf("  Not included: swap %s, orphaned disks %s", analysis.Human(st.Swap), analysis.Human(st.Orphans))) + "\n")
	var types []string
	for _, u := range st.ByType {
		types = append(types, fmt.Sprintf("%s %s %s", u.Type, u.Protocol, analysis.Human(u.Used)))
	}
	b.WriteString(fmt.Sprintf("  Datastores %s used of %s  %s\n", analysis.Human(st.Used), analysis.Human(st.Capacity), sMuted.Render(strings.Join(types, " · "))))
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
		b.WriteString(fmt.Sprintf("  IOPS p%.0f %s  %s\n", sz.Percentile, sAccent.Render(report.Num(io.IOPS)), sMuted.Render(strings.Join(parts, " · ")+src)))
	} else {
		b.WriteString(sMuted.Render("  Storage performance: waiting for the first samples.") + "\n")
	}

	b.WriteString("\n" + sBold.Render("Connectivity today") + "\n")
	for _, c := range sz.Clusters {
		parts := []string{}
		if c.Links.NICs != "" {
			nics := c.Links.NICs
			if c.Links.NICsDown > 0 {
				nics += fmt.Sprintf(" (+%d down)", c.Links.NICsDown)
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
			parts = append(parts, sMuted.Render("hardware details not read yet"))
		}
		b.WriteString(fmt.Sprintf("  %-*s %s\n", w, clip(c.Name, w), strings.Join(parts, " · ")))
	}
	if len(sz.Notes) > 0 {
		b.WriteString("\n" + sBold.Render("Before you order") + sMuted.Render("  all notes are in the PDF") + "\n")
		for i, n := range sz.Notes {
			if i == 3 {
				break
			}
			b.WriteString(sMuted.Render("  • "+clip(n, max(m.w-10, 40))) + "\n")
		}
	}
	return b.String()
}

// scrolled shows the part of body that fits below `above` lines, from the
// scroll offset.
func (m Model) scrolled(body string, above int) string {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	avail := max(m.h-above-7, 6)
	if len(lines) <= avail {
		return body
	}
	off := min(max(m.szScroll, 0), len(lines)-avail+1)
	end := min(off+avail-1, len(lines))
	out := strings.Join(lines[off:end], "\n")
	more := fmt.Sprintf("  ↑/↓ scroll · lines %d-%d of %d", off+1, end, len(lines))
	return out + "\n" + sMuted.Render(more)
}

func (m Model) scrollSizing(d int) Model {
	n := strings.Count(m.sizingView(), "\n") + 1
	m.szScroll = min(max(m.szScroll+d, 0), max(n-6, 0))
	return m
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
