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
	"sync"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/version"
)

const Repo = "MarcoColomb0/rightsizer"

var tagRE = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

type Release struct {
	Tag string
	URL string
}

// Checker polls GitHub for the latest release.
type Checker struct {
	mu     sync.Mutex
	latest Release
}

func (c *Checker) Latest() Release {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.latest
}

func (c *Checker) Run(ctx context.Context, every time.Duration) {
	for {
		if r, err := fetchLatest(ctx); err == nil {
			c.mu.Lock()
			c.latest = r
			c.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

func fetchLatest(ctx context.Context) (Release, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+Repo+"/releases/latest", nil)
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
	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&body); err != nil {
		return Release{}, err
	}
	if !tagRE.MatchString(body.TagName) {
		return Release{}, errors.New("unexpected release tag")
	}
	return Release{Tag: body.TagName, URL: "https://github.com/" + Repo + "/releases/tag/" + body.TagName}, nil
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

// RequestUpgrade asks the appliance host to upgrade the engine.
func (h Host) RequestUpgrade(tag string) error {
	if !tagRE.MatchString(tag) || !version.Newer(tag, h.Current) {
		return fmt.Errorf("%q is not a newer release", tag)
	}
	if err := os.MkdirAll(h.Dir, 0o700); err != nil {
		return err
	}
	tmp := filepath.Join(h.Dir, ".upgrade-request")
	if err := os.WriteFile(tmp, []byte(tag+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(h.Dir, "upgrade-request"))
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
