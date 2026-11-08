package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/akhilrex/hammond/db"
)

// Station prices from a Tankerkönig-compatible API (list.php). Profiles: "tankerkoenig" (Germany,
// creativecommons.tankerkoenig.de, free key) or "custom" (any URL speaking the same format).

const TankerkoenigURL = "https://creativecommons.tankerkoenig.de/json"

type Station struct {
	ID      string             `json:"id"`
	Name    string             `json:"name"`
	Brand   string             `json:"brand"`
	Address string             `json:"address"`
	Dist    float64            `json:"dist"`   // km
	Prices  map[string]float64 `json:"prices"` // accepted grades this station sells
	Price   *float64           `json:"price"`  // the preferred grade's price, or the first accepted one it sells
	Grade   string             `json:"grade"`  // grade of Price
	IsOpen  bool               `json:"isOpen"`
}

type StationsResult struct {
	Grades      []string  `json:"grades"` // accepted, preferred first
	Attribution string    `json:"attribution"`
	Stations    []Station `json:"stations"`
}

// accepted grades, preferred first: the vehicle's list, else from its fuel type
func VehicleFuelGrades(v *db.Vehicle) []string {
	if v.FuelGrade != "" {
		return strings.Split(v.FuelGrade, ",")
	}
	switch v.FuelType {
	case db.PETROL:
		return []string{"e5"}
	case db.DIESEL:
		return []string{"diesel"}
	case db.ETHANOL:
		return []string{"e10"}
	}
	return nil
}

// NormalizeFuelGrades checks "e10, E5" → "e10,e5" (known grades, no duplicates, order kept)
func NormalizeFuelGrades(s string) (string, error) {
	var out []string
	seen := map[string]bool{}
	for _, g := range strings.Split(s, ",") {
		g = strings.ToLower(strings.TrimSpace(g))
		if g == "" || seen[g] {
			continue
		}
		if g != "e5" && g != "e10" && g != "diesel" {
			return "", fmt.Errorf("unknown fuel grade %q (e5, e10, diesel)", g)
		}
		seen[g] = true
		out = append(out, g)
	}
	return strings.Join(out, ","), nil
}

// NormalizeFuelSubtypes: own fuel types, one per line (or comma), trimmed, no duplicates, ≤ 20 of ≤ 40 chars
func NormalizeFuelSubtypes(s string) string {
	var out []string
	seen := map[string]bool{}
	for _, t := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == ',' }) {
		t = strings.TrimSpace(t)
		if t == "" || len([]rune(t)) > 40 || seen[strings.ToLower(t)] || len(out) == 20 {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	return strings.Join(out, "\n")
}

var (
	stationCache   = map[string]cachedStations{}
	stationCacheMu sync.Mutex
	priceClient    = &http.Client{Timeout: 8 * time.Second}
)

type cachedStations struct {
	at  time.Time
	res []tkStation
	lic string
}

// ponytail: in-memory cache keyed by rounded position, 5 min (Tankerkönig asks not to poll faster);
// grows only with distinct places looked up, fine for a self-hosted app
func cacheKey(base string, lat, lng, rad float64) string {
	return fmt.Sprintf("%s|%.3f|%.3f|%.0f", base, lat, lng, rad)
}

type tkStation struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Brand       string          `json:"brand"`
	Street      string          `json:"street"`
	HouseNumber string          `json:"houseNumber"`
	PostCode    json.RawMessage `json:"postCode"`
	Place       string          `json:"place"`
	Dist        float64         `json:"dist"`
	E5          json.RawMessage `json:"e5"`
	E10         json.RawMessage `json:"e10"`
	Diesel      json.RawMessage `json:"diesel"`
	IsOpen      bool            `json:"isOpen"`
}

// prices are numbers, or false/null when the station doesn't sell that grade
func rawPrice(r json.RawMessage) *float64 {
	var f float64
	if json.Unmarshal(r, &f) == nil && f > 0 {
		return &f
	}
	return nil
}

func NearbyStations(vehicle *db.Vehicle, lat, lng, radiusKm float64) (*StationsResult, error) {
	s := db.GetOrCreateSetting()
	base := ""
	switch s.FuelPriceProvider {
	case "tankerkoenig":
		base = TankerkoenigURL
		if s.FuelPriceURL != "" {
			base = s.FuelPriceURL
		}
	case "custom":
		base = s.FuelPriceURL
	}
	if base == "" {
		return nil, errors.New("station prices are not set up (Admin → Site settings → Fuel prices)")
	}
	grades := VehicleFuelGrades(vehicle)
	if len(grades) == 0 {
		return nil, errors.New("no station prices for this vehicle's fuel type")
	}
	if math.IsNaN(lat) || lat < -90 || lat > 90 || math.IsNaN(lng) || lng < -180 || lng > 180 {
		return nil, errors.New("invalid position")
	}
	radiusKm = math.Min(math.Max(radiusKm, 1), 25) // Tankerkönig: max 25 km
	base = strings.TrimRight(base, "/")

	key := cacheKey(base, lat, lng, radiusKm)
	stationCacheMu.Lock()
	c, ok := stationCache[key]
	stationCacheMu.Unlock()
	if !ok || time.Since(c.at) > 5*time.Minute {
		q := url.Values{"lat": {fmt.Sprint(lat)}, "lng": {fmt.Sprint(lng)}, "rad": {fmt.Sprint(radiusKm)},
			"sort": {"dist"}, "type": {"all"}, "apikey": {s.FuelPriceAPIKey}}
		resp, err := priceClient.Get(base + "/list.php?" + q.Encode())
		if err != nil {
			return nil, fmt.Errorf("price service not reachable: %s", strings.ReplaceAll(err.Error(), s.FuelPriceAPIKey, "***"))
		}
		defer resp.Body.Close()
		var body struct {
			OK       bool        `json:"ok"`
			Message  string      `json:"message"`
			License  string      `json:"license"`
			Data     string      `json:"data"`
			Stations []tkStation `json:"stations"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return nil, fmt.Errorf("price service answered HTTP %d without valid data", resp.StatusCode)
		}
		if !body.OK {
			return nil, fmt.Errorf("price service: %s", body.Message)
		}
		lic := strings.TrimSpace(strings.Join([]string{body.Data, body.License}, " · "))
		c = cachedStations{at: time.Now(), res: body.Stations, lic: strings.Trim(lic, " ·")}
		stationCacheMu.Lock()
		stationCache[key] = c
		stationCacheMu.Unlock()
	}

	res := &StationsResult{Grades: grades, Attribution: c.lic, Stations: []Station{}}
	if s.FuelPriceProvider == "tankerkoenig" {
		res.Attribution = "Prices: MTS-K via Tankerkönig (CC BY 4.0)"
	}
	for _, st := range c.res {
		prices := map[string]float64{}
		var p *float64
		chosen := ""
		for _, g := range grades {
			var v *float64
			switch g {
			case "e5":
				v = rawPrice(st.E5)
			case "e10":
				v = rawPrice(st.E10)
			case "diesel":
				v = rawPrice(st.Diesel)
			}
			if v != nil {
				prices[g] = *v
				if p == nil {
					p, chosen = v, g
				}
			}
		}
		addr := strings.TrimSpace(st.Street + " " + st.HouseNumber)
		if st.Place != "" {
			addr = strings.Trim(addr+", "+strings.TrimSpace(strings.Trim(string(st.PostCode), `"`)+" "+st.Place), ", ")
		}
		res.Stations = append(res.Stations, Station{ID: st.ID, Name: strings.TrimSpace(st.Name), Brand: strings.TrimSpace(st.Brand),
			Address: addr, Dist: st.Dist, Prices: prices, Price: p, Grade: chosen, IsOpen: st.IsOpen})
	}
	// stations that sell the grade first, then by distance
	sort.SliceStable(res.Stations, func(i, j int) bool {
		if (res.Stations[i].Price == nil) != (res.Stations[j].Price == nil) {
			return res.Stations[i].Price != nil
		}
		return res.Stations[i].Dist < res.Stations[j].Dist
	})
	if len(res.Stations) > 10 {
		res.Stations = res.Stations[:10]
	}
	return res, nil
}

// UpdateFuelPriceSettings (admin). apiKey "" keeps the stored key, "-" removes it.
func UpdateFuelPriceSettings(provider, rawURL, apiKey string) error {
	s := db.GetOrCreateSetting()
	if provider == "custom" && rawURL == "" {
		return errors.New("a custom provider needs a URL")
	}
	s.FuelPriceProvider, s.FuelPriceURL = provider, rawURL
	switch apiKey {
	case "":
	case "-":
		s.FuelPriceAPIKey = ""
	default:
		s.FuelPriceAPIKey = apiKey
	}
	stationCacheMu.Lock()
	stationCache = map[string]cachedStations{}
	stationCacheMu.Unlock()
	return db.UpdateSettings(s)
}

// TestFuelPriceProvider (admin "Test connection"): asks the stored provider with the stored key for
// stations around a fixed point (Frankfurt city centre for the German default, or the given position).
func TestFuelPriceProvider(lat, lng float64) (*StationsResult, error) {
	stationCacheMu.Lock()
	stationCache = map[string]cachedStations{} // always a real request
	stationCacheMu.Unlock()
	probe := &db.Vehicle{FuelGrade: "e10,e5,diesel"}
	return NearbyStations(probe, lat, lng, 5)
}
