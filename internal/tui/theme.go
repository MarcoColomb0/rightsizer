package tui

import (
	"image/color"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
)

// Twilight palette: ice blues fading into violets.
var (
	frozenLake     = lipgloss.Color("#97DFFC")
	skyBlue        = lipgloss.Color("#93CAF6")
	babyBlueIce    = lipgloss.Color("#8EB5F0")
	softPeriwinkle = lipgloss.Color("#858AE3")
	slateBlue      = lipgloss.Color("#7364D2")
	violetTwilight = lipgloss.Color("#613DC1")
	rebeccaPurple  = lipgloss.Color("#5829A7")
	darkAmethyst   = lipgloss.Color("#3D0E61")
)

// theme holds the palette and styles for a light or dark terminal. Bubble
// Tea downsamples the true colours for terminals with fewer.
type theme struct {
	dark bool

	bg, text, muted, subtle, border, surface, sel, track color.Color
	accent, good, warn, bad, info                        color.Color
	onAccent                                             color.Color
	// grad colours text, rules and borders; fills is the background of
	// filled elements, readable under onAccent.
	grad, fills, heatStops []color.Color

	bold, mute, faint, acc, ok, wrn, err, inf lipgloss.Style
	label, focusLabel, key, keyDesc           lipgloss.Style
	h1, h2                                    lipgloss.Style
}

func newTheme(dark bool) theme {
	ld := lipgloss.LightDark(dark)
	c := lipgloss.Color
	t := theme{
		dark:     dark,
		bg:       ld(c("#FCFBFF"), c("#13111E")),
		text:     ld(c("#1E1A33"), c("#E9E8F7")),
		muted:    ld(c("#5D5880"), c("#A3A6D4")),
		subtle:   ld(c("#8F89B8"), c("#5D5791")),
		border:   ld(c("#DDD9F2"), c("#383257")),
		surface:  ld(c("#F2F0FB"), c("#1D1934")),
		sel:      ld(c("#ECE9FB"), darkAmethyst),
		track:    ld(c("#E3E0F4"), c("#2E2950")),
		accent:   ld(violetTwilight, frozenLake),
		good:     ld(c("#15803D"), c("#4ADE80")),
		warn:     ld(c("#B45309"), c("#FBBF24")),
		bad:      ld(c("#C2334D"), c("#F87171")),
		info:     ld(slateBlue, skyBlue),
		onAccent: ld(c("#FFFFFF"), darkAmethyst),
	}
	if dark {
		t.grad = []color.Color{frozenLake, skyBlue, babyBlueIce, softPeriwinkle, slateBlue}
		t.fills = []color.Color{frozenLake, babyBlueIce, softPeriwinkle}
		t.heatStops = []color.Color{darkAmethyst, violetTwilight, softPeriwinkle, frozenLake, t.warn, t.bad}
	} else {
		t.grad = []color.Color{slateBlue, violetTwilight, rebeccaPurple}
		t.fills = []color.Color{slateBlue, violetTwilight}
		t.heatStops = []color.Color{t.sel, babyBlueIce, softPeriwinkle, violetTwilight, t.warn, t.bad}
	}
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

// gradient colours each character of s along the palette.
func (t theme) gradient(s string, bold bool) string {
	return t.blend(s, bold, t.grad...)
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
	return t.blend(strings.Repeat("─", w), false, append(slices.Clone(t.grad), t.subtle)...)
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
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForegroundBlend(t.grad...).Render(t.fill("  " + label + "  "))
}

// fill paints s on the palette.
func (t theme) fill(s string) string {
	r := []rune(s)
	cs := lipgloss.Blend1D(max(len(r), 2), t.fills...)
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
		return s.BorderForegroundBlend(t.grad...)
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
