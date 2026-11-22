// Package chat: the fuel commands of a chat bot (prices, watched, price, cheap-time, stats, graph, trips),
// answered as text plus an optional picture or file. Used by the Telegram bot (telegram.go) and by
// /api/v1/command (homebot on Matrix). The commands read hammond's own /api/v1, in-process as the user:
// the same data and access checks as for an API key, without one.
package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
)

// Handler: hammond's router (set by main); the commands call /api/v1 on it
var Handler http.Handler

type ctxKey struct{}

// UserFromContext: the user an in-process request runs as ("" = a normal request: API key needed)
func UserFromContext(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

type hammond struct{ userID string }

func (h *hammond) get(path string, out any) error {
	req, err := http.NewRequestWithContext(context.WithValue(context.Background(), ctxKey{}, h.userID), "GET", "/api/v1"+path, nil)
	if err != nil {
		return err
	}
	rec := httptest.NewRecorder()
	Handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		return fmt.Errorf("hammond %s: HTTP %d", path, rec.Code)
	}
	return json.Unmarshal(rec.Body.Bytes(), out)
}

type bot struct{ hammond *hammond }

// Reply: text (lines; "**" = bold line, [label](url) a link, `x` code) and an optional picture or file
type Reply struct {
	Text     string `json:"text"`
	Image    []byte `json:"image,omitempty"` // PNG
	File     []byte `json:"file,omitempty"`
	FileName string `json:"fileName,omitempty"`
	FileType string `json:"fileType,omitempty"`
}

var Help = []string{"prices — watched stations, cheapest first", "watched — numbered stations",
	"price <n> [7|30|3m|1y] — price history", "cheap-time [e10|n] [days] — cheapest hours",
	"stats [car] — fill-up, month, l/100 km", "graph fuel|costs|other [30|6m|1y|all] [car] — chart",
	"trips [today|yesterday|10|20.11.] [car] — trip log", "trips 30 pdf — as a file (or csv)"}

// Command: one chat message of the user → the answer
func Command(userID, text string) Reply {
	f := strings.Fields(strings.ToLower(strings.TrimLeft(strings.TrimSpace(text), "!/")))
	if len(f) == 0 {
		return Reply{}
	}
	if i := strings.Index(f[0], "@"); i > 0 { // Telegram groups: /prices@my_hammond_bot
		f[0] = f[0][:i]
	}
	b := &bot{hammond: &hammond{userID: userID}}
	var r string
	var err error
	switch f[0] {
	case "start", "help", "?":
		var out []string
		out = append(out, "**hammond")
		for _, h := range Help {
			cmd, desc, _ := strings.Cut(h, " — ")
			out = append(out, "`"+cmd+"`", desc)
		}
		return Reply{Text: strings.Join(out, "\n")}
	case "prices":
		r, err = b.fuelPrices()
	case "watched":
		r, err = b.watchedList()
	case "price":
		r, err = b.priceStats(f[1:])
	case "cheap-time", "cheap", "cheaptime":
		r, err = b.cheapTime(f[1:])
	case "stats":
		r, err = b.fuelStats(strings.Join(f[1:], " "))
	case "graph":
		g, msg, gerr := b.graphOf(f[1:])
		if msg != "" || gerr != nil {
			r, err = msg, gerr
			break
		}
		png, perr := g.png()
		if perr != nil {
			return Reply{Text: g.text()}
		}
		return Reply{Text: g.title + "\n" + strings.Join(g.foot, "\n"), Image: png}
	case "trips":
		rep, msg, terr := b.tripsData(f[1:])
		if msg != "" || terr != nil {
			r, err = msg, terr
			break
		}
		switch rep.format {
		case "pdf":
			return Reply{Text: rep.filename(), File: rep.pdf(), FileName: rep.filename(), FileType: "application/pdf"}
		case "csv":
			return Reply{Text: rep.filename(), File: rep.csv(), FileName: rep.filename(), FileType: "text/csv"}
		}
		r = rep.text()
	default:
		return Reply{Text: "unknown command " + f[0] + " — help lists them"}
	}
	if err != nil {
		return Reply{Text: "⚠️ " + err.Error()}
	}
	return Reply{Text: r}
}

func first(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func mapsSearch(q string) string {
	return "https://www.google.com/maps/search/?api=1&query=" + url.QueryEscape(q)
}
