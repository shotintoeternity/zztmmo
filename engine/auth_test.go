package zztgo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

type fakeIDTokenVerifier struct {
	account AuthenticatedAccount
	token   string
}

func (v fakeIDTokenVerifier) VerifyIDToken(ctx context.Context, token, clientID string) (AuthenticatedAccount, error) {
	if token != v.token {
		return AuthenticatedAccount{}, ErrInvalidAuth
	}
	return v.account, nil
}

func TestM62GoogleOAuthCallbackSetsSignedSession(t *testing.T) {
	now := time.Unix(1000, 0)
	auth := NewAuthService("client-id", "client-secret", "http://example.test/api/auth/google/callback", []byte("test-cookie-secret"))
	auth.Now = func() time.Time { return now }
	auth.Verifier = fakeIDTokenVerifier{
		token: "id-token",
		account: AuthenticatedAccount{
			ID:    "google:123",
			Email: "ada@example.test",
			Name:  "Ada Lovelace",
		},
	}
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse token form: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "authorization_code" {
			t.Errorf("grant_type=%q, want authorization_code", got)
		}
		if got := r.Form.Get("code_verifier"); got == "" {
			t.Error("missing PKCE code_verifier")
		}
		if got := r.Form.Get("client_secret"); got != "client-secret" {
			t.Errorf("client_secret=%q, want configured secret", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": "id-token"})
	}))
	defer tokenServer.Close()
	auth.TokenEndpoint = tokenServer.URL

	startReq := httptest.NewRequest(http.MethodGet, "/api/auth/google/start?return=/play", nil)
	startRec := httptest.NewRecorder()
	auth.HandleStart(startRec, startReq)
	if startRec.Code != http.StatusFound {
		t.Fatalf("start status=%d, want %d", startRec.Code, http.StatusFound)
	}
	var stateCookie *http.Cookie
	for _, cookie := range startRec.Result().Cookies() {
		if cookie.Name == authStateCookie {
			stateCookie = cookie
		}
	}
	if stateCookie == nil {
		t.Fatal("start did not set OAuth state cookie")
	}
	location := startRec.Result().Header.Get("Location")
	authURL, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	if authURL.Query().Get("code_challenge") == "" || authURL.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("redirect missing PKCE challenge: %s", location)
	}

	callbackReq := httptest.NewRequest(http.MethodGet, "/api/auth/google/callback?code=abc&state="+url.QueryEscape(authURL.Query().Get("state")), nil)
	callbackReq.AddCookie(stateCookie)
	callbackRec := httptest.NewRecorder()
	auth.HandleCallback(callbackRec, callbackReq)
	if callbackRec.Code != http.StatusFound {
		t.Fatalf("callback status=%d body=%q", callbackRec.Code, callbackRec.Body.String())
	}

	var sessionCookie *http.Cookie
	for _, cookie := range callbackRec.Result().Cookies() {
		if cookie.Name == authSessionCookie {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil {
		t.Fatal("callback did not set session cookie")
	}
	checkReq := httptest.NewRequest(http.MethodGet, "/", nil)
	checkReq.AddCookie(sessionCookie)
	account, ok := auth.AccountFromRequest(checkReq)
	if !ok {
		t.Fatal("signed session cookie did not authenticate")
	}
	if account.ID != "google:123" || account.DisplayName() != "Ada Lovelace" {
		t.Fatalf("account=%+v, want google:123/Ada Lovelace", account)
	}
}

func TestM62AuthenticatedWebSocketJoinUsesGoogleNameForChat(t *testing.T) {
	world := testEmptyWorld(t)
	server := NewWebSocketServer(world, 1)
	server.Auth = NewAuthService("client-id", "", "", []byte("test-cookie-secret"))
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	authCookie := signedAuthCookie(t, server.Auth, AuthenticatedAccount{
		ID:    "google:123",
		Email: "ada@example.test",
		Name:  "Ada Lovelace",
	})
	authConn, authSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "ignored", Board: 1}, authCookie)
	defer authConn.Close(websocket.StatusNormalClosure, "")
	guestConn, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Guesty", Board: 1}, nil)
	defer guestConn.Close(websocket.StatusNormalClosure, "")

	server.DefaultInstance.mu.Lock()
	accountID, name, ok := server.DefaultInstance.RoomManager.PlayerIdentity(authSnap.You.ID)
	server.DefaultInstance.mu.Unlock()
	if !ok || accountID != "google:123" || name != "Ada Lovelace" {
		t.Fatalf("player identity=(%q,%q,%v), want google:123/Ada Lovelace/true", accountID, name, ok)
	}

	if err := wsjson.Write(ctx, authConn, map[string]string{"type": "chat", "text": "hello"}); err != nil {
		t.Fatalf("write authenticated chat: %v", err)
	}
	chat := readChatMessage(t, ctx, guestConn)
	if chat.From != "Ada Lovelace" || chat.Text != "hello" {
		t.Fatalf("authenticated chat=%+v, want Ada Lovelace/hello", chat)
	}
	_ = readChatMessage(t, ctx, authConn)

	if err := wsjson.Write(ctx, guestConn, map[string]string{"type": "chat", "text": "hi"}); err != nil {
		t.Fatalf("write guest chat: %v", err)
	}
	chat = readChatMessage(t, ctx, authConn)
	if chat.From != "Guesty" || chat.Text != "hi" {
		t.Fatalf("guest chat=%+v, want Guesty/hi", chat)
	}
}

func signedAuthCookie(t *testing.T, auth *AuthService, account AuthenticatedAccount) *http.Cookie {
	t.Helper()
	value, err := auth.Signer.Encode(authSession{Account: account, ExpiresAt: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatalf("sign auth cookie: %v", err)
	}
	return &http.Cookie{Name: authSessionCookie, Value: value}
}

func dialJoinWithCookie(t *testing.T, ctx context.Context, wsURL string, join JoinMessage, cookie *http.Cookie) (*websocket.Conn, SnapshotMessage) {
	t.Helper()
	opts := &websocket.DialOptions{}
	if cookie != nil {
		opts.HTTPHeader = http.Header{"Cookie": []string{cookie.String()}}
	}
	conn, _, err := websocket.Dial(ctx, wsURL, opts)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	conn.SetReadLimit(ServerReadLimit)
	if err := wsjson.Write(ctx, conn, join); err != nil {
		t.Fatalf("write join: %v", err)
	}
	var snapshot SnapshotMessage
	if err := wsjson.Read(ctx, conn, &snapshot); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	return conn, snapshot
}

func readChatMessage(t *testing.T, ctx context.Context, conn *websocket.Conn) struct {
	Type string `json:"type"`
	From string `json:"from"`
	Text string `json:"text"`
} {
	t.Helper()
	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, conn, &raw); err != nil {
			t.Fatalf("read chat: %v", err)
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			continue
		}
		if envelope.Type != "chat" {
			continue
		}
		var chat struct {
			Type string `json:"type"`
			From string `json:"from"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &chat); err != nil {
			t.Fatalf("unmarshal chat: %v", err)
		}
		return chat
	}
}
