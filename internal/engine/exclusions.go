package engine

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/atomicfile"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
)

const maxExclusions = 1000

type exclusionFile struct {
	Version    int
	Exclusions []analysis.Exclusion
}

func (e *Engine) exclusionsPath() string { return filepath.Join(e.dir, "exclusions.json") }

func (e *Engine) loadExclusions() error {
	b, err := os.ReadFile(e.exclusionsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var f exclusionFile
	if err := json.Unmarshal(b, &f); err != nil {
		return preserve(e.exclusionsPath(), err)
	}
	e.excl = f.Exclusions
	return nil
}

func (e *Engine) saveExclusions() error {
	b, err := json.MarshalIndent(exclusionFile{Version: 1, Exclusions: e.excl}, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(e.exclusionsPath(), b, 0o600)
}

// Exclusions lists every exclusion across all vCenters.
func (e *Engine) Exclusions() []analysis.Exclusion {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]analysis.Exclusion, len(e.excl))
	copy(out, e.excl)
	return out
}

// Exclude records an exclusion and recomputes every affected analysis.
func (e *Engine) Exclude(x analysis.Exclusion) (string, error) {
	x.Note = strings.TrimSpace(x.Note)
	x.Name = strings.TrimSpace(x.Name)
	if err := x.Validate(); err != nil {
		return "", err
	}
	x.ID = newID()
	x.Created = time.Now()
	e.mu.Lock()
	if len(e.excl) >= maxExclusions {
		e.mu.Unlock()
		return "", errors.New("too many exclusions")
	}
	e.excl = append(e.excl, x)
	err := e.saveExclusions()
	e.mu.Unlock()
	if err != nil {
		return "", err
	}
	e.refreshAll()
	return x.ID, nil
}

func (e *Engine) Unexclude(id string) error {
	e.mu.Lock()
	n := len(e.excl)
	for i, x := range e.excl {
		if x.ID == id {
			e.excl = append(e.excl[:i], e.excl[i+1:]...)
			break
		}
	}
	if len(e.excl) == n {
		e.mu.Unlock()
		return errors.New("unknown exclusion")
	}
	err := e.saveExclusions()
	e.mu.Unlock()
	if err != nil {
		return err
	}
	e.refreshAll()
	return nil
}

// exclusionsFor returns the exclusions that apply to one vCenter.
func (e *Engine) exclusionsFor(host string) []analysis.Exclusion {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []analysis.Exclusion
	for _, x := range e.excl {
		if x.VCenter == "" || strings.EqualFold(x.VCenter, host) {
			out = append(out, x)
		}
	}
	return out
}

func (e *Engine) refreshAll() {
	for _, s := range e.list() {
		s.refreshResult()
	}
}
