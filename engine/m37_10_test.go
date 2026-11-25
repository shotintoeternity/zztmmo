package zztgo

// M37.10: with RequireUsername on (zzt-server sets it with sign-in), a
// signed-in account must claim a unique username before it plays or edits, and
// is shown by that username, never by the name on its Google account.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

type m3710Harness struct {
	server *WebSocketServer
	http   *httptest.Server
	wsURL  string
	ctx    context.Context
}

var m3710Ada = AuthenticatedAccount{ID: "google:m3710-ada", Email: "ada@example.test", Name: "Ada Lovelace"}

func m3710Server(t *testing.T) *m3710Harness {
	t.Helper()
	world := testEmptyWorld(t)
	server := NewWebSocketServer(world, 1)
	server.ChatDB = NewMemChatDatabase()
	server.Auth = NewAuthService("m3710-client", "m3710-secret", "", []byte("m37-10-cookie-secret"))
	server.RequireUsername = true
	api := &WebAPI{RoomManager: server.RoomManager, World: world, Server: server, Auth: server.Auth}
	mux := http.NewServeMux()
	mux.Handle("/ws", server)
	mux.Handle("/api/", api.Handler())
	h := httptest.NewServer(mux)
	t.Cleanup(h.Close)
	t.Cleanup(server.CloseRecorders)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return &m3710Harness{server: server, http: h, wsURL: "ws" + strings.TrimPrefix(h.URL, "http") + "/ws", ctx: ctx}
}

func (h *m3710Harness) claim(t *testing.T, accountID, username string) {
	t.Helper()
	err := h.server.ChatDB.UpdateAccountPreferences(accountID, func(prefs *AccountPreferences, _ bool) error {
		prefs.Profile.Handle = username
		return nil
	})
	if err != nil {
		t.Fatalf("claim %q: %v", username, err)
	}
}

// firstMessage dials, sends one message, and returns the type and body of the
// first thing the server says back.
func (h *m3710Harness) firstMessage(t *testing.T, cookie *http.Cookie, send interface{}) (string, json.RawMessage) {
	t.Helper()
	opts := &websocket.DialOptions{}
	if cookie != nil {
		opts.HTTPHeader = http.Header{"Cookie": []string{cookie.String()}}
	}
	conn, _, err := websocket.Dial(h.ctx, h.wsURL, opts)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	conn.SetReadLimit(ServerReadLimit)
	if err := wsjson.Write(h.ctx, conn, send); err != nil {
		t.Fatalf("write: %v", err)
	}
	var raw json.RawMessage
	if err := wsjson.Read(h.ctx, conn, &raw); err != nil {
		t.Fatalf("read: %v", err)
	}
	var envelope struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &envelope)
	return envelope.Type, raw
}

// claimTestUsername claims a username through PUT /api/preferences, as the
// launch box does. Suites that run the real zzt-server binary with sign-in on
// use it before a signed-in join, which that server would otherwise refuse.
func claimTestUsername(t *testing.T, baseURL string, cookie *http.Cookie, username string) {
	t.Helper()
	body := `{"profile":{"handle":"` + username + `"}}`
	req, err := http.NewRequest(http.MethodPut, baseURL+"/api/preferences", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("claim username %q: %v", username, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim username %q: status %d", username, resp.StatusCode)
	}
}

func TestM3710SignedInWithoutAUsernameIsTurnedAway(t *testing.T) {
	h := m3710Server(t)
	cookie := signedAuthCookie(t, h.server.Auth, m3710Ada)
	for _, tc := range []struct {
		name string
		send interface{}
	}{
		{"play", JoinMessage{Type: MessageTypeJoin, Name: "Ada"}},
		{"editor", EditorEnterMessage{Type: MessageTypeEditorEnter, World: h.server.DefaultInstance.Name}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			typ, raw := h.firstMessage(t, cookie, tc.send)
			var notice ModerationNoticeMessage
			if err := json.Unmarshal(raw, &notice); err != nil || typ != MessageTypeModerationNotice {
				t.Fatalf("first message = %s %s, want a moderation notice", typ, raw)
			}
			if notice.Text != usernameNeededText || !notice.Ended {
				t.Fatalf("notice = %+v, want %q and ended", notice, usernameNeededText)
			}
		})
	}
	// Nobody was spawned.
	inst := h.server.DefaultInstance
	inst.mu.Lock()
	n := len(inst.RoomManager.players)
	inst.mu.Unlock()
	if n != 0 {
		t.Fatalf("a turned-away account left %d player(s) in the room", n)
	}
	// A guest is not asked for a username.
	if typ, raw := h.firstMessage(t, nil, JoinMessage{Type: MessageTypeJoin, Name: "Gus"}); typ != MessageTypeSnapshot {
		t.Fatalf("a guest's first message = %s %s, want a snapshot", typ, raw)
	}
}

func TestM3710ASignedInPlayerIsShownByTheirUsername(t *testing.T) {
	h := m3710Server(t)
	h.claim(t, m3710Ada.ID, "ada_l")
	cookie := signedAuthCookie(t, h.server.Auth, m3710Ada)
	// Ada types something else in the box: the username still wins.
	typ, raw := h.firstMessage(t, cookie, JoinMessage{Type: MessageTypeJoin, Name: "Not My Name"})
	if typ != MessageTypeSnapshot {
		t.Fatalf("first message = %s %s, want a snapshot", typ, raw)
	}
	var snap SnapshotMessage
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatal(err)
	}
	if got := string(snap.You.Name); got != "ada_l" {
		t.Fatalf("Ada is shown as %q, want her username", got)
	}
	if bytes.Contains(raw, []byte("Lovelace")) {
		t.Fatalf("the snapshot carries the Google name: %s", raw)
	}
	if got := h.server.accountPublicName(m3710Ada); got != "ada_l" {
		t.Fatalf("accountPublicName = %q", got)
	}
	// An account with no username yet is never shown by its Google name.
	if got := h.server.accountPublicName(AuthenticatedAccount{ID: "google:nobody", Name: "Real Name"}); got != "Player" {
		t.Fatalf("an account without a username is shown as %q", got)
	}
}

func (h *m3710Harness) request(t *testing.T, method, path, body string, cookie *http.Cookie) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, h.http.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.String()
}

func TestM3710TheBoxLearnsAboutUsernamesAndCanClaimOne(t *testing.T) {
	h := m3710Server(t)
	cookie := signedAuthCookie(t, h.server.Auth, m3710Ada)

	type me struct {
		Authenticated   bool   `json:"authenticated"`
		Name            string `json:"name"`
		Username        string `json:"username"`
		RequireUsername bool   `json:"requireUsername"`
	}
	read := func() me {
		status, body := h.request(t, http.MethodGet, "/api/auth/me", "", cookie)
		var out me
		if status != http.StatusOK || json.Unmarshal([]byte(body), &out) != nil {
			t.Fatalf("/api/auth/me = %d %s", status, body)
		}
		return out
	}
	if got := read(); !got.Authenticated || got.Username != "" || !got.RequireUsername || got.Name != "Ada Lovelace" {
		t.Fatalf("/api/auth/me before a username = %+v", got)
	}

	// Someone else holds "taken".
	h.claim(t, "google:someone-else", "taken")
	if status, body := h.request(t, http.MethodPut, "/api/preferences", `{"profile":{"handle":"Taken"}}`, cookie); status != http.StatusConflict {
		t.Fatalf("claiming a taken username = %d %s, want 409", status, body)
	}
	if status, body := h.request(t, http.MethodPut, "/api/preferences", `{"profile":{"handle":"no spaces"}}`, cookie); status != http.StatusBadRequest {
		t.Fatalf("claiming an invalid username = %d %s, want 400", status, body)
	}
	if status, body := h.request(t, http.MethodPut, "/api/preferences", `{"profile":{"handle":"Ada_L"}}`, cookie); status != http.StatusOK {
		t.Fatalf("claiming a free username = %d %s", status, body)
	}
	if got := read(); got.Username != "ada_l" {
		t.Fatalf("/api/auth/me after claiming = %+v, want username ada_l", got)
	}

	// It can be changed, but not removed.
	if status, body := h.request(t, http.MethodPut, "/api/preferences", `{"profile":{"handle":""}}`, cookie); status != http.StatusBadRequest {
		t.Fatalf("removing the username = %d %s, want 400", status, body)
	}
	if status, body := h.request(t, http.MethodPut, "/api/preferences", `{"profile":{"handle":"ada_king"}}`, cookie); status != http.StatusOK {
		t.Fatalf("changing the username = %d %s", status, body)
	}
	if got := read(); got.Username != "ada_king" {
		t.Fatalf("/api/auth/me after the change = %+v", got)
	}

	// A guest is told sign-in exists and nothing else.
	status, body := h.request(t, http.MethodGet, "/api/auth/me", "", nil)
	if status != http.StatusOK || strings.Contains(body, "username\"") {
		t.Fatalf("a guest's /api/auth/me = %d %s", status, body)
	}
}
