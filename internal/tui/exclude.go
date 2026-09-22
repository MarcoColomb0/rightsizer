package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
)

type exclusionsMsg struct {
	xs  []analysis.Exclusion
	err error
}

const (
	exTarget = iota
	exReason
	exScope
	exReview
	exNote
	exSave
	exCount
)

var reviews = []struct {
	label  string
	months int
}{{"No review date", 0}, {"In 3 months", 3}, {"In 6 months", 6}, {"In 12 months", 12}}

type excludeForm struct {
	ret     screen
	x       analysis.Exclusion
	kind    analysis.Kind
	pattern bool
	focus   int
	reason  int
	scope   int
	review  int
}

func (m Model) fetchExclusions() tea.Cmd {
	b := m.b
	return func() tea.Msg {
		xs, err := b.Exclusions()
		return exclusionsMsg{xs, err}
	}
}

// openExclude starts the form for the finding under the cursor.
func (m Model) openExclude() (tea.Model, tea.Cmd) {
	if m.src == nil || m.src.Result == nil {
		return m, nil
	}
	i := m.tbl.Cursor()
	if i < 0 || i >= len(m.src.Result.Findings) {
		return m, nil
	}
	f := m.src.Result.Findings[i]
	if f.UUID == "" && f.Path == "" {
		m.err = "This finding covers several VMs; exclude them one by one or with a name pattern (x on the home screen)."
		return m, nil
	}
	m.ex = excludeForm{
		x:     analysis.Exclusion{VCenter: m.src.Config.Host, UUID: f.UUID, Name: f.VM, Path: f.Path},
		kind:  f.Kind,
		focus: exReason,
	}
	if f.Path != "" {
		m.ex.x.Name = ""
	}
	m.exNote.SetValue("")
	m.ex.ret, m.scr, m.err = scrSource, scrExclude, ""
	return m, nil
}

func (m Model) openPattern() (tea.Model, tea.Cmd) {
	m.ex = excludeForm{pattern: true, focus: exTarget, ret: scrExclusions}
	m.exName.SetValue("")
	m.exNote.SetValue("")
	m.exName.Focus()
	m.scr, m.err = scrExclude, ""
	return m, nil
}

func (m *Model) exFocus(f int) {
	if !m.ex.pattern && f == exTarget {
		f = exReason
	}
	m.ex.focus = f
	m.exName.Blur()
	m.exNote.Blur()
	switch f {
	case exTarget:
		m.exName.Focus()
	case exNote:
		m.exNote.Focus()
	}
}

func (m Model) keyExclude(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.scr, m.err = m.ex.ret, ""
		return m, nil
	case "tab", "down":
		m.exFocus((m.ex.focus + 1) % exCount)
		return m, nil
	case "shift+tab", "up":
		f := (m.ex.focus + exCount - 1) % exCount
		if !m.ex.pattern && f == exTarget {
			f = exSave
		}
		m.exFocus(f)
		return m, nil
	case "left", "right":
		d := 1
		if k.String() == "left" {
			d = -1
		}
		switch m.ex.focus {
		case exReason:
			m.ex.reason = (m.ex.reason + d + len(analysis.Reasons)) % len(analysis.Reasons)
			return m, nil
		case exScope:
			if !m.ex.pattern && m.ex.kind != "" {
				m.ex.scope = 1 - m.ex.scope
			}
			return m, nil
		case exReview:
			m.ex.review = (m.ex.review + d + len(reviews)) % len(reviews)
			return m, nil
		}
	case "enter":
		if m.ex.focus != exSave && m.ex.focus != exNote {
			m.exFocus(m.ex.focus + 1)
			return m, nil
		}
		x := m.ex.x
		if m.ex.pattern {
			x.Name = strings.TrimSpace(m.exName.Value())
		}
		x.Reason = analysis.Reasons[m.ex.reason]
		x.Note = m.exNote.Value()
		if m.ex.scope == 1 {
			x.Kinds = []analysis.Kind{m.ex.kind}
		}
		if n := reviews[m.ex.review].months; n > 0 {
			x.ReviewBy = time.Now().AddDate(0, n, 0)
		}
		if err := x.Validate(); err != nil {
			m.err = err.Error()
			switch {
			case strings.TrimSpace(x.Note) == "":
				m.exFocus(exNote)
			case m.ex.pattern:
				m.exFocus(exTarget)
			}
			return m, nil
		}
		b := m.b
		return m.busyCmd("Saving the exclusion…", "exclude", "", func() error { return b.Exclude(x) })
	}
	var cmd tea.Cmd
	switch m.ex.focus {
	case exTarget:
		m.exName, cmd = m.exName.Update(k)
	case exNote:
		m.exNote, cmd = m.exNote.Update(k)
	}
	return m, cmd
}

func (m Model) keyExclusions(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirm == "unexclude" {
		m.confirm = ""
		if k.String() == "y" || k.String() == "Y" {
			id := m.excl[m.exSel].ID
			b := m.b
			return m.busyCmd("Removing the exclusion…", "unexclude", "", func() error { return b.Unexclude(id) })
		}
		return m, nil
	}
	switch k.String() {
	case "esc", "q":
		m.scr = scrHome
	case "up", "k":
		m.exSel = max(m.exSel-1, 0)
	case "down", "j":
		m.exSel = min(m.exSel+1, max(len(m.excl)-1, 0))
	case "a":
		return m.openPattern()
	case "d", "delete", "backspace":
		if len(m.excl) > 0 {
			m.confirm = "unexclude"
		}
	}
	return m, nil
}

func (m Model) viewExclude() string {
	var b strings.Builder
	title := "Exclude from recommendations"
	if m.ex.pattern {
		title = "Exclude VMs by name pattern"
	}
	b.WriteString(sBold.Render(title) + "\n")
	b.WriteString(sMuted.Render("Excluded VMs count at their provisioned size and are listed with this justification in every report.") + "\n\n")
	row := func(f int, label, val string) {
		l := sLabel.Render(label)
		if m.ex.focus == f {
			l = sFocus.Width(16).Render("› " + label)
		}
		b.WriteString(l + val + "\n")
	}
	switch {
	case m.ex.pattern:
		row(exTarget, "Name pattern", m.exName.View())
		b.WriteString(sLabel.Render("") + sMuted.Render("* matches any text, e.g. citrix-* or *-vendor-??; applies to every vCenter") + "\n")
	case m.ex.x.Path != "":
		row(exTarget, "Disk", m.ex.x.Path)
	default:
		row(exTarget, "VM", m.ex.x.Name+sMuted.Render("  ("+m.ex.x.VCenter+"; follows the VM if it is renamed)"))
	}
	row(exReason, "Reason", choice(analysis.Reasons[m.ex.reason], m.ex.focus == exReason))
	scope := "All recommendations"
	if m.ex.scope == 1 {
		scope = "Only " + string(m.ex.kind)
	}
	row(exScope, "Scope", choice(scope, m.ex.focus == exScope))
	row(exReview, "Review", choice(reviews[m.ex.review].label, m.ex.focus == exReview))
	row(exNote, "Note", m.exNote.View())
	b.WriteString("\n")
	btn := sBox
	if m.ex.focus == exSave {
		btn = btn.BorderForeground(accent).Foreground(accent).Bold(true)
	}
	b.WriteString(btn.Render("Save exclusion") + "\n\n")
	b.WriteString(keys("↑/↓", "move", "←/→", "change", "enter", "next/save", "esc", "cancel"))
	return b.String()
}

func (m Model) viewExclusions() string {
	var b strings.Builder
	b.WriteString(sBold.Render("Exclusions") + "\n")
	b.WriteString(sMuted.Render("Kept across upgrades and applied to every current and future analysis.") + "\n\n")
	if len(m.excl) == 0 {
		b.WriteString(sMuted.Render("None yet. Press e on a finding, or a to add a name pattern.") + "\n\n")
	}
	now := time.Now()
	for i, x := range m.excl {
		cursor := "  "
		if i == m.exSel {
			cursor = sAccent.Render("▸ ")
		}
		where := x.VCenter
		if where == "" {
			where = "all vCenters"
		}
		review := ""
		switch {
		case x.Overdue(now):
			review = sBad.Render("  review overdue since " + x.ReviewBy.Format("2006-01-02"))
		case !x.ReviewBy.IsZero():
			review = sMuted.Render("  review by " + x.ReviewBy.Format("2006-01-02"))
		}
		b.WriteString(cursor + sBold.Render(x.Target()) + sMuted.Render("  "+where+" · "+x.Scope()) + review + "\n")
		b.WriteString("    " + x.Reason + ": " + x.Note + sMuted.Render(fmt.Sprintf("  (since %s)", x.Created.Format("2006-01-02"))) + "\n")
	}
	b.WriteString("\n")
	if m.confirm == "unexclude" {
		b.WriteString(sWarn.Render("Remove this exclusion? Its VMs get recommendations again. [y/N]"))
	} else {
		b.WriteString(keys("↑/↓", "select", "a", "add pattern", "d", "remove", "esc", "back"))
	}
	return b.String()
}
