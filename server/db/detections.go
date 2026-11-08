package db

import "time"

// ApiKey lets an external system (Home Assistant, Node-RED, scripts) call /api/v1 as a user.
// Only the SHA-256 of the key is stored; the key itself is shown once on creation.
type ApiKey struct {
	Base
	UserID     string     `gorm:"index" json:"userId"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"` // first characters, to recognise the key in the list
	Hash       string     `gorm:"uniqueIndex" json:"-"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
}

type DetectionStatus string

const (
	DetectionPending   DetectionStatus = "pending"   // waiting for the user
	DetectionConfirmed DetectionStatus = "confirmed" // turned into a fill-up
	DetectionDiscarded DetectionStatus = "discarded" // user said no / "keep mine"
	DetectionMerged    DetectionStatus = "merged"    // matched a fill-up the user entered
	DetectionApplied   DetectionStatus = "applied"   // its values were written into an existing fill-up
	DetectionIgnored   DetectionStatus = "ignored"   // duplicate, ignored by the "my entry wins" setting
)

// FillupDetection is a fill-up suggested by an external system (e.g. fuel level jumped in Home
// Assistant). It never counts in stats; the user completes it into a real Fillup or discards it.
type FillupDetection struct {
	Base
	VehicleID     string          `gorm:"index" json:"vehicleId"`
	UserID        string          `json:"userId"`
	Source        string          `json:"source"`
	Date          time.Time       `json:"date"`
	OdoReading    int             `json:"odoReading"`
	LevelBefore   *float32        `json:"levelBefore"`  // %
	LevelAfter    *float32        `json:"levelAfter"`   // %
	OdoAtBefore   *int            `json:"odoAtBefore"`  // odometer when LevelBefore was read
	FuelQuantity  float32         `json:"fuelQuantity"` // given, or estimated from levels × tank size
	Estimated     bool            `json:"estimated"`    // FuelQuantity came from the levels
	Uncertain     bool            `json:"uncertain"`    // LevelBefore was read well before the fill-up
	UncertainNote string          `json:"uncertainNote"`
	IsTankFull    bool            `json:"isTankFull"`
	Lat           *float64        `json:"lat"` // where the phone/car was when it was detected (optional)
	Lng           *float64        `json:"lng"`
	ExternalID    string          `gorm:"index" json:"externalId"` // e.g. "evcc:42": imported once
	ChargeEnd     *time.Time      `json:"chargeEnd"`               // electric: when the charge finished
	PerUnitPrice  *float32        `json:"perUnitPrice"`            // when the source knows the price
	Station       string          `json:"station"`                 // filling station / charging place
	Status        DetectionStatus `gorm:"index" json:"status"`
	FillupID      *string         `json:"fillupId"`      // fill-up it became / was merged into / applied to
	DuplicateOfID *string         `json:"duplicateOfId"` // existing fill-up it probably duplicates
	DuplicateOf   *Fillup         `gorm:"foreignKey:DuplicateOfID" json:"duplicateOf,omitempty"`
}
