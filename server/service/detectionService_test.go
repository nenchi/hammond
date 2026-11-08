package service

import (
	"math"
	"testing"
	"time"

	"github.com/akhilrex/hammond/db"
	"github.com/akhilrex/hammond/models"
)

func setupDB(t *testing.T) {
	t.Helper()
	t.Setenv("CONFIG", t.TempDir())
	GeocodeURL = ""        // no Nominatim in tests: every profile asked (country tests set a fake)
	BuiltinSources = false // no real price services in tests (TestBuiltinSources fakes them)
	var err error
	if db.DB, err = db.Init(); err != nil {
		t.Fatal(err)
	}
	db.Migrate()
}

func mkUserVehicle(t *testing.T, mode string, tank float32) (db.User, db.Vehicle) {
	t.Helper()
	u := db.User{Email: "u@example.com", Name: "u", DetectionDuplicateMode: mode, Role: db.USER}
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
	// the samples cover 20–100 %: those segments learn 0.43 l/%, the bottom one (no data) stays near the
	// configured 50 l (0.5 l/%), so the learned tank is a bit above 43
	size, n := SuggestTankSize(v.ID)
	c := Calibrate(&v)
	if n != 3 || size < 43 || size > 45.5 || !c.Calibrated {
		t.Fatalf("want ~43–45 l from 3 samples, got %v from %d", size, n)
	}
	// 3 samples for 4 unknowns: single segments may spread a little, the covered 25–100 % must hold ~0.43×75
	covered := c.SegmentLitres[1] + c.SegmentLitres[2] + c.SegmentLitres[3]
	if math.Abs(covered-32.25) > 1 {
		t.Fatalf("segments 2–4: want ~32.25 l, got %v (%v)", covered, c.SegmentLitres)
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

// a non-linear 43 l tank like the user's A180: the top quarter holds the most, the gauge stays "full" longer
func TestGaugeCalibrationLearnsNonLinearTank(t *testing.T) {
	setupDB(t)
	u, v := mkUserVehicle(t, "", 43)
	truth := []float64{8, 10, 11, 14} // litres per quarter, bottom first: 43 l
	real := func(from, to float64) float32 {
		sum := 0.0
		for i, o := range overlaps(from, to, 4) {
			sum += o * truth[i] / 25
		}
		return float32(math.Round(sum*100) / 100)
	}
	// before calibration: linear
	if got := EstimateLitres(&v, 74, 99); got != 10.8 {
		t.Fatalf("uncalibrated estimate: want linear 10.8, got %v", got)
	}
	odo := 1000
	for _, c := range [][2]float64{{20, 99}, {45, 100}, {10, 98}, {60, 99}, {30, 100}} {
		odo += 500
		res, err := CreateDetection(u.ID, v.ID, models.CreateDetectionRequest{OdoReading: odo, Date: &at,
			LevelBefore: f32(float32(c[0])), LevelAfter: f32(float32(c[1])), OdoAtBefore: iptr(odo - 2)})
		if err != nil {
			t.Fatal(err)
		}
		manualFillup(t, u, v, odo, real(c[0], c[1]), res.Detection.ID) // the user confirms the real litres
	}
	cal := Calibrate(&v)
	if !cal.Calibrated || cal.Samples != 5 {
		t.Fatalf("want calibrated from 5 samples: %+v", cal)
	}
	for i, want := range truth {
		if math.Abs(cal.SegmentLitres[i]-want) > 1.0 {
			t.Fatalf("segment %d: want ~%v l, got %v (%v)", i+1, want, cal.SegmentLitres[i], cal.SegmentLitres)
		}
	}
	// your 7 Nov case: 74 % → 99 % now estimates close to the real amount instead of the linear 10.8 l
	want := real(74, 99)
	if got := EstimateLitres(&v, 74, 99); math.Abs(float64(got-want)) > 0.6 {
		t.Fatalf("calibrated estimate 74→99: want ~%v, got %v", want, got)
	}
	// reset: back to linear
	if err := ResetGaugeCalibration(v.ID); err != nil {
		t.Fatal(err)
	}
	v2, _ := db.GetVehicleById(v.ID)
	if c := Calibrate(v2); c.Calibrated || c.Samples != 0 {
		t.Fatalf("after reset: want linear, got %+v", c)
	}
}
