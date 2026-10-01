package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/MarcoColomb0/rightsizer/internal/demo"
)

// TestScreens walks every screen on the demo backend at several terminal
// sizes and checks that nothing spills past the terminal. With
// RIGHTSIZER_TUI_SHOTS set to a directory, each frame is also written there
// as ANSI text for a look with a terminal or freeze.
func TestScreens(t *testing.T) {
	dir := os.Getenv("RIGHTSIZER_TUI_SHOTS")
	for _, c := range []struct {
		w, h int
		dark bool
	}{{130, 40, true}, {100, 30, false}, {200, 55, true}} {
		size, dark := [2]int{c.w, c.h}, c.dark
		{
			shots := screens(t, size[0], size[1], dark)
			for name, frame := range shots {
				lines := strings.Split(frame, "\n")
				if len(lines) > size[1] {
					t.Errorf("%s at %dx%d: %d lines", name, size[0], size[1], len(lines))
				}
				for i, l := range lines {
					if w := lipgloss.Width(l); w > size[0] {
						t.Errorf("%s at %dx%d: line %d is %d wide", name, size[0], size[1], i, w)
					}
				}
				if dir != "" {
					theme := "dark"
					if !dark {
						theme = "light"
					}
					f := filepath.Join(dir, fmt.Sprintf("%s-%s-%dx%d.ansi", name, theme, size[0], size[1]))
					if err := os.WriteFile(f, []byte(frame), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
}

func screens(t *testing.T, w, h int, dark bool) map[string]string {
	t.Helper()
	out := map[string]string{}
	b := demo.New(demo.Options{Appliance: true})
	m := New(b, Options{Version: "v1.0.0", Latest: "v1.1.0", CanUpgrade: true, AdminSettings: true, Appliance: true})
	m.applyTheme(dark)
	m = send(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	snap := func(name string, mm Model) {
		out[name] = mm.View().Content
	}
	snap("loading", m)
	m = send(t, m, m.fetch()())
	snap("update", m)
	m = send(t, m, typ("n"))
	snap("home", m)
	snap("home-help", send(t, m, typ("?")))
	snap("restart", send(t, m, typ("R")))
	m = send(t, m, enter, m.fetch()())
	snap("overview", m)
	snap("findings", send(t, m, typ("2")))
	snap("search", send(t, m, typ("2"), typ("/"), typ("db")))
	snap("peaks", send(t, m, typ("3")))
	sz := send(t, m, typ("4"))
	snap("sizing", sz)
	snap("sizing-options", send(t, sz, typ("o")))
	snap("finish", send(t, m, typ("f")))
	snap("exclude", send(t, m, typ("2"), typ("e")))
	home := send(t, m, esc)
	snap("exclusions", send(t, home, typ("x")))
	snap("setup", send(t, home, typ("a")))
	snap("settings", send(t, home, typ("c")))
	paused := send(t, home, down, enter, m.fetch()())
	snap("paused", paused)
	snap("resume", send(t, paused, typ("r")))
	cert := send(t, home, typ("a"), typ("vcsa03.corp.local"), tab, typ("ro@vsphere.local"), tab, typ("secret"))
	for cert.focus != fStart {
		cert = send(t, cert, tab)
	}
	cert = send(t, cert, enter)
	snap("cert", cert)
	busy := home
	busy.scr, busy.busy = scrBusy, "Building the PDF report"
	snap("busy", busy)
	e := New(demo.New(demo.Options{Empty: true}), Options{Version: "v1.0.0"})
	e.applyTheme(dark)
	e = send(t, e, tea.WindowSizeMsg{Width: w, Height: h})
	snap("empty", send(t, e, e.fetch()()))
	return out
}
