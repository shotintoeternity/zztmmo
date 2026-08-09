package zztgo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func TestM261PreferencesRoundTripPreservesExistingFields(t *testing.T) {
	db := NewMemChatDatabase()
	prefs := AccountPreferences{
		Color:                      "#112233",
		BlockedAccounts:            []string{"google:blocked"},
		Hints:                      AccountHintPreferences{Players: true},
		Profile:                    AccountProfilePreferences{Handle: "ada", DisplayName: "Ada", About: []string{"Builder"}},
		FollowedAccounts:           []string{"google:bo", "google:bo", "google:ada", ""},
		ShareLocationWithFollowers: true,
	}
	if err := db.PutAccountPreferences("google:ada", prefs); err != nil {
		t.Fatalf("PutAccountPreferences: %v", err)
	}
	got, ok, err := db.GetAccountPreferences("google:ada")
	if err != nil || !ok {
		t.Fatalf("GetAccountPreferences = (_, %v, %v)", ok, err)
	}
	if got.Color != prefs.Color || got.BlockedAccounts[0] != "google:blocked" || !got.Hints.Players || got.Profile.Handle != "ada" {
		t.Fatalf("sibling fields were not preserved: %+v", got)
	}
	if !got.ShareLocationWithFollowers {
		t.Fatalf("share flag did not round-trip: %+v", got)
	}
	if len(got.FollowedAccounts) != 1 || got.FollowedAccounts[0] != "google:bo" {
		t.Fatalf("follow list sanitized to %+v, want only google:bo", got.FollowedAccounts)
	}
}

func TestM261FollowUnfollowAndNoAccountIDLeak(t *testing.T) {
	db := NewMemChatDatabase()
	secret := []byte("test-cookie-secret")
	ada := AuthenticatedAccount{ID: "google:ada", Email: "ada@example.test", Name: "Ada"}
	bo := AuthenticatedAccount{ID: "google:bo", Email: "bo@example.test", Name: "Bo"}
	if err := db.PutAccountPreferences(bo.ID, AccountPreferences{Profile: AccountProfilePreferences{Handle: "bob", DisplayName: "Bo"}}); err != nil {
		t.Fatalf("seed bo profile: %v", err)
	}
	server, wsURL := m193Server(t, "ALPHA", db, secret)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	adaConn, adaSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Board: 1}, signedAuthCookie(t, server.Auth, ada))
	defer adaConn.Close(websocket.StatusNormalClosure, "")
	boConn, boSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Board: 1}, signedAuthCookie(t, server.Auth, bo))
	defer boConn.Close(websocket.StatusNormalClosure, "")
	guestConn, guestSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Guest", Board: 1}, nil)
	defer guestConn.Close(websocket.StatusNormalClosure, "")

	m261Follow(t, ctx, guestConn, boSnap.You.ID, true)
	if guestFollower := m261ReadFollowResult(t, ctx, guestConn); guestFollower.Followed || !strings.Contains(guestFollower.Text, "Sign in") {
		t.Fatalf("guest follower result = %+v", guestFollower)
	}
	m261Follow(t, ctx, adaConn, PlayerID(4242), true)
	if missing := m261ReadFollowResult(t, ctx, adaConn); missing.Followed || !strings.Contains(missing.Text, "already left") {
		t.Fatalf("missing target result = %+v", missing)
	}
	m261Follow(t, ctx, adaConn, adaSnap.You.ID, true)
	if self := m261ReadFollowResult(t, ctx, adaConn); self.Followed || !strings.Contains(self.Text, "yourself") {
		t.Fatalf("self follow result = %+v", self)
	}

	m261Follow(t, ctx, adaConn, boSnap.You.ID, true)
	result := m261ReadFollowResult(t, ctx, adaConn)
	if !result.Followed || result.PlayerID != boSnap.You.ID || !strings.Contains(result.Text, "@bob") {
		t.Fatalf("follow result = %+v", result)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), bo.ID) || strings.Contains(string(raw), ada.ID) {
		t.Fatalf("follow result leaked account id: %s", raw)
	}
	prefs, ok, err := db.GetAccountPreferences(ada.ID)
	if err != nil || !ok || len(prefs.FollowedAccounts) != 1 || prefs.FollowedAccounts[0] != bo.ID {
		t.Fatalf("Ada follows = %+v, ok %v, err %v", prefs.FollowedAccounts, ok, err)
	}

	m261Follow(t, ctx, adaConn, guestSnap.You.ID, true)
	if guestResult := m261ReadFollowResult(t, ctx, adaConn); guestResult.Followed || !strings.Contains(guestResult.Text, "Guests") {
		t.Fatalf("guest follow result = %+v", guestResult)
	}
	m261Follow(t, ctx, adaConn, boSnap.You.ID, false)
	if unfollow := m261ReadFollowResult(t, ctx, adaConn); unfollow.Followed || !strings.Contains(unfollow.Text, "Unfollowed") {
		t.Fatalf("unfollow result = %+v", unfollow)
	}
	prefs, _, _ = db.GetAccountPreferences(ada.ID)
	if len(prefs.FollowedAccounts) != 0 {
		t.Fatalf("unfollow left durable accounts: %+v", prefs.FollowedAccounts)
	}
}

func TestM261PreferencesEndpointStoresSharingWithoutReturningFollowIDs(t *testing.T) {
	db := NewMemChatDatabase()
	secret := []byte("test-cookie-secret")
	account := AuthenticatedAccount{ID: "google:ada", Email: "ada@example.test", Name: "Ada"}
	if err := db.PutAccountPreferences(account.ID, AccountPreferences{
		Color:            "#112233",
		BlockedAccounts:  []string{"google:blocked"},
		FollowedAccounts: []string{"google:bo"},
		Profile:          AccountProfilePreferences{Handle: "ada", DisplayName: "Ada"},
	}); err != nil {
		t.Fatalf("seed preferences: %v", err)
	}
	server, _ := m193Server(t, "ALPHA", db, secret)
	api := &WebAPI{RoomManager: server.RoomManager, Server: server, Auth: server.Auth}
	httpServer := httptest.NewServer(api.Handler())
	defer httpServer.Close()
	cookie := signedAuthCookie(t, server.Auth, account)

	code, body := m193PutPreferences(t, httpServer.URL, cookie, `{"shareLocationWithFollowers":true}`)
	if code != http.StatusOK || !body.ShareLocationWithFollowers || body.Color != "#112233" || body.Profile.Handle != "ada" {
		t.Fatalf("share PUT = %d %+v", code, body)
	}
	prefs, ok, err := db.GetAccountPreferences(account.ID)
	if err != nil || !ok || len(prefs.FollowedAccounts) != 1 || prefs.FollowedAccounts[0] != "google:bo" || len(prefs.BlockedAccounts) != 1 {
		t.Fatalf("stored prefs after share PUT = %+v, ok %v, err %v", prefs, ok, err)
	}
	req, _ := http.NewRequest(http.MethodGet, httpServer.URL+"/api/preferences", nil)
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/preferences: %v", err)
	}
	defer resp.Body.Close()
	var raw map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode preferences: %v", err)
	}
	if _, leaked := raw["followedAccounts"]; leaked {
		t.Fatalf("preferences response leaked followedAccounts: %+v", raw)
	}
}

func TestM261FollowSurvivesHandleRenameAndSnapshotMarksFollowedPlayer(t *testing.T) {
	db := NewMemChatDatabase()
	secret := []byte("test-cookie-secret")
	ada := AuthenticatedAccount{ID: "google:ada", Email: "ada@example.test", Name: "Ada"}
	bo := AuthenticatedAccount{ID: "google:bo", Email: "bo@example.test", Name: "Bo"}
	if err := db.PutAccountPreferences(ada.ID, AccountPreferences{FollowedAccounts: []string{bo.ID}}); err != nil {
		t.Fatalf("seed Ada follows: %v", err)
	}
	if err := db.PutAccountPreferences(bo.ID, AccountPreferences{Profile: AccountProfilePreferences{Handle: "bo2", DisplayName: "Bo New"}}); err != nil {
		t.Fatalf("rename Bo handle: %v", err)
	}
	server, wsURL := m193Server(t, "ALPHA", db, secret)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	boConn, boSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Board: 1}, signedAuthCookie(t, server.Auth, bo))
	defer boConn.Close(websocket.StatusNormalClosure, "")
	adaConn, adaSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Board: 1}, signedAuthCookie(t, server.Auth, ada))
	defer adaConn.Close(websocket.StatusNormalClosure, "")

	if !m261HasID(adaSnap.FollowedPlayers, boSnap.You.ID) {
		t.Fatalf("Ada snapshot followedPlayers = %v, want Bo %d", adaSnap.FollowedPlayers, boSnap.You.ID)
	}
	foundRenamed := false
	for _, player := range adaSnap.Players {
		if player.ID == boSnap.You.ID {
			foundRenamed = player.Handle == "bo2"
		}
	}
	if !foundRenamed {
		t.Fatalf("Ada roster did not carry Bo's renamed public handle: %+v", adaSnap.Players)
	}
}

func TestM261WorldsFriendPresenceIsPerRecipientAndOptIn(t *testing.T) {
	db := NewMemChatDatabase()
	secret := []byte("test-cookie-secret")
	ada := AuthenticatedAccount{ID: "google:ada", Email: "ada@example.test", Name: "Ada"}
	bo := AuthenticatedAccount{ID: "google:bo", Email: "bo@example.test", Name: "Bo"}
	cy := AuthenticatedAccount{ID: "google:cy", Email: "cy@example.test", Name: "Cy"}
	if err := db.PutAccountPreferences(ada.ID, AccountPreferences{FollowedAccounts: []string{bo.ID, cy.ID}}); err != nil {
		t.Fatalf("seed Ada follows: %v", err)
	}
	if err := db.PutAccountPreferences(bo.ID, AccountPreferences{
		Profile:                    AccountProfilePreferences{Handle: "bob", DisplayName: "Bo"},
		ShareLocationWithFollowers: true,
	}); err != nil {
		t.Fatalf("seed Bo prefs: %v", err)
	}
	if err := db.PutAccountPreferences(cy.ID, AccountPreferences{
		Profile:                    AccountProfilePreferences{Handle: "cyd", DisplayName: "Cy"},
		ShareLocationWithFollowers: false,
	}); err != nil {
		t.Fatalf("seed Cy prefs: %v", err)
	}
	server, wsURL := m193Server(t, "ALPHA", db, secret)
	server.WorldsDir = t.TempDir()
	m1616WriteWorldFile(t, testEmptyWorld(t), filepath.Join(server.WorldsDir, "ALPHA.ZZT"))
	api := &WebAPI{RoomManager: server.RoomManager, Server: server, Auth: server.Auth}
	httpServer := httptest.NewServer(api.Handler())
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	boConn, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Board: 1}, signedAuthCookie(t, server.Auth, bo))
	defer boConn.Close(websocket.StatusNormalClosure, "")
	cyConn, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Board: 1}, signedAuthCookie(t, server.Auth, cy))
	defer cyConn.Close(websocket.StatusNormalClosure, "")

	adaWorlds := m261Worlds(t, httpServer.URL, signedAuthCookie(t, server.Auth, ada))
	alpha := m261WorldEntry(t, adaWorlds, "ALPHA")
	if len(alpha.FriendsHere) != 1 || alpha.FriendsHere[0].Handle != "bob" {
		t.Fatalf("Ada friend presence = %+v, want only opted-in Bo", alpha.FriendsHere)
	}
	raw, _ := json.Marshal(adaWorlds)
	if strings.Contains(string(raw), bo.ID) || strings.Contains(string(raw), cy.ID) {
		t.Fatalf("/api/worlds leaked account ids: %s", raw)
	}

	guestWorlds := m261Worlds(t, httpServer.URL, nil)
	if friends := m261WorldEntry(t, guestWorlds, "ALPHA").FriendsHere; len(friends) != 0 {
		t.Fatalf("guest /api/worlds saw friends: %+v", friends)
	}

	boPrefs, _, _ := db.GetAccountPreferences(bo.ID)
	boPrefs.ShareLocationWithFollowers = false
	if err := db.PutAccountPreferences(bo.ID, boPrefs); err != nil {
		t.Fatalf("turn off Bo location: %v", err)
	}
	if friends := m261WorldEntry(t, m261Worlds(t, httpServer.URL, signedAuthCookie(t, server.Auth, ada)), "ALPHA").FriendsHere; len(friends) != 0 {
		t.Fatalf("opted-out Bo still visible: %+v", friends)
	}
}

func m261Follow(t *testing.T, ctx context.Context, conn *websocket.Conn, target PlayerID, follow bool) {
	t.Helper()
	if err := wsjson.Write(ctx, conn, FollowMessage{Type: MessageTypeFollow, PlayerID: target, Follow: follow}); err != nil {
		t.Fatalf("write follow: %v", err)
	}
}

func m261ReadFollowResult(t *testing.T, ctx context.Context, conn *websocket.Conn) FollowResultMessage {
	t.Helper()
	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, conn, &raw); err != nil {
			t.Fatalf("read follow result: %v", err)
		}
		var result FollowResultMessage
		if err := json.Unmarshal(raw, &result); err == nil && result.Type == MessageTypeFollowResult {
			return result
		}
	}
}

func m261HasID(ids []PlayerID, want PlayerID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func m261Worlds(t *testing.T, baseURL string, cookie *http.Cookie) []WorldListEntry {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api/worlds", nil)
	if err != nil {
		t.Fatalf("build /api/worlds: %v", err)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/worlds: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Worlds []WorldListEntry `json:"worlds"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode /api/worlds: %v", err)
	}
	return out.Worlds
}

func m261WorldEntry(t *testing.T, worlds []WorldListEntry, name string) WorldListEntry {
	t.Helper()
	for _, entry := range worlds {
		if entry.World == name {
			return entry
		}
	}
	t.Fatalf("world %s not listed in %+v", name, worlds)
	return WorldListEntry{}
}
