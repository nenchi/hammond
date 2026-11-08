package service

import (
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/akhilrex/hammond/db"
)

// Slovenia-style: energy with high/low tariff + network blocks by season and day type (values made up)
func sloveniaPlace() db.ChargingPlace {
	winter := []int{11, 12, 1, 2}
	return db.ChargingPlace{Name: "Home", SessionFee: 0, LossPercent: 10, Holidays: "12-25\n2026-04-06",
		Components: []db.TariffComponent{
			{Name: "Energy", Price: 0.10, Rules: []db.TariffRule{{Label: "VT", Days: "work", From: "06:00", To: "22:00", Price: 0.15}}},
			{Name: "Network", Price: 0.01, Rules: []db.TariffRule{
				{Label: "Block 1", Months: winter, Days: "work", From: "06:00", To: "12:00", Price: 0.05},
				{Label: "Block 1", Months: winter, Days: "work", From: "17:00", To: "20:00", Price: 0.05},
				{Label: "Block 3", Months: winter, Days: "work", From: "22:00", To: "06:00", Price: 0.02},
				{Label: "Block 4", Months: winter, Days: "free", From: "22:00", To: "06:00", Price: 0.015},
			}},
		}}
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.0005 }

func TestQuoteChargeTariffRules(t *testing.T) {
	lj, _ := time.LoadLocation("Europe/Ljubljana")
	old := time.Local
	time.Local = lj
	defer func() { time.Local = old }()
	p := sloveniaPlace()
	if err := ValidateChargingPlace(&p); err != nil {
		t.Fatal(err)
	}

	// Tue 2026-11-10 22:00–02:00 (past midnight), 36 kWh into the car, 10 % loss → 40 kWh billed:
	// low tariff 0.10 + block 3 0.02
	start := time.Date(2026, 11, 10, 22, 0, 0, 0, lj)
	q := QuoteCharge(p, start, start.Add(4*time.Hour), 36, true, nil)
	if !near(q.BilledKWh, 40) || !near(q.Total, 4.80) || !near(q.PerUnitPrice, 0.1333) {
		t.Fatalf("night charge: %+v", q)
	}
	if len(q.Parts) != 2 || q.Parts[0].Name != "Energy" || q.Parts[1].Name != "Network · Block 3" {
		t.Fatalf("parts: %+v", q.Parts)
	}

	// 21:00–23:00: half high tariff, half low; network: 21–22 default (no rule), 22–23 block 3
	start = time.Date(2026, 11, 10, 21, 0, 0, 0, lj)
	q = QuoteCharge(p, start, start.Add(2*time.Hour), 20, false, nil)
	want := 10*0.15 + 10*0.10 + 10*0.01 + 10*0.02
	if !near(q.Total, math.Round(want*100)/100) || len(q.Parts) != 4 {
		t.Fatalf("21–23: want %.2f, got %+v", want, q)
	}

	// Christmas (Friday) is a free day: block 4, low tariff all day; summer has no network rule → default
	xmas := time.Date(2026, 12, 25, 23, 0, 0, 0, lj)
	q = QuoteCharge(p, xmas, xmas, 10, false, nil)
	if !near(q.Total, 1.15) {
		t.Fatalf("holiday: %+v", q)
	}
	july := time.Date(2026, 7, 7, 8, 0, 0, 0, lj) // Tuesday
	q = QuoteCharge(p, july, july, 10, false, nil)
	if !near(q.Total, 1.60) {
		t.Fatalf("summer workday morning: VT 0.15 + network default 0.01: %+v", q)
	}
	// a one-off holiday (YYYY-MM-DD), and a session fee
	p.SessionFee = 0.5
	easter := time.Date(2026, 4, 6, 8, 0, 0, 0, lj)
	q = QuoteCharge(p, easter, easter, 10, false, nil)
	if !near(q.Total, 1.60) || !IsFreeDay(easter, p.Holidays) {
		t.Fatalf("Easter Monday is free: 0.10 + 0.01 + fee: %+v", q)
	}

	// a Home Assistant price adds per slice: 0.20 until 01:00, then 0.08
	p.SessionFee = 0
	p.Components = nil
	start = time.Date(2026, 11, 11, 0, 0, 0, 0, lj)
	hist := []PricePoint{{At: start.Add(-time.Hour), Price: 0.20}, {At: start.Add(time.Hour), Price: 0.08}}
	q = QuoteCharge(p, start, start.Add(2*time.Hour), 10, false, hist)
	if !near(q.Total, 1.40) {
		t.Fatalf("HA prices: 5 kWh × 0.20 + 5 × 0.08: %+v", q)
	}
}

func TestValidateChargingPlace(t *testing.T) {
	for name, p := range map[string]db.ChargingPlace{
		"no name":   {},
		"bad time":  {Name: "x", Components: []db.TariffComponent{{Rules: []db.TariffRule{{From: "25:00"}}}}},
		"bad days":  {Name: "x", Components: []db.TariffComponent{{Rules: []db.TariffRule{{Days: "monday"}}}}},
		"bad month": {Name: "x", Components: []db.TariffComponent{{Rules: []db.TariffRule{{Months: []int{13}}}}}},
		"holiday":   {Name: "x", Holidays: "25.12."},
		"entity":    {Name: "x", PriceEntity: "../api"},
		"loss":      {Name: "x", LossPercent: 60},
		"half pos":  {Name: "x", Lat: new(float64)},
	} {
		if err := ValidateChargingPlace(&p); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	p := db.ChargingPlace{Name: " Home ", Components: []db.TariffComponent{{Rules: []db.TariffRule{{From: "06:00", To: "24:00"}}}}}
	if err := ValidateChargingPlace(&p); err != nil || p.Name != "Home" || p.RadiusM != 200 || p.Components[0].Name != "Part 1" ||
		p.Components[0].Rules[0].Days != "all" {
		t.Fatalf("defaults: %+v %v", p, err)
	}
}

func TestChargingPlaceWithHAPrice(t *testing.T) {
	setupDB(t)
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ha-token" || !strings.HasPrefix(r.URL.Path, "/api/history/period/") ||
			r.URL.Query().Get("filter_entity_id") != "sensor.price" {
			t.Errorf("unexpected request %s", r.URL)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Write([]byte(`[[{"entity_id":"sensor.price","state":"0.30","last_changed":"2026-11-10T21:00:00Z"},
		  {"state":"unavailable","last_changed":"2026-11-10T22:30:00Z"},
		  {"state":"0.10","last_changed":"2026-11-10T23:00:00Z"}]]`))
	}))
	defer ha.Close()
	UpdateHomeAssistantSettings(ha.URL, "ha-token")
	p, err := SaveChargingPlace(db.ChargingPlace{Name: "Home", PriceEntity: "sensor.price", SessionFee: 1})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 10, 22, 0, 0, 0, time.UTC)
	q, err := QuoteChargeAt(p.ID, start, start.Add(2*time.Hour), 20, false)
	if err != nil || !near(q.Total, 5.0) { // 10 × 0.30 + 10 × 0.10 + 1 (unavailable ignored)
		t.Fatalf("want 5.00: %+v %v", q, err)
	}
	ps, _ := ListChargingPlaces()
	if len(ps) != 1 || ps[0].PriceEntity != "sensor.price" {
		t.Fatalf("list: %+v", ps)
	}
	p.Name = "Home 2"
	if _, err := SaveChargingPlace(*p); err != nil {
		t.Fatal(err)
	}
	if got, _ := GetChargingPlace(p.ID); got.Name != "Home 2" {
		t.Fatalf("update: %+v", got)
	}
	DeleteChargingPlace(p.ID)
	if _, err := GetChargingPlace(p.ID); err == nil {
		t.Fatal("deleted")
	}
}
