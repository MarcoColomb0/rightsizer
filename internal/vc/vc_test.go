package vc

import (
	"context"
	"crypto/tls"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
)

func sim(t *testing.T) (Credentials, func()) {
	t.Helper()
	m := simulator.VPX()
	m.Host = 2
	m.Cluster = 2
	m.Machine = 3
	if err := m.Create(); err != nil {
		t.Fatal(err)
	}
	m.Service.TLS = new(tls.Config)
	s := m.Service.NewServer()
	pw, _ := s.URL.User.Password()
	ci, err := Probe(context.Background(), s.URL.Host)
	if err != nil {
		t.Fatal(err)
	}
	if ci.Trusted {
		t.Fatal("simulator certificate should not be trusted")
	}
	return Credentials{Host: s.URL.Host, User: s.URL.User.Username(), Password: pw, Fingerprint: ci.Fingerprint}, func() {
		s.Close()
		m.Remove()
	}
}

func TestPinnedConnectAndReadOnlyGuard(t *testing.T) {
	creds, done := sim(t)
	defer done()
	ctx := context.Background()

	bad := creds
	bad.Fingerprint = "00:11"
	if _, err := Connect(ctx, bad); err == nil {
		t.Fatal("connect with wrong fingerprint must fail")
	}

	c, err := Connect(ctx, creds)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)

	vms, err := find.NewFinder(c.vim).VirtualMachineList(ctx, "*")
	if err != nil || len(vms) == 0 {
		t.Fatalf("finder: %v", err)
	}
	_, err = vms[0].PowerOff(ctx)
	var ro *ReadOnlyError
	if !errors.As(err, &ro) || ro.Method != "PowerOffVM_Task" {
		t.Fatalf("PowerOff must be blocked, got %v", err)
	}
	if _, err := vms[0].Destroy(ctx); !errors.As(err, &ro) {
		t.Fatalf("Destroy must be blocked, got %v", err)
	}
}

func TestInventoryAndSample(t *testing.T) {
	creds, done := sim(t)
	defer done()
	ctx := context.Background()
	c, err := Connect(ctx, creds)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)

	inv, err := c.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Hosts) == 0 || len(inv.VMs) == 0 {
		t.Fatalf("empty inventory: %d hosts %d vms", len(inv.Hosts), len(inv.VMs))
	}
	var refs []string
	for _, v := range inv.VMs {
		if v.VCPU == 0 || v.MemMB == 0 || v.Cluster == "" {
			t.Fatalf("incomplete vm %+v", v)
		}
		refs = append(refs, v.Ref)
	}
	series, err := c.Sample(ctx, "VirtualMachine", refs, VMMetrics, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(series) == 0 || len(series[0].TS) == 0 {
		t.Fatal("no samples")
	}
	if _, ok := series[0].Values[CPUUsage]; !ok {
		t.Fatalf("missing %s", CPUUsage)
	}
}

func TestSamplesAscendingAndHistoryPlan(t *testing.T) {
	creds, done := sim(t)
	defer done()
	ctx := context.Background()
	c, err := Connect(ctx, creds)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	inv, err := c.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	refs := []string{}
	for _, v := range inv.VMs {
		refs = append(refs, v.Ref)
	}
	series, err := c.Sample(ctx, "VirtualMachine", refs, VMMetrics, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range series {
		if len(s.TS) < 2 || s.Interval != RealtimeInterval {
			t.Fatalf("want several real-time samples, got %d", len(s.TS))
		}
		for i := 1; i < len(s.TS); i++ {
			if !s.TS[i].After(s.TS[i-1]) {
				t.Fatal("samples must be in ascending time order")
			}
		}
	}

	plan, err := c.HistoryPlan(ctx, 14*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 3 || plan[0].Interval != 300 || plan[1].Interval != 1800 || plan[2].Interval != 7200 {
		t.Fatalf("unexpected plan %+v", plan)
	}
	for i := 1; i < len(plan); i++ {
		if !plan[i].End.Equal(plan[i-1].Start) {
			t.Fatal("windows must be contiguous and must not overlap")
		}
	}
	if got := plan[0].End.Sub(plan[len(plan)-1].Start); got != 14*24*time.Hour {
		t.Fatalf("plan covers %s, want 14 days", got)
	}
	if plan[0].End.Sub(plan[0].Start) != 24*time.Hour || plan[1].End.Sub(plan[1].Start) != 6*24*time.Hour {
		t.Fatal("finer intervals must cover the most recent period")
	}

	hist, err := c.History(ctx, "VirtualMachine", refs, HistoryVMMetrics, plan[1])
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != len(refs) {
		t.Fatalf("history for %d of %d VMs", len(hist), len(refs))
	}
	for _, s := range hist {
		if s.Interval != 1800 || len(s.TS) == 0 {
			t.Fatalf("bad history series: interval %d, %d samples", s.Interval, len(s.TS))
		}
		if s.TS[0].Before(plan[1].Start) || s.TS[len(s.TS)-1].After(plan[1].End) {
			t.Fatal("history samples outside the requested window")
		}
	}
}

func TestOrphanedDisks(t *testing.T) {
	creds, done := sim(t)
	defer done()
	ctx := context.Background()
	c, err := Connect(ctx, creds)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	inv, err := c.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Datastores) == 0 {
		t.Fatal("no datastores in inventory")
	}
	var dsm mo.Datastore
	if err := c.retrieveOne(ctx, types.ManagedObjectReference{Type: "Datastore", Value: inv.Datastores[0].Ref}, []string{"summary"}, &dsm); err != nil {
		t.Fatal(err)
	}
	root := strings.TrimPrefix(dsm.Summary.Url, "ds://")
	for _, p := range []string{"forgotten-vm/forgotten-vm.vmdk", "fcd/managed.vmdk"} {
		_ = os.MkdirAll(filepath.Join(root, filepath.Dir(p)), 0o755)
		if err := os.WriteFile(filepath.Join(root, p), []byte("# Disk DescriptorFile"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	orphans, err := c.OrphanedDisks(ctx, inv)
	if err != nil {
		t.Fatal(err)
	}
	var found, fcd bool
	for _, o := range orphans {
		if strings.HasSuffix(o.Path, "forgotten-vm/forgotten-vm.vmdk") {
			found = true
		}
		if strings.Contains(o.Path, "fcd/") {
			fcd = true
		}
		for _, vm := range inv.VMs {
			for _, f := range vm.Files {
				if normalize(f) == normalize(o.Path) {
					t.Fatalf("disk in use reported as orphaned: %s", o.Path)
				}
			}
		}
	}
	if !found {
		t.Fatalf("planted orphan not found in %+v", orphans)
	}
	if fcd {
		t.Fatal("first-class disks must be skipped")
	}
}

func TestEstate(t *testing.T) {
	creds, done := sim(t)
	defer done()
	ctx := context.Background()
	c, err := Connect(ctx, creds)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	e, err := c.Estate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Hosts) == 0 || len(e.Clusters) == 0 || len(e.Datastores) == 0 {
		t.Fatalf("empty estate: %d hosts, %d clusters, %d datastores", len(e.Hosts), len(e.Clusters), len(e.Datastores))
	}
	h := e.Hosts[0]
	if h.Cores == 0 || h.MemBytes == 0 || h.ESXi == "" || len(h.HBAs) == 0 || len(h.NICs) == 0 {
		t.Fatalf("incomplete host %+v", h)
	}
	for _, d := range e.Datastores {
		if d.ID == "" || d.Capacity == 0 || len(d.Hosts) == 0 {
			t.Fatalf("incomplete datastore %+v", d)
		}
	}
	if f := e.Filter([]string{e.Clusters[0].Name}); len(f.Hosts) == 0 || len(f.Hosts) >= len(e.Hosts) {
		t.Fatalf("filter kept %d of %d hosts", len(f.Hosts), len(e.Hosts))
	}
	t.Logf("host %+v", h)
	t.Logf("datastore %+v", e.Datastores[0])
	t.Logf("cluster %+v", e.Clusters[0])
}

func TestDatastoreCounters(t *testing.T) {
	creds, done := sim(t)
	defer done()
	ctx := context.Background()
	c, err := Connect(ctx, creds)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	inv, err := c.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var hosts []string
	for _, h := range inv.Hosts {
		hosts = append(hosts, h.Ref)
	}
	ss, err := c.Sample(ctx, "HostSystem", hosts, HostMetrics, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) == 0 || len(ss[0].Inst[ReadIOPS]) == 0 {
		t.Fatalf("no per-datastore samples: %+v", ss)
	}
	for _, v := range ss[0].Inst[ReadIOPS] {
		if len(v) != len(ss[0].TS) {
			t.Fatalf("%d values for %d samples", len(v), len(ss[0].TS))
		}
	}
	if _, ok := ss[0].Values[ReadIOPS]; ok {
		t.Fatal("per-datastore counter must not be reported as an aggregate")
	}
}

// refuseWildcard fails performance queries for per-datastore counters the way
// vCenter does when a query exceeds vpxd.stats.maxQueryMetrics.
type refuseWildcard struct {
	next  soap.RoundTripper
	calls int
}

func (r *refuseWildcard) RoundTrip(ctx context.Context, req, res soap.HasFault) error {
	if q, ok := req.(*methods.QueryPerfBody); ok {
		for _, s := range q.Req.QuerySpec {
			for _, id := range s.MetricId {
				if id.Instance == "*" {
					r.calls++
					return errors.New("ServerFaultCode: Request exceeds vpxd.stats.maxQueryMetrics")
				}
			}
		}
	}
	return r.next.RoundTrip(ctx, req, res)
}

func TestDatastoreCountersBestEffort(t *testing.T) {
	creds, done := sim(t)
	defer done()
	ctx := context.Background()
	c, err := Connect(ctx, creds)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	inv, err := c.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var hosts []string
	for _, h := range inv.Hosts {
		hosts = append(hosts, h.Ref)
	}
	rw := &refuseWildcard{next: c.vim.RoundTripper}
	c.vim.RoundTripper = rw
	ss, err := c.Sample(ctx, "HostSystem", hosts, HostMetrics, time.Time{})
	if err != nil {
		t.Fatalf("core counters must survive a refused datastore query: %v", err)
	}
	if len(ss) != len(hosts) || len(ss[0].Values[CPUMHz]) == 0 || len(ss[0].Inst) != 0 {
		t.Fatalf("want core samples only, got %+v", ss[0])
	}
	first := rw.calls
	if first == 0 || !c.skipDatastores(RealtimeInterval) {
		t.Fatalf("refusal at one entity must disable the datastore query (%d calls)", first)
	}
	if _, err := c.Sample(ctx, "HostSystem", hosts, HostMetrics, time.Time{}); err != nil || rw.calls != first {
		t.Fatalf("disabled datastore query retried: %d calls, %v", rw.calls, err)
	}
}

func TestMergeInst(t *testing.T) {
	t0 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	ts := []time.Time{t0, t0.Add(20 * time.Second), t0.Add(40 * time.Second)}
	core := []Series{{Ref: "h1", TS: ts, Values: map[string][]float64{CPUMHz: {1, 2, 3}}}, {Ref: "h2", TS: ts}}
	extra := []Series{{Ref: "h1", TS: ts[1:], Inst: map[string]map[string][]float64{ReadIOPS: {"ds1": {10, 20}}}}, {Ref: "h3", TS: ts, Inst: map[string]map[string][]float64{ReadIOPS: {"x": {1, 1, 1}}}}}
	mergeInst(core, extra)
	got := core[0].Inst[ReadIOPS]["ds1"]
	if len(got) != 3 || got[0] != -1 || got[1] != 10 || got[2] != 20 {
		t.Fatalf("aligned %v", got)
	}
	if core[1].Inst != nil {
		t.Fatal("entities without datastore data stay untouched")
	}
}
