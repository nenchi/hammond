package service

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
)

func TestFetchFrance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/ds/records" || !strings.Contains(q.Get("where"), "within_distance(geom, geom'POINT(4.835700 45.764000)', 3km)") ||
			!strings.HasPrefix(q.Get("order_by"), "distance(geom") {
			t.Errorf("unexpected request %s", r.URL)
		}
		w.Write([]byte(`{"total_count":2,"results":[
		  {"id":69004020,"adresse":"55 BIS QUAI GILLET","ville":"Lyon","cp":"69004","geom":{"lon":4.812,"lat":45.781},
		   "gazole_prix":2.269,"sp95_prix":null,"e10_prix":2.099,"sp98_prix":2.199},
		  {"id":69003001,"adresse":"1 Rue X","ville":"Lyon","cp":"69003","geom":{"lon":4.84,"lat":45.76},"gazole_prix":2.1,"sp95_prix":2.3,"e10_prix":null}]}`))
	}))
	defer srv.Close()
	got, err := fetchFrance(srv.URL+"/ds", []string{"e10", "e5"}, 45.764, 4.8357, 3)
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %+v", err, got)
	}
	a, b := got[0], got[1]
	if a.ID != "69004020" || a.Name != "55 BIS QUAI GILLET" || a.Address != "69004 Lyon" || a.Prices["e10"] != 2.099 || len(a.Prices) != 1 {
		t.Fatalf("first: %+v (diesel not accepted, no SP95)", a)
	}
	if got, _ := fetchFrance(srv.URL+"/ds", []string{"plus"}, 45.764, 4.8357, 3); got[0].Prices["plus"] != 2.199 {
		t.Fatalf("SP98 → plus: %+v", got[0])
	}
	if b.Prices["e5"] != 2.3 || b.Prices["e10"] != 0 || a.Dist <= 0 || a.Lat != 45.781 {
		t.Fatalf("second: %+v", b)
	}
}

func TestFetchPortugal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fuel := r.URL.Query().Get("idsTiposComb")
		price := map[string]string{"3201": "1,769 €", "2101": "1,659 €"}[fuel]
		if r.URL.Path != "/PesquisarPostos" || price == "" {
			t.Errorf("unexpected request %s", r.URL)
		}
		fmt.Fprintf(w, `{"status":true,"resultado":[{"Id":70586,"Nome":"Posto Lisboa","Marca":"GALP","Preco":%q,
		  "Morada":"Av. X","Localidade":"Lisboa","CodPostal":"1000-001","Latitude":38.7223,"Longitude":-9.1393}]}`, price)
	}))
	defer srv.Close()
	portugalList = countryList{}
	got, err := fetchPortugal(srv.URL, []string{"e10", "e5", "diesel"}, 38.7223, -9.1393, 2)
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %+v", err, got)
	}
	st := got[0]
	// one "95": the vehicle's first petrol grade; both fuels merged into one station
	if st.Brand != "GALP" || st.Prices["e10"] != 1.769 || st.Prices["diesel"] != 1.659 || len(st.Prices) != 2 {
		t.Fatalf("station: %+v", st)
	}
}

func TestCommaFloat(t *testing.T) {
	for in, want := range map[string]float64{"1,649": 1.649, "1,769 €": 1.769, "": 0, " 2.10 ": 2.1} {
		if got := commaFloat(in); got != want {
			t.Errorf("%q → %v, want %v", in, got, want)
		}
	}
}

// outside Portugal nothing is downloaded at all
func TestPortugalSkipsOtherCountries(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	portugalList = countryList{}
	for _, pos := range [][2]float64{{46.0569, 14.5058}, {40.4168, -3.7038}, {48.2082, 16.3738}} { // Ljubljana, Madrid, Vienna
		if got, err := fetchPortugal(srv.URL, []string{"e5"}, pos[0], pos[1], 25); err != nil || len(got) != 0 {
			t.Fatalf("portugal at %v: %v %v", pos, got, err)
		}
	}
	if calls != 0 {
		t.Fatalf("nothing may be downloaded outside Portugal: %d", calls)
	}
	if !portugalBox.near(37.74, -25.67, 2) || !portugalBox.near(32.65, -16.91, 2) { // Azores, Madeira
		t.Fatal("islands are in the box")
	}
}

func TestFetchItaly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/it/anagrafica_impianti_attivi.csv":
			w.Write([]byte("Estrazione del 2026-11-17\n" +
				"idImpianto|Gestore|Bandiera|Tipo Impianto|Nome Impianto|Indirizzo|Comune|Provincia|Latitudine|Longitudine\n" +
				"10|X SRL|Agip Eni|Stradale|ENI MILANO|VIA ROMA 1 \"A\"|MILANO|MI|45.4642|9.19\n" +
				"11|Y|Q8|Stradale|FAR|VIA X|ROMA|RM|41.9|12.5\n" +
				"12|Z|Bianca|Stradale|NO POS|VIA Y|MILANO|MI||\n"))
		case "/it/prezzo_alle_8.csv":
			w.Write([]byte("Estrazione del 2026-11-17\nidImpianto|descCarburante|prezzo|isSelf|dtComu\n" +
				"10|Benzina|1.799|1|x\n10|Benzina|1.949|0|x\n10|Gasolio|1.699|1|x\n10|Blue Diesel|1.789|1|x\n" +
				"10|Blue Super|1.899|1|x\n10|GPL|0.699|1|x\n10|Metano|1.4|1|x\n11|Benzina|1.7|1|x\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	got, err := fetchItaly(srv.URL+"/it", []string{"e10", "diesel", "plus", "diesel_premium", "lpg"}, 45.4642, 9.19, 2)
	if err != nil || len(got) != 1 {
		t.Fatalf("only the Milan station within 2 km (no position = dropped): %v %+v", err, got)
	}
	p := got[0].Prices
	if got[0].Brand != "Agip Eni" || got[0].Name != "ENI MILANO" || p["e10"] != 1.799 || p["diesel"] != 1.699 ||
		p["diesel_premium"] != 1.789 || p["plus"] != 1.899 || p["lpg"] != 0.699 || len(p) != 5 {
		t.Fatalf("self-service price wins, brand premiums mapped, methane ignored: %+v", got[0])
	}
	if got, _ := fetchItaly(srv.URL+"/it", []string{"e5"}, 50.1, 8.7, 5); len(got) != 0 {
		t.Fatal("outside Italy nothing is downloaded")
	}
}

func TestFetchSpain(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("no curl here (the image has it)")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/es/EstacionesTerrestres/" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"ResultadoConsulta":"OK","ListaEESSPrecio":[
		 {"IDEESS":"4375","Rótulo":"REPSOL","Dirección":"CALLE MAYOR, 1","C.P.":"28013","Localidad":"MADRID","Latitud":"40,4168","Longitud (WGS84)":"-3,7038",
		  "Tipo Venta":"P","Precio Gasolina 95 E5":"1,499","Precio Gasolina 95 E10":"","Precio Gasoleo A":"1,459","Precio Gasoleo Premium":"1,559",
		  "Precio Gasolina 98 E5":"1,699","Precio Gasolina 98 E10":"1,659","Precio Gases licuados del petróleo":"0,899"},
		 {"IDEESS":"9","Rótulo":"COOP","Latitud":"40,4170","Longitud (WGS84)":"-3,7040","Tipo Venta":"R","Precio Gasoleo A":"1,1"},
		 {"IDEESS":"10","Rótulo":"FAR","Latitud":"41,3874","Longitud (WGS84)":"2,1686","Tipo Venta":"P","Precio Gasoleo A":"1,4"}]}`))
	}))
	defer srv.Close()
	got, err := fetchSpain(srv.URL+"/es", []string{"e5", "e10", "diesel", "diesel_premium", "plus", "lpg"}, 40.4168, -3.7038, 2)
	if err != nil || len(got) != 1 {
		t.Fatalf("one public station near Madrid (members-only skipped): %v %+v", err, got)
	}
	p := got[0].Prices
	if got[0].Brand != "REPSOL" || got[0].Address != "CALLE MAYOR, 1, 28013 MADRID" || p["e5"] != 1.499 || p["diesel"] != 1.459 ||
		p["diesel_premium"] != 1.559 || p["plus"] != 1.659 || p["lpg"] != 0.899 || len(p) != 5 {
		t.Fatalf("grades by name, cheaper 98, no empty E10: %+v", got[0])
	}
	if got, _ := fetchSpain(srv.URL+"/es", []string{"e5"}, 50.1, 8.7, 5); len(got) != 0 {
		t.Fatal("outside Spain nothing is downloaded")
	}
}
