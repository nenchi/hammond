package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/akhilrex/hammond/db"
	"github.com/akhilrex/hammond/models"
)

// fake Tankerkönig list.php: two stations, one without E10 (false), like the real API
func fakeTankerkoenig(t *testing.T, calls *int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		q := r.URL.Query()
		if r.URL.Path != "/json/list.php" || q.Get("type") != "all" || q.Get("sort") != "dist" {
			t.Errorf("unexpected request %s", r.URL)
		}
		if q.Get("apikey") != "good-key" {
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "message": "apikey nicht angegeben, falsch, oder im falschen Format"})
			return
		}
		w.Write([]byte(`{"ok":true,"license":"CC BY 4.0 -  https:\/\/creativecommons.tankerkoenig.de","data":"MTS-K","status":"ok","stations":[
		  {"id":"a","name":"ARAL Tankstelle","brand":"ARAL","street":"Frankfurter Str.","houseNumber":"12","postCode":63067,"place":"Offenbach","lat":50.1,"lng":8.76,"dist":1.4,"diesel":1.629,"e5":1.799,"e10":1.739,"isOpen":true},
		  {"id":"b","name":"Freie Tankstelle","brand":"","street":"Hafen","houseNumber":"","postCode":63067,"place":"Offenbach","lat":50.1,"lng":8.75,"dist":0.3,"diesel":1.599,"e5":1.769,"e10":false,"isOpen":true},
		  {"id":"c","name":"Shell","brand":"Shell","street":"Main","houseNumber":"1","postCode":63065,"place":"Offenbach","lat":50.1,"lng":8.77,"dist":0.9,"diesel":1.619,"e5":1.789,"e10":1.729,"isOpen":false}]}`))
	}))
}

func TestNearbyStationsTankerkoenigFormat(t *testing.T) {
	setupDB(t)
	_, v := mkUserVehicle(t, "", 43)
	var calls int32
	srv := fakeTankerkoenig(t, &calls)
	defer srv.Close()

	v.FuelType, v.FuelGrade = db.PETROL, ""
	if _, err := NearbyStations(&v, 50.1, 8.76, 2); err == nil || !strings.Contains(err.Error(), "not set up") {
		t.Fatalf("want 'not set up' without a provider, got %v", err)
	}
	if err := UpdateFuelPriceSettings("custom", srv.URL+"/json", "bad-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := NearbyStations(&v, 50.1, 8.76, 2); err == nil || !strings.Contains(err.Error(), "apikey") {
		t.Fatalf("want the provider's message for a wrong key, got %v", err)
	}
	if err := UpdateFuelPriceSettings("custom", srv.URL+"/json", "good-key"); err != nil {
		t.Fatal(err)
	}

	v.FuelGrade = "e10"
	res, err := NearbyStations(&v, 50.1, 8.76, 2)
	if err != nil {
		t.Fatal(err)
	}
	if res.Grade != "e10" || len(res.Stations) != 3 || res.Attribution != "MTS-K · CC BY 4.0 -  https://creativecommons.tankerkoenig.de" {
		t.Fatalf("unexpected result %+v", res)
	}
	// stations selling E10 first (by distance), the one without E10 last
	first, second, last := res.Stations[0], res.Stations[1], res.Stations[2]
	if first.ID != "c" || *first.Price != 1.729 || first.IsOpen || second.ID != "a" || last.ID != "b" || last.Price != nil {
		t.Fatalf("order/prices wrong: %+v %+v %+v", first, second, last)
	}
	if second.Address != "Frankfurter Str. 12, 63067 Offenbach" || second.Brand != "ARAL" {
		t.Fatalf("address/brand: %+v", second)
	}

	// cached: a second lookup at the same place doesn't call the service again
	before := atomic.LoadInt32(&calls)
	v.FuelGrade = "diesel"
	res, _ = NearbyStations(&v, 50.1, 8.76, 2)
	if atomic.LoadInt32(&calls) != before || res.Stations[0].ID != "b" || *res.Stations[0].Price != 1.599 {
		t.Fatalf("want cached diesel prices without a new call: calls %d→%d %+v", before, calls, res.Stations[0])
	}

	// the grade follows the fuel type when the vehicle has none
	v.FuelGrade, v.FuelType = "", db.DIESEL
	if g := VehicleFuelGrade(&v); g != "diesel" {
		t.Fatalf("diesel vehicle → %q", g)
	}
	v.FuelType = db.ELECTRIC
	if _, err := NearbyStations(&v, 50.1, 8.76, 2); err == nil {
		t.Fatal("electric vehicles have no fuel price")
	}
}

func TestFuelPriceKeyNeverSent(t *testing.T) {
	setupDB(t)
	if err := UpdateFuelPriceSettings("tankerkoenig", "", "secret-key-123"); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(GetSettings())
	if strings.Contains(string(b), "secret-key-123") || !strings.Contains(string(b), `"fuelPriceKeySet":true`) {
		t.Fatalf("settings JSON must hide the key but say it's set: %s", b)
	}
	UpdateFuelPriceSettings("tankerkoenig", "", "") // empty = keep
	if db.GetOrCreateSetting().FuelPriceAPIKey != "secret-key-123" {
		t.Fatal("empty apiKey must keep the stored key")
	}
	UpdateFuelPriceSettings("tankerkoenig", "", "-") // "-" = remove
	if db.GetOrCreateSetting().FuelPriceAPIKey != "" {
		t.Fatal(`"-" must remove the key`)
	}
	if err := UpdateFuelPriceSettings("custom", "", ""); err == nil {
		t.Fatal("custom without URL must be refused")
	}
}

func TestDetectionKeepsLocation(t *testing.T) {
	setupDB(t)
	u, v := mkUserVehicle(t, "", 43)
	lat, lng := 50.1, 8.76
	res, err := CreateDetection(u.ID, v.ID, models.CreateDetectionRequest{OdoReading: 81068, FuelQuantity: f32(11), Lat: &lat, Lng: &lng})
	if err != nil || res.Detection.Lat == nil || *res.Detection.Lat != lat || *res.Detection.Lng != lng {
		t.Fatalf("location not stored: %+v %v", res, err)
	}
}
