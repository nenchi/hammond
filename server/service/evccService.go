package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/akhilrex/hammond/db"
	"github.com/akhilrex/hammond/models"
)

// EVCC import, per user (Settings → EVCC): finished sessions of the user's vehicles linked by name
// (Edit vehicle → EVCC vehicle) become detections the user confirms, priced with the user's EVCC
// charging place (else EVCC's own price). GET <url>/api/sessions needs no login in EVCC.

type evccSession struct {
	ID            int       `json:"id"`
	Created       time.Time `json:"created"`
	Finished      time.Time `json:"finished"`
	Vehicle       string    `json:"vehicle"`
	Odometer      float64   `json:"odometer"`
	ChargedEnergy float64   `json:"chargedEnergy"` // kWh at the wallbox
	SocEnd        float64   `json:"socEnd"`
	PricePerKWh   *float64  `json:"pricePerKWh"`
}

var (
	evccClient = &http.Client{Timeout: 10 * time.Second}
	evccMu     sync.Mutex // one sync at a time (timer + "Sync now")
)

func fetchEvccSessions(base string) ([]evccSession, error) {
	resp, err := evccClient.Get(strings.TrimRight(base, "/") + "/api/sessions")
	if err != nil {
		return nil, fmt.Errorf("EVCC not reachable: %s", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("EVCC answered HTTP %d", resp.StatusCode)
	}
	var list []evccSession
	if json.Unmarshal(body, &list) != nil { // older EVCC: {"result": [...]}
		var wrapped struct {
			Result []evccSession `json:"result"`
		}
		if err := json.Unmarshal(body, &wrapped); err != nil {
			return nil, errors.New("EVCC answered without a session list")
		}
		list = wrapped.Result
	}
	return list, nil
}

type EvccSyncResult struct {
	Imported int      `json:"imported"`
	Skipped  int      `json:"skipped"` // no linked vehicle, not finished, before "since", or already imported
	Notes    []string `json:"notes"`
}

func SyncEvcc(userID string) (*EvccSyncResult, error) {
	evccMu.Lock()
	defer evccMu.Unlock()
	s := getUser(userID)
	if s == nil || s.EvccURL == "" {
		return nil, errors.New("EVCC is not set up (Settings → EVCC)")
	}
	sessions, err := fetchEvccSessions(s.EvccURL)
	if err != nil {
		return nil, err
	}
	var vehicles []db.Vehicle
	db.DB.Where("evcc_vehicle <> '' AND id IN (?)", db.DB.Model(&db.UserVehicle{}).Select("vehicle_id").Where("user_id = ?", userID)).Find(&vehicles)
	var place *db.ChargingPlace
	if s.EvccPlaceID != "" {
		place, _ = GetChargingPlace(userID, s.EvccPlaceID)
	}
	res := &EvccSyncResult{Notes: []string{}}
	for _, ses := range sessions {
		var v *db.Vehicle
		for i := range vehicles {
			if strings.EqualFold(strings.TrimSpace(ses.Vehicle), vehicles[i].EvccVehicle) {
				v = &vehicles[i]
			}
		}
		ext := fmt.Sprintf("evcc:%d", ses.ID)
		var n int64
		db.DB.Model(&db.FillupDetection{}).Where("external_id = ?", ext).Count(&n)
		if v == nil || n > 0 || ses.Finished.IsZero() || ses.ChargedEnergy <= 0 || (s.EvccSince != nil && ses.Created.Before(*s.EvccSince)) {
			res.Skipped++
			continue
		}
		kwh := float32(math.Round(ses.ChargedEnergy*100) / 100)
		full := ses.SocEnd <= 0 || ses.SocEnd >= 95
		created, finished := ses.Created, ses.Finished
		det, err := CreateDetection(userID, v.ID, models.CreateDetectionRequest{
			OdoReading: int(math.Round(ses.Odometer)), Date: &created, FuelQuantity: &kwh, IsTankFull: &full, Source: "EVCC"})
		if err != nil {
			res.Notes = append(res.Notes, fmt.Sprintf("session %d: %s", ses.ID, err))
			continue
		}
		upd := map[string]interface{}{"external_id": ext, "charge_end": finished}
		var price *float64
		if place != nil {
			upd["station"] = place.Name
			if len(place.Components) > 0 || place.PriceEntity != "" {
				q, err := QuoteChargeAt(userID, place.ID, created, finished, float64(kwh), false) // EVCC meters at the wallbox
				if err == nil {
					price = &q.PerUnitPrice
				} else {
					res.Notes = append(res.Notes, fmt.Sprintf("session %d: %s (EVCC's price used)", ses.ID, err))
				}
			}
		}
		if price == nil && ses.PricePerKWh != nil && *ses.PricePerKWh > 0 {
			price = ses.PricePerKWh
		}
		if price != nil {
			upd["per_unit_price"] = float32(*price)
		}
		db.DB.Model(det.Detection).Updates(upd)
		res.Imported++
	}
	return res, nil
}

// UpdateEvccSettings (the user's own)
func UpdateEvccSettings(userID string, req models.UpdateEvccRequest) error {
	u := getUser(userID)
	if u == nil {
		return errors.New("user not found")
	}
	if req.PlaceID != "" {
		if _, err := GetChargingPlace(userID, req.PlaceID); err != nil {
			return err
		}
	}
	if u.EvccURL == "" && req.URL != "" && req.Since == nil {
		now := time.Now().AddDate(0, 0, -7) // first connection: the last week, not years of history
		req.Since = &now
	}
	return db.DB.Model(u).Updates(map[string]interface{}{"evcc_url": strings.TrimSpace(req.URL),
		"evcc_sync_minutes": req.SyncMinutes, "evcc_place_id": req.PlaceID, "evcc_since": req.Since}).Error
}

// EvccTimer: every minute, syncs each user whose interval has passed
func EvccTimer() {
	last := map[string]time.Time{}
	for range time.Tick(time.Minute) {
		var users []db.User
		db.DB.Where("evcc_url <> '' AND evcc_sync_minutes > 0 AND is_disabled = ?", false).Find(&users)
		for _, u := range users {
			if time.Since(last[u.ID]) < time.Duration(u.EvccSyncMinutes)*time.Minute {
				continue
			}
			last[u.ID] = time.Now()
			if r, err := SyncEvcc(u.ID); err != nil {
				log.Printf("EVCC sync (%s): %s", u.Email, err)
			} else if r.Imported > 0 {
				log.Printf("EVCC sync (%s): %d charging sessions imported", u.Email, r.Imported)
			}
		}
	}
}
