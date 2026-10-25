// Package ontapsim is a fake ONTAP REST API for tests, built from the
// documented schema: the same records, field names, pagination, archive
// intervals and errors as a cluster, without one.
package ontapsim

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/ontap"
)

// Server serves one cluster's inventory and generated performance data.
type Server struct {
	*httptest.Server
	User, Password string

	mu  sync.Mutex
	inv *ontap.Inventory
	// PageSize splits collections into pages linked by next, 0 for one page.
	PageSize int
	// Unsupported fields are refused as an older release does.
	Unsupported []string
	// Forbidden paths answer 403, as for a role without access.
	Forbidden []string
	// Load returns the performance of an object at t; Default is used when
	// nil.
	Load func(o ontap.Object, uuid string, t time.Time) ontap.Sample
	// Now is the end of the performance archive; time.Now when zero.
	Now      time.Time
	requests []string
}

// New starts a TLS server for inv with a self-signed certificate.
func New(inv *ontap.Inventory) *Server {
	s := &Server{User: "monitor", Password: "s3cret-pass", inv: inv}
	s.Server = httptest.NewUnstartedServer(http.HandlerFunc(s.serve))
	s.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	// Refused handshakes are what pinning tests expect.
	s.Config.ErrorLog = log.New(io.Discard, "", 0)
	s.StartTLS()
	return s
}

// Host is the address to connect to.
func (s *Server) Host() string { return strings.TrimPrefix(s.URL, "https://") }

// Fingerprint is the pinned form of the server certificate.
func (s *Server) Fingerprint() string { return ontap.Thumbprint(s.Certificate()) }

// Requests lists every request received, as "METHOD path".
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// SetInventory replaces the cluster's records.
func (s *Server) SetInventory(inv *ontap.Inventory) {
	s.mu.Lock()
	s.inv = inv
	s.mu.Unlock()
}

func fail(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": msg, "code": code}})
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, r.Method+" "+r.URL.Path)
	inv := s.inv
	s.mu.Unlock()
	if u, p, ok := r.BasicAuth(); !ok || u != s.User || p != s.Password {
		fail(w, http.StatusUnauthorized, "6691623", "User is not authorized.")
		return
	}
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "6", "method not allowed")
		return
	}
	if slices.Contains(s.Forbidden, r.URL.Path) {
		fail(w, http.StatusForbidden, "6", "not authorized for that command")
		return
	}
	q := r.URL.Query()
	if fs := q.Get("fields"); fs != "" {
		for f := range strings.SplitSeq(fs, ",") {
			if slices.Contains(s.Unsupported, f) {
				fail(w, http.StatusBadRequest, "262197", fmt.Sprintf("The value \"%s\" is invalid for field \"fields\"", f))
				return
			}
		}
	}
	path := r.URL.Path
	switch {
	case path == "/api/cluster":
		writeJSON(w, cluster(inv.Cluster))
	case path == "/api/cluster/nodes":
		s.collection(w, r, mapEach(inv.Nodes, node))
	case path == "/api/storage/aggregates":
		s.collection(w, r, mapEach(inv.Aggregates, aggregate))
	case path == "/api/storage/volumes":
		s.collection(w, r, mapEach(inv.Volumes, volume))
	case path == "/api/storage/luns":
		s.collection(w, r, mapEach(inv.LUNs, lun))
	case path == "/api/network/ip/interfaces":
		s.collection(w, r, mapEach(inv.LIFs, lif))
	case path == "/api/cluster/metrics":
		s.metrics(w, r, ontap.ClusterObject, "")
	case strings.HasPrefix(path, "/api/cluster/nodes/") && strings.HasSuffix(path, "/metrics"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/api/cluster/nodes/"), "/metrics")
		if !slices.ContainsFunc(inv.Nodes, func(n ontap.Node) bool { return n.UUID == id }) {
			fail(w, http.StatusNotFound, "4", "entry doesn't exist")
			return
		}
		s.metrics(w, r, ontap.NodeObject, id)
	case strings.HasPrefix(path, "/api/storage/volumes/") && strings.HasSuffix(path, "/metrics"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/api/storage/volumes/"), "/metrics")
		if inv.Volume(id) == nil {
			fail(w, http.StatusNotFound, "4", "entry doesn't exist")
			return
		}
		s.metrics(w, r, ontap.VolumeObject, id)
	default:
		fail(w, http.StatusNotFound, "4", "API not found")
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/hal+json")
	_ = json.NewEncoder(w).Encode(v)
}

func mapEach[T any](xs []T, f func(T) map[string]any) []map[string]any {
	out := make([]map[string]any, len(xs))
	for i, x := range xs {
		out[i] = f(x)
	}
	return out
}

// collection pages records the way ONTAP does: num_records, records and a
// next link carrying the original query.
func (s *Server) collection(w http.ResponseWriter, r *http.Request, recs []map[string]any) {
	q := r.URL.Query()
	start, _ := strconv.Atoi(q.Get("start.index"))
	size := len(recs)
	if s.PageSize > 0 {
		size = s.PageSize
	}
	if m, err := strconv.Atoi(q.Get("max_records")); err == nil && m > 0 && m < size {
		size = m
	}
	start = min(start, len(recs))
	end := min(start+max(size, 1), len(recs))
	page := recs[start:end]
	if fs := q.Get("fields"); fs != "" {
		page = make([]map[string]any, end-start)
		for i, rec := range recs[start:end] {
			page[i] = project(rec, strings.Split(fs, ","))
		}
	}
	out := map[string]any{"records": page, "num_records": end - start}
	if end < len(recs) {
		nq := url.Values{}
		maps.Copy(nq, q)
		nq.Set("start.index", strconv.Itoa(end))
		out["_links"] = map[string]any{"next": map[string]any{"href": r.URL.Path + "?" + nq.Encode()}}
	}
	writeJSON(w, out)
}

// project keeps only the requested fields, as ONTAP returns only those: a
// dotted field selects inside objects and inside each element of arrays.
func project(rec map[string]any, fields []string) map[string]any {
	sub := map[string][]string{}
	whole := map[string]bool{}
	for _, f := range fields {
		head, rest, nested := strings.Cut(f, ".")
		if nested {
			sub[head] = append(sub[head], rest)
		} else {
			whole[head] = true
		}
	}
	out := map[string]any{}
	for k, v := range rec {
		switch {
		case whole[k]:
			out[k] = v
		case sub[k] != nil:
			switch x := v.(type) {
			case map[string]any:
				out[k] = project(x, sub[k])
			case []any:
				arr := make([]any, len(x))
				for i, e := range x {
					if m, ok := e.(map[string]any); ok {
						arr[i] = project(m, sub[k])
					}
				}
				out[k] = arr
			}
		}
	}
	return out
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request, o ontap.Object, uuid string) {
	iv := ontap.Interval(r.URL.Query().Get("interval"))
	if iv == "" {
		iv = ontap.Hour
	}
	if !slices.Contains([]ontap.Interval{ontap.Hour, ontap.Day, ontap.Week, ontap.Month, "1y"}, iv) {
		fail(w, http.StatusBadRequest, "262179", "invalid interval")
		return
	}
	now := s.Now
	if now.IsZero() {
		now = time.Now()
	}
	load := s.Load
	if load == nil {
		load = Default
	}
	step := time.Duration(iv.Step()) * time.Second
	end := now.Truncate(step)
	var recs []map[string]any
	for t := end; t.After(now.Add(-iv.Span())); t = t.Add(-step) {
		recs = append(recs, metric(o, load(o, uuid, t), t, iv))
	}
	s.collection(w, r, recs)
}

var durations = map[ontap.Interval]string{ontap.Hour: "PT15S", ontap.Day: "PT5M", ontap.Week: "PT30M", ontap.Month: "PT2H"}

func metric(o ontap.Object, x ontap.Sample, t time.Time, iv ontap.Interval) map[string]any {
	m := map[string]any{"timestamp": t.UTC().Format(time.RFC3339), "duration": durations[iv], "status": "ok"}
	if o == ontap.NodeObject {
		m["processor_utilization"] = int(math.Round(x.CPU))
		return m
	}
	total := x.ReadIOPS + x.WriteIOPS + x.OtherIOPS
	m["iops"] = map[string]any{"read": int64(x.ReadIOPS), "write": int64(x.WriteIOPS), "other": int64(x.OtherIOPS), "total": int64(total)}
	m["throughput"] = map[string]any{"read": int64(x.ReadBps), "write": int64(x.WriteBps), "other": 0, "total": int64(x.ReadBps + x.WriteBps)}
	m["latency"] = map[string]any{"read": int64(x.ReadLatUs), "write": int64(x.WriteLatUs), "other": 100, "total": int64(x.TotalLatUs)}
	return m
}

// Default is a daily cycle peaking in the afternoon, the same for every
// object scaled by its kind.
func Default(o ontap.Object, uuid string, t time.Time) ontap.Sample {
	d := math.Sin(float64(t.Hour()*60+t.Minute())/1440*2*math.Pi-math.Pi/2)*0.5 + 0.5
	scale := 1.0
	if o == ontap.VolumeObject {
		scale = 0.2
	}
	iops := (4000 + 20000*d) * scale
	return ontap.Sample{
		ReadIOPS: iops * 0.7, WriteIOPS: iops * 0.3, OtherIOPS: iops * 0.05,
		ReadBps: iops * 0.7 * 24 * 1024, WriteBps: iops * 0.3 * 32 * 1024,
		ReadLatUs: 300 + 400*d, WriteLatUs: 200 + 300*d, TotalLatUs: 270 + 370*d,
		CPU: 20 + 45*d,
	}
}

func cluster(c ontap.Cluster) map[string]any {
	return map[string]any{"name": c.Name, "uuid": c.UUID, "version": map[string]any{
		"full": c.Version.Full, "generation": c.Version.Generation, "major": c.Version.Major, "minor": c.Version.Minor}}
}

func node(n ontap.Node) map[string]any {
	m := map[string]any{"uuid": n.UUID, "name": n.Name, "model": n.Model, "serial_number": n.Serial, "state": n.State,
		"version":    map[string]any{"full": n.Version},
		"controller": map[string]any{"cpu": map[string]any{"count": n.CPUs, "processor": n.CPUModel}, "memory_size": n.MemBytes}}
	if n.Partner != "" {
		m["ha"] = map[string]any{"enabled": true, "partners": []any{map[string]any{"name": n.Partner}}}
	}
	return m
}

func ratio(r float64) float64 { return math.Round(r*100) / 100 }

func aggregate(a ontap.Aggregate) map[string]any {
	return map[string]any{
		"uuid": a.UUID, "name": a.Name, "node": map[string]any{"name": a.Node}, "state": a.State,
		"block_storage": map[string]any{
			"storage_type": a.StorageType, "mirror": map[string]any{"enabled": a.Mirrored},
			"primary": map[string]any{"disk_class": a.DiskClass, "disk_type": a.DiskType, "raid_type": a.RAID, "disk_count": a.Disks},
		},
		"data_encryption": map[string]any{"software_encryption_enabled": a.Encrypted, "drive_protection_enabled": false},
		"space": map[string]any{
			"block_storage": map[string]any{"size": a.Size, "used": a.Used, "available": a.Available, "physical_used": a.PhysicalUsed,
				"inactive_user_data": a.Inactive, "data_compaction_space_saved": a.CompactionSaved, "volume_deduplication_space_saved": a.DedupeSaved},
			"snapshot":                     map[string]any{"used": a.SnapshotUsed},
			"efficiency":                   map[string]any{"logical_used": a.Logical, "ratio": ratio(a.Ratio), "savings": a.Logical - int64(float64(a.Logical)/max(a.Ratio, 1))},
			"efficiency_without_snapshots": map[string]any{"logical_used": a.LogicalNoSnap, "ratio": ratio(a.RatioNoSnap)},
		},
	}
}

func volume(v ontap.Volume) map[string]any {
	var aggrs []any
	for _, a := range v.Aggregates {
		aggrs = append(aggrs, map[string]any{"name": a})
	}
	m := map[string]any{
		"uuid": v.UUID, "name": v.Name, "svm": map[string]any{"name": v.SVM}, "style": v.Style, "type": v.Type, "state": v.State,
		"is_svm_root": v.Root, "tiering": map[string]any{"policy": v.Tiering}, "guarantee": map[string]any{"type": v.Guarantee},
		"aggregates": aggrs,
		"space": map[string]any{"size": v.Size, "used": v.Used, "available": v.Available,
			"logical_space": map[string]any{"used": v.Logical, "reporting": true}, "physical_used": v.Physical, "footprint": v.Footprint,
			"snapshot": map[string]any{"used": v.SnapshotUsed, "reserve_size": v.SnapshotReserve}, "capacity_tier_footprint": v.CapacityTier},
		"efficiency": map[string]any{"space_savings": map[string]any{"total": v.Saved, "dedupe": v.DedupeSaved, "compression": v.CompressionSaved}},
	}
	if v.Path != "" {
		m["nas"] = map[string]any{"path": v.Path}
	}
	return m
}

func lun(l ontap.LUN) map[string]any {
	return map[string]any{
		"uuid": l.UUID, "name": l.Name, "svm": map[string]any{"name": l.SVM},
		"location":      map[string]any{"volume": map[string]any{"name": l.Volume, "uuid": l.VolumeUUID}},
		"serial_number": l.Serial, "os_type": l.OSType,
		"status": map[string]any{"state": l.State, "mapped": l.Mapped},
		"space":  map[string]any{"size": l.Size, "used": l.Used},
	}
}

func lif(l ontap.LIF) map[string]any {
	state := "down"
	if l.Up {
		state = "up"
	}
	return map[string]any{"name": l.Name, "svm": map[string]any{"name": l.SVM}, "ip": map[string]any{"address": l.IP, "netmask": "24"},
		"location": map[string]any{"node": map[string]any{"name": l.Node}}, "services": l.Services, "state": state, "scope": "svm"}
}
