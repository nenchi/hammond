package models

import (
	"time"

	"github.com/akhilrex/hammond/db"
)

type UpdateSettingModel struct {
	Currency     string           `json:"currency" form:"currency" query:"currency"`
	DateFormat   string           `json:"dateFormat" form:"dateFormat" query:"dateFormat"`
	DistanceUnit *db.DistanceUnit `json:"distanceUnit" form:"distanceUnit" query:"distanceUnit" `
	// optional: older clients don't send them, nil keeps the stored value
	Theme                  *string `json:"theme" form:"theme" binding:"omitempty,oneof=auto light dark"`
	PrefillFillup          *bool   `json:"prefillFillup" form:"prefillFillup"`
	DetectionDuplicateMode *string `json:"detectionDuplicateMode" form:"detectionDuplicateMode" binding:"omitempty,oneof=ask manual detection"`
	ShowFillupTime         *bool   `json:"showFillupTime" form:"showFillupTime"`
	StationSuggestions     *string `json:"stationSuggestions" form:"stationSuggestions" binding:"omitempty,oneof=location button off"`
	CustomFuelSubtypes     *string `json:"customFuelSubtypes" form:"customFuelSubtypes" binding:"omitempty,max=1000"`
}

// UpdateFuelPriceSettingsRequest (admin): the whole profile list, in order. Per profile apiKey
// empty = keep the stored one (matched by id), "-" = remove it. Checked by service.UpdatePriceProfiles.
type UpdateFuelPriceSettingsRequest struct {
	Profiles []db.PriceProfile `json:"profiles" binding:"max=10"`
}

// UpdateHomeAssistantRequest (admin): token empty = keep, "-" = remove
type UpdateHomeAssistantRequest struct {
	URL   string `json:"url" binding:"omitempty,url,max=300"`
	Token string `json:"token" binding:"max=500"`
}

// CreateDetectionRequest: POST /api/v1/vehicles/:id/detections. Send fuelQuantity, or the fuel level
// before and after in % (hammond estimates with the vehicle's tank size).
type CreateDetectionRequest struct {
	OdoReading   int        `json:"odoReading" binding:"required,min=1"`
	Date         *time.Time `json:"date"`
	FuelQuantity *float32   `json:"fuelQuantity" binding:"omitempty,gt=0"`
	LevelBefore  *float32   `json:"levelBefore" binding:"omitempty,min=0,max=100"`
	LevelAfter   *float32   `json:"levelAfter" binding:"omitempty,min=0,max=100"`
	// odometer when levelBefore was read: if it's well below odoReading, the estimate is marked uncertain
	OdoAtBefore *int   `json:"odoAtBefore" binding:"omitempty,min=0"`
	IsTankFull  *bool  `json:"isTankFull"`
	Source      string `json:"source" binding:"max=64"`
	// optional position (phone or car) when it was detected: the fill-up form lists stations near it
	Lat *float64 `json:"lat" binding:"omitempty,min=-90,max=90"`
	Lng *float64 `json:"lng" binding:"omitempty,min=-180,max=180"`
}

type CreateApiKeyRequest struct {
	Name string `json:"name" binding:"required,max=64"`
}

type ClarksonMigrationModel struct {
	Url string `json:"url" form:"url" query:"url"`
}
