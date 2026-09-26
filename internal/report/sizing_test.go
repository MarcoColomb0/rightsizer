package report

import (
	"archive/zip"
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

func demoSizing() *analysis.Sizing {
	in := demoInput()
	in.Inv.VMs[3].Name = "=HYPERLINK(\"http://x\")"
	for i := range in.Inv.VMs {
		vm := &in.Inv.VMs[i]
		vm.SwapBytes = int64(vm.MemMB) << 20
		vm.DiskBytes = max(vm.Committed-vm.SnapshotBytes-vm.SwapBytes-(1<<30), 0)
		if i%3 == 0 {
			vm.GuestOS = "Microsoft Windows Server 2022 (64-bit)"
		}
	}
	in.Inv.VMs[5].Disks = append(in.Inv.VMs[5].Disks, vc.Disk{Capacity: 2 << 40, RDM: "physical"})
	in.Inv.VMs[7].VGPU = []string{"grid_a40-8q"}
	est := &vc.Estate{Taken: in.End}
	for _, name := range []string{"prod-cl01", "prod-cl02", "dmz-cl01"} {
		est.Clusters = append(est.Clusters, vc.ClusterDetail{Name: name, Hosts: 4, HA: true, Admission: "N+1 (25% CPU, 25% memory)", DRS: "fullyAutomated", EVC: "intel-icelake"})
	}
	for i, h := range in.Inv.Hosts {
		est.Hosts = append(est.Hosts, vc.HostDetail{Name: h.Name, Cluster: h.Cluster, Vendor: "Dell Inc.", Model: "PowerEdge R650", Serial: fmt.Sprintf("SN%04d", i),
			BIOS: "1.9.2", BIOSDate: time.Date(2022, 3, 1, 0, 0, 0, 0, time.UTC), CPUModel: h.CPUModel, Sockets: 2, Cores: 64, Threads: 128, MHz: 2000, MemBytes: h.MemBytes,
			ESXi: "VMware ESXi 8.0.3 build-24280767", Connected: true,
			NICs: []vc.NIC{{Device: "vmnic0", SpeedMb: 25000}, {Device: "vmnic1", SpeedMb: 25000}, {Device: "vmnic2", SpeedMb: 1000}, {Device: "vmnic3"}},
			HBAs: []vc.HBA{{Device: "vmhba2", Type: "FC", SpeedGb: 16, WWPN: "20:00:00:25:b5:00:00:01"}, {Device: "vmhba3", Type: "FC", SpeedGb: 16}},
			VMKs: []vc.VMK{{Device: "vmk0", MTU: 1500, Services: []string{"management"}}, {Device: "vmk1", MTU: 9000, Services: []string{"vmotion"}}}})
	}
	var all []string
	for _, h := range est.Hosts {
		all = append(all, h.Name)
	}
	st := in.RT
	st.IO[""] = &analysis.IOStats{}
	for i := range 4 {
		id := fmt.Sprintf("ds%d", i)
		est.Datastores = append(est.Datastores, vc.DatastoreDetail{Name: fmt.Sprintf("ds-prod-%02d", i+1), Type: "VMFS", Version: "VMFS 6.82", ID: id,
			Capacity: 20 << 40, Free: int64(4+i*2) << 40, Uncommitted: 8 << 40, Accessible: true, Hosts: all, VMs: 20,
			LUNs: []vc.LUN{{Name: "naa.600a0980383030", Vendor: "ACME", Model: "Array 9000", Capacity: 20 << 40, Transport: "FC", Paths: 4, Policy: "VMW_PSP_RR"}}})
		st.IO[id] = &analysis.IOStats{}
	}
	est.Datastores = append(est.Datastores, vc.DatastoreDetail{Name: "nfs-backup", Type: "NFS", Version: "NFS 3", ID: "nfs", Capacity: 40 << 40, Free: 12 << 40, Accessible: true, Hosts: all, Remote: "10.0.40.10:/vol/backup"})
	tot := st.IO[""]
	start := in.Start
	for t := start; t.Before(in.End); t = t.Add(5 * time.Minute) {
		d := math.Sin(float64(t.Hour())/24*2*math.Pi-math.Pi/2)*0.5 + 0.5
		iops := 8000 + 30000*d
		tot.IOPS.AddN(iops, 15)
		tot.KBps.AddN(iops*24, 15)
		tot.Latency.AddN(0.8+2*d, 15)
		tot.Read.AddN(iops*0.7, 15)
		tot.Write.AddN(iops*0.3, 15)
		tot.ReadKB.AddN(iops*0.7*20, 15)
		tot.WriteKB.AddN(iops*0.3*32, 15)
		tot.SizeKB += iops * 24 * 15
		tot.SizeOps += iops * 15
		tot.Points = append(tot.Points, analysis.IOPoint{T: t, IOPS: iops, KBps: iops * 24})
		st.IO["ds0"].IOPS.AddN(iops/2, 15)
		st.IO["ds0"].KBps.AddN(iops*12, 15)
		st.IO["ds0"].Latency.AddN(1+d, 15)
	}
	for name, cs := range st.Clusters {
		for i := range 1000 {
			cs.Net.AddN(float64(200000+i*800), 1)
			cs.IOPS.AddN(float64(5000+i*10), 1)
			cs.KBps.AddN(float64(120000+i*300), 1)
		}
		_ = name
	}
	r := analyzeDemo(in)
	p := analysis.DefaultSizing()
	p.Groups = "databases=*db*; web=*web*"
	sz := analysis.Size(analysis.SizingInput{Input: in, Result: r, Estate: est, Params: p})
	sz.VCenter = r.VCenter
	sz.Final = true
	return sz
}

func TestWriteSizing(t *testing.T) {
	dir := t.TempDir()
	out := os.Getenv("RIGHTSIZER_DEMO_SIZING")
	if out == "" {
		out = filepath.Join(dir, "sizing.pdf")
	}
	sz := demoSizing()
	if len(sz.Clusters) != 3 || sz.Totals.For(sz.Params.Basis).Nodes == 0 || !sz.Storage.IO.Available {
		t.Fatalf("demo sizing incomplete: %+v", sz.Totals)
	}
	if err := WriteSizingPDF(sz, out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil || !strings.HasPrefix(string(b), "%PDF") {
		t.Fatalf("invalid pdf: %v", err)
	}
	data := filepath.Join(dir, "data.zip")
	if err := WriteSizingData(sz, data); err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(data)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	files := map[string][][]string{}
	for _, f := range z.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		rows, err := csv.NewReader(rc).ReadAll()
		rc.Close()
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		files[f.Name] = rows
	}
	for _, n := range []string{"vms.csv", "hosts.csv", "adapters.csv", "vmkernel.csv", "datastores.csv", "clusters.csv", "node-options.csv", "needs.csv", "workloads.csv", "storage-summary.csv"} {
		if len(files[n]) < 2 {
			t.Fatalf("%s has no rows", n)
		}
	}
	if len(files["vms.csv"]) != len(sz.VMs)+1 {
		t.Fatalf("vms.csv: %d rows for %d VMs", len(files["vms.csv"]), len(sz.VMs))
	}
	for _, r := range files["vms.csv"] {
		if strings.HasPrefix(r[0], "=") {
			t.Fatalf("formula not neutralised: %q", r[0])
		}
	}
}

func TestCell(t *testing.T) {
	for in, want := range map[string]string{"=1+1": "'=1+1", "-5": "-5", "+cmd": "'+cmd", "@x": "'@x", "web": "web", "": ""} {
		if got := cell(in); got != want {
			t.Fatalf("cell(%q) = %q, want %q", in, got, want)
		}
	}
}
