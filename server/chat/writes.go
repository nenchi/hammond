package chat

// Writing commands: fill, cost, purpose, business/private, undo, and a bare price answering a detected fill-up.
// They go through hammond's UI API in-process (call), so the web app's validation and access checks apply.

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// undo: the last thing a write command saved, per user (10 minutes)
type undoEntry struct {
	label, path string // path to DELETE
	at          time.Time
}

var (
	undoMu  sync.Mutex
	undoFor = map[string]undoEntry{}
)

func remember(userID, label, path string) {
	undoMu.Lock()
	undoFor[userID] = undoEntry{label, path, time.Now()}
	undoMu.Unlock()
}

func (b *bot) undo() (string, error) {
	undoMu.Lock()
	e, ok := undoFor[b.hammond.userID]
	delete(undoFor, b.hammond.userID)
	undoMu.Unlock()
	if !ok || time.Since(e.at) > 10*time.Minute {
		return "nothing to undo (only the last fill-up or cost, within 10 minutes)", nil
	}
	if err := b.hammond.call("DELETE", e.path, nil, nil); err != nil {
		return "", err
	}
	return "↩️ deleted: " + e.label, nil
}

// num: "1,759" or "1.759" or "1.759€"
func num(s string) (float64, bool) {
	s = strings.TrimRight(strings.ReplaceAll(s, ",", "."), "€$l")
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil && f > 0 && !math.IsInf(f, 0)
}

// fill <litres> <price per litre> [odometer] [full|partial] [car]: odometer from Home Assistant when not given,
// station and fuel type from the last fill-up
func (b *bot) fill(args []string) (string, error) {
	var nums []float64
	full, carArg := true, ""
	for _, a := range args {
		switch a {
		case "full", "voll":
			full = true
		case "partial", "part", "teil":
			full = false
		default:
			if f, ok := num(a); ok {
				nums = append(nums, f)
			} else {
				carArg = strings.TrimSpace(carArg + " " + a)
			}
		}
	}
	if len(nums) < 2 || len(nums) > 3 {
		return "usage: fill <litres> <price per litre> [odometer] [partial] [car]\ne.g. fill 40.5 1.759", nil
	}
	litres, price := nums[0], nums[1]
	if price > 10 && litres < 10 { // "fill 1.759 40.5": swapped
		litres, price = price, litres
	}
	v, msg, err := b.writeCar(carArg)
	if msg != "" || err != nil {
		return msg, err
	}
	var veh struct {
		FuelUnit int    `json:"fuelUnit"`
		ID       string `json:"id"`
	}
	if err := b.hammond.call("GET", "/api/vehicles/"+v.ID, nil, &veh); err != nil {
		return "", err
	}
	odo, odoNote := 0.0, ""
	if len(nums) == 3 {
		odo = nums[2]
	} else {
		var o struct {
			Configured bool
			Value      float64
		}
		if err := b.hammond.call("GET", "/api/vehicles/"+v.ID+"/odometer", nil, &o); err != nil || !o.Configured || o.Value == 0 {
			return "I need the odometer: fill " + fmt.Sprint(litres) + " " + fmt.Sprint(price) + " <km>\n(or set the odometer entity of the car in hammond)", nil
		}
		odo, odoNote = o.Value, " (from Home Assistant)"
	}
	var last []struct {
		FillingStation, FuelSubType string
	}
	b.hammond.get("/vehicles/"+v.ID+"/fillups", &last) // newest first: its station and fuel type
	station, sub := "", ""
	if len(last) > 0 {
		station, sub = last[0].FillingStation, last[0].FuelSubType
	}
	total := math.Round(litres*price*100) / 100
	var created struct{ ID string }
	err = b.hammond.call("POST", "/api/vehicles/"+v.ID+"/fillups", map[string]any{
		"vehicleId": v.ID, "fuelUnit": veh.FuelUnit, "fuelQuantity": litres, "perUnitPrice": price, "totalAmount": total,
		"odoReading": int(math.Round(odo)), "isTankFull": full, "hasMissedFillup": false, "userId": b.hammond.userID,
		"date": time.Now().UTC().Format(time.RFC3339), "fillingStation": station, "fuelSubType": sub, "comments": "from the chat",
	}, &created)
	if err != nil {
		return "", err
	}
	remember(b.hammond.userID, fmt.Sprintf("fill-up %.2f l · %s", litres, v.Nickname), "/api/vehicles/"+v.ID+"/fillups/"+created.ID)
	kind := "full tank"
	if !full {
		kind = "partial"
	}
	out := []string{"**✓ Fill-up saved · " + v.Nickname, fmt.Sprintf("%.2f l × %.3f = %.2f", litres, price, total),
		fmt.Sprintf("%.0f km%s · %s", odo, odoNote, kind)}
	if station != "" {
		out = append(out, "📍 "+station+" (as last time)")
	}
	return strings.Join(append(out, "`undo` within 10 min"), "\n"), nil
}

var costTypes = map[string]string{"toll": "Toll", "maut": "Toll", "vignette": "Vignette", "vinjeta": "Vignette",
	"tunnel": "Tunnel", "parking": "Parking", "park": "Parking", "ferry": "Ferry", "wash": "Car wash",
	"service": "Service", "repair": "Repair", "insurance": "Insurance", "tax": "Tax", "other": "Other"}

// tripsOf: the user's trips of the last days across the given cars, newest first
type tripRef struct {
	ID, Purpose, Notes string
	Business           bool
	Start              time.Time
	Distance           float64
	car                car
}

func (b *bot) recentTrips(vs []car, days int) ([]tripRef, error) {
	now := time.Now()
	q := "?from=" + url.QueryEscape(now.AddDate(0, 0, -days).Format("2006-01-02")) + "&to=" + url.QueryEscape(now.Format("2006-01-02"))
	var all []tripRef
	for _, v := range vs {
		var ts []tripRef
		if err := b.hammond.get("/vehicles/"+v.ID+"/trips"+q, &ts); err != nil {
			return nil, err
		}
		for _, t := range ts {
			t.car = v
			all = append(all, t)
		}
	}
	for i := 1; i < len(all); i++ { // newest first
		for j := i; j > 0 && all[j].Start.After(all[j-1].Start); j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}
	return all, nil
}

// cost <type> <amount> [comment]: on today's latest trip when there is one, else an expense of the car
func (b *bot) cost(args []string) (string, error) {
	kind, amount, words := "", 0.0, []string{}
	for _, a := range args {
		if t, ok := costTypes[strings.ToLower(a)]; ok && kind == "" {
			kind = t
		} else if f, ok := num(a); ok && amount == 0 {
			amount = f
		} else {
			words = append(words, a)
		}
	}
	if amount == 0 {
		return "usage: cost <type> <amount> [comment]\ne.g. cost toll 12.50 A3 · types: toll, vignette, tunnel, parking, ferry, wash, service, other", nil
	}
	if kind == "" {
		kind = "Other"
	}
	comment := strings.Join(words, " ")
	vs, err := b.cars()
	if err != nil {
		return "", err
	}
	var trips []tripRef
	if b.tripsOn() { // Trips hidden in the settings: a cost is a plain expense
		if trips, err = b.recentTrips(vs, 1); err != nil {
			return "", err
		}
	}
	today := time.Now().Format("2006-01-02")
	if len(trips) > 0 && trips[0].Start.Local().Format("2006-01-02") == today {
		t := trips[0]
		// the new cost's own ID for undo: the trip may have other costs already
		var created struct{ ID string }
		if err := b.hammond.call("POST", "/api/trips/"+t.ID+"/costs", map[string]any{"amount": amount, "type": kind, "comment": comment}, &created); err != nil {
			return "", err
		}
		remember(b.hammond.userID, fmt.Sprintf("%s %.2f", kind, amount), "/api/vehicles/"+t.car.ID+"/expenses/"+created.ID)
		return fmt.Sprintf("**✓ %s %.2f on today's trip\n🚗 %s · %s %.1f km\n`undo` within 10 min", kind, amount, t.car.Nickname,
			t.Start.Local().Format("15:04"), t.Distance), nil
	}
	v, msg, err := b.writeCar("")
	if msg != "" || err != nil {
		return msg, err
	}
	var created struct{ ID string }
	err = b.hammond.call("POST", "/api/vehicles/"+v.ID+"/expenses", map[string]any{"vehicleId": v.ID, "amount": amount,
		"expenseType": kind, "comments": comment, "userId": b.hammond.userID, "date": time.Now().UTC().Format(time.RFC3339)}, &created)
	if err != nil {
		return "", err
	}
	remember(b.hammond.userID, fmt.Sprintf("%s %.2f", kind, amount), "/api/vehicles/"+v.ID+"/expenses/"+created.ID)
	return fmt.Sprintf("**✓ %s %.2f saved\n🚗 %s (no trip today)\n`undo` within 10 min", kind, amount, v.Nickname), nil
}

// purpose <text> / business / private: the latest trip (last 7 days, all cars)
// tripsOn: the user shows Trips (Settings → Show Trips); unknown = on
func (b *bot) tripsOn() bool {
	var me struct {
		ShowTrips *bool `json:"showTrips"`
	}
	if err := b.hammond.get("/me", &me); err != nil || me.ShowTrips == nil {
		return true
	}
	return *me.ShowTrips
}

const tripsOffText = "Trips are off in your settings (Settings → Show Trips)."

func (b *bot) tripFlag(cmd string, args []string) (string, error) {
	if !b.tripsOn() {
		return tripsOffText, nil
	}
	vs, err := b.cars()
	if err != nil {
		return "", err
	}
	trips, err := b.recentTrips(vs, 7)
	if err != nil {
		return "", err
	}
	if len(trips) == 0 {
		return "no trip in the last 7 days", nil
	}
	t := trips[0]
	switch cmd {
	case "purpose":
		if len(args) == 0 {
			return "usage: purpose <text>, e.g. purpose Customer Müller, Frankfurt", nil
		}
		t.Purpose = strings.Join(args, " ")
	case "business":
		t.Business = true
	case "private":
		t.Business = false
	}
	if err := b.hammond.call("PUT", "/api/trips/"+t.ID, map[string]any{"business": t.Business, "purpose": t.Purpose, "notes": t.Notes}, nil); err != nil {
		return "", err
	}
	kind := "private"
	if t.Business {
		kind = "business"
	}
	out := fmt.Sprintf("**✓ Trip %s · %s\n%s · %.1f km", t.Start.Local().Format("Mon 2 Jan 15:04"), t.car.Nickname, kind, t.Distance)
	if t.Purpose != "" {
		out += "\n📝 " + t.Purpose
	}
	return out, nil
}

var bareNumber = regexp.MustCompile(`^\d+([.,]\d+)?€?$`)

// detectionPrice: a bare price ("1.759") completes the newest pending fill-up detection (e.g. from Home
// Assistant) at that price per litre
func (b *bot) detectionPrice(text string) (string, bool, error) {
	if !bareNumber.MatchString(text) {
		return "", false, nil
	}
	price, ok := num(text)
	if !ok {
		return "", false, nil
	}
	var all []struct {
		ID, VehicleID, Station string
		FuelQuantity           float64
		OdoReading             int
		IsTankFull             bool
		Date                   time.Time
		DuplicateOf            any // a possible duplicate of an entry: decided in hammond, not here
	}
	if err := b.hammond.call("GET", "/api/detections", nil, &all); err != nil {
		return "", false, err
	}
	var d *struct {
		ID, VehicleID, Station string
		FuelQuantity           float64
		OdoReading             int
		IsTankFull             bool
		Date                   time.Time
		DuplicateOf            any
	}
	for i := range all {
		if all[i].DuplicateOf == nil && (d == nil || all[i].Date.After(d.Date)) {
			d = &all[i]
		}
	}
	if d == nil {
		return "", false, nil
	}
	var veh struct {
		FuelUnit int    `json:"fuelUnit"`
		Nickname string `json:"nickname"`
	}
	if err := b.hammond.call("GET", "/api/vehicles/"+d.VehicleID, nil, &veh); err != nil {
		return "", true, err
	}
	total := math.Round(d.FuelQuantity*price*100) / 100
	var created struct{ ID string }
	err := b.hammond.call("POST", "/api/vehicles/"+d.VehicleID+"/fillups", map[string]any{
		"vehicleId": d.VehicleID, "fuelUnit": veh.FuelUnit, "fuelQuantity": d.FuelQuantity, "perUnitPrice": price,
		"totalAmount": total, "odoReading": d.OdoReading, "isTankFull": d.IsTankFull, "hasMissedFillup": false,
		"userId": b.hammond.userID, "date": d.Date.UTC().Format(time.RFC3339), "fillingStation": d.Station,
		"detectionId": d.ID, "comments": "detected, price from the chat",
	}, &created)
	if err != nil {
		return "", true, err
	}
	remember(b.hammond.userID, fmt.Sprintf("fill-up %.2f l · %s", d.FuelQuantity, veh.Nickname), "/api/vehicles/"+d.VehicleID+"/fillups/"+created.ID)
	return fmt.Sprintf("**✓ Detected fill-up saved · %s\n%.2f l × %.3f = %.2f\n%d km\n`undo` within 10 min", veh.Nickname,
		d.FuelQuantity, price, total, d.OdoReading), true, nil
}
