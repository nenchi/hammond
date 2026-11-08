package service

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/akhilrex/hammond/db"
	"github.com/akhilrex/hammond/models"
)

func TestParseAmountEUR(t *testing.T) {
	for in, want := range map[string]float64{"22.69": 22.69, "22,69": 22.69, "1.234,56": 1234.56, " 81063 ": 81063} {
		if got, err := parseAmount(in, 3, "EUR"); err != nil || got != want {
			t.Errorf("parseAmount(%q, EUR) = %v, %v; want %v (upstream gave 2269 for 22.69)", in, got, err, want)
		}
	}
}

// export → delete → import restores the same entries; importing again adds nothing
func TestExportImportRoundTrip(t *testing.T) {
	setupDB(t)
	u, v := mkUserVehicle(t, "", 43)
	db.DB.Model(&u).Updates(map[string]interface{}{"currency": "EUR"})
	full, partial, unit := true, false, db.LITRE
	missed := true
	for _, f := range []models.CreateFillupRequest{
		{OdoReading: 80864, FuelQuantity: 30.5, PerUnitPrice: 1.689, TotalAmount: 51.51, IsTankFull: &full, FillingStation: "OMV, Ljubljana", Comments: "a \"quoted\" note"},
		{OdoReading: 81063, FuelQuantity: 12.14, PerUnitPrice: 1.869, TotalAmount: 22.69, IsTankFull: &partial, HasMissedFillup: &missed, FuelSubType: "95"},
	} {
		f.VehicleID, f.UserID, f.FuelUnit, f.Date = v.ID, u.ID, &unit, time.Date(2026, 11, 7, 18, 30, 0, 0, time.UTC)
		if _, err := CreateFillup(f); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := CreateExpense(models.CreateExpenseRequest{VehicleID: v.ID, UserID: u.ID, Amount: 89.9, OdoReading: 81000,
		ExpenseType: "Service", Comments: "oil", Date: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}

	csvData, name, err := ExportVehicleCSV(v.ID)
	if err != nil || !strings.HasPrefix(name, "hammond-A180-") {
		t.Fatalf("export: %v %s", err, name)
	}
	lines := strings.Split(strings.TrimSpace(string(csvData)), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[1], "Service,") {
		t.Fatalf("want header + 3 rows, oldest (the expense) first:\n%s", csvData)
	}

	db.DB.Where("vehicle_id = ?", v.ID).Delete(&db.Fillup{})
	db.DB.Where("vehicle_id = ?", v.ID).Delete(&db.Expense{})
	if errs := FuellyImport(csvData, u.ID); len(errs) != 0 {
		t.Fatalf("import errors: %v", errs)
	}
	var fs []db.Fillup
	db.DB.Where("vehicle_id = ?", v.ID).Order("odo_reading").Find(&fs)
	if len(fs) != 2 {
		t.Fatalf("want 2 fill-ups back, got %d", len(fs))
	}
	a, b := fs[0], fs[1]
	if a.TotalAmount != 51.51 || a.PerUnitPrice != 1.689 || a.FillingStation != "OMV, Ljubljana" || a.Comments != "a \"quoted\" note" || !*a.IsTankFull {
		t.Fatalf("first fill-up changed: %+v", a)
	}
	if b.TotalAmount != 22.69 || b.FuelQuantity != 12.14 || *b.IsTankFull || !*b.HasMissedFillup || b.FuelSubType != "95" {
		t.Fatalf("second fill-up changed (EUR amount, partial, missed, subtype): %+v", b)
	}
	var es []db.Expense
	db.DB.Where("vehicle_id = ?", v.ID).Find(&es)
	if len(es) != 1 || es[0].Amount != 89.9 || es[0].ExpenseType != "Service" || es[0].Comments != "oil" {
		t.Fatalf("expense changed: %+v", es)
	}

	if errs := FuellyImport(csvData, u.ID); len(errs) != 0 {
		t.Fatalf("second import errors: %v", errs)
	}
	var n int64
	db.DB.Model(&db.Fillup{}).Where("vehicle_id = ?", v.ID).Count(&n)
	if n != 2 {
		t.Fatalf("importing the same file again must not duplicate: %d fill-ups", n)
	}
}

func TestDatabaseBackupIsSQLite(t *testing.T) {
	setupDB(t)
	mkUserVehicle(t, "", 43)
	var buf strings.Builder
	if err := DatabaseBackupGzip(&buf); err != nil {
		t.Fatal(err)
	}
	if b := buf.String(); len(b) < 100 || b[0] != 0x1f || b[1] != 0x8b {
		t.Fatalf("want gzip data, got %d bytes", len(b))
	}
}

func TestAutomaticBackupContainsDatabase(t *testing.T) {
	setupDB(t)
	mkUserVehicle(t, "", 43)
	name, err := CreateBackup()
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path.Join(os.Getenv("CONFIG"), "backups", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	h, err := tr.Next()
	if err != nil || h.Name != "hammond.db" {
		t.Fatalf("want hammond.db in the archive, got %v %v", h, err)
	}
	head := make([]byte, 16)
	io.ReadFull(tr, head)
	if string(head[:15]) != "SQLite format 3" {
		t.Fatalf("not an SQLite database: %q", head)
	}
	if _, err := os.Stat(path.Join(os.Getenv("CONFIG"), "backups", ".snapshot-hammond.db")); !os.IsNotExist(err) {
		t.Fatal("temporary snapshot left behind")
	}
}
