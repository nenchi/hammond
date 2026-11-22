package chat

// watched: numbered list of the watched stations; price <n> [range]: one watch's price history as
// summary (+ a table per day up to 31 days, per month beyond)

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// numbered: the watches in a stable order (name, then grade): the numbers don't move when prices change
func numbered(ws []watch) []watch {
	sort.SliceStable(ws, func(i, j int) bool {
		a, b := strings.ToLower(ws[i].Name), strings.ToLower(ws[j].Name)
		if a != b {
			return a < b
		}
		if ws[i].Grade != ws[j].Grade {
			return ws[i].Grade < ws[j].Grade
		}
		return ws[i].ID < ws[j].ID
	})
	return ws
}

var postCode = regexp.MustCompile(`,\s*(?:[A-Z]{1,2}-)?\d{4,5}\s+`)

// shortAddress: "ODENWALDRING 84, 63069 OFFENBACH" → "Odenwaldring 84, Offenbach" (as hammond's ShortAddress)
func shortAddress(a string) string {
	a = postCode.ReplaceAllString(strings.TrimSpace(a), ", ")
	if strings.ToUpper(a) != a {
		return a
	}
	words := strings.Fields(strings.ToLower(a))
	for i, w := range words {
		r := []rune(w)
		for j, c := range r {
			if unicode.IsLetter(c) {
				r[j] = unicode.ToUpper(c)
				break
			}
		}
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}

func (b *bot) watches() ([]watch, error) {
	var ws []watch
	if err := b.hammond.get("/me/priceWatches", &ws); err != nil {
		return nil, err
	}
	return numbered(ws), nil
}

func (b *bot) watchedList() (string, error) {
	ws, err := b.watches()
	if err != nil {
		return "", err
	}
	if len(ws) == 0 {
		return "no watched stations — add some in hammond → Price watch", nil
	}
	out := []string{"**⛽ Watched stations"}
	for i, w := range ws {
		name := w.Name
		if w.Lat != 0 || w.Lng != 0 {
			name = "[" + w.Name + "](" + mapsSearch(fmt.Sprintf("%.6f,%.6f", w.Lat, w.Lng)) + ")"
		}
		l := fmt.Sprintf("%d · %s · %s", i+1, name, gradeName(w.Grade))
		if c := watchCountry(w); c != "" {
			l += " · " + c
		}
		out = append(out, l)
		if a := shortAddress(w.Address); a != "" {
			out = append(out, a)
		}
	}
	return strings.Join(append(out, "`price 1 30` — history of number 1"), "\n"), nil
}

type priceSpan struct {
	label      string
	sum, dur   float64 // price × seconds, seconds: the time-weighted average
	lo, hi     float64
	loAt, hiAt time.Time
}

func (s *priceSpan) add(p float64, from, to time.Time) {
	d := to.Sub(from).Seconds()
	if d <= 0 {
		return
	}
	if s.dur == 0 || p < s.lo {
		s.lo, s.loAt = p, from
	}
	if s.dur == 0 || p > s.hi {
		s.hi, s.hiAt = p, from
	}
	s.sum += p * d
	s.dur += d
}

func (s *priceSpan) avg() float64 { return s.sum / s.dur }

// priceSpans: the whole range, and one span per day or month; each price holds until the next change
// (the last one until end)
func priceSpans(pts []pricePoint, end time.Time, monthly bool) (all priceSpan, spans []*priceSpan) {
	key := func(t time.Time) (time.Time, string) {
		t = t.In(time.Local)
		if monthly {
			return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.Local), t.Format("Jan 06")
		}
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local), t.Format("Mon 02")
	}
	next := func(k time.Time) time.Time {
		if monthly {
			return k.AddDate(0, 1, 0)
		}
		return k.AddDate(0, 0, 1)
	}
	byKey := map[time.Time]*priceSpan{}
	for i, p := range pts {
		until := end
		if i+1 < len(pts) {
			until = pts[i+1].At
		}
		all.add(p.Price, p.At, until)
		for t := p.At; t.Before(until); {
			k, label := key(t)
			stop := next(k)
			if stop.After(until) {
				stop = until
			}
			s := byKey[k]
			if s == nil {
				s = &priceSpan{label: label}
				byKey[k] = s
				spans = append(spans, s)
			}
			s.add(p.Price, t, stop)
			t = stop
		}
	}
	return
}

// clip: only the range from since; a price set before it counts from since on
func clip(pts []pricePoint, since time.Time) []pricePoint {
	var out []pricePoint
	for i, p := range pts {
		if !p.At.Before(since) {
			if len(out) == 0 && i > 0 && p.At.After(since) {
				out = append(out, pricePoint{pts[i-1].Price, since})
			}
			out = append(out, p)
		}
	}
	if len(out) == 0 && len(pts) > 0 && !since.IsZero() {
		out = []pricePoint{{pts[len(pts)-1].Price, since}}
	}
	return out
}

func (b *bot) priceStats(args []string) (string, error) {
	if len(args) == 0 {
		return "usage: price <number> [7 | 30 | 3m | 1y | all] — numbers: watched", nil
	}
	n, err := strconv.Atoi(args[0])
	ws, werr := b.watches()
	if werr != nil {
		return "", werr
	}
	if err != nil || n < 1 || n > len(ws) {
		return fmt.Sprintf("no watch %s — numbers 1–%d, see watched", args[0], len(ws)), nil
	}
	w := ws[n-1]
	rng := "7"
	if len(args) > 1 {
		rng = args[1]
	}
	now := time.Now()
	since, label, ok := parseRange(rng, now)
	if !ok {
		return "range: 7 (days), 30, 3m, 1y or all", nil
	}
	days := 3660
	if !since.IsZero() {
		days = int(math.Ceil(now.Sub(since).Hours() / 24))
	}
	var hist struct{ Prices []pricePoint }
	if err := b.hammond.get(fmt.Sprintf("/me/priceWatches/%s/history?days=%d", w.ID, days), &hist); err != nil {
		return "", err
	}
	cur := "€"
	out := []string{fmt.Sprintf("**⛽ %d · %s · %s", n, w.Name, gradeName(w.Grade)), "📅 " + label}
	if w.LastPrice != nil {
		out = append(out, fmt.Sprintf("now %.3f%s", *w.LastPrice, cur))
	}
	pts := clip(hist.Prices, since)
	if len(pts) == 0 {
		return strings.Join(append(out, "no price history in this range"), "\n"), nil
	}
	span := now.Sub(pts[0].At).Hours() / 24
	all, spans := priceSpans(pts, now, span > 31)
	at := func(t time.Time) string { return t.In(time.Local).Format("Mon 2 Jan 15:04") }
	out = append(out, fmt.Sprintf("⌀ %.3f%s", all.avg(), cur),
		fmt.Sprintf("⬇ %.3f%s · %s", all.lo, cur, at(all.loAt)),
		fmt.Sprintf("⬆ %.3f%s · %s", all.hi, cur, at(all.hiAt)))
	avg, okh := averageByHour(pts, now)
	round := func(p float64) float64 { return math.Round(p*1000) / 1000 }
	lo, hi := math.Inf(1), math.Inf(-1)
	for h := range avg {
		if okh[h] {
			lo, hi = min(lo, round(avg[h])), max(hi, round(avg[h]))
		}
	}
	if hi > lo {
		out = append(out, "🕐 cheapest "+hourRanges(avg, okh, func(p float64) bool { return round(p) == lo }),
			"🕐 dearest "+hourRanges(avg, okh, func(p float64) bool { return round(p) == hi }))
	}
	if span > 7 {
		// monospace, NBSP padding: "Mon 17 1.782 1.709 1.859"
		row := func(cols ...string) string { return "`" + strings.ReplaceAll(strings.Join(cols, " "), " ", " ") + "`" }
		head := "day   "
		if span > 31 {
			head = "month "
		}
		out = append(out, row(head, "  ⌀  ", " min ", " max "))
		for _, s := range spans {
			out = append(out, row(fmt.Sprintf("%-6s", s.label), fmt.Sprintf("%.3f", s.avg()), fmt.Sprintf("%.3f", s.lo), fmt.Sprintf("%.3f", s.hi)))
		}
	}
	return strings.Join(out, "\n"), nil
}
