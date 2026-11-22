package chat

// fuel commands: stats, prices, cheap-time. Read-only through hammond's own /api/v1 (in-process, see chat.go).

import (
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

var gradeNames = map[string]string{"e5": "Super E5", "e10": "Super E10", "diesel": "Diesel", "plus": "98/100",
	"diesel_premium": "Premium diesel", "lpg": "LPG"}

func gradeName(g string) string { return first(gradeNames[g], g) }

type watch struct {
	ID, Name, Address, Grade, Kind string
	Lat, Lng                       float64
	Below, LastPrice               *float64
	LastError                      string
}

func (b *bot) fuelStats(filter string) (string, error) {
	h := b.hammond
	var me struct{ Currency, DistanceUnitShort string }
	var vs []struct{ ID, Nickname, FuelUnit string }
	if err := h.get("/me", &me); err != nil {
		return "", err
	}
	if err := h.get("/vehicles", &vs); err != nil {
		return "", err
	}
	now := time.Now()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	since := url.QueryEscape(now.AddDate(0, 0, -90).UTC().Format(time.RFC3339))
	var out []string
	for _, v := range vs {
		if filter != "" && !strings.Contains(strings.ToLower(v.Nickname), filter) {
			continue
		}
		var fills []struct {
			Date                                    time.Time
			FuelQuantity, PerUnitPrice, TotalAmount float64
			FillingStation                          string
		}
		var exps []struct {
			Date   time.Time
			Amount float64
		}
		var mil []struct{ FuelQuantity, Mileage float64 }
		for path, dst := range map[string]any{"/fillups": &fills, "/expenses": &exps, "/mileage?since=" + since: &mil} {
			if err := h.get("/vehicles/"+v.ID+path, dst); err != nil {
				return "", err
			}
		}
		// short lines (~28 characters): a phone's chat bubble wraps at ~30
		unit := v.FuelUnit
		if unit == "Lt" {
			unit = "l"
		}
		out = append(out, "**🚗 "+v.Nickname)
		sort.Slice(fills, func(i, j int) bool { return fills[i].Date.After(fills[j].Date) })
		if len(fills) > 0 {
			f := fills[0]
			out = append(out, fmt.Sprintf("⛽ %s · %.2f %s", f.Date.In(time.Local).Format("Mon 2 Jan"), f.TotalAmount, me.Currency),
				fmt.Sprintf("%.1f %s × %.3f", f.FuelQuantity, unit, f.PerUnitPrice))
			if f.FillingStation != "" {
				out = append(out, "📍 "+f.FillingStation)
			}
		}
		var fuel, other float64
		n := 0
		for _, f := range fills {
			if !f.Date.Before(month) {
				fuel += f.TotalAmount
				n++
			}
		}
		for _, e := range exps {
			if !e.Date.Before(month) {
				other += e.Amount
			}
		}
		out = append(out, fmt.Sprintf("📅 %s: %.2f %s", now.Format("January"), fuel+other, me.Currency))
		l := plural(n, "fill-up")
		if other > 0 {
			l += fmt.Sprintf(" · other %.2f", other)
		}
		out = append(out, l)
		// mileage = distance per unit for full-tank fill-ups (0 otherwise): distance = mileage × quantity
		var dist, qty float64
		for _, m := range mil {
			if m.Mileage > 0 {
				dist, qty = dist+m.Mileage*m.FuelQuantity, qty+m.FuelQuantity
			}
		}
		switch {
		case dist == 0:
		case me.DistanceUnitShort == "Km" && v.FuelUnit == "Lt":
			out = append(out, fmt.Sprintf("📉 %.1f l/100 km (90 days)", 100*qty/dist))
		default:
			out = append(out, fmt.Sprintf("📉 %.1f %s/%s (90 days)", dist/qty, me.DistanceUnitShort, unit))
		}
	}
	if len(out) == 0 {
		return "no vehicle matches " + filter, nil
	}
	return strings.Join(out, "\n"), nil
}

func (b *bot) fuelPrices() (string, error) {
	var ws []watch
	if err := b.hammond.get("/me/priceWatches", &ws); err != nil {
		return "", err
	}
	if len(ws) == 0 {
		return "no watched stations — add some in hammond → Price watch", nil
	}
	sort.SliceStable(ws, func(i, j int) bool {
		if ws[i].Grade != ws[j].Grade {
			return ws[i].Grade < ws[j].Grade
		}
		if ci, cj := watchCountry(ws[i]), watchCountry(ws[j]); ci != cj {
			return ci < cj
		}
		return nowPrice(ws[i]) < nowPrice(ws[j])
	})
	var me struct{ Currency string }
	if err := b.hammond.get("/me", &me); err != nil {
		return "", err
	}
	cur := money(me.Currency)
	var out []string
	for i, w := range ws {
		if i == 0 || ws[i-1].Grade != w.Grade {
			out = append(out, "**⛽ "+gradeName(w.Grade))
		}
		// DE 1.759€ - JET - 🔔: 1.800€ — the station name links to the map (tap → Google Maps, Navigate from there)
		name := w.Name
		if w.Lat != 0 || w.Lng != 0 {
			name = "[" + w.Name + "](" + mapsSearch(fmt.Sprintf("%.6f,%.6f", w.Lat, w.Lng)) + ")"
		}
		l := "—"
		if w.LastPrice != nil {
			l = fmt.Sprintf("%.3f%s", *w.LastPrice, cur)
		}
		if c := watchCountry(w); c != "" {
			l = c + " " + l
		}
		l += " - " + name
		if w.Below != nil {
			l += fmt.Sprintf(" - 🔔: %.3f%s", *w.Below, cur)
		}
		if w.LastError != "" {
			l += " ⚠️ " + w.LastError
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n"), nil
}

// country of a watch from its price source (hammond's kindCountry); "" for custom sources
var kindCountry = map[string]string{"tankerkoenig": "DE", "econtrol": "AT", "goriva": "SI", "mzoe": "HR",
	"france": "FR", "portugal": "PT", "italy": "IT", "spain": "ES"}

func watchCountry(w watch) string { return kindCountry[w.Kind] }

// money: € instead of EUR, other currencies as " CHF"
func money(cur string) string {
	if cur == "EUR" || cur == "" {
		return "€"
	}
	return " " + cur
}

type pricePoint struct {
	Price float64
	At    time.Time
}

// averageByHour: average price per local hour of day, weighted by how long each price held (a price holds
// until the next change, the last one until end). Same as hammond's ui/src/utils/price-hours.js.
func averageByHour(pts []pricePoint, end time.Time) (avg [24]float64, ok [24]bool) {
	var sum, dur [24]float64
	for i, p := range pts {
		until := end
		if i+1 < len(pts) {
			until = pts[i+1].At
		}
		for t := p.At; t.Before(until); {
			lt := t.In(time.Local)
			next := time.Date(lt.Year(), lt.Month(), lt.Day(), lt.Hour()+1, 0, 0, 0, time.Local)
			if next.After(until) {
				next = until
			}
			step := next.Sub(t).Seconds()
			sum[lt.Hour()] += p.Price * step
			dur[lt.Hour()] += step
			t = next
		}
	}
	for h := range avg {
		if dur[h] > 0 {
			avg[h], ok[h] = sum[h]/dur[h], true
		}
	}
	return
}

// cheapTime: cheapest hours of the day per watched station; args: a grade (e10) or a watch number, and days
func (b *bot) cheapTime(args []string) (string, error) {
	ws, err := b.watches()
	if err != nil {
		return "", err
	}
	if len(ws) == 0 {
		return "no watched stations — add some in hammond → Price watch", nil
	}
	grade, num, days := "", 0, 30
	for _, a := range args {
		if n, err := strconv.Atoi(strings.TrimSuffix(a, "d")); err == nil {
			if num == 0 && !strings.HasSuffix(a, "d") && n <= len(ws) && len(args) > 1 && a == args[0] {
				num = n // "cheap-time 3 7": watch 3, 7 days
			} else if n >= 1 && n <= 3660 {
				days = n
			}
		} else {
			grade = a
		}
	}
	if num == 0 && len(args) == 1 {
		if n, err := strconv.Atoi(args[0]); err == nil && n <= len(ws) && n >= 1 {
			num, days = n, 30 // "cheap-time 3": watch 3
		}
	}
	byGrade := map[string][]int{} // grade → watch numbers (0-based)
	for i, w := range ws {
		if (num == 0 && (grade == "" || w.Grade == grade)) || num == i+1 {
			byGrade[w.Grade] = append(byGrade[w.Grade], i)
		}
	}
	if len(byGrade) == 0 {
		return "no watched station has " + gradeName(grade) + " — grades: e5, e10, diesel, plus, diesel_premium, lpg", nil
	}
	now := time.Now()
	gs := make([]string, 0, len(byGrade))
	for g := range byGrade {
		gs = append(gs, g)
	}
	sort.Strings(gs)
	var out []string
	for _, g := range gs {
		list := byGrade[g]
		sort.SliceStable(list, func(i, j int) bool { return nowPrice(ws[list[i]]) < nowPrice(ws[list[j]]) })
		out = append(out, fmt.Sprintf("**⏰ %s · %s", gradeName(g), plural(days, "day")))
		for _, i := range list {
			var hist struct{ Prices []pricePoint }
			if err := b.hammond.get(fmt.Sprintf("/me/priceWatches/%s/history?days=%d", ws[i].ID, days), &hist); err != nil {
				return "", err
			}
			out = append(out, stationHours(i+1, ws[i], hist.Prices, now)...)
		}
	}
	return strings.Join(out, "\n"), nil
}

func nowPrice(w watch) float64 {
	if w.LastPrice == nil {
		return math.Inf(1)
	}
	return *w.LastPrice
}

// stationHours: short lines for a phone: "3 · JET · now 1.729€", "⬇ 20:00–24:00 · 1.719€", "⬆ 13:00 · 1.832€"
func stationHours(n int, w watch, pts []pricePoint, now time.Time) []string {
	l := fmt.Sprintf("%d · %s", n, w.Name)
	if w.LastPrice != nil {
		l += fmt.Sprintf(" · now %.3f€", *w.LastPrice)
	}
	avg, ok := averageByHour(pts, now)
	round := func(p float64) float64 { return math.Round(p*1000) / 1000 } // prices have 3 decimals
	seen, lo, hi := 0, math.Inf(1), math.Inf(-1)
	for h := range avg {
		if ok[h] {
			seen++
			lo, hi = min(lo, round(avg[h])), max(hi, round(avg[h]))
		}
	}
	if seen == 0 {
		return []string{l, "no price history yet"}
	}
	out := []string{l, "⬇ " + hourRanges(avg, ok, func(p float64) bool { return round(p) == lo }) + fmt.Sprintf(" · %.3f€", lo)}
	if hi > lo {
		out = append(out, "⬆ "+hourRanges(avg, ok, func(p float64) bool { return round(p) == hi })+fmt.Sprintf(" · %.3f€", hi))
	}
	if seen < 24 {
		out = append(out, fmt.Sprintf("%d/24 h seen", seen))
	}
	return out
}

// hourRanges joins consecutive matching hours: [0..11] → "00:00–12:00", a single hour → "13:00"
func hourRanges(avg [24]float64, ok [24]bool, match func(float64) bool) string {
	var parts []string
	for h := 0; h < 24; h++ {
		if !ok[h] || !match(avg[h]) {
			continue
		}
		end := h
		for end+1 < 24 && ok[end+1] && match(avg[end+1]) {
			end++
		}
		if end == h {
			parts = append(parts, fmt.Sprintf("%02d:00", h))
		} else {
			parts = append(parts, fmt.Sprintf("%02d:00–%02d:00", h, end+1))
		}
		h = end
	}
	return strings.Join(parts, ", ")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
