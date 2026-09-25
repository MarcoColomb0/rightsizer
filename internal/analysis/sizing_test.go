package analysis

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

func TestLogHist(t *testing.T) {
	var h LogHist
	for i := 1; i <= 1000; i++ {
		h.AddN(float64(i), 1)
	}
	if p := h.Pct(95); math.Abs(p-950)/950 > 0.02 {
		t.Fatalf("p95 = %v", p)
	}
	if h.Pct(100) != 1000 || math.Abs(h.Avg()-500.5) > 1e-9 {
		t.Fatalf("max %v avg %v", h.Pct(100), h.Avg())
	}
	var z LogHist
	z.AddN(0, 3)
	if z.Pct(95) != 0 {
		t.Fatalf("zero p95 = %v", z.Pct(95))
	}
}

func ioSeries(ref string, ts []time.Time, vals map[string]map[string]float64, net float64) vc.Series {
	s := vc.Series{Ref: ref, Interval: 20, TS: ts, Values: map[string][]float64{}, Inst: map[string]map[string][]float64{}}
	for range ts {
		s.Values[vc.CPUMHz] = append(s.Values[vc.CPUMHz], 1000)
		s.Values[vc.MemConsume] = append(s.Values[vc.MemConsume], 1024)
		s.Values[vc.NetUsage] = append(s.Values[vc.NetUsage], net)
	}
	for m, byInst := range vals {
		s.Inst[m] = map[string][]float64{}
		for inst, v := range byInst {
			for range ts {
				s.Inst[m][inst] = append(s.Inst[m][inst], v)
			}
		}
	}
	return s
}

func TestIOSummedAcrossHosts(t *testing.T) {
	start := time.Now().Add(-time.Hour).Truncate(5 * time.Minute)
	ts := []time.Time{start.Add(20 * time.Second), start.Add(40 * time.Second), start.Add(60 * time.Second)}
	st := NewStore(start)
	h1 := ioSeries("h1", ts, map[string]map[string]float64{
		vc.ReadIOPS: {"a": 100}, vc.WriteIOPS: {"a": 50}, vc.ReadKBps: {"a": 800}, vc.WriteKBps: {"a": 400},
		vc.ReadLat: {"a": 2}, vc.WriteLat: {"a": 4},
	}, 1000)
	h2 := ioSeries("h2", ts, map[string]map[string]float64{
		vc.ReadIOPS: {"a": 100, "b": 10}, vc.WriteIOPS: {"a": 50, "b": 0}, vc.ReadKBps: {"a": 800, "b": 80}, vc.WriteKBps: {"a": 400, "b": 0},
		vc.ReadLat: {"a": 2, "b": 10}, vc.WriteLat: {"a": 4, "b": 0},
	}, 3000)
	hc := map[string]string{"h1": "cl", "h2": "cl"}
	st.AddHosts([]vc.Series{h1, h2}, hc, map[string]clusterCap{"cl": {MHz: 10000, MemB: 1 << 40}})
	st.Flush()
	tot := st.IO[""]
	if tot == nil || tot.IOPS.Max != 310 || tot.IOPS.N != 3 {
		t.Fatalf("total IOPS: %+v", tot)
	}
	if a := st.IO["a"]; a.IOPS.Max != 300 || math.Abs(a.ReadShare()-2.0/3) > 1e-9 {
		t.Fatalf("datastore a: %+v", a)
	}
	// (2×200 + 4×100 + 10×10) / 310
	if lat := tot.Latency.Max; math.Abs(lat-900.0/310) > 1e-9 {
		t.Fatalf("latency %v", lat)
	}
	if kb := tot.IOSize(); math.Abs(kb-2480.0/310) > 1e-9 {
		t.Fatalf("io size %v", kb)
	}
	cs := st.Clusters["cl"]
	if cs.Net.Max != 4000 || cs.IOPS.Max != 310 || cs.KBps.Max != 2480 {
		t.Fatalf("cluster net %v iops %v kbps %v", cs.Net.Max, cs.IOPS.Max, cs.KBps.Max)
	}
	if len(tot.Points) != 1 || len(st.IO["a"].Points) != 0 {
		t.Fatalf("timeline kept for the total only: %d, %d", len(tot.Points), len(st.IO["a"].Points))
	}
	st.AddHosts([]vc.Series{h1}, hc, nil)
	if tot.IOPS.N != 3 {
		t.Fatal("samples already seen must be ignored")
	}
}

func sizingFixture() SizingInput {
	inv := &vc.Inventory{}
	for i := 0; i < 4; i++ {
		inv.Hosts = append(inv.Hosts, vc.Host{Ref: fmt.Sprintf("h%d", i), Name: fmt.Sprintf("esx%d", i), Cluster: "prod",
			CPUModel: "Intel(R) Xeon(R) Gold 6130", Sockets: 2, Cores: 32, Threads: 64, MHz: 2600, MemBytes: 512 << 30, NUMANodes: 2, Connected: true})
	}
	for i := 0; i < 39; i++ {
		os := "Microsoft Windows Server 2022 (64-bit)"
		if i%2 == 1 {
			os = "Ubuntu Linux (64-bit)"
		}
		inv.VMs = append(inv.VMs, vc.VM{Ref: fmt.Sprintf("vm%d", i), Name: fmt.Sprintf("app-%02d", i), Cluster: "prod", PowerOn: true,
			GuestOS: os, VCPU: 8, MemMB: 32 << 10, Committed: 100 << 30, Uncommitted: 50 << 30, DiskBytes: 90 << 30, SwapBytes: 8 << 30,
			Disks: []vc.Disk{{Capacity: 150 << 30, Thin: true, Datastore: "ds-fc"}}, GuestDisks: []vc.GuestDisk{{Capacity: 150 << 30, Free: 100 << 30}}})
	}
	inv.VMs = append(inv.VMs,
		vc.VM{Ref: "sql", Name: "sql-01", Cluster: "prod", PowerOn: true, GuestOS: "Microsoft Windows Server 2022 (64-bit)", VCPU: 32, MemMB: 256 << 10,
			Committed: 210 << 30, DiskBytes: 200 << 30, MemReserveMB: 256 << 10,
			Disks: []vc.Disk{{Capacity: 200 << 30, Datastore: "ds-fc"}, {Capacity: 1 << 40, RDM: "physical", Datastore: "ds-fc"}}},
		vc.VM{Ref: "off", Name: "old-01", Cluster: "prod", GuestOS: "Red Hat Enterprise Linux 7", VCPU: 4, MemMB: 16 << 10, Committed: 50 << 30, DiskBytes: 50 << 30},
		vc.VM{Ref: "tpl", Name: "tpl-w2022", Cluster: "prod", Template: true, VCPU: 2, MemMB: 4096, Committed: 20 << 30},
		vc.VM{Ref: "gpu", Name: "cad-01", Cluster: "prod", GuestOS: "Microsoft Windows 11", VGPU: []string{"grid_a40-8q"}, VCPU: 4, MemMB: 16 << 10},
	)
	est := &vc.Estate{Taken: time.Now(), Clusters: []vc.ClusterDetail{{Name: "prod", Hosts: 4, HA: true, EVC: "intel-skylake"}}}
	for _, h := range inv.Hosts {
		est.Hosts = append(est.Hosts, vc.HostDetail{Name: h.Name, Cluster: "prod", Sockets: 2, Cores: 32, MemBytes: h.MemBytes, ESXi: "VMware ESXi 8.0.3",
			NICs: []vc.NIC{{Device: "vmnic0", SpeedMb: 10000}, {Device: "vmnic1", SpeedMb: 10000}, {Device: "vmnic2"}},
			HBAs: []vc.HBA{{Device: "vmhba1", Type: "FC", SpeedGb: 16}, {Device: "vmhba2", Type: "FC", SpeedGb: 16}},
			VMKs: []vc.VMK{{Device: "vmk2", MTU: 9000, Storage: true}}})
	}
	all := []string{"esx0", "esx1", "esx2", "esx3"}
	est.Datastores = []vc.DatastoreDetail{
		{Name: "ds-fc", Type: "VMFS", Version: "VMFS 6.82", ID: "a", Capacity: 10 << 40, Free: 6 << 40, Accessible: true, Hosts: all,
			LUNs: []vc.LUN{{Name: "naa.600a", Vendor: "ACME", Model: "Array", Transport: "FC", Paths: 4, Policy: "VMW_PSP_RR"}}},
		{Name: "nfs-01", Type: "NFS", Version: "NFS 3", ID: "b", Capacity: 5 << 40, Free: 1 << 40, Accessible: true, Hosts: all, Remote: "10.0.0.5:/vol/nfs01"},
	}
	res := &Result{Profile: ProfileByName("balanced"), VCenter: "vc01",
		Clusters: []ClusterResult{{Name: "prod", CPUP: 30, CPUPeak: 50, MemP: 60, CapMHz: 128 * 2600, CapMemB: 2 << 40}}}
	return SizingInput{Input: Input{Inv: inv, Profile: res.Profile}, Result: res, Estate: est, Params: DefaultSizing()}
}

func TestSizingCompute(t *testing.T) {
	in := sizingFixture()
	in.Params.Groups = "databases=sql*"
	sz := Size(in)
	if len(sz.Clusters) != 1 {
		t.Fatalf("clusters: %+v", sz.Clusters)
	}
	c := sz.Clusters[0]
	if c.VCPU != 344 || c.MemMB != 1504<<10 || c.VMsOff != 2 || c.Cores != 128 {
		t.Fatalf("today: vcpu %d mem %d off %d cores %d", c.VCPU, c.MemMB, c.VMsOff, c.Cores)
	}
	n := c.Needs[0]
	if n.Basis != BasisProvisioned || n.VCPU != 413 || n.Ratio != 4 || n.CoresByRatio != 104 || n.CoresByDemand != 66 || n.Cores != 104 {
		t.Fatalf("need: %+v", n)
	}
	o, ok := n.Picked()
	if !ok || o.Nodes != 5 || o.CoresPerSocket != 16 || o.MemGB != 768 || o.TotalCores != 160 {
		t.Fatalf("pick %+v of %+v", o, n.Options)
	}
	if math.Abs(o.CPUUtil-36) > 0.1 || math.Abs(o.MinGHz-1.337) > 0.01 || o.NUMAFit {
		t.Fatalf("option figures %+v", o)
	}
	for _, x := range n.Options {
		if x.Sockets*x.CoresPerSocket < 32 {
			t.Fatalf("option smaller than the largest VM: %+v", x)
		}
	}
	if pp := n.Ports; pp.StorageKind != "FC" || pp.StorageGb != 32 || pp.DataGb != 25 || pp.DataPorts != 2 || pp.OOBPorts != 1 {
		t.Fatalf("ports %+v", pp)
	}
	if nt := sz.Totals.For(BasisProvisioned); nt.Nodes != 5 || nt.Cores != 160 || nt.StoragePorts["32G FC"] != 10 {
		t.Fatalf("totals %+v", nt)
	}
	if c.Links.NICs != "8 × 10 GbE" || c.Links.NICsDown != 4 || c.Links.HBAs != "8 × FC 16G" || c.Links.StorageMTU != "9000" {
		t.Fatalf("connectivity %+v", c.Links)
	}

	st := sz.Storage
	const gb = 1 << 30
	wantRaw := int64(39*(90+2)+210+50+20)*gb + 1<<40
	if st.RawUsed != wantRaw {
		t.Fatalf("raw used %s, want %s", Human(st.RawUsed), Human(wantRaw))
	}
	if st.Swap != 39*8*gb || st.RDM != 1<<40 || st.PhysicalRDM != 1 || st.Templates != 20*gb || st.PoweredOff != 50*gb {
		t.Fatalf("storage breakdown %+v", st)
	}
	if want := int64(float64(wantRaw) * 1.2 / 0.8); st.Plan != want {
		t.Fatalf("plan %d want %d", st.Plan, want)
	}
	if st.Used != 8<<40 || st.Capacity != 15<<40 || len(st.ByType) != 2 {
		t.Fatalf("datastores %+v", st)
	}
	wl := map[string]WorkloadSizing{}
	for _, w := range sz.Workloads {
		wl[w.Name] = w
	}
	if wl["databases"].VMs != 1 || wl["databases"].Used != 210*gb+1<<40 || wl["Windows Server"].VMs != 20 || wl["Linux"].VMs != 20 || wl["Templates"].VMs != 1 || wl["Windows desktop"].VMs != 1 {
		t.Fatalf("workloads %+v", sz.Workloads)
	}
	joined := strings.Join(sz.Notes, "\n")
	for _, want := range []string{"Intel", "intel-skylake", "sql-01", "grid_a40-8q", "raw device mappings", "reserve", "No storage performance data"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("notes miss %q:\n%s", want, joined)
		}
	}
	if _, err := json.Marshal(sz); err != nil {
		t.Fatal(err)
	}
}

func TestSizingRightsizedAndPoweredOff(t *testing.T) {
	in := sizingFixture()
	in.Result.VMs = []VMResult{{Ref: "sql", Name: "sql-01", RecVCPU: 16, RecMemMB: 128 << 10}}
	in.Params.PoweredOff = true
	in.Params.Growth = 0
	sz := Size(in)
	prov, right := sz.Clusters[0].Needs[0], sz.Clusters[0].Needs[1]
	if prov.VCPU != 344+8 || prov.MemMB != (1504+32)<<10 {
		t.Fatalf("powered-off VMs must count: %+v", prov)
	}
	if right.VCPU != 344+8-16 || right.MemMB != (1504+32-128)<<10 {
		t.Fatalf("rightsized: %+v", right)
	}
	if nt := sz.Totals.For(BasisRightsized); nt.Nodes == 0 {
		t.Fatalf("totals %+v", sz.Totals)
	}
}

func TestSizingParams(t *testing.T) {
	if err := DefaultSizing().Validate(); err != nil {
		t.Fatal(err)
	}
	bad := []func(*SizingParams){
		func(p *SizingParams) { p.Basis = "x" },
		func(p *SizingParams) { p.Ratio = 0.5 },
		func(p *SizingParams) { p.Sockets = 3 },
		func(p *SizingParams) { p.FreeSpace = 90 },
		func(p *SizingParams) { p.Groups = "db" },
		func(p *SizingParams) { p.Groups = "db=[" },
	}
	for i, f := range bad {
		p := DefaultSizing()
		f(&p)
		if p.Validate() == nil {
			t.Fatalf("case %d must fail", i)
		}
	}
	g, err := parseGroups(" db = SQL*, *ora* ; vdi=vdi-* ")
	if err != nil || len(g) != 2 || g[0].name != "db" || g[0].patterns[0] != "sql*" {
		t.Fatalf("%+v %v", g, err)
	}
	if w := workloadOf(g, vc.VM{Name: "PROD-ORA-01"}); w != "db" {
		t.Fatalf("got %s", w)
	}
}

func TestSizingStandaloneAndEmpty(t *testing.T) {
	if sz := Size(SizingInput{}); sz == nil || len(sz.Clusters) != 0 {
		t.Fatal("empty input")
	}
	inv := &vc.Inventory{
		Hosts: []vc.Host{{Ref: "h", Name: "esx", Cluster: "standalone/esx", Sockets: 1, Cores: 8, MHz: 2000, MemBytes: 64 << 30, Connected: true}},
		VMs:   []vc.VM{{Ref: "v", Name: "v", Cluster: "standalone/esx", PowerOn: true, VCPU: 2, MemMB: 4096}},
	}
	sz := Size(SizingInput{Input: Input{Inv: inv}, Params: DefaultSizing()})
	o, ok := sz.Clusters[0].Needs[0].Picked()
	if !ok || o.Nodes != 1 {
		t.Fatalf("standalone host needs no spare: %+v", sz.Clusters[0].Needs[0])
	}
	if _, err := json.Marshal(sz); err != nil {
		t.Fatal(err)
	}
}

func TestSizingUnsized(t *testing.T) {
	in := sizingFixture()
	in.Inv.VMs = append(in.Inv.VMs, vc.VM{Ref: "huge", Name: "hana-01", Cluster: "prod", PowerOn: true, VCPU: 300, MemMB: 6 << 20})
	sz := Size(in)
	n := sz.Clusters[0].Needs[0]
	if !n.Unsized() || len(n.Options) != 0 {
		t.Fatalf("a 300 vCPU VM fits no node: %+v", n)
	}
	if !strings.Contains(sz.Notes[0], "prod: no node shape fits") || !strings.Contains(sz.Notes[0], "300 vCPU") {
		t.Fatalf("unsized cluster must lead the notes: %q", sz.Notes[0])
	}
	if nt := sz.Totals.For(BasisProvisioned); nt.Nodes != 0 {
		t.Fatalf("totals %+v", nt)
	}
	var pe *ParamError
	p := DefaultSizing()
	p.CPUTarget = 150
	if err := p.Validate(); !errors.As(err, &pe) || pe.Field != "CPUTarget" {
		t.Fatalf("want a CPUTarget error, got %v", err)
	}
	p = DefaultSizing()
	p.Groups = "x"
	if err := p.Validate(); !errors.As(err, &pe) || pe.Field != "Groups" {
		t.Fatalf("want a Groups error, got %v", err)
	}
}

func TestIOWithoutThroughput(t *testing.T) {
	start := time.Now().Add(-2 * time.Hour).Truncate(5 * time.Minute)
	ts := []time.Time{start.Add(5 * time.Minute), start.Add(10 * time.Minute)}
	st := NewStore(start)
	// History at a statistics level that keeps operations and latency only.
	h := ioSeries("h1", ts, map[string]map[string]float64{vc.ReadIOPS: {"a": 300}, vc.WriteIOPS: {"a": 100}, vc.ReadLat: {"a": 2}, vc.WriteLat: {"a": 2}}, 0)
	h.Interval = 300
	st.AddHosts([]vc.Series{h}, map[string]string{"h1": "cl"}, map[string]clusterCap{"cl": {MHz: 1000, MemB: 1 << 30}})
	sum := ioSummary(st.IO[""], 95)
	if !sum.Available || sum.Throughput || sum.IOSizeKB != 0 || !sum.Latency || sum.IOPS < 395 {
		t.Fatalf("missing throughput must not read as zero: %+v", sum)
	}
	if st.Clusters["cl"].KBps.N != 0 || st.Clusters["cl"].IOPS.N == 0 {
		t.Fatal("cluster throughput must stay empty when not reported")
	}
}
