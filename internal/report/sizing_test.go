package report

import (
	"archive/zip"
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
	"github.com/MarcoColomb0/rightsizer/internal/demo/sample"
)

func demoSizing() *analysis.Sizing {
	d := sample.Generate(sample.Default())
	d.Input.Inv.VMs[3].Name = "=HYPERLINK(\"http://x\")"
	p := analysis.DefaultSizing()
	p.Groups = "databases=*db*; web=*web*"
	return sample.Sizing(d, sample.Result(d), p)
}

func TestWriteSizing(t *testing.T) {
	dir := t.TempDir()
	out := os.Getenv("RIGHTSIZER_DEMO_SIZING")
	if out == "" {
		out = filepath.Join(dir, "sizing.pdf")
	}
	sz := demoSizing()
	if len(sz.Clusters) != 3 || sz.Totals.For(sz.Params.Basis).Nodes == 0 || !sz.Storage.IO.Available {
		t.Fatalf("demo sizing incomplete: %+v", sz.Totals)
	}
	if err := WriteSizingPDF(sz, out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil || !strings.HasPrefix(string(b), "%PDF") {
		t.Fatalf("invalid pdf: %v", err)
	}
	data := filepath.Join(dir, "data.zip")
	if err := WriteSizingData(sz, data); err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(data)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	files := map[string][][]string{}
	for _, f := range z.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		rows, err := csv.NewReader(rc).ReadAll()
		rc.Close()
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		files[f.Name] = rows
	}
	for _, n := range []string{"vms.csv", "hosts.csv", "adapters.csv", "vmkernel.csv", "datastores.csv", "clusters.csv", "node-options.csv", "needs.csv", "workloads.csv", "storage-summary.csv"} {
		if len(files[n]) < 2 {
			t.Fatalf("%s has no rows", n)
		}
	}
	if len(files["vms.csv"]) != len(sz.VMs)+1 {
		t.Fatalf("vms.csv: %d rows for %d VMs", len(files["vms.csv"]), len(sz.VMs))
	}
	for _, r := range files["vms.csv"] {
		if strings.HasPrefix(r[0], "=") {
			t.Fatalf("formula not neutralised: %q", r[0])
		}
	}
}

func TestCell(t *testing.T) {
	for in, want := range map[string]string{"=1+1": "'=1+1", "-5": "-5", "+cmd": "'+cmd", "@x": "'@x", "web": "web", "": ""} {
		if got := cell(in); got != want {
			t.Fatalf("cell(%q) = %q, want %q", in, got, want)
		}
	}
}
