package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/MarcoColomb0/rightsizer/internal/engine"
)

const (
	padX    = 2
	headerH = 3
)

// zone is a clickable area of the screen. Its id is "key:<k>" to act as
// if k was pressed, or "row:<i>" to select a finding.
type zone struct {
	id             string
	x0, y0, x1, y1 int
}

func (z zone) shift(dx, dy int) zone {
	return zone{z.id, z.x0 + dx, z.y0 + dy, z.x1 + dx, z.y1 + dy}
}

func shift(zs []zone, dx, dy int) []zone {
	out := make([]zone, len(zs))
	for i, z := range zs {
		out[i] = z.shift(dx, dy)
	}
	return out
}

// stack joins blocks vertically and keeps track of their clickable zones.
type stack struct {
	parts []string
	zones []zone
	h     int
}

func (s *stack) add(blocks ...string) {
	for _, b := range blocks {
		s.parts = append(s.parts, b)
		s.h += lipgloss.Height(b)
	}
}

func (s *stack) addZoned(b string, zs []zone, dx int) {
	s.zones = append(s.zones, shift(zs, dx, s.h)...)
	s.add(b)
}

func (s *stack) String() string { return strings.Join(s.parts, "\n") }

func (m Model) bodyW() int { return max(m.w-2*padX, 40) }

func (m Model) bodyH() int { return max(m.h-headerH-m.footerH(), 5) }

func (m Model) footerH() int { return lipgloss.Height(m.footer()) }

func (m Model) View() tea.View {
	content, _ := m.compose()
	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = m.windowTitle()
	v.ProgressBar = m.osProgress()
	return v
}

// compose renders the whole screen with its overlays, and the zones that
// react to clicks.
func (m Model) compose() (string, []zone) {
	bw, bh := m.bodyW(), m.bodyH()
	head, hz := m.header()
	body, bz := m.body()
	body = lipgloss.NewStyle().Width(bw).Height(bh).MaxHeight(bh).MaxWidth(bw).Render(body)
	foot := m.footer()
	page := lipgloss.NewStyle().Padding(0, padX).Render(head + "\n\n" + body + "\n" + foot)
	zones := append(shift(hz, padX, 0), shift(bz, padX, headerH)...)
	layers := []*lipgloss.Layer{lipgloss.NewLayer(page)}
	if t := m.toast(); t != "" {
		tw, th := lipgloss.Width(t), lipgloss.Height(t)
		layers = append(layers, lipgloss.NewLayer(t).X(max(m.w-tw-padX, 0)).Y(max(headerH+bh-th, 0)).Z(1))
	}
	if d, dz := m.modal(); d != "" {
		dw, dh := lipgloss.Width(d), lipgloss.Height(d)
		x, y := max((m.w-dw)/2, 0), max((m.h-dh)/2, 0)
		layers = append(layers, lipgloss.NewLayer(d).X(x).Y(y).Z(2))
		zones = shift(dz, x, y) // a dialog takes every click
	}
	return lipgloss.NewCompositor(layers...).Render(), zones
}

func (m Model) body() (string, []zone) {
	switch m.scr {
	case scrLoading:
		return m.viewLoading(), nil
	case scrHome:
		return m.viewHome()
	case scrSetup:
		return m.viewSetup()
	case scrCert:
		return m.viewCert()
	case scrBusy:
		return m.viewBusy(), nil
	case scrSource:
		return m.viewSource()
	case scrResume:
		return m.viewResume()
	case scrSettings:
		return m.viewSettings()
	case scrUpdate:
		return m.viewUpdate()
	case scrExclude:
		return m.viewExclude()
	case scrExclusions:
		return m.viewExclusions(), nil
	case scrSizing:
		return m.viewSizingOpts()
	}
	return "", nil
}

func (m Model) header() (string, []zone) {
	t := m.th
	bw := m.bodyW()
	logo, tagline := t.gradient("◆ rightsizer", true), t.mute.Render("  vSphere rightsizing · read-only")
	var right []string
	if m.scr == scrSource && m.src != nil {
		right = append(right, t.bold.Render(clip(m.src.Config.Host, 40)), m.phasePill(m.src.Phase))
	}
	upd := -1
	if m.updateAvailable() && m.scr != scrUpdate {
		upd = len(right)
		right = append(right, t.pill("↑ "+m.opt.Latest+" available", t.warn))
	}
	if m.opt.Version != "" {
		right = append(right, t.faint.Render(m.opt.Version))
	}
	width := func(parts []string) int { return lipgloss.Width(strings.Join(parts, "  ")) }
	left := logo + tagline
	if lipgloss.Width(left)+width(right)+1 > bw {
		left = logo
	}
	if lipgloss.Width(left)+width(right)+1 > bw && m.opt.Version != "" {
		right = right[:len(right)-1]
	}
	r := strings.Join(right, "  ")
	gap := max(bw-lipgloss.Width(left)-lipgloss.Width(r), 1)
	var zones []zone
	if upd >= 0 {
		x := lipgloss.Width(left) + gap + width(right[:upd])
		if upd > 0 {
			x += 2
		}
		zones = append(zones, zone{"key:u", x, 0, x + lipgloss.Width(right[upd]) - 1, 0})
	}
	return left + strings.Repeat(" ", gap) + r + "\n" + t.rule(bw), zones
}

func (m Model) phasePill(p engine.Phase) string {
	switch p {
	case engine.Running:
		return m.th.pill("● collecting", m.th.accent)
	case engine.NeedPassword:
		return m.th.pill("‖ paused", m.th.warn)
	case engine.Done:
		return m.th.pill("✓ complete", m.th.good)
	}
	return ""
}

func (m Model) footer() string {
	h, k := m.help, m.keyMap()
	h.ShowAll = m.fullHelp && len(k.full) > 0
	h.SetWidth(m.bodyW())
	return m.th.faint.Render(strings.Repeat("─", m.bodyW())) + "\n" + h.View(k)
}

// toast shows the latest note or error in the bottom-right corner.
func (m Model) toast() string {
	t := m.th
	var icon, text string
	var c = t.good
	switch {
	case m.err != "":
		icon, text, c = "✗", m.err, t.bad
	case m.note != "":
		icon, text = "✓", m.note
	default:
		return ""
	}
	w := min(lipgloss.Width(text)+6, max(m.bodyW()*2/3, 30))
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(c).Padding(0, 1).Width(w).
		Render(lipgloss.NewStyle().Foreground(c).Bold(true).Render(icon) + " " + t.bold.UnsetBold().Render(text))
}

// modal renders the confirmation in progress as a dialog over the screen.
func (m Model) modal() (string, []zone) {
	if m.confirm == "" {
		return "", nil
	}
	t := m.th
	title, text, yes := "", "", "Yes"
	danger := false
	switch m.confirm {
	case "reboot":
		title, text, yes = "Restart the appliance now?", "This session closes and collection pauses until you log in again.", "Restart"
	case "finish":
		title, text, yes = "Finish the analysis now?", "Collection stops and the final report is built from the data so far.", "Finish"
	case "remove":
		title, text, yes, danger = "Remove this source?", "Its collected data and reports are deleted. This cannot be undone.", "Remove", true
	case "unexclude":
		title, text, yes = "Remove this exclusion?", "Its VMs get recommendations again.", "Remove"
	}
	c := t.warn
	if danger {
		c = t.bad
	}
	btns, bz := t.buttons(0, action{yes, "y"}, action{"Cancel", "n"})
	var s stack
	s.add(lipgloss.NewStyle().Foreground(c).Bold(true).Render("▲ "+title), "", t.mute.Width(54).Render(text), "")
	s.addZoned(btns, bz, 0)
	s.add(t.faint.Render("y confirm · n or esc cancel"))
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(c).Padding(1, 3).Render(s.String())
	return box, shift(s.zones, 4, 2)
}

func (m Model) windowTitle() string {
	if m.scr == scrSource && m.src != nil {
		return fmt.Sprintf("rightsizer · %s %.0f%%", m.src.Config.Host, m.srcFrac()*100)
	}
	return "rightsizer"
}

// osProgress drives the progress indicator that terminals such as Windows
// Terminal, Ghostty and iTerm2 show in the tab or dock.
func (m Model) osProgress() *tea.ProgressBar {
	switch {
	case m.scr == scrBusy:
		return tea.NewProgressBar(tea.ProgressBarIndeterminate, 0)
	case m.scr == scrSource && m.src != nil && m.src.Phase == engine.Running:
		return tea.NewProgressBar(tea.ProgressBarDefault, int(m.srcFrac()*100))
	case m.scr == scrSource && m.src != nil && m.src.Phase == engine.NeedPassword:
		return tea.NewProgressBar(tea.ProgressBarWarning, int(m.srcFrac()*100))
	}
	return nil
}

// layout sizes the components to the terminal.
func (m *Model) layout() {
	bw := m.bodyW()
	m.help.SetWidth(bw)
	h := max(m.bodyH()-m.sourceHeadH(), 3)
	m.vp.SetWidth(bw - 2)
	m.vp.SetHeight(h)
	m.tbl.width = bw
	m.tbl.setHeight(max(h-detailH-5, 3))
	m.exList.SetSize(bw, max(m.bodyH()-3, 4))
	m.pager.PerPage = max((m.bodyH()-3-m.homeExtrasH())/cardH, 1)
	m.pager.SetTotalPages(m.sources())
	m.pager.Page = m.sel / m.pager.PerPage
	m.exNote.SetWidth(min(bw-30, 60))
	m.syncViewport()
}

func (m Model) sources() int {
	if m.sum == nil {
		return 0
	}
	return len(m.sum.Sources)
}

func (m Model) mouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		return m.wheel(msg.Button == tea.MouseWheelUp)
	case tea.MouseClickMsg:
		if msg.Button != tea.MouseLeft {
			return m, nil
		}
		_, zones := m.compose()
		for _, z := range zones {
			if msg.X >= z.x0 && msg.X <= z.x1 && msg.Y >= z.y0 && msg.Y <= z.y1 {
				return m.click(z.id)
			}
		}
	}
	return m, nil
}

func (m Model) click(id string) (tea.Model, tea.Cmd) {
	kind, arg, _ := strings.Cut(id, ":")
	switch kind {
	case "key":
		return m.key(press(arg))
	case "submit":
		switch m.scr {
		case scrSetup:
			m.setFocus(fStart)
		case scrExclude:
			m.exFocus(exSave)
		case scrSizing:
			m.szFocus(soSave)
		case scrSettings:
			m.pwFocus = len(m.pw) - 1
			m.focusPw()
		default:
		}
		return m.key(press("enter"))
	case "row":
		if i, err := strconv.Atoi(arg); err == nil {
			m.tbl.SetCursor(i)
		}
	case "src":
		if i, err := strconv.Atoi(arg); err == nil {
			if i == m.sel {
				return m.openSource(i)
			}
			m.sel = i
		}
	}
	return m, nil
}

func (m Model) wheel(up bool) (tea.Model, tea.Cmd) {
	k := "down"
	if up {
		k = "up"
	}
	switch {
	case m.confirm != "":
	case m.scr == scrHome:
		return m.key(press(k))
	case m.scr == scrSource && m.tab == tabFindings:
		m.tbl.key(k)
	case m.scr == scrSource:
		if up {
			m.vp.ScrollUp(3)
		} else {
			m.vp.ScrollDown(3)
		}
	case m.scr == scrExclusions:
		if up {
			m.exList.CursorUp()
		} else {
			m.exList.CursorDown()
		}
	}
	return m, nil
}

// press builds the key event for a key name such as "y", "enter" or "up".
func press(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	}
	r := []rune(k)
	return tea.KeyPressMsg{Code: r[0], Text: k}
}

type action struct{ label, key string }

// buttons lays out actions side by side; the one at index focus is filled.
func (t theme) buttons(focus int, as ...action) (string, []zone) {
	var parts []string
	var zones []zone
	x := 0
	for i, a := range as {
		b := t.button(a.label, i == focus)
		w := lipgloss.Width(b)
		zones = append(zones, zone{"key:" + a.key, x, 0, x + w - 1, 2})
		parts = append(parts, b)
		x += w + 2
	}
	return strings.Join(joinGap(parts, "  "), ""), zones
}

func joinGap(parts []string, gap string) []string {
	if len(parts) < 2 {
		return parts
	}
	out := []string{parts[0]}
	for _, p := range parts[1:] {
		out = append(out, lipgloss.JoinHorizontal(lipgloss.Top, gap), p)
	}
	return []string{lipgloss.JoinHorizontal(lipgloss.Top, out...)}
}

// card frames a form or dialog and centres it in the body.
func (m Model) card(s *stack, focused bool) (string, []zone) {
	w := min(m.bodyW(), 100)
	py := 1
	if s.h+4 > m.bodyH() {
		py = 0
	}
	box := m.th.panel(w, focused).Padding(py, 3).Render(s.String())
	x := max((m.bodyW()-lipgloss.Width(box))/2, 0)
	return lipgloss.PlaceHorizontal(m.bodyW(), lipgloss.Center, box), shift(s.zones, x+4, 1+py)
}

func elapsed(d time.Duration) string {
	d = d.Truncate(100 * time.Millisecond)
	return fmt.Sprintf("%d:%04.1f", int(d.Minutes()), d.Seconds()-float64(int(d.Minutes())*60))
}

// keyMap feeds the help component for the current screen.
type keyMap struct {
	short []key.Binding
	full  [][]key.Binding
}

func (k keyMap) ShortHelp() []key.Binding  { return k.short }
func (k keyMap) FullHelp() [][]key.Binding { return k.full }

func bind(k, desc string) key.Binding {
	return key.NewBinding(key.WithKeys(k), key.WithHelp(k, desc))
}

func (m Model) keyMap() keyMap {
	more := bind("?", "more")
	if m.fullHelp {
		more = bind("?", "less")
	}
	if m.confirm != "" {
		return keyMap{short: []key.Binding{bind("y", "confirm"), bind("n", "cancel")}}
	}
	switch m.scr {
	case scrLoading:
		return keyMap{short: []key.Binding{bind("q", "quit")}}
	case scrHome:
		if m.sources() == 0 {
			return keyMap{short: []key.Binding{bind("a", "add vCenter"), bind("x", "exclusions"), bind("o", "sizing options"), bind("q", "quit")}}
		}
		nav := []key.Binding{bind("↑/↓", "select"), bind("←/→", "page"), bind("enter", "open"), bind("q", "quit")}
		act := []key.Binding{bind("a", "add vCenter"), bind("p", "combined PDF"), bind("z", "sizing PDF + data"), bind("o", "sizing options"), bind("x", "exclusions")}
		var sys []key.Binding
		if m.sum != nil && len(m.sum.Shares) > 0 {
			act = append(act, bind("s", "stop sharing"))
		}
		if m.opt.AdminSettings {
			sys = append(sys, bind("c", "change password"))
		}
		if m.updateAvailable() {
			sys = append(sys, bind("u", "upgrade"))
		}
		if m.canReboot() {
			sys = append(sys, bind("R", "restart"))
		}
		return keyMap{
			short: []key.Binding{bind("↑/↓", "select"), bind("enter", "open"), bind("a", "add vCenter"), bind("p", "PDF"), bind("x", "exclusions"), more, bind("q", "quit")},
			full:  [][]key.Binding{nav, act, append(sys, more)},
		}
	case scrSetup, scrExclude, scrSizing:
		return keyMap{short: []key.Binding{bind("↑/↓", "move"), bind("←/→", "change"), bind("enter", "next/save"), bind("esc", "cancel")}}
	case scrCert:
		return keyMap{short: []key.Binding{bind("y", "trust and start"), bind("n", "back")}}
	case scrResume:
		return keyMap{short: []key.Binding{bind("enter", "resume"), bind("esc", "back")}}
	case scrSettings:
		return keyMap{short: []key.Binding{bind("↑/↓", "move"), bind("enter", "next/save"), bind("esc", "cancel")}}
	case scrUpdate:
		return keyMap{short: []key.Binding{bind("y", "upgrade now"), bind("n", "later")}}
	case scrExclusions:
		if m.exList.SettingFilter() {
			return keyMap{short: []key.Binding{bind("enter", "apply filter"), bind("esc", "clear")}}
		}
		return keyMap{short: []key.Binding{bind("↑/↓", "select"), bind("/", "filter"), bind("a", "add pattern"), bind("d", "remove"), bind("esc", "back")}}
	case scrSource:
		return m.sourceKeys(more)
	case scrBusy:
	}
	return keyMap{}
}

func (m Model) sourceKeys(more key.Binding) keyMap {
	if m.srch.prompt {
		return keyMap{short: []key.Binding{bind("enter", "search"), bind("esc", "cancel")}}
	}
	nav := []key.Binding{bind("1-4", "tabs"), bind("tab", "next tab"), bind("esc", "back")}
	view := []key.Binding{}
	act := []key.Binding{}
	short := []key.Binding{bind("esc", "back"), bind("1-4", "tabs")}
	switch m.tab {
	case tabFindings:
		view = append(view, bind("↑/↓", "select"), bind("/", "search"), bind("?", "search back"), bind("n/N", "next/previous"))
		act = append(act, bind("e", "exclude"))
		short = append(short, bind("↑/↓", "select"), bind("/", "search"), bind("e", "exclude"), bind("p", "PDF"))
	case tabSizing:
		view = append(view, bind("↑/↓", "scroll"), bind("pgup/pgdn", "page"))
		act = append(act, bind("o", "sizing options"))
		short = append(short, bind("↑/↓", "scroll"), bind("p", "sizing PDF + data"), bind("o", "options"))
	default:
		view = append(view, bind("↑/↓", "scroll"), bind("pgup/pgdn", "page"))
		short = append(short, bind("↑/↓", "scroll"), bind("p", "PDF"))
	}
	if m.tab == tabSizing {
		act = append(act, bind("p", "sizing PDF + data"))
	} else {
		act = append(act, bind("p", "PDF report"))
	}
	if len(m.shares(m.cur)) > 0 {
		act = append(act, bind("s", "stop sharing"))
	}
	if m.src != nil {
		switch m.src.Phase {
		case engine.NeedPassword:
			act = append(act, bind("r", "resume"), bind("f", "finish now"))
			short = append(short, bind("r", "resume"))
		case engine.Running:
			act = append(act, bind("f", "finish now"))
		case engine.Done:
		}
	}
	act = append(act, bind("x", "remove"))
	if m.updateAvailable() {
		act = append(act, bind("u", "upgrade"))
	}
	if m.tab != tabFindings {
		short = append(short, more)
		nav = append(nav, more)
	}
	return keyMap{short: short, full: [][]key.Binding{nav, view, act}}
}
