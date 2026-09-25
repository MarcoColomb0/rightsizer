package report

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
)

// WriteSizingPDF renders the hardware refresh sizing.
func WriteSizingPDF(sz *analysis.Sizing, path string) error {
	d := newDoc("Hardware Refresh Sizing", sz.VCenter, sz.Generated)
	s := &sizingDoc{doc: d, sz: sz}
	s.cover()
	s.compute()
	s.connectivity()
	s.storage()
	s.current()
	s.checklist()
	return d.save(path)
}

type sizingDoc struct {
	*doc
	sz *analysis.Sizing
}

func (s *sizingDoc) basis() int {
	if s.sz.Params.Basis == analysis.BasisRightsized {
		return 1
	}
	return 0
}

func basisLabel(b string) string {
	if b == analysis.BasisRightsized {
		return "rightsized"
	}
	return "as provisioned"
}

func (s *sizingDoc) kpi(x, y, w float64, label, value, sub string) {
	s.fill(cBand)
	s.Rect(x, y, w, 26, "F")
	s.fill(cAccent)
	s.Rect(x, y, 1, 26, "F")
	s.SetXY(x+4, y+3)
	s.font("", 8)
	s.color(cMuted)
	s.CellFormat(w-6, 4, s.tr(label), "", 2, "L", false, 0, "")
	s.SetX(x + 4)
	s.font("B", 12.5)
	for size := 12.5; s.GetStringWidth(value) > w-6 && size > 7; size -= 0.5 {
		s.SetFontSize(size - 0.5)
	}
	s.color(cInk)
	s.CellFormat(w-6, 8, s.tr(value), "", 2, "L", false, 0, "")
	s.SetX(x + 4)
	s.font("", 8)
	s.color(cAccent)
	s.CellFormat(w-6, 4, s.tr(sub), "", 2, "L", false, 0, "")
}

func tb(b int64) string { return analysis.Human(b) }

func gbToTB(gb int) string {
	if gb >= 1024 {
		return fmt.Sprintf("%.1f TB", float64(gb)/1024)
	}
	return fmt.Sprintf("%d GB", gb)
}

func (s *sizingDoc) cover() {
	sz, p := s.sz, s.sz.Params
	s.AddPage()
	s.fill(cAccent)
	s.Rect(0, 0, pageW, 3, "F")
	s.SetY(22)
	s.font("B", 24)
	s.color(cInk)
	s.CellFormat(content, 11, "Hardware Refresh Sizing", "", 1, "L", false, 0, "")
	s.font("", 11)
	s.color(cMuted)
	s.CellFormat(content, 6, s.tr("Compute sized "+basisLabel(p.Basis)+", storage from raw used capacity"), "", 1, "L", false, 0, "")
	s.Ln(4)
	window := "no performance data yet"
	if !sz.Start.IsZero() {
		window = fmt.Sprintf("%s → %s", sz.Start.Format("2006-01-02 15:04"), sz.End.Format("2006-01-02 15:04"))
	}
	ratio := "automatic (today's, at least 4:1)"
	if p.Ratio > 0 {
		ratio = fmt.Sprintf("%g:1", p.Ratio)
	}
	estate := "not read yet"
	if !sz.EstateTaken.IsZero() {
		estate = sz.EstateTaken.Format("2006-01-02 15:04")
	}
	meta := [][2]string{
		{"vCenter", sz.VCenter},
		{"Performance data", window},
		{"Hardware read", estate},
		{"Parameters", fmt.Sprintf("growth %g%% · vCPU per core %s · CPU ≤ %g%% and memory ≤ %g%% with %d HA spare(s) out · %d socket(s) per node · per-core uplift %g%% · keep %g%% storage free · p%.0f",
			p.Growth, ratio, p.CPUTarget, p.MemTarget, p.Spares, p.Sockets, p.Uplift, p.FreeSpace, sz.Percentile)},
		{"Generated", sz.Generated.Format("2006-01-02 15:04 MST")},
	}
	for _, m := range meta {
		s.font("B", 9)
		s.color(cMuted)
		s.CellFormat(35, 5.5, s.tr(m[0]), "", 0, "L", false, 0, "")
		s.font("", 9)
		s.color(cInk)
		s.MultiCell(content-35, 5.5, s.tr(m[1]), "", "L", false)
	}
	s.Ln(5)

	t := sz.Totals
	nt := t.For(p.Basis)
	w := (content - 3*4) / 4
	y := s.GetY()
	s.kpi(margin, y, w, "New nodes", fmt.Sprint(nt.Nodes), fmt.Sprintf("today %d hosts", t.Hosts))
	s.kpi(margin+(w+4), y, w, "Physical cores", fmt.Sprintf("%d → %d", t.Cores, nt.Cores), pct(t.Cores, nt.Cores))
	s.kpi(margin+2*(w+4), y, w, "RAM", fmt.Sprintf("%s → %s", tb(t.MemB), gbToTB(nt.MemGB)), pct(int(t.MemB>>30), nt.MemGB))
	s.kpi(margin+3*(w+4), y, w, "Raw used storage", tb(sz.Storage.RawUsed), "plan "+tb(sz.Storage.Plan)+" usable")
	s.SetY(y + 32)

	s.h2("Recommended nodes")
	rows := [][]string{}
	for _, c := range sz.Clusters {
		n := c.Needs[s.basis()]
		o, ok := n.Picked()
		if !ok {
			continue
		}
		rows = append(rows, []string{c.Name, fmt.Sprint(o.Nodes), o.Spec(), fmt.Sprint(o.TotalCores), gbToTB(o.Nodes * o.MemGB), n.Ports.String()})
	}
	if len(rows) == 0 {
		s.para("No cluster with running VMs yet.")
	} else {
		s.table([]col{{"Cluster", 30, "L"}, {"Nodes", 12, "R"}, {"Per node", 42, "L"}, {"Cores", 14, "R"}, {"RAM", 16, "R"}, {"Ports per node", 66, "L"}}, rows)
		s.font("I", 7.5)
		s.color(cMuted)
		s.text(4, fmt.Sprintf("Node counts include %d HA spare(s) per cluster. Other node shapes are compared on the next page.", p.Spares))
	}

	s.h2("Summary")
	alt := t.For(otherBasis(p.Basis))
	s.para(fmt.Sprintf("%d powered-on VMs (%d vCPU, %s configured) and %d powered-off VMs run on %d hosts with %d physical cores and %s of RAM. "+
		"Sized %s with %g%% growth, they need %d nodes with %d cores and %s of RAM. Applying the rightsizing recommendations instead would need %d nodes with %d cores and %s.",
		t.VMs, t.VCPU, analysis.GiB(t.MemMB), t.VMsOff, t.Hosts, t.Cores, tb(t.MemB),
		basisLabel(p.Basis), p.Growth, nt.Nodes, nt.Cores, gbToTB(nt.MemGB), alt.Nodes, alt.Cores, gbToTB(alt.MemGB)))
	st := sz.Storage
	perf := "Storage performance data is not available yet."
	if io := st.IO; io.Available {
		perf = fmt.Sprintf("Storage load at p%.0f: %s IOPS (%.0f%% reads, peak %s) and %.0f MB/s (peak %.0f MB/s), average I/O size %.0f KB.",
			sz.Percentile, Num(io.IOPS), io.ReadPct, Num(io.IOPSPeak), io.MBps, io.MBpsPeak, io.IOSizeKB)
	}
	s.para(fmt.Sprintf("The VMs store %s of data (raw used, before any data reduction on the new storage); with growth and %g%% free space, plan %s of usable capacity before data reduction. %s",
		tb(st.RawUsed), p.FreeSpace, tb(st.Plan), perf))
	if sz.Preview || !sz.Final {
		s.Ln(1)
		s.font("I", 8.5)
		s.color(cWarn)
		msg := "Interim sizing: the analysis is still collecting data. Percentiles settle as the window completes."
		if sz.Preview {
			msg = "Preview: some figures come from vCenter's historical averages, which smooth out short peaks. Each figure switches to 20-second data once it has 24 hours of it."
		}
		s.text(4.2, msg)
	}
}

func otherBasis(b string) string {
	if b == analysis.BasisRightsized {
		return analysis.BasisProvisioned
	}
	return analysis.BasisRightsized
}

// Num formats a count with thousands separators.
func Num(f float64) string {
	n := int64(math.Round(f))
	neg := n < 0
	if neg {
		n = -n
	}
	s := fmt.Sprint(n)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func ghzOf(mhz float64) string {
	if mhz <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f GHz", mhz/1000)
}

func (s *sizingDoc) compute() {
	sz := s.sz
	s.AddPage()
	s.h1("Compute")
	s.para(fmt.Sprintf("Cores cover the larger of two needs: the vCPUs at the planned vCPU-per-core ratio, and the measured CPU demand (p%.0f) at %g%% utilisation with the HA spares out. "+
		"Memory is not overcommitted: configured memory plus 5%% hypervisor overhead must fit in %g%% of the RAM of the nodes left after losing the spares.",
		sz.Percentile, sz.Params.CPUTarget, sz.Params.MemTarget))
	s.h2("Today")
	rows := [][]string{}
	for _, c := range sz.Clusters {
		rows = append(rows, []string{
			c.Name, fmt.Sprint(c.Hosts), threads(c.Cores, c.Threads), tb(c.MemB),
			fmt.Sprintf("%d / %d", c.VMs, c.VMsOff), fmt.Sprint(c.VCPU), analysis.GiB(c.MemMB), fmt.Sprintf("%.1f:1", c.Ratio),
			fmt.Sprintf("%s / %s", ghzOf(c.DemandMHz), ghzOf(c.PeakMHz)), tb(int64(c.ConsumedB)),
		})
	}
	s.table([]col{{"Cluster", 28, "L"}, {"Hosts", 11, "R"}, {"Cores (thr.)", 19, "R"}, {"RAM", 16, "R"}, {"VMs on/off", 16, "R"},
		{"vCPU", 12, "R"}, {"vRAM", 17, "R"}, {"vCPU:core", 15, "R"}, {"CPU p/peak", 24, "R"}, {"Mem used", 16, "R"}}, rows)
	s.font("I", 7.5)
	s.color(cMuted)
	s.text(4, fmt.Sprintf("vCPU and vRAM: powered-on VMs as configured. CPU: demand of all hosts at p%.0f and peak. Mem used: memory the hosts back for VMs (consumed) at p%.0f.", sz.Percentile, sz.Percentile))

	s.h2("Needed")
	rows = rows[:0]
	for _, c := range sz.Clusters {
		for _, n := range c.Needs {
			rows = append(rows, []string{c.Name, basisLabel(n.Basis), fmt.Sprint(n.VCPU), analysis.GiB(n.MemMB), fmt.Sprintf("%g:1", n.Ratio),
				fmt.Sprint(n.CoresByRatio), fmt.Sprint(n.CoresByDemand), fmt.Sprint(n.Cores), tb(int64(n.MemB))})
		}
	}
	s.table([]col{{"Cluster", 28, "L"}, {"Basis", 22, "L"}, {"vCPU", 14, "R"}, {"vRAM", 18, "R"}, {"Ratio", 14, "R"},
		{"Cores by ratio", 20, "R"}, {"Cores by demand", 22, "R"}, {"Cores", 14, "R"}, {"RAM needed", 20, "R"}}, rows)
	s.font("I", 7.5)
	s.color(cMuted)
	s.text(4, fmt.Sprintf("Figures include %g%% growth. RAM needed covers the nodes left running with the HA spares out.", sz.Params.Growth))

	for _, c := range sz.Clusters {
		n := c.Needs[s.basis()]
		if len(n.Options) == 0 {
			continue
		}
		if s.GetY() > 200 {
			s.AddPage()
		}
		s.h2(fmt.Sprintf("%s: node options (%s)", c.Name, basisLabel(n.Basis)))
		rows = rows[:0]
		for i, o := range n.Options {
			mark := ""
			if i == n.Pick {
				mark = "recommended"
			}
			fit := "no"
			if o.NUMAFit {
				fit = "yes"
			}
			rows = append(rows, []string{mark, fmt.Sprint(o.Nodes), fmt.Sprintf("%d × %d", o.Sockets, o.CoresPerSocket), fmt.Sprintf("%d GB", o.MemGB),
				fmt.Sprint(o.TotalCores), gbToTB(o.Nodes * o.MemGB), fmt.Sprintf("%.0f%%", o.CPUUtil), fmt.Sprintf("%.0f%%", o.MemUtil),
				fmt.Sprintf("%.1f:1", o.Ratio), minGHz(o.MinGHz), fit})
		}
		s.table([]col{{"", 20, "L"}, {"Nodes", 11, "R"}, {"CPUs × cores", 18, "R"}, {"RAM/node", 16, "R"}, {"Cores", 13, "R"}, {"RAM total", 17, "R"},
			{"CPU load", 14, "R"}, {"RAM load", 14, "R"}, {"vCPU:core", 16, "R"}, {"Min clock", 16, "R"}, {"NUMA fit", 14, "R"}}, rows)
		extra := ""
		if c.LargestCPU.VCPU > 0 {
			extra = fmt.Sprintf(" Largest VMs: %s (%d vCPU), %s (%s).", c.LargestCPU.Name, c.LargestCPU.VCPU, c.LargestMem.Name, analysis.GiB(c.LargestMem.MemMB))
		}
		if c.ReserveMB > 0 || c.ReserveMHz > 0 {
			extra += fmt.Sprintf(" Reservations: %.1f GHz, %s.", float64(c.ReserveMHz)/1000, analysis.GiB(int(c.ReserveMB)))
		}
		s.font("I", 7.5)
		s.color(cMuted)
		s.text(4, "Loads are the planned demand, growth included, with the HA spares out. Min clock: lowest core clock that keeps CPU at target, at today's performance per clock. NUMA fit: the largest VM fits in one socket."+extra)
	}
}

func threads(cores, threads int) string {
	if threads > cores {
		return fmt.Sprintf("%d (%d)", cores, threads)
	}
	return fmt.Sprint(cores)
}

func minGHz(g float64) string {
	if g <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f GHz", g)
}

func gbps(kbps float64) string {
	if kbps <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f Gb/s", kbps*8/1e6)
}

func (s *sizingDoc) connectivity() {
	sz := s.sz
	s.AddPage()
	s.h1("Connectivity")
	s.para("Today's adapters and the measured network and storage load, followed by the ports each new node needs. New nodes start from redundant 25 GbE and 32G FC; a faster speed is suggested when today's adapters are already faster or the peak load per node would fill more than 40-50% of the pair.")
	rows := [][]string{}
	for _, c := range sz.Clusters {
		nics := c.Links.NICs
		if c.Links.NICsDown > 0 {
			nics += fmt.Sprintf(" (+%d down)", c.Links.NICsDown)
		}
		rows = append(rows, []string{c.Name, dashS(nics), dashS(c.Links.HBAs), dashS(strings.Join(c.Protocols, ", ")), dashS(c.Links.StorageMTU),
			gbps(c.NetPeakKBps), gbps(c.KBpsPeak)})
	}
	s.table([]col{{"Cluster", 24, "L"}, {"NIC ports", 50, "L"}, {"Storage adapters", 32, "L"}, {"Protocols", 20, "L"}, {"MTU", 12, "R"}, {"Net peak", 16, "R"}, {"Storage peak", 18, "R"}}, rows)
	s.font("I", 7.5)
	s.color(cMuted)
	s.text(4, "Ports are counted on every host of the cluster. Peaks are the busiest 20-second sample of all hosts together. World wide port names and every adapter are listed in the data download.")

	s.h2("Ports for the new nodes")
	rows = rows[:0]
	b := s.basis()
	eth, oob := 0, 0
	stor := map[string]int{}
	for _, c := range sz.Clusters {
		n := c.Needs[b]
		o, ok := n.Picked()
		if !ok {
			continue
		}
		pp := n.Ports
		st := "-"
		if pp.StoragePorts > 0 {
			st = fmt.Sprintf("%d × %s", pp.StoragePorts, pp.StorageLabel())
			stor[pp.StorageLabel()] += o.Nodes * pp.StoragePorts
		}
		eth += o.Nodes * pp.DataPorts
		oob += o.Nodes * pp.OOBPorts
		rows = append(rows, []string{c.Name, fmt.Sprint(o.Nodes), fmt.Sprintf("%d × %d GbE", pp.DataPorts, pp.DataGb), st, fmt.Sprintf("%d × 1 GbE", pp.OOBPorts),
			fmt.Sprint(o.Nodes * pp.DataPorts), storageTotal(o.Nodes, pp)})
	}
	s.table([]col{{"Cluster", 30, "L"}, {"Nodes", 12, "R"}, {"Data", 22, "L"}, {"Storage", 34, "L"}, {"Out-of-band", 22, "L"}, {"Data ports", 18, "R"}, {"Storage ports", 22, "R"}}, rows)
	var parts []string
	for k, v := range stor {
		parts = append(parts, fmt.Sprintf("%d × %s", v, k))
	}
	total := fmt.Sprintf("Switch ports for all new nodes: %d data ports, %d out-of-band ports", eth, oob)
	if len(parts) > 0 {
		total += ", " + strings.Join(parts, ", ")
	}
	s.para(total + ". Add inter-switch links and uplinks to your core network.")
	for _, c := range sz.Clusters {
		n := c.Needs[b]
		if n.Ports.Jumbo {
			s.para(fmt.Sprintf("%s: storage traffic uses jumbo frames today; keep MTU 9000 end to end on the new storage network.", c.Name))
		}
	}
}

func storageTotal(nodes int, pp analysis.PortPlan) string {
	if pp.StoragePorts == 0 {
		return "-"
	}
	return fmt.Sprint(nodes * pp.StoragePorts)
}

func dashS(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func (s *sizingDoc) storage() {
	sz := s.sz
	st := sz.Storage
	s.AddPage()
	s.h1("Storage")
	s.para(fmt.Sprintf("Raw used capacity is the data the VMs store, as vSphere sees it, before any data reduction on the new storage: apply the reduction ratio you expect for each workload type below. "+
		"Planned usable capacity adds %g%% growth and keeps %g%% free. Capacities are binary (1 TB = 1024 GB).", sz.Params.Growth, sz.Params.FreeSpace))
	rows := [][]string{
		{"VM disks", tb(st.VMDisks), "Base virtual disks of all VMs, powered off included"},
		{"Snapshots", tb(st.Snapshots), "Snapshot deltas and memory files"},
		{"Other VM files", tb(st.Other), "Configuration, logs, NVRAM, suspend files"},
		{"Templates", tb(st.Templates), ""},
		{"Raw device mappings", tb(st.RDM), fmt.Sprintf("%d VM(s); LUNs outside the datastores", st.RDMs)},
		{"Raw used", tb(st.RawUsed), "Sum of the rows above"},
		{"Planned usable", tb(st.Plan), fmt.Sprintf("Raw used × %.2f growth ÷ %.2f free space", 1+sz.Params.Growth/100, 1-sz.Params.FreeSpace/100)},
		{"Not included: swap files", tb(st.Swap), "Recreated by the new hosts at power-on"},
		{"Not included: orphaned disks", tb(st.Orphans), "Not used by any VM; verify and delete"},
		{"Of which powered-off VMs", tb(st.PoweredOff), "Candidates to archive before migrating"},
		{"Provisioned", tb(st.Provisioned), "Committed plus thin space not yet written"},
		{"Guest file systems used", tb(st.GuestUsed), fmt.Sprintf("As reported by VMware Tools, %.0f%% of VM disk data covered", st.GuestCoverage*100)},
		{"Datastores used", tb(st.Used), fmt.Sprintf("of %s on %d datastores", tb(st.Capacity), st.Datastores)},
	}
	s.table([]col{{"Capacity", 50, "L"}, {"Size", 25, "R"}, {"", 105, "L"}}, rows)

	if len(st.ByType) > 0 {
		s.h2("By datastore type")
		rows = rows[:0]
		for _, t := range st.ByType {
			free := "-"
			if t.Capacity > 0 {
				free = fmt.Sprintf("%.0f%%", float64(t.Capacity-t.Used)/float64(t.Capacity)*100)
			}
			rows = append(rows, []string{t.Type, t.Protocol, fmt.Sprint(t.Datastores), tb(t.Capacity), tb(t.Used), free})
		}
		s.table([]col{{"Type", 35, "L"}, {"Protocol", 30, "L"}, {"Datastores", 25, "R"}, {"Capacity", 30, "R"}, {"Used", 30, "R"}, {"Free", 20, "R"}}, rows)
	}

	if len(sz.Workloads) > 0 {
		if s.GetY() > 210 {
			s.AddPage()
		}
		s.h2("By workload")
		rows = rows[:0]
		var iops float64
		for _, w := range sz.Workloads {
			iops += w.IOPS
		}
		for _, w := range sz.Workloads {
			share := "-"
			if iops > 0 {
				share = fmt.Sprintf("%.0f%%", w.IOPS/iops*100)
			}
			rows = append(rows, []string{w.Name, fmt.Sprintf("%d / %d", w.On, w.VMs), fmt.Sprint(w.VCPU), analysis.GiB(w.MemMB), tb(w.Used), tb(w.Provisioned), tb(w.GuestUsed), Num(w.IOPS), share})
		}
		s.table([]col{{"Workload", 32, "L"}, {"VMs on/all", 18, "R"}, {"vCPU", 13, "R"}, {"vRAM", 18, "R"}, {"Raw used", 20, "R"}, {"Provisioned", 20, "R"}, {"Guest used", 20, "R"}, {"Avg IOPS", 18, "R"}, {"IOPS share", 18, "R"}}, rows)
		s.font("I", 7.5)
		s.color(cMuted)
		s.text(4, "Workloads follow the groups set in the sizing options, otherwise the guest operating system. Raw used includes raw device mappings; swap files are left out.")
	}

	s.storagePerf()
	s.datastoreTable()
}

func (s *sizingDoc) storagePerf() {
	sz := s.sz
	io := sz.Storage.IO
	if s.GetY() > 200 {
		s.AddPage()
	}
	s.h2("Performance")
	if !io.Available {
		s.para("No datastore performance data yet. rightsizer reads the 20-second datastore counters of every host from the first poll; vCenter keeps them in its history only at a higher statistics level.")
		return
	}
	pc := fmt.Sprintf("p%.0f", sz.Percentile)
	rows := [][]string{
		{"IOPS", Num(io.IOPS), Num(io.IOPSPeak), Num(io.IOPSAvg)},
		{"Throughput", fmt.Sprintf("%.0f MB/s", io.MBps), fmt.Sprintf("%.0f MB/s", io.MBpsPeak), "-"},
		{"Latency", fmt.Sprintf("%.1f ms", io.LatencyMs), "-", "-"},
	}
	s.table([]col{{"", 50, "L"}, {pc, 40, "R"}, {"Peak", 40, "R"}, {"Average", 40, "R"}}, rows)
	src := "20-second samples"
	if io.Preview {
		src = "vCenter history (averages smooth out peaks)"
	}
	s.para(fmt.Sprintf("Reads are %.0f%% of I/O operations; the average transfer is %.0f KB. All hosts summed at each sample, from %s over %.0f hours.", io.ReadPct, io.IOSizeKB, src, io.Hours))
	if len(io.Points) > 1 {
		if s.GetY() > 225 {
			s.AddPage()
		}
		s.ioChart(io.Points)
	}
}

func (s *sizingDoc) ioChart(pts []analysis.IOPoint) {
	x0, y0, w, h := margin+14, s.GetY()+2, content-16, 36.0
	var top float64
	for _, p := range pts {
		top = max(top, p.IOPS)
	}
	if top <= 0 {
		return
	}
	step := math.Pow(10, math.Floor(math.Log10(top)))
	top = math.Ceil(top/step) * step
	s.draw(cRule)
	s.SetLineWidth(0.2)
	s.font("", 6.5)
	s.color(cMuted)
	for _, g := range []float64{0, 0.25, 0.5, 0.75, 1} {
		y := y0 + h - h*g
		s.Line(x0, y, x0+w, y)
		s.SetXY(margin, y-1.5)
		s.CellFormat(13, 3, Num(top*g), "", 0, "R", false, 0, "")
	}
	t0, t1 := pts[0].T, pts[len(pts)-1].T
	span := t1.Sub(t0).Seconds()
	if span <= 0 {
		span = 1
	}
	s.draw(cAccent)
	s.SetLineWidth(0.45)
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		s.Line(x0+w*a.T.Sub(t0).Seconds()/span, y0+h-h*a.IOPS/top, x0+w*b.T.Sub(t0).Seconds()/span, y0+h-h*b.IOPS/top)
	}
	s.SetLineWidth(0.2)
	s.SetXY(x0, y0+h+1)
	s.CellFormat(w/2, 3, t0.Format("Jan 02 15:04"), "", 0, "L", false, 0, "")
	s.CellFormat(w/2, 3, t1.Format("Jan 02 15:04"), "", 1, "R", false, 0, "")
	s.SetX(x0)
	s.CellFormat(w, 4, s.tr("IOPS of all datastores, highest 5-minute average per point"), "", 1, "L", false, 0, "")
	s.Ln(2)
}

func (s *sizingDoc) datastoreTable() {
	ds := s.sz.Datastores
	if len(ds) == 0 {
		return
	}
	if s.GetY() > 220 {
		s.AddPage()
	}
	s.h2("Datastores")
	rows := [][]string{}
	for _, d := range ds {
		backing := d.Remote
		if len(d.LUNs) > 0 {
			l := d.LUNs[0]
			backing = strings.TrimSpace(fmt.Sprintf("%s %s", l.Vendor, l.Model))
			if l.Transport != "" {
				backing += " · " + l.Transport
			}
			if l.Paths > 0 {
				backing += fmt.Sprintf(" · %d paths", l.Paths)
			}
			if len(d.LUNs) > 1 {
				backing += fmt.Sprintf(" · %d extents", len(d.LUNs))
			}
		}
		iops, mbps, lat := "-", "-", "-"
		if d.IO.Available {
			iops, mbps, lat = Num(d.IO.IOPS), fmt.Sprintf("%.0f", d.IO.MBps), fmt.Sprintf("%.1f", d.IO.LatencyMs)
		}
		rows = append(rows, []string{d.Name, d.Version, dashS(backing), tb(d.Capacity), tb(d.Used), tb(d.Provisioned), iops, mbps, lat})
	}
	pc := fmt.Sprintf("p%.0f", s.sz.Percentile)
	s.table([]col{{"Datastore", 32, "L"}, {"Type", 18, "L"}, {"Backing", 44, "L"}, {"Capacity", 17, "R"}, {"Used", 16, "R"}, {"Prov.", 16, "R"}, {"IOPS " + pc, 15, "R"}, {"MB/s", 11, "R"}, {"ms", 10, "R"}}, rows)
	s.font("I", 7.5)
	s.color(cMuted)
	s.text(4, "Backing is the storage device as reported to vSphere, or the NFS export. Used is capacity minus free space as the datastore reports it: on NFS it may already reflect the current array's data reduction.")
}

func (s *sizingDoc) current() {
	sz := s.sz
	if len(sz.Hosts) == 0 {
		return
	}
	s.AddPage()
	s.h1("Current hosts")
	rows := [][]string{}
	for _, h := range sz.Hosts {
		bios := h.BIOS
		if !h.BIOSDate.IsZero() {
			bios = h.BIOSDate.Format("2006-01")
		}
		state := ""
		switch {
		case !h.Connected:
			state = " (disconnected)"
		case h.Maintenance:
			state = " (maintenance)"
		}
		rows = append(rows, []string{h.Cluster, h.Name + state, dashS(strings.TrimSpace(shortVendor(h.Vendor) + " " + h.Model)), dashS(h.Serial), shortCPU(h.CPUModel),
			fmt.Sprintf("%d × %d", h.Sockets, h.Cores/max(h.Sockets, 1)), tb(h.MemBytes), esxi(h.ESXi), dashS(bios)})
	}
	s.table([]col{{"Cluster", 16, "L"}, {"Host", 28, "L"}, {"Model", 30, "L"}, {"Serial", 18, "L"}, {"CPU", 44, "L"}, {"Sockets", 12, "R"}, {"RAM", 12, "R"}, {"ESXi", 10, "L"}, {"BIOS", 12, "L"}}, rows)
	s.font("I", 7.5)
	s.color(cMuted)
	s.text(4, "BIOS: release date of the installed firmware. Full versions, ESXi builds and every adapter are in the data download.")

	s.h2("Adapters")
	rows = rows[:0]
	for _, h := range sz.Hosts {
		nics := map[string]int{}
		for _, n := range h.NICs {
			l := "down"
			if n.SpeedMb > 0 {
				l = fmt.Sprintf("%g GbE", float64(n.SpeedMb)/1000)
			}
			nics[l]++
		}
		hbas := map[string]int{}
		for _, a := range h.HBAs {
			switch {
			case strings.Contains(a.Type, "FC") && a.SpeedGb > 0:
				hbas[fmt.Sprintf("%s %gG", a.Type, a.SpeedGb)]++
			case strings.Contains(a.Type, "FC"):
				hbas[a.Type+" down"]++
			case strings.HasPrefix(a.Type, "iSCSI"), strings.HasPrefix(a.Type, "NVMe/"):
				hbas[a.Type]++
			}
		}
		var mtu []string
		for _, k := range h.VMKs {
			if k.Storage {
				mtu = append(mtu, fmt.Sprintf("%s %d", k.Device, k.MTU))
			}
		}
		gpus := map[string]int{}
		for _, g := range h.GPUs {
			gpus[g.Name]++
		}
		rows = append(rows, []string{h.Name, dashS(counts(nics)), dashS(counts(hbas)), dashS(strings.Join(mtu, ", ")), dashS(counts(gpus))})
	}
	s.table([]col{{"Host", 34, "L"}, {"NIC ports", 42, "L"}, {"Storage adapters", 42, "L"}, {"Storage VMkernel MTU", 32, "L"}, {"GPUs", 30, "L"}}, rows)

	if len(sz.ClusterConfig) > 0 {
		s.h2("Cluster settings")
		rows = rows[:0]
		for _, c := range sz.ClusterConfig {
			ha := "off"
			if c.HA {
				ha = c.Admission
			}
			vsan := "no"
			if c.VSAN {
				vsan = "yes"
			}
			rows = append(rows, []string{c.Name, fmt.Sprint(c.Hosts), ha, dashS(c.DRS), dashS(c.EVC), vsan})
		}
		s.table([]col{{"Cluster", 32, "L"}, {"Hosts", 12, "R"}, {"HA admission control", 62, "L"}, {"DRS", 28, "L"}, {"EVC", 32, "L"}, {"vSAN", 12, "L"}}, rows)
	}
}

func esxi(full string) string {
	full = strings.TrimPrefix(full, "VMware ESXi ")
	if i := strings.Index(full, " build-"); i > 0 {
		return full[:i]
	}
	return dashS(full)
}

func shortCPU(m string) string {
	for _, x := range []string{"(R)", "(TM)", "(tm)", " CPU", " Processor", "-Core"} {
		m = strings.ReplaceAll(m, x, "")
	}
	return strings.Join(strings.Fields(m), " ")
}

func shortVendor(v string) string {
	for _, x := range []string{", Inc.", " Inc.", " Inc", " Corporation", " Corp.", " Co., Ltd.", " Technologies"} {
		v = strings.TrimSuffix(v, x)
	}
	return v
}

func counts(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%d × %s", m[k], k)
	}
	return strings.Join(parts, ", ")
}

func (s *sizingDoc) checklist() {
	sz := s.sz
	p := sz.Params
	s.AddPage()
	s.h1("Before you order")
	bullet := func(t string) {
		s.font("", 8.8)
		s.color(cInk)
		s.CellFormat(4, 4.6, "•", "", 0, "L", false, 0, "")
		s.MultiCell(content-4, 4.6, s.tr(t), "", "L", false)
		s.Ln(0.8)
	}
	for _, n := range sz.Notes {
		bullet(n)
	}
	s.h2("How the figures are computed")
	for _, t := range []string{
		fmt.Sprintf("As provisioned: every powered-on VM keeps its configured vCPU and memory%s. Rightsized: VMs with enough data take rightsizer's recommendation; the others stay as configured.", ifs(p.PoweredOff, ", and powered-off VMs are included", "; powered-off VMs are left out of compute but not of storage")),
		"vCPU per core: the planned ratio, or today's ratio with a floor of 4:1 when set to automatic. Cores by demand: measured CPU demand of all hosts at the percentile, divided by the CPU target and today's per-core clock, raised by the per-core uplift you entered for the new CPUs.",
		fmt.Sprintf("Node options use %d socket(s) with 16 to 128 cores each and 256 GB to 4 TB of RAM. CPUs below 16 cores are not suggested: vSphere is licensed per core with at least 16 counted per CPU. Each node must hold the largest VM.", p.Sockets),
		"The recommended option has the fewest servers among those whose total cores are within 10% of the lowest, preferring nodes where the largest VM fits in one socket. HA spares are added on top of the nodes needed; vSAN clusters get at least 3 nodes.",
		"Ports: two data ports and two storage ports per node for redundancy, plus one out-of-band management port. Ethernet storage can share the data ports when bandwidth allows.",
		"Storage performance: each host's datastore counters are summed across hosts at every 20-second sample, so percentiles and peaks describe the whole estate at once. Latency is weighted by the I/O of each host and datastore.",
		"rightsizer only reads from vCenter: property reads and performance queries, checked against an allow-list before they leave the tool.",
	} {
		bullet(t)
	}
	s.Ln(4)
	s.font("", 8.5)
	s.color(cMuted)
	s.text(4.4, fmt.Sprintf("rightsizer %s is open-source software by %s (%s), released under the Apache License 2.0. Source: %s", Version, Author, Website, Project))
}

func ifs(c bool, a, b string) string {
	if c {
		return a
	}
	return b
}
