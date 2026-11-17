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
	if err != nil || len(sum.Sources) != 4 || len(sum.Reboot) == 0 || len(sum.Shares) != 1 {
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
	if err != nil || len(sz.Clusters) == 0 || sz.Storage.RawUsed == 0 || sz.Storage.Array.Datastores == 0 {
		t.Fatalf("sizing %+v %v", sz, err)
	}
	arr := sum.Sources[1]
	if !arr.Config.IsONTAP() || arr.Array == nil || arr.Array.Matched != 5 || arr.Array.Ratio < 1.5 {
		t.Fatalf("ONTAP source %+v", arr)
	}
	if st, err := b.Source(arr.ID); err != nil || st.Array == nil || len(st.Array.Aggregates) != 2 {
		t.Fatalf("ONTAP status %+v %v", st, err)
	}
	if _, err := b.Sizing(arr.ID); err == nil {
		t.Fatal("an ONTAP source has no sizing of its own")
	}
	if all, err := b.Sizing(""); err != nil || len(all.Storage.Arrays) != 1 || all.Storage.NetAppDatastores == 0 {
		t.Fatalf("combined sizing %+v %v", all.Storage.Array, err)
	}
	if id, err := b.Add(engine.Config{Kind: engine.KindONTAP, Host: "aff02.corp.local", Duration: 7 * 24 * 3600e9}, "secret"); err != nil {
		t.Fatal(err)
	} else if st, _ := b.Source(id); st.Array == nil || st.Config.Host != "aff02.corp.local" {
		t.Fatalf("new ONTAP source %+v", st)
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
