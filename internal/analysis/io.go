package analysis

import (
	"math"
	"slices"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

// logStep spaces LogHist buckets 2% apart.
var logStep = math.Log(1.02)

// LogHist is a histogram for rates of any magnitude, such as IOPS or KB/s,
// accurate to about 1%.
type LogHist struct {
	B   map[uint16]uint32
	N   uint64
	Max float64
	Sum float64
}

func (h *LogHist) AddN(v float64, n uint64) {
	if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) || n == 0 {
		return
	}
	if h.B == nil {
		h.B = map[uint16]uint32{}
	}
	i := uint16(min(math.Log1p(v)/logStep, math.MaxUint16))
	h.B[i] = addCount(h.B[i], n)
	h.N += n
	h.Sum += v * float64(n)
	h.Max = max(h.Max, v)
}

func (h *LogHist) Pct(p float64) float64 {
	if h.N == 0 {
		return 0
	}
	keys := make([]uint16, 0, len(h.B))
	for k := range h.B {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	target := uint64(math.Ceil(float64(h.N) * p / 100))
	var acc uint64
	for _, k := range keys {
		acc += uint64(h.B[k])
		if acc >= target {
			return min(math.Expm1(float64(k+1)*logStep), h.Max)
		}
	}
	return h.Max
}

func (h *LogHist) Avg() float64 {
	if h.N == 0 {
		return 0
	}
	return h.Sum / float64(h.N)
}

// IOStats is the storage load on one datastore, or on all of them, summed
// over every host at each sample.
type IOStats struct {
	IOPS    LogHist
	KBps    LogHist
	Latency LogHist
	Read    Stat
	Write   Stat
	ReadKB  Stat
	WriteKB Stat
	// SizeKB and SizeOps add up samples that reported both throughput and
	// operations, for the average transfer size.
	SizeKB  float64
	SizeOps float64
	Points  []IOPoint
	acc     map[time.Time]*ioPointAcc
}

type IOPoint struct {
	T    time.Time
	IOPS float64
	KBps float64
}

type ioPointAcc struct {
	iops, kbps float64
	n          int
}

// ioSample sums one timestamp. vCenter's history may keep some datastore
// counters and not others, depending on its statistics level, so each kind
// is recorded only when reported.
type ioSample struct {
	r, w, rkb, wkb float64
	// latW is latency weighted by IOPS, divided by latIO when recorded.
	latW, latIO float64
	ops, kb     bool
}

func (a *ioSample) ok() bool { return a.ops || a.kb }

func (a *ioSample) add(s vc.Series, inst string, i int) {
	at := func(m string, seen *bool) float64 {
		if v := s.Inst[m][inst]; i < len(v) && v[i] >= 0 {
			if seen != nil {
				*seen = true
			}
			return v[i]
		}
		return -1
	}
	r, w := at(vc.ReadIOPS, &a.ops), at(vc.WriteIOPS, &a.ops)
	rkb, wkb := at(vc.ReadKBps, &a.kb), at(vc.WriteKBps, &a.kb)
	rl, wl := at(vc.ReadLat, nil), at(vc.WriteLat, nil)
	a.r += max(r, 0)
	a.w += max(w, 0)
	a.rkb += max(rkb, 0)
	a.wkb += max(wkb, 0)
	if rl >= 0 && r > 0 {
		a.latW += rl * r
		a.latIO += r
	}
	if wl >= 0 && w > 0 {
		a.latW += wl * w
		a.latIO += w
	}
}

// add records one summed sample; the 5-minute timeline is kept only when
// points is set, for the all-datastores total.
func (st *IOStats) add(a *ioSample, t time.Time, w uint64, points bool) {
	if a.ops {
		st.IOPS.AddN(a.r+a.w, w)
		st.Read.AddN(a.r, w)
		st.Write.AddN(a.w, w)
	}
	if a.kb {
		st.KBps.AddN(a.rkb+a.wkb, w)
		st.ReadKB.AddN(a.rkb, w)
		st.WriteKB.AddN(a.wkb, w)
	}
	if a.ops && a.kb {
		st.SizeKB += (a.rkb + a.wkb) * float64(w)
		st.SizeOps += (a.r + a.w) * float64(w)
	}
	if a.latIO > 0 {
		st.Latency.AddN(a.latW/a.latIO, w)
	}
	if !points {
		return
	}
	if st.acc == nil {
		st.acc = map[time.Time]*ioPointAcc{}
	}
	bt := t.Truncate(pointEvery)
	p := st.acc[bt]
	if p == nil {
		p = &ioPointAcc{}
		st.acc[bt] = p
	}
	p.iops += a.r + a.w
	p.kbps += a.rkb + a.wkb
	p.n++
}

// Flush moves every pending 5-minute average into Points.
func (st *IOStats) Flush() { st.flush(time.Now().Add(time.Hour)) }

func (st *IOStats) flush(before time.Time) {
	for t, p := range st.acc {
		if t.Before(before) && p.n > 0 {
			st.Points = append(st.Points, IOPoint{T: t, IOPS: p.iops / float64(p.n), KBps: p.kbps / float64(p.n)})
			delete(st.acc, t)
		}
	}
	slices.SortFunc(st.Points, func(a, b IOPoint) int { return a.T.Compare(b.T) })
}

// ReadShare is the fraction of I/O operations that are reads.
func (st *IOStats) ReadShare() float64 {
	if t := st.Read.Sum + st.Write.Sum; t > 0 {
		return st.Read.Sum / t
	}
	return 0
}

// IOSize is the average transfer size in KB, 0 when unknown.
func (st *IOStats) IOSize() float64 {
	if st.SizeOps > 0 {
		return st.SizeKB / st.SizeOps
	}
	return 0
}

func instances(s vc.Series) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range s.Inst {
		for inst := range m {
			if !seen[inst] {
				seen[inst] = true
				out = append(out, inst)
			}
		}
	}
	return out
}

func DownsampleIO(p []IOPoint, n int) []IOPoint {
	if len(p) <= n || n <= 0 {
		return p
	}
	out := make([]IOPoint, 0, n)
	step := float64(len(p)) / float64(n)
	for i := range n {
		lo, hi := int(float64(i)*step), int(float64(i+1)*step)
		var iops, kbps float64
		for _, x := range p[lo:hi] {
			iops = max(iops, x.IOPS)
			kbps = max(kbps, x.KBps)
		}
		out = append(out, IOPoint{T: p[lo].T, IOPS: iops, KBps: kbps})
	}
	return out
}
