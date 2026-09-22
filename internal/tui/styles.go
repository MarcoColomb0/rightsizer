package tui

import "github.com/charmbracelet/lipgloss"

var (
	accent = lipgloss.AdaptiveColor{Light: "#0F766E", Dark: "#2DD4BF"}
	muted  = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#8B949E"}
	warn   = lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#FBBF24"}
	bad    = lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#F87171"}
	line   = lipgloss.AdaptiveColor{Light: "#D1D5DB", Dark: "#30363D"}

	sTitle  = lipgloss.NewStyle().Bold(true).Foreground(accent)
	sMuted  = lipgloss.NewStyle().Foreground(muted)
	sBold   = lipgloss.NewStyle().Bold(true)
	sWarn   = lipgloss.NewStyle().Foreground(warn)
	sBad    = lipgloss.NewStyle().Foreground(bad)
	sAccent = lipgloss.NewStyle().Foreground(accent)
	sBox    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(line).Padding(0, 1)
	sKPI    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(line).Padding(0, 1).Width(22)
	sKey    = lipgloss.NewStyle().Foreground(accent).Bold(true)
	sTabOn  = lipgloss.NewStyle().Bold(true).Foreground(accent).Underline(true)
	sTabOff = lipgloss.NewStyle().Foreground(muted)
	sLabel  = lipgloss.NewStyle().Width(16).Foreground(muted)
	sFocus  = lipgloss.NewStyle().Foreground(accent).Bold(true)
)

func keys(pairs ...string) string {
	out := ""
	for i := 0; i+1 < len(pairs); i += 2 {
		if out != "" {
			out += sMuted.Render("  ·  ")
		}
		out += sKey.Render(pairs[i]) + " " + sMuted.Render(pairs[i+1])
	}
	return out
}
