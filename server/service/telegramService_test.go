package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/akhilrex/hammond/chat"
	"github.com/akhilrex/hammond/db"
)

// a fake Bot API: getMe, and every sendMessage recorded
func fakeTelegram(t *testing.T) (*[]map[string]any, func()) {
	var mu sync.Mutex
	sent := []map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			w.Write([]byte(`{"ok":true,"result":{"username":"ci_hammond_bot"}}`))
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			var m map[string]any
			json.NewDecoder(r.Body).Decode(&m)
			mu.Lock()
			sent = append(sent, m)
			mu.Unlock()
			w.Write([]byte(`{"ok":true,"result":{}}`))
		default:
			w.Write([]byte(`{"ok":false,"description":"Not Found"}`))
		}
	}))
	old := TelegramAPI
	TelegramAPI = srv.URL
	return &sent, func() { TelegramAPI = old; srv.Close() }
}

func TestTelegramChannel(t *testing.T) {
	setupDB(t)
	sent, done := fakeTelegram(t)
	defer done()
	// the chat commands read hammond's /api/v1: here a fake one without watched stations
	chat.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) })
	u, _ := mkUserVehicle(t, "", 43)
	if err := SendAlert(&u, "x", "y", ""); err == nil || !strings.Contains(err.Error(), "no notification channel") {
		t.Fatalf("no channel: %v", err)
	}
	token := "123456789:" + strings.Repeat("A", 35)
	if _, err := CreateChannel(u.ID, ChannelInput{Kind: "telegram", Secret: "not-a-token", Enabled: true}); err == nil {
		t.Fatal("a wrong bot token must be refused")
	}
	ch, err := CreateChannel(u.ID, ChannelInput{Kind: "telegram", Secret: token, Enabled: true, Commands: true})
	if err != nil || ch.Linked || ch.Code == "" || ch.Bot != "@ci_hammond_bot" || ch.Name != "Telegram" {
		t.Fatalf("new channel: %+v %v", ch, err)
	}
	last := func() string {
		if len(*sent) == 0 {
			return ""
		}
		return (*sent)[len(*sent)-1]["text"].(string)
	}
	tgHandle(ch.ID, token, 555, "/start 000000")
	if !strings.Contains(last(), "Link this chat first") {
		t.Fatalf("wrong code: %q", last())
	}
	tgHandle(ch.ID, token, 555, "/start "+ch.Code)
	var c db.NotifyChannel
	db.DB.First(&c, "id = ?", ch.ID)
	if c.ChatID != 555 || !strings.HasPrefix(last(), "<b>✓ Linked to hammond</b>") {
		t.Fatalf("link: chat %d, %q", c.ChatID, last())
	}
	n := len(*sent)
	tgHandle(ch.ID, token, 999, "prices") // a stranger who found the bot
	if len(*sent) != n {
		t.Fatal("a stranger got an answer")
	}
	tgHandle(ch.ID, token, 555, "/prices")
	if !strings.Contains(last(), "no watched stations") {
		t.Fatalf("command: %q", last())
	}
	if err := SendAlert(&u, "⛽ JET · Super E10", "1.759 EUR  (limit 1.800)\nOdenwaldring 84, Offenbach", "https://www.google.com/maps/dir/?api=1&destination=50.1,8.7"); err != nil {
		t.Fatal(err)
	}
	if want := "<b>⛽ JET · Super E10</b>\n1.759 EUR  (limit 1.800)\nOdenwaldring 84, Offenbach\n" +
		`<a href="https://www.google.com/maps/dir/?api=1&amp;destination=50.1,8.7">🧭 Navigate</a>`; last() != want || (*sent)[len(*sent)-1]["parse_mode"] != "HTML" {
		t.Fatalf("alert:\n%q\nwant\n%q", last(), want)
	}
	UpdateChannel(u.ID, ch.ID, ChannelInput{Name: "Phone", Enabled: true, Commands: false})
	tgHandle(ch.ID, token, 555, "prices")
	if !strings.HasPrefix(last(), "Commands are off") {
		t.Fatalf("commands off: %q", last())
	}
	// a new bot token: the old chat isn't linked any more
	v, err := UpdateChannel(u.ID, ch.ID, ChannelInput{Name: "Phone", Enabled: true, Secret: "987654321:" + strings.Repeat("B", 35)})
	if err != nil || v.Linked {
		t.Fatalf("new token unlinks: %+v %v", v, err)
	}
	if err := DeleteChannel("someone-else", ch.ID); err == nil {
		t.Fatal("deleted another user's channel")
	}
}

func TestTelegramHTMLAndChunks(t *testing.T) {
	if h := TelegramHTML("**A <b> & [map](https://x.y/?a=1&b=2)\n`price 3` <i>"); h != "<b>A &lt;b&gt; &amp; <a href=\"https://x.y/?a=1&amp;b=2\">map</a></b>\n<code>price 3</code> &lt;i&gt;" {
		t.Errorf("html: %q", h)
	}
	long := strings.Repeat("line of text\n", 400)
	parts := tgChunks(strings.TrimSuffix(long, "\n"), 3800)
	if len(parts) != 2 || strings.Join(parts, "\n") != strings.TrimSuffix(long, "\n") {
		t.Errorf("chunks: %d", len(parts))
	}
}

func TestMoveGotifyToChannels(t *testing.T) {
	setupDB(t)
	u, _ := mkUserVehicle(t, "", 43)
	db.DB.Model(&u).Updates(map[string]interface{}{"gotify_url": "https://gotify.example.com", "gotify_token": "AbC"})
	MoveGotifyToChannels()
	MoveGotifyToChannels() // runs at every start: once is enough
	var cs []db.NotifyChannel
	db.DB.Where("user_id = ?", u.ID).Find(&cs)
	var after db.User
	db.DB.First(&after, "id = ?", u.ID)
	if len(cs) != 1 || cs[0].Kind != "gotify" || !cs[0].Enabled || cs[0].URL != "https://gotify.example.com" || cs[0].Secret != "AbC" ||
		after.GotifyURL != "" || after.GotifyToken != "" {
		t.Fatalf("channels %+v, user %q %q", cs, after.GotifyURL, after.GotifyToken)
	}
}
