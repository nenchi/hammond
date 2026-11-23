package service

// MQTT to Home Assistant: each user's own broker (Settings → MQTT). hammond announces its sensors with HA's
// MQTT discovery (a device per vehicle and per watched station, no YAML in HA) and publishes the Summary:
// after every change (fill-up, expense, trip, price) and every 5 minutes. Topics:
//   <prefix>/sensor/hammond_<id>/<key>/config  discovery (retained), prefix default "homeassistant"
//   hammond/<user>/vehicle/<id>, hammond/<user>/watch/<id>  state JSON (retained)
//   hammond/<user>/status  online/offline (offline = last will)

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/akhilrex/hammond/db"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"gorm.io/gorm"
)

type mqttConn struct {
	client mqtt.Client
	key    string          // url|user|password|prefix it was made with
	topics map[string]bool // discovery topics published: the ones gone next time get an empty config (removed in HA)
}

var (
	mqttMu    sync.Mutex
	mqttConns = map[string]*mqttConn{}
	mqttDirty = make(chan struct{}, 1)
)

// MqttNotify: something changed; publishes soon (several changes in a row = one publish)
func MqttNotify() {
	select {
	case mqttDirty <- struct{}{}:
	default:
	}
}

// mqttTables: changes in these tables change the summary
var mqttTables = map[string]bool{"fillups": true, "expenses": true, "trip_legs": true, "trips": true, "price_watches": true, "vehicles": true}

// MqttTimer: publishes every user's summary every 5 minutes and shortly after changes (GORM callbacks see
// every write, whichever code path made it)
func MqttTimer() {
	cb := func(tx *gorm.DB) {
		if tx.Statement != nil && mqttTables[tx.Statement.Table] {
			MqttNotify()
		}
	}
	db.DB.Callback().Create().After("gorm:create").Register("hammond:mqtt_create", cb)
	db.DB.Callback().Update().After("gorm:update").Register("hammond:mqtt_update", cb)
	db.DB.Callback().Delete().After("gorm:delete").Register("hammond:mqtt_delete", cb)
	tick := time.NewTicker(5 * time.Minute)
	for {
		select {
		case <-tick.C:
		case <-mqttDirty:
			time.Sleep(5 * time.Second) // debounce
			select {
			case <-mqttDirty:
			default:
			}
		}
		var users []db.User
		db.DB.Where("mqtt_url <> ''").Find(&users)
		for _, u := range users {
			if _, err := PublishMqtt(u.ID); err != nil {
				fmt.Printf("mqtt %s: %v\n", u.Email, err)
			}
		}
	}
}

func mqttBase(u *db.User) string { return "hammond/" + u.ID[:8] }

// mqttClient: the user's connection, made again when the settings changed
func mqttClient(u *db.User) (*mqttConn, error) {
	key := strings.Join([]string{u.MqttURL, u.MqttUser, u.MqttPassword, u.MqttPrefix}, "|")
	mqttMu.Lock()
	defer mqttMu.Unlock()
	if c := mqttConns[u.ID]; c != nil && c.key == key && c.client.IsConnectionOpen() {
		return c, nil
	}
	if c := mqttConns[u.ID]; c != nil {
		c.client.Disconnect(250)
		delete(mqttConns, u.ID)
	}
	broker := u.MqttURL
	broker = strings.Replace(strings.Replace(broker, "mqtts://", "ssl://", 1), "mqtt://", "tcp://", 1)
	status := mqttBase(u) + "/status"
	opts := mqtt.NewClientOptions().AddBroker(broker).SetClientID("hammond-"+u.ID[:8]).
		SetUsername(u.MqttUser).SetPassword(u.MqttPassword).SetConnectTimeout(5*time.Second).
		SetAutoReconnect(true).SetWill(status, "offline", 1, true)
	opts.SetOnConnectHandler(func(c mqtt.Client) { c.Publish(status, 1, true, "online") })
	c := mqtt.NewClient(opts)
	if t := c.Connect(); !t.WaitTimeout(6*time.Second) || t.Error() != nil {
		err := t.Error()
		if err == nil {
			err = errors.New("timeout")
		}
		return nil, fmt.Errorf("MQTT broker not reachable: %v", err)
	}
	conn := &mqttConn{client: c, key: key, topics: map[string]bool{}}
	mqttConns[u.ID] = conn
	return conn, nil
}

type mqttEntity struct {
	component, key, name, unit, deviceClass, stateClass, icon string
}

func vehicleEntities(cur, dist string) []mqttEntity {
	return []mqttEntity{
		{"sensor", "odometer", "Odometer", dist, "distance", "total_increasing", ""},
		{"sensor", "last_fillup", "Last fill-up", "", "timestamp", "", "mdi:gas-station"},
		{"sensor", "last_fillup_litres", "Last fill-up quantity", "L", "", "", "mdi:gas-station"},
		{"sensor", "last_fillup_price", "Last fill-up price", cur + "/L", "", "", "mdi:cash"},
		{"sensor", "last_fillup_total", "Last fill-up cost", cur, "monetary", "", ""},
		{"sensor", "month_fuel", "Fuel this month", cur, "monetary", "total", ""},
		{"sensor", "month_other", "Other costs this month", cur, "monetary", "total", ""},
		{"sensor", "month_total", "Costs this month", cur, "monetary", "total", ""},
		{"sensor", "month_fillups", "Fill-ups this month", "", "", "measurement", "mdi:gas-station"},
		{"sensor", "last30_fuel", "Fuel last 30 days", cur, "monetary", "", "mdi:gas-station"},
		{"sensor", "last30_other", "Other costs last 30 days", cur, "monetary", "", "mdi:cash"},
		{"sensor", "last30_total", "Costs last 30 days", cur, "monetary", "", "mdi:cash-multiple"},
		{"sensor", "last30_fillups", "Fill-ups last 30 days", "", "", "measurement", "mdi:gas-station"},
		{"sensor", "last30_litres", "Litres last 30 days", "L", "", "measurement", "mdi:fuel"},
		{"sensor", "last30_price", "Average price last 30 days", cur + "/L", "", "measurement", "mdi:cash"},
		{"sensor", "year_total", "Costs this year", cur, "monetary", "total", "mdi:calendar"},
		{"sensor", "consumption", "Consumption", "L/100" + dist, "", "measurement", "mdi:fuel"},
		{"sensor", "trips_today_distance", "Trips today", dist, "distance", "total", "mdi:map-marker-path"},
		{"sensor", "trips_today_count", "Trips today count", "", "", "measurement", "mdi:map-marker-path"},
		{"sensor", "trips_today_minutes", "Driving today", "min", "duration", "total", "mdi:steering"},
		{"sensor", "trips_today_cost", "Trips today cost", cur, "monetary", "total", ""},
		{"sensor", "trips_month_distance", "Trips this month", dist, "distance", "total", "mdi:map-marker-path"},
		{"sensor", "trips_month_business", "Business trips this month", dist, "distance", "total", "mdi:briefcase"},
		{"sensor", "trips_month_cost", "Trips this month cost", cur, "monetary", "total", ""},
	}
}

func watchEntities(cur string) []mqttEntity {
	return []mqttEntity{
		{"sensor", "price", "Price", cur + "/L", "", "measurement", "mdi:gas-station"},
		{"sensor", "limit", "Price limit", cur + "/L", "", "", "mdi:bell"},
		{"binary_sensor", "below_limit", "Below limit", "", "", "", "mdi:cash-check"},
		{"sensor", "checked", "Checked", "", "timestamp", "", "mdi:clock-check"},
	}
}

func vehicleState(v VehicleSummary) map[string]any {
	st := map[string]any{"odometer": v.Odometer, "month_fuel": round2(v.MonthFuel), "month_other": round2(v.MonthOther),
		"month_total": round2(v.MonthFuel + v.MonthOther), "month_fillups": v.MonthFillups, "consumption": nil,
		"last_fillup": nil, "last_fillup_litres": nil, "last_fillup_price": nil, "last_fillup_total": nil,
		"trips_today_distance": round2(v.TripsToday.Distance), "trips_today_count": v.TripsToday.Count,
		"trips_today_minutes": v.TripsToday.DriveMinutes, "trips_today_cost": round2(v.TripsToday.Cost),
		"trips_month_distance": round2(v.TripsMonth.Distance), "trips_month_business": round2(v.TripsMonth.BusinessDistance),
		"trips_month_cost": round2(v.TripsMonth.Cost),
		"last30_fuel":      v.Last30Fuel, "last30_other": v.Last30Other, "last30_total": round2(v.Last30Fuel + v.Last30Other),
		"last30_fillups": v.Last30Fillups, "last30_litres": v.Last30Litres, "last30_price": v.Last30Price,
		"year_total": round2(v.YearFuel + v.YearOther),
		// when the "this month" / "this year" totals start over: HA's statistics then show correct bars per period
		"month_start": time.Date(time.Now().Year(), time.Now().Month(), 1, 0, 0, 0, 0, time.Local).Format(time.RFC3339),
		"year_start":  time.Date(time.Now().Year(), 1, 1, 0, 0, 0, 0, time.Local).Format(time.RFC3339),
		"day_start":   time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.Local).Format(time.RFC3339)}
	if v.Consumption != nil {
		st["consumption"] = round2(*v.Consumption)
	}
	if f := v.LastFillup; f != nil {
		st["last_fillup"], st["last_fillup_litres"], st["last_fillup_price"], st["last_fillup_total"] =
			f.Date.Format(time.RFC3339), round2(f.Quantity), math.Round(f.PerUnitPrice*1000)/1000, round2(f.Total)
		st["last_fillup_station"] = f.Station
	}
	return st
}

func watchState(w WatchSummary) map[string]any {
	st := map[string]any{"price": w.Price, "limit": w.Below, "below_limit": map[bool]string{true: "ON", false: "OFF"}[w.BelowLimit],
		"checked": nil, "address": w.Address, "grade": w.GradeName, "country": w.Country}
	if w.CheckedAt != nil {
		st["checked"] = w.CheckedAt.Format(time.RFC3339)
	}
	return st
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

type MqttResult struct {
	Vehicles int `json:"vehicles"`
	Watches  int `json:"watches"`
	Removed  int `json:"removed"` // devices of deleted vehicles/watches removed from HA
}

// PublishMqtt: discovery + state of every vehicle and watch of the user
func PublishMqtt(userID string) (*MqttResult, error) {
	u, err := db.GetUserById(userID)
	if err != nil {
		return nil, err
	}
	if u.MqttURL == "" {
		return nil, errors.New("MQTT is not set up")
	}
	s, err := GetSummary(userID)
	if err != nil {
		return nil, err
	}
	conn, err := mqttClient(u)
	if err != nil {
		return nil, err
	}
	prefix := u.MqttPrefix
	if prefix == "" {
		prefix = "homeassistant"
	}
	base := mqttBase(u)
	var firstErr error
	pub := func(topic string, payload any) {
		var b []byte
		switch p := payload.(type) {
		case string:
			b = []byte(p)
		default:
			b, _ = json.Marshal(p)
		}
		if t := conn.client.Publish(topic, 1, true, b); !t.WaitTimeout(5*time.Second) || t.Error() != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("publish %s: %v", topic, t.Error())
			}
		}
	}
	seen := map[string]bool{}
	announce := func(id, stateTopic string, ents []mqttEntity, device map[string]any) {
		for _, e := range ents {
			topic := fmt.Sprintf("%s/%s/hammond_%s/%s/config", prefix, e.component, id, e.key)
			seen[topic] = true
			cfg := map[string]any{"name": e.name, "unique_id": "hammond_" + id + "_" + e.key, "state_topic": stateTopic,
				"value_template": "{{ value_json." + e.key + " }}", "availability_topic": base + "/status", "device": device,
				"json_attributes_topic": stateTopic, "origin": map[string]string{"name": "hammond", "url": "https://github.com/nenchi/hammond"}}
			for k, v := range map[string]string{"unit_of_measurement": e.unit, "device_class": e.deviceClass, "state_class": e.stateClass, "icon": e.icon} {
				if v != "" {
					cfg[k] = v
				}
			}
			if e.component == "binary_sensor" {
				delete(cfg, "json_attributes_topic")
			}
			if e.stateClass == "total" { // totals that start over: tell HA when
				for prefix, start := range map[string]string{"month_": "month_start", "trips_month": "month_start",
					"year_": "year_start", "trips_today": "day_start"} {
					if strings.HasPrefix(e.key, prefix) {
						cfg["last_reset_value_template"] = "{{ value_json." + start + " }}"
					}
				}
			}
			pub(topic, cfg)
		}
	}
	for _, v := range s.Vehicles {
		st := base + "/vehicle/" + v.ID
		// HA's distance units are lower case (km, mi); hammond's are "Km", "Mi"
		announce(v.ID, st, vehicleEntities(s.Currency, strings.ToLower(s.DistanceUnit)), map[string]any{"identifiers": []string{"hammond_" + v.ID},
			"name": "hammond " + v.Nickname, "manufacturer": strings.TrimSpace(v.Make + " " + v.Model), "model": v.Registration})
		pub(st, vehicleState(v))
	}
	for _, w := range s.Watches {
		st := base + "/watch/" + w.ID
		announce(w.ID, st, watchEntities(s.Currency), map[string]any{"identifiers": []string{"hammond_" + w.ID},
			"name": "hammond " + w.Name + " " + w.GradeName, "manufacturer": "hammond price watch", "model": w.Address})
		pub(st, watchState(w))
	}
	res := &MqttResult{Vehicles: len(s.Vehicles), Watches: len(s.Watches)}
	mqttMu.Lock()
	for topic := range conn.topics {
		if !seen[topic] {
			pub(topic, "") // empty retained config: HA removes the entity
			res.Removed++
		}
	}
	conn.topics = seen
	mqttMu.Unlock()
	return res, firstErr
}

// UpdateMqttSettings: password "" keeps the stored one, "-" removes it; an empty URL turns MQTT off
func UpdateMqttSettings(userID, rawURL, user, password, prefix string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL != "" {
		p, err := url.Parse(rawURL)
		if err != nil || p.Host == "" || len(rawURL) > 300 || !map[string]bool{"tcp": true, "mqtt": true, "ssl": true, "mqtts": true, "ws": true, "wss": true}[p.Scheme] {
			return errors.New("MQTT URL like mqtt://192.168.1.10:1883 (tcp, mqtt, mqtts/ssl, ws, wss)")
		}
	}
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if len(prefix) > 100 || strings.ContainsAny(prefix, "#+ ") || len(user) > 200 {
		return errors.New("invalid MQTT username or discovery prefix")
	}
	upd := map[string]interface{}{"mqtt_url": rawURL, "mqtt_user": strings.TrimSpace(user), "mqtt_prefix": prefix}
	// a password pasted from a terminal often brings a space or line break along: the broker then says
	// "not Authorized" although the password looks right
	switch password = strings.TrimSpace(password); password {
	case "":
	case "-":
		upd["mqtt_password"] = ""
	default:
		if len(password) > 200 {
			return errors.New("MQTT password too long")
		}
		upd["mqtt_password"] = password
	}
	if err := db.DB.Model(&db.User{}).Where("id = ?", userID).Updates(upd).Error; err != nil {
		return err
	}
	if rawURL == "" { // off: drop the connection (HA shows the entities unavailable via the last will)
		mqttMu.Lock()
		if c := mqttConns[userID]; c != nil {
			c.client.Publish("hammond/"+userID[:8]+"/status", 1, true, "offline").WaitTimeout(2 * time.Second) // a clean disconnect sends no last will
			c.client.Disconnect(250)
			delete(mqttConns, userID)
		}
		mqttMu.Unlock()
	}
	return nil
}
