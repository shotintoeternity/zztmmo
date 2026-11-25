package zztgo

// M37.3a: a signed-in player can delete their account. What deletion does was
// the owner's decision (TASKS.md M37.3): chat lines stay as "(deleted
// player)"; worlds, challenge times and replays stay, unowned and anonymous;
// moderation records survive.

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
	"nhooyr.io/websocket/wsjson"
)

func m373aPostDelete(t *testing.T, baseURL string, cookie *http.Cookie, confirm string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/account/delete", strings.NewReader(`{"confirm":"`+confirm+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/account/delete: %v", err)
	}
	resp.Body.Close()
	return resp
}

// Every store, on disk, through the real endpoint; then everything reopened.
func TestM373aDeletingAnAccountForgetsItEverywhere(t *testing.T) {
	h := m321Server(t)
	s := h.server
	chatPath := filepath.Join(h.savesDir, "chat.jsonl")
	db, err := NewFileChatDatabase(chatPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s.ChatDB = db
	s.RequireUsername = true
	s.Auth.Accounts = NewAccountStore(AccountStorePath(h.savesDir))
	s.Refusals = NewRefusalStore(filepath.Join(h.savesDir, "refused.json"))
	api := &WebAPI{RoomManager: s.RoomManager, World: s.DefaultInstance.SourceWorld, SavesDir: h.savesDir, Server: s, Auth: s.Auth}
	mux := http.NewServeMux()
	mux.Handle("/api/", api.Handler())
	apiServer := httptest.NewServer(mux)
	t.Cleanup(apiServer.Close)

	ada := m321Ada
	bob := "google:m373a-bob"
	claim := func(id, name string) {
		t.Helper()
		if err := db.UpdateAccountPreferences(id, func(p *AccountPreferences, _ bool) error {
			p.Profile.Handle = name
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	claim(ada.ID, "ada")
	claim(bob, "bob")
	if err := db.UpdateAccountPreferences(bob, func(p *AccountPreferences, _ bool) error {
		p.FollowedAccounts = []string{ada.ID}
		p.BlockedAccounts = []string{ada.ID, "google:someone"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Auth.Accounts.RecordSignIn(ada, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := db.PutPlayerState(ada.ID, "TOWN", PlayerState{Gems: 7}); err != nil {
		t.Fatal(err)
	}
	if err := db.PutPlayerState(bob, "TOWN", PlayerState{Gems: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddMessage(ChatAuthor{Name: "ada", AccountID: ada.ID}, "ada was here"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddMessage(ChatAuthor{Name: "bob", AccountID: bob}, "bob was here"); err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"ADASAVE": ada.ID, "BOBSAVE": bob} {
		if err := writeSnapshotPlayerStateSidecar(filepath.Join(h.savesDir, name+".SAV"), "TOWN", id, PlayerState{Gems: 2}); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeWorldAccess(h.worldsDir, "MINE", WorldAccess{OwnerAccountID: ada.ID, OwnerName: "ada"}); err != nil {
		t.Fatal(err)
	}
	if err := writeWorldAccess(h.worldsDir, "SHARED", WorldAccess{OwnerAccountID: bob, OwnerName: "bob", CollaboratorAccountIDs: []string{ada.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := writeWorldMeta(h.worldsDir, "DREAM", WorldMeta{Title: "A Dream", Author: "ada"}); err != nil {
		t.Fatal(err)
	}

	// A real, signed-in challenge run, finished and recorded.
	conn, snap := h.run(t, "?challenge=gem-dash", &ada)
	inst, ok := s.ChallengeRunInstance(snap.ChallengeRun)
	if !ok {
		t.Fatal("no run instance")
	}
	run := m321Finish(t, h, inst, snap.You.ID)
	conn.Close(websocket.StatusNormalClosure, "")
	s.CloseRecorders()
	recording := filepath.Join(h.recordDir, run.RecordingID+".jsonl")
	if data, _ := os.ReadFile(recording); !strings.Contains(string(data), `"name":"ada"`) {
		t.Fatalf("the run's recording does not name ada before deletion")
	}
	if ok, err := s.VerifyChallengeResult(run.Result); err != nil || !ok {
		t.Fatalf("the run does not verify before deletion: %v %v", ok, err)
	}

	// A moderator refused the account. Deleting it must not lift that.
	if err := s.Refusals.Refuse(RefusedAccount{Account: ada.ID, Name: "ada", At: time.Now()}); err != nil {
		t.Fatal(err)
	}

	cookie := signedAuthCookie(t, s.Auth, ada)
	// The wrong username deletes nothing.
	if resp := m373aPostDelete(t, apiServer.URL, cookie, "bob"); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a wrong confirmation = %d, want 400", resp.StatusCode)
	}
	if db.HandleOwner("ada") != ada.ID {
		t.Fatal("a refused confirmation deleted the account")
	}
	if resp := m373aPostDelete(t, apiServer.URL, nil, "ada"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a guest's delete = %d, want 401", resp.StatusCode)
	}
	resp := m373aPostDelete(t, apiServer.URL, cookie, "@ADA")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	cleared := false
	for _, c := range resp.Cookies() {
		if c.Name == authSessionCookie && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("the delete did not clear the session cookie")
	}
	// The log still takes lines after its rewrite.
	if _, err := db.AddMessage(ChatAuthor{Name: "bob", AccountID: bob}, "after the rewrite"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	// Everything reopened from disk.
	reopened, err := NewFileChatDatabase(chatPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, ok, _ := reopened.GetAccountPreferences(ada.ID); ok {
		t.Fatal("ada's preferences survived")
	}
	if reopened.HandleOwner("ada") != "" {
		t.Fatal("the username ada was not freed")
	}
	bobPrefs, _, _ := reopened.GetAccountPreferences(bob)
	if len(bobPrefs.FollowedAccounts) != 0 || strings.Join(bobPrefs.BlockedAccounts, ",") != "google:someone" {
		t.Fatalf("bob still lists ada: follows=%v blocks=%v", bobPrefs.FollowedAccounts, bobPrefs.BlockedAccounts)
	}
	if states := reopened.PlayerStatesByAccount(ada.ID); len(states) != 0 {
		t.Fatalf("ada's progress survived: %+v", states)
	}
	if states := reopened.PlayerStatesByAccount(bob); states["TOWN"].Gems != 1 {
		t.Fatalf("bob's progress was touched: %+v", states)
	}
	msgs, _ := reopened.GetRecentMessages(10)
	var lines []string
	for _, m := range msgs {
		lines = append(lines, string(m.From)+": "+string(m.Text))
		if m.AccountID == ada.ID {
			t.Fatalf("a chat line still carries ada's account: %+v", m)
		}
	}
	if got := strings.Join(lines, " | "); got != "(deleted player): ada was here | bob: bob was here | bob: after the rewrite" {
		t.Fatalf("chat after deletion = %q", got)
	}

	if _, err := os.Stat(snapshotPlayerStateSidecarPath(filepath.Join(h.savesDir, "ADASAVE.SAV"))); !os.IsNotExist(err) {
		t.Fatalf("ada's save sidecar survived: %v", err)
	}
	if _, err := os.Stat(snapshotPlayerStateSidecarPath(filepath.Join(h.savesDir, "BOBSAVE.SAV"))); err != nil {
		t.Fatalf("bob's save sidecar went: %v", err)
	}

	mine, _, _ := loadWorldAccess(h.worldsDir, "MINE")
	shared, _, _ := loadWorldAccess(h.worldsDir, "SHARED")
	if mine.OwnerAccountID != "" || mine.OwnerName != "" {
		t.Fatalf("MINE still has an owner: %+v", mine)
	}
	if shared.OwnerAccountID != bob || len(shared.CollaboratorAccountIDs) != 0 {
		t.Fatalf("SHARED = %+v, want bob's with no collaborators", shared)
	}
	if meta, _, _ := loadWorldMeta(h.worldsDir, "DREAM"); meta.Author != DeletedPlayerName || meta.Title != "A Dream" {
		t.Fatalf("DREAM meta = %+v", meta)
	}

	store, err := NewChallengeStore(ChallengeStorePath(h.savesDir))
	if err != nil {
		t.Fatal(err)
	}
	rows := store.Leaderboard("gem-dash", ada.ID)
	if len(rows) != 1 || string(rows[0].Name) != DeletedPlayerName || rows[0].You {
		t.Fatalf("the leaderboard after deletion = %+v", rows)
	}
	row, ok := store.ResultForRecording("gem-dash", run.RecordingID)
	if !ok || !strings.HasPrefix(row.AccountKey, "deleted:") {
		t.Fatalf("the stored row = %+v", row)
	}
	data, _ := os.ReadFile(recording)
	if strings.Contains(string(data), `"name":"ada"`) || !strings.Contains(string(data), DeletedPlayerName) {
		t.Fatal("the recording still names ada")
	}
	// The decisive check: names are presentation, so the rewritten recording
	// still replays to the evidence its row carries.
	if ok, err := s.VerifyChallengeResult(row); err != nil || !ok {
		t.Fatalf("the anonymized run no longer verifies: ok=%v err=%v", ok, err)
	}

	if _, ok := NewAccountStore(AccountStorePath(h.savesDir)).Get(ada.ID); ok {
		t.Fatal("ada's sign-in record survived")
	}
	if !NewRefusalStore(filepath.Join(h.savesDir, "refused.json")).Refuses(ada.ID) {
		t.Fatal("deleting the account lifted its refusal")
	}
}

// A connected player is told, disconnected, and nothing is written back.
func TestM373aALivePlayerIsDisconnectedAndNothingComesBack(t *testing.T) {
	h := m3710Server(t)
	h.claim(t, m3710Ada.ID, "ada")
	cookie := signedAuthCookie(t, h.server.Auth, m3710Ada)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, h.wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Cookie": []string{cookie.String()}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(ServerReadLimit)
	if err := wsjson.Write(ctx, conn, JoinMessage{Type: MessageTypeJoin, Name: "ada"}); err != nil {
		t.Fatal(err)
	}
	notices := make(chan ModerationNoticeMessage, 1)
	go func() {
		for {
			var raw json.RawMessage
			if err := wsjson.Read(ctx, conn, &raw); err != nil {
				close(notices)
				return
			}
			var n ModerationNoticeMessage
			if json.Unmarshal(raw, &n) == nil && n.Type == MessageTypeModerationNotice {
				notices <- n
			}
		}
	}()
	inst := h.server.DefaultInstance
	waitFor(t, "ada in the room", func() bool {
		inst.mu.Lock()
		defer inst.mu.Unlock()
		return len(inst.RoomManager.players) == 1
	})

	report, err := h.server.DeleteAccount(m3710Ada, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Connections != 1 {
		t.Fatalf("ended %d connections, want 1", report.Connections)
	}
	select {
	case n, ok := <-notices:
		if !ok || n.Text != "This account has been deleted." || !n.Ended {
			t.Fatalf("notice = %+v (ok=%v)", n, ok)
		}
	case <-ctx.Done():
		t.Fatal("no deletion notice")
	}
	// The disconnect runs its usual detach, which persists a signed-in
	// player's progress. The room no longer knows the account, so nothing is.
	waitFor(t, "the player to detach", func() bool {
		inst.mu.Lock()
		defer inst.mu.Unlock()
		return len(inst.Detached) == 1 || len(inst.RoomManager.players) == 0
	})
	time.Sleep(100 * time.Millisecond)
	if states := h.server.ChatDB.(accountDataStore).PlayerStatesByAccount(m3710Ada.ID); len(states) != 0 {
		t.Fatalf("the disconnect wrote ada's progress back: %+v", states)
	}
	if h.server.accountUsername(m3710Ada.ID) != "" {
		t.Fatal("ada's username survived")
	}
}
