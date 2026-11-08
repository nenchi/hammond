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
func UpdateUserSettings(userId, currency string, distanceUnit db.DistanceUnit, dateFormat string, theme *string, prefillFillup *bool) error {
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
	return db.UpdateUser(user)
}

func GetSettings() *db.Setting {
	return db.GetOrCreateSetting()
}
