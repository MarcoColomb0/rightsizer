package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MarcoColomb0/rightsizer/internal/atomicfile"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
	"github.com/MarcoColomb0/rightsizer/internal/report"
)

type sizingFile struct {
	Version int
	Params  analysis.SizingParams
}

func (e *Engine) sizingPath() string { return filepath.Join(e.dir, "sizing.json") }

func (e *Engine) loadSizing() error {
	e.sizing = analysis.DefaultSizing()
	b, err := os.ReadFile(e.sizingPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var f sizingFile
	if err := json.Unmarshal(b, &f); err != nil || f.Params.Validate() != nil {
		if err == nil {
			err = f.Params.Validate()
		}
		return preserve(e.sizingPath(), err)
	}
	e.sizing = f.Params
	return nil
}

func (e *Engine) SizingParams() analysis.SizingParams {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sizing
}

// SetSizingParams stores the refresh parameters used by every sizing.
func (e *Engine) SetSizingParams(p analysis.SizingParams) error {
	if err := p.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(sizingFile{Version: 1, Params: p}, "", "  ")
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := atomicfile.WriteFile(e.sizingPath(), b, 0o600); err != nil {
		return err
	}
	e.sizing = p
	return nil
}

func sizingShare(id string) string {
	if id == "" {
		id = "all"
	}
	return "sizing:" + id
}

// Sizing returns the hardware refresh sizing of one source, or of every
// source combined when id is empty, without the per-VM rows.
func (e *Engine) Sizing(id string) (*analysis.Sizing, error) {
	sz, _, err := e.buildSizing(id)
	if err != nil {
		return nil, err
	}
	out := *sz
	out.VMs = nil
	return &out, nil
}

func (e *Engine) buildSizing(id string) (*analysis.Sizing, string, error) {
	p := e.SizingParams()
	if id == "" {
		var ss []*analysis.Sizing
		for _, s := range e.list() {
			if sz := s.sizing(p); sz != nil {
				ss = append(ss, sz)
			}
		}
		if len(ss) == 0 {
			return nil, "", errors.New("no inventory yet")
		}
		return analysis.MergeSizing(ss), filepath.Join(e.dir, "reports"), nil
	}
	s, err := e.get(id)
	if err != nil {
		return nil, "", err
	}
	sz := s.sizing(p)
	if sz == nil {
		return nil, "", errors.New("inventory not loaded yet")
	}
	return sz, filepath.Join(s.dir, "reports"), nil
}

// PublishSizing renders the sizing PDF and its spreadsheet data and shares
// both on the download server.
func (e *Engine) PublishSizing(id string) (*report.Share, error) {
	sz, dir, err := e.buildSizing(id)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	base := filepath.Join(dir, fmt.Sprintf("rightsizer-sizing-%s", sz.Generated.Format("20060102-150405")))
	if err := report.WriteSizingPDF(sz, base+".pdf"); err != nil {
		return nil, err
	}
	if err := report.WriteSizingData(sz, base+"-data.zip"); err != nil {
		return nil, err
	}
	return e.web.Serve(sizingShare(id), base+".pdf", base+"-data.zip")
}
