package analysis

import (
	"slices"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

// Accuracy compares vCenter's historical averages with 20-second samples
// over the same period, to show how much averaging hides.
type Accuracy struct {
	Interval   int32
	VMs        int
	CPUMedian  float64 // median of history p ÷ real-time p, CPU
	MemMedian  float64
	Bursty     []Burst
	HoursBoth  float64
	Percentile float64
}

type Burst struct {
	VM         string
	RealtimeP  float64
	HistoryP   float64
	RealtimeMx float64
}

// EarlierPeak is set when the history before the analysis window holds a
// clearly higher cluster peak than the window itself.
type EarlierPeak struct {
	At        time.Time
	Pct       float64
	WindowPct float64
}

const minCompareSamples = fullDay

func accuracy(inv *vc.Inventory, rt, live *Store, p Profile) *Accuracy {
	if rt == nil || live == nil {
		return nil
	}
	a := &Accuracy{Percentile: p.Percentile}
	var cpu, mem []float64
	for _, vm := range inv.VMs {
		r, h := rt.VMs[vm.Ref], live.VMs[vm.Ref]
		if r == nil || h == nil || r.Samples < minCompareSamples || h.Samples < minCompareSamples {
			continue
		}
		a.VMs++
		a.HoursBoth += h.Hours()
		rp, hp := r.CPU.Pct(p.Percentile), h.CPU.Pct(p.Percentile)
		if rp >= 5 {
			cpu = append(cpu, hp/rp)
			if hp < rp*0.7 {
				a.Bursty = append(a.Bursty, Burst{VM: vm.Name, RealtimeP: rp, HistoryP: hp, RealtimeMx: r.CPU.Max})
			}
		}
		if rm, hm := r.Mem.Pct(p.Percentile), h.Mem.Pct(p.Percentile); rm >= 5 {
			mem = append(mem, hm/rm)
		}
	}
	if a.VMs == 0 {
		return nil
	}
	a.HoursBoth /= float64(a.VMs)
	a.CPUMedian, a.MemMedian = median(cpu), median(mem)
	slices.SortFunc(a.Bursty, func(x, y Burst) int { return int((y.RealtimeP - y.HistoryP) - (x.RealtimeP - x.HistoryP)) })
	if len(a.Bursty) > 10 {
		a.Bursty = a.Bursty[:10]
	}
	return a
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	v = slices.Clone(v)
	slices.Sort(v)
	return v[len(v)/2]
}

func earlierPeak(hist *Store, name string, start time.Time, capMHz float64) *EarlierPeak {
	if hist == nil || capMHz == 0 {
		return nil
	}
	cs := hist.Clusters[name]
	if cs == nil {
		return nil
	}
	var before, during Point
	for _, pt := range cs.Points {
		if pt.T.Before(start) {
			if pt.CPUMHz > before.CPUMHz {
				before = pt
			}
		} else if pt.CPUMHz > during.CPUMHz {
			during = pt
		}
	}
	bp, dp := before.CPUMHz/capMHz*100, during.CPUMHz/capMHz*100
	if during.T.IsZero() || bp < 30 || bp < dp*1.15 {
		return nil
	}
	return &EarlierPeak{At: before.T, Pct: bp, WindowPct: dp}
}
