package backup

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	keep     = 3
	maxBytes = 4 << 30
	prefix   = "rightsizer-data-"
)

// Create archives the regular files under dataDir into dst (tar.gz) and keeps
// only the newest backups next to it.
func Create(dataDir, dst string) error {
	if !strings.HasPrefix(filepath.Base(dst), prefix) || !strings.HasSuffix(dst, ".tar.gz") {
		return fmt.Errorf("backup file name must match %s*.tar.gz", prefix)
	}
	tmp := dst + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	n := 0
	err = filepath.WalkDir(dataDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() || strings.HasSuffix(path, ".tmp") {
			return nil
		}
		rel, err := filepath.Rel(dataDir, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr := &tar.Header{Name: filepath.ToSlash(rel), Mode: 0o600, Size: info.Size(), ModTime: info.ModTime(), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		if _, err := io.Copy(tw, src); err != nil {
			return err
		}
		n++
		return nil
	})
	if err == nil {
		err = tw.Close()
	}
	if err == nil {
		err = gz.Close()
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "backed up %d files to %s\n", n, dst)
	return prune(filepath.Dir(dst))
}

func prune(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) && strings.HasSuffix(e.Name(), ".tar.gz") {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	for len(names) > keep {
		if err := os.Remove(filepath.Join(dir, names[0])); err != nil {
			return err
		}
		names = names[1:]
	}
	return nil
}

// Restore replaces the contents of dataDir with the archive. The archive is
// fully validated and extracted to a staging directory before anything in
// dataDir is touched.
func Restore(src, dataDir string) error {
	stage, err := os.MkdirTemp(dataDir, ".restore-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := extract(src, stage); err != nil {
		return fmt.Errorf("invalid backup: %w", err)
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if filepath.Join(dataDir, e.Name()) == stage {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dataDir, e.Name())); err != nil {
			return err
		}
	}
	staged, err := os.ReadDir(stage)
	if err != nil {
		return err
	}
	for _, e := range staged {
		if err := os.Rename(filepath.Join(stage, e.Name()), filepath.Join(dataDir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func extract(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	var total int64
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			return fmt.Errorf("unexpected entry type for %q", hdr.Name)
		}
		name := filepath.Clean(filepath.FromSlash(hdr.Name))
		if !filepath.IsLocal(name) {
			return fmt.Errorf("unsafe path %q", hdr.Name)
		}
		total += hdr.Size
		if total > maxBytes {
			return errors.New("archive too large")
		}
		out := filepath.Join(dst, name)
		if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
			return err
		}
		w, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		_, err = io.CopyN(w, tr, hdr.Size)
		if cerr := w.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
}
