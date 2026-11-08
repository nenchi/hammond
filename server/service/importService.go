package service

import (
	"bytes"
	"encoding/csv"
	"strconv"
	"strings"
	"time"

	"github.com/akhilrex/hammond/db"
	"github.com/leekchan/accounting"
)

func FuellyImport(content []byte, userId string) []string {
	stream := bytes.NewReader(content)
	reader := csv.NewReader(stream)
	reader.FieldsPerRecord = -1 // Fuelly has 18 columns, hammond's export 19 ("Missed")
	records, err := reader.ReadAll()

	var errors []string
	if err != nil {
		errors = append(errors, err.Error())
		return errors
	}

	vehicles, err := GetUserVehicles(userId)
	if err != nil {
		errors = append(errors, err.Error())
		return errors
	}
	user, err := GetUserById(userId)

	if err != nil {
		errors = append(errors, err.Error())
		return errors
	}

	var vehicleMap map[string]db.Vehicle = make(map[string]db.Vehicle)
	for _, vehicle := range *vehicles {
		vehicleMap[vehicle.Nickname] = vehicle
	}

	var fillups []db.Fillup
	var expenses []db.Expense
	layout := "2006-01-02 15:04"
	altLayout := "2006-01-02 3:04 PM"

	for index, record := range records {
		if index == 0 {
			continue
		}

		var vehicle db.Vehicle
		var ok bool
		if vehicle, ok = vehicleMap[record[4]]; !ok {
			errors = append(errors, "Found an unmapped vehicle entry at row "+strconv.Itoa(index+1))
		}
		dateStr := record[2] + " " + record[3]
		date, err := time.Parse(layout, dateStr)
		if err != nil {
			date, err = time.Parse(altLayout, dateStr)
		}
		if err != nil {
			errors = append(errors, "Found an invalid date/time at row "+strconv.Itoa(index+1))
		}

		if len(record) < 18 {
			errors = append(errors, "Expected 18 columns at row "+strconv.Itoa(index+1))
			continue
		}
		totalCost64, err := parseAmount(record[9], 3, user.Currency)
		if err != nil {
			errors = append(errors, "Found an invalid total cost at row "+strconv.Itoa(index+1))
		}

		totalCost := float32(totalCost64)
		odo64, err := parseAmount(record[5], 0, user.Currency)
		odoreading := int(odo64)
		if err != nil {
			errors = append(errors, "Found an invalid odo reading at row "+strconv.Itoa(index+1))
		}
		location := record[12]

		//Create Fillup
		if record[0] == "Gas" {
			ratet64, err := parseAmount(record[7], 3, user.Currency)
			if err != nil {
				errors = append(errors, "Found an invalid cost per gallon at row "+strconv.Itoa(index+1))
			}
			rate := float32(ratet64)

			quantity64, err := parseAmount(record[8], 3, user.Currency)
			if err != nil {
				errors = append(errors, "Found an invalid quantity at row "+strconv.Itoa(index+1))
			}
			quantity := float32(quantity64)

			// only the fields that have something; a hammond export has just the notes
			notes := joinNotes(record[16], "Gas Brand", record[11], "Tags", record[13], "Payment Type", record[14],
				"Tire Pressure", record[15], "MPG", record[1])

			isTankFull := record[6] == "Full"
			fal := len(record) > 18 && record[18] == "1" // hammond's "Missed" column
			fillups = append(fillups, db.Fillup{
				VehicleID:       vehicle.ID,
				FuelUnit:        vehicle.FuelUnit,
				FuelQuantity:    quantity,
				PerUnitPrice:    rate,
				TotalAmount:     totalCost,
				OdoReading:      odoreading,
				IsTankFull:      &isTankFull,
				Comments:        notes,
				FillingStation:  location,
				HasMissedFillup: &fal,
				UserID:          userId,
				Date:            date,
				Currency:        user.Currency,
				DistanceUnit:    user.DistanceUnit,
				Source:          "Fuelly",
				FuelSubType:     record[10], // Fuelly's octane; hammond exports the fuel subtype here
			})

		}
		if record[0] == "Service" {
			notes := joinNotes(record[16], "Tags", record[13], "Payment Type", record[14])
			expenses = append(expenses, db.Expense{
				VehicleID:    vehicle.ID,
				Amount:       totalCost,
				OdoReading:   odoreading,
				Comments:     notes,
				ExpenseType:  record[17],
				UserID:       userId,
				Currency:     user.Currency,
				Date:         date,
				DistanceUnit: user.DistanceUnit,
				Source:       "Fuelly",
			})
		}

	}
	if len(errors) != 0 {
		return errors
	}

	// importing the same file again (e.g. restoring an export) must not duplicate anything:
	// skip entries that already exist (same vehicle and odometer, same day, same quantity / amount)
	fillups = skipExistingFillups(fillups)
	expenses = skipExistingExpenses(expenses)

	tx := db.DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()
	if err := tx.Error; err != nil {
		errors = append(errors, err.Error())
		return errors
	}
	if len(fillups) > 0 {
		if err := tx.Create(&fillups).Error; err != nil {
			tx.Rollback()
			errors = append(errors, err.Error())
			return errors
		}
	}
	if len(expenses) > 0 {
		if err := tx.Create(&expenses).Error; err != nil {
			tx.Rollback()
			errors = append(errors, err.Error())
			return errors
		}
	}
	err = tx.Commit().Error
	if err != nil {
		errors = append(errors, err.Error())
	}
	return errors
}

// parseAmount reads "22.69" as 22.69 for every currency; only numbers that aren't plain decimals go
// through the currency format. Upstream always used the currency format, so for EUR ("," decimal)
// a Fuelly "22.69" became 2269.
func parseAmount(s string, precision int, currency string) (float64, error) {
	s = strings.TrimSpace(s)
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return v, nil
	}
	return strconv.ParseFloat(accounting.UnformatNumber(s, precision, currency), 64)
}

func joinNotes(notes string, labelValue ...string) string {
	var parts []string
	for i := 0; i+1 < len(labelValue); i += 2 {
		if v := strings.TrimSpace(labelValue[i+1]); v != "" {
			parts = append(parts, labelValue[i]+": "+v)
		}
	}
	if n := strings.TrimSpace(notes); n != "" {
		parts = append(parts, n)
	}
	return strings.Join(parts, "\n")
}

func sameDay(t time.Time) (time.Time, time.Time) {
	return t.Add(-12 * time.Hour), t.Add(12 * time.Hour)
}

func skipExistingFillups(in []db.Fillup) []db.Fillup {
	out := in[:0]
	for _, f := range in {
		from, to := sameDay(f.Date)
		var n int64
		db.DB.Model(&db.Fillup{}).Where("vehicle_id = ? AND odo_reading = ? AND date BETWEEN ? AND ? AND ABS(fuel_quantity - ?) < 0.01",
			f.VehicleID, f.OdoReading, from, to, f.FuelQuantity).Count(&n)
		if n == 0 {
			out = append(out, f)
		}
	}
	return out
}

func skipExistingExpenses(in []db.Expense) []db.Expense {
	out := in[:0]
	for _, e := range in {
		from, to := sameDay(e.Date)
		var n int64
		db.DB.Model(&db.Expense{}).Where("vehicle_id = ? AND odo_reading = ? AND date BETWEEN ? AND ? AND ABS(amount - ?) < 0.01",
			e.VehicleID, e.OdoReading, from, to, e.Amount).Count(&n)
		if n == 0 {
			out = append(out, e)
		}
	}
	return out
}
