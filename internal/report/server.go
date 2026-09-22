package report

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

type Share struct {
	ID          string
	Source      string
	URL         string
	Fingerprint string
	Expires     time.Time
	File        string
	Downloads   int
}

type entry struct {
	Share
	path  string
	timer *time.Timer
}

// Server exposes reports on a short-lived HTTPS listener. It listens only
// while at least one report is shared; each share has its own unguessable
// link and expires on its own.
type Server struct {
	Listen     string
	PublicHost string
	PublicPort string
	TTL        time.Duration

	mu     sync.Mutex
	srv    *http.Server
	port   string
	fp     string
	shares map[string]*entry
}

func (s *Server) Shares() []Share {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Share, 0, len(s.shares))
	for _, e := range s.shares {
		out = append(out, e.Share)
	}
	slices.SortFunc(out, func(a, b Share) int { return a.Expires.Compare(b.Expires) })
	return out
}

// Serve shares path, replacing any earlier share of the same source.
func (s *Server) Serve(source, path string) (*Share, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, e := range s.shares {
		if e.Source == source {
			s.drop(id)
		}
	}
	if s.srv == nil {
		if err := s.start(); err != nil {
			return nil, err
		}
	}
	tok := make([]byte, 24)
	if _, err := rand.Read(tok); err != nil {
		return nil, err
	}
	id := base64.RawURLEncoding.EncodeToString(tok)
	name := filepath.Base(path)
	e := &entry{
		Share: Share{
			ID:          id[:8],
			Source:      source,
			URL:         fmt.Sprintf("https://%s/%s/%s", net.JoinHostPort(s.PublicHost, s.port), id, name),
			Fingerprint: s.fp,
			Expires:     time.Now().Add(s.TTL),
			File:        name,
		},
		path: path,
	}
	if s.shares == nil {
		s.shares = map[string]*entry{}
	}
	s.shares[id] = e
	e.timer = time.AfterFunc(s.TTL, func() { s.Stop(e.ID) })
	slog.Info("report shared", "file", name, "expires", e.Expires)
	sh := e.Share
	return &sh, nil
}

// Stop removes the share with the given short ID ("" stops everything).
func (s *Server) Stop(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for tok, e := range s.shares {
		if id == "" || e.ID == id {
			s.drop(tok)
		}
	}
}

func (s *Server) drop(tok string) {
	e := s.shares[tok]
	if e == nil {
		return
	}
	e.timer.Stop()
	delete(s.shares, tok)
	slog.Info("report no longer shared", "file", e.File)
	if len(s.shares) == 0 && s.srv != nil {
		srv := s.srv
		s.srv = nil
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(ctx)
			slog.Info("download server stopped")
		}()
	}
}

func (s *Server) start() error {
	cert, fp, err := selfSigned(s.PublicHost)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", s.Listen)
	if err != nil {
		return fmt.Errorf("download server: %w", err)
	}
	s.port = s.PublicPort
	if s.port == "" {
		_, s.port, _ = net.SplitHostPort(ln.Addr().String())
	}
	s.fp = fp
	s.srv = &http.Server{
		Handler:           http.HandlerFunc(s.handle),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 << 10,
		ErrorLog:          log.New(io.Discard, "", 0),
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}},
	}
	srv := s.srv
	go func() {
		if err := srv.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("download server", "err", err)
		}
	}()
	return nil
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Strict-Transport-Security", "max-age=86400")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Content-Security-Policy", "default-src 'none'")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tok, name, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	s.mu.Lock()
	e := s.shares[tok]
	if ok && e != nil && e.File == name {
		e.Downloads++
	} else {
		e = nil
	}
	s.mu.Unlock()
	if e == nil {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(e.path)
	if err != nil {
		http.Error(w, "report unavailable", http.StatusGone)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.Error(w, "report unavailable", http.StatusGone)
		return
	}
	h.Set("Content-Type", "application/pdf")
	h.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	slog.Info("report downloaded", "file", name, "remote", r.RemoteAddr)
	http.ServeContent(w, r, name, st.ModTime(), f)
}

func selfSigned(host string) (tls.Certificate, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return tls.Certificate{}, "", err
	}
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "rightsizer report", Organization: []string{"rightsizer"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(30 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	if ip := net.ParseIP(host); ip != nil {
		tpl.IPAddresses = append(tpl.IPAddresses, ip)
	} else if host != "" && host != "localhost" {
		tpl.DNSNames = append(tpl.DNSNames, host)
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, Fingerprint(der), nil
}

func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}
