package vc

import (
	"context"
	"strings"
	"time"

	"github.com/vmware/govmomi/view"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

type Host struct {
	Ref         string
	Name        string
	Cluster     string
	CPUModel    string
	Sockets     int
	Cores       int
	Threads     int
	MHz         int
	MemBytes    int64
	NUMANodes   int
	Connected   bool
	Maintenance bool
}

type Disk struct {
	Label     string
	Capacity  int64
	Thin      bool
	Datastore string
	// RDM is "physical" or "virtual" for raw device mappings, whose data
	// lives on a LUN outside the datastore.
	RDM string
}

type GuestDisk struct {
	Path     string
	Capacity int64
	Free     int64
}

type Snapshot struct {
	Name    string
	Created time.Time
}

type VM struct {
	Ref           string
	UUID          string
	Name          string
	Cluster       string
	Host          string
	GuestOS       string
	GuestID       string
	PowerOn       bool
	Template      bool
	VCPU          int
	CoresPerSock  int
	MemMB         int
	Committed     int64
	Uncommitted   int64
	Disks         []Disk
	GuestDisks    []GuestDisk
	Snapshots     []Snapshot
	SnapshotBytes int64
	// DiskBytes is the space used by the base virtual disks, SwapBytes by
	// the swap files the host creates at power-on.
	DiskBytes     int64
	SwapBytes     int64
	CPUReserveMHz int64
	MemReserveMB  int64
	VGPU          []string
	Passthrough   int
	ToolsOK       bool
	Files         []string
}

type Datastore struct {
	Ref        string
	Name       string
	Type       string
	Capacity   int64
	Free       int64
	Accessible bool
	Browser    string
}

type Inventory struct {
	Taken      time.Time
	Hosts      []Host
	VMs        []VM
	Datastores []Datastore
}

var vmProps = []string{
	"name", "config.instanceUuid", "config.template", "config.guestFullName", "config.guestId", "config.hardware.numCPU",
	"config.hardware.numCoresPerSocket", "config.hardware.memoryMB", "config.hardware.device", "runtime.powerState",
	"runtime.host", "summary.storage", "guest.disk", "guest.toolsRunningStatus",
	"snapshot", "layoutEx", "config.cpuAllocation", "config.memoryAllocation",
}

var hostProps = []string{
	"name", "parent", "summary.hardware", "hardware.numaInfo.numNodes", "runtime.connectionState", "runtime.inMaintenanceMode",
}

func (c *Client) Inventory(ctx context.Context) (*Inventory, error) {
	if err := c.Ensure(ctx); err != nil {
		return nil, err
	}
	m := view.NewManager(c.vim)
	v, err := m.CreateContainerView(ctx, c.vim.ServiceContent.RootFolder, nil, true)
	if err != nil {
		return nil, err
	}
	defer v.Destroy(context.WithoutCancel(ctx))

	crName, err := computeResources(ctx, v)
	if err != nil {
		return nil, err
	}

	var hs []mo.HostSystem
	if err := v.Retrieve(ctx, []string{"HostSystem"}, hostProps, &hs); err != nil {
		return nil, err
	}
	inv := &Inventory{Taken: time.Now()}
	hostCluster := map[string]string{}
	hostName := map[string]string{}
	for _, h := range hs {
		cl := clusterOf(h.Parent, crName)
		host := Host{
			Ref:         h.Self.Value,
			Name:        h.Name,
			Cluster:     cl,
			Connected:   h.Runtime.ConnectionState == types.HostSystemConnectionStateConnected,
			Maintenance: h.Runtime.InMaintenanceMode,
		}
		if hw := h.Summary.Hardware; hw != nil {
			host.CPUModel = strings.Join(strings.Fields(hw.CpuModel), " ")
			host.Sockets = int(hw.NumCpuPkgs)
			host.Cores = int(hw.NumCpuCores)
			host.Threads = int(hw.NumCpuThreads)
			host.MHz = int(hw.CpuMhz)
			host.MemBytes = hw.MemorySize
		}
		if h.Hardware != nil && h.Hardware.NumaInfo != nil {
			host.NUMANodes = int(h.Hardware.NumaInfo.NumNodes)
		}
		if host.NUMANodes <= 0 {
			host.NUMANodes = max(host.Sockets, 1)
		}
		hostCluster[host.Ref] = cl
		hostName[host.Ref] = host.Name
		inv.Hosts = append(inv.Hosts, host)
	}

	var vms []mo.VirtualMachine
	if err := v.Retrieve(ctx, []string{"VirtualMachine"}, vmProps, &vms); err != nil {
		return nil, err
	}
	for i := range vms {
		inv.VMs = append(inv.VMs, convertVM(&vms[i], hostCluster, hostName))
	}

	var dss []mo.Datastore
	if err := v.Retrieve(ctx, []string{"Datastore"}, []string{"name", "summary", "browser"}, &dss); err != nil {
		return nil, err
	}
	for _, d := range dss {
		ds := Datastore{Ref: d.Self.Value, Name: d.Summary.Name, Type: d.Summary.Type, Capacity: d.Summary.Capacity, Free: d.Summary.FreeSpace, Accessible: d.Summary.Accessible, Browser: d.Browser.Value}
		if ds.Name == "" {
			ds.Name = d.Name
		}
		inv.Datastores = append(inv.Datastores, ds)
	}
	return inv, nil
}

// computeResources maps clusters and standalone hosts' compute resources to
// their names.
func computeResources(ctx context.Context, v *view.ContainerView) (map[string]string, error) {
	var crs []mo.ComputeResource
	if err := v.Retrieve(ctx, []string{"ComputeResource"}, []string{"name"}, &crs); err != nil {
		return nil, err
	}
	names := make(map[string]string, len(crs))
	for _, cr := range crs {
		names[cr.Self.Value] = cr.Name
	}
	return names, nil
}

// clusterOf names a host's cluster; hosts outside a cluster get
// "standalone/<name>".
func clusterOf(parent *types.ManagedObjectReference, names map[string]string) string {
	if parent == nil {
		return ""
	}
	if parent.Type == "ComputeResource" {
		return "standalone/" + names[parent.Value]
	}
	return names[parent.Value]
}

func convertDisk(disk *types.VirtualDisk) Disk {
	vd := Disk{Capacity: disk.CapacityInBytes, Thin: true}
	if disk.DeviceInfo != nil {
		vd.Label = disk.DeviceInfo.GetDescription().Label
	}
	switch b := disk.Backing.(type) {
	case *types.VirtualDiskFlatVer2BackingInfo:
		vd.Thin = b.ThinProvisioned != nil && *b.ThinProvisioned
		vd.Datastore = datastoreOf(b.FileName)
	case *types.VirtualDiskRawDiskMappingVer1BackingInfo:
		vd.Datastore = datastoreOf(b.FileName)
		vd.RDM = "virtual"
		if b.CompatibilityMode == string(types.VirtualDiskCompatibilityModePhysicalMode) {
			vd.RDM = "physical"
		}
	}
	return vd
}

func convertVM(m *mo.VirtualMachine, hostCluster, hostName map[string]string) VM {
	rdm := map[int32]bool{}
	vm := VM{
		Ref:     m.Self.Value,
		Name:    m.Name,
		PowerOn: m.Runtime.PowerState == types.VirtualMachinePowerStatePoweredOn,
		ToolsOK: m.Guest != nil && m.Guest.ToolsRunningStatus == string(types.VirtualMachineToolsRunningStatusGuestToolsRunning),
	}
	if m.Runtime.Host != nil {
		vm.Host = hostName[m.Runtime.Host.Value]
		vm.Cluster = hostCluster[m.Runtime.Host.Value]
	}
	if m.Config != nil {
		vm.UUID = m.Config.InstanceUuid
		vm.Template = m.Config.Template
		vm.GuestOS = m.Config.GuestFullName
		vm.GuestID = m.Config.GuestId
		vm.VCPU = int(m.Config.Hardware.NumCPU)
		vm.CoresPerSock = 1
		if c := m.Config.Hardware.NumCoresPerSocket; c != nil && *c > 0 {
			vm.CoresPerSock = int(*c)
		}
		vm.MemMB = int(m.Config.Hardware.MemoryMB)
		if a := m.Config.CpuAllocation; a != nil && a.Reservation != nil {
			vm.CPUReserveMHz = *a.Reservation
		}
		if a := m.Config.MemoryAllocation; a != nil && a.Reservation != nil {
			vm.MemReserveMB = *a.Reservation
		}
		for _, d := range m.Config.Hardware.Device {
			switch dev := d.(type) {
			case *types.VirtualDisk:
				vd := convertDisk(dev)
				if vd.RDM != "" {
					rdm[dev.Key] = true
				}
				vm.Disks = append(vm.Disks, vd)
			case *types.VirtualPCIPassthrough:
				if b, ok := dev.Backing.(*types.VirtualPCIPassthroughVmiopBackingInfo); ok {
					vm.VGPU = append(vm.VGPU, b.Vgpu)
				} else {
					vm.Passthrough++
				}
			}
		}
	}
	if s := m.Summary.Storage; s != nil {
		vm.Committed = s.Committed
		vm.Uncommitted = s.Uncommitted
	}
	if m.Guest != nil {
		for _, g := range m.Guest.Disk {
			vm.GuestDisks = append(vm.GuestDisks, GuestDisk{Path: g.DiskPath, Capacity: g.Capacity, Free: g.FreeSpace})
		}
	}
	if m.Snapshot != nil {
		var walk func([]types.VirtualMachineSnapshotTree)
		walk = func(l []types.VirtualMachineSnapshotTree) {
			for _, s := range l {
				vm.Snapshots = append(vm.Snapshots, Snapshot{Name: s.Name, Created: s.CreateTime})
				walk(s.ChildSnapshotList)
			}
		}
		walk(m.Snapshot.RootSnapshotList)
	}
	if m.LayoutEx != nil {
		if len(vm.Snapshots) > 0 {
			vm.SnapshotBytes = snapshotBytes(m.LayoutEx)
		}
		vm.DiskBytes, vm.SwapBytes = layoutBytes(m.LayoutEx, rdm)
		for _, f := range m.LayoutEx.File {
			if strings.HasSuffix(f.Name, ".vmdk") {
				vm.Files = append(vm.Files, f.Name)
			}
		}
	}
	return vm
}

func snapshotBytes(l *types.VirtualMachineFileLayoutEx) int64 {
	size := map[int32]int64{}
	for _, f := range l.File {
		size[f.Key] = f.Size
	}
	var total int64
	seen := map[int32]bool{}
	add := func(k int32) {
		if !seen[k] {
			seen[k] = true
			total += size[k]
		}
	}
	for _, f := range l.File {
		if f.Type == "snapshotData" || f.Type == "snapshotMemory" {
			add(f.Key)
		}
	}
	for _, d := range l.Disk {
		for i, ch := range d.Chain {
			if i == 0 {
				continue
			}
			for _, k := range ch.FileKey {
				add(k)
			}
		}
	}
	return total
}

// layoutBytes returns the size of the base disks, skipping raw device
// mappings, and of the swap files.
func layoutBytes(l *types.VirtualMachineFileLayoutEx, rdm map[int32]bool) (disk, swap int64) {
	size := map[int32]int64{}
	for _, f := range l.File {
		size[f.Key] = f.Size
		if f.Type == "swap" || f.Type == "uwswap" {
			swap += f.Size
		}
	}
	seen := map[int32]bool{}
	for _, d := range l.Disk {
		if rdm[d.Key] || len(d.Chain) == 0 {
			continue
		}
		for _, k := range d.Chain[0].FileKey {
			if !seen[k] {
				seen[k] = true
				disk += size[k]
			}
		}
	}
	return disk, swap
}

func datastoreOf(path string) string {
	if strings.HasPrefix(path, "[") {
		if i := strings.Index(path, "]"); i > 0 {
			return path[1:i]
		}
	}
	return ""
}
