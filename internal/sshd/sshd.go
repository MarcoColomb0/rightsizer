package sshd

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/atomicfile"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/ssh"
	"github.com/muesli/termenv"
	gossh "golang.org/x/crypto/ssh"
)

const (
	User        = "admin"
	maxSessions = 8
	failWindow  = 15 * time.Minute
	maxFails    = 5
)

type Config struct {
	Listen      string
	HostKeyPath string
	// Login verifies the administrator password and unlocks the vault.
	Login func(password string) error
	// Program builds the console for a new session.
	Program func() tea.Model
}

// Server serves the console over SSH and nothing else: password login for a
// single administrator, a PTY is required, no commands, no subsystems, no
// forwarding, and brute-force attempts are throttled per address.
type Server struct {
	cfg      Config
	srv      *ssh.Server
	mu       sync.Mutex
	fails    map[string][]time.Time
	sessions int
}

func New(cfg Config) (*Server, error) {
	signer, err := hostKey(cfg.HostKeyPath)
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, fails: map[string][]time.Time{}}
	s.srv = &ssh.Server{
		Addr:            cfg.Listen,
		Version:         "rightsizer",
		Banner:          "rightsizer appliance - authorized access only\n",
		Handler:         s.handle,
		PasswordHandler: s.password,
		IdleTimeout:     30 * time.Minute,
		MaxTimeout:      12 * time.Hour,
		ServerConfigCallback: func(ssh.Context) *gossh.ServerConfig {
			return &gossh.ServerConfig{
				Config: gossh.Config{
					KeyExchanges: []string{"mlkem768x25519-sha256", "curve25519-sha256", "curve25519-sha256@libssh.org"},
					Ciphers:      []string{"chacha20-poly1305@openssh.com", "aes256-gcm@openssh.com", "aes128-gcm@openssh.com"},
					MACs:         []string{"hmac-sha2-256-etm@openssh.com", "hmac-sha2-512-etm@openssh.com"},
				},
				MaxAuthTries: 3,
			}
		},
	}
	s.srv.AddHostKey(signer)
	// Sessions render to the SSH client, not to the daemon's stdout, so
	// colour support cannot be detected from the process; every current SSH
	// terminal handles 256 colours.
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	return s, nil
}

// hostKey loads the Ed25519 host key, creating it on first start.
func hostKey(path string) (gossh.Signer, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		block, err := gossh.MarshalPrivateKey(priv, "")
		if err != nil {
			return nil, err
		}
		b = pem.EncodeToMemory(block)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		if err := atomicfile.WriteFile(path, b, 0o600); err != nil {
			return nil, err
		}
		pub, err := gossh.NewPublicKey(priv.Public())
		if err != nil {
			return nil, err
		}
		_ = os.WriteFile(path+".pub", gossh.MarshalAuthorizedKey(pub), 0o600)
	} else if err != nil {
		return nil, err
	}
	return gossh.ParsePrivateKey(b)
}

func (s *Server) ListenAndServe() error {
	slog.Info("console available over SSH", "address", s.cfg.Listen, "user", User)
	if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, ssh.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error { return s.srv.Shutdown(ctx) }

func host(addr net.Addr) string {
	h, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	return h
}

func (s *Server) password(ctx ssh.Context, password string) bool {
	ip := host(ctx.RemoteAddr())
	if s.blocked(ip) {
		slog.Warn("ssh login refused, too many failures", "remote", ip)
		time.Sleep(2 * time.Second)
		return false
	}
	if ctx.User() == User && len(password) <= 1024 && s.cfg.Login(password) == nil {
		s.mu.Lock()
		delete(s.fails, ip)
		s.mu.Unlock()
		slog.Info("ssh login", "remote", ip)
		return true
	}
	s.mu.Lock()
	s.fails[ip] = append(s.fails[ip], time.Now())
	s.mu.Unlock()
	slog.Warn("ssh login failed", "remote", ip, "user", ctx.User())
	time.Sleep(time.Second)
	return false
}

func (s *Server) blocked(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cut := time.Now().Add(-failWindow)
	recent := s.fails[ip][:0]
	for _, t := range s.fails[ip] {
		if t.After(cut) {
			recent = append(recent, t)
		}
	}
	if len(recent) == 0 {
		delete(s.fails, ip)
		return false
	}
	s.fails[ip] = recent
	return len(recent) >= maxFails
}

func refuse(sess ssh.Session, msg string) {
	fmt.Fprintln(sess.Stderr(), msg)
	_ = sess.Exit(1)
}

// handle runs the console for one session. Commands, subsystems and
// sessions without a terminal are refused before anything else happens.
func (s *Server) handle(sess ssh.Session) {
	if sess.RawCommand() != "" || sess.Subsystem() != "" {
		refuse(sess, "only the interactive console is available")
		return
	}
	pty, windows, ok := sess.Pty()
	if !ok {
		refuse(sess, "the console needs an interactive terminal (ssh -t)")
		return
	}
	s.mu.Lock()
	if s.sessions >= maxSessions {
		s.mu.Unlock()
		refuse(sess, "too many open sessions")
		return
	}
	s.sessions++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.sessions--
		s.mu.Unlock()
	}()

	p := tea.NewProgram(s.cfg.Program(), tea.WithInput(sess), tea.WithOutput(sess), tea.WithAltScreen())
	ctx, cancel := context.WithCancel(sess.Context())
	defer cancel()
	go func() {
		p.Send(tea.WindowSizeMsg{Width: pty.Window.Width, Height: pty.Window.Height})
		for {
			select {
			case <-ctx.Done():
				p.Quit()
				return
			case w, ok := <-windows:
				if !ok {
					return
				}
				p.Send(tea.WindowSizeMsg{Width: w.Width, Height: w.Height})
			}
		}
	}()
	if _, err := p.Run(); err != nil {
		slog.Warn("console session ended with an error", "err", err)
	}
	p.Kill()
	_ = sess.Exit(0)
}
