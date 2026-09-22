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
	"name", "config.instanceUuid", "config.template", "config.guestFullName", "config.hardware.numCPU",
	"config.hardware.numCoresPerSocket", "config.hardware.memoryMB", "config.hardware.device", "runtime.powerState",
	"runtime.host", "summary.storage", "guest.disk", "guest.toolsRunningStatus",
	"snapshot", "layoutEx",
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

	var crs []mo.ComputeResource
	if err := v.Retrieve(ctx, []string{"ComputeResource"}, []string{"name"}, &crs); err != nil {
		return nil, err
	}
	crName := map[string]string{}
	for _, cr := range crs {
		crName[cr.Self.Value] = cr.Name
	}

	var hs []mo.HostSystem
	if err := v.Retrieve(ctx, []string{"HostSystem"}, hostProps, &hs); err != nil {
		return nil, err
	}
	inv := &Inventory{Taken: time.Now()}
	hostCluster := map[string]string{}
	hostName := map[string]string{}
	for _, h := range hs {
		cl := ""
		if h.Parent != nil {
			cl = crName[h.Parent.Value]
			if h.Parent.Type == "ComputeResource" {
				cl = "standalone/" + cl
			}
		}
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

func convertVM(m *mo.VirtualMachine, hostCluster, hostName map[string]string) VM {
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
		vm.VCPU = int(m.Config.Hardware.NumCPU)
		vm.CoresPerSock = 1
		if c := m.Config.Hardware.NumCoresPerSocket; c != nil && *c > 0 {
			vm.CoresPerSock = int(*c)
		}
		vm.MemMB = int(m.Config.Hardware.MemoryMB)
		for _, d := range m.Config.Hardware.Device {
			disk, ok := d.(*types.VirtualDisk)
			if !ok {
				continue
			}
			vd := Disk{Capacity: disk.CapacityInBytes}
			if disk.DeviceInfo != nil {
				vd.Label = disk.DeviceInfo.GetDescription().Label
			}
			if b, ok := disk.Backing.(*types.VirtualDiskFlatVer2BackingInfo); ok {
				vd.Thin = b.ThinProvisioned != nil && *b.ThinProvisioned
				vd.Datastore = datastoreOf(b.FileName)
			} else {
				vd.Thin = true
			}
			vm.Disks = append(vm.Disks, vd)
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

func datastoreOf(path string) string {
	if strings.HasPrefix(path, "[") {
		if i := strings.Index(path, "]"); i > 0 {
			return path[1:i]
		}
	}
	return ""
}
