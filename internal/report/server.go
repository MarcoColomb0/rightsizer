package report

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
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
	"strings"
	"sync"
	"time"
)

type Share struct {
	URL         string
	Fingerprint string
	Expires     time.Time
	File        string
	Downloads   int
}

type Server struct {
	Listen     string
	PublicHost string
	PublicPort string
	TTL        time.Duration

	mu    sync.Mutex
	srv   *http.Server
	share *Share
	timer *time.Timer
}

func (s *Server) Current() *Share {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.share == nil {
		return nil
	}
	c := *s.share
	return &c
}

// Serve starts a short-lived HTTPS server exposing only path, behind an
// unguessable URL and a freshly generated self-signed certificate.
func (s *Server) Serve(path string) (*Share, error) {
	s.Stop()
	tok := make([]byte, 24)
	if _, err := rand.Read(tok); err != nil {
		return nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(tok)
	cert, fp, err := selfSigned(s.PublicHost)
	if err != nil {
		return nil, err
	}
	name := filepath.Base(path)
	want := "/" + token + "/" + name
	ln, err := net.Listen("tcp", s.Listen)
	if err != nil {
		return nil, fmt.Errorf("download server: %w", err)
	}
	port := s.PublicPort
	if port == "" {
		_, port, _ = net.SplitHostPort(ln.Addr().String())
	}
	sh := &Share{
		URL:         fmt.Sprintf("https://%s%s", net.JoinHostPort(s.PublicHost, port), want),
		Fingerprint: fp,
		Expires:     time.Now().Add(s.TTL),
		File:        name,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
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
		if subtle.ConstantTimeCompare([]byte(r.URL.Path), []byte(want)) != 1 {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(path)
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
		slog.Info("report downloaded", "remote", r.RemoteAddr)
		s.mu.Lock()
		if s.share != nil {
			s.share.Downloads++
		}
		s.mu.Unlock()
		http.ServeContent(w, r, name, st.ModTime(), f)
	})
	srv := &http.Server{
		Addr:              s.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 << 10,
		ErrorLog:          log.New(io.Discard, "", 0),
		TLSConfig: &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{cert},
		},
	}
	go func() {
		if err := srv.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("download server", "err", err)
		}
	}()
	s.mu.Lock()
	s.srv, s.share = srv, sh
	s.timer = time.AfterFunc(s.TTL, s.Stop)
	s.mu.Unlock()
	slog.Info("report available for download", "file", name, "expires", sh.Expires)
	return s.Current(), nil
}

func (s *Server) Stop() {
	s.mu.Lock()
	srv, t := s.srv, s.timer
	s.srv, s.share, s.timer = nil, nil, nil
	s.mu.Unlock()
	if t != nil {
		t.Stop()
	}
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		slog.Info("download server stopped")
	}
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
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, strings.Join(parts, ":"), nil
}
