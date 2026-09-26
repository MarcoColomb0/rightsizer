package analysis

import (
	"slices"
	"time"
)

func sortPoints(p []Point) {
	slices.SortFunc(p, func(a, b Point) int { return a.T.Compare(b.T) })
}

func Downsample(p []Point, n int) []Point {
	if len(p) <= n || n <= 0 {
		return p
	}
	out := make([]Point, 0, n)
	step := float64(len(p)) / float64(n)
	for i := range n {
		lo, hi := int(float64(i)*step), int(float64(i+1)*step)
		var cpu, mem float64
		for _, x := range p[lo:hi] {
			cpu = max(cpu, x.CPUMHz)
			mem = max(mem, x.MemB)
		}
		out = append(out, Point{T: p[lo].T, CPUMHz: cpu, MemB: mem})
	}
	return out
}

func hoursBetween(a, b time.Time) float64 {
	if a.IsZero() || b.IsZero() {
		return 0
	}
	return b.Sub(a).Hours()
}
