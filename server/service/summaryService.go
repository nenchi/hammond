package service

// Summary: the numbers a dashboard wants, per vehicle and per watched station. Served as /api/v1/summary
// and published to Home Assistant over MQTT (mqttService.go).

import (
	"math"
	"time"

	"github.com/akhilrex/hammond/db"
)

type FillupSummary struct {
	Date         time.Time `json:"date"`
	Quantity     float64   `json:"quantity"`
	PerUnitPrice float64   `json:"perUnitPrice"`
	Total        float64   `json:"total"`
	Station      string    `json:"station"`
}

type TripTotals struct {
	Count            int     `json:"count"`
	Distance         float64 `json:"distance"`
	BusinessDistance float64 `json:"businessDistance"`
	DriveMinutes     int     `json:"driveMinutes"`
	Cost             float64 `json:"cost"` // fuel (first in, first out) + the trips' own costs
}

type VehicleSummary struct {
	ID           string         `json:"id"`
	Nickname     string         `json:"nickname"`
	Registration string         `json:"registration"`
	Make         string         `json:"make"`
	Model        string         `json:"model"`
	Odometer     float64        `json:"odometer"` // newest of fill-ups, expenses and trips
	LastFillup   *FillupSummary `json:"lastFillup"`
	MonthFuel    float64        `json:"monthFuel"`
	MonthOther   float64        `json:"monthOther"`
	MonthFillups int            `json:"monthFillups"`
	// fuel per 100 distance units over the last 90 days (full-tank fill-ups); nil = not enough data
	Consumption *float64   `json:"consumption"`
	TripsToday  TripTotals `json:"tripsToday"`
	TripsMonth  TripTotals `json:"tripsMonth"`
}

type WatchSummary struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Address    string     `json:"address"`
	Grade      string     `json:"grade"`
	GradeName  string     `json:"gradeName"`
	Country    string     `json:"country"` // ISO code from the price source, "" for custom sources
	Price      *float64   `json:"price"`
	Below      *float64   `json:"below"`
	BelowLimit bool       `json:"belowLimit"`
	CheckedAt  *time.Time `json:"checkedAt"`
}

type Summary struct {
	Currency     string           `json:"currency"`
	DistanceUnit string           `json:"distanceUnit"`
	Vehicles     []VehicleSummary `json:"vehicles"`
	Watches      []WatchSummary   `json:"watches"`
	At           time.Time        `json:"at"`
}

func tripTotals(ts []TripView) (t TripTotals) {
	for _, x := range ts {
		t.Count++
		t.Distance += x.Distance
		if x.Business {
			t.BusinessDistance += x.Distance
		}
		t.DriveMinutes += x.DriveMins
		t.Cost += x.FuelCost + x.ExtraCost
	}
	return
}

func GetSummary(userID string) (*Summary, error) {
	u, err := db.GetUserById(userID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	s := &Summary{Currency: u.Currency, DistanceUnit: db.DistanceUnitDetails[u.DistanceUnit].Short, At: now,
		Vehicles: []VehicleSummary{}, Watches: []WatchSummary{}}
	vs, err := GetUserVehicles(userID)
	if err != nil {
		return nil, err
	}
	for _, v := range *vs {
		vsum := VehicleSummary{ID: v.ID, Nickname: v.Nickname, Registration: v.Registration, Make: v.Make, Model: v.Model}
		var fills []db.Fillup
		db.DB.Where("vehicle_id = ?", v.ID).Order("date desc").Find(&fills)
		for i, f := range fills {
			if i == 0 {
				// float32 in the database: round, so 38.6 isn't 38.599998474121094
				vsum.LastFillup = &FillupSummary{f.Date, round2(float64(f.FuelQuantity)), math.Round(float64(f.PerUnitPrice)*1000) / 1000,
					round2(float64(f.TotalAmount)), f.FillingStation}
			}
			vsum.Odometer = max(vsum.Odometer, float64(f.OdoReading))
			if !f.Date.Before(month) {
				vsum.MonthFuel += float64(f.TotalAmount)
				vsum.MonthFillups++
			}
		}
		var exps []db.Expense
		db.DB.Where("vehicle_id = ?", v.ID).Find(&exps)
		for _, e := range exps {
			vsum.Odometer = max(vsum.Odometer, float64(e.OdoReading))
			if !e.Date.Before(month) {
				vsum.MonthOther += float64(e.Amount)
			}
		}
		var last db.TripLeg
		if db.DB.Where("vehicle_id = ?", v.ID).Order("ended_at desc").Limit(1).Find(&last); last.ID != "" {
			vsum.Odometer = max(vsum.Odometer, last.EndOdo)
		}
		// mileage = distance per fuel unit of a full-tank fill-up (0 otherwise): distance = mileage × quantity
		if ms, err := GetMileageByVehicleId(v.ID, now.AddDate(0, 0, -90)); err == nil {
			var dist, qty float64
			for _, m := range ms {
				if m.Mileage > 0 {
					dist, qty = dist+float64(m.Mileage*m.FuelQuantity), qty+float64(m.FuelQuantity)
				}
			}
			if dist > 0 {
				c := 100 * qty / dist
				vsum.Consumption = &c
			}
		}
		if ts, err := ListTrips(v.ID, month, today.AddDate(0, 0, 1)); err == nil {
			vsum.TripsMonth = tripTotals(ts)
			var td []TripView
			for _, t := range ts {
				if !t.Start.Before(today) {
					td = append(td, t)
				}
			}
			vsum.TripsToday = tripTotals(td)
		}
		s.Vehicles = append(s.Vehicles, vsum)
	}
	ws, err := ListPriceWatches(userID)
	if err != nil {
		return nil, err
	}
	for _, w := range ws {
		s.Watches = append(s.Watches, WatchSummary{ID: w.ID, Name: w.Name, Address: ShortAddress(w.Address), Grade: w.Grade,
			GradeName: gradeName(w.Grade), Country: kindCountry[w.Kind], Price: w.LastPrice, Below: w.Below,
			BelowLimit: w.LastPrice != nil && w.Below != nil && *w.LastPrice <= *w.Below, CheckedAt: w.CheckedAt})
	}
	for i := range s.Vehicles {
		v := &s.Vehicles[i]
		v.MonthFuel, v.MonthOther = round2(v.MonthFuel), round2(v.MonthOther)
	}
	return s, nil
}
