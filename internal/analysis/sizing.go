package analysis

import (
	"fmt"
	"math"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

const (
	BasisProvisioned = "provisioned"
	BasisRightsized  = "rightsized"
)

// SizingParams describe the hardware refresh being planned. They are kept
// across runs and upgrades.
type SizingParams struct {
	// Basis sizes compute for the VMs as configured today, or as rightsized.
	Basis string
	// PoweredOff counts powered-off VMs in compute; their storage always counts.
	PoweredOff bool
	Growth     float64
	// Ratio is the vCPU per physical core to plan for; 0 keeps today's ratio,
	// or 4:1 when today's is lower.
	Ratio     float64
	CPUTarget float64
	MemTarget float64
	Spares    int
	Sockets   int
	// Uplift is how much faster per core the new CPUs are, in percent.
	Uplift    float64
	FreeSpace float64
	// Groups names workloads by VM name pattern: "db=sql*,*ora*; vdi=vdi-*".
	Groups string
}

func DefaultSizing() SizingParams {
	return SizingParams{Basis: BasisProvisioned, Growth: 20, CPUTarget: 70, MemTarget: 90, Spares: 1, Sockets: 2, FreeSpace: 20}
}

// ParamError names the sizing option that is invalid.
type ParamError struct {
	Field string
	Msg   string
}

func (e *ParamError) Error() string { return e.Msg }

func (p SizingParams) Validate() error {
	bad := func(field, msg string) error { return &ParamError{field, msg} }
	switch {
	case p.Basis != BasisProvisioned && p.Basis != BasisRightsized:
		return bad("Basis", "basis must be provisioned or rightsized")
	case p.Growth < 0 || p.Growth > 300:
		return bad("Growth", "growth must be between 0 and 300%")
	case p.Ratio != 0 && (p.Ratio < 1 || p.Ratio > 32):
		return bad("Ratio", "vCPU per core must be 0 (automatic) or between 1 and 32")
	case p.CPUTarget < 20 || p.CPUTarget > 100:
		return bad("CPUTarget", "CPU target must be between 20 and 100%")
	case p.MemTarget < 50 || p.MemTarget > 100:
		return bad("MemTarget", "memory target must be between 50 and 100%")
	case p.Spares < 0 || p.Spares > 4:
		return bad("Spares", "HA spares must be between 0 and 4")
	case p.Sockets < 1 || p.Sockets > 2:
		return bad("Sockets", "sockets per node must be 1 or 2")
	case p.Uplift < 0 || p.Uplift > 200:
		return bad("Uplift", "per-core uplift must be between 0 and 200%")
	case p.FreeSpace < 0 || p.FreeSpace > 60:
		return bad("FreeSpace", "free space must be between 0 and 60%")
	case len(p.Groups) > 1000:
		return bad("Groups", "workload groups too long")
	}
	if _, err := parseGroups(p.Groups); err != nil {
		return bad("Groups", err.Error())
	}
	return nil
}

// BasisLabel describes a sizing basis in reports.
func BasisLabel(b string) string {
	if b == BasisRightsized {
		return "rightsized"
	}
	return "as provisioned"
}

// OtherBasis is the basis compared with b.
func OtherBasis(b string) string {
	if b == BasisRightsized {
		return BasisProvisioned
	}
	return BasisRightsized
}

// GBLabel shows node memory in GB, or TB from 1024 GB.
func GBLabel(gb int) string {
	if gb >= 1024 {
		return fmt.Sprintf("%.1f TB", float64(gb)/1024)
	}
	return fmt.Sprintf("%d GB", gb)
}

// Unsized reports that a basis needs capacity but no node option fits.
func (n Need) Unsized() bool {
	return n.Pick < 0 && (n.Cores > 0 || n.MemB > 0)
}

type group struct {
	name     string
	patterns []string
}

func parseGroups(s string) ([]group, error) {
	var out []group
	for _, part := range strings.Split(s, ";") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		name, pats, ok := strings.Cut(part, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" || len(name) > 40 {
			return nil, fmt.Errorf("workload group %q: use name=pattern,pattern", part)
		}
		g := group{name: name}
		for _, p := range strings.Split(pats, ",") {
			if p = strings.ToLower(strings.TrimSpace(p)); p == "" {
				continue
			}
			if _, err := path.Match(p, ""); err != nil {
				return nil, fmt.Errorf("workload group %q: invalid pattern %q", name, p)
			}
			g.patterns = append(g.patterns, p)
		}
		if len(g.patterns) == 0 {
			return nil, fmt.Errorf("workload group %q has no pattern", name)
		}
		out = append(out, g)
	}
	return out, nil
}

func workloadOf(groups []group, vm vc.VM) string {
	if vm.Template {
		return "Templates"
	}
	n := strings.ToLower(vm.Name)
	for _, g := range groups {
		for _, p := range g.patterns {
			if ok, _ := path.Match(p, n); ok {
				return g.name
			}
		}
	}
	return osFamily(vm)
}

func osFamily(vm vc.VM) string {
	s := strings.ToLower(vm.GuestOS + " " + vm.GuestID)
	switch {
	case strings.Contains(s, "windows") && (strings.Contains(s, "server") || strings.Contains(s, "srv")):
		return "Windows Server"
	case strings.Contains(s, "windows"):
		return "Windows desktop"
	}
	for _, k := range []string{"linux", "ubuntu", "red hat", "rhel", "centos", "suse", "sles", "debian", "photon", "rocky", "alma", "fedora", "coreos", "flatcar"} {
		if strings.Contains(s, k) {
			return "Linux"
		}
	}
	return "Other"
}

// Sizing is everything needed to specify replacement hardware.
type Sizing struct {
	Generated     time.Time
	VCenter       string
	Params        SizingParams
	Percentile    float64
	Start         time.Time
	End           time.Time
	Final         bool
	Preview       bool
	EstateTaken   time.Time
	Clusters      []ClusterSizing
	Totals        SizingTotals
	Storage       StorageSizing
	Workloads     []WorkloadSizing
	Datastores    []DatastoreSizing
	Hosts         []vc.HostDetail
	ClusterConfig []vc.ClusterDetail
	Notes         []string
	VMs           []VMSizing
}

type VMShape struct {
	Name  string
	VCPU  int
	MemMB int
}

type ClusterSizing struct {
	Name       string
	Standalone bool
	VSAN       bool
	Hosts      int
	CPUModel   string
	Sockets    int
	Cores      int
	Threads    int
	CoreMHz    float64
	MemB       int64
	VMs        int
	VMsOff     int
	VCPU       int
	VCPUOff    int
	MemMB      int
	MemMBOff   int
	RecVCPU    int
	RecMemMB   int
	// Ratio is today's provisioned vCPU per physical core, powered-on VMs.
	Ratio       float64
	DemandMHz   float64
	PeakMHz     float64
	ConsumedB   float64
	ReserveMHz  int64
	ReserveMB   int64
	LargestCPU  VMShape
	LargestMem  VMShape
	NetKBps     float64
	NetPeakKBps float64
	IOPS        float64
	IOPSPeak    float64
	KBps        float64
	KBpsPeak    float64
	Used        int64
	Protocols   []string
	Links       Connectivity
	Needs       []Need
}

// Connectivity summarises today's adapters in a cluster.
type Connectivity struct {
	NICs         string
	NICsDown     int
	HBAs         string
	FastestNICMb int
	FastestFCGb  float64
	StorageMTU   string
	GPUs         string
}

// Need is what one sizing basis requires, growth included.
type Need struct {
	Basis         string
	VCPU          int
	MemMB         int
	DemandMHz     float64
	Ratio         float64
	CoresByRatio  int
	CoresByDemand int
	Cores         int
	MemB          float64
	Options       []NodeOption
	Pick          int
	Ports         PortPlan
}

func (n Need) Picked() (NodeOption, bool) {
	if n.Pick < 0 || n.Pick >= len(n.Options) {
		return NodeOption{}, false
	}
	return n.Options[n.Pick], true
}

type SizingTotals struct {
	Hosts   int
	Sockets int
	Cores   int
	Threads int
	MemB    int64
	VMs     int
	VMsOff  int
	VCPU    int
	MemMB   int
	New     []NewTotals
}

// NewTotals adds up the recommended nodes of every cluster for one basis.
type NewTotals struct {
	Basis        string
	Nodes        int
	Cores        int
	MemGB        int
	DataPorts    int
	StoragePorts map[string]int
	OOBPorts     int
}

func (t SizingTotals) For(basis string) NewTotals {
	for _, n := range t.New {
		if n.Basis == basis {
			return n
		}
	}
	return NewTotals{Basis: basis}
}

// memOverhead is the hypervisor memory used per VM on top of its vRAM.
const memOverhead = 1.05

// SizingInput joins an analysis with the estate and the refresh parameters.
type SizingInput struct {
	Input
	Result *Result
	Estate *vc.Estate
	Params SizingParams
}

func Size(in SizingInput) *Sizing {
	r, p := in.Result, in.Params
	if p.Sockets == 0 {
		p = DefaultSizing()
	}
	sz := &Sizing{Generated: time.Now(), Params: p, Start: in.Start, End: in.End}
	if r != nil {
		sz.VCenter, sz.Percentile, sz.Final, sz.Preview = r.VCenter, r.Profile.Percentile, r.Final, r.Preview
	}
	if sz.Percentile == 0 {
		sz.Percentile = in.Profile.Percentile
	}
	if in.Inv == nil {
		return sz
	}
	est := in.Estate
	if est == nil {
		est = &vc.Estate{}
	}
	sz.EstateTaken = est.Taken
	sz.Hosts, sz.ClusterConfig = est.Hosts, est.Clusters
	groups, _ := parseGroups(p.Groups)

	rec := map[string]VMResult{}
	byName := map[string]ClusterResult{}
	if r != nil {
		for _, v := range r.VMs {
			rec[v.Ref] = v
		}
		for _, c := range r.Clusters {
			byName[c.Name] = c
		}
	}
	cls := map[string]*ClusterSizing{}
	get := func(name string) *ClusterSizing {
		if name == "" {
			name = "(no host)"
		}
		c := cls[name]
		if c == nil {
			c = &ClusterSizing{Name: name, Standalone: strings.HasPrefix(name, "standalone/")}
			cls[name] = c
		}
		return c
	}
	models := map[string]map[string]int{}
	for _, h := range in.Inv.Hosts {
		if !h.Connected {
			continue
		}
		c := get(h.Cluster)
		c.Hosts++
		c.Sockets += h.Sockets
		c.Cores += h.Cores
		c.Threads += h.Threads
		c.MemB += h.MemBytes
		c.CoreMHz += float64(h.MHz * h.Cores)
		if models[h.Cluster] == nil {
			models[h.Cluster] = map[string]int{}
		}
		models[h.Cluster][h.CPUModel]++
	}
	for _, c := range cls {
		if c.Cores > 0 {
			c.CoreMHz /= float64(c.Cores)
		}
		c.CPUModel = topModel(models[c.Name])
	}
	for _, cd := range est.Clusters {
		if c := cls[cd.Name]; c != nil {
			c.VSAN = cd.VSAN
		}
	}

	for _, vm := range in.Inv.VMs {
		if vm.Template {
			continue
		}
		c := get(vm.Cluster)
		v, ok := rec[vm.Ref]
		recCPU, recMem := vm.VCPU, vm.MemMB
		if ok {
			recCPU, recMem = v.RecVCPU, v.RecMemMB
		}
		c.Used += vmUsed(vm)
		if !vm.PowerOn {
			c.VMsOff++
			c.VCPUOff += vm.VCPU
			c.MemMBOff += vm.MemMB
			if !p.PoweredOff {
				continue
			}
		} else {
			c.VMs++
			c.VCPU += vm.VCPU
			c.MemMB += vm.MemMB
		}
		c.RecVCPU += recCPU
		c.RecMemMB += recMem
		c.ReserveMHz += vm.CPUReserveMHz
		c.ReserveMB += vm.MemReserveMB
		if vm.VCPU > c.LargestCPU.VCPU {
			c.LargestCPU = VMShape{vm.Name, vm.VCPU, vm.MemMB}
		}
		if vm.MemMB > c.LargestMem.MemMB {
			c.LargestMem = VMShape{vm.Name, vm.VCPU, vm.MemMB}
		}
	}

	for name, c := range cls {
		if c.Cores > 0 {
			c.Ratio = float64(c.VCPU) / float64(c.Cores)
		}
		if cr, ok := byName[name]; ok && cr.CapMHz > 0 {
			c.DemandMHz = cr.CPUP / 100 * cr.CapMHz
			c.PeakMHz = cr.CPUPeak / 100 * cr.CapMHz
			c.ConsumedB = cr.MemP / 100 * cr.CapMemB
		}
		if st, preview := in.clusterStore(name); st != nil && st.Clusters[name] != nil {
			cs := st.Clusters[name]
			if cr, ok := byName[name]; ok && cs.Mem.N > 0 {
				c.ConsumedB = cs.Mem.Pct(sz.Percentile) / 100 * cr.CapMemB
			}
			c.NetKBps, c.NetPeakKBps = cs.Net.Pct(sz.Percentile), cs.Net.Max
			c.IOPS, c.IOPSPeak = cs.IOPS.Pct(sz.Percentile), cs.IOPS.Max
			c.KBps, c.KBpsPeak = cs.KBps.Pct(sz.Percentile), cs.KBps.Max
			sz.Preview = sz.Preview || preview
		}
		c.Links = connectivity(est.Hosts, name)
		c.Protocols = protocols(est, name)
		for _, basis := range []string{BasisProvisioned, BasisRightsized} {
			c.Needs = append(c.Needs, need(c, basis, p))
		}
	}

	names := make([]string, 0, len(cls))
	for n := range cls {
		names = append(names, n)
	}
	slices.Sort(names)
	t := &sz.Totals
	for _, n := range names {
		c := cls[n]
		if c.Hosts == 0 && c.VMs == 0 && c.VMsOff == 0 {
			continue
		}
		sz.Clusters = append(sz.Clusters, *c)
		t.Hosts += c.Hosts
		t.Sockets += c.Sockets
		t.Cores += c.Cores
		t.Threads += c.Threads
		t.MemB += c.MemB
		t.VMs += c.VMs
		t.VMsOff += c.VMsOff
		t.VCPU += c.VCPU
		t.MemMB += c.MemMB
	}
	t.New = newTotals(sz.Clusters)
	sizeStorage(sz, in, est, groups)
	sz.Notes = notes(sz, in.Inv, est)
	return sz
}

func newTotals(cs []ClusterSizing) []NewTotals {
	var out []NewTotals
	for i, basis := range []string{BasisProvisioned, BasisRightsized} {
		nt := NewTotals{Basis: basis, StoragePorts: map[string]int{}}
		for _, c := range cs {
			if i >= len(c.Needs) {
				continue
			}
			n := c.Needs[i]
			o, ok := n.Picked()
			if !ok {
				continue
			}
			nt.Nodes += o.Nodes
			nt.Cores += o.TotalCores
			nt.MemGB += o.Nodes * o.MemGB
			nt.DataPorts += o.Nodes * n.Ports.DataPorts
			nt.OOBPorts += o.Nodes * n.Ports.OOBPorts
			if n.Ports.StoragePorts > 0 {
				nt.StoragePorts[n.Ports.StorageLabel()] += o.Nodes * n.Ports.StoragePorts
			}
		}
		out = append(out, nt)
	}
	return out
}

// need computes cores and memory for one basis. Cores cover whichever is
// larger: the vCPU at the planned ratio, or the measured CPU demand at the
// target utilisation.
func need(c *ClusterSizing, basis string, p SizingParams) Need {
	g := 1 + p.Growth/100
	n := Need{Basis: basis, Pick: -1}
	vcpu, mem := c.VCPU, c.MemMB
	if p.PoweredOff {
		vcpu, mem = vcpu+c.VCPUOff, mem+c.MemMBOff
	}
	if basis == BasisRightsized {
		vcpu, mem = c.RecVCPU, c.RecMemMB
	}
	n.VCPU = int(math.Ceil(float64(vcpu) * g))
	n.MemMB = int(math.Ceil(float64(mem) * g))
	n.DemandMHz = c.DemandMHz * g
	n.Ratio = p.Ratio
	if n.Ratio == 0 {
		n.Ratio = max(math.Round(c.Ratio*10)/10, 4)
	}
	n.CoresByRatio = int(math.Ceil(float64(n.VCPU) / n.Ratio))
	if c.CoreMHz > 0 && n.DemandMHz > 0 {
		n.CoresByDemand = int(math.Ceil(n.DemandMHz / (p.CPUTarget / 100 * c.CoreMHz * (1 + p.Uplift/100))))
	}
	n.Cores = max(n.CoresByRatio, n.CoresByDemand)
	n.MemB = float64(n.MemMB) * (1 << 20) * memOverhead / (p.MemTarget / 100)
	n.Options = nodeOptions(c, n, p)
	n.Pick = pickOption(n.Options)
	if o, ok := n.Picked(); ok {
		n.Ports = portPlan(c, o, p)
	}
	return n
}

// vmUsed is the data a VM stores, snapshots and raw device mappings
// included, swap files excluded because the new hosts recreate them.
func vmUsed(vm vc.VM) int64 {
	disk, snap, _, other := vmBytes(vm)
	return disk + snap + other + rdmBytes(vm)
}

func vmBytes(vm vc.VM) (disk, snap, swap, other int64) {
	snap, swap = vm.SnapshotBytes, vm.SwapBytes
	disk = vm.DiskBytes
	if disk == 0 {
		disk = max(vm.Committed-snap-swap, 0)
	}
	other = max(vm.Committed-disk-snap-swap, 0)
	return disk, snap, swap, other
}

func rdmBytes(vm vc.VM) int64 {
	var n int64
	for _, d := range vm.Disks {
		if d.RDM != "" {
			n += d.Capacity
		}
	}
	return n
}

func ceilDiv(a, b int) int {
	if b <= 0 {
		return 0
	}
	return (a + b - 1) / b
}
