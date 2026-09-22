package vc

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vmware/govmomi/performance"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

const (
	CPUUsage   = "cpu.usage.average"
	CPUReady   = "cpu.ready.summation"
	CPUCoStop  = "cpu.costop.summation"
	CPUMHz     = "cpu.usagemhz.average"
	MemUsage   = "mem.usage.average"
	MemConsume = "mem.consumed.average"
	DiskUsage  = "disk.usage.average"
	NetUsage   = "net.usage.average"
)

var (
	VMMetrics   = []string{CPUUsage, CPUReady, CPUCoStop, CPUMHz, MemUsage, MemConsume, DiskUsage, NetUsage}
	HostMetrics = []string{CPUMHz, MemConsume}
	// HistoryVMMetrics are statistics level 1 counters, kept by every vCenter
	// in its historical rollups.
	HistoryVMMetrics = []string{CPUUsage, CPUReady, CPUMHz, MemUsage, MemConsume, DiskUsage, NetUsage}
	optional         = map[string]bool{CPUCoStop: true}
)

const (
	RealtimeInterval = 20
	realtimeBatch    = 64
	// vCenter rejects historical queries above config.vpxd.stats.maxQueryMetrics
	// entity x counter pairs (64 by default).
	historyMetricLimit = 64
)

type perfCounters struct {
	byName map[string]int32
	byID   map[int32]string
}

// Series holds the samples of one entity in ascending time order. Interval
// is the length in seconds that each sample represents.
type Series struct {
	Ref      string
	Interval int32
	TS       []time.Time
	Values   map[string][]float64
}

func (c *Client) counters(ctx context.Context) (*perfCounters, error) {
	if c.perf != nil {
		return c.perf, nil
	}
	info, err := performance.NewManager(c.vim).CounterInfoByName(ctx)
	if err != nil {
		return nil, err
	}
	pc := &perfCounters{byName: map[string]int32{}, byID: map[int32]string{}}
	for _, n := range append(slices.Clone(VMMetrics), HostMetrics...) {
		ci, ok := info[n]
		if !ok {
			if optional[n] {
				continue
			}
			return nil, errors.New("perf counter " + n + " not available")
		}
		pc.byName[n] = ci.Key
		pc.byID[ci.Key] = n
	}
	c.perf = pc
	return pc, nil
}

// Sample returns 20-second real-time samples newer than since. vCenter keeps
// real-time data for about an hour, so callers must poll more often.
func (c *Client) Sample(ctx context.Context, kind string, refs []string, metrics []string, since time.Time) ([]Series, error) {
	w := Window{Interval: RealtimeInterval, Start: since}
	return c.query(ctx, kind, refs, metrics, w, realtimeBatch)
}

// Window is a time range read at one statistics interval.
type Window struct {
	Interval int32
	Start    time.Time
	End      time.Time
}

// HistoryPlan splits the last `back` of history across vCenter's enabled
// statistics intervals, finest first, so that no period is read twice.
func (c *Client) HistoryPlan(ctx context.Context, back time.Duration) ([]Window, error) {
	if err := c.Ensure(ctx); err != nil {
		return nil, err
	}
	var pm mo.PerformanceManager
	if err := c.retrieveOne(ctx, *c.vim.ServiceContent.PerfManager, []string{"historicalInterval"}, &pm); err != nil {
		return nil, err
	}
	now, err := c.Now(ctx)
	if err != nil {
		return nil, err
	}
	ivs := slices.Clone(pm.HistoricalInterval)
	sort.Slice(ivs, func(i, j int) bool { return ivs[i].SamplingPeriod < ivs[j].SamplingPeriod })
	oldest := now.Add(-back)
	cursor := now
	var out []Window
	for _, iv := range ivs {
		if !iv.Enabled || iv.Level < 1 || iv.SamplingPeriod <= 0 || !cursor.After(oldest) {
			continue
		}
		from := now.Add(-time.Duration(iv.Length) * time.Second)
		if from.Before(oldest) {
			from = oldest
		}
		if !from.Before(cursor) {
			continue
		}
		out = append(out, Window{Interval: iv.SamplingPeriod, Start: from, End: cursor})
		cursor = from
	}
	return out, nil
}

// History reads one window of rolled-up statistics.
func (c *Client) History(ctx context.Context, kind string, refs []string, metrics []string, w Window) ([]Series, error) {
	return c.query(ctx, kind, refs, metrics, w, max(1, historyMetricLimit/len(metrics)))
}

func (c *Client) query(ctx context.Context, kind string, refs []string, metrics []string, w Window, batch int) ([]Series, error) {
	if err := c.Ensure(ctx); err != nil {
		return nil, err
	}
	pc, err := c.counters(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]types.PerfMetricId, 0, len(metrics))
	for _, m := range metrics {
		if id, ok := pc.byName[m]; ok {
			ids = append(ids, types.PerfMetricId{CounterId: id, Instance: ""})
		}
	}
	pm := performance.NewManager(c.vim)
	var out []Series
	for start := 0; start < len(refs); {
		end := min(start+batch, len(refs))
		specs := make([]types.PerfQuerySpec, 0, end-start)
		for _, r := range refs[start:end] {
			s := types.PerfQuerySpec{
				Entity:     types.ManagedObjectReference{Type: kind, Value: r},
				MetricId:   ids,
				IntervalId: w.Interval,
				Format:     string(types.PerfFormatNormal),
			}
			switch {
			case !w.Start.IsZero():
				t := w.Start
				s.StartTime = &t
			case w.Interval == RealtimeInterval:
				s.MaxSample = 15
			}
			if !w.End.IsZero() {
				t := w.End
				s.EndTime = &t
			}
			specs = append(specs, s)
		}
		res, err := pm.Query(ctx, specs)
		if err != nil {
			if batch > 1 && tooLarge(err) {
				batch /= 2
				continue
			}
			return out, err
		}
		for _, b := range res {
			if em, ok := b.(*types.PerfEntityMetric); ok {
				out = append(out, trim(convert(em, pc, w.Interval), w))
			}
		}
		start = end
	}
	return out, nil
}

func tooLarge(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "maxquerymetrics") || strings.Contains(s, "exceed") || strings.Contains(s, "too many")
}

// convert returns samples sorted oldest first whatever order the server used.
func convert(em *types.PerfEntityMetric, pc *perfCounters, interval int32) Series {
	n := len(em.SampleInfo)
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return em.SampleInfo[order[a]].Timestamp.Before(em.SampleInfo[order[b]].Timestamp)
	})
	s := Series{Ref: em.Entity.Value, Interval: interval, Values: map[string][]float64{}}
	for _, i := range order {
		s.TS = append(s.TS, em.SampleInfo[i].Timestamp)
	}
	for _, v := range em.Value {
		iv, ok := v.(*types.PerfMetricIntSeries)
		if !ok || iv.Id.Instance != "" {
			continue
		}
		name := pc.byID[iv.Id.CounterId]
		vals := make([]float64, n)
		for k, i := range order {
			if i < len(iv.Value) {
				vals[k] = scale(name, iv.Value[i])
			} else {
				vals[k] = -1
			}
		}
		s.Values[name] = vals
	}
	return s
}

// trim drops samples outside the window: the start is exclusive in the
// vSphere API, and a sample at the start describes the period before it.
func trim(s Series, w Window) Series {
	keep := func(t time.Time) bool {
		return (w.Start.IsZero() || t.After(w.Start)) && (w.End.IsZero() || !t.After(w.End))
	}
	var idx []int
	for i, t := range s.TS {
		if keep(t) {
			idx = append(idx, i)
		}
	}
	if len(idx) == len(s.TS) {
		return s
	}
	out := Series{Ref: s.Ref, Interval: s.Interval, Values: map[string][]float64{}}
	for _, i := range idx {
		out.TS = append(out.TS, s.TS[i])
	}
	for k, v := range s.Values {
		for _, i := range idx {
			out.Values[k] = append(out.Values[k], v[i])
		}
	}
	return out
}

func scale(name string, v int64) float64 {
	if v < 0 {
		return -1
	}
	switch name {
	case CPUUsage, MemUsage:
		return float64(v) / 100
	}
	return float64(v)
}
