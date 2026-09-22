package vc

import (
	"context"
	"fmt"
	"time"

	"github.com/vmware/govmomi/performance"
	"github.com/vmware/govmomi/vim25/types"
)

const (
	CPUUsage   = "cpu.usage.average"
	CPUReady   = "cpu.ready.summation"
	CPUMHz     = "cpu.usagemhz.average"
	MemUsage   = "mem.usage.average"
	MemConsume = "mem.consumed.average"
	DiskUsage  = "disk.usage.average"
	NetUsage   = "net.usage.average"
)

var (
	VMMetrics   = []string{CPUUsage, CPUReady, CPUMHz, MemUsage, MemConsume, DiskUsage, NetUsage}
	HostMetrics = []string{CPUMHz, MemConsume}
)

const realtimeInterval = 20
const batchSize = 64

type perfCounters struct {
	byName map[string]int32
	byID   map[int32]string
}

type Series struct {
	Ref    string
	TS     []time.Time
	Values map[string][]float64
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
	for _, n := range append(VMMetrics, HostMetrics...) {
		ci, ok := info[n]
		if !ok {
			return nil, fmt.Errorf("perf counter %s not available", n)
		}
		pc.byName[n] = ci.Key
		pc.byID[ci.Key] = n
	}
	c.perf = pc
	return pc, nil
}

// Sample returns 20-second realtime samples newer than since for each entity.
// vCenter keeps realtime data for about one hour, so callers must poll more
// often than that to avoid gaps.
func (c *Client) Sample(ctx context.Context, kind string, refs []string, metrics []string, since time.Time) ([]Series, error) {
	if err := c.Ensure(ctx); err != nil {
		return nil, err
	}
	pc, err := c.counters(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]types.PerfMetricId, 0, len(metrics))
	for _, m := range metrics {
		ids = append(ids, types.PerfMetricId{CounterId: pc.byName[m], Instance: ""})
	}
	pm := performance.NewManager(c.vim)
	var out []Series
	for start := 0; start < len(refs); start += batchSize {
		end := min(start+batchSize, len(refs))
		specs := make([]types.PerfQuerySpec, 0, end-start)
		for _, r := range refs[start:end] {
			s := types.PerfQuerySpec{
				Entity:     types.ManagedObjectReference{Type: kind, Value: r},
				MetricId:   ids,
				IntervalId: realtimeInterval,
				Format:     string(types.PerfFormatNormal),
			}
			if !since.IsZero() {
				t := since
				s.StartTime = &t
			} else {
				s.MaxSample = 15
			}
			specs = append(specs, s)
		}
		res, err := pm.Query(ctx, specs)
		if err != nil {
			return out, err
		}
		for _, b := range res {
			em, ok := b.(*types.PerfEntityMetric)
			if !ok {
				continue
			}
			s := Series{Ref: em.Entity.Value, Values: map[string][]float64{}}
			for _, si := range em.SampleInfo {
				s.TS = append(s.TS, si.Timestamp)
			}
			for _, v := range em.Value {
				iv, ok := v.(*types.PerfMetricIntSeries)
				if !ok || iv.Id.Instance != "" {
					continue
				}
				name := pc.byID[iv.Id.CounterId]
				vals := make([]float64, len(iv.Value))
				for i, x := range iv.Value {
					vals[i] = scale(name, x)
				}
				s.Values[name] = vals
			}
			out = append(out, s)
		}
	}
	return out, nil
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
