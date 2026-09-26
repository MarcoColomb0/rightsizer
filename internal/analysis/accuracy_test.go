package analysis

import (
	"testing"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

func TestAccuracyAndEarlierPeak(t *testing.T) {
	inv := &vc.Inventory{VMs: []vc.VM{{Ref: "bursty", Name: "bursty"}, {Ref: "steady", Name: "steady"}}}
	rt, live := NewStore(time.Now()), NewStore(time.Now())
	for i := range 2 * fullDay {
		// bursty: idle most of the time with 20-second spikes to 90%
		v := 10.0
		if i%10 == 0 {
			v = 90
		}
		rt.VMs["bursty"] = addCPU(rt.VMs["bursty"], v, 1)
		rt.VMs["steady"] = addCPU(rt.VMs["steady"], 40, 1)
	}
	for range 2 * fullDay / 15 {
		live.VMs["bursty"] = addCPU(live.VMs["bursty"], 18, 15) // 5-minute average of the same load
		live.VMs["steady"] = addCPU(live.VMs["steady"], 40, 15)
	}
	a := accuracy(inv, rt, live, ProfileByName("balanced"))
	if a == nil || a.VMs != 2 {
		t.Fatalf("both VMs must be compared: %+v", a)
	}
	if len(a.Bursty) != 1 || a.Bursty[0].VM != "bursty" || a.Bursty[0].RealtimeP < 85 || a.Bursty[0].HistoryP > 20 {
		t.Fatalf("bursty VM not identified: %+v", a.Bursty)
	}
	if a.CPUMedian > 1.01 || a.CPUMedian < 0.2 {
		t.Fatalf("median ratio out of range: %.2f", a.CPUMedian)
	}

	start := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	hist := NewStore(start.Add(-14 * 24 * time.Hour))
	hist.Clusters["cl"] = &ClusterStats{Points: []Point{
		{T: start.Add(-5 * 24 * time.Hour), CPUMHz: 7500},
		{T: start.Add(-24 * time.Hour), CPUMHz: 3000},
		{T: start.Add(24 * time.Hour), CPUMHz: 5000},
	}}
	ep := earlierPeak(hist, "cl", start, 10000)
	if ep == nil || ep.Pct != 75 || ep.WindowPct != 50 || !ep.At.Equal(start.Add(-5*24*time.Hour)) {
		t.Fatalf("earlier peak not reported: %+v", ep)
	}
	hist.Clusters["cl"].Points[0].CPUMHz = 5200
	if earlierPeak(hist, "cl", start, 10000) != nil {
		t.Fatal("a similar peak before the window must not be reported")
	}
}

func addCPU(s *VMStats, v float64, n uint64) *VMStats {
	if s == nil {
		s = &VMStats{}
	}
	s.CPU.AddN(v, n)
	s.Mem.AddN(30, n)
	s.Samples += n
	return s
}
