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
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Brand   string   `json:"brand"`
	Address string   `json:"address"`
	Dist    float64  `json:"dist"` // km
	Price   *float64 `json:"price"`
	IsOpen  bool     `json:"isOpen"`
}

type StationsResult struct {
	Grade       string    `json:"grade"`
	Attribution string    `json:"attribution"`
	Stations    []Station `json:"stations"`
}

// grade to ask for: the vehicle's, else from its fuel type
func VehicleFuelGrade(v *db.Vehicle) string {
	if v.FuelGrade != "" {
		return v.FuelGrade
	}
	switch v.FuelType {
	case db.PETROL:
		return "e5"
	case db.DIESEL:
		return "diesel"
	case db.ETHANOL:
		return "e10"
	}
	return ""
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
	grade := VehicleFuelGrade(vehicle)
	if grade == "" {
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

	res := &StationsResult{Grade: grade, Attribution: c.lic, Stations: []Station{}}
	if s.FuelPriceProvider == "tankerkoenig" {
		res.Attribution = "Prices: MTS-K via Tankerkönig (CC BY 4.0)"
	}
	for _, st := range c.res {
		var p *float64
		switch grade {
		case "e5":
			p = rawPrice(st.E5)
		case "e10":
			p = rawPrice(st.E10)
		case "diesel":
			p = rawPrice(st.Diesel)
		}
		addr := strings.TrimSpace(st.Street + " " + st.HouseNumber)
		if st.Place != "" {
			addr = strings.Trim(addr+", "+strings.TrimSpace(strings.Trim(string(st.PostCode), `"`)+" "+st.Place), ", ")
		}
		res.Stations = append(res.Stations, Station{ID: st.ID, Name: strings.TrimSpace(st.Name), Brand: strings.TrimSpace(st.Brand),
			Address: addr, Dist: st.Dist, Price: p, IsOpen: st.IsOpen})
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
