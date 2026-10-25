package ontapsim

import (
	"fmt"

	"github.com/MarcoColomb0/rightsizer/internal/ontap"
)

// Datastore describes a vSphere datastore to back with the fixture.
type Datastore struct {
	// Name of the volume; NFS volumes are mounted at /Name.
	Name string
	NFS  bool
	// Used is the logical data in bytes, Ratio the data reduction.
	Used  int64
	Ratio float64
	// Snapshots is the space array Snapshot copies hold.
	Snapshots int64
}

// Fixture is a two-node AFF cluster with one aggregate per node, an SVM
// with an NFS and an iSCSI/FC interface, and a volume per datastore: LUN
// datastores get one LUN each, with serial numbers whose NAA identifier is
// returned in the same order.
func Fixture(name string, ds []Datastore) (*ontap.Inventory, []string) {
	inv := &ontap.Inventory{
		Cluster: ontap.Cluster{Name: name, UUID: "c0ffee00-0000-4000-8000-" + fmt.Sprintf("%012x", len(name)),
			Version: ontap.Version{Full: "NetApp Release 9.14.1P6: Fri Aug 02 2024", Generation: 9, Major: 14, Minor: 1}},
	}
	for i := range 2 {
		inv.Nodes = append(inv.Nodes, ontap.Node{
			UUID: fmt.Sprintf("0000000%d-aaaa-4bbb-8ccc-000000000000", i+1), Name: fmt.Sprintf("%s-%02d", name, i+1),
			Model: "AFF-A400", Serial: fmt.Sprintf("72190000%04d", i+1), Partner: fmt.Sprintf("%s-%02d", name, 2-i),
			Version: inv.Cluster.Version.Full, State: "up", CPUs: 40, CPUModel: "Intel(R) Xeon(R) Silver 4210", MemBytes: 144 << 30,
		})
		inv.LIFs = append(inv.LIFs,
			ontap.LIF{Name: fmt.Sprintf("nfs_lif%d", i+1), SVM: "svm_vmware", IP: fmt.Sprintf("10.0.40.%d", 10+i), Node: inv.Nodes[i].Name,
				Services: []string{"data_core", "data_nfs"}, Up: true},
			ontap.LIF{Name: fmt.Sprintf("iscsi_lif%d", i+1), SVM: "svm_vmware", IP: fmt.Sprintf("10.0.41.%d", 10+i), Node: inv.Nodes[i].Name,
				Services: []string{"data_core", "data_iscsi"}, Up: true})
	}
	var serials []string
	aggrLogical, aggrPhysical := [2]float64{}, [2]float64{}
	for i, d := range ds {
		a := i % 2
		ratio := max(d.Ratio, 1)
		phys := int64(float64(d.Used) / ratio)
		v := ontap.Volume{
			UUID: fmt.Sprintf("%08x-1111-4222-8333-444455556666", i+1), Name: d.Name, SVM: "svm_vmware", Style: "flexvol", Type: "rw",
			State: "online", Tiering: "none", Guarantee: "none", Aggregates: []string{fmt.Sprintf("aggr%d_%s_%02d", a+1, name, a+1)},
			Size: d.Used * 2, Used: d.Used + d.Snapshots, Available: d.Used - d.Snapshots, Logical: d.Used + d.Snapshots, Physical: phys + d.Snapshots,
			Footprint: phys + d.Snapshots, SnapshotUsed: d.Snapshots, SnapshotReserve: 0, Saved: d.Used - phys,
			DedupeSaved: (d.Used - phys) * 6 / 10, CompressionSaved: (d.Used - phys) * 4 / 10,
		}
		if d.NFS {
			v.Path = "/" + d.Name
		} else {
			serial := fmt.Sprintf("80Ab%c%07d", 'K'+rune(i%10), i+1)
			serials = append(serials, ontap.NAA(serial))
			inv.LUNs = append(inv.LUNs, ontap.LUN{UUID: fmt.Sprintf("%08x-9999-4888-8777-666655554444", i+1), Name: "/vol/" + d.Name + "/lun0",
				SVM: "svm_vmware", Volume: d.Name, VolumeUUID: v.UUID, Serial: serial, OSType: "vmware", State: "online",
				Size: d.Used * 2, Used: d.Used, Mapped: true})
		}
		inv.Volumes = append(inv.Volumes, v)
		aggrLogical[a] += float64(d.Used)
		aggrPhysical[a] += float64(phys)
	}
	for a := range 2 {
		lg, ph := int64(aggrLogical[a]), int64(aggrPhysical[a]*0.92)
		size := int64(60) << 40
		r := 1.0
		if ph > 0 {
			r = float64(lg) / float64(ph)
		}
		inv.Aggregates = append(inv.Aggregates, ontap.Aggregate{
			UUID: fmt.Sprintf("aaaa000%d-bbbb-4ccc-8ddd-eeeeffff0000", a+1), Name: fmt.Sprintf("aggr%d_%s_%02d", a+1, name, a+1),
			Node: inv.Nodes[a].Name, State: "online", StorageType: "ssd", DiskClass: "solid_state", DiskType: "ssd", RAID: "raid_dp", Disks: 23,
			Size: size, Used: ph, Available: size - ph, PhysicalUsed: ph, SnapshotUsed: 0,
			Logical: lg, Ratio: r, LogicalNoSnap: lg, RatioNoSnap: r, CompactionSaved: int64(aggrPhysical[a] * 0.08),
			DedupeSaved: int64(aggrLogical[a] - aggrPhysical[a]),
		})
	}
	return inv, serials
}
