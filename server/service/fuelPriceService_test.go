package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
	u, v := mkUserVehicle(t, "", 43)
	var calls int32
	srv := fakeTankerkoenig(t, &calls)
	defer srv.Close()

	v.FuelType, v.FuelGrade = db.PETROL, ""
	if _, err := NearbyStations(u.ID, &v, 50.1, 8.76, 2, ""); err == nil || !strings.Contains(err.Error(), "not set up") {
		t.Fatalf("want 'not set up' without a provider, got %v", err)
	}
	custom := func(key string) []db.PriceProfile {
		return []db.PriceProfile{{ID: "c1", Name: "My TK", Kind: "custom", URL: srv.URL + "/json", APIKey: key}}
	}
	if err := UpdatePriceProfiles(custom("bad-key")); err != nil {
		t.Fatal(err)
	}
	if _, err := NearbyStations(u.ID, &v, 50.1, 8.76, 2, ""); err == nil || !strings.Contains(err.Error(), "apikey") || !strings.Contains(err.Error(), "My TK") {
		t.Fatalf("want the provider's message for a wrong key, got %v", err)
	}
	if err := UpdatePriceProfiles(custom("good-key")); err != nil {
		t.Fatal(err)
	}

	v.FuelGrade = "e10"
	res, err := NearbyStations(u.ID, &v, 50.1, 8.76, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Grades) != 1 || res.Grades[0] != "e10" || len(res.Stations) != 3 || res.Profile != "c1" || res.ProfileName != "My TK" || res.Attribution != "MTS-K · CC BY 4.0 -  https://creativecommons.tankerkoenig.de" {
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
	res, _ = NearbyStations(u.ID, &v, 50.1, 8.76, 2, "")
	if atomic.LoadInt32(&calls) != before || res.Stations[0].ID != "b" || *res.Stations[0].Price != 1.599 {
		t.Fatalf("want cached diesel prices without a new call: calls %d→%d %+v", before, calls, res.Stations[0])
	}

	// several accepted grades, preferred first: E10, else E5 — the station without E10 gets its E5 price
	v.FuelGrade = "e10,e5"
	res, _ = NearbyStations(u.ID, &v, 50.1, 8.76, 2, "")
	byID := map[string]Station{}
	for _, st := range res.Stations {
		byID[st.ID] = st
	}
	free := byID["b"]
	if res.Stations[0].ID != "b" {
		t.Fatalf("with E5 accepted the nearest station (E5 only) comes first: %+v", res.Stations[0])
	}
	if free.Grade != "e5" || *free.Price != 1.769 || len(free.Prices) != 1 {
		t.Fatalf("station without E10 should offer its E5 price: %+v", free)
	}
	if a := byID["c"]; a.Grade != "e10" || a.Prices["e5"] != 1.789 || a.Prices["e10"] != 1.729 {
		t.Fatalf("both prices, preferred E10 first: %+v", a)
	}

	// the grade follows the fuel type when the vehicle has none
	v.FuelGrade, v.FuelType = "", db.DIESEL
	if g := VehicleFuelGrades(&v); len(g) != 1 || g[0] != "diesel" {
		t.Fatalf("diesel vehicle → %v", g)
	}
	v.FuelType = db.ELECTRIC
	if _, err := NearbyStations(u.ID, &v, 50.1, 8.76, 2, ""); err == nil {
		t.Fatal("electric vehicles have no fuel price")
	}
}

func TestFuelPriceKeyNeverSent(t *testing.T) {
	setupDB(t)
	tk := []db.PriceProfile{{ID: "tk", Name: "Germany", Kind: "tankerkoenig", APIKey: "secret-key-123"}}
	if err := UpdatePriceProfiles(tk); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(GetSettings(true))
	if strings.Contains(string(b), "secret-key-123") || !strings.Contains(string(b), `"keySet":true`) {
		t.Fatalf("settings JSON must hide the key but say it's set: %s", b)
	}
	tk[0].APIKey = "" // empty = keep
	UpdatePriceProfiles(tk)
	if PriceProfiles(db.GetOrCreateSetting())[0].APIKey != "secret-key-123" {
		t.Fatal("empty apiKey must keep the stored key")
	}
	tk[0].APIKey = "-" // "-" = remove
	UpdatePriceProfiles(tk)
	if PriceProfiles(db.GetOrCreateSetting())[0].APIKey != "" {
		t.Fatal(`"-" must remove the key`)
	}
	for _, bad := range []db.PriceProfile{{Name: "x", Kind: "custom"}, {Name: "", Kind: "econtrol"}, {Name: "x", Kind: "nope"},
		{Name: "x", Kind: "custom", URL: "file:///etc/passwd"}} {
		if err := UpdatePriceProfiles([]db.PriceProfile{bad}); err == nil {
			t.Fatalf("must be refused: %+v", bad)
		}
	}
	// a new profile gets an id; an empty list stays empty (doesn't bring back the old provider)
	UpdatePriceProfiles([]db.PriceProfile{{Name: "AT", Kind: "econtrol"}})
	if p := PriceProfiles(db.GetOrCreateSetting()); len(p) != 1 || p[0].ID == "" {
		t.Fatalf("want an id: %+v", p)
	}
	s := db.GetOrCreateSetting()
	s.FuelPriceProvider = "tankerkoenig"
	db.UpdateSettings(s)
	UpdatePriceProfiles(nil)
	if p := PriceProfiles(db.GetOrCreateSetting()); len(p) != 0 {
		t.Fatalf("emptied list must stay empty: %+v", p)
	}
}

// a v1.5 single provider shows up as the first profile, key kept
func TestOldProviderBecomesProfile(t *testing.T) {
	setupDB(t)
	s := db.GetOrCreateSetting()
	s.FuelPriceProvider, s.FuelPriceAPIKey = "tankerkoenig", "old-key"
	db.UpdateSettings(s)
	p := PriceProfiles(db.GetOrCreateSetting())
	if len(p) != 1 || p[0].Kind != "tankerkoenig" || p[0].APIKey != "old-key" {
		t.Fatalf("want the old provider as a profile: %+v", p)
	}
	p[0].APIKey = "" // saving the list as the browser sends it keeps the key and clears the old fields
	if err := UpdatePriceProfiles(p); err != nil {
		t.Fatal(err)
	}
	s = db.GetOrCreateSetting()
	if s.FuelPriceProvider != "" || PriceProfiles(s)[0].APIKey != "old-key" {
		t.Fatalf("converted badly: %+v", s)
	}
}

// fake E-Control: Super 95 / Diesel per request, 10 nearest however far, prices only for some
func fakeEControl(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/sprit/1.0/search/gas-stations/by-address" || q.Get("latitude") == "" {
			t.Errorf("unexpected request %s", r.URL)
		}
		if q.Get("latitude") != "47.07" { // outside Austria: nothing
			w.Write([]byte(`[]`))
			return
		}
		ft := q.Get("fuelType")
		price := map[string]float64{"SUP": 1.579, "DIE": 1.599}[ft]
		fmt.Fprintf(w, `[{"id":4176,"name":"F. LEITNER","location":{"address":"Kärntner Straße 4","postalCode":"8020","city":"Graz"},
		  "open":true,"distance":1.5,"prices":[{"fuelType":%q,"amount":%v,"label":"x"}]},
		  {"id":7,"name":"Far away","location":{"address":"A","postalCode":"8000","city":"Graz"},"open":true,"distance":9.5,"prices":[]},
		  {"id":8,"name":"No price","location":{"address":"B","postalCode":"8020","city":"Graz"},"open":false,"distance":0.9,"prices":[]}]`, ft, price)
	}))
}

func TestEControlAndAutoPick(t *testing.T) {
	setupDB(t)
	u, v := mkUserVehicle(t, "", 43)
	var calls int32
	tk, ec := fakeTankerkoenig(t, &calls), fakeEControl(t)
	defer tk.Close()
	defer ec.Close()
	// order: Austria first, then the German one (the fake answers stations everywhere)
	if err := UpdatePriceProfiles([]db.PriceProfile{
		{ID: "at", Name: "Austria", Kind: "econtrol", URL: ec.URL + "/sprit/1.0"},
		{ID: "de", Name: "Germany", Kind: "custom", URL: tk.URL + "/json", APIKey: "good-key"}}); err != nil {
		t.Fatal(err)
	}
	v.FuelGrade = "e10,e5,diesel"
	res, err := NearbyStations(u.ID, &v, 47.07, 15.44, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Profile != "at" || len(res.Stations) != 2 || !strings.Contains(res.Attribution, "E-Control") {
		t.Fatalf("in Graz Austria answers, 9.5 km station dropped: %+v", res)
	}
	st := res.Stations[0]
	if st.ID != "4176" || st.Grade != "e10" || st.Prices["e10"] != 1.579 || st.Prices["diesel"] != 1.599 || len(st.Prices) != 2 ||
		st.Address != "Kärntner Straße 4, 8020 Graz" {
		t.Fatalf("Super 95 → first petrol grade, diesel merged: %+v", st)
	}
	if res.Stations[1].ID != "8" || res.Stations[1].Price != nil || res.Stations[1].IsOpen {
		t.Fatalf("station without price last: %+v", res.Stations[1])
	}
	// outside Austria the next profile answers
	res, err = NearbyStations(u.ID, &v, 50.1, 8.76, 2, "")
	if err != nil || res.Profile != "de" || len(res.Stations) != 3 {
		t.Fatalf("want the German profile: %+v %v", res, err)
	}
	// a chosen profile only asks that one
	res, err = NearbyStations(u.ID, &v, 50.1, 8.76, 2, "at")
	if err != nil || res.Profile != "at" || len(res.Stations) != 0 {
		t.Fatalf("chosen Austria in Germany = no stations: %+v %v", res, err)
	}
	if _, err := NearbyStations(u.ID, &v, 50.1, 8.76, 2, "nope"); err == nil {
		t.Fatal("unknown profile must fail")
	}
	// diesel only: one request type
	if got := eControlTypes([]string{"diesel"}); len(got) != 1 || got[0] != "DIE" {
		t.Fatalf("diesel → DIE: %v", got)
	}
}

func TestHomeAssistantOdometer(t *testing.T) {
	setupDB(t)
	u, v := mkUserVehicle(t, "", 43)
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ha-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/states/sensor.a180_odometer":
			w.Write([]byte(`{"entity_id":"sensor.a180_odometer","state":"21345.0","attributes":{"unit_of_measurement":"km"},
			  "last_changed":"2026-11-08T08:00:00Z","last_updated":"2026-11-08T08:00:00Z","last_reported":"2026-11-08T09:30:00Z"}`))
		case "/api/config":
			w.Write([]byte(`{"location_name":"Home","version":"2026.11.1"}`))
		case "/api/states/sensor.offline":
			w.Write([]byte(`{"state":"unavailable","attributes":{},"last_updated":"2026-11-08T08:00:00Z"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ha.Close()

	if o, err := CurrentOdometer(u.ID, &v); err != nil || o.Configured {
		t.Fatalf("not set up = not configured, no error: %+v %v", o, err)
	}
	if r := TestHomeAssistant(u.ID); r.OK {
		t.Fatal("test without settings must fail")
	}
	if _, err := CheckHAEntity(u.ID, "sensor.a180_odometer"); err == nil || !strings.Contains(err.Error(), "Settings → Home Assistant") {
		t.Fatalf("check without HA: %v", err)
	}
	UpdateHomeAssistantSettings(u.ID, ha.URL+"/", "ha-token")
	v.OdometerEntity = "sensor.a180_odometer"
	db.DB.Save(&v)
	if r := TestHomeAssistant(u.ID); !r.OK || r.Name != "Home" || r.Version != "2026.11.1" || !strings.Contains(r.Odometer, "= 21345 km") {
		t.Fatalf("test connection: %+v", r)
	}
	o, err := CurrentOdometer(u.ID, &v)
	if err != nil || !o.Configured || o.Value != 21345 || o.Unit != "km" || o.UpdatedAt.Format(time.RFC3339) != "2026-11-08T09:30:00Z" {
		t.Fatalf("want 21345 km reported 09:30: %+v %v", o, err)
	}
	me, _ := db.GetUserById(u.ID)
	b, _ := json.Marshal(me)
	if strings.Contains(string(b), "ha-token") || !strings.Contains(string(b), `"haTokenSet":true`) {
		t.Fatalf("token must stay hidden: %s", b)
	}
	if o, err := CheckHAEntity(u.ID, "sensor.a180_odometer"); err != nil || o.Value != 21345 || o.Unit != "km" {
		t.Fatalf("check entity: %+v %v", o, err)
	}
	if _, err := CheckHAEntity(u.ID, "sensor.missing"); err == nil || !strings.Contains(err.Error(), "no entity") {
		t.Fatalf("check missing entity: %v", err)
	}
	if _, err := CheckHAEntity(u.ID, "../api"); err == nil {
		t.Fatal("check must refuse non-entity ids")
	}
	v.OdometerEntity = "sensor.offline"
	if _, err := CurrentOdometer(u.ID, &v); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("unavailable must be an error: %v", err)
	}
	v.OdometerEntity = "sensor.missing"
	if _, err := CurrentOdometer(u.ID, &v); err == nil || !strings.Contains(err.Error(), "no entity") {
		t.Fatalf("missing entity: %v", err)
	}
	UpdateHomeAssistantSettings(u.ID, ha.URL, "wrong")
	v.OdometerEntity = "sensor.a180_odometer"
	if _, err := CurrentOdometer(u.ID, &v); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("wrong token: %v", err)
	}
	if r := TestHomeAssistant(u.ID); r.OK || !strings.Contains(r.Error, "401") {
		t.Fatalf("test with a wrong token: %+v", r)
	}
	for _, bad := range []string{"../config", "sensor.x/../../api", "Sensor.X", "sensor"} {
		if ValidEntityID(bad) {
			t.Fatalf("entity %q must be refused", bad)
		}
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

func TestNormalizeGradesAndSubtypes(t *testing.T) {
	if g, err := NormalizeFuelGrades(" E10, e5,e10 "); err != nil || g != "e10,e5" {
		t.Fatalf("got %q %v", g, err)
	}
	if _, err := NormalizeFuelGrades("e10,hbuzg"); err == nil {
		t.Fatal("unknown grade must be refused")
	}
	if got := NormalizeFuelSubtypes(" Q Max 95 \nMaxxMotion 100, q max 95\n\n"); got != "Q Max 95\nMaxxMotion 100" {
		t.Fatalf("got %q", got)
	}
}

// users see their own profiles first, then the shared site ones; private site profiles only admins
func TestPriceProfileSharing(t *testing.T) {
	setupDB(t)
	u, _ := mkUserVehicle(t, "", 43)
	admin := db.User{Email: "admin@example.com", Name: "a", Role: db.ADMIN}
	db.DB.Create(&admin)
	if err := UpdatePriceProfiles([]db.PriceProfile{{Name: "Shared DE", Kind: "tankerkoenig", APIKey: "site-key"},
		{Name: "Admin only", Kind: "econtrol", Private: true}}); err != nil {
		t.Fatal(err)
	}
	if err := UpdateUserPriceProfiles(u.ID, []db.PriceProfile{{Name: "My proxy", Kind: "custom", URL: "http://x.example/json", APIKey: "mine"}}); err != nil {
		t.Fatal(err)
	}
	names := func(ps []db.PriceProfile) (out []string) {
		for _, p := range ps {
			out = append(out, p.Name)
		}
		return
	}
	if got := names(AvailablePriceProfiles(u.ID)); strings.Join(got, ",") != "My proxy,Shared DE" {
		t.Fatalf("user: own first, then shared, no private: %v", got)
	}
	if got := names(AvailablePriceProfiles(admin.ID)); strings.Join(got, ",") != "Shared DE,Admin only" {
		t.Fatalf("admin: all site profiles: %v", got)
	}
	if got := names(PublicPriceProfiles(db.GetOrCreateSetting(), false)); strings.Join(got, ",") != "Shared DE" {
		t.Fatalf("site settings for users: %v", got)
	}
	b, _ := json.Marshal(PublicAvailableProfiles(u.ID))
	if strings.Contains(string(b), "mine") || strings.Contains(string(b), "site-key") || !strings.Contains(string(b), `"own":true`) {
		t.Fatalf("keys hidden, own marked: %s", b)
	}
	// a user can't take over a site profile by sending its id: it becomes a new own profile
	siteID := PriceProfiles(db.GetOrCreateSetting())[0].ID
	UpdateUserPriceProfiles(u.ID, []db.PriceProfile{{ID: siteID, Name: "Copy", Kind: "tankerkoenig"}})
	if own := UserPriceProfiles(u.ID); len(own) != 1 || own[0].ID == siteID || own[0].KeySet {
		t.Fatalf("no id/key takeover: %+v", own)
	}
	if _, err := NearbyStations(u.ID, &db.Vehicle{FuelGrade: "e10"}, 48.2, 16.37, 2, PriceProfiles(db.GetOrCreateSetting())[1].ID); err == nil {
		t.Fatal("a user can't use the admin's private profile")
	}
}

// the v1.6 site-wide Home Assistant becomes the first admin's own
func TestMoveSiteConnectionsToAdmin(t *testing.T) {
	setupDB(t)
	admin := db.User{Email: "admin@example.com", Name: "a", Role: db.ADMIN}
	db.DB.Create(&admin)
	s := db.GetOrCreateSetting()
	s.HAURL, s.HAToken = "http://ha.local:8123", "old-token"
	db.UpdateSettings(s)
	MoveSiteConnectionsToAdmin()
	u, _ := db.GetUserById(admin.ID)
	if u.HAURL != "http://ha.local:8123" || u.HAToken != "old-token" || db.GetOrCreateSetting().HAURL != "" {
		t.Fatalf("moved: %+v", u)
	}
	MoveSiteConnectionsToAdmin() // twice: nothing happens
}
