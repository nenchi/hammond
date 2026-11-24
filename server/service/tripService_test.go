package service

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/akhilrex/hammond/db"
)

func TestLegsFromHistory(t *testing.T) {
	t0 := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	m := func(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }
	p := func(min int, s string) haPoint { return haPoint{m(min), s} }
	e := db.TripEntities{Ignition: "sensor.ign", FuelLevel: "sensor.fuel", Distance: "sensor.dist", Consumption: "sensor.cons"}
	h := map[string][]haPoint{
		// driving already at the start (skipped), then 8:30–9:00, 9:10–9:40 (fuel stop between), 12:00– (still on)
		"sensor.ign":  {p(0, "4"), p(10, "1"), p(30, "4"), p(60, "1"), p(70, "5"), p(100, "0"), p(240, "4")},
		"sensor.odo":  {p(0, "1000"), p(62, "1040"), p(102, "1075")},
		"sensor.fuel": {p(0, "60"), p(61, "50"), p(101, "42")},
		// trip computer: reset at the first start, then counting on across the fuel stop (like a Mercedes)
		"sensor.dist": {p(0, "3"), p(31, "0"), p(61, "40.4"), p(101, "75.6")},
		"sensor.cons": {p(61, "7.5"), p(101, "x"), p(102, "6")},
	}
	legs, pending := legsFromHistory(h, e, "sensor.odo", linear(50), m(300))
	if len(legs) != 2 || !pending.Equal(m(240)) { // the drive since 12:00 is still running
		t.Fatalf("legs: %+v pending %v", legs, pending)
	}
	a, b := legs[0], legs[1]
	if !a.Start.Equal(m(30)) || !a.End.Equal(m(60)) || a.StartOdo != 1000 || a.EndOdo != 1040 || a.Distance != 40.4 ||
		*a.FuelStart != 60 || *a.FuelEnd != 50 || math.Abs(*a.FuelUsed-3.03) > 1e-9 || !a.FuelFromCar {
		t.Errorf("leg 1: %+v used %v", a, *a.FuelUsed)
	}
	// the end values are read until the next start (9:10), not 15 min after the stop; distance and fuel are
	// the growth of the running counters: 75.6 − 40.4 km, 6 l/100 × 75.6 − 7.5 l/100 × 40.4
	if b.StartOdo != 1040 || b.EndOdo != 1075 || math.Abs(b.Distance-35.2) > 1e-9 || math.Abs(*b.FuelUsed-1.506) > 1e-9 {
		t.Errorf("leg 2: %+v used %v", b, *b.FuelUsed)
	}
	// without trip computer: odometer distance, fuel from the level × tank
	legs, _ = legsFromHistory(h, db.TripEntities{Ignition: "sensor.ign", FuelLevel: "sensor.fuel"}, "sensor.odo", linear(50), m(300))
	if legs[0].Distance != 40 || *legs[0].FuelUsed != 5 || legs[0].FuelFromCar { // from the gauge: not for calibration
		t.Errorf("no trip computer: %+v", legs[0])
	}
	// the last stop isn't settled yet: no leg until 15 min after it
	if l, p := legsFromHistory(h, e, "sensor.odo", linear(50), m(110)); len(l) != 1 || !p.Equal(m(70)) {
		t.Errorf("unsettled: %d legs, pending %v", len(l), p)
	}
}

func TestFifoCosts(t *testing.T) {
	d := func(day int) time.Time { return time.Date(2026, 11, day, 12, 0, 0, 0, time.UTC) }
	f := func(day int, qty, price float32, full bool) db.Fillup {
		return db.Fillup{Date: d(day), FuelQuantity: qty, PerUnitPrice: price, IsTankFull: &full}
	}
	used := func(l float64) *float64 { return &l }
	pct := 50.0
	fills := []db.Fillup{f(1, 30, 1.50, false), f(2, 10, 1.60, false), f(5, 20, 2.00, false)}
	legs := []db.TripLeg{
		{Base: db.Base{ID: "a"}, Start: d(3), FuelStart: &pct, FuelUsed: used(15)},
		{Base: db.Base{ID: "b"}, Start: d(4), FuelUsed: used(15)},
		{Base: db.Base{ID: "c"}, Start: d(6), FuelUsed: used(30)},
	}
	// tank 40 l at 50% = 20 l: the newest fill-ups before = 10 l @1.60 + 10 l @1.50
	c := fifoCosts(fills, legs, linear(40))
	// a: oldest first = 10 @1.50, then 5 @1.60; b: the 5 @1.60 left, then nothing → the last price (1.60);
	// c: the fill-up of day 5 (20 l @2.00), then nothing → 2.00
	want := map[string]float64{"a": 10*1.50 + 5*1.60, "b": 5*1.60 + 10*1.60, "c": 20*2.00 + 10*2.00}
	for id, w := range want {
		if math.Abs(c[id]-w) > 1e-6 {
			t.Errorf("%s: %.3f, want %.3f", id, c[id], w)
		}
	}
	// a full tank sets the content to the tank size: old cheap fuel beyond it is dropped
	fills = []db.Fillup{f(1, 40, 1.00, true), f(2, 40, 2.00, true)}
	legs = []db.TripLeg{{Base: db.Base{ID: "x"}, Start: d(1).Add(time.Hour), FuelUsed: used(5)}, {Base: db.Base{ID: "y"}, Start: d(3), FuelUsed: used(10)}}
	c = fifoCosts(fills, legs, linear(40))
	if c["x"] != 5 || c["y"] != 20 {
		t.Errorf("full tank: %v", c)
	}
}

func TestTripsCSV(t *testing.T) {
	var b struct{ s []byte }
	w := writerFunc(func(p []byte) (int, error) { b.s = append(b.s, p...); return len(p), nil })
	tr := TripView{Trip: db.Trip{Business: true, Purpose: "Customer, Frankfurt"}, Start: time.Date(2026, 11, 20, 8, 0, 0, 0, time.Local),
		End: time.Date(2026, 11, 20, 9, 40, 0, 0, time.Local), StartOdo: 1000, EndOdo: 1075, Distance: 75.6, DriveMins: 60,
		FuelUsed: 5.1, FuelCost: 8.5, ExtraCost: 2, Costs: []db.Expense{{ExpenseType: "Parking", Amount: 2}},
		From: "Neusalzer Straße 77, 63069 Offenbach am Main", To: "Mainzer Landstraße 1, 60329 Frankfurt am Main"}
	if err := TripsCSV(w, []TripView{tr}, "EUR"); err != nil {
		t.Fatal(err)
	}
	want := "2026-11-20,08:00,09:40,\"Neusalzer Straße 77, 63069 Offenbach am Main\",\"Mainzer Landstraße 1, 60329 Frankfurt am Main\",60,1000,1075,75.6,yes,\"Customer, Frankfurt\",,5.10,8.50,2.00,Parking 2.00,10.50\n"
	if got := string(b.s); len(got) < len(want) || got[len(got)-len(want):] != want {
		t.Errorf("csv:\n%s", strconv.Quote(got))
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// linear: a gauge without learned samples (tank / 100 litres per %)
func linear(tank float64) GaugeCalibration {
	return GaugeCalibration{Segments: 1, LitresPerPercent: []float64{tank / 100}, TankCapacity: tank}
}

func TestTripsUseLearnedGauge(t *testing.T) {
	// top quarter holds more (gauge reads "full" long): 75–100 % = 0.64 l/%, the rest 0.36 l/%
	g := GaugeCalibration{Segments: 4, LitresPerPercent: []float64{0.36, 0.36, 0.36, 0.64}, TankCapacity: 43}
	if l := g.Litres(81, 100); math.Abs(l-12.16) > 1e-9 {
		t.Errorf("81→100 %%: %.2f l", l)
	}
	t0 := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	p := func(min int, s string) haPoint { return haPoint{t0.Add(time.Duration(min) * time.Minute), s} }
	h := map[string][]haPoint{"i": {p(0, "0"), p(10, "4"), p(40, "0")}, "o": {p(0, "100"), p(41, "130")}, "f": {p(0, "90"), p(41, "70")}}
	legs, _ := legsFromHistory(h, db.TripEntities{Ignition: "i", FuelLevel: "f"}, "o", g, t0.Add(2*time.Hour))
	// 90→75 % = 15 × 0.64, 75→70 % = 5 × 0.36
	if len(legs) != 1 || math.Abs(*legs[0].FuelUsed-(15*0.64+5*0.36)) > 1e-9 {
		t.Errorf("legs %+v", legs)
	}
}

// the chat's undo deletes the cost by the ID AddTripCost returns: the new one, not an older cost of the trip
func TestAddTripCostReturnsItsID(t *testing.T) {
	setupDB(t)
	u, v := mkUserVehicle(t, "", 43)
	trip := db.Trip{VehicleID: v.ID}
	db.DB.Create(&trip)
	db.DB.Create(&db.TripLeg{VehicleID: v.ID, TripID: trip.ID, Start: at, End: at.Add(time.Hour), StartOdo: 1000, EndOdo: 1075, Distance: 75})
	first, err := AddTripCost(u.ID, trip.ID, 12.5, "Toll", "A3")
	if err != nil {
		t.Fatal(err)
	}
	second, err := AddTripCost(u.ID, trip.ID, 4.2, "Toll", "A5")
	if err != nil || second == "" || second == first {
		t.Fatalf("want a new ID per cost: %q %q %v", first, second, err)
	}
	var e db.Expense
	if db.DB.First(&e, "id = ?", second); e.Comments != "A5" {
		t.Fatalf("second ID is the A5 toll: %+v", e)
	}
}

// addresses: the car's tracker first, else the phone; a phone that stayed home while the car drove gives none
func TestLocateCarThenPhone(t *testing.T) {
	m := func(min int) time.Time { return at.Add(time.Duration(min) * time.Minute) }
	home, work := posPoint{m(-5), 50.079, 8.77, 15}, posPoint{m(42), 50.11, 8.68, 20}
	mid := posPoint{m(20), 50.095, 8.72, 30}
	leg := func() db.TripLeg { return db.TripLeg{Start: m(0), End: m(40), Distance: 9.5} }

	l := leg()
	locate(&l, nil, []posPoint{home, mid, work})
	if l.LocationFrom != "phone" || l.StartLat == nil || *l.StartLat != 50.079 || *l.EndLat != 50.11 || len(l.Route) != 3 {
		t.Fatalf("phone: %+v", l)
	}
	l = leg()
	carStart := posPoint{m(0), 50.08, 8.771, 0}
	carEnd := posPoint{m(41), 50.111, 8.681, 0}
	locate(&l, []posPoint{carStart, carEnd}, []posPoint{home, mid, work})
	if l.LocationFrom != "car" || *l.StartLat != 50.08 || *l.EndLat != 50.111 || len(l.Route) != 2 {
		t.Fatalf("car first: %+v", l)
	}
	l = leg()
	locate(&l, nil, []posPoint{home, {m(41), 50.0791, 8.7701, 15}}) // phone at home the whole time, car drove 9.5 km
	if l.StartLat != nil || l.EndLat != nil || l.LocationFrom != "" {
		t.Fatalf("phone left at home: want no position, got %+v", l)
	}
	l = leg()
	locate(&l, nil, []posPoint{home, {m(41), 51.5, 7.0, 15}}) // 150 km away after 9.5 km: not with the car
	if l.EndLat != nil {
		t.Fatalf("phone elsewhere: want no position, got %+v", l)
	}
	l = leg()
	locate(&l, nil, []posPoint{{m(-90), 50.079, 8.77, 15}}) // only a position 90 min before
	if l.StartLat != nil {
		t.Fatalf("too old: %+v", l)
	}
}

// addresses from Nominatim: street, postcode, town; a backlog is limited per sync
func TestFillAddresses(t *testing.T) {
	setupDB(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("lat") == "50.11" {
			fmt.Fprint(w, `{"display_name":"Rastanlage Taunusblick, A 5, Hessen, Deutschland","address":{"road":"A 5"}}`)
			return
		}
		fmt.Fprint(w, `{"display_name":"x","address":{"road":"Neusalzer Straße","house_number":"77","postcode":"63069","city":"Offenbach am Main"}}`)
	}))
	defer srv.Close()
	old := GeocodeURL
	GeocodeURL = srv.URL
	defer func() { GeocodeURL = old }()
	_, v := mkUserVehicle(t, "", 43)
	la, ln, la2, ln2 := 50.079, 8.77, 50.11, 8.68
	db.DB.Create(&db.TripLeg{VehicleID: v.ID, Start: at, End: at.Add(time.Hour), StartLat: &la, StartLng: &ln, EndLat: &la2, EndLng: &ln2,
		Route: [][2]float64{{la, ln}, {la2, ln2}}})
	fillAddresses(v.ID, 20)
	var l db.TripLeg
	db.DB.First(&l, "vehicle_id = ?", v.ID)
	if l.StartAddress != "Neusalzer Straße 77, 63069 Offenbach am Main" || l.EndAddress != "Rastanlage Taunusblick, A 5, Hessen" || len(l.Route) != 2 {
		t.Fatalf("addresses: %q / %q route %v", l.StartAddress, l.EndAddress, l.Route)
	}
	fillAddresses(v.ID, 20) // nothing left to look up
	if calls != 2 {
		t.Fatalf("want 2 lookups, got %d", calls)
	}
}

// paused: no sync (also not "Sync now"); back on, recording starts then, the paused days aren't read
func TestTripsPaused(t *testing.T) {
	setupDB(t)
	_, v := mkUserVehicle(t, "", 43)
	db.DB.Model(&v).Updates(map[string]any{"odometer_entity": "sensor.odo"})
	if err := SetTripEntities(v.ID, db.TripEntities{Ignition: "sensor.ign"}); err != nil {
		t.Fatal(err)
	}
	if err := SetTripsPaused(v.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncTrips(v.ID); err == nil || !strings.Contains(err.Error(), "paused") {
		t.Fatalf("paused: want a 'paused' error, got %v", err)
	}
	before := time.Now()
	if err := SetTripsPaused(v.ID, false); err != nil {
		t.Fatal(err)
	}
	v2, _ := db.GetVehicleById(v.ID)
	if v2.TripsPaused || v2.TripsSyncedTo == nil || v2.TripsSyncedTo.Before(before.Add(-time.Second)) {
		t.Fatalf("resumed: want not paused and synced from now, got paused=%v syncedTo=%v", v2.TripsPaused, v2.TripsSyncedTo)
	}
}

// recording days: Monday first, "" = every day; a drive counts by its start
func TestTripDays(t *testing.T) {
	mon := time.Date(2026, 11, 23, 9, 0, 0, 0, time.Local) // a Monday
	sat, sun := mon.AddDate(0, 0, 5), mon.AddDate(0, 0, 6)
	for _, c := range []struct {
		days string
		t    time.Time
		want bool
	}{{"", sun, true}, {"1111100", mon, true}, {"1111100", sat, false}, {"1111100", sun, false},
		{"1111110", sat, true}, {"1111110", sun, false}, {"0000001", sun, true}, {"0000001", mon, false}} {
		if got := tripDayOn(c.days, c.t); got != c.want {
			t.Errorf("%s on %s: want %v", c.days, c.t.Weekday(), c.want)
		}
	}
	setupDB(t)
	_, v := mkUserVehicle(t, "", 43)
	for _, bad := range []string{"11111", "11111x0", "0000000"} {
		if err := SetTripDays(v.ID, bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	if err := SetTripDays(v.ID, "1111111"); err != nil {
		t.Fatal(err)
	}
	if v2, _ := db.GetVehicleById(v.ID); v2.TripDays != "" {
		t.Errorf("every day is stored as \"\", got %q", v2.TripDays)
	}
}

// a trip by hand with From/To: addresses, the road route (fake OSRM), fuel estimated from the fill-ups;
// routing down = a straight line
func TestManualTripRouteAndFuel(t *testing.T) {
	setupDB(t)
	u, v := mkUserVehicle(t, "", 50)
	up := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up {
			http.Error(w, "down", 503)
			return
		}
		fmt.Fprint(w, `{"code":"Ok","routes":[{"distance":7140,"duration":780,"geometry":{"coordinates":[[8.7621,50.1036],[8.73,50.105],[8.6821,50.1109]]}}]}`)
	}))
	defer srv.Close()
	old := RoutingURL
	RoutingURL = srv.URL
	defer func() { RoutingURL = old }()
	// two full tanks 1000 km apart, 65 l: 6.5 l/100 km
	manualFillup(t, u, v, 10000, 40, "")
	manualFillup(t, u, v, 11000, 65, "")
	from, to := &Place{Name: "Kaiserstraße 50, Offenbach", Lat: 50.1036, Lng: 8.7621}, &Place{Name: "Neue Mainzer Straße 52, Frankfurt", Lat: 50.1109, Lng: 8.6821}
	r, err := RouteBetween(from.Lat, from.Lng, to.Lat, to.Lng)
	if err != nil || r.Km != 7.1 || r.Minutes != 13 || len(r.Route) != 3 || r.Route[0] != [2]float64{50.1036, 8.7621} {
		t.Fatalf("route: %+v %v", r, err)
	}
	id, err := AddTrip(u.ID, v.ID, LegInput{Start: at, End: at.Add(15 * time.Minute), StartOdo: 11000, EndOdo: 11008, From: from, To: to}, true, "Customer")
	if err != nil {
		t.Fatal(err)
	}
	var l db.TripLeg
	db.DB.First(&l, "trip_id = ?", id)
	if l.StartAddress != from.Name || l.EndAddress != to.Name || len(l.Route) != 3 || l.FuelUsed == nil || *l.FuelUsed != 0.52 || !l.FuelEstimated {
		t.Fatalf("leg: %+v fuel %v", l, l.FuelUsed)
	}
	up = false
	id, _ = AddTrip(u.ID, v.ID, LegInput{Start: at.Add(time.Hour), End: at.Add(80 * time.Minute), StartOdo: 11008, EndOdo: 11016, From: to, To: from}, false, "")
	var l2 db.TripLeg // a fresh struct: GORM would also filter by the old one's ID
	db.DB.First(&l2, "trip_id = ?", id)
	if len(l2.Route) != 2 || l2.EndAddress != from.Name {
		t.Fatalf("routing down: want a straight line, got %v", l2.Route)
	}
	litres := 3.0 // typed litres win over the estimate
	id, _ = AddTrip(u.ID, v.ID, LegInput{Start: at.Add(2 * time.Hour), End: at.Add(150 * time.Minute), StartOdo: 11016, EndOdo: 11030, FuelUsed: &litres}, false, "")
	var l3 db.TripLeg
	db.DB.First(&l3, "trip_id = ?", id)
	if *l3.FuelUsed != 3 || l3.FuelEstimated {
		t.Fatalf("typed litres: %+v", l3)
	}
}

// a real tank: older fuel at 2.06 / 1.97 / 1.87, then 19.53 l at 1.789 (full). First in, first out burns the
// 1.97 fuel first (1.61 €); the average of the mixed tank is ≈ 1.87 €/l (≈ 1.53 €)
func TestTripCostAverage(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Berlin")
	d := func(day, h int) time.Time { return time.Date(2026, 11, day, h, 0, 0, 0, loc) }
	full, f := true, func(v float64) *float64 { return &v }
	fills := []db.Fillup{{FuelQuantity: 30.25, PerUnitPrice: 2.059, Date: d(3, 18).AddDate(0, -1, 0), IsTankFull: &full},
		{FuelQuantity: 27.27, PerUnitPrice: 1.969, Date: d(30, 20).AddDate(0, -1, 0), IsTankFull: &full},
		{FuelQuantity: 12.14, PerUnitPrice: 1.869, Date: d(7, 18), IsTankFull: &full},
		{FuelQuantity: 19.53, PerUnitPrice: 1.789, Date: d(23, 19), IsTankFull: &full}}
	legs := []db.TripLeg{{Base: db.Base{ID: "a"}, Start: d(22, 12), FuelStart: f(63), FuelUsed: f(0.3)},
		{Base: db.Base{ID: "b"}, Start: d(23, 18), FuelStart: f(63), FuelUsed: f(0.4)},
		{Base: db.Base{ID: "c"}, Start: d(24, 18), FuelStart: f(100), FuelUsed: f(0.82)}}
	g := linear(43)
	fifo := tripCosts(append([]db.Fillup(nil), fills...), append([]db.TripLeg(nil), legs...), g, false)
	avg := tripCosts(append([]db.Fillup(nil), fills...), append([]db.TripLeg(nil), legs...), g, true)
	if math.Abs(fifo["c"]-0.82*1.969) > 0.005 {
		t.Errorf("first in, first out: want %.2f (the 1.969 fuel), got %.3f", 0.82*1.969, fifo["c"])
	}
	if p := avg["c"] / 0.82; p < 1.85 || p > 1.88 {
		t.Errorf("average: want ≈ 1.866 €/l, got %.3f (%.2f €)", p, avg["c"])
	}
	if p := avg["a"] / 0.3; p < 1.91 || p > 1.94 { // before the fill-up: the 1.97 and 1.87 fuel mixed
		t.Errorf("average before the fill-up: want ≈ 1.924 €/l, got %.3f", p)
	}
}
