package service

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Official open station prices of France, Portugal, Italy and Spain (no keys). France searches by
// radius on the server; the others only offer the whole country, loaded at most every 30 min into a
// compact list, and only when the position is in that country. Spain's firewall resets Go's TLS
// handshakes (curl gets through), so Spain is fetched with curl (in the image).

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
			SP98   *float64 `json:"sp98_prix"`
			GPLc   *float64 `json:"gplc_prix"`
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
		setGrade(prices, grades, "plus", val(st.SP98))
		setGrade(prices, grades, "lpg", val(st.GPLc))
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
	diesel, p95           float64            // p95: a "95" without E5/E10 (Portugal): the first petrol grade
	more                  map[string]float64 // other kinds (PLUS, DPREM, LPG; see eControlGrade) → price
	exact                 map[string]float64 // grade (e5, e10, …) → price, when the source names them (Spain)
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
		for g, v := range st.exact {
			setGrade(prices, grades, g, v)
		}
		for kind, v := range st.more {
			if g := eControlGrade(kind, grades); g != "" && v > 0 {
				prices[g] = v
			}
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

// ---- Italy: MIMIT (Osservaprezzi carburanti), two whole-country CSVs of the day ----

var (
	italyList countryList
	italyBox  = box{35.3, 47.1, 6.6, 18.6} // with Sicily and Sardinia
)

func fetchItaly(base string, grades []string, lat, lng, rad float64) ([]rawStation, error) {
	if !italyBox.near(lat, lng, rad) {
		return []rawStation{}, nil
	}
	list, err := italyList.get(base, loadItaly)
	if err != nil {
		return nil, err
	}
	return nearby(list, grades, lat, lng, rad), nil
}

// italyKind: the kind of an Italian fuel name. Brand premiums (Blue Diesel, Hi-Q Diesel, Gasolio
// speciale…) are premium diesel; Blue Super, HiQ Perform+, Benzina speciale/WR 100 are 98/100.
// ponytail: name matching; methane, LNG and HVO are left out.
func italyKind(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	switch {
	case n == "benzina":
		return "SUP"
	case n == "gasolio":
		return "DIE"
	case n == "gpl":
		return "LPG"
	case strings.Contains(n, "hvo") || strings.Contains(n, "metano") || strings.Contains(n, "gnl") || strings.Contains(n, "gnc"):
		return ""
	case strings.Contains(n, "diesel") || strings.Contains(n, "gasolio"):
		return "DPREM"
	case strings.Contains(n, "benzina") || strings.Contains(n, "super") || strings.Contains(n, "perform"):
		return "PLUS"
	}
	return ""
}

// readPipeCSV: MIMIT's files start with an "Estrazione del …" line, then the header; "|"-separated
func readPipeCSV(c *http.Client, u string) ([][]string, error) {
	resp, err := c.Get(u)
	if err != nil {
		return nil, fmt.Errorf("price service not reachable: %s", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("price service answered HTTP %d", resp.StatusCode)
	}
	r := csv.NewReader(resp.Body)
	r.Comma, r.LazyQuotes, r.FieldsPerRecord = '|', true, -1
	rows, err := r.ReadAll()
	if err != nil || len(rows) < 3 {
		return nil, errors.New("price service answered without valid data")
	}
	return rows[2:], nil
}

func loadItaly(base string) ([]countryStation, error) {
	stations, err := readPipeCSV(bigClient, base+"/anagrafica_impianti_attivi.csv")
	if err != nil {
		return nil, err
	}
	prices, err := readPipeCSV(bigClient, base+"/prezzo_alle_8.csv")
	if err != nil {
		return nil, err
	}
	// idImpianto|Gestore|Bandiera|Tipo Impianto|Nome Impianto|Indirizzo|Comune|Provincia|Latitudine|Longitudine
	byID := map[string]*countryStation{}
	var order []string
	for _, r := range stations {
		if len(r) < 10 {
			continue
		}
		la, err1 := strconv.ParseFloat(strings.TrimSpace(r[8]), 64)
		ln, err2 := strconv.ParseFloat(strings.TrimSpace(r[9]), 64)
		if err1 != nil || err2 != nil || (la == 0 && ln == 0) {
			continue
		}
		id := strings.TrimSpace(r[0])
		addr := strings.Trim(strings.TrimSpace(r[5])+", "+strings.TrimSpace(r[6]+" "+r[7]), ", ")
		byID[id] = &countryStation{id: id, brand: strings.TrimSpace(r[2]), name: strings.TrimSpace(r[4]), addr: addr,
			lat: la, lng: ln, more: map[string]float64{}}
		order = append(order, id)
	}
	// idImpianto|descCarburante|prezzo|isSelf|dtComu: self-service and served prices; the cheaper one wins
	for _, r := range prices {
		if len(r) < 3 {
			continue
		}
		st := byID[strings.TrimSpace(r[0])]
		kind := italyKind(r[1])
		v, err := strconv.ParseFloat(strings.TrimSpace(r[2]), 64)
		if st == nil || kind == "" || err != nil || v <= 0 {
			continue
		}
		if old, ok := st.more[kind]; !ok || v < old {
			st.more[kind] = v
		}
	}
	list := make([]countryStation, 0, len(order))
	for _, id := range order {
		st := byID[id]
		st.p95, st.diesel = st.more["SUP"], st.more["DIE"]
		delete(st.more, "SUP")
		delete(st.more, "DIE")
		list = append(list, *st)
	}
	if len(list) == 0 {
		return nil, errors.New("price service answered an empty station list")
	}
	return list, nil
}

// ---- Spain: Ministerio para la Transición Ecológica, all stations every 30 min ----

var (
	spainList countryList
	spainBox  = box{27.6, 43.8, -18.2, 4.4} // with the Balearic and Canary Islands
)

func fetchSpain(base string, grades []string, lat, lng, rad float64) ([]rawStation, error) {
	if !spainBox.near(lat, lng, rad) {
		return []rawStation{}, nil
	}
	list, err := spainList.get(base, loadSpain)
	if err != nil {
		return nil, err
	}
	return nearby(list, grades, lat, lng, rad), nil
}

// curlGet: the body of a URL fetched with curl. ponytail: the Spanish server resets every TLS handshake
// Go makes (tried its TLS 1.2/1.3, curve and HTTP/1.1 variants), curl's is accepted. Only http(s), no
// redirects; the URL is a checked profile URL (http/https, so it can't be read as a curl option).
func curlGet(u string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "curl", "-sS", "--fail", "--compressed", "--proto", "=http,https",
		"-H", "Accept: application/json", "-A", "hammond (https://github.com/nenchi/hammond)", u).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("price service not reachable: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("price service not reachable: %s", err)
	}
	return out, nil
}

func loadSpain(base string) ([]countryStation, error) {
	body, err := curlGet(base + "/EstacionesTerrestres/")
	if err != nil {
		return nil, err
	}
	var data struct {
		Result string              `json:"ResultadoConsulta"`
		List   []map[string]string `json:"ListaEESSPrecio"`
	}
	if err := json.Unmarshal(body, &data); err != nil || data.Result != "OK" {
		return nil, errors.New("price service answered without valid data")
	}
	cheaper := func(a, b float64) float64 {
		if a == 0 || (b > 0 && b < a) {
			return b
		}
		return a
	}
	list := make([]countryStation, 0, len(data.List))
	for _, r := range data.List {
		la, ln := commaFloat(r["Latitud"]), commaFloat(r["Longitud (WGS84)"])
		if (la == 0 && ln == 0) || r["Tipo Venta"] == "R" { // R: only for members (cooperatives)
			continue
		}
		addr := strings.Trim(strings.TrimSpace(r["Dirección"])+", "+strings.TrimSpace(r["C.P."]+" "+r["Localidad"]), ", ")
		list = append(list, countryStation{id: r["IDEESS"], brand: strings.TrimSpace(r["Rótulo"]), addr: addr, lat: la, lng: ln,
			exact: map[string]float64{"e5": commaFloat(r["Precio Gasolina 95 E5"]), "e10": commaFloat(r["Precio Gasolina 95 E10"]),
				"diesel": commaFloat(r["Precio Gasoleo A"]), "diesel_premium": commaFloat(r["Precio Gasoleo Premium"]),
				"plus": cheaper(commaFloat(r["Precio Gasolina 98 E5"]), commaFloat(r["Precio Gasolina 98 E10"])),
				"lpg":  commaFloat(r["Precio Gases licuados del petróleo"])}})
	}
	if len(list) == 0 {
		return nil, errors.New("price service answered an empty station list")
	}
	return list, nil
}
