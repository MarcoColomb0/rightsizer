package analysis

import (
	"fmt"
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
		src := strings.SplitN(r.VCenter, " ", 2)[0]
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
