package ontap

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Check is one result of a diagnostic run.
type Check struct {
	Status string // pass, warn, fail or info
	Name   string
	Detail string
}

// SeriesInfo describes what one archive interval returned for an object.
type SeriesInfo struct {
	Object   string
	Interval Interval
	Samples  int
	Error    string `json:",omitempty"`
	First    time.Time
	Last     time.Time
	Steps    map[int32]int
	// Ranges of the values, to spot unit mistakes.
	MaxIOPS  float64
	MaxBps   float64
	MaxLatUs float64
	MinLatUs float64
	MaxCPU   float64
}

// Diagnosis is what a diagnostic run found.
type Diagnosis struct {
	Generated time.Time
	About     string
	Cluster   Cluster
	Checks    []Check
	Inventory *Inventory
	Series    []SeriesInfo
	Dropped   []string
}

// Diagnose reads everything rightsizer uses from the cluster and checks
// the assumptions the sizing makes about it. It only sends GET requests;
// call Record first to keep the raw responses.
func Diagnose(ctx context.Context, c *Client) *Diagnosis {
	// Read again so the cluster's own record is among the exchanges.
	var cl Cluster
	_ = c.get(ctx, "/api/cluster", url.Values{"fields": {"name,uuid,version"}}, &cl)
	d := &Diagnosis{Generated: time.Now(), About: c.About(), Cluster: c.Cluster()}
	add := func(status, name, f string, a ...any) {
		d.Checks = append(d.Checks, Check{status, name, fmt.Sprintf(f, a...)})
	}
	v := c.info.Version
	add("pass", "release", "%s (%s), at least 9.%d", v.Release(), v.Full, MinMajor)

	inv, err := c.Inventory(ctx)
	if err != nil {
		add("fail", "inventory", "%v", err)
		d.Dropped = c.Dropped()
		return d
	}
	d.Inventory = inv
	d.Dropped = c.Dropped()
	if len(d.Dropped) > 0 {
		add("warn", "fields", "refused by this release and left out: %s", strings.Join(d.Dropped, ", "))
	} else {
		add("pass", "fields", "every requested field is known to this release")
	}
	checkNodes(inv, add)
	checkAggregates(inv, add)
	checkVolumes(inv, add)
	checkLUNs(inv, add)
	checkLIFs(inv, add)
	d.Series = diagnoseMetrics(ctx, c, inv, add)
	return d
}

type addFunc func(status, name, f string, a ...any)

func checkNodes(inv *Inventory, add addFunc) {
	if len(inv.Nodes) == 0 {
		add("fail", "nodes", "no node returned")
		return
	}
	var missing []string
	for _, n := range inv.Nodes {
		if n.Model == "" || n.CPUs == 0 || n.MemBytes == 0 {
			missing = append(missing, n.Name)
		}
	}
	if len(missing) > 0 {
		add("warn", "nodes", "model, CPU count or memory missing on %s", strings.Join(missing, ", "))
	} else {
		add("pass", "nodes", "%d nodes: %s", len(inv.Nodes), nodeModels(inv))
	}
}

func nodeModels(inv *Inventory) string {
	var out []string
	for _, n := range inv.Nodes {
		out = append(out, fmt.Sprintf("%s %s (HA %s)", n.Name, n.Model, dash(n.Partner)))
	}
	return strings.Join(out, "; ")
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func checkAggregates(inv *Inventory, add addFunc) {
	if len(inv.Aggregates) == 0 {
		add("fail", "aggregates", "no aggregate returned")
		return
	}
	var noRatio, noNS, odd []string
	for _, a := range inv.Aggregates {
		if a.Ratio <= 0 {
			noRatio = append(noRatio, a.Name)
		}
		if a.RatioNoSnap <= 0 {
			noNS = append(noNS, a.Name)
		}
		// ratio = logical_used / used, as documented.
		if a.Ratio > 0 && a.Logical > 0 && a.Used > 0 {
			if r := float64(a.Logical) / float64(a.Used); math.Abs(r-a.Ratio)/a.Ratio > 0.15 {
				odd = append(odd, fmt.Sprintf("%s ratio %.2f but logical/used %.2f", a.Name, a.Ratio, r))
			}
		}
	}
	switch {
	case len(noRatio) == len(inv.Aggregates):
		add("fail", "aggregate efficiency", "no aggregate reports space.efficiency.ratio: data reduction cannot be measured")
	case len(noRatio) > 0:
		add("warn", "aggregate efficiency", "no ratio on %s", strings.Join(noRatio, ", "))
	default:
		add("pass", "aggregate efficiency", "every aggregate reports its data reduction")
	}
	if len(noNS) > 0 {
		add("info", "aggregate efficiency without snapshots", "not reported on %s; the ratio with Snapshot copies is used", strings.Join(noNS, ", "))
	}
	if len(odd) > 0 {
		add("warn", "aggregate ratio semantics", "%s", strings.Join(odd, "; "))
	}
}

func checkVolumes(inv *Inventory, add addFunc) {
	var rw, noLogical, snapOver, noSaved, mismatch int
	var examples []string
	for _, v := range inv.Volumes {
		if v.Root || v.Type != "rw" {
			continue
		}
		rw++
		if v.Physical > 0 && v.Logical == 0 {
			noLogical++
		}
		// rightsizer assumes physical_used includes Snapshot copies.
		if v.SnapshotUsed > v.Physical && v.Physical > 0 {
			snapOver++
			if len(examples) < 5 {
				examples = append(examples, fmt.Sprintf("%s: physical %d < snapshots %d", v.Name, v.Physical, v.SnapshotUsed))
			}
		}
		if v.Saved == 0 && v.DedupeSaved+v.CompressionSaved > 0 {
			noSaved++
		}
		// and that logical ≈ physical + savings.
		saved := v.Saved
		if saved == 0 {
			saved = v.DedupeSaved + v.CompressionSaved
		}
		if v.Logical > 1<<30 {
			if est := v.Physical + saved; math.Abs(float64(est-v.Logical))/float64(v.Logical) > 0.2 {
				mismatch++
				if len(examples) < 10 {
					examples = append(examples, fmt.Sprintf("%s: logical %d vs physical+saved %d", v.Name, v.Logical, est))
				}
			}
		}
	}
	add("pass", "volumes", "%d volumes, %d read-write data volumes", len(inv.Volumes), rw)
	if noLogical > 0 {
		add("warn", "volume logical space", "%d volumes report no logical_space.used: their savings are taken from the efficiency counters", noLogical)
	}
	if snapOver > 0 {
		add("fail", "snapshot accounting", "%d volumes hold more in Snapshot copies than physical_used: the active-data formula is wrong on this release", snapOver)
	} else {
		add("pass", "snapshot accounting", "physical_used includes Snapshot copies on every volume")
	}
	if noSaved > 0 {
		add("info", "volume savings", "%d volumes without space_savings.total; deduplication plus compression is used", noSaved)
	}
	if mismatch > 0 {
		add("warn", "volume logical vs physical", "%d volumes where logical_space.used differs from physical_used + savings by more than 20%%", mismatch)
	} else if rw > 0 {
		add("pass", "volume logical vs physical", "logical_space.used matches physical_used + savings")
	}
	if len(examples) > 0 {
		add("info", "volume examples", "%s", strings.Join(examples, "; "))
	}
}

func checkLUNs(inv *Inventory, add addFunc) {
	if len(inv.LUNs) == 0 {
		add("warn", "LUNs", "no LUN returned: VMFS datastores cannot be matched (no LUNs, or the role cannot read them)")
		return
	}
	var bad []string
	for _, l := range inv.LUNs {
		if len(l.Serial) != 12 {
			bad = append(bad, fmt.Sprintf("%s serial %q", l.Name, l.Serial))
		}
	}
	if len(bad) > 0 {
		add("warn", "LUN serials", "unexpected serial numbers: %s", strings.Join(bad[:min(len(bad), 5)], "; "))
	} else {
		add("pass", "LUNs", "%d LUNs, e.g. %s seen by ESXi as %s", len(inv.LUNs), inv.LUNs[0].Name, inv.LUNs[0].NAA())
	}
}

func checkLIFs(inv *Inventory, add addFunc) {
	nfs := 0
	for _, l := range inv.LIFs {
		if slices.Contains(l.Services, "data_nfs") {
			nfs++
		}
	}
	switch {
	case len(inv.LIFs) == 0:
		add("warn", "data interfaces", "none returned: NFS datastores are matched by path only (or the role cannot read them)")
	default:
		add("pass", "data interfaces", "%d SVM interfaces, %d serving NFS", len(inv.LIFs), nfs)
	}
}

// diagnoseMetrics reads every archive interval for the cluster, the first
// node and a few volumes, LUN volumes first.
func diagnoseMetrics(ctx context.Context, c *Client, inv *Inventory, add addFunc) []SeriesInfo {
	type obj struct {
		o          Object
		uuid, name string
	}
	objs := []obj{{ClusterObject, "", "cluster"}}
	if len(inv.Nodes) > 0 {
		objs = append(objs, obj{NodeObject, inv.Nodes[0].UUID, "node " + inv.Nodes[0].Name})
	}
	seen := map[string]bool{}
	for _, l := range inv.LUNs {
		if len(seen) < 2 && !seen[l.VolumeUUID] && l.VolumeUUID != "" {
			seen[l.VolumeUUID] = true
			objs = append(objs, obj{VolumeObject, l.VolumeUUID, "volume " + l.Volume})
		}
	}
	for _, v := range inv.Volumes {
		if len(seen) < 3 && v.Path != "" && !v.Root && v.Type == "rw" && !seen[v.UUID] {
			seen[v.UUID] = true
			objs = append(objs, obj{VolumeObject, v.UUID, "volume " + v.Name})
		}
	}
	var out []SeriesInfo
	now := time.Now()
	for _, o := range objs {
		for _, iv := range []Interval{Hour, Day, Week, Month} {
			si := SeriesInfo{Object: o.name, Interval: iv, Steps: map[int32]int{}, MinLatUs: math.Inf(1), MaxCPU: -1}
			ss, err := c.Metrics(ctx, o.o, o.uuid, iv)
			if err != nil {
				si.Error = err.Error()
				out = append(out, si)
				continue
			}
			si.Samples = len(ss)
			for _, s := range ss {
				si.Steps[s.Interval]++
				si.MaxIOPS = max(si.MaxIOPS, s.ReadIOPS+s.WriteIOPS+s.OtherIOPS)
				si.MaxBps = max(si.MaxBps, s.ReadBps+s.WriteBps)
				if s.TotalLatUs > 0 {
					si.MaxLatUs = max(si.MaxLatUs, s.TotalLatUs)
					si.MinLatUs = min(si.MinLatUs, s.TotalLatUs)
				}
				si.MaxCPU = max(si.MaxCPU, s.CPU)
			}
			if math.IsInf(si.MinLatUs, 1) {
				si.MinLatUs = 0
			}
			if len(ss) > 0 {
				si.First, si.Last = ss[0].T, ss[len(ss)-1].T
			}
			out = append(out, si)
		}
	}
	for _, si := range out {
		name := si.Object + " " + string(si.Interval)
		want := int(si.Interval.Span() / (time.Duration(si.Interval.Step()) * time.Second))
		switch {
		case si.Error != "":
			add("fail", "metrics "+name, "%s", si.Error)
		case si.Samples == 0:
			add("warn", "metrics "+name, "no complete sample")
		case si.Samples < want*8/10:
			add("warn", "metrics "+name, "%d of about %d samples (%s to %s)", si.Samples, want, si.First.Format(time.RFC3339), si.Last.Format(time.RFC3339))
		case len(si.Steps) > 1 || si.Steps[si.Interval.Step()] == 0:
			add("warn", "metrics "+name, "sample lengths %v, expected %d s", si.Steps, si.Interval.Step())
		case si.MaxLatUs > 0 && si.MaxLatUs < 20:
			add("warn", "metrics "+name, "latency peaks at %.0f: not microseconds?", si.MaxLatUs)
		case si.MaxCPU > 100:
			add("warn", "metrics "+name, "CPU above 100%%: %.0f", si.MaxCPU)
		default:
			if si.MaxCPU >= 0 {
				add("pass", "metrics "+name, "%d samples · CPU ≤ %.0f%%", si.Samples, si.MaxCPU)
			} else {
				add("pass", "metrics "+name, "%d samples · IOPS ≤ %.0f · %.0f MB/s · latency %.0f-%.0f µs", si.Samples, si.MaxIOPS, si.MaxBps/(1<<20), si.MinLatUs, si.MaxLatUs)
			}
		}
		if si.Interval == Month && si.Error == "" && !si.First.IsZero() && now.Sub(si.First) < 14*24*time.Hour {
			add("warn", "history "+si.Object, "the archive starts %s: less than the 14 days imported", si.First.Format(time.RFC3339))
		}
	}
	return out
}

// Summary renders checks as text, failures first.
func (d *Diagnosis) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s\n\n", d.About, d.Generated.Format(time.RFC3339))
	order := map[string]int{"fail": 0, "warn": 1, "pass": 2, "info": 3}
	cs := slices.Clone(d.Checks)
	slices.SortStableFunc(cs, func(a, b Check) int { return order[a.Status] - order[b.Status] })
	for _, c := range cs {
		fmt.Fprintf(&b, "[%s] %s: %s\n", strings.ToUpper(c.Status), c.Name, c.Detail)
	}
	return b.String()
}
