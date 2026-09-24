package appliance

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRequestUpgrade(t *testing.T) {
	h := Host{Dir: t.TempDir(), Current: "v1.2.0"}
	for _, bad := range []string{"v1.2.0", "v1.1.0", "latest", "v1.3.0; rm -rf /", "../v9.9.9"} {
		if err := h.RequestUpgrade(bad); err == nil {
			t.Fatalf("%q must be rejected", bad)
		}
	}
	if err := h.RequestUpgrade("v1.3.0"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(h.Dir, "upgrade-request"))
	if string(b) != "v1.3.0\n" {
		t.Fatalf("got %q", b)
	}
	_ = os.WriteFile(filepath.Join(h.Dir, "upgrade-status.json"), []byte(`{"Version":"v1.3.0","State":"done"}`), 0o600)
	if s := h.Status(); s == nil || s.State != "done" {
		t.Fatal("status not read")
	}
}

func TestRebootFlags(t *testing.T) {
	dir := t.TempDir()
	bootIDPath = filepath.Join(dir, "boot_id")
	_ = os.WriteFile(bootIDPath, []byte("boot-2\n"), 0o600)
	h := Host{Dir: filepath.Join(dir, "host"), Current: "v1.0.0"}
	if err := h.RequestReboot(); err == nil {
		t.Fatal("a restart must not be requested when none is needed")
	}
	_ = os.MkdirAll(filepath.Join(h.Dir, "reboot"), 0o700)
	_ = os.WriteFile(filepath.Join(h.Dir, "reboot", "os.json"), []byte(`{"Text":"Flatcar OS update 1.2.3 installed","BootID":"boot-2"}`), 0o600)
	_ = os.WriteFile(filepath.Join(h.Dir, "reboot", "upgrade.json"), []byte(`{"Text":"kernel settings changed","BootID":"boot-1"}`), 0o600)
	r := h.RebootReasons()
	if len(r) != 1 || r[0] != "Flatcar OS update 1.2.3 installed" {
		t.Fatalf("flags from an earlier boot must be ignored: %v", r)
	}
	if err := h.RequestReboot(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(h.Dir, "reboot-request"))
	if string(b) != "reboot\n" {
		t.Fatalf("got %q", b)
	}
}
