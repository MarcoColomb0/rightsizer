package ontap

import (
	"context"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"time"
)

// Sample is one performance record: rates are per second over Interval
// seconds, latency is in microseconds, CPU is a percentage.
type Sample struct {
	T          time.Time
	Interval   int32
	ReadIOPS   float64
	WriteIOPS  float64
	OtherIOPS  float64
	ReadBps    float64
	WriteBps   float64
	ReadLatUs  float64
	WriteLatUs float64
	TotalLatUs float64
	CPU        float64
}

// Object is what metrics are read for.
type Object int

const (
	ClusterObject Object = iota
	NodeObject
	VolumeObject
)

func (o Object) path(uuid string) string {
	switch o {
	case NodeObject:
		return "/api/cluster/nodes/" + url.PathEscape(uuid) + "/metrics"
	case VolumeObject:
		return "/api/storage/volumes/" + url.PathEscape(uuid) + "/metrics"
	case ClusterObject:
	}
	return "/api/cluster/metrics"
}

func (o Object) fields() []string {
	if o == NodeObject {
		return []string{"timestamp", "duration", "status", "processor_utilization"}
	}
	return []string{"timestamp", "duration", "status", "iops", "throughput", "latency"}
}

// Interval names a range of ONTAP's performance archive.
type Interval string

const (
	// Hour holds 15-second samples.
	Hour Interval = "1h"
	// Day holds 5-minute samples.
	Day Interval = "1d"
	// Week holds 30-minute samples.
	Week Interval = "1w"
	// Month holds 2-hour samples.
	Month Interval = "1m"
)

// Span is how far back an interval reaches, and Step its sample length.
func (i Interval) Span() time.Duration {
	switch i {
	case Day:
		return 24 * time.Hour
	case Week:
		return 7 * 24 * time.Hour
	case Month:
		return 30 * 24 * time.Hour
	case Hour:
	}
	return time.Hour
}

func (i Interval) Step() int32 {
	switch i {
	case Day:
		return 300
	case Week:
		return 1800
	case Month:
		return 7200
	case Hour:
	}
	return 15
}

// Window is the part of an interval read during the history import.
type Window struct {
	Interval   Interval
	Start, End time.Time
}

// HistoryPlan reads each interval only for the period it alone covers,
// finest first, back to now - back.
func HistoryPlan(now time.Time, back time.Duration) []Window {
	var out []Window
	end := now
	for _, iv := range []Interval{Day, Week, Month} {
		start := now.Add(-min(iv.Span(), back))
		if !start.Before(end) {
			continue
		}
		out = append(out, Window{Interval: iv, Start: start, End: end})
		end = start
		if iv.Span() >= back {
			break
		}
	}
	return out
}

type rawRate struct {
	Read  float64 `json:"read"`
	Write float64 `json:"write"`
	Other float64 `json:"other"`
	Total float64 `json:"total"`
}

type rawMetric struct {
	Timestamp  time.Time `json:"timestamp"`
	Duration   string    `json:"duration"`
	Status     string    `json:"status"`
	IOPS       rawRate   `json:"iops"`
	Throughput rawRate   `json:"throughput"`
	Latency    rawRate   `json:"latency"`
	CPU        *float64  `json:"processor_utilization"`
}

var isoDuration = regexp.MustCompile(`^P(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

// seconds parses ONTAP's ISO-8601 durations such as PT15S, PT5M or P1D.
func seconds(d string) int32 {
	m := isoDuration.FindStringSubmatch(d)
	if m == nil {
		return 0
	}
	var s int64
	for i, mul := range []int64{86400, 3600, 60, 1} {
		if m[i+1] != "" {
			n, _ := strconv.ParseInt(m[i+1], 10, 32)
			s += n * mul
		}
	}
	return int32(min(s, 1<<30))
}

// Metrics reads one interval of an object's performance archive, oldest
// first. Samples ONTAP flags as incomplete are left out.
func (c *Client) Metrics(ctx context.Context, o Object, uuid string, iv Interval) ([]Sample, error) {
	raw, err := list[rawMetric](ctx, c, o.path(uuid), o.fields(), url.Values{"interval": {string(iv)}})
	if err != nil {
		return nil, err
	}
	out := make([]Sample, 0, len(raw))
	for _, r := range raw {
		if r.Timestamp.IsZero() || (r.Status != "" && r.Status != "ok") {
			continue
		}
		s := Sample{
			T: r.Timestamp, Interval: seconds(r.Duration),
			ReadIOPS: r.IOPS.Read, WriteIOPS: r.IOPS.Write, OtherIOPS: r.IOPS.Other,
			ReadBps: r.Throughput.Read, WriteBps: r.Throughput.Write,
			ReadLatUs: r.Latency.Read, WriteLatUs: r.Latency.Write, TotalLatUs: r.Latency.Total, CPU: -1,
		}
		if s.Interval == 0 {
			s.Interval = iv.Step()
		}
		if r.CPU != nil {
			s.CPU = *r.CPU
		}
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b Sample) int { return a.T.Compare(b.T) })
	return out, nil
}
