package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/akhilrex/hammond/chat"
	"github.com/akhilrex/hammond/db"
)

// a fake homeserver: whoami, join, sync (a queue of events), send (recorded), upload, authenticated download
type fakeHS struct {
	mu     sync.Mutex
	sent   []map[string]any
	events []map[string]any
	srv    *httptest.Server
}

func newFakeHS(t *testing.T) *fakeHS {
	f := &fakeHS{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_matrix/client/v3/login" {
			var req struct {
				Identifier struct{ User string }
				Password   string
			}
			json.NewDecoder(r.Body).Decode(&req)
			if req.Identifier.User == "hammond" && req.Password == `p"w\$x` {
				w.Write([]byte(`{"access_token":"bot-token","user_id":"@hammond:ci"}`))
				return
			}
			w.WriteHeader(403)
			w.Write([]byte(`{"errcode":"M_FORBIDDEN","error":"Invalid identifier or password."}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer bot-token" {
			w.WriteHeader(401)
			w.Write([]byte(`{"errcode":"M_UNKNOWN_TOKEN","error":"Unknown token"}`))
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		p := r.URL.Path
		switch {
		case p == "/_matrix/client/v3/account/whoami":
			w.Write([]byte(`{"user_id":"@hammond:ci"}`))
		case strings.HasPrefix(p, "/_matrix/client/v3/join/"):
			w.Write([]byte(`{"room_id":"!fuel:ci"}`))
		case p == "/_matrix/client/v3/sync":
			evs := f.events
			if r.URL.Query().Get("since") == "" {
				evs = nil // the first sync only gives the starting point
			}
			f.events = nil
			b, _ := json.Marshal(map[string]any{"next_batch": "s" + time.Now().Format("150405.000000"),
				"rooms": map[string]any{"join": map[string]any{"!fuel:ci": map[string]any{"timeline": map[string]any{"events": evs}}}}})
			w.Write(b)
		case strings.Contains(p, "/send/m.room.message/"):
			var m map[string]any
			json.NewDecoder(r.Body).Decode(&m)
			f.sent = append(f.sent, m)
			w.Write([]byte(`{"event_id":"$x"}`))
		case p == "/_matrix/media/v3/upload":
			io.Copy(io.Discard, r.Body)
			w.Write([]byte(`{"content_uri":"mxc://ci/up1"}`))
		case p == "/_matrix/client/v1/media/download/ci/receipt1":
			w.Write([]byte("JPEG"))
		default:
			w.WriteHeader(404)
			w.Write([]byte(`{"errcode":"M_NOT_FOUND"}`))
		}
	}))
	return f
}

func (f *fakeHS) last() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		return map[string]any{}
	}
	return f.sent[len(f.sent)-1]
}

func mxEv(sender, msgtype, body string) mxEvent {
	e := mxEvent{Type: "m.room.message", Sender: sender}
	e.Content.MsgType, e.Content.Body = msgtype, body
	return e
}

func TestMatrixChannelCommands(t *testing.T) {
	setupDB(t)
	hs := newFakeHS(t)
	defer hs.srv.Close()
	var upload string
	chat.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/quickEntries" {
			f, h, _ := r.FormFile("file")
			b, _ := io.ReadAll(f)
			upload = h.Filename + ":" + string(b)
			w.WriteHeader(http.StatusCreated)
			return
		}
		if r.URL.Path == "/api/v1/vehicles" {
			w.Write([]byte(`[{"id":"v1","nickname":"A180","registration":"OF-N1372"}]`))
			return
		}
		w.Write([]byte(`[]`))
	})
	u, _ := mkUserVehicle(t, "", 43)
	ch, err := CreateChannel(u.ID, ChannelInput{Kind: "matrix", URL: hs.srv.URL, Room: "!fuel:ci", Secret: "bot-token", Enabled: true, Commands: true})
	if err != nil || ch.Linked || ch.Code == "" {
		t.Fatalf("new channel: %+v %v", ch, err)
	}
	var c db.NotifyChannel
	load := func() *db.NotifyChannel { db.DB.First(&c, "id = ?", ch.ID); return &c }
	mxHandle(load(), "!fuel:ci", mxEv("@nenad:ci", "m.text", "prices")) // before linking: silent
	if len(hs.sent) != 0 {
		t.Fatal("answered before linking")
	}
	mxHandle(load(), "!fuel:ci", mxEv("@nenad:ci", "m.text", "/start 000000"))
	if b := hs.last()["body"]; b != "Wrong code: hammond → Settings → Notifications shows it." {
		t.Fatalf("wrong code: %v", b)
	}
	mxHandle(load(), "!fuel:ci", mxEv("@nenad:ci", "m.text", "/start "+ch.Code))
	if load().MatrixUser != "@nenad:ci" || !strings.HasPrefix(hs.last()["formatted_body"].(string), "<b>✓ Linked to hammond (@nenad:ci)</b>") {
		t.Fatalf("link: %q %v", c.MatrixUser, hs.last())
	}
	n := len(hs.sent)
	mxHandle(load(), "!fuel:ci", mxEv("@guest:ci", "m.text", "cars")) // someone else in the room
	if len(hs.sent) != n {
		t.Fatal("answered a stranger")
	}
	mxHandle(load(), "!fuel:ci", mxEv("@nenad:ci", "m.text", "cars"))
	if b := hs.last()["body"]; b != "🚗 Cars\n1 · A180 · OF-N1372" {
		t.Fatalf("cars: %q", b)
	}
	img := mxEv("@nenad:ci", "m.image", "receipt.jpg")
	img.Content.URL = "mxc://ci/receipt1"
	mxHandle(load(), "!fuel:ci", img)
	if upload != "receipt.jpg:JPEG" || !strings.HasPrefix(hs.last()["body"].(string), "📸 Saved as a Quick Entry") {
		t.Fatalf("photo: %q %v", upload, hs.last())
	}
	if err := mxReply(load(), "!fuel:ci", chat.Reply{Text: "Fuel · 30 days", Image: []byte("\x89PNG")}); err != nil ||
		hs.last()["msgtype"] != "m.image" || hs.last()["url"] != "mxc://ci/up1" {
		t.Fatalf("image: %v %v", err, hs.last())
	}
	if err := SendAlert(&u, "⛽ JET · Super E10", "1.759 EUR", "https://maps.example/?a=1&b=2"); err != nil ||
		hs.last()["formatted_body"] != `<b>⛽ JET · Super E10</b><br>1.759 EUR<br><a href="https://maps.example/?a=1&amp;b=2">🧭 Navigate</a>` ||
		hs.last()["body"] != "⛽ JET · Super E10\n1.759 EUR\n🧭 Navigate https://maps.example/?a=1&b=2" {
		t.Fatalf("alert: %v %v", err, hs.last())
	}
	// another room = link again
	v, _ := UpdateChannel(u.ID, ch.ID, ChannelInput{Enabled: true, Commands: true, URL: hs.srv.URL, Room: "!other:ci"})
	if v.Linked || v.Code == "" {
		t.Fatalf("new room unlinks: %+v", v)
	}
}

// the poller: whoami, join, first sync skips the backlog, then a /start from the room links the sender
func TestMatrixPoller(t *testing.T) {
	setupDB(t)
	hs := newFakeHS(t)
	defer hs.srv.Close()
	u, _ := mkUserVehicle(t, "", 43)
	ch, _ := CreateChannel(u.ID, ChannelInput{Kind: "matrix", URL: hs.srv.URL, Room: "!fuel:ci", Secret: "bot-token", Enabled: true, Commands: true})
	hs.mu.Lock()
	hs.events = []map[string]any{{"type": "m.room.message", "sender": "@hammond:ci", "content": map[string]any{"msgtype": "m.text", "body": "own"}}}
	hs.mu.Unlock()
	key := "test"
	mxMu.Lock()
	mxPollers[ch.ID] = key
	mxMu.Unlock()
	go mxPoll(ch.ID, key)
	defer func() { mxMu.Lock(); delete(mxPollers, ch.ID); mxMu.Unlock() }()
	time.Sleep(300 * time.Millisecond)
	hs.mu.Lock()
	hs.events = append(hs.events, map[string]any{"type": "m.room.message", "sender": "@nenad:ci",
		"content": map[string]any{"msgtype": "m.text", "body": "/start " + ch.Code}})
	hs.mu.Unlock()
	var c db.NotifyChannel
	for i := 0; i < 50 && c.MatrixUser == ""; i++ {
		time.Sleep(100 * time.Millisecond)
		db.DB.First(&c, "id = ?", ch.ID)
	}
	if c.MatrixUser != "@nenad:ci" {
		t.Fatalf("not linked by the poller: %q, sent %v", c.MatrixUser, hs.sent)
	}
}

func TestMatrixLogin(t *testing.T) {
	hs := newFakeHS(t)
	defer hs.srv.Close()
	// a password with quotes and backslashes (what broke the curl by hand), the user given as a full Matrix ID
	if tok, user, err := MatrixLogin(hs.srv.URL+"/", "@hammond:ci", `p"w\$x`); err != nil || tok != "bot-token" || user != "@hammond:ci" {
		t.Fatalf("login: %q %q %v", tok, user, err)
	}
	if _, _, err := MatrixLogin(hs.srv.URL, "hammond", "wrong"); err == nil || !strings.Contains(err.Error(), "Invalid identifier or password. (HTTP 403)") {
		t.Fatalf("wrong password: %v", err)
	}
}

func TestMatrixRoomInput(t *testing.T) {
	for in, want := range map[string]string{" !AbC:m.example.com ": "!AbC:m.example.com",
		"https://matrix.to/#/!AbC:m.example.com?via=m.example.com": "!AbC:m.example.com",
		"https://matrix.to/#/%23fuel%3Am.example.com":              "#fuel:m.example.com", "#fuel:m.example.com": "#fuel:m.example.com"} {
		if got := matrixRoom(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}
