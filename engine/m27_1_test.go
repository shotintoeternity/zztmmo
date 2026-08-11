package zztgo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

func TestM271FavoritePreferencesValidateCapTogglePersistAndPreserveFields(t *testing.T) {
	for _, tc := range []struct {
		name string
		db   func(t *testing.T) ChatDatabase
	}{
		{name: "mem", db: func(t *testing.T) ChatDatabase { return NewMemChatDatabase() }},
		{name: "file", db: func(t *testing.T) ChatDatabase {
			db, err := NewFileChatDatabase(filepath.Join(t.TempDir(), "chat.jsonl"))
			if err != nil {
				t.Fatalf("NewFileChatDatabase: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })
			return db
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := tc.db(t)
			input := []string{"alpha", "../bad", "BETA", "ALPHA"}
			for i := 0; i < MaxFavoriteWorlds+4; i++ {
				input = append(input, "W"+strings.Repeat("A", i%6)+string(rune('A'+i%26)))
			}
			prefs := AccountPreferences{
				Color:                      "#112233",
				BlockedAccounts:            []string{"google:blocked"},
				Hints:                      AccountHintPreferences{Players: true, Death: true, Chat: true},
				Profile:                    AccountProfilePreferences{Handle: "Ada", DisplayName: "Ada", About: []string{"Builder"}},
				FollowedAccounts:           []string{"google:bo"},
				ShareLocationWithFollowers: true,
				FavoriteWorlds:             input,
			}
			if err := db.PutAccountPreferences("google:ada", prefs); err != nil {
				t.Fatalf("PutAccountPreferences: %v", err)
			}
			got, ok, err := db.GetAccountPreferences("google:ada")
			if err != nil || !ok {
				t.Fatalf("GetAccountPreferences = (_, %v, %v)", ok, err)
			}
			if got.Color != "#112233" || len(got.BlockedAccounts) != 1 || !got.Hints.Chat || got.Profile.Handle != "ada" || len(got.FollowedAccounts) != 1 || !got.ShareLocationWithFollowers {
				t.Fatalf("sibling preference fields were not preserved: %+v", got)
			}
			if len(got.FavoriteWorlds) > MaxFavoriteWorlds {
				t.Fatalf("favorite cap not enforced: %d > %d", len(got.FavoriteWorlds), MaxFavoriteWorlds)
			}
			firstFavorites := got.FavoriteWorlds
			if len(firstFavorites) > 2 {
				firstFavorites = firstFavorites[:2]
			}
			if len(firstFavorites) < 2 || firstFavorites[0] != "ALPHA" || firstFavorites[1] != "BETA" {
				t.Fatalf("favorites were not sanitized/deduped in order: %+v", firstFavorites)
			}
			for _, world := range got.FavoriteWorlds {
				if strings.Contains(world, ".") || world != strings.ToUpper(world) {
					t.Fatalf("invalid favorite survived: %+v", got.FavoriteWorlds)
				}
			}
		})
	}

	db := NewMemChatDatabase()
	secret := []byte("test-cookie-secret")
	account := AuthenticatedAccount{ID: "google:ada", Email: "ada@example.test", Name: "Ada"}
	server, _ := m193Server(t, "ALPHA", db, secret)
	server.WorldsDir = t.TempDir()
	m1616WriteWorldFile(t, testEmptyWorld(t), filepath.Join(server.WorldsDir, "ALPHA.ZZT"))
	api := &WebAPI{RoomManager: server.RoomManager, Server: server, Auth: server.Auth}
	httpServer := httptest.NewServer(api.Handler())
	defer httpServer.Close()
	cookie := signedAuthCookie(t, server.Auth, account)

	code, body := m193PutPreferences(t, httpServer.URL, cookie, `{"color":"#445566","favoriteWorld":"ALPHA","favorite":true}`)
	if code != http.StatusOK || !containsString(body.FavoriteWorlds, "ALPHA") || body.Color != "#445566" {
		t.Fatalf("favorite toggle PUT = %d %+v", code, body)
	}
	code, body = m193PutPreferences(t, httpServer.URL, cookie, `{"favoriteWorld":"ALPHA","favorite":false}`)
	if code != http.StatusOK || containsString(body.FavoriteWorlds, "ALPHA") || body.Color != "#445566" {
		t.Fatalf("favorite untoggle PUT = %d %+v", code, body)
	}
	if code, _ := m193PutPreferences(t, httpServer.URL, cookie, `{"favoriteWorld":"MISSING","favorite":true}`); code != http.StatusBadRequest {
		t.Fatalf("missing favorite world PUT = %d, want 400", code)
	}
	if code, _ := m193PutPreferences(t, httpServer.URL, nil, `{"favoriteWorld":"ALPHA","favorite":true}`); code != http.StatusUnauthorized {
		t.Fatalf("guest favorite PUT = %d, want 401", code)
	}
}

func TestM271WorldActivityCountsOnlyFreshActiveJoinsAndPersists(t *testing.T) {
	db := NewMemChatDatabase()
	secret := []byte("test-cookie-secret")
	server, wsURL := m193Server(t, "ALPHA", db, secret)
	path := filepath.Join(t.TempDir(), "world_activity.json")
	activity, err := NewWorldActivityStore(path)
	if err != nil {
		t.Fatalf("NewWorldActivityStore: %v", err)
	}
	server.Activity = activity
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, snap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Ada", Board: 1}, nil)
	token := snap.ResumeToken
	if got := server.Activity.Counts()["ALPHA"]; got != 1 {
		t.Fatalf("fresh play count = %d, want 1", got)
	}

	conn.Close(websocket.StatusAbnormalClosure, "resume")
	waitFor(t, "detach before resume", func() bool {
		_, ok := detachedCount(server, snap.You.ID)
		return ok
	})
	resumed, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Ada", Board: 1, ResumeToken: token}, nil)
	defer resumed.Close(websocket.StatusNormalClosure, "")
	if got := server.Activity.Counts()["ALPHA"]; got != 1 {
		t.Fatalf("resume incremented play count to %d", got)
	}

	watcher, _ := m221Watch(t, ctx, wsURL, 1)
	defer watcher.Close(websocket.StatusNormalClosure, "")
	if got := server.Activity.Counts()["ALPHA"]; got != 1 {
		t.Fatalf("spectator incremented play count to %d", got)
	}

	api := &WebAPI{RoomManager: server.RoomManager, Server: server, Auth: server.Auth}
	httpServer := httptest.NewServer(api.Handler())
	defer httpServer.Close()
	resp, err := http.Get(httpServer.URL + "/api/title?world=ALPHA")
	if err != nil {
		t.Fatalf("GET title: %v", err)
	}
	resp.Body.Close()
	if got := server.Activity.Counts()["ALPHA"]; got != 1 {
		t.Fatalf("title preview incremented play count to %d", got)
	}

	reopened, err := NewWorldActivityStore(path)
	if err != nil {
		t.Fatalf("reopen activity: %v", err)
	}
	if got := reopened.Counts()["ALPHA"]; got != 1 {
		t.Fatalf("persisted play count = %d, want 1", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read activity file: %v", err)
	}
	if strings.Contains(string(data), "google:") || strings.Contains(string(data), "Ada") {
		t.Fatalf("activity store contains per-player data: %s", data)
	}
}

func TestM271WorldsShelvesArePerRecipientAndReferenceFlatWorlds(t *testing.T) {
	db := NewMemChatDatabase()
	secret := []byte("test-cookie-secret")
	ada := AuthenticatedAccount{ID: "google:ada", Email: "ada@example.test", Name: "Ada"}
	bo := AuthenticatedAccount{ID: "google:bo", Email: "bo@example.test", Name: "Bo"}
	if err := db.PutAccountPreferences(ada.ID, AccountPreferences{FavoriteWorlds: []string{"ALPHA"}, FollowedAccounts: []string{bo.ID}}); err != nil {
		t.Fatalf("seed Ada prefs: %v", err)
	}
	if err := db.PutAccountPreferences(bo.ID, AccountPreferences{
		Profile:                    AccountProfilePreferences{Handle: "bob", DisplayName: "Bo"},
		ShareLocationWithFollowers: true,
	}); err != nil {
		t.Fatalf("seed Bo prefs: %v", err)
	}
	server, wsURL := m193Server(t, "TOWN", db, secret)
	server.WorldsDir = t.TempDir()
	for _, name := range []string{"TOWN", "ALPHA", "BETA", "CAVES", "DREAM"} {
		m1616WriteWorldFile(t, testEmptyWorld(t), filepath.Join(server.WorldsDir, name+".ZZT"))
	}
	if err := os.WriteFile(filepath.Join(server.WorldsDir, "DREAM.zwd"), []byte("world DREAM\n"), 0o644); err != nil {
		t.Fatalf("write dream sidecar: %v", err)
	}
	server.Activity = &WorldActivityStore{counts: map[string]int{"CAVES": 7, "ALPHA": 2}}
	api := &WebAPI{RoomManager: server.RoomManager, Server: server, Auth: server.Auth}
	httpServer := httptest.NewServer(api.Handler())
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	boConn, _ := dialJoinWithCookie(t, ctx, wsURL+"?world=BETA", JoinMessage{Type: MessageTypeJoin, Board: 1}, signedAuthCookie(t, server.Auth, bo))
	defer boConn.Close(websocket.StatusNormalClosure, "")

	adaResp := m271Worlds(t, httpServer.URL, signedAuthCookie(t, server.Auth, ada))
	flat := make(map[string]WorldListEntry, len(adaResp.Worlds))
	for _, entry := range adaResp.Worlds {
		flat[entry.World] = entry
	}
	if !flat["ALPHA"].Favorite || flat["ALPHA"].PlayCount != 2 || flat["CAVES"].PlayCount != 7 {
		t.Fatalf("favorite/play counts not annotated: %+v %+v", flat["ALPHA"], flat["CAVES"])
	}
	if friends := flat["BETA"].FriendsHere; len(friends) != 1 || friends[0].Handle != "bob" {
		t.Fatalf("friend presence = %+v, want Bo on BETA", friends)
	}
	for _, want := range []string{"favorites", "active", "played", "classics", "dreams"} {
		if !m271HasShelf(adaResp.Shelves, want) {
			t.Fatalf("missing shelf %q in %+v", want, adaResp.Shelves)
		}
	}
	// The archive outranks the day's dreaming (owner 2026-08-11). Asserted as
	// relative position rather than as an index, so adding a shelf between them
	// does not read as a regression.
	classicsAt, dreamsAt := -1, -1
	for i, shelf := range adaResp.Shelves {
		switch shelf.ID {
		case "classics":
			classicsAt = i
		case "dreams":
			dreamsAt = i
		}
	}
	if classicsAt < 0 || dreamsAt < 0 || classicsAt > dreamsAt {
		t.Fatalf("Classics must sit above Recent dreams, got classics=%d dreams=%d in %+v", classicsAt, dreamsAt, adaResp.Shelves)
	}
	// Nothing pins a first-party world to the top of the picker any more.
	for _, shelf := range adaResp.Shelves {
		if shelf.ID == "start" || shelf.ID == "lobby" || shelf.ID == "arena" {
			t.Fatalf("removed shelf %q is still served: %+v", shelf.ID, adaResp.Shelves)
		}
	}
	for _, shelf := range adaResp.Shelves {
		for _, world := range shelf.Worlds {
			if _, ok := flat[world]; !ok {
				t.Fatalf("shelf %s references %s outside flat worlds %+v", shelf.ID, world, flat)
			}
		}
	}
	raw, _ := json.Marshal(adaResp)
	if strings.Contains(string(raw), ada.ID) || strings.Contains(string(raw), bo.ID) {
		t.Fatalf("/api/worlds leaked account ids: %s", raw)
	}

	guestResp := m271Worlds(t, httpServer.URL, nil)
	for _, entry := range guestResp.Worlds {
		if entry.Favorite || len(entry.FriendsHere) != 0 {
			t.Fatalf("guest saw recipient-only data: %+v", entry)
		}
	}
	if m271HasShelf(guestResp.Shelves, "favorites") {
		t.Fatalf("guest got a favorites shelf: %+v", guestResp.Shelves)
	}
}

func TestM271TitleThumbnailCacheHitInvalidationAndFallback(t *testing.T) {
	db := NewMemChatDatabase()
	server, _ := m193Server(t, "TOWN", db, []byte("test-cookie-secret"))
	server.WorldsDir = t.TempDir()
	m1616WriteWorldFile(t, testEmptyWorld(t), filepath.Join(server.WorldsDir, "BETA.ZZT"))
	if err := os.WriteFile(filepath.Join(server.WorldsDir, "BROKEN.ZZT"), []byte("not a zzt world"), 0o644); err != nil {
		t.Fatalf("write broken world: %v", err)
	}
	api := &WebAPI{RoomManager: server.RoomManager, Server: server, Auth: server.Auth}
	httpServer := httptest.NewServer(api.Handler())
	defer httpServer.Close()

	first := m271WorldEntry(t, m271Worlds(t, httpServer.URL, nil).Worlds, "BETA")
	if first.Thumbnail == nil || len(first.Thumbnail.Cells) == 0 {
		t.Fatalf("BETA thumbnail missing: %+v", first)
	}
	if _, ok := server.Instances["BETA"]; ok {
		t.Fatal("thumbnail render instantiated a live BETA room")
	}
	second := m271WorldEntry(t, m271Worlds(t, httpServer.URL, nil).Worlds, "BETA")
	if second.Thumbnail == nil || second.Thumbnail.Key != first.Thumbnail.Key {
		t.Fatalf("cache hit changed key: first=%+v second=%+v", first.Thumbnail, second.Thumbnail)
	}
	if len(api.thumbnailCache) != 1 {
		t.Fatalf("cache hit created extra entries: %d", len(api.thumbnailCache))
	}

	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(server.WorldsDir, "BETA.ZZT"), future, future); err != nil {
		t.Fatalf("touch BETA: %v", err)
	}
	third := m271WorldEntry(t, m271Worlds(t, httpServer.URL, nil).Worlds, "BETA")
	if third.Thumbnail == nil || third.Thumbnail.Key == first.Thumbnail.Key {
		t.Fatalf("thumbnail cache did not invalidate after source change: first=%q third=%+v", first.Thumbnail.Key, third.Thumbnail)
	}
	broken := m271WorldEntry(t, m271Worlds(t, httpServer.URL, nil).Worlds, "BROKEN")
	if broken.Thumbnail != nil {
		t.Fatalf("broken world got a thumbnail instead of text fallback: %+v", broken.Thumbnail)
	}
}

type m271WorldsResponse struct {
	Worlds  []WorldListEntry `json:"worlds"`
	Shelves []WorldShelf     `json:"shelves"`
}

func m271Worlds(t *testing.T, baseURL string, cookie *http.Cookie) m271WorldsResponse {
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
	var out m271WorldsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode /api/worlds: %v", err)
	}
	return out
}

func m271WorldEntry(t *testing.T, worlds []WorldListEntry, name string) WorldListEntry {
	t.Helper()
	for _, entry := range worlds {
		if entry.World == name {
			return entry
		}
	}
	t.Fatalf("world %s not listed in %+v", name, worlds)
	return WorldListEntry{}
}

func m271HasShelf(shelves []WorldShelf, id string) bool {
	for _, shelf := range shelves {
		if shelf.ID == id {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
