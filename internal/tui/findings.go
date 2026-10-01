package tui

import (
	"image/color"
	"strconv"

	"charm.land/lipgloss/v2"
	ltable "charm.land/lipgloss/v2/table"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
)

// detailH is the height of the panel describing the selected finding.
const detailH = 6

// findingsTable is the cursor and scroll state of the findings list; the
// rows themselves are read from the current result when drawn.
type findingsTable struct {
	n              int
	cursor, offset int
	height, width  int
}

func (t findingsTable) Cursor() int { return t.cursor }

func (t *findingsTable) SetCursor(i int) {
	t.cursor = min(max(i, 0), max(t.n-1, 0))
	t.scroll()
}

func (t *findingsTable) setHeight(h int) {
	t.height = max(h, 1)
	t.scroll()
}

func (t *findingsTable) setRows(n int) {
	t.n = n
	t.SetCursor(t.cursor)
}

func (t *findingsTable) scroll() {
	if t.cursor < t.offset {
		t.offset = t.cursor
	}
	if t.cursor >= t.offset+t.height {
		t.offset = t.cursor - t.height + 1
	}
	t.offset = max(min(t.offset, t.n-t.height), 0)
}

func (t *findingsTable) key(k string) {
	switch k {
	case "up", "k":
		t.SetCursor(t.cursor - 1)
	case "down", "j":
		t.SetCursor(t.cursor + 1)
	case "pgup", "ctrl+b", "ctrl+u":
		t.SetCursor(t.cursor - t.height)
	case "pgdown", "ctrl+f", "ctrl+d", "space":
		t.SetCursor(t.cursor + t.height)
	case "home", "g":
		t.SetCursor(0)
	case "end", "G":
		t.SetCursor(t.n - 1)
	}
}

func (m *Model) fillTable() {
	if m.src == nil || m.src.Result == nil {
		m.tbl.setRows(0)
		return
	}
	m.srch.matches = m.matchRows(m.srch.query)
	m.tbl.setRows(len(m.src.Result.Findings))
}

func (t theme) severity(s analysis.Severity) color.Color {
	switch s {
	case analysis.High:
		return t.bad
	case analysis.Medium:
		return t.warn
	case analysis.Low:
	}
	return t.info
}

func (m Model) findingsView() (string, []zone) {
	t := m.th
	if m.src.Result == nil {
		return t.mute.Render("⋯ Waiting for the first samples…"), nil
	}
	fs := m.src.Result.Findings
	if len(fs) == 0 {
		return t.ok.Render("✓ No findings: every VM looks rightsized so far."), nil
	}
	hit := map[int]bool{}
	for _, r := range m.srch.matches {
		hit[r] = true
	}
	tb := m.tbl
	end := min(tb.offset+tb.height, len(fs))
	sev := make([]analysis.Severity, 0, end-tb.offset)
	var zones []zone
	table := ltable.New().Border(lipgloss.RoundedBorder()).BorderStyle(t.faint).BorderColumn(false).Wrap(false).
		Headers("Priority", "VM", "Finding", "Current → Suggested", "Conf.")
	for i := tb.offset; i < end; i++ {
		f := fs[i]
		mark := "  "
		if i == tb.cursor {
			mark = "▌ "
		}
		vm := f.VM
		if hit[i] {
			vm = "» " + vm
		}
		table.Row(mark+"● "+f.Severity.String(), vm, string(f.Kind), f.Current+" → "+f.Suggested, f.Confidence)
		sev = append(sev, f.Severity)
		zones = append(zones, zone{"row:" + strconv.Itoa(i), 0, 3 + i - tb.offset, m.bodyW() - 1, 3 + i - tb.offset})
	}
	table.StyleFunc(func(row, col int) lipgloss.Style {
		s := lipgloss.NewStyle().Padding(0, 1)
		if row == ltable.HeaderRow {
			return s.Foreground(t.muted).Bold(true)
		}
		i := tb.offset + row
		selected := i == tb.cursor
		if selected {
			s = s.Background(t.sel).Foreground(t.text).Bold(true)
		}
		switch col {
		case 0:
			s = s.Foreground(t.severity(sev[row]))
		case 1:
			if hit[i] {
				s = s.Foreground(t.info)
			}
		case 3:
			if !selected {
				s = s.Foreground(t.muted)
			}
		}
		return s
	})
	table.Width(m.bodyW())
	var s stack
	s.add(table.String())
	s.zones = zones
	status := m.searchStatus()
	if status == "" {
		status = t.faint.Render(strconv.Itoa(tb.cursor+1) + " of " + strconv.Itoa(len(fs)))
	}
	s.add(status, m.findingDetail(fs[tb.cursor]))
	return s.String(), s.zones
}

func (m Model) findingDetail(f analysis.Finding) string {
	t := m.th
	w := m.bodyW()
	head := t.bold.Render(f.VM)
	if f.Cluster != "" {
		head += t.faint.Render(" · ") + t.mute.Render(f.Cluster)
	}
	head += t.faint.Render(" · ") + lipgloss.NewStyle().Foreground(t.severity(f.Severity)).Render(string(f.Kind))
	if f.Confidence != "" {
		head += t.faint.Render(" · " + f.Confidence + " confidence")
	}
	change := t.mute.Render(f.Current) + t.faint.Render("  →  ") + t.acc.Bold(true).Render(f.Suggested)
	detail := f.Detail
	if detail == "" {
		detail = "Press e to exclude this VM from recommendations with a justification."
	}
	detail = lipgloss.NewStyle().Width(w - 4).MaxHeight(detailH - 4).Foreground(t.muted).Render(detail)
	return t.panel(w, false).Height(detailH).Render(head + "\n" + change + "\n" + detail)
}
