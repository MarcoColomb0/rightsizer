package vc

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/vmware/govmomi/view"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// Estate describes the hardware and storage behind the VMs: what a hardware
// refresh has to replace.
type Estate struct {
	Taken      time.Time
	Hosts      []HostDetail
	Clusters   []ClusterDetail
	Datastores []DatastoreDetail
}

type NIC struct {
	Device  string
	Driver  string
	SpeedMb int
	Switch  string
}

type HBA struct {
	Device string
	Type   string
	Model  string
	Driver string
	// SpeedGb is the negotiated link speed, 0 when unknown or down.
	SpeedGb float64
	WWPN    string
	Status  string
}

type VMK struct {
	Device    string
	Portgroup string
	Switch    string
	MTU       int
	IP        string
	Services  []string
	// Storage is set when the adapter carries storage traffic: vSAN or
	// NVMe services, or a subnet shared with an NFS server or iSCSI target.
	Storage bool
}

type GPU struct {
	Name   string
	Vendor string
	MemMB  int64
	Mode   string
}

type HostDetail struct {
	Name        string
	Cluster     string
	Vendor      string
	Model       string
	Serial      string
	BIOS        string
	BIOSDate    time.Time
	CPUModel    string
	Sockets     int
	Cores       int
	Threads     int
	MHz         int
	MemBytes    int64
	ESXi        string
	HTActive    bool
	Connected   bool
	Maintenance bool
	NICs        []NIC
	HBAs        []HBA
	VMKs        []VMK
	GPUs        []GPU
}

type LUN struct {
	Name      string
	Vendor    string
	Model     string
	Capacity  int64
	SSD       bool
	Transport string
	Paths     int
	Policy    string
}

type DatastoreDetail struct {
	Name        string
	Type        string
	Version     string
	ID          string
	Capacity    int64
	Free        int64
	Uncommitted int64
	Accessible  bool
	Local       bool
	SSD         bool
	Remote      string
	LUNs        []LUN
	Hosts       []string
	VMs         int
}

type ClusterDetail struct {
	Name      string
	Hosts     int
	HA        bool
	Admission string
	DRS       string
	EVC       string
	VSAN      bool
}

var estateHostProps = []string{
	"name", "parent", "summary.hardware", "hardware.systemInfo", "hardware.biosInfo", "config.product", "config.hyperThread",
	"config.network", "config.storageDevice", "config.virtualNicManagerInfo", "config.graphicsInfo",
	"runtime.connectionState", "runtime.inMaintenanceMode",
}

// Estate reads host hardware, adapters, cluster settings and datastore
// backing with property reads only.
func (c *Client) Estate(ctx context.Context) (*Estate, error) {
	if err := c.Ensure(ctx); err != nil {
		return nil, err
	}
	v, err := view.NewManager(c.vim).CreateContainerView(ctx, c.vim.ServiceContent.RootFolder, nil, true)
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
	var ccs []mo.ClusterComputeResource
	if err := v.Retrieve(ctx, []string{"ClusterComputeResource"}, []string{"name", "configurationEx", "summary", "host"}, &ccs); err != nil {
		return nil, err
	}
	var hs []mo.HostSystem
	if err := v.Retrieve(ctx, []string{"HostSystem"}, estateHostProps, &hs); err != nil {
		return nil, err
	}
	var dss []mo.Datastore
	if err := v.Retrieve(ctx, []string{"Datastore"}, []string{"name", "summary", "info", "host", "vm"}, &dss); err != nil {
		return nil, err
	}

	e := &Estate{Taken: time.Now()}
	for _, cc := range ccs {
		e.Clusters = append(e.Clusters, convertCluster(cc))
	}
	var targets []string
	for _, d := range dss {
		if n, ok := d.Info.(*types.NasDatastoreInfo); ok && n.Nas != nil {
			targets = append(targets, n.Nas.RemoteHost)
			targets = append(targets, n.Nas.RemoteHostNames...)
		}
	}
	hostName := map[string]string{}
	luns := map[string]LUN{}
	for _, h := range hs {
		cl := ""
		if h.Parent != nil {
			cl = crName[h.Parent.Value]
			if h.Parent.Type == "ComputeResource" {
				cl = "standalone/" + cl
			}
		}
		hd, hl := convertHost(h, cl, targets)
		hostName[h.Self.Value] = hd.Name
		for k, l := range hl {
			if _, ok := luns[k]; !ok {
				luns[k] = l
			}
		}
		e.Hosts = append(e.Hosts, hd)
	}
	for _, d := range dss {
		e.Datastores = append(e.Datastores, convertDatastore(d, hostName, luns))
	}
	slices.SortFunc(e.Hosts, func(a, b HostDetail) int { return strings.Compare(a.Cluster+"/"+a.Name, b.Cluster+"/"+b.Name) })
	slices.SortFunc(e.Datastores, func(a, b DatastoreDetail) int { return strings.Compare(a.Name, b.Name) })
	slices.SortFunc(e.Clusters, func(a, b ClusterDetail) int { return strings.Compare(a.Name, b.Name) })
	return e, nil
}

func convertCluster(cc mo.ClusterComputeResource) ClusterDetail {
	cd := ClusterDetail{Name: cc.Name, Hosts: len(cc.Host)}
	if s, ok := cc.Summary.(*types.ClusterComputeResourceSummary); ok {
		cd.EVC = s.CurrentEVCModeKey
	}
	cfg, ok := cc.ConfigurationEx.(*types.ClusterConfigInfoEx)
	if !ok {
		return cd
	}
	das := cfg.DasConfig
	cd.HA = das.Enabled != nil && *das.Enabled
	if cd.HA {
		cd.Admission = "disabled"
		if das.AdmissionControlEnabled == nil || *das.AdmissionControlEnabled {
			switch p := das.AdmissionControlPolicy.(type) {
			case *types.ClusterFailoverLevelAdmissionControlPolicy:
				cd.Admission = fmt.Sprintf("N+%d, slot policy", p.FailoverLevel)
			case *types.ClusterFailoverResourcesAdmissionControlPolicy:
				cd.Admission = fmt.Sprintf("%d%% CPU, %d%% memory reserved", p.CpuFailoverResourcesPercent, p.MemoryFailoverResourcesPercent)
				if p.AutoComputePercentages != nil && *p.AutoComputePercentages && p.FailoverLevel > 0 {
					cd.Admission = fmt.Sprintf("N+%d (%d%% CPU, %d%% memory)", p.FailoverLevel, p.CpuFailoverResourcesPercent, p.MemoryFailoverResourcesPercent)
				}
			case *types.ClusterFailoverHostAdmissionControlPolicy:
				cd.Admission = fmt.Sprintf("%d dedicated failover host(s)", len(p.FailoverHosts))
			default:
				cd.Admission = "enabled"
			}
		}
	}
	if cfg.DrsConfig.Enabled != nil && *cfg.DrsConfig.Enabled {
		cd.DRS = string(cfg.DrsConfig.DefaultVmBehavior)
		if cd.DRS == "" {
			cd.DRS = "enabled"
		}
	}
	if vs := cfg.VsanConfigInfo; vs != nil && vs.Enabled != nil {
		cd.VSAN = *vs.Enabled
	}
	return cd
}

func convertHost(h mo.HostSystem, cluster string, nfs []string) (HostDetail, map[string]LUN) {
	hd := HostDetail{
		Name:        h.Name,
		Cluster:     cluster,
		Connected:   h.Runtime.ConnectionState == types.HostSystemConnectionStateConnected,
		Maintenance: h.Runtime.InMaintenanceMode,
	}
	if hw := h.Summary.Hardware; hw != nil {
		hd.Vendor, hd.Model = strings.TrimSpace(hw.Vendor), strings.TrimSpace(hw.Model)
		hd.CPUModel = strings.Join(strings.Fields(hw.CpuModel), " ")
		hd.Sockets, hd.Cores, hd.Threads = int(hw.NumCpuPkgs), int(hw.NumCpuCores), int(hw.NumCpuThreads)
		hd.MHz, hd.MemBytes = int(hw.CpuMhz), hw.MemorySize
		hd.Serial = serial(hw.OtherIdentifyingInfo)
	}
	if h.Hardware != nil {
		if hd.Serial == "" {
			hd.Serial = h.Hardware.SystemInfo.SerialNumber
		}
		if b := h.Hardware.BiosInfo; b != nil {
			hd.BIOS = b.BiosVersion
			if b.ReleaseDate != nil {
				hd.BIOSDate = *b.ReleaseDate
			}
		}
	}
	luns := map[string]LUN{}
	cfg := h.Config
	if cfg == nil {
		return hd, luns
	}
	hd.ESXi = cfg.Product.FullName
	if ht := cfg.HyperThread; ht != nil {
		hd.HTActive = ht.Active
	}
	for _, g := range cfg.GraphicsInfo {
		hd.GPUs = append(hd.GPUs, GPU{Name: g.DeviceName, Vendor: g.VendorName, MemMB: g.MemorySizeInKB / 1024, Mode: g.GraphicsType})
	}
	var targets []string
	if sd := cfg.StorageDevice; sd != nil {
		var hbaType map[string]string
		hd.HBAs, hbaType, targets = convertHBAs(sd.HostBusAdapter)
		luns = convertLUNs(sd, hbaType)
	}
	if n := cfg.Network; n != nil {
		hd.NICs, hd.VMKs = convertNetwork(n, cfg.VirtualNicManagerInfo, append(targets, nfs...))
	}
	return hd, luns
}

func serial(ids []types.HostSystemIdentificationInfo) string {
	best, rank := "", 0
	for _, id := range ids {
		if id.IdentifierType == nil {
			continue
		}
		r := map[string]int{"SerialNumberTag": 3, "ServiceTag": 2, "EnclosureSerialNumberTag": 1}[id.IdentifierType.GetElementDescription().Key]
		if v := strings.TrimSpace(id.IdentifierValue); r > rank && v != "" {
			best, rank = v, r
		}
	}
	return best
}

func convertHBAs(list []types.BaseHostHostBusAdapter) ([]HBA, map[string]string, []string) {
	var out []HBA
	kinds := map[string]string{}
	var targets []string
	for _, b := range list {
		a := b.GetHostHostBusAdapter()
		h := HBA{Device: a.Device, Model: strings.TrimSpace(a.Model), Driver: a.Driver, Status: a.Status}
		nvme := a.StorageProtocol == "nvme"
		switch x := b.(type) {
		case *types.HostFibreChannelOverEthernetHba:
			h.Type, h.SpeedGb, h.WWPN = "FCoE", fcSpeed(x.Speed), wwn(x.PortWorldWideName)
		case *types.HostFibreChannelHba:
			h.Type, h.SpeedGb, h.WWPN = "FC", fcSpeed(x.Speed), wwn(x.PortWorldWideName)
			if nvme {
				h.Type = "NVMe/FC"
			}
		case *types.HostInternetScsiHba:
			h.Type = "iSCSI"
			if x.IsSoftwareBased {
				h.Type = "iSCSI (software)"
			}
			h.SpeedGb = float64(x.CurrentSpeedMb) / 1000
			for _, t := range x.ConfiguredSendTarget {
				targets = append(targets, t.Address)
			}
			for _, t := range x.ConfiguredStaticTarget {
				targets = append(targets, t.Address)
			}
		case *types.HostTcpHba:
			h.Type = "NVMe/TCP"
		case *types.HostRdmaHba:
			h.Type = "NVMe/RDMA"
		case *types.HostPcieHba:
			h.Type = "NVMe (local)"
		case *types.HostSerialAttachedHba:
			h.Type = "SAS"
		case *types.HostParallelScsiHba:
			h.Type = "SCSI"
		case *types.HostBlockHba:
			h.Type = "Block"
		default:
			h.Type = "Other"
		}
		kinds[a.Key] = h.Type
		out = append(out, h)
	}
	return out, kinds, targets
}

// fcSpeed accepts both bits per second, as documented, and the Gbit value
// some drivers report.
func fcSpeed(v int64) float64 {
	if v >= 1_000_000 {
		return float64(v) / 1e9
	}
	return float64(v)
}

func wwn(v int64) string {
	if v == 0 {
		return ""
	}
	s := fmt.Sprintf("%016x", uint64(v))
	parts := make([]string, 0, 8)
	for i := 0; i < 16; i += 2 {
		parts = append(parts, s[i:i+2])
	}
	return strings.Join(parts, ":")
}

func convertLUNs(sd *types.HostStorageDeviceInfo, hbaType map[string]string) map[string]LUN {
	type mp struct {
		paths     int
		policy    string
		transport string
	}
	byLun := map[string]mp{}
	if mi := sd.MultipathInfo; mi != nil {
		for _, l := range mi.Lun {
			m := mp{paths: len(l.Path)}
			if l.Policy != nil {
				m.policy = l.Policy.GetHostMultipathInfoLogicalUnitPolicy().Policy
			}
			for _, p := range l.Path {
				if t := hbaType[p.Adapter]; t != "" {
					m.transport = t
					break
				}
			}
			byLun[l.Lun] = m
		}
	}
	out := map[string]LUN{}
	for _, b := range sd.ScsiLun {
		sl := b.GetScsiLun()
		if sl.CanonicalName == "" {
			continue
		}
		l := LUN{Name: sl.CanonicalName, Vendor: strings.TrimSpace(sl.Vendor), Model: strings.TrimSpace(sl.Model)}
		if d, ok := b.(*types.HostScsiDisk); ok {
			l.Capacity = int64(d.Capacity.BlockSize) * d.Capacity.Block
			l.SSD = d.Ssd != nil && *d.Ssd
		}
		if m, ok := byLun[sl.Key]; ok {
			l.Paths, l.Policy, l.Transport = m.paths, m.policy, m.transport
		}
		out[l.Name] = l
	}
	return out
}

func convertNetwork(n *types.HostNetworkInfo, vnm *types.HostVirtualNicManagerInfo, storageTargets []string) ([]NIC, []VMK) {
	pnicSwitch := map[string]string{}
	switchMTU := map[string]int{}
	pgSwitch := map[string]string{}
	dvsName := map[string]string{}
	for _, vs := range n.Vswitch {
		switchMTU[vs.Name] = int(vs.Mtu)
		for _, p := range vs.Pnic {
			pnicSwitch[p] = vs.Name
		}
	}
	for _, ps := range n.ProxySwitch {
		switchMTU[ps.DvsName] = int(ps.Mtu)
		dvsName[ps.DvsUuid] = ps.DvsName
		for _, p := range ps.Pnic {
			pnicSwitch[p] = ps.DvsName
		}
	}
	for _, pg := range n.Portgroup {
		pgSwitch[pg.Spec.Name] = pg.Spec.VswitchName
	}
	var nics []NIC
	for _, p := range n.Pnic {
		nic := NIC{Device: p.Device, Driver: p.Driver, Switch: pnicSwitch[p.Key]}
		if p.LinkSpeed != nil {
			nic.SpeedMb = int(p.LinkSpeed.SpeedMb)
		}
		nics = append(nics, nic)
	}
	services := map[string][]string{}
	if vnm != nil {
		for _, nc := range vnm.NetConfig {
			for _, sel := range nc.SelectedVnic {
				services[sel] = append(services[sel], nc.NicType)
			}
		}
	}
	var targets []net.IP
	for _, t := range storageTargets {
		if ip := net.ParseIP(strings.TrimSpace(t)); ip != nil {
			targets = append(targets, ip)
		}
	}
	var vmks []VMK
	for _, v := range n.Vnic {
		k := VMK{Device: v.Device, Portgroup: v.Portgroup, MTU: int(v.Spec.Mtu)}
		k.Switch = pgSwitch[v.Portgroup]
		if dp := v.Spec.DistributedVirtualPort; dp != nil {
			k.Switch = dvsName[dp.SwitchUuid]
			if k.Portgroup == "" {
				k.Portgroup = dp.PortgroupKey
			}
		}
		if k.MTU == 0 {
			k.MTU = 1500
		}
		if m := switchMTU[k.Switch]; m > 0 && m < k.MTU {
			k.MTU = m
		}
		for sel, ts := range services {
			if strings.HasSuffix(sel, "."+v.Key) || sel == v.Key {
				k.Services = append(k.Services, ts...)
			}
		}
		slices.Sort(k.Services)
		for _, s := range k.Services {
			if s == "vsan" || strings.HasPrefix(s, "nvme") {
				k.Storage = true
			}
		}
		if ipc := v.Spec.Ip; ipc != nil && ipc.IpAddress != "" {
			k.IP = ipc.IpAddress
			ip, mask := net.ParseIP(ipc.IpAddress), net.IPMask(net.ParseIP(ipc.SubnetMask).To4())
			if ip != nil && len(mask) == net.IPv4len {
				sub := &net.IPNet{IP: ip.Mask(mask), Mask: mask}
				for _, t := range targets {
					if sub.Contains(t) {
						k.Storage = true
					}
				}
			}
		}
		vmks = append(vmks, k)
	}
	return nics, vmks
}

func convertDatastore(d mo.Datastore, hostName map[string]string, luns map[string]LUN) DatastoreDetail {
	s := d.Summary
	dd := DatastoreDetail{
		Name: s.Name, Type: s.Type, Capacity: s.Capacity, Free: s.FreeSpace, Uncommitted: s.Uncommitted,
		Accessible: s.Accessible, ID: instanceID(s.Url), VMs: len(d.Vm),
	}
	if dd.Name == "" {
		dd.Name = d.Name
	}
	switch info := d.Info.(type) {
	case *types.VmfsDatastoreInfo:
		if v := info.Vmfs; v != nil {
			dd.Version = "VMFS " + v.Version
			dd.Local = v.Local != nil && *v.Local
			dd.SSD = v.Ssd != nil && *v.Ssd
			for _, ext := range v.Extent {
				l, ok := luns[ext.DiskName]
				if !ok {
					l = LUN{Name: ext.DiskName}
				}
				dd.LUNs = append(dd.LUNs, l)
			}
		}
	case *types.NasDatastoreInfo:
		if n := info.Nas; n != nil {
			host := n.RemoteHost
			if len(n.RemoteHostNames) > 1 {
				host = strings.Join(n.RemoteHostNames, ",")
			}
			dd.Remote = host + ":" + n.RemotePath
			dd.Version = map[string]string{"NFS": "NFS 3", "NFS41": "NFS 4.1"}[n.Type]
		}
	}
	if dd.Version == "" {
		dd.Version = map[string]string{"NFS": "NFS 3", "NFS41": "NFS 4.1", "vsan": "vSAN", "VVOL": "vVols", "PMEM": "PMem"}[dd.Type]
	}
	if dd.Version == "" {
		dd.Version = dd.Type
	}
	for _, m := range d.Host {
		if n := hostName[m.Key.Value]; n != "" {
			dd.Hosts = append(dd.Hosts, n)
		}
	}
	slices.Sort(dd.Hosts)
	return dd
}

// instanceID is the datastore identifier used by performance counters: the
// last element of its URL.
func instanceID(url string) string {
	parts := strings.Split(strings.TrimRight(url, "/"), "/")
	return parts[len(parts)-1]
}

// Filter keeps the clusters listed and the datastores their hosts mount.
func (e *Estate) Filter(keep []string) *Estate {
	if len(keep) == 0 {
		return e
	}
	out := &Estate{Taken: e.Taken}
	hosts := map[string]bool{}
	for _, h := range e.Hosts {
		if slices.Contains(keep, h.Cluster) {
			out.Hosts = append(out.Hosts, h)
			hosts[h.Name] = true
		}
	}
	for _, c := range e.Clusters {
		if slices.Contains(keep, c.Name) {
			out.Clusters = append(out.Clusters, c)
		}
	}
	for _, d := range e.Datastores {
		if slices.ContainsFunc(d.Hosts, func(h string) bool { return hosts[h] }) {
			out.Datastores = append(out.Datastores, d)
		}
	}
	return out
}
