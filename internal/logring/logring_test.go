package logring

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestRing(t *testing.T) {
	r := New(3)
	var out bytes.Buffer
	l := slog.New(r.Handler(slog.LevelDebug, slog.NewTextHandler(&out, nil)))
	l.Debug("only in the ring")
	for i := range 4 {
		l.With("source", "abcd").Info("poll", "n", i)
	}
	lines := r.Lines()
	if len(lines) != 3 || !strings.Contains(lines[0], "n=1") || !strings.Contains(lines[2], "source=abcd n=3") {
		t.Fatalf("ring %q", lines)
	}
	if strings.Contains(out.String(), "only in the ring") || strings.Count(out.String(), "poll") != 4 {
		t.Fatalf("stderr %q", out.String())
	}
	var nilRing *Ring
	if nilRing.Lines() != nil {
		t.Fatal("no ring, no lines")
	}
}
