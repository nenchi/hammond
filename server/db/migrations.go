package db

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

type localMigration struct {
	Name  string
	Query string
}

var migrations = []localMigration{
	{
		Name:  "2021_06_24_04_42_SetUserDisabledFalse",
		Query: "update users set is_disabled=0",
	},
	{
		Name:  "2021_02_07_00_09_LowerCaseEmails",
		Query: "update users set email=lower(email)",
	},
	{
		// drives synced before FuelFromCar existed: the trip computer's whenever the vehicle has its entities.
		// ponytail: a drive that fell back to the gauge (no trip computer values then) is marked too; rare
		Name: "2026_11_23_TripLegFuelFromCar",
		Query: `update trip_legs set fuel_from_car = 1 where source = 'ha' and fuel_used is not null and vehicle_id in
			(select id from vehicles where json_extract(trip_entities, '$.consumption') <> '' and json_extract(trip_entities, '$.distance') <> '')`,
	},
}

func RunMigrations() {
	for _, mig := range migrations {
		ExecuteAndSaveMigration(mig.Name, mig.Query)
	}
}
func ExecuteAndSaveMigration(name string, query string) error {
	var migration Migration
	result := DB.Where("name=?", name).First(&migration)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		fmt.Println(query)
		result = DB.Exec(query)
		if result.Error == nil {
			DB.Save(&Migration{
				Date: time.Now(),
				Name: name,
			})
		}
		return result.Error
	}
	return nil
}
