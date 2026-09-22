package analysis

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

type Profile struct {
	Name        string
	Percentile  float64
	CPUTarget   float64
	MemHeadroom float64
	MinMemRatio float64
	HostCPU     float64
	HostMem     float64
}

var Profiles = []Profile{
	{"conservative", 99, 0.6, 1.4, 0.5, 0.6, 0.8},
	{"balanced", 95, 0.7, 1.25, 0.35, 0.7, 0.85},
	{"aggressive", 95, 0.8, 1.1, 0.25, 0.8, 0.9},
}

func ProfileByName(n string) Profile {
	for _, p := range Profiles {
		if p.Name == n {
			return p
		}
	}
	return Profiles[1]
}

type Kind string

const (
	CPUOver    Kind = "Oversized vCPU"
	CPUUnder   Kind = "Undersized vCPU"
	MemOver    Kind = "Oversized memory"
	MemUnder   Kind = "Undersized memory"
	Idle       Kind = "Idle VM"
	PoweredOff Kind = "Powered-off VM"
	OldSnap    Kind = "Old snapshot"
	ThickDisk  Kind = "Thick disk, low usage"
	Orphan     Kind = "Orphaned disk"
	WideVM     Kind = "Spans NUMA nodes"
	CoStopHigh Kind = "High co-stop"
)

type Severity int

const (
	Low Severity = iota
	Medium
	High
)

func (s Severity) String() string {
	return [...]string{"low", "medium", "high"}[s]
}

type Finding struct {
	UUID       string
	Path       string
	VM         string
	Cluster    string
	Kind       Kind
	Severity   Severity
	Current    string
	Suggested  string
	Detail     string
	VCPU       int
	MemMB      int
	Bytes      int64
	Confidence string
}

type VMResult struct {
	Name       string
	Cluster    string
	PowerOn    bool
	VCPU       int
	RecVCPU    int
	MemMB      int
	RecMemMB   int
	CPUP       float64
	CPUMax     float64
	MemP       float64
	ReadyAvg   float64
	CoStopAvg  float64
	Hours      float64
	Confidence string
	Preview    bool
	Excluded   bool
}

type ClusterResult struct {
	Name        string
	EarlierPeak *EarlierPeak
	Preview     bool
	Peaks       *Peaks
	Hosts       int
	CPUModel    string
	Cores       int
	CapMHz      float64
	CapMemB     float64
	CPUP        float64
	CPUPeak     float64
	MemP        float64
	VCPU        int
	RecVCPU     int
	MemMB       int
	RecMemMB    int
	NeedMHz     float64
	NeedMemB    float64
	NeedCores   int
	HostsNeeded int
	HostsForCPU int
	HostsForMem int
	HASpare     int
	Points      []Point
}

type Totals struct {
	VMs, On, Off, Templates, Analyzed int
	VCPU, RecVCPU                     int
	MemMB, RecMemMB                   int
	IdleVCPU, IdleMemMB               int
	Storage, Reclaim                  int64
	Hosts, HostsNeeded                int
	Cores, NeedCores                  int
}

type Result struct {
	Generated time.Time
	Start     time.Time
	End       time.Time
	Planned   time.Duration
	Final     bool
	Profile   Profile
	VCenter   string
	Totals    Totals
	Findings  []Finding
	VMs       []VMResult
	Clusters  []ClusterResult
	// Preview is set while some results still come from vCenter's
	// historical averages instead of 20-second samples.
	Preview     bool
	HistoryFrom time.Time
	Orphans     []vc.OrphanDisk
	WasteNote   string
	Excluded    []Excluded
	Accuracy    *Accuracy
}

// Input is everything one analysis is computed from. History holds vCenter's
// rolled-up statistics for the period before the analysis started.
type Input struct {
	Inv     *vc.Inventory
	RT      *Store
	History *Store
	// Live holds historical samples collected during the window, to compare
	// vCenter's averages with the 20-second data.
	Live       *Store
	Orphans    []vc.OrphanDisk
	WasteNote  string
	Exclusions []Exclusion
	Profile    Profile
	Start      time.Time
	End        time.Time
	Planned    time.Duration
}

// fullDay is the number of 20-second samples in 24 hours.
const fullDay = 24 * 3600 / 20

func (in Input) vmStats(ref string) (*VMStats, bool) {
	var rt, h *VMStats
	if in.RT != nil {
		rt = in.RT.VMs[ref]
	}
	if in.History != nil {
		h = in.History.VMs[ref]
	}
	switch {
	case rt != nil && rt.Samples >= fullDay:
		return rt, false
	case h != nil && (rt == nil || h.Samples > rt.Samples):
		return h, true
	}
	return rt, false
}

func (in Input) clusterStore(name string) (*Store, bool) {
	if in.RT != nil {
		if cs := in.RT.Clusters[name]; cs != nil && cs.CPU.N >= fullDay {
			return in.RT, false
		}
	}
	if in.History != nil && in.History.Clusters[name] != nil {
		return in.History, true
	}
	return in.RT, false
}

const minHours = 1.0

func Analyze(in Input) *Result {
	p, inv, start, end := in.Profile, in.Inv, in.Start, in.End
	r := &Result{Generated: time.Now(), Start: start, End: end, Planned: in.Planned, Profile: p, Orphans: in.Orphans, WasteNote: in.WasteNote}
	if inv == nil {
		return r
	}
	if in.History != nil {
		r.HistoryFrom = in.History.Anchor
	}
	window := hoursBetween(start, end)
	matched := map[string][]string{}
	cl := map[string]*ClusterResult{}
	getCl := func(n string) *ClusterResult {
		c := cl[n]
		if c == nil {
			c = &ClusterResult{Name: n}
			cl[n] = c
		}
		return c
	}
	models := map[string]map[string]int{}
	hosts := map[string]vc.Host{}
	for _, h := range inv.Hosts {
		hosts[h.Name] = h
		if !h.Connected {
			continue
		}
		c := getCl(h.Cluster)
		c.Hosts++
		c.Cores += h.Cores
		c.CapMHz += float64(h.MHz * h.Cores)
		c.CapMemB += float64(h.MemBytes)
		if models[h.Cluster] == nil {
			models[h.Cluster] = map[string]int{}
		}
		models[h.Cluster][h.CPUModel]++
	}

	t := &r.Totals
	vmsByCluster := map[string][]vc.VM{}
	for _, vm := range inv.VMs {
		t.VMs++
		if vm.Template {
			t.Templates++
			continue
		}
		t.Storage += vm.Committed
		xs := exclusions(in.Exclusions).forVM(vm.UUID, vm.Name)
		for _, x := range xs {
			matched[x.ID] = append(matched[x.ID], vm.Name)
		}
		keep := func(fs []Finding) []Finding {
			out := fs[:0]
			for _, f := range fs {
				f.UUID = vm.UUID
				if !coversAny(xs, f.Kind) {
					out = append(out, f)
				}
			}
			return out
		}
		r.Findings = append(r.Findings, keep(storageFindings(vm, end))...)
		s, preview := in.vmStats(vm.Ref)
		if !vm.PowerOn {
			t.Off++
			if s == nil || s.Samples == 0 {
				r.Findings = append(r.Findings, keep([]Finding{{
					VM: vm.Name, Cluster: vm.Cluster, Kind: PoweredOff, Severity: sevBytes(vm.Committed),
					Current:   fmt.Sprintf("off, %s on disk", human(vm.Committed)),
					Suggested: "Archive / delete",
					Detail:    fmt.Sprintf("Powered off for the whole analysis window (%d vCPU, %s configured). Still consumes datastore capacity and may count toward licensing. Confirm with the owner first.", vm.VCPU, gib(vm.MemMB)),
					Bytes:     vm.Committed, Confidence: conf(window, window),
				}})...)
				continue
			}
		} else {
			t.On++
		}
		vmsByCluster[vm.Cluster] = append(vmsByCluster[vm.Cluster], vm)
		c := getCl(vm.Cluster)
		c.VCPU += vm.VCPU
		c.MemMB += vm.MemMB
		t.VCPU += vm.VCPU
		t.MemMB += vm.MemMB
		if s == nil || s.Hours() < minHours {
			c.RecVCPU += vm.VCPU
			c.RecMemMB += vm.MemMB
			t.RecVCPU += vm.VCPU
			t.RecMemMB += vm.MemMB
			continue
		}
		t.Analyzed++
		vr, fs := rightsize(vm, s, p, window)
		if preview {
			vr.Preview, vr.Confidence = true, "preview"
			for i := range fs {
				fs[i].Confidence = "preview"
			}
			r.Preview = true
		}
		fs = keep(append(fs, placementFindings(vm, vr, s, hosts[vm.Host])...))
		if len(xs) > 0 {
			vr.Excluded = true
			if coversAny(xs, CPUOver) || coversAny(xs, CPUUnder) || coversAny(xs, Idle) {
				vr.RecVCPU = vm.VCPU
			}
			if coversAny(xs, MemOver) || coversAny(xs, MemUnder) || coversAny(xs, Idle) {
				vr.RecMemMB = vm.MemMB
			}
		}
		r.VMs = append(r.VMs, vr)
		r.Findings = append(r.Findings, fs...)
		c.RecVCPU += vr.RecVCPU
		c.RecMemMB += vr.RecMemMB
		t.RecVCPU += vr.RecVCPU
		t.RecMemMB += vr.RecMemMB
		for _, f := range fs {
			if f.Kind == Idle {
				t.IdleVCPU += f.VCPU
				t.IdleMemMB += f.MemMB
			}
		}
	}

	for name, c := range cl {
		st, preview := in.clusterStore(name)
		var cs *ClusterStats
		if st != nil {
			cs = st.Clusters[name]
		}
		if preview {
			r.Preview = true
		}
		c.CPUModel = topModel(models[name])
		c.Preview = preview
		sizeCluster(c, cs, p)
		c.Points = mergePoints(in, name)
		c.Peaks = peaks(c, vmsByCluster[name], st, p)
		c.Points = Downsample(c.Points, 336)
		c.EarlierPeak = earlierPeak(in.History, name, start, c.CapMHz)
		r.Clusters = append(r.Clusters, *c)
		t.Hosts += c.Hosts
		t.HostsNeeded += c.HostsNeeded
		t.Cores += c.Cores
		t.NeedCores += c.NeedCores
	}
	var orphans []vc.OrphanDisk
	for _, o := range in.Orphans {
		if x, ok := exclusions(in.Exclusions).forDisk(o.Path); ok {
			matched[x.ID] = append(matched[x.ID], o.Path)
			continue
		}
		orphans = append(orphans, o)
	}
	r.Orphans = orphans
	r.Accuracy = accuracy(inv, in.RT, in.Live, p)
	r.Findings = append(r.Findings, orphanFindings(orphans)...)
	for _, x := range in.Exclusions {
		if names := matched[x.ID]; len(names) > 0 {
			r.Excluded = append(r.Excluded, Excluded{Exclusion: x, Matched: names})
		}
	}
	for _, f := range r.Findings {
		switch f.Kind {
		case PoweredOff, OldSnap, ThickDisk, Orphan:
			t.Reclaim += f.Bytes
		}
	}
	slices.SortFunc(r.Clusters, func(a, b ClusterResult) int { return strings.Compare(a.Name, b.Name) })
	slices.SortFunc(r.VMs, func(a, b VMResult) int { return strings.Compare(a.Name, b.Name) })
	slices.SortStableFunc(r.Findings, func(a, b Finding) int {
		if a.Severity != b.Severity {
			return int(b.Severity) - int(a.Severity)
		}
		return strings.Compare(a.VM, b.VM)
	})
	return r
}

// mergePoints joins the historical and real-time demand timelines.
func mergePoints(in Input, name string) []Point {
	var out []Point
	var rtStart time.Time
	if in.RT != nil {
		if cs := in.RT.Clusters[name]; cs != nil && len(cs.Points) > 0 {
			rtStart = cs.Points[0].T
		}
	}
	if in.History != nil {
		if cs := in.History.Clusters[name]; cs != nil {
			for _, p := range cs.Points {
				if rtStart.IsZero() || p.T.Before(rtStart) {
					out = append(out, p)
				}
			}
		}
	}
	if in.RT != nil {
		if cs := in.RT.Clusters[name]; cs != nil {
			out = append(out, cs.Points...)
		}
	}
	return out
}

// placementFindings flag VMs that are slow because of their shape: wider than
// a NUMA node, or waiting for enough free cores to run all vCPUs together.
func placementFindings(vm vc.VM, vr VMResult, s *VMStats, h vc.Host) []Finding {
	var fs []Finding
	base := Finding{VM: vm.Name, Cluster: vm.Cluster, Confidence: vr.Confidence}
	if h.Cores > 0 && h.NUMANodes > 0 {
		perNode := h.Cores / h.NUMANodes
		memNode := h.MemBytes / int64(h.NUMANodes)
		wideCPU := perNode > 0 && vm.VCPU > perNode
		wideMem := memNode > 0 && int64(vm.MemMB)<<20 > memNode
		if wideCPU || wideMem {
			f := base
			f.Kind, f.Severity = WideVM, Medium
			f.Current = fmt.Sprintf("%d vCPU / %s on %d-core, %s NUMA nodes", vm.VCPU, gib(vm.MemMB), perNode, human(memNode))
			switch {
			case wideCPU && vr.RecVCPU <= perNode && !wideMem:
				f.Suggested = fmt.Sprintf("Rightsize to %d vCPU", vr.RecVCPU)
				f.Detail = fmt.Sprintf("The VM is wider than one NUMA node of %s, so some memory access is remote. The recommended %d vCPU fit in a single node.", vm.Host, vr.RecVCPU)
			case wideCPU:
				sockets := (vm.VCPU + perNode - 1) / perNode
				f.Suggested = fmt.Sprintf("Align to %d sockets", sockets)
				f.Detail = fmt.Sprintf("The VM needs more vCPU than one NUMA node of %s has (%d cores). Set cores per socket so each virtual socket fits in a node (currently %d cores per socket) so the guest sees the real topology.", vm.Host, perNode, vm.CoresPerSock)
			default:
				f.Suggested = "Reduce memory or align vNUMA"
				f.Detail = fmt.Sprintf("Configured memory exceeds the %s of one NUMA node on %s, so part of it is remote.", human(memNode), vm.Host)
			}
			fs = append(fs, f)
		}
	}
	if cs := s.CoStop.Avg(); cs >= 3 && vm.VCPU >= 2 {
		f := base
		f.Kind, f.Severity = CoStopHigh, High
		f.Current = fmt.Sprintf("%d vCPU, co-stop %.1f%%", vm.VCPU, cs)
		f.Suggested = fmt.Sprintf("Reduce to %d vCPU", min(vr.RecVCPU, vm.VCPU-1))
		f.Detail = "The hypervisor often has to pause some vCPUs while it waits for enough free cores to run them all together. Fewer vCPUs will make this VM faster, not slower."
		fs = append(fs, f)
	}
	return fs
}

func orphanFindings(os []vc.OrphanDisk) []Finding {
	var fs []Finding
	for _, o := range os {
		size := max(o.Size, 0)
		age := ""
		if !o.Modified.IsZero() {
			age = fmt.Sprintf(", last changed %s", o.Modified.Format("2006-01-02"))
		}
		fs = append(fs, Finding{
			VM: o.Path, Path: o.Path, Cluster: o.Datastore, Kind: Orphan, Severity: sevBytes(size),
			Current:   fmt.Sprintf("%s on %s%s", human(size), o.Datastore, age),
			Suggested: "Verify and delete",
			Detail:    "No VM or template registered in this vCenter uses this disk. Check that it does not belong to a VM in another vCenter, a backup or replication product, or a VM that is being restored before deleting it.",
			Bytes:     size, Confidence: "medium",
		})
	}
	return fs
}

func rightsize(vm vc.VM, s *VMStats, p Profile, window float64) (VMResult, []Finding) {
	h := s.Hours()
	cf := conf(h, window)
	vr := VMResult{
		Name: vm.Name, Cluster: vm.Cluster, PowerOn: vm.PowerOn, VCPU: vm.VCPU, MemMB: vm.MemMB,
		CPUP: s.CPU.Pct(p.Percentile), CPUMax: s.CPU.Max, MemP: s.Mem.Pct(p.Percentile),
		ReadyAvg: s.Ready.Avg(), CoStopAvg: s.CoStop.Avg(), Hours: h, Confidence: cf, RecVCPU: vm.VCPU, RecMemMB: vm.MemMB,
	}
	var fs []Finding
	base := Finding{VM: vm.Name, Cluster: vm.Cluster, Confidence: cf}

	idle := s.CPU.Pct(95) <= 2 && s.Net.Avg() < 5 && s.Disk.Avg() < 20 && vm.PowerOn
	if idle {
		f := base
		f.Kind, f.Severity = Idle, Medium
		f.Current = fmt.Sprintf("%d vCPU / %s", vm.VCPU, gib(vm.MemMB))
		f.Suggested = "Confirm owner, power off, then decommission"
		f.Detail = fmt.Sprintf("CPU p95 %.1f%%, network avg %.1f KB/s, disk avg %.1f KB/s over %.0fh.", s.CPU.Pct(95), s.Net.Avg(), s.Disk.Avg(), h)
		f.VCPU, f.MemMB, f.Bytes = vm.VCPU, vm.MemMB, 0
		fs = append(fs, f)
	}

	cpuP := s.CPU.Pct(p.Percentile)
	needCPU := max(1, int(math.Ceil(float64(vm.VCPU)*cpuP/100/p.CPUTarget)))
	switch {
	case s.CPU.Pct(99) >= 90 && vm.VCPU < 64:
		vr.RecVCPU = max(vm.VCPU+1, int(math.Ceil(float64(vm.VCPU)*s.CPU.Pct(99)/100/p.CPUTarget)))
		f := base
		f.Kind, f.Severity = CPUUnder, High
		f.Current, f.Suggested = fmt.Sprintf("%d vCPU", vm.VCPU), fmt.Sprintf("%d vCPU", vr.RecVCPU)
		f.Detail = fmt.Sprintf("CPU p99 %.0f%% of provisioned, peak %.0f%%.", s.CPU.Pct(99), s.CPU.Max)
		f.VCPU = vm.VCPU - vr.RecVCPU
		fs = append(fs, f)
	case needCPU < vm.VCPU:
		vr.RecVCPU = needCPU
		f := base
		f.Kind = CPUOver
		f.Severity = sevSaving(vm.VCPU-needCPU, float64(vm.VCPU-needCPU)/float64(vm.VCPU), 8, 2)
		f.Current, f.Suggested = fmt.Sprintf("%d vCPU", vm.VCPU), fmt.Sprintf("%d vCPU", needCPU)
		f.Detail = fmt.Sprintf("CPU p%.0f %.1f%% of provisioned, peak %.0f%%.", p.Percentile, cpuP, s.CPU.Max)
		if s.Ready.Avg() > 5 {
			f.Detail += fmt.Sprintf(" CPU ready %.1f%%/vCPU: fewer vCPUs should also cut scheduling latency.", s.Ready.Avg())
		}
		f.VCPU = vm.VCPU - needCPU
		if !idle {
			fs = append(fs, f)
		}
	}

	memP := s.Mem.Pct(p.Percentile)
	active := memP / 100 * float64(vm.MemMB)
	floor := 1024.0
	if strings.Contains(strings.ToLower(vm.GuestOS), "windows") {
		floor = 2048
	}
	need := roundGiB(max(active*p.MemHeadroom, floor, float64(vm.MemMB)*p.MinMemRatio))
	switch {
	case s.Mem.Pct(95) >= 90:
		vr.RecMemMB = roundGiB(float64(vm.MemMB) * 1.25)
		f := base
		f.Kind, f.Severity = MemUnder, High
		f.Current, f.Suggested = gib(vm.MemMB), gib(vr.RecMemMB)
		f.Detail = fmt.Sprintf("Active memory p95 %.0f%% of configured.", s.Mem.Pct(95))
		f.MemMB = vm.MemMB - vr.RecMemMB
		fs = append(fs, f)
	case need <= vm.MemMB-1024:
		vr.RecMemMB = need
		f := base
		f.Kind = MemOver
		f.Severity = sevSaving((vm.MemMB-need)/1024, float64(vm.MemMB-need)/float64(vm.MemMB), 32, 8)
		f.Current, f.Suggested = gib(vm.MemMB), gib(need)
		f.Detail = fmt.Sprintf("Active memory p%.0f %.1f%% (%s).", p.Percentile, memP, gib(int(active)))
		if c := s.Consumed.Avg(); c > 0 {
			f.Detail += fmt.Sprintf(" Host-consumed avg %s.", gib(int(c/1024)))
		}
		f.Detail += " Validate in-guest before reducing."
		f.MemMB = vm.MemMB - need
		if !idle {
			fs = append(fs, f)
		}
	}
	return vr, fs
}

func storageFindings(vm vc.VM, now time.Time) []Finding {
	var fs []Finding
	var oldest time.Time
	for _, s := range vm.Snapshots {
		if oldest.IsZero() || s.Created.Before(oldest) {
			oldest = s.Created
		}
	}
	if !oldest.IsZero() {
		age := now.Sub(oldest)
		if age > 72*time.Hour {
			sev := Medium
			if age > 7*24*time.Hour || vm.SnapshotBytes > 50<<30 {
				sev = High
			}
			fs = append(fs, Finding{
				VM: vm.Name, Cluster: vm.Cluster, Kind: OldSnap, Severity: sev,
				Current:   fmt.Sprintf("%d, %dd old, %s", len(vm.Snapshots), int(age.Hours()/24), human(vm.SnapshotBytes)),
				Suggested: "Delete / consolidate",
				Detail:    fmt.Sprintf("%d snapshot(s), oldest %q from %s. The delta chain uses %s and slows disk I/O.", len(vm.Snapshots), oldestName(vm.Snapshots), oldest.Format("2006-01-02"), human(vm.SnapshotBytes)),
				Bytes:     vm.SnapshotBytes, Confidence: "high",
			})
		}
	}
	var thick, guestCap, guestUsed int64
	for _, d := range vm.Disks {
		if !d.Thin && d.Capacity >= 20<<30 {
			thick += d.Capacity
		}
	}
	for _, g := range vm.GuestDisks {
		guestCap += g.Capacity
		guestUsed += g.Capacity - g.Free
	}
	if thick > 0 && guestCap > 0 && float64(guestUsed) < 0.5*float64(guestCap) {
		reclaim := int64(float64(thick) * (1 - float64(guestUsed)/float64(guestCap)))
		fs = append(fs, Finding{
			VM: vm.Name, Cluster: vm.Cluster, Kind: ThickDisk, Severity: sevBytes(reclaim),
			Current:   fmt.Sprintf("%s thick, %.0f%% used", human(thick), float64(guestUsed)/float64(guestCap)*100),
			Suggested: "Thin via Storage vMotion",
			Detail:    "Thick-provisioned disks reserve full capacity on the datastore regardless of use.",
			Bytes:     reclaim, Confidence: "medium",
		})
	}
	return fs
}

func sizeCluster(c *ClusterResult, cs *ClusterStats, p Profile) {
	if c.Hosts == 0 {
		return
	}
	if cs != nil {
		c.CPUP = cs.CPU.Pct(p.Percentile)
		c.CPUPeak = cs.CPU.Max
		c.MemP = cs.Mem.Pct(p.Percentile)
	}
	perHostMHz := c.CapMHz / float64(c.Hosts)
	perHostMem := c.CapMemB / float64(c.Hosts)
	demandMHz := c.CPUP / 100 * c.CapMHz
	demandMem := float64(c.RecMemMB) * (1 << 20) * 1.05
	c.NeedMHz = demandMHz / p.HostCPU
	c.NeedMemB = demandMem / p.HostMem
	if c.Cores > 0 {
		c.NeedCores = int(math.Ceil(c.NeedMHz / (c.CapMHz / float64(c.Cores))))
	}
	c.HostsForCPU = int(math.Ceil(c.NeedMHz / perHostMHz))
	c.HostsForMem = int(math.Ceil(c.NeedMemB / perHostMem))
	c.HostsNeeded = max(1, c.HostsForCPU, c.HostsForMem)
	if !strings.HasPrefix(c.Name, "standalone/") {
		c.HASpare = 1
		c.HostsNeeded = max(2, c.HostsNeeded+c.HASpare)
	}
}

func conf(hours, window float64) string {
	switch {
	case hours >= 0.8*min(window, 168) && hours >= 72:
		return "high"
	case hours >= 24:
		return "medium"
	}
	return "low"
}

// sevSaving ranks by absolute savings so that 2→1 vCPU never outranks 32→8.
func sevSaving(units int, ratio float64, high, medium int) Severity {
	switch {
	case units >= high || (units >= medium*2 && ratio >= 0.5):
		return High
	case units >= medium:
		return Medium
	}
	return Low
}

func sevBytes(b int64) Severity {
	switch {
	case b >= 200<<30:
		return High
	case b >= 20<<30:
		return Medium
	}
	return Low
}

func oldestName(l []vc.Snapshot) string {
	o := l[0]
	for _, s := range l {
		if s.Created.Before(o.Created) {
			o = s
		}
	}
	return o.Name
}

func topModel(m map[string]int) string {
	best, n := "", 0
	for k, v := range m {
		if v > n || (v == n && k < best) {
			best, n = k, v
		}
	}
	return best
}

func roundGiB(mb float64) int {
	return int(math.Ceil(mb/1024)) * 1024
}

func gib(mb int) string {
	g := float64(mb) / 1024
	if g == math.Trunc(g) {
		return fmt.Sprintf("%.0f GB", g)
	}
	return fmt.Sprintf("%.1f GB", g)
}

func GiB(mb int) string { return gib(mb) }

func human(b int64) string { return Human(b) }

func Human(b int64) string {
	const u = 1024
	if b < u {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(u), 0
	for n := b / u; n >= u && exp < 4; n /= u {
		div *= u
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTP"[exp])
}
