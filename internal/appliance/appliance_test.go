package appliance

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRequestUpgrade(t *testing.T) {
	h := Host{Dir: t.TempDir(), Current: "v1.2.0"}
	for _, bad := range []string{"v1.2.0", "v1.1.0", "latest", "v1.3.0; rm -rf /", "../v9.9.9"} {
		if err := h.RequestUpgrade(bad); err == nil {
			t.Fatalf("%q must be rejected", bad)
		}
	}
	if err := h.RequestUpgrade("v1.3.0"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(h.Dir, "upgrade-request"))
	if string(b) != "v1.3.0\n" {
		t.Fatalf("got %q", b)
	}
	_ = os.WriteFile(filepath.Join(h.Dir, "upgrade-status.json"), []byte(`{"Version":"v1.3.0","State":"done"}`), 0o600)
	if s := h.Status(); s == nil || s.State != "done" {
		t.Fatal("status not read")
	}
}

func TestRebootFlags(t *testing.T) {
	dir := t.TempDir()
	bootIDPath = filepath.Join(dir, "boot_id")
	_ = os.WriteFile(bootIDPath, []byte("boot-2\n"), 0o600)
	h := Host{Dir: filepath.Join(dir, "host"), Current: "v1.0.0"}
	if err := h.RequestReboot(); err == nil {
		t.Fatal("a restart must not be requested when none is needed")
	}
	_ = os.MkdirAll(filepath.Join(h.Dir, "reboot"), 0o700)
	_ = os.WriteFile(filepath.Join(h.Dir, "reboot", "os.json"), []byte(`{"Text":"Flatcar OS update 1.2.3 installed","BootID":"boot-2"}`), 0o600)
	_ = os.WriteFile(filepath.Join(h.Dir, "reboot", "upgrade.json"), []byte(`{"Text":"kernel settings changed","BootID":"boot-1"}`), 0o600)
	r := h.RebootReasons()
	if len(r) != 1 || r[0] != "Flatcar OS update 1.2.3 installed" {
		t.Fatalf("flags from an earlier boot must be ignored: %v", r)
	}
	if err := h.RequestReboot(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(h.Dir, "reboot-request"))
	if string(b) != "reboot\n" {
		t.Fatalf("got %q", b)
	}
}

func TestLatestOf(t *testing.T) {
	list := []release{
		{TagName: "v1.0.0", Body: "first"},
		{TagName: "v1.2.0", Body: "### Fixed\n- b"},
		{TagName: "v1.3.0-rc1", Prerelease: true},
		{TagName: "v1.1.0", Body: "### Added\n- a"},
		{TagName: "v2.0.0", Draft: true},
		{TagName: "nightly"},
	}
	r, err := latestOf(list, "v1.0.0", false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Tag != "v1.2.0" || r.URL != "https://github.com/"+Repo+"/releases/tag/v1.2.0" {
		t.Fatalf("latest: %+v", r)
	}
	if len(r.Notes) != 2 || r.Notes[0].Tag != "v1.2.0" || r.Notes[1].Tag != "v1.1.0" {
		t.Fatalf("notes must cover every newer release, newest first: %+v", r.Notes)
	}
	if r, _ := latestOf(list, "v1.2.0", false); len(r.Notes) != 0 {
		t.Fatalf("no notes when up to date: %+v", r.Notes)
	}
	if _, err := latestOf(list[2:3], "v1.0.0", false); err == nil {
		t.Fatal("prereleases alone are not a release")
	}
}

func TestPreReleaseChannel(t *testing.T) {
	h := Host{Dir: t.TempDir(), Current: "v1.3.1"}
	if h.PreReleases() {
		t.Fatal("the pre-release channel is off by default")
	}
	if err := h.RequestUpgrade("v1.4.0-rc.2"); err == nil {
		t.Fatal("a pre-release is refused outside the channel")
	}
	if err := h.SetPreReleases(true); err != nil || !h.PreReleases() {
		t.Fatalf("join: %v", err)
	}
	for _, bad := range []string{"v1.3.1", "v1.3.0", "v1.4.0-preview.1", "v1.4.0-rc.2; reboot", "v1.4.0-12-gabc"} {
		if err := h.RequestUpgrade(bad); err == nil {
			t.Fatalf("%q must be rejected", bad)
		}
	}
	if err := h.RequestUpgrade("v1.4.0-rc.2"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(h.Dir, "upgrade-request")); string(b) != "v1.4.0-rc.2\n" {
		t.Fatalf("got %q", b)
	}
	if err := h.SetPreReleases(false); err != nil || h.PreReleases() {
		t.Fatalf("leave: %v", err)
	}
	if err := h.SetPreReleases(false); err != nil {
		t.Fatal("leaving twice is fine")
	}
	// On a pre-release, its final release is offered outside the channel.
	h.Current = "v1.4.0-rc.2"
	if err := h.RequestUpgrade("v1.4.0"); err != nil {
		t.Fatal(err)
	}
}

func TestLatestOfPreReleases(t *testing.T) {
	list := []release{
		{TagName: "v1.3.1", Body: "fix"},
		{TagName: "v1.4.0-rc.1", Prerelease: true, Body: "rc1"},
		{TagName: "v1.4.0-rc.2", Prerelease: true, Body: "rc2"},
		{TagName: "v1.4.0-rc.9", Body: "not flagged as a pre-release"},
		{TagName: "v1.4.0-preview.1", Prerelease: true},
		{TagName: "v1.5.0-rc.1", Prerelease: true, Draft: true},
	}
	r, err := latestOf(list, "v1.3.1", true)
	if err != nil || r.Tag != "v1.4.0-rc.2" {
		t.Fatalf("pre-release channel: %+v %v", r, err)
	}
	if len(r.Notes) != 2 || r.Notes[0].Tag != "v1.4.0-rc.2" || r.Notes[1].Tag != "v1.4.0-rc.1" {
		t.Fatalf("notes %+v", r.Notes)
	}
	if r, _ := latestOf(list, "v1.3.0", false); r.Tag != "v1.3.1" {
		t.Fatalf("full releases only outside the channel: %+v", r)
	}
	list = append(list, release{TagName: "v1.4.0", Body: "final"})
	if r, _ := latestOf(list, "v1.4.0-rc.2", true); r.Tag != "v1.4.0" || len(r.Notes) != 1 {
		t.Fatalf("the final release follows its candidates: %+v", r)
	}
}
