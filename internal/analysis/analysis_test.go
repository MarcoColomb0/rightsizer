package analysis

import (
	"math"
	"testing"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

func TestHistPct(t *testing.T) {
	var h Hist
	for i := range 100 {
		h.Add(float64(i))
	}
	if p := h.Pct(95); p < 94 || p > 95.5 {
		t.Fatalf("p95 = %v", p)
	}
	h.Add(250)
	if h.Max != 100 {
		t.Fatal("values must be capped at 100")
	}
}

func feed(s *VMStats, cpu, mem float64, n int) {
	for range n {
		s.CPU.Add(cpu)
		s.Mem.Add(mem)
		s.Net.Add(100)
		s.Disk.Add(100)
		s.Samples++
	}
}

func TestRightsize(t *testing.T) {
	p := ProfileByName("balanced")
	over := &VMStats{}
	feed(over, 10, 10, 30000)
	vm := vc.VM{Name: "big", VCPU: 16, MemMB: 65536, PowerOn: true, GuestOS: "Ubuntu Linux"}
	r, fs := rightsize(vm, over, p, 336)
	if r.RecVCPU != 3 {
		t.Fatalf("vCPU: want 3 got %d", r.RecVCPU)
	}
	if r.RecMemMB >= vm.MemMB || r.RecMemMB < int(float64(vm.MemMB)*p.MinMemRatio) {
		t.Fatalf("memory: got %d", r.RecMemMB)
	}
	if len(fs) != 2 || fs[0].Confidence != "high" {
		t.Fatalf("findings: %+v", fs)
	}

	hot := &VMStats{}
	feed(hot, 97, 95, 5000)
	_, fs = rightsize(vc.VM{Name: "hot", VCPU: 2, MemMB: 4096, PowerOn: true}, hot, p, 336)
	kinds := map[Kind]bool{}
	for _, f := range fs {
		kinds[f.Kind] = true
	}
	if !kinds[CPUUnder] || !kinds[MemUnder] {
		t.Fatalf("expected undersized findings, got %+v", fs)
	}

	idle := &VMStats{}
	for range 5000 {
		idle.CPU.Add(0.5)
		idle.Mem.Add(3)
		idle.Samples++
	}
	_, fs = rightsize(vc.VM{Name: "idle", VCPU: 4, MemMB: 8192, PowerOn: true, GuestOS: "Microsoft Windows Server 2022"}, idle, p, 336)
	if fs[0].Kind != Idle {
		t.Fatalf("expected idle, got %+v", fs)
	}
}

func TestStorageFindings(t *testing.T) {
	now := time.Now()
	vm := vc.VM{
		Name:          "snap",
		Snapshots:     []vc.Snapshot{{Name: "pre-upgrade", Created: now.Add(-10 * 24 * time.Hour)}},
		SnapshotBytes: 30 << 30,
		Disks:         []vc.Disk{{Capacity: 500 << 30, Thin: false}},
		GuestDisks:    []vc.GuestDisk{{Capacity: 500 << 30, Free: 400 << 30}},
	}
	fs := storageFindings(vm, now)
	if len(fs) != 2 || fs[0].Kind != OldSnap || fs[0].Severity != High || fs[1].Kind != ThickDisk {
		t.Fatalf("got %+v", fs)
	}
}

func TestFindingsOrderedByCriticality(t *testing.T) {
	fs := []Finding{
		{VM: "a-small", Kind: CPUOver, Severity: High, VCPU: 2},
		{VM: "b-disk", Kind: Orphan, Severity: High, Bytes: 2 << 40},
		{VM: "c-costop", Kind: CoStopHigh, Severity: High, Metric: 40},
		{VM: "d-big", Kind: CPUOver, Severity: High, VCPU: 12},
		{VM: "e-under", Kind: CPUUnder, Severity: High, VCPU: -2, Metric: 97},
		{VM: "f-medium", Kind: MemOver, Severity: Medium, MemMB: 256 << 10},
		{VM: "g-low", Kind: CPUOver, Severity: Low, VCPU: 1},
	}
	for i := range fs {
		fs[i].Impact = impact(fs[i])
	}
	SortFindings(fs)
	var got []string
	for _, f := range fs {
		got = append(got, f.VM)
	}
	want := []string{"e-under", "c-costop", "b-disk", "d-big", "a-small", "f-medium", "g-low"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order\n got %v\nwant %v", got, want)
		}
	}
}

func TestAddCountSaturates(t *testing.T) {
	for _, tc := range []struct {
		c    uint32
		n    uint64
		want uint32
	}{
		{1, 2, 3},
		{math.MaxUint32 - 1, 1, math.MaxUint32},
		{math.MaxUint32 - 1, 5, math.MaxUint32},
		{0, math.MaxUint64, math.MaxUint32},
	} {
		if got := addCount(tc.c, tc.n); got != tc.want {
			t.Errorf("addCount(%d, %d) = %d, want %d", tc.c, tc.n, got, tc.want)
		}
	}
}
