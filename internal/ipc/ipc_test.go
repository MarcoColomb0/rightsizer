package ipc

import (
	"context"
	"crypto/tls"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
	"github.com/MarcoColomb0/rightsizer/internal/engine"
	"github.com/MarcoColomb0/rightsizer/internal/report"
	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

func TestSizingOverSocket(t *testing.T) {
	m := simulator.VPX()
	if err := m.Create(); err != nil {
		t.Fatal(err)
	}
	defer m.Remove()
	m.Service.TLS = new(tls.Config)
	s := m.Service.NewServer()
	defer s.Close()
	ci, err := vc.Probe(context.Background(), s.URL.Host)
	if err != nil {
		t.Fatal(err)
	}

	dir, err := os.MkdirTemp("", "rs")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	web := &report.Server{Listen: "127.0.0.1:0", PublicHost: "127.0.0.1", TTL: time.Hour}
	e, err := engine.New(dir, web, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock := filepath.Join(dir, "s")
	go func() { _ = Serve(ctx, sock, Local{E: e}) }()
	c := NewClient(sock)
	for i := 0; ; i++ {
		if _, err := c.Summary(); err == nil {
			break
		} else if i > 50 {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if _, err := c.Sizing(""); err == nil || !strings.Contains(err.Error(), "no inventory") {
		t.Fatalf("want an error without sources, got %v", err)
	}
	p, err := c.SizingParams()
	if err != nil || *p != analysis.DefaultSizing() {
		t.Fatalf("%+v %v", p, err)
	}
	p.Growth = 50
	p.Groups = "db=sql*"
	if err := c.SetSizingParams(*p); err != nil {
		t.Fatal(err)
	}
	p.FreeSpace = 99
	if err := c.SetSizingParams(*p); err == nil {
		t.Fatal("invalid parameters must be refused")
	}

	pw, _ := s.URL.User.Password()
	id, err := c.Add(engine.Config{Host: s.URL.Host, User: s.URL.User.Username(), Fingerprint: ci.Fingerprint, Duration: 24 * time.Hour, Profile: "balanced"}, pw)
	if err != nil {
		t.Fatal(err)
	}
	var sz *analysis.Sizing
	for i := 0; ; i++ {
		sz, err = c.Sizing(id)
		if err == nil && len(sz.Clusters) > 0 && len(sz.Hosts) > 0 {
			break
		}
		if i > 200 {
			t.Fatalf("no sizing: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if sz.Params.Growth != 50 || sz.Totals.For(analysis.BasisProvisioned).Nodes == 0 {
		t.Fatalf("sizing over the socket: %+v", sz.Totals)
	}
	sh, err := c.PublishSizing(id)
	if err != nil || len(sh.Extra) != 1 {
		t.Fatalf("publish: %+v %v", sh, err)
	}
}
