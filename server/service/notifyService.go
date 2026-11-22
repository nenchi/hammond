package service

// Notification channels (Settings → Notifications): a user's list of places alerts go to: Gotify, ntfy,
// Matrix, Telegram. Each can be turned off, edited, deleted and tested; an alert goes to every enabled one.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/akhilrex/hammond/db"
)

var notifyClient = &http.Client{Timeout: 15 * time.Second}

var notifyKinds = map[string]string{"gotify": "Gotify", "ntfy": "ntfy", "matrix": "Matrix", "telegram": "Telegram"}

// ChannelView: a channel as the settings page shows it (secrets only as "set")
type ChannelView struct {
	db.NotifyChannel
	SecretSet bool   `json:"secretSet"`
	Linked    bool   `json:"linked"`         // telegram: a chat is linked
	Code      string `json:"code,omitempty"` // telegram, not linked: send "/start <code>" to the bot
	Bot       string `json:"bot,omitempty"`  // telegram: @username
}

func channelView(c db.NotifyChannel, withBot bool) ChannelView {
	v := ChannelView{NotifyChannel: c, SecretSet: c.Secret != "", Linked: c.ChatID != 0}
	if c.Kind == "telegram" && c.Secret != "" {
		if !v.Linked {
			v.Code = TelegramCode(c.ID)
		}
		if withBot {
			var me struct{ Username string }
			if tgCall(c.Secret, "getMe", map[string]any{}, &me) == nil {
				v.Bot = "@" + me.Username
			}
		}
	}
	return v
}

func ListChannels(userID string) ([]ChannelView, error) {
	var cs []db.NotifyChannel
	if err := db.DB.Where("user_id = ?", userID).Order("created_at").Find(&cs).Error; err != nil {
		return nil, err
	}
	out := []ChannelView{}
	for _, c := range cs {
		out = append(out, channelView(c, true))
	}
	return out, nil
}

func getChannel(userID, id string) (*db.NotifyChannel, error) {
	var c db.NotifyChannel
	if db.DB.Where("id = ? AND user_id = ?", id, userID).Limit(1).Find(&c); c.ID == "" {
		return nil, errors.New("channel not found")
	}
	return &c, nil
}

type ChannelInput struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	URL      string `json:"url"`
	Secret   string `json:"secret"` // "" keeps the stored one, "-" removes it
	Room     string `json:"room"`
	Commands bool   `json:"commands"`
}

func httpURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && len(s) <= 300
}

// apply: checks the input for the kind and copies it into c (a new token for Telegram unlinks)
func (in ChannelInput) apply(c *db.NotifyChannel) error {
	if c.ID == "" {
		if notifyKinds[in.Kind] == "" {
			return errors.New("kind: gotify, ntfy, matrix or telegram")
		}
		c.Kind = in.Kind
	}
	c.Name = strings.TrimSpace(in.Name)
	if c.Name == "" {
		c.Name = notifyKinds[c.Kind]
	}
	if len(c.Name) > 60 {
		return errors.New("name too long")
	}
	c.Enabled, c.Commands = in.Enabled, in.Commands && c.Kind == "telegram"
	c.URL, c.Room = strings.TrimRight(strings.TrimSpace(in.URL), "/"), strings.TrimSpace(in.Room)
	switch secret := strings.TrimSpace(in.Secret); secret {
	case "":
	case "-":
		c.Secret, c.ChatID = "", 0
	default:
		if len(secret) > 500 {
			return errors.New("token too long")
		}
		if c.Kind == "telegram" && secret != c.Secret {
			if !tgTokenRe.MatchString(secret) {
				return errors.New("that's not a bot token (from @BotFather, like 123456789:AA…)")
			}
			c.ChatID = 0 // another bot: link again
		}
		c.Secret = secret
	}
	switch c.Kind {
	case "gotify":
		if !httpURL(c.URL) {
			return errors.New("Gotify: the server URL, e.g. https://gotify.example.com")
		}
		if c.Secret == "" {
			return errors.New("Gotify: the app token (Gotify → Apps → create an app)")
		}
	case "ntfy":
		if !httpURL(c.URL) || strings.Trim(mustPath(c.URL), "/") == "" {
			return errors.New("ntfy: the topic URL, e.g. https://ntfy.sh/my-hammond-alerts")
		}
	case "matrix":
		if !httpURL(c.URL) {
			return errors.New("Matrix: the homeserver URL, e.g. https://matrix.example.com")
		}
		if !strings.HasPrefix(c.Room, "!") && !strings.HasPrefix(c.Room, "#") || !strings.Contains(c.Room, ":") || len(c.Room) > 255 {
			return errors.New("Matrix: the room ID (!abc:example.com) or alias (#fuel:example.com)")
		}
		if c.Secret == "" {
			return errors.New("Matrix: the access token of the account that posts (Element → Settings → Help & About)")
		}
	case "telegram":
		c.URL, c.Room = "", ""
		if c.Secret == "" {
			return errors.New("Telegram: the bot token from @BotFather")
		}
	}
	return nil
}

func mustPath(s string) string {
	u, _ := url.Parse(s)
	if u == nil {
		return ""
	}
	return u.Path
}

func CreateChannel(userID string, in ChannelInput) (*ChannelView, error) {
	c := db.NotifyChannel{UserID: userID}
	if err := in.apply(&c); err != nil {
		return nil, err
	}
	if err := db.DB.Create(&c).Error; err != nil {
		return nil, err
	}
	v := channelView(c, true)
	return &v, nil
}

func UpdateChannel(userID, id string, in ChannelInput) (*ChannelView, error) {
	c, err := getChannel(userID, id)
	if err != nil {
		return nil, err
	}
	if err := in.apply(c); err != nil {
		return nil, err
	}
	if err := db.DB.Save(c).Error; err != nil {
		return nil, err
	}
	v := channelView(*c, true)
	return &v, nil
}

func DeleteChannel(userID, id string) error {
	c, err := getChannel(userID, id)
	if err != nil {
		return err
	}
	return db.DB.Delete(c).Error
}

// UnlinkChannel: telegram: forget the linked chat (a new code links another one)
func UnlinkChannel(userID, id string) error {
	c, err := getChannel(userID, id)
	if err != nil {
		return err
	}
	return db.DB.Model(c).Update("chat_id", 0).Error
}

// TestChannel: a test message through this channel, also when it's turned off
func TestChannel(userID, id string) error {
	c, err := getChannel(userID, id)
	if err != nil {
		return err
	}
	return sendChannel(c, "hammond", "Test: alerts will arrive here.", "")
}

func sendChannel(c *db.NotifyChannel, title, message, click string) error {
	switch c.Kind {
	case "gotify":
		return sendGotify(c, title, message, click)
	case "ntfy":
		return sendNtfy(c, title, message, click)
	case "matrix":
		return sendMatrix(c, title, message, click)
	case "telegram":
		return sendTelegram(c, title, message, click)
	}
	return fmt.Errorf("unknown channel kind %q", c.Kind)
}

func postJSON(u string, body any, headers map[string]string, method string) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(method, u, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := notifyClient.Do(req)
	if err != nil {
		return fmt.Errorf("not reachable: %s", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("answered HTTP %d (URL or token?)", resp.StatusCode)
	}
	return nil
}

// sendGotify: the token in a header, not in the URL (no logs with it); click = Gotify's standard extra,
// opened when the notification is tapped (homebot shows it as Navigate/Map links)
func sendGotify(c *db.NotifyChannel, title, message, click string) error {
	m := map[string]interface{}{"title": title, "message": message, "priority": 5}
	if click != "" {
		m["extras"] = map[string]interface{}{"client::notification": map[string]interface{}{"click": map[string]string{"url": click}}}
	}
	if err := postJSON(c.URL+"/message", m, map[string]string{"X-Gotify-Key": c.Secret}, "POST"); err != nil {
		return errors.New("Gotify " + err.Error())
	}
	return nil
}

// sendNtfy: JSON to the server root (not headers: titles with "⛽" or "č" aren't valid in HTTP headers)
func sendNtfy(c *db.NotifyChannel, title, message, click string) error {
	p, err := url.Parse(c.URL)
	if err != nil {
		return err
	}
	path := strings.Trim(p.Path, "/")
	i := strings.LastIndex(path, "/")
	topic, root := path[i+1:], p.Scheme+"://"+p.Host+"/"+path[:i+1] // a server under a sub-path keeps it
	m := map[string]interface{}{"topic": topic, "title": title, "message": message, "tags": []string{"fuelpump"}}
	if click != "" {
		m["click"] = click
	}
	h := map[string]string{}
	if c.Secret != "" {
		h["Authorization"] = "Bearer " + c.Secret
	}
	if err := postJSON(root, m, h, "POST"); err != nil {
		return errors.New("ntfy " + err.Error())
	}
	return nil
}

// sendMatrix: an m.text message (title bold, link as "Navigate") as the account of the access token
func sendMatrix(c *db.NotifyChannel, title, message, click string) error {
	room := c.Room
	auth := map[string]string{"Authorization": "Bearer " + c.Secret}
	if strings.HasPrefix(room, "#") { // alias → room ID
		req, _ := http.NewRequest("GET", c.URL+"/_matrix/client/v3/directory/room/"+url.PathEscape(room), nil)
		req.Header.Set("Authorization", "Bearer "+c.Secret)
		resp, err := notifyClient.Do(req)
		if err != nil {
			return fmt.Errorf("Matrix not reachable: %s", err)
		}
		var r struct {
			RoomID string `json:"room_id"`
		}
		json.NewDecoder(resp.Body).Decode(&r)
		resp.Body.Close()
		if r.RoomID == "" {
			return fmt.Errorf("Matrix: no room %s (HTTP %d)", room, resp.StatusCode)
		}
		room = r.RoomID
	}
	esc := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace
	plain := title + "\n" + message
	html := "<b>" + esc(title) + "</b><br>" + strings.ReplaceAll(esc(message), "\n", "<br>")
	if click != "" {
		plain += "\n🧭 Navigate " + click
		html += `<br><a href="` + esc(click) + `">🧭 Navigate</a>`
	}
	txn := fmt.Sprintf("hammond%d", time.Now().UnixNano())
	err := postJSON(c.URL+"/_matrix/client/v3/rooms/"+url.PathEscape(room)+"/send/m.room.message/"+txn,
		map[string]string{"msgtype": "m.text", "body": plain, "format": "org.matrix.custom.html", "formatted_body": html}, auth, "PUT")
	if err != nil {
		return errors.New("Matrix " + err.Error())
	}
	return nil
}

func alertsOn(u *db.User) bool {
	var n int64
	db.DB.Model(&db.NotifyChannel{}).Where("user_id = ? AND enabled = ?", u.ID, true).Count(&n)
	return n > 0
}

// SendAlert: to every enabled channel of the user; an error names the ones that failed
func SendAlert(u *db.User, title, message, click string) error {
	var cs []db.NotifyChannel
	db.DB.Where("user_id = ? AND enabled = ?", u.ID, true).Find(&cs)
	if len(cs) == 0 {
		return errors.New("no notification channel on (Settings → Notifications)")
	}
	var errs []string
	for i := range cs {
		if err := sendChannel(&cs[i], title, message, click); err != nil {
			errs = append(errs, cs[i].Name+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// MoveGotifyToChannels: before v2.1 Gotify was a user setting; it becomes the user's first channel, once
func MoveGotifyToChannels() {
	var users []db.User
	db.DB.Where("gotify_url <> '' AND gotify_token <> ''").Find(&users)
	for _, u := range users {
		c := db.NotifyChannel{UserID: u.ID, Kind: "gotify", Name: "Gotify", Enabled: true, URL: u.GotifyURL, Secret: u.GotifyToken}
		if db.DB.Create(&c).Error == nil {
			db.DB.Model(&db.User{}).Where("id = ?", u.ID).Updates(map[string]interface{}{"gotify_url": "", "gotify_token": ""})
		}
	}
}
