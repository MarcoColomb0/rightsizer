package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
	"github.com/MarcoColomb0/rightsizer/internal/engine"
	"github.com/MarcoColomb0/rightsizer/internal/ipc"
	"github.com/MarcoColomb0/rightsizer/internal/vc"
	"github.com/MarcoColomb0/rightsizer/internal/version"
)

type screen int

const (
	scrLoading screen = iota
	scrHome
	scrSetup
	scrCert
	scrBusy
	scrSource
	scrResume
	scrSettings
	scrUpdate
)

// ExitUpgrade tells the host launcher that the user asked to upgrade.
const ExitUpgrade = 42

const (
	fHost = iota
	fUser
	fPass
	fDuration
	fProfile
	fClusters
	fStart
	fCount
)

var durations = []struct {
	label string
	d     time.Duration
}{
	{"24 hours", 24 * time.Hour},
	{"3 days", 72 * time.Hour},
	{"7 days", 7 * 24 * time.Hour},
	{"14 days (recommended)", 14 * 24 * time.Hour},
}

type (
	summaryMsg struct {
		s   *engine.Summary
		err error
	}
	sourceMsg struct {
		s   *engine.Status
		err error
	}
	probeMsg struct {
		c   *vc.CertInfo
		err error
	}
	doneMsg struct {
		what string
		id   string
		err  error
	}
	tickMsg time.Time
)

type Options struct {
	Version       string
	Latest        string
	ReleaseURL    string
	CanUpgrade    bool
	AdminSettings bool
	// Appliance upgrades are performed by the host after a request; the
	// console does not exit.
	Appliance bool
}

type Model struct {
	b    ipc.Backend
	opt  Options
	sum  *engine.Summary
	src  *engine.Status
	cur  string
	sel  int
	scr  screen
	back screen
	w, h int
	err  string
	note string

	in      [4]textinput.Model
	focus   int
	durIdx  int
	profIdx int
	cert    *vc.CertInfo

	spin    spinner.Model
	busy    string
	tab     int
	tbl     table.Model
	prog    progress.Model
	confirm string
	pass    textinput.Model
	pw      [3]textinput.Model
	pwFocus int

	asked, upgrade bool
}

func New(b ipc.Backend, opt Options) Model {
	m := Model{b: b, opt: opt, durIdx: 3, profIdx: 1}
	ph := []string{"vcenter.example.local", "readonly@vsphere.local", "", "all clusters (or: prod-01, prod-02)"}
	for i := range m.in {
		m.in[i] = input(ph[i], i == fPass)
	}
	for i := range m.pw {
		m.pw[i] = input("", true)
	}
	m.pass = input("", true)
	m.spin = spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(sAccent))
	m.prog = progress.New(progress.WithSolidFill(string(accent.Dark)), progress.WithoutPercentage())
	m.tbl = table.New(table.WithFocused(true))
	st := table.DefaultStyles()
	st.Header = st.Header.BorderStyle(lipgloss.NormalBorder()).BorderForeground(line).BorderBottom(true).Bold(true).Foreground(muted)
	st.Selected = st.Selected.Foreground(lipgloss.Color("0")).Background(accent).Bold(false)
	m.tbl.SetStyles(st)
	m.w, m.h = 100, 30
	m.resizeTable()
	return m
}

func input(placeholder string, secret bool) textinput.Model {
	t := textinput.New()
	t.Placeholder = placeholder
	t.CharLimit = 256
	t.Prompt = ""
	if secret {
		t.EchoMode = textinput.EchoPassword
		t.EchoCharacter = '•'
	}
	return t
}

func (m Model) UpgradeRequested() bool { return m.upgrade }

func (m Model) updateAvailable() bool {
	return m.opt.CanUpgrade && version.Newer(m.opt.Latest, m.opt.Version)
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.fetch(), m.spin.Tick, tick())
}

func tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) fetch() tea.Cmd {
	b, cur, scr := m.b, m.cur, m.scr
	cmds := []tea.Cmd{func() tea.Msg {
		s, err := b.Summary()
		return summaryMsg{s, err}
	}}
	if cur != "" && (scr == scrSource || scr == scrResume) {
		cmds = append(cmds, func() tea.Msg {
			s, err := b.Source(cur)
			return sourceMsg{s, err}
		})
	}
	return tea.Batch(cmds...)
}

func (m Model) busyCmd(label, what, id string, fn func() error) (tea.Model, tea.Cmd) {
	m.back, m.scr, m.busy, m.err = m.scr, scrBusy, label, ""
	return m, tea.Batch(m.spin.Tick, func() tea.Msg { return doneMsg{what, id, fn()} })
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.prog.Width = min(max(m.w-40, 20), 60)
		m.resizeTable()
		return m, nil
	case tickMsg:
		return m, tea.Batch(m.fetch(), tick())
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case summaryMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.sum = msg.s
		m.sel = min(m.sel, max(len(m.sum.Sources)-1, 0))
		if m.scr == scrLoading {
			m.scr = scrHome
			if !m.asked && m.updateAvailable() {
				m.asked = true
				m.scr = scrUpdate
			}
		}
		return m, nil
	case sourceMsg:
		if msg.err != nil {
			if m.scr == scrSource {
				m.scr, m.cur, m.src = scrHome, "", nil
			}
			return m, nil
		}
		m.src = msg.s
		m.fillTable()
		return m, nil
	case probeMsg:
		if msg.err != nil {
			m.scr, m.err = scrSetup, "Cannot reach vCenter: "+msg.err.Error()
			return m, nil
		}
		m.cert = msg.c
		if msg.c.Trusted {
			return m.add("")
		}
		m.scr = scrCert
		return m, nil
	case doneMsg:
		return m.done(msg)
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		switch m.scr {
		case scrHome:
			return m.keyHome(msg)
		case scrSetup:
			return m.keySetup(msg)
		case scrCert:
			return m.keyCert(msg)
		case scrSource:
			return m.keySource(msg)
		case scrResume:
			return m.keyResume(msg)
		case scrSettings:
			return m.keySettings(msg)
		case scrUpdate:
			return m.keyUpdate(msg)
		case scrLoading:
			if msg.String() == "q" {
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m Model) done(msg doneMsg) (tea.Model, tea.Cmd) {
	m.busy = ""
	if msg.err != nil {
		m.err = msg.err.Error()
		switch msg.what {
		case "add":
			m.scr = scrSetup
		case "resume":
			m.scr = scrResume
			m.pass.Focus()
		case "password":
			m.scr = scrSettings
		default:
			m.scr = m.back
		}
		return m, nil
	}
	m.err = ""
	switch msg.what {
	case "add":
		m.in[fPass].SetValue("")
		m.cur, m.scr, m.tab, m.src = msg.id, scrSource, 0, nil
		m.note = "Analysis started. You can leave the console; collection continues."
	case "resume":
		m.pass.SetValue("")
		m.scr = scrSource
	case "remove":
		m.cur, m.src, m.scr = "", nil, scrHome
	case "password":
		for i := range m.pw {
			m.pw[i].SetValue("")
		}
		m.scr, m.note = scrHome, "Administrator password changed."
	case "publish":
		m.scr, m.note = m.back, "Report published. The link is shown below."
	case "upgrade":
		m.opt.CanUpgrade = false
		m.scr, m.note = scrHome, "Upgrade to "+m.opt.Latest+" started. This session will close while the engine restarts; reconnect in about a minute. Collected data is backed up first."
	default:
		m.scr = m.back
	}
	return m, m.fetch()
}

func (m Model) selected() *engine.Status {
	if m.sum == nil || m.sel >= len(m.sum.Sources) {
		return nil
	}
	return &m.sum.Sources[m.sel]
}

func (m Model) keyHome(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.note = ""
	n := 0
	if m.sum != nil {
		n = len(m.sum.Sources)
	}
	switch k.String() {
	case "q", "esc":
		return m, tea.Quit
	case "up", "k":
		m.sel = max(m.sel-1, 0)
	case "down", "j":
		m.sel = min(m.sel+1, max(n-1, 0))
	case "enter":
		if s := m.selected(); s != nil {
			m.cur, m.src, m.scr, m.tab = s.ID, nil, scrSource, 0
			return m, m.fetch()
		}
	case "a":
		m.scr, m.err = scrSetup, ""
		m.setFocus(fHost)
	case "p":
		if n > 0 {
			return m.busyCmd("Building the combined PDF report…", "publish", "", func() error {
				_, err := m.b.Publish("")
				return err
			})
		}
	case "s":
		if m.sum != nil && len(m.sum.Shares) > 0 {
			b := m.b
			shares := m.sum.Shares
			return m, func() tea.Msg {
				for _, sh := range shares {
					if err := b.StopShare(sh.ID); err != nil {
						return doneMsg{"unshare", "", err}
					}
				}
				return doneMsg{"unshare", "", nil}
			}
		}
	case "c":
		if m.opt.AdminSettings {
			m.scr, m.pwFocus, m.err = scrSettings, 0, ""
			m.focusPw()
		}
	case "u":
		if m.updateAvailable() {
			m.scr = scrUpdate
		}
	}
	return m, nil
}

func (m Model) keySetup(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.scr, m.err = scrHome, ""
		return m, nil
	case "tab", "down":
		m.setFocus((m.focus + 1) % fCount)
		return m, nil
	case "shift+tab", "up":
		m.setFocus((m.focus + fCount - 1) % fCount)
		return m, nil
	case "left", "right":
		d := 1
		if k.String() == "left" {
			d = -1
		}
		switch m.focus {
		case fDuration:
			m.durIdx = (m.durIdx + d + len(durations)) % len(durations)
			return m, nil
		case fProfile:
			m.profIdx = (m.profIdx + d + len(analysis.Profiles)) % len(analysis.Profiles)
			return m, nil
		}
	case "enter":
		if m.focus != fStart {
			m.setFocus(m.focus + 1)
			return m, nil
		}
		host := strings.TrimSpace(m.in[fHost].Value())
		if host == "" || strings.TrimSpace(m.in[fUser].Value()) == "" || m.in[fPass].Value() == "" {
			m.err = "vCenter, username and password are required."
			return m, nil
		}
		m.err, m.scr, m.busy = "", scrBusy, "Checking the vCenter certificate…"
		b := m.b
		return m, tea.Batch(m.spin.Tick, func() tea.Msg {
			c, err := b.Probe(host)
			return probeMsg{c, err}
		})
	}
	var cmd tea.Cmd
	if idx := m.inputIdx(); idx >= 0 {
		m.in[idx], cmd = m.in[idx].Update(k)
	}
	return m, cmd
}

func (m *Model) inputIdx() int {
	switch m.focus {
	case fHost, fUser, fPass:
		return m.focus
	case fClusters:
		return 3
	}
	return -1
}

func (m *Model) setFocus(f int) {
	m.focus = f
	for i := range m.in {
		m.in[i].Blur()
	}
	if idx := m.inputIdx(); idx >= 0 {
		m.in[idx].Focus()
	}
}

func (m Model) keyCert(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "y", "Y":
		return m.add(m.cert.Fingerprint)
	case "n", "N", "esc":
		m.scr, m.err = scrSetup, "Certificate not trusted; the source was not added."
	}
	return m, nil
}

func (m Model) add(fp string) (tea.Model, tea.Cmd) {
	var cl []string
	for _, s := range strings.Split(m.in[3].Value(), ",") {
		if s = strings.TrimSpace(s); s != "" {
			cl = append(cl, s)
		}
	}
	cfg := engine.Config{
		Host:        strings.TrimSpace(m.in[fHost].Value()),
		User:        strings.TrimSpace(m.in[fUser].Value()),
		Fingerprint: fp,
		Duration:    durations[m.durIdx].d,
		Profile:     analysis.Profiles[m.profIdx].Name,
		Clusters:    cl,
	}
	pw, b := m.in[fPass].Value(), m.b
	m.scr, m.busy = scrBusy, "Connecting to vCenter and reading the inventory…"
	return m, tea.Batch(m.spin.Tick, func() tea.Msg {
		id, err := b.Add(cfg, pw)
		return doneMsg{"add", id, err}
	})
}

func (m Model) keySource(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := k.String()
	m.note = ""
	id := m.cur
	if m.confirm != "" {
		what := m.confirm
		m.confirm = ""
		if key != "y" && key != "Y" {
			return m, nil
		}
		switch what {
		case "finish":
			return m.busyCmd("Finishing the analysis and building the report…", "finish", id, func() error { return m.b.Finish(id) })
		case "remove":
			return m.busyCmd("Removing the source and its data…", "remove", id, func() error { return m.b.Remove(id) })
		}
		return m, nil
	}
	ph := engine.Phase("")
	if m.src != nil {
		ph = m.src.Phase
	}
	switch key {
	case "esc", "backspace", "h", "left":
		m.scr, m.cur, m.src = scrHome, "", nil
		return m, m.fetch()
	case "q":
		return m, tea.Quit
	case "1":
		m.tab = 0
	case "2":
		m.tab = 1
	case "3":
		m.tab = 2
	case "tab":
		m.tab = (m.tab + 1) % 3
	case "p":
		return m.busyCmd("Building the PDF report…", "publish", id, func() error {
			_, err := m.b.Publish(id)
			return err
		})
	case "s":
		for _, sh := range m.shares(id) {
			b := m.b
			shID := sh.ID
			return m, func() tea.Msg { return doneMsg{"unshare", id, b.StopShare(shID)} }
		}
	case "f":
		if ph == engine.Running || ph == engine.NeedPassword {
			m.confirm = "finish"
		}
	case "x":
		m.confirm = "remove"
	case "r":
		if ph == engine.NeedPassword {
			if m.sum != nil && m.sum.Vault.Enabled && !m.sum.Vault.Locked {
				return m.busyCmd("Resuming with stored credentials…", "resume", id, func() error { return m.b.Resume(id, "") })
			}
			m.scr, m.err = scrResume, ""
			m.pass.Focus()
		}
	case "u":
		if m.updateAvailable() {
			m.scr = scrUpdate
		}
	default:
		if m.tab == 1 {
			var cmd tea.Cmd
			m.tbl, cmd = m.tbl.Update(k)
			return m, cmd
		}
	}
	return m, nil
}

func (m Model) keyResume(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.scr, m.err = scrSource, ""
		m.pass.SetValue("")
		return m, nil
	case "enter":
		pw, id := m.pass.Value(), m.cur
		if pw == "" {
			return m, nil
		}
		return m.busyCmd("Reconnecting to vCenter…", "resume", id, func() error { return m.b.Resume(id, pw) })
	}
	var cmd tea.Cmd
	m.pass, cmd = m.pass.Update(k)
	return m, cmd
}

func (m *Model) focusPw() {
	for i := range m.pw {
		m.pw[i].Blur()
	}
	m.pw[m.pwFocus].Focus()
}

func (m Model) keySettings(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		for i := range m.pw {
			m.pw[i].SetValue("")
		}
		m.scr, m.err = scrHome, ""
		return m, nil
	case "tab", "down":
		m.pwFocus = (m.pwFocus + 1) % len(m.pw)
		m.focusPw()
		return m, nil
	case "shift+tab", "up":
		m.pwFocus = (m.pwFocus + len(m.pw) - 1) % len(m.pw)
		m.focusPw()
		return m, nil
	case "enter":
		if m.pwFocus < len(m.pw)-1 {
			m.pwFocus++
			m.focusPw()
			return m, nil
		}
		old, next, again := m.pw[0].Value(), m.pw[1].Value(), m.pw[2].Value()
		if next != again {
			m.err = "The new passwords do not match."
			return m, nil
		}
		return m.busyCmd("Re-encrypting stored credentials…", "password", "", func() error { return m.b.ChangePassword(old, next) })
	}
	var cmd tea.Cmd
	m.pw[m.pwFocus], cmd = m.pw[m.pwFocus].Update(k)
	return m, cmd
}

func (m Model) keyUpdate(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "y", "Y", "enter":
		if m.opt.Appliance {
			tag := m.opt.Latest
			m.back = scrHome
			return m.busyCmd("Requesting the upgrade…", "upgrade", "", func() error { return m.b.RequestUpgrade(tag) })
		}
		m.upgrade = true
		return m, tea.Quit
	case "n", "N", "esc":
		m.scr = scrHome
	}
	return m, nil
}

func (m *Model) resizeTable() {
	w := max(m.w-4, 60)
	vmW := max(w-12-24-44-8-8, 16)
	m.tbl.SetColumns([]table.Column{
		{Title: "Prio", Width: 6}, {Title: "VM", Width: vmW}, {Title: "Finding", Width: 22},
		{Title: "Current → Suggested", Width: 42}, {Title: "Conf.", Width: 6},
	})
	m.tbl.SetHeight(max(m.h-20, 5))
	m.tbl.SetWidth(w)
}

func (m *Model) fillTable() {
	if m.src == nil || m.src.Result == nil {
		m.tbl.SetRows(nil)
		return
	}
	rows := make([]table.Row, 0, len(m.src.Result.Findings))
	for _, f := range m.src.Result.Findings {
		rows = append(rows, table.Row{f.Severity.String(), f.VM, string(f.Kind), f.Current + " → " + f.Suggested, f.Confidence})
	}
	m.tbl.SetRows(rows)
}
