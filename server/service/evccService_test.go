package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/akhilrex/hammond/db"
	"github.com/akhilrex/hammond/models"
)

func TestSyncEvcc(t *testing.T) {
	setupDB(t)
	_, v := mkUserVehicle(t, "", 60)
	v.EvccVehicle = "My EV"
	db.DB.Save(&v)
	wrapped := false
	evcc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/sessions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		list := `[
		  {"id":1,"created":"2026-11-10T21:00:00Z","finished":"2026-11-10T23:00:00Z","vehicle":"my ev","odometer":12345.6,"chargedEnergy":20,"socEnd":80,"pricePerKWh":0.25},
		  {"id":2,"created":"2026-11-11T08:00:00Z","finished":"2026-11-11T09:00:00Z","vehicle":"Other car","chargedEnergy":10},
		  {"id":3,"created":"2026-11-11T10:00:00Z","finished":"0001-01-01T00:00:00Z","vehicle":"My EV","chargedEnergy":5},
		  {"id":4,"created":"2026-01-01T10:00:00Z","finished":"2026-01-01T11:00:00Z","vehicle":"My EV","chargedEnergy":5},
		  {"id":5,"created":"2026-11-12T10:00:00Z","finished":"2026-11-12T11:00:00Z","vehicle":"My EV","chargedEnergy":8,"pricePerKWh":0.30}]`
		if wrapped {
			list = `{"result":` + list + `}`
		}
		w.Write([]byte(list))
	}))
	defer evcc.Close()

	if _, err := SyncEvcc(); err == nil {
		t.Fatal("not set up must fail")
	}
	since := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	if err := UpdateEvccSettings(models.UpdateEvccRequest{URL: evcc.URL, Since: &since}); err != nil {
		t.Fatal(err)
	}
	r, err := SyncEvcc()
	if err != nil || r.Imported != 2 || r.Skipped != 3 {
		t.Fatalf("want 2 imported (1, 5), 3 skipped (other car, unfinished, before since): %+v %v", r, err)
	}
	var ds []db.FillupDetection
	db.DB.Order("date").Find(&ds)
	d := ds[0]
	if d.ExternalID != "evcc:1" || d.OdoReading != 12346 || d.FuelQuantity != 20 || d.IsTankFull || d.Source != "EVCC" ||
		d.PerUnitPrice == nil || *d.PerUnitPrice != 0.25 || d.ChargeEnd == nil {
		t.Fatalf("session 1 → detection with EVCC's price: %+v", d)
	}
	if ds[1].OdoReading != 0 || !ds[1].IsTankFull || *ds[1].PerUnitPrice != 0.30 {
		t.Fatalf("session 5 without odometer/soc: %+v", ds[1])
	}

	// again (old EVCC format): nothing new
	wrapped = true
	if r, err = SyncEvcc(); err != nil || r.Imported != 0 {
		t.Fatalf("already imported: %+v %v", r, err)
	}

	// with a charging place the place's tariff prices it, and the station is the place
	db.DB.Where("1 = 1").Delete(&db.FillupDetection{})
	p, _ := SaveChargingPlace(db.ChargingPlace{Name: "Home", LossPercent: 10,
		Components: []db.TariffComponent{{Name: "Energy", Price: 0.12}}})
	UpdateEvccSettings(models.UpdateEvccRequest{URL: evcc.URL, Since: &since, PlaceID: p.ID})
	if r, err = SyncEvcc(); err != nil || r.Imported != 2 {
		t.Fatalf("re-import: %+v %v", r, err)
	}
	db.DB.Order("date").Find(&ds)
	if *ds[0].PerUnitPrice != 0.12 || ds[0].Station != "Home" { // no loss: EVCC meters at the wallbox
		t.Fatalf("priced at the place: %+v", ds[0])
	}
	if err := UpdateEvccSettings(models.UpdateEvccRequest{URL: evcc.URL, PlaceID: "nope"}); err == nil {
		t.Fatal("unknown place must be refused")
	}
}
