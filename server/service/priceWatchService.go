package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/akhilrex/hammond/db"
)

// Price watch: favourite stations (one grade each) checked in the background, every N minutes per price
// source kind (prices change hourly in Germany, every two weeks for Slovenia's regulated grades). Each
// price change is stored; at or below the user's limit one Gotify message is sent, and the next only
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
	GotifyURL      string            `json:"gotifyUrl"`
	GotifyTokenSet bool              `json:"gotifyTokenSet"`
	Intervals      map[string]int    `json:"intervals"` // every kind, with defaults filled in
	Defaults       map[string]int    `json:"defaults"`
	Labels         map[string]string `json:"labels"`
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
	return &PriceWatchSettings{GotifyURL: u.GotifyURL, GotifyTokenSet: u.GotifyToken != "", Intervals: iv,
		Defaults: DefaultWatchMinutes, Labels: labels}, nil
}

// UpdatePriceWatchSettings: token "" keeps the stored one, "-" removes it; intervals outside the limits
// are refused, missing kinds keep their default
func UpdatePriceWatchSettings(userID, gotifyURL, token string, intervals map[string]int) error {
	gotifyURL = strings.TrimRight(strings.TrimSpace(gotifyURL), "/")
	if gotifyURL != "" {
		if u, err := url.Parse(gotifyURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(gotifyURL) > 300 {
			return errors.New("invalid Gotify URL")
		}
	}
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
	upd := map[string]interface{}{"gotify_url": gotifyURL, "price_watch_intervals": string(b)}
	switch token = strings.TrimSpace(token); token {
	case "":
	case "-":
		upd["gotify_token"] = ""
	default:
		if len(token) > 200 {
			return errors.New("Gotify token too long")
		}
		upd["gotify_token"] = token
	}
	return db.DB.Model(&db.User{}).Where("id = ?", userID).Updates(upd).Error
}

// SendGotify: one message to the user's Gotify (token in a header, not in the URL: no logs with it)
func SendGotify(u *db.User, title, message string) error {
	if u.GotifyURL == "" || u.GotifyToken == "" {
		return errors.New("Gotify is not set up")
	}
	body, _ := json.Marshal(map[string]interface{}{"title": title, "message": message, "priority": 5})
	req, _ := http.NewRequest("POST", u.GotifyURL+"/message", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gotify-Key", u.GotifyToken)
	resp, err := priceClient.Do(req)
	if err != nil {
		return fmt.Errorf("Gotify not reachable: %s", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Gotify answered HTTP %d (wrong token?)", resp.StatusCode)
	}
	return nil
}

func TestGotify(userID string) error {
	u := getUser(userID)
	if u == nil {
		return errors.New("user not found")
	}
	return SendGotify(u, "hammond", "Test: price alerts will arrive here.")
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

// watchPrice asks the watch's profile around the station and picks it out of the list (the sources
// only search by position)
func watchPrice(w *db.PriceWatch, profiles []db.PriceProfile) (float64, error) {
	var p *db.PriceProfile
	for _, x := range profiles {
		if x.ID == w.ProfileID {
			x := x
			p = &x
		}
	}
	if p == nil {
		return 0, errors.New("its price profile was removed")
	}
	if p.Kind == "fuelo" { // one request for the station, not a search around it
		base := p.URL
		if base == "" {
			base = FueloURL
		}
		return fueloWatchPrice(strings.TrimRight(base, "/"), p.APIKey, w.StationID, w.Grade)
	}
	res, err := profileStations(*p, []string{w.Grade}, w.Lat, w.Lng, 1)
	if err != nil {
		return 0, err
	}
	for _, st := range res.Stations {
		if st.ID == w.StationID {
			if v, ok := st.Prices[w.Grade]; ok {
				return v, nil
			}
			return 0, errors.New("no price for this grade right now")
		}
	}
	return 0, errors.New("station not in the price list any more")
}

// applyPrice: the state after a successful check, and the alert to send ("" = none)
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
	for i := range watches {
		w := &watches[i]
		now := time.Now()
		price, err := watchPrice(w, profiles)
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
			msg := fmt.Sprintf("%s %.3f %s (your limit %.3f)\n%s", gradeName(w.Grade), price, u.Currency, *w.Below, w.Address)
			if err := SendGotify(u, "⛽ "+w.Name, msg); err != nil && u.GotifyURL != "" {
				log.Printf("price watch (%s): %s", u.Email, err)
			}
		}
	}
	if len(down) > 0 {
		msg := fmt.Sprintf("Failed %d checks in a row (it keeps trying):\n%s", failAlertAfter, strings.Join(down, "\n"))
		if err := SendGotify(u, "hammond: station prices not reachable", msg); err != nil && u.GotifyURL != "" {
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
				due[w.UserID] = append(due[w.UserID], w)
			}
		}
		for id, list := range due {
			checkWatches(users[id], list)
		}
	}
}
