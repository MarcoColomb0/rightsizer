package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
