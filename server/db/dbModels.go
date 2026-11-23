package db

import (
	"encoding/json"
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type User struct {
	Base
	Email        string       `gorm:"unique" json:"email"`
	Password     string       `json:"-"`
	Currency     string       `json:"currency"`
	DistanceUnit DistanceUnit `json:"distanceUnit"`
	DateFormat   string       `json:"dateFormat"`
	Role         Role         `json:"role"`
	Name         string       `json:"name"`
	Vehicles     []Vehicle    `gorm:"many2many:user_vehicles;" json:"vehicles"`
	IsDisabled   bool         `json:"isDisabled"`
	// "auto" (follow the device), "light" or "dark"; empty = auto
	Theme string `json:"theme"`
	// pre-fill new fill-ups with the vehicle's last price and station
	PrefillFillup bool `gorm:"default:true" json:"prefillFillup"`
	// what a detection (e.g. from Home Assistant) does when a matching fill-up already exists:
	// "ask" (empty), "manual" (my entry wins, ignore) or "detection" (write its values into my entry)
	DetectionDuplicateMode string `json:"detectionDuplicateMode"`
	// show the time of fill-ups in lists (entries without a time always show the date only)
	ShowFillupTime bool `gorm:"default:true" json:"showFillupTime"`
	// station prices in the fill-up form: "" = button + stations near a detection's location,
	// "button" = only the "stations nearby" button, "off"
	StationSuggestions string `json:"stationSuggestions"`
	// search radius of "Stations nearby" in km (1–25)
	StationRadiusKm int `gorm:"default:2" json:"stationRadiusKm"`
	// own fuel subtypes for the fill-up drop-down ("Q Max 95"), newline-separated
	CustomFuelSubtypes string `json:"customFuelSubtypes"`
	// own station price profiles (JSON like Setting.FuelPriceProfiles, keys included; never sent as is)
	FuelPriceProfiles string `json:"-"`
	// own Home Assistant (odometer, price sensors); the token is write-only
	HAURL   string `json:"haUrl"`
	HAToken string `json:"-"`
	// own EVCC: finished sessions of the user's vehicles become detections, priced at EvccPlaceID
	EvccURL         string     `json:"evccUrl"`
	EvccSyncMinutes int        `json:"evccSyncMinutes"` // 0 = only "Sync now"
	EvccPlaceID     string     `json:"evccPlaceId"`
	EvccSince       *time.Time `json:"evccSince"` // sessions that started before aren't imported
	// the car chat commands use when none is named and the user has several (`car 2`); "" = ask
	DefaultVehicleID string `json:"defaultVehicleId"`
	// own MQTT broker: dashboard values to Home Assistant (discovery); the password is write-only
	MqttURL      string `json:"mqttUrl"`
	MqttUser     string `json:"mqttUser"`
	MqttPassword string `json:"-"`
	MqttPrefix   string `json:"mqttPrefix"` // HA discovery prefix, "" = homeassistant
	// before v2.1: price alerts to Gotify; moved once into a NotifyChannel (MoveGotifyToChannels), then empty
	GotifyURL   string `json:"gotifyUrl"`
	GotifyToken string `json:"-"`

	// drives with a stop up to this many minutes between them are one trip (a fuel stop doesn't split it)
	TripMergeMinutes int `gorm:"default:15" json:"tripMergeMinutes"`
	// minutes between price watch checks per price source kind ("tankerkoenig": 60); missing = default
	PriceWatchIntervals map[string]int `gorm:"serializer:json" json:"priceWatchIntervals"`
}

func (b *User) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		User
		RoleDetail         EnumDetail `json:"roleDetail"`
		DistanceUnitDetail EnumDetail `json:"distanceUnitDetail"`
		HATokenSet         bool       `json:"haTokenSet"`
		GotifyTokenSet     bool       `json:"gotifyTokenSet"`
		MqttPasswordSet    bool       `json:"mqttPasswordSet"`
	}{
		User:               *b,
		HATokenSet:         b.HAToken != "",
		GotifyTokenSet:     b.GotifyToken != "",
		MqttPasswordSet:    b.MqttPassword != "",
		RoleDetail:         b.RoleDetail(),
		DistanceUnitDetail: b.DistanceUnitDetail(),
	})
}
func (v *User) RoleDetail() EnumDetail {
	return RoleDetails[v.Role]
}
func (v *User) DistanceUnitDetail() EnumDetail {
	return DistanceUnitDetails[v.DistanceUnit]
}

func (u *User) SetPassword(password string) error {
	if len(password) == 0 {
		return errors.New("password should not be empty")
	}
	bytePassword := []byte(password)
	// Make sure the second param `bcrypt generator cost` between [4, 32)
	passwordHash, _ := bcrypt.GenerateFromPassword(bytePassword, bcrypt.DefaultCost)
	u.Password = string(passwordHash)
	return nil
}

func (u *User) CheckPassword(password string) error {
	bytePassword := []byte(password)
	byteHashedPassword := []byte(u.Password)
	return bcrypt.CompareHashAndPassword(byteHashedPassword, bytePassword)
}

type Vehicle struct {
	Base
	Nickname          string   `json:"nickname"`
	Registration      string   `json:"registration"`
	Make              string   `json:"make"`
	Model             string   `json:"model"`
	YearOfManufacture int      `json:"yearOfManufacture"`
	EngineSize        float32  `json:"engineSize"`
	FuelUnit          FuelUnit `json:"fuelUnit"`
	FuelType          FuelType `json:"fuelType"`
	// litres (or kWh for electric); used to estimate quantities from detected fuel levels
	TankCapacity float32 `json:"tankCapacity"`
	// accepted grades for station prices, preferred first: "e10,e5"; empty = from the fuel type
	FuelGrade string `json:"fuelGrade"`
	// Home Assistant entity with the car's odometer (sensor.xyz_odometer): pre-fills new fill-ups
	OdometerEntity string `json:"odometerEntity"`
	// Home Assistant entities for the trip log (Trips); empty Ignition = no trips from HA
	TripEntities TripEntities `gorm:"serializer:json" json:"tripEntities"`
	// HA history is read from here on: drives starting before it are done (a deleted trip stays deleted)
	TripsSyncedTo *time.Time `json:"-"`
	// vehicle name in EVCC (its title): EVCC sessions of that vehicle are imported for this one
	EvccVehicle string `json:"evccVehicle"`
	// fuel gauge calibration: number of segments (default 4) and when learning was last reset
	GaugeSegments int        `gorm:"default:4" json:"gaugeSegments"`
	GaugeResetAt  *time.Time `json:"gaugeResetAt"`
	// car photo: an attachment, shown via a random link (an <img> and Home Assistant can't log in);
	// PhotoOrigin = the address the browser used when uploading, for Home Assistant's absolute link
	PhotoID     string       `json:"-"`
	PhotoToken  string       `json:"-"`
	PhotoOrigin string       `json:"-"`
	PhotoURL    string       `gorm:"-" json:"photoUrl"` // relative, "" = no photo
	PhotoHidden bool         `json:"photoHidden"`       // only for Home Assistant, not shown in hammond
	Users       []User       `gorm:"many2many:user_vehicles;" json:"users"`
	Fillups     []Fillup     `json:"fillups"`
	Expenses    []Expense    `json:"expenses"`
	Attachments []Attachment `gorm:"many2many:vehicle_attachments;" json:"attachments"`
	IsOwner     bool         `gorm:"->" json:"isOwner"`
}

func (b *Vehicle) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Vehicle
		FuelTypeDetail EnumDetail `json:"fuelTypeDetail"`
		FuelUnitDetail EnumDetail `json:"fuelUnitDetail"`
	}{
		Vehicle:        *b,
		FuelTypeDetail: b.FuelTypeDetail(),
		FuelUnitDetail: b.FuelUnitDetail(),
	})
}
func (v *Vehicle) FuelTypeDetail() EnumDetail {
	return FuelTypeDetails[v.FuelType]
}

func (v *Vehicle) FuelUnitDetail() EnumDetail {
	return FuelUnitDetails[v.FuelUnit]
}

type UserVehicle struct {
	Base
	UserID    string `gorm:"primaryKey"`
	User      User   `json:"user"`
	VehicleID string `gorm:"primaryKey"`
	IsOwner   bool   `json:"isOwner"`
}

type Fillup struct {
	Base
	VehicleID       string       `json:"vehicleId"`
	Vehicle         Vehicle      `json:"-"`
	FuelUnit        FuelUnit     `json:"fuelUnit"`
	FuelQuantity    float32      `json:"fuelQuantity"`
	PerUnitPrice    float32      `json:"perUnitPrice"`
	TotalAmount     float32      `json:"totalAmount"`
	OdoReading      int          `json:"odoReading"`
	IsTankFull      *bool        `json:"isTankFull"`
	HasMissedFillup *bool        `json:"hasMissedFillup"`
	Comments        string       `json:"comments"`
	FillingStation  string       `json:"fillingStation"`
	UserID          string       `json:"userId"`
	User            User         `json:"user"`
	Date            time.Time    `json:"date"`
	Currency        string       `json:"currency"`
	DistanceUnit    DistanceUnit `json:"distanceUnit"`
	Source          string       `json:"source"`
	FuelSubType     string       `json:"fuelSubType"`
}

func (v *Fillup) FuelUnitDetail() EnumDetail {
	return FuelUnitDetails[v.FuelUnit]
}
func (b *Fillup) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Fillup
		FuelUnitDetail EnumDetail `json:"fuelUnitDetail"`
	}{
		Fillup:         *b,
		FuelUnitDetail: b.FuelUnitDetail(),
	})
}

type Expense struct {
	Base
	VehicleID    string       `json:"vehicleId"`
	Vehicle      Vehicle      `json:"-"`
	Amount       float32      `json:"amount"`
	OdoReading   int          `json:"odoReading"`
	Comments     string       `json:"comments"`
	ExpenseType  string       `json:"expenseType"`
	UserID       string       `json:"userId"`
	User         User         `json:"user"`
	Date         time.Time    `json:"date"`
	Currency     string       `json:"currency"`
	DistanceUnit DistanceUnit `json:"distanceUnit"`
	Source       string       `json:"source"`
	// a cost of a trip (toll, parking, vignette…): shown with the trip and in the trip export
	TripID string `gorm:"index" json:"tripId"`
}

type Setting struct {
	Base
	Currency     string       `json:"currency" gorm:"default:INR"`
	DistanceUnit DistanceUnit `json:"distanceUnit" gorm:"default:1"`
	// station prices before profiles (v1.5): read once to convert into a profile, then cleared
	FuelPriceProvider string `json:"-"`
	FuelPriceURL      string `json:"-"`
	FuelPriceAPIKey   string `json:"-"`
	// station price profiles as JSON ([]PriceProfile, keys included): never sent as is,
	// GET /api/settings is readable by every user — PriceProfiles is the copy without keys
	FuelPriceProfiles string         `json:"-"`
	PriceProfiles     []PriceProfile `gorm:"-" json:"fuelPriceProfiles"`
	// Home Assistant of v1.6 (site-wide): moved once to the first admin (MoveSiteConnectionsToAdmin)
	HAURL   string `json:"-"`
	HAToken string `json:"-"`
	// trip log: only admins may change a trip's times and kilometres (users: purpose, notes, costs)
	TripsLocked bool `json:"tripsLocked"`
}

// PriceProfile: one station price source, tried in list order (the first with stations nearby wins).
// Kind "tankerkoenig" (Germany), "econtrol" (Austria), "goriva" (Slovenia), "mzoe" (Croatia) or "custom"
// (Tankerkönig-compatible URL).
type PriceProfile struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	URL    string `json:"url"`              // empty = the kind's default
	APIKey string `json:"apiKey,omitempty"` // request: "" keeps the stored key, "-" removes it
	KeySet bool   `json:"keySet"`
	// site profiles: only admins use it (zero value = shared, so v1.6 profiles stay shared)
	Private bool `json:"private"`
	Own     bool `json:"own"`     // in lists for a user: their own profile (not stored)
	Builtin bool `json:"builtin"` // a free source hammond adds by itself (not stored)
}

// PriceWatch: a favourite station whose price of one grade is checked in the background
type PriceWatch struct {
	Base
	UserID    string     `gorm:"index" json:"userId"`
	ProfileID string     `json:"profileId"` // price profile that found it (own, shared or "builtin-<kind>")
	Kind      string     `json:"kind"`      // the profile's kind: picks the check interval
	StationID string     `json:"stationId"`
	Name      string     `json:"name"`
	Address   string     `json:"address"`
	Lat       float64    `json:"lat"`
	Lng       float64    `json:"lng"`
	Grade     string     `json:"grade"`     // e5, e10 or diesel
	Below     *float64   `json:"below"`     // alert when the price is at or below; nil = only track
	LastPrice *float64   `json:"lastPrice"` // last price seen
	CheckedAt *time.Time `json:"checkedAt"`
	LastError string     `json:"lastError"`
	Failures  int        `json:"failures"` // checks in a row that failed
	Alerted   bool       `json:"alerted"`  // under the limit and already notified: no new alert until it's above again
}

// StationPrice: price history of a watch, one row per change
type StationPrice struct {
	ID      uint      `gorm:"primaryKey" json:"-"`
	WatchID string    `gorm:"index" json:"-"`
	Price   float64   `json:"price"`
	At      time.Time `json:"at"`
}

type Migration struct {
	Base
	Date time.Time
	Name string
}
type JobLock struct {
	Base
	Date     time.Time
	Name     string
	Duration int
}

type Attachment struct {
	Base
	Path         string `json:"path"`
	OriginalName string `json:"originalName"`
	Size         int64  `json:"size"`
	ContentType  string `json:"contentType"`
	Title        string `gorm:"->" json:"title"`
	UserID       string `json:"userId"`
	User         User   `json:"user"`
}

type QuickEntry struct {
	Base
	AttachmentID string     `json:"attachmentId"`
	Attachment   Attachment `json:"attachment"`
	ProcessDate  *time.Time `json:"processDate"`
	UserID       string     `json:"userId"`
	User         User       `json:"user"`
	Comments     string     `json:"comments"`
}

type VehicleAttachment struct {
	Base
	AttachmentID string `gorm:"primaryKey" json:"attachmentId"`
	VehicleID    string `gorm:"primaryKey" json:"vehicleId"`
	Title        string `json:"title"`
}

type VehicleAlert struct {
	Base
	VehicleID       string         `json:"vehicleId"`
	Vehicle         Vehicle        `json:"-"`
	UserID          string         `json:"userId"`
	User            User           `json:"user"`
	Title           string         `json:"title"`
	Comments        string         `json:"comments"`
	StartDate       time.Time      `json:"date"`
	StartOdoReading int            `json:"startOdoReading"`
	DistanceUnit    DistanceUnit   `json:"distanceUnit"`
	AlertFrequency  AlertFrequency `json:"alertFrequency"`
	OdoFrequency    int            `json:"odoFrequency"`
	DayFrequency    int            `json:"dayFrequency"`
	AlertAllUsers   bool           `json:"alertAllUsers"`
	IsActive        bool           `json:"isActive"`
	EndDate         *time.Time     `json:"endDate"`
	AlertType       AlertType      `json:"alertType"`
}
type AlertOccurance struct {
	Base
	VehicleID        string       `json:"vehicleId"`
	Vehicle          Vehicle      `json:"-"`
	VehicleAlertID   string       `json:"vehicleAlertId"`
	VehicleAlert     VehicleAlert `json:"-"`
	UserID           string       `json:"userId"`
	User             User         `json:"-"`
	OdoReading       int          `json:"odoReading"`
	Date             *time.Time   `json:"date"`
	ProcessDate      *time.Time   `json:"processDate"`
	AlertProcessType AlertType    `json:"alertProcessType"`
	CompleteDate     *time.Time   `json:"completeDate"`
}

type Notification struct {
	Base
	Title      string     `json:"title"`
	Content    string     `json:"content"`
	UserID     string     `json:"userId"`
	VehicleID  string     `json:"vehicleId"`
	User       User       `json:"-"`
	Date       time.Time  `json:"date"`
	ReadDate   *time.Time `json:"readDate"`
	ParentID   string     `json:"parentId"`
	ParentType string     `json:"parentType"`
}

// ChargingPlace: where an electric car charges (home, work, a public charger used often) and how a
// charge there is priced. Price per kWh = sum of the components (each: first matching rule, else its
// default price) + the Home Assistant price entity (time-weighted over the charge) when set.
type ChargingPlace struct {
	Base
	OwnerID     string            `gorm:"index" json:"ownerId"` // who made it: edits it, its HA prices the price sensor
	Shared      bool              `json:"shared"`               // all users can use it (only admins can share)
	Name        string            `json:"name"`
	Lat         *float64          `json:"lat"` // optional: picked automatically within RadiusM
	Lng         *float64          `json:"lng"`
	RadiusM     int               `gorm:"default:200" json:"radiusM"`
	Components  []TariffComponent `gorm:"serializer:json" json:"components"`
	PriceEntity string            `json:"priceEntity"` // HA sensor with a price per kWh, e.g. sensor.electricity_price
	SessionFee  float64           `json:"sessionFee"`  // per charge
	LossPercent float64           `json:"lossPercent"` // charging loss: billed kWh = kWh in the car / (1 - loss)
	Holidays    string            `json:"holidays"`    // non-working days, one per line: MM-DD (every year) or YYYY-MM-DD
}

// TariffComponent: one part of the price, e.g. "Energy" (high/low tariff) or "Network" (time blocks)
type TariffComponent struct {
	Name  string       `json:"name"`
	Price float64      `json:"price"` // when no rule matches
	Rules []TariffRule `json:"rules"` // first match wins
}

// TariffRule: months (empty = all), days ("all", "work" = Mon–Fri except holidays, "free" = weekend and
// holidays), local time window From–To ("HH:MM"; To ≤ From runs past midnight; 00:00–00:00 = all day)
type TariffRule struct {
	Label  string  `json:"label"` // e.g. "Block 2" or "NT"
	Months []int   `json:"months"`
	Days   string  `json:"days"`
	From   string  `json:"from"`
	To     string  `json:"to"`
	Price  float64 `json:"price"`
}

// TripEntities: the car's sensors in Home Assistant. Ignition (required) is driving when "on", "4" or "5"
// (Mercedes: 4 = on, 5 = engine running); the others are optional and make the log more exact.
type TripEntities struct {
	Ignition    string `json:"ignition"`
	FuelLevel   string `json:"fuelLevel"`   // % of the tank: fuel used when the car has no trip computer
	Distance    string `json:"distance"`    // trip computer: km since start (0.1 km instead of the whole-km odometer)
	Consumption string `json:"consumption"` // trip computer: l/100 km since start
	// start and end addresses: the car's own tracker, else the driver's phone (HA companion app)
	CarLocation   string `json:"carLocation"`
	PhoneLocation string `json:"phoneLocation"`
}

// TripLeg: one drive, ignition on → off (from Home Assistant history, or entered by hand)
type TripLeg struct {
	Base
	VehicleID string    `gorm:"index" json:"vehicleId"`
	TripID    string    `gorm:"index" json:"tripId"`
	Start     time.Time `gorm:"column:started_at;index" json:"start"` // not "start"/"end": reserved words in SQL
	End       time.Time `gorm:"column:ended_at" json:"end"`
	StartOdo  float64   `json:"startOdo"`
	EndOdo    float64   `json:"endOdo"`
	Distance  float64   `json:"distance"`  // trip computer when known, else EndOdo − StartOdo
	FuelStart *float64  `json:"fuelStart"` // tank %
	FuelEnd   *float64  `json:"fuelEnd"`
	FuelUsed  *float64  `json:"fuelUsed"` // litres (or kWh); nil = unknown
	// FuelUsed is the car's own (trip computer), not from the gauge: the gauge calibration learns from it
	FuelFromCar bool   `json:"fuelFromCar"`
	Source      string `json:"source"` // "ha" or "manual"
	// where the drive started and ended (CarLocation, else PhoneLocation); addresses filled in afterwards
	StartLat     *float64 `json:"startLat"`
	StartLng     *float64 `json:"startLng"`
	EndLat       *float64 `json:"endLat"`
	EndLng       *float64 `json:"endLng"`
	LocationFrom string   `json:"locationFrom"` // "car" or "phone" (the phone when either end came from it)
	StartAddress string   `json:"startAddress"`
	EndAddress   string   `json:"endAddress"`
	// the positions during the drive, [[lat, lng], …] for the map (same source as LocationFrom)
	Route [][2]float64 `gorm:"serializer:json" json:"route"`
}

// Trip: drives with short stops between them; purpose and notes for a business log
type Trip struct {
	Base
	VehicleID string `gorm:"index" json:"vehicleId"`
	Business  bool   `json:"business"`
	Purpose   string `json:"purpose"`
	Notes     string `json:"notes"`
}

// NotifyChannel: where a user's alerts go (Settings → Notifications); several per user, each on/off
type NotifyChannel struct {
	Base
	UserID  string `gorm:"index" json:"-"`
	Kind    string `json:"kind"` // gotify, ntfy, matrix, telegram
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// gotify: server URL; ntfy: topic URL (https://ntfy.sh/my-topic); matrix: homeserver URL
	URL string `json:"url"`
	// gotify: app token; ntfy: access token (optional); matrix: access token; telegram: bot token. Write-only.
	Secret string `json:"-"`
	Room   string `json:"room"` // matrix: room ID (!abc:server) or alias (#fuel:server)
	ChatID int64  `json:"-"`    // telegram: the chat linked with /start <code>
	// matrix with commands: the user linked with /start <code> (only they get answers)
	MatrixUser string `json:"-"`
	// telegram: also answer the fuel commands (prices, graph, trips…) in the linked chat
	Commands bool `json:"commands"`
}

// PhotoPath: public link of a vehicle photo, + its random token (no login: <img> tags and Home Assistant)
const PhotoPath = "/api/public/photo/"

func (v *Vehicle) AfterFind(tx *gorm.DB) error {
	if v.PhotoToken != "" {
		v.PhotoURL = PhotoPath + v.PhotoToken
	}
	return nil
}
