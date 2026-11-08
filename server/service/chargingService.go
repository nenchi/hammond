package service

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/akhilrex/hammond/db"
)

// Electric charging: places with tariffs. Times are matched in the server's local time zone (TZ of the
// container). ponytail: one zone for all places; a per-place zone if someone charges across zones often.

const sliceMinutes = 5 // a charge is priced in 5-minute slices, energy spread evenly over its duration

func parseHM(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	h, m, ok := strings.Cut(s, ":")
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || hh < 0 || hh > 24 || mm < 0 || mm > 59 || (hh == 24 && mm != 0) {
		return 0, fmt.Errorf("invalid time %q (HH:MM)", s)
	}
	return hh*60 + mm, nil
}

// IsFreeDay: weekend or one of the place's holidays (MM-DD every year, or YYYY-MM-DD)
func IsFreeDay(t time.Time, holidays string) bool {
	if wd := t.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return true
	}
	md, ymd := t.Format("01-02"), t.Format("2006-01-02")
	for _, h := range strings.Fields(holidays) {
		if h == md || h == ymd {
			return true
		}
	}
	return false
}

func ruleMatches(r db.TariffRule, t time.Time, free bool) bool {
	if len(r.Months) > 0 {
		ok := false
		for _, m := range r.Months {
			ok = ok || time.Month(m) == t.Month()
		}
		if !ok {
			return false
		}
	}
	if (r.Days == "work" && free) || (r.Days == "free" && !free) {
		return false
	}
	from, _ := parseHM(r.From)
	to, _ := parseHM(r.To)
	now := t.Hour()*60 + t.Minute()
	switch {
	case from == to:
		return true // all day
	case from < to:
		return now >= from && now < to
	default: // past midnight
		return now >= from || now < to
	}
}

// componentPrice: price and label of the rule that applies at t (local time)
func componentPrice(c db.TariffComponent, t time.Time, holidays string) (float64, string) {
	free := IsFreeDay(t, holidays)
	for _, r := range c.Rules {
		if ruleMatches(r, t, free) {
			return r.Price, r.Label
		}
	}
	return c.Price, ""
}

// ValidateChargingPlace checks names, times, months and days; trims the place
func ValidateChargingPlace(p *db.ChargingPlace) error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || len([]rune(p.Name)) > 60 {
		return errors.New("a charging place needs a name (up to 60 characters)")
	}
	if (p.Lat == nil) != (p.Lng == nil) || (p.Lat != nil && (*p.Lat < -90 || *p.Lat > 90 || *p.Lng < -180 || *p.Lng > 180)) {
		return errors.New("invalid position")
	}
	if p.RadiusM <= 0 {
		p.RadiusM = 200
	}
	if p.LossPercent < 0 || p.LossPercent >= 50 || p.SessionFee < 0 {
		return errors.New("charging loss must be 0–50 %, the session fee not negative")
	}
	if !ValidEntityID(p.PriceEntity) {
		return errors.New("price entity must look like sensor.electricity_price")
	}
	if len(p.Components) > 10 {
		return errors.New("at most 10 price components")
	}
	for i := range p.Components {
		c := &p.Components[i]
		c.Name = strings.TrimSpace(c.Name)
		if c.Name == "" {
			c.Name = fmt.Sprintf("Part %d", i+1)
		}
		if len(c.Rules) > 60 {
			return fmt.Errorf("%s: at most 60 rules", c.Name)
		}
		for j := range c.Rules {
			r := &c.Rules[j]
			if r.Days == "" {
				r.Days = "all"
			}
			if r.Days != "all" && r.Days != "work" && r.Days != "free" {
				return fmt.Errorf("%s rule %d: days must be all, work or free", c.Name, j+1)
			}
			for _, m := range r.Months {
				if m < 1 || m > 12 {
					return fmt.Errorf("%s rule %d: months are 1–12", c.Name, j+1)
				}
			}
			if _, err := parseHM(r.From); err != nil {
				return fmt.Errorf("%s rule %d: %w", c.Name, j+1, err)
			}
			if _, err := parseHM(r.To); err != nil {
				return fmt.Errorf("%s rule %d: %w", c.Name, j+1, err)
			}
		}
	}
	for _, h := range strings.Fields(p.Holidays) {
		if _, err := time.Parse("01-02", h); err != nil {
			if _, err := time.Parse("2006-01-02", h); err != nil {
				return fmt.Errorf("holiday %q: use MM-DD or YYYY-MM-DD", h)
			}
		}
	}
	return nil
}

// PricePoint: a price valid from At (e.g. from Home Assistant's history)
type PricePoint struct {
	At    time.Time
	Price float64
}

type QuotePart struct {
	Name  string  `json:"name"` // "Network · Block 2"
	KWh   float64 `json:"kWh"`
	Price float64 `json:"price"` // per kWh
	Cost  float64 `json:"cost"`
}

type ChargeQuote struct {
	Place        string      `json:"place"`
	KWh          float64     `json:"kWh"`       // as given
	BilledKWh    float64     `json:"billedKWh"` // with the charging loss
	SessionFee   float64     `json:"sessionFee"`
	Total        float64     `json:"total"`
	PerUnitPrice float64     `json:"perUnitPrice"` // total / kWh: what goes into the fill-up
	Parts        []QuotePart `json:"parts"`
}

// QuoteCharge prices kWh charged from start to end (end ≤ start: all at start). applyLoss: the kWh are
// what the car got (manual entry); EVCC meters at the wallbox, so no loss there. haPrices: the price
// entity's values (sorted, the first at or before start), nil without one.
func QuoteCharge(p db.ChargingPlace, start, end time.Time, kWh float64, applyLoss bool, haPrices []PricePoint) ChargeQuote {
	q := ChargeQuote{Place: p.Name, KWh: kWh, BilledKWh: kWh, SessionFee: p.SessionFee}
	if applyLoss && p.LossPercent > 0 {
		q.BilledKWh = kWh / (1 - p.LossPercent/100)
	}
	n := 1
	if d := end.Sub(start); d > 0 {
		n = int(math.Ceil(d.Minutes() / sliceMinutes))
	}
	step := end.Sub(start) / time.Duration(n)
	parts := map[string]*QuotePart{}
	var order []string
	add := func(name string, kwh, price float64) {
		key := fmt.Sprintf("%s|%.6f", name, price)
		if parts[key] == nil {
			parts[key] = &QuotePart{Name: name, Price: price}
			order = append(order, key)
		}
		parts[key].KWh += kwh
		parts[key].Cost += kwh * price
	}
	for i := 0; i < n; i++ {
		t := start.Add(step*time.Duration(i) + step/2).Local()
		e := q.BilledKWh / float64(n)
		for _, c := range p.Components {
			price, label := componentPrice(c, t, p.Holidays)
			name := c.Name
			if label != "" {
				name += " · " + label
			}
			add(name, e, price)
		}
		if len(haPrices) > 0 {
			price := haPrices[0].Price
			for _, pp := range haPrices {
				if pp.At.After(t) {
					break
				}
				price = pp.Price
			}
			add("Home Assistant", e, price)
		}
	}
	for _, k := range order {
		pt := parts[k]
		pt.KWh, pt.Cost = math.Round(pt.KWh*1000)/1000, math.Round(pt.Cost*10000)/10000
		q.Parts = append(q.Parts, *pt)
		q.Total += pt.Cost
	}
	q.Total = math.Round((q.Total+q.SessionFee)*100) / 100
	if kWh > 0 {
		q.PerUnitPrice = math.Round(q.Total/kWh*10000) / 10000
	}
	sort.SliceStable(q.Parts, func(i, j int) bool { return q.Parts[i].Name < q.Parts[j].Name })
	return q
}

// ListChargingPlaces: the user's own places and the shared ones
func ListChargingPlaces(userID string) ([]db.ChargingPlace, error) {
	var ps []db.ChargingPlace
	err := db.DB.Where("shared = ? OR owner_id = ?", true, userID).Order("name").Find(&ps).Error
	return ps, err
}

// GetChargingPlace: a place the user may use (own or shared)
func GetChargingPlace(userID, id string) (*db.ChargingPlace, error) {
	var ps []db.ChargingPlace
	db.DB.Where("id = ? AND (shared = ? OR owner_id = ?)", id, true, userID).Limit(1).Find(&ps)
	if len(ps) == 0 {
		return nil, errors.New("charging place not found")
	}
	return &ps[0], nil
}

// editable: the owner, or an admin for a shared place
func editablePlace(userID string, isAdmin bool, id string) (*db.ChargingPlace, error) {
	p, err := GetChargingPlace(userID, id)
	if err != nil {
		return nil, err
	}
	if p.OwnerID != userID && !(isAdmin && p.Shared) {
		return nil, errors.New("only its owner or an admin can change this place")
	}
	return p, nil
}

// SaveChargingPlace creates (no id) or replaces a place. Only admins share places.
func SaveChargingPlace(userID string, isAdmin bool, p db.ChargingPlace) (*db.ChargingPlace, error) {
	if err := ValidateChargingPlace(&p); err != nil {
		return nil, err
	}
	if !isAdmin {
		p.Shared = false
	}
	if p.ID != "" {
		old, err := editablePlace(userID, isAdmin, p.ID)
		if err != nil {
			return nil, err
		}
		p.CreatedAt, p.OwnerID = old.CreatedAt, old.OwnerID
		return &p, db.DB.Save(&p).Error
	}
	p.OwnerID = userID
	return &p, db.DB.Create(&p).Error
}

func DeleteChargingPlace(userID string, isAdmin bool, id string) error {
	if _, err := editablePlace(userID, isAdmin, id); err != nil {
		return err
	}
	return db.DB.Where("id = ?", id).Delete(&db.ChargingPlace{}).Error
}

// QuoteChargeAt prices a charge at a place the user may use; a price sensor is read from the Home
// Assistant of the place's owner
func QuoteChargeAt(userID, placeID string, start, end time.Time, kWh float64, applyLoss bool) (*ChargeQuote, error) {
	p, err := GetChargingPlace(userID, placeID)
	if err != nil {
		return nil, err
	}
	if kWh <= 0 || kWh > 1000 {
		return nil, errors.New("kWh must be between 0 and 1000")
	}
	if end.Sub(start) > 72*time.Hour {
		return nil, errors.New("a charge can't last more than 72 hours")
	}
	var hist []PricePoint
	if p.PriceEntity != "" {
		if hist, err = HAPriceHistory(p.OwnerID, p.PriceEntity, start, end); err != nil {
			return nil, err
		}
	}
	q := QuoteCharge(*p, start, end, kWh, applyLoss, hist)
	return &q, nil
}
