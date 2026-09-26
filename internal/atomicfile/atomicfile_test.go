package atomicfile

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReplacesOrKeeps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	failed := errors.New("encoder failed")
	err := Write(path, 0o600, func(w io.Writer) error {
		if _, err := w.Write([]byte("partial")); err != nil {
			return err
		}
		return failed
	})
	if !errors.Is(err, failed) {
		t.Fatalf("want the writer's error, got %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "old" {
		t.Fatalf("a failed write must keep the old content, got %q", b)
	}
	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary file left behind: %v", err)
	}
	if err := WriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	fi, _ := os.Stat(path)
	if string(b) != "new" || fi.Mode().Perm() != 0o600 {
		t.Fatalf("got %q with mode %v", b, fi.Mode().Perm())
	}
}
