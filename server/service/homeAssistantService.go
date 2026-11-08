package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/akhilrex/hammond/db"
)

// Each user's own Home Assistant (Settings → Home Assistant): the car's odometer for the fill-up form
// (GET <url>/api/states/<entity>) and price sensors' history for charges. Long-lived access token from
// HA profile → Security; read-only, nothing is written to HA.

// haConfig: the user's HA URL + token ("" when not set up)
func haConfig(userID string) (string, string) {
	var u db.User
	if userID == "" || db.DB.Where("id = ?", userID).Limit(1).Find(&u).Error != nil {
		return "", ""
	}
	return strings.TrimRight(u.HAURL, "/"), u.HAToken
}

// MoveSiteConnectionsToAdmin: v1.6 had one site-wide Home Assistant; it becomes the first admin's own
func MoveSiteConnectionsToAdmin() {
	s := db.GetOrCreateSetting()
	if s.HAURL == "" && s.HAToken == "" {
		return
	}
	var admin db.User
	if db.DB.Where("role = ?", db.ADMIN).Order("created_at").Limit(1).Find(&admin); admin.ID == "" {
		return
	}
	if admin.HAURL == "" && admin.HAToken == "" {
		db.DB.Model(&admin).Updates(map[string]interface{}{"ha_url": s.HAURL, "ha_token": s.HAToken})
	}
	s.HAURL, s.HAToken = "", ""
	db.UpdateSettings(s)
}

var entityRe = regexp.MustCompile(`^[a-z0-9_]+\.[a-z0-9_]+$`)

func ValidEntityID(s string) bool { return s == "" || (len(s) <= 100 && entityRe.MatchString(s)) }

type Odometer struct {
	Configured bool      `json:"configured"`
	Value      float64   `json:"value"`
	Unit       string    `json:"unit"`      // HA's unit_of_measurement, e.g. "km"
	UpdatedAt  time.Time `json:"updatedAt"` // when the car last reported it
	Entity     string    `json:"entity"`
}

var haClient = &http.Client{Timeout: 5 * time.Second}

// CurrentOdometer with the user's HA: {configured: false} when HA or the vehicle's entity isn't set up
func CurrentOdometer(userID string, v *db.Vehicle) (*Odometer, error) {
	haURL, token := haConfig(userID)
	if haURL == "" || token == "" || v.OdometerEntity == "" {
		return &Odometer{}, nil
	}
	req, _ := http.NewRequest("GET", haURL+"/api/states/"+v.OdometerEntity, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := haClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Home Assistant not reachable: %s", strings.ReplaceAll(err.Error(), token, "***"))
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, errors.New("Home Assistant refused the token")
	case http.StatusNotFound:
		return nil, fmt.Errorf("Home Assistant has no entity %s", v.OdometerEntity)
	default:
		return nil, fmt.Errorf("Home Assistant answered HTTP %d", resp.StatusCode)
	}
	var st struct {
		State      string `json:"state"`
		Attributes struct {
			Unit string `json:"unit_of_measurement"`
		} `json:"attributes"`
		LastUpdated  time.Time  `json:"last_updated"`
		LastReported *time.Time `json:"last_reported"` // HA ≥ 2024.3: also when the value didn't change
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return nil, errors.New("Home Assistant answered without valid data")
	}
	val, err := strconv.ParseFloat(st.State, 64)
	if err != nil {
		return nil, fmt.Errorf("%s is %q, not a number", v.OdometerEntity, st.State)
	}
	at := st.LastUpdated
	if st.LastReported != nil && st.LastReported.After(at) {
		at = *st.LastReported
	}
	return &Odometer{Configured: true, Value: val, Unit: st.Attributes.Unit, UpdatedAt: at, Entity: v.OdometerEntity}, nil
}

// UpdateHomeAssistantSettings (the user's own). token "" keeps the stored one, "-" removes it.
func UpdateHomeAssistantSettings(userID, rawURL, token string) error {
	upd := map[string]interface{}{"ha_url": strings.TrimSpace(rawURL)}
	switch token {
	case "":
	case "-":
		upd["ha_token"] = ""
	default:
		upd["ha_token"] = strings.TrimSpace(token)
	}
	return db.DB.Model(&db.User{}).Where("id = ?", userID).Updates(upd).Error
}

// HAPriceHistory: the values of a price entity in the user's HA from start to end (the first one is the
// value at start), for pricing a charge with a dynamic or block tariff that Home Assistant already knows.
func HAPriceHistory(userID, entity string, start, end time.Time) ([]PricePoint, error) {
	haURL, token := haConfig(userID)
	if haURL == "" || token == "" {
		return nil, errors.New("Home Assistant of the place's owner is not set up (Settings → Home Assistant)")
	}
	if end.Before(start) {
		end = start
	}
	u := fmt.Sprintf("%s/api/history/period/%s?end_time=%s&filter_entity_id=%s&minimal_response&no_attributes",
		haURL, url.QueryEscape(start.UTC().Format(time.RFC3339)),
		url.QueryEscape(end.Add(time.Second).UTC().Format(time.RFC3339)), url.QueryEscape(entity))
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := haClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Home Assistant not reachable: %s", strings.ReplaceAll(err.Error(), token, "***"))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Home Assistant answered HTTP %d for the price history", resp.StatusCode)
	}
	var hist [][]struct {
		State       string    `json:"state"`
		LastChanged time.Time `json:"last_changed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&hist); err != nil {
		return nil, errors.New("Home Assistant answered without valid history")
	}
	var out []PricePoint
	for _, list := range hist {
		for _, st := range list {
			if v, err := strconv.ParseFloat(st.State, 64); err == nil {
				out = append(out, PricePoint{At: st.LastChanged, Price: v})
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no price history for %s at that time", entity)
	}
	return out, nil
}

type HATestResult struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Name     string `json:"name,omitempty"` // HA location name
	Version  string `json:"version,omitempty"`
	Odometer string `json:"odometer,omitempty"` // first vehicle with an odometer entity: its value
}

// TestHomeAssistant ("Test connection"): the user's saved URL + token against /api/config, and the
// odometer of the user's first vehicle that has an entity set
func TestHomeAssistant(userID string) HATestResult {
	haURL, token := haConfig(userID)
	if haURL == "" || token == "" {
		return HATestResult{Error: "save the URL and the token first"}
	}
	req, _ := http.NewRequest("GET", haURL+"/api/config", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := haClient.Do(req)
	if err != nil {
		return HATestResult{Error: "Home Assistant not reachable: " + strings.ReplaceAll(err.Error(), token, "***")}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return HATestResult{Error: "Home Assistant refused the token (401)"}
	}
	if resp.StatusCode != http.StatusOK {
		return HATestResult{Error: fmt.Sprintf("Home Assistant answered HTTP %d (is the URL the HA base URL?)", resp.StatusCode)}
	}
	var cfg struct {
		Name    string `json:"location_name"`
		Version string `json:"version"`
	}
	if json.NewDecoder(resp.Body).Decode(&cfg) != nil {
		return HATestResult{Error: "the URL answers, but not like Home Assistant"}
	}
	r := HATestResult{OK: true, Name: cfg.Name, Version: cfg.Version}
	var vs []db.Vehicle
	if db.DB.Where("odometer_entity <> '' AND id IN (?)", db.DB.Model(&db.UserVehicle{}).Select("vehicle_id").Where("user_id = ?", userID)).
		Limit(1).Find(&vs); len(vs) > 0 {
		if o, err := CurrentOdometer(userID, &vs[0]); err != nil {
			r.Odometer = fmt.Sprintf("%s: %s", vs[0].Nickname, err)
		} else {
			r.Odometer = fmt.Sprintf("%s: %s = %g %s", vs[0].Nickname, o.Entity, o.Value, o.Unit)
		}
	}
	return r
}
