package service

// Matrix channels that answer commands: like Telegram, but the bot is a normal Matrix account (homeserver +
// its access token). hammond syncs the channel's room (long polling, outgoing only), links the user who sends
// "/start <code>" and answers only them: the fuel commands (chat package), charts as m.image, PDF/CSV as
// m.file; a picture becomes a Quick Entry. The room must be unencrypted (hammond doesn't do E2EE).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/akhilrex/hammond/chat"
	"github.com/akhilrex/hammond/db"
)

var (
	mxMu      sync.Mutex
	mxPollers = map[string]string{} // channel ID → token its poller runs with
	mxClient  = &http.Client{Timeout: 60 * time.Second}
)

// mxDo: a Matrix client API call with the channel's token (path from /_matrix/…)
func mxDo(c *db.NotifyChannel, method, path string, body io.Reader, ctype string, out any) error {
	req, err := http.NewRequest(method, c.URL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Secret)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := mxClient.Do(req)
	if err != nil {
		return fmt.Errorf("Matrix not reachable: %s", strings.ReplaceAll(err.Error(), c.Secret, "***"))
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct{ Errcode, Error string }
		json.Unmarshal(data, &e)
		return fmt.Errorf("Matrix: %s (HTTP %d)", first(e.Error, e.Errcode, http.StatusText(resp.StatusCode)), resp.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func mxJSON(c *db.NotifyChannel, method, path string, body, out any) error {
	b, _ := json.Marshal(body)
	return mxDo(c, method, path, bytes.NewReader(b), "application/json", out)
}

func first(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// matrixRoomID: the channel's room as an ID (an alias is resolved)
func matrixRoomID(c *db.NotifyChannel) (string, error) {
	if !strings.HasPrefix(c.Room, "#") {
		return c.Room, nil
	}
	var r struct {
		RoomID string `json:"room_id"`
	}
	if err := mxJSON(c, "GET", "/_matrix/client/v3/directory/room/"+url.PathEscape(c.Room), nil, &r); err != nil || r.RoomID == "" {
		return "", fmt.Errorf("Matrix: no room %s (%v)", c.Room, err)
	}
	return r.RoomID, nil
}

var (
	mdBoldLine = regexp.MustCompile(`^\*\*`)
	mdLinkTxt  = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^)\s]+)\)`)
)

// matrixMessage: the chat markup as Matrix text: plain body ("label url" for links) and HTML
func matrixMessage(text string) (plain, html string) {
	var p []string
	for _, l := range strings.Split(text, "\n") {
		l = mdBoldLine.ReplaceAllString(l, "")
		p = append(p, strings.ReplaceAll(mdLinkTxt.ReplaceAllString(l, "$1 $2"), "`", ""))
	}
	return strings.Join(p, "\n"), strings.ReplaceAll(TelegramHTML(text), "\n", "<br>")
}

func mxSend(c *db.NotifyChannel, roomID string, content map[string]any) error {
	txn := fmt.Sprintf("hammond%d", time.Now().UnixNano())
	return mxJSON(c, "PUT", "/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/send/m.room.message/"+txn, content, nil)
}

// mxReply: text, a picture (m.image) or a file (m.file)
func mxReply(c *db.NotifyChannel, roomID string, r chat.Reply) error {
	upload := func(data []byte, ctype, name string) (string, error) {
		var out struct {
			URI string `json:"content_uri"`
		}
		err := mxDo(c, "POST", "/_matrix/media/v3/upload?filename="+url.QueryEscape(name), bytes.NewReader(data), ctype, &out)
		return out.URI, err
	}
	switch {
	case r.Image != nil:
		mxc, err := upload(r.Image, "image/png", "graph.png")
		if err != nil {
			return err
		}
		plain, _ := matrixMessage(r.Text)
		return mxSend(c, roomID, map[string]any{"msgtype": "m.image", "body": plain, "url": mxc,
			"info": map[string]any{"mimetype": "image/png", "size": len(r.Image)}})
	case r.File != nil:
		mxc, err := upload(r.File, r.FileType, r.FileName)
		if err != nil {
			return err
		}
		return mxSend(c, roomID, map[string]any{"msgtype": "m.file", "body": r.FileName, "filename": r.FileName, "url": mxc,
			"info": map[string]any{"mimetype": r.FileType, "size": len(r.File)}})
	}
	plain, html := matrixMessage(r.Text)
	return mxSend(c, roomID, map[string]any{"msgtype": "m.text", "body": plain, "format": "org.matrix.custom.html", "formatted_body": html})
}

// sendMatrix: an alert (title bold, Navigate link)
func sendMatrix(c *db.NotifyChannel, title, message, click string) error {
	roomID, err := matrixRoomID(c)
	if err != nil {
		return err
	}
	text := "**" + title + "\n" + message
	if click != "" {
		text += "\n[🧭 Navigate](" + click + ")"
	}
	return mxReply(c, roomID, chat.Reply{Text: text})
}

// MatrixTimer: a poller per enabled Matrix channel that answers commands
func MatrixTimer() {
	for ; ; time.Sleep(15 * time.Second) {
		var cs []db.NotifyChannel
		db.DB.Where("kind = ? AND enabled = ? AND commands = ? AND secret <> ''", "matrix", true, true).Find(&cs)
		want := map[string]string{}
		for _, c := range cs {
			if u := getUser(c.UserID); u != nil && !u.IsDisabled {
				want[c.ID] = c.Secret + "|" + c.Room + "|" + c.URL
			}
		}
		mxMu.Lock()
		for id, key := range want {
			if mxPollers[id] != key {
				mxPollers[id] = key
				go mxPoll(id, key)
			}
		}
		for id := range mxPollers {
			if _, ok := want[id]; !ok {
				delete(mxPollers, id)
			}
		}
		mxMu.Unlock()
	}
}

func mxRunning(id, key string) bool {
	mxMu.Lock()
	defer mxMu.Unlock()
	return mxPollers[id] == key
}

type mxEvent struct {
	Type    string `json:"type"`
	Sender  string `json:"sender"`
	Content struct {
		MsgType string `json:"msgtype"`
		Body    string `json:"body"`
		URL     string `json:"url"`
		Info    struct {
			MimeType string `json:"mimetype"`
		} `json:"info"`
	} `json:"content"`
}

func mxPoll(channelID, key string) {
	var c db.NotifyChannel
	since, me, roomID := "", "", ""
	for mxRunning(channelID, key) {
		if db.DB.Where("id = ?", channelID).Limit(1).Find(&c); c.ID == "" {
			return
		}
		if me == "" { // who the bot is, its room (joined: accepts the invite), and the point to start from
			var who struct {
				UserID string `json:"user_id"`
			}
			err := mxJSON(&c, "GET", "/_matrix/client/v3/account/whoami", nil, &who)
			if err == nil {
				roomID, err = matrixRoomID(&c)
			}
			if err == nil {
				err = mxJSON(&c, "POST", "/_matrix/client/v3/join/"+url.PathEscape(roomID), map[string]any{}, nil)
			}
			var s struct {
				NextBatch string `json:"next_batch"`
			}
			if err == nil { // skip the backlog: commands sent while hammond was down aren't replayed
				err = mxJSON(&c, "GET", "/_matrix/client/v3/sync?timeout=0&filter="+url.QueryEscape(mxFilter(roomID)), nil, &s)
			}
			if err != nil {
				log.Printf("matrix (%s): %v", channelID[:8], err)
				time.Sleep(time.Minute)
				continue
			}
			me, since = who.UserID, s.NextBatch
		}
		var s struct {
			NextBatch string `json:"next_batch"`
			Rooms     struct {
				Join map[string]struct {
					Timeline struct{ Events []mxEvent }
				}
			}
		}
		q := "/_matrix/client/v3/sync?timeout=30000&since=" + url.QueryEscape(since) + "&filter=" + url.QueryEscape(mxFilter(roomID))
		if err := mxJSON(&c, "GET", q, nil, &s); err != nil {
			log.Printf("matrix (%s): %v", channelID[:8], err)
			time.Sleep(30 * time.Second)
			continue
		}
		since = s.NextBatch
		for _, ev := range s.Rooms.Join[roomID].Timeline.Events {
			if ev.Type == "m.room.message" && ev.Sender != me && mxRunning(channelID, key) {
				mxHandle(&c, roomID, ev)
			}
		}
	}
}

func mxFilter(roomID string) string {
	b, _ := json.Marshal(map[string]any{"presence": map[string]any{"not_types": []string{"*"}},
		"account_data": map[string]any{"not_types": []string{"*"}},
		"room": map[string]any{"rooms": []string{roomID}, "timeline": map[string]any{"types": []string{"m.room.message"}, "limit": 20},
			"state": map[string]any{"not_types": []string{"*"}}, "ephemeral": map[string]any{"not_types": []string{"*"}},
			"account_data": map[string]any{"not_types": []string{"*"}}}})
	return string(b)
}

// mxDownload: a picture or file the user sent (mxc://server/id), through authenticated media
func mxDownload(c *db.NotifyChannel, mxc string) ([]byte, error) {
	server, id, ok := strings.Cut(strings.TrimPrefix(mxc, "mxc://"), "/")
	if !ok {
		return nil, errors.New("not an mxc URL")
	}
	req, _ := http.NewRequest("GET", c.URL+"/_matrix/client/v1/media/download/"+url.PathEscape(server)+"/"+url.PathEscape(id), nil)
	req.Header.Set("Authorization", "Bearer "+c.Secret)
	resp, err := mxClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Matrix: download HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 20<<20))
}

func mxHandle(c *db.NotifyChannel, roomID string, ev mxEvent) {
	text := strings.TrimSpace(ev.Content.Body)
	if c.MatrixUser == "" { // not linked yet: only "/start <code>" links its sender
		f := strings.Fields(text)
		if len(f) == 2 && f[0] == "/start" && f[1] == LinkCode(c.ID) {
			db.DB.Model(c).Update("matrix_user", ev.Sender)
			c.MatrixUser = ev.Sender
			forgetCode(c.ID)
			mxReply(c, roomID, chat.Reply{Text: "**✓ Linked to hammond (" + ev.Sender + ")\nAlerts arrive here. Type help for the commands."})
		} else if len(f) == 2 && f[0] == "/start" {
			wrongCode(c.ID)
			mxReply(c, roomID, chat.Reply{Text: "Wrong code: hammond → Settings → Notifications shows it."})
		}
		return // other messages before linking: silent (the room may have other people)
	}
	if ev.Sender != c.MatrixUser {
		return // someone else in the room: ignored
	}
	var reply chat.Reply
	switch ev.Content.MsgType {
	case "m.image", "m.file":
		if mt := ev.Content.Info.MimeType; ev.Content.MsgType == "m.file" && !strings.HasPrefix(mt, "image/") && mt != "application/pdf" {
			return
		}
		data, err := mxDownload(c, ev.Content.URL)
		if err != nil {
			reply = chat.Reply{Text: "⚠️ " + err.Error()}
			break
		}
		reply = chat.QuickEntry(c.UserID, ev.Content.Body, data, "")
	case "m.text":
		reply = chat.Command(c.UserID, text)
	default:
		return
	}
	if err := mxReply(c, roomID, reply); err != nil {
		log.Printf("matrix (%s): %v", c.ID[:8], err)
	}
}
