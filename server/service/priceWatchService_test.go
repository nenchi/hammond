package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/akhilrex/hammond/db"
)

// one alert when the price drops to the limit, none while it stays below, again after it went above
func TestApplyPriceAlertsOncePerDrop(t *testing.T) {
	limit := 1.65
	w := &db.PriceWatch{Below: &limit}
	now := time.Now()
	steps := []struct {
		price          float64
		changed, alert bool
	}{
		{1.70, true, false},
		{1.70, false, false},
		{1.65, true, true}, // at the limit counts
		{1.62, true, false},
		{1.69, true, false},
		{1.60, true, true},
	}
	for i, s := range steps {
		changed, alert := applyPrice(w, s.price, now)
		if changed != s.changed || alert != s.alert {
			t.Fatalf("step %d (%.2f): changed=%v alert=%v, want %v %v", i, s.price, changed, alert, s.changed, s.alert)
		}
	}
	w.Below = nil
	if _, alert := applyPrice(w, 1.0, now); alert || w.Alerted {
		t.Fatal("no limit: never alerts")
	}
}

func TestWatchMinutes(t *testing.T) {
	u := &db.User{PriceWatchIntervals: map[string]int{"goriva": 120, "mzoe": 5}}
	if WatchMinutes(u, "goriva") != 120 || WatchMinutes(u, "mzoe") != 1440 || WatchMinutes(u, "tankerkoenig") != 60 || WatchMinutes(u, "x") != 60 {
		t.Fatal("own interval, out-of-range ignored, defaults")
	}
}

func TestSearchPlaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" || r.URL.Query().Get("q") != "Ljubljana" || r.Header.Get("User-Agent") == "" {
			w.Write([]byte(`[]`))
			return
		}
		w.Write([]byte(`[{"display_name":"Ljubljana, Slovenia","lat":"46.0500","lon":"14.5069"},{"display_name":"broken","lat":"x","lon":"1"}]`))
	}))
	defer srv.Close()
	old := GeocodeURL
	GeocodeURL = srv.URL + "/reverse"
	defer func() { GeocodeURL = old }()
	got, err := SearchPlaces(" Ljubljana ")
	if err != nil || len(got) != 1 || got[0].Lat != 46.05 || got[0].Lng != 14.5069 {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := SearchPlaces("Nowhere"); err == nil {
		t.Fatal("no result is an error")
	}
}

// the graph shows the price before the period, the changes in it, and my fill-ups at that station
func TestPriceWatchHistory(t *testing.T) {
	setupDB(t)
	u, v := mkUserVehicle(t, "", 43)
	w := db.PriceWatch{UserID: u.ID, Name: "ARAL", Address: "Frankfurter Str. 12, 63067 Offenbach", Grade: "e10"}
	db.DB.Create(&w)
	now := time.Now()
	for i, p := range []float64{1.80, 1.75, 1.70} {
		db.DB.Create(&db.StationPrice{WatchID: w.ID, Price: p, At: now.AddDate(0, 0, -10+4*i)})
	}
	for _, st := range []string{"aral, frankfurter str. 12, 63067 offenbach", "Shell"} {
		db.DB.Create(&db.Fillup{VehicleID: v.ID, UserID: u.ID, FillingStation: st, PerUnitPrice: 1.749, Date: now.AddDate(0, 0, -3)})
	}
	h, err := PriceWatchHistory(u.ID, w.ID, now.AddDate(0, 0, -7))
	if err != nil || len(h.Prices) != 3 || h.Prices[0].Price != 1.80 || len(h.Fillups) != 1 || h.Fillups[0].Price != 1.749 {
		t.Fatalf("got %+v %v", h, err)
	}
	if _, err := PriceWatchHistory("someone-else", w.ID, now); err == nil {
		t.Fatal("other users' watches are not found")
	}
}

func TestShortAddress(t *testing.T) {
	for in, want := range map[string]string{
		"ODENWALDRING 84, 63069 OFFENBACH": "Odenwaldring 84, Offenbach",
		"Celovška 150, 1000 Ljubljana":     "Celovška 150, Ljubljana", // mixed case kept, postcode goes
		"HAUPTSTRASSE 1, A-1010 WIEN":      "Hauptstrasse 1, Wien",
		"STRAßE DES 17. JUNI 1":            "Straße Des 17. Juni 1",
		"":                                 "",
	} {
		if got := ShortAddress(in); got != want {
			t.Errorf("ShortAddress(%q) = %q, want %q", in, got, want)
		}
	}
	if MapsDirections(0, 0) != "" || MapsDirections(50.1031, 8.7601) != "https://www.google.com/maps/dir/?api=1&destination=50.103100,8.760100" {
		t.Error("MapsDirections")
	}
}
