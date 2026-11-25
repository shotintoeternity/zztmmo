package zztgo

// M37.11: where usernames are required, a guest cannot take one somebody has
// claimed, in any capitalisation or with a leading @. The launch box asks
// /api/names/reserved first; the join refuses it regardless.

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestM3711AGuestCannotTakeAClaimedUsername(t *testing.T) {
	h := m3710Server(t)
	h.claim(t, m3710Ada.ID, "ada")

	for _, name := range []string{"ada", "ADA", " Ada ", "@ada"} {
		typ, raw := h.firstMessage(t, nil, JoinMessage{Type: MessageTypeJoin, Name: name})
		var notice ModerationNoticeMessage
		if err := json.Unmarshal(raw, &notice); err != nil || typ != MessageTypeModerationNotice || notice.Text != reservedNameText || !notice.Ended {
			t.Fatalf("a guest named %q got %s %s, want the reserved-name notice", name, typ, raw)
		}
	}
	for _, name := range []string{"adam", "Gus", "a d a"} {
		if typ, raw := h.firstMessage(t, nil, JoinMessage{Type: MessageTypeJoin, Name: name}); typ != MessageTypeSnapshot {
			t.Fatalf("a guest named %q got %s %s, want a snapshot", name, typ, raw)
		}
	}
	// Ada herself, signed in, is not a guest.
	if typ, raw := h.firstMessage(t, signedAuthCookie(t, h.server.Auth, m3710Ada), JoinMessage{Type: MessageTypeJoin, Name: "ada"}); typ != MessageTypeSnapshot {
		t.Fatalf("Ada signed in got %s %s, want a snapshot", typ, raw)
	}
	// A watcher's name is not read at all.
	if typ, raw := h.firstMessage(t, nil, JoinMessage{Type: MessageTypeJoin, Name: "ada", Spectate: true}); typ == MessageTypeModerationNotice {
		t.Fatalf("a watcher named ada was refused: %s", raw)
	}

	// The box's question, and its answer, which never says whose.
	for name, want := range map[string]bool{"Ada": true, "@ADA": true, "adam": false, "": false} {
		status, body := h.request(t, http.MethodGet, "/api/names/reserved?name="+name, "", nil)
		var got struct {
			Reserved bool `json:"reserved"`
		}
		if status != http.StatusOK || json.Unmarshal([]byte(body), &got) != nil || got.Reserved != want {
			t.Fatalf("/api/names/reserved?name=%q = %d %s, want reserved=%v", name, status, body, want)
		}
		if strings.Contains(body, m3710Ada.ID) || strings.Contains(body, "Lovelace") {
			t.Fatalf("/api/names/reserved says whose name it is: %s", body)
		}
	}

	// A server that does not require usernames reserves nothing.
	h.server.RequireUsername = false
	if h.server.nameReserved("ada") {
		t.Fatal("a server without usernames reserved a name")
	}
}

func TestM3711TheFileStoreKnowsWhoHoldsAHandle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.jsonl")
	db, err := NewFileChatDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateAccountPreferences("google:ada", func(p *AccountPreferences, _ bool) error {
		p.Profile.Handle = "ada"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	reopened, err := NewFileChatDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := reopened.HandleOwner("ada"); got != "google:ada" {
		t.Fatalf("HandleOwner(ada) after a restart = %q", got)
	}
	if got := reopened.HandleOwner("bob"); got != "" {
		t.Fatalf("HandleOwner(bob) = %q", got)
	}
}
