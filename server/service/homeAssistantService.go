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

// Current odometer from Home Assistant for the fill-up form: GET <url>/api/states/<entity> with a
// long-lived access token (HA profile → Security). Read-only, nothing is written to HA.

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

// CurrentOdometer: {configured: false} when HA or the vehicle's entity isn't set up
func CurrentOdometer(v *db.Vehicle) (*Odometer, error) {
	s := db.GetOrCreateSetting()
	if s.HAURL == "" || s.HAToken == "" || v.OdometerEntity == "" {
		return &Odometer{}, nil
	}
	req, _ := http.NewRequest("GET", strings.TrimRight(s.HAURL, "/")+"/api/states/"+v.OdometerEntity, nil)
	req.Header.Set("Authorization", "Bearer "+s.HAToken)
	resp, err := haClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Home Assistant not reachable: %s", strings.ReplaceAll(err.Error(), s.HAToken, "***"))
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

// UpdateHomeAssistantSettings (admin). token "" keeps the stored one, "-" removes it.
func UpdateHomeAssistantSettings(rawURL, token string) error {
	s := db.GetOrCreateSetting()
	s.HAURL = strings.TrimSpace(rawURL)
	switch token {
	case "":
	case "-":
		s.HAToken = ""
	default:
		s.HAToken = strings.TrimSpace(token)
	}
	return db.UpdateSettings(s)
}

// HAPriceHistory: the values of a price entity from start to end (the first one is the value at start),
// for pricing a charge with a dynamic or block tariff that Home Assistant already knows.
func HAPriceHistory(entity string, start, end time.Time) ([]PricePoint, error) {
	s := db.GetOrCreateSetting()
	if s.HAURL == "" || s.HAToken == "" {
		return nil, errors.New("Home Assistant is not set up (Admin → Site Settings → Home Assistant)")
	}
	if end.Before(start) {
		end = start
	}
	u := fmt.Sprintf("%s/api/history/period/%s?end_time=%s&filter_entity_id=%s&minimal_response&no_attributes",
		strings.TrimRight(s.HAURL, "/"), url.QueryEscape(start.UTC().Format(time.RFC3339)),
		url.QueryEscape(end.Add(time.Second).UTC().Format(time.RFC3339)), url.QueryEscape(entity))
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Authorization", "Bearer "+s.HAToken)
	resp, err := haClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Home Assistant not reachable: %s", strings.ReplaceAll(err.Error(), s.HAToken, "***"))
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
