// Package appliance holds what the engine shares with the appliance host:
// release checks, upgrade requests and the host's upgrade status. The engine
// never controls the host directly; it only drops a request file that a
// host unit validates and acts on.
package appliance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/atomicfile"

	"github.com/MarcoColomb0/rightsizer/internal/version"
)

const Repo = "MarcoColomb0/rightsizer"

var (
	tagRE = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
	// preTagRE adds pre-releases such as v1.4.0-rc.1, offered only on the
	// pre-release channel. Same rule as the appliance host script.
	preTagRE = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-(alpha|beta|rc)\.?[0-9]*)?$`)
)

type Release struct {
	Tag string
	URL string
	// Notes covers every release newer than the running version up to Tag,
	// newest first, so skipping versions shows everything that changed.
	Notes []Note
}

// Note is the release notes of one version, in Markdown.
type Note struct {
	Tag  string
	Date time.Time
	Body string
}

// Checker polls GitHub for the latest release, including pre-releases
// while Pre reports the pre-release channel on.
type Checker struct {
	Current string
	Pre     func() bool

	mu     sync.Mutex
	latest Release
}

func (c *Checker) pre() bool { return c.Pre != nil && c.Pre() }

// Refresh checks now, as after switching channel.
func (c *Checker) Refresh(ctx context.Context) (Release, error) {
	r, err := Fetch(ctx, c.Current, c.pre())
	if err != nil {
		return Release{}, err
	}
	c.mu.Lock()
	c.latest = r
	c.mu.Unlock()
	return r, nil
}

func (c *Checker) Latest() Release {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.latest
}

func (c *Checker) Run(ctx context.Context, every time.Duration) {
	for {
		_, _ = c.Refresh(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

const (
	maxNotes    = 20
	maxNoteSize = 16 << 10
)

// Fetch reads the published releases and returns the latest one with the
// notes of every release newer than current. With pre, pre-releases count
// too.
func Fetch(ctx context.Context, current string, pre bool) (Release, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+Repo+"/releases?per_page=50", nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("release check: %s", res.Status)
	}
	var list []release
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&list); err != nil {
		return Release{}, err
	}
	return latestOf(list, current, pre)
}

type release struct {
	TagName     string    `json:"tag_name"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
}

func latestOf(list []release, current string, pre bool) (Release, error) {
	var notes []Note
	latest := ""
	newer := version.Newer
	if pre {
		newer = version.NewerAny
	}
	for _, r := range list {
		switch {
		case r.Draft:
			continue
		case pre && (!preTagRE.MatchString(r.TagName) || r.Prerelease != version.IsPre(r.TagName)):
			continue
		case !pre && (r.Prerelease || !tagRE.MatchString(r.TagName)):
			continue
		}
		if latest == "" || version.NewerAny(r.TagName, latest) {
			latest = r.TagName
		}
		if newer(r.TagName, current) {
			notes = append(notes, Note{Tag: r.TagName, Date: r.PublishedAt, Body: clipNote(r.Body)})
		}
	}
	if latest == "" {
		return Release{}, errors.New("no published release")
	}
	slices.SortFunc(notes, func(a, b Note) int {
		switch {
		case version.NewerAny(a.Tag, b.Tag):
			return -1
		case version.NewerAny(b.Tag, a.Tag):
			return 1
		}
		return 0
	})
	return Release{Tag: latest, URL: "https://github.com/" + Repo + "/releases/tag/" + latest, Notes: notes[:min(len(notes), maxNotes)]}, nil
}

func clipNote(s string) string {
	if len(s) <= maxNoteSize {
		return s
	}
	s = s[:maxNoteSize]
	if i := strings.LastIndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	return s + "\n…"
}

type Host struct {
	Dir     string
	Current string
}

type UpgradeStatus struct {
	Version string
	State   string
	Message string
	Time    time.Time
}

func (h Host) channelPath() string { return filepath.Join(h.Dir, "pre-releases") }

// PreReleases reports whether the appliance follows the pre-release
// channel: release candidates are offered as upgrades.
func (h Host) PreReleases() bool {
	_, err := os.Stat(h.channelPath())
	return err == nil
}

// SetPreReleases joins or leaves the pre-release channel. Leaving keeps the
// version installed; the next full release is offered as usual.
func (h Host) SetPreReleases(on bool) error {
	if !on {
		if err := os.Remove(h.channelPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(h.Dir, 0o700); err != nil {
		return err
	}
	return atomicfile.WriteFile(h.channelPath(), []byte("on\n"), 0o600)
}

// RequestUpgrade asks the appliance host to upgrade the engine. A
// pre-release is accepted only on the pre-release channel.
func (h Host) RequestUpgrade(tag string) error {
	ok := tagRE.MatchString(tag) && version.Newer(tag, h.Current)
	if h.PreReleases() {
		ok = preTagRE.MatchString(tag) && version.NewerAny(tag, h.Current)
	}
	if !ok {
		return fmt.Errorf("%q is not a newer release", tag)
	}
	if err := os.MkdirAll(h.Dir, 0o700); err != nil {
		return err
	}
	return atomicfile.WriteFile(filepath.Join(h.Dir, "upgrade-request"), []byte(tag+"\n"), 0o600)
}

// bootIDPath is shared with the host kernel, so the engine container sees the
// same boot ID as the host unit that wrote the flag.
var bootIDPath = "/proc/sys/kernel/random/boot_id"

type rebootFlag struct {
	Text   string
	BootID string
}

// RebootReasons lists why the appliance needs a restart. Flags written
// before the current boot are stale and ignored, so a restart clears them.
func (h Host) RebootReasons() []string {
	cur, err := os.ReadFile(bootIDPath)
	if err != nil {
		return nil
	}
	boot := strings.TrimSpace(string(cur))
	files, _ := filepath.Glob(filepath.Join(h.Dir, "reboot", "*.json"))
	slices.Sort(files)
	var out []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var r rebootFlag
		if json.Unmarshal(b, &r) == nil && r.BootID == boot && r.Text != "" {
			out = append(out, r.Text)
		}
	}
	return out
}

// RequestReboot asks the appliance host to restart.
func (h Host) RequestReboot() error {
	if len(h.RebootReasons()) == 0 {
		return errors.New("no restart is needed")
	}
	if err := os.MkdirAll(h.Dir, 0o700); err != nil {
		return err
	}
	return atomicfile.WriteFile(filepath.Join(h.Dir, "reboot-request"), []byte("reboot\n"), 0o600)
}

func (h Host) Status() *UpgradeStatus {
	b, err := os.ReadFile(filepath.Join(h.Dir, "upgrade-status.json"))
	if err != nil {
		return nil
	}
	var s UpgradeStatus
	if json.Unmarshal(b, &s) != nil {
		return nil
	}
	return &s
}
