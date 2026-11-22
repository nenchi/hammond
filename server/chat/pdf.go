package chat

// trips as CSV or PDF, sent as a file into the chat ("trips 30 pdf"). The PDF is written by hand: A4
// landscape, the PDF's built-in Helvetica (Windows-1252: €, umlauts, č/š/ž), a table per car.

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strings"
	"time"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
)

func (r tripReport) filename() string {
	return fmt.Sprintf("trips-%s-%s.%s", r.from.Format("2006-01-02"), r.to.Format("2006-01-02"), r.format)
}

func costItems(t trip) string {
	var items []string
	for _, c := range t.Costs {
		items = append(items, fmt.Sprintf("%s %.2f %s", c.ExpenseType, c.Amount, c.Comments))
	}
	return strings.TrimSpace(strings.Join(items, "; "))
}

func (r tripReport) csv() []byte {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.Write([]string{"car", "date", "start", "end", "drive minutes", "stops", "start odometer", "end odometer", "distance " + r.km,
		"business", "purpose", "notes", "fuel l", "fuel cost " + r.curCode, "other costs " + r.curCode, "other cost items", "total " + r.curCode})
	for _, c := range r.cars {
		for _, t := range c.trips {
			w.Write([]string{c.name, t.Start.In(time.Local).Format("2006-01-02"), hm(t.Start), hm(t.End), fmt.Sprint(t.DriveMinutes),
				fmt.Sprint(max(len(t.Legs)-1, 0)), fmt.Sprintf("%.0f", t.StartOdo), fmt.Sprintf("%.0f", t.EndOdo), fmt.Sprintf("%.1f", t.Distance),
				map[bool]string{true: "yes", false: "no"}[t.Business], t.Purpose, t.Notes, fmt.Sprintf("%.2f", t.FuelUsed),
				fmt.Sprintf("%.2f", t.FuelCost), fmt.Sprintf("%.2f", t.ExtraCost), costItems(t), fmt.Sprintf("%.2f", t.FuelCost+t.ExtraCost)})
		}
	}
	w.Flush()
	return buf.Bytes()
}

// ── a minimal PDF ───────────────────────────────────────────────────────────

type pdf struct {
	pages []*bytes.Buffer
	y     float64
}

const pdfW, pdfH, pdfMargin = 842.0, 595.0, 36.0 // A4 landscape, points

var cp1252 = encoding.ReplaceUnsupported(charmap.Windows1252.NewEncoder())

// pdfString: Windows-1252 bytes in a PDF literal string (unsupported characters, e.g. emoji, become "?")
func pdfString(s string) string {
	b, err := cp1252.Bytes([]byte(s))
	if err != nil {
		b = []byte(s)
	}
	return "(" + strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`, "\r", "", "\n", " ").Replace(string(b)) + ")"
}

func (p *pdf) page() {
	p.pages = append(p.pages, &bytes.Buffer{})
	p.y = pdfH - pdfMargin
}

// text at x on the current line; bold = Helvetica-Bold
func (p *pdf) text(x, size float64, bold bool, s string) {
	f := 1
	if bold {
		f = 2
	}
	fmt.Fprintf(p.pages[len(p.pages)-1], "BT /F%d %.1f Tf %.1f %.1f Td %s Tj ET\n", f, size, x, p.y, pdfString(s))
}

func (p *pdf) rule() {
	fmt.Fprintf(p.pages[len(p.pages)-1], "0.6 G 0.5 w %.1f %.1f m %.1f %.1f l S 0 G\n", pdfMargin, p.y-3, pdfW-pdfMargin, p.y-3)
}

// next line; a new page when this one is full (head: the table header to repeat)
func (p *pdf) newline(h float64, head func()) {
	p.y -= h
	if p.y < pdfMargin+20 {
		p.page()
		if head != nil {
			head()
		}
	}
}

func (p *pdf) bytes() []byte {
	var out bytes.Buffer
	var offs []int
	obj := func(body string) {
		offs = append(offs, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", len(offs), body)
	}
	out.WriteString("%PDF-1.4\n")
	n := len(p.pages)
	kids := make([]string, n)
	for i := range p.pages {
		kids[i] = fmt.Sprintf("%d 0 R", 5+2*i)
	}
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	obj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), n))
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>")
	for i, pg := range p.pages {
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.0f %.0f] /Resources << /Font << /F1 3 0 R /F2 4 0 R >> >> /Contents %d 0 R >>",
			pdfW, pdfH, 6+2*i))
		obj(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", pg.Len(), pg.String()))
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offs)+1)
	for _, o := range offs {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offs)+1, xref)
	return out.Bytes()
}

// wrap: lines of at most n characters, broken at spaces (a longer word is split)
func wrap(s string, n int) []string {
	var out []string
	line := ""
	for _, w := range strings.Fields(s) {
		for len([]rune(w)) > n {
			if line != "" {
				out, line = append(out, line), ""
			}
			out, w = append(out, string([]rune(w)[:n])), string([]rune(w)[n:])
		}
		if line == "" {
			line = w
		} else if len([]rune(line))+1+len([]rune(w)) <= n {
			line += " " + w
		} else {
			out, line = append(out, line), w
		}
	}
	if line != "" || len(out) == 0 {
		out = append(out, line)
	}
	return out
}

func (r tripReport) pdf() []byte {
	p := &pdf{}
	p.page()
	cols := []float64{36, 96, 166, 266, 316, 371, 421, 646, 696, 746}
	head := func() {
		for i, h := range []string{"Date", "Time", "Odometer", r.km, "Driving", "Type", "Purpose / notes", "Fuel l", "Fuel " + r.cur, "Other " + r.cur} {
			p.text(cols[i], 9, true, h)
		}
		p.rule()
		p.newline(14, nil)
	}
	p.text(pdfMargin, 16, true, "Trip log")
	p.newline(18, nil)
	p.text(pdfMargin, 10, false, fmt.Sprintf("%s – %s · created %s", r.from.Format("02.01.2006"), r.to.Format("02.01.2006"),
		time.Now().Format("02.01.2006 15:04")))
	p.newline(24, nil)
	for _, c := range r.cars {
		p.text(pdfMargin, 12, true, c.name)
		p.newline(16, nil)
		if len(c.trips) == 0 {
			p.text(pdfMargin, 9, false, "no trips")
			p.newline(22, nil)
			continue
		}
		head()
		var dist, fuel, fuelCost, other, business float64
		var drive int
		for _, t := range c.trips {
			kind := "private"
			if t.Business {
				kind, business = "business", business+t.Distance
			}
			note := t.Purpose
			if t.Notes != "" {
				note = strings.TrimSpace(note + " · " + t.Notes)
			}
			if items := costItems(t); items != "" {
				note = strings.TrimSpace(note + " · " + items)
			}
			for i, v := range []string{t.Start.In(time.Local).Format("02.01.2006"), hm(t.Start) + "–" + hm(t.End),
				fmt.Sprintf("%.0f – %.0f", t.StartOdo, t.EndOdo), fmt.Sprintf("%.1f", t.Distance), mins(t.DriveMinutes), kind,
				"", fmt.Sprintf("%.2f", t.FuelUsed), fmt.Sprintf("%.2f", t.FuelCost), fmt.Sprintf("%.2f", t.ExtraCost)} {
				p.text(cols[i], 9, false, v)
			}
			for _, l := range wrap(note, 48) { // a business log keeps the whole purpose: more lines, not cut
				p.text(cols[6], 9, false, l)
				p.newline(13, head)
			}
			dist, fuel, fuelCost, other, drive = dist+t.Distance, fuel+t.FuelUsed, fuelCost+t.FuelCost, other+t.ExtraCost, drive+t.DriveMinutes
		}
		p.rule()
		p.newline(14, head)
		for i, v := range map[int]string{0: "Total", 3: fmt.Sprintf("%.1f", dist), 4: mins(drive),
			6: fmt.Sprintf("business %.1f %s · total %.2f %s", business, r.km, fuelCost+other, r.cur),
			7: fmt.Sprintf("%.2f", fuel), 8: fmt.Sprintf("%.2f", fuelCost), 9: fmt.Sprintf("%.2f", other)} {
			p.text(cols[i], 9, true, v)
		}
		p.newline(26, nil)
	}
	return p.bytes()
}
