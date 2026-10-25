package tui

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MarcoColomb0/rightsizer/internal/engine"
)

// extendDays are the extensions offered, in days.
var extendDays = []int{1, 3, 7, 14}

const (
	xDays = iota
	xPass
	xSave
)

// extendForm is the state of the "collect for longer" form.
type extendForm struct {
	choices []int
	pick    int
	focus   int
}

func (m Model) extendEnd(days int) time.Time {
	extra := time.Duration(days) * 24 * time.Hour
	if m.src.Phase == engine.Done {
		return time.Now().Add(extra)
	}
	return m.src.Ends.Add(extra)
}

// extendNeedsPassword reports whether reopening must ask for the vCenter
// password: only a finished analysis reconnects, and stored credentials are
// used when the vault holds them.
func (m Model) extendNeedsPassword() bool {
	if m.src == nil || m.src.Phase != engine.Done {
		return false
	}
	return m.sum == nil || !m.sum.Vault.Enabled || m.sum.Vault.Locked
}

func (m Model) openExtend() (tea.Model, tea.Cmd) {
	if m.src == nil {
		return m, nil
	}
	var choices []int
	for _, d := range extendDays {
		if m.extendEnd(d).Sub(m.src.Started) <= engine.MaxTotal {
			choices = append(choices, d)
		}
	}
	if len(choices) == 0 {
		m.err = fmt.Sprintf("This analysis already covers the most it can: %d days in total.", int(engine.MaxTotal.Hours()/24))
		return m, nil
	}
	m.ext = extendForm{choices: choices, pick: min(2, len(choices)-1)}
	m.pass.SetValue("")
	m.pass.Blur()
	m.scr, m.err = scrExtend, ""
	return m, nil
}

func (m *Model) extFocus(f int) {
	if f == xPass && !m.extendNeedsPassword() {
		f = xSave
	}
	m.ext.focus = f
	m.pass.Blur()
	if f == xPass {
		m.pass.Focus()
	}
}

func (m Model) keyExtend(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.pass.SetValue("")
		m.scr, m.err = scrSource, ""
		return m, nil
	case "tab", "down":
		m.extFocus(min(m.ext.focus+1, xSave))
		return m, nil
	case "shift+tab", "up":
		f := max(m.ext.focus-1, xDays)
		if f == xPass && !m.extendNeedsPassword() {
			f = xDays
		}
		m.extFocus(f)
		return m, nil
	case "left", "right":
		if m.ext.focus == xDays {
			d := 1
			if k.String() == "left" {
				d = -1
			}
			m.ext.pick = (m.ext.pick + d + len(m.ext.choices)) % len(m.ext.choices)
			return m, nil
		}
	case "enter":
		if m.ext.focus == xDays {
			m.extFocus(xPass)
			return m, nil
		}
		pw := m.pass.Value()
		if m.extendNeedsPassword() && pw == "" {
			m.err = "Enter the " + srcKind(m.src) + " password to reopen the analysis."
			m.extFocus(xPass)
			return m, nil
		}
		id, days, b := m.cur, m.ext.choices[m.ext.pick], m.b
		label := "Extending the analysis"
		if m.src.Phase == engine.Done {
			label = "Reconnecting to " + srcKind(m.src)
		}
		return m.busyCmd(label, "extend", id, func() error { return b.Extend(id, days, pw) })
	}
	if m.ext.focus == xPass {
		var cmd tea.Cmd
		m.pass, cmd = m.pass.Update(k)
		return m, cmd
	}
	return m, nil
}

func (m Model) viewExtend() (string, []zone) {
	t := m.th
	s := m.src
	var st stack
	st.add(t.h1.Render("Collect for longer"))
	if s.Phase == engine.Done {
		st.add(t.mute.Width(80).Render(fmt.Sprintf("The analysis reopens and collects again from now. Everything collected so far is kept; the time since it finished on %s is not collected.",
			s.Finished.Local().Format("Mon Jan 02 15:04"))))
	} else {
		st.add(t.mute.Width(80).Render("Collection carries on until the new end. Everything collected so far is kept."))
	}
	st.add("")
	labels := make([]string, len(m.ext.choices))
	for i, d := range m.ext.choices {
		labels[i] = plural(d, "day", "days")
	}
	st.add(m.field(m.ext.focus == xDays, "Add", t.segmented(labels, m.ext.pick, m.ext.focus == xDays)))
	end := m.extendEnd(m.ext.choices[m.ext.pick])
	st.add(m.field(false, "New end", t.bold.Render(end.Local().Format("Mon Jan 02 15:04"))+
		t.faint.Render(fmt.Sprintf("  %s in total", plural(int(end.Sub(s.Started).Hours()/24+0.5), "day", "days")))))
	if m.extendNeedsPassword() {
		st.add("", m.field(m.ext.focus == xPass, srcKind(m.src)+" password", m.pass.View()))
		st.add(m.hint(s.Config.User + " on " + s.Config.Host))
	}
	st.add("")
	focus := -1
	if m.ext.focus == xSave {
		focus = 0
	}
	btn, bz := t.buttons(focus, action{"Extend analysis", "submit"})
	st.addZoned(btn, submitZones(bz), 0)
	return m.card(&st, true)
}
