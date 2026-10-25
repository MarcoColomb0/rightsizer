// Package logring keeps the latest log records in memory, so a diagnostic
// bundle can include them without reading the container's or the
// appliance's logs.
package logring

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
)

// Ring holds the last records written through its handler.
type Ring struct {
	mu    sync.Mutex
	lines []string
	next  int
	full  bool
}

func New(size int) *Ring { return &Ring{lines: make([]string, size)} }

// Default is the ring the engine writes to, nil when none is installed.
var Default *Ring

func (r *Ring) add(line string) {
	r.mu.Lock()
	r.lines[r.next] = line
	r.next = (r.next + 1) % len(r.lines)
	r.full = r.full || r.next == 0
	r.mu.Unlock()
}

// Lines returns the records kept, oldest first.
func (r *Ring) Lines() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		return append([]string(nil), r.lines[:r.next]...)
	}
	return append(append([]string(nil), r.lines[r.next:]...), r.lines[:r.next]...)
}

// Write keeps one record: slog's text handler writes each in one call.
func (r *Ring) Write(p []byte) (int, error) {
	r.add(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// Handler records at level and above into the ring and passes records to
// next at next's own level. Both see the same attributes and groups.
func (r *Ring) Handler(level slog.Level, next slog.Handler) slog.Handler {
	return fanout{slog.NewTextHandler(r, &slog.HandlerOptions{Level: level}), next}
}

type fanout []slog.Handler

func (f fanout) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range f {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (f fanout) Handle(ctx context.Context, rec slog.Record) error {
	var errs []error
	for _, h := range f {
		if h.Enabled(ctx, rec.Level) {
			errs = append(errs, h.Handle(ctx, rec.Clone()))
		}
	}
	return errors.Join(errs...)
}

func (f fanout) WithAttrs(as []slog.Attr) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithAttrs(as)
	}
	return out
}

func (f fanout) WithGroup(name string) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithGroup(name)
	}
	return out
}
