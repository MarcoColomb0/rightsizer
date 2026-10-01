// Package sample builds synthetic vCenter estates with realistic load
// patterns, for the demo console and for report tests.
package sample

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

// Config describes one synthetic vCenter.
type Config struct {
	Seed     uint64
	VCenter  string
	About    string
	Clusters []string
	// VMs per cluster.
	VMs int
	// Load scales CPU demand; 1 is a typical, oversized estate.
	Load float64
	End  time.Time
	Days int
}

// Default is the estate used by report tests.
func Default() Config {
	return Config{
		Seed: 1, VCenter: "vcsa01.corp.local", About: "VMware vCenter Server 8.0.3 build-24322831",
		Clusters: []string{"prod-cl01", "prod-cl02", "dmz-cl01"}, VMs: 25, Load: 1,
		End: time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC), Days: 14,
	}
}

// Data is everything one synthetic vCenter produces.
type Data struct {
	Config Config
	Input  analysis.Input
	Estate *vc.Estate
}

var guests = []string{"Red Hat Enterprise Linux 9", "Microsoft Windows Server 2022 (64-bit)", "Ubuntu Linux (64-bit)"}

// Generate builds the inventory, statistics and hardware of one vCenter.
func Generate(c Config) Data {
	rng := rand.New(rand.NewPCG(c.Seed, c.Seed*7+2)) //nolint:gosec // synthetic data must repeat for a given seed
	end := c.End
	start := end.Add(-time.Duration(c.Days) * 24 * time.Hour)
	samples := c.Days * 24 * 180
	inv := &vc.Inventory{Taken: end}
	st := analysis.NewStore(start)
	est := &vc.Estate{Taken: end}
	for ci, name := range c.Clusters {
		hosts := 4 + 2*(1-ci/2)
		for h := range hosts {
			host := vc.Host{Ref: fmt.Sprintf("host-%d-%d", ci, h), Name: fmt.Sprintf("esx%02d.%s", h+1, name), Cluster: name,
				CPUModel: "Intel(R) Xeon(R) Gold 6338 CPU @ 2.00GHz", Sockets: 2, Cores: 64, NUMANodes: 2, MHz: 2000, MemBytes: 1024 << 30, Connected: true}
			inv.Hosts = append(inv.Hosts, host)
			est.Hosts = append(est.Hosts, vc.HostDetail{Name: host.Name, Cluster: name, Vendor: "Dell Inc.", Model: "PowerEdge R650",
				Serial: fmt.Sprintf("SN%d%03d", ci, h), BIOS: "1.9.2", BIOSDate: time.Date(2022, 3, 1, 0, 0, 0, 0, time.UTC),
				CPUModel: host.CPUModel, Sockets: 2, Cores: 64, Threads: 128, MHz: 2000, MemBytes: host.MemBytes,
				ESXi: "VMware ESXi 8.0.3 build-24280767", HTActive: true, Connected: true,
				NICs: []vc.NIC{{Device: "vmnic0", SpeedMb: 25000, Switch: "dvs-prod"}, {Device: "vmnic1", SpeedMb: 25000, Switch: "dvs-prod"}, {Device: "vmnic2", SpeedMb: 1000}, {Device: "vmnic3"}},
				HBAs: []vc.HBA{{Device: "vmhba2", Type: "FC", SpeedGb: 16, WWPN: fmt.Sprintf("20:00:00:25:b5:%02x:00:%02x", ci, h)}, {Device: "vmhba3", Type: "FC", SpeedGb: 16}},
				VMKs: []vc.VMK{{Device: "vmk0", MTU: 1500, Services: []string{"management"}}, {Device: "vmk1", MTU: 9000, Services: []string{"vmotion"}}}})
		}
		est.Clusters = append(est.Clusters, vc.ClusterDetail{Name: name, Hosts: hosts, HA: true, Admission: "N+1 (25% CPU, 25% memory)", DRS: "fullyAutomated", EVC: "intel-icelake"})
		prefix := strings.ReplaceAll(name, "-cl", "")
		for v := range c.VMs {
			vm := vc.VM{Ref: fmt.Sprintf("vm-%d-%d", ci, v), UUID: fmt.Sprintf("uuid-%d-%d-%d", c.Seed, ci, v),
				Name:    fmt.Sprintf("%s-%s%02d", prefix, []string{"web", "app", "db", "svc"}[v%4], v+1),
				Cluster: name, GuestOS: guests[v%len(guests)], PowerOn: v%11 != 10,
				VCPU: []int{2, 4, 8, 16}[rng.IntN(4)], MemMB: []int{4096, 8192, 16384, 32768, 65536}[rng.IntN(5)],
				Host: fmt.Sprintf("esx%02d.%s", v%4+1, name), CoresPerSock: 1,
				Committed: int64(40+rng.IntN(400)) << 30, Disks: []vc.Disk{{Capacity: 200 << 30, Thin: v%4 != 0, Datastore: "ds-prod-01"}},
				GuestDisks: []vc.GuestDisk{{Path: "/", Capacity: 200 << 30, Free: int64(50+rng.IntN(140)) << 30}}}
			if v%7 == 3 {
				vm.Snapshots = []vc.Snapshot{{Name: "before patch", Created: end.Add(-time.Duration(4+rng.IntN(40)) * 24 * time.Hour)}}
				vm.SnapshotBytes = int64(5+rng.IntN(90)) << 30
			}
			vm.SwapBytes = int64(vm.MemMB) << 20
			vm.DiskBytes = max(vm.Committed-vm.SnapshotBytes-vm.SwapBytes-(1<<30), 0)
			base, memb := rng.Float64()*30*c.Load, 5+rng.Float64()*60
			if v%9 == 5 {
				base = 0.3
			}
			if v == 13 {
				base, vm.VCPU = 70, 40
			}
			if ci == 0 && v == 5 {
				vm.Disks = append(vm.Disks, vc.Disk{Capacity: 2 << 40, RDM: "physical"})
			}
			if ci == 0 && v == 7 {
				vm.VGPU = []string{"grid_a40-8q"}
			}
			inv.VMs = append(inv.VMs, vm)
			if !vm.PowerOn {
				continue
			}
			st.VMs[vm.Ref] = vmStats(rng, start, end, samples, c.Days, vm.VCPU, base, memb, v%9 == 5, v%4 == 3)
		}
		_, cp := analysis.Capacity(inv)
		cs := &analysis.ClusterStats{}
		for t := start; t.Before(end); t = t.Add(5 * time.Minute) {
			d := math.Sin(float64(t.Hour())/24*2*math.Pi-math.Pi/2)*0.5 + 0.5
			cpu := cp[name].MHz * min((0.12+0.25*d+rng.Float64()*0.05)*c.Load, 0.95)
			mem := cp[name].MemB * (0.55 + rng.Float64()*0.05)
			cs.CPU.Add(cpu / cp[name].MHz * 100)
			cs.Mem.Add(mem / cp[name].MemB * 100)
			cs.Points = append(cs.Points, analysis.Point{T: t, CPUMHz: cpu, MemB: mem})
		}
		for i := range 1000 {
			cs.Net.AddN(float64(200000+i*800), 1)
			cs.IOPS.AddN(float64(5000+i*10), 1)
			cs.KBps.AddN(float64(120000+i*300), 1)
		}
		st.Clusters[name] = cs
	}
	storage(st, est, start, end)
	in := analysis.Input{Inv: inv, RT: st, Profile: analysis.ProfileByName("balanced"), Start: start, End: end,
		Planned: time.Duration(c.Days) * 24 * time.Hour,
		Exclusions: []analysis.Exclusion{
			{ID: "1", Name: inv.VMs[len(inv.VMs)-3].Name, Reason: "Vendor requirement", Note: "Vendor sizing guide for the appliance requires 8 vCPU and 32 GB.", Created: end.Add(-40 * 24 * time.Hour)},
		},
		Orphans: []vc.OrphanDisk{{Datastore: "ds-prod-01", Path: "[ds-prod-01] old-sql02/old-sql02.vmdk", Size: 412 << 30, Modified: end.Add(-200 * 24 * time.Hour)}}}
	return Data{Config: c, Input: in, Estate: est}
}

func vmStats(rng *rand.Rand, start, end time.Time, samples, days, vcpu int, base, memb float64, idle, night bool) *analysis.VMStats {
	s := &analysis.VMStats{First: start, Last: end}
	period := float64(24 * 180)
	for i := range samples {
		d := math.Sin(float64(i)/period*2*math.Pi)*0.5 + 0.5
		s.CPU.Add(base * (0.4 + d + rng.Float64()*0.4))
		s.Mem.Add(memb * (0.9 + rng.Float64()*0.2))
		n := 1.0
		if idle {
			n = 0
		}
		s.Net.Add(n * 200)
		s.Disk.Add(n * 300)
		s.Ready.Add(rng.Float64() * 3)
		s.ReadIOPS.Add(n * (40 + rng.Float64()*120))
		s.WriteIOPS.Add(n * (15 + rng.Float64()*60))
		s.Samples++
	}
	for slot := range days * 48 {
		hour := float64(slot%48) / 2
		d := math.Max(0, math.Sin((hour-6)/12*math.Pi))
		if night {
			d = math.Max(0, math.Sin((hour-18)/12*math.Pi))
		}
		s.Demand = append(s.Demand, float32(base/100*float64(vcpu)*2000*(0.2+0.8*d)*(0.9+rng.Float64()*0.2)))
		s.Slots = append(s.Slots, 1)
	}
	return s
}

func storage(st *analysis.Store, est *vc.Estate, start, end time.Time) {
	var all []string
	for _, h := range est.Hosts {
		all = append(all, h.Name)
	}
	st.IO[""] = &analysis.IOStats{}
	for i := range 4 {
		id := fmt.Sprintf("ds%d", i)
		est.Datastores = append(est.Datastores, vc.DatastoreDetail{Name: fmt.Sprintf("ds-prod-%02d", i+1), Type: "VMFS", Version: "VMFS 6.82", ID: id,
			Capacity: 20 << 40, Free: int64(4+i*2) << 40, Uncommitted: 8 << 40, Accessible: true, Hosts: all, VMs: 20,
			LUNs: []vc.LUN{{Name: "naa.600a0980383030", Vendor: "ACME", Model: "Array 9000", Capacity: 20 << 40, Transport: "FC", Paths: 4, Policy: "VMW_PSP_RR"}}})
		st.IO[id] = &analysis.IOStats{}
	}
	est.Datastores = append(est.Datastores, vc.DatastoreDetail{Name: "nfs-backup", Type: "NFS", Version: "NFS 3", ID: "nfs",
		Capacity: 40 << 40, Free: 12 << 40, Accessible: true, Hosts: all, Remote: "10.0.40.10:/vol/backup"})
	tot := st.IO[""]
	for t := start; t.Before(end); t = t.Add(5 * time.Minute) {
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
}

// Result analyses the data the way the engine does, with history accuracy
// and an earlier peak to show every section.
func Result(d Data) *analysis.Result {
	r := analysis.Analyze(d.Input)
	r.Final = true
	r.Accuracy = &analysis.Accuracy{VMs: 64, HoursBoth: 330, Percentile: 95, CPUMedian: 0.66, MemMedian: 0.94,
		Bursty: []analysis.Burst{{VM: d.Input.Inv.VMs[13].Name, RealtimeP: 81, HistoryP: 37, RealtimeMx: 100}}}
	if len(r.Clusters) > 0 {
		r.Clusters[0].EarlierPeak = &analysis.EarlierPeak{At: d.Input.Start.Add(-4*24*time.Hour + 14*time.Hour), Pct: 71, WindowPct: 48}
	}
	r.VCenter = d.Config.VCenter + " — " + d.Config.About
	return r
}

// Sizing computes the hardware refresh sizing of the data.
func Sizing(d Data, r *analysis.Result, p analysis.SizingParams) *analysis.Sizing {
	sz := analysis.Size(analysis.SizingInput{Input: d.Input, Result: r, Estate: d.Estate, Params: p})
	sz.VCenter = r.VCenter
	sz.Final = r.Final
	return sz
}
