package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Fuelo (api.fuelo.net/v2, the user's own key): stations in 28 countries. The free plan (100 requests a
// day, 5 a minute) has stations nearby and country averages; per-station prices need Pro (feature
// "station_prices"). The plan is asked once a day (/v2/usage), so a key upgraded to Pro just starts
// showing station prices. Every request is counted per key and day: at fueloDailyStop hammond stops
// asking, before Fuelo's own limit.

const fueloDailyStop = 90 // of the free plan's 100; Pro keys use their own limit minus 10

// Fuelo fuel codes → the kinds eControlGrade maps to the vehicle's grades
var fueloKinds = map[string]string{"gasoline": "SUP", "diesel": "DIE", "lpg": "LPG", "gasoline98": "PLUS",
	"gasoline98plus": "PLUS", "dieselplus": "DPREM"}

type fueloKey struct {
	day      string // UTC day of the counts (Fuelo's day)
	used     int
	limit    int // the plan's daily limit (0 = unknown yet)
	perMin   int // the plan's requests per minute (0 = unknown yet: the free plan's 5)
	recent   []time.Time
	pro      bool // the plan has station_prices
	planDay  string
	averages map[string]fueloAverage // "sk|gasoline" → the country's latest average
}

type fueloAverage struct {
	at       time.Time
	price    float64
	currency string
}

var (
	fueloKeys   = map[string]*fueloKey{}
	fueloKeysMu sync.Mutex
)

func fueloState(key string) *fueloKey {
	day := time.Now().UTC().Format("2006-01-02")
	k := fueloKeys[key]
	if k == nil {
		k = &fueloKey{averages: map[string]fueloAverage{}}
		fueloKeys[key] = k
	}
	if k.day != day {
		k.day, k.used = day, 0
	}
	return k
}

// fueloGet: one counted request; refused once the day's budget is used up
func fueloGet(base, key, path string, q url.Values, out interface{}) (int, error) {
	fueloKeysMu.Lock()
	k := fueloState(key)
	stop := fueloDailyStop
	if k.limit > 100 {
		stop = k.limit - 10
	}
	if k.used >= stop {
		fueloKeysMu.Unlock()
		return 0, fmt.Errorf("today's request budget is used up (%d), again after 00:00 UTC", stop)
	}
	// the per-minute limit, counted here: a refused request still counts at Fuelo
	perMin := k.perMin
	if perMin == 0 {
		perMin = 5
	}
	now := time.Now()
	var last []time.Time
	for _, t := range k.recent {
		if now.Sub(t) < time.Minute {
			last = append(last, t)
		}
	}
	if len(last) >= perMin {
		fueloKeysMu.Unlock()
		wait := time.Minute - now.Sub(last[0])
		return 0, fmt.Errorf("Fuelo allows %d requests a minute on this plan, try again in %d s", perMin, int(wait.Seconds())+1)
	}
	k.recent = append(last, now)
	k.used++
	fueloKeysMu.Unlock()

	u := base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	resp, err := priceClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("not reachable: %s", strings.ReplaceAll(err.Error(), key, "***"))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var p struct {
			Code   string `json:"code"`
			Detail string `json:"detail"`
		}
		json.NewDecoder(resp.Body).Decode(&p)
		switch p.Code {
		case "daily_quota_exceeded":
			fueloKeysMu.Lock()
			fueloState(key).used = 1 << 20 // stop until tomorrow
			fueloKeysMu.Unlock()
		case "missing_api_key", "invalid_api_key", "api_key_inactive":
			return resp.StatusCode, errors.New("the key is wrong or no longer active")
		}
		if p.Code == "" {
			p.Code = "HTTP " + strconv.Itoa(resp.StatusCode)
		}
		return resp.StatusCode, fmt.Errorf("%s %s", p.Code, p.Detail)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return resp.StatusCode, errors.New("answered without valid data")
	}
	return resp.StatusCode, nil
}

// fueloPro: whether the key's plan has per-station prices, asked once a UTC day
func fueloPro(base, key string) (bool, error) {
	fueloKeysMu.Lock()
	k := fueloState(key)
	if k.planDay == k.day {
		defer fueloKeysMu.Unlock()
		return k.pro, nil
	}
	fueloKeysMu.Unlock()
	var body struct {
		Data struct {
			Plan struct {
				DailyLimit *int     `json:"daily_limit"`
				PerMinute  int      `json:"rate_per_minute"`
				Features   []string `json:"features"`
			} `json:"plan"`
		} `json:"data"`
	}
	if _, err := fueloGet(base, key, "/v2/usage", nil, &body); err != nil {
		return false, err
	}
	fueloKeysMu.Lock()
	defer fueloKeysMu.Unlock()
	k = fueloState(key)
	k.pro, k.planDay, k.perMin = false, k.day, body.Data.Plan.PerMinute
	for _, f := range body.Data.Plan.Features {
		if f == "station_prices" {
			k.pro = true
		}
	}
	if l := body.Data.Plan.DailyLimit; l != nil {
		k.limit = *l
	} else {
		k.limit = 1 << 20 // unlimited
	}
	return k.pro, nil
}

type fueloStation struct {
	ID      int     `json:"id"`
	Country string  `json:"country"`
	Brand   string  `json:"brand"`
	Name    *string `json:"name"`
	City    *string `json:"city"`
	Address *string `json:"address"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	Dist    float64 `json:"distance_km"`
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}

// fueloStationPrices (Pro): the station's prices by the vehicle's grades, and the currency
func fueloStationPrices(base, key string, id string, grades []string) (map[string]float64, string, error) {
	var body struct {
		Data struct {
			Active   bool   `json:"active"`
			Currency string `json:"currency"`
			Prices   []struct {
				Fuel  string   `json:"fuel"`
				Price *float64 `json:"price"`
			} `json:"prices"`
		} `json:"data"`
	}
	if _, err := fueloGet(base, key, "/v2/gasstations/"+url.PathEscape(id)+"/prices", nil, &body); err != nil {
		return nil, "", err
	}
	prices := map[string]float64{}
	for _, p := range body.Data.Prices {
		g := eControlGrade(fueloKinds[strings.ToLower(p.Fuel)], grades)
		if g == "" || p.Price == nil || *p.Price <= 0 {
			continue
		}
		if old, ok := prices[g]; !ok || *p.Price < old {
			prices[g] = *p.Price
		}
	}
	return prices, body.Data.Currency, nil
}

// fueloCountryAverages: the country's latest average per grade the vehicle takes (free plan), each
// fuel asked at most every 6 hours
func fueloCountryAverages(base, key, country string, grades []string) (map[string]float64, string) {
	out, currency := map[string]float64{}, ""
	asked := map[string]bool{}
	for fuel, kind := range fueloKinds {
		g := eControlGrade(kind, grades)
		if g == "" || asked[g] || (kind == "PLUS" && fuel != "gasoline98") { // one 98 average is enough
			continue
		}
		asked[g] = true
		cacheKey := country + "|" + fuel
		fueloKeysMu.Lock()
		a, ok := fueloState(key).averages[cacheKey]
		fueloKeysMu.Unlock()
		if !ok || time.Since(a.at) > 6*time.Hour {
			var body struct {
				Data struct {
					Price    float64 `json:"price"`
					Currency string  `json:"currency"`
				} `json:"data"`
			}
			status, err := fueloGet(base, key, "/v2/prices/average", url.Values{"country": {country}, "fuel": {fuel}}, &body)
			if err != nil && status != http.StatusNotFound {
				continue // budget used up or Fuelo down: not shown, asked again next time
			}
			// 404 no_data is remembered too (price 0), so it isn't asked on every search
			a = fueloAverage{at: time.Now(), price: body.Data.Price, currency: body.Data.Currency}
			fueloKeysMu.Lock()
			fueloState(key).averages[cacheKey] = a
			fueloKeysMu.Unlock()
		}
		if a.price > 0 {
			out[g], currency = a.price, a.currency
		}
	}
	return out, currency
}

// fetchFuelo: stations nearby; with Pro their prices (the nearest 10: one request each), on the free
// plan the country's average per grade instead
func fetchFuelo(base, key string, grades []string, lat, lng, rad float64) (*fueloResult, error) {
	if key == "" {
		return nil, errors.New("Fuelo needs your API key (developers.fuelo.net)")
	}
	pro, err := fueloPro(base, key)
	if err != nil {
		return nil, err
	}
	var body struct {
		Data []fueloStation `json:"data"`
	}
	q := url.Values{"lat": {fmt.Sprint(lat)}, "lon": {fmt.Sprint(lng)}, "radius": {fmt.Sprint(rad)}, "limit": {"20"}}
	// only stations selling the vehicle's first grade (Fuelo counts chargers as stations too)
	if len(grades) > 0 {
		if f := map[string]string{"e5": "gasoline", "e10": "gasoline", "diesel": "diesel", "diesel_premium": "diesel",
			"plus": "gasoline98", "lpg": "lpg"}[grades[0]]; f != "" {
			q.Set("fuel", f)
		}
	}
	if _, err := fueloGet(base, key, "/v2/gasstations/nearby", q, &body); err != nil {
		return nil, err
	}
	res := &fueloResult{}
	for i, st := range body.Data {
		if st.Dist > rad || (st.Brand == "" && str(st.Name) == "") {
			continue
		}
		raw := rawStation{ID: strconv.Itoa(st.ID), Name: str(st.Name), Brand: strings.TrimSpace(st.Brand),
			Address: strings.Trim(str(st.Address)+", "+str(st.City), ", "), Dist: st.Dist, Lat: st.Lat, Lng: st.Lon,
			Prices: map[string]float64{}, IsOpen: true}
		if raw.Name == "" {
			raw.Name = raw.Brand
		}
		if pro && i < 10 {
			if p, cur, err := fueloStationPrices(base, key, raw.ID, grades); err == nil {
				raw.Prices, res.currency = p, cur
			}
		}
		if res.country == "" {
			res.country = strings.ToLower(st.Country)
		}
		res.stations = append(res.stations, raw)
	}
	if !pro && res.country != "" {
		res.averages, res.currency = fueloCountryAverages(base, key, res.country, grades)
	}
	res.pro = pro
	return res, nil
}

type fueloResult struct {
	stations []rawStation
	averages map[string]float64
	currency string
	country  string
	pro      bool
}

// fueloWatchPrices: a watched station's prices of its watched grades, one request (Pro only; free keys can't watch)
func fueloWatchPrices(base, key, stationID string, grades []string) (map[string]float64, error) {
	pro, err := fueloPro(base, key)
	if err != nil {
		return nil, err
	}
	if !pro {
		return nil, errors.New("Fuelo's free plan has no station prices (Pro has)")
	}
	prices, _, err := fueloStationPrices(base, key, stationID, grades)
	return prices, err
}
