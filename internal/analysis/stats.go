package analysis

import (
	"math"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

const histBuckets = 200

// Hist is a fixed 0.5%-resolution histogram for percentage metrics. It keeps
// memory constant no matter how long the analysis runs.
type Hist struct {
	B   [histBuckets + 1]uint32
	N   uint64
	Max float64
	Sum float64
}

func (h *Hist) Add(v float64) {
	if v < 0 || math.IsNaN(v) {
		return
	}
	v = min(v, 100)
	h.B[int(v*2)]++
	h.N++
	h.Sum += v
	h.Max = max(h.Max, v)
}

func (h *Hist) Pct(p float64) float64 {
	if h.N == 0 {
		return 0
	}
	target := uint64(math.Ceil(float64(h.N) * p / 100))
	var acc uint64
	for i, c := range h.B {
		acc += uint64(c)
		if acc >= target {
			return min(float64(i+1)/2, h.Max)
		}
	}
	return 100
}

func (h *Hist) Avg() float64 {
	if h.N == 0 {
		return 0
	}
	return h.Sum / float64(h.N)
}

type Stat struct {
	N   uint64
	Sum float64
	Max float64
}

func (s *Stat) Add(v float64) {
	if v < 0 || math.IsNaN(v) {
		return
	}
	s.N++
	s.Sum += v
	s.Max = max(s.Max, v)
}

func (s *Stat) Avg() float64 {
	if s.N == 0 {
		return 0
	}
	return s.Sum / float64(s.N)
}

type VMStats struct {
	CPU      Hist
	Mem      Hist
	Ready    Stat
	CPUMHz   Stat
	Consumed Stat
	Disk     Stat
	Net      Stat
	First    time.Time
	Last     time.Time
	Samples  uint64
}

func (s *VMStats) Hours() float64 {
	return float64(s.Samples) * 20 / 3600
}

type Point struct {
	T      time.Time
	CPUMHz float64
	MemB   float64
}

type ClusterStats struct {
	CPU    Hist
	Mem    Hist
	CPUMHz Stat
	MemB   Stat
	Points []Point
	acc    map[time.Time]*bucket
}

type bucket struct {
	cpu, mem float64
	n        int
}

const pointEvery = 5 * time.Minute

type Store struct {
	VMs      map[string]*VMStats
	Clusters map[string]*ClusterStats
	HostLast map[string]time.Time
	Polls    uint64
}

func NewStore() *Store {
	return &Store{VMs: map[string]*VMStats{}, Clusters: map[string]*ClusterStats{}, HostLast: map[string]time.Time{}}
}

// Newest is the latest sample timestamp seen, in vCenter's clock.
func (st *Store) Newest() time.Time {
	var t time.Time
	for _, v := range st.VMs {
		if v.Last.After(t) {
			t = v.Last
		}
	}
	return t
}

func (st *Store) AddVM(s vc.Series, vcpu int) {
	vs := st.VMs[s.Ref]
	if vs == nil {
		vs = &VMStats{}
		st.VMs[s.Ref] = vs
	}
	for i, ts := range s.TS {
		if !ts.After(vs.Last) {
			continue
		}
		at := func(m string) float64 {
			if v, ok := s.Values[m]; ok && i < len(v) {
				return v[i]
			}
			return -1
		}
		vs.CPU.Add(at(vc.CPUUsage))
		vs.Mem.Add(at(vc.MemUsage))
		if r := at(vc.CPUReady); r >= 0 && vcpu > 0 {
			vs.Ready.Add(r / (20000 * float64(vcpu)) * 100)
		}
		vs.CPUMHz.Add(at(vc.CPUMHz))
		vs.Consumed.Add(at(vc.MemConsume))
		vs.Disk.Add(at(vc.DiskUsage))
		vs.Net.Add(at(vc.NetUsage))
		if vs.First.IsZero() {
			vs.First = ts
		}
		vs.Last = ts
		vs.Samples++
	}
}

type clusterCap struct {
	MHz  float64
	MemB float64
}

// AddHosts folds per-host samples into per-cluster demand, summing hosts that
// share a sample timestamp.
func (st *Store) AddHosts(series []vc.Series, hostCluster map[string]string, capacity map[string]clusterCap) {
	type key struct {
		cl string
		t  time.Time
	}
	sum := map[key]*bucket{}
	for _, s := range series {
		cl := hostCluster[s.Ref]
		last := st.HostLast[s.Ref]
		for i, ts := range s.TS {
			if !ts.After(last) {
				continue
			}
			k := key{cl, ts}
			b := sum[k]
			if b == nil {
				b = &bucket{}
				sum[k] = b
			}
			if v := s.Values[vc.CPUMHz]; i < len(v) && v[i] >= 0 {
				b.cpu += v[i]
			}
			if v := s.Values[vc.MemConsume]; i < len(v) && v[i] >= 0 {
				b.mem += v[i] * 1024
			}
			st.HostLast[s.Ref] = ts
		}
	}
	for k, b := range sum {
		cs := st.Clusters[k.cl]
		if cs == nil {
			cs = &ClusterStats{}
			st.Clusters[k.cl] = cs
		}
		c := capacity[k.cl]
		if c.MHz > 0 {
			cs.CPU.Add(b.cpu / c.MHz * 100)
		}
		if c.MemB > 0 {
			cs.Mem.Add(b.mem / c.MemB * 100)
		}
		cs.CPUMHz.Add(b.cpu)
		cs.MemB.Add(b.mem)
		if cs.acc == nil {
			cs.acc = map[time.Time]*bucket{}
		}
		bt := k.t.Truncate(pointEvery)
		a := cs.acc[bt]
		if a == nil {
			a = &bucket{}
			cs.acc[bt] = a
		}
		a.cpu += b.cpu
		a.mem += b.mem
		a.n++
	}
	for _, cs := range st.Clusters {
		cs.flush(time.Now().Add(-2 * pointEvery))
	}
}

func (cs *ClusterStats) flush(before time.Time) {
	for t, a := range cs.acc {
		if t.Before(before) && a.n > 0 {
			cs.Points = append(cs.Points, Point{T: t, CPUMHz: a.cpu / float64(a.n), MemB: a.mem / float64(a.n)})
			delete(cs.acc, t)
		}
	}
	sortPoints(cs.Points)
}

func Capacity(inv *vc.Inventory) (map[string]string, map[string]clusterCap) {
	hc := map[string]string{}
	cp := map[string]clusterCap{}
	for _, h := range inv.Hosts {
		hc[h.Ref] = h.Cluster
		if !h.Connected {
			continue
		}
		c := cp[h.Cluster]
		c.MHz += float64(h.MHz * h.Cores)
		c.MemB += float64(h.MemBytes)
		cp[h.Cluster] = c
	}
	return hc, cp
}
