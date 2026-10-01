package tui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/paginator"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/stopwatch"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/timer"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

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
	scrExclude
	scrExclusions
	scrSizing
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

// Source screen tabs.
const (
	tabClusters = iota
	tabFindings
	tabPeaks
	tabSizing
	tabCount
)

var tabNames = []string{"Overview", "Findings", "Peaks", "Sizing"}

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
	th   theme
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

	spin     spinner.Model
	busy     string
	watch    stopwatch.Model
	tab      int
	tbl      findingsTable
	vp       viewport.Model
	pager    paginator.Model
	poll     timer.Model
	pollAt   time.Time
	help     help.Model
	fullHelp bool
	confirm  string
	pass     textinput.Model
	pw       [3]textinput.Model
	pwFocus  int

	asked, upgrade bool

	ex     excludeForm
	exNote textarea.Model
	exName textinput.Model
	excl   []analysis.Exclusion
	exList list.Model

	srch   search
	srchIn textinput.Model

	sz    *analysis.Sizing
	szID  string
	szErr string
	szAt  time.Time
	szf   sizingForm
	szIn  [8]textinput.Model
}

func New(b ipc.Backend, opt Options) Model {
	m := Model{b: b, opt: opt, durIdx: 3, profIdx: 1, w: 120, h: 40}
	ph := []string{"vcenter.example.local", "readonly@vsphere.local", "", "all clusters (or: prod-01, prod-02)"}
	for i := range m.in {
		m.in[i] = input(ph[i], i == fPass)
	}
	for i := range m.pw {
		m.pw[i] = input("", true)
	}
	m.pass = input("", true)
	m.exNote = textarea.New()
	m.exNote.Placeholder = "e.g. vendor sizing guide requires 16 vCPU / 64 GB"
	m.exNote.CharLimit = 500
	m.exNote.ShowLineNumbers = false
	m.exNote.SetHeight(3)
	m.exNote.KeyMap.InsertNewline.SetKeys("ctrl+j")
	m.exName = input("citrix-*", false)
	m.srchIn = newSearchInput()
	for i := range m.szIn {
		m.szIn[i] = input("", false)
		m.szIn[i].CharLimit = 16
		m.szIn[i].SetWidth(10)
	}
	m.szIn[szInput[soGroups]].CharLimit = 1000
	m.szIn[szInput[soGroups]].SetWidth(48)
	m.szIn[szInput[soGroups]].Placeholder = "none: group by guest OS"
	m.spin = spinner.New(spinner.WithSpinner(spinner.MiniDot))
	m.watch = stopwatch.New(stopwatch.WithInterval(100 * time.Millisecond))
	m.vp = viewport.New()
	m.vp.SoftWrap = false
	m.help = help.New()
	m.pager = paginator.New()
	m.pager.Type = paginator.Dots
	m.exList = newExclusionList()
	m.applyTheme(true)
	m.layout()
	return m
}

// applyTheme rebuilds every style once the terminal background is known.
func (m *Model) applyTheme(dark bool) {
	m.th = newTheme(dark)
	t := m.th
	m.spin.Style = t.acc
	m.help.Styles = help.DefaultStyles(dark)
	m.help.Styles.ShortKey = t.key
	m.help.Styles.FullKey = t.key
	m.help.Styles.ShortDesc = t.keyDesc
	m.help.Styles.FullDesc = t.keyDesc
	m.pager.ActiveDot = t.acc.Render("●")
	m.pager.InactiveDot = t.faint.Render("●")
	for _, ti := range m.allInputs() {
		s := textinput.DefaultStyles(dark)
		s.Focused.Text = t.bold
		s.Focused.Placeholder = t.faint
		s.Blurred.Placeholder = t.faint
		s.Blurred.Text = t.bold.UnsetBold()
		s.Cursor.Color = t.accent
		ti.SetStyles(s)
	}
	ts := textarea.DefaultStyles(dark)
	ts.Focused.Base = ts.Focused.Base.BorderForeground(t.accent)
	ts.Focused.CursorLine = ts.Focused.CursorLine.UnsetBackground()
	m.exNote.SetStyles(ts)
	styleExclusionList(&m.exList, t)
}

func (m *Model) allInputs() []*textinput.Model {
	out := []*textinput.Model{&m.pass, &m.exName, &m.srchIn}
	for i := range m.in {
		out = append(out, &m.in[i])
	}
	for i := range m.pw {
		out = append(out, &m.pw[i])
	}
	for i := range m.szIn {
		out = append(out, &m.szIn[i])
	}
	return out
}

func input(placeholder string, secret bool) textinput.Model {
	t := textinput.New()
	t.Placeholder = placeholder
	t.CharLimit = 256
	t.Prompt = ""
	t.SetWidth(48)
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
	return tea.Batch(tea.RequestBackgroundColor, m.fetch(), m.spin.Tick, tick())
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
	if scr == scrSource && m.tab == tabSizing && time.Since(m.szAt) >= sizingEvery {
		cmds = append(cmds, m.fetchSizing())
	}
	return tea.Batch(cmds...)
}

func (m Model) busyCmd(label, what, id string, fn func() error) (tea.Model, tea.Cmd) {
	m.back, m.scr, m.busy, m.err = m.scr, scrBusy, label, ""
	m.watch = stopwatch.New(stopwatch.WithInterval(100 * time.Millisecond))
	return m, tea.Batch(m.spin.Tick, m.watch.Start(), func() tea.Msg { return doneMsg{what, id, fn()} })
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.layout()
		return m, nil
	case tea.BackgroundColorMsg:
		m.applyTheme(msg.IsDark())
		m.syncViewport()
		return m, nil
	case tickMsg:
		return m, tea.Batch(m.fetch(), tick())
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case stopwatch.TickMsg, stopwatch.StartStopMsg, stopwatch.ResetMsg:
		var cmd tea.Cmd
		m.watch, cmd = m.watch.Update(msg)
		return m, cmd
	case timer.TickMsg, timer.StartStopMsg, timer.TimeoutMsg:
		var cmd tea.Cmd
		m.poll, cmd = m.poll.Update(msg)
		return m, cmd
	case summaryMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.sum = msg.s
		m.sel = min(m.sel, max(len(m.sum.Sources)-1, 0))
		m.layout()
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
		m.layout()
		return m, m.restartPollTimer()
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
	case sizingMsg:
		if msg.id != m.cur {
			return m, nil
		}
		m.szAt = time.Now()
		m.szID, m.szErr = msg.id, ""
		if msg.err != nil {
			m.sz, m.szErr = nil, msg.err.Error()
		} else {
			m.sz = msg.sz
		}
		m.syncViewport()
		return m, nil
	case sizingParamsMsg:
		return m.sizingParams(msg)
	case exclusionsMsg:
		if msg.err == nil {
			m.excl = msg.xs
			return m, m.exList.SetItems(exclusionItems(m.excl))
		}
		return m, nil
	case tea.MouseMsg:
		return m.mouse(msg)
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m.forward(msg)
}

// forward hands other messages, such as cursor blinks, to the focused
// field.
func (m Model) forward(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.scr {
	case scrSetup:
		if idx := m.inputIdx(); idx >= 0 {
			m.in[idx], cmd = m.in[idx].Update(msg)
		}
	case scrResume:
		m.pass, cmd = m.pass.Update(msg)
	case scrSettings:
		m.pw[m.pwFocus], cmd = m.pw[m.pwFocus].Update(msg)
	case scrExclude:
		switch m.ex.focus {
		case exTarget:
			m.exName, cmd = m.exName.Update(msg)
		case exNote:
			m.exNote, cmd = m.exNote.Update(msg)
		}
	case scrSizing:
		if i, ok := szInput[m.szf.focus]; ok {
			m.szIn[i], cmd = m.szIn[i].Update(msg)
		}
	case scrSource:
		if m.srch.prompt {
			m.srchIn, cmd = m.srchIn.Update(msg)
		}
	case scrExclusions:
		m.exList, cmd = m.exList.Update(msg)
	default:
	}
	return m, cmd
}

func (m Model) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if msg.String() == "?" && m.helpToggles() {
		m.fullHelp = !m.fullHelp
		m.layout()
		return m, nil
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
	case scrExclude:
		return m.keyExclude(msg)
	case scrExclusions:
		return m.keyExclusions(msg)
	case scrSizing:
		return m.keySizing(msg)
	case scrLoading:
		if msg.String() == "q" {
			return m, tea.Quit
		}
	case scrBusy:
		// Keys wait until the running operation finishes.
	}
	return m, nil
}

// helpToggles reports whether ? opens the full help here: in text fields
// and in the findings search it is a character or a search instead.
func (m Model) helpToggles() bool {
	switch m.scr {
	case scrHome, scrCert, scrUpdate:
		return true
	case scrSource:
		return m.tab != tabFindings && !m.srch.prompt
	case scrExclusions:
		return !m.exList.SettingFilter()
	default:
	}
	return false
}

// restartPollTimer counts down to the source's next sample.
func (m *Model) restartPollTimer() tea.Cmd {
	if m.src == nil || m.src.Phase != engine.Running || m.src.NextPoll.IsZero() || m.src.NextPoll.Equal(m.pollAt) {
		return nil
	}
	m.pollAt = m.src.NextPoll
	d := time.Until(m.pollAt).Round(time.Second)
	if d <= 0 {
		return nil
	}
	m.poll = timer.New(d, timer.WithInterval(time.Second))
	return m.poll.Start()
}

func (m Model) done(msg doneMsg) (tea.Model, tea.Cmd) {
	m.busy = ""
	stop := m.watch.Stop()
	if msg.what == "unshare" {
		// Stopping a share never leaves the current screen.
		if msg.err != nil {
			m.err = msg.err.Error()
		}
		return m, m.fetch()
	}
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
		case "exclude":
			m.scr = scrExclude
		case "sizing-params":
			m.scr = scrSizing
		default:
			m.scr = m.back
		}
		return m, stop
	}
	m.err = ""
	switch msg.what {
	case "add":
		m.in[fPass].SetValue("")
		m.cur, m.scr, m.tab, m.src = msg.id, scrSource, tabClusters, nil
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
		m.scr, m.note = m.back, "Report published. The download link is in the shared reports panel."
	case "exclude":
		m.scr, m.note = m.ex.ret, "Exclusion saved. Reports are updated."
		return m, tea.Batch(stop, m.fetch(), m.fetchExclusions())
	case "unexclude":
		m.scr, m.note = scrExclusions, "Exclusion removed."
		return m, tea.Batch(stop, m.fetch(), m.fetchExclusions())
	case "sizing-params":
		m.scr, m.note, m.szAt = m.szf.ret, "Sizing options saved.", time.Time{}
		if m.scr == scrSource {
			return m, tea.Batch(stop, m.fetch(), m.fetchSizing())
		}
	case "reboot":
		m.scr, m.note = scrHome, "The appliance is restarting. This session will close; reconnect in a few minutes and log in so collection resumes."
	case "upgrade":
		m.opt.CanUpgrade = false
		m.scr, m.note = scrHome, "Upgrade to "+m.opt.Latest+" started. This session will close while the engine restarts; reconnect in about a minute. Collected data is backed up first."
	default:
		m.scr = m.back
	}
	return m, tea.Batch(stop, m.fetch())
}

func (m Model) selected() *engine.Status {
	if m.sum == nil || m.sel >= len(m.sum.Sources) {
		return nil
	}
	return &m.sum.Sources[m.sel]
}

func (m Model) canReboot() bool {
	return m.opt.Appliance && m.sum != nil && len(m.sum.Reboot) > 0
}

func (m Model) openSource(i int) (tea.Model, tea.Cmd) {
	m.sel = i
	s := m.selected()
	if s == nil {
		return m, nil
	}
	m.cur, m.src, m.scr, m.tab = s.ID, nil, scrSource, tabClusters
	m.srch = search{}
	m.tbl.SetCursor(0)
	m.vp.GotoTop()
	m.layout()
	return m, m.fetch()
}

func (m Model) keyHome(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.note = ""
	if m.confirm == "reboot" {
		m.confirm = ""
		if k.String() == "y" || k.String() == "Y" {
			b := m.b
			return m.busyCmd("Requesting the restart", "reboot", "", func() error { return b.RequestReboot() })
		}
		return m, nil
	}
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
	case "pgup", "left", "h":
		m.sel = max(m.sel-max(m.pager.PerPage, 1), 0)
	case "pgdown", "right", "l":
		m.sel = min(m.sel+max(m.pager.PerPage, 1), max(n-1, 0))
	case "enter":
		return m.openSource(m.sel)
	case "a":
		m.scr, m.err = scrSetup, ""
		m.setFocus(fHost)
	case "p":
		if n > 0 {
			return m.busyCmd("Building the combined PDF report", "publish", "", func() error {
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
	case "x":
		m.scr, m.err = scrExclusions, ""
		m.exList.ResetFilter()
		return m, m.fetchExclusions()
	case "z":
		if n > 0 {
			b := m.b
			return m.busyCmd("Building the combined sizing PDF and data", "publish", "", func() error {
				_, err := b.PublishSizing("")
				return err
			})
		}
	case "o":
		return m.openSizingOptions(scrHome)
	case "c":
		if m.opt.AdminSettings {
			m.scr, m.pwFocus, m.err = scrSettings, 0, ""
			m.focusPw()
		}
	case "u":
		if m.updateAvailable() {
			m.scr = scrUpdate
		}
	case "R":
		if m.canReboot() {
			m.confirm = "reboot"
		}
	}
	m.pager.Page = m.sel / max(m.pager.PerPage, 1)
	return m, nil
}

func (m Model) keySetup(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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
		m.err, m.scr, m.busy = "", scrBusy, "Checking the vCenter certificate"
		m.watch = stopwatch.New(stopwatch.WithInterval(100 * time.Millisecond))
		b := m.b
		return m, tea.Batch(m.spin.Tick, m.watch.Start(), func() tea.Msg {
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

func (m Model) keyCert(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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
	for s := range strings.SplitSeq(m.in[3].Value(), ",") {
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
	m.scr, m.busy = scrBusy, "Connecting to vCenter and reading the inventory"
	m.watch = stopwatch.New(stopwatch.WithInterval(100 * time.Millisecond))
	return m, tea.Batch(m.spin.Tick, m.watch.Start(), func() tea.Msg {
		id, err := b.Add(cfg, pw)
		return doneMsg{"add", id, err}
	})
}

func (m Model) switchTab(t int) (tea.Model, tea.Cmd) {
	m.tab = t
	m.vp.GotoTop()
	m.layout()
	m.syncViewport()
	if t == tabSizing {
		return m, m.fetchSizing()
	}
	return m, nil
}

func (m Model) keySource(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.srch.prompt {
		return m.keySearchPrompt(k)
	}
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
			return m.busyCmd("Finishing the analysis and building the report", "finish", id, func() error { return m.b.Finish(id) })
		case "remove":
			return m.busyCmd("Removing the source and its data", "remove", id, func() error { return m.b.Remove(id) })
		}
		return m, nil
	}
	ph := engine.Phase("")
	if m.src != nil {
		ph = m.src.Phase
	}
	if m.tab == tabFindings {
		switch key {
		case "/", "?":
			return m.openSearch(key == "?")
		case "n", "N":
			if m.srch.query != "" {
				m.jump(m.tbl.Cursor(), m.srch.backward != (key == "N"))
			}
			return m, nil
		case "esc":
			if m.srch.query != "" {
				m.srch = search{}
				m.err = ""
				m.fillTable()
				return m, nil
			}
		}
	}
	switch key {
	case "esc", "backspace", "h":
		m.srch = search{}
		m.scr, m.cur, m.src = scrHome, "", nil
		m.layout()
		return m, m.fetch()
	case "q":
		return m, tea.Quit
	case "1", "2", "3", "4":
		return m.switchTab(int(key[0] - '1'))
	case "tab", "right", "l":
		return m.switchTab((m.tab + 1) % tabCount)
	case "shift+tab", "left":
		return m.switchTab((m.tab + tabCount - 1) % tabCount)
	case "o":
		return m.openSizingOptions(scrSource)
	case "p":
		if m.tab == tabSizing {
			return m.busyCmd("Building the sizing PDF and data", "publish", id, func() error {
				_, err := m.b.PublishSizing(id)
				return err
			})
		}
		return m.busyCmd("Building the PDF report", "publish", id, func() error {
			_, err := m.b.Publish(id)
			return err
		})
	case "s":
		if shares := m.shares(id); len(shares) > 0 {
			b := m.b
			return m, func() tea.Msg {
				for _, sh := range shares {
					if err := b.StopShare(sh.ID); err != nil {
						return doneMsg{"unshare", id, err}
					}
				}
				return doneMsg{"unshare", id, nil}
			}
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
				return m.busyCmd("Resuming with stored credentials", "resume", id, func() error { return m.b.Resume(id, "") })
			}
			m.scr, m.err = scrResume, ""
			m.pass.Focus()
		}
	case "u":
		if m.updateAvailable() {
			m.scr = scrUpdate
		}
	case "e":
		if m.tab == tabFindings {
			return m.openExclude()
		}
	default:
		if m.tab == tabFindings {
			m.tbl.key(key)
			return m, nil
		}
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(k)
		return m, cmd
	}
	return m, nil
}

func (m Model) keyResume(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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
		return m.busyCmd("Reconnecting to vCenter", "resume", id, func() error { return m.b.Resume(id, pw) })
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

func (m Model) keySettings(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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
		return m.busyCmd("Re-encrypting stored credentials", "password", "", func() error { return m.b.ChangePassword(old, next) })
	}
	var cmd tea.Cmd
	m.pw[m.pwFocus], cmd = m.pw[m.pwFocus].Update(k)
	return m, cmd
}

func (m Model) keyUpdate(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "y", "Y", "enter":
		if m.opt.Appliance {
			tag := m.opt.Latest
			m.back = scrHome
			return m.busyCmd("Requesting the upgrade", "upgrade", "", func() error { return m.b.RequestUpgrade(tag) })
		}
		m.upgrade = true
		return m, tea.Quit
	case "n", "N", "esc":
		m.scr = scrHome
	}
	return m, nil
}
