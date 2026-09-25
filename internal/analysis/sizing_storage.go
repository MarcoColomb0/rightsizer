package analysis

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

// StorageSizing separates what the VMs store from what the datastores
// report, so the new storage can be sized from the logical data before any
// data reduction.
type StorageSizing struct {
	Capacity   int64
	Used       int64
	Free       int64
	Datastores int
	ByType     []TypeUsage

	VMDisks     int64
	Snapshots   int64
	Swap        int64
	Other       int64
	Templates   int64
	RDM         int64
	RDMs        int
	PhysicalRDM int
	PoweredOff  int64
	Provisioned int64
	GuestUsed   int64
	// GuestCoverage is the share of VM data whose guest file systems were
	// reported by VMware Tools.
	GuestCoverage float64
	Orphans       int64
	// RawUsed is the data to move: VM disks, snapshots, other VM files,
	// templates and raw device mappings. Plan adds growth and free space.
	RawUsed int64
	Plan    int64
	IO      IOSummary
}

type TypeUsage struct {
	Type       string
	Protocol   string
	Datastores int
	Capacity   int64
	Used       int64
}

type IOSummary struct {
	Available bool
	Preview   bool
	Hours     float64
	IOPS      float64
	IOPSPeak  float64
	IOPSAvg   float64
	ReadPct   float64
	MBps      float64
	MBpsPeak  float64
	IOSizeKB  float64
	LatencyMs float64
	Points    []IOPoint
}

type DatastoreSizing struct {
	vc.DatastoreDetail
	Protocol    string
	Used        int64
	Provisioned int64
	IO          IOSummary
}

type WorkloadSizing struct {
	Name        string
	VMs         int
	On          int
	VCPU        int
	MemMB       int
	RecVCPU     int
	RecMemMB    int
	Used        int64
	Provisioned int64
	GuestUsed   int64
	IOPS        float64
}

type VMSizing struct {
	Name          string
	Cluster       string
	Host          string
	Workload      string
	GuestOS       string
	PowerOn       bool
	Template      bool
	VCPU          int
	MemMB         int
	RecVCPU       int
	RecMemMB      int
	CPUAvgMHz     float64
	CPUPeakMHz    float64
	CPUPct        float64
	MemPct        float64
	ConsumedMB    float64
	ReadIOPS      float64
	WriteIOPS     float64
	PeakIOPS      float64
	DiskBytes     int64
	SnapshotBytes int64
	SwapBytes     int64
	OtherBytes    int64
	RDMBytes      int64
	Used          int64
	Provisioned   int64
	GuestUsed     int64
	Datastores    string
	CPUReserveMHz int64
	MemReserveMB  int64
	Devices       string
}

func ioSummary(st *IOStats, pct float64) IOSummary {
	if st == nil || st.IOPS.N == 0 {
		return IOSummary{}
	}
	return IOSummary{
		Available: true,
		Hours:     float64(st.IOPS.N) * 20 / 3600,
		IOPS:      st.IOPS.Pct(pct),
		IOPSPeak:  st.IOPS.Max,
		IOPSAvg:   st.IOPS.Avg(),
		ReadPct:   st.ReadShare() * 100,
		MBps:      st.KBps.Pct(pct) / 1024,
		MBpsPeak:  st.KBps.Max / 1024,
		IOSizeKB:  st.IOSize(),
		LatencyMs: st.Latency.Pct(pct),
	}
}

// ioStore picks 20-second data once it covers a day, like the CPU figures.
func (in Input) ioStore() (*Store, bool) {
	if in.RT != nil {
		if t := in.RT.IO[""]; t != nil && t.IOPS.N >= fullDay {
			return in.RT, false
		}
	}
	if in.History != nil {
		if t := in.History.IO[""]; t != nil && t.IOPS.N > 0 {
			return in.History, true
		}
	}
	return in.RT, false
}

func sizeStorage(sz *Sizing, in SizingInput, est *vc.Estate, groups []group) {
	s := &sz.Storage
	pct := sz.Percentile
	ios, preview := in.ioStore()
	var io map[string]*IOStats
	if ios != nil {
		io = ios.IO
	}
	s.IO = ioSummary(io[""], pct)
	s.IO.Preview = preview && s.IO.Available
	if t := io[""]; t != nil {
		s.IO.Points = DownsampleIO(t.Points, 336)
	}

	byType := map[string]*TypeUsage{}
	for _, d := range est.Datastores {
		ds := DatastoreSizing{DatastoreDetail: d, Protocol: protocolOf(d), Used: d.Capacity - d.Free}
		ds.Provisioned = ds.Used + d.Uncommitted
		ds.IO = ioSummary(io[d.ID], pct)
		sz.Datastores = append(sz.Datastores, ds)
		if !d.Accessible {
			continue
		}
		s.Datastores++
		s.Capacity += d.Capacity
		s.Free += d.Free
		s.Used += ds.Used
		k := d.Version + "|" + ds.Protocol
		t := byType[k]
		if t == nil {
			t = &TypeUsage{Type: d.Version, Protocol: ds.Protocol}
			byType[k] = t
		}
		t.Datastores++
		t.Capacity += d.Capacity
		t.Used += ds.Used
	}
	for _, t := range byType {
		s.ByType = append(s.ByType, *t)
	}
	slices.SortFunc(s.ByType, func(a, b TypeUsage) int { return int((b.Used - a.Used) >> 20) })
	slices.SortFunc(sz.Datastores, func(a, b DatastoreSizing) int {
		if a.Used != b.Used {
			return int((b.Used - a.Used) >> 20)
		}
		return strings.Compare(a.Name, b.Name)
	})

	rec := map[string]VMResult{}
	if in.Result != nil {
		for _, v := range in.Result.VMs {
			rec[v.Ref] = v
		}
		for _, o := range in.Result.Orphans {
			s.Orphans += max(o.Size, 0)
		}
	}
	wl := map[string]*WorkloadSizing{}
	var guestBase, covered int64
	for _, vm := range in.Inv.VMs {
		disk, snap, swap, other := vmBytes(vm)
		rdm := rdmBytes(vm)
		used := disk + snap + other + rdm
		var guest int64
		for _, g := range vm.GuestDisks {
			guest += max(g.Capacity-g.Free, 0)
		}
		name := workloadOf(groups, vm)
		row := VMSizing{
			Name: vm.Name, Cluster: vm.Cluster, Host: vm.Host, Workload: name, GuestOS: vm.GuestOS,
			PowerOn: vm.PowerOn, Template: vm.Template, VCPU: vm.VCPU, MemMB: vm.MemMB, RecVCPU: vm.VCPU, RecMemMB: vm.MemMB,
			DiskBytes: disk, SnapshotBytes: snap, SwapBytes: swap, OtherBytes: other, RDMBytes: rdm, Used: used,
			Provisioned: vm.Committed + vm.Uncommitted, GuestUsed: guest,
			CPUReserveMHz: vm.CPUReserveMHz, MemReserveMB: vm.MemReserveMB, Datastores: vmDatastores(vm), Devices: vmDevices(vm),
		}
		if v, ok := rec[vm.Ref]; ok {
			row.RecVCPU, row.RecMemMB, row.CPUPct, row.MemPct = v.RecVCPU, v.RecMemMB, v.CPUP, v.MemP
		}
		if st, _ := in.vmStats(vm.Ref); st != nil {
			row.CPUAvgMHz, row.CPUPeakMHz, row.ConsumedMB = st.CPUMHz.Avg(), st.CPUMHz.Max, st.Consumed.Avg()/1024
			row.ReadIOPS, row.WriteIOPS = st.ReadIOPS.Avg(), st.WriteIOPS.Avg()
			row.PeakIOPS = max(st.ReadIOPS.Max, st.WriteIOPS.Max)
		}
		sz.VMs = append(sz.VMs, row)

		w := wl[name]
		if w == nil {
			w = &WorkloadSizing{Name: name}
			wl[name] = w
		}
		w.VMs++
		w.Used += used
		w.Provisioned += row.Provisioned
		w.GuestUsed += guest
		w.IOPS += row.ReadIOPS + row.WriteIOPS
		if vm.Template {
			s.Templates += vm.Committed
			continue
		}
		if vm.PowerOn {
			w.On++
		}
		w.VCPU += vm.VCPU
		w.MemMB += vm.MemMB
		w.RecVCPU += row.RecVCPU
		w.RecMemMB += row.RecMemMB
		s.VMDisks += disk
		s.Snapshots += snap
		s.Swap += swap
		s.Other += other
		s.Provisioned += row.Provisioned
		s.GuestUsed += guest
		if rdm > 0 {
			s.RDM += rdm
			s.RDMs++
			for _, d := range vm.Disks {
				if d.RDM == "physical" {
					s.PhysicalRDM++
					break
				}
			}
		}
		if !vm.PowerOn {
			s.PoweredOff += used
		} else {
			guestBase += disk
			if len(vm.GuestDisks) > 0 {
				covered += disk
			}
		}
	}
	if guestBase > 0 {
		s.GuestCoverage = float64(covered) / float64(guestBase)
	}
	s.RawUsed = s.VMDisks + s.Snapshots + s.Other + s.Templates + s.RDM
	s.Plan = int64(float64(s.RawUsed) * (1 + sz.Params.Growth/100) / (1 - sz.Params.FreeSpace/100))
	for _, w := range wl {
		sz.Workloads = append(sz.Workloads, *w)
	}
	sort.Slice(sz.Workloads, func(i, j int) bool {
		a, b := sz.Workloads[i], sz.Workloads[j]
		if a.Used != b.Used {
			return a.Used > b.Used
		}
		return a.Name < b.Name
	})
	slices.SortFunc(sz.VMs, func(a, b VMSizing) int { return strings.Compare(a.Name, b.Name) })
}

func vmDatastores(vm vc.VM) string {
	var ds []string
	for _, d := range vm.Disks {
		if d.Datastore != "" && !slices.Contains(ds, d.Datastore) {
			ds = append(ds, d.Datastore)
		}
	}
	return strings.Join(ds, ", ")
}

func vmDevices(vm vc.VM) string {
	var out []string
	for _, g := range vm.VGPU {
		out = append(out, "vGPU "+g)
	}
	if vm.Passthrough > 0 {
		out = append(out, fmt.Sprintf("%d PCI passthrough", vm.Passthrough))
	}
	for _, d := range vm.Disks {
		if d.RDM != "" {
			out = append(out, fmt.Sprintf("%s RDM %s", d.RDM, Human(d.Capacity)))
		}
	}
	return strings.Join(out, "; ")
}

// notes lists what the numbers alone do not say but the refresh must handle.
func notes(sz *Sizing, inv *vc.Inventory, est *vc.Estate) []string {
	var out []string
	add := func(f string, a ...any) { out = append(out, fmt.Sprintf(f, a...)) }
	for _, c := range sz.Clusters {
		for _, n := range c.Needs {
			if n.Basis == sz.Params.Basis && n.Unsized() {
				add("%s: no node shape fits (up to %d × 128 cores and 4 TB per node, 64 nodes): its largest VMs have %d vCPU and %s of memory, and it needs %d cores and %s of RAM. It is left out of the node totals; size it separately.",
					c.Name, sz.Params.Sockets, c.LargestCPU.VCPU, gib(c.LargestMem.MemMB), n.Cores, Human(int64(n.MemB)))
			}
		}
	}

	vendors := map[string]int{}
	for _, h := range inv.Hosts {
		m := strings.ToLower(h.CPUModel)
		switch {
		case strings.Contains(m, "intel"):
			vendors["Intel"]++
		case strings.Contains(m, "amd"):
			vendors["AMD"]++
		}
	}
	evc := map[string]bool{}
	for _, c := range est.Clusters {
		if c.EVC != "" {
			evc[c.EVC] = true
		}
	}
	if len(vendors) > 0 {
		var vs []string
		for v := range vendors {
			vs = append(vs, v)
		}
		slices.Sort(vs)
		msg := fmt.Sprintf("Hosts run %s CPUs.", strings.Join(vs, " and "))
		if len(evc) > 0 {
			var modes []string
			for m := range evc {
				modes = append(modes, m)
			}
			slices.Sort(modes)
			msg += fmt.Sprintf(" EVC mode: %s.", strings.Join(modes, ", "))
		}
		msg += " VMs move live with vMotion to new hosts of the same CPU vendor; moving between Intel and AMD needs each VM powered off during its migration."
		add("%s", msg)
	}

	var big VMShape
	var bigMem VMShape
	for _, c := range sz.Clusters {
		if c.LargestCPU.VCPU > big.VCPU {
			big = c.LargestCPU
		}
		if c.LargestMem.MemMB > bigMem.MemMB {
			bigMem = c.LargestMem
		}
	}
	if big.VCPU > 0 {
		add("Largest VMs: %s with %d vCPU and %s with %s. Every new node needs at least %d cores and %s of RAM; one socket with that many cores and memory keeps them in a single NUMA node.",
			big.Name, big.VCPU, bigMem.Name, gib(bigMem.MemMB), big.VCPU, gib(int(float64(bigMem.MemMB)*memOverhead)))
	}

	vgpu := map[string]int{}
	var pass []string
	var resMHz, resMB int64
	for _, vm := range inv.VMs {
		for _, g := range vm.VGPU {
			vgpu[g]++
		}
		if vm.Passthrough > 0 {
			pass = append(pass, vm.Name)
		}
		if !vm.Template && vm.PowerOn {
			resMHz += vm.CPUReserveMHz
			resMB += vm.MemReserveMB
		}
	}
	if len(vgpu) > 0 {
		add("VMs use vGPU profiles (%s): the new nodes need compatible GPUs and drivers.", countList(vgpu, func(s string) string { return s }))
	}
	if len(pass) > 0 {
		add("%d VM(s) use PCI passthrough devices (%s). They cannot be moved with vMotion and need the same devices in the new nodes.", len(pass), clip(pass, 5))
	}
	if resMHz > 0 || resMB > 0 {
		add("Powered-on VMs reserve %.1f GHz of CPU and %s of memory. Reservations must fit with the HA spares out.", float64(resMHz)/1000, gib(int(resMB)))
	}
	st := sz.Storage
	if st.RDMs > 0 {
		add("%d VM(s) use raw device mappings (%s, %d with physical compatibility). That data is on LUNs outside the datastores: present them to the new hosts or migrate them. It is included in raw used capacity.", st.RDMs, Human(st.RDM), st.PhysicalRDM)
	}
	if st.Snapshots > 10<<30 {
		add("Snapshots hold %s. Consolidate them before migrating; they are included in raw used capacity.", Human(st.Snapshots))
	}
	if st.PoweredOff > 0 {
		add("Powered-off VMs store %s. Archive or delete what is no longer needed before migrating.", Human(st.PoweredOff))
	}
	if st.Orphans > 0 {
		add("Orphaned virtual disks use %s. They are not included in raw used capacity; verify and delete them.", Human(st.Orphans))
	}
	if st.Swap > 0 {
		add("VM swap files use %s. They are not included in raw used capacity: the new hosts create them when VMs power on, sized to configured memory minus reservations.", Human(st.Swap))
	}
	var local int64
	var localN int
	for _, d := range est.Datastores {
		if d.Local && d.Accessible {
			local += d.Capacity - d.Free
			localN++
		}
	}
	if localN > 0 {
		add("%d local datastore(s) hold %s on the servers' own disks; it leaves with the old servers unless migrated.", localN, Human(local))
	}
	if st.Capacity > 0 && st.Provisioned > st.Capacity {
		add("VMs are provisioned %.1f× the datastore capacity (thin provisioning). Keep monitoring free space on the new storage.", float64(st.Provisioned)/float64(st.Capacity))
	}
	mixed, fc8 := false, 0
	for _, c := range sz.Clusters {
		if strings.Contains(c.Links.StorageMTU, "mixed") {
			mixed = true
		}
	}
	for _, h := range est.Hosts {
		for _, a := range h.HBAs {
			if strings.Contains(a.Type, "FC") && a.SpeedGb > 0 && a.SpeedGb <= 8 {
				fc8++
			}
		}
	}
	if mixed {
		add("Storage VMkernel ports use different MTUs on some hosts. Use one MTU end to end on the new design.")
	}
	if fc8 > 0 {
		add("%d FC port(s) run at 8G or slower. 32G adapters still link at 16G and 8G, 64G adapters only down to 16G: check which fabrics must stay connected during the migration.", fc8)
	}
	versions := map[string]int{}
	for _, h := range est.Hosts {
		if h.ESXi != "" {
			versions[h.ESXi]++
		}
	}
	if len(versions) > 1 {
		add("Hosts run %d different ESXi builds. Check the compatibility guide for the new servers, adapters and the ESXi version you will deploy.", len(versions))
	}
	if !st.IO.Available {
		add("No storage performance data yet: datastore IOPS, throughput and latency are read every 5 minutes from the first sample on. vCenter keeps them in its history only at a higher statistics level.")
	}
	return out
}

func clip(names []string, n int) string {
	if len(names) <= n {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:n], ", ") + fmt.Sprintf(" and %d more", len(names)-n)
}
