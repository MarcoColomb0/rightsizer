package ontap

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Inventory is what the cluster stores and how efficiently: the capacity a
// replacement array has to hold.
type Inventory struct {
	Taken      time.Time
	Cluster    Cluster
	Nodes      []Node
	Aggregates []Aggregate
	Volumes    []Volume
	LUNs       []LUN
	LIFs       []LIF
}

type Version struct {
	Full       string `json:"full"`
	Generation int    `json:"generation"`
	Major      int    `json:"major"`
	Minor      int    `json:"minor"`
}

// Release is the short version, such as 9.14.1.
func (v Version) Release() string {
	if v.Generation == 0 {
		return v.Full
	}
	return fmt.Sprintf("%d.%d.%d", v.Generation, v.Major, v.Minor)
}

type Cluster struct {
	Name    string  `json:"name"`
	UUID    string  `json:"uuid"`
	Version Version `json:"version"`
}

type Node struct {
	UUID     string
	Name     string
	Model    string
	Serial   string
	Partner  string
	Version  string
	State    string
	CPUs     int
	CPUModel string
	MemBytes int64
}

type Aggregate struct {
	UUID        string
	Name        string
	Node        string
	State       string
	StorageType string
	DiskClass   string
	DiskType    string
	RAID        string
	Disks       int
	Mirrored    bool
	Encrypted   bool
	// Size is the usable space, Used includes volume guarantees and
	// metadata, PhysicalUsed is what is written.
	Size         int64
	Used         int64
	Available    int64
	PhysicalUsed int64
	// Inactive is cold data, reported when inactive data reporting is on.
	Inactive     int64
	SnapshotUsed int64
	// Logical and Ratio include every saving: deduplication, compression
	// and compaction. NoSnap leaves out Snapshot copies.
	Logical         int64
	Ratio           float64
	LogicalNoSnap   int64
	RatioNoSnap     float64
	CompactionSaved int64
	DedupeSaved     int64
}

type Volume struct {
	UUID       string
	Name       string
	SVM        string
	Style      string
	Type       string
	State      string
	Path       string
	Root       bool
	Tiering    string
	Guarantee  string
	Aggregates []string
	Size       int64
	Used       int64
	Available  int64
	// Logical is the data as written by clients, before savings; Physical
	// is what the volume holds after deduplication and compression.
	Logical          int64
	Physical         int64
	Footprint        int64
	SnapshotUsed     int64
	SnapshotReserve  int64
	Saved            int64
	DedupeSaved      int64
	CompressionSaved int64
	CapacityTier     int64
}

type LUN struct {
	UUID       string
	Name       string
	SVM        string
	Volume     string
	VolumeUUID string
	Serial     string
	OSType     string
	State      string
	Size       int64
	Used       int64
	Mapped     bool
}

// NAA is the device identifier hosts see for the LUN, as in vSphere's
// canonical name naa.600a0980….
func (l LUN) NAA() string { return NAA(l.Serial) }

// NAA builds the NetApp NAA identifier from a LUN serial number.
func NAA(serial string) string {
	if serial == "" {
		return ""
	}
	return "naa.600a0980" + hex.EncodeToString([]byte(serial))
}

type LIF struct {
	Name     string
	SVM      string
	IP       string
	Node     string
	Services []string
	Up       bool
}

var nodeFields = []string{"uuid", "name", "model", "serial_number", "ha.partners.name", "version.full", "state",
	"controller.cpu.count", "controller.cpu.processor", "controller.memory_size"}

var aggrFields = []string{"uuid", "name", "node.name", "state", "block_storage.storage_type", "block_storage.primary.disk_class",
	"block_storage.primary.disk_type", "block_storage.primary.raid_type", "block_storage.primary.disk_count", "block_storage.mirror.enabled",
	"data_encryption.software_encryption_enabled", "data_encryption.drive_protection_enabled",
	"space.block_storage.size", "space.block_storage.used", "space.block_storage.available", "space.block_storage.physical_used",
	"space.block_storage.inactive_user_data", "space.block_storage.data_compaction_space_saved", "space.block_storage.volume_deduplication_space_saved",
	"space.snapshot.used", "space.efficiency.logical_used", "space.efficiency.ratio",
	"space.efficiency_without_snapshots.logical_used", "space.efficiency_without_snapshots.ratio"}

var volFields = []string{"uuid", "name", "svm.name", "style", "type", "state", "nas.path", "is_svm_root", "tiering.policy", "guarantee.type",
	"aggregates.name", "space.size", "space.used", "space.available", "space.logical_space.used", "space.physical_used",
	"space.footprint", "space.snapshot.used", "space.snapshot.reserve_size", "space.capacity_tier_footprint",
	"efficiency.space_savings.total", "efficiency.space_savings.dedupe", "efficiency.space_savings.compression"}

var lunFields = []string{"uuid", "name", "svm.name", "location.volume.name", "location.volume.uuid", "serial_number", "os_type",
	"status.state", "status.mapped", "space.size", "space.used"}

var lifFields = []string{"name", "svm.name", "ip.address", "location.node.name", "services", "state"}

type named struct {
	Name string `json:"name"`
}

type rawNode struct {
	UUID    string `json:"uuid"`
	Name    string `json:"name"`
	Model   string `json:"model"`
	Serial  string `json:"serial_number"`
	State   string `json:"state"`
	Version struct {
		Full string `json:"full"`
	} `json:"version"`
	HA struct {
		Partners []named `json:"partners"`
	} `json:"ha"`
	Controller struct {
		CPU struct {
			Count     int    `json:"count"`
			Processor string `json:"processor"`
		} `json:"cpu"`
		Memory int64 `json:"memory_size"`
	} `json:"controller"`
}

type efficiency struct {
	Logical int64   `json:"logical_used"`
	Ratio   float64 `json:"ratio"`
}

type rawAggr struct {
	UUID  string `json:"uuid"`
	Name  string `json:"name"`
	Node  named  `json:"node"`
	State string `json:"state"`
	Block struct {
		Type    string `json:"storage_type"`
		Primary struct {
			Class string `json:"disk_class"`
			Type  string `json:"disk_type"`
			RAID  string `json:"raid_type"`
			Count int    `json:"disk_count"`
		} `json:"primary"`
		Mirror struct {
			Enabled bool `json:"enabled"`
		} `json:"mirror"`
	} `json:"block_storage"`
	Encryption struct {
		Software bool `json:"software_encryption_enabled"`
		Drive    bool `json:"drive_protection_enabled"`
	} `json:"data_encryption"`
	Space struct {
		Block struct {
			Size       int64 `json:"size"`
			Used       int64 `json:"used"`
			Available  int64 `json:"available"`
			Physical   int64 `json:"physical_used"`
			Inactive   int64 `json:"inactive_user_data"`
			Compaction int64 `json:"data_compaction_space_saved"`
			Dedupe     int64 `json:"volume_deduplication_space_saved"`
		} `json:"block_storage"`
		Snapshot struct {
			Used int64 `json:"used"`
		} `json:"snapshot"`
		Efficiency   efficiency `json:"efficiency"`
		EfficiencyNS efficiency `json:"efficiency_without_snapshots"`
	} `json:"space"`
}

type rawVol struct {
	UUID  string `json:"uuid"`
	Name  string `json:"name"`
	SVM   named  `json:"svm"`
	Style string `json:"style"`
	Type  string `json:"type"`
	State string `json:"state"`
	NAS   struct {
		Path string `json:"path"`
	} `json:"nas"`
	Root    bool `json:"is_svm_root"`
	Tiering struct {
		Policy string `json:"policy"`
	} `json:"tiering"`
	Guarantee struct {
		Type string `json:"type"`
	} `json:"guarantee"`
	Aggregates []named `json:"aggregates"`
	Space      struct {
		Size      int64 `json:"size"`
		Used      int64 `json:"used"`
		Available int64 `json:"available"`
		Logical   struct {
			Used int64 `json:"used"`
		} `json:"logical_space"`
		Physical  int64 `json:"physical_used"`
		Footprint int64 `json:"footprint"`
		Snapshot  struct {
			Used    int64 `json:"used"`
			Reserve int64 `json:"reserve_size"`
		} `json:"snapshot"`
		CapacityTier int64 `json:"capacity_tier_footprint"`
	} `json:"space"`
	Efficiency struct {
		Savings struct {
			Total       int64 `json:"total"`
			Dedupe      int64 `json:"dedupe"`
			Compression int64 `json:"compression"`
		} `json:"space_savings"`
	} `json:"efficiency"`
}

type rawLUN struct {
	UUID     string `json:"uuid"`
	Name     string `json:"name"`
	SVM      named  `json:"svm"`
	Location struct {
		Volume struct {
			Name string `json:"name"`
			UUID string `json:"uuid"`
		} `json:"volume"`
	} `json:"location"`
	Serial string `json:"serial_number"`
	OSType string `json:"os_type"`
	Status struct {
		State  string `json:"state"`
		Mapped bool   `json:"mapped"`
	} `json:"status"`
	Space struct {
		Size int64 `json:"size"`
		Used int64 `json:"used"`
	} `json:"space"`
}

type rawLIF struct {
	Name string `json:"name"`
	SVM  named  `json:"svm"`
	IP   struct {
		Address string `json:"address"`
	} `json:"ip"`
	Location struct {
		Node named `json:"node"`
	} `json:"location"`
	Services []string `json:"services"`
	State    string   `json:"state"`
}

// Inventory reads nodes, aggregates, volumes, LUNs and data interfaces.
// LUNs and interfaces are optional: a role that cannot read them still gets
// capacity and data reduction.
func (c *Client) Inventory(ctx context.Context) (*Inventory, error) {
	inv := &Inventory{Taken: time.Now(), Cluster: c.info}
	nodes, err := list[rawNode](ctx, c, "/api/cluster/nodes", nodeFields, nil)
	if err != nil {
		return nil, fmt.Errorf("nodes: %w", err)
	}
	for _, n := range nodes {
		node := Node{UUID: n.UUID, Name: n.Name, Model: n.Model, Serial: n.Serial, Version: n.Version.Full, State: n.State,
			CPUs: n.Controller.CPU.Count, CPUModel: n.Controller.CPU.Processor, MemBytes: n.Controller.Memory}
		if len(n.HA.Partners) > 0 {
			node.Partner = n.HA.Partners[0].Name
		}
		inv.Nodes = append(inv.Nodes, node)
	}
	aggrs, err := list[rawAggr](ctx, c, "/api/storage/aggregates", aggrFields, nil)
	if err != nil {
		return nil, fmt.Errorf("aggregates: %w", err)
	}
	for _, a := range aggrs {
		s := a.Space
		inv.Aggregates = append(inv.Aggregates, Aggregate{
			UUID: a.UUID, Name: a.Name, Node: a.Node.Name, State: a.State, StorageType: a.Block.Type,
			DiskClass: a.Block.Primary.Class, DiskType: a.Block.Primary.Type, RAID: a.Block.Primary.RAID, Disks: a.Block.Primary.Count,
			Mirrored: a.Block.Mirror.Enabled, Encrypted: a.Encryption.Software || a.Encryption.Drive,
			Size: s.Block.Size, Used: s.Block.Used, Available: s.Block.Available, PhysicalUsed: s.Block.Physical, Inactive: s.Block.Inactive,
			SnapshotUsed: s.Snapshot.Used, Logical: s.Efficiency.Logical, Ratio: s.Efficiency.Ratio,
			LogicalNoSnap: s.EfficiencyNS.Logical, RatioNoSnap: s.EfficiencyNS.Ratio,
			CompactionSaved: s.Block.Compaction, DedupeSaved: s.Block.Dedupe,
		})
	}
	vols, err := list[rawVol](ctx, c, "/api/storage/volumes", volFields, nil)
	if err != nil {
		return nil, fmt.Errorf("volumes: %w", err)
	}
	for _, v := range vols {
		s := v.Space
		vol := Volume{
			UUID: v.UUID, Name: v.Name, SVM: v.SVM.Name, Style: v.Style, Type: v.Type, State: v.State, Path: v.NAS.Path, Root: v.Root,
			Tiering: v.Tiering.Policy, Guarantee: v.Guarantee.Type,
			Size: s.Size, Used: s.Used, Available: s.Available, Logical: s.Logical.Used, Physical: s.Physical, Footprint: s.Footprint,
			SnapshotUsed: s.Snapshot.Used, SnapshotReserve: s.Snapshot.Reserve, CapacityTier: s.CapacityTier,
			Saved: v.Efficiency.Savings.Total, DedupeSaved: v.Efficiency.Savings.Dedupe, CompressionSaved: v.Efficiency.Savings.Compression,
		}
		for _, a := range v.Aggregates {
			vol.Aggregates = append(vol.Aggregates, a.Name)
		}
		inv.Volumes = append(inv.Volumes, vol)
	}
	luns, err := list[rawLUN](ctx, c, "/api/storage/luns", lunFields, nil)
	switch {
	case IsForbidden(err):
	case err != nil:
		return nil, fmt.Errorf("LUNs: %w", err)
	}
	for _, l := range luns {
		inv.LUNs = append(inv.LUNs, LUN{
			UUID: l.UUID, Name: l.Name, SVM: l.SVM.Name, Volume: l.Location.Volume.Name, VolumeUUID: l.Location.Volume.UUID,
			Serial: l.Serial, OSType: l.OSType, State: l.Status.State, Mapped: l.Status.Mapped, Size: l.Space.Size, Used: l.Space.Used,
		})
	}
	lifs, err := list[rawLIF](ctx, c, "/api/network/ip/interfaces", lifFields, url.Values{"scope": {"svm"}})
	switch {
	case IsForbidden(err):
	case err != nil:
		return nil, fmt.Errorf("network interfaces: %w", err)
	}
	for _, l := range lifs {
		inv.LIFs = append(inv.LIFs, LIF{Name: l.Name, SVM: l.SVM.Name, IP: l.IP.Address, Node: l.Location.Node.Name, Services: l.Services, Up: l.State == "up"})
	}
	slices.SortFunc(inv.Nodes, func(a, b Node) int { return strings.Compare(a.Name, b.Name) })
	slices.SortFunc(inv.Aggregates, func(a, b Aggregate) int { return strings.Compare(a.Name, b.Name) })
	slices.SortFunc(inv.Volumes, func(a, b Volume) int { return strings.Compare(a.SVM+"/"+a.Name, b.SVM+"/"+b.Name) })
	slices.SortFunc(inv.LUNs, func(a, b LUN) int { return strings.Compare(a.SVM+a.Name, b.SVM+b.Name) })
	return inv, nil
}

// Totals sums the cluster's aggregates. Logical data counts only where its
// aggregate reports a ratio, so the cluster ratio never divides data with
// no physical counterpart.
type Totals struct {
	Size, Used, Physical, Logical, LogicalNoSnap, Snapshots, Inactive int64
	Ratio, RatioNoSnap                                                float64
}

func (inv *Inventory) Totals() Totals {
	var t Totals
	var phys, physNS float64
	for _, a := range inv.Aggregates {
		t.Size += a.Size
		t.Used += a.Used
		t.Physical += a.PhysicalUsed
		t.Snapshots += a.SnapshotUsed
		t.Inactive += a.Inactive
		if a.Ratio > 0 {
			t.Logical += a.Logical
			phys += float64(a.Logical) / a.Ratio
		}
		if a.RatioNoSnap > 0 {
			t.LogicalNoSnap += a.LogicalNoSnap
			physNS += float64(a.LogicalNoSnap) / a.RatioNoSnap
		}
	}
	if phys > 0 {
		t.Ratio = float64(t.Logical) / phys
	}
	if physNS > 0 {
		t.RatioNoSnap = float64(t.LogicalNoSnap) / physNS
	}
	return t
}

// Volume finds a volume by UUID.
func (inv *Inventory) Volume(uuid string) *Volume {
	for i := range inv.Volumes {
		if inv.Volumes[i].UUID == uuid {
			return &inv.Volumes[i]
		}
	}
	return nil
}

// Aggregate finds an aggregate by name.
func (inv *Inventory) Aggregate(name string) *Aggregate {
	for i := range inv.Aggregates {
		if inv.Aggregates[i].Name == name {
			return &inv.Aggregates[i]
		}
	}
	return nil
}
