package analysis

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/ontap"
	"github.com/MarcoColomb0/rightsizer/internal/ontap/ontapsim"
	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

// arrayFixture backs the sizing fixture's datastores with an ONTAP cluster:
// ds-fc on a LUN, nfs-01 on an NFS volume, plus a datastore elsewhere.
func arrayFixture(t *testing.T) (SizingInput, *ontap.Inventory) {
	t.Helper()
	in := sizingFixture()
	inv, naa := ontapsim.Fixture("aff01", []ontapsim.Datastore{
		{Name: "ds_fc", Used: 4 << 40, Ratio: 3, Snapshots: 200 << 30},
		{Name: "nfs01", NFS: true, Used: 4 << 40, Ratio: 2},
		{Name: "shares", NFS: true, Used: 10 << 40, Ratio: 1.2},
	})
	inv.Volumes[1].Path = "/vol/nfs01"
	inv.LIFs = append(inv.LIFs, ontap.LIF{Name: "nfs_lif9", SVM: "svm_vmware", IP: "10.0.0.5", Up: true})
	in.Estate.Datastores[0].LUNs[0].Name = strings.ToUpper(naa[0])
	in.Estate.Datastores = append(in.Estate.Datastores,
		vc.DatastoreDetail{Name: "other", Type: "NFS", Version: "NFS 3", Capacity: 2 << 40, Free: 1 << 40, Accessible: true, Hosts: []string{"esx0"}, Remote: "10.9.9.9:/export/x"},
		vc.DatastoreDetail{Name: "local-esx0", Type: "VMFS", Capacity: 1 << 40, Free: 1 << 40, Accessible: true, Local: true, Hosts: []string{"esx0"}})
	st := NewArrayStore()
	end := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	var cl, vol, cpu []ontap.Sample
	for ts := end.Add(-48 * time.Hour); ts.Before(end); ts = ts.Add(15 * time.Second) {
		x := ontapsim.Default(ontap.ClusterObject, "", ts)
		x.T, x.Interval = ts, 15
		cl = append(cl, x)
		v := ontapsim.Default(ontap.VolumeObject, "", ts)
		v.T, v.Interval = ts, 15
		vol = append(vol, v)
		x.CPU = 30 + 40*math.Max(0, math.Sin(float64(ts.Hour())/24*2*math.Pi))
		cpu = append(cpu, x)
	}
	st.Add(ontap.ClusterObject, "", cl, time.Time{}, end)
	st.Add(ontap.VolumeObject, inv.Volumes[0].UUID, vol, time.Time{}, end)
	st.Add(ontap.NodeObject, inv.Nodes[0].Name, cpu, time.Time{}, end)
	st.Flush()
	in.Arrays = []ArrayInput{{Host: "aff01.corp.local", Inv: inv, RT: st}}
	return in, inv
}

func TestArrayStoreCountsOverlapsOnce(t *testing.T) {
	st := NewArrayStore()
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var ss []ontap.Sample
	for i := range 40 {
		ss = append(ss, ontap.Sample{T: t0.Add(time.Duration(i) * 15 * time.Second), Interval: 15, ReadIOPS: 100, WriteIOPS: 50,
			ReadBps: 100 << 10, WriteBps: 50 << 10, ReadLatUs: 500, WriteLatUs: 1000})
	}
	st.Add(ontap.ClusterObject, "", ss[:30], time.Time{}, t0.Add(time.Hour))
	st.Add(ontap.ClusterObject, "", ss, time.Time{}, t0.Add(time.Hour))
	if n := st.Cluster.IOPS.N; n != 40 {
		t.Fatalf("overlapping polls must count each sample once: %d", n)
	}
	if lat := st.Cluster.Latency.Max; math.Abs(lat-(0.5*100+1*50)/150) > 1e-9 {
		t.Fatalf("latency is weighted by reads and writes, in ms: %v", lat)
	}
	if kb := st.Cluster.KBps.Max; kb != 150 {
		t.Fatalf("throughput in KB/s: %v", kb)
	}
	h := NewArrayStore()
	h.Add(ontap.VolumeObject, "v", ss, t0.Add(5*time.Minute), t0.Add(8*time.Minute))
	if n := h.Volumes["v"].IOPS.N; n != 12 {
		t.Fatalf("an archive window keeps only its own period: %d", n)
	}
	if s := h.Seconds["volume:v"]; s != 180 {
		t.Fatalf("seconds covered %v", s)
	}
}

func TestSizeArrays(t *testing.T) {
	in, inv := arrayFixture(t)
	sz := Size(in)
	st := sz.Storage
	r := st.Array
	if r.Datastores != 2 || r.Of != 3 {
		t.Fatalf("matched %d of %d shared datastores (local excluded)", r.Datastores, r.Of)
	}
	byName := map[string]DatastoreSizing{}
	for _, d := range sz.Datastores {
		byName[d.Name] = d
	}
	fc := byName["ds-fc"].Array
	if fc == nil || fc.Volume != "ds_fc" || fc.LUNs != 1 || fc.Cluster != "aff01" || fc.Snapshots != 200<<30 {
		t.Fatalf("FC datastore backing %+v", fc)
	}
	if fc.Ratio < 3 || fc.Ratio > 3.4 {
		t.Fatalf("LUN volume reduction %v: 3:1 from the volume plus compaction", fc.Ratio)
	}
	if !fc.Latency || fc.IOPS <= 0 {
		t.Fatalf("array-side performance of the datastore volume: %+v", fc)
	}
	nfs := byName["nfs-01"].Array
	if nfs == nil || nfs.Volume != "nfs01" || nfs.LUNs != 0 {
		t.Fatalf("NFS datastore backing %+v", nfs)
	}
	if byName["other"].Array != nil || byName["local-esx0"].Array != nil {
		t.Fatal("datastores on other storage stay unmatched")
	}
	// 8 TB stored in 4/3 + 4/2 TB, not the average of the two ratios.
	if v := r.VolumeRatio(); math.Abs(v-2.4) > 0.01 {
		t.Fatalf("volume reduction of 4 TB at 3:1 and 4 TB at 2:1 is 2.4:1, got %v", v)
	}
	if r.Ratio() < r.VolumeRatio() || r.PlanPhysical <= 0 || r.PlanPhysical >= st.Plan {
		t.Fatalf("ratio %v, plan %d physical %d", r.Ratio(), st.Plan, r.PlanPhysical)
	}
	if want := int64(float64(st.Plan) / r.Ratio()); r.PlanPhysical != want {
		t.Fatalf("planned physical %d, want %d", r.PlanPhysical, want)
	}
	if len(st.Arrays) != 1 {
		t.Fatalf("arrays %+v", st.Arrays)
	}
	a := st.Arrays[0]
	if a.Name != "aff01" || a.Models != "2 × AFF-A400" || a.Matched != 2 || a.Volumes != 2 || a.OtherData != 10<<40 || len(a.Aggregates) != 2 {
		t.Fatalf("array %s %q: %d datastores, %d volumes, other %d, %d aggregates", a.Name, a.Models, a.Matched, a.Volumes, a.OtherData, len(a.Aggregates))
	}
	if !a.IO.Available || a.Preview || a.IO.Hours != 48 || a.CPU < 60 || a.CPUNode != inv.Nodes[0].Name || len(a.IO.Points) == 0 {
		t.Fatalf("array performance: available %v preview %v hours %v CPU %v on %s", a.IO.Available, a.Preview, a.IO.Hours, a.CPU, a.CPUNode)
	}
	notes := strings.Join(sz.Notes, "\n")
	for _, want := range []string{"ONTAP reduces the data of 2 of 3 shared datastore(s)", "Snapshot copies of the datastore volumes hold 200.0 GB",
		"also holds 10.0 TB of data no analysed datastore uses", "controllers reach", "covers"} {
		if !strings.Contains(notes, want) {
			t.Fatalf("notes miss %q:\n%s", want, notes)
		}
	}
}

func TestNetAppHintWithoutArrays(t *testing.T) {
	in, _ := arrayFixture(t)
	in.Arrays = nil
	in.Estate.Datastores[0].LUNs[0].Vendor = "NETAPP"
	sz := Size(in)
	if sz.Storage.NetAppDatastores != 1 || sz.Storage.Array.Datastores != 0 {
		t.Fatalf("storage %+v", sz.Storage.Array)
	}
	if !strings.Contains(strings.Join(sz.Notes, "\n"), "Add their ONTAP cluster") {
		t.Fatalf("notes %v", sz.Notes)
	}
}

func TestNFSNeedsTheSVMsInterface(t *testing.T) {
	in, inv := arrayFixture(t)
	inv.LIFs = inv.LIFs[:len(inv.LIFs)-1]
	sz := Size(in)
	for _, d := range sz.Datastores {
		if d.Name == "nfs-01" && d.Array != nil {
			t.Fatal("an export on an address no SVM owns is another server, even with the same path")
		}
	}
}

func TestMergeSizingKeepsOneArray(t *testing.T) {
	in, _ := arrayFixture(t)
	a := Size(in)
	b := Size(in)
	m := MergeSizing([]*Sizing{a, b})
	if len(m.Storage.Arrays) != 1 || m.Storage.Array.Datastores != 4 || m.Storage.Arrays[0].Matched != 4 {
		t.Fatalf("merged %+v", m.Storage.Array)
	}
	if math.Abs(m.Storage.Array.Ratio()-a.Storage.Array.Ratio()) > 1e-9 || m.Storage.Array.PlanPhysical == 0 {
		t.Fatalf("merged ratio %v vs %v", m.Storage.Array.Ratio(), a.Storage.Array.Ratio())
	}
}

func TestExplainMatches(t *testing.T) {
	in, inv := arrayFixture(t)
	in.Estate.Datastores = append(in.Estate.Datastores, vc.DatastoreDetail{Name: "other-netapp", Type: "VMFS", Version: "VMFS 6", Accessible: true,
		LUNs: []vc.LUN{{Name: ontap.NAA("zzzzzz000001"), Vendor: "NETAPP"}}})
	rows := ExplainMatches(inv, map[string]*vc.Estate{"vc01": in.Estate})
	got := map[string]MatchRow{}
	for _, r := range rows {
		got[r.Datastore] = r
	}
	for name, want := range map[string]string{
		"ds-fc":        "matched",
		"nfs-01":       "matched",
		"other":        "export address 10.9.9.9 is not a data interface",
		"local-esx0":   "skipped: Local datastore",
		"other-netapp": `NetApp LUN with serial "zzzzzz000001" is not on this cluster`,
	} {
		if r := got[name]; !strings.HasPrefix(r.Result, want) || r.VCenter != "vc01" {
			t.Fatalf("%s: %+v, want %q", name, r, want)
		}
	}
	if got["ds-fc"].Volume != "svm_vmware/ds_fc" {
		t.Fatalf("volume %q", got["ds-fc"].Volume)
	}
}
