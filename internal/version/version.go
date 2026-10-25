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
	l, lpre, ok1 := parse(latest)
	c, cpre, ok2 := parse(current)
	if !ok1 || !ok2 || lpre {
		return false
	}
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return cpre
}

// Diagnostics reports whether a version offers diagnostic bundles: only
// pre-releases, which are built to be tested and reported on.
func Diagnostics(v string) bool { return IsPre(v) }

// IsPre reports a pre-release version such as v1.4.0-rc.1.
func IsPre(v string) bool {
	_, pre, ok := parse(v)
	return ok && pre
}

// preSuffix is a release candidate, beta or alpha such as -rc.1; other
// suffixes, like git describe's -12-gabcdef, are development builds. Same
// rule as .github/tag-kind.sh and install.sh.
var preSuffix = regexp.MustCompile(`^(rc|beta|alpha)\.?[0-9]*$`)

func parse(v string) ([3]int, bool, bool) {
	core, suffix, pre := strings.Cut(strings.TrimSpace(v), "-")
	if pre && !preSuffix.MatchString(suffix) {
		return [3]int{}, false, false
	}
	out, ok := parseCore(core)
	return out, pre, ok
}

func parseCore(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		return out, false
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
