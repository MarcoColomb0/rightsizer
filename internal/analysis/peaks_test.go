package analysis

import (
	"math"
	"testing"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

func series(ref string, interval int32, start time.Time, n int, f func(i int) (cpuPct, ready, mhz float64)) vc.Series {
	s := vc.Series{Ref: ref, Interval: interval, Values: map[string][]float64{}}
	for i := 0; i < n; i++ {
		c, r, m := f(i)
		s.TS = append(s.TS, start.Add(time.Duration(i+1)*time.Duration(interval)*time.Second))
		s.Values[vc.CPUUsage] = append(s.Values[vc.CPUUsage], c)
		s.Values[vc.CPUReady] = append(s.Values[vc.CPUReady], r)
		s.Values[vc.CPUMHz] = append(s.Values[vc.CPUMHz], m)
	}
	return s
}

func TestHistoryWeightsAndReady(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	st := NewStore(start)
	// 12 two-hour samples = 24 h, 2 vCPU, 1440 ms ready per 2 h = 0.01% per vCPU
	st.AddVM(series("vm", 7200, start, 12, func(int) (float64, float64, float64) { return 10, 1440, 500 }), 2)
	s := st.VMs["vm"]
	if s.Samples != 12*360 || math.Abs(s.Hours()-24) > 0.01 {
		t.Fatalf("2-hour samples must count for their duration: %d samples, %.1f h", s.Samples, s.Hours())
	}
	if got := s.Ready.Avg(); math.Abs(got-0.01) > 1e-9 {
		t.Fatalf("ready must be divided by the sample interval, got %.4f%%", got)
	}
	if len(s.Demand) != 48 {
		t.Fatalf("24 h must fill 48 half-hour slots, got %d", len(s.Demand))
	}
	for i, n := range s.Slots {
		if n == 0 {
			t.Fatalf("slot %d empty", i)
		}
	}

	rt := NewStore(start)
	rt.AddVM(series("vm", 20, start, 90, func(int) (float64, float64, float64) { return 50, 200, 1000 }), 1)
	if got := rt.VMs["vm"].Ready.Avg(); math.Abs(got-1) > 1e-9 {
		t.Fatalf("real-time ready: want 1%%, got %.3f%%", got)
	}
	if len(rt.VMs["vm"].Demand) != 1 {
		t.Fatal("30 minutes of 20-second samples belong in one slot")
	}
}

func TestPearsonAndDiversity(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	st := NewStore(start)
	day := func(i int, phase float64) float64 {
		return 0.5 + 0.5*math.Sin(2*math.Pi*float64(i)/48+phase)
	}
	const slots = 96
	for _, v := range []struct {
		ref   string
		phase float64
	}{{"a", 0}, {"b", 0.1}, {"c", math.Pi}} {
		st.AddVM(series(v.ref, 1800, start, slots, func(i int) (float64, float64, float64) {
			return 100 * day(i, v.phase), 0, 4000 * day(i, v.phase)
		}), 2)
	}
	ab, _ := pearson(st.VMs["a"], st.VMs["b"])
	ac, _ := pearson(st.VMs["a"], st.VMs["c"])
	if ab < 0.95 || ac > -0.95 {
		t.Fatalf("correlation wrong: a~b %.2f, a~c %.2f", ab, ac)
	}

	c := &ClusterResult{Name: "cl", Hosts: 2, Cores: 10, CapMHz: 20000, HostsForCPU: 1, NeedMHz: 9000}
	vms := []vc.VM{
		{Ref: "a", Name: "a", VCPU: 2, PowerOn: true, Host: "h1"},
		{Ref: "b", Name: "b", VCPU: 2, PowerOn: true, Host: "h1"},
		{Ref: "c", Name: "c", VCPU: 2, PowerOn: true, Host: "h2"},
	}
	pk := peaks(c, vms, st, ProfileByName("balanced"))
	// a+b peak together at ~8000 MHz while c is idle, and vice versa, so the
	// combined 95th percentile is well below the sum of the three peaks.
	if pk == nil || pk.Diversity < 1.3 || pk.NaiveHosts <= pk.AwareHosts {
		t.Fatalf("anti-correlated VMs must give a diversity factor above 1, got %+v", pk)
	}
	if len(pk.CoPeak) != 1 || len(pk.CoPeak[0].VMs) != 2 {
		t.Fatalf("a and b peak together: %+v", pk.CoPeak)
	}
	if len(pk.Complementary) == 0 {
		t.Fatal("a and c are complementary")
	}
}

func TestPreviewSelection(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	hist := NewStore(start.Add(-14 * 24 * time.Hour))
	rt := NewStore(start)
	hist.AddVM(series("vm", 1800, start.Add(-14*24*time.Hour), 14*48, func(int) (float64, float64, float64) { return 5, 0, 100 }), 4)
	rt.AddVM(series("vm", 20, start, 180, func(int) (float64, float64, float64) { return 80, 0, 100 }), 4)
	in := Input{RT: rt, History: hist}
	if s, preview := in.vmStats("vm"); !preview || s != hist.VMs["vm"] {
		t.Fatal("with only one hour of 20-second data the history must be used as a preview")
	}
	rt.AddVM(series("vm", 20, start.Add(time.Hour), fullDay, func(int) (float64, float64, float64) { return 80, 0, 100 }), 4)
	if s, preview := in.vmStats("vm"); preview || s != rt.VMs["vm"] {
		t.Fatal("after 24 hours of 20-second data the preview must be replaced")
	}
}

func TestPlacementFindings(t *testing.T) {
	h := vc.Host{Name: "esx1", Cores: 32, NUMANodes: 2, MemBytes: 512 << 30}
	s := &VMStats{}
	for i := 0; i < 1000; i++ {
		s.CoStop.Add(6)
	}
	vm := vc.VM{Name: "big", VCPU: 24, CoresPerSock: 1, MemMB: 64 << 10, Host: "esx1"}
	fs := placementFindings(vm, VMResult{RecVCPU: 20}, s, h)
	kinds := map[Kind]Finding{}
	for _, f := range fs {
		kinds[f.Kind] = f
	}
	if _, ok := kinds[WideVM]; !ok {
		t.Fatal("24 vCPU on 16-core NUMA nodes must be flagged")
	}
	if f, ok := kinds[CoStopHigh]; !ok || f.Severity != High {
		t.Fatal("6% co-stop must be flagged")
	}
	if fs := placementFindings(vc.VM{VCPU: 8, MemMB: 16 << 10}, VMResult{RecVCPU: 4}, &VMStats{}, h); len(fs) != 0 {
		t.Fatalf("a VM inside one node must not be flagged: %+v", fs)
	}
}

func TestOrphanFindingsCountAsReclaimable(t *testing.T) {
	inv := &vc.Inventory{}
	r := Analyze(Input{Inv: inv, RT: NewStore(time.Now()), Profile: ProfileByName("balanced"),
		Orphans: []vc.OrphanDisk{{Datastore: "ds1", Path: "[ds1] old/old.vmdk", Size: 300 << 30}}})
	if r.Totals.Reclaim != 300<<30 || len(r.Findings) != 1 || r.Findings[0].Kind != Orphan || r.Findings[0].Severity != High {
		t.Fatalf("orphaned disk not reported: %+v %+v", r.Totals, r.Findings)
	}
}
