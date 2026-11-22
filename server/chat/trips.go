package chat

// trips [today|yesterday|10|2026-11-20|20.11.] [car]: the trip log from hammond, per day with start/stop,
// odometer, driving time, fuel and costs, plus that day's fill-ups.

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// tripDays: first and last day (local) of "today", "yesterday", "10" (last 10 days, today included),
// "2026-11-20", "20.11." / "20.11.2026"; default today
func tripDays(s string, now time.Time) (from, to time.Time, label string, ok bool) {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	switch s {
	case "", "today":
		return today, today, "today", true
	case "yesterday":
		y := today.AddDate(0, 0, -1)
		return y, y, "yesterday", true
	}
	if n, err := strconv.Atoi(strings.TrimSuffix(s, "d")); err == nil && n >= 1 && n <= 3660 {
		return today.AddDate(0, 0, 1-n), today, "last " + plural(n, "day"), true
	}
	for _, f := range []string{"2006-01-02", "2.1.2006", "2.1.", "2.1"} {
		if t, err := time.ParseInLocation(f, s, time.Local); err == nil {
			if t.Year() == 0 {
				t = t.AddDate(now.Year(), 0, 0)
			}
			return t, t, t.Format("Mon 2 Jan 2006"), true
		}
	}
	return time.Time{}, time.Time{}, "", false
}

type tripLeg struct {
	Start, End time.Time
	Distance   float64
	FuelUsed   *float64
}

type trip struct {
	Start, End                 time.Time
	StartOdo, EndOdo, Distance float64
	DriveMinutes               int
	FuelUsed, FuelCost         float64
	ExtraCost                  float64
	Business                   bool
	Purpose, Notes             string
	Legs                       []tripLeg
	Costs                      []struct {
		ExpenseType, Comments string
		Amount                float64
	}
}

func hm(t time.Time) string { return t.In(time.Local).Format("15:04") }

func mins(m int) string {
	if m >= 60 {
		return fmt.Sprintf("%d h %02d min", m/60, m%60)
	}
	return fmt.Sprintf("%d min", m)
}

type fillup struct {
	Date                                    time.Time
	FuelQuantity, PerUnitPrice, TotalAmount float64
	FillingStation                          string
}

type carTrips struct {
	name  string
	trips []trip // oldest first
	fills []fillup
}

// tripReport: what trips shows, also the source of the CSV and PDF
type tripReport struct {
	label, cur, km, curCode string
	from, to                time.Time
	cars                    []carTrips
	format                  string // "", "csv" or "pdf"
}

// tripsData: args = [range] [csv|pdf] [car]; msg = answer instead of a report (no vehicle matches)
func (b *bot) tripsData(args []string) (r tripReport, msg string, err error) {
	rng, car := "", ""
	for _, a := range args {
		switch _, _, _, ok := tripDays(a, time.Now()); {
		case a == "csv" || a == "pdf":
			r.format = a
		case ok && rng == "" && a != "":
			rng = a
		default:
			car = strings.TrimSpace(car + " " + a)
		}
	}
	r.from, r.to, r.label, _ = tripDays(rng, time.Now())
	h := b.hammond
	var me struct{ Currency, DistanceUnitShort string }
	var vs []struct{ ID, Nickname string }
	if err := h.get("/me", &me); err != nil {
		return r, "", err
	}
	if err := h.get("/vehicles", &vs); err != nil {
		return r, "", err
	}
	r.cur, r.curCode, r.km = money(me.Currency), first(me.Currency, "EUR"), strings.ToLower(first(me.DistanceUnitShort, "km"))
	q := "?from=" + url.QueryEscape(r.from.Format("2006-01-02")) + "&to=" + url.QueryEscape(r.to.Format("2006-01-02"))
	for _, v := range vs {
		if car != "" && !strings.Contains(strings.ToLower(v.Nickname), car) {
			continue
		}
		c := carTrips{name: v.Nickname}
		if err := h.get("/vehicles/"+v.ID+"/trips"+q, &c.trips); err != nil {
			return r, "", err
		}
		slices.Reverse(c.trips) // hammond: newest first
		if err := h.get("/vehicles/"+v.ID+"/fillups", &c.fills); err != nil {
			return r, "", err
		}
		r.cars = append(r.cars, c)
	}
	if len(r.cars) == 0 {
		return r, "no vehicle matches " + car, nil
	}
	return r, "", nil
}

func (b *bot) trips(args []string) (string, error) {
	r, msg, err := b.tripsData(args)
	if msg != "" || err != nil {
		return msg, err
	}
	return r.text(), nil
}

func (r tripReport) text() string {
	cur, km := r.cur, r.km
	var out []string
	for _, c := range r.cars {
		ts := c.trips
		if len(ts) == 0 {
			out = append(out, "**🚗 "+c.name+" · "+r.label, "no trips")
			continue
		}
		var dist, cost, fuel float64
		var drive int
		day := ""
		for _, t := range ts { // oldest first, a heading per day
			if d := t.Start.In(time.Local).Format("Mon 2 Jan"); d != day {
				day = d
				out = append(out, "**🚗 "+c.name+" · "+d)
				for _, f := range c.fills { // that day's fill-ups
					if f.Date.In(time.Local).Format("Mon 2 Jan 2006") == t.Start.In(time.Local).Format("Mon 2 Jan 2006") {
						out = append(out, fmt.Sprintf("⛽ %s %.1f l · %.2f%s", hm(f.Date), f.FuelQuantity, f.TotalAmount, cur))
					}
				}
			}
			l := fmt.Sprintf("🕐 %s–%s · %.1f %s", hm(t.Start), hm(t.End), t.Distance, km)
			if t.Business {
				l = "💼 " + strings.TrimPrefix(l, "🕐 ")
			}
			out = append(out, l, fmt.Sprintf("%.0f → %.0f %s", t.StartOdo, t.EndOdo, km), mins(t.DriveMinutes)+" driving")
			if len(t.Legs) > 1 {
				out[len(out)-1] += " · " + plural(len(t.Legs)-1, "stop")
			}
			if t.FuelUsed > 0 {
				out = append(out, fmt.Sprintf("%.2f l · %.2f%s", t.FuelUsed, t.FuelCost, cur))
			}
			for _, c := range t.Costs {
				out = append(out, fmt.Sprintf("💶 %s %.2f%s", c.ExpenseType, c.Amount, cur))
			}
			if t.Purpose != "" {
				out = append(out, "📝 "+t.Purpose)
			}
			dist, cost, fuel, drive = dist+t.Distance, cost+t.FuelCost+t.ExtraCost, fuel+t.FuelUsed, drive+t.DriveMinutes
		}
		out = append(out, fmt.Sprintf("**Σ %.1f %s · %s", dist, km, mins(drive)), fmt.Sprintf("%.2f l · %.2f%s", fuel, cost, cur))
	}
	return strings.Join(out, "\n")
}
