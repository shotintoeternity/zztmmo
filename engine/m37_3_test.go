package zztgo

// M37.3: a signed-in player can download everything the server keeps about
// their account, and nothing of anyone else's.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestM373ExportHoldsTheAccountsDataAndNobodyElses(t *testing.T) {
	withChallengeCatalogue(t, gemDashFixture())
	h := m3710Server(t)
	s := h.server
	at := time.Date(2026, 11, 26, 8, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return at }

	bob := "google:m373-bob"
	quiet := "google:m373-no-username"
	h.claim(t, m3710Ada.ID, "ada")
	h.claim(t, bob, "bob")
	if err := s.ChatDB.UpdateAccountPreferences(m3710Ada.ID, func(p *AccountPreferences, _ bool) error {
		p.Color = "#a1b2c3"
		p.FollowedAccounts = []string{bob}
		p.BlockedAccounts = []string{quiet}
		p.FavoriteWorlds = []string{"TOWN"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	s.Auth.Accounts = NewAccountStore("")
	if _, err := s.Auth.Accounts.RecordSignIn(m3710Ada, at.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.ChatDB.PutPlayerState(m3710Ada.ID, "TOWN", PlayerState{Ammo: 12, Gems: 3}); err != nil {
		t.Fatal(err)
	}
	if err := s.ChatDB.PutPlayerState(bob, "TOWN", PlayerState{Ammo: 99}); err != nil {
		t.Fatal(err)
	}
	for _, line := range []struct {
		author ChatAuthor
		text   string
	}{
		{ChatAuthor{Name: "ada", AccountID: m3710Ada.ID}, "hello from ada"},
		{ChatAuthor{Name: "bob", AccountID: bob}, "hello from bob"},
		{ChatAuthor{Name: "Gus"}, "hello from a guest"},
	} {
		if _, err := s.ChatDB.AddMessage(line.author, line.text); err != nil {
			t.Fatal(err)
		}
	}

	store, err := NewChallengeStore("")
	if err != nil {
		t.Fatal(err)
	}
	s.ChallengeStore = store
	def := gemDashFixture()
	for _, r := range []ChallengeResult{
		{ChallengeID: def.ID, Version: def.Version, AccountKey: m3710Ada.ID, Name: "ada", Ticks: 120, Score: 30, Gems: 3, RecordingID: "rec-ada"},
		{ChallengeID: def.ID, Version: def.Version, AccountKey: bob, Name: "bob", Ticks: 99, Score: 30, Gems: 3, RecordingID: "rec-bob"},
	} {
		if _, err := store.Submit(r); err != nil {
			t.Fatal(err)
		}
	}

	s.WorldsDir = t.TempDir()
	if err := writeWorldAccess(s.WorldsDir, "MINE", WorldAccess{OwnerAccountID: m3710Ada.ID, OwnerName: "ada"}); err != nil {
		t.Fatal(err)
	}
	if err := writeWorldAccess(s.WorldsDir, "SHARED", WorldAccess{OwnerAccountID: bob, OwnerName: "bob", CollaboratorAccountIDs: []string{m3710Ada.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := writeWorldAccess(s.WorldsDir, "BOBS", WorldAccess{OwnerAccountID: bob, OwnerName: "bob"}); err != nil {
		t.Fatal(err)
	}

	cookie := signedAuthCookie(t, s.Auth, m3710Ada)
	req, _ := http.NewRequest(http.MethodGet, h.http.URL+"/api/account/export", nil)
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export status %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Disposition"); got != `attachment; filename="zztmmo-ada-data.json"` {
		t.Fatalf("Content-Disposition = %q", got)
	}
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	var got AccountExport
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}

	if got.AccountID != m3710Ada.ID || got.Email != m3710Ada.Email || got.GoogleName != m3710Ada.Name || got.SignIns != 1 {
		t.Fatalf("identity = %+v", got)
	}
	if got.FirstSignIn == nil || !got.FirstSignIn.Equal(at.Add(-time.Hour)) || !got.ExportedAt.Equal(at) {
		t.Fatalf("dates: first=%v exported=%v", got.FirstSignIn, got.ExportedAt)
	}
	if got.Username != "ada" || got.Settings.Color != "#a1b2c3" || len(got.Settings.FavoriteWorlds) != 1 {
		t.Fatalf("settings = %+v / %+v", got.Username, got.Settings)
	}
	if strings.Join(got.Following, ",") != "bob" || strings.Join(got.Blocking, ",") != noUsernameLabel {
		t.Fatalf("following=%v blocking=%v", got.Following, got.Blocking)
	}
	if len(got.Progress) != 1 || got.Progress["TOWN"].Ammo != 12 {
		t.Fatalf("progress = %+v", got.Progress)
	}
	if len(got.Chat) != 1 || string(got.Chat[0].Text) != "hello from ada" {
		t.Fatalf("chat = %+v", got.Chat)
	}
	if len(got.Challenges) != 1 || got.Challenges[0].RecordingID != "rec-ada" || got.Challenges[0].Ticks != 120 {
		t.Fatalf("challenges = %+v", got.Challenges)
	}
	if len(got.Worlds) != 2 || got.Worlds[0] != (AccountExportWorld{World: "MINE", Role: "owner"}) || got.Worlds[1] != (AccountExportWorld{World: "SHARED", Role: "collaborator"}) {
		t.Fatalf("worlds = %+v", got.Worlds)
	}
	// Nothing of anyone else's: no other account id, and none of bob's data.
	for _, leak := range []string{bob, quiet, "hello from bob", "hello from a guest", "rec-bob", "BOBS"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("the export carries %q: %s", leak, raw)
		}
	}

	// A guest has nothing to export.
	guest, err := http.Get(h.http.URL + "/api/account/export")
	if err != nil {
		t.Fatal(err)
	}
	guest.Body.Close()
	if guest.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a guest's export = %d, want 401", guest.StatusCode)
	}
}

// The file store lists the same things across a restart.
func TestM373TheFileStoreListsAnAccountsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.jsonl")
	db, err := NewFileChatDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddMessage(ChatAuthor{Name: "ada", AccountID: "google:ada"}, "hi"); err != nil {
		t.Fatal(err)
	}
	if err := db.PutPlayerState("google:ada", "TOWN", PlayerState{Gems: 5}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	reopened, err := NewFileChatDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if msgs := reopened.MessagesByAccount("google:ada"); len(msgs) != 1 || string(msgs[0].Text) != "hi" {
		t.Fatalf("messages = %+v", msgs)
	}
	if states := reopened.PlayerStatesByAccount("google:ada"); states["TOWN"].Gems != 5 {
		t.Fatalf("states = %+v", states)
	}
	if len(reopened.MessagesByAccount("")) != 0 {
		t.Fatal("an empty account id matched guest lines")
	}
	_ = os.Remove(path)
}
