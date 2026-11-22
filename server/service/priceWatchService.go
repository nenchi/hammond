package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/akhilrex/hammond/db"
)

// Price watch: favourite stations (one grade each) checked in the background, every N minutes per price
// source kind (prices change hourly in Germany, every two weeks for Slovenia's regulated grades). Each
// price change is stored; at or below the user's limit one alert is sent (notification channels), the next only
// after the price went above the limit again.

// default minutes between checks per kind; users can change them (MinWatchMinutes–MaxWatchMinutes)
var DefaultWatchMinutes = map[string]int{"tankerkoenig": 60, "econtrol": 60, "goriva": 1440, "mzoe": 1440,
	"france": 360, "portugal": 360, "italy": 1440, "spain": 60, "fuelo": 720, "custom": 60}

const (
	MinWatchMinutes = 15
	MaxWatchMinutes = 7 * 24 * 60
	maxWatches      = 20
	failAlertAfter  = 3 // failed checks in a row before "not reachable" is sent (once)
)

func WatchMinutes(u *db.User, kind string) int {
	if m, ok := u.PriceWatchIntervals[kind]; ok && m >= MinWatchMinutes && m <= MaxWatchMinutes {
		return m
	}
	if m, ok := DefaultWatchMinutes[kind]; ok {
		return m
	}
	return 60
}

type PriceWatchSettings struct {
	Intervals map[string]int    `json:"intervals"` // every kind, with defaults filled in
	Defaults  map[string]int    `json:"defaults"`
	Labels    map[string]string `json:"labels"`
	// alerts go to the notification channels (Settings → Notifications): how many are on
	Channels int `json:"channels"`
}

func GetPriceWatchSettings(userID string) (*PriceWatchSettings, error) {
	u := getUser(userID)
	if u == nil {
		return nil, errors.New("user not found")
	}
	iv := map[string]int{}
	labels := map[string]string{"custom": "Custom"}
	for k := range DefaultWatchMinutes {
		iv[k] = WatchMinutes(u, k)
		if l, ok := kindLabel[k]; ok {
			labels[k] = l
		}
	}
	var n int64
	db.DB.Model(&db.NotifyChannel{}).Where("user_id = ? AND enabled = ?", userID, true).Count(&n)
	return &PriceWatchSettings{Intervals: iv, Defaults: DefaultWatchMinutes, Labels: labels, Channels: int(n)}, nil
}

// UpdatePriceWatchSettings: intervals outside the limits are refused, missing kinds keep their default
func UpdatePriceWatchSettings(userID string, intervals map[string]int) error {
	iv := map[string]int{}
	for k, m := range intervals {
		if _, ok := DefaultWatchMinutes[k]; !ok {
			return fmt.Errorf("unknown price source %q", k)
		}
		if m < MinWatchMinutes || m > MaxWatchMinutes {
			return fmt.Errorf("check interval must be %d minutes to %d days", MinWatchMinutes, MaxWatchMinutes/1440)
		}
		if m != DefaultWatchMinutes[k] {
			iv[k] = m
		}
	}
	b, _ := json.Marshal(iv)
	return db.DB.Model(&db.User{}).Where("id = ?", userID).Update("price_watch_intervals", string(b)).Error
}

func ListPriceWatches(userID string) ([]db.PriceWatch, error) {
	list := []db.PriceWatch{}
	return list, db.DB.Where("user_id = ?", userID).Order("created_at").Find(&list).Error
}

func validBelow(b *float64) bool { return b == nil || (*b > 0 && *b < 100 && !math.IsNaN(*b)) }

// CreatePriceWatch: a station from "Stations nearby" (profile, station id, position) with one grade
func CreatePriceWatch(userID string, w db.PriceWatch) (*db.PriceWatch, error) {
	var p *db.PriceProfile
	for _, x := range AvailablePriceProfiles(userID) {
		if x.ID == w.ProfileID {
			x := x
			p = &x
		}
	}
	switch {
	case p == nil:
		return nil, errors.New("unknown price profile")
	case w.StationID == "" || len(w.StationID) > 100:
		return nil, errors.New("station id missing")
	case !ValidGrade(w.Grade):
		return nil, errors.New("unknown fuel grade")
	case !validBelow(w.Below):
		return nil, errors.New("invalid price limit")
	case math.IsNaN(w.Lat) || w.Lat < -90 || w.Lat > 90 || math.IsNaN(w.Lng) || w.Lng < -180 || w.Lng > 180 || (w.Lat == 0 && w.Lng == 0):
		return nil, errors.New("the station has no position, it can't be watched")
	}
	var n int64
	db.DB.Model(&db.PriceWatch{}).Where("user_id = ?", userID).Count(&n)
	if n >= maxWatches {
		return nil, fmt.Errorf("at most %d watched stations", maxWatches)
	}
	db.DB.Model(&db.PriceWatch{}).Where("user_id = ? AND profile_id = ? AND station_id = ? AND grade = ?", userID, p.ID, w.StationID, w.Grade).Count(&n)
	if n > 0 {
		return nil, errors.New("this station and grade is already watched")
	}
	watch := db.PriceWatch{UserID: userID, ProfileID: p.ID, Kind: p.Kind, StationID: w.StationID, Name: trim(w.Name, 100),
		Address: trim(w.Address, 200), Lat: w.Lat, Lng: w.Lng, Grade: w.Grade, Below: w.Below, LastPrice: w.LastPrice}
	if err := db.DB.Create(&watch).Error; err != nil {
		return nil, err
	}
	if watch.LastPrice != nil { // today's price from the list: the graph starts right away
		now := time.Now()
		watch.CheckedAt = &now
		db.DB.Model(&watch).Update("checked_at", now)
		db.DB.Create(&db.StationPrice{WatchID: watch.ID, Price: *watch.LastPrice, At: now})
	}
	return &watch, nil
}

func trim(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func getWatch(userID, id string) (*db.PriceWatch, error) {
	var w db.PriceWatch
	if err := db.DB.Where("id = ? AND user_id = ?", id, userID).Limit(1).Find(&w).Error; err != nil || w.ID == "" {
		return nil, errors.New("not found")
	}
	return &w, nil
}

// UpdatePriceWatch: grade and limit. A new grade starts a new history.
func UpdatePriceWatch(userID, id, grade string, below *float64) (*db.PriceWatch, error) {
	w, err := getWatch(userID, id)
	if err != nil {
		return nil, err
	}
	if !ValidGrade(grade) || !validBelow(below) {
		return nil, errors.New("invalid grade or price limit")
	}
	if grade != w.Grade {
		var n int64
		db.DB.Model(&db.PriceWatch{}).Where("user_id = ? AND profile_id = ? AND station_id = ? AND grade = ?", userID, w.ProfileID, w.StationID, grade).Count(&n)
		if n > 0 {
			return nil, errors.New("this station and grade is already watched")
		}
		db.DB.Where("watch_id = ?", w.ID).Delete(&db.StationPrice{})
		w.LastPrice, w.CheckedAt = nil, nil
	}
	w.Grade, w.Below, w.Alerted = grade, below, false // a new limit may alert again
	return w, db.DB.Select("grade", "below", "alerted", "last_price", "checked_at").Save(w).Error
}

func DeletePriceWatch(userID, id string) error {
	w, err := getWatch(userID, id)
	if err != nil {
		return err
	}
	db.DB.Where("watch_id = ?", w.ID).Delete(&db.StationPrice{})
	return db.DB.Delete(w).Error
}

type FillupPoint struct {
	At    time.Time `json:"at"`
	Price float32   `json:"price"`
}

type WatchHistory struct {
	Prices  []db.StationPrice `json:"prices"`
	Fillups []FillupPoint     `json:"fillups"` // the user's own fill-ups at this station, for the graph
}

func PriceWatchHistory(userID, id string, since time.Time) (*WatchHistory, error) {
	w, err := getWatch(userID, id)
	if err != nil {
		return nil, err
	}
	h := &WatchHistory{Prices: []db.StationPrice{}, Fillups: []FillupPoint{}}
	// the last change before "since" too: the line starts at the price that was valid then
	var before db.StationPrice
	db.DB.Where("watch_id = ? AND at < ?", w.ID, since).Order("at desc").Limit(1).Find(&before)
	if before.ID != 0 {
		h.Prices = append(h.Prices, before)
	}
	var rest []db.StationPrice
	if err := db.DB.Where("watch_id = ? AND at >= ?", w.ID, since).Order("at").Find(&rest).Error; err != nil {
		return nil, err
	}
	h.Prices = append(h.Prices, rest...)
	// fill-ups whose station is what "Stations nearby" writes for it: "Brand, address".
	// ponytail: exact name match; a hand-typed station name isn't found
	station := w.Name
	if w.Address != "" {
		station += ", " + w.Address
	}
	var fillups []db.Fillup
	db.DB.Where("user_id = ? AND date >= ? AND lower(filling_station) = lower(?)", userID, since, station).Order("date").Find(&fillups)
	for _, f := range fillups {
		h.Fillups = append(h.Fillups, FillupPoint{At: f.Date, Price: f.PerUnitPrice})
	}
	return h, nil
}

// stationKey: watches of the same station (one per grade) are checked together, with one request
func stationKey(w *db.PriceWatch) string { return w.UserID + "|" + w.ProfileID + "|" + w.StationID }

// stationPrices: today's prices of one watched station for all its watched grades, one request (the
// sources only search by position: asked around the station, it's picked out of the list).
// ponytail: sources with one plain 95 (SUP) store it under the first of e5/e10 asked, so watching both
// e5 and e10 there prices only the first; neither country publishes both, so it doesn't matter.
func stationPrices(w *db.PriceWatch, grades []string, profiles []db.PriceProfile) (map[string]float64, error) {
	var p *db.PriceProfile
	for _, x := range profiles {
		if x.ID == w.ProfileID {
			x := x
			p = &x
		}
	}
	if p == nil {
		return nil, errors.New("its price profile was removed")
	}
	if p.Kind == "fuelo" { // one request for the station, not a search around it
		base := p.URL
		if base == "" {
			base = FueloURL
		}
		return fueloWatchPrices(strings.TrimRight(base, "/"), p.APIKey, w.StationID, grades)
	}
	res, err := profileStations(*p, grades, w.Lat, w.Lng, 1)
	if err != nil {
		return nil, err
	}
	for _, st := range res.Stations {
		if st.ID == w.StationID {
			return st.Prices, nil
		}
	}
	return nil, errors.New("station not in the price list any more")
}

func applyPrice(w *db.PriceWatch, price float64, now time.Time) (changed bool, alert bool) {
	changed = w.LastPrice == nil || math.Abs(*w.LastPrice-price) > 0.0005
	w.LastPrice, w.CheckedAt, w.LastError, w.Failures = &price, &now, "", 0
	switch {
	case w.Below == nil:
		w.Alerted = false
	case price <= *w.Below+1e-9:
		alert = !w.Alerted
		w.Alerted = true
	default:
		w.Alerted = false
	}
	return changed, alert
}

// checkWatches checks the given watches of one user; failures that just reached failAlertAfter are
// sent as one message
func checkWatches(u *db.User, watches []db.PriceWatch) {
	profiles := AvailablePriceProfiles(u.ID)
	var down []string
	// one request per station for all its watched grades
	grades := map[string][]string{}
	for _, w := range watches {
		grades[stationKey(&w)] = append(grades[stationKey(&w)], w.Grade)
	}
	type fetched struct {
		prices map[string]float64
		err    error
	}
	got := map[string]fetched{}
	for i := range watches {
		w := &watches[i]
		now := time.Now()
		f, ok := got[stationKey(w)]
		if !ok {
			f.prices, f.err = stationPrices(w, grades[stationKey(w)], profiles)
			got[stationKey(w)] = f
		}
		price, err := f.prices[w.Grade], f.err
		if err == nil && f.prices[w.Grade] == 0 {
			err = errors.New("no price for this grade right now")
		}
		if err != nil {
			w.CheckedAt, w.LastError = &now, err.Error()
			w.Failures++
			if w.Failures == failAlertAfter {
				down = append(down, fmt.Sprintf("%s (%s): %s", w.Name, kindLabelOr(w.Kind), err))
			}
			db.DB.Select("checked_at", "last_error", "failures").Save(w)
			continue
		}
		changed, alert := applyPrice(w, price, now)
		if changed {
			db.DB.Create(&db.StationPrice{WatchID: w.ID, Price: price, At: now})
		}
		db.DB.Select("last_price", "checked_at", "last_error", "failures", "alerted").Save(w)
		if alert {
			// short lines: a phone's chat bubble wraps at ~30 characters
			msg := fmt.Sprintf("%.3f %s  (limit %.3f)", price, u.Currency, *w.Below)
			if a := ShortAddress(w.Address); a != "" {
				msg += "\n" + a
			}
			if err := SendAlert(u, "⛽ "+w.Name+" · "+gradeName(w.Grade), msg, MapsDirections(w.Lat, w.Lng)); err != nil && alertsOn(u) {
				log.Printf("price watch (%s): %s", u.Email, err)
			}
		}
	}
	if len(down) > 0 {
		msg := fmt.Sprintf("Failed %d checks in a row (it keeps trying):\n%s", failAlertAfter, strings.Join(down, "\n"))
		if err := SendAlert(u, "hammond: station prices not reachable", msg, ""); err != nil && alertsOn(u) {
			log.Printf("price watch (%s): %s", u.Email, err)
		}
	}
}

func kindLabelOr(kind string) string {
	if l, ok := kindLabel[kind]; ok {
		return l
	}
	return kind
}

// MapsDirections: Google Maps directions to the station (on Android it opens the Maps app).
func MapsDirections(lat, lng float64) string {
	if lat == 0 && lng == 0 {
		return ""
	}
	return fmt.Sprintf("https://www.google.com/maps/dir/?api=1&destination=%.6f,%.6f", lat, lng)
}

var postCode = regexp.MustCompile(`,\s*(?:[A-Z]{1,2}-)?\d{4,5}\s+`)

// ShortAddress: "ODENWALDRING 84, 63069 OFFENBACH" → "Odenwaldring 84, Offenbach" (all-caps sources such as
// Tankerkönig get normal capitals, the postcode goes: it makes the line too long for a phone).
func ShortAddress(a string) string {
	a = postCode.ReplaceAllString(strings.TrimSpace(a), ", ")
	if strings.ToUpper(a) != a {
		return a // already mixed case
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

func gradeName(g string) string {
	return map[string]string{"e5": "Super E5", "e10": "Super E10", "diesel": "Diesel", "plus": "98/100",
		"diesel_premium": "Premium diesel", "lpg": "LPG"}[g]
}

// CheckPriceWatchesNow ("Check now"): all of the user's watches (the 5-minute price cache still applies)
func CheckPriceWatchesNow(userID string) ([]db.PriceWatch, error) {
	u := getUser(userID)
	if u == nil {
		return nil, errors.New("user not found")
	}
	list, err := ListPriceWatches(userID)
	if err != nil {
		return nil, err
	}
	checkWatches(u, list)
	return list, nil
}

// PriceWatchTimer: every minute, the watches whose interval has passed
func PriceWatchTimer() {
	for range time.Tick(time.Minute) {
		var all []db.PriceWatch
		db.DB.Find(&all)
		due := map[string][]db.PriceWatch{}
		users := map[string]*db.User{}
		dueStation := map[string]bool{} // one grade due = all grades of that station, so they share the request
		for _, w := range all {
			u, ok := users[w.UserID]
			if !ok {
				u = getUser(w.UserID)
				users[w.UserID] = u
			}
			if u == nil || u.IsDisabled {
				continue
			}
			if w.CheckedAt == nil || time.Since(*w.CheckedAt) >= time.Duration(WatchMinutes(u, w.Kind))*time.Minute {
				dueStation[stationKey(&w)] = true
			}
		}
		for _, w := range all {
			if dueStation[stationKey(&w)] {
				due[w.UserID] = append(due[w.UserID], w)
			}
		}
		for id, list := range due {
			checkWatches(users[id], list)
		}
	}
}
