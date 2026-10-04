package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
	"github.com/MarcoColomb0/rightsizer/internal/engine"
)

// field renders one form row; the focused row gets an accent bar.
func (m Model) field(focused bool, label, val string) string {
	t := m.th
	if focused {
		return t.acc.Render("┃ ") + t.focusLabel.Render(label) + val
	}
	return "  " + t.label.Render(label) + val
}

// hint renders help text aligned with the field values.
func (m Model) hint(text string) string {
	return "  " + m.th.label.Render("") + m.th.faint.Render(text)
}

// segmented renders a choice with every option visible.
func (t theme) segmented(opts []string, sel int, focused bool) string {
	parts := make([]string, len(opts))
	for i, o := range opts {
		switch {
		case i == sel && focused:
			parts[i] = t.fill(" " + o + " ")
		case i == sel:
			parts[i] = lipgloss.NewStyle().Background(t.border).Foreground(t.text).Bold(true).Render(" " + o + " ")
		default:
			parts[i] = t.mute.Render(" " + o + " ")
		}
	}
	return strings.Join(parts, t.faint.Render("│"))
}

// carousel renders a choice with too many options to show at once: the
// current one, arrows and a dot per option.
func (t theme) carousel(opts []string, sel int, focused bool) string {
	val := t.bold.Render(opts[sel])
	arrows := t.faint
	if focused {
		val, arrows = t.fill(" "+opts[sel]+" "), t.acc
	}
	dots := make([]string, len(opts))
	for i := range opts {
		dots[i] = t.faint.Render("·")
		if i == sel {
			dots[i] = t.acc.Render("•")
		}
	}
	return arrows.Render("‹ ") + val + arrows.Render(" ›") + "  " + strings.Join(dots, "")
}

// toggle renders a two-way choice as a switch.
func (t theme) toggle(on bool, off, onLabel string, focused bool) string {
	sel := 0
	if on {
		sel = 1
	}
	return t.segmented([]string{off, onLabel}, sel, focused)
}

func (m Model) viewBusy() string {
	t := m.th
	el := m.watch.Elapsed()
	s := m.spin.View() + " " + t.bold.Render(m.busy) + "\n\n" + t.pulse(el, 44) + "\n\n" + t.faint.Render(elapsed(el)+" elapsed")
	return lipgloss.Place(m.bodyW(), m.bodyH(), lipgloss.Center, lipgloss.Center, s)
}

// pulse is an indeterminate progress bar: a gradient segment that sweeps
// back and forth.
func (t theme) pulse(el time.Duration, w int) string {
	seg := w / 4
	span := w - seg
	step := int(el/(50*time.Millisecond)) % (2 * span)
	pos := step
	if step > span {
		pos = 2*span - step
	}
	return t.faint.Render(strings.Repeat("━", pos)) + t.gradient(strings.Repeat("━", seg), true) + t.faint.Render(strings.Repeat("━", w-pos-seg))
}

func (m Model) viewSetup() (string, []zone) {
	t := m.th
	var s stack
	note := "The password is kept in memory only. A read-only vCenter role is enough."
	if m.sum != nil && m.sum.Vault.Enabled {
		note = "The password is stored encrypted with the administrator password. A read-only vCenter role is enough."
	}
	s.add(t.h1.Render("Add a vCenter source"), t.mute.Render(note), "")
	s.add(m.field(m.focus == fHost, "vCenter", m.in[0].View()))
	s.add(m.field(m.focus == fUser, "Username", m.in[1].View()))
	s.add(m.field(m.focus == fPass, "Password", m.in[2].View()))
	s.add("")
	labels := make([]string, len(durations))
	for i, d := range durations {
		labels[i] = d.label
	}
	s.add(m.field(m.focus == fDuration, "Duration", t.segmented(labels, m.durIdx, m.focus == fDuration)))
	names := make([]string, len(analysis.Profiles))
	for i, p := range analysis.Profiles {
		names[i] = p.Name
	}
	p := analysis.Profiles[m.profIdx]
	s.add(m.field(m.focus == fProfile, "Profile", t.segmented(names, m.profIdx, m.focus == fProfile)))
	s.add(m.hint(fmt.Sprintf("p%.0f · vCPU target %.0f%% · memory headroom +%.0f%%", p.Percentile, p.CPUTarget*100, (p.MemHeadroom-1)*100)))
	s.add("")
	s.add(m.field(m.focus == fClusters, "Clusters", m.in[3].View()))
	s.add("")
	focus := -1
	if m.focus == fStart {
		focus = 0
	}
	btn, bz := t.buttons(focus, action{"Start analysis", "submit"})
	s.addZoned(btn, submitZones(bz), 0)
	return m.card(&s, true)
}

// submitZones turns button zones into "submit" clicks: focus the button,
// then press enter.
func submitZones(zs []zone) []zone {
	for i := range zs {
		zs[i].id = "submit:"
	}
	return zs
}

func (m Model) viewCert() (string, []zone) {
	t := m.th
	c := m.cert
	var s stack
	s.add(t.wrn.Bold(true).Render("⚠ The vCenter certificate is not signed by a trusted CA"),
		t.mute.Width(80).Render("This is normal for self-signed VMCA certificates. Compare the fingerprint with the one shown in vCenter before you trust it."), "")
	kv := func(k, v string) { s.add("  " + t.label.Render(k) + v) }
	kv("Host", t.bold.Render(c.Host))
	kv("Subject", c.Subject)
	kv("Issuer", c.Issuer)
	exp := c.NotAfter.Format("2006-01-02")
	if d := time.Until(c.NotAfter); d > 0 {
		exp += t.faint.Render(fmt.Sprintf("  in %d days", int(d.Hours()/24)))
	} else {
		exp = t.err.Render(exp + "  expired")
	}
	kv("Expires", exp)
	s.add("", "  "+t.label.Render("SHA-256")+t.acc.Bold(true).Render(wrapFP(c.Fingerprint, 2+22)), "")
	s.add(t.mute.Render("The fingerprint is pinned for this source. Any other certificate will be refused."), "")
	btn, bz := t.buttons(0, action{"Trust and start", "y"}, action{"Back", "n"})
	s.addZoned(btn, bz, 0)
	return m.card(&s, true)
}

// wrapFP splits a long fingerprint over two lines, the second indented.
func wrapFP(fp string, indent int) string {
	if len(fp) > 48 {
		return fp[:48] + "\n" + strings.Repeat(" ", indent) + fp[48:]
	}
	return fp
}

func (m Model) viewResume() (string, []zone) {
	t := m.th
	var s stack
	s.add(t.wrn.Bold(true).Render("‖ Analysis paused"),
		t.mute.Width(80).Render("vCenter rejected the saved session or the engine restarted. Enter the vCenter password to continue collecting."), "")
	if src := m.src; src != nil {
		kv := func(k, v string) { s.add("  " + t.label.Render(k) + v) }
		kv("vCenter", t.bold.Render(src.Config.Host))
		kv("Username", src.Config.User)
		kv("Window", fmt.Sprintf("%s → %s", src.Started.Format("Jan 02 15:04"), src.Ends.Format("Jan 02 15:04")))
		if src.LastError != "" {
			kv("Last error", t.err.Render(src.LastError))
		}
		s.add("")
	}
	s.add(m.field(true, "Password", m.pass.View()), "")
	btn, bz := t.buttons(0, action{"Resume collection", "enter"})
	s.addZoned(btn, bz, 0)
	return m.card(&s, true)
}

func (m Model) viewSettings() (string, []zone) {
	t := m.th
	var s stack
	s.add(t.h1.Render("Change administrator password"),
		t.mute.Render("Used for console logins and to encrypt stored vCenter credentials. At least 12 characters."), "")
	for i, l := range []string{"Current", "New", "Repeat new"} {
		s.add(m.field(i == m.pwFocus, l, m.pw[i].View()))
	}
	s.add("")
	focus := -1
	if m.pwFocus == len(m.pw)-1 {
		focus = 0
	}
	btn, bz := t.buttons(focus, action{"Change password", "submit"})
	s.addZoned(btn, submitZones(bz), 0)
	return m.card(&s, true)
}

func (m Model) viewUpdate() (string, []zone) {
	t := m.th
	var s stack
	s.add(t.gradient("↑ Update available", true), "")
	s.add("  " + t.label.Render("Installed") + t.mute.Render(m.opt.Version))
	s.add("  " + t.label.Render("Latest") + t.pill(m.opt.Latest, t.accent))
	if m.opt.ReleaseURL != "" {
		s.add("  " + t.label.Render("Release notes") + t.link(m.opt.ReleaseURL, m.opt.ReleaseURL))
	}

	var after stack
	after.add("", t.faint.Render("The upgrade downloads first, stops the engine cleanly, backs up your data, and rolls back on its own if anything fails."))
	if m.sum != nil {
		for _, src := range m.sum.Sources {
			if src.Phase == engine.Running || src.Phase == engine.NeedPassword {
				msg := "Running analyses continue after the upgrade."
				if m.opt.Appliance {
					msg += " Log in again afterwards so collection resumes with the stored credentials."
				}
				if !m.sum.Vault.Enabled {
					msg += " You will be asked for their vCenter passwords again."
				}
				after.add("", t.callout(msg+"\n"+t.mute.Render("vCenter keeps one hour of real-time samples, so a short upgrade leaves no gap."), t.warn, m.notesWidth()))
				break
			}
		}
	}
	after.add("")
	btn, bz := t.buttons(0, action{"Upgrade now", "y"}, action{"Later", "n"})
	after.addZoned(btn, bz, 0)

	if m.notes != nil || m.notesErr != "" || m.opt.Notes != nil {
		s.add("")
		lines := m.renderNotes(m.notesWidth())
		h := max(m.bodyH()-s.h-after.h-6, 3)
		head := t.h2.Render("What's new")
		if len(lines) > h {
			head += t.faint.Render("  ↑/↓ to scroll")
		}
		s.add(head)
		switch {
		case len(lines) > 0:
			off := min(m.notesOff, max(len(lines)-h, 0))
			s.add(strings.Join(lines[off:min(off+h, len(lines))], "\n"))
		case m.notesErr != "":
			s.add(t.faint.Render("The release notes could not be loaded; they are on the release page."))
		case m.notes != nil:
			s.add(t.faint.Render("No notes for this release."))
		default:
			s.add(m.spin.View() + t.mute.Render(" Loading the release notes…"))
		}
	}
	s.addZoned(after.String(), after.zones, 0)
	return m.card(&s, true)
}
