// Package logring keeps the latest log records in memory, so a diagnostic
// bundle can include them without reading the container's or the
// appliance's logs.
package logring

import (
	"bytes"
	"context"
	"log/slog"
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

// Handler records at level and above into the ring and passes records to
// next at next's own level.
func (r *Ring) Handler(level slog.Level, next slog.Handler) slog.Handler {
	return &tee{ring: r, level: level, next: next}
}

type tee struct {
	ring  *Ring
	level slog.Level
	next  slog.Handler
	attrs []slog.Attr
	group string
}

func (t *tee) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= t.level || t.next.Enabled(ctx, l)
}

func (t *tee) Handle(ctx context.Context, rec slog.Record) error {
	if rec.Level >= t.level {
		var buf bytes.Buffer
		h := slog.Handler(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		if len(t.attrs) > 0 {
			h = h.WithAttrs(t.attrs)
		}
		if t.group != "" {
			h = h.WithGroup(t.group)
		}
		if err := h.Handle(ctx, rec); err == nil {
			t.ring.add(string(bytes.TrimRight(buf.Bytes(), "\n")))
		}
	}
	if t.next.Enabled(ctx, rec.Level) {
		return t.next.Handle(ctx, rec)
	}
	return nil
}

func (t *tee) WithAttrs(as []slog.Attr) slog.Handler {
	c := *t
	c.attrs = append(append([]slog.Attr(nil), t.attrs...), as...)
	c.next = t.next.WithAttrs(as)
	return &c
}

func (t *tee) WithGroup(name string) slog.Handler {
	c := *t
	c.group = name
	c.next = t.next.WithGroup(name)
	return &c
}
