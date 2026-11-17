package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
	"github.com/MarcoColomb0/rightsizer/internal/ontap"
	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

const (
	// maxArrayVolumes caps the volumes whose performance is read per cluster.
	maxArrayVolumes = 256
	// readsAtOnce is how many requests run in parallel against a cluster.
	readsAtOnce = 4
	// importPerPoll caps the objects whose archive one poll imports.
	importPerPoll = 24
)

// each runs fn for every item, at most n at a time.
func each[T any](items []T, n int, fn func(int, T)) {
	var wg sync.WaitGroup
	slots := make(chan struct{}, n)
	for i, it := range items {
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			fn(i, it)
		})
	}
	wg.Wait()
}

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

	// Reads run a few at a time: one after another they took 7 s for 16
	// objects on a real cluster, and grow with every matched volume.
	type read struct {
		o   arrayObject
		ss  []ontap.Sample
		err error
	}
	results := make([]read, len(objs))
	each(objs, readsAtOnce, func(i int, o arrayObject) {
		ss, err := cl.Metrics(ctx, o.kind, o.uuid, ontap.Hour)
		results[i] = read{o, ss, err}
	})
	var reads []read
	for _, r := range results {
		switch {
		case r.err == nil:
			reads = append(reads, r)
		case r.o.kind == ontap.ClusterObject || ontap.IsAuthError(r.err) || ctx.Err() != nil:
			return r.err
		default:
			// A volume moved or deleted since the last inventory.
			slog.Debug("ONTAP metrics skipped", "source", s.id, "object", r.o.id, "err", r.err)
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
	state, tried := s.st.HistoryState, s.st.HistorySync
	var todo []arrayObject
	for _, o := range objs {
		if hist.LastOf(o.kind, o.id).IsZero() {
			todo = append(todo, o)
		}
	}
	s.mu.Unlock()
	// An unavailable archive is tried again every hour, not on every poll.
	if len(todo) == 0 || state == "unavailable" && time.Since(tried) < historyEvery {
		return
	}
	// A large import is spread over several polls, so none is held up.
	total := len(todo)
	todo = todo[:min(total, importPerPoll)]
	now := time.Now()
	plan := ontap.HistoryPlan(now, historyBack)
	fresh := state == "" || state == "unavailable" || strings.HasPrefix(state, "loading")
	if fresh {
		s.setHistory(fmt.Sprintf("loading %d objects", total))
	}
	type archive struct {
		adds    []func()
		failure error
	}
	results := make([]archive, len(todo))
	each(todo, readsAtOnce, func(i int, o arrayObject) {
		// Oldest window first: samples older than the last one seen are
		// ignored.
		for _, w := range slices.Backward(plan) {
			ss, err := cl.Metrics(ctx, o.kind, o.uuid, w.Interval)
			if err != nil {
				results[i].failure = err
				return
			}
			slog.Debug("ONTAP archive", "source", s.id, "object", o.id, "interval", w.Interval, "samples", len(ss))
			results[i].adds = append(results[i].adds, func() { hist.Add(o.kind, o.id, ss, w.Start, w.End) })
		}
	})
	for i, o := range todo {
		r := results[i]
		switch {
		case r.failure == nil:
		case ctx.Err() != nil || ontap.IsAuthError(r.failure):
			// Not the archive's fault: the next poll tries again.
			s.restoreHistory(state)
			return
		case o.kind == ontap.ClusterObject:
			slog.Warn("ONTAP archive unavailable, trying again in an hour", "source", s.id, "err", r.failure)
			s.mu.Lock()
			s.st.HistoryState, s.st.HistorySync = "unavailable", now
			_ = s.save()
			s.mu.Unlock()
			return
		default:
			slog.Warn("ONTAP history import failed", "source", s.id, "object", o.id, "err", r.failure)
		}
		s.mu.Lock()
		for _, add := range r.adds {
			add()
		}
		// Read, even when the archive was empty or missing: it is not read
		// again on every poll.
		hist.Mark(o.kind, o.id, now)
		s.mu.Unlock()
	}
	s.mu.Lock()
	hist.Flush()
	if h := s.st.HistoryState; total == len(todo) && (h == "" || strings.HasPrefix(h, "loading")) {
		s.st.HistoryState = fmt.Sprintf("imported from %s", plan[len(plan)-1].Start.Local().Format("Jan 02"))
		s.st.HistorySync = now
	}
	_ = s.save()
	s.mu.Unlock()
	slog.Info("ONTAP history imported", "source", s.id, "objects", len(todo), "remaining", total-len(todo))
}

// restoreHistory puts back the archive state an interrupted import found.
func (s *Source) restoreHistory(state string) {
	s.mu.Lock()
	if strings.HasPrefix(s.st.HistoryState, "loading") {
		s.st.HistoryState = state
	}
	s.mu.Unlock()
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
