package analysis

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

// Peak-aware sizing: VMs rarely all peak at the same moment, so a cluster
// needs less than the sum of its VMs' individual peaks.

type Pair struct {
	A, B string
	R    float64
}

type Group struct {
	VMs  []string
	Host string
	R    float64
}

type Peaks struct {
	SumPeakMHz      float64
	CombinedPeakMHz float64
	Diversity       float64
	NaiveHosts      int
	AwareHosts      int
	CoPeak          []Group
	Complementary   []Pair
	Heatmap         [7][24]float64
	HeatmapN        [7][24]int
}

const (
	minCorrSlots = 48
	minPeakMHz   = 1000
	maxCorrVMs   = 200
	coPeakR      = 0.8
	complementR  = -0.4
)

type vmLoad struct {
	vm   vc.VM
	s    *VMStats
	peak float64
}

func peaks(c *ClusterResult, vms []vc.VM, st *Store, p Profile) *Peaks {
	if st == nil || c.CapMHz == 0 || c.Hosts == 0 {
		return nil
	}
	// Both sides come from the same VMs' 30-minute demand, so the ratio
	// compares like with like.
	var loads []vmLoad
	var total []float64
	var seen []bool
	pk := &Peaks{}
	for _, vm := range vms {
		s := st.VMs[vm.Ref]
		if s == nil || s.Hours() < minHours || !vm.PowerOn || len(s.Demand) == 0 {
			continue
		}
		var vals []float64
		for i, d := range s.Demand {
			if s.Slots[i] == 0 {
				continue
			}
			vals = append(vals, float64(d))
			for len(total) <= i {
				total = append(total, 0)
				seen = append(seen, false)
			}
			total[i] += float64(d)
			seen[i] = true
		}
		peak := percentile(vals, p.Percentile)
		pk.SumPeakMHz += peak
		loads = append(loads, vmLoad{vm, s, peak})
	}
	var sums []float64
	for i, v := range total {
		if seen[i] {
			sums = append(sums, v)
		}
	}
	pk.CombinedPeakMHz = percentile(sums, p.Percentile)
	if pk.CombinedPeakMHz <= 0 {
		return nil
	}
	pk.Diversity = pk.SumPeakMHz / pk.CombinedPeakMHz
	perHost := c.CapMHz / float64(c.Hosts)
	pk.AwareHosts = c.HostsForCPU
	pk.NaiveHosts = int(math.Ceil(c.NeedMHz * pk.Diversity / perHost))
	for _, pt := range c.Points {
		t := pt.T.Local()
		d, h := (int(t.Weekday())+6)%7, t.Hour()
		pk.Heatmap[d][h] += pt.CPUMHz / c.CapMHz * 100
		pk.HeatmapN[d][h]++
	}
	for d := range pk.Heatmap {
		for h := range pk.Heatmap[d] {
			if n := pk.HeatmapN[d][h]; n > 0 {
				pk.Heatmap[d][h] /= float64(n)
			}
		}
	}
	correlate(pk, loads)
	return pk
}

func percentile(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	v = slices.Clone(v)
	slices.Sort(v)
	i := int(math.Ceil(float64(len(v))*p/100)) - 1
	return v[max(0, min(i, len(v)-1))]
}

func correlate(pk *Peaks, loads []vmLoad) {
	loads = slices.DeleteFunc(loads, func(l vmLoad) bool { return l.peak < minPeakMHz })
	slices.SortFunc(loads, func(a, b vmLoad) int { return int(b.peak - a.peak) })
	if len(loads) > maxCorrVMs {
		loads = loads[:maxCorrVMs]
	}
	n := len(loads)
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	strongest := make([]float64, n)
	var comps []Pair
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			r, ok := pearson(loads[i].s, loads[j].s)
			if !ok {
				continue
			}
			if r >= coPeakR && loads[i].vm.Host == loads[j].vm.Host {
				parent[find(i)] = find(j)
				strongest[i] = max(strongest[i], r)
				strongest[j] = max(strongest[j], r)
			}
			if r <= complementR {
				comps = append(comps, Pair{loads[i].vm.Name, loads[j].vm.Name, r})
			}
		}
	}
	groups := map[int]*Group{}
	for i := 0; i < n; i++ {
		root := find(i)
		if root == i && strongest[i] == 0 {
			continue
		}
		g := groups[root]
		if g == nil {
			g = &Group{Host: loads[i].vm.Host}
			groups[root] = g
		}
		g.VMs = append(g.VMs, loads[i].vm.Name)
		g.R = max(g.R, strongest[i])
	}
	for _, g := range groups {
		if len(g.VMs) > 1 {
			slices.Sort(g.VMs)
			pk.CoPeak = append(pk.CoPeak, *g)
		}
	}
	slices.SortFunc(pk.CoPeak, func(a, b Group) int { return len(b.VMs) - len(a.VMs) })
	slices.SortFunc(comps, func(a, b Pair) int { return int((a.R - b.R) * 1000) })
	if len(comps) > 5 {
		comps = comps[:5]
	}
	if len(pk.CoPeak) > 5 {
		pk.CoPeak = pk.CoPeak[:5]
	}
	pk.Complementary = comps
}

// pearson correlates two demand series over the slots both have data for.
func pearson(a, b *VMStats) (float64, bool) {
	var n, sx, sy, sxx, syy, sxy float64
	for i := 0; i < min(len(a.Demand), len(b.Demand)); i++ {
		if a.Slots[i] == 0 || b.Slots[i] == 0 {
			continue
		}
		x, y := float64(a.Demand[i]), float64(b.Demand[i])
		n++
		sx += x
		sy += y
		sxx += x * x
		syy += y * y
		sxy += x * y
	}
	if n < minCorrSlots {
		return 0, false
	}
	cov := sxy - sx*sy/n
	vx, vy := sxx-sx*sx/n, syy-sy*sy/n
	if vx <= 0 || vy <= 0 {
		return 0, false
	}
	return cov / math.Sqrt(vx*vy), true
}

func coPeakFindings(c ClusterResult) []Finding {
	if c.Peaks == nil {
		return nil
	}
	var fs []Finding
	for _, g := range c.Peaks.CoPeak {
		name := g.VMs[0]
		if len(g.VMs) > 1 {
			name = fmt.Sprintf("%s + %d more", g.VMs[0], len(g.VMs)-1)
		}
		fs = append(fs, Finding{
			VM: name, Cluster: c.Name, Kind: CoPeak, Severity: Medium,
			Current:   fmt.Sprintf("%d VMs peak together on %s", len(g.VMs), g.Host),
			Suggested: "Spread with a DRS anti-affinity rule",
			Detail: fmt.Sprintf("%s rise and fall together (correlation %.2f) and run on the same host, so their peaks stack. "+
				"A VM-VM anti-affinity rule lets DRS place them on different hosts.", strings.Join(g.VMs, ", "), g.R),
			Confidence: "medium",
		})
	}
	return fs
}

func hourName(d, h int) string {
	return fmt.Sprintf("%s %02d:00", time.Weekday((d + 1) % 7).String()[:3], h)
}
