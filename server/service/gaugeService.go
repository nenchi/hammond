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

type GaugeCalibration struct {
	Segments         int       `json:"segments"`
	Samples          int       `json:"samples"`
	Calibrated       bool      `json:"calibrated"`       // ≥ 2 samples; else linear
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
		out = append(out, gaugeSample{float64(*d.LevelBefore), float64(*d.LevelAfter), float64(f.FuelQuantity)})
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
		a := overlaps(s.from, s.to, n)
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				m[i][j] += a[i] * a[j]
			}
			m[i][n] += a[i] * s.litres
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
