package chat

// graph fuel|costs|other [range] [car]: a bar chart as a PNG in the chat (plus the totals as text);
// the text bars are the fallback when the image can't be made or uploaded.

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// parseRange: "14" or "14d" days, "8w" weeks, "6m" months, "2y" years, "all" (zero time) — default 30 days
func parseRange(s string, now time.Time) (since time.Time, label string, ok bool) {
	if s == "all" {
		return time.Time{}, "all time", true
	}
	unit := "d"
	if s != "" && strings.ContainsAny(s[len(s)-1:], "dwmy") {
		s, unit = s[:len(s)-1], s[len(s)-1:]
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 100*365 {
		return time.Time{}, "", false
	}
	switch unit {
	case "w":
		return now.AddDate(0, 0, -7*n), plural(n, "week"), true
	case "m":
		return now.AddDate(0, -n, 0), plural(n, "month"), true
	case "y":
		return now.AddDate(-n, 0, 0), plural(n, "year"), true
	}
	return now.AddDate(0, 0, -n), plural(n, "day"), true
}

type entry struct {
	at          time.Time
	amount, qty float64 // qty: litres, only for fill-ups
}

type bucket struct {
	label       string
	amount, qty float64
	parts       []entry // the fill-ups / costs in it, oldest first: the stacked pieces of the bar
}

type graphData struct {
	title, sub string // "⛽ Fuel · 14 days", car names
	cur        string
	fuel       bool
	buckets    []bucket
	foot       []string // Σ lines
}

// group: one bucket per day with entries (≤31 days), or every month (≤2 years) / year of the range
func group(es []entry, since, now time.Time) (bs []bucket, total, litres float64, n int) {
	if since.IsZero() {
		since = now
		for _, e := range es {
			if e.at.Before(since) {
				since = e.at
			}
		}
	}
	span := now.Sub(since).Hours() / 24
	key, label := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local) }, "02 Jan"
	next := func(t time.Time) time.Time { return t.AddDate(0, 0, 1) }
	switch {
	case span > 731:
		key, label = func(t time.Time) time.Time { return time.Date(t.Year(), 1, 1, 0, 0, 0, 0, time.Local) }, "2006"
		next = func(t time.Time) time.Time { return t.AddDate(1, 0, 0) }
	case span > 31:
		key, label = func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.Local) }, "Jan 06"
		next = func(t time.Time) time.Time { return t.AddDate(0, 1, 0) }
	}
	sum, qty, parts := map[time.Time]float64{}, map[time.Time]float64{}, map[time.Time][]entry{}
	es = slices.Clone(es)
	slices.SortFunc(es, func(a, b entry) int { return a.at.Compare(b.at) })
	for _, e := range es {
		if e.at.Before(since) || e.at.After(now) {
			continue
		}
		k := key(e.at.In(time.Local))
		sum[k] += e.amount
		qty[k] += e.qty
		parts[k] = append(parts[k], e)
		total, litres, n = total+e.amount, litres+e.qty, n+1
	}
	for k := key(since.In(time.Local)); !k.After(now); k = next(k) {
		if _, ok := sum[k]; ok || label != "02 Jan" { // days: only the ones with entries
			bs = append(bs, bucket{k.Format(label), sum[k], qty[k], parts[k]})
		}
	}
	return
}

// bar: width cells for v/max, with eighth blocks for the last cell
func bar(v, max float64, width int) string {
	if max <= 0 || v <= 0 {
		return ""
	}
	e := int(v/max*float64(width*8) + 0.5)
	if e == 0 {
		e = 1
	}
	return strings.Repeat("█", e/8) + []string{"", "▏", "▎", "▍", "▌", "▋", "▊", "▉"}[e%8]
}

// text: the chart as lines; label + amount in code (monospace) so the bars start in one column
func (g graphData) text() string {
	out := []string{"**📊 " + g.title, "🚗 " + g.sub}
	var max float64
	for _, b := range g.buckets {
		max = maxf(max, b.amount)
	}
	for _, b := range g.buckets {
		l := "`" + strings.ReplaceAll(fmt.Sprintf("%s %7.2f", b.label, b.amount), " ", " ") + "` " + bar(b.amount, max, 8)
		if g.fuel && b.qty > 0 {
			l += fmt.Sprintf(" %.0f l", b.qty)
		}
		out = append(out, l)
	}
	return strings.Join(append(out, g.foot...), "\n")
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

var graphKinds = map[string]string{"fuel": "fuel", "costs": "costs", "cost": "costs", "other": "other"}

// graphOf: the chart data, or msg (usage, no match, nothing in range) when there's nothing to draw
func (b *bot) graphOf(args []string) (g graphData, msg string, err error) {
	kind, rng, car := "", "", ""
	for _, a := range args {
		switch {
		case kind == "" && graphKinds[a] != "":
			kind = graphKinds[a]
		case rng == "" && (a == "all" || strings.ContainsAny(a[:1], "0123456789")):
			rng = a
		default:
			car = strings.TrimSpace(car + " " + a)
		}
	}
	if kind == "" {
		return g, "usage: graph fuel|costs|other [14 | 8w | 6m | 2y | all] [car]", nil
	}
	if rng == "" {
		rng = "30"
	}
	now := time.Now()
	since, label, ok := parseRange(rng, now)
	if !ok {
		return g, "range: 14 (days), 8w, 6m, 2y or all", nil
	}
	h := b.hammond
	var me struct{ Currency string }
	var vs []struct{ ID, Nickname string }
	if err := h.get("/me", &me); err != nil {
		return g, "", err
	}
	if err := h.get("/vehicles", &vs); err != nil {
		return g, "", err
	}
	var es []entry
	var names []string
	for _, v := range vs {
		if car != "" && !strings.Contains(strings.ToLower(v.Nickname), car) {
			continue
		}
		names = append(names, v.Nickname)
		if kind != "other" {
			var fills []struct {
				Date                      time.Time
				FuelQuantity, TotalAmount float64
			}
			if err := h.get("/vehicles/"+v.ID+"/fillups", &fills); err != nil {
				return g, "", err
			}
			for _, f := range fills {
				es = append(es, entry{f.Date, f.TotalAmount, f.FuelQuantity})
			}
		}
		if kind != "fuel" {
			var exps []struct {
				Date   time.Time
				Amount float64
			}
			if err := h.get("/vehicles/"+v.ID+"/expenses", &exps); err != nil {
				return g, "", err
			}
			for _, e := range exps {
				es = append(es, entry{e.Date, e.Amount, 0})
			}
		}
	}
	if len(names) == 0 {
		return g, "no vehicle matches " + car, nil
	}
	bs, total, litres, n := group(es, since, now)
	if n == 0 {
		return g, "nothing in this range", nil
	}
	g = graphData{title: map[string]string{"fuel": "⛽ Fuel", "costs": "💶 All costs", "other": "🔧 Other costs"}[kind] + " · " + label,
		sub: strings.Join(names, ", "), cur: money(me.Currency), fuel: kind == "fuel", buckets: bs}
	switch {
	case g.fuel:
		g.foot = []string{fmt.Sprintf("Σ %.2f%s · %s", total, g.cur, plural(n, "fill-up"))}
		if litres > 0 {
			g.foot = append(g.foot, fmt.Sprintf("%.2f l · ⌀ %.3f%s/l", litres, total/litres, g.cur))
		}
	case n == 1:
		g.foot = []string{fmt.Sprintf("Σ %.2f%s · 1 entry", total, g.cur)}
	default:
		g.foot = []string{fmt.Sprintf("Σ %.2f%s · %d entries", total, g.cur, n)}
	}
	return g, "", nil
}

// graph as text (tests, and the fallback)
func (b *bot) graph(args []string) (string, error) {
	g, msg, err := b.graphOf(args)
	if msg != "" || err != nil {
		return msg, err
	}
	return g.text(), nil
}
