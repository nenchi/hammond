package service

import (
	"fmt"
	"net/http"
	"net/http/httptest"
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
