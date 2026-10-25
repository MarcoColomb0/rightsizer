package report

import (
	"fmt"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
)

func reduction(r float64) string {
	if r <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f:1", r)
}

// arrays shows what the ONTAP clusters measured: the data reduction of the
// datastores they serve and each cluster's capacity and load.
func (s *sizingDoc) arrays() {
	sz := s.sz
	st := sz.Storage
	if len(st.Arrays) == 0 {
		return
	}
	r := st.Array
	if s.GetY() > 190 {
		s.AddPage()
	}
	s.h2("Measured on NetApp ONTAP")
	s.para("rightsizer read the ONTAP clusters behind the datastores: how much their data shrinks today, the Snapshot copies vSphere does not see, and how busy the controllers are. " +
		"Data reduction is the active data before savings divided by what it occupies after volume deduplication and compression and aggregate compaction; Snapshot copies are left out.")
	if r.Datastores > 0 {
		rows := [][]string{
			{"Datastores on ONTAP", fmt.Sprintf("%d of %d", r.Datastores, r.Of), fmt.Sprintf("%.0f%% of the shared datastores' used capacity", r.Coverage()*100)},
			{"Active data before savings", tb(r.Logical), "As written by the hosts, in the matched volumes"},
			{"After volume savings", tb(r.Physical), "Deduplication and compression: " + reduction(r.VolumeRatio())},
			{"Data reduction", reduction(r.Ratio()), "Including aggregate compaction"},
			{"Array Snapshot copies", tb(r.Snapshots), "Not seen by vSphere, not in raw used"},
			{"Planned usable", tb(st.Plan), "From raw used, as above"},
			{"Planned physical", tb(r.PlanPhysical), "Planned usable at the measured reduction"},
		}
		s.tableWith([]col{{"Measured", 50, "L"}, {"Size", 25, "R"}, {"", 105, "L"}}, rows, tableOpts{pill: -1, hl: func(i int) bool { return i == 3 || i == 6 }})
		s.note("The reduction a new array achieves depends on its platform and on the data: it is a measured reference for an ONTAP successor, and an upper bound to check with other vendors' sizing tools.")
	} else {
		s.para("No datastore matched a volume or LUN of the added clusters.")
	}

	pc := fmt.Sprintf("p%.0f", sz.Percentile)
	rows := [][]string{}
	for _, a := range st.Arrays {
		iops, lat, cpu := "-", "-", "-"
		if a.IO.Available {
			iops = Num(a.IO.IOPS)
			if a.IO.Latency {
				lat = fmt.Sprintf("%.2f", a.IO.LatencyMs)
			}
		}
		if a.CPU > 0 {
			cpu = fmt.Sprintf("%.0f%%", a.CPU)
		}
		rows = append(rows, []string{a.Name, a.Models, a.Version, tb(a.Size), tb(a.Physical), tb(a.Logical), reduction(a.Ratio), tb(a.Snapshots), iops, lat, cpu})
	}
	s.h2("ONTAP clusters")
	s.table([]col{{"Cluster", 22, "L"}, {"Nodes", 26, "L"}, {"ONTAP", 13, "L"}, {"Usable", 16, "R"}, {"Written", 16, "R"}, {"Logical", 16, "R"},
		{"Reduction", 15, "R"}, {"Snapshots", 16, "R"}, {"IOPS " + pc, 15, "R"}, {"ms", 11, "R"}, {"CPU", 14, "R"}}, rows)
	preview := false
	for _, a := range st.Arrays {
		preview = preview || a.Preview
	}
	msg := "Usable is the aggregates' space; written is what they hold after every saving; logical is the data before savings, without Snapshot copies. CPU is the busiest node at " + pc + "."
	if preview {
		msg += " Performance comes from ONTAP's archive until 24 hours of 15-second samples are collected."
	}
	s.note(msg)

	rows = rows[:0]
	for _, a := range st.Arrays {
		for _, g := range a.Aggregates {
			rows = append(rows, []string{a.Name, g.Name, g.Node, fmt.Sprintf("%s %s · %d disks", g.Type, g.RAID, g.Disks), tb(g.Size), tb(g.Physical), tb(g.Logical), reduction(g.Ratio), tb(g.Inactive)})
		}
	}
	if len(rows) > 0 {
		if s.GetY() > 230 {
			s.AddPage()
		}
		s.h2("ONTAP aggregates")
		s.table([]col{{"Cluster", 22, "L"}, {"Aggregate", 30, "L"}, {"Node", 22, "L"}, {"Media", 34, "L"}, {"Usable", 15, "R"}, {"Written", 15, "R"},
			{"Logical", 15, "R"}, {"Reduction", 15, "R"}, {"Cold", 12, "R"}}, rows)
	}
}

// arrayBacking describes the ONTAP volume behind a datastore.
func arrayBacking(a *analysis.DatastoreArray) string {
	b := fmt.Sprintf("ONTAP %s · %s/%s", a.Cluster, a.SVM, a.Volume)
	if a.Ratio > 0 {
		b += " · " + reduction(a.Ratio)
	}
	return b
}
