package service

import (
	"compress/gzip"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/akhilrex/hammond/db"
	"github.com/google/uuid"
)

// Station prices from a list of profiles (Admin → Site settings), tried in order: the first one with
// stations near the position wins, so no country borders are needed (each API only knows its country).
// Kinds: "tankerkoenig" (Germany, free key), "econtrol" (Austria, no key), "goriva" (Slovenia, no key),
// "mzoe" (Croatia, no key), "france" (prix-carburants, no key), "portugal" (DGEG, no key) and "custom"
// (any URL speaking the Tankerkönig list.php format).

const (
	TankerkoenigURL   = "https://creativecommons.tankerkoenig.de/json"
	EControlURL       = "https://api.e-control.at/sprit/1.0"
	GorivaURL         = "https://goriva.si/api/v1"
	MzoeURL           = "https://webservis.mzoe-gor.hr/data.gz"
	PrixCarburantsURL = "https://data.economie.gouv.fr/api/explore/v2.1/catalog/datasets/prix-des-carburants-en-france-flux-instantane-v2"
	DgegURL           = "https://precoscombustiveis.dgeg.gov.pt/api/PrecoComb"
)

type Station struct {
	ID      string             `json:"id"`
	Name    string             `json:"name"`
	Brand   string             `json:"brand"`
	Address string             `json:"address"`
	Dist    float64            `json:"dist"` // km
	Lat     float64            `json:"lat"`  // 0/0 = unknown (no navigation link)
	Lng     float64            `json:"lng"`
	Prices  map[string]float64 `json:"prices"` // accepted grades this station sells
	Price   *float64           `json:"price"`  // the preferred grade's price, or the first accepted one it sells
	Grade   string             `json:"grade"`  // grade of Price
	IsOpen  bool               `json:"isOpen"`
}

type StationsResult struct {
	Profile     string    `json:"profile"` // id of the profile that answered
	ProfileName string    `json:"profileName"`
	Grades      []string  `json:"grades"` // accepted, preferred first
	Attribution string    `json:"attribution"`
	Stations    []Station `json:"stations"`
	Errors      []string  `json:"errors,omitempty"` // profiles that failed (only when no stations were found)
	Country     string    `json:"country"`          // where the position is ("" = unknown: every profile asked)
}

// country of each kind ("custom" has none: asked everywhere, after the country's own profiles)
var kindCountry = map[string]string{"tankerkoenig": "de", "econtrol": "at", "goriva": "si", "mzoe": "hr", "france": "fr", "portugal": "pt"}

var kindLabel = map[string]string{"tankerkoenig": "Germany – Tankerkönig", "econtrol": "Austria – E-Control", "goriva": "Slovenia – goriva.si",
	"mzoe": "Croatia – MZOE", "france": "France – prix-carburants", "portugal": "Portugal – DGEG"}

// GeocodeURL: OpenStreetMap Nominatim reverse geocoding, only for the country of a station search
// ("" = off: every profile is asked in order, as before)
var GeocodeURL = "https://nominatim.openstreetmap.org/reverse"

var (
	countryCache   = map[string][2]string{}
	countryCacheMu sync.Mutex
)

// countryAt: ISO code and English name of the country at a position, "" when unknown (sea, Nominatim
// down). ponytail: cached per ~1 km forever, never evicted; fine for one household's fill-ups
// (Nominatim allows 1 request/s, a fill-up is far rarer).
func countryAt(lat, lng float64) (code, name string) {
	if GeocodeURL == "" {
		return "", ""
	}
	key := fmt.Sprintf("%.2f,%.2f", lat, lng)
	countryCacheMu.Lock()
	c, ok := countryCache[key]
	countryCacheMu.Unlock()
	if ok {
		return c[0], c[1]
	}
	q := url.Values{"format": {"jsonv2"}, "lat": {fmt.Sprint(lat)}, "lon": {fmt.Sprint(lng)}, "zoom": {"3"}, "accept-language": {"en"}}
	req, _ := http.NewRequest("GET", GeocodeURL+"?"+q.Encode(), nil)
	req.Header.Set("User-Agent", "hammond (https://github.com/nenchi/hammond)") // Nominatim policy: identify the app
	resp, err := priceClient.Do(req)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()
	var body struct {
		Address struct {
			Country     string `json:"country"`
			CountryCode string `json:"country_code"`
		} `json:"address"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil || resp.StatusCode != http.StatusOK {
		return "", "" // not cached: try again next time
	}
	c = [2]string{strings.ToLower(body.Address.CountryCode), body.Address.Country}
	countryCacheMu.Lock()
	countryCache[key] = c
	countryCacheMu.Unlock()
	return c[0], c[1]
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

// one station as every kind delivers it: prices per grade (e5/e10/diesel) it sells
type rawStation struct {
	ID, Name, Brand, Address string
	Dist, Lat, Lng           float64
	Prices                   map[string]float64
	IsOpen                   bool
}

type cachedStations struct {
	at  time.Time
	res []rawStation
	lic string
}

// ponytail: in-memory cache keyed by profile + rounded position, 5 min (Tankerkönig asks not to poll
// faster); grows only with distinct places looked up, fine for a self-hosted app
func cacheKey(p db.PriceProfile, extra string, lat, lng, rad float64) string {
	return fmt.Sprintf("%s|%s|%s|%s|%.3f|%.3f|%.0f", p.ID, p.Kind, p.URL, extra, lat, lng, rad)
}

func clearStationCache() {
	stationCacheMu.Lock()
	stationCache = map[string]cachedStations{}
	stationCacheMu.Unlock()
}

// PriceProfiles: the stored list with keys. A v1.5 single provider becomes the first profile.
func PriceProfiles(s *db.Setting) []db.PriceProfile {
	var list []db.PriceProfile
	if s.FuelPriceProfiles != "" {
		json.Unmarshal([]byte(s.FuelPriceProfiles), &list)
		return list
	}
	switch s.FuelPriceProvider {
	case "tankerkoenig":
		return []db.PriceProfile{{ID: "tankerkoenig", Name: "Germany – Tankerkönig", Kind: "tankerkoenig", URL: s.FuelPriceURL, APIKey: s.FuelPriceAPIKey}}
	case "custom":
		return []db.PriceProfile{{ID: "custom", Name: "Custom", Kind: "custom", URL: s.FuelPriceURL, APIKey: s.FuelPriceAPIKey}}
	}
	return nil
}

// public: keys replaced by keySet
func public(list []db.PriceProfile) []db.PriceProfile {
	out := []db.PriceProfile{}
	for _, p := range list {
		p.KeySet, p.APIKey = p.APIKey != "", ""
		out = append(out, p)
	}
	return out
}

// PublicPriceProfiles: the site list for the browser, keys replaced by keySet; users only see the
// shared ones, admins all
func PublicPriceProfiles(s *db.Setting, isAdmin bool) []db.PriceProfile {
	var list []db.PriceProfile
	for _, p := range PriceProfiles(s) {
		if isAdmin || !p.Private {
			list = append(list, p)
		}
	}
	return public(list)
}

func getUser(userID string) *db.User {
	var u db.User
	if userID == "" || db.DB.Where("id = ?", userID).Limit(1).Find(&u).Error != nil || u.ID == "" {
		return nil
	}
	return &u
}

func userProfiles(u *db.User) []db.PriceProfile {
	var list []db.PriceProfile
	if u != nil && u.FuelPriceProfiles != "" {
		json.Unmarshal([]byte(u.FuelPriceProfiles), &list)
	}
	for i := range list {
		list[i].Own = true
	}
	return list
}

// AvailablePriceProfiles (keys included), in the order they're asked: the user's own first, then the
// site's shared ones (all for admins)
func AvailablePriceProfiles(userID string) []db.PriceProfile {
	u := getUser(userID)
	list := userProfiles(u)
	for _, p := range PriceProfiles(db.GetOrCreateSetting()) {
		if !p.Private || (u != nil && u.Role == db.ADMIN) {
			list = append(list, p)
		}
	}
	// the free sources (no key) work without setup: added last, unless a profile of that kind exists
	have := map[string]bool{}
	for _, p := range list {
		have[p.Kind] = true
	}
	for _, kind := range builtinKinds {
		if BuiltinSources && !have[kind] {
			list = append(list, db.PriceProfile{ID: "builtin-" + kind, Name: kindLabel[kind], Kind: kind, Builtin: true})
		}
	}
	return list
}

// builtinKinds: sources without a key, always available (only asked in their country when the country
// is known; Portugal's whole-country list is only loaded inside Portugal anyway)
var builtinKinds = []string{"econtrol", "goriva", "mzoe", "france", "portugal"}

// BuiltinSources: off in tests (no real services)
var BuiltinSources = true

func PublicAvailableProfiles(userID string) []db.PriceProfile {
	return public(AvailablePriceProfiles(userID))
}

// UserPriceProfiles: the user's own list for their settings page
func UserPriceProfiles(userID string) []db.PriceProfile { return public(userProfiles(getUser(userID))) }

var priceKinds = map[string]string{"tankerkoenig": TankerkoenigURL, "econtrol": EControlURL, "goriva": GorivaURL, "mzoe": MzoeURL,
	"france": PrixCarburantsURL, "portugal": DgegURL, "custom": ""}

// normalizeProfiles checks a list; keys: "" keeps the old one of the same id, "-" removes it.
// freshIDs (users' own lists): an id the list didn't have gets a new one, so a user's profile never
// shares an id with a site profile.
func normalizeProfiles(in, oldList []db.PriceProfile, freshIDs bool) ([]db.PriceProfile, error) {
	old := map[string]db.PriceProfile{}
	for _, p := range oldList {
		old[p.ID] = p
	}
	out := []db.PriceProfile{}
	seen := map[string]bool{}
	for _, p := range in {
		p.Name, p.URL = strings.TrimSpace(p.Name), strings.TrimSpace(p.URL)
		if _, ok := priceKinds[p.Kind]; !ok {
			return nil, fmt.Errorf("unknown profile kind %q", p.Kind)
		}
		if p.Name == "" || len([]rune(p.Name)) > 40 {
			return nil, errors.New("every profile needs a name (up to 40 characters)")
		}
		if p.Kind == "custom" && p.URL == "" {
			return nil, fmt.Errorf("profile %q needs a URL", p.Name)
		}
		if p.URL != "" {
			if u, err := url.Parse(p.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(p.URL) > 300 {
				return nil, fmt.Errorf("profile %q: invalid URL", p.Name)
			}
		}
		if len(p.APIKey) > 200 {
			return nil, fmt.Errorf("profile %q: key too long", p.Name)
		}
		if _, known := old[p.ID]; p.ID == "" || (freshIDs && !known) || seen[p.ID] {
			p.ID = uuid.NewString()
		}
		seen[p.ID] = true
		switch p.APIKey {
		case "":
			p.APIKey = old[p.ID].APIKey
		case "-":
			p.APIKey = ""
		}
		p.KeySet, p.Own = false, false
		out = append(out, p)
	}
	if len(out) > 10 {
		return nil, errors.New("at most 10 profiles")
	}
	return out, nil
}

// UpdatePriceProfiles (admin) replaces the site list
func UpdatePriceProfiles(in []db.PriceProfile) error {
	s := db.GetOrCreateSetting()
	out, err := normalizeProfiles(in, PriceProfiles(s), false)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(out)
	s.FuelPriceProfiles = string(b) // "[]" when empty, not "": that would bring back a v1.5 provider
	s.FuelPriceProvider, s.FuelPriceURL, s.FuelPriceAPIKey = "", "", ""
	clearStationCache()
	return db.UpdateSettings(s)
}

// UpdateUserPriceProfiles replaces the user's own list
func UpdateUserPriceProfiles(userID string, in []db.PriceProfile) error {
	u := getUser(userID)
	if u == nil {
		return errors.New("user not found")
	}
	for i := range in {
		in[i].Private = false
	}
	out, err := normalizeProfiles(in, userProfiles(u), true)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(out)
	clearStationCache()
	return db.DB.Model(u).Update("fuel_price_profiles", string(b)).Error
}

// NearbyStations: with profileID that profile, else the first of the user's available profiles with
// stations nearby.
func NearbyStations(userID string, vehicle *db.Vehicle, lat, lng, radiusKm float64, profileID string) (*StationsResult, error) {
	profiles := AvailablePriceProfiles(userID)
	if profileID != "" {
		var only []db.PriceProfile
		for _, p := range profiles {
			if p.ID == profileID {
				only = append(only, p)
			}
		}
		if len(only) == 0 {
			return nil, errors.New("unknown price profile")
		}
		profiles = only
	}
	if len(profiles) == 0 {
		return nil, errors.New("no station prices here (Germany needs a free Tankerkönig key: Settings → My fuel price profiles)")
	}
	grades := VehicleFuelGrades(vehicle)
	if len(grades) == 0 {
		return nil, errors.New("no station prices for this vehicle's fuel type")
	}
	if math.IsNaN(lat) || lat < -90 || lat > 90 || math.IsNaN(lng) || lng < -180 || lng > 180 {
		return nil, errors.New("invalid position")
	}
	radiusKm = math.Min(math.Max(radiusKm, 1), 25) // Tankerkönig: max 25 km

	// no profile chosen: only the profiles of the country the position is in (own country's first, then
	// "custom" ones), so in Austria it never answers "nothing in Germany"
	country := ""
	if profileID == "" {
		if code, name := countryAt(lat, lng); code != "" {
			country = name
			var mine, custom []db.PriceProfile
			for _, p := range profiles {
				switch kindCountry[p.Kind] {
				case code:
					mine = append(mine, p)
				case "":
					custom = append(custom, p)
				}
			}
			if len(mine)+len(custom) == 0 {
				for kind, cc := range kindCountry {
					if cc == code {
						return nil, fmt.Errorf("you are in %s, but no price profile for it is set up: add “%s” in Settings → My fuel price profiles", name, kindLabel[kind])
					}
				}
				return nil, fmt.Errorf("you are in %s: hammond has no station prices for this country", name)
			}
			profiles = append(mine, custom...)
		}
	}

	var errs, asked []string
	var empty *StationsResult
	for _, p := range profiles {
		res, err := profileStations(p, grades, lat, lng, radiusKm)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %s", p.Name, err))
			continue
		}
		res.Country = country
		if len(res.Stations) > 0 {
			return res, nil
		}
		asked = append(asked, p.Name)
		if empty == nil {
			empty = res
		}
	}
	if empty == nil {
		if country != "" {
			return nil, fmt.Errorf("%s: %s", country, strings.Join(errs, "; "))
		}
		return nil, errors.New(strings.Join(errs, "; "))
	}
	// answered, but nothing here: name every profile asked, not just the first (that read as "it only
	// asks Germany" when in Austria), and say which ones failed
	empty.ProfileName, empty.Errors = strings.Join(asked, ", "), errs
	return empty, nil
}

func profileStations(p db.PriceProfile, grades []string, lat, lng, rad float64) (*StationsResult, error) {
	base := p.URL
	if base == "" {
		base = priceKinds[p.Kind]
	}
	base = strings.TrimRight(base, "/")
	extra := ""
	if p.Kind != "tankerkoenig" && p.Kind != "custom" {
		extra = strings.Join(grades, ",") // prices are stored under the vehicle's grades (95 → first petrol one)
	}
	key := cacheKey(p, extra, lat, lng, rad)
	stationCacheMu.Lock()
	c, ok := stationCache[key]
	stationCacheMu.Unlock()
	if !ok || time.Since(c.at) > 5*time.Minute {
		var err error
		switch p.Kind {
		case "econtrol":
			c.res, err = fetchEControl(base, grades, lat, lng, rad)
			c.lic = "Prices: E-Control Spritpreisrechner (only the 5 cheapest stations show a price)"
		case "goriva":
			c.res, err = fetchGoriva(base, grades, lat, lng, rad)
			c.lic = "Prices: goriva.si"
		case "mzoe":
			c.res, err = fetchMzoe(base, grades, lat, lng, rad)
			c.lic = "Prices: Ministarstvo gospodarstva (mzoe-gor.hr)"
		case "france":
			c.res, err = fetchFrance(base, grades, lat, lng, rad)
			c.lic = "Prices: prix-carburants.gouv.fr (Licence Ouverte)"
		case "portugal":
			c.res, err = fetchPortugal(base, grades, lat, lng, rad)
			c.lic = "Prices: DGEG (precoscombustiveis.dgeg.gov.pt)"
		default:
			c.res, c.lic, err = fetchTankerkoenig(base, p.APIKey, lat, lng, rad)
			if p.Kind == "tankerkoenig" {
				c.lic = "Prices: MTS-K via Tankerkönig (CC BY 4.0)"
			}
		}
		if err != nil {
			return nil, err
		}
		c.at = time.Now()
		stationCacheMu.Lock()
		stationCache[key] = c
		stationCacheMu.Unlock()
	}

	res := &StationsResult{Profile: p.ID, ProfileName: p.Name, Grades: grades, Attribution: c.lic, Stations: []Station{}}
	for _, st := range c.res {
		prices := map[string]float64{}
		var price *float64
		chosen := ""
		for _, g := range grades {
			if v, ok := st.Prices[g]; ok {
				prices[g] = v
				if price == nil {
					v := v
					price, chosen = &v, g
				}
			}
		}
		res.Stations = append(res.Stations, Station{ID: st.ID, Name: st.Name, Brand: st.Brand, Address: st.Address,
			Dist: st.Dist, Lat: st.Lat, Lng: st.Lng, Prices: prices, Price: price, Grade: chosen, IsOpen: st.IsOpen})
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

type tkStation struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Brand       string          `json:"brand"`
	Street      string          `json:"street"`
	HouseNumber string          `json:"houseNumber"`
	PostCode    json.RawMessage `json:"postCode"`
	Place       string          `json:"place"`
	Dist        float64         `json:"dist"`
	Lat         float64         `json:"lat"`
	Lng         float64         `json:"lng"`
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

func fetchTankerkoenig(base, apiKey string, lat, lng, rad float64) ([]rawStation, string, error) {
	q := url.Values{"lat": {fmt.Sprint(lat)}, "lng": {fmt.Sprint(lng)}, "rad": {fmt.Sprint(rad)},
		"sort": {"dist"}, "type": {"all"}, "apikey": {apiKey}}
	resp, err := priceClient.Get(base + "/list.php?" + q.Encode())
	if err != nil {
		msg := err.Error()
		if apiKey != "" {
			msg = strings.ReplaceAll(msg, apiKey, "***")
		}
		return nil, "", fmt.Errorf("price service not reachable: %s", msg)
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
		return nil, "", fmt.Errorf("price service answered HTTP %d without valid data", resp.StatusCode)
	}
	if !body.OK {
		return nil, "", fmt.Errorf("price service: %s", body.Message)
	}
	out := []rawStation{}
	for _, st := range body.Stations {
		prices := map[string]float64{}
		for g, r := range map[string]json.RawMessage{"e5": st.E5, "e10": st.E10, "diesel": st.Diesel} {
			if v := rawPrice(r); v != nil {
				prices[g] = *v
			}
		}
		addr := strings.TrimSpace(st.Street + " " + st.HouseNumber)
		if st.Place != "" {
			addr = strings.Trim(addr+", "+strings.TrimSpace(strings.Trim(string(st.PostCode), `"`)+" "+st.Place), ", ")
		}
		out = append(out, rawStation{ID: st.ID, Name: strings.TrimSpace(st.Name), Brand: strings.TrimSpace(st.Brand),
			Address: addr, Dist: st.Dist, Lat: st.Lat, Lng: st.Lng, Prices: prices, IsOpen: st.IsOpen})
	}
	lic := strings.Trim(strings.TrimSpace(strings.Join([]string{body.Data, body.License}, " · ")), " ·")
	return out, lic, nil
}

// E-Control knows "SUP" (Super 95) and "DIE" per request. ponytail: Super 95 goes to the first petrol
// grade the vehicle accepts (Austria sells one 95 grade); Super Plus isn't asked for.
func eControlTypes(grades []string) []string {
	var out []string
	for _, t := range []string{"SUP", "DIE"} {
		if eControlGrade(t, grades) != "" {
			out = append(out, t)
		}
	}
	return out
}

func eControlGrade(fuelType string, grades []string) string {
	for _, g := range grades {
		if (fuelType == "DIE") == (g == "diesel") {
			return g
		}
	}
	return ""
}

func fetchEControl(base string, grades []string, lat, lng, rad float64) ([]rawStation, error) {
	byID := map[string]*rawStation{}
	var order []string
	for _, t := range eControlTypes(grades) {
		q := url.Values{"latitude": {fmt.Sprint(lat)}, "longitude": {fmt.Sprint(lng)}, "fuelType": {t}, "includeClosed": {"true"}}
		resp, err := priceClient.Get(base + "/search/gas-stations/by-address?" + q.Encode())
		if err != nil {
			return nil, fmt.Errorf("price service not reachable: %s", err)
		}
		var list []struct {
			ID       int    `json:"id"`
			Name     string `json:"name"`
			Location struct {
				Address    string  `json:"address"`
				PostalCode string  `json:"postalCode"`
				City       string  `json:"city"`
				Latitude   float64 `json:"latitude"`
				Longitude  float64 `json:"longitude"`
			} `json:"location"`
			Open     bool    `json:"open"`
			Distance float64 `json:"distance"`
			Prices   []struct {
				FuelType string  `json:"fuelType"`
				Amount   float64 `json:"amount"`
			} `json:"prices"`
		}
		err = json.NewDecoder(resp.Body).Decode(&list)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("price service answered HTTP %d without valid data", resp.StatusCode)
		}
		for _, st := range list {
			if st.Distance > rad { // it answers the 10 nearest, however far
				continue
			}
			id := strconv.Itoa(st.ID)
			r, ok := byID[id]
			if !ok {
				addr := strings.Trim(st.Location.Address+", "+strings.TrimSpace(st.Location.PostalCode+" "+st.Location.City), ", ")
				r = &rawStation{ID: id, Name: strings.TrimSpace(st.Name), Address: addr, Dist: st.Distance,
					Lat: st.Location.Latitude, Lng: st.Location.Longitude, Prices: map[string]float64{}, IsOpen: st.Open}
				byID[id] = r
				order = append(order, id)
			}
			for _, p := range st.Prices {
				if g := eControlGrade(p.FuelType, grades); g != "" && p.FuelType == t && p.Amount > 0 {
					r.Prices[g] = p.Amount
				}
			}
		}
	}
	out := []rawStation{}
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out, nil
}

// goriva.si (Slovenia): the 25 nearest stations within the radius (metres), nearest first; "95" and
// "dizel" map like E-Control's SUP/DIE. ponytail: opening hours are free text there, so every station
// counts as open.
func fetchGoriva(base string, grades []string, lat, lng, rad float64) ([]rawStation, error) {
	q := url.Values{"position": {fmt.Sprintf("%f,%f", lat, lng)}, "radius": {fmt.Sprint(int(rad * 1000))}}
	resp, err := priceClient.Get(base + "/search/?" + q.Encode())
	if err != nil {
		return nil, fmt.Errorf("price service not reachable: %s", err)
	}
	defer resp.Body.Close()
	var body struct {
		Results []struct {
			PK       int                 `json:"pk"`
			Name     string              `json:"name"`
			Address  string              `json:"address"`
			ZipCode  string              `json:"zip_code"`
			Lat      float64             `json:"lat"`
			Lng      float64             `json:"lng"`
			Distance float64             `json:"distance"` // m
			Prices   map[string]*float64 `json:"prices"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("price service answered HTTP %d without valid data", resp.StatusCode)
	}
	out := []rawStation{}
	for _, st := range body.Results {
		prices := map[string]float64{}
		for t, key := range map[string]string{"SUP": "95", "DIE": "dizel"} {
			if g := eControlGrade(t, grades); g != "" && st.Prices[key] != nil && *st.Prices[key] > 0 {
				prices[g] = *st.Prices[key]
			}
		}
		out = append(out, rawStation{ID: strconv.Itoa(st.PK), Name: strings.TrimSpace(st.Name),
			Address: strings.Trim(strings.TrimSpace(st.Address)+", "+st.ZipCode, ", "), Dist: st.Distance / 1000,
			Lat: st.Lat, Lng: st.Lng, Prices: prices, IsOpen: true})
	}
	return out, nil
}

// mzoe-gor.hr (Croatia): one gzipped JSON of all ~900 stations, filtered by distance here. ponytail:
// downloaded (~330 kB) on every cache miss of a position; cache the whole file if lookups get frequent.
// Eurosuper 95 / Eurodizel (with or without additives) map like E-Control's SUP/DIE, the cheaper one wins.
// Opening hours are ignored (every station counts as open).
func distanceKm(lat1, lng1, lat2, lng2 float64) float64 {
	rad := math.Pi / 180
	a := math.Pow(math.Sin((lat2-lat1)*rad/2), 2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Pow(math.Sin((lng2-lng1)*rad/2), 2)
	return 6371 * 2 * math.Asin(math.Sqrt(a))
}

// webservis.mzoe-gor.hr never answers Go's default post-quantum TLS key share (X25519MLKEM768, a large
// ClientHello); curl and browsers get through. Classic curves only for this host.
var mzoeClient = &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment,
	TLSClientConfig: &tls.Config{CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256}}}}

var mzoeKinds = map[int]string{1: "SUP", 2: "SUP", 7: "DIE", 8: "DIE"} // vrsta_goriva ids

func fetchMzoe(base string, grades []string, lat, lng, rad float64) ([]rawStation, error) {
	resp, err := mzoeClient.Get(base)
	if err != nil {
		return nil, fmt.Errorf("price service not reachable: %s", err)
	}
	defer resp.Body.Close()
	var body struct {
		Postajas []struct {
			ID      int    `json:"id"`
			Naziv   string `json:"naziv"`
			Adresa  string `json:"adresa"`
			Mjesto  string `json:"mjesto"`
			Obv     int    `json:"obveznik_id"`
			Lat     string `json:"lat"`
			Long    string `json:"long"`
			Cjenici []struct {
				Gorivo int     `json:"gorivo_id"`
				Cijena float64 `json:"cijena"`
			} `json:"cjenici"`
		} `json:"postajas"`
		Gorivos []struct {
			ID    int `json:"id"`
			Vrsta int `json:"vrsta_goriva_id"`
		} `json:"gorivos"`
		Obvezniks []struct {
			ID    int    `json:"id"`
			Naziv string `json:"naziv"`
		} `json:"obvezniks"`
	}
	var r io.Reader = resp.Body
	if strings.HasSuffix(base, ".gz") {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("price service answered HTTP %d without valid data", resp.StatusCode)
		}
		r = gz
	}
	if err := json.NewDecoder(r).Decode(&body); err != nil || resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("price service answered HTTP %d without valid data", resp.StatusCode)
	}
	kind := map[int]string{}
	for _, g := range body.Gorivos {
		kind[g.ID] = mzoeKinds[g.Vrsta]
	}
	brand := map[int]string{}
	for _, o := range body.Obvezniks {
		brand[o.ID] = strings.TrimSpace(o.Naziv)
	}
	out := []rawStation{}
	for _, st := range body.Postajas {
		a, err1 := strconv.ParseFloat(strings.TrimSpace(st.Lat), 64)
		b, err2 := strconv.ParseFloat(strings.TrimSpace(st.Long), 64)
		if err1 != nil || err2 != nil {
			continue // a few are empty or in degrees/minutes
		}
		// "lat" and "long" are swapped for most stations; in Croatia latitude (42–47) > longitude (13–20)
		sLat, sLng := math.Max(a, b), math.Min(a, b)
		d := distanceKm(lat, lng, sLat, sLng)
		if d > rad {
			continue
		}
		prices := map[string]float64{}
		for _, c := range st.Cjenici {
			g := eControlGrade(kind[c.Gorivo], grades)
			if kind[c.Gorivo] == "" || g == "" || c.Cijena <= 0 {
				continue
			}
			if old, ok := prices[g]; !ok || c.Cijena < old {
				prices[g] = c.Cijena
			}
		}
		out = append(out, rawStation{ID: strconv.Itoa(st.ID), Name: strings.TrimSpace(st.Naziv), Brand: brand[st.Obv],
			Address: strings.Trim(strings.TrimSpace(st.Adresa)+", "+strings.TrimSpace(st.Mjesto), ", "), Dist: d,
			Lat: sLat, Lng: sLng, Prices: prices, IsOpen: true})
	}
	return out, nil
}

// TestFuelPriceProvider (admin "Test connection"): asks one saved profile (the first without id) with
// its stored key around the given position, or a fixed point in its country.
func TestFuelPriceProvider(userID, profileID string, lat, lng *float64) (*StationsResult, error) {
	profiles := AvailablePriceProfiles(userID)
	if len(profiles) == 0 {
		return nil, errors.New("no profile saved yet")
	}
	p := profiles[0]
	for _, x := range profiles {
		if x.ID == profileID {
			p = x
		}
	}
	la, ln := 50.1109, 8.6821 // Frankfurt am Main
	switch p.Kind {
	case "econtrol":
		la, ln = 48.2082, 16.3738 // Vienna
	case "goriva":
		la, ln = 46.0569, 14.5058 // Ljubljana
	case "mzoe":
		la, ln = 45.8150, 15.9819 // Zagreb
	case "france":
		la, ln = 45.7640, 4.8357 // Lyon
	case "portugal":
		la, ln = 38.7223, -9.1393 // Lisbon
	}
	if lat != nil && lng != nil {
		la, ln = *lat, *lng
	}
	clearStationCache() // always a real request
	probe := &db.Vehicle{FuelGrade: "e10,e5,diesel"}
	return NearbyStations(userID, probe, la, ln, 5, p.ID)
}
