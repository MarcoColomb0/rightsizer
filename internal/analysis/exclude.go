package analysis

import (
	"errors"
	"path"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Exclusion keeps a VM, a group of VMs or an orphaned disk out of the
// recommendations, with the reason recorded for the report.
type Exclusion struct {
	ID       string
	VCenter  string // empty: every vCenter
	UUID     string // VM instance UUID
	Name     string // VM name or glob pattern
	Path     string // orphaned disk path
	Kinds    []Kind // empty: every recommendation
	Reason   string
	Note     string
	Created  time.Time
	ReviewBy time.Time
}

var Reasons = []string{"Vendor requirement", "Licensing", "Business critical", "Planned change", "Other"}

func (x Exclusion) Validate() error {
	switch {
	case x.UUID == "" && x.Name == "" && x.Path == "":
		return errors.New("choose a VM, a name pattern or a disk")
	case strings.TrimSpace(x.Note) == "":
		return errors.New("a note explaining the exclusion is required")
	case utf8.RuneCountInString(x.Note) > 500 || len(x.Name) > 256 || len(x.Path) > 1024:
		return errors.New("text too long")
	}
	if x.Name != "" {
		if _, err := path.Match(x.Name, ""); err != nil {
			return errors.New("invalid name pattern")
		}
	}
	return nil
}

func (x Exclusion) Pattern() bool { return strings.ContainsAny(x.Name, "*?[") }

func (x Exclusion) Overdue(now time.Time) bool { return !x.ReviewBy.IsZero() && now.After(x.ReviewBy) }

func (x Exclusion) Scope() string {
	if len(x.Kinds) == 0 {
		return "All recommendations"
	}
	parts := make([]string, len(x.Kinds))
	for i, k := range x.Kinds {
		parts[i] = string(k)
	}
	return strings.Join(parts, ", ")
}

func (x Exclusion) Target() string {
	switch {
	case x.Path != "":
		return x.Path
	case x.Pattern():
		return x.Name + " (pattern)"
	}
	return x.Name
}

func (x Exclusion) covers(k Kind) bool {
	if len(x.Kinds) == 0 {
		return true
	}
	return slices.Contains(x.Kinds, k)
}

func (x Exclusion) matchesVM(uuid, name string) bool {
	if x.Path != "" {
		return false
	}
	if x.UUID != "" {
		return uuid != "" && strings.EqualFold(x.UUID, uuid)
	}
	ok, _ := path.Match(strings.ToLower(x.Name), strings.ToLower(name))
	return ok
}

// Excluded is an exclusion applied to one analysis.
type Excluded struct {
	Exclusion
	Matched []string
}

type exclusions []Exclusion

func (xs exclusions) forVM(uuid, name string) []Exclusion {
	var out []Exclusion
	for _, x := range xs {
		if x.matchesVM(uuid, name) {
			out = append(out, x)
		}
	}
	return out
}

func (xs exclusions) forDisk(p string) (Exclusion, bool) {
	for _, x := range xs {
		if x.Path != "" && x.Path == p {
			return x, true
		}
	}
	return Exclusion{}, false
}

func coversAny(xs []Exclusion, k Kind) bool {
	for _, x := range xs {
		if x.covers(k) {
			return true
		}
	}
	return false
}
