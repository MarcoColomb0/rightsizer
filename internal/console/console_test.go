package console

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MarcoColomb0/rightsizer/internal/engine"
)

type src struct {
	s   *engine.Summary
	err error
}

func (s src) Summary() (*engine.Summary, error) { return s.s, s.err }

func TestRender(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "ssh"), 0o700)
	_ = os.WriteFile(filepath.Join(dir, "ssh", "fingerprint"), []byte("SHA256:abc\n"), 0o600)
	out := Render("v1.0.0", dir, src{s: &engine.Summary{
		Sources: []engine.Status{{Phase: engine.NeedPassword}, {Phase: engine.Running}},
		Vault:   engine.VaultState{Enabled: true, Locked: true},
		Reboot:  []string{"Flatcar OS update 4757.3.0 installed"},
	}})
	for _, want := range []string{"rightsizer appliance", "v1.0.0", "SHA256:abc", "2 vCenter sources", "Log in over SSH to resume", "vApp Options", "read-only",
		"Restart", "required: Flatcar OS update 4757.3.0 installed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	if out := Render("v1", dir, src{err: errors.New("x")}); !strings.Contains(out, "starting") {
		t.Fatal("engine down must show starting")
	}
}
