package vault

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestVault(t *testing.T) {
	dir := t.TempDir()
	v := Open(dir)
	if v.Configured() {
		t.Fatal("fresh vault must not be configured")
	}
	if err := v.Reset("short"); err == nil {
		t.Fatal("short password must be rejected")
	}
	pw := "correct horse battery"
	if err := v.Reset(pw); err != nil {
		t.Fatal(err)
	}
	if err := v.Put("vc1", Secret{User: "ro@vsphere.local", Password: "vc-secret-1"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(dir + "/vault.json")
	if strings.Contains(string(raw), "vc-secret-1") || strings.Contains(string(raw), "ro@vsphere") {
		t.Fatal("secrets must not be stored in clear text")
	}

	v2 := Open(dir)
	if !v2.Locked() {
		t.Fatal("reopened vault must be locked")
	}
	if _, err := v2.Get("vc1"); !errors.Is(err, ErrLocked) {
		t.Fatal("locked vault must refuse reads")
	}
	if err := v2.Unlock("wrong password here"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("want wrong password, got %v", err)
	}
	if err := v2.Unlock(pw); err != nil {
		t.Fatal(err)
	}
	s, err := v2.Get("vc1")
	if err != nil || s.Password != "vc-secret-1" {
		t.Fatalf("got %+v %v", s, err)
	}

	next := "another long passphrase"
	if err := v2.ChangePassword(pw, next); err != nil {
		t.Fatal(err)
	}
	v3 := Open(dir)
	if v3.Verify(pw) || !v3.Verify(next) {
		t.Fatal("password change not applied")
	}
	if err := v3.Unlock(next); err != nil {
		t.Fatal(err)
	}
	if s, _ := v3.Get("vc1"); s.Password != "vc-secret-1" {
		t.Fatal("secrets lost on password change")
	}
	if err := v3.Delete("vc1"); err != nil {
		t.Fatal(err)
	}
	if _, err := v3.Get("vc1"); err == nil {
		t.Fatal("deleted secret still present")
	}
}

func TestTamperDetected(t *testing.T) {
	dir := t.TempDir()
	v := Open(dir)
	pw := "correct horse battery"
	if err := v.Reset(pw); err != nil {
		t.Fatal(err)
	}
	_ = v.Put("a", Secret{Password: "x"})
	b, _ := os.ReadFile(dir + "/vault.json")
	i := strings.Index(string(b), `"Data":"`) + 10
	b[i] ^= 1
	_ = os.WriteFile(dir+"/vault.json", b, 0o600)
	if err := Open(dir).Unlock(pw); err == nil {
		t.Fatal("tampered vault must not unlock")
	}
}
