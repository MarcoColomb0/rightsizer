package tui

import (
	"fmt"
	"io"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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
		x:    analysis.Exclusion{VCenter: m.src.Config.Host, UUID: f.UUID, Name: f.VM, Path: f.Path},
		kind: f.Kind,
	}
	if f.Path != "" {
		m.ex.x.Name = ""
	}
	m.exNote.SetValue("")
	m.exFocus(exReason)
	m.ex.ret, m.scr, m.err = scrSource, scrExclude, ""
	return m, nil
}

func (m Model) openPattern() (tea.Model, tea.Cmd) {
	m.ex = excludeForm{pattern: true, ret: scrExclusions}
	m.exName.SetValue("")
	m.exNote.SetValue("")
	m.exFocus(exTarget)
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

func (m Model) keyExclude(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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
		return m.busyCmd("Saving the exclusion", "exclude", "", func() error { return b.Exclude(x) })
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

func (m Model) keyExclusions(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.confirm == "unexclude" {
		m.confirm = ""
		it, ok := m.exList.SelectedItem().(exclusionItem)
		if ok && (k.String() == "y" || k.String() == "Y") {
			id, b := it.ID, m.b
			return m.busyCmd("Removing the exclusion", "unexclude", "", func() error { return b.Unexclude(id) })
		}
		return m, nil
	}
	if !m.exList.SettingFilter() {
		switch k.String() {
		case "esc", "q":
			if m.exList.IsFiltered() {
				m.exList.ResetFilter()
				return m, nil
			}
			m.scr = scrHome
			return m, nil
		case "a":
			return m.openPattern()
		case "d", "delete", "backspace":
			if m.exList.SelectedItem() != nil {
				m.confirm = "unexclude"
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.exList, cmd = m.exList.Update(k)
	return m, cmd
}

func (m Model) viewExclude() (string, []zone) {
	t := m.th
	var s stack
	title := "Exclude from recommendations"
	if m.ex.pattern {
		title = "Exclude VMs by name pattern"
	}
	s.add(t.h1.Render(title), t.mute.Render("Excluded VMs count at their provisioned size and are listed with this justification in every report."), "")
	f := m.ex.focus
	switch {
	case m.ex.pattern:
		s.add(m.field(f == exTarget, "Name pattern", m.exName.View()), m.hint("* matches any text, e.g. citrix-* or *-vendor-??; applies to every vCenter"))
	case m.ex.x.Path != "":
		s.add(m.field(false, "Disk", t.bold.Render(m.ex.x.Path)))
	default:
		s.add(m.field(false, "VM", t.bold.Render(m.ex.x.Name)), m.hint(m.ex.x.VCenter+"; follows the VM if it is renamed"))
	}
	s.add("")
	s.add(m.field(f == exReason, "Reason", t.carousel(analysis.Reasons, m.ex.reason, f == exReason)))
	scopes := []string{"All recommendations"}
	if !m.ex.pattern && m.ex.kind != "" {
		scopes = append(scopes, "Only "+string(m.ex.kind))
	}
	s.add(m.field(f == exScope, "Scope", t.segmented(scopes, m.ex.scope, f == exScope)))
	labels := make([]string, len(reviews))
	for i, r := range reviews {
		labels[i] = r.label
	}
	s.add(m.field(f == exReview, "Review", t.segmented(labels, m.ex.review, f == exReview)))
	s.add("")
	s.add(lipgloss.JoinHorizontal(lipgloss.Top, m.field(f == exNote, "Note", ""), m.exNote.View()))
	s.add(m.hint(fmt.Sprintf("%d/500 · ctrl+j new line", len([]rune(m.exNote.Value())))), "")
	focus := -1
	if f == exSave {
		focus = 0
	}
	btn, bz := t.buttons(focus, action{"Save exclusion", "submit"})
	s.addZoned(btn, submitZones(bz), 0)
	return m.card(&s, true)
}

func (m Model) viewExclusions() string {
	t := m.th
	head := t.h1.Render("Exclusions") + "\n" +
		t.mute.Render("Kept across upgrades and applied to every current and future analysis.") + "\n"
	if len(m.excl) == 0 {
		return head + "\n" + t.callout("None yet. Press e on a finding, or a to add a name pattern.", t.info, m.bodyW())
	}
	return head + "\n" + m.exList.View()
}

type exclusionItem struct{ analysis.Exclusion }

func (i exclusionItem) FilterValue() string {
	return strings.Join([]string{i.Target(), i.VCenter, i.Reason, i.Note}, " ")
}

func exclusionItems(xs []analysis.Exclusion) []list.Item {
	out := make([]list.Item, len(xs))
	for i, x := range xs {
		out[i] = exclusionItem{x}
	}
	return out
}

// exclusionDelegate draws an exclusion over two lines.
type exclusionDelegate struct{ t theme }

func (d exclusionDelegate) Height() int                         { return 2 }
func (d exclusionDelegate) Spacing() int                        { return 1 }
func (d exclusionDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (d exclusionDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	x, ok := item.(exclusionItem)
	if !ok {
		return
	}
	t := d.t
	bar, name := "  ", t.bold
	if index == m.Index() {
		bar, name = t.acc.Render("┃ "), t.acc.Bold(true)
	}
	where := x.VCenter
	if where == "" {
		where = "all vCenters"
	}
	var review string
	switch {
	case x.Overdue(time.Now()):
		review = "  " + t.pill("review overdue since "+x.ReviewBy.Format("2006-01-02"), t.bad)
	case !x.ReviewBy.IsZero():
		review = t.faint.Render("  review by " + x.ReviewBy.Format("2006-01-02"))
	}
	width := m.Width() - 2
	l1 := name.Render(clip(x.Target(), width/2)) + t.mute.Render("  "+where+" · "+x.Scope()) + review
	l2 := t.wrn.Render(x.Reason) + t.faint.Render(": ") + clip(x.Note, width-len(x.Reason)-24) + t.faint.Render(fmt.Sprintf("  since %s", x.Created.Format("2006-01-02")))
	fmt.Fprint(w, bar+l1+"\n"+bar+l2)
}

func newExclusionList() list.Model {
	l := list.New(nil, exclusionDelegate{}, 80, 20)
	l.SetShowTitle(false)
	l.SetShowHelp(false)
	l.SetStatusBarItemName("exclusion", "exclusions")
	l.DisableQuitKeybindings()
	l.FilterInput.Prompt = "/ "
	return l
}

func styleExclusionList(l *list.Model, t theme) {
	st := list.DefaultStyles(t.dark)
	st.StatusBar = st.StatusBar.Foreground(t.muted)
	st.StatusEmpty = t.faint
	st.StatusBarActiveFilter = t.acc
	st.StatusBarFilterCount = t.faint
	st.NoItems = t.mute
	st.ActivePaginationDot = t.acc
	st.InactivePaginationDot = t.faint
	st.DividerDot = t.faint
	st.Filter.Focused.Prompt = t.acc.Bold(true)
	st.Filter.Blurred.Prompt = t.faint
	st.Filter.Cursor.Color = t.accent
	l.Styles = st
	l.FilterInput.SetStyles(st.Filter)
	l.Paginator.ActiveDot = t.acc.Render("●")
	l.Paginator.InactiveDot = t.faint.Render("●")
	l.SetDelegate(exclusionDelegate{t})
}
