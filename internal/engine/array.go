package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
	"github.com/MarcoColomb0/rightsizer/internal/ontap"
	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

// maxArrayVolumes caps the volumes whose performance is read per cluster.
const maxArrayVolumes = 256

// arrayObject is one thing whose performance is read: the cluster, a node
// (read by UUID, kept by name) or a volume.
type arrayObject struct {
	kind     ontap.Object
	uuid, id string
}

// arrayLoop collects an ONTAP cluster: its records every hour, its archive
// once per object, and the last hour of 15-second samples every poll.
func (s *Source) arrayLoop(ctx context.Context) {
	var lastInv time.Time
	for {
		s.mu.Lock()
		ends := s.st.Ends
		s.mu.Unlock()
		if !time.Now().Before(ends) {
			go func() {
				s.stop()
				if err := s.finalize(); err != nil {
					slog.Error("finalize", "source", s.id, "err", err)
				}
			}()
			return
		}
		if time.Since(lastInv) >= inventoryEvery {
			if err := s.refreshArrayInventory(ctx); err != nil {
				if s.fail(err) {
					return
				}
			} else {
				lastInv = time.Now()
			}
		}
		if err := s.pollArray(ctx); err != nil {
			if s.fail(err) {
				return
			}
		}
		next := time.Now().Add(pollEvery)
		if next.After(ends) {
			next = ends
		}
		s.mu.Lock()
		s.nextPoll = next
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
	}
}

func (s *Source) refreshArrayInventory(ctx context.Context) error {
	s.mu.Lock()
	cl := s.array
	s.mu.Unlock()
	start := time.Now()
	inv, err := cl.Inventory(ctx)
	if err != nil {
		return err
	}
	slog.Debug("ONTAP inventory", "source", s.id, "nodes", len(inv.Nodes), "aggregates", len(inv.Aggregates), "volumes", len(inv.Volumes),
		"luns", len(inv.LUNs), "lifs", len(inv.LIFs), "dropped", cl.Dropped(), "ms", time.Since(start).Milliseconds())
	s.mu.Lock()
	s.st.Array = inv
	s.mu.Unlock()
	return nil
}

func (s *Source) pollArray(ctx context.Context) error {
	s.mu.Lock()
	cl, inv, rt, hist, started := s.array, s.st.Array, s.st.ArrayRT, s.st.ArrayHistory, s.st.Started
	s.mu.Unlock()
	if inv == nil {
		return errors.New("cluster records not loaded yet")
	}
	start := time.Now()
	vols, matched := s.eng.arrayVolumes(inv)
	objs := []arrayObject{{kind: ontap.ClusterObject}}
	for _, n := range inv.Nodes {
		objs = append(objs, arrayObject{ontap.NodeObject, n.UUID, n.Name})
	}
	for _, v := range vols {
		objs = append(objs, arrayObject{ontap.VolumeObject, v, v})
	}
	s.importArrayHistory(ctx, cl, hist, objs)

	type read struct {
		o  arrayObject
		ss []ontap.Sample
	}
	var reads []read
	for _, o := range objs {
		ss, err := cl.Metrics(ctx, o.kind, o.uuid, ontap.Hour)
		switch {
		case err == nil:
			reads = append(reads, read{o, ss})
		case o.kind == ontap.ClusterObject || ontap.IsAuthError(err) || ctx.Err() != nil:
			return err
		default:
			// A volume moved or deleted since the last inventory.
			slog.Debug("ONTAP metrics skipped", "source", s.id, "object", o.id, "err", err)
		}
	}
	s.mu.Lock()
	samples := 0
	for _, r := range reads {
		samples += len(r.ss)
		rt.Add(r.o.kind, r.o.id, r.ss, started, time.Now().Add(time.Hour))
	}
	slog.Debug("ONTAP poll", "source", s.id, "objects", len(objs), "read", len(reads), "volumes", len(vols), "datastores", matched,
		"samples", samples, "cluster_hours", rt.Seconds["cluster"]/3600, "ms", time.Since(start).Milliseconds())
	rt.Polls++
	s.lastPoll = time.Now()
	s.lastErr = ""
	s.authFails = 0
	err := s.save()
	s.mu.Unlock()
	s.refreshArray()
	return err
}

// importArrayHistory reads ONTAP's archive for every object not imported
// yet: at the start for the cluster and nodes, later for volumes that back
// a datastore of a vCenter added afterwards.
func (s *Source) importArrayHistory(ctx context.Context, cl *ontap.Client, hist *analysis.ArrayStore, objs []arrayObject) {
	s.mu.Lock()
	state := s.st.HistoryState
	var todo []arrayObject
	for _, o := range objs {
		if hist.LastOf(o.kind, o.id).IsZero() {
			todo = append(todo, o)
		}
	}
	s.mu.Unlock()
	if len(todo) == 0 || state == "unavailable" {
		return
	}
	now := time.Now()
	plan := ontap.HistoryPlan(now, historyBack)
	if state == "" {
		s.setHistory("loading")
	}
	for i, o := range todo {
		if state == "" {
			s.setHistory(fmt.Sprintf("loading %d%%", i*100/len(todo)))
		}
		var got []func()
		failed := false
		// Oldest window first: samples older than the last one seen are
		// ignored.
		for _, w := range slices.Backward(plan) {
			ss, err := cl.Metrics(ctx, o.kind, o.uuid, w.Interval)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				slog.Warn("ONTAP history import failed", "source", s.id, "object", o.id, "interval", w.Interval, "err", err)
				failed = true
				break
			}
			slog.Debug("ONTAP archive", "source", s.id, "object", o.id, "interval", w.Interval, "samples", len(ss))
			got = append(got, func() { hist.Add(o.kind, o.id, ss, w.Start, w.End) })
		}
		if failed && o.kind == ontap.ClusterObject {
			s.setHistory("unavailable")
			return
		}
		s.mu.Lock()
		for _, add := range got {
			add()
		}
		if failed {
			// A missing archive is not retried on every poll.
			hist.Mark(o.kind, o.id, now)
		}
		s.mu.Unlock()
	}
	s.mu.Lock()
	hist.Flush()
	if h := s.st.HistoryState; h == "" || strings.HasPrefix(h, "loading") {
		s.st.HistoryState = fmt.Sprintf("imported from %s", plan[len(plan)-1].Start.Local().Format("Jan 02"))
		s.st.HistorySync = now
	}
	_ = s.save()
	s.mu.Unlock()
	slog.Info("ONTAP history imported", "source", s.id, "objects", len(todo))
}

// refreshArray recomputes the summary shown for an ONTAP source.
func (s *Source) refreshArray() {
	s.mu.Lock()
	array, inv := s.st.Config.IsONTAP(), s.st.Array
	s.mu.Unlock()
	if !array || inv == nil {
		return
	}
	vols, n := s.eng.arrayVolumes(inv)
	s.mu.Lock()
	defer s.mu.Unlock()
	a := analysis.ArrayInput{Host: s.st.Config.Host, About: s.st.About, Inv: s.st.Array, RT: s.st.ArrayRT, History: s.st.ArrayHistory}
	sum := analysis.SummarizeArray(a, vols, n, analysis.ProfileByName(s.st.Config.Profile).Percentile)
	s.summary = &sum
}

// estates are the hardware of every vCenter source.
func (e *Engine) estates() []*vc.Estate {
	var out []*vc.Estate
	for _, s := range e.list() {
		s.mu.Lock()
		if !s.st.Config.IsONTAP() && s.st.Estate != nil {
			out = append(out, s.st.Estate)
		}
		s.mu.Unlock()
	}
	return out
}

// arrayVolumes are the volumes of a cluster that back datastores of any
// vCenter source, and the number of those datastores.
func (e *Engine) arrayVolumes(inv *ontap.Inventory) ([]string, int) {
	vols, n := analysis.MatchedVolumes(inv, e.estates())
	if len(vols) > maxArrayVolumes {
		vols = vols[:maxArrayVolumes]
	}
	return vols, n
}

// arrayInputs copies every ONTAP source for sizing, so collection goes on
// while reports are built.
func (e *Engine) arrayInputs() []analysis.ArrayInput {
	var out []analysis.ArrayInput
	for _, s := range e.list() {
		s.mu.Lock()
		if s.st.Config.IsONTAP() && s.st.Array != nil {
			out = append(out, analysis.ArrayInput{Host: s.st.Config.Host, About: s.st.About, Inv: s.st.Array,
				RT: s.st.ArrayRT.Clone(), History: s.st.ArrayHistory.Clone()})
		}
		s.mu.Unlock()
	}
	return out
}
