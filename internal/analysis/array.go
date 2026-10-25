package analysis

import (
	"bytes"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/ontap"
	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

// ArrayStore holds the performance of one ONTAP cluster: the whole cluster,
// every volume behind a datastore, and each node's CPU.
type ArrayStore struct {
	Cluster *IOStats
	Volumes map[string]*IOStats
	CPU     map[string]*Hist
	// Seconds is the time covered per object, Last its newest sample.
	Seconds map[string]float64
	Last    map[string]time.Time
	Polls   uint64
}

func NewArrayStore() *ArrayStore {
	return &ArrayStore{Cluster: &IOStats{}, Volumes: map[string]*IOStats{}, CPU: map[string]*Hist{},
		Seconds: map[string]float64{}, Last: map[string]time.Time{}}
}

func arrayKey(o ontap.Object, id string) string {
	switch o {
	case ontap.NodeObject:
		return "node:" + id
	case ontap.VolumeObject:
		return "volume:" + id
	case ontap.ClusterObject:
	}
	return "cluster"
}

// LastOf is the newest sample recorded for an object.
func (st *ArrayStore) LastOf(o ontap.Object, id string) time.Time { return st.Last[arrayKey(o, id)] }

// Add records the samples of one object that end after its newest one and
// within [from, to): overlapping archive intervals are counted once. id is
// a volume UUID or a node name.
func (st *ArrayStore) Add(o ontap.Object, id string, ss []ontap.Sample, from, to time.Time) {
	key := arrayKey(o, id)
	last := st.Last[key]
	for _, s := range ss {
		if !s.T.After(last) || s.T.Before(from) || !s.T.Before(to) {
			continue
		}
		w := weight(s.Interval)
		switch o {
		case ontap.NodeObject:
			h := st.CPU[id]
			if h == nil {
				h = &Hist{}
				st.CPU[id] = h
			}
			h.AddN(s.CPU, w)
		case ontap.ClusterObject, ontap.VolumeObject:
			a := &ioSample{r: s.ReadIOPS, w: s.WriteIOPS, rkb: s.ReadBps / 1024, wkb: s.WriteBps / 1024, ops: true, kb: true}
			if s.ReadIOPS > 0 {
				a.latW += s.ReadLatUs / 1000 * s.ReadIOPS
				a.latIO += s.ReadIOPS
			}
			if s.WriteIOPS > 0 {
				a.latW += s.WriteLatUs / 1000 * s.WriteIOPS
				a.latIO += s.WriteIOPS
			}
			if o == ontap.ClusterObject {
				st.Cluster.add(a, s.T, w, true)
			} else {
				io := st.Volumes[id]
				if io == nil {
					io = &IOStats{}
					st.Volumes[id] = io
				}
				io.add(a, s.T, w, false)
			}
		}
		st.Seconds[key] += float64(max(s.Interval, 1))
		last = s.T
	}
	st.Last[key] = last
	st.Cluster.flush(last.Add(-pointEvery))
}

// Mark records an object as read up to t without samples, as when its
// archive is missing.
func (st *ArrayStore) Mark(o ontap.Object, id string, t time.Time) {
	if k := arrayKey(o, id); st.Last[k].Before(t) {
		st.Last[k] = t
	}
}

// Flush moves every pending 5-minute average into the cluster timeline.
func (st *ArrayStore) Flush() { st.Cluster.Flush() }

// ArrayInput is one ONTAP cluster for sizing.
type ArrayInput struct {
	Host    string
	About   string
	Inv     *ontap.Inventory
	RT      *ArrayStore
	History *ArrayStore
}

// store picks 15-second data once it covers a day, like vCenter data.
func (a ArrayInput) store() (*ArrayStore, bool) {
	if a.RT != nil && a.RT.Seconds["cluster"] >= 24*3600 {
		return a.RT, false
	}
	if a.History != nil && a.History.Seconds["cluster"] > 0 {
		return a.History, true
	}
	return a.RT, false
}

// ArraySizing describes one ONTAP cluster: what it holds, how well it
// reduces data and how busy it is.
type ArraySizing struct {
	Host    string
	Name    string
	Version string
	Models  string
	Nodes   int
	// Size, Used and Physical are the aggregates' usable, used and written
	// space; Logical the data before savings.
	Size        int64
	Used        int64
	Physical    int64
	Logical     int64
	Ratio       float64
	Snapshots   int64
	Inactive    int64
	Matched     int
	Volumes     int
	MatchedData int64
	// OtherData is the logical data in volumes no analysed datastore uses.
	OtherData  int64
	IO         IOSummary
	CPU        float64
	CPUPeak    float64
	CPUNode    string
	Preview    bool
	Hours      float64
	Aggregates []AggregateSizing
}

type AggregateSizing struct {
	Name      string
	Node      string
	Type      string
	RAID      string
	Disks     int
	Size      int64
	Used      int64
	Physical  int64
	Logical   int64
	Ratio     float64
	Inactive  int64
	Snapshots int64
}

// ArrayVolume is one matched volume's share of the measurement, kept so
// vCenters sharing a volume count it once when combined.
type ArrayVolume struct {
	Array     string
	UUID      string
	Data      int64
	Logical   int64
	Physical  int64
	Reduced   float64
	Snapshots int64
}

func arrayID(a *ArrayInput) string { return a.Host + "/" + a.Inv.Cluster.Name }

// DatastoreArray is the ONTAP storage behind a datastore.
type DatastoreArray struct {
	Cluster   string
	SVM       string
	Volume    string
	Aggregate string
	LUNs      int
	// Logical and Physical are the volume's active data before and after
	// deduplication and compression; Ratio adds aggregate savings.
	Logical   int64
	Physical  int64
	Ratio     float64
	Snapshots int64
	IOPS      float64
	LatencyMs float64
	Latency   bool
}

// ArrayReduction is the data reduction measured on ONTAP for the datastores
// it backs. The sums add up across vCenters.
type ArrayReduction struct {
	Datastores int
	Of         int
	// Used is what vSphere reports used on the matched datastores, OfUsed
	// on every shared datastore.
	Used   int64
	OfUsed int64
	// Logical and Physical are the matched volumes' active data before and
	// after volume savings; Reduced divides each by its aggregate's ratio.
	Logical   int64
	Physical  int64
	Reduced   float64
	Snapshots int64
	// PlanPhysical is planned usable capacity divided by Ratio.
	PlanPhysical int64
}

// Ratio is the measured reduction including aggregate savings.
func (r ArrayReduction) Ratio() float64 {
	if r.Reduced <= 0 {
		return 0
	}
	return float64(r.Logical) / r.Reduced
}

// VolumeRatio is the reduction from volume deduplication and compression.
func (r ArrayReduction) VolumeRatio() float64 {
	if r.Physical <= 0 {
		return 0
	}
	return float64(r.Logical) / float64(r.Physical)
}

// Coverage is the share of shared datastore data the measurement covers.
func (r ArrayReduction) Coverage() float64 {
	if r.OfUsed <= 0 {
		return 0
	}
	return float64(r.Used) / float64(r.OfUsed)
}

// activeData is a volume's data without Snapshot copies, before and after
// volume savings.
func activeData(v *ontap.Volume) (logical, physical int64) {
	physical = max(v.Physical-v.SnapshotUsed, 0)
	saved := v.Saved
	if saved == 0 {
		saved = v.DedupeSaved + v.CompressionSaved
	}
	return physical + max(saved, 0), physical
}

// aggrFactor is the reduction an aggregate adds on top of its volumes'
// own deduplication and compression: compaction, which applies to every
// volume alike.
func aggrFactor(inv *ontap.Inventory, v *ontap.Volume) float64 {
	f := 1.0
	for _, name := range v.Aggregates {
		if a := inv.Aggregate(name); a != nil && a.PhysicalUsed > 0 {
			f = max(f, 1+float64(a.CompactionSaved)/float64(a.PhysicalUsed))
		}
	}
	return min(f, 2)
}

type lunRef struct {
	arr *ArrayInput
	lun ontap.LUN
}

type arrayIndex struct {
	arrays []ArrayInput
	naa    map[string]lunRef
}

func newArrayIndex(arrays []ArrayInput) *arrayIndex {
	ix := &arrayIndex{arrays: arrays, naa: map[string]lunRef{}}
	for i := range arrays {
		a := &ix.arrays[i]
		if a.Inv == nil {
			continue
		}
		for _, l := range a.Inv.LUNs {
			if id := l.NAA(); id != "" {
				ix.naa[id] = lunRef{a, l}
			}
		}
	}
	return ix
}

// splitRemote parses a datastore's NFS export, host[,host]:/path.
func splitRemote(remote string) ([]string, string) {
	i := strings.Index(remote, ":/")
	if i < 0 {
		return nil, ""
	}
	return strings.Split(remote[:i], ","), strings.TrimRight(remote[i+1:], "/")
}

// nfsVolume finds the volume whose junction path holds the export: the
// longest path, on an SVM that has a data interface with one of the hosts
// when they are addresses and interfaces could be read.
func (ix *arrayIndex) nfsVolume(remote string) (*ArrayInput, *ontap.Volume) {
	hosts, p := splitRemote(remote)
	if p == "" {
		return nil, nil
	}
	var best *ontap.Volume
	var bestArr *ArrayInput
	matches := 0
	for i := range ix.arrays {
		a := &ix.arrays[i]
		if a.Inv == nil {
			continue
		}
		svms := map[string]bool{}
		ips := false
		for _, h := range hosts {
			if net.ParseIP(h) == nil {
				continue
			}
			ips = true
			for _, l := range a.Inv.LIFs {
				if l.IP == h {
					svms[l.SVM] = true
				}
			}
		}
		strict := ips && len(a.Inv.LIFs) > 0
		for j := range a.Inv.Volumes {
			v := &a.Inv.Volumes[j]
			vp := strings.TrimRight(v.Path, "/")
			if v.Path == "" || (p != vp && !strings.HasPrefix(p, vp+"/") && vp != "") {
				continue
			}
			if strict && !svms[v.SVM] {
				continue
			}
			if best == nil || len(vp) > len(strings.TrimRight(best.Path, "/")) {
				best, bestArr, matches = v, a, 1
			} else if len(vp) == len(strings.TrimRight(best.Path, "/")) {
				matches++
			}
		}
	}
	if matches != 1 {
		return nil, nil
	}
	return bestArr, best
}

// backing finds the ONTAP volume behind a datastore and how many of its
// extents are ONTAP LUNs.
func (ix *arrayIndex) backing(d vc.DatastoreDetail) (*ArrayInput, *ontap.Volume, int) {
	if d.Remote != "" {
		a, v := ix.nfsVolume(d.Remote)
		return a, v, 0
	}
	var arr *ArrayInput
	var vol *ontap.Volume
	n := 0
	for _, l := range d.LUNs {
		ref, ok := ix.naa[strings.ToLower(l.Name)]
		if !ok {
			continue
		}
		n++
		if vol == nil {
			arr, vol = ref.arr, ref.arr.Inv.Volume(ref.lun.VolumeUUID)
		}
	}
	return arr, vol, n
}

// isNetApp reports a datastore on NetApp storage that no added cluster
// covers: a NetApp LUN or an NFS export.
func isNetApp(d vc.DatastoreDetail) bool {
	for _, l := range d.LUNs {
		if strings.EqualFold(l.Vendor, "NETAPP") || strings.HasPrefix(strings.ToLower(l.Name), "naa.600a0980") {
			return true
		}
	}
	return false
}

// sizeArrays matches datastores with ONTAP volumes and measures the data
// reduction and load of every cluster.
func sizeArrays(sz *Sizing, arrays []ArrayInput) {
	s := &sz.Storage
	pct := sz.Percentile
	ix := newArrayIndex(arrays)
	matched := map[*ArrayInput]map[string]bool{}
	datastores := map[*ArrayInput]int{}
	seen := map[string]bool{}
	for i := range sz.Datastores {
		d := &sz.Datastores[i]
		if !d.Accessible || d.Local || d.Protocol == "vSAN" || d.Protocol == "vVols" || d.Protocol == "Local" {
			continue
		}
		s.Array.Of++
		s.Array.OfUsed += d.Used
		if isNetApp(d.DatastoreDetail) {
			s.NetAppDatastores++
		}
		a, v, luns := ix.backing(d.DatastoreDetail)
		if v == nil {
			continue
		}
		s.Array.Datastores++
		s.Array.Used += d.Used
		if matched[a] == nil {
			matched[a] = map[string]bool{}
		}
		matched[a][v.UUID] = true
		datastores[a]++
		lg, ph := activeData(v)
		da := &DatastoreArray{Cluster: a.Inv.Cluster.Name, SVM: v.SVM, Volume: v.Name, Aggregate: strings.Join(v.Aggregates, ", "), LUNs: luns,
			Logical: lg, Physical: ph, Snapshots: v.SnapshotUsed}
		if ph > 0 {
			da.Ratio = float64(lg) / float64(ph) * aggrFactor(a.Inv, v)
		}
		if st, _ := a.store(); st != nil {
			if io := st.Volumes[v.UUID]; io != nil && io.IOPS.N > 0 {
				da.IOPS = io.IOPS.Pct(pct)
				if io.Latency.N > 0 {
					da.LatencyMs, da.Latency = io.Latency.Pct(pct), true
				}
			}
		}
		d.Array = da
		// A volume holding several datastores counts once.
		key := a.Inv.Cluster.UUID + "/" + v.UUID
		if seen[key] {
			continue
		}
		seen[key] = true
		av := ArrayVolume{Array: arrayID(a), UUID: key, Data: lg, Snapshots: v.SnapshotUsed}
		// A volume holding nothing outside its Snapshot copies has no
		// measurable reduction: its savings would count with nothing
		// written against them.
		if ph > 0 && da.Ratio > 0 {
			av.Logical, av.Physical, av.Reduced = lg, ph, float64(lg)/da.Ratio
		}
		s.ArrayVolumes = append(s.ArrayVolumes, av)
		s.Array.Logical += av.Logical
		s.Array.Physical += av.Physical
		s.Array.Reduced += av.Reduced
		s.Array.Snapshots += av.Snapshots
	}
	if r := s.Array.Ratio(); r > 0 {
		s.Array.PlanPhysical = int64(float64(s.Plan) / r)
	}
	for i := range arrays {
		a := &arrays[i]
		if a.Inv == nil {
			continue
		}
		as := arraySizing(a, matched[a], pct)
		as.Matched = datastores[a]
		s.Arrays = append(s.Arrays, as)
	}
	slices.SortFunc(s.Arrays, func(a, b ArraySizing) int { return strings.Compare(a.Name, b.Name) })
}

func arraySizing(a *ArrayInput, vols map[string]bool, pct float64) ArraySizing {
	inv := a.Inv
	t := inv.Totals()
	out := ArraySizing{Host: a.Host, Name: inv.Cluster.Name, Version: inv.Cluster.Version.Release(), Nodes: len(inv.Nodes),
		Size: t.Size, Used: t.Used, Physical: t.Physical, Logical: t.LogicalNoSnap, Ratio: t.RatioNoSnap, Snapshots: t.Snapshots, Inactive: t.Inactive,
		Volumes: len(vols)}
	if out.Ratio == 0 {
		out.Logical, out.Ratio = t.Logical, t.Ratio
	}
	models := map[string]int{}
	for _, n := range inv.Nodes {
		models[n.Model]++
	}
	out.Models = countList(models, func(s string) string { return s })
	for _, v := range inv.Volumes {
		if v.Root || v.Type != "rw" {
			continue
		}
		out.Snapshots += v.SnapshotUsed
		lg, _ := activeData(&v)
		if vols[v.UUID] {
			out.MatchedData += lg
		} else {
			out.OtherData += lg
		}
	}
	for _, ag := range inv.Aggregates {
		r := ag.RatioNoSnap
		lg := ag.LogicalNoSnap
		if r == 0 {
			r, lg = ag.Ratio, ag.Logical
		}
		typ := ag.DiskType
		if typ == "" {
			typ = ag.StorageType
		}
		out.Aggregates = append(out.Aggregates, AggregateSizing{Name: ag.Name, Node: ag.Node, Type: strings.ToUpper(typ), RAID: ag.RAID, Disks: ag.Disks,
			Size: ag.Size, Used: ag.Used, Physical: ag.PhysicalUsed, Logical: lg, Ratio: r, Inactive: ag.Inactive, Snapshots: ag.SnapshotUsed})
	}
	st, preview := a.store()
	if st == nil {
		return out
	}
	out.Preview = preview
	out.IO = ioSummary(st.Cluster, pct)
	out.IO.Preview = preview && out.IO.Available
	out.IO.Hours = st.Seconds["cluster"] / 3600
	out.Hours = out.IO.Hours
	out.IO.Points = DownsampleIO(st.Cluster.Points, 336)
	for _, n := range inv.Nodes {
		if h := st.CPU[n.Name]; h != nil && h.N > 0 {
			if p := h.Pct(pct); p > out.CPU {
				out.CPU, out.CPUNode = p, n.Name
			}
			out.CPUPeak = max(out.CPUPeak, h.Max)
		}
	}
	return out
}

// arrayNotes explains what ONTAP adds to the storage sizing.
func arrayNotes(sz *Sizing, add func(string, ...any)) {
	st := sz.Storage
	r := st.Array
	if len(st.Arrays) == 0 {
		if st.NetAppDatastores > 0 {
			add("%d datastore(s) are on NetApp LUNs. Add their ONTAP cluster to measure the data reduction they get today and the controllers' load.", st.NetAppDatastores)
		}
		return
	}
	if r.Datastores > 0 {
		msg := fmt.Sprintf("ONTAP reduces the data of %d of %d shared datastore(s) %.1f:1 today (%.1f:1 from volume deduplication and compression, the rest from compaction), without Snapshot copies.",
			r.Datastores, r.Of, r.Ratio(), r.VolumeRatio())
		if r.PlanPhysical > 0 {
			msg += fmt.Sprintf(" With the same reduction, the planned usable capacity of %s needs about %s of physical capacity.", Human(st.Plan), Human(r.PlanPhysical))
		}
		if c := r.Coverage(); c < 0.95 {
			msg += fmt.Sprintf(" The measurement covers %.0f%% of the shared datastores' data; the rest is on storage that was not added.", c*100)
		}
		add("%s", msg)
	} else {
		add("No datastore matched a volume or LUN of the added ONTAP clusters: check that the cluster serving these datastores is added, and that its role can read LUNs and network interfaces.")
	}
	if r.Snapshots > 10<<30 {
		add("ONTAP Snapshot copies of the datastore volumes hold %s. vSphere does not see them and they are not in raw used capacity: plan space for them if the new storage keeps the same snapshot schedule.", Human(r.Snapshots))
	}
	for _, a := range st.Arrays {
		if a.OtherData > 100<<30 && a.OtherData > a.MatchedData/10 {
			add("%s also holds %s of data no analysed datastore uses (file shares, other hosts or vCenters). Include it if the array is replaced.", a.Name, Human(a.OtherData))
		}
		if a.CPU >= 50 {
			add("%s controllers reach %.0f%% CPU at p%.0f (%s, peak %.0f%%). With an HA partner down, one node carries both: keep the new controllers below 50%% each.", a.Name, a.CPU, sz.Percentile, a.CPUNode, a.CPUPeak)
		}
		if a.Inactive > 0 && a.Physical > 0 && float64(a.Inactive)/float64(a.Physical) > 0.2 {
			add("%s reports %s of inactive data (%.0f%% of what is written): a capacity tier or cheaper media could hold it.", a.Name, Human(a.Inactive), float64(a.Inactive)/float64(a.Physical)*100)
		}
		if a.Preview {
			add("%s performance is from ONTAP's archive (5-minute to 2-hour averages smooth out peaks) until 24 hours of 15-second samples are collected.", a.Name)
		}
	}
}

// mergeArrays combines the ONTAP figures of several vCenters: clusters
// serving more than one appear once.
func mergeArrays(out *StorageSizing, in StorageSizing) {
	o := &out.Array
	o.Datastores += in.Array.Datastores
	o.Of += in.Array.Of
	o.Used += in.Array.Used
	o.OfUsed += in.Array.OfUsed
	out.NetAppDatastores += in.NetAppDatastores
	known := map[string]bool{}
	for _, a := range out.Arrays {
		known[a.Host+"/"+a.Name] = true
	}
	seen := map[string]bool{}
	for _, v := range out.ArrayVolumes {
		seen[v.UUID] = true
	}
	// A volume serving datastores of several vCenters counts once.
	newData, newVols := map[string]int64{}, map[string]int{}
	for _, v := range in.ArrayVolumes {
		if seen[v.UUID] {
			continue
		}
		seen[v.UUID] = true
		out.ArrayVolumes = append(out.ArrayVolumes, v)
		o.Logical += v.Logical
		o.Physical += v.Physical
		o.Reduced += v.Reduced
		o.Snapshots += v.Snapshots
		newData[v.Array] += v.Data
		newVols[v.Array]++
	}
	for _, a := range in.Arrays {
		key := a.Host + "/" + a.Name
		if !known[key] {
			out.Arrays = append(out.Arrays, a)
			continue
		}
		i := slices.IndexFunc(out.Arrays, func(b ArraySizing) bool { return b.Host+"/"+b.Name == key })
		b := &out.Arrays[i]
		b.Matched += a.Matched
		b.Volumes += newVols[key]
		b.MatchedData += newData[key]
		b.OtherData = max(b.OtherData-newData[key], 0)
	}
	slices.SortFunc(out.Arrays, func(a, b ArraySizing) int { return strings.Compare(a.Name, b.Name) })
}

// MatchedVolumes lists the volumes of a cluster that back datastores of the
// estates, and how many datastores they back.
func MatchedVolumes(inv *ontap.Inventory, estates []*vc.Estate) ([]string, int) {
	ix := newArrayIndex([]ArrayInput{{Inv: inv}})
	seen := map[string]bool{}
	var out []string
	n := 0
	for _, est := range estates {
		if est == nil {
			continue
		}
		for _, d := range est.Datastores {
			if !d.Accessible || d.Local {
				continue
			}
			if _, v, _ := ix.backing(d); v != nil {
				n++
				if !seen[v.UUID] {
					seen[v.UUID] = true
					out = append(out, v.UUID)
				}
			}
		}
	}
	slices.Sort(out)
	return out, n
}

// SummarizeArray describes one cluster on its own, with the volumes that
// back analysed datastores.
func SummarizeArray(a ArrayInput, volumes []string, datastores int, pct float64) ArraySizing {
	vols := map[string]bool{}
	for _, v := range volumes {
		vols[v] = true
	}
	out := arraySizing(&a, vols, pct)
	out.Matched = datastores
	return out
}

// Clone copies the store so it can be read while collection continues.
func (st *ArrayStore) Clone() *ArrayStore {
	if st == nil {
		return nil
	}
	var buf bytes.Buffer
	out := &ArrayStore{}
	if err := gob.NewEncoder(&buf).Encode(st); err != nil {
		return NewArrayStore()
	}
	if err := gob.NewDecoder(&buf).Decode(out); err != nil {
		return NewArrayStore()
	}
	if out.Cluster == nil {
		out.Cluster = &IOStats{}
	}
	if out.Seconds == nil {
		out.Seconds = map[string]float64{}
	}
	if out.Volumes == nil {
		out.Volumes = map[string]*IOStats{}
	}
	if out.CPU == nil {
		out.CPU = map[string]*Hist{}
	}
	if out.Last == nil {
		out.Last = map[string]time.Time{}
	}
	return out
}

// MatchRow explains how one datastore was matched to an ONTAP cluster.
type MatchRow struct {
	VCenter   string
	Datastore string
	Type      string
	Backing   string
	Volume    string
	Result    string
}

// ExplainMatches says, for every datastore of every vCenter, which volume
// of the cluster backs it, or why none does.
func ExplainMatches(inv *ontap.Inventory, estates map[string]*vc.Estate) []MatchRow {
	ix := newArrayIndex([]ArrayInput{{Inv: inv}})
	var out []MatchRow
	vcs := make([]string, 0, len(estates))
	for k := range estates {
		vcs = append(vcs, k)
	}
	slices.Sort(vcs)
	for _, name := range vcs {
		est := estates[name]
		if est == nil {
			continue
		}
		for _, d := range est.Datastores {
			row := MatchRow{VCenter: name, Datastore: d.Name, Type: d.Version}
			proto := protocolOf(d)
			switch {
			case d.Remote != "":
				row.Backing = d.Remote
			default:
				var luns []string
				for _, l := range d.LUNs {
					luns = append(luns, strings.TrimSpace(l.Name+" "+l.Vendor))
				}
				row.Backing = strings.Join(luns, ", ")
			}
			_, v, _ := ix.backing(d)
			switch {
			case !d.Accessible:
				row.Result = "skipped: not accessible"
			case d.Local || proto == "Local" || proto == "vSAN" || proto == "vVols":
				row.Result = "skipped: " + proto + " datastore"
			case v != nil:
				row.Volume = v.SVM + "/" + v.Name
				row.Result = "matched"
			case d.Remote != "":
				row.Result = explainNFS(inv, d.Remote)
			default:
				row.Result = explainLUNs(d)
			}
			out = append(out, row)
		}
	}
	return out
}

func explainLUNs(d vc.DatastoreDetail) string {
	if len(d.LUNs) == 0 {
		return "no LUN extent reported by vSphere"
	}
	var parts []string
	for _, l := range d.LUNs {
		name := strings.ToLower(l.Name)
		if hexSerial, ok := strings.CutPrefix(name, "naa.600a0980"); ok {
			serial := hexSerial
			if b, err := hex.DecodeString(hexSerial); err == nil {
				serial = string(b)
			}
			parts = append(parts, fmt.Sprintf("NetApp LUN with serial %q is not on this cluster (another cluster, or the role cannot read LUNs)", serial))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s is not a NetApp ONTAP LUN (vendor %q)", l.Name, l.Vendor))
	}
	return strings.Join(parts, "; ")
}

func explainNFS(inv *ontap.Inventory, remote string) string {
	hosts, p := splitRemote(remote)
	if p == "" {
		return "export not in host:/path form"
	}
	var ips []string
	for _, h := range hosts {
		if net.ParseIP(h) != nil {
			ips = append(ips, h)
		}
	}
	owned := false
	for _, l := range inv.LIFs {
		if slices.Contains(ips, l.IP) {
			owned = true
		}
	}
	if len(ips) > 0 && len(inv.LIFs) > 0 && !owned {
		return fmt.Sprintf("export address %s is not a data interface of this cluster", strings.Join(ips, ", "))
	}
	var cands []ontap.Volume
	for _, v := range inv.Volumes {
		vp := strings.TrimRight(v.Path, "/")
		if v.Path != "" && (p == vp || strings.HasPrefix(p, vp+"/") || vp == "") {
			cands = append(cands, v)
		}
	}
	switch {
	case len(cands) == 0:
		return fmt.Sprintf("no volume is mounted at %s or above it", p)
	case len(ips) > 0 && len(inv.LIFs) > 0:
		return fmt.Sprintf("the volume mounted at %s is on an SVM without an interface at %s", cands[0].Path, strings.Join(ips, ", "))
	}
	return fmt.Sprintf("%d volumes could hold %s and the export names a host, not an address: ambiguous", len(cands), p)
}
