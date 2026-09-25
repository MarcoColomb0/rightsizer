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

func (h *Hist) Add(v float64) { h.AddN(v, 1) }

// AddN records v with a weight of n 20-second samples, so that coarser
// historical samples count for the time they cover.
func (h *Hist) AddN(v float64, n uint64) {
	if v < 0 || math.IsNaN(v) || n == 0 {
		return
	}
	v = min(v, 100)
	h.B[int(v*2)] += uint32(n)
	h.N += n
	h.Sum += v * float64(n)
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

func (s *Stat) Add(v float64) { s.AddN(v, 1) }

func (s *Stat) AddN(v float64, n uint64) {
	if v < 0 || math.IsNaN(v) || n == 0 {
		return
	}
	s.N += n
	s.Sum += v * float64(n)
	s.Max = max(s.Max, v)
}

func (s *Stat) Avg() float64 {
	if s.N == 0 {
		return 0
	}
	return s.Sum / float64(s.N)
}

type VMStats struct {
	CPU       Hist
	Mem       Hist
	Ready     Stat
	CoStop    Stat
	CPUMHz    Stat
	Consumed  Stat
	Disk      Stat
	Net       Stat
	ReadIOPS  Stat
	WriteIOPS Stat
	First     time.Time
	Last      time.Time
	Samples   uint64
	// Demand holds average CPU MHz per 30-minute slot since Store.Anchor.
	Demand []float32
	Slots  []uint16
}

const (
	SlotLength = 30 * time.Minute
	maxSlots   = 32 * 48
)

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
	// Net, IOPS and KBps are the network and storage load of all hosts.
	Net    LogHist
	IOPS   LogHist
	KBps   LogHist
	Points []Point
	acc    map[time.Time]*bucket
}

type bucket struct {
	cpu, mem        float64
	net, iops, kbps float64
	hasNet, hasIO   bool
	hasKB           bool
	n               int
}

const pointEvery = 5 * time.Minute

type Store struct {
	VMs      map[string]*VMStats
	Clusters map[string]*ClusterStats
	// IO is keyed by datastore instance; "" holds all datastores together.
	IO       map[string]*IOStats
	HostLast map[string]time.Time
	Polls    uint64
	Anchor   time.Time
}

// NewStore keeps demand slots from anchor onwards.
func NewStore(anchor time.Time) *Store {
	return &Store{
		VMs: map[string]*VMStats{}, Clusters: map[string]*ClusterStats{}, IO: map[string]*IOStats{}, HostLast: map[string]time.Time{},
		Anchor: anchor.Truncate(SlotLength),
	}
}

func weight(interval int32) uint64 {
	if interval <= 20 {
		return 1
	}
	return uint64(interval / 20)
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
	w := weight(s.Interval)
	secs := float64(max(s.Interval, 20))
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
		vs.CPU.AddN(at(vc.CPUUsage), w)
		vs.Mem.AddN(at(vc.MemUsage), w)
		// ready and co-stop are milliseconds summed over the sample interval
		if r := at(vc.CPUReady); r >= 0 && vcpu > 0 {
			vs.Ready.AddN(r/(secs*1000*float64(vcpu))*100, w)
		}
		if r := at(vc.CPUCoStop); r >= 0 && vcpu > 0 {
			vs.CoStop.AddN(r/(secs*1000*float64(vcpu))*100, w)
		}
		mhz := at(vc.CPUMHz)
		vs.CPUMHz.AddN(mhz, w)
		vs.Consumed.AddN(at(vc.MemConsume), w)
		vs.Disk.AddN(at(vc.DiskUsage), w)
		vs.Net.AddN(at(vc.NetUsage), w)
		vs.ReadIOPS.AddN(s.Sum(vc.ReadIOPS, i), w)
		vs.WriteIOPS.AddN(s.Sum(vc.WriteIOPS, i), w)
		st.addDemand(vs, ts, mhz, s.Interval)
		if vs.First.IsZero() {
			vs.First = ts.Add(-time.Duration(secs) * time.Second)
		}
		vs.Last = ts
		vs.Samples += w
	}
}

// addDemand records a sample in the 30-minute slots it covers: short samples
// in the slot of their midpoint, long historical ones in every slot.
func (st *Store) addDemand(vs *VMStats, ts time.Time, mhz float64, interval int32) {
	if mhz < 0 || st.Anchor.IsZero() {
		return
	}
	span := time.Duration(max(interval, 20)) * time.Second
	if span <= SlotLength {
		st.setSlot(vs, ts.Add(-span/2), mhz)
		return
	}
	for t := ts.Add(-span).Truncate(SlotLength); t.Before(ts); t = t.Add(SlotLength) {
		st.setSlot(vs, t, mhz)
	}
}

func (st *Store) setSlot(vs *VMStats, t time.Time, mhz float64) {
	i := int(t.Sub(st.Anchor) / SlotLength)
	if t.Before(st.Anchor) || i >= maxSlots {
		return
	}
	for len(vs.Demand) <= i {
		vs.Demand = append(vs.Demand, 0)
		vs.Slots = append(vs.Slots, 0)
	}
	n := float64(vs.Slots[i])
	vs.Demand[i] = float32((float64(vs.Demand[i])*n + mhz) / (n + 1))
	if vs.Slots[i] < math.MaxUint16 {
		vs.Slots[i]++
	}
}

type clusterCap struct {
	MHz  float64
	MemB float64
}

// AddHosts folds per-host samples into per-cluster demand, summing hosts that
// share a sample timestamp.
func (st *Store) AddHosts(series []vc.Series, hostCluster map[string]string, capacity map[string]clusterCap) {
	var w uint64 = 1
	if len(series) > 0 {
		w = weight(series[0].Interval)
	}
	type key struct {
		cl string
		t  time.Time
	}
	sum := map[key]*bucket{}
	io := map[key]*ioSample{}
	ioAt := func(ds string, t time.Time) *ioSample {
		a := io[key{ds, t}]
		if a == nil {
			a = &ioSample{}
			io[key{ds, t}] = a
		}
		return a
	}
	for _, s := range series {
		cl := hostCluster[s.Ref]
		last := st.HostLast[s.Ref]
		insts := instances(s)
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
			if v := s.Values[vc.NetUsage]; i < len(v) && v[i] >= 0 {
				b.net += v[i]
				b.hasNet = true
			}
			var host ioSample
			for _, inst := range insts {
				host.add(s, inst, i)
				ioAt(inst, ts).add(s, inst, i)
				ioAt("", ts).add(s, inst, i)
			}
			if host.ops {
				b.iops += host.r + host.w
				b.hasIO = true
			}
			if host.kb {
				b.kbps += host.rkb + host.wkb
				b.hasKB = true
			}
			st.HostLast[s.Ref] = ts
		}
	}
	if st.IO == nil {
		st.IO = map[string]*IOStats{}
	}
	for k, a := range io {
		if !a.ok() {
			continue
		}
		d := st.IO[k.cl]
		if d == nil {
			d = &IOStats{}
			st.IO[k.cl] = d
		}
		d.add(a, k.t, w, k.cl == "")
	}
	for k, b := range sum {
		cs := st.Clusters[k.cl]
		if cs == nil {
			cs = &ClusterStats{}
			st.Clusters[k.cl] = cs
		}
		c := capacity[k.cl]
		if c.MHz > 0 {
			cs.CPU.AddN(b.cpu/c.MHz*100, w)
		}
		if c.MemB > 0 {
			cs.Mem.AddN(b.mem/c.MemB*100, w)
		}
		cs.CPUMHz.AddN(b.cpu, w)
		cs.MemB.AddN(b.mem, w)
		if b.hasNet {
			cs.Net.AddN(b.net, w)
		}
		if b.hasIO {
			cs.IOPS.AddN(b.iops, w)
		}
		if b.hasKB {
			cs.KBps.AddN(b.kbps, w)
		}
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
	for _, d := range st.IO {
		d.flush(time.Now().Add(-2 * pointEvery))
	}
}

// Flush moves every pending demand bucket into Points.
func (cs *ClusterStats) Flush() { cs.flush(time.Now().Add(time.Hour)) }

// Flush completes the timelines of every cluster and of the storage total.
func (st *Store) Flush() {
	for _, c := range st.Clusters {
		c.Flush()
	}
	for _, d := range st.IO {
		d.Flush()
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
