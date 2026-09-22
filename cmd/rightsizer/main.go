package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
	_ "time/tzdata"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MarcoColomb0/rightsizer/internal/backup"
	"github.com/MarcoColomb0/rightsizer/internal/engine"
	"github.com/MarcoColomb0/rightsizer/internal/ipc"
	"github.com/MarcoColomb0/rightsizer/internal/report"
	"github.com/MarcoColomb0/rightsizer/internal/tui"
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
  rightsizer [tui]   open the interactive console (default)
  rightsizer status  print a one-line status
  rightsizer export  write the latest PDF report to stdout
  rightsizer backup <file>   archive the data directory
  rightsizer restore <file>  replace the data directory from an archive
  rightsizer daemon  run the collector (used by the container)
  rightsizer version
`)
}

func dataDir() string {
	if d := os.Getenv("RIGHTSIZER_DATA"); d != "" {
		return d
	}
	return "/data"
}

func socket() string { return filepath.Join(dataDir(), "rightsizer.sock") }

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

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
		return fmt.Errorf("invalid RIGHTSIZER_SHARE_HOURS")
	}
	web := &report.Server{
		Listen:     env("RIGHTSIZER_LISTEN", ":8443"),
		PublicHost: env("RIGHTSIZER_PUBLIC_HOST", "localhost"),
		PublicPort: env("RIGHTSIZER_PUBLIC_PORT", "8443"),
		TTL:        time.Duration(hours) * time.Hour,
	}
	e, err := engine.New(dataDir(), web)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	slog.Info("rightsizer daemon started", "version", version, "data", dataDir())
	err = ipc.Serve(ctx, socket(), e)
	e.Shutdown()
	slog.Info("rightsizer daemon stopped")
	return err
}

func runTUI() error {
	c := ipc.NewClient(socket())
	if _, err := c.Status(); err != nil {
		return fmt.Errorf("daemon not running (%v). Start it with: docker start rightsizer", err)
	}
	m, err := tea.NewProgram(tui.New(c, version, os.Getenv("RIGHTSIZER_LATEST"), os.Getenv("RIGHTSIZER_RELEASE_URL")), tea.WithAltScreen()).Run()
	if err != nil {
		return err
	}
	if m.(tui.Model).UpgradeRequested() {
		os.Exit(tui.ExitUpgrade)
	}
	return nil
}

func printStatus() error {
	s, err := ipc.NewClient(socket()).Status()
	if err != nil {
		return err
	}
	switch s.Phase {
	case engine.Idle:
		fmt.Println("idle: no analysis running")
	case engine.NeedPassword:
		fmt.Printf("paused: %s, run `rightsizer` to enter the password again\n", s.Config.Host)
	case engine.Running:
		fmt.Printf("running: %s, %d polls, ends %s\n", s.Config.Host, s.Polls, s.Ends.Format(time.RFC1123))
	case engine.Done:
		fmt.Printf("done: %s, finished %s\n", s.Config.Host, s.Finished.Format(time.RFC1123))
		if s.Share != nil {
			fmt.Printf("report: %s (expires %s)\n", s.Share.URL, s.Share.Expires.Format(time.RFC1123))
		}
	}
	return nil
}

func export() error {
	if fi, err := os.Stdout.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		return fmt.Errorf("redirect the output to a file, e.g. rightsizer export > report.pdf")
	}
	s, err := ipc.NewClient(socket()).Status()
	if err != nil {
		return err
	}
	path := s.ReportFile
	if s.Share != nil {
		path = filepath.Join(dataDir(), "reports", s.Share.File)
	}
	if path == "" {
		return fmt.Errorf("no report yet: finish the analysis or press p in the console")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(os.Stdout, f)
	return err
}
