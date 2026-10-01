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

	tea "charm.land/bubbletea/v2"
	"charm.land/ssh"
	"charm.land/wish/v2"
	"charm.land/wish/v2/bubbletea"
	gossh "golang.org/x/crypto/ssh"

	"github.com/MarcoColomb0/rightsizer/internal/atomicfile"
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
	key, err := hostKey(cfg.HostKeyPath)
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, fails: map[string][]time.Time{}}
	// Middlewares run last to first: the guard refuses anything but an
	// interactive console before Bubble Tea starts.
	s.srv, err = wish.NewServer(
		wish.WithAddress(cfg.Listen),
		wish.WithVersion("rightsizer"),
		wish.WithBanner("rightsizer appliance - authorized access only\n"),
		wish.WithHostKeyPEM(key),
		wish.WithPasswordAuth(s.password),
		wish.WithIdleTimeout(30*time.Minute),
		wish.WithMaxTimeout(12*time.Hour),
		wish.WithMiddleware(bubbletea.Middleware(s.program), s.guard),
	)
	if err != nil {
		return nil, err
	}
	s.srv.ServerConfigCallback = func(ssh.Context) *gossh.ServerConfig {
		return &gossh.ServerConfig{
			Config: gossh.Config{
				KeyExchanges: []string{"mlkem768x25519-sha256", "curve25519-sha256", "curve25519-sha256@libssh.org"},
				Ciphers:      []string{"chacha20-poly1305@openssh.com", "aes256-gcm@openssh.com", "aes128-gcm@openssh.com"},
				MACs:         []string{"hmac-sha2-256-etm@openssh.com", "hmac-sha2-512-etm@openssh.com"},
			},
			MaxAuthTries: 3,
		}
	}
	return s, nil
}

// hostKey loads the Ed25519 host key in PEM form, creating it on first start.
func hostKey(path string) ([]byte, error) {
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
	if _, err := gossh.ParsePrivateKey(b); err != nil {
		return nil, err
	}
	return b, nil
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

// guard refuses commands, subsystems, sessions without a terminal and
// sessions above the limit before the console starts.
func (s *Server) guard(next ssh.Handler) ssh.Handler {
	return func(sess ssh.Session) {
		if sess.RawCommand() != "" || sess.Subsystem() != "" {
			refuse(sess, "only the interactive console is available")
			return
		}
		if _, _, ok := sess.Pty(); !ok {
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
		next(sess)
		_ = sess.Exit(0)
	}
}

// program builds the console for a session. The middleware sizes it to the
// client's terminal and detects its colours from the session environment.
func (s *Server) program(ssh.Session) (tea.Model, []tea.ProgramOption) {
	return s.cfg.Program(), nil
}
