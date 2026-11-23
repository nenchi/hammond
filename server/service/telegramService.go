package service

// Telegram channels: a user's own bot (token from @BotFather, Settings → Notifications). hammond long-polls
// getUpdates (outgoing only: no webhook, no open port). The one chat linked with "/start <code>" gets the
// alerts and, when the channel allows it, answers to the fuel commands (chat package); others are ignored.

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"mime/multipart"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/akhilrex/hammond/chat"
	"github.com/akhilrex/hammond/db"
)

// TelegramAPI: the Bot API base (a fake in tests)
var TelegramAPI = "https://api.telegram.org"

var (
	tgMu      sync.Mutex
	tgPollers = map[string]string{} // channel ID → token its poller runs with
	tgCodes   = map[string]string{} // channel ID → current link code
	tgWrong   = map[string]int{}    // wrong codes per channel: a new code after 10, so it can't be guessed
	tgClient  = &http.Client{Timeout: 70 * time.Second}
	tgTokenRe = regexp.MustCompile(`^\d{5,15}:[A-Za-z0-9_-]{30,60}$`)
)

func tgCall(token, method string, body any, out any) error {
	b, _ := json.Marshal(body)
	resp, err := tgClient.Post(TelegramAPI+"/bot"+token+"/"+method, "application/json", bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("Telegram not reachable: %s", strings.ReplaceAll(err.Error(), token, "***"))
	}
	return tgResult(resp, out)
}

func tgResult(resp *http.Response, out any) error {
	defer resp.Body.Close()
	var r struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&r); err != nil || !r.OK {
		return fmt.Errorf("Telegram: %s (HTTP %d)", map[bool]string{true: r.Description, false: "no answer"}[r.Description != ""], resp.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

// tgUpload: sendPhoto / sendDocument with the file as multipart
func tgUpload(token, method, field string, chatID int64, name string, data []byte, caption string) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("chat_id", fmt.Sprint(chatID))
	if caption != "" {
		w.WriteField("caption", caption)
		w.WriteField("parse_mode", "HTML")
	}
	fw, _ := w.CreateFormFile(field, name)
	fw.Write(data)
	w.Close()
	resp, err := tgClient.Post(TelegramAPI+"/bot"+token+"/"+method, w.FormDataContentType(), &buf)
	if err != nil {
		return fmt.Errorf("Telegram not reachable: %s", strings.ReplaceAll(err.Error(), token, "***"))
	}
	return tgResult(resp, nil)
}

var (
	mdLink = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^)\s]+)\)`)
	mdCode = regexp.MustCompile("`([^`]+)`")
)

// TelegramHTML: the chat markup (a "**" line is bold, [label](url), `code`) as Telegram HTML
func TelegramHTML(text string) string {
	esc := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace
	var out []string
	for _, l := range strings.Split(text, "\n") {
		bold := strings.HasPrefix(l, "**")
		h := mdCode.ReplaceAllString(mdLink.ReplaceAllString(esc(strings.TrimPrefix(l, "**")), `<a href="$2">$1</a>`), "<code>$1</code>")
		if bold {
			h = "<b>" + h + "</b>"
		}
		out = append(out, h)
	}
	return strings.Join(out, "\n")
}

// tgChunks: Telegram takes at most 4096 characters a message: split at line ends
func tgChunks(s string, max int) []string {
	var out []string
	cur := ""
	for _, l := range strings.Split(s, "\n") {
		if cur != "" && len([]rune(cur))+1+len([]rune(l)) > max {
			out, cur = append(out, cur), ""
		}
		if cur != "" {
			cur += "\n"
		}
		cur += l
	}
	return append(out, cur)
}

func tgSend(token string, chatID int64, r chat.Reply) error {
	switch {
	case r.Image != nil:
		return tgUpload(token, "sendPhoto", "photo", chatID, "graph.png", r.Image, TelegramHTML(r.Text))
	case r.File != nil:
		return tgUpload(token, "sendDocument", "document", chatID, r.FileName, r.File, "")
	}
	for _, part := range tgChunks(r.Text, 3800) { // room for the HTML tags
		if err := tgCall(token, "sendMessage", map[string]any{"chat_id": chatID, "text": TelegramHTML(part), "parse_mode": "HTML",
			"link_preview_options": map[string]bool{"is_disabled": true}}, nil); err != nil {
			return err
		}
	}
	return nil
}

// sendTelegram: an alert to the channel's linked chat
func sendTelegram(c *db.NotifyChannel, title, message, click string) error {
	if c.ChatID == 0 {
		return errors.New("Telegram: no chat linked yet: send /start <code> to the bot")
	}
	text := "**" + title + "\n" + message
	if click != "" {
		text += "\n[🧭 Navigate](" + click + ")"
	}
	return tgSend(c.Secret, c.ChatID, chat.Reply{Text: text})
}

// SendToCommandChats: to the user's linked Telegram chats that answer commands (where a reply can act on it)
func SendToCommandChats(u *db.User, title, message string) {
	var cs []db.NotifyChannel
	db.DB.Where("user_id = ? AND kind = ? AND enabled = ? AND commands = ? AND chat_id <> 0", u.ID, "telegram", true, true).Find(&cs)
	for i := range cs {
		if err := sendTelegram(&cs[i], title, message, ""); err != nil {
			log.Printf("telegram (%s): %v", cs[i].ID[:8], err)
		}
	}
}

// TelegramCode: the code to send to the bot as "/start <code>" (one per channel, until linked or restart)
func TelegramCode(channelID string) string {
	tgMu.Lock()
	defer tgMu.Unlock()
	if c, ok := tgCodes[channelID]; ok {
		return c
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(900000))
	c := fmt.Sprint(100000 + n.Int64())
	tgCodes[channelID] = c
	return c
}

// TelegramTimer: a poller per enabled Telegram channel, started and stopped as channels come and go
func TelegramTimer() {
	for ; ; time.Sleep(15 * time.Second) {
		var cs []db.NotifyChannel
		db.DB.Where("kind = ? AND enabled = ? AND secret <> ''", "telegram", true).Find(&cs)
		want := map[string]string{}
		for _, c := range cs {
			if u := getUser(c.UserID); u != nil && !u.IsDisabled {
				want[c.ID] = c.Secret
			}
		}
		tgMu.Lock()
		for id, tok := range want {
			if tgPollers[id] != tok {
				tgPollers[id] = tok
				go tgPoll(id, tok)
			}
		}
		for id := range tgPollers {
			if _, ok := want[id]; !ok {
				delete(tgPollers, id) // its poller sees this and stops
			}
		}
		tgMu.Unlock()
	}
}

func tgRunning(channelID, token string) bool {
	tgMu.Lock()
	defer tgMu.Unlock()
	return tgPollers[channelID] == token
}

type tgMessage struct {
	Text    string `json:"text"`
	Caption string `json:"caption"`
	Chat    struct {
		ID int64 `json:"id"`
	} `json:"chat"`
	Photo []struct {
		FileID   string `json:"file_id"`
		FileSize int    `json:"file_size"`
	} `json:"photo"` // sizes of one picture, the largest last
	Document *struct {
		FileID   string `json:"file_id"`
		FileName string `json:"file_name"`
		MimeType string `json:"mime_type"`
	} `json:"document"`
}

type tgUpdate struct {
	UpdateID int64      `json:"update_id"`
	Message  *tgMessage `json:"message"`
}

// tgDownload: a file the user sent (photo or document), at most 20 MB (the Bot API's limit)
func tgDownload(token, fileID string) ([]byte, string, error) {
	var f struct {
		FilePath string `json:"file_path"`
	}
	if err := tgCall(token, "getFile", map[string]any{"file_id": fileID}, &f); err != nil {
		return nil, "", err
	}
	resp, err := tgClient.Get(TelegramAPI + "/file/bot" + token + "/" + f.FilePath)
	if err != nil {
		return nil, "", fmt.Errorf("Telegram not reachable: %s", strings.ReplaceAll(err.Error(), token, "***"))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	return data, f.FilePath[strings.LastIndex(f.FilePath, "/")+1:], err
}

func tgPoll(channelID, token string) {
	offset := int64(0)
	for tgRunning(channelID, token) {
		var ups []tgUpdate
		if err := tgCall(token, "getUpdates", map[string]any{"offset": offset, "timeout": 50, "allowed_updates": []string{"message"}}, &ups); err != nil {
			log.Printf("telegram (%s): %v", channelID[:8], err)
			time.Sleep(30 * time.Second)
			continue
		}
		for _, up := range ups {
			offset = up.UpdateID + 1
			if up.Message == nil || !tgRunning(channelID, token) {
				continue
			}
			tgHandle(channelID, token, up.Message)
		}
	}
}

func tgHandle(channelID, token string, m *tgMessage) {
	chatID, text := m.Chat.ID, m.Text
	var c db.NotifyChannel
	if db.DB.Where("id = ?", channelID).Limit(1).Find(&c); c.ID == "" || c.Secret != token {
		return
	}
	if c.ChatID == 0 { // not linked yet: only "/start <code>" links this chat
		f := strings.Fields(text)
		if len(f) == 2 && f[0] == "/start" && f[1] == TelegramCode(channelID) {
			db.DB.Model(&c).Update("chat_id", chatID)
			tgMu.Lock()
			delete(tgCodes, channelID)
			tgMu.Unlock()
			msg := "**✓ Linked to hammond\nAlerts arrive here."
			if c.Commands {
				msg += " Type help for the commands."
			}
			tgSend(token, chatID, chat.Reply{Text: msg})
			return
		}
		if len(f) == 2 && f[0] == "/start" {
			tgMu.Lock()
			if tgWrong[channelID]++; tgWrong[channelID] >= 10 {
				delete(tgCodes, channelID)
				tgWrong[channelID] = 0
			}
			tgMu.Unlock()
		}
		tgSend(token, chatID, chat.Reply{Text: "Link this chat first: hammond → Settings → Notifications shows the code, send /start <code>."})
		return
	}
	if chatID != c.ChatID {
		return // someone else found the bot: ignored
	}
	reply := chat.Reply{Text: "Commands are off for this channel (hammond → Settings → Notifications)."}
	switch {
	case !c.Commands:
	case len(m.Photo) > 0 || (m.Document != nil && (strings.HasPrefix(m.Document.MimeType, "image/") || m.Document.MimeType == "application/pdf")):
		// a receipt or the pump's display → a Quick Entry, completed later in hammond
		id := ""
		if len(m.Photo) > 0 {
			id = m.Photo[len(m.Photo)-1].FileID
		} else {
			id = m.Document.FileID
		}
		data, name, err := tgDownload(token, id)
		if err != nil {
			reply = chat.Reply{Text: "⚠️ " + err.Error()}
			break
		}
		if m.Document != nil && m.Document.FileName != "" {
			name = m.Document.FileName
		}
		reply = chat.QuickEntry(c.UserID, name, data, m.Caption)
	default:
		reply = chat.Command(c.UserID, text)
	}
	if err := tgSend(token, chatID, reply); err != nil {
		log.Printf("telegram (%s): %v", channelID[:8], err)
	}
}
