package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fake Fuelo: key "free" (no station_prices) or "pro"; counts requests
func fakeFuelo(t *testing.T, calls *int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if key != "free" && key != "pro" {
			w.WriteHeader(401)
			w.Write([]byte(`{"code":"invalid_api_key"}`))
			return
		}
		switch r.URL.Path {
		case "/v2/usage":
			f, rate := `[]`, "5"
			if key == "pro" {
				f, rate = `["history","station_prices"]`, "60"
			}
			w.Write([]byte(`{"data":{"plan":{"daily_limit":100,"rate_per_minute":` + rate + `,"features":` + f + `}}}`))
		case "/v2/gasstations/nearby":
			if r.URL.Query().Get("fuel") == "" {
				t.Errorf("nearby without a fuel filter also lists chargers: %s", r.URL)
			}
			w.Write([]byte(`{"data":[
			 {"id":1816,"country":"sk","brand":"Slovnaft","name":"Slovnaft Bratislava","city":"Bratislava","address":"Mlynské nivy 1","lat":48.1486,"lon":17.1077,"distance_km":0.3},
			 {"id":7,"country":"sk","brand":"OMV","name":null,"city":"Bratislava","address":null,"lat":48.15,"lon":17.11,"distance_km":0.9}]}`))
		case "/v2/gasstations/1816/prices", "/v2/gasstations/7/prices":
			if key != "pro" {
				w.WriteHeader(403)
				w.Write([]byte(`{"code":"plan_required"}`))
				return
			}
			w.Write([]byte(`{"data":{"gasstation_id":1816,"country":"sk","active":true,"currency":"EUR","prices":[
			 {"fuel":"gasoline","unit":"L","price":1.629},{"fuel":"diesel","unit":"L","price":1.559},{"fuel":"dieselplus","unit":"L","price":1.699},
			 {"fuel":"lpg","unit":"L","price":null}]}}`))
		case "/v2/prices/average":
			price := map[string]string{"gasoline": "1.62", "diesel": "1.58"}[r.URL.Query().Get("fuel")]
			if r.URL.Query().Get("country") != "sk" || price == "" {
				w.WriteHeader(404)
				w.Write([]byte(`{"code":"no_data"}`))
				return
			}
			w.Write([]byte(`{"data":{"country":"sk","fuel":"x","price":` + price + `,"currency":"EUR","unit":"L"}}`))
		default:
			t.Errorf("unexpected %s", r.URL)
		}
	}))
}

func TestFueloFreeAndPro(t *testing.T) {
	calls := 0
	srv := fakeFuelo(t, &calls)
	defer srv.Close()
	fueloKeys = map[string]*fueloKey{}
	grades := []string{"e10", "diesel", "diesel_premium"}

	// free: stations without prices, the country averages instead (usage + nearby + 2 averages)
	f, err := fetchFuelo(srv.URL, "free", grades, 48.1486, 17.1077, 2)
	if err != nil || f.pro || len(f.stations) != 2 || len(f.stations[0].Prices) != 0 || f.averages["e10"] != 1.62 ||
		f.averages["diesel"] != 1.58 || f.country != "sk" || f.stations[1].Name != "OMV" || f.stations[1].Address != "Bratislava" {
		t.Fatalf("free plan: %+v %v", f, err)
	}
	first := calls
	fueloKeys["free"].recent = nil // a minute later
	if _, err := fetchFuelo(srv.URL, "free", grades, 48.1486, 17.1077, 2); err != nil || calls != first+1 {
		t.Fatalf("plan asked once a day, averages cached: only the nearby search again (%d → %d) %v", first, calls, err)
	}
	if _, err := fueloWatchPrices(srv.URL, "free", "1816", []string{"e10"}); err == nil || !strings.Contains(err.Error(), "Pro") {
		t.Fatalf("free keys can't watch: %v", err)
	}

	// pro: per-station prices (nearest 10), premium diesel mapped, null price skipped, no averages
	f, err = fetchFuelo(srv.URL, "pro", grades, 48.1486, 17.1077, 2)
	if err != nil || !f.pro || f.stations[0].Prices["e10"] != 1.629 || f.stations[0].Prices["diesel_premium"] != 1.699 ||
		len(f.stations[0].Prices) != 3 || f.averages != nil || f.currency != "EUR" {
		t.Fatalf("pro plan: %+v %v", f, err)
	}
	if p, err := fueloWatchPrices(srv.URL, "pro", "1816", []string{"diesel", "e10"}); err != nil || p["diesel"] != 1.559 || p["e10"] == 0 {
		t.Fatalf("watch with pro: two grades, one request: %v %v", p, err)
	}

	// the per-minute limit (free: 5): the 6th request in a minute isn't sent
	fueloKeys = map[string]*fueloKey{}
	for i := 0; i < 5; i++ {
		fueloGet(srv.URL, "free", "/v2/usage", nil, &struct{}{})
	}
	before := calls
	if _, err := fueloGet(srv.URL, "free", "/v2/usage", nil, &struct{}{}); err == nil || calls != before || !strings.Contains(err.Error(), "a minute") {
		t.Fatalf("per-minute limit: %v (%d → %d)", err, before, calls)
	}

	// the budget: no request once it's used up
	fueloPro(srv.URL, "pro")
	fueloKeys["pro"].used = 1000
	before = calls
	if _, err := fetchFuelo(srv.URL, "pro", grades, 48.1486, 17.1077, 2); err == nil || calls != before {
		t.Fatalf("budget used up: no request (%d → %d) %v", before, calls, err)
	}
	if _, err := fetchFuelo(srv.URL, "wrong", grades, 48.1, 17.1, 2); err == nil || !strings.Contains(err.Error(), "key") {
		t.Fatalf("a wrong key says so: %v", err)
	}
}
