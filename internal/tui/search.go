package tui

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// search is a vim-style search over the findings table: / forward, ?
// backward, n and N to repeat, incremental while typing, smartcase.
type search struct {
	prompt   bool
	backward bool
	query    string
	origin   int
	matches  []int
}

func newSearchInput() textinput.Model {
	t := textinput.New()
	t.Prompt = ""
	t.CharLimit = 100
	return t
}

func (m *Model) findingText(i int) string {
	f := m.src.Result.Findings[i]
	return strings.Join([]string{f.Severity.String(), f.VM, f.Cluster, string(f.Kind), f.Current, f.Suggested, f.Confidence}, " ")
}

// matchRows returns the rows containing q. Like vim's smartcase, the search
// ignores case unless the pattern has an uppercase letter.
func (m *Model) matchRows(q string) []int {
	if q == "" || m.src == nil || m.src.Result == nil {
		return nil
	}
	fold := !strings.ContainsFunc(q, unicode.IsUpper)
	if fold {
		q = strings.ToLower(q)
	}
	var out []int
	for i := range m.src.Result.Findings {
		t := m.findingText(i)
		if fold {
			t = strings.ToLower(t)
		}
		if strings.Contains(t, q) {
			out = append(out, i)
		}
	}
	return out
}

// next finds the match after (or before) row from, wrapping around the ends.
func next(matches []int, from int, backward bool) (row int, wrapped bool) {
	if len(matches) == 0 {
		return -1, false
	}
	if backward {
		for i := len(matches) - 1; i >= 0; i-- {
			if matches[i] < from {
				return matches[i], false
			}
		}
		return matches[len(matches)-1], true
	}
	for _, r := range matches {
		if r > from {
			return r, false
		}
	}
	return matches[0], true
}

func (m Model) openSearch(backward bool) (tea.Model, tea.Cmd) {
	m.srch.prompt, m.srch.backward, m.srch.origin = true, backward, m.tbl.Cursor()
	m.srchIn.SetValue("")
	m.srchIn.Focus()
	m.err, m.note = "", ""
	return m, nil
}

func (m Model) keySearchPrompt(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.srch.prompt = false
		m.srchIn.Blur()
		m.tbl.SetCursor(m.srch.origin)
		m.fillTable()
		return m, nil
	case "enter":
		m.srch.prompt = false
		m.srchIn.Blur()
		if q := m.srchIn.Value(); q != "" {
			m.srch.query = q
		}
		m.srch.matches = m.matchRows(m.srch.query)
		m.fillTable()
		if len(m.srch.matches) == 0 {
			m.tbl.SetCursor(m.srch.origin)
			if m.srch.query != "" {
				m.err = "Pattern not found: " + m.srch.query
			}
			return m, nil
		}
		m.jump(m.srch.origin, m.srch.backward)
		return m, nil
	case "backspace":
		if m.srchIn.Value() == "" {
			return m.keySearchPrompt(tea.KeyMsg{Type: tea.KeyEsc})
		}
	}
	var cmd tea.Cmd
	m.srchIn, cmd = m.srchIn.Update(k)
	// incsearch: preview the first match while typing
	if rows := m.matchRows(m.srchIn.Value()); len(rows) > 0 {
		r, _ := next(rows, m.srch.origin, m.srch.backward)
		m.tbl.SetCursor(r)
	} else {
		m.tbl.SetCursor(m.srch.origin)
	}
	return m, cmd
}

// jump moves to the next match in the given direction and reports wrapping
// like vim does.
func (m *Model) jump(from int, backward bool) {
	m.srch.matches = m.matchRows(m.srch.query)
	if len(m.srch.matches) == 0 {
		if m.srch.query != "" {
			m.err = "Pattern not found: " + m.srch.query
		}
		return
	}
	r, wrapped := next(m.srch.matches, from, backward)
	m.tbl.SetCursor(r)
	m.err = ""
	switch {
	case wrapped && backward:
		m.note = "search hit TOP, continuing at BOTTOM"
	case wrapped:
		m.note = "search hit BOTTOM, continuing at TOP"
	}
}

func (m Model) searchStatus() string {
	if m.srch.prompt {
		lead := "/"
		if m.srch.backward {
			lead = "?"
		}
		return sAccent.Render(lead) + m.srchIn.View()
	}
	if m.srch.query == "" {
		return ""
	}
	pos := 0
	for i, r := range m.srch.matches {
		if r == m.tbl.Cursor() {
			pos = i + 1
		}
	}
	count := fmt.Sprintf("%d matches", len(m.srch.matches))
	if pos > 0 {
		count = fmt.Sprintf("%d/%d", pos, len(m.srch.matches))
	}
	return sMuted.Render("/"+m.srch.query+"  ") + sAccent.Render(count) + sMuted.Render("  n/N next/previous · esc clear")
}
