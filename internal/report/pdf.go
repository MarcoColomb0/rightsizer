package report

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
	"github.com/marcocolombo/rightsizer/internal/analysis"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/goregular"
)

const (
	Author  = "Marco Colombo"
	Website = "https://marco.wf"
	Project = "https://github.com/marcocolombo/rightsizer"
)

var Version = "dev"

type rgb struct{ r, g, b int }

var (
	cInk    = rgb{24, 32, 38}
	cMuted  = rgb{100, 112, 122}
	cAccent = rgb{15, 118, 110}
	cWarn   = rgb{194, 120, 3}
	cBad    = rgb{190, 40, 50}
	cRule   = rgb{222, 226, 230}
	cBand   = rgb{244, 247, 248}
	cProv   = rgb{160, 170, 178}
)

type doc struct {
	*fpdf.Fpdf
	tr func(string) string
	r  *analysis.Result
}

const (
	pageW   = 210.0
	margin  = 15.0
	content = pageW - 2*margin
)

func WritePDF(r *analysis.Result, path string) error {
	p := fpdf.New("P", "mm", "A4", "")
	p.SetMargins(margin, 18, margin)
	p.SetAutoPageBreak(true, 18)
	p.SetTitle("vSphere Rightsizing Report", true)
	p.SetAuthor("rightsizer by "+Author, true)
	p.SetCreator("rightsizer "+Version, true)
	p.SetCreationDate(r.Generated)
	p.AliasNbPages("{nb}")
	p.AddUTF8FontFromBytes("Go", "", goregular.TTF)
	p.AddUTF8FontFromBytes("Go", "B", gobold.TTF)
	p.AddUTF8FontFromBytes("Go", "I", goitalic.TTF)
	d := &doc{Fpdf: p, tr: func(s string) string { return s }, r: r}
	p.SetFooterFunc(d.footer)
	p.SetHeaderFunc(d.header)

	d.cover()
	d.refresh()
	d.findingSummary()
	d.findings()
	d.vmTable()
	d.method()

	if err := p.Error(); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := p.OutputFileAndClose(tmp); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (d *doc) color(c rgb)                 { d.SetTextColor(c.r, c.g, c.b) }
func (d *doc) fill(c rgb)                  { d.SetFillColor(c.r, c.g, c.b) }
func (d *doc) draw(c rgb)                  { d.SetDrawColor(c.r, c.g, c.b) }
func (d *doc) font(s string, size float64) { d.SetFont("Go", s, size) }

func (d *doc) text(h float64, s string) {
	d.MultiCell(content, h, d.tr(s), "", "L", false)
}

func (d *doc) header() {
	if d.PageNo() == 1 {
		return
	}
	d.font("", 7.5)
	d.color(cMuted)
	d.SetY(8)
	d.CellFormat(content/2, 4, d.tr("vSphere Rightsizing Report"), "", 0, "L", false, 0, "")
	d.CellFormat(content/2, 4, d.tr(d.r.VCenter), "", 1, "R", false, 0, "")
	d.draw(cRule)
	d.Line(margin, 13, pageW-margin, 13)
	d.SetY(18)
}

func (d *doc) footer() {
	d.SetY(-12)
	d.font("", 7.5)
	d.color(cMuted)
	d.CellFormat(content*0.75, 4, d.tr(fmt.Sprintf("rightsizer %s · open source by %s · %s", Version, Author, Website)), "", 0, "L", false, 0, Website)
	d.CellFormat(content*0.25, 4, fmt.Sprintf("Page %d / {nb}", d.PageNo()), "", 0, "R", false, 0, "")
}

func (d *doc) h1(s string) {
	d.font("B", 15)
	d.color(cInk)
	d.CellFormat(content, 9, d.tr(s), "", 1, "L", false, 0, "")
	d.fill(cAccent)
	d.Rect(margin, d.GetY(), 18, 0.8, "F")
	d.Ln(4)
}

func (d *doc) h2(s string) {
	d.Ln(2)
	d.font("B", 11)
	d.color(cInk)
	d.CellFormat(content, 7, d.tr(s), "", 1, "L", false, 0, "")
}

func (d *doc) para(s string) {
	d.font("", 9)
	d.color(cInk)
	d.text(4.6, s)
	d.Ln(1.5)
}

func (d *doc) cover() {
	r := d.r
	d.AddPage()
	d.fill(cAccent)
	d.Rect(0, 0, pageW, 3, "F")
	d.SetY(22)
	d.font("B", 24)
	d.color(cInk)
	d.CellFormat(content, 11, "vSphere Rightsizing Report", "", 1, "L", false, 0, "")
	d.font("", 11)
	d.color(cMuted)
	kind := "Interim report — analysis still running"
	if r.Final {
		kind = "Final report"
	}
	d.CellFormat(content, 6, d.tr(kind), "", 1, "L", false, 0, "")
	d.Ln(4)

	meta := [][2]string{
		{"vCenter", r.VCenter},
		{"Analysis window", fmt.Sprintf("%s → %s (%s of %s planned)", r.Start.Format("2006-01-02 15:04"), r.End.Format("2006-01-02 15:04"), dur(r.End.Sub(r.Start)), dur(r.Planned))},
		{"Sizing profile", fmt.Sprintf("%s (p%.0f, vCPU target %.0f%%, memory headroom %.0f%%)", r.Profile.Name, r.Profile.Percentile, r.Profile.CPUTarget*100, (r.Profile.MemHeadroom-1)*100)},
		{"Generated", r.Generated.Format("2006-01-02 15:04 MST")},
	}
	for _, m := range meta {
		d.font("B", 9)
		d.color(cMuted)
		d.CellFormat(35, 5.5, d.tr(m[0]), "", 0, "L", false, 0, "")
		d.font("", 9)
		d.color(cInk)
		d.CellFormat(content-35, 5.5, d.tr(m[1]), "", 1, "L", false, 0, "")
	}
	d.Ln(6)

	t := r.Totals
	kpis := []struct {
		label, value, sub string
		good              bool
	}{
		{"vCPU", fmt.Sprintf("%d → %d", t.VCPU, t.RecVCPU), pct(t.VCPU, t.RecVCPU), t.RecVCPU <= t.VCPU},
		{"Memory", fmt.Sprintf("%s → %s", analysis.GiB(t.MemMB), analysis.GiB(t.RecMemMB)), pct(t.MemMB, t.RecMemMB), t.RecMemMB <= t.MemMB},
		{"Hosts", fmt.Sprintf("%d → %d", t.Hosts, t.HostsNeeded), "incl. N+1 HA per cluster", t.HostsNeeded <= t.Hosts},
		{"Reclaimable storage", analysis.Human(t.Reclaim), fmt.Sprintf("of %s committed", analysis.Human(t.Storage)), true},
	}
	w := (content - 3*4) / 4
	y := d.GetY()
	for i, k := range kpis {
		x := margin + float64(i)*(w+4)
		d.fill(cBand)
		d.Rect(x, y, w, 26, "F")
		d.fill(cAccent)
		d.Rect(x, y, 1, 26, "F")
		d.SetXY(x+4, y+3)
		d.font("", 8)
		d.color(cMuted)
		d.CellFormat(w-6, 4, d.tr(k.label), "", 2, "L", false, 0, "")
		d.SetX(x + 4)
		d.font("B", 12.5)
		for size := 12.5; d.GetStringWidth(k.value) > w-6 && size > 7; size -= 0.5 {
			d.SetFontSize(size - 0.5)
		}
		d.color(cInk)
		d.CellFormat(w-6, 8, d.tr(k.value), "", 2, "L", false, 0, "")
		d.SetX(x + 4)
		d.font("", 8)
		if k.good {
			d.color(cAccent)
		} else {
			d.color(cWarn)
		}
		d.CellFormat(w-6, 4, d.tr(k.sub), "", 2, "L", false, 0, "")
	}
	d.SetY(y + 32)

	d.h2("Summary")
	sev := map[analysis.Severity]int{}
	for _, f := range r.Findings {
		sev[f.Severity]++
	}
	idle := 0
	for _, f := range r.Findings {
		if f.Kind == analysis.Idle {
			idle++
		}
	}
	d.para(fmt.Sprintf(
		"%d virtual machines inventoried (%d powered on, %d powered off, %d templates); %d had enough performance data to be rightsized. "+
			"Applying the recommendations would release %d vCPU and %s of configured memory. "+
			"%d VMs look idle (another %d vCPU / %s if decommissioned). "+
			"%d findings in total: %d high, %d medium, %d low priority.",
		t.VMs, t.On, t.Off, t.Templates, t.Analyzed,
		t.VCPU-t.RecVCPU, analysis.GiB(t.MemMB-t.RecMemMB),
		idle, t.IdleVCPU, analysis.GiB(t.IdleMemMB),
		len(r.Findings), sev[analysis.High], sev[analysis.Medium], sev[analysis.Low]))
	if t.Cores > 0 {
		d.para(fmt.Sprintf("For a hardware refresh, observed demand at the chosen percentile fits in %d physical cores (currently %d) and %d hosts of the current type (currently %d), including one HA spare per cluster.",
			t.NeedCores, t.Cores, t.HostsNeeded, t.Hosts))
	}

	d.h2("Provisioned vs recommended")
	d.bars([]bar{
		{"vCPU", float64(t.VCPU), float64(t.RecVCPU), fmt.Sprint(t.VCPU), fmt.Sprint(t.RecVCPU)},
		{"Memory", float64(t.MemMB), float64(t.RecMemMB), analysis.GiB(t.MemMB), analysis.GiB(t.RecMemMB)},
		{"Physical cores", float64(t.Cores), float64(t.NeedCores), fmt.Sprint(t.Cores), fmt.Sprint(t.NeedCores)},
		{"Hosts", float64(t.Hosts), float64(t.HostsNeeded), fmt.Sprint(t.Hosts), fmt.Sprint(t.HostsNeeded)},
	})
	if !r.Final {
		d.Ln(3)
		d.font("I", 8.5)
		d.color(cWarn)
		d.text(4.2, "Interim report: percentiles stabilise as more data is collected. Do not act on low-confidence findings until the analysis completes.")
	}
}

type bar struct {
	label     string
	prov, rec float64
	pl, rl    string
}

func (d *doc) bars(bs []bar) {
	labelW, rowH := 32.0, 11.0
	maxW := content - labelW - 26
	y := d.GetY() + 1
	for _, b := range bs {
		scale := math.Max(b.prov, b.rec)
		if scale <= 0 {
			scale = 1
		}
		d.SetXY(margin, y)
		d.font("", 8.5)
		d.color(cInk)
		d.CellFormat(labelW, rowH, d.tr(b.label), "", 0, "L", false, 0, "")
		pw := maxW * b.prov / scale
		rw := maxW * b.rec / scale
		d.fill(cProv)
		d.Rect(margin+labelW, y+1.5, math.Max(pw, 0.3), 3.6, "F")
		d.fill(cAccent)
		d.Rect(margin+labelW, y+5.8, math.Max(rw, 0.3), 3.6, "F")
		d.font("", 7.5)
		d.color(cMuted)
		d.SetXY(margin+labelW+pw+1.5, y+1.3)
		d.CellFormat(25, 4, d.tr(b.pl), "", 0, "L", false, 0, "")
		d.color(cAccent)
		d.SetXY(margin+labelW+rw+1.5, y+5.6)
		d.CellFormat(25, 4, d.tr(b.rl), "", 0, "L", false, 0, "")
		y += rowH
	}
	d.SetXY(margin+labelW, y+1)
	d.font("", 7.5)
	d.fill(cProv)
	d.Rect(margin+labelW, y+2, 3, 3, "F")
	d.color(cMuted)
	d.SetX(margin + labelW + 4)
	d.CellFormat(25, 5, "provisioned", "", 0, "L", false, 0, "")
	d.fill(cAccent)
	d.Rect(d.GetX(), y+2, 3, 3, "F")
	d.SetX(d.GetX() + 4)
	d.CellFormat(25, 5, "recommended", "", 1, "L", false, 0, "")
	d.SetY(y + 8)
}

func (d *doc) refresh() {
	r := d.r
	d.AddPage()
	d.h1("Tech refresh sizing")
	d.para(fmt.Sprintf("Required capacity per cluster, derived from observed demand (p%.0f) with the %s profile: CPU sized so hosts run at ≤%.0f%% and memory at ≤%.0f%% "+
		"after rightsizing. Required GHz and GB are hardware-neutral, so they can be matched against any new server model. Host counts assume the current host type plus one HA spare.",
		r.Profile.Percentile, r.Profile.Name, r.Profile.HostCPU*100, r.Profile.HostMem*100))
	rows := [][]string{}
	for _, c := range r.Clusters {
		if c.Hosts == 0 {
			continue
		}
		rows = append(rows, []string{
			c.Name, fmt.Sprint(c.Hosts), fmt.Sprint(c.Cores),
			fmt.Sprintf("%.0f%% / %.0f%%", c.CPUP, c.CPUPeak), fmt.Sprintf("%.0f%%", c.MemP),
			fmt.Sprintf("%d → %d", c.VCPU, c.RecVCPU), fmt.Sprintf("%s → %s", analysis.GiB(c.MemMB), analysis.GiB(c.RecMemMB)),
			fmt.Sprintf("%.0f GHz", c.NeedMHz/1000), fmt.Sprintf("%.0f GB", c.NeedMemB/(1<<30)),
			fmt.Sprintf("%d", c.NeedCores),
			fmt.Sprintf("%d (%d/%d+%d)", c.HostsNeeded, c.HostsForCPU, c.HostsForMem, c.HASpare),
		})
	}
	d.table([]col{
		{"Cluster", 28, "L"}, {"Hosts", 11, "R"}, {"Cores", 12, "R"}, {"CPU p/peak", 17, "R"}, {"Mem p", 11, "R"},
		{"vCPU", 17, "R"}, {"vRAM", 25, "R"}, {"Need GHz", 15, "R"}, {"Need RAM", 16, "R"}, {"Need cores", 15, "R"}, {"Need hosts", 17, "R"},
	}, rows)
	d.font("I", 7.5)
	d.color(cMuted)
	d.text(4, "Need hosts: total (hosts for CPU / hosts for memory + HA spare) using the current host type. CPU p = cluster CPU demand percentile as % of capacity.")

	for _, c := range r.Clusters {
		if len(c.Points) < 2 || c.CapMHz == 0 {
			continue
		}
		if d.GetY() > 230 {
			d.AddPage()
		}
		d.h2(fmt.Sprintf("%s — %s", c.Name, c.CPUModel))
		d.timeline(c)
	}
}

func (d *doc) timeline(c analysis.ClusterResult) {
	x0, y0, w, h := margin+10, d.GetY()+2, content-12, 36.0
	d.draw(cRule)
	d.SetLineWidth(0.2)
	d.font("", 6.5)
	d.color(cMuted)
	for _, g := range []float64{0, 25, 50, 75, 100} {
		y := y0 + h - h*g/100
		d.Line(x0, y, x0+w, y)
		d.SetXY(margin, y-1.5)
		d.CellFormat(9, 3, fmt.Sprintf("%.0f%%", g), "", 0, "R", false, 0, "")
	}
	n := len(c.Points)
	t0, t1 := c.Points[0].T, c.Points[n-1].T
	span := t1.Sub(t0).Seconds()
	if span <= 0 {
		span = 1
	}
	plot := func(col rgb, val func(analysis.Point) float64) {
		d.draw(col)
		d.SetLineWidth(0.45)
		for i := 1; i < n; i++ {
			a, b := c.Points[i-1], c.Points[i]
			xa := x0 + w*a.T.Sub(t0).Seconds()/span
			xb := x0 + w*b.T.Sub(t0).Seconds()/span
			ya := y0 + h - h*math.Min(val(a), 100)/100
			yb := y0 + h - h*math.Min(val(b), 100)/100
			d.Line(xa, ya, xb, yb)
		}
	}
	plot(cProv, func(p analysis.Point) float64 { return p.MemB / c.CapMemB * 100 })
	plot(cAccent, func(p analysis.Point) float64 { return p.CPUMHz / c.CapMHz * 100 })
	d.SetLineWidth(0.2)
	d.SetXY(x0, y0+h+1)
	d.CellFormat(w/2, 3, t0.Format("Jan 02 15:04"), "", 0, "L", false, 0, "")
	d.CellFormat(w/2, 3, t1.Format("Jan 02 15:04"), "", 1, "R", false, 0, "")
	d.SetX(x0)
	d.fill(cAccent)
	d.Rect(x0, d.GetY()+1.2, 3, 1.2, "F")
	d.SetX(x0 + 4)
	d.CellFormat(30, 4, "CPU demand % of capacity", "", 0, "L", false, 0, "")
	d.fill(cProv)
	d.Rect(d.GetX()+4, d.GetY()+1.2, 3, 1.2, "F")
	d.SetX(d.GetX() + 8)
	d.CellFormat(40, 4, "Memory consumed % of capacity", "", 1, "L", false, 0, "")
	d.Ln(2)
}

func (d *doc) findingSummary() {
	d.AddPage()
	d.h1("Findings")
	type agg struct {
		n, vcpu, mem int
		bytes        int64
	}
	by := map[analysis.Kind]*agg{}
	for _, f := range d.r.Findings {
		a := by[f.Kind]
		if a == nil {
			a = &agg{}
			by[f.Kind] = a
		}
		a.n++
		a.vcpu += f.VCPU
		a.mem += f.MemMB
		a.bytes += f.Bytes
	}
	kinds := make([]string, 0, len(by))
	for k := range by {
		kinds = append(kinds, string(k))
	}
	sort.Strings(kinds)
	rows := [][]string{}
	for _, k := range kinds {
		a := by[analysis.Kind(k)]
		rows = append(rows, []string{k, fmt.Sprint(a.n), signed(a.vcpu, ""), signedGiB(a.mem), dash(a.bytes)})
	}
	if len(rows) == 0 {
		d.para("No findings yet.")
		return
	}
	d.table([]col{{"Category", 60, "L"}, {"VMs", 20, "R"}, {"vCPU", 30, "R"}, {"Memory", 35, "R"}, {"Storage", 35, "R"}}, rows)
	d.font("I", 7.5)
	d.color(cMuted)
	d.text(4, "Positive values are resources that can be released; negative values are additional resources needed by undersized VMs. Idle VM savings assume decommissioning.")
}

func (d *doc) findings() {
	if len(d.r.Findings) == 0 {
		return
	}
	d.h2("Recommendations")
	rows := [][]string{}
	for _, f := range d.r.Findings {
		rows = append(rows, []string{f.Severity.String(), f.VM, string(f.Kind), f.Current, f.Suggested, f.Confidence})
	}
	d.table([]col{{"Priority", 14, "L"}, {"VM", 40, "L"}, {"Finding", 32, "L"}, {"Current", 36, "L"}, {"Suggested", 42, "L"}, {"Conf.", 16, "L"}}, rows)

	d.h2("Details by VM")
	byVM := map[string][]analysis.Finding{}
	var names []string
	for _, f := range d.r.Findings {
		if byVM[f.VM] == nil {
			names = append(names, f.VM)
		}
		byVM[f.VM] = append(byVM[f.VM], f)
	}
	sort.Strings(names)
	for _, n := range names {
		fs := byVM[n]
		if d.GetY() > 262 {
			d.AddPage()
		}
		d.font("B", 8.5)
		d.color(cInk)
		d.CellFormat(content, 5, d.tr(n+"  "), "", 0, "L", false, 0, "")
		d.SetX(margin + d.GetStringWidth(n+"  "))
		d.font("", 7.5)
		d.color(cMuted)
		d.CellFormat(60, 5, d.tr(fs[0].Cluster), "", 1, "L", false, 0, "")
		for _, f := range fs {
			d.font("B", 7.8)
			d.color(sevColor(f.Severity))
			d.SetX(margin + 3)
			d.CellFormat(34, 3.9, d.tr(string(f.Kind)), "", 0, "L", false, 0, "")
			d.font("", 7.8)
			d.color(cInk)
			for i, l := range d.SplitText(fmt.Sprintf("%s → %s. %s", f.Current, f.Suggested, f.Detail), content-38) {
				if strings.TrimSpace(l) == "" {
					continue
				}
				if i > 0 {
					d.SetX(margin + 37)
				}
				d.CellFormat(content-37, 3.9, l, "", 1, "L", false, 0, "")
			}
		}
		d.Ln(1.2)
	}
}

func (d *doc) vmTable() {
	if len(d.r.VMs) == 0 {
		return
	}
	d.AddPage()
	d.h1("Per-VM analysis")
	pc := fmt.Sprintf("p%.0f", d.r.Profile.Percentile)
	rows := [][]string{}
	for _, v := range d.r.VMs {
		rows = append(rows, []string{
			v.Name, v.Cluster,
			arrow(fmt.Sprint(v.VCPU), fmt.Sprint(v.RecVCPU)), arrow(analysis.GiB(v.MemMB), analysis.GiB(v.RecMemMB)),
			fmt.Sprintf("%.0f%%", v.CPUP), fmt.Sprintf("%.0f%%", v.CPUMax), fmt.Sprintf("%.0f%%", v.MemP),
			fmt.Sprintf("%.1f%%", v.ReadyAvg), fmt.Sprintf("%.0fh", v.Hours),
		})
	}
	d.table([]col{
		{"VM", 44, "L"}, {"Cluster", 28, "L"}, {"vCPU", 16, "R"}, {"Memory", 24, "R"},
		{"CPU " + pc, 15, "R"}, {"CPU max", 14, "R"}, {"Mem " + pc, 15, "R"}, {"Ready", 12, "R"}, {"Data", 12, "R"},
	}, rows)
}

func (d *doc) method() {
	p := d.r.Profile
	d.AddPage()
	d.h1("Methodology")
	d.para("rightsizer is strictly read-only. It authenticates to vCenter and only issues property-retrieval and performance-query calls; every other vSphere API method is blocked inside the tool before it is sent. No setting, VM or host was modified to produce this report. A vCenter role with read-only privileges is sufficient.")
	d.para("Every five minutes the collector downloads the 20-second real-time samples of every powered-on VM and host (cpu.usage, cpu.usagemhz, cpu.ready, mem.usage/active, mem.consumed, disk.usage, net.usage). Samples are folded into fixed-resolution histograms, so percentiles reflect every sample over the full window rather than vCenter's averaged roll-ups.")
	d.h2("Rules")
	rules := []string{
		fmt.Sprintf("vCPU: recommended = ceil(vCPU × CPU p%.0f ÷ %.0f%%), minimum 1. VMs with CPU p99 ≥ 90%% are flagged undersized.", p.Percentile, p.CPUTarget*100),
		fmt.Sprintf("Memory: recommended = active memory p%.0f × %.2f, rounded up to 1 GB, never below %.0f%% of current, 1 GB (Linux) or 2 GB (Windows). Active memory p95 ≥ 90%% is flagged undersized.", p.Percentile, p.MemHeadroom, p.MinMemRatio*100),
		"Idle: CPU p95 ≤ 2%, average network < 5 KB/s and disk < 20 KB/s.",
		"Powered-off: no performance samples during the whole window. Reclaimable storage = committed space.",
		"Snapshots: older than 3 days (high priority after 7 days or above 50 GB).",
		"Thick disks: ≥ 20 GB thick-provisioned while the guest file systems are less than 50% used.",
		fmt.Sprintf("Clusters: CPU need = cluster demand p%.0f ÷ %.0f%%; memory need = recommended VM memory + 5%% overhead ÷ %.0f%%; plus one HA host.", p.Percentile, p.HostCPU*100, p.HostMem*100),
		"Confidence: high with ≥ 72 h of data covering ≥ 80% of the window (capped at 7 days), medium with ≥ 24 h, low otherwise.",
	}
	for _, s := range rules {
		d.font("", 8.8)
		d.color(cInk)
		d.CellFormat(4, 4.6, "•", "", 0, "L", false, 0, "")
		d.MultiCell(content-4, 4.6, d.tr(s), "", "L", false)
		d.Ln(0.8)
	}
	d.h2("Before you act")
	d.para("Active memory is what the hypervisor observed being touched; databases, JVMs and caching layers may reserve more than they touch. Validate memory reductions with in-guest metrics and application owners. Reducing vCPU usually requires a VM power cycle unless CPU hot-remove is supported. Month-end or seasonal peaks outside the analysis window are not represented; prefer a two-week window for production systems.")
	d.Ln(4)
	d.font("", 8.5)
	d.color(cMuted)
	d.text(4.4, fmt.Sprintf("rightsizer %s is open-source software by %s (%s), released under the Apache License 2.0. Source: %s", Version, Author, Website, Project))
}

type col struct {
	h     string
	w     float64
	align string
}

func (d *doc) table(cols []col, rows [][]string) {
	total := 0.0
	for _, c := range cols {
		total += c.w
	}
	k := content / total
	head := func() {
		d.font("B", 7.5)
		d.color(cMuted)
		d.fill(cBand)
		for _, c := range cols {
			d.CellFormat(c.w*k, 6, d.tr(c.h), "", 0, c.align, true, 0, "")
		}
		d.Ln(-1)
	}
	head()
	d.font("", 7.5)
	for i, row := range rows {
		if d.GetY()+5 > 279 {
			d.AddPage()
			head()
			d.font("", 7.5)
		}
		d.color(cInk)
		if i%2 == 1 {
			d.fill(rgb{250, 251, 252})
		} else {
			d.fill(rgb{255, 255, 255})
		}
		for j, c := range cols {
			s := ""
			if j < len(row) {
				s = row[j]
			}
			if j == 0 && len(cols) == 6 && cols[0].h == "Priority" {
				d.color(sevColor(sevOf(s)))
			} else {
				d.color(cInk)
			}
			d.CellFormat(c.w*k, 5, d.fit(s, c.w*k-1.5), "", 0, c.align, true, 0, "")
		}
		d.Ln(-1)
	}
	d.draw(cRule)
	d.Line(margin, d.GetY(), pageW-margin, d.GetY())
	d.Ln(3)
}

func (d *doc) fit(s string, w float64) string {
	s = d.tr(s)
	if d.GetStringWidth(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 1 && d.GetStringWidth(string(r)+"…") > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

func sevOf(s string) analysis.Severity {
	switch s {
	case "high":
		return analysis.High
	case "medium":
		return analysis.Medium
	}
	return analysis.Low
}

func sevColor(s analysis.Severity) rgb {
	switch s {
	case analysis.High:
		return cBad
	case analysis.Medium:
		return cWarn
	}
	return cMuted
}

func pct(from, to int) string {
	if from == 0 {
		return "-"
	}
	return fmt.Sprintf("%+.0f%%", float64(to-from)/float64(from)*100)
}

func arrow(a, b string) string {
	if a == b {
		return a
	}
	return a + " → " + b
}

func signed(n int, unit string) string {
	if n == 0 {
		return "-"
	}
	return fmt.Sprintf("%d%s", n, unit)
}

func signedGiB(mb int) string {
	if mb == 0 {
		return "-"
	}
	if mb < 0 {
		return "-" + analysis.GiB(-mb)
	}
	return analysis.GiB(mb)
}

func dash(b int64) string {
	if b == 0 {
		return "-"
	}
	return analysis.Human(b)
}

func dur(d time.Duration) string {
	h := int(d.Hours())
	if h >= 48 {
		return fmt.Sprintf("%dd %dh", h/24, h%24)
	}
	return fmt.Sprintf("%dh %dm", h, int(d.Minutes())%60)
}
