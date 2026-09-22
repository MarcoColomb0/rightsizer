package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
	"github.com/MarcoColomb0/rightsizer/internal/engine"
	"github.com/MarcoColomb0/rightsizer/internal/report"
)

func (m Model) View() string {
	var body string
	switch m.scr {
	case scrLoading:
		body = m.spin.View() + " Connecting to the rightsizer engine…"
	case scrHome:
		body = m.viewHome()
	case scrSetup:
		body = m.viewSetup()
	case scrCert:
		body = m.viewCert()
	case scrBusy:
		body = m.spin.View() + " " + m.busy
	case scrSource:
		body = m.viewSource()
	case scrResume:
		body = m.viewResume()
	case scrSettings:
		body = m.viewSettings()
	case scrUpdate:
		body = m.viewUpdate()
	case scrExclude:
		body = m.viewExclude()
	case scrExclusions:
		body = m.viewExclusions()
	}
	out := m.header() + "\n\n" + body
	if m.note != "" {
		out += "\n\n" + sAccent.Render("✓ "+m.note)
	}
	if m.err != "" {
		out += "\n\n" + sBad.Render("✗ "+m.err)
	}
	return lipgloss.NewStyle().Padding(1, 2).Render(out)
}

func (m Model) header() string {
	l := sTitle.Render("rightsizer") + sMuted.Render("  vSphere rightsizing · read-only")
	if m.scr == scrSource && m.src != nil {
		l += sMuted.Render("  ·  ") + m.src.Config.Host + "  " + phaseBadge(m.src.Phase)
	}
	if m.updateAvailable() && m.scr != scrUpdate {
		l += "  " + sWarn.Render("↑ "+m.opt.Latest+" available")
		if m.scr == scrHome || m.scr == scrSource {
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

func (m Model) viewHome() string {
	var b strings.Builder
	if m.sum == nil {
		return m.spin.View()
	}
	if len(m.sum.Sources) == 0 {
		b.WriteString(sBold.Render("No vCenter sources yet") + "\n")
		b.WriteString(sMuted.Render("Add one to start collecting. Use a vCenter account with the Read-only role.") + "\n\n")
		b.WriteString(keys("a", "add vCenter", "q", "quit"))
		return b.String()
	}
	b.WriteString(sBold.Render("vCenter sources") + "\n\n")
	b.WriteString(sMuted.Render(fmt.Sprintf("  %-30s %-14s %-20s %-16s %-18s %s", "vCenter", "Status", "Progress", "vCPU", "Memory", "Findings")) + "\n")
	for i, s := range m.sum.Sources {
		cursor := "  "
		if i == m.sel {
			cursor = sAccent.Render("▸ ")
		}
		name := s.Config.Host
		if len([]rune(name)) > 30 {
			name = string([]rune(name)[:29]) + "…"
		}
		vcpu, mem := "–", "–"
		if t := s.Totals; t != nil {
			vcpu = fmt.Sprintf("%d → %d", t.VCPU, t.RecVCPU)
			mem = fmt.Sprintf("%s → %s", analysis.GiB(t.MemMB), analysis.GiB(t.RecMemMB))
		}
		line := fmt.Sprintf("%-30s %s %-20s %-16s %-18s %d", name, pad(phaseBadge(s.Phase), 14), progressText(s), vcpu, mem, s.Findings)
		if i == m.sel {
			line = sBold.Render(line)
		}
		b.WriteString(cursor + line + "\n")
	}
	if len(m.sum.Shares) > 0 {
		b.WriteString("\n" + sharesBox(m.sum.Shares, m.sum.Sources) + "\n")
	}
	if u := m.sum.Upgrade; u != nil && time.Since(u.Time) < 24*time.Hour {
		msg := fmt.Sprintf("Upgrade to %s: %s", u.Version, u.State)
		if u.Message != "" {
			msg += " (" + u.Message + ")"
		}
		if u.State == "failed" || u.State == "rolled back" {
			b.WriteString("\n" + sBad.Render("✗ "+msg) + "\n")
		} else {
			b.WriteString("\n" + sMuted.Render("↑ "+msg) + "\n")
		}
	}
	b.WriteString("\n")
	k := []string{"↑/↓", "select", "enter", "open", "a", "add vCenter", "p", "combined PDF", "x", "exclusions"}
	if len(m.sum.Shares) > 0 {
		k = append(k, "s", "stop sharing")
	}
	if m.opt.AdminSettings {
		k = append(k, "c", "change password")
	}
	if m.updateAvailable() {
		k = append(k, "u", "upgrade")
	}
	b.WriteString(keys(append(k, "q", "quit")...))
	b.WriteString("\n" + sMuted.Render("Leaving the console does not stop collection."))
	return b.String()
}

func pad(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

func progressText(s engine.Status) string {
	switch s.Phase {
	case engine.Done:
		return "finished " + s.Finished.Format("Jan 02")
	case engine.NeedPassword:
		return "needs password"
	}
	total := s.Ends.Sub(s.Started)
	if total <= 0 {
		return ""
	}
	frac := min(float64(time.Since(s.Started))/float64(total), 1)
	out := fmt.Sprintf("%3.0f%% · %s left", frac*100, human(time.Until(s.Ends)))
	if s.Preview {
		out = fmt.Sprintf("%3.0f%% · preview", frac*100)
	}
	return out
}

func sharesBox(shares []report.Share, sources []engine.Status) string {
	names := map[string]string{"all": "All sources"}
	for _, s := range sources {
		names[s.ID] = s.Config.Host
	}
	var b strings.Builder
	b.WriteString(sBold.Render("Shared reports"))
	for _, sh := range shares {
		b.WriteString(fmt.Sprintf("\n%s  %s\n%s", sBold.Render(names[sh.Source]),
			sMuted.Render(fmt.Sprintf("expires %s · %d downloads", sh.Expires.Format("Jan 02 15:04"), sh.Downloads)),
			sAccent.Render(sh.URL)))
	}
	b.WriteString("\n" + sMuted.Render("Self-signed TLS, SHA-256 "+shares[0].Fingerprint))
	return sBox.BorderForeground(accent).Render(b.String())
}

func (m Model) shares(id string) []report.Share {
	var out []report.Share
	if m.sum != nil {
		for _, sh := range m.sum.Shares {
			if sh.Source == id {
				out = append(out, sh)
			}
		}
	}
	return out
}

func (m Model) viewSetup() string {
	var b strings.Builder
	b.WriteString(sBold.Render("Add a vCenter source") + "\n")
	note := "The password is kept in memory only. A read-only vCenter role is enough."
	if m.sum != nil && m.sum.Vault.Enabled {
		note = "The password is stored encrypted with the administrator password. A read-only vCenter role is enough."
	}
	b.WriteString(sMuted.Render(note) + "\n\n")
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
	b.WriteString(keys("↑/↓", "move", "←/→", "change", "enter", "next/start", "esc", "back"))
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
	b.WriteString(sMuted.Render("This is normal for self-signed VMCA certificates. Compare the fingerprint with the one shown in vCenter before you trust it.") + "\n\n")
	kv := func(k, v string) { b.WriteString(sLabel.Render(k) + v + "\n") }
	kv("Host", c.Host)
	kv("Subject", c.Subject)
	kv("Issuer", c.Issuer)
	kv("Expires", c.NotAfter.Format("2006-01-02"))
	kv("SHA-256", "")
	b.WriteString(sBold.Render(wrapFP(c.Fingerprint)) + "\n\n")
	b.WriteString("The fingerprint is pinned for this source. Any other certificate will be refused.\n\n")
	b.WriteString(keys("y", "trust and start", "n", "back"))
	return b.String()
}

func wrapFP(fp string) string {
	if len(fp) > 48 {
		return fp[:48] + "\n" + fp[48:]
	}
	return fp
}

func (m Model) viewResume() string {
	var b strings.Builder
	b.WriteString(sWarn.Render("Analysis paused") + "\n")
	b.WriteString(sMuted.Render("vCenter rejected the saved session or the engine restarted. Enter the vCenter password to continue collecting.") + "\n\n")
	if s := m.src; s != nil {
		b.WriteString(sLabel.Render("vCenter") + s.Config.Host + "\n")
		b.WriteString(sLabel.Render("Username") + s.Config.User + "\n")
		b.WriteString(sLabel.Render("Window") + fmt.Sprintf("%s → %s", s.Started.Format("Jan 02 15:04"), s.Ends.Format("Jan 02 15:04")) + "\n")
		if s.LastError != "" {
			b.WriteString(sLabel.Render("Last error") + sBad.Render(s.LastError) + "\n")
		}
	}
	b.WriteString("\n" + sFocus.Width(16).Render("› Password") + m.pass.View() + "\n\n")
	b.WriteString(keys("enter", "resume", "esc", "back"))
	return b.String()
}

func (m Model) viewSettings() string {
	var b strings.Builder
	b.WriteString(sBold.Render("Change administrator password") + "\n")
	b.WriteString(sMuted.Render("Used for console logins and to encrypt stored vCenter credentials. At least 12 characters.") + "\n\n")
	labels := []string{"Current", "New", "Repeat new"}
	for i, l := range labels {
		lab := sLabel.Render(l)
		if i == m.pwFocus {
			lab = sFocus.Width(16).Render("› " + l)
		}
		b.WriteString(lab + m.pw[i].View() + "\n")
	}
	b.WriteString("\n" + keys("↑/↓", "move", "enter", "next/save", "esc", "cancel"))
	return b.String()
}

func (m Model) viewUpdate() string {
	var b strings.Builder
	b.WriteString(sBold.Render("Update available") + "\n\n")
	b.WriteString(sLabel.Render("Installed") + m.opt.Version + "\n")
	b.WriteString(sLabel.Render("Latest") + sAccent.Render(m.opt.Latest) + "\n")
	if m.opt.ReleaseURL != "" {
		b.WriteString(sLabel.Render("Release notes") + m.opt.ReleaseURL + "\n")
	}
	b.WriteString("\n")
	steps := []string{
		"Download the new version while collection keeps running",
		"Stop the engine cleanly so every sample is saved",
		"Back up collected data and reports",
		"Start the new version and check that the data loaded",
		"Roll back automatically if anything fails",
	}
	for i, s := range steps {
		b.WriteString(sMuted.Render(fmt.Sprintf("  %d. ", i+1)) + s + "\n")
	}
	if m.sum != nil {
		for _, s := range m.sum.Sources {
			if s.Phase == engine.Running || s.Phase == engine.NeedPassword {
				msg := "Running analyses continue after the upgrade."
				if m.opt.Appliance {
					msg += " Log in again afterwards so collection resumes with the stored credentials."
				}
				if !m.sum.Vault.Enabled {
					msg += " You will be asked for their vCenter passwords again."
				}
				b.WriteString("\n" + sWarn.Render(msg) + "\n")
				b.WriteString(sMuted.Render("vCenter keeps one hour of real-time samples, so a short upgrade leaves no gap.") + "\n")
				break
			}
		}
	}
	b.WriteString("\n" + sBold.Render("Upgrade now?") + " " + sAccent.Render("[Y/n]"))
	return b.String()
}

func (m Model) viewSource() string {
	s := m.src
	if s == nil {
		return m.spin.View() + " Loading…"
	}
	var b strings.Builder
	b.WriteString(m.progressLine() + "\n\n")
	if r := s.Result; r != nil {
		b.WriteString(kpis(r) + "\n")
	} else {
		b.WriteString(m.spin.View() + sMuted.Render(" Waiting for the first samples…") + "\n\n")
	}
	if s.Result != nil && s.Result.Preview {
		b.WriteString(sWarn.Render("Preview based on vCenter history (5-minute to 2-hour averages): peaks are smoothed and read low.") + "\n")
		b.WriteString(sMuted.Render("Each VM switches to 20-second data once it has 24 hours of it.") + "\n\n")
	}
	if sh := m.shares(s.ID); len(sh) > 0 {
		b.WriteString(sharesBox(sh, []engine.Status{*s}) + "\n")
	}
	tabs := []string{"1 Clusters", "2 Findings", "3 Peaks"}
	for i, t := range tabs {
		if i == m.tab {
			tabs[i] = sTabOn.Render(t)
		} else {
			tabs[i] = sTabOff.Render(t)
		}
	}
	b.WriteString(strings.Join(tabs, "   ") + sMuted.Render(fmt.Sprintf("   (%d findings)", s.Findings)) + "\n\n")
	switch m.tab {
	case 0:
		b.WriteString(clusters(s.Result))
		if s.History != "" {
			b.WriteString("\n\n" + sMuted.Render("vCenter history: "+s.History))
		}
		if s.Result != nil && s.Result.WasteNote != "" {
			b.WriteString("\n" + sWarn.Render(s.Result.WasteNote))
		}
	case 1:
		b.WriteString(m.tbl.View())
	case 2:
		b.WriteString(peaksView(s.Result))
	}
	b.WriteString("\n\n")
	switch m.confirm {
	case "finish":
		b.WriteString(sWarn.Render("Stop collecting now and build the final report? [y/N]"))
		return b.String()
	case "remove":
		b.WriteString(sBad.Render("Remove this source with all its data and reports? [y/N]"))
		return b.String()
	}
	k := []string{"esc", "back", "1-3", "tabs", "p", "PDF"}
	if m.tab == 1 {
		k = append(k, "e", "exclude")
	}
	if len(m.shares(s.ID)) > 0 {
		k = append(k, "s", "stop sharing")
	}
	switch s.Phase {
	case engine.NeedPassword:
		k = append(k, "r", "resume", "f", "finish now")
	case engine.Running:
		k = append(k, "f", "finish now")
	}
	k = append(k, "x", "remove")
	if m.updateAvailable() {
		k = append(k, "u", "upgrade")
	}
	b.WriteString(keys(k...))
	return b.String()
}

func (m Model) progressLine() string {
	s := m.src
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
	case engine.NeedPassword:
		sub = append(sub, "paused, press r to resume")
	case engine.Done:
		sub = append(sub, "finished "+s.Finished.Format("Mon Jan 02 15:04"))
	}
	sub = append(sub, fmt.Sprintf("%d polls", s.Polls), s.Config.Profile+" profile")
	out := line + "\n" + sMuted.Render(strings.Join(sub, " · "))
	if s.LastError != "" {
		out += "\n" + sWarn.Render("⚠ "+s.LastError)
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
	head := fmt.Sprintf("%-22s %6s %9s %7s %13s %19s %6s %6s  %s", "Cluster", "Hosts", "CPU p/pk", "Mem p", "vCPU", "Memory", "Need", "Div", "CPU trend")
	rows := []string{sMuted.Render(head)}
	for _, c := range r.Clusters {
		name := c.Name
		if len([]rune(name)) > 22 {
			name = string([]rune(name)[:21]) + "…"
		}
		need := fmt.Sprintf("%6d", c.HostsNeeded)
		switch {
		case c.HostsNeeded < c.Hosts:
			need = sAccent.Render(need)
		case c.HostsNeeded > c.Hosts:
			need = sWarn.Render(need)
		}
		div := "–"
		if c.Peaks != nil && c.Peaks.Diversity > 0 {
			div = fmt.Sprintf("%.1f×", c.Peaks.Diversity)
		}
		rows = append(rows, fmt.Sprintf("%-22s %6d %9s %7s %13s %19s %s %6s  %s",
			name, c.Hosts,
			fmt.Sprintf("%.0f/%.0f%%", c.CPUP, c.CPUPeak), fmt.Sprintf("%.0f%%", c.MemP),
			fmt.Sprintf("%d→%d", c.VCPU, c.RecVCPU),
			fmt.Sprintf("%s→%s", analysis.GiB(c.MemMB), analysis.GiB(c.RecMemMB)),
			need, div, sAccent.Render(spark(c.Points, c.CapMHz))))
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

var shades = []rune(" ░▒▓█")

func peaksView(r *analysis.Result) string {
	if r == nil {
		return sMuted.Render("No data yet.")
	}
	var b strings.Builder
	for _, c := range r.Clusters {
		pk := c.Peaks
		if pk == nil {
			continue
		}
		b.WriteString(sBold.Render(c.Name) + "\n")
		b.WriteString(fmt.Sprintf("  Sum of VM peaks %s · combined peak %s · diversity %s\n",
			ghz(pk.SumPeakMHz), ghz(pk.CombinedPeakMHz), sAccent.Render(fmt.Sprintf("%.2f×", pk.Diversity))))
		b.WriteString(fmt.Sprintf("  Hosts for CPU: %d if sized on the sum of peaks, %s peak-aware\n", pk.NaiveHosts, sAccent.Render(fmt.Sprint(pk.AwareHosts))))
		b.WriteString(sMuted.Render("        00    03    06    09    12    15    18    21") + "\n")
		for d, day := range []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"} {
			var row strings.Builder
			for h := 0; h < 24; h++ {
				i := 0
				if pk.HeatmapN[d][h] > 0 {
					i = 1 + int(min(pk.Heatmap[d][h]/100, 0.999)*float64(len(shades)-1))
				}
				row.WriteString(strings.Repeat(string(shades[min(i, len(shades)-1)]), 2))
			}
			b.WriteString("  " + sMuted.Render(day) + "   " + sAccent.Render(row.String()) + "\n")
		}
		for _, g := range pk.CoPeak {
			b.WriteString("  " + sWarn.Render("Peak together on "+g.Host+": ") + strings.Join(g.VMs, ", ") + "\n")
		}
		for i, p := range pk.Complementary {
			if i == 3 {
				break
			}
			b.WriteString("  " + sMuted.Render(fmt.Sprintf("Good host mates (r %.2f): ", p.R)) + p.A + " + " + p.B + "\n")
		}
		b.WriteString("\n")
	}
	if b.Len() == 0 {
		return sMuted.Render("Peak analysis needs at least an hour of data.")
	}
	return b.String()
}

func ghz(mhz float64) string { return fmt.Sprintf("%.0f GHz", mhz/1000) }
