// Package atomicfile replaces files so that readers, and the disk after a
// crash or power loss, see either the old content or the new one in full.
package atomicfile

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

// Write replaces path with what fn writes. The content goes to path+".tmp",
// is synced to disk, then renamed over path, and the directory is synced so
// the rename survives a power loss.
func Write(path string, perm os.FileMode, fn func(io.Writer) error) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	err = fn(f)
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	if err := os.Rename(tmp, path); err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	return syncDir(filepath.Dir(path))
}

// WriteFile is Write for content already in memory.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	return Write(path, perm, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}
