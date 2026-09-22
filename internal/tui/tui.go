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
)

type screen int

const (
	scrLoading screen = iota
	scrSetup
	scrCert
	scrBusy
	scrDash
	scrResume
)

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
	statusMsg struct {
		s   *engine.Status
		err error
	}
	probeMsg struct {
		c   *vc.CertInfo
		err error
	}
	doneMsg struct {
		what string
		err  error
	}
	tickMsg time.Time
)

type Model struct {
	c       *ipc.Client
	st      *engine.Status
	scr     screen
	w, h    int
	err     string
	note    string
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
}

func New(c *ipc.Client) Model {
	m := Model{c: c, durIdx: 3, profIdx: 1}
	ph := []string{"vcenter.example.local", "readonly@vsphere.local", "", "all clusters (or: prod-01, prod-02)"}
	for i := range m.in {
		t := textinput.New()
		t.Placeholder = ph[i]
		t.CharLimit = 256
		t.Prompt = ""
		m.in[i] = t
	}
	m.in[2].EchoMode = textinput.EchoPassword
	m.in[2].EchoCharacter = '•'
	m.in[0].Focus()
	m.pass = textinput.New()
	m.pass.Prompt = ""
	m.pass.EchoMode = textinput.EchoPassword
	m.pass.EchoCharacter = '•'
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

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.fetch, m.spin.Tick, tick())
}

func tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) fetch() tea.Msg {
	s, err := m.c.Status()
	return statusMsg{s, err}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.prog.Width = min(max(m.w-40, 20), 60)
		m.resizeTable()
		return m, nil
	case tickMsg:
		return m, tea.Batch(m.fetch, tick())
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case statusMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.st = msg.s
		if m.scr != scrBusy && m.scr != scrCert {
			m.route()
		}
		m.fillTable()
		return m, nil
	case probeMsg:
		if msg.err != nil {
			m.scr, m.err = scrSetup, "Cannot reach vCenter: "+msg.err.Error()
			return m, nil
		}
		m.cert = msg.c
		if msg.c.Trusted {
			return m.start("")
		}
		m.scr = scrCert
		return m, nil
	case doneMsg:
		m.busy = ""
		if msg.err != nil {
			m.err = msg.err.Error()
			switch msg.what {
			case "start":
				m.scr = scrSetup
			case "resume":
				m.scr = scrResume
			default:
				m.scr = scrDash
			}
			return m, nil
		}
		m.err = ""
		if msg.what == "start" || msg.what == "resume" {
			m.in[2].SetValue("")
			m.pass.SetValue("")
		}
		m.scr = scrLoading
		return m, m.fetch
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		switch m.scr {
		case scrSetup:
			return m.updateSetup(msg)
		case scrCert:
			return m.updateCert(msg)
		case scrResume:
			return m.updateResume(msg)
		case scrDash:
			return m.updateDash(msg)
		case scrLoading:
			if msg.String() == "q" {
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m *Model) route() {
	switch m.st.Phase {
	case engine.Idle:
		if m.scr != scrSetup {
			m.scr = scrSetup
			m.setFocus(fHost)
		}
	case engine.NeedPassword:
		if m.scr != scrResume {
			m.scr = scrResume
			m.pass.Focus()
		}
	default:
		m.scr = scrDash
	}
}

func (m Model) updateSetup(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		return m, tea.Quit
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
		m.err, m.scr, m.busy = "", scrBusy, "Checking vCenter certificate…"
		return m, tea.Batch(m.spin.Tick, func() tea.Msg {
			c, err := m.c.Probe(host)
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

func (m Model) updateCert(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "y", "Y":
		return m.start(m.cert.Fingerprint)
	case "n", "N", "esc":
		m.scr, m.err = scrSetup, "Certificate not trusted; analysis not started."
	}
	return m, nil
}

func (m Model) start(fp string) (tea.Model, tea.Cmd) {
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
	pw := m.in[fPass].Value()
	m.scr, m.busy = scrBusy, "Connecting to vCenter and reading inventory…"
	return m, tea.Batch(m.spin.Tick, func() tea.Msg {
		return doneMsg{"start", m.c.Start(cfg, pw)}
	})
}

func (m Model) updateResume(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		return m, tea.Quit
	case "enter":
		pw := m.pass.Value()
		if pw == "" {
			return m, nil
		}
		m.scr, m.busy = scrBusy, "Reconnecting…"
		return m, tea.Batch(m.spin.Tick, func() tea.Msg { return doneMsg{"resume", m.c.Resume(pw)} })
	case "ctrl+f":
		m.scr, m.busy = scrBusy, "Finishing with collected data…"
		return m, tea.Batch(m.spin.Tick, func() tea.Msg { return doneMsg{"finish", m.c.Finish()} })
	}
	var cmd tea.Cmd
	m.pass, cmd = m.pass.Update(k)
	return m, cmd
}

func (m Model) updateDash(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := k.String()
	if m.confirm != "" {
		what := m.confirm
		m.confirm = ""
		if key != "y" && key != "Y" {
			return m, nil
		}
		switch what {
		case "finish":
			m.scr, m.busy = scrBusy, "Finishing analysis and building the report…"
			return m, tea.Batch(m.spin.Tick, func() tea.Msg { return doneMsg{"finish", m.c.Finish()} })
		case "cancel":
			m.scr, m.busy = scrBusy, "Discarding analysis…"
			return m, tea.Batch(m.spin.Tick, func() tea.Msg { return doneMsg{"cancel", m.c.Cancel()} })
		}
		return m, nil
	}
	running := m.st != nil && m.st.Phase == engine.Running
	switch key {
	case "q", "esc":
		return m, tea.Quit
	case "1":
		m.tab = 0
		return m, nil
	case "2":
		m.tab = 1
		return m, nil
	case "tab":
		m.tab = 1 - m.tab
		return m, nil
	case "p":
		m.scr, m.busy = scrBusy, "Building PDF report…"
		return m, tea.Batch(m.spin.Tick, func() tea.Msg {
			_, err := m.c.Publish()
			return doneMsg{"publish", err}
		})
	case "s":
		if m.st != nil && m.st.Share != nil {
			return m, func() tea.Msg { return doneMsg{"unpublish", m.c.Unpublish()} }
		}
	case "f":
		if running {
			m.confirm = "finish"
		}
		return m, nil
	case "x":
		if running {
			m.confirm = "cancel"
		}
		return m, nil
	case "n":
		if m.st != nil && m.st.Phase == engine.Done {
			m.confirm = "cancel"
		}
		return m, nil
	}
	if m.tab == 1 {
		var cmd tea.Cmd
		m.tbl, cmd = m.tbl.Update(k)
		return m, cmd
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
	if m.st == nil || m.st.Result == nil {
		m.tbl.SetRows(nil)
		return
	}
	rows := make([]table.Row, 0, len(m.st.Result.Findings))
	for _, f := range m.st.Result.Findings {
		rows = append(rows, table.Row{f.Severity.String(), f.VM, string(f.Kind), f.Current + " → " + f.Suggested, f.Confidence})
	}
	m.tbl.SetRows(rows)
}
