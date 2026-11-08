package service

import (
	"testing"
	"time"

	"github.com/akhilrex/hammond/db"
	"github.com/akhilrex/hammond/models"
)

func setupDB(t *testing.T) {
	t.Helper()
	t.Setenv("CONFIG", t.TempDir())
	var err error
	if db.DB, err = db.Init(); err != nil {
		t.Fatal(err)
	}
	db.Migrate()
}

func mkUserVehicle(t *testing.T, mode string, tank float32) (db.User, db.Vehicle) {
	t.Helper()
	u := db.User{Email: "u@example.com", Name: "u", DetectionDuplicateMode: mode}
	if err := db.DB.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	v := db.Vehicle{Nickname: "A180", TankCapacity: tank}
	if err := db.DB.Create(&v).Error; err != nil {
		t.Fatal(err)
	}
	db.DB.Create(&db.UserVehicle{UserID: u.ID, VehicleID: v.ID, IsOwner: true})
	return u, v
}

func f32(f float32) *float32 { return &f }
func iptr(i int) *int        { return &i }

var at = time.Date(2026, 11, 7, 18, 0, 0, 0, time.UTC)

func TestDetectionEstimatesFromLevels(t *testing.T) {
	setupDB(t)
	u, v := mkUserVehicle(t, "", 43)
	res, err := CreateDetection(u.ID, v.ID, models.CreateDetectionRequest{
		OdoReading: 81068, Date: &at, LevelBefore: f32(74), LevelAfter: f32(99), OdoAtBefore: iptr(81066), Source: "Home Assistant"})
	if err != nil {
		t.Fatal(err)
	}
	d := res.Detection
	if res.Result != "pending" || !d.Estimated || d.FuelQuantity != 10.8 || !d.IsTankFull || d.Uncertain {
		t.Fatalf("want pending ~10.8 l (25%% of 43), full, certain; got %s %+v", res.Result, d)
	}
	// your 5 Nov case: last reading 15 km before the fill-up -> uncertain
	res, _ = CreateDetection(u.ID, v.ID, models.CreateDetectionRequest{
		OdoReading: 81300, Date: &at, LevelBefore: f32(82), LevelAfter: f32(99), OdoAtBefore: iptr(81285)})
	if !res.Detection.Uncertain {
		t.Fatalf("reading 15 km before the fill-up must be uncertain: %+v", res.Detection)
	}
	// no tank size -> a clear error instead of a wrong number
	_, v2 := mkUserVehicleNoUser(t)
	db.DB.Create(&db.UserVehicle{UserID: u.ID, VehicleID: v2.ID})
	if _, err := CreateDetection(u.ID, v2.ID, models.CreateDetectionRequest{OdoReading: 1, LevelBefore: f32(10), LevelAfter: f32(90)}); err == nil {
		t.Fatal("want an error without tank size")
	}
}

func mkUserVehicleNoUser(t *testing.T) (db.User, db.Vehicle) {
	v := db.Vehicle{Nickname: "no tank"}
	db.DB.Create(&v)
	return db.User{}, v
}

func manualFillup(t *testing.T, u db.User, v db.Vehicle, odo int, qty float32, detectionID string) *db.Fillup {
	t.Helper()
	full, unit := true, db.LITRE
	f, err := CreateFillup(models.CreateFillupRequest{VehicleID: v.ID, UserID: u.ID, OdoReading: odo, FuelQuantity: qty,
		PerUnitPrice: 1.6, TotalAmount: qty * 1.6, IsTankFull: &full, FuelUnit: &unit, Date: at, DetectionID: detectionID})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func status(id string) db.DetectionStatus {
	var d db.FillupDetection
	db.DB.First(&d, "id = ?", id)
	return d.Status
}

func TestDuplicateModes(t *testing.T) {
	for _, tc := range []struct{ mode, result string }{{"", "duplicate"}, {"manual", "ignored"}, {"detection", "applied"}} {
		t.Run("mode="+tc.mode, func(t *testing.T) {
			setupDB(t)
			u, v := mkUserVehicle(t, tc.mode, 43)
			mine := manualFillup(t, u, v, 81064, 11, "")
			res, err := CreateDetection(u.ID, v.ID, models.CreateDetectionRequest{
				OdoReading: 81068, Date: &at, LevelBefore: f32(74), LevelAfter: f32(99)})
			if err != nil || res.Result != tc.result {
				t.Fatalf("want %s, got %v %v", tc.result, res, err)
			}
			got, _ := db.GetFillupById(mine.ID)
			if tc.mode == "detection" {
				if got.OdoReading != 81068 || got.FuelQuantity != 10.8 || got.PerUnitPrice != 1.6 {
					t.Fatalf("HA wins: want odo/litres from the detection, my price kept; got %+v", got)
				}
			} else if got.OdoReading != 81064 || got.FuelQuantity != 11 {
				t.Fatalf("my fill-up must stay unchanged, got %+v", got)
			}
			if tc.mode == "" { // ask: "use detected values"
				if err := ApplyDetection(u.ID, res.Detection.ID); err != nil {
					t.Fatal(err)
				}
				got, _ = db.GetFillupById(mine.ID)
				if got.FuelQuantity != 10.8 || got.PerUnitPrice != 1.6 || status(res.Detection.ID) != db.DetectionApplied {
					t.Fatalf("apply: want 10.8 l with my price, status applied; got %+v %s", got, status(res.Detection.ID))
				}
			}
		})
	}
}

func TestManualEntryClosesPendingDetection(t *testing.T) {
	setupDB(t)
	u, v := mkUserVehicle(t, "", 43)
	res, _ := CreateDetection(u.ID, v.ID, models.CreateDetectionRequest{OdoReading: 81068, Date: &at, FuelQuantity: f32(11)})
	other, _ := CreateDetection(u.ID, v.ID, models.CreateDetectionRequest{OdoReading: 82000, Date: &at, FuelQuantity: f32(30)})
	manualFillup(t, u, v, 81064, 11, "") // entered by hand, not via the detection
	if status(res.Detection.ID) != db.DetectionMerged {
		t.Fatalf("matching detection must be merged, is %s", status(res.Detection.ID))
	}
	if status(other.Detection.ID) != db.DetectionPending {
		t.Fatal("a detection of another stop must stay pending")
	}
	if ps, _ := GetPendingDetections(u.ID); len(ps) != 1 {
		t.Fatalf("want 1 pending, got %d", len(ps))
	}
	f := manualFillup(t, u, v, 82000, 31.5, other.Detection.ID) // completed from the detection
	var d db.FillupDetection
	db.DB.First(&d, "id = ?", other.Detection.ID)
	if d.Status != db.DetectionConfirmed || d.FillupID == nil || *d.FillupID != f.ID {
		t.Fatalf("want confirmed and linked, got %+v", d)
	}
}

func TestSuggestTankSize(t *testing.T) {
	setupDB(t)
	u, v := mkUserVehicle(t, "", 50) // configured too big
	for i, c := range []struct {
		odo           int
		before, after float32
		real          float32 // what the user confirmed
	}{{1000, 50, 100, 21.5}, {1500, 20, 80, 25.8}, {2000, 40, 90, 21.5}} {
		res, _ := CreateDetection(u.ID, v.ID, models.CreateDetectionRequest{OdoReading: c.odo, Date: &at,
			LevelBefore: f32(c.before), LevelAfter: f32(c.after), OdoAtBefore: iptr(c.odo - 2)})
		manualFillup(t, u, v, c.odo, c.real, res.Detection.ID)
		_ = i
	}
	size, n := SuggestTankSize(v.ID)
	if n != 3 || size != 43 {
		t.Fatalf("want 43 l from 3 samples, got %v from %d", size, n)
	}
}

func TestApiKeys(t *testing.T) {
	setupDB(t)
	u, _ := mkUserVehicle(t, "", 43)
	key, k, err := CreateApiKey(u.ID, "Home Assistant")
	if err != nil || len(key) < 40 || k.Hash == key || k.Prefix != key[:10] {
		t.Fatalf("bad key %q %+v %v", key, k, err)
	}
	if UserIDForApiKey(key) != u.ID || UserIDForApiKey(key+"x") != "" || UserIDForApiKey("") != "" {
		t.Fatal("key lookup wrong")
	}
	db.DB.Model(&u).Update("is_disabled", true)
	if UserIDForApiKey(key) != "" {
		t.Fatal("disabled user's key must not work")
	}
	db.DB.Model(&u).Update("is_disabled", false)
	if err := DeleteApiKey(u.ID, k.ID); err != nil || UserIDForApiKey(key) != "" {
		t.Fatal("revoked key must not work")
	}
}
