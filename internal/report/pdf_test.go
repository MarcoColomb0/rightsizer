package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
	"github.com/MarcoColomb0/rightsizer/internal/demo/sample"
)

func demo() *analysis.Result {
	return sample.Result(sample.Generate(sample.Default()))
}

func TestWritePDF(t *testing.T) {
	out := os.Getenv("RIGHTSIZER_DEMO_PDF")
	if out == "" {
		out = filepath.Join(t.TempDir(), "demo.pdf")
	}
	if err := WritePDF(demo(), out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil || !strings.HasPrefix(string(b), "%PDF") {
		t.Fatalf("invalid pdf: %v", err)
	}
}

func TestTableWrapsLongCells(t *testing.T) {
	height := func(cell string) float64 {
		d := newDoc("t", "vc", time.Now())
		d.AddPage()
		y := d.GetY()
		d.table([]col{{"Disk", 40, "L"}, {"Size", 140, "R"}}, [][]string{{cell, "1 GB"}})
		return d.GetY() - y
	}
	short := height("disk.vmdk")
	long := height("[ds-prod-01] archive/2024/old-sql02/old-sql02_1-000004-sesparse.vmdk")
	if long <= short {
		t.Fatalf("a long cell must wrap onto more lines instead of being cut: %.1f vs %.1f mm", long, short)
	}
}
