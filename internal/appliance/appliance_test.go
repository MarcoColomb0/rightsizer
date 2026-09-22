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
