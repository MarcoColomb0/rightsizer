package analysis

import (
	"testing"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

func excludeFixture() (Input, *Store) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	st := NewStore(start)
	inv := &vc.Inventory{Hosts: []vc.Host{{Name: "h1", Cluster: "cl", Cores: 32, NUMANodes: 2, MHz: 2000, MemBytes: 512 << 30, Connected: true}}}
	for _, v := range []struct{ ref, uuid, name string }{
		{"vm-1", "uuid-1", "vendor-appliance"}, {"vm-2", "uuid-2", "citrix-01"}, {"vm-3", "uuid-3", "citrix-02"}, {"vm-4", "uuid-4", "app-01"},
	} {
		inv.VMs = append(inv.VMs, vc.VM{Ref: v.ref, UUID: v.uuid, Name: v.name, Cluster: "cl", Host: "h1", PowerOn: true, VCPU: 16, MemMB: 65536})
		s := &VMStats{}
		feed(s, 10, 10, 30000)
		st.VMs[v.ref] = s
	}
	return Input{Inv: inv, RT: st, Profile: ProfileByName("balanced"), Start: start, End: start.Add(14 * 24 * time.Hour)}, st
}

func findingsFor(r *Result, vm string) []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.VM == vm {
			out = append(out, f)
		}
	}
	return out
}

func TestExclusions(t *testing.T) {
	in, _ := excludeFixture()
	base := Analyze(in)
	if len(findingsFor(base, "vendor-appliance")) == 0 || len(findingsFor(base, "app-01")) == 0 {
		t.Fatal("fixture must produce findings")
	}
	for _, f := range base.Findings {
		if f.UUID == "" {
			t.Fatalf("finding without VM identity: %+v", f)
		}
	}

	in.Exclusions = []Exclusion{
		{ID: "x1", UUID: "uuid-1", Name: "renamed-long-ago", Reason: "Vendor requirement", Note: "Vendor sizing guide requires 16 vCPU / 64 GB"},
		{ID: "x2", Name: "citrix-*", Kinds: []Kind{MemOver}, Reason: "Other", Note: "Citrix PVS cache needs RAM"},
		{ID: "x3", Name: "no-such-vm", Note: "stale"},
	}
	r := Analyze(in)
	if fs := findingsFor(r, "vendor-appliance"); len(fs) != 0 {
		t.Fatalf("UUID exclusion must survive a rename and drop every finding: %+v", fs)
	}
	for _, name := range []string{"citrix-01", "citrix-02"} {
		fs := findingsFor(r, name)
		if len(fs) == 0 {
			t.Fatalf("%s: only memory advice is excluded", name)
		}
		for _, f := range fs {
			if f.Kind == MemOver {
				t.Fatalf("%s: memory finding must be excluded", name)
			}
		}
	}
	if len(findingsFor(r, "app-01")) == 0 {
		t.Fatal("other VMs must be unaffected")
	}
	// Savings: excluded VM at provisioned size, citrix memory kept, app-01 rightsized.
	wantVCPU := base.Totals.RecVCPU + (16 - recVCPU(base, "vendor-appliance"))
	wantMem := base.Totals.RecMemMB + (65536 - recMem(base, "vendor-appliance")) + 2*(65536-recMem(base, "citrix-01"))
	if r.Totals.RecVCPU != wantVCPU || r.Totals.RecMemMB != wantMem {
		t.Fatalf("excluded VMs must count at provisioned size: vCPU %d want %d, mem %d want %d", r.Totals.RecVCPU, wantVCPU, r.Totals.RecMemMB, wantMem)
	}
	if len(r.Excluded) != 2 || len(r.Excluded[1].Matched) != 2 {
		t.Fatalf("report must list the exclusions that applied: %+v", r.Excluded)
	}
}

func recVCPU(r *Result, name string) int {
	for _, v := range r.VMs {
		if v.Name == name {
			return v.RecVCPU
		}
	}
	return -1
}

func recMem(r *Result, name string) int {
	for _, v := range r.VMs {
		if v.Name == name {
			return v.RecMemMB
		}
	}
	return -1
}

func TestDiskExclusionAndValidation(t *testing.T) {
	in, _ := excludeFixture()
	in.Orphans = []vc.OrphanDisk{{Datastore: "ds", Path: "[ds] veeam/backup.vmdk", Size: 100 << 30}, {Datastore: "ds", Path: "[ds] old/old.vmdk", Size: 50 << 30}}
	in.Exclusions = []Exclusion{{ID: "d", Path: "[ds] veeam/backup.vmdk", Reason: "Other", Note: "Veeam proxy disk"}}
	r := Analyze(in)
	if len(r.Orphans) != 1 || r.Orphans[0].Path != "[ds] old/old.vmdk" || r.Totals.Reclaim != 50<<30 {
		t.Fatalf("excluded disk must not count as reclaimable: %+v", r.Orphans)
	}

	for _, bad := range []Exclusion{{Note: "x"}, {UUID: "u"}, {Name: "[", Note: "x"}} {
		if bad.Validate() == nil {
			t.Fatalf("must be rejected: %+v", bad)
		}
	}
	x := Exclusion{Name: "a", Note: "n", ReviewBy: time.Now().Add(-time.Hour)}
	if x.Validate() != nil || !x.Overdue(time.Now()) {
		t.Fatal("review date handling")
	}
}
