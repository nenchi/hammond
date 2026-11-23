package service

import (
	"math"
	"time"

	"github.com/akhilrex/hammond/db"
)

// Fuel gauge calibration. A car's level % is usually not linear in litres (tank shape, gauges tuned to
// read "full" longer), so each of the vehicle's gauge segments (default 4: quarters) gets its own
// litres-per-%. Learned from confirmed detections: "74 % → 99 %, the user filled 12.14 l" means
// overlap(74–99, segment i) × k_i summed over i = 12.14. Least squares over all samples, pulled gently
// towards the linear value (tank / 100) so few samples can't produce wild curves.
// Drives teach it too (Trips from Home Assistant): "the gauge went 62 → 57 % over 80 km" at the average
// consumption of the full-tank fill-ups (receipt litres, so never from the gauge itself) is a sample at half
// weight. They cover the parts of the tank you rarely fill up from, and every drive adds one.

type GaugeCalibration struct {
	Segments         int       `json:"segments"`
	Samples          int       `json:"samples"`          // confirmed fill-ups
	DriveSamples     int       `json:"driveSamples"`     // stretches of drives (≥ 5 % of the gauge each)
	LitresPerKm      float64   `json:"litresPerKm"`      // average of the full-tank fill-ups, used for the drives
	Calibrated       bool      `json:"calibrated"`       // ≥ 2 samples (fill-ups and drives); else linear
	LitresPerPercent []float64 `json:"litresPerPercent"` // per segment, bottom (0 %) first
	SegmentLitres    []float64 `json:"segmentLitres"`    // litres each segment holds
	LinearPerPercent float64   `json:"linearPerPercent"` // tank / 100
	TankCapacity     float64   `json:"tankCapacity"`     // sum of the segments (learned) or the set tank size
}

const minCalibrationSamples = 2

func gaugeSegments(v *db.Vehicle) int {
	if v.GaugeSegments < 1 || v.GaugeSegments > 10 {
		return 4
	}
	return v.GaugeSegments
}

// overlaps: how many % of [from, to] fall into each of n equal segments of 0–100 %
func overlaps(from, to float64, n int) []float64 {
	out := make([]float64, n)
	w := 100 / float64(n)
	for i := 0; i < n; i++ {
		lo, hi := float64(i)*w, float64(i+1)*w
		if o := math.Min(to, hi) - math.Max(from, lo); o > 0 {
			out[i] = o
		}
	}
	return out
}

type gaugeSample struct {
	from, to, litres float64
	w                float64 // weight; 0 = 1 (a confirmed fill-up)
}

const (
	driveWeight  = 0.5 // a drive's litres are km × average consumption: less exact than a receipt
	driveMinDrop = 5.0 // % of the gauge per drive sample: the level comes in whole % steps
	driveMaxJump = 1.0 // a level higher than the last one by more = fuel was added in between
)

// litresPerKm: average consumption of the full-tank fill-ups (fuel per distance unit), 0 = unknown
func litresPerKm(v *db.Vehicle) float64 {
	ms, err := GetMileageByVehicleId(v.ID, time.Time{})
	if err != nil {
		return 0
	}
	var km, litres float64
	for _, m := range ms {
		if m.Mileage > 0 {
			km += float64(m.Mileage * m.FuelQuantity)
			litres += float64(m.FuelQuantity)
		}
	}
	if km < 300 { // ponytail: one short tank says little about the average
		return 0
	}
	return litres / km
}

// driveSamples: consecutive drives chained until the gauge dropped driveMinDrop %; a fill-up (level up),
// a drive without levels or a level that rose while driving starts a new chain.
// ponytail: reads all legs since the reset on every Calibrate; a few thousand rows, cache it if it gets slow
func driveSamples(v *db.Vehicle, lpk float64) []gaugeSample {
	if lpk <= 0 {
		return nil
	}
	var legs []db.TripLeg
	q := db.DB.Where("vehicle_id = ? AND fuel_start IS NOT NULL AND fuel_end IS NOT NULL", v.ID)
	if v.GaugeResetAt != nil {
		q = q.Where("started_at > ?", *v.GaugeResetAt)
	}
	q.Order("started_at").Find(&legs)
	var out []gaugeSample
	from, last, km, open := 0.0, 0.0, 0.0, false
	for _, l := range legs {
		s, e := *l.FuelStart, *l.FuelEnd
		if !open || s > last+driveMaxJump || e > s+driveMaxJump {
			from, km, open = s, 0, e <= s+driveMaxJump
			if !open {
				continue
			}
		}
		last, km = e, km+l.Distance
		if from-last >= driveMinDrop {
			out = append(out, gaugeSample{last, from, km * lpk, driveWeight})
			from, km = last, 0
		}
	}
	return out
}

func calibrationSamples(v *db.Vehicle) []gaugeSample {
	var ds []db.FillupDetection
	q := db.DB.Where("vehicle_id = ? AND status = ? AND estimated = ? AND uncertain = ? AND fillup_id IS NOT NULL",
		v.ID, db.DetectionConfirmed, true, false)
	if v.GaugeResetAt != nil {
		q = q.Where("created_at > ?", *v.GaugeResetAt)
	}
	q.Find(&ds)
	var out []gaugeSample
	for _, d := range ds {
		if d.LevelBefore == nil || d.LevelAfter == nil || *d.LevelAfter-*d.LevelBefore < 10 {
			continue // small jumps are too coarse to learn from
		}
		f, err := db.GetFillupById(*d.FillupID)
		if err != nil || f.FuelQuantity <= 0 {
			continue
		}
		out = append(out, gaugeSample{float64(*d.LevelBefore), float64(*d.LevelAfter), float64(f.FuelQuantity), 1})
	}
	return out
}

// solve (AᵀA + p²I) k = AᵀL + p²·k0 by Gaussian elimination (n ≤ 10)
func solveCalibration(samples []gaugeSample, n int, k0 float64) []float64 {
	const prior = 5.0 // weight of "like linear" per segment, in % units: a fraction of one real sample
	m := make([][]float64, n)
	for i := range m {
		m[i] = make([]float64, n+1)
		m[i][i] = prior * prior
		m[i][n] = prior * prior * k0
	}
	for _, s := range samples {
		a, w := overlaps(s.from, s.to, n), s.w
		if w == 0 {
			w = 1
		}
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				m[i][j] += w * a[i] * a[j]
			}
			m[i][n] += w * a[i] * s.litres
		}
	}
	for c := 0; c < n; c++ {
		p := c
		for r := c + 1; r < n; r++ {
			if math.Abs(m[r][c]) > math.Abs(m[p][c]) {
				p = r
			}
		}
		m[c], m[p] = m[p], m[c]
		for r := 0; r < n; r++ {
			if r != c && m[c][c] != 0 {
				f := m[r][c] / m[c][c]
				for j := c; j <= n; j++ {
					m[r][j] -= f * m[c][j]
				}
			}
		}
	}
	k := make([]float64, n)
	for i := range k {
		k[i] = m[i][n] / m[i][i]
		if k[i] < 0.2*k0 { // a segment can't hold (almost) nothing
			k[i] = 0.2 * k0
		}
	}
	return k
}

func Calibrate(v *db.Vehicle) GaugeCalibration {
	n := gaugeSegments(v)
	k0 := float64(v.TankCapacity) / 100
	c := GaugeCalibration{Segments: n, LinearPerPercent: k0, TankCapacity: float64(v.TankCapacity)}
	samples := calibrationSamples(v)
	c.Samples = len(samples)
	c.LitresPerKm = litresPerKm(v)
	drives := driveSamples(v, c.LitresPerKm)
	c.DriveSamples = len(drives)
	c.LitresPerKm = math.Round(c.LitresPerKm*10000) / 10000
	samples = append(samples, drives...)
	k := make([]float64, n)
	if len(samples) >= minCalibrationSamples && k0 > 0 {
		k = solveCalibration(samples, n, k0)
		c.Calibrated = true
	} else {
		for i := range k {
			k[i] = k0
		}
	}
	w := 100 / float64(n)
	c.TankCapacity = 0
	for _, ki := range k {
		c.LitresPerPercent = append(c.LitresPerPercent, math.Round(ki*1000)/1000)
		c.SegmentLitres = append(c.SegmentLitres, math.Round(ki*w*10)/10)
		c.TankCapacity += ki * w
	}
	c.TankCapacity = math.Round(c.TankCapacity*10) / 10
	return c
}

// Litres between two gauge levels (%), with this calibration
func (c GaugeCalibration) Litres(from, to float64) float64 {
	sum := 0.0
	for i, o := range overlaps(from, to, c.Segments) {
		if i < len(c.LitresPerPercent) {
			sum += o * c.LitresPerPercent[i]
		}
	}
	return sum
}

// EstimateLitres for a level jump, with the vehicle's calibration (linear until it has enough samples)
func EstimateLitres(v *db.Vehicle, from, to float64) float32 {
	c := Calibrate(v)
	sum := 0.0
	for i, o := range overlaps(from, to, c.Segments) {
		sum += o * c.LitresPerPercent[i]
	}
	return round1(float32(sum))
}

func ResetGaugeCalibration(vehicleID string) error {
	now := time.Now()
	return db.DB.Model(&db.Vehicle{}).Where("id = ?", vehicleID).Update("gauge_reset_at", &now).Error
}
