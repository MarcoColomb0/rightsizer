package engine

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"

	"github.com/MarcoColomb0/rightsizer/internal/report"
	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

func TestEndToEnd(t *testing.T) {
	pollEvery = 200 * time.Millisecond
	m := simulator.VPX()
	m.Host, m.Cluster, m.Machine = 3, 2, 4
	if err := m.Create(); err != nil {
		t.Fatal(err)
	}
	defer m.Remove()
	m.Service.TLS = new(tls.Config)
	s := m.Service.NewServer()
	defer s.Close()
	pw, _ := s.URL.User.Password()
	ci, err := vc.Probe(context.Background(), s.URL.Host)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	web := &report.Server{Listen: "127.0.0.1:0", PublicHost: "127.0.0.1", TTL: time.Hour}
	e, err := New(dir, web)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Host: s.URL.Host, User: s.URL.User.Username(), Fingerprint: ci.Fingerprint, Duration: 24 * time.Hour, Profile: "balanced"}
	if err := e.Start(context.Background(), Config{Duration: time.Hour}, pw); err == nil {
		t.Fatal("short duration must be rejected")
	}
	if err := e.Start(context.Background(), cfg, pw); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		st := e.Status()
		if st.Polls >= 3 && st.Result != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no polls: %+v", st)
		}
		time.Sleep(100 * time.Millisecond)
	}

	sh, err := e.Publish()
	if err != nil {
		t.Fatal(err)
	}
	cl := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	res, err := cl.Get(sh.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.HasPrefix(string(body), "%PDF") {
		t.Fatalf("download failed: %d", res.StatusCode)
	}
	sum := sha256.Sum256(res.TLS.PeerCertificates[0].Raw)
	if got := fmt.Sprintf("%X", sum); strings.ReplaceAll(sh.Fingerprint, ":", "") != got {
		t.Fatal("served certificate does not match advertised fingerprint")
	}
	bad, err := cl.Get(strings.Replace(sh.URL, "/rightsizer-", "x/rightsizer-", 1))
	if err != nil {
		t.Fatal(err)
	}
	bad.Body.Close()
	if bad.StatusCode != 404 {
		t.Fatalf("wrong token must 404, got %d", bad.StatusCode)
	}

	if err := e.Finish(); err != nil {
		t.Fatal(err)
	}
	st := e.Status()
	if st.Phase != Done || st.ReportFile == "" || st.Share == nil {
		t.Fatalf("not finalized: %+v", st)
	}
	if fi, err := os.Stat(st.ReportFile); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("report file: %v %v", fi, err)
	}
	if out := os.Getenv("RIGHTSIZER_PDF_OUT"); out != "" {
		b, _ := os.ReadFile(st.ReportFile)
		_ = os.WriteFile(out, b, 0o600)
	}

	e2, err := New(dir, web)
	if err != nil {
		t.Fatal(err)
	}
	if e2.Status().Phase != Done {
		t.Fatal("state not restored")
	}
	if err := e2.Cancel(); err != nil {
		t.Fatal(err)
	}
	if e2.Status().Phase != Idle {
		t.Fatal("cancel did not reset")
	}
}
