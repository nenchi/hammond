package service

// Trip log: drives (legs) read from the car's sensors in the user's Home Assistant history, joined into trips
// when the stop between them is short, priced first-in-first-out from the fill-ups (a trip burns the oldest
// fuel in the tank at the price it was bought for, then the next fill-up's).

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/akhilrex/hammond/db"
	"gorm.io/gorm"
)

const (
	// the car reports odometer/fuel/trip computer shortly after the ignition goes off: wait this long
	legSettle = 15 * time.Minute
	// HA keeps ~10 days of history by default: the first sync reads that far back
	tripBackfill = 10 * 24 * time.Hour
)

// ignition states that mean driving: binary sensors "on", Mercedes (mbapi2020) 4 = on, 5 = engine running
func driving(state string) bool {
	return state == "on" || state == "4" || state == "5" || state == "true"
}

type haPoint struct {
	At    time.Time
	State string
}

// haHistory: state changes of the entities from start to end, per entity (the first is the state at start)
func haHistory(userID string, entities []string, start, end time.Time) (map[string][]haPoint, error) {
	haURL, token := haConfig(userID)
	if haURL == "" || token == "" {
		return nil, errors.New("Home Assistant is not set up (Settings → Home Assistant)")
	}
	u := fmt.Sprintf("%s/api/history/period/%s?end_time=%s&filter_entity_id=%s&minimal_response&no_attributes",
		haURL, url.QueryEscape(start.UTC().Format(time.RFC3339)), url.QueryEscape(end.UTC().Format(time.RFC3339)),
		url.QueryEscape(strings.Join(entities, ",")))
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req) // 10 days of history can take a while
	if err != nil {
		return nil, fmt.Errorf("Home Assistant not reachable: %s", strings.ReplaceAll(err.Error(), token, "***"))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Home Assistant answered HTTP %d for the trip history", resp.StatusCode)
	}
	var hist [][]struct {
		EntityID    string    `json:"entity_id"`
		State       string    `json:"state"`
		LastChanged time.Time `json:"last_changed"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&hist); err != nil {
		return nil, errors.New("Home Assistant answered without valid history")
	}
	out := map[string][]haPoint{}
	for _, list := range hist {
		id := ""
		for _, st := range list { // minimal_response: only the first item of each list has entity_id
			if st.EntityID != "" {
				id = st.EntityID
			}
			out[id] = append(out[id], haPoint{st.LastChanged, st.State})
		}
	}
	return out, nil
}

// valueAt: the last number of a series at or before t (ok false when there is none)
func valueAt(pts []haPoint, t time.Time) (float64, bool) {
	v, ok := 0.0, false
	for _, p := range pts {
		if p.At.After(t) {
			break
		}
		if f, err := strconv.ParseFloat(p.State, 64); err == nil {
			v, ok = f, true
		}
	}
	return v, ok
}

// reset: the counter went down between start and end (the trip computer started over)
func reset(pts []haPoint, start, end time.Time) bool {
	prev, ok := valueAt(pts, start)
	for _, p := range pts {
		if !p.At.After(start) || p.At.After(end) {
			continue
		}
		if f, err := strconv.ParseFloat(p.State, 64); err == nil {
			if ok && f < prev {
				return true
			}
			prev, ok = f, true
		}
	}
	return false
}

// sinceStart: how much a "since start" counter grew from start to end (scale: unit factor)
func sinceStart(pts []haPoint, start, end time.Time, scale float64) (float64, bool) {
	b, ok := valueAt(pts, end)
	if !ok {
		return 0, false
	}
	if a, ok := valueAt(pts, start); ok && !reset(pts, start, end) {
		b -= a
	}
	return b * scale, true
}

// fuelSinceStart: litres used from start to end from the average consumption and distance since start
func fuelSinceStart(cons, dist []haPoint, start, end time.Time) (float64, bool) {
	total := func(t time.Time) (float64, bool) {
		c, ok1 := valueAt(cons, t)
		d, ok2 := valueAt(dist, t)
		return c * d / 100, ok1 && ok2
	}
	b, ok := total(end)
	if !ok {
		return 0, false
	}
	if a, ok := total(start); ok && !reset(dist, start, end) {
		b -= a
	}
	return b, true
}

// legsFromHistory: one leg per ignition on → off seen completely (a drive already running at the start of
// the history is skipped: its start is unknown). End values are read up to legSettle after the stop (or the
// next start), since the car sends them late.
func legsFromHistory(h map[string][]haPoint, e db.TripEntities, odoEntity string, tank float64, now time.Time) []db.TripLeg {
	ign := h[e.Ignition]
	var legs []db.TripLeg
	for i := 1; i < len(ign); i++ {
		if !driving(ign[i].State) || driving(ign[i-1].State) || ign[i-1].State == "unavailable" || ign[i-1].State == "unknown" {
			continue
		}
		start := ign[i].At
		j := i + 1
		for j < len(ign) && driving(ign[j].State) {
			j++
		}
		if j == len(ign) {
			break // still driving
		}
		end := ign[j].At
		settle := end.Add(legSettle)
		if settle.After(now) {
			break // the car may still send the end values
		}
		for k := j + 1; k < len(ign); k++ {
			if driving(ign[k].State) && ign[k].At.Before(settle) {
				settle = ign[k].At.Add(-time.Second)
				break
			}
		}
		l := db.TripLeg{Start: start, End: end, Source: "ha"}
		l.StartOdo, _ = valueAt(h[odoEntity], start)
		l.EndOdo, _ = valueAt(h[odoEntity], settle)
		l.Distance = l.EndOdo - l.StartOdo
		// the trip computer counts "since start" across short stops (Mercedes resets it only after a few
		// hours parked): this drive = end − start value, or the end value when it was reset meanwhile
		dist, distOK := sinceStart(h[e.Distance], start, settle, 1)
		if e.Distance != "" && distOK && dist > 0 {
			l.Distance = dist
		}
		if e.FuelLevel != "" {
			if v, ok := valueAt(h[e.FuelLevel], start); ok {
				l.FuelStart = &v
			}
			if v, ok := valueAt(h[e.FuelLevel], settle); ok {
				l.FuelEnd = &v
			}
		}
		// fuel since start = average l/100 km × distance since start, per point in time
		if used, ok := fuelSinceStart(h[e.Consumption], h[e.Distance], start, settle); e.Consumption != "" && e.Distance != "" && ok && used > 0 {
			l.FuelUsed = &used
		} else if l.FuelStart != nil && l.FuelEnd != nil && *l.FuelStart > *l.FuelEnd && tank > 0 {
			used := (*l.FuelStart - *l.FuelEnd) * tank / 100
			l.FuelUsed = &used
		}
		if l.Distance < 0 {
			l.Distance = 0
		}
		legs = append(legs, l)
		i = j
	}
	return legs
}

// tripUser: whose Home Assistant reads the vehicle's trips: the owner, else any user of it with HA set up
func tripUser(vehicleID string) string {
	var uvs []db.UserVehicle
	db.DB.Where("vehicle_id = ?", vehicleID).Order("is_owner desc").Find(&uvs)
	for _, uv := range uvs {
		if u, t := haConfig(uv.UserID); u != "" && t != "" {
			return uv.UserID
		}
	}
	return ""
}

// SyncTrips: new drives from Home Assistant since the last stored one; returns how many were added
func SyncTrips(vehicleID string) (int, error) {
	v, err := GetVehicleById(vehicleID)
	if err != nil {
		return 0, err
	}
	if v.TripEntities.Ignition == "" || v.OdometerEntity == "" {
		return 0, errors.New("set the ignition and odometer entities of the vehicle first")
	}
	userID := tripUser(vehicleID)
	if userID == "" {
		return 0, errors.New("no user of this vehicle has Home Assistant set up")
	}
	now := time.Now()
	from := now.Add(-tripBackfill)
	var last db.TripLeg
	if db.DB.Where("vehicle_id = ?", vehicleID).Order("ended_at desc").Limit(1).Find(&last); last.ID != "" && last.End.After(from) {
		from = last.End
	}
	ents := []string{v.TripEntities.Ignition, v.OdometerEntity}
	for _, e := range []string{v.TripEntities.FuelLevel, v.TripEntities.Distance, v.TripEntities.Consumption} {
		if e != "" {
			ents = append(ents, e)
		}
	}
	h, err := haHistory(userID, ents, from, now)
	if err != nil {
		return 0, err
	}
	merge := 15
	if u, err := db.GetUserById(userID); err == nil {
		merge = u.TripMergeMinutes
	}
	n := 0
	for _, l := range legsFromHistory(h, v.TripEntities, v.OdometerEntity, float64(v.TankCapacity), now) {
		if !l.Start.After(from) {
			continue
		}
		l.VehicleID = vehicleID
		l.TripID = tripFor(vehicleID, l.Start, merge)
		if err := db.DB.Create(&l).Error; err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// tripFor: the trip of the drive before when it ended at most merge minutes before start, else a new trip
func tripFor(vehicleID string, start time.Time, merge int) string {
	var prev db.TripLeg
	db.DB.Where("vehicle_id = ? AND ended_at <= ?", vehicleID, start).Order("ended_at desc").Limit(1).Find(&prev)
	if prev.ID != "" && prev.TripID != "" && start.Sub(prev.End) <= time.Duration(merge)*time.Minute {
		return prev.TripID
	}
	t := db.Trip{VehicleID: vehicleID}
	db.DB.Create(&t)
	return t.ID
}

// TripTimer: every 10 minutes, new drives of every vehicle with an ignition entity
func TripTimer() {
	for range time.Tick(10 * time.Minute) {
		var vs []db.Vehicle
		db.DB.Find(&vs)
		for _, v := range vs {
			if v.TripEntities.Ignition != "" {
				if _, err := SyncTrips(v.ID); err != nil {
					fmt.Printf("trips %s: %v\n", v.Nickname, err)
				}
			}
		}
	}
}

// ---------- cost: first in, first out ----------

type fuelLot struct{ qty, price float64 }

// fifoCosts: fuel cost per leg ID. The tank at the first leg holds the newest fill-ups before it (as much
// as its level says, else the last fill-up); every fill-up adds a lot, a full-tank one sets the content to
// the tank size; a leg takes from the oldest lot. With nothing left, the last price is used.
func fifoCosts(fills []db.Fillup, legs []db.TripLeg, tank float64) map[string]float64 {
	out := map[string]float64{}
	if len(legs) == 0 || len(fills) == 0 {
		return out
	}
	sort.Slice(fills, func(i, j int) bool { return fills[i].Date.Before(fills[j].Date) })
	sort.Slice(legs, func(i, j int) bool { return legs[i].Start.Before(legs[j].Start) })
	first := legs[0].Start
	content := 0.0
	if legs[0].FuelStart != nil && tank > 0 {
		content = *legs[0].FuelStart * tank / 100
	}
	var lots []fuelLot
	fi := 0
	for fi < len(fills) && !fills[fi].Date.After(first) {
		fi++
	}
	if content == 0 && fi > 0 {
		content = float64(fills[fi-1].FuelQuantity)
	}
	for k := fi - 1; k >= 0 && content > 0; k-- { // newest fill-ups back until the tank content is covered
		q := minF(content, float64(fills[k].FuelQuantity))
		lots = append([]fuelLot{{q, float64(fills[k].PerUnitPrice)}}, lots...)
		content -= q
	}
	last := 0.0
	if len(lots) > 0 {
		last = lots[len(lots)-1].price
	} else {
		last = float64(fills[0].PerUnitPrice) // no fill-up before the first leg: the first one's price
	}
	for _, l := range legs {
		for fi < len(fills) && fills[fi].Date.Before(l.Start) {
			f := fills[fi]
			lots = append(lots, fuelLot{float64(f.FuelQuantity), float64(f.PerUnitPrice)})
			last = float64(f.PerUnitPrice)
			if f.IsTankFull != nil && *f.IsTankFull && tank > 0 {
				lots = trimTo(lots, tank)
			}
			fi++
		}
		if l.FuelUsed == nil {
			continue
		}
		need, cost := *l.FuelUsed, 0.0
		for need > 1e-9 && len(lots) > 0 {
			q := minF(need, lots[0].qty)
			cost += q * lots[0].price
			need -= q
			if lots[0].qty -= q; lots[0].qty <= 1e-9 {
				lots = lots[1:]
			}
		}
		out[l.ID] = cost + need*last
	}
	return out
}

// trimTo: a full tank holds exactly tank litres: drop the oldest beyond that, or top up the oldest lot
func trimTo(lots []fuelLot, tank float64) []fuelLot {
	total := 0.0
	for _, l := range lots {
		total += l.qty
	}
	for total > tank && len(lots) > 0 {
		q := minF(total-tank, lots[0].qty)
		lots[0].qty -= q
		total -= q
		if lots[0].qty <= 1e-9 {
			lots = lots[1:]
		}
	}
	if total < tank && len(lots) > 0 {
		lots[0].qty += tank - total
	}
	return lots
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// ---------- reading ----------

type TripView struct {
	db.Trip
	Start     time.Time    `json:"start"`
	End       time.Time    `json:"end"`
	StartOdo  float64      `json:"startOdo"`
	EndOdo    float64      `json:"endOdo"`
	Distance  float64      `json:"distance"`
	DriveMins int          `json:"driveMinutes"` // engine on, stops not counted
	FuelUsed  float64      `json:"fuelUsed"`
	FuelKnown bool         `json:"fuelKnown"` // every leg's fuel is known
	FuelCost  float64      `json:"fuelCost"`
	ExtraCost float64      `json:"extraCost"`
	Legs      []db.TripLeg `json:"legs"`
	Costs     []db.Expense `json:"costs"`
	LegCosts  []float64    `json:"legCosts"` // fuel cost of each leg, same order as Legs
}

// ListTrips: the vehicle's trips that started in [from, to), newest first
func ListTrips(vehicleID string, from, to time.Time) ([]TripView, error) {
	v, err := GetVehicleById(vehicleID)
	if err != nil {
		return nil, err
	}
	var all []db.TripLeg
	if err := db.DB.Where("vehicle_id = ?", vehicleID).Order("started_at").Find(&all).Error; err != nil {
		return nil, err
	}
	var fills []db.Fillup
	db.DB.Where("vehicle_id = ?", vehicleID).Find(&fills)
	costs := fifoCosts(fills, append([]db.TripLeg(nil), all...), float64(v.TankCapacity))
	byTrip := map[string][]db.TripLeg{}
	for _, l := range all {
		byTrip[l.TripID] = append(byTrip[l.TripID], l)
	}
	var trips []db.Trip
	db.DB.Where("vehicle_id = ?", vehicleID).Find(&trips)
	out := []TripView{}
	for _, t := range trips {
		legs := byTrip[t.ID]
		if len(legs) == 0 || legs[0].Start.Before(from) || !legs[0].Start.Before(to) {
			continue
		}
		tv := TripView{Trip: t, Legs: legs, Start: legs[0].Start, End: legs[len(legs)-1].End,
			StartOdo: legs[0].StartOdo, EndOdo: legs[len(legs)-1].EndOdo, FuelKnown: true}
		for _, l := range legs {
			tv.Distance += l.Distance
			tv.DriveMins += int(l.End.Sub(l.Start).Minutes() + 0.5)
			if l.FuelUsed != nil {
				tv.FuelUsed += *l.FuelUsed
			} else {
				tv.FuelKnown = false
			}
			tv.FuelCost += costs[l.ID]
			tv.LegCosts = append(tv.LegCosts, costs[l.ID])
		}
		db.DB.Where("trip_id = ?", t.ID).Order("date").Find(&tv.Costs)
		for _, c := range tv.Costs {
			tv.ExtraCost += float64(c.Amount)
		}
		out = append(out, tv)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.After(out[j].Start) })
	return out, nil
}

// TripsCSV: the trips as CSV (oldest first), for the accountant or a spreadsheet
func TripsCSV(w io.Writer, trips []TripView, currency string) error {
	c := csv.NewWriter(w)
	c.Write([]string{"date", "start", "end", "drive minutes", "start odometer", "end odometer", "distance",
		"business", "purpose", "notes", "fuel used", "fuel cost " + currency, "other costs " + currency, "other cost items", "total " + currency})
	for i := len(trips) - 1; i >= 0; i-- {
		t := trips[i]
		var items []string
		for _, e := range t.Costs {
			items = append(items, fmt.Sprintf("%s %.2f %s", e.ExpenseType, e.Amount, e.Comments))
		}
		fuel := ""
		if t.FuelUsed > 0 {
			fuel = fmt.Sprintf("%.2f", t.FuelUsed)
		}
		c.Write([]string{t.Start.Local().Format("2006-01-02"), t.Start.Local().Format("15:04"), t.End.Local().Format("15:04"),
			strconv.Itoa(t.DriveMins), fmt.Sprintf("%.0f", t.StartOdo), fmt.Sprintf("%.0f", t.EndOdo), fmt.Sprintf("%.1f", t.Distance),
			map[bool]string{true: "yes", false: "no"}[t.Business], t.Purpose, t.Notes, fuel, fmt.Sprintf("%.2f", t.FuelCost),
			fmt.Sprintf("%.2f", t.ExtraCost), strings.TrimSpace(strings.Join(items, "; ")), fmt.Sprintf("%.2f", t.FuelCost+t.ExtraCost)})
	}
	c.Flush()
	return c.Error()
}

// ---------- changes ----------

// TripsLocked: only admins may change a trip's times and kilometres
func TripsLocked() bool { return db.GetOrCreateSetting().TripsLocked }

func SetTripsLocked(locked bool) error {
	s := db.GetOrCreateSetting()
	s.TripsLocked = locked
	return db.UpdateSettings(s)
}

func SetTripMergeMinutes(userID string, m int) error {
	return db.DB.Model(&db.User{}).Where("id = ?", userID).Update("trip_merge_minutes", m).Error
}

func SetTripEntities(vehicleID string, e db.TripEntities) error {
	for _, id := range []string{e.Ignition, e.FuelLevel, e.Distance, e.Consumption} {
		if !ValidEntityID(id) {
			return fmt.Errorf("%q is not an entity id", id)
		}
	}
	return db.DB.Model(&db.Vehicle{}).Where("id = ?", vehicleID).Update("trip_entities", e).Error
}

// TripVehicle: the vehicle of a trip ("" when there's no such trip)
func TripVehicle(tripID string) string {
	var t db.Trip
	db.DB.Where("id = ?", tripID).Limit(1).Find(&t)
	return t.VehicleID
}

func LegVehicle(legID string) (db.TripLeg, bool) {
	var l db.TripLeg
	db.DB.Where("id = ?", legID).Limit(1).Find(&l)
	return l, l.ID != ""
}

func UpdateTrip(tripID string, business bool, purpose, notes string) error {
	return db.DB.Model(&db.Trip{}).Where("id = ?", tripID).
		Updates(map[string]interface{}{"business": business, "purpose": purpose, "notes": notes}).Error
}

// JoinTrip: the trip's drives go to the trip before it (purpose/notes kept when the earlier one has none)
func JoinTrip(tripID string) error {
	var first db.TripLeg
	db.DB.Where("trip_id = ?", tripID).Order("started_at").Limit(1).Find(&first)
	if first.ID == "" {
		return errors.New("trip has no drives")
	}
	var prev db.TripLeg
	db.DB.Where("vehicle_id = ? AND started_at < ? AND trip_id <> ?", first.VehicleID, first.Start, tripID).Order("started_at desc").Limit(1).Find(&prev)
	if prev.ID == "" {
		return errors.New("no trip before this one")
	}
	var a, b db.Trip
	db.DB.Where("id = ?", prev.TripID).Find(&a)
	db.DB.Where("id = ?", tripID).Find(&b)
	upd := map[string]interface{}{"business": a.Business || b.Business}
	if a.Purpose == "" {
		upd["purpose"] = b.Purpose
	}
	if b.Notes != "" {
		upd["notes"] = strings.TrimSpace(a.Notes + "\n" + b.Notes)
	}
	return db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&db.Trip{}).Where("id = ?", a.ID).Updates(upd).Error; err != nil {
			return err
		}
		if err := tx.Model(&db.TripLeg{}).Where("trip_id = ?", tripID).Update("trip_id", a.ID).Error; err != nil {
			return err
		}
		if err := tx.Model(&db.Expense{}).Where("trip_id = ?", tripID).Update("trip_id", a.ID).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", tripID).Delete(&db.Trip{}).Error
	})
}

// SplitTrip: this drive and the ones after it in its trip become a new trip
func SplitTrip(leg db.TripLeg) (string, error) {
	var n int64
	db.DB.Model(&db.TripLeg{}).Where("trip_id = ? AND started_at < ?", leg.TripID, leg.Start).Count(&n)
	if n == 0 {
		return "", errors.New("this is already the first drive of the trip")
	}
	t := db.Trip{VehicleID: leg.VehicleID}
	if err := db.DB.Create(&t).Error; err != nil {
		return "", err
	}
	return t.ID, db.DB.Model(&db.TripLeg{}).Where("trip_id = ? AND started_at >= ?", leg.TripID, leg.Start).Update("trip_id", t.ID).Error
}

type LegInput struct {
	Start    time.Time `json:"start" binding:"required"`
	End      time.Time `json:"end" binding:"required"`
	StartOdo float64   `json:"startOdo" binding:"min=0"`
	EndOdo   float64   `json:"endOdo" binding:"min=0"`
	Distance *float64  `json:"distance"` // nil = EndOdo − StartOdo
	FuelUsed *float64  `json:"fuelUsed"`
}

func (in LegInput) check() error {
	if !in.End.After(in.Start) || in.EndOdo < in.StartOdo {
		return errors.New("the end must be after the start (time and odometer)")
	}
	return nil
}

func (in LegInput) distance() float64 {
	if in.Distance != nil && *in.Distance >= 0 {
		return *in.Distance
	}
	return in.EndOdo - in.StartOdo
}

// AddTrip: a drive entered by hand, as its own trip
func AddTrip(userID, vehicleID string, in LegInput, business bool, purpose string) (string, error) {
	if err := in.check(); err != nil {
		return "", err
	}
	t := db.Trip{VehicleID: vehicleID, Business: business, Purpose: purpose}
	if err := db.DB.Create(&t).Error; err != nil {
		return "", err
	}
	l := db.TripLeg{VehicleID: vehicleID, TripID: t.ID, Start: in.Start, End: in.End, StartOdo: in.StartOdo,
		EndOdo: in.EndOdo, Distance: in.distance(), FuelUsed: in.FuelUsed, Source: "manual"}
	return t.ID, db.DB.Create(&l).Error
}

func UpdateLeg(legID string, in LegInput) error {
	if err := in.check(); err != nil {
		return err
	}
	return db.DB.Model(&db.TripLeg{}).Where("id = ?", legID).Updates(map[string]interface{}{"started_at": in.Start, "ended_at": in.End,
		"start_odo": in.StartOdo, "end_odo": in.EndOdo, "distance": in.distance(), "fuel_used": in.FuelUsed}).Error
}

// DeleteTrip: the trip and its drives; its costs stay as normal expenses
func DeleteTrip(tripID string) error {
	return db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("trip_id = ?", tripID).Delete(&db.TripLeg{}).Error; err != nil {
			return err
		}
		if err := tx.Model(&db.Expense{}).Where("trip_id = ?", tripID).Update("trip_id", "").Error; err != nil {
			return err
		}
		return tx.Where("id = ?", tripID).Delete(&db.Trip{}).Error
	})
}

// AddTripCost: an expense (toll, parking, vignette…) that belongs to the trip, dated at its start
func AddTripCost(userID, tripID string, amount float64, kind, comment string) error {
	var first db.TripLeg
	db.DB.Where("trip_id = ?", tripID).Order("started_at").Limit(1).Find(&first)
	if first.ID == "" {
		return errors.New("trip has no drives")
	}
	u, err := db.GetUserById(userID)
	if err != nil {
		return err
	}
	e := db.Expense{VehicleID: first.VehicleID, Amount: float32(amount), OdoReading: int(first.StartOdo), Comments: comment,
		ExpenseType: kind, UserID: userID, Date: first.Start, Currency: u.Currency, DistanceUnit: u.DistanceUnit, Source: "trip", TripID: tripID}
	return db.DB.Create(&e).Error
}
