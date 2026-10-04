package tui

import (
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/MarcoColomb0/rightsizer/internal/appliance"
)

// notesMsg carries the release notes shown on the update prompt.
type notesMsg struct {
	notes []appliance.Note
	err   error
}

func (m Model) fetchNotes() tea.Cmd {
	f := m.opt.Notes
	if f == nil || !m.updateAvailable() {
		return nil
	}
	return func() tea.Msg {
		n, err := f()
		return notesMsg{n, err}
	}
}

var (
	mdLink = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^)\s]+)\)`)
	mdBold = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	mdCode = regexp.MustCompile("`([^`]+)`")
)

// inline styles the Markdown that release notes use within a line.
func (t theme) inline(s string) string {
	s = mdLink.ReplaceAllStringFunc(s, func(x string) string {
		g := mdLink.FindStringSubmatch(x)
		return t.link(g[1], g[2])
	})
	s = mdBold.ReplaceAllStringFunc(s, func(x string) string { return t.bold.Render(mdBold.FindStringSubmatch(x)[1]) })
	return mdCode.ReplaceAllStringFunc(s, func(x string) string { return t.acc.Render(mdCode.FindStringSubmatch(x)[1]) })
}

// renderNotes lays out the release notes of every pending version at width
// w. It understands the subset of Markdown the changelog uses: ### headings,
// bullet lists, bold, code and links.
func (m Model) renderNotes(w int) []string {
	t := m.th
	var out []string
	blank := func() {
		if len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
		}
	}
	for _, n := range m.notes {
		blank()
		head := t.pill(n.Tag, t.accent)
		if !n.Date.IsZero() {
			head += t.faint.Render("  " + n.Date.Local().Format("2 Jan 2006"))
		}
		out = append(out, head)
		top := len(out)
		for l := range strings.SplitSeq(strings.ReplaceAll(n.Body, "\r", ""), "\n") {
			trim := strings.TrimSpace(l)
			switch {
			case trim == "":
				if len(out) > top {
					blank()
				}
			case strings.HasPrefix(trim, "**Full Changelog**"), strings.HasPrefix(trim, "# "), strings.HasPrefix(trim, "## "):
			case strings.HasPrefix(trim, "### "):
				if len(out) > top {
					blank()
				}
				out = append(out, t.h2.Render(strings.TrimPrefix(trim, "### ")))
			case strings.HasPrefix(trim, "- "), strings.HasPrefix(trim, "* "):
				pad := strings.Repeat("  ", (len(l)-len(strings.TrimLeft(l, " ")))/2)
				body := lipgloss.NewStyle().Width(max(w-len(pad)-2, 10)).Render(t.inline(trim[2:]))
				for i, bl := range strings.Split(body, "\n") {
					lead := pad + "  "
					if i == 0 {
						lead = pad + t.acc.Render("• ")
					}
					out = append(out, lead+bl)
				}
			default:
				out = append(out, strings.Split(lipgloss.NewStyle().Width(w).Render(t.inline(trim)), "\n")...)
			}
		}
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// notesWidth is the width of the notes inside the update card.
func (m Model) notesWidth() int { return min(m.bodyW(), 100) - 8 }

func (m *Model) scrollNotes(d int) {
	m.notesOff = min(max(m.notesOff+d, 0), max(len(m.renderNotes(m.notesWidth()))-1, 0))
}
