package analysis

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

// Cores per socket start at 16: vSphere is licensed per core with at least
// 16 counted per CPU, so smaller parts pay for cores they do not have.
var (
	socketCores = []int{16, 24, 32, 48, 64, 96, 128}
	nodeMemGB   = []int{256, 384, 512, 768, 1024, 1536, 2048, 3072, 4096}
)

type NodeOption struct {
	Nodes          int
	Sockets        int
	CoresPerSocket int
	MemGB          int
	TotalCores     int
	// CPUUtil and MemUtil are the planned load, growth included, on the
	// nodes left after losing the HA spares.
	CPUUtil float64
	MemUtil float64
	Ratio   float64
	// MinGHz is the lowest core clock, at today's performance per clock,
	// that keeps CPU at the target with the spares out.
	MinGHz  float64
	NUMAFit bool
}

func (o NodeOption) String() string {
	return fmt.Sprintf("%d × %s", o.Nodes, o.Spec())
}

func (o NodeOption) Spec() string {
	cpu := fmt.Sprintf("%d-core CPU", o.CoresPerSocket)
	if o.Sockets > 1 {
		cpu = fmt.Sprintf("%d × %d-core CPU", o.Sockets, o.CoresPerSocket)
	}
	return fmt.Sprintf("%s, %d GB", cpu, o.MemGB)
}

func (c *ClusterSizing) spares(p SizingParams) int {
	if c.Standalone {
		return 0
	}
	return p.Spares
}

func nodeOptions(c *ClusterSizing, n Need, p SizingParams) []NodeOption {
	if n.Cores == 0 && n.MemB == 0 {
		return nil
	}
	spares := c.spares(p)
	minNodes := max(2, spares+1)
	if c.Standalone {
		minNodes = 1
	}
	if c.VSAN {
		minNodes = max(minNodes, 3)
	}
	var out []NodeOption
	for _, cps := range socketCores {
		perNode := p.Sockets * cps
		if perNode < c.LargestCPU.VCPU {
			continue
		}
		nodes, mem := max(ceilDiv(n.Cores, perNode)+spares, minNodes), 0
		for ; nodes <= 64 && mem == 0; nodes++ {
			perGB := max(n.MemB/float64(nodes-spares)/(1<<30), float64(c.LargestMem.MemMB)/1024*memOverhead)
			for _, m := range nodeMemGB {
				if float64(m) >= perGB {
					mem = m
					break
				}
			}
		}
		if mem == 0 {
			continue
		}
		nodes--
		run := nodes - spares
		o := NodeOption{Nodes: nodes, Sockets: p.Sockets, CoresPerSocket: cps, MemGB: mem, TotalCores: nodes * perNode}
		if capMHz := float64(run*perNode) * c.CoreMHz * (1 + p.Uplift/100); capMHz > 0 {
			o.CPUUtil = n.DemandMHz / capMHz * 100
		}
		o.MemUtil = float64(n.MemMB) * (1 << 20) * memOverhead / (float64(run*mem) * (1 << 30)) * 100
		o.Ratio = float64(n.VCPU) / float64(o.TotalCores)
		if n.DemandMHz > 0 {
			o.MinGHz = n.DemandMHz / (p.CPUTarget / 100 * float64(run*perNode) * (1 + p.Uplift/100)) / 1000
		}
		o.NUMAFit = cps >= c.LargestCPU.VCPU && mem*1024/p.Sockets >= c.LargestMem.MemMB
		out = append(out, o)
	}
	return out
}

// pickOption recommends, among the options whose licensed cores are within
// 10% of the lowest, one that fits the largest VM in a NUMA node, then the
// fewest servers.
func pickOption(opts []NodeOption) int {
	if len(opts) == 0 {
		return -1
	}
	least := opts[0].TotalCores
	for _, o := range opts {
		least = min(least, o.TotalCores)
	}
	best := -1
	for i, o := range opts {
		if float64(o.TotalCores) > float64(least)*1.1 {
			continue
		}
		if best < 0 || better(o, opts[best]) {
			best = i
		}
	}
	return best
}

func better(a, b NodeOption) bool {
	switch {
	case a.NUMAFit != b.NUMAFit:
		return a.NUMAFit
	case a.Nodes != b.Nodes:
		return a.Nodes < b.Nodes
	}
	return a.TotalCores < b.TotalCores
}

// PortPlan is the connectivity each new node needs.
type PortPlan struct {
	DataPorts    int
	DataGb       int
	StoragePorts int
	StorageKind  string
	StorageGb    int
	OOBPorts     int
	Jumbo        bool
}

func (pp PortPlan) StorageLabel() string {
	if pp.StorageKind == "FC" {
		return fmt.Sprintf("%dG FC", pp.StorageGb)
	}
	return fmt.Sprintf("%d GbE %s", pp.StorageGb, pp.StorageKind)
}

func (pp PortPlan) String() string {
	parts := []string{fmt.Sprintf("%d × %d GbE", pp.DataPorts, pp.DataGb)}
	if pp.StoragePorts > 0 {
		s := fmt.Sprintf("%d × %s", pp.StoragePorts, pp.StorageLabel())
		if pp.Jumbo && pp.StorageKind != "FC" {
			s += " (MTU 9000)"
		}
		parts = append(parts, s)
	}
	if pp.OOBPorts > 0 {
		parts = append(parts, fmt.Sprintf("%d × 1 GbE out-of-band", pp.OOBPorts))
	}
	return strings.Join(parts, " + ")
}

// portPlan starts from redundant 25 GbE and 32G FC, and moves up a speed when
// today's adapters are faster or the peak load per node would fill more than
// 40-50% of the pair.
func portPlan(c *ClusterSizing, o NodeOption, p SizingParams) PortPlan {
	run := float64(max(o.Nodes-c.spares(p), 1))
	pp := PortPlan{DataPorts: 2, DataGb: 25, OOBPorts: 1}
	netGb := c.NetPeakKBps * 8 / 1e6 / run
	if c.Links.FastestNICMb >= 100000 || netGb > 0.4*2*25 {
		pp.DataGb = 100
	}
	storGb := c.KBpsPeak * 8 / 1e6 / run
	proto := ""
	if len(c.Protocols) > 0 {
		proto = c.Protocols[0]
	}
	switch proto {
	case "FC", "NVMe/FC", "FCoE":
		pp.StorageKind, pp.StoragePorts, pp.StorageGb = "FC", 2, 32
		if c.Links.FastestFCGb > 32 || storGb > 0.5*2*32 {
			pp.StorageGb = 64
		}
	case "iSCSI", "NFS", "NVMe/TCP", "NVMe/RDMA", "vSAN":
		pp.StorageKind, pp.StoragePorts, pp.StorageGb = proto, 2, pp.DataGb
		if storGb > 0.4*2*float64(pp.StorageGb) {
			pp.StorageGb = 100
		}
		pp.Jumbo = proto == "vSAN" || strings.Contains(c.Links.StorageMTU, "9000")
	}
	return pp
}

func protocolOf(d vc.DatastoreDetail) string {
	switch d.Type {
	case "NFS", "NFS41":
		return "NFS"
	case "vsan":
		return "vSAN"
	case "VVOL":
		return "vVols"
	case "VMFS":
		if d.Local {
			return "Local"
		}
		for _, l := range d.LUNs {
			switch {
			case l.Transport == "":
			case strings.HasPrefix(l.Transport, "iSCSI"):
				return "iSCSI"
			case slices.Contains([]string{"SAS", "SCSI", "Block", "NVMe (local)"}, l.Transport):
				return "Local"
			default:
				return l.Transport
			}
		}
		return "VMFS"
	}
	return d.Type
}

// protocols lists the storage protocols a cluster's hosts use, by used
// capacity, largest first. Datastores whose transport is unknown fall back to
// the host bus adapters present.
func protocols(e *vc.Estate, cluster string) []string {
	hosts := map[string]bool{}
	var fc, iscsi bool
	for _, h := range e.Hosts {
		if h.Cluster != cluster {
			continue
		}
		hosts[h.Name] = true
		for _, a := range h.HBAs {
			switch {
			case strings.Contains(a.Type, "FC") && a.SpeedGb > 0:
				fc = true
			case strings.HasPrefix(a.Type, "iSCSI"):
				iscsi = true
			}
		}
	}
	used := map[string]int64{}
	for _, d := range e.Datastores {
		if !slices.ContainsFunc(d.Hosts, func(h string) bool { return hosts[h] }) {
			continue
		}
		pr := protocolOf(d)
		if pr == "VMFS" || pr == "vVols" {
			switch {
			case fc:
				pr = "FC"
			case iscsi:
				pr = "iSCSI"
			}
		}
		if pr == "Local" || pr == "VMFS" {
			continue
		}
		used[pr] += max(d.Capacity-d.Free, 1)
	}
	out := make([]string, 0, len(used))
	for k := range used {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if used[out[i]] != used[out[j]] {
			return used[out[i]] > used[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

func connectivity(hosts []vc.HostDetail, cluster string) Connectivity {
	var c Connectivity
	nics := map[int]int{}
	hbas := map[string]int{}
	mtus := map[int]bool{}
	gpus := map[string]int{}
	for _, h := range hosts {
		if h.Cluster != cluster {
			continue
		}
		for _, n := range h.NICs {
			if n.SpeedMb == 0 {
				c.NICsDown++
				continue
			}
			nics[n.SpeedMb]++
			c.FastestNICMb = max(c.FastestNICMb, n.SpeedMb)
		}
		for _, a := range h.HBAs {
			switch {
			case strings.Contains(a.Type, "FC"):
				if a.SpeedGb > 0 {
					hbas[fmt.Sprintf("%s %sG", a.Type, trimFloat(a.SpeedGb))]++
					c.FastestFCGb = max(c.FastestFCGb, a.SpeedGb)
				} else {
					hbas[a.Type+" (link down)"]++
				}
			case strings.HasPrefix(a.Type, "iSCSI"), strings.HasPrefix(a.Type, "NVMe/"):
				hbas[a.Type]++
			}
		}
		for _, k := range h.VMKs {
			if k.Storage {
				mtus[k.MTU] = true
			}
		}
		for _, g := range h.GPUs {
			gpus[g.Name]++
		}
	}
	c.NICs = countList(nics, func(mb int) string { return speedLabel(mb) })
	c.HBAs = countList(hbas, func(s string) string { return s })
	c.GPUs = countList(gpus, func(s string) string { return s })
	var ms []int
	for m := range mtus {
		ms = append(ms, m)
	}
	slices.Sort(ms)
	switch len(ms) {
	case 0:
	case 1:
		c.StorageMTU = fmt.Sprint(ms[0])
	default:
		parts := make([]string, len(ms))
		for i, m := range ms {
			parts[i] = fmt.Sprint(m)
		}
		c.StorageMTU = strings.Join(parts, "/") + " (mixed)"
	}
	return c
}

func speedLabel(mb int) string {
	if mb >= 1000 {
		return trimFloat(float64(mb)/1000) + " GbE"
	}
	return fmt.Sprintf("%d MbE", mb)
}

func trimFloat(f float64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.1f", f), "0"), ".")
}

// countList renders counts as "4 × 10 GbE, 2 × 1 GbE", largest key first.
func countList[K int | string](m map[K]int, label func(K) string) string {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b K) int {
		switch {
		case a > b:
			return -1
		case a < b:
			return 1
		}
		return 0
	})
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%d × %s", m[k], label(k))
	}
	return strings.Join(parts, ", ")
}
