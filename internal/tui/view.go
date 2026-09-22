package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
	"github.com/MarcoColomb0/rightsizer/internal/engine"
)

func (m Model) View() string {
	var body string
	switch m.scr {
	case scrLoading:
		body = m.spin.View() + " Connecting to rightsizer daemon…"
	case scrSetup:
		body = m.viewSetup()
	case scrCert:
		body = m.viewCert()
	case scrBusy:
		body = m.spin.View() + " " + m.busy
	case scrResume:
		body = m.viewResume()
	case scrDash:
		body = m.viewDash()
	case scrUpdate:
		body = m.viewUpdate()
	}
	out := m.header() + "\n\n" + body
	if m.err != "" {
		out += "\n\n" + sBad.Render("✗ "+m.err)
	}
	return lipgloss.NewStyle().Padding(1, 2).Render(out)
}

func (m Model) header() string {
	l := sTitle.Render("rightsizer") + sMuted.Render("  vSphere rightsizing · read-only")
	if m.st != nil && m.st.Config.Host != "" && m.st.Phase != engine.Idle {
		l += sMuted.Render("  ·  ") + m.st.Config.Host + "  " + phaseBadge(m.st.Phase)
	}
	if m.updateAvailable() && m.scr != scrUpdate {
		l += "  " + sWarn.Render("↑ "+m.latest+" available")
		if m.scr == scrDash {
			l += sMuted.Render(" (u)")
		}
	}
	return l
}

func phaseBadge(p engine.Phase) string {
	switch p {
	case engine.Running:
		return sAccent.Render("● collecting")
	case engine.NeedPassword:
		return sWarn.Render("● paused")
	case engine.Done:
		return sAccent.Render("✓ complete")
	}
	return ""
}

func (m Model) viewSetup() string {
	var b strings.Builder
	b.WriteString(sBold.Render("New analysis") + "\n")
	b.WriteString(sMuted.Render("Credentials stay in memory only. A read-only vCenter role is enough.") + "\n\n")
	row := func(f int, label, val string) {
		l := sLabel.Render(label)
		if m.focus == f {
			l = sFocus.Width(16).Render("› " + label)
		}
		b.WriteString(l + val + "\n")
	}
	row(fHost, "vCenter", m.in[0].View())
	row(fUser, "Username", m.in[1].View())
	row(fPass, "Password", m.in[2].View())
	row(fDuration, "Duration", choice(durations[m.durIdx].label, m.focus == fDuration))
	p := analysis.Profiles[m.profIdx]
	row(fProfile, "Profile", choice(p.Name, m.focus == fProfile)+sMuted.Render(fmt.Sprintf("  p%.0f · vCPU target %.0f%% · mem headroom +%.0f%%", p.Percentile, p.CPUTarget*100, (p.MemHeadroom-1)*100)))
	row(fClusters, "Clusters", m.in[3].View())
	b.WriteString("\n")
	btn := lipgloss.NewStyle().Padding(0, 2).Border(lipgloss.RoundedBorder()).BorderForeground(line)
	if m.focus == fStart {
		btn = btn.BorderForeground(accent).Foreground(accent).Bold(true)
	}
	b.WriteString(btn.Render("Start analysis") + "\n\n")
	b.WriteString(keys("↑/↓", "move", "←/→", "change", "enter", "next/start", "esc", "quit"))
	return b.String()
}

func choice(v string, focused bool) string {
	if focused {
		return sAccent.Render("‹ ") + sBold.Render(v) + sAccent.Render(" ›")
	}
	return v
}

func (m Model) viewCert() string {
	c := m.cert
	var b strings.Builder
	b.WriteString(sWarn.Render("⚠ The vCenter certificate is not signed by a trusted CA") + "\n")
	b.WriteString(sMuted.Render("Common with self-signed VMCA certificates. Compare the fingerprint with the one shown in vCenter before trusting it.") + "\n\n")
	kv := func(k, v string) { b.WriteString(sLabel.Render(k) + v + "\n") }
	kv("Host", c.Host)
	kv("Subject", c.Subject)
	kv("Issuer", c.Issuer)
	kv("Expires", c.NotAfter.Format("2006-01-02"))
	kv("SHA-256", "")
	b.WriteString(sBold.Render(wrapFP(c.Fingerprint)) + "\n\n")
	b.WriteString("The fingerprint is pinned for this analysis: any other certificate is refused.\n\n")
	b.WriteString(keys("y", "trust and start", "n", "back"))
	return b.String()
}

func wrapFP(fp string) string {
	if len(fp) > 48 {
		return fp[:48] + "\n" + fp[48:]
	}
	return fp
}

func (m Model) viewUpdate() string {
	var b strings.Builder
	b.WriteString(sBold.Render("Update available") + "\n\n")
	b.WriteString(sLabel.Render("Installed") + m.current + "\n")
	b.WriteString(sLabel.Render("Latest") + sAccent.Render(m.latest) + "\n")
	if m.notes != "" {
		b.WriteString(sLabel.Render("Release notes") + m.notes + "\n")
	}
	b.WriteString("\n")
	steps := []string{
		"Download the new image while the collector keeps running",
		"Stop the collector cleanly so all samples are saved",
		"Back up collected data and reports",
		"Start the new version and check the data loaded",
		"Roll back automatically if anything fails",
	}
	for i, s := range steps {
		b.WriteString(sMuted.Render(fmt.Sprintf("  %d. ", i+1)) + s + "\n")
	}
	if m.st != nil && (m.st.Phase == engine.Running || m.st.Phase == engine.NeedPassword) {
		b.WriteString("\n" + sWarn.Render("An analysis is in progress. It continues after the upgrade; you will be asked for the vCenter password again.") + "\n")
		b.WriteString(sMuted.Render("vCenter keeps one hour of real-time samples, so a short upgrade leaves no gap.") + "\n")
	}
	b.WriteString("\n" + sBold.Render("Upgrade now?") + " " + sAccent.Render("[Y/n]"))
	return b.String()
}

func (m Model) viewResume() string {
	s := m.st
	var b strings.Builder
	b.WriteString(sWarn.Render("Analysis paused") + " — the collector restarted or vCenter rejected the saved session.\n")
	b.WriteString(sMuted.Render("Passwords are never written to disk, so enter it again to continue collecting.") + "\n\n")
	b.WriteString(sLabel.Render("vCenter") + s.Config.Host + "\n")
	b.WriteString(sLabel.Render("Username") + s.Config.User + "\n")
	b.WriteString(sLabel.Render("Window") + fmt.Sprintf("%s → %s", s.Started.Format("Jan 02 15:04"), s.Ends.Format("Jan 02 15:04")) + "\n")
	if s.LastError != "" {
		b.WriteString(sLabel.Render("Last error") + sBad.Render(s.LastError) + "\n")
	}
	b.WriteString("\n" + sFocus.Width(16).Render("› Password") + m.pass.View() + "\n\n")
	b.WriteString(keys("enter", "resume", "ctrl+f", "finish with data so far", "esc", "quit"))
	return b.String()
}

func (m Model) viewDash() string {
	s := m.st
	if s == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.progressLine() + "\n\n")
	if r := s.Result; r != nil {
		b.WriteString(kpis(r) + "\n")
	} else {
		b.WriteString(m.spin.View() + sMuted.Render(" Waiting for the first samples…") + "\n\n")
	}
	if s.Share != nil {
		b.WriteString(shareBox(s) + "\n")
	}
	tabs := []string{"1 Clusters", "2 Findings"}
	for i, t := range tabs {
		if i == m.tab {
			tabs[i] = sTabOn.Render(t)
		} else {
			tabs[i] = sTabOff.Render(t)
		}
	}
	n := 0
	if s.Result != nil {
		n = len(s.Result.Findings)
	}
	b.WriteString(strings.Join(tabs, "   ") + sMuted.Render(fmt.Sprintf("   (%d findings)", n)) + "\n\n")
	if m.tab == 0 {
		b.WriteString(clusters(s.Result))
	} else {
		b.WriteString(m.tbl.View())
	}
	b.WriteString("\n\n")
	switch {
	case m.confirm == "finish":
		b.WriteString(sWarn.Render("Stop collecting now and build the final report? [y/N]"))
	case m.confirm == "cancel":
		b.WriteString(sBad.Render("Discard this analysis and all collected data? [y/N]"))
	case s.Phase == engine.Running:
		k := []string{"1/2", "tabs", "p", "interim PDF", "f", "finish now", "x", "discard"}
		if m.updateAvailable() {
			k = append(k, "u", "upgrade")
		}
		b.WriteString(keys(append(k, "q", "detach")...))
	default:
		k := []string{"1/2", "tabs", "p", "publish PDF"}
		if s.Share != nil {
			k = append(k, "s", "stop sharing")
		}
		if m.updateAvailable() {
			k = append(k, "u", "upgrade")
		}
		b.WriteString(keys(append(k, "n", "new analysis", "q", "quit")...))
	}
	if s.Phase == engine.Running {
		b.WriteString("\n" + sMuted.Render("Detaching leaves the analysis running in the background. Run `rightsizer` again to reattach."))
	}
	return b.String()
}

func (m Model) progressLine() string {
	s := m.st
	total := s.Ends.Sub(s.Started)
	end := time.Now()
	if !s.Finished.IsZero() {
		end = s.Finished
	}
	el := end.Sub(s.Started)
	frac := 0.0
	if total > 0 {
		frac = min(float64(el)/float64(total), 1)
	}
	if s.Phase == engine.Done {
		frac = 1
	}
	line := m.prog.ViewAs(frac) + fmt.Sprintf("  %3.0f%%  ", frac*100) + sMuted.Render(fmt.Sprintf("%s of %s", human(el), human(total)))
	var sub []string
	switch s.Phase {
	case engine.Running:
		sub = append(sub, "ends "+s.Ends.Format("Mon Jan 02 15:04"))
		if !s.LastPoll.IsZero() {
			sub = append(sub, "last sample "+s.LastPoll.Format("15:04"))
		}
		if !s.NextPoll.IsZero() {
			sub = append(sub, "next "+s.NextPoll.Format("15:04"))
		}
	case engine.Done:
		sub = append(sub, "finished "+s.Finished.Format("Mon Jan 02 15:04"))
	}
	sub = append(sub, fmt.Sprintf("%d polls", s.Polls), s.Config.Profile+" profile")
	out := line + "\n" + sMuted.Render(strings.Join(sub, " · "))
	if s.LastError != "" {
		out += "\n" + sWarn.Render("⚠ "+s.LastError+" (retrying)")
	}
	return out
}

func kpis(r *analysis.Result) string {
	t := r.Totals
	box := func(label, val, sub string) string {
		return sKPI.Render(sMuted.Render(label) + "\n" + sBold.Render(val) + "\n" + sAccent.Render(sub))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top,
		box("vCPU", fmt.Sprintf("%d → %d", t.VCPU, t.RecVCPU), delta(t.VCPU, t.RecVCPU)),
		box("Memory", fmt.Sprintf("%s → %s", analysis.GiB(t.MemMB), analysis.GiB(t.RecMemMB)), delta(t.MemMB, t.RecMemMB)),
		box("Hosts (N+1)", fmt.Sprintf("%d → %d", t.Hosts, t.HostsNeeded), fmt.Sprintf("cores %d → %d", t.Cores, t.NeedCores)),
		box("Reclaimable disk", analysis.Human(t.Reclaim), fmt.Sprintf("%d VMs analysed", t.Analyzed)),
	)
}

func delta(a, b int) string {
	if a == 0 {
		return "–"
	}
	return fmt.Sprintf("%+.0f%%", float64(b-a)/float64(a)*100)
}

func shareBox(s *engine.Status) string {
	sh := s.Share
	body := sBold.Render("Report ready") + sMuted.Render(fmt.Sprintf("  %s · expires %s · %d downloads", sh.File, sh.Expires.Format("Jan 02 15:04"), sh.Downloads)) + "\n" +
		sAccent.Render(sh.URL) + "\n" +
		sMuted.Render("Self-signed TLS, SHA-256 "+sh.Fingerprint)
	return sBox.BorderForeground(accent).Render(body)
}

var sparks = []rune("▁▂▃▄▅▆▇█")

func spark(pts []analysis.Point, capMHz float64) string {
	if len(pts) == 0 || capMHz == 0 {
		return ""
	}
	if len(pts) > 24 {
		pts = analysis.Downsample(pts, 24)
	}
	var b strings.Builder
	for _, p := range pts {
		i := int(min(p.CPUMHz/capMHz, 0.999) * float64(len(sparks)))
		b.WriteRune(sparks[max(i, 0)])
	}
	return b.String()
}

func clusters(r *analysis.Result) string {
	if r == nil || len(r.Clusters) == 0 {
		return sMuted.Render("No cluster data yet.")
	}
	head := fmt.Sprintf("%-22s %6s %9s %7s %13s %19s %9s  %s", "Cluster", "Hosts", "CPU p/pk", "Mem p", "vCPU", "Memory", "Need", "CPU trend")
	rows := []string{sMuted.Render(head)}
	for _, c := range r.Clusters {
		name := c.Name
		if len([]rune(name)) > 22 {
			name = string([]rune(name)[:21]) + "…"
		}
		need := fmt.Sprintf("%d", c.HostsNeeded)
		if c.HostsNeeded < c.Hosts {
			need = sAccent.Render(fmt.Sprintf("%9s", need))
		} else if c.HostsNeeded > c.Hosts {
			need = sWarn.Render(fmt.Sprintf("%9s", need))
		} else {
			need = fmt.Sprintf("%9s", need)
		}
		rows = append(rows, fmt.Sprintf("%-22s %6d %9s %7s %13s %19s %s  %s",
			name, c.Hosts,
			fmt.Sprintf("%.0f/%.0f%%", c.CPUP, c.CPUPeak), fmt.Sprintf("%.0f%%", c.MemP),
			fmt.Sprintf("%d→%d", c.VCPU, c.RecVCPU),
			fmt.Sprintf("%s→%s", analysis.GiB(c.MemMB), analysis.GiB(c.RecMemMB)),
			need, sAccent.Render(spark(c.Points, c.CapMHz))))
	}
	return strings.Join(rows, "\n")
}

func human(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	h := int(d.Hours())
	switch {
	case h >= 24:
		return fmt.Sprintf("%dd %dh", h/24, h%24)
	case h >= 1:
		return fmt.Sprintf("%dh %dm", h, int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}
