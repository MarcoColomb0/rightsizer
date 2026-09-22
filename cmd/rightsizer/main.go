package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MarcoColomb0/rightsizer/internal/backup"
	"github.com/MarcoColomb0/rightsizer/internal/engine"
	"github.com/MarcoColomb0/rightsizer/internal/ipc"
	"github.com/MarcoColomb0/rightsizer/internal/report"
	"github.com/MarcoColomb0/rightsizer/internal/sshd"
	"github.com/MarcoColomb0/rightsizer/internal/tui"
	"github.com/MarcoColomb0/rightsizer/internal/vault"
)

var version = "dev"

func main() {
	report.Version = version
	cmd := "tui"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "tui":
		err = runTUI()
	case "daemon":
		err = runDaemon()
	case "status":
		err = printStatus()
	case "export":
		err = export()
	case "backup", "restore":
		if len(os.Args) != 3 {
			err = fmt.Errorf("usage: rightsizer %s <file.tar.gz>", cmd)
		} else if cmd == "backup" {
			err = backup.Create(dataDir(), os.Args[2])
		} else {
			err = backup.Restore(os.Args[2], dataDir())
		}
	case "version", "--version", "-v":
		fmt.Println("rightsizer", version)
	case "help", "--help", "-h":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "rightsizer:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`rightsizer - read-only vSphere rightsizing analysis

Usage:
  rightsizer [tui]           open the interactive console (default)
  rightsizer status          print a short status
  rightsizer export          write the latest PDF report to stdout
  rightsizer backup <file>   archive the data directory
  rightsizer restore <file>  replace the data directory from an archive
  rightsizer daemon          run the engine (used by the container)
  rightsizer version
`)
}

func dataDir() string { return env("RIGHTSIZER_DATA", "/data") }

func socket() string { return filepath.Join(dataDir(), "rightsizer.sock") }

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func appliance() bool { return os.Getenv("RIGHTSIZER_APPLIANCE") == "1" }

func runDaemon() error {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err := os.MkdirAll(dataDir(), 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dataDir(), 0o700); err != nil {
		return err
	}
	hours, err := strconv.Atoi(env("RIGHTSIZER_SHARE_HOURS", "24"))
	if err != nil || hours < 1 {
		return errors.New("invalid RIGHTSIZER_SHARE_HOURS")
	}
	web := &report.Server{
		Listen:     env("RIGHTSIZER_LISTEN", ":8443"),
		PublicHost: env("RIGHTSIZER_PUBLIC_HOST", "localhost"),
		PublicPort: env("RIGHTSIZER_PUBLIC_PORT", "8443"),
		TTL:        time.Duration(hours) * time.Hour,
	}
	var v *vault.Vault
	if appliance() {
		v = vault.Open(dataDir())
		if err := bootstrap(v); err != nil {
			return err
		}
	}
	e, err := engine.New(dataDir(), web, v)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	slog.Info("rightsizer engine started", "version", version, "appliance", appliance())

	errc := make(chan error, 2)
	go func() { errc <- ipc.Serve(ctx, socket(), e) }()
	if appliance() {
		srv, err := sshd.New(sshd.Config{
			Listen:      env("RIGHTSIZER_SSH_LISTEN", ":2222"),
			HostKeyPath: filepath.Join(dataDir(), "ssh", "host_ed25519"),
			Login:       e.Unlock,
			Program: func() tea.Model {
				return tui.New(ipc.Local{E: e}, tui.Options{Version: version, AdminSettings: true})
			},
		})
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(dataDir(), "ssh"), 0o700); err != nil {
			return err
		}
		go func() { errc <- srv.ListenAndServe() }()
		defer func() {
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(sctx)
		}()
	}
	select {
	case <-ctx.Done():
	case err = <-errc:
	}
	e.Shutdown()
	slog.Info("rightsizer engine stopped")
	return err
}

// bootstrap applies an administrator password provided by the appliance
// (vApp property) on first boot or as a reset. The file is removed once read.
func bootstrap(v *vault.Vault) error {
	path := env("RIGHTSIZER_BOOTSTRAP", filepath.Join(dataDir(), "bootstrap", "admin-password"))
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if !v.Configured() {
			slog.Error("no administrator password set; set it in the appliance vApp options")
		}
		return nil
	}
	if err != nil {
		return err
	}
	defer os.Remove(path)
	pw := strings.TrimRight(string(b), "\r\n")
	if err := v.Reset(pw); err != nil {
		slog.Error("administrator password from vApp options rejected", "err", err)
		return nil
	}
	slog.Info("administrator password set from vApp options; stored vCenter credentials were cleared")
	return nil
}

func runTUI() error {
	c := ipc.NewClient(socket())
	if _, err := c.Summary(); err != nil {
		return fmt.Errorf("engine not running (%v). Start it with: rightsizer start", err)
	}
	m, err := tea.NewProgram(tui.New(c, tui.Options{
		Version:    version,
		Latest:     os.Getenv("RIGHTSIZER_LATEST"),
		ReleaseURL: os.Getenv("RIGHTSIZER_RELEASE_URL"),
		CanUpgrade: true,
	}), tea.WithAltScreen()).Run()
	if err != nil {
		return err
	}
	if m.(tui.Model).UpgradeRequested() {
		os.Exit(tui.ExitUpgrade)
	}
	return nil
}

func printStatus() error {
	s, err := ipc.NewClient(socket()).Summary()
	if err != nil {
		return err
	}
	count := map[engine.Phase]int{}
	for _, src := range s.Sources {
		count[src.Phase]++
	}
	fmt.Printf("sources: %d (collecting %d, paused %d, done %d)\n", len(s.Sources), count[engine.Running], count[engine.NeedPassword], count[engine.Done])
	for _, src := range s.Sources {
		fmt.Printf("  %s  %-14s %s\n", src.ID, src.Phase, src.Config.Host)
	}
	for _, sh := range s.Shares {
		fmt.Printf("report: %s (expires %s)\n", sh.URL, sh.Expires.Format(time.RFC1123))
	}
	return nil
}

func export() error {
	if fi, err := os.Stdout.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		return errors.New("redirect the output to a file, e.g. rightsizer export > report.pdf")
	}
	path, err := ipc.NewClient(socket()).LatestReport()
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(os.Stdout, f)
	return err
}
