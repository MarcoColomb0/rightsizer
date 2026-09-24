// Package console renders the appliance's read-only status screen on the VM
// console. It reads nothing from the keyboard.
package console

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/MarcoColomb0/rightsizer/internal/engine"
)

type Source interface {
	Summary() (*engine.Summary, error)
}

var (
	title = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	label = lipgloss.NewStyle().Width(14).Foreground(lipgloss.Color("8"))
	bold  = lipgloss.NewStyle().Bold(true)
	warn  = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	muted = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

func Run(ctx context.Context, w io.Writer, version, dataDir string, src Source) {
	for {
		fmt.Fprint(w, "\033[H\033[2J\033[?25l"+Render(version, dataDir, src))
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second):
		}
	}
}

func Render(version, dataDir string, src Source) string {
	var b strings.Builder
	line := func(k, v string) { b.WriteString("  " + label.Render(k) + v + "\n") }
	b.WriteString("\n  " + title.Render("rightsizer appliance") + muted.Render("  "+version) + "\n")
	b.WriteString("  " + muted.Render(strings.Repeat("─", 60)) + "\n\n")

	host, _ := os.Hostname()
	line("Hostname", host)
	addrs := ipv4()
	if len(addrs) == 0 {
		line("IPv4", warn.Render("no address yet (waiting for network)"))
	}
	for _, a := range addrs {
		line("IPv4", a)
	}
	if gw := gateway(); gw != "" {
		line("Gateway", gw)
	}
	if dns := resolvers(); len(dns) > 0 {
		line("DNS", strings.Join(dns, ", "))
	}
	b.WriteString("\n")
	if len(addrs) > 0 {
		ip, _, _ := strings.Cut(strings.Fields(addrs[0])[0], "/")
		line("Connect", bold.Render("ssh admin@"+ip))
	}
	if fp, err := os.ReadFile(filepath.Join(dataDir, "ssh", "fingerprint")); err == nil {
		line("SSH key", strings.TrimSpace(string(fp)))
	}
	b.WriteString("\n")
	if s, err := src.Summary(); err != nil {
		line("Engine", warn.Render("starting..."))
	} else {
		n := map[engine.Phase]int{}
		for _, x := range s.Sources {
			n[x.Phase]++
		}
		line("Engine", fmt.Sprintf("running · %d vCenter sources", len(s.Sources)))
		if len(s.Sources) > 0 {
			line("", fmt.Sprintf("%d collecting · %d paused · %d complete", n[engine.Running], n[engine.NeedPassword], n[engine.Done]))
		}
		if n[engine.NeedPassword] > 0 && s.Vault.Locked {
			line("", warn.Render("Log in over SSH to resume paused analyses."))
		}
		if len(s.Shares) > 0 {
			line("Reports", fmt.Sprintf("%d shared", len(s.Shares)))
		}
		for i, r := range s.Reboot {
			label := ""
			if i == 0 {
				label = "Restart"
			}
			line(label, warn.Render("required: "+r))
		}
		if len(s.Reboot) > 0 {
			line("", muted.Render("Restart from the SSH console (R), or restart the VM in vCenter."))
		}
	}
	b.WriteString("\n  " + muted.Render("Network settings and the administrator password are managed in vCenter:") + "\n")
	b.WriteString("  " + muted.Render("select the VM > Configure > vApp Options, then restart the VM.") + "\n")
	b.WriteString("  " + muted.Render("This console is read-only. Updated "+time.Now().Format("2006-01-02 15:04")) + "\n")
	return b.String()
}

func ipv4() []string {
	var out []string
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagLoopback != 0 || i.Flags&net.FlagUp == 0 || strings.HasPrefix(i.Name, "docker") || strings.HasPrefix(i.Name, "veth") || strings.HasPrefix(i.Name, "br-") {
			continue
		}
		as, _ := i.Addrs()
		for _, a := range as {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
				out = append(out, fmt.Sprintf("%s  (%s)", n.String(), i.Name))
			}
		}
	}
	return out
}

func gateway() string {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) > 2 && fs[1] == "00000000" && fs[2] != "00000000" {
			b, err := hex.DecodeString(fs[2])
			if err == nil && len(b) == 4 {
				ip := make(net.IP, 4)
				binary.BigEndian.PutUint32(ip, binary.LittleEndian.Uint32(b))
				return ip.String()
			}
		}
	}
	return ""
}

func resolvers() []string {
	for _, p := range []string{"/run/systemd/resolve/resolv.conf", "/etc/resolv.conf"} {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		var out []string
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if fs := strings.Fields(sc.Text()); len(fs) == 2 && fs[0] == "nameserver" {
				out = append(out, fs[1])
			}
		}
		f.Close()
		if len(out) > 0 {
			return out
		}
	}
	return nil
}
