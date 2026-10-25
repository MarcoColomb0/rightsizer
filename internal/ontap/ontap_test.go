package ontap_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/vim25/soap"

	"github.com/MarcoColomb0/rightsizer/internal/ontap"
	"github.com/MarcoColomb0/rightsizer/internal/ontap/ontapsim"
	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

func fixture() *ontap.Inventory {
	inv, _ := ontapsim.Fixture("aff01", []ontapsim.Datastore{
		{Name: "ds_prod_01", Used: 10 << 40, Ratio: 3},
		{Name: "ds_prod_02", Used: 6 << 40, Ratio: 2, Snapshots: 1 << 40},
		{Name: "nfs_backup", NFS: true, Used: 20 << 40, Ratio: 1.5},
	})
	return inv
}

func creds(s *ontapsim.Server) ontap.Credentials {
	return ontap.Credentials{Host: s.Host(), User: s.User, Password: s.Password, Fingerprint: s.Fingerprint()}
}

func TestConnectPinsCertificateAndChecksCredentials(t *testing.T) {
	s := ontapsim.New(fixture())
	defer s.Close()
	ctx := context.Background()

	ci, err := vc.Probe(ctx, s.Host())
	if err != nil {
		t.Fatal(err)
	}
	if ci.Fingerprint != s.Fingerprint() || ci.Fingerprint != soap.ThumbprintSHA256(s.Certificate()) {
		t.Fatalf("probe fingerprint %s, pinned form %s: the console shows one and the client pins the other", ci.Fingerprint, s.Fingerprint())
	}

	bad := creds(s)
	bad.Fingerprint = strings.Repeat("00:", 31) + "00"
	if _, err := ontap.Connect(ctx, bad); err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Fatalf("wrong fingerprint must fail, got %v", err)
	}
	unpinned := creds(s)
	unpinned.Fingerprint = ""
	if _, err := ontap.Connect(ctx, unpinned); err == nil {
		t.Fatal("a self-signed certificate without a pin must be refused")
	}
	wrong := creds(s)
	wrong.Password = "nope"
	if _, err := ontap.Connect(ctx, wrong); !ontap.IsAuthError(err) {
		t.Fatalf("wrong password must be an auth error, got %v", err)
	}

	c, err := ontap.Connect(ctx, creds(s))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	if got := c.About(); got != "NetApp ONTAP 9.14.1 (cluster aff01)" {
		t.Fatalf("about %q", got)
	}
}

func TestRefusesOldReleases(t *testing.T) {
	inv := fixture()
	inv.Cluster.Version = ontap.Version{Full: "NetApp Release 9.9.1P3", Generation: 9, Major: 9, Minor: 1}
	s := ontapsim.New(inv)
	defer s.Close()
	_, err := ontap.Connect(context.Background(), creds(s))
	if err == nil || !strings.Contains(err.Error(), "9.10 or later") {
		t.Fatalf("ONTAP 9.9 must be refused, got %v", err)
	}
}

func TestOnlyGETRequestsReachTheCluster(t *testing.T) {
	s := ontapsim.New(fixture())
	defer s.Close()
	ctx := context.Background()
	c, err := ontap.Connect(ctx, creds(s))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []struct{ method, url string }{
		{http.MethodPost, s.URL + "/api/storage/volumes"},
		{http.MethodPatch, s.URL + "/api/storage/volumes/x"},
		{http.MethodDelete, s.URL + "/api/storage/luns/x"},
		{http.MethodGet, s.URL + "/sysmgr/v4/"},
		{http.MethodGet, "https://example.invalid/api/cluster"},
	} {
		req, _ := http.NewRequestWithContext(ctx, r.method, r.url, nil)
		err := ontap.Do(c, req)
		if _, ok := errors.AsType[*ontap.ReadOnlyError](err); !ok {
			t.Fatalf("%s %s: expected the read-only guard, got %v", r.method, r.url, err)
		}
	}
	for _, r := range s.Requests() {
		if !strings.HasPrefix(r, "GET /api/") {
			t.Fatalf("request %q reached the cluster", r)
		}
	}
}

func TestInventoryPagesAndOlderReleases(t *testing.T) {
	s := ontapsim.New(fixture())
	defer s.Close()
	s.PageSize = 1
	// Fields added after 9.10 are refused by older clusters.
	s.Unsupported = []string{"space.efficiency_without_snapshots.ratio", "space.efficiency_without_snapshots.logical_used", "efficiency.space_savings.total"}
	s.Forbidden = []string{"/api/network/ip/interfaces"}
	ctx := context.Background()
	c, err := ontap.Connect(ctx, creds(s))
	if err != nil {
		t.Fatal(err)
	}
	inv, err := c.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Nodes) != 2 || len(inv.Aggregates) != 2 || len(inv.Volumes) != 3 || len(inv.LUNs) != 2 {
		t.Fatalf("records: %d nodes, %d aggregates, %d volumes, %d LUNs", len(inv.Nodes), len(inv.Aggregates), len(inv.Volumes), len(inv.LUNs))
	}
	if len(inv.LIFs) != 0 {
		t.Fatal("interfaces the role cannot read are skipped")
	}
	a := inv.Aggregates[0]
	if a.Ratio < 1.5 || a.RatioNoSnap != 0 || a.Node != "aff01-01" || a.Logical == 0 {
		t.Fatalf("aggregate %+v", a)
	}
	v := inv.Volume(inv.LUNs[0].VolumeUUID)
	if v == nil || v.Name != "ds_prod_01" || v.Logical != 10<<40 || v.Physical == 0 || v.Saved != 0 || v.DedupeSaved == 0 {
		t.Fatalf("volume %+v", v)
	}
	if inv.Volumes[2].Path != "/nfs_backup" {
		t.Fatalf("NFS path %q", inv.Volumes[2].Path)
	}
	if got := inv.LUNs[0].NAA(); !strings.HasPrefix(got, "naa.600a0980") || len(got) != 4+32 {
		t.Fatalf("NAA %q", got)
	}
	if n := inv.Nodes[0]; n.Model != "AFF-A400" || n.Partner != "aff01-02" || n.CPUs != 40 || n.MemBytes != 144<<30 {
		t.Fatalf("node %+v", n)
	}
	var pages int
	for _, r := range s.Requests() {
		if r == "GET /api/storage/volumes" {
			pages++
		}
	}
	if pages < 3 {
		t.Fatalf("volumes read in %d requests, expected one page each", pages)
	}
}

func TestNAA(t *testing.T) {
	// A LUN with serial 80AbK0000001 is seen by ESXi as this device.
	if got := ontap.NAA("80AbK0000001"); got != "naa.600a0980"+"383041624b30303030303031" {
		t.Fatalf("NAA %q", got)
	}
	if ontap.NAA("") != "" {
		t.Fatal("no serial, no identifier")
	}
}

func TestMetrics(t *testing.T) {
	s := ontapsim.New(fixture())
	defer s.Close()
	s.Now = time.Date(2026, 10, 20, 15, 0, 0, 0, time.UTC)
	ctx := context.Background()
	c, err := ontap.Connect(ctx, creds(s))
	if err != nil {
		t.Fatal(err)
	}
	inv, err := c.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hour, err := c.Metrics(ctx, ontap.ClusterObject, "", ontap.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(hour) != 240 || hour[0].Interval != 15 || !hour[0].T.Before(hour[1].T) {
		t.Fatalf("an hour of 15-second samples, oldest first: %d samples, first %+v", len(hour), hour[0])
	}
	x := hour[len(hour)-1]
	if x.ReadIOPS <= 0 || x.WriteIOPS <= 0 || x.ReadBps <= 0 || x.ReadLatUs <= 0 || x.CPU != -1 {
		t.Fatalf("sample %+v", x)
	}
	week, err := c.Metrics(ctx, ontap.VolumeObject, inv.Volumes[0].UUID, ontap.Week)
	if err != nil || len(week) != 7*48 || week[0].Interval != 1800 {
		t.Fatalf("a week of 30-minute volume samples: %d, %v", len(week), err)
	}
	cpu, err := c.Metrics(ctx, ontap.NodeObject, inv.Nodes[0].UUID, ontap.Day)
	if err != nil || len(cpu) != 288 || cpu[0].CPU < 20 || cpu[0].Interval != 300 {
		t.Fatalf("a day of node CPU: %d, %v", len(cpu), err)
	}
	if _, err := c.Metrics(ctx, ontap.VolumeObject, "missing", ontap.Hour); err == nil {
		t.Fatal("unknown volume must fail")
	}
}

func TestHistoryPlan(t *testing.T) {
	now := time.Date(2026, 10, 20, 15, 0, 0, 0, time.UTC)
	plan := ontap.HistoryPlan(now, 14*24*time.Hour)
	want := []ontap.Window{
		{Interval: ontap.Day, Start: now.Add(-24 * time.Hour), End: now},
		{Interval: ontap.Week, Start: now.Add(-7 * 24 * time.Hour), End: now.Add(-24 * time.Hour)},
		{Interval: ontap.Month, Start: now.Add(-14 * 24 * time.Hour), End: now.Add(-7 * 24 * time.Hour)},
	}
	if !slices.Equal(plan, want) {
		t.Fatalf("plan %+v", plan)
	}
	if p := ontap.HistoryPlan(now, 12*time.Hour); len(p) != 1 || p[0].Start != now.Add(-12*time.Hour) {
		t.Fatalf("short plan %+v", p)
	}
}
