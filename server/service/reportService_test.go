package service

import (
	"testing"
	"time"

	"github.com/akhilrex/hammond/db"
)

// The first fill-up inside the range needs the last one before it as its baseline;
// the baseline itself must not become a chart point.
func TestMileageUsesFillupBeforeRange(t *testing.T) {
	t.Setenv("CONFIG", t.TempDir())
	var err error
	if db.DB, err = db.Init(); err != nil {
		t.Fatal(err)
	}
	db.Migrate()

	v := db.Vehicle{Nickname: "t"}
	if err := db.DB.Create(&v).Error; err != nil {
		t.Fatal(err)
	}
	full := true
	day := func(d int) time.Time { return time.Date(2026, 10, d, 10, 0, 0, 0, time.UTC) }
	for _, f := range []db.Fillup{
		{VehicleID: v.ID, OdoReading: 10000, FuelQuantity: 40, TotalAmount: 60, IsTankFull: &full, Date: day(1)},
		{VehicleID: v.ID, OdoReading: 10600, FuelQuantity: 40, TotalAmount: 60, IsTankFull: &full, Date: day(20)},
	} {
		if err := db.DB.Create(&f).Error; err != nil {
			t.Fatal(err)
		}
	}

	got, err := GetMileageByVehicleId(v.ID, day(15)) // only the 20th is in range
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].OdoReading != 10600 || got[0].Mileage != 15 {
		t.Fatalf("want one point: odo 10600, 600 km / 40 l = 15; got %+v", got)
	}

	got, _ = GetMileageByVehicleId(v.ID, day(25)) // nothing in range
	if len(got) != 0 {
		t.Fatalf("want no points after the last fill-up, got %+v", got)
	}
}
