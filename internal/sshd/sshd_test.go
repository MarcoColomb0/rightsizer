package sshd

import (
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	gossh "golang.org/x/crypto/ssh"
)

type hello struct{}

func (hello) Init() tea.Cmd                       { return tea.Quit }
func (hello) Update(tea.Msg) (tea.Model, tea.Cmd) { return hello{}, tea.Quit }
func (hello) View() string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#2DD4BF")).Render("console-ready")
}

func start(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	srv, err := New(Config{
		Listen:      addr,
		HostKeyPath: t.TempDir() + "/host_ed25519",
		Login: func(pw string) error {
			if pw == "right password here" {
				return nil
			}
			return errors.New("no")
		},
		Program: func() tea.Model { return hello{} },
	})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.ListenAndServe() }()
	t.Cleanup(func() { srv.srv.Close() })
	for range 50 {
		var d net.Dialer
		if c, err := d.DialContext(t.Context(), "tcp", addr); err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return addr
}

func dial(addr, user, pw string) (*gossh.Client, error) {
	return gossh.Dial("tcp", addr, &gossh.ClientConfig{
		User:            user,
		Auth:            []gossh.AuthMethod{gossh.Password(pw)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
}

func TestAuthAndConsoleOnly(t *testing.T) {
	addr := start(t)
	if _, err := dial(addr, "root", "right password here"); err == nil {
		t.Fatal("only the admin user may log in")
	}
	c, err := dial(addr, User, "right password here")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	s, _ := c.NewSession()
	out, err := s.CombinedOutput("id")
	if err == nil || strings.Contains(string(out), "uid") {
		t.Fatalf("commands must be refused, got %q", out)
	}
	s, _ = c.NewSession()
	if err := s.RequestSubsystem("sftp"); err == nil {
		t.Fatal("sftp must be unavailable")
	}
	if _, err := c.Dial("tcp", "127.0.0.1:22"); err == nil {
		t.Fatal("port forwarding must be refused")
	}
	s, _ = c.NewSession()
	var buf strings.Builder
	s.Stdout = &buf
	if err := s.RequestPty("xterm-256color", 24, 80, gossh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	_ = s.Shell()
	_ = s.Wait()
	if !strings.Contains(buf.String(), "console-ready") {
		t.Fatalf("console not served over the PTY: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "\x1b[38;5;") {
		t.Fatalf("console must be rendered in colour over SSH: %q", buf.String())
	}

	s, _ = c.NewSession()
	out, err = s.CombinedOutput("")
	if err == nil || strings.Contains(string(out), "console-ready") {
		t.Fatalf("a session without a terminal must be refused: %q", out)
	}
}

func TestHostKeyIsKept(t *testing.T) {
	path := t.TempDir() + "/ssh/host_ed25519"
	a, err := hostKey(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := hostKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if gossh.FingerprintSHA256(a.PublicKey()) != gossh.FingerprintSHA256(b.PublicKey()) {
		t.Fatal("the host key must be reused, or clients see a changed fingerprint after restarts")
	}
	if a.PublicKey().Type() != gossh.KeyAlgoED25519 {
		t.Fatalf("want ed25519, got %s", a.PublicKey().Type())
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatal("host key must be private")
	}
}

func TestLockout(t *testing.T) {
	addr := start(t)
	for range maxFails {
		if _, err := dial(addr, User, "wrong"); err == nil {
			t.Fatal("wrong password accepted")
		}
	}
	if _, err := dial(addr, User, "right password here"); err == nil {
		t.Fatal("address must be locked out after repeated failures")
	}
}
