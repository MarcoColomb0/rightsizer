package tui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// theme holds the palette and styles for a light or dark terminal. Bubble
// Tea downsamples the true colours for terminals with fewer.
type theme struct {
	dark bool

	text, muted, subtle, border, surface, sel color.Color
	accent, accent2, good, warn, bad, info    color.Color
	onAccent                                  color.Color
	heatStops                                 []color.Color

	bold, mute, faint, acc, ok, wrn, err, inf lipgloss.Style
	label, focusLabel, key, keyDesc           lipgloss.Style
	h1, h2                                    lipgloss.Style
}

func newTheme(dark bool) theme {
	ld := lipgloss.LightDark(dark)
	c := lipgloss.Color
	t := theme{
		dark:     dark,
		text:     ld(c("#1F2328"), c("#E6EDF3")),
		muted:    ld(c("#57606A"), c("#8B949E")),
		subtle:   ld(c("#AFB8C1"), c("#3D444D")),
		border:   ld(c("#D0D7DE"), c("#30363D")),
		surface:  ld(c("#F6F8FA"), c("#161B22")),
		sel:      ld(c("#D8F3EE"), c("#123B39")),
		accent:   ld(c("#0F766E"), c("#2DD4BF")),
		accent2:  ld(c("#6D28D9"), c("#A78BFA")),
		good:     ld(c("#1A7F37"), c("#3FB950")),
		warn:     ld(c("#9A6700"), c("#FBBF24")),
		bad:      ld(c("#CF222E"), c("#F87171")),
		info:     ld(c("#0969DA"), c("#60A5FA")),
		onAccent: ld(c("#FFFFFF"), c("#0D1117")),
	}
	t.heatStops = []color.Color{ld(c("#D8F3EE"), c("#123B39")), t.accent, t.warn, t.bad}
	s := lipgloss.NewStyle
	t.bold = s().Bold(true).Foreground(t.text)
	t.mute = s().Foreground(t.muted)
	t.faint = s().Foreground(t.subtle)
	t.acc = s().Foreground(t.accent)
	t.ok = s().Foreground(t.good)
	t.wrn = s().Foreground(t.warn)
	t.err = s().Foreground(t.bad)
	t.inf = s().Foreground(t.info)
	t.label = s().Foreground(t.muted).Width(22)
	t.focusLabel = s().Foreground(t.accent).Bold(true).Width(22)
	t.key = s().Foreground(t.accent).Bold(true)
	t.keyDesc = s().Foreground(t.muted)
	t.h1 = s().Bold(true).Foreground(t.text)
	t.h2 = s().Bold(true).Foreground(t.accent)
	return t
}

// gradient colours each character of s along the accent gradient.
func (t theme) gradient(s string, bold bool) string {
	return t.blend(s, bold, t.accent, t.accent2)
}

func (t theme) blend(s string, bold bool, stops ...color.Color) string {
	r := []rune(s)
	if len(r) == 0 {
		return ""
	}
	cs := lipgloss.Blend1D(max(len(r), 2), stops...)
	var b strings.Builder
	for i, ch := range r {
		b.WriteString(lipgloss.NewStyle().Foreground(cs[i]).Bold(bold).Render(string(ch)))
	}
	return b.String()
}

// rule draws a horizontal line that fades along the accent gradient.
func (t theme) rule(w int) string {
	if w <= 0 {
		return ""
	}
	return t.blend(strings.Repeat("─", w), false, t.accent, t.accent2, t.subtle)
}

// heat maps 0..1 to the cool-to-hot palette used for load.
func (t theme) heat(v float64) color.Color {
	v = min(max(v, 0), 1)
	cs := lipgloss.Blend1D(21, t.heatStops...)
	return cs[int(v*20+0.5)]
}

// load colours a utilisation percentage: calm, busy, saturated.
func (t theme) load(pct float64) lipgloss.Style {
	switch {
	case pct >= 85:
		return t.err
	case pct >= 65:
		return t.wrn
	}
	return t.ok
}

// pill renders a compact badge with a coloured background.
func (t theme) pill(text string, bg color.Color) string {
	return lipgloss.NewStyle().Background(bg).Foreground(t.onAccent).Bold(true).Padding(0, 1).Render(text)
}

// button renders an action; the focused one is filled with the gradient.
func (t theme) button(label string, focused bool) string {
	if !focused {
		return lipgloss.NewStyle().Foreground(t.muted).Border(lipgloss.RoundedBorder()).BorderForeground(t.border).Padding(0, 2).Render(label)
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForegroundBlend(t.accent, t.accent2).Render(t.fill("  " + label + "  "))
}

// fill paints s on the accent gradient.
func (t theme) fill(s string) string {
	r := []rune(s)
	cs := lipgloss.Blend1D(max(len(r), 2), t.accent, t.accent2)
	var b strings.Builder
	for i, ch := range r {
		b.WriteString(lipgloss.NewStyle().Background(cs[i]).Foreground(t.onAccent).Bold(true).Render(string(ch)))
	}
	return b.String()
}

// panel frames content; the focused panel gets the gradient border.
func (t theme) panel(w int, focused bool) lipgloss.Style {
	s := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Width(w)
	if focused {
		return s.BorderForegroundBlend(t.accent, t.accent2)
	}
	return s.BorderForeground(t.border)
}

// callout highlights a remark with a coloured bar on its left.
func (t theme) callout(text string, c color.Color, w int) string {
	return lipgloss.NewStyle().Border(lipgloss.ThickBorder(), false, false, false, true).BorderForeground(c).
		Foreground(t.text).PaddingLeft(1).Width(w).Render(text)
}

// link makes text clickable in terminals that support hyperlinks.
func (t theme) link(text, url string) string {
	return lipgloss.NewStyle().Foreground(t.info).Underline(true).Hyperlink(url).Render(text)
}
