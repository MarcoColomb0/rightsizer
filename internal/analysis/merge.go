package analysis

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Merge combines the results of several vCenters into one report. Cluster
// and VM names are prefixed with their vCenter so they stay unique.
func Merge(rs []*Result) *Result {
	if len(rs) == 1 {
		return rs[0]
	}
	out := &Result{Generated: time.Now(), Final: true}
	var names []string
	for i, r := range rs {
		src, _, _ := strings.Cut(r.VCenter, " ")
		names = append(names, src)
		if i == 0 || r.Start.Before(out.Start) {
			out.Start = r.Start
		}
		if r.End.After(out.End) {
			out.End = r.End
		}
		out.Planned = max(out.Planned, r.Planned)
		out.Final = out.Final && r.Final
		if i == 0 {
			out.Profile = r.Profile
		} else if r.Profile.Name != out.Profile.Name {
			out.Profile.Name = "mixed"
		}
		t, a := &out.Totals, r.Totals
		t.VMs += a.VMs
		t.On += a.On
		t.Off += a.Off
		t.Templates += a.Templates
		t.Analyzed += a.Analyzed
		t.VCPU += a.VCPU
		t.RecVCPU += a.RecVCPU
		t.MemMB += a.MemMB
		t.RecMemMB += a.RecMemMB
		t.IdleVCPU += a.IdleVCPU
		t.IdleMemMB += a.IdleMemMB
		t.Storage += a.Storage
		t.Reclaim += a.Reclaim
		t.Hosts += a.Hosts
		t.HostsNeeded += a.HostsNeeded
		t.Cores += a.Cores
		t.NeedCores += a.NeedCores
		for _, f := range r.Findings {
			f.Cluster = src + " / " + f.Cluster
			out.Findings = append(out.Findings, f)
		}
		for _, v := range r.VMs {
			v.Cluster = src + " / " + v.Cluster
			out.VMs = append(out.VMs, v)
		}
		for _, c := range r.Clusters {
			c.Name = src + " / " + c.Name
			out.Clusters = append(out.Clusters, c)
		}
	}
	out.VCenter = fmt.Sprintf("%d vCenters: %s", len(rs), strings.Join(names, ", "))
	SortFindings(out.Findings)
	return out
}

// MergeSizing combines the sizing of several vCenters. Storage performance
// is added up, so combined percentiles are an upper bound.
func MergeSizing(ss []*Sizing) *Sizing {
	if len(ss) == 1 {
		return ss[0]
	}
	out := &Sizing{Generated: time.Now(), Final: true}
	var names []string
	wl := map[string]*WorkloadSizing{}
	types := map[string]*TypeUsage{}
	points := map[time.Time]*IOPoint{}
	var covered float64
	var readW, sizeW, iopsW float64
	for i, s := range ss {
		src, _, _ := strings.Cut(s.VCenter, " ")
		names = append(names, src)
		if i == 0 {
			out.Params, out.Percentile, out.Start, out.EstateTaken = s.Params, s.Percentile, s.Start, s.EstateTaken
		}
		if s.Start.Before(out.Start) {
			out.Start = s.Start
		}
		if s.End.After(out.End) {
			out.End = s.End
		}
		if s.EstateTaken.Before(out.EstateTaken) {
			out.EstateTaken = s.EstateTaken
		}
		out.Final = out.Final && s.Final
		out.Preview = out.Preview || s.Preview
		for _, c := range s.Clusters {
			c.Name = src + " / " + c.Name
			out.Clusters = append(out.Clusters, c)
		}
		for _, h := range s.Hosts {
			h.Cluster = src + " / " + h.Cluster
			out.Hosts = append(out.Hosts, h)
		}
		for _, c := range s.ClusterConfig {
			c.Name = src + " / " + c.Name
			out.ClusterConfig = append(out.ClusterConfig, c)
		}
		for _, d := range s.Datastores {
			d.Name = src + " / " + d.Name
			out.Datastores = append(out.Datastores, d)
		}
		for _, v := range s.VMs {
			v.Cluster = src + " / " + v.Cluster
			out.VMs = append(out.VMs, v)
		}
		for _, n := range s.Notes {
			out.Notes = append(out.Notes, src+": "+n)
		}
		for _, w := range s.Workloads {
			a := wl[w.Name]
			if a == nil {
				a = &WorkloadSizing{Name: w.Name}
				wl[w.Name] = a
			}
			a.VMs += w.VMs
			a.On += w.On
			a.VCPU += w.VCPU
			a.MemMB += w.MemMB
			a.RecVCPU += w.RecVCPU
			a.RecMemMB += w.RecMemMB
			a.Used += w.Used
			a.Provisioned += w.Provisioned
			a.GuestUsed += w.GuestUsed
			a.IOPS += w.IOPS
		}
		t, a := &out.Totals, s.Totals
		t.Hosts += a.Hosts
		t.Sockets += a.Sockets
		t.Cores += a.Cores
		t.Threads += a.Threads
		t.MemB += a.MemB
		t.VMs += a.VMs
		t.VMsOff += a.VMsOff
		t.VCPU += a.VCPU
		t.MemMB += a.MemMB

		o, b := &out.Storage, s.Storage
		o.Capacity += b.Capacity
		o.Used += b.Used
		o.Free += b.Free
		o.Datastores += b.Datastores
		o.VMDisks += b.VMDisks
		o.Snapshots += b.Snapshots
		o.Swap += b.Swap
		o.Other += b.Other
		o.Templates += b.Templates
		o.RDM += b.RDM
		o.RDMs += b.RDMs
		o.PhysicalRDM += b.PhysicalRDM
		o.PoweredOff += b.PoweredOff
		o.Provisioned += b.Provisioned
		o.GuestUsed += b.GuestUsed
		o.Orphans += b.Orphans
		o.RawUsed += b.RawUsed
		o.Plan += b.Plan
		covered += b.GuestCoverage * float64(b.VMDisks)
		for _, u := range b.ByType {
			k := u.Type + "|" + u.Protocol
			x := types[k]
			if x == nil {
				x = &TypeUsage{Type: u.Type, Protocol: u.Protocol}
				types[k] = x
			}
			x.Datastores += u.Datastores
			x.Capacity += u.Capacity
			x.Used += u.Used
		}
		if io := b.IO; io.Available {
			m := &o.IO
			m.Available = true
			m.Throughput = m.Throughput || io.Throughput
			m.Latency = m.Latency || io.Latency
			m.Preview = m.Preview || io.Preview
			if m.Hours == 0 || io.Hours < m.Hours {
				m.Hours = io.Hours
			}
			m.IOPS += io.IOPS
			m.IOPSPeak += io.IOPSPeak
			m.IOPSAvg += io.IOPSAvg
			m.MBps += io.MBps
			m.MBpsPeak += io.MBpsPeak
			m.LatencyMs = max(m.LatencyMs, io.LatencyMs)
			readW += io.ReadPct * io.IOPSAvg
			sizeW += io.IOSizeKB * io.IOPSAvg
			iopsW += io.IOPSAvg
			for _, p := range io.Points {
				t := p.T.Truncate(pointEvery)
				x := points[t]
				if x == nil {
					x = &IOPoint{T: t}
					points[t] = x
				}
				x.IOPS += p.IOPS
				x.KBps += p.KBps
			}
		}
	}
	if iopsW > 0 {
		out.Storage.IO.ReadPct = readW / iopsW
		out.Storage.IO.IOSizeKB = sizeW / iopsW
	}
	for _, p := range points {
		out.Storage.IO.Points = append(out.Storage.IO.Points, *p)
	}
	slices.SortFunc(out.Storage.IO.Points, func(a, b IOPoint) int { return a.T.Compare(b.T) })
	if out.Storage.VMDisks > 0 {
		out.Storage.GuestCoverage = covered / float64(out.Storage.VMDisks)
	}
	for _, u := range types {
		out.Storage.ByType = append(out.Storage.ByType, *u)
	}
	slices.SortFunc(out.Storage.ByType, func(a, b TypeUsage) int { return int((b.Used - a.Used) >> 20) })
	for _, w := range wl {
		out.Workloads = append(out.Workloads, *w)
	}
	slices.SortFunc(out.Workloads, func(a, b WorkloadSizing) int {
		if a.Used != b.Used {
			return int((b.Used - a.Used) >> 20)
		}
		return strings.Compare(a.Name, b.Name)
	})
	out.Totals.New = newTotals(out.Clusters)
	out.VCenter = fmt.Sprintf("%d vCenters: %s", len(ss), strings.Join(names, ", "))
	return out
}
