package report

import (
	"fmt"
	"math"
	"strings"

	"codeberg.org/go-pdf/fpdf"
)

type rgb struct{ r, g, b int }

// Twilight palette, shared with the console: ice blues fading into violets.
var (
	cInk    = rgb{30, 26, 51}
	cMuted  = rgb{93, 88, 128}
	cFaint  = rgb{143, 137, 184}
	cRule   = rgb{228, 225, 244}
	cBand   = rgb{242, 240, 251}
	cZebra  = rgb{250, 249, 254}
	cTint   = rgb{236, 233, 251}
	cWhite  = rgb{255, 255, 255}
	cLake   = rgb{151, 223, 252}
	cSky    = rgb{147, 202, 246}
	cIce    = rgb{142, 181, 240}
	cPeri   = rgb{133, 138, 227}
	cSlate  = rgb{115, 100, 210}
	cAccent = rgb{97, 61, 193}
	cDeep   = rgb{61, 14, 97}
	cProv   = rgb{214, 210, 238}
	cGood   = rgb{21, 128, 61}
	cWarn   = rgb{180, 83, 9}
	cBad    = rgb{194, 51, 77}

	cWarnTint = rgb{254, 243, 230}
	cBadTint  = rgb{252, 231, 235}
)

// heatStops colour load from idle to busy.
var heatStops = []rgb{cBand, {212, 226, 250}, cIce, cPeri, cAccent, cDeep}

func mix(a, b rgb, t float64) rgb {
	t = min(max(t, 0), 1)
	f := func(x, y int) int { return int(math.Round(float64(x) + (float64(y)-float64(x))*t)) }
	return rgb{f(a.r, b.r), f(a.g, b.g), f(a.b, b.b)}
}

// along picks the colour at v (0..1) along evenly spaced stops.
func along(stops []rgb, v float64) rgb {
	v = min(max(v, 0), 1) * float64(len(stops)-1)
	i := min(int(v), len(stops)-2)
	return mix(stops[i], stops[i+1], v-float64(i))
}

func (d *doc) color(c rgb)                 { d.SetTextColor(c.r, c.g, c.b) }
func (d *doc) fill(c rgb)                  { d.SetFillColor(c.r, c.g, c.b) }
func (d *doc) draw(c rgb)                  { d.SetDrawColor(c.r, c.g, c.b) }
func (d *doc) font(s string, size float64) { d.SetFont("Go", s, size) }

// gradient fills a rectangle from a on the left to b on the right.
func (d *doc) gradient(x, y, w, h float64, a, b rgb) {
	d.LinearGradient(x, y, w, h, a.r, a.g, a.b, b.r, b.g, b.b, 0, 0, 1, 0)
}

// gradientRound is gradient with rounded corners.
func (d *doc) gradientRound(x, y, w, h, r float64, a, b rgb) {
	if w <= 0 || h <= 0 {
		return
	}
	d.ClipRoundedRect(x, y, w, h, min(r, w/2, h/2), false)
	d.gradient(x, y, w, h, a, b)
	d.ClipEnd()
}

// diamond draws the rightsizer mark centred on x, y.
func (d *doc) diamond(x, y, r float64, c rgb) {
	d.fill(c)
	d.Polygon([]fpdf.PointType{{X: x, Y: y - r}, {X: x + r, Y: y}, {X: x, Y: y + r}, {X: x - r, Y: y}}, "F")
}

// pill draws a rounded badge at x, y and returns its width.
func (d *doc) pill(x, y float64, text string, bg, fg rgb, size float64) float64 {
	d.font("B", size)
	h := size * 0.55
	w := d.GetStringWidth(text) + h*1.1
	d.fill(bg)
	d.RoundedRect(x, y, w, h, h/2, "1234", "F")
	d.color(fg)
	d.SetXY(x, y)
	d.CellFormat(w, h, d.tr(text), "", 0, "C", false, 0, "")
	return w
}

func sevPill(s string) (bg, fg rgb) {
	switch s {
	case "high":
		return cBadTint, cBad
	case "medium":
		return cWarnTint, cWarn
	}
	return cTint, cAccent
}

func (d *doc) text(h float64, s string) {
	d.MultiCell(content, h, d.tr(s), "", "L", false)
}

func (d *doc) para(s string) {
	d.font("", 9)
	d.color(cInk)
	d.text(4.7, s)
	d.Ln(1.5)
}

// note is the small print under a table or chart.
func (d *doc) note(s string) {
	d.font("I", 7.5)
	d.color(cMuted)
	d.text(4, s)
}

// label is a small coloured heading inside a section.
func (d *doc) label(s string, c rgb) {
	d.Ln(1)
	d.font("B", 7)
	d.color(c)
	d.CellFormat(content, 4.5, d.tr(strings.ToUpper(s)), "", 1, "L", false, 0, "")
}

func (d *doc) bullet(s string) {
	d.font("", 8.8)
	d.color(cInk)
	y := d.GetY()
	d.fill(cPeri)
	d.RoundedRect(margin+0.4, y+1.75, 1.3, 1.3, 0.3, "1234", "F")
	d.SetX(margin + 4)
	d.MultiCell(content-4, 4.6, d.tr(s), "", "L", false)
	d.Ln(0.8)
}

// callout boxes a remark with a coloured bar on its left.
func (d *doc) callout(s string, bar, bg rgb) {
	d.font("", 8.5)
	lines := d.SplitText(d.tr(s), content-9)
	h := float64(len(lines))*4.4 + 4.4
	if d.GetY()+h > 279 {
		d.AddPage()
	}
	y := d.GetY() + 1
	d.fill(bg)
	d.RoundedRect(margin, y, content, h, 1.6, "1234", "F")
	d.fill(bar)
	d.RoundedRect(margin, y, 1.3, h, 0.65, "1234", "F")
	d.SetXY(margin+5, y+2.2)
	d.color(cInk)
	d.MultiCell(content-9, 4.4, d.tr(s), "", "L", false)
	d.SetY(y + h + 2.5)
}

func (d *doc) header() {
	if d.PageNo() == 1 {
		return
	}
	d.gradient(0, 0, pageW, 1.4, cLake, cAccent)
	d.diamond(margin+1.3, 8.6, 1.3, cAccent)
	d.SetXY(margin+3.6, 6.6)
	d.font("B", 7.5)
	d.color(cAccent)
	d.CellFormat(16, 4, "rightsizer", "", 0, "L", false, 0, "")
	d.font("", 7.5)
	d.color(cMuted)
	d.CellFormat(content/2-20, 4, d.tr(d.title), "", 0, "L", false, 0, "")
	d.CellFormat(content/2, 4, d.tr(d.source), "", 1, "R", false, 0, "")
	d.draw(cRule)
	d.SetLineWidth(0.2)
	d.Line(margin, 12.5, pageW-margin, 12.5)
	d.SetY(18)
}

func (d *doc) footer() {
	d.SetY(-12)
	d.font("", 7.5)
	d.color(cFaint)
	d.CellFormat(content*0.75, 4, d.tr(fmt.Sprintf("rightsizer %s · open source by %s · %s", Version, Author, Website)), "", 0, "L", false, 0, Website)
	d.font("B", 7.5)
	d.color(cAccent)
	d.CellFormat(content*0.25, 4, fmt.Sprintf("%d / {nb}", d.PageNo()), "", 0, "R", false, 0, "")
}

// h1 starts a numbered section and adds it to the document outline.
func (d *doc) h1(s string) {
	d.sec++
	if d.GetY() > 245 {
		d.AddPage()
	}
	y := d.GetY()
	d.Bookmark(s, 0, y)
	num := fmt.Sprintf("%02d", d.sec)
	d.font("B", 17)
	d.color(cPeri)
	d.SetXY(margin, y)
	d.CellFormat(d.GetStringWidth(num)+3, 9, num, "", 0, "L", false, 0, "")
	d.color(cInk)
	d.CellFormat(content-12, 9, d.tr(s), "", 1, "L", false, 0, "")
	d.gradientRound(margin, y+10.5, 24, 1.1, 0.55, cLake, cAccent)
	d.SetY(y + 15)
}

func (d *doc) h2(s string) {
	d.Ln(2)
	if d.GetY() > 262 {
		d.AddPage()
	}
	y := d.GetY()
	d.Bookmark(s, 1, y)
	d.fill(cAccent)
	d.RoundedRect(margin, y+2.1, 2.2, 2.2, 0.5, "1234", "F")
	d.SetXY(margin+4, y)
	d.font("B", 11)
	d.color(cInk)
	d.CellFormat(content-4, 6.5, d.tr(s), "", 1, "L", false, 0, "")
	d.Ln(0.5)
}

// hero draws the cover band: a twilight gradient with the title.
func (d *doc) hero(title, subtitle, status string) {
	d.AddPage()
	const h = 84.0
	d.LinearGradient(0, 0, pageW, h, cDeep.r, cDeep.g, cDeep.b, cAccent.r, cAccent.g, cAccent.b, 0, 0, 1, 1)
	d.ClipRect(0, 0, pageW, h, false)
	for _, c := range []struct {
		x, y, r, a float64
		c          rgb
	}{{pageW - 18, 6, 50, 0.10, cLake}, {pageW - 64, 80, 30, 0.12, cPeri}, {pageW - 10, 66, 16, 0.16, cSky}, {pageW - 44, 30, 7, 0.18, cLake}} {
		d.SetAlpha(c.a, "Normal")
		d.fill(c.c)
		d.Circle(c.x, c.y, c.r, "F")
	}
	d.SetAlpha(1, "Normal")
	d.ClipEnd()
	d.diamond(margin+1.8, 19, 1.9, cLake)
	d.SetXY(margin+5, 16.5)
	d.font("B", 11)
	d.color(cWhite)
	d.CellFormat(60, 5, "rightsizer", "", 0, "L", false, 0, "")
	d.pill(margin, 37, status, cLake, cDeep, 6.5)
	d.SetXY(margin, 43)
	d.font("B", 25)
	d.color(cWhite)
	d.CellFormat(content, 12, d.tr(title), "", 1, "L", false, 0, "")
	d.SetX(margin)
	d.font("", 11.5)
	d.color(cLake)
	d.CellFormat(content, 7, d.tr(subtitle), "", 1, "L", false, 0, "")
	d.SetY(h + 8)
}

// meta lists the facts under the cover band.
func (d *doc) meta(rows [][2]string) {
	for _, m := range rows {
		y := d.GetY()
		d.SetX(margin)
		d.font("B", 6.8)
		d.color(cFaint)
		d.CellFormat(36, 5.6, d.tr(strings.ToUpper(m[0])), "", 0, "L", false, 0, "")
		d.font("", 9)
		d.color(cInk)
		d.MultiCell(content-36, 5.6, d.tr(m[1]), "", "L", false)
		if d.GetY() < y+5.6 {
			d.SetY(y + 5.6)
		}
	}
	d.Ln(5)
}

type kpi struct {
	label, value, sub string
	subColor          rgb
}

// kpis draws a row of headline figures.
func (d *doc) kpis(ks []kpi) {
	const h, gap = 27.0, 4.0
	w := (content - gap*float64(len(ks)-1)) / float64(len(ks))
	y := d.GetY()
	for i, k := range ks {
		x := margin + float64(i)*(w+gap)
		d.fill(cBand)
		d.RoundedRect(x, y, w, h, 2.4, "1234", "F")
		d.gradientRound(x+5, y+4.2, 9, 1.1, 0.55, cLake, cAccent)
		d.SetXY(x+5, y+7)
		d.font("B", 6.8)
		d.color(cMuted)
		d.CellFormat(w-8, 4, d.tr(strings.ToUpper(k.label)), "", 2, "L", false, 0, "")
		d.SetX(x + 5)
		d.font("B", 14)
		for size := 14.0; d.GetStringWidth(k.value) > w-9 && size > 7; size -= 0.5 {
			d.SetFontSize(size - 0.5)
		}
		d.color(cInk)
		d.CellFormat(w-8, 8, d.tr(k.value), "", 2, "L", false, 0, "")
		d.SetX(x + 5)
		d.font("", 7.8)
		d.color(k.subColor)
		d.CellFormat(w-8, 4, d.tr(k.sub), "", 2, "L", false, 0, "")
	}
	d.SetY(y + h + 6)
}

// change colours a before/after delta: lower is good news.
func change(from, to int) rgb {
	if to <= from {
		return cGood
	}
	return cWarn
}

type col struct {
	h     string
	w     float64
	align string
}

type tableOpts struct {
	// pill is the column drawn as a severity badge, or -1.
	pill int
	// hl marks rows to highlight, such as the recommended option.
	hl func(row int) bool
}

func (d *doc) table(cols []col, rows [][]string) {
	d.tableWith(cols, rows, tableOpts{pill: -1})
}

func (d *doc) tableWith(cols []col, rows [][]string, o tableOpts) {
	total := 0.0
	for _, c := range cols {
		total += c.w
	}
	k := content / total
	const rowH = 5.4
	head := func() {
		d.font("B", 7.2)
		d.color(cMuted)
		d.fill(cBand)
		d.RoundedRect(margin, d.GetY(), content, 6.4, 1.2, "1234", "F")
		for _, c := range cols {
			d.CellFormat(c.w*k, 6.4, d.tr(c.h), "", 0, c.align, false, 0, "")
		}
		d.Ln(-1)
		d.Ln(0.6)
	}
	if d.GetY()+6.4+rowH > 279 {
		d.AddPage()
	}
	head()
	for i, row := range rows {
		if d.GetY()+rowH > 279 {
			d.AddPage()
			head()
		}
		hl := o.hl != nil && o.hl(i)
		switch {
		case hl:
			d.fill(cTint)
		case i%2 == 1:
			d.fill(cZebra)
		default:
			d.fill(cWhite)
		}
		y := d.GetY()
		d.Rect(margin, y, content, rowH, "F")
		if hl {
			d.fill(cAccent)
			d.RoundedRect(margin-2, y+0.6, 0.9, rowH-1.2, 0.45, "1234", "F")
		}
		x := margin
		for j, c := range cols {
			s := ""
			if j < len(row) {
				s = row[j]
			}
			w := c.w * k
			d.SetXY(x, y)
			if j == o.pill && s != "" {
				bg, fg := sevPill(s)
				d.pill(x+1, y+1, s, bg, fg, 6.2)
			} else {
				d.font("", 7.5)
				d.color(cInk)
				if hl {
					d.font("B", 7.5)
					if j == 0 {
						d.color(cAccent)
					}
				}
				d.CellFormat(w, rowH, d.fit(s, w-1.5), "", 0, c.align, false, 0, "")
			}
			x += w
		}
		d.SetXY(margin, y+rowH)
	}
	d.draw(cRule)
	d.SetLineWidth(0.25)
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

// legend draws a colour swatch and its label, returning the next x.
func (d *doc) legend(x, y float64, c rgb, text string) float64 {
	d.fill(c)
	d.RoundedRect(x, y+1.3, 3.2, 1.4, 0.7, "1234", "F")
	d.SetXY(x+4.2, y)
	d.font("", 6.8)
	d.color(cMuted)
	w := d.GetStringWidth(text) + 1
	d.CellFormat(w, 4, d.tr(text), "", 0, "L", false, 0, "")
	return x + 4.2 + w + 5
}

// area shades the region under a line with a translucent colour.
func (d *doc) area(pts []fpdf.PointType, base float64, c rgb) {
	if len(pts) < 2 {
		return
	}
	poly := append([]fpdf.PointType{{X: pts[0].X, Y: base}}, pts...)
	poly = append(poly, fpdf.PointType{X: pts[len(pts)-1].X, Y: base})
	d.SetAlpha(0.14, "Normal")
	d.fill(c)
	d.Polygon(poly, "F")
	d.SetAlpha(1, "Normal")
}

// line draws a polyline.
func (d *doc) line(pts []fpdf.PointType, c rgb, w float64) {
	d.draw(c)
	d.SetLineWidth(w)
	d.SetLineJoinStyle("round")
	d.SetLineCapStyle("round")
	for i := 1; i < len(pts); i++ {
		d.Line(pts[i-1].X, pts[i-1].Y, pts[i].X, pts[i].Y)
	}
	d.SetLineWidth(0.2)
}

// grid draws horizontal guides with labels on the left.
func (d *doc) grid(x0, y0, w, h float64, labels []string) {
	d.draw(cRule)
	d.SetLineWidth(0.2)
	d.font("", 6.5)
	d.color(cFaint)
	n := len(labels) - 1
	for i, l := range labels {
		y := y0 + h - h*float64(i)/float64(n)
		d.Line(x0, y, x0+w, y)
		d.SetXY(margin, y-1.5)
		d.CellFormat(x0-margin-1.5, 3, l, "", 0, "R", false, 0, "")
	}
}
