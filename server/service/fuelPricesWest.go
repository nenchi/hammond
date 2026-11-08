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

// Official open station prices of France and Portugal (no keys). France searches by radius on the
// server; Portugal only offers the whole country, loaded at most every 30 min into a compact list, and
// only when the position is in Portugal. Spain (Ministerio, geoportalgasolineras.es) isn't here: its
// firewall resets Go's TLS handshakes (curl and browsers get through) — a proxy could add it later.

// bigClient: whole-country downloads take longer than the 8 s of priceClient
var bigClient = &http.Client{Timeout: 90 * time.Second}

// commaFloat: "1,649" or "1,769 €" → 1.649 (0 when empty)
func commaFloat(s string) float64 {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "€"))
	f, _ := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
	return f
}

// setGrade stores a price under grade g when the vehicle accepts it (and the price is real)
func setGrade(prices map[string]float64, grades []string, g string, v float64) {
	if v <= 0 {
		return
	}
	for _, x := range grades {
		if x == g {
			prices[g] = v
		}
	}
}

// ---- France: data.economie.gouv.fr (prix-carburants, Licence Ouverte) ----

func fetchFrance(base string, grades []string, lat, lng, rad float64) ([]rawStation, error) {
	pt := fmt.Sprintf("geom'POINT(%f %f)'", lng, lat)
	q := url.Values{"where": {fmt.Sprintf("within_distance(geom, %s, %gkm)", pt, rad)},
		"order_by": {fmt.Sprintf("distance(geom, %s)", pt)}, "limit": {"50"}}
	resp, err := priceClient.Get(base + "/records?" + q.Encode())
	if err != nil {
		return nil, fmt.Errorf("price service not reachable: %s", err)
	}
	defer resp.Body.Close()
	var body struct {
		Results []struct {
			ID      json.Number `json:"id"`
			Adresse string      `json:"adresse"`
			Ville   string      `json:"ville"`
			CP      string      `json:"cp"`
			Geom    struct {
				Lon float64 `json:"lon"`
				Lat float64 `json:"lat"`
			} `json:"geom"`
			Gazole *float64 `json:"gazole_prix"`
			SP95   *float64 `json:"sp95_prix"`
			E10    *float64 `json:"e10_prix"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("price service answered HTTP %d without valid data", resp.StatusCode)
	}
	val := func(p *float64) float64 {
		if p == nil {
			return 0
		}
		return *p
	}
	out := []rawStation{}
	for _, st := range body.Results {
		prices := map[string]float64{}
		setGrade(prices, grades, "e5", val(st.SP95))
		setGrade(prices, grades, "e10", val(st.E10))
		setGrade(prices, grades, "diesel", val(st.Gazole))
		// ponytail: the open data has no brand or opening state; the address is the name
		out = append(out, rawStation{ID: st.ID.String(), Name: strings.TrimSpace(st.Adresse),
			Address: strings.TrimSpace(st.CP + " " + st.Ville), Dist: distanceKm(lat, lng, st.Geom.Lat, st.Geom.Lon),
			Lat: st.Geom.Lat, Lng: st.Geom.Lon, Prices: prices, IsOpen: true})
	}
	return out, nil
}

// ---- whole-country lists (Portugal) ----

type countryStation struct {
	id, brand, name, addr string
	lat, lng              float64
	diesel, p95           float64 // p95: a "95" without E5/E10 (Portugal): the first petrol grade
}

type countryList struct {
	mu   sync.Mutex
	at   time.Time
	base string
	list []countryStation
}

var portugalList countryList

// get returns the cached list, loading it when older than 30 min (one download at a time)
func (c *countryList) get(base string, load func(string) ([]countryStation, error)) ([]countryStation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.list != nil && c.base == base && time.Since(c.at) < 30*time.Minute {
		return c.list, nil
	}
	list, err := load(base)
	if err != nil {
		if c.list != nil && c.base == base { // keep serving the old prices for a while
			return c.list, nil
		}
		return nil, err
	}
	c.list, c.at, c.base = list, time.Now(), base
	return list, nil
}

func nearby(list []countryStation, grades []string, lat, lng, rad float64) []rawStation {
	out := []rawStation{}
	for _, st := range list {
		d := distanceKm(lat, lng, st.lat, st.lng)
		if d > rad {
			continue
		}
		prices := map[string]float64{}
		setGrade(prices, grades, "diesel", st.diesel)
		if g := eControlGrade("SUP", grades); g != "" && st.p95 > 0 && prices[g] == 0 {
			prices[g] = st.p95
		}
		out = append(out, rawStation{ID: st.id, Name: st.name, Brand: st.brand, Address: st.addr, Dist: d,
			Lat: st.lat, Lng: st.lng, Prices: prices, IsOpen: true})
	}
	return out
}

// box: a rough rectangle around a country (with its islands): outside it, a whole-country list isn't
// even downloaded. ponytail: rectangles, not borders; the radius filter does the exact part.
type box struct{ minLat, maxLat, minLng, maxLng float64 }

func (b box) near(lat, lng, radKm float64) bool {
	d := radKm / 111 // ~ degrees; generous at these latitudes
	return lat >= b.minLat-d && lat <= b.maxLat+d && lng >= b.minLng-d*1.5 && lng <= b.maxLng+d*1.5
}

var portugalBox = box{32.4, 42.2, -31.5, -6.1} // mainland, Madeira, Azores

// ---- Portugal: DGEG, one list per fuel type (Gasolina simples 95: 3201, Gasóleo simples: 2101) ----

func fetchPortugal(base string, grades []string, lat, lng, rad float64) ([]rawStation, error) {
	if !portugalBox.near(lat, lng, rad) {
		return []rawStation{}, nil // not in Portugal: nothing downloaded
	}
	list, err := portugalList.get(base, loadPortugal)
	if err != nil {
		return nil, err
	}
	return nearby(list, grades, lat, lng, rad), nil
}

func loadPortugal(base string) ([]countryStation, error) {
	byID := map[int]*countryStation{}
	var order []int
	for _, fuel := range []int{3201, 2101} {
		q := url.Values{"idsTiposComb": {strconv.Itoa(fuel)}, "qtdPorPagina": {"10000"}, "pagina": {"1"}}
		resp, err := bigClient.Get(base + "/PesquisarPostos?" + q.Encode())
		if err != nil {
			return nil, fmt.Errorf("price service not reachable: %s", err)
		}
		var body struct {
			Status    bool `json:"status"`
			Resultado []struct {
				ID        int     `json:"Id"`
				Nome      string  `json:"Nome"`
				Marca     string  `json:"Marca"`
				Preco     string  `json:"Preco"`
				Morada    string  `json:"Morada"`
				Localidad string  `json:"Localidade"`
				CodPostal string  `json:"CodPostal"`
				Lat       float64 `json:"Latitude"`
				Lng       float64 `json:"Longitude"`
			} `json:"resultado"`
		}
		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK || !body.Status {
			return nil, fmt.Errorf("price service answered HTTP %d without valid data", resp.StatusCode)
		}
		for _, r := range body.Resultado {
			st := byID[r.ID]
			if st == nil {
				st = &countryStation{id: strconv.Itoa(r.ID), brand: strings.TrimSpace(r.Marca), name: strings.TrimSpace(r.Nome),
					addr: strings.Trim(strings.TrimSpace(r.Morada)+", "+strings.TrimSpace(r.CodPostal+" "+r.Localidad), ", "),
					lat:  r.Lat, lng: r.Lng}
				byID[r.ID] = st
				order = append(order, r.ID)
			}
			if fuel == 2101 {
				st.diesel = commaFloat(r.Preco)
			} else {
				st.p95 = commaFloat(r.Preco)
			}
		}
	}
	list := make([]countryStation, 0, len(order))
	for _, id := range order {
		list = append(list, *byID[id])
	}
	if len(list) == 0 {
		return nil, errors.New("price service answered an empty station list")
	}
	return list, nil
}
