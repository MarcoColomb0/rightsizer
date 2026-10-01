package demo

import (
	"testing"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
	"github.com/MarcoColomb0/rightsizer/internal/engine"
	"github.com/MarcoColomb0/rightsizer/internal/ipc"
)

var _ ipc.Backend = (*Backend)(nil)

func TestDemoBackend(t *testing.T) {
	b := New(Options{Appliance: true})
	sum, err := b.Summary()
	if err != nil || len(sum.Sources) != 3 || len(sum.Reboot) == 0 || len(sum.Shares) != 1 {
		t.Fatalf("summary %+v %v", sum, err)
	}
	run := sum.Sources[0]
	if run.Phase != engine.Running || run.Totals == nil || run.Findings == 0 || run.Polls == 0 {
		t.Fatalf("running source %+v", run)
	}
	st, err := b.Source(run.ID)
	if err != nil || st.Result == nil || len(st.Result.Clusters) != 3 {
		t.Fatalf("source %+v %v", st, err)
	}
	sz, err := b.Sizing(run.ID)
	if err != nil || len(sz.Clusters) == 0 || sz.Storage.RawUsed == 0 {
		t.Fatalf("sizing %+v %v", sz, err)
	}
	before := st.Findings
	f := st.Result.Findings[0]
	if err := b.Exclude(analysis.Exclusion{VCenter: run.Config.Host, UUID: f.UUID, Name: f.VM, Reason: "Other", Note: "demo"}); err != nil {
		t.Fatal(err)
	}
	if st, _ = b.Source(run.ID); st.Findings >= before {
		t.Fatalf("exclusion must remove findings: %d -> %d", before, st.Findings)
	}
	if _, err := b.PublishSizing(run.ID); err != nil {
		t.Fatal(err)
	}
	id, err := b.Add(engine.Config{Host: "vc-new.corp.local", Profile: "balanced", Duration: 7 * 24 * 3600e9}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := b.Source(id); !st.Preview || st.Result == nil {
		t.Fatalf("new source must start in preview: %+v", st)
	}
	if err := b.Finish(run.ID); err != nil {
		t.Fatal(err)
	}
	if sum, _ = b.Summary(); len(sum.Shares) < 3 {
		t.Fatalf("finishing shares the final report: %d shares", len(sum.Shares))
	}
}
