package service

import (
	"bytes"
	"compress/gzip"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"time"

	"github.com/akhilrex/hammond/db"
)

// Fuelly's CSV columns: what the Fuelly import reads, so an export can be imported again.
// "Missed" is ours (hammond's "missed the fill-up before this one"); the import reads it if present.
var fuellyHeader = []string{"Type", "MPG", "Date", "Time", "Car Name", "Odometer", "Fill Amount", "Price",
	"Fuel Quantity", "Total Cost", "Octane", "Gas Brand", "Location", "Tags", "Payment Type", "Tire Pressure",
	"Notes", "Services", "Missed"}

func num(f float32) string { return strconv.FormatFloat(float64(f), 'f', -1, 32) }

// ExportVehicleCSV: all fill-ups and expenses of a vehicle, oldest first, in Fuelly format.
func ExportVehicleCSV(vehicleID string) ([]byte, string, error) {
	vehicle, err := db.GetVehicleById(vehicleID)
	if err != nil {
		return nil, "", err
	}
	var fillups []db.Fillup
	var expenses []db.Expense
	if err := db.DB.Where("vehicle_id = ?", vehicleID).Find(&fillups).Error; err != nil {
		return nil, "", err
	}
	if err := db.DB.Where("vehicle_id = ?", vehicleID).Find(&expenses).Error; err != nil {
		return nil, "", err
	}
	type row struct {
		date time.Time
		rec  []string
	}
	var rows []row
	for _, f := range fillups {
		fill, missed := "Partial", ""
		if f.IsTankFull != nil && *f.IsTankFull {
			fill = "Full"
		}
		if f.HasMissedFillup != nil && *f.HasMissedFillup {
			missed = "1"
		}
		rows = append(rows, row{f.Date, []string{"Gas", "", f.Date.Format("2006-01-02"), f.Date.Format("15:04"),
			vehicle.Nickname, strconv.Itoa(f.OdoReading), fill, num(f.PerUnitPrice), num(f.FuelQuantity),
			num(f.TotalAmount), f.FuelSubType, "", f.FillingStation, "", "", "", f.Comments, "", missed}})
	}
	for _, e := range expenses {
		rows = append(rows, row{e.Date, []string{"Service", "", e.Date.Format("2006-01-02"), e.Date.Format("15:04"),
			vehicle.Nickname, strconv.Itoa(e.OdoReading), "", "", "", num(e.Amount), "", "", "", "", "", "",
			e.Comments, e.ExpenseType, ""}})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].date.Before(rows[j].date) })

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.Write(fuellyHeader)
	for _, r := range rows {
		w.Write(r.rec)
	}
	w.Flush()
	name := fmt.Sprintf("hammond-%s-%s.csv", sanitizeFileName(vehicle.Nickname), time.Now().Format("2006-01-02"))
	return buf.Bytes(), name, w.Error()
}

func sanitizeFileName(s string) string {
	out := []rune{}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			out = append(out, r)
		} else if len(out) > 0 && out[len(out)-1] != '-' {
			out = append(out, '-')
		}
	}
	if len(out) == 0 {
		return "vehicle"
	}
	return string(out)
}

// SnapshotDatabase writes a consistent copy of the live SQLite database (VACUUM INTO). Copying the
// file itself while hammond writes can give a broken copy.
func SnapshotDatabase(dst string) error {
	os.Remove(dst)
	return db.DB.Exec("VACUUM INTO ?", dst).Error
}

// DatabaseBackupGzip: a gzip'ed snapshot of the whole database, for the "Download backup" button.
// Restore: stop hammond, gunzip it to /config/hammond.db, start hammond.
func DatabaseBackupGzip(w io.Writer) error {
	tmp := path.Join(os.TempDir(), fmt.Sprintf("hammond-snapshot-%d.db", time.Now().UnixNano()))
	defer os.Remove(tmp)
	if err := SnapshotDatabase(tmp); err != nil {
		return err
	}
	f, err := os.Open(tmp)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(w)
	if _, err := io.Copy(gz, f); err != nil {
		return err
	}
	return gz.Close()
}
