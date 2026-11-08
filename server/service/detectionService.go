package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/akhilrex/hammond/db"
	"github.com/akhilrex/hammond/models"
	"gorm.io/gorm/clause"
)

// ---------- API keys ----------

const apiKeyPrefix = "hmd_"

func hashApiKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// CreateApiKey returns the plain key once; only its hash is stored.
func CreateApiKey(userID, name string) (string, *db.ApiKey, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, err
	}
	key := apiKeyPrefix + hex.EncodeToString(buf)
	k := db.ApiKey{UserID: userID, Name: name, Prefix: key[:len(apiKeyPrefix)+6], Hash: hashApiKey(key)}
	if err := db.DB.Create(&k).Error; err != nil {
		return "", nil, err
	}
	return key, &k, nil
}

func ListApiKeys(userID string) ([]db.ApiKey, error) {
	var keys []db.ApiKey
	err := db.DB.Where("user_id = ?", userID).Order("created_at desc").Find(&keys).Error
	return keys, err
}

func DeleteApiKey(userID, id string) error {
	tx := db.DB.Where("id = ? AND user_id = ?", id, userID).Delete(&db.ApiKey{})
	if tx.Error == nil && tx.RowsAffected == 0 {
		return errors.New("api key not found")
	}
	return tx.Error
}

// UserIDForApiKey returns the owner of a valid key ("" if unknown) and records its use.
func UserIDForApiKey(key string) string {
	if len(key) < len(apiKeyPrefix)+10 || key[:len(apiKeyPrefix)] != apiKeyPrefix {
		return ""
	}
	var keys []db.ApiKey
	db.DB.Where("hash = ?", hashApiKey(key)).Limit(1).Find(&keys)
	if len(keys) == 0 {
		return ""
	}
	now := time.Now()
	db.DB.Model(&keys[0]).Update("last_used_at", &now)
	var user db.User
	if db.DB.Where("id = ?", keys[0].UserID).Limit(1).Find(&user); user.ID == "" || user.IsDisabled {
		return ""
	}
	return user.ID
}

func UserCanAccessVehicle(userID, vehicleID string) bool {
	var n int64
	db.DB.Model(&db.UserVehicle{}).Where("user_id = ? AND vehicle_id = ?", userID, vehicleID).Count(&n)
	return n > 0
}

// ---------- detections ----------

// a detection and a fill-up are the same stop if this close
const (
	matchKm    = 50
	matchHours = 48
	// LevelBefore read more than this many km before the fill-up: the % difference understates the litres
	uncertainKm = 10
)

type DetectionResult struct {
	Detection *db.FillupDetection `json:"detection"`
	// "pending", "duplicate" (pending, needs a decision), "ignored", "applied"
	Result  string `json:"result"`
	Message string `json:"message"` // ready-made text for a notification
}

func findMatchingFillup(vehicleID string, odo int, date time.Time) *db.Fillup {
	var fs []db.Fillup
	db.DB.Where("vehicle_id = ? AND odo_reading BETWEEN ? AND ? AND date BETWEEN ? AND ?", vehicleID,
		odo-matchKm, odo+matchKm, date.Add(-matchHours*time.Hour), date.Add(matchHours*time.Hour)).
		Order("date desc").Limit(1).Find(&fs)
	if len(fs) == 0 {
		return nil
	}
	return &fs[0]
}

func round1(f float32) float32 { return float32(math.Round(float64(f)*10) / 10) }

func CreateDetection(userID, vehicleID string, req models.CreateDetectionRequest) (*DetectionResult, error) {
	vehicle, err := db.GetVehicleById(vehicleID)
	if err != nil {
		return nil, err
	}
	user, err := db.GetUserById(userID)
	if err != nil {
		return nil, err
	}
	d := db.FillupDetection{
		VehicleID: vehicleID, UserID: userID, Source: req.Source, OdoReading: req.OdoReading,
		LevelBefore: req.LevelBefore, LevelAfter: req.LevelAfter, OdoAtBefore: req.OdoAtBefore, Lat: req.Lat, Lng: req.Lng,
		Date: time.Now(), Status: db.DetectionPending,
	}
	if d.Source == "" {
		d.Source = "API"
	}
	if req.Date != nil {
		d.Date = *req.Date
	}
	unit := vehicle.FuelUnitDetail().Short

	switch {
	case req.FuelQuantity != nil && *req.FuelQuantity > 0:
		d.FuelQuantity = *req.FuelQuantity
	case req.LevelBefore != nil && req.LevelAfter != nil:
		if *req.LevelAfter <= *req.LevelBefore {
			return nil, errors.New("levelAfter must be higher than levelBefore")
		}
		if vehicle.TankCapacity <= 0 {
			return nil, errors.New("set the vehicle's tank size in hammond to estimate the quantity from fuel levels")
		}
		d.FuelQuantity = round1((*req.LevelAfter - *req.LevelBefore) / 100 * vehicle.TankCapacity)
		d.Estimated = true
	default:
		return nil, errors.New("send fuelQuantity, or levelBefore and levelAfter")
	}
	switch {
	case req.IsTankFull != nil:
		d.IsTankFull = *req.IsTankFull
	case req.LevelAfter != nil:
		d.IsTankFull = *req.LevelAfter >= 97
	default:
		d.IsTankFull = true
	}
	if d.Estimated && req.OdoAtBefore != nil && d.OdoReading-*req.OdoAtBefore > uncertainKm {
		d.Uncertain = true
		d.UncertainNote = fmt.Sprintf("the last fuel reading before was %d km before this fill-up, so more was probably added",
			d.OdoReading-*req.OdoAtBefore)
	}

	res := &DetectionResult{Detection: &d, Result: "pending"}
	approx := ""
	if d.Estimated {
		approx = "~"
	}
	res.Message = fmt.Sprintf("Fill-up detected for %s: %s%.1f %s at %d. Tap to add the price.",
		vehicle.Nickname, approx, d.FuelQuantity, unit, d.OdoReading)
	if d.Uncertain {
		res.Message += " Check the quantity: " + d.UncertainNote + "."
	}

	if dup := findMatchingFillup(vehicleID, d.OdoReading, d.Date); dup != nil {
		switch user.DetectionDuplicateMode {
		case "manual":
			d.Status, d.FillupID = db.DetectionIgnored, &dup.ID
			res.Result = "ignored"
			res.Message = fmt.Sprintf("Fill-up at %d for %s was already entered, detection ignored.", dup.OdoReading, vehicle.Nickname)
		case "detection":
			dup.OdoReading, dup.FuelQuantity, dup.IsTankFull = d.OdoReading, d.FuelQuantity, &d.IsTankFull
			if err := db.DB.Omit(clause.Associations).Save(dup).Error; err != nil {
				return nil, err
			}
			d.Status, d.FillupID = db.DetectionApplied, &dup.ID
			res.Result = "applied"
			res.Message = fmt.Sprintf("Updated your fill-up at %d for %s with the detected values (%s%.1f %s).",
				d.OdoReading, vehicle.Nickname, approx, d.FuelQuantity, unit)
		default: // "ask"
			d.DuplicateOfID = &dup.ID
			res.Result = "duplicate"
			res.Message = fmt.Sprintf("Fill-up detected for %s at %d, but you already entered one at %d. Tap to choose which to keep.",
				vehicle.Nickname, d.OdoReading, dup.OdoReading)
		}
	}
	if err := db.DB.Create(&d).Error; err != nil {
		return nil, err
	}
	return res, nil
}

func GetPendingDetections(userID string) ([]db.FillupDetection, error) {
	var ds []db.FillupDetection
	err := db.DB.Preload("DuplicateOf").
		Where("status = ? AND vehicle_id IN (?)", db.DetectionPending,
			db.DB.Model(&db.UserVehicle{}).Select("vehicle_id").Where("user_id = ?", userID)).
		Order("date desc").Find(&ds).Error
	return ds, err
}

func getOwnDetection(userID, id string) (*db.FillupDetection, error) {
	var ds []db.FillupDetection
	db.DB.Where("id = ?", id).Limit(1).Find(&ds)
	if len(ds) == 0 || !UserCanAccessVehicle(userID, ds[0].VehicleID) {
		return nil, errors.New("detection not found")
	}
	return &ds[0], nil
}

func DiscardDetection(userID, id string) error {
	d, err := getOwnDetection(userID, id)
	if err != nil {
		return err
	}
	return db.DB.Model(d).Update("status", db.DetectionDiscarded).Error
}

// ApplyDetection ("use detected values"): writes odometer, quantity and tank-full into the fill-up it
// duplicates; price and everything else the user entered stay.
func ApplyDetection(userID, id string) error {
	d, err := getOwnDetection(userID, id)
	if err != nil {
		return err
	}
	if d.DuplicateOfID == nil || d.Status != db.DetectionPending {
		return errors.New("detection has no fill-up to apply to")
	}
	f, err := db.GetFillupById(*d.DuplicateOfID)
	if err != nil {
		return err
	}
	f.OdoReading, f.FuelQuantity, f.IsTankFull = d.OdoReading, d.FuelQuantity, &d.IsTankFull
	if err := db.DB.Omit(clause.Associations).Save(f).Error; err != nil {
		return err
	}
	return db.DB.Model(d).Updates(map[string]interface{}{"status": db.DetectionApplied, "fillup_id": f.ID}).Error
}

// ResolveDetectionsForFillup runs after a fill-up is created: the detection it was completed from
// becomes "confirmed", any other pending detection of the same stop "merged" (no duplicates).
func ResolveDetectionsForFillup(f *db.Fillup, fromDetectionID string) {
	if fromDetectionID != "" {
		db.DB.Model(&db.FillupDetection{}).Where("id = ? AND vehicle_id = ? AND status = ?", fromDetectionID, f.VehicleID, db.DetectionPending).
			Updates(map[string]interface{}{"status": db.DetectionConfirmed, "fillup_id": f.ID})
	}
	db.DB.Model(&db.FillupDetection{}).Where("vehicle_id = ? AND status = ? AND odo_reading BETWEEN ? AND ? AND date BETWEEN ? AND ?",
		f.VehicleID, db.DetectionPending, f.OdoReading-matchKm, f.OdoReading+matchKm,
		f.Date.Add(-matchHours*time.Hour), f.Date.Add(matchHours*time.Hour)).
		Updates(map[string]interface{}{"status": db.DetectionMerged, "fillup_id": f.ID})
}

// SuggestTankSize learns the tank size from confirmed, certain level-based detections: the litres
// the user confirmed divided by the level difference. Median of at least 2, else 0.
func SuggestTankSize(vehicleID string) (float32, int) {
	var ds []db.FillupDetection
	db.DB.Where("vehicle_id = ? AND status = ? AND estimated = ? AND uncertain = ? AND fillup_id IS NOT NULL",
		vehicleID, db.DetectionConfirmed, true, false).Find(&ds)
	var sizes []float64
	for _, d := range ds {
		if d.LevelBefore == nil || d.LevelAfter == nil || *d.LevelAfter-*d.LevelBefore < 15 {
			continue // small jumps are too coarse to learn from
		}
		f, err := db.GetFillupById(*d.FillupID)
		if err != nil || f.FuelQuantity <= 0 {
			continue
		}
		sizes = append(sizes, float64(f.FuelQuantity)/float64(*d.LevelAfter-*d.LevelBefore)*100)
	}
	if len(sizes) < 2 {
		return 0, len(sizes)
	}
	sort.Float64s(sizes)
	m := sizes[len(sizes)/2]
	if len(sizes)%2 == 0 {
		m = (sizes[len(sizes)/2-1] + sizes[len(sizes)/2]) / 2
	}
	return round1(float32(m)), len(sizes)
}
