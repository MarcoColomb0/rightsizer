package version

import (
	"regexp"
	"strconv"
	"strings"
)

// Newer reports whether latest is a higher semantic version than current.
// Development builds and malformed versions never trigger an update, and a
// pre-release is never offered; a pre-release installed is older than its
// final release.
func Newer(latest, current string) bool {
	l, ok1 := parse(latest)
	c, ok2 := parse(current)
	if !ok1 || !ok2 || l.pre() {
		return false
	}
	return compare(l, c) > 0
}

// NewerAny is Newer for the pre-release channel: pre-releases are offered
// too, in order (alpha < beta < rc < the final release).
func NewerAny(latest, current string) bool {
	l, ok1 := parse(latest)
	c, ok2 := parse(current)
	return ok1 && ok2 && compare(l, c) > 0
}

// IsPre reports a pre-release version such as v1.4.0-rc.1.
func IsPre(v string) bool {
	p, ok := parse(v)
	return ok && p.pre()
}

// Valid reports a release or pre-release version.
func Valid(v string) bool {
	_, ok := parse(v)
	return ok
}

type parsed struct {
	core [3]int
	// kind is 0 for alpha, 1 for beta, 2 for rc and 3 for a final release.
	kind, n int
}

func (p parsed) pre() bool { return p.kind < 3 }

// preSuffix is a release candidate, beta or alpha such as -rc.1; other
// suffixes, like git describe's -12-gabcdef, are development builds. Same
// rule as install.sh and the appliance host.
var preSuffix = regexp.MustCompile(`^(alpha|beta|rc)\.?([0-9]*)$`)

func parse(v string) (parsed, bool) {
	core, suffix, hasSuffix := strings.Cut(strings.TrimPrefix(strings.TrimSpace(v), "v"), "-")
	out := parsed{kind: 3}
	if hasSuffix {
		m := preSuffix.FindStringSubmatch(suffix)
		if m == nil {
			return out, false
		}
		out.kind = map[string]int{"alpha": 0, "beta": 1, "rc": 2}[m[1]]
		out.n, _ = strconv.Atoi(m[2])
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || strings.ContainsAny(p, "+-") {
			return out, false
		}
		out.core[i] = n
	}
	return out, true
}

func compare(a, b parsed) int {
	for i := range a.core {
		if a.core[i] != b.core[i] {
			return a.core[i] - b.core[i]
		}
	}
	if a.kind != b.kind {
		return a.kind - b.kind
	}
	return a.n - b.n
}
