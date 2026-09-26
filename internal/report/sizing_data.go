package report

import (
	"archive/zip"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/atomicfile"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
)

// WriteSizingData writes the sizing tables as CSV files in a zip archive,
// for spreadsheets.
func WriteSizingData(sz *analysis.Sizing, path string) error {
	return atomicfile.Write(path, 0o600, func(w io.Writer) error {
		z := zip.NewWriter(w)
		for _, t := range sizingTables(sz) {
			f, err := z.CreateHeader(&zip.FileHeader{Name: t.name, Method: zip.Deflate, Modified: sz.Generated})
			if err != nil {
				return err
			}
			c := csv.NewWriter(f)
			if err := c.Write(t.head); err != nil {
				return err
			}
			for _, r := range t.rows {
				for i := range r {
					r[i] = cell(r[i])
				}
				if err := c.Write(r); err != nil {
					return err
				}
			}
			c.Flush()
			if err := c.Error(); err != nil {
				return err
			}
		}
		return z.Close()
	})
}

// cell keeps spreadsheet programs from running text from vCenter, such as a
// VM name, as a formula.
func cell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		if _, err := strconv.ParseFloat(s, 64); err != nil {
			return "'" + s
		}
	}
	return s
}

type csvTable struct {
	name string
	head []string
	rows [][]string
}

func gib(b int64) string  { return strconv.FormatFloat(float64(b)/(1<<30), 'f', 2, 64) }
func f1(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }
func itoa(v int) string   { return strconv.Itoa(v) }
func i64(v int64) string  { return strconv.FormatInt(v, 10) }
func yes(b bool) string   { return strconv.FormatBool(b) }

// known leaves a cell empty when vCenter did not report the value.
func known(ok bool, v float64) string {
	if !ok {
		return ""
	}
	return f1(v)
}

func day(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

func sizingTables(sz *analysis.Sizing) []csvTable {
	vms := csvTable{name: "vms.csv", head: []string{"vm", "cluster", "host", "workload", "guest_os", "powered_on", "template",
		"vcpu", "memory_mib", "recommended_vcpu", "recommended_memory_mib", "cpu_avg_mhz", "cpu_peak_mhz", "cpu_pct_percentile", "memory_active_pct_percentile", "memory_consumed_mib",
		"read_iops_avg", "write_iops_avg", "iops_peak", "raw_used_gib", "disks_gib", "snapshots_gib", "swap_gib", "other_gib", "rdm_gib", "provisioned_gib", "guest_used_gib",
		"datastores", "cpu_reservation_mhz", "memory_reservation_mib", "devices"}}
	for _, v := range sz.VMs {
		vms.rows = append(vms.rows, []string{v.Name, v.Cluster, v.Host, v.Workload, v.GuestOS, yes(v.PowerOn), yes(v.Template),
			itoa(v.VCPU), itoa(v.MemMB), itoa(v.RecVCPU), itoa(v.RecMemMB), f1(v.CPUAvgMHz), f1(v.CPUPeakMHz), f1(v.CPUPct), f1(v.MemPct), f1(v.ConsumedMB),
			f1(v.ReadIOPS), f1(v.WriteIOPS), f1(v.PeakIOPS), gib(v.Used), gib(v.DiskBytes), gib(v.SnapshotBytes), gib(v.SwapBytes), gib(v.OtherBytes), gib(v.RDMBytes), gib(v.Provisioned), gib(v.GuestUsed),
			v.Datastores, i64(v.CPUReserveMHz), i64(v.MemReserveMB), v.Devices})
	}

	cls := csvTable{name: "clusters.csv", head: []string{"cluster", "hosts", "cpu_model", "sockets", "cores", "threads", "core_mhz", "memory_gib",
		"vms_on", "vms_off", "vcpu", "memory_mib", "vcpu_per_core", "cpu_demand_mhz", "cpu_peak_mhz", "memory_consumed_gib", "network_peak_gbps", "storage_iops", "storage_iops_peak", "storage_peak_mbps",
		"nics", "storage_adapters", "storage_protocols", "storage_mtu", "gpus"}}
	opts := csvTable{name: "node-options.csv", head: []string{"cluster", "basis", "recommended", "nodes", "sockets", "cores_per_socket", "memory_gb_per_node", "total_cores",
		"cpu_load_pct", "memory_load_pct", "vcpu_per_core", "min_core_ghz", "numa_fit", "data_ports", "storage_ports", "oob_ports"}}
	needs := csvTable{name: "needs.csv", head: []string{"cluster", "basis", "vcpu", "memory_mib", "cpu_demand_mhz", "vcpu_per_core", "cores_by_ratio", "cores_by_demand", "cores", "memory_needed_gib"}}
	for _, c := range sz.Clusters {
		cls.rows = append(cls.rows, []string{c.Name, itoa(c.Hosts), c.CPUModel, itoa(c.Sockets), itoa(c.Cores), itoa(c.Threads), f1(c.CoreMHz), gib(c.MemB),
			itoa(c.VMs), itoa(c.VMsOff), itoa(c.VCPU), itoa(c.MemMB), f1(c.Ratio), f1(c.DemandMHz), f1(c.PeakMHz), gib(int64(c.ConsumedB)),
			f1(c.NetPeakKBps * 8 / 1e6), f1(c.IOPS), f1(c.IOPSPeak), f1(c.KBpsPeak / 1024),
			c.Links.NICs, c.Links.HBAs, strings.Join(c.Protocols, " "), c.Links.StorageMTU, c.Links.GPUs})
		for _, n := range c.Needs {
			needs.rows = append(needs.rows, []string{c.Name, n.Basis, itoa(n.VCPU), itoa(n.MemMB), f1(n.DemandMHz), f1(n.Ratio), itoa(n.CoresByRatio), itoa(n.CoresByDemand), itoa(n.Cores), gib(int64(n.MemB))})
			for i, o := range n.Options {
				ports := []string{"", "", ""}
				if i == n.Pick {
					ports = []string{fmt.Sprintf("%d x %d GbE", n.Ports.DataPorts, n.Ports.DataGb), "", fmt.Sprintf("%d x 1 GbE", n.Ports.OOBPorts)}
					if n.Ports.StoragePorts > 0 {
						ports[1] = fmt.Sprintf("%d x %s", n.Ports.StoragePorts, n.Ports.StorageLabel())
					}
				}
				opts.rows = append(opts.rows, append([]string{c.Name, n.Basis, yes(i == n.Pick), itoa(o.Nodes), itoa(o.Sockets), itoa(o.CoresPerSocket), itoa(o.MemGB), itoa(o.TotalCores),
					f1(o.CPUUtil), f1(o.MemUtil), f1(o.Ratio), strconv.FormatFloat(o.MinGHz, 'f', 2, 64), yes(o.NUMAFit)}, ports...))
			}
		}
	}

	hosts := csvTable{name: "hosts.csv", head: []string{"cluster", "host", "vendor", "model", "serial", "bios", "bios_date", "cpu_model", "sockets", "cores", "threads", "mhz", "memory_gib",
		"esxi", "hyperthreading", "connected", "maintenance"}}
	adapters := csvTable{name: "adapters.csv", head: []string{"cluster", "host", "kind", "device", "type", "model", "driver", "speed_gbps", "wwpn", "switch", "status"}}
	vmk := csvTable{name: "vmkernel.csv", head: []string{"cluster", "host", "device", "portgroup", "switch", "mtu", "ip", "services", "storage"}}
	for _, h := range sz.Hosts {
		hosts.rows = append(hosts.rows, []string{h.Cluster, h.Name, h.Vendor, h.Model, h.Serial, h.BIOS, day(h.BIOSDate), h.CPUModel, itoa(h.Sockets), itoa(h.Cores), itoa(h.Threads), itoa(h.MHz), gib(h.MemBytes),
			h.ESXi, yes(h.HTActive), yes(h.Connected), yes(h.Maintenance)})
		for _, n := range h.NICs {
			adapters.rows = append(adapters.rows, []string{h.Cluster, h.Name, "nic", n.Device, "Ethernet", "", n.Driver, f1(float64(n.SpeedMb) / 1000), "", n.Switch, ""})
		}
		for _, a := range h.HBAs {
			adapters.rows = append(adapters.rows, []string{h.Cluster, h.Name, "hba", a.Device, a.Type, a.Model, a.Driver, f1(a.SpeedGb), a.WWPN, "", a.Status})
		}
		for _, g := range h.GPUs {
			adapters.rows = append(adapters.rows, []string{h.Cluster, h.Name, "gpu", g.Name, g.Mode, g.Vendor, "", "", "", "", ""})
		}
		for _, k := range h.VMKs {
			vmk.rows = append(vmk.rows, []string{h.Cluster, h.Name, k.Device, k.Portgroup, k.Switch, itoa(k.MTU), k.IP, strings.Join(k.Services, " "), yes(k.Storage)})
		}
	}

	ds := csvTable{name: "datastores.csv", head: []string{"datastore", "type", "protocol", "capacity_gib", "used_gib", "free_gib", "provisioned_gib", "vms", "hosts", "local", "ssd",
		"nfs_export", "luns", "iops_percentile", "iops_peak", "read_pct", "mbps_percentile", "latency_ms_percentile"}}
	for _, d := range sz.Datastores {
		var luns []string
		for _, l := range d.LUNs {
			luns = append(luns, strings.Join([]string{l.Name, l.Vendor, l.Model, gib(l.Capacity) + " GiB", l.Transport, itoa(l.Paths) + " paths", l.Policy}, " / "))
		}
		ds.rows = append(ds.rows, []string{d.Name, d.Version, d.Protocol, gib(d.Capacity), gib(d.Used), gib(d.Free), gib(d.Provisioned), itoa(d.VMs), itoa(len(d.Hosts)), yes(d.Local), yes(d.SSD),
			d.Remote, strings.Join(luns, "; "), f1(d.IO.IOPS), f1(d.IO.IOPSPeak), f1(d.IO.ReadPct), known(d.IO.Throughput, d.IO.MBps), known(d.IO.Latency, d.IO.LatencyMs)})
	}

	wl := csvTable{name: "workloads.csv", head: []string{"workload", "vms", "vms_on", "vcpu", "memory_mib", "recommended_vcpu", "recommended_memory_mib", "raw_used_gib", "provisioned_gib", "guest_used_gib", "iops_avg"}}
	for _, w := range sz.Workloads {
		wl.rows = append(wl.rows, []string{w.Name, itoa(w.VMs), itoa(w.On), itoa(w.VCPU), itoa(w.MemMB), itoa(w.RecVCPU), itoa(w.RecMemMB), gib(w.Used), gib(w.Provisioned), gib(w.GuestUsed), f1(w.IOPS)})
	}

	st := sz.Storage
	sum := csvTable{name: "storage-summary.csv", head: []string{"item", "gib"}, rows: [][]string{
		{"vm_disks", gib(st.VMDisks)}, {"snapshots", gib(st.Snapshots)}, {"other_vm_files", gib(st.Other)}, {"templates", gib(st.Templates)},
		{"raw_device_mappings", gib(st.RDM)}, {"raw_used", gib(st.RawUsed)}, {"planned_usable", gib(st.Plan)}, {"swap_not_included", gib(st.Swap)},
		{"orphaned_not_included", gib(st.Orphans)}, {"powered_off_vms", gib(st.PoweredOff)}, {"provisioned", gib(st.Provisioned)},
		{"guest_used", gib(st.GuestUsed)}, {"datastore_capacity", gib(st.Capacity)}, {"datastore_used", gib(st.Used)},
	}}
	return []csvTable{sum, cls, needs, opts, wl, vms, ds, hosts, adapters, vmk}
}
