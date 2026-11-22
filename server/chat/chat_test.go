package chat

import (
	"bytes"
	"fmt"
	"image/png"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAverageByHour(t *testing.T) { // same cases as hammond's tests/price_hours_check.mjs
	at := func(h, m int) time.Time { return time.Date(2026, 11, 18, h, m, 0, 0, time.Local) }
	avg, ok := averageByHour([]pricePoint{{1.8, at(6, 0)}, {1.7, at(6, 30)}, {1.9, at(8, 0)}}, at(9, 0))
	if math.Abs(avg[6]-1.75) > 1e-9 || avg[7] != 1.7 || avg[8] != 1.9 || ok[5] || ok[9] {
		t.Fatalf("got %v %v", avg, ok)
	}
	if _, ok := averageByHour(nil, at(9, 0)); ok != [24]bool{} {
		t.Fatal("empty input must give no hours")
	}
}

// fake hammond /api/v1 with one car and two E10 stations
func TestHammondCommands(t *testing.T) {
	now := time.Now()
	day := func(d int) string { return now.AddDate(0, 0, -d).UTC().Format(time.RFC3339) }
	// today's trip: since midnight, so the test also passes just after 00:00
	tripStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	routes := map[string]string{
		"/api/v1/me":       `{"currency":"EUR","distanceUnitShort":"Km"}`,
		"/api/v1/vehicles": `[{"id":"v1","nickname":"A180","fuelUnit":"Lt"}]`,
		"/api/v1/vehicles/v1/fillups": `[{"date":"` + day(40) + `","fuelQuantity":30,"perUnitPrice":1.7,"totalAmount":51},` +
			`{"date":"` + now.UTC().Format(time.RFC3339) + `","fuelQuantity":40,"perUnitPrice":1.6,"totalAmount":64,"fillingStation":"Aral, Hauptstr. 1"}]`,
		"/api/v1/vehicles/v1/expenses": `[{"date":"` + now.UTC().Format(time.RFC3339) + `","amount":20}]`,
		"/api/v1/vehicles/v1/mileage":  `[{"fuelQuantity":40,"mileage":16},{"fuelQuantity":30,"mileage":0}]`,
		"/api/v1/vehicles/v1/trips": `[{"start":"` + tripStart.UTC().Format(time.RFC3339) + `","end":"` + tripStart.Add(time.Minute).UTC().Format(time.RFC3339) +
			`","startOdo":81233,"endOdo":81245,"distance":12.4,"driveMinutes":43,"fuelUsed":1.75,"fuelCost":3.06,"extraCost":12.5,` +
			`"business":true,"purpose":"Customer A","legs":[{},{}],"costs":[{"expenseType":"Toll","amount":12.5}]}]`,
		"/api/v1/me/priceWatches": `[{"id":"w1","name":"Aral","kind":"econtrol","grade":"e10","lastPrice":1.689,"below":1.65},` +
			`{"id":"w2","name":"Shell","kind":"tankerkoenig","grade":"e10","lastPrice":1.659},{"id":"w3","name":"Omv","grade":"diesel","lastError":"timeout"}]`,
		"/api/v1/me/priceWatches/w1/history": `{"prices":[{"price":1.8,"at":"` + day(40) + `"},{"price":1.75,"at":"` + day(20) + `"},{"price":1.7,"at":"` + day(2) + `"}]}`,
		"/api/v1/me/priceWatches/w2/history": `{"prices":[{"price":1.6,"at":"` + day(2) + `"}]}`,
	}
	Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if UserFromContext(r.Context()) != "u1" { // the commands run as the chatting user
			http.Error(w, "no", 401)
			return
		}
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	})
	b := &bot{hammond: &hammond{userID: "u1"}}
	cmd := func(text string) string { return Command("u1", text).Text }
	// graph sends a picture: its text bars (the fallback) are tested through b.graph
	gtext := func(text string) string { r, _ := b.graph(strings.Fields(text)[1:]); return r }
	s := cmd("stats")
	for _, want := range []string{"**🚗 A180", "· 64.00 EUR\n40.0 l × 1.600\n📍 Aral, Hauptstr. 1", ": 84.00 EUR\n1 fill-up · other 20.00", "📉 6.2 l/100 km (90 days)"} {
		if !strings.Contains(s, want) {
			t.Errorf("stats: missing %q in\n%s", want, s)
		}
	}
	if s := cmd("stats golf"); !strings.Contains(s, "no vehicle matches golf") {
		t.Errorf("stats filter: %s", s)
	}
	p := cmd("prices")
	// grade, then country (AT before DE, even though DE is cheaper), then price
	if !strings.Contains(p, "**⛽ Super E10\nAT 1.689€ - Aral - 🔔: 1.650€\nDE 1.659€ - Shell") ||
		!strings.Contains(p, "**⛽ Diesel\n— - Omv ⚠️ timeout") {
		t.Errorf("prices:\n%s", p)
	}
	g := gtext("graph fuel 14")
	if !strings.HasPrefix(g, "**📊 ⛽ Fuel · 14 days\n🚗 A180\n`") || strings.Count(g, "█") != 8 ||
		!strings.HasSuffix(g, "40 l\nΣ 64.00€ · 1 fill-up\n40.00 l · ⌀ 1.600€/l") {
		t.Errorf("graph fuel:\n%s", g)
	}
	if g := gtext("graph costs 3m"); !strings.Contains(g, "Σ 135.00€ · 3 entries") || strings.Count(g, "`") != 8 {
		t.Errorf("graph costs (4 month lines):\n%s", g)
	}
	if g := gtext("graph other all a180"); !strings.Contains(g, "🔧 Other costs · all time") || !strings.Contains(g, "Σ 20.00€ · 1 entry") {
		t.Errorf("graph other:\n%s", g)
	}
	for in, want := range map[string]string{"graph": "usage: graph", "graph fuel 9x": "range:", "graph fuel 0": "range:", "graph fuel golf": "no vehicle matches golf"} {
		if g := gtext(in); !strings.HasPrefix(g, want) {
			t.Errorf("%s: %q", in, g)
		}
	}
	tr := cmd("trips today")
	for _, want := range []string{"**🚗 A180 · " + now.Format("Mon 2 Jan"), "⛽ " + now.In(time.Local).Format("15:04") + " 40.0 l · 64.00€",
		"💼 " + tripStart.Format("15:04") + "–", "· 12.4 km\n81233 → 81245 km\n43 min driving · 1 stop", "1.75 l · 3.06€\n💶 Toll 12.50€\n📝 Customer A",
		"**Σ 12.4 km · 43 min\n1.75 l · 15.56€"} {
		if !strings.Contains(tr, want) {
			t.Errorf("trips: missing %q in\n%s", want, tr)
		}
	}
	c := cmd("cheap-time e10")
	// numbers by name: 1 Aral, 2 Omv, 3 Shell. Shell is cheaper now (1.659 < 1.689): first.
	if !strings.Contains(c, "**⏰ Super E10 · 30 days\n3 · Shell · now 1.659€\n⬇ 00:00–24:00 · 1.600€\n1 · Aral · now 1.689€") ||
		strings.Contains(c, "Diesel") {
		t.Errorf("cheap-time:\n%s", c)
	}
	if c := cmd("cheap-time 3 7"); !strings.HasPrefix(c, "**⏰ Super E10 · 7 days\n3 · Shell") || strings.Contains(c, "Aral") {
		t.Errorf("cheap-time 3 7:\n%s", c)
	}
	wl := cmd("watched")
	if !strings.Contains(wl, "1 · Aral · Super E10 · AT\n") || !strings.Contains(wl, "2 · Omv · Diesel\n3 · Shell · Super E10 · DE") {
		t.Errorf("watched:\n%s", wl)
	}
	p7 := cmd("price 1")
	if !strings.HasPrefix(p7, "**⛽ 1 · Aral · Super E10\n📅 7 days\nnow 1.689€\n⌀ ") || !strings.Contains(p7, "⬇ 1.700€") ||
		!strings.Contains(p7, "⬆ 1.750€") || strings.Contains(p7, "`day") {
		t.Errorf("price 1 (7 days: summary, no table):\n%s", p7)
	}
	if p := cmd("price 1 30"); !strings.Contains(p, "`day") || strings.Count(p, "\n`") < 31 {
		t.Errorf("price 1 30 (header + a row per day):\n%s", p)
	}
	if p := cmd("price 1 3m"); !strings.Contains(p, "`month") || !strings.Contains(p, "⬆ 1.800€") {
		t.Errorf("price 1 3m (monthly):\n%s", p)
	}
	if p := cmd("price 9"); !strings.HasPrefix(p, "no watch 9 — numbers 1–3") {
		t.Errorf("price 9: %s", p)
	}
	if c := cmd("cheap-time lpg"); !strings.Contains(c, "no watched station has LPG") {
		t.Errorf("cheap-time lpg: %s", c)
	}
	if r := Command("u1", "graph fuel 30"); r.Image == nil || !strings.HasPrefix(r.Text, "⛽ Fuel · 30 days") {
		t.Errorf("graph: picture + title, got %q", r.Text)
	}
	if r := Command("u1", "/trips today pdf"); !bytes.HasPrefix(r.File, []byte("%PDF")) || r.FileType != "application/pdf" {
		t.Errorf("trips pdf: %q %s", r.FileName, r.FileType)
	}
	if r := Command("u2", "prices"); !strings.Contains(r.Text, "HTTP 401") {
		t.Errorf("another user: %q", r.Text)
	}
	if r := Command("u1", "/help@my_bot"); !strings.Contains(r.Text, "`trips 30 pdf`") {
		t.Errorf("help: %q", r.Text)
	}
	if r := Command("u1", "nope"); !strings.HasPrefix(r.Text, "unknown command nope") {
		t.Errorf("unknown: %q", r.Text)
	}
}

func TestHourRanges(t *testing.T) {
	var avg [24]float64
	var ok [24]bool
	for h := range avg {
		avg[h], ok[h] = 1.7, h >= 4
	}
	avg[13], avg[14], avg[20] = 1.9, 1.9, 1.9
	cheap := hourRanges(avg, ok, func(p float64) bool { return p == 1.7 })
	dear := hourRanges(avg, ok, func(p float64) bool { return p == 1.9 })
	if cheap != "04:00–13:00, 15:00–20:00, 21:00–24:00" || dear != "13:00–15:00, 20:00" {
		t.Fatalf("cheap %q dear %q", cheap, dear)
	}
	l := stationHours(4, watch{Name: "JET"}, []pricePoint{{1.7, time.Now().Add(-90 * time.Minute)}}, time.Now())
	if len(l) != 3 || l[0] != "4 · JET" || !strings.HasPrefix(l[1], "⬇ ") || !strings.HasSuffix(l[2], "/24 h seen") {
		t.Fatalf("got %q", l)
	}
}

func TestTripDays(t *testing.T) {
	now := time.Date(2026, 11, 22, 15, 0, 0, 0, time.Local)
	d := func(m, day int) time.Time { return time.Date(2026, time.Month(m), day, 0, 0, 0, 0, time.Local) }
	for in, want := range map[string][2]time.Time{"": {d(11, 22), d(11, 22)}, "yesterday": {d(11, 21), d(11, 21)},
		"10": {d(11, 13), d(11, 22)}, "2026-11-20": {d(11, 20), d(11, 20)}, "20.11.": {d(11, 20), d(11, 20)}, "3.1.2026": {d(1, 3), d(1, 3)}} {
		f, to, _, ok := tripDays(in, now)
		if !ok || !f.Equal(want[0]) || !to.Equal(want[1]) {
			t.Errorf("%q: %v %v %v", in, f, to, ok)
		}
	}
	if _, _, _, ok := tripDays("golf", now); ok {
		t.Error("golf is not a range")
	}
}

func TestGraphPNG(t *testing.T) {
	g := graphData{title: "⛽ Fuel · 6 months", sub: "A180", cur: "€", fuel: true, foot: []string{"Σ 135.00€ · 3 fill-ups", "70 l · ⌀ 1.650€/l"}}
	for i := range 31 { // many bars: labels thinned out, no panic
		g.buckets = append(g.buckets, bucket{label: fmt.Sprintf("%02d Nov", i+1), amount: float64(i * 3), qty: float64(i)})
	}
	data, err := g.png()
	if err != nil {
		t.Fatal(err)
	}
	if c, err := png.DecodeConfig(bytes.NewReader(data)); err != nil || c.Width != 900 || c.Height != 640 {
		t.Fatalf("png %v %+v", err, c)
	}
	if os.Getenv("GRAPH_PNG") != "" { // GRAPH_PNG=/tmp/x.png go test -run PNG: look at it
		os.WriteFile(os.Getenv("GRAPH_PNG"), data, 0o644)
	}
}

func TestTripsPDFAndCSV(t *testing.T) {
	at := func(d, h, m int) time.Time { return time.Date(2026, 11, d, h, m, 0, 0, time.Local) }
	tr := func(d, h int, km float64, business bool, purpose string) trip {
		x := trip{Start: at(d, h, 5), End: at(d, h+1, 40), StartOdo: 81200, EndOdo: 81200 + km, Distance: km, DriveMinutes: 80,
			FuelUsed: km * 0.085, FuelCost: km * 0.085 * 1.79, Business: business, Purpose: purpose, Legs: []tripLeg{{}, {}}}
		return x
	}
	t1 := tr(20, 8, 42.4, true, "Kunde Müller, Frankfurt (Vertrag)")
	t1.Costs = append(t1.Costs, struct {
		ExpenseType, Comments string
		Amount                float64
	}{"Toll", "A3 €", 12.5})
	t1.ExtraCost = 12.5
	var trips []trip
	for i := 0; i < 50; i++ { // more than a page
		trips = append(trips, tr(1+i%20, 14, 10+float64(i), i%2 == 0, "Baumarkt"))
	}
	r := tripReport{label: "last 30 days", cur: "€", curCode: "EUR", km: "km", from: at(1, 0, 0), to: at(22, 0, 0), format: "pdf",
		cars: []carTrips{{name: "A-Klasse", trips: append([]trip{t1}, trips...)}}}
	data := r.pdf()
	if !bytes.HasPrefix(data, []byte("%PDF-1.4")) || !bytes.HasSuffix(data, []byte("%%EOF\n")) || !bytes.Contains(data, []byte("/Count 2")) ||
		!bytes.Contains(data, []byte("(Kunde M\xfcller, Frankfurt \\(Vertrag\\)")) || !bytes.Contains(data, []byte("Toll 12.50)")) || !bytes.Contains(data, []byte("(A3 \x80)")) { // the toll line wraps onto 2 lines
		t.Errorf("pdf: %d bytes, 2 pages, Windows-1252 text with escaped brackets expected", len(data))
	}
	// the xref offsets point at the objects
	xref := bytes.Index(data, []byte("\nxref\n")) + 1 // not the one in "startxref"
	for i, line := range strings.Split(string(data[xref:]), "\n")[3:7] {
		off, _ := strconv.Atoi(line[:10])
		if !bytes.HasPrefix(data[off:], []byte(fmt.Sprintf("%d 0 obj", i+1))) {
			t.Errorf("xref %d → %q", i+1, data[off:off+10])
		}
	}
	if os.Getenv("TRIPS_PDF") != "" {
		os.WriteFile(os.Getenv("TRIPS_PDF"), data, 0o644)
	}
	c := string(r.csv())
	if !strings.Contains(c, "A-Klasse,2026-11-20,08:05,09:40,80,1,81200,81242,42.4,yes,\"Kunde Müller, Frankfurt (Vertrag)\",,3.60,6.45,12.50,Toll 12.50 A3 €,18.95") {
		t.Errorf("csv:\n%s", c[:300])
	}
	if r.filename() != "trips-2026-11-01-2026-11-22.pdf" {
		t.Error(r.filename())
	}
}

func TestWrap(t *testing.T) {
	if w := wrap("Kunde Müller, Frankfurt (Vertrag) · Toll 12.50 A3 €", 20); strings.Join(w, "|") != "Kunde Müller,|Frankfurt (Vertrag)|· Toll 12.50 A3 €" {
		t.Errorf("%q", w)
	}
	if w := wrap("", 10); len(w) != 1 || w[0] != "" {
		t.Errorf("empty: %q", w)
	}
	if w := wrap("abcdefghijkl x", 5); strings.Join(w, "|") != "abcde|fghij|kl x" {
		t.Errorf("long word: %q", w)
	}
}
