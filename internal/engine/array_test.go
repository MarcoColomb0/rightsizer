package engine

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/ontap"
	"github.com/MarcoColomb0/rightsizer/internal/ontap/ontapsim"
	"github.com/MarcoColomb0/rightsizer/internal/report"
	"github.com/MarcoColomb0/rightsizer/internal/vault"
	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

func newArray(t *testing.T) (*ontapsim.Server, string) {
	t.Helper()
	inv, naa := ontapsim.Fixture("aff01", []ontapsim.Datastore{
		{Name: "ds_fc", Used: 4 << 40, Ratio: 3},
		{Name: "nfs01", NFS: true, Used: 2 << 40, Ratio: 2},
	})
	s := ontapsim.New(inv)
	// The archive reaches past the start of collection, so the first poll
	// already has 15-second samples newer than it.
	s.Now = time.Now().Add(2 * time.Minute)
	t.Cleanup(s.Close)
	return s, naa[0]
}

func arrayCfg(s *ontapsim.Server) Config {
	return Config{Kind: KindONTAP, Host: s.Host(), User: s.User, Fingerprint: s.Fingerprint(), Duration: 24 * time.Hour}
}

func waitArray(t *testing.T, e *Engine, id string, ok func(Status) bool) Status {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		st, err := e.Source(id)
		if err == nil && st.Array != nil && ok(st) {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("ONTAP source: %+v %v", st, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestONTAPSource(t *testing.T) {
	arr, naa := newArray(t)
	vcs := newSim(t)
	defer vcs.close()
	dir := t.TempDir()
	web := &report.Server{Listen: "127.0.0.1:0", PublicHost: "127.0.0.1", TTL: time.Hour}
	v := vault.Open(dir)
	admin := "appliance admin password"
	if err := v.Reset(admin); err != nil {
		t.Fatal(err)
	}
	e, err := New(dir, web, v)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if _, err := e.Add(ctx, arrayCfg(arr), "wrong"); !ontap.IsAuthError(err) {
		t.Fatalf("wrong ONTAP password must be refused, got %v", err)
	}
	bad := arrayCfg(arr)
	bad.Kind = "hyper-v"
	if _, err := e.Add(ctx, bad, arr.Password); err == nil {
		t.Fatal("unknown kinds must be refused")
	}
	ida, err := e.Add(ctx, arrayCfg(arr), arr.Password)
	if err != nil {
		t.Fatal(err)
	}
	idv, err := e.Add(ctx, vcs.cfg(), vcs.pass)
	if err != nil {
		t.Fatal(err)
	}
	st := waitArray(t, e, ida, func(s Status) bool { return s.Polls >= 2 && strings.HasPrefix(s.History, "imported") })
	if !strings.HasPrefix(st.About, "NetApp ONTAP 9.14.1") || st.Array.Nodes != 2 || st.Array.Ratio < 1.5 || !st.Array.IO.Available || st.Array.CPU <= 0 {
		t.Fatalf("status %q %+v", st.About, st.Array)
	}
	if st.Result != nil || st.Totals != nil {
		t.Fatal("an ONTAP source has no rightsizing of its own")
	}
	if _, err := e.Publish(ida); !errors.Is(err, errArrayReport) {
		t.Fatalf("publish: %v", err)
	}
	if _, err := e.Sizing(ida); !errors.Is(err, errArrayReport) {
		t.Fatalf("sizing: %v", err)
	}
	for _, r := range arr.Requests() {
		if !strings.HasPrefix(r, "GET /api/") {
			t.Fatalf("request %q reached the cluster", r)
		}
	}

	// Once the vCenter's estate shows a datastore on the cluster, its volume
	// is collected and the vCenter's sizing measures its reduction.
	waitPolls(t, e, idv, 1)
	src := e.mustSource(t, idv)
	deadline := time.Now().Add(10 * time.Second)
	for {
		src.mu.Lock()
		est := src.st.Estate
		if est != nil {
			// Estates are replaced, never changed in place, as a scan does.
			cp := *est
			cp.Datastores = append(slices.Clone(est.Datastores), vc.DatastoreDetail{Name: "ds-fc", Type: "VMFS", Version: "VMFS 6.82", ID: "x",
				Capacity: 8 << 40, Free: 4 << 40, Accessible: true, Hosts: []string{"h"}, LUNs: []vc.LUN{{Name: naa, Vendor: "NETAPP", Transport: "FC"}}})
			src.st.Estate = &cp
		}
		src.mu.Unlock()
		if est != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no vCenter estate")
		}
		time.Sleep(100 * time.Millisecond)
	}
	st = waitArray(t, e, ida, func(s Status) bool { return s.Array.Matched == 1 && s.Array.Volumes == 1 })
	as := e.mustSource(t, ida)
	deadline = time.Now().Add(10 * time.Second)
	for {
		as.mu.Lock()
		n := len(as.st.ArrayRT.Volumes)
		h := len(as.st.ArrayHistory.Volumes)
		as.mu.Unlock()
		if n == 1 && h == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("matched volume collected %d times, archive %d", n, h)
		}
		time.Sleep(50 * time.Millisecond)
	}
	sz, err := e.Sizing(idv)
	if err != nil {
		t.Fatal(err)
	}
	if len(sz.Storage.Arrays) != 1 || sz.Storage.Array.Datastores != 1 || sz.Storage.Array.Ratio() < 2.9 {
		t.Fatalf("vCenter sizing with ONTAP: %+v, arrays %d", sz.Storage.Array, len(sz.Storage.Arrays))
	}

	if _, err := e.Diagnose(ctx, idv, false); err == nil {
		t.Fatal("diagnostics are for ONTAP sources")
	}
	sh, err := e.Diagnose(ctx, ida, false)
	if err != nil {
		t.Fatal(err)
	}
	files := unzip(t, sh.URL)
	for _, n := range []string{"README.txt", "checks.txt", "matching.txt", "matching.json", "collection.json", "diagnosis.json", "api.json", "logs.txt", "about.json"} {
		if files[n] == "" {
			t.Fatalf("bundle misses %s: %v", n, files)
		}
	}
	checks := files["checks.txt"]
	for _, want := range []string{"[PASS] release", "[PASS] snapshot accounting", "[PASS] metrics cluster 1h", "CPU ≤", "matching: 2 datastores of 1 vCenter(s): 1 matched", "[PASS] archive import"} {
		if !strings.Contains(checks, want) {
			t.Fatalf("checks.txt misses %q:\n%s", want, checks)
		}
	}
	if strings.Contains(checks, "[FAIL]") {
		t.Fatalf("simulator must pass every check:\n%s", checks)
	}
	if !strings.Contains(files["matching.txt"], "ds-fc") || !strings.Contains(files["matching.txt"], "→ svm_vmware/ds_fc") {
		t.Fatalf("matching.txt:\n%s", files["matching.txt"])
	}
	if !strings.Contains(files["api.json"], "/api/storage/volumes") || strings.Contains(files["api.json"], arr.Password) {
		t.Fatal("api.json must hold the requests and never the password")
	}
	anon, err := e.Diagnose(ctx, ida, true)
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range unzip(t, anon.URL) {
		for _, leak := range []string{"svm_vmware", "ds_fc", "aff01-01", "cluster aff01", "80AbK0000001", naa} {
			if strings.Contains(body, leak) {
				t.Fatalf("anonymised %s leaks %q", name, leak)
			}
		}
	}
	e.Shutdown()

	// ONTAP state is written in a format earlier releases set aside.
	raw, err := readState(filepath.Join(dir, "sources", ida, "state.gob"))
	if err != nil || raw.Version != stateVersion || raw.Array == nil || raw.ArrayRT == nil {
		t.Fatalf("saved ONTAP state: version %d, %v", raw.Version, err)
	}
	if rv, _ := readState(filepath.Join(dir, "sources", idv, "state.gob")); rv.Version != vcenterFormat {
		t.Fatalf("vCenter state must stay readable by earlier releases: version %d", rv.Version)
	}

	e2, err := New(dir, web, vault.Open(dir))
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := e2.Source(ida); st.Phase != NeedPassword || st.Array == nil || st.Array.Nodes != 2 {
		t.Fatalf("after restart: %s %+v", st.Phase, st.Array)
	}
	if err := e2.Unlock(admin); err != nil {
		t.Fatal(err)
	}
	waitArray(t, e2, ida, func(s Status) bool { return s.Phase == Running })
	if err := e2.Finish(ida); err != nil {
		t.Fatal(err)
	}
	if st, _ := e2.Source(ida); st.Phase != Done || st.ReportFile != "" {
		t.Fatalf("finished ONTAP source: %s %q", st.Phase, st.ReportFile)
	}
	if err := e2.Remove(ida); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sources", ida)); !os.IsNotExist(err) {
		t.Fatal("removed ONTAP source data must be deleted")
	}
	e2.Shutdown()
}

func unzip(t *testing.T, url string) map[string]string {
	t.Helper()
	res, body := download(t, url)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("download %s: %d", url, res.StatusCode)
	}
	z, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range z.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(data)
	}
	return out
}
