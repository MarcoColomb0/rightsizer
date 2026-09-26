package report

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/atomicfile"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
	"github.com/go-pdf/fpdf"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/goregular"
)

const (
	Author  = "Marco Colombo"
	Website = "https://marco.wf"
	Project = "https://github.com/MarcoColomb0/rightsizer"
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
	tr     func(string) string
	r      *analysis.Result
	title  string
	source string
}

const (
	pageW   = 210.0
	margin  = 15.0
	content = pageW - 2*margin
)

func newDoc(title, source string, created time.Time) *doc {
	p := fpdf.New("P", "mm", "A4", "")
	p.SetMargins(margin, 18, margin)
	p.SetAutoPageBreak(true, 18)
	p.SetTitle(title, true)
	p.SetAuthor("rightsizer by "+Author, true)
	p.SetCreator("rightsizer "+Version, true)
	p.SetCreationDate(created)
	p.AliasNbPages("{nb}")
	p.AddUTF8FontFromBytes("Go", "", goregular.TTF)
	p.AddUTF8FontFromBytes("Go", "B", gobold.TTF)
	p.AddUTF8FontFromBytes("Go", "I", goitalic.TTF)
	d := &doc{Fpdf: p, tr: func(s string) string { return s }, title: title, source: source}
	p.SetFooterFunc(d.footer)
	p.SetHeaderFunc(d.header)
	return d
}

func (d *doc) save(path string) error {
	if err := d.Error(); err != nil {
		return err
	}
	return atomicfile.Write(path, 0o600, d.Output)
}

func WritePDF(r *analysis.Result, path string) error {
	d := newDoc("vSphere Rightsizing Report", r.VCenter, r.Generated)
	d.r = r

	d.cover()
	d.refresh()
	d.peaks()
	d.accuracy()
	d.waste()
	d.excluded()
	d.findingSummary()
	d.findings()
	d.vmTable()
	d.method()
	return d.save(path)
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
	d.CellFormat(content/2, 4, d.tr(d.title), "", 0, "L", false, 0, "")
	d.CellFormat(content/2, 4, d.tr(d.source), "", 1, "R", false, 0, "")
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
	if r.Preview {
		d.Ln(3)
		d.font("I", 8.5)
		d.color(cWarn)
		d.text(4.2, fmt.Sprintf("Preview: part of this report is based on vCenter's historical averages since %s (5-minute to 2-hour samples). Averages smooth out short peaks, so utilisation reads low and recommendations are optimistic. Each VM switches to 20-second data once it has 24 hours of it.", r.HistoryFrom.Format("2006-01-02")))
	} else if !r.Final {
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
		"after rightsizing. Required GHz and GB are hardware-neutral, so they can be matched against any new server model. Host counts assume the current host type plus one HA spare. "+
		"For node shapes, ports and storage capacity, build the hardware refresh sizing PDF from the Sizing tab.",
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
		{"Cluster", 24, "L"}, {"Hosts", 10, "R"}, {"Cores", 11, "R"}, {"CPU p/peak", 17, "R"}, {"Mem p", 11, "R"},
		{"vCPU", 17, "R"}, {"vRAM", 25, "R"}, {"Need GHz", 16, "R"}, {"Need RAM", 18, "R"}, {"Need cores", 18, "R"}, {"Need hosts", 18, "R"},
	}, rows)
	d.font("I", 7.5)
	d.color(cMuted)
	d.text(4, "Need hosts: total (hosts for CPU / hosts for memory + HA spare) using the current host type. CPU p = cluster CPU demand percentile as % of capacity.")
	for _, c := range r.Clusters {
		if ep := c.EarlierPeak; ep != nil {
			d.Ln(1)
			d.font("", 8.5)
			d.color(cWarn)
			d.text(4.4, fmt.Sprintf("%s: vCenter history shows a higher peak of %.0f%% of CPU capacity on %s, before this analysis started. The window itself peaked at %.0f%%. Check whether that load recurs (month-end, batch runs) before relying on these figures.",
				c.Name, ep.Pct, ep.At.Local().Format("Monday 2006-01-02 15:04"), ep.WindowPct))
		}
	}

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

func (d *doc) peaks() {
	var cs []analysis.ClusterResult
	for _, c := range d.r.Clusters {
		if c.Peaks != nil && c.Peaks.CombinedPeakMHz > 0 {
			cs = append(cs, c)
		}
	}
	if len(cs) == 0 {
		return
	}
	d.AddPage()
	d.h1("Peak-aware sizing")
	d.para("VMs rarely peak at the same moment. Sizing a cluster on the sum of every VM's own peak buys hardware for a moment that never happens. " +
		"The diversity factor compares that sum with the peak of the VMs' combined demand, both measured the same way: 1.5× means the naive method asks for 50% more CPU than needed.")
	rows := [][]string{}
	for _, c := range cs {
		pk := c.Peaks
		rows = append(rows, []string{c.Name, ghz(pk.SumPeakMHz), ghz(pk.CombinedPeakMHz), fmt.Sprintf("%.2f×", pk.Diversity),
			fmt.Sprint(pk.NaiveHosts), fmt.Sprint(pk.AwareHosts)})
	}
	d.table([]col{{"Cluster", 50, "L"}, {"Sum of VM peaks", 30, "R"}, {"Combined peak", 28, "R"}, {"Diversity", 20, "R"}, {"Hosts, naive", 24, "R"}, {"Hosts, peak-aware", 28, "R"}}, rows)
	d.font("I", 7.5)
	d.color(cMuted)
	d.text(4, fmt.Sprintf("Host counts cover CPU only, at the %s profile's %.0f%% target, before memory sizing and the HA spare.", d.r.Profile.Name, d.r.Profile.HostCPU*100))
	for _, c := range cs {
		if d.GetY() > 200 {
			d.AddPage()
		}
		d.h2(c.Name + ": demand by hour of week")
		d.heatmap(c.Peaks)
		for _, g := range c.Peaks.CoPeak {
			d.font("", 8.5)
			d.color(cInk)
			d.text(4.4, fmt.Sprintf("Peak together (r %.2f): %s. Their peaks add up; check whether they share a schedule, such as a batch window.", g.R, strings.Join(g.VMs, ", ")))
		}
		for i, p := range c.Peaks.Complementary {
			if i == 5 {
				break
			}
			d.font("", 8.5)
			d.color(cMuted)
			d.text(4.4, fmt.Sprintf("Peak at different times (r %.2f): %s and %s. Pairs like these are why the cluster needs less than the sum of its peaks.", p.R, p.A, p.B))
		}
		d.Ln(2)
	}
}

func (d *doc) accuracy() {
	a := d.r.Accuracy
	if a == nil {
		return
	}
	if d.GetY() > 190 {
		d.AddPage()
	} else {
		d.Ln(4)
	}
	d.h1("How accurate is vCenter's history?")
	d.para(fmt.Sprintf("While collecting 20-second samples, rightsizer also read vCenter's own 5-minute averages for the same period and compared them across %d VMs (about %.0f hours each). "+
		"At the %.0fth percentile, the averages read CPU %.0f%% lower and memory %.0f%% lower than the real samples. Sizing from vCenter history alone would under-size bursty workloads like the ones below.",
		a.VMs, a.HoursBoth, a.Percentile, (1-a.CPUMedian)*100, (1-a.MemMedian)*100))
	if len(a.Bursty) == 0 {
		return
	}
	rows := [][]string{}
	for _, x := range a.Bursty {
		rows = append(rows, []string{x.VM, fmt.Sprintf("%.0f%%", x.RealtimeP), fmt.Sprintf("%.0f%%", x.HistoryP), fmt.Sprintf("%.0f%%", x.RealtimeMx)})
	}
	pc := fmt.Sprintf("p%.0f", a.Percentile)
	d.table([]col{{"Bursty VM", 80, "L"}, {"CPU " + pc + ", 20-second", 35, "R"}, {"CPU " + pc + ", vCenter average", 40, "R"}, {"CPU peak", 25, "R"}}, rows)
}

func (d *doc) heatmap(pk *analysis.Peaks) {
	x0, y0 := margin+12, d.GetY()+1
	cw, ch := (content-14)/24, 4.2
	d.font("", 6.5)
	d.color(cMuted)
	for h := 0; h < 24; h += 3 {
		d.SetXY(x0+float64(h)*cw, y0)
		d.CellFormat(cw*3, 3, fmt.Sprintf("%02d:00", h), "", 0, "L", false, 0, "")
	}
	y0 += 4
	for day, name := range []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"} {
		d.SetXY(margin, y0+float64(day)*ch)
		d.color(cMuted)
		d.CellFormat(11, ch, name, "", 0, "L", false, 0, "")
		for h := range 24 {
			c := cBand
			if pk.HeatmapN[day][h] > 0 {
				v := min(pk.Heatmap[day][h]/100, 1)
				c = rgb{int(float64(cBand.r) + (float64(cAccent.r)-float64(cBand.r))*v), int(float64(cBand.g) + (float64(cAccent.g)-float64(cBand.g))*v), int(float64(cBand.b) + (float64(cAccent.b)-float64(cBand.b))*v)}
				if pk.Heatmap[day][h] >= 80 {
					c = cBad
				}
			}
			d.fill(c)
			d.Rect(x0+float64(h)*cw+0.15, y0+float64(day)*ch+0.15, cw-0.3, ch-0.3, "F")
		}
	}
	d.SetY(y0 + 7*ch + 1)
	d.font("", 7)
	d.color(cMuted)
	d.CellFormat(content, 4, "Average CPU demand as % of cluster capacity; darker is busier, red is 80% or more.", "", 1, "L", false, 0, "")
	d.Ln(1)
}

func (d *doc) waste() {
	r := d.r
	if len(r.Orphans) == 0 && r.WasteNote == "" {
		return
	}
	if d.GetY() > 180 {
		d.AddPage()
	} else {
		d.Ln(4)
	}
	d.h1("Hidden waste")
	if r.WasteNote != "" {
		d.font("I", 8.5)
		d.color(cWarn)
		d.text(4.4, r.WasteNote)
		d.Ln(1)
	}
	if len(r.Orphans) == 0 {
		return
	}
	var total int64
	rows := [][]string{}
	for _, o := range r.Orphans {
		total += o.Size
		mod := ""
		if !o.Modified.IsZero() {
			mod = o.Modified.Format("2006-01-02")
		}
		rows = append(rows, []string{o.Path, analysis.Human(o.Size), mod})
	}
	files, verb := "virtual disk files", "are"
	if len(r.Orphans) == 1 {
		files, verb = "virtual disk file", "is"
	}
	d.para(fmt.Sprintf("%d %s (%s) %s not used by any VM or template registered in this vCenter. "+
		"Before deleting one, check that it does not belong to another vCenter sharing the datastore, a backup or replication product, or a VM being restored.", len(r.Orphans), files, analysis.Human(total), verb))
	d.table([]col{{"Disk", 120, "L"}, {"Size", 25, "R"}, {"Last changed", 30, "R"}}, rows)
}

func (d *doc) excluded() {
	xs := d.r.Excluded
	if len(xs) == 0 {
		return
	}
	if d.GetY() > 200 {
		d.AddPage()
	} else {
		d.Ln(4)
	}
	d.h1("Excluded from recommendations")
	d.para("These VMs and disks were deliberately left out. Excluded VMs count at their provisioned size in every total, so the savings above are not overstated.")
	for _, x := range xs {
		if d.GetY() > 262 {
			d.AddPage()
		}
		d.font("B", 8.5)
		d.color(cInk)
		d.CellFormat(content, 5, d.tr(x.Target()+"  ·  "+x.Scope()), "", 1, "L", false, 0, "")
		d.font("", 8)
		d.SetX(margin + 3)
		d.MultiCell(content-3, 4, d.tr(x.Reason+": "+x.Note), "", "L", false)
		meta := "Excluded since " + x.Created.Format("2006-01-02")
		if len(x.Matched) > 1 || x.Pattern() {
			meta += fmt.Sprintf(" · matches %d: %s", len(x.Matched), strings.Join(x.Matched, ", "))
		}
		d.SetX(margin + 3)
		d.font("", 7.5)
		d.color(cMuted)
		if x.Overdue(d.r.Generated) {
			d.color(cBad)
			meta += " · review overdue since " + x.ReviewBy.Format("2006-01-02")
		} else if !x.ReviewBy.IsZero() {
			meta += " · review by " + x.ReviewBy.Format("2006-01-02")
		}
		d.MultiCell(content-3, 3.8, d.tr(meta), "", "L", false)
		d.Ln(1.5)
	}
}

func ghz(mhz float64) string { return fmt.Sprintf("%.0f GHz", mhz/1000) }

func (d *doc) findingSummary() {
	d.AddPage()
	d.h1("Findings")
	type agg struct {
		n, vcpu, mem int
		bytes        int64
		sev          analysis.Severity
		impact       float64
	}
	by := map[analysis.Kind]*agg{}
	for _, f := range d.r.Findings {
		a := by[f.Kind]
		if a == nil {
			a = &agg{}
			by[f.Kind] = a
		}
		a.n++
		a.sev = max(a.sev, f.Severity)
		a.impact += f.Impact
		a.vcpu += f.VCPU
		a.mem += f.MemMB
		a.bytes += f.Bytes
	}
	kinds := make([]string, 0, len(by))
	for k := range by {
		kinds = append(kinds, string(k))
	}
	sort.SliceStable(kinds, func(i, j int) bool {
		a, b := by[analysis.Kind(kinds[i])], by[analysis.Kind(kinds[j])]
		if a.sev != b.sev {
			return a.sev > b.sev
		}
		if a.impact != b.impact {
			return a.impact > b.impact
		}
		return kinds[i] < kinds[j]
	})
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
	// Findings are already ordered by criticality; VMs keep the order of
	// their most critical finding.
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
			fmt.Sprintf("%.1f%%", v.ReadyAvg), dataLabel(v),
		})
	}
	d.table([]col{
		{"VM", 44, "L"}, {"Cluster", 28, "L"}, {"vCPU", 16, "R"}, {"Memory", 24, "R"},
		{"CPU " + pc, 15, "R"}, {"CPU max", 14, "R"}, {"Mem " + pc, 15, "R"}, {"Ready", 11, "R"}, {"Data", 14, "R"},
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
		"NUMA: a VM is flagged when its vCPUs exceed the cores of one physical NUMA node, or its memory exceeds one node's memory.",
		"Co-stop: average co-stop of 3% or more per vCPU on a multi-vCPU VM means it waits for enough free cores; fewer vCPUs make it faster.",
		"Peak-aware sizing: diversity = sum of each VM's percentile of 30-minute CPU demand ÷ the same percentile of the VMs' combined demand. VMs whose 30-minute demand correlates at 0.8 or more are reported as peaking together, -0.4 or less as peaking at different times. Placement is left to DRS.",
		"Orphaned disks: .vmdk files on accessible datastores that no registered VM, template or snapshot references. First-class disks and replication, HA and vSAN system folders are ignored. Needs the Browse datastore privilege.",
		"History accuracy: during the window, vCenter's finest stored interval is read every hour and compared with the 20-second data for the same period, for VMs with at least 24 hours of both.",
		"Preview: until a VM has 24 hours of 20-second data, results use vCenter's stored history for the previous 14 days, read at the finest interval available for each period and weighted by the time each sample covers.",
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

func dataLabel(v analysis.VMResult) string {
	if v.Preview {
		return fmt.Sprintf("%.0fh hist", v.Hours)
	}
	return fmt.Sprintf("%.0fh", v.Hours)
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
	default:
		return cMuted
	}
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
