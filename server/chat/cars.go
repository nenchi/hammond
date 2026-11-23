package chat

// Cars in the commands: a numbered list (`cars`), a default car (`car 2`), and every command takes a car as its
// number, name or plate. One car = never asked; several = the reading commands show all, writing ones ask.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
)

// call: hammond's UI API (/api/…) in-process as the user: writes get the web app's validation and access checks
func (h *hammond) call(method, path string, body, out any) error {
	var r *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(context.WithValue(context.Background(), ctxKey{}, h.userID), method, path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	Handler.ServeHTTP(rec, req)
	if rec.Code >= 300 {
		var e struct {
			Error  string `json:"error"`
			Errors any    `json:"errors"`
		}
		json.Unmarshal(rec.Body.Bytes(), &e)
		if e.Error == "" && e.Errors != nil {
			b, _ := json.Marshal(e.Errors)
			e.Error = string(b)
		}
		return fmt.Errorf("%s (HTTP %d)", first(e.Error, http.StatusText(rec.Code)), rec.Code)
	}
	if out != nil {
		return json.Unmarshal(rec.Body.Bytes(), out)
	}
	return nil
}

type car struct {
	ID, Nickname, Registration string
	FuelUnit                   string // "Lt", "kWh"…
	TankCapacity               float64
}

// cars: the user's vehicles in a stable order (name, then plate): the numbers don't move
func (b *bot) cars() ([]car, error) {
	var vs []car
	if err := b.hammond.get("/vehicles", &vs); err != nil {
		return nil, err
	}
	sort.SliceStable(vs, func(i, j int) bool {
		a, c := strings.ToLower(vs[i].Nickname), strings.ToLower(vs[j].Nickname)
		if a != c {
			return a < c
		}
		return vs[i].Registration < vs[j].Registration
	})
	return vs, nil
}

func plate(s string) string {
	return strings.ToLower(strings.NewReplacer(" ", "", "-", "", ".", "").Replace(s))
}

// matchCars: "" = all; a number from `cars`; a plate (spaces/dashes ignored); else part of the name
func matchCars(vs []car, arg string) []car {
	arg = strings.TrimSpace(strings.ToLower(arg))
	if arg == "" {
		return vs
	}
	if n, err := strconv.Atoi(arg); err == nil {
		if n >= 1 && n <= len(vs) {
			return []car{vs[n-1]}
		}
		return nil
	}
	var out []car
	for _, v := range vs {
		if v.Registration != "" && plate(v.Registration) == plate(arg) {
			return []car{v}
		}
		if strings.Contains(strings.ToLower(v.Nickname), arg) {
			out = append(out, v)
		}
	}
	return out
}

func carLabel(n int, v car) string {
	l := fmt.Sprintf("%d · %s", n, v.Nickname)
	if v.Registration != "" {
		l += " · " + v.Registration
	}
	return l
}

func (b *bot) carList() (string, error) {
	vs, err := b.cars()
	if err != nil {
		return "", err
	}
	if len(vs) == 0 {
		return "no cars yet — add one in hammond", nil
	}
	var me struct{ DefaultVehicleID string }
	b.hammond.get("/me", &me)
	out := []string{"**🚗 Cars"}
	for i, v := range vs {
		l := carLabel(i+1, v)
		if v.ID == me.DefaultVehicleID {
			l += " ⭐"
		}
		out = append(out, l)
	}
	if len(vs) > 1 {
		out = append(out, "`car 2` — default for fill/cost", "`stats 2`, `trips 10 golf` — one car")
	}
	return strings.Join(out, "\n"), nil
}

// setDefaultCar: `car 2` / `car golf` / `car OF-N1372`; `car none` = ask again
func (b *bot) setDefaultCar(arg string) (string, error) {
	vs, err := b.cars()
	if err != nil {
		return "", err
	}
	if arg == "" {
		return b.carList()
	}
	id, label := "", "none: write commands ask when you have several cars"
	if arg != "none" && arg != "-" {
		m := matchCars(vs, arg)
		if len(m) != 1 {
			return b.whichCar(vs, arg), nil
		}
		id = m[0].ID
		for i, v := range vs {
			if v.ID == id {
				label = carLabel(i+1, v)
			}
		}
	}
	if err := b.hammond.call("POST", "/api/me/defaultVehicle", map[string]string{"vehicleId": id}, nil); err != nil {
		return "", err
	}
	return "⭐ default car: " + label, nil
}

func (b *bot) whichCar(vs []car, arg string) string {
	out := []string{"Which car?"}
	if arg != "" {
		out[0] = "No single car matches " + arg + " — which one?"
	}
	for i, v := range vs {
		out = append(out, carLabel(i+1, v))
	}
	return strings.Join(append(out, "e.g. `stats 1`, or `car 1` as default"), "\n")
}

// writeCar: the one car a write command is for: named, the only one, or the default; msg = ask instead
func (b *bot) writeCar(arg string) (v car, msg string, err error) {
	vs, err := b.cars()
	if err != nil {
		return v, "", err
	}
	if len(vs) == 0 {
		return v, "no cars yet — add one in hammond", nil
	}
	if arg != "" {
		if m := matchCars(vs, arg); len(m) == 1 {
			return m[0], "", nil
		}
		return v, b.whichCar(vs, arg), nil
	}
	if len(vs) == 1 {
		return vs[0], "", nil
	}
	var me struct{ DefaultVehicleID string }
	b.hammond.get("/me", &me)
	for _, x := range vs {
		if x.ID == me.DefaultVehicleID {
			return x, "", nil
		}
	}
	return v, b.whichCar(vs, ""), nil
}

// readCars: the cars a reading command shows (all when none is named); msg = nothing matches
func (b *bot) readCars(arg string) ([]car, string, error) {
	vs, err := b.cars()
	if err != nil {
		return nil, "", err
	}
	m := matchCars(vs, arg)
	if len(m) == 0 {
		if len(vs) == 0 {
			return nil, "no cars yet — add one in hammond", nil
		}
		return nil, "no vehicle matches " + arg + "\n" + strings.TrimPrefix(b.whichCar(vs, ""), "Which car?\n"), nil
	}
	return m, "", nil
}
