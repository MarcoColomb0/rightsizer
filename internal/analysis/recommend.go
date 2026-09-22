package analysis

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/marcocolombo/rightsizer/internal/vc"
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
	Hours      float64
	Confidence string
}

type ClusterResult struct {
	Name        string
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
}

const minHours = 1.0

func Analyze(inv *vc.Inventory, st *Store, p Profile, start, end time.Time, planned time.Duration) *Result {
	r := &Result{Generated: time.Now(), Start: start, End: end, Planned: planned, Profile: p}
	if inv == nil {
		return r
	}
	window := hoursBetween(start, end)
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
	for _, h := range inv.Hosts {
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
	for _, vm := range inv.VMs {
		t.VMs++
		if vm.Template {
			t.Templates++
			continue
		}
		t.Storage += vm.Committed
		r.Findings = append(r.Findings, storageFindings(vm, end)...)
		if !vm.PowerOn {
			t.Off++
			s := st.VMs[vm.Ref]
			if s == nil || s.Samples == 0 {
				r.Findings = append(r.Findings, Finding{
					VM: vm.Name, Cluster: vm.Cluster, Kind: PoweredOff, Severity: sevBytes(vm.Committed),
					Current:   fmt.Sprintf("off, %s on disk", human(vm.Committed)),
					Suggested: "Archive / delete",
					Detail:    fmt.Sprintf("Powered off for the whole analysis window (%d vCPU, %s configured). Still consumes datastore capacity and may count toward licensing. Confirm with the owner first.", vm.VCPU, gib(vm.MemMB)),
					Bytes:     vm.Committed, Confidence: conf(window, window),
				})
				continue
			}
		} else {
			t.On++
		}
		s := st.VMs[vm.Ref]
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
		cs := st.Clusters[name]
		c.CPUModel = topModel(models[name])
		sizeCluster(c, cs, p)
		r.Clusters = append(r.Clusters, *c)
		t.Hosts += c.Hosts
		t.HostsNeeded += c.HostsNeeded
		t.Cores += c.Cores
		t.NeedCores += c.NeedCores
	}
	for _, f := range r.Findings {
		if f.Kind == PoweredOff || f.Kind == OldSnap || f.Kind == ThickDisk {
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

func rightsize(vm vc.VM, s *VMStats, p Profile, window float64) (VMResult, []Finding) {
	h := s.Hours()
	cf := conf(h, window)
	vr := VMResult{
		Name: vm.Name, Cluster: vm.Cluster, PowerOn: vm.PowerOn, VCPU: vm.VCPU, MemMB: vm.MemMB,
		CPUP: s.CPU.Pct(p.Percentile), CPUMax: s.CPU.Max, MemP: s.Mem.Pct(p.Percentile),
		ReadyAvg: s.Ready.Avg(), Hours: h, Confidence: cf, RecVCPU: vm.VCPU, RecMemMB: vm.MemMB,
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
		c.Points = Downsample(cs.Points, 336)
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
