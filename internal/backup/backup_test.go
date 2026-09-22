package backup

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTripAndPrune(t *testing.T) {
	data, bdir := t.TempDir(), t.TempDir()
	must(t, os.MkdirAll(filepath.Join(data, "reports"), 0o700))
	must(t, os.WriteFile(filepath.Join(data, "state.gob"), []byte("state-v1"), 0o600))
	must(t, os.WriteFile(filepath.Join(data, "reports", "r.pdf"), []byte("%PDF"), 0o600))
	for i := 1; i <= 5; i++ {
		must(t, Create(data, filepath.Join(bdir, fmt.Sprintf("rightsizer-data-2026010%d.tar.gz", i))))
	}
	left, _ := os.ReadDir(bdir)
	if len(left) != keep {
		t.Fatalf("want %d backups, got %d", keep, len(left))
	}

	must(t, os.WriteFile(filepath.Join(data, "state.gob"), []byte("corrupted"), 0o600))
	must(t, os.WriteFile(filepath.Join(data, "extra"), []byte("x"), 0o600))
	must(t, Restore(filepath.Join(bdir, "rightsizer-data-20260105.tar.gz"), data))
	b, _ := os.ReadFile(filepath.Join(data, "state.gob"))
	if string(b) != "state-v1" {
		t.Fatalf("state not restored: %q", b)
	}
	if _, err := os.Stat(filepath.Join(data, "extra")); !os.IsNotExist(err) {
		t.Fatal("restore must replace directory contents")
	}
	if _, err := os.Stat(filepath.Join(data, "reports", "r.pdf")); err != nil {
		t.Fatal(err)
	}
}

func TestRejectTraversal(t *testing.T) {
	data := t.TempDir()
	must(t, os.WriteFile(filepath.Join(data, "state.gob"), []byte("keep"), 0o600))
	evil := filepath.Join(t.TempDir(), "evil.tar.gz")
	f, _ := os.Create(evil)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "../../etc/passwd", Mode: 0o600, Size: 1, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("x"))
	tw.Close()
	gz.Close()
	f.Close()
	if err := Restore(evil, data); err == nil {
		t.Fatal("path traversal must be rejected")
	}
	b, _ := os.ReadFile(filepath.Join(data, "state.gob"))
	if string(b) != "keep" {
		t.Fatal("failed restore must not touch existing data")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
