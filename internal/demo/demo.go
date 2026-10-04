// Package demo is an in-memory engine with synthetic vCenters, so the
// console can be tried without vCenter, Docker or the appliance.
package demo

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
	"github.com/MarcoColomb0/rightsizer/internal/appliance"
	"github.com/MarcoColomb0/rightsizer/internal/demo/sample"
	"github.com/MarcoColomb0/rightsizer/internal/engine"
	"github.com/MarcoColomb0/rightsizer/internal/report"
	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

// Options select which situations the demo shows.
type Options struct {
	// Appliance shows the restart-required notice and a pending upgrade.
	Appliance bool
	// Delay simulates vCenter round trips for slow operations.
	Delay time.Duration
	// Empty starts without any vCenter source.
	Empty bool
}

type source struct {
	st      engine.Status
	data    sample.Data
	result  *analysis.Result
	created time.Time
}

// Backend implements the console backend with synthetic data.
type Backend struct {
	opt    Options
	mu     sync.Mutex
	srcs   []*source
	shares []report.Share
	excl   []analysis.Exclusion
	params analysis.SizingParams
	reboot []string
}

func New(opt Options) *Backend {
	b := &Backend{opt: opt, params: analysis.DefaultSizing()}
	now := time.Now()
	b.excl = []analysis.Exclusion{
		{ID: newID(), VCenter: "vcsa01.corp.local", Name: "prod01-db03", Reason: "Vendor requirement", Note: "Vendor sizing guide requires 16 vCPU / 64 GB for the ERP database.", Created: now.Add(-40 * 24 * time.Hour), ReviewBy: now.Add(50 * 24 * time.Hour)},
		{ID: newID(), Name: "citrix-*", Reason: "Licensing", Note: "Citrix workers are sized by the VDI team; keep them out of rightsizing.", Created: now.Add(-120 * 24 * time.Hour), ReviewBy: now.Add(-5 * 24 * time.Hour)},
	}
	if opt.Empty {
		return b
	}
	b.add(sample.Config{Seed: 11, VCenter: "vcsa01.corp.local", About: "VMware vCenter Server 8.0.3 build-24322831",
		Clusters: []string{"prod-cl01", "prod-cl02", "dmz-cl01"}, VMs: 25, Load: 1, Days: 5},
		engine.Running, 14*24*time.Hour, 5*24*time.Hour+6*time.Hour, "balanced")
	b.add(sample.Config{Seed: 23, VCenter: "vcsa02.lab.local", About: "VMware vCenter Server 8.0.2 build-22617221",
		Clusters: []string{"lab-cl01"}, VMs: 18, Load: 0.6, Days: 2},
		engine.NeedPassword, 7*24*time.Hour, 2*24*time.Hour+3*time.Hour, "aggressive")
	b.add(sample.Config{Seed: 37, VCenter: "vcsa-dr.corp.local", About: "VMware vCenter Server 8.0.3 build-24322831",
		Clusters: []string{"dr-cl01", "dr-cl02"}, VMs: 20, Load: 0.4, Days: 14},
		engine.Done, 14*24*time.Hour, 14*24*time.Hour, "conservative")
	dr := b.srcs[2]
	b.share(dr.st.ID, fmt.Sprintf("rightsizer-final-%s.pdf", dr.st.Finished.Format("20060102-150405")), false)
	if opt.Appliance {
		b.reboot = []string{"Appliance updated to v0.8.1: kernel settings changed", "Flatcar Container Linux 4081.3.11 is installed"}
	}
	return b
}

func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (b *Backend) add(c sample.Config, phase engine.Phase, planned, elapsed time.Duration, profile string) *source {
	now := time.Now()
	c.End = now
	d := sample.Generate(c)
	d.Input.Profile = analysis.ProfileByName(profile)
	s := &source{data: d, created: now}
	s.st = engine.Status{
		ID: newID(), Phase: phase,
		Config:  engine.Config{Host: c.VCenter, User: "svc-rightsizer@vsphere.local", Duration: planned, Profile: profile},
		Started: now.Add(-elapsed), Ends: now.Add(planned - elapsed), About: c.About,
		History: "imported from " + now.Add(-elapsed-14*24*time.Hour).Format("Jan 02") + ", synced " + now.Add(-17*time.Minute).Format("Jan 02 15:04"),
	}
	switch phase {
	case engine.Done:
		s.st.Finished = s.st.Ends.Add(-time.Hour)
		s.st.Started, s.st.Ends = now.Add(-planned-26*time.Hour), now.Add(-26*time.Hour)
		s.st.Finished = s.st.Ends
	case engine.Running:
	case engine.NeedPassword:
		s.st.LastError = now.Add(-3*time.Hour).Format("Jan 02 15:04") + ": login failed: ServerFaultCode: Cannot complete login due to an incorrect user name or password."
	}
	b.srcs = append(b.srcs, s)
	b.analyze(s)
	return s
}

// analyze recomputes a source's results with the current exclusions.
func (b *Backend) analyze(s *source) {
	in := s.data.Input
	in.Exclusions = nil
	for _, x := range b.excl {
		if x.VCenter == "" || strings.EqualFold(x.VCenter, s.st.Config.Host) {
			in.Exclusions = append(in.Exclusions, x)
		}
	}
	d := s.data
	d.Input = in
	r := sample.Result(d)
	r.Final = s.st.Phase == engine.Done
	r.Start, r.End = s.st.Started, time.Now()
	if r.Final {
		r.End = s.st.Finished
	}
	s.result = r
	t := r.Totals
	s.st.Totals, s.st.Findings, s.st.Preview = &t, len(r.Findings), r.Preview
}

func (b *Backend) find(id string) (*source, error) {
	for _, s := range b.srcs {
		if s.st.ID == id {
			return s, nil
		}
	}
	return nil, fmt.Errorf("unknown source %q", id)
}

// tick advances running sources to the current time: polls every five
// minutes, finishing when the window ends.
func (b *Backend) tick() {
	now := time.Now()
	for _, s := range b.srcs {
		if s.st.Phase != engine.Running {
			continue
		}
		const step = 5 * time.Minute
		el := max(now.Sub(s.st.Started), 0)
		s.st.Polls = uint64(el / step)
		s.st.LastPoll = s.st.Started.Add(el - el%step)
		if since := now.Sub(s.created); since < 5*time.Minute {
			s.st.LastPoll = now.Add(-since % (20 * time.Second))
		}
		s.st.NextPoll = s.st.LastPoll.Add(5 * time.Minute)
	}
	b.shares = slices.DeleteFunc(b.shares, func(sh report.Share) bool { return now.After(sh.Expires) })
}

func (b *Backend) Summary() (*engine.Summary, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tick()
	sum := &engine.Summary{Shares: slices.Clone(b.shares), Vault: engine.VaultState{Enabled: b.opt.Appliance}, Reboot: b.reboot}
	for _, s := range b.srcs {
		st := s.st
		st.Result = nil
		sum.Sources = append(sum.Sources, st)
	}
	if b.opt.Appliance {
		sum.Upgrade = &engine.UpgradeInfo{Version: "v0.8.1", State: "done", Message: "engine and appliance host updated", Time: time.Now().Add(-2 * time.Hour)}
	}
	return sum, nil
}

func (b *Backend) Source(id string) (*engine.Status, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tick()
	s, err := b.find(id)
	if err != nil {
		return nil, err
	}
	st := s.st
	r := *s.result
	r.VMs = nil
	r.Clusters = slices.Clone(r.Clusters)
	for i := range r.Clusters {
		if n := len(r.Clusters[i].Points); n > 72 {
			r.Clusters[i].Points = r.Clusters[i].Points[n-72:]
		}
	}
	st.Result = &r
	return &st, nil
}

func (b *Backend) wait() {
	if b.opt.Delay > 0 {
		time.Sleep(b.opt.Delay)
	}
}

func (b *Backend) Probe(host string) (*vc.CertInfo, error) {
	b.wait()
	if strings.Contains(host, "offline") {
		return nil, errors.New("dial tcp: lookup " + host + ": no such host")
	}
	return &vc.CertInfo{
		Host: host + ":443", Subject: "CN=" + host + ",OU=VMware Engineering,O=VMware", Issuer: "CN=CA,DC=vsphere,DC=local,C=US,ST=California,O=" + host,
		NotAfter:    time.Now().AddDate(1, 3, 0),
		Fingerprint: "9F:3A:6C:12:D4:8E:21:7B:C0:55:A9:1E:4F:B2:73:0D:E8:96:3C:5A:11:F7:2E:84:6B:D0:39:A5:C7:1F:8B:42",
		Trusted:     strings.HasSuffix(host, ".trusted.local"),
	}, nil
}

func (b *Backend) Add(cfg engine.Config, password string) (string, error) {
	b.wait()
	if password == "wrong" {
		return "", errors.New("login failed: Cannot complete login due to an incorrect user name or password")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.srcs {
		if s.st.Config.Host == cfg.Host && s.st.Phase != engine.Done {
			return "", fmt.Errorf("%s is already being analysed", cfg.Host)
		}
	}
	clusters := cfg.Clusters
	if len(clusters) == 0 {
		clusters = []string{"cl01", "cl02"}
	}
	s := b.add(sample.Config{Seed: uint64(len(b.srcs)*13 + 5), VCenter: cfg.Host, About: "VMware vCenter Server 8.0.3 build-24322831",
		Clusters: clusters, VMs: 16, Load: 0.8, Days: 14}, engine.Running, cfg.Duration, 0, cfg.Profile)
	s.st.Config = cfg
	s.st.Preview = true
	s.result.Preview = true
	s.st.History = "imported from " + time.Now().Add(-14*24*time.Hour).Format("Jan 02")
	return s.st.ID, nil
}

func (b *Backend) Resume(id, password string) error {
	b.wait()
	if password == "wrong" {
		return errors.New("login failed: Cannot complete login due to an incorrect user name or password")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	s, err := b.find(id)
	if err != nil {
		return err
	}
	s.st.Phase, s.st.LastError = engine.Running, ""
	return nil
}

func (b *Backend) Finish(id string) error {
	b.wait()
	b.mu.Lock()
	defer b.mu.Unlock()
	s, err := b.find(id)
	if err != nil {
		return err
	}
	s.st.Phase, s.st.Finished = engine.Done, time.Now()
	b.analyze(s)
	b.share(id, fmt.Sprintf("rightsizer-final-%s.pdf", s.st.Finished.Format("20060102-150405")), false)
	return nil
}

func (b *Backend) Remove(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.srcs = slices.DeleteFunc(b.srcs, func(s *source) bool { return s.st.ID == id })
	b.shares = slices.DeleteFunc(b.shares, func(sh report.Share) bool { return sh.Source == id || sh.Source == "sizing:"+id })
	return nil
}

func (b *Backend) share(source, file string, data bool) *report.Share {
	b.shares = slices.DeleteFunc(b.shares, func(sh report.Share) bool { return sh.Source == source })
	tok := newID() + newID() + newID()
	base := "https://10.20.30.40:8443/" + tok + "/"
	sh := report.Share{ID: tok[:8], Source: source, URL: base + file, File: file, Expires: time.Now().Add(24 * time.Hour),
		Fingerprint: "4B:91:0C:7E:A2:33:F8:15:6D:C9:E0:47:B8:2A:91:D3:5F:66:0E:7C:A4:13:89:F2:BB:50:3D:E1:72:C8:09:AF"}
	if data {
		zip := strings.TrimSuffix(file, ".pdf") + "-data.zip"
		sh.Extra = []report.Link{{Name: zip, URL: base + zip}}
	}
	b.shares = append(b.shares, sh)
	return &sh
}

func (b *Backend) Publish(id string) (*report.Share, error) {
	b.wait()
	b.mu.Lock()
	defer b.mu.Unlock()
	kind, key := "interim", id
	if id == "" {
		key = "all"
	} else if s, err := b.find(id); err != nil {
		return nil, err
	} else if s.st.Phase == engine.Done {
		kind = "final"
	}
	return b.share(key, fmt.Sprintf("rightsizer-%s-%s.pdf", kind, time.Now().Format("20060102-150405")), false), nil
}

func (b *Backend) PublishSizing(id string) (*report.Share, error) {
	b.wait()
	b.mu.Lock()
	defer b.mu.Unlock()
	key := "sizing:all"
	if id != "" {
		key = "sizing:" + id
	}
	return b.share(key, fmt.Sprintf("rightsizer-sizing-%s.pdf", time.Now().Format("20060102-150405")), true), nil
}

func (b *Backend) StopShare(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.shares = slices.DeleteFunc(b.shares, func(sh report.Share) bool { return id == "" || sh.ID == id })
	return nil
}

func (b *Backend) ChangePassword(old, next string) error {
	b.wait()
	switch {
	case old == "":
		return errors.New("wrong password")
	case len(next) < 12:
		return errors.New("the password must be at least 12 characters")
	}
	return nil
}

func (b *Backend) RequestUpgrade(string) error {
	if !b.opt.Appliance {
		return errors.New("upgrades are run by the rightsizer launcher on the host")
	}
	return nil
}

func (b *Backend) RequestReboot() error {
	if !b.opt.Appliance {
		return errors.New("only the appliance can be restarted from the console")
	}
	b.mu.Lock()
	b.reboot = nil
	b.mu.Unlock()
	return nil
}

func (b *Backend) Exclusions() ([]analysis.Exclusion, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.excl), nil
}

func (b *Backend) Exclude(x analysis.Exclusion) error {
	x.Note, x.Name = strings.TrimSpace(x.Note), strings.TrimSpace(x.Name)
	if err := x.Validate(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	x.ID, x.Created = newID(), time.Now()
	b.excl = append(b.excl, x)
	for _, s := range b.srcs {
		b.analyze(s)
	}
	return nil
}

func (b *Backend) Unexclude(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(b.excl)
	b.excl = slices.DeleteFunc(b.excl, func(x analysis.Exclusion) bool { return x.ID == id })
	if len(b.excl) == n {
		return errors.New("unknown exclusion")
	}
	for _, s := range b.srcs {
		b.analyze(s)
	}
	return nil
}

func (b *Backend) Sizing(id string) (*analysis.Sizing, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []*analysis.Sizing
	for _, s := range b.srcs {
		if id == "" || s.st.ID == id {
			out = append(out, sample.Sizing(s.data, s.result, b.params))
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("unknown source %q", id)
	}
	sz := analysis.MergeSizing(out)
	cp := *sz
	cp.VMs = nil
	return &cp, nil
}

func (b *Backend) SizingParams() (*analysis.SizingParams, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.params
	return &p, nil
}

func (b *Backend) SetSizingParams(p analysis.SizingParams) error {
	if err := p.Validate(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.params = p
	return nil
}

// Notes are sample release notes for the update prompt.
func Notes() []appliance.Note {
	now := time.Now()
	return []appliance.Note{
		{Tag: "v1.1.0", Date: now.Add(-2 * 24 * time.Hour), Body: "### Added\n- **Changelog in the console:** the update prompt lists what changed in every version since yours.\n- Sizing CSV includes the `NUMA fit` of each node option.\n\n### Fixed\n- Paused analyses resume after a restart once an administrator logs in.\n\n**Full Changelog**: https://github.com/MarcoColomb0/rightsizer/compare/v1.0.1...v1.1.0"},
		{Tag: "v1.0.1", Date: now.Add(-12 * 24 * time.Hour), Body: "### Fixed\n- Report links work behind a host name with upper-case letters.\n- The heatmap legend no longer overlaps the last weekday on narrow terminals."},
	}
}
