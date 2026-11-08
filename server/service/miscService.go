package service

import (
	"github.com/akhilrex/hammond/db"
)

func CanInitializeSystem() (bool, error) {
	return db.CanInitializeSystem()
}

func UpdateSettings(currency string, distanceUnit db.DistanceUnit) error {
	setting := db.GetOrCreateSetting()
	setting.Currency = currency
	setting.DistanceUnit = distanceUnit
	return db.UpdateSettings(setting)
}
func UpdateUserSettings(userId, currency string, distanceUnit db.DistanceUnit, dateFormat string, theme *string, prefillFillup *bool, duplicateMode *string, showFillupTime *bool, stationSuggestions *string, customFuelSubtypes *string, stationRadiusKm *int) error {
	user, err := db.GetUserById(userId)
	if err != nil {
		return err
	}

	user.Currency = currency
	user.DistanceUnit = distanceUnit
	user.DateFormat = dateFormat
	if theme != nil {
		user.Theme = *theme
	}
	if prefillFillup != nil {
		user.PrefillFillup = *prefillFillup
	}
	if duplicateMode != nil {
		user.DetectionDuplicateMode = *duplicateMode
	}
	if showFillupTime != nil {
		user.ShowFillupTime = *showFillupTime
	}
	if stationSuggestions != nil {
		user.StationSuggestions = *stationSuggestions
	}
	if stationRadiusKm != nil {
		user.StationRadiusKm = *stationRadiusKm
	}
	if customFuelSubtypes != nil {
		user.CustomFuelSubtypes = NormalizeFuelSubtypes(*customFuelSubtypes)
	}
	return db.UpdateUser(user)
}

func GetSettings(isAdmin bool) *db.Setting {
	s := db.GetOrCreateSetting()
	s.PriceProfiles = PublicPriceProfiles(s, isAdmin)
	return s
}
