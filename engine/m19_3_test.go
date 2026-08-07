package zztgo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// M19.3 — the account-wide preferences store, with the color as its first key.
//
// Two things are being proved here and they pull in opposite directions. The
// store must be genuinely account-wide — the same player is the same color in
// another world and in another browser, which is exactly what localStorage
// cannot do — and it must stay outside the simulation, which is what M19.1
// bought and what this task must not spend. So every test below either follows
// a color somewhere localStorage cannot reach, or refuses something the store
// must never accept.

// The refusal the spec asks for from day one. An account-keyed store with no
// account has no sane default: writing under "" makes one shared bucket that
// every guest overwrites and every other guest reads back, which is a privacy
// bug wearing a convenience feature's clothes.
func TestM193EmptyAccountIDIsRefusedByBothStores(t *testing.T) {
	dir := t.TempDir()
	fileDB, err := NewFileChatDatabase(filepath.Join(dir, "chat.log"))
	if err != nil {
		t.Fatalf("NewFileChatDatabase: %v", err)
	}
	defer fileDB.Close()

	for _, tc := range []struct {
		name string
		db   ChatDatabase
	}{
		{"mem", NewMemChatDatabase()},
		{"file", fileDB},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, id := range []string{"", " ", "\t\n"} {
				if err := tc.db.PutAccountPreferences(id, AccountPreferences{Color: "#ff0000"}); !errors.Is(err, ErrNoAccountID) {
					t.Fatalf("PutAccountPreferences(%q) error = %v, want ErrNoAccountID", id, err)
				}
				if _, ok, err := tc.db.GetAccountPreferences(id); !errors.Is(err, ErrNoAccountID) || ok {
					t.Fatalf("GetAccountPreferences(%q) = ok %v, error %v, want ErrNoAccountID and not found", id, ok, err)
				}
			}

			// And the refusal is not merely an error return: a real account's
			// write must not become readable through the empty key either.
			if err := tc.db.PutAccountPreferences("google:ada", AccountPreferences{Color: "#00ff00"}); err != nil {
				t.Fatalf("PutAccountPreferences: %v", err)
			}
			if _, ok, _ := tc.db.GetAccountPreferences(""); ok {
				t.Fatal("the empty account id read back another account's preferences — the shared bucket the spec forbids")
			}
		})
	}

	// The file document is the durable half of that claim: nothing keyed by ""
	// may be written to disk at all.
	raw, err := os.ReadFile(filepath.Join(dir, "chat.log.accountprefs.json"))
	if err != nil {
		t.Fatalf("read preferences document: %v", err)
	}
	var stored map[string]AccountPreferences
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("decode preferences document: %v", err)
	}
	if _, ok := stored[""]; ok {
		t.Fatalf("the preferences document has an empty-id entry: %s", raw)
	}
	if len(stored) != 1 {
		t.Fatalf("preferences document holds %d entries, want only google:ada: %s", len(stored), raw)
	}
}

func TestM193FilePreferencesRoundTripAcrossAServerRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.log")
	db, err := NewFileChatDatabase(path)
	if err != nil {
		t.Fatalf("NewFileChatDatabase: %v", err)
	}
	if err := db.PutAccountPreferences("google:ada", AccountPreferences{Color: "#a1b2c3"}); err != nil {
		t.Fatalf("PutAccountPreferences: %v", err)
	}
	// An account that deliberately wears no color: the document exists and its
	// Color is empty, which is a different thing from having no document, and
	// the restart must not blur the two.
	if err := db.PutAccountPreferences("google:bo", AccountPreferences{}); err != nil {
		t.Fatalf("PutAccountPreferences: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := NewFileChatDatabase(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	prefs, ok, err := reopened.GetAccountPreferences("google:ada")
	if err != nil || !ok || prefs.Color != "#a1b2c3" {
		t.Fatalf("after restart Ada = %+v, ok %v, err %v; want #a1b2c3", prefs, ok, err)
	}
	prefs, ok, err = reopened.GetAccountPreferences("google:bo")
	if err != nil || !ok || prefs.Color != "" {
		t.Fatalf("after restart Bo = %+v, ok %v, err %v; want a stored empty color", prefs, ok, err)
	}
	if _, ok, _ := reopened.GetAccountPreferences("google:never-here"); ok {
		t.Fatal("an account that never wrote reads back as stored")
	}
}

// m193FuturePreferences is the second caller the store is being shaped for,
// standing in for whatever the profile work actually adds. It is declared in
// the test and never shipped, exactly as the spec asks: the point is that the
// NEXT preference is a field with a JSON tag, not a convention two callers
// agree on out of band.
type m193FuturePreferences struct {
	Color  string `json:"color,omitempty"`
	Handle string `json:"handle,omitempty"`
}

// Both directions of the extension are checked, because both happen: a document
// written when the field exists must still be readable by code that predates it
// (this is what a rollback looks like), and a document written today must be
// readable by the code that adds it (this is what the deploy looks like).
//
// The limit, stated rather than discovered later: today's writer serializes
// today's struct, so rewriting a document that carries a field this binary does
// not know DROPS it. That is fine for the way the field will actually arrive —
// added to AccountPreferences, so the binary that writes it knows it — and it
// is why the field belongs in the struct rather than in a loose map.
func TestM193PreferencesGrowByFieldsNotConventions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chat.log")
	prefsPath := path + ".accountprefs.json"

	future, err := json.MarshalIndent(map[string]m193FuturePreferences{
		"google:ada": {Color: "#a1b2c3", Handle: "ada-the-first"},
	}, "", "  ")
	if err != nil {
		t.Fatalf("marshal future document: %v", err)
	}
	if err := os.WriteFile(prefsPath, future, 0o600); err != nil {
		t.Fatalf("write future document: %v", err)
	}

	db, err := NewFileChatDatabase(path)
	if err != nil {
		t.Fatalf("NewFileChatDatabase: %v", err)
	}
	defer db.Close()

	prefs, ok, err := db.GetAccountPreferences("google:ada")
	if err != nil || !ok || prefs.Color != "#a1b2c3" {
		t.Fatalf("reading a document with an unknown field gave %+v, ok %v, err %v; want the color intact", prefs, ok, err)
	}

	// Now the other direction: what today's writer produces must load into the
	// reader that knows the new field, with the new field simply zero.
	if err := db.PutAccountPreferences("google:bo", AccountPreferences{Color: "#010203"}); err != nil {
		t.Fatalf("PutAccountPreferences: %v", err)
	}
	raw, err := os.ReadFile(prefsPath)
	if err != nil {
		t.Fatalf("read document: %v", err)
	}
	var byFutureReader map[string]m193FuturePreferences
	if err := json.Unmarshal(raw, &byFutureReader); err != nil {
		t.Fatalf("a future reader could not decode today's document: %v", err)
	}
	if byFutureReader["google:bo"].Color != "#010203" || byFutureReader["google:bo"].Handle != "" {
		t.Fatalf("future reader saw %+v, want the color and an empty handle", byFutureReader["google:bo"])
	}
}

// The headline claim: a signed-in player's color follows them where
// localStorage cannot go. Two servers hosting two differently-named worlds share
// one database, which is both "a different world" and "a different process";
// the joins that carry no color at all — or a stale one — are the different
// browser.
func TestM193SignedInColorFollowsToAnotherWorldAndAnotherBrowser(t *testing.T) {
	db := NewMemChatDatabase()
	secret := []byte("test-cookie-secret")
	account := AuthenticatedAccount{ID: "google:ada", Email: "ada@example.test", Name: "Ada"}

	alpha, alphaURL := m193Server(t, "ALPHA", db, secret)
	beta, betaURL := m193Server(t, "BETA", db, secret)
	if alpha.DefaultInstance.Name == beta.DefaultInstance.Name {
		t.Fatalf("both servers host %q; the different-world half of this test is not being exercised", alpha.DefaultInstance.Name)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cookie := signedAuthCookie(t, alpha.Auth, account)

	// The browser that made the pick. Nothing is stored for this account yet,
	// so the join adopts it — the migration path for everyone who picked a
	// color before this store existed.
	conn, snap := dialJoinWithCookie(t, ctx, alphaURL, JoinMessage{Type: MessageTypeJoin, Name: "ignored", Board: 1, Color: "#ff0000"}, cookie)
	defer conn.Close(websocket.StatusNormalClosure, "")
	if got := m193MyColor(t, snap); got != "#ff0000" {
		t.Fatalf("the picking browser's own roster color = %q, want #ff0000", got)
	}
	prefs, ok, err := db.GetAccountPreferences(account.ID)
	if err != nil || !ok || prefs.Color != "#ff0000" {
		t.Fatalf("after the first signed-in join the account holds %+v, ok %v, err %v; want #ff0000 adopted", prefs, ok, err)
	}

	// A different world, in a different process, from a browser with nothing in
	// its localStorage: the join carries no color and the account supplies it.
	fresh, freshSnap := dialJoinWithCookie(t, ctx, betaURL, JoinMessage{Type: MessageTypeJoin, Name: "ignored", Board: 1}, signedAuthCookie(t, beta.Auth, account))
	defer fresh.Close(websocket.StatusNormalClosure, "")
	if got := m193MyColor(t, freshSnap); got != "#ff0000" {
		t.Fatalf("in another world from a fresh browser the roster color = %q, want the account's #ff0000", got)
	}

	// A browser holding a stale pick — one made as a guest, or before this
	// account signed in here. The account wins, which is what "account-wide"
	// has to mean or the color is per-browser again.
	stale, staleSnap := dialJoinWithCookie(t, ctx, betaURL, JoinMessage{Type: MessageTypeJoin, Name: "ignored", Board: 1, Color: "#00ff00"}, signedAuthCookie(t, beta.Auth, account))
	defer stale.Close(websocket.StatusNormalClosure, "")
	if got := m193MyColor(t, staleSnap); got != "#ff0000" {
		t.Fatalf("a stale browser pick produced %q, want the stored #ff0000 to win", got)
	}
	if prefs, _, _ := db.GetAccountPreferences(account.ID); prefs.Color != "#ff0000" {
		t.Fatalf("the stale pick overwrote the account: %+v", prefs)
	}
}

// A guest has no durable identity to key on, so the store must not invent one:
// their color still reaches the room (it rides the join, as M19.1 built it) and
// nothing at all is written.
func TestM193GuestColorIsNeverStored(t *testing.T) {
	db := NewMemChatDatabase()
	_, url := m193Server(t, "ALPHA", db, []byte("test-cookie-secret"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, snap := dialJoinWithCookie(t, ctx, url, JoinMessage{Type: MessageTypeJoin, Name: "guest", Board: 1, Color: "#123456"}, nil)
	defer conn.Close(websocket.StatusNormalClosure, "")

	if got := m193MyColor(t, snap); got != "#123456" {
		t.Fatalf("guest roster color = %q, want the color they joined with", got)
	}
	db.mu.Lock()
	written := len(db.accountPrefs)
	db.mu.Unlock()
	if written != 0 {
		t.Fatalf("a guest join wrote %d preference documents, want none", written)
	}
}

// The write path. The picker cannot store a signed-in player's choice through
// the join — a join that carries no color is indistinguishable from a browser
// that has none — so "no color" has to be sayable out loud, which is what this
// endpoint is for. It is the same lesson as M19.2's "No color" row, one layer
// down.
func TestM193PreferencesEndpointIsTheWritePath(t *testing.T) {
	db := NewMemChatDatabase()
	secret := []byte("test-cookie-secret")
	account := AuthenticatedAccount{ID: "google:ada", Email: "ada@example.test", Name: "Ada"}

	server, wsURL := m193Server(t, "ALPHA", db, secret)
	api := &WebAPI{RoomManager: server.RoomManager, Server: server, Auth: server.Auth}
	httpServer := httptest.NewServer(api.Handler())
	defer httpServer.Close()
	cookie := signedAuthCookie(t, server.Auth, account)

	// A guest is not an error: the client needs an answer it can fall back
	// from, not a failed request to interpret.
	if got := m193GetPreferences(t, httpServer.URL, nil); got.Authenticated || got.Stored || got.Color != "" {
		t.Fatalf("guest GET = %+v, want an unauthenticated, unstored answer", got)
	}
	if code, _ := m193PutPreferences(t, httpServer.URL, nil, `{"color":"#ff0000"}`); code != http.StatusUnauthorized {
		t.Fatalf("guest PUT status = %d, want 401", code)
	}
	if _, ok, _ := db.GetAccountPreferences(account.ID); ok {
		t.Fatal("a guest PUT wrote to an account")
	}

	code, body := m193PutPreferences(t, httpServer.URL, cookie, `{"color":"#a1b2c3"}`)
	if code != http.StatusOK || body.Color != "#a1b2c3" || !body.Stored {
		t.Fatalf("PUT = %d %+v, want 200 with the stored color", code, body)
	}
	if got := m193GetPreferences(t, httpServer.URL, cookie); !got.Authenticated || !got.Stored || got.Color != "#a1b2c3" {
		t.Fatalf("GET after PUT = %+v, want the stored #a1b2c3", got)
	}

	// The stored value is untrusted input that now OUTLIVES the connection that
	// sent it, so it is validated at this edge exactly as the join is: junk
	// becomes the vanilla player rather than being kept for later broadcast.
	if _, body := m193PutPreferences(t, httpServer.URL, cookie, `{"color":"red;background:url(x)"}`); body.Color != "" {
		t.Fatalf("PUT of a junk color stored %q, want it sanitized away", body.Color)
	}
	if code, _ := m193PutPreferences(t, httpServer.URL, cookie, "not json"); code != http.StatusBadRequest {
		t.Fatalf("PUT of a malformed body = %d, want 400", code)
	}
	oversized := `{"color":"` + strings.Repeat("#", 4096) + `"}`
	if code, _ := m193PutPreferences(t, httpServer.URL, cookie, oversized); code != http.StatusBadRequest {
		t.Fatalf("PUT of an oversized body = %d, want 400", code)
	}
	if code, _ := m193PutPreferences(t, httpServer.URL, cookie, `{"color":"#a1b2c3"}`); code != http.StatusOK {
		t.Fatalf("re-PUT after the bad body = %d, want 200", code)
	}
	if code, body := m193PutPreferences(t, httpServer.URL, cookie, `{"hints":{"players":true,"death":true}}`); code != http.StatusOK || body.Color != "#a1b2c3" || !body.Hints.Players || !body.Hints.Death || body.Hints.Chat {
		t.Fatalf("PUT of hints = %d %+v, want hints merged without clearing color", code, body)
	}

	// "No color" — the way back to the vanilla white-on-blue player. The
	// account says it explicitly, and a browser still holding the old pick in
	// localStorage does not undo it at the next join.
	if _, body := m193PutPreferences(t, httpServer.URL, cookie, `{"color":""}`); !body.Stored || body.Color != "" {
		t.Fatalf("PUT of no color = %+v, want a stored empty color", body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, snap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "ignored", Board: 1, Color: "#a1b2c3"}, cookie)
	defer conn.Close(websocket.StatusNormalClosure, "")
	if got := m193MyColor(t, snap); got != "" {
		t.Fatalf("after choosing no color the roster still shows %q; the stale browser pick came back", got)
	}

	req, err := http.NewRequest(http.MethodDelete, httpServer.URL+"/api/preferences", nil)
	if err != nil {
		t.Fatalf("build DELETE: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE status = %d, want 405", resp.StatusCode)
	}
}

// The invariant M19.1 bought, re-checked at this layer: a color that now comes
// from an account instead of a browser is still not simulation state. Two rooms
// whose players differ only in stored color hash identically, and nothing about
// the preferences store touches the recorder.
func TestM193AStoredColorIsStillNotSimulationState(t *testing.T) {
	db := NewMemChatDatabase()
	secret := []byte("test-cookie-secret")
	if err := db.PutAccountPreferences("google:ada", AccountPreferences{Color: "#ff00ff"}); err != nil {
		t.Fatalf("PutAccountPreferences: %v", err)
	}

	colored, coloredURL := m193Server(t, "ALPHA", db, secret)
	plain, plainURL := m193Server(t, "ALPHA", NewMemChatDatabase(), secret)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	account := AuthenticatedAccount{ID: "google:ada", Email: "ada@example.test", Name: "Ada"}

	conn1, snap1 := dialJoinWithCookie(t, ctx, coloredURL, JoinMessage{Type: MessageTypeJoin, Name: "ignored", Board: 1}, signedAuthCookie(t, colored.Auth, account))
	defer conn1.Close(websocket.StatusNormalClosure, "")
	conn2, snap2 := dialJoinWithCookie(t, ctx, plainURL, JoinMessage{Type: MessageTypeJoin, Name: "ignored", Board: 1}, signedAuthCookie(t, plain.Auth, account))
	defer conn2.Close(websocket.StatusNormalClosure, "")

	if m193MyColor(t, snap1) != "#ff00ff" || m193MyColor(t, snap2) != "" {
		t.Fatalf("setup wrong: colored=%q plain=%q", m193MyColor(t, snap1), m193MyColor(t, snap2))
	}

	colored.DefaultInstance.mu.Lock()
	coloredHashes := colored.DefaultInstance.RoomManager.RoomStateHashes()
	colored.DefaultInstance.mu.Unlock()
	plain.DefaultInstance.mu.Lock()
	plainHashes := plain.DefaultInstance.RoomManager.RoomStateHashes()
	plain.DefaultInstance.mu.Unlock()
	if len(coloredHashes) == 0 || !reflect.DeepEqual(coloredHashes, plainHashes) {
		t.Fatalf("room hashes diverged on a stored color alone: %v vs %v", coloredHashes, plainHashes)
	}
}

func m193Server(t *testing.T, worldName string, db ChatDatabase, secret []byte) (*WebSocketServer, string) {
	t.Helper()
	world := testEmptyWorld(t)
	world.Info.Name = worldName

	server := NewWebSocketServer(world, 1)
	server.ChatDB = db
	server.Auth = NewAuthService("client-id", "", "", secret)
	httpServer := httptestServer(t, server)
	t.Cleanup(httpServer.Close)
	return server, "ws" + strings.TrimPrefix(httpServer.URL, "http")
}

// m193MyColor is the roster entry for the player this snapshot belongs to —
// what this browser's own ☻ will be drawn on.
func m193MyColor(t *testing.T, snap SnapshotMessage) string {
	t.Helper()
	for _, p := range snap.Players {
		if p.ID == snap.You.ID {
			return p.Color
		}
	}
	t.Fatalf("player %d is not in their own roster of %d", snap.You.ID, len(snap.Players))
	return ""
}

func m193GetPreferences(t *testing.T, baseURL string, cookie *http.Cookie) preferencesResponse {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api/preferences", nil)
	if err != nil {
		t.Fatalf("build GET: %v", err)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/preferences: %v", err)
	}
	defer resp.Body.Close()
	var out preferencesResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode preferences: %v", err)
	}
	return out
}

func m193PutPreferences(t *testing.T, baseURL string, cookie *http.Cookie, body string) (int, preferencesResponse) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, baseURL+"/api/preferences", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build PUT: %v", err)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /api/preferences: %v", err)
	}
	defer resp.Body.Close()
	var out preferencesResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}
