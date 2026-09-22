package report

import (
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

func demo() *analysis.Result {
	rng := rand.New(rand.NewPCG(1, 2))
	end := time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)
	start := end.Add(-14 * 24 * time.Hour)
	inv := &vc.Inventory{Taken: end}
	st := analysis.NewStore(start)
	for c, name := range []string{"prod-cl01", "prod-cl02", "dmz-cl01"} {
		for h := 0; h < 4+2*(1-c/2); h++ {
			inv.Hosts = append(inv.Hosts, vc.Host{Ref: fmt.Sprintf("host-%d-%d", c, h), Name: fmt.Sprintf("esx%02d.%s", h+1, name), Cluster: name,
				CPUModel: "Intel(R) Xeon(R) Gold 6338 CPU @ 2.00GHz", Sockets: 2, Cores: 64, NUMANodes: 2, MHz: 2000, MemBytes: 1024 << 30, Connected: true})
		}
		for v := 0; v < 25; v++ {
			vm := vc.VM{Ref: fmt.Sprintf("vm-%d-%d", c, v), Name: fmt.Sprintf("%s-%s%02d", strings.ReplaceAll(name, "-cl", ""), []string{"web", "app", "db", "svc"}[v%4], v+1), Cluster: name,
				GuestOS: "Red Hat Enterprise Linux 9", PowerOn: v%11 != 10, VCPU: []int{2, 4, 8, 16}[rng.IntN(4)], MemMB: []int{4096, 8192, 16384, 32768, 65536}[rng.IntN(5)],
				Host: fmt.Sprintf("esx%02d.%s", v%4+1, name), CoresPerSock: 1,
				Committed: int64(40+rng.IntN(400)) << 30, Disks: []vc.Disk{{Capacity: 200 << 30, Thin: v%4 != 0}},
				GuestDisks: []vc.GuestDisk{{Path: "/", Capacity: 200 << 30, Free: int64(50+rng.IntN(140)) << 30}}}
			if v%7 == 3 {
				vm.Snapshots = []vc.Snapshot{{Name: "before patch", Created: end.Add(-time.Duration(4+rng.IntN(40)) * 24 * time.Hour)}}
				vm.SnapshotBytes = int64(5+rng.IntN(90)) << 30
			}
			inv.VMs = append(inv.VMs, vm)
			if !vm.PowerOn {
				continue
			}
			base, memb := rng.Float64()*30, 5+rng.Float64()*60
			if v%9 == 5 {
				base = 0.3
			}
			if v == 13 {
				base = 70
				vm.VCPU = 40
				inv.VMs[len(inv.VMs)-1].VCPU = 40
			}
			night := v%4 == 3
			s := &analysis.VMStats{First: start, Last: end}
			for i := 0; i < 60000; i++ {
				d := math.Sin(float64(i)/4320*2*math.Pi)*0.5 + 0.5
				s.CPU.Add(base * (0.4 + d + rng.Float64()*0.4))
				s.Mem.Add(memb * (0.9 + rng.Float64()*0.2))
				n := 1.0
				if v%9 == 5 {
					n = 0
				}
				s.Net.Add(n * 200)
				s.Disk.Add(n * 300)
				s.Ready.Add(rng.Float64() * 3)
				s.Samples++
			}
			for slot := 0; slot < 14*48; slot++ {
				hour := float64(slot%48) / 2
				d := math.Max(0, math.Sin((hour-6)/12*math.Pi))
				if night {
					d = math.Max(0, math.Sin((hour-18)/12*math.Pi))
				}
				s.Demand = append(s.Demand, float32(base/100*float64(vm.VCPU)*2000*(0.2+0.8*d)*(0.9+rng.Float64()*0.2)))
				s.Slots = append(s.Slots, 1)
			}
			st.VMs[vm.Ref] = s
		}
		_, cp := analysis.Capacity(inv)
		cs := &analysis.ClusterStats{}
		for t := start; t.Before(end); t = t.Add(5 * time.Minute) {
			d := math.Sin(float64(t.Hour())/24*2*math.Pi-math.Pi/2)*0.5 + 0.5
			cpu := cp[name].MHz * (0.12 + 0.25*d + rng.Float64()*0.05)
			mem := cp[name].MemB * (0.55 + rng.Float64()*0.05)
			cs.CPU.Add(cpu / cp[name].MHz * 100)
			cs.Mem.Add(mem / cp[name].MemB * 100)
			cs.Points = append(cs.Points, analysis.Point{T: t, CPUMHz: cpu, MemB: mem})
		}
		st.Clusters[name] = cs
	}
	r := analysis.Analyze(analysis.Input{Inv: inv, RT: st, Profile: analysis.ProfileByName("balanced"), Start: start, End: end, Planned: 14 * 24 * time.Hour,
		Orphans: []vc.OrphanDisk{{Datastore: "ds-prod-01", Path: "[ds-prod-01] old-sql02/old-sql02.vmdk", Size: 412 << 30, Modified: end.Add(-200 * 24 * time.Hour)}}})
	r.Final = true
	r.VCenter = "vcsa01.corp.local — VMware vCenter Server 8.0.3 build-24322831"
	return r
}

func TestWritePDF(t *testing.T) {
	out := os.Getenv("RIGHTSIZER_DEMO_PDF")
	if out == "" {
		out = filepath.Join(t.TempDir(), "demo.pdf")
	}
	if err := WritePDF(demo(), out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil || !strings.HasPrefix(string(b), "%PDF") {
		t.Fatalf("invalid pdf: %v", err)
	}
}
