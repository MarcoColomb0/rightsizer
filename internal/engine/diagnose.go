package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
	"github.com/MarcoColomb0/rightsizer/internal/diag"
	"github.com/MarcoColomb0/rightsizer/internal/logring"
	"github.com/MarcoColomb0/rightsizer/internal/ontap"
	"github.com/MarcoColomb0/rightsizer/internal/report"
	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

func diagShare(id string) string { return "diag:" + id }

var errNoDiagnostics = errors.New("diagnostic bundles are available in pre-releases only")

const diagReadme = `rightsizer diagnostic bundle

Built on request from the console; nothing was sent anywhere. Share it with
whoever helps you, for example attached to a GitHub issue.

checks.txt      what was checked, failures first
matching.txt    every datastore of every vCenter and the volume it matched, or why not
collection.json what the engine collected so far and the sizing figures
diagnosis.json  the cluster's records and archive ranges as rightsizer read them
api.json        every ONTAP REST request and response of this run (GET only)
logs.txt        the engine's latest log lines
about.json      versions

Passwords are never included. An anonymised bundle replaces names, addresses,
serial numbers and paths with tokens; the same value always gets the same
token within one bundle.
`

// Diagnose builds a diagnostic bundle for an ONTAP source and shares it on
// the download server.
func (e *Engine) Diagnose(ctx context.Context, id string, anonymise bool) (*report.Share, error) {
	if !e.Diagnostics {
		return nil, errNoDiagnostics
	}
	s, err := e.get(id)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	cfg, live := s.st.Config, s.array
	s.mu.Unlock()
	if !cfg.IsONTAP() {
		return nil, errors.New("diagnostic bundles are available for ONTAP sources")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var cl *ontap.Client
	var password string
	switch {
	case live != nil:
		cl, err = live.Reconnect(ctx)
	case e.vault != nil && !e.vault.Locked():
		if password, err = e.secret(id); err == nil {
			var c session
			if c, err = s.connect(ctx, password); err == nil {
				cl, _ = c.(*ontap.Client)
			}
		}
	default:
		err = errors.New("the source is not connected: resume it first")
	}
	if err != nil {
		return nil, fmt.Errorf("connect to ONTAP: %w", err)
	}
	defer cl.Close(ctx)
	cl.Record()
	d := ontap.Diagnose(ctx, cl)

	b := diag.New()
	b.Anonymise = anonymise
	if password != "" {
		b.Secrets = append(b.Secrets, password)
	}
	b.Text("README.txt", diagReadme)
	estates := e.estatesByHost()
	var rows []analysis.MatchRow
	if d.Inventory != nil {
		rows = analysis.ExplainMatches(d.Inventory, estates)
	}
	b.Text("checks.txt", d.Summary()+"\n"+matchSummary(rows, len(estates))+"\n"+e.collectionSummary(s))
	b.Text("matching.txt", matchTable(rows))
	b.JSON("matching.json", rows)
	b.JSON("collection.json", e.collectionState(s))
	b.JSON("diagnosis.json", d)
	b.JSON("api.json", cl.Recorded())
	b.Text("logs.txt", strings.Join(logring.Default.Lines(), "\n")+"\n")
	b.JSON("about.json", map[string]any{
		"rightsizer": report.Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		"appliance": e.vault != nil, "generated": time.Now(), "anonymised": anonymise, "source": id,
	})

	dir := filepath.Join(s.dir, "diagnostics")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	kind := "diag"
	if anonymise {
		kind = "diag-anonymised"
	}
	path := filepath.Join(dir, fmt.Sprintf("rightsizer-%s-%s.zip", kind, time.Now().Format("20060102-150405")))
	if err := b.Write(path); err != nil {
		return nil, err
	}
	return e.web.Serve(diagShare(id), path)
}

func (e *Engine) secret(id string) (string, error) {
	sec, err := e.vault.Get(id)
	if err != nil {
		return "", err
	}
	return sec.Password, nil
}

// estatesByHost are the vCenter sources' estates by vCenter address.
func (e *Engine) estatesByHost() map[string]*vc.Estate {
	out := map[string]*vc.Estate{}
	for _, s := range e.list() {
		s.mu.Lock()
		if !s.st.Config.IsONTAP() {
			out[s.st.Config.Host] = s.st.Estate
		}
		s.mu.Unlock()
	}
	return out
}

func matchSummary(rows []analysis.MatchRow, vcenters int) string {
	n := map[string]int{}
	for _, r := range rows {
		switch {
		case r.Result == "matched":
			n["matched"]++
		case strings.HasPrefix(r.Result, "skipped"):
			n["skipped"]++
		default:
			n["unmatched"]++
		}
	}
	status := "PASS"
	switch {
	case vcenters == 0:
		return "[WARN] matching: no vCenter source added, datastores cannot be matched\n"
	case n["matched"] == 0:
		status = "FAIL"
	case n["unmatched"] > 0:
		status = "WARN"
	}
	return fmt.Sprintf("[%s] matching: %d datastores of %d vCenter(s): %d matched, %d not on this cluster, %d skipped (see matching.txt)\n",
		status, len(rows), vcenters, n["matched"], n["unmatched"], n["skipped"])
}

func matchTable(rows []analysis.MatchRow) string {
	if len(rows) == 0 {
		return "No vCenter datastores to match yet: add the vCenter sources and wait for their hardware scan.\n"
	}
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%s / %s (%s)\n  backing: %s\n  result:  %s", r.VCenter, r.Datastore, r.Type, r.Backing, r.Result)
		if r.Volume != "" {
			fmt.Fprintf(&b, " → %s", r.Volume)
		}
		b.WriteString("\n\n")
	}
	return b.String()
}

// collectionSummary says whether collection is healthy.
func (e *Engine) collectionSummary(s *Source) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	add := func(status, name, f string, a ...any) {
		fmt.Fprintf(&b, "[%s] %s: %s\n", status, name, fmt.Sprintf(f, a...))
	}
	add("INFO", "collection", "phase %s, started %s, %s", s.st.Phase, s.st.Started.Format(time.RFC3339), fmt.Sprintf("%d polls", pollsOf(s.st.ArrayRT)))
	if s.lastErr != "" {
		add("WARN", "last error", "%s", s.lastErr)
	}
	switch h := s.st.HistoryState; {
	case strings.HasPrefix(h, "imported"):
		add("PASS", "archive import", "%s", h)
	case h == "":
		add("WARN", "archive import", "not started yet")
	default:
		add("WARN", "archive import", "%s", h)
	}
	if rt := s.st.ArrayRT; rt != nil {
		hours := rt.Seconds["cluster"] / 3600
		status := "PASS"
		if rt.Polls > 2 && hours == 0 {
			status = "FAIL"
		}
		add(status, "15-second data", "%.1f hours for the cluster, %d volumes, %d nodes", hours, len(rt.Volumes), len(rt.CPU))
	}
	return b.String()
}

func pollsOf(st *analysis.ArrayStore) uint64 {
	if st == nil {
		return 0
	}
	return st.Polls
}

// collectionState is what the engine holds for the source, and the ONTAP
// figures of every vCenter's sizing.
func (e *Engine) collectionState(s *Source) map[string]any {
	type objectState struct {
		Seconds float64
		Last    time.Time
	}
	objects := func(st *analysis.ArrayStore) map[string]objectState {
		out := map[string]objectState{}
		if st == nil {
			return out
		}
		for k, v := range st.Seconds {
			out[k] = objectState{v, st.Last[k]}
		}
		for k, v := range st.Last {
			if _, ok := out[k]; !ok {
				out[k] = objectState{0, v}
			}
		}
		return out
	}
	status := s.status(true)
	s.mu.Lock()
	state := map[string]any{
		"status":   status,
		"history":  s.st.HistoryState,
		"realtime": objects(s.st.ArrayRT),
		"archive":  objects(s.st.ArrayHistory),
		"lastPoll": s.lastPoll,
		"lastErr":  s.lastErr,
	}
	host := s.st.Config.Host
	s.mu.Unlock()
	sizings := map[string]any{}
	for _, v := range e.list() {
		st := v.status(false)
		if st.Config.IsONTAP() {
			continue
		}
		sz, err := e.Sizing(v.id)
		if err != nil {
			sizings[st.Config.Host] = err.Error()
			continue
		}
		var arrays []analysis.ArraySizing
		for _, a := range sz.Storage.Arrays {
			if a.Host == host {
				a.IO.Points = nil
				arrays = append(arrays, a)
			}
		}
		var ds []map[string]any
		for _, d := range sz.Datastores {
			ds = append(ds, map[string]any{"name": d.Name, "used": d.Used, "array": d.Array})
		}
		sizings[st.Config.Host] = map[string]any{"reduction": sz.Storage.Array, "ratio": sz.Storage.Array.Ratio(), "plan": sz.Storage.Plan,
			"rawUsed": sz.Storage.RawUsed, "arrays": arrays, "datastores": ds, "notes": sz.Notes}
	}
	state["sizing"] = sizings
	return state
}
