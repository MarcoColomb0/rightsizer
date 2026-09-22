package sshd

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	gossh "golang.org/x/crypto/ssh"
)

type hello struct{}

func (hello) Init() tea.Cmd                       { return tea.Quit }
func (hello) Update(tea.Msg) (tea.Model, tea.Cmd) { return hello{}, tea.Quit }
func (hello) View() string                        { return "console-ready" }

func start(t *testing.T) string {
	t.Helper()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
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
	go srv.ListenAndServe()
	t.Cleanup(func() { srv.srv.Close() })
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
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
}

func TestLockout(t *testing.T) {
	addr := start(t)
	for i := 0; i < maxFails; i++ {
		if _, err := dial(addr, User, "wrong"); err == nil {
			t.Fatal("wrong password accepted")
		}
	}
	if _, err := dial(addr, User, "right password here"); err == nil {
		t.Fatal("address must be locked out after repeated failures")
	}
}
