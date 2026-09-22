package vc

import (
	"context"
	"crypto/tls"
	"errors"
	"testing"
	"time"

	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/simulator"
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
