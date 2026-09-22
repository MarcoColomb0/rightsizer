package sshd

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/activeterm"
	bm "github.com/charmbracelet/wish/bubbletea"
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
	s := &Server{cfg: cfg, fails: map[string][]time.Time{}}
	srv, err := wish.NewServer(
		wish.WithAddress(cfg.Listen),
		wish.WithHostKeyPath(cfg.HostKeyPath),
		wish.WithVersion("rightsizer"),
		wish.WithBanner("rightsizer appliance - authorized access only\n"),
		wish.WithPasswordAuth(s.password),
		wish.WithIdleTimeout(30*time.Minute),
		wish.WithMaxTimeout(12*time.Hour),
		wish.WithMiddleware(
			bm.Middleware(func(ssh.Session) (tea.Model, []tea.ProgramOption) {
				return cfg.Program(), []tea.ProgramOption{tea.WithAltScreen()}
			}),
			activeterm.Middleware(),
			s.gate,
		),
	)
	if err != nil {
		return nil, err
	}
	srv.ServerConfigCallback = func(ssh.Context) *gossh.ServerConfig {
		return &gossh.ServerConfig{
			Config: gossh.Config{
				KeyExchanges: []string{"mlkem768x25519-sha256", "curve25519-sha256", "curve25519-sha256@libssh.org"},
				Ciphers:      []string{"chacha20-poly1305@openssh.com", "aes256-gcm@openssh.com", "aes128-gcm@openssh.com"},
				MACs:         []string{"hmac-sha2-256-etm@openssh.com", "hmac-sha2-512-etm@openssh.com"},
			},
			MaxAuthTries: 3,
		}
	}
	s.srv = srv
	return s, nil
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

// gate rejects commands and caps concurrent sessions before the console starts.
func (s *Server) gate(next ssh.Handler) ssh.Handler {
	return func(sess ssh.Session) {
		if len(sess.RawCommand()) > 0 || sess.Subsystem() != "" {
			wish.Fatalln(sess, "only the interactive console is available")
			return
		}
		s.mu.Lock()
		if s.sessions >= maxSessions {
			s.mu.Unlock()
			wish.Fatalln(sess, "too many open sessions")
			return
		}
		s.sessions++
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			s.sessions--
			s.mu.Unlock()
		}()
		next(sess)
	}
}
