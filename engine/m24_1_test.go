package zztgo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func TestM241AccountProfileHandleUniquenessAndPersistence(t *testing.T) {
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
			ada := AccountPreferences{
				Color:           "#112233",
				BlockedAccounts: []string{"google:blocked"},
				Hints:           AccountHintPreferences{Chat: true},
				Profile: AccountProfilePreferences{
					Handle:      "Ada_1",
					DisplayName: "Ada",
					About:       []string{"I build rooms."},
				},
			}
			if err := db.PutAccountPreferences("google:ada", ada); err != nil {
				t.Fatalf("put ada profile: %v", err)
			}
			got, ok, err := db.GetAccountPreferences("google:ada")
			if err != nil || !ok {
				t.Fatalf("get ada = (_, %v, %v)", ok, err)
			}
			if got.Profile.Handle != "ada_1" || got.Color != ada.Color || got.BlockedAccounts[0] != "google:blocked" || !got.Hints.Chat {
				t.Fatalf("stored profile did not normalize while preserving sibling fields: %+v", got)
			}

			err = db.PutAccountPreferences("google:bo", AccountPreferences{Profile: AccountProfilePreferences{Handle: "ADA_1"}})
			if !errors.Is(err, ErrProfileHandleTaken) {
				t.Fatalf("conflicting handle error = %v, want ErrProfileHandleTaken", err)
			}
			if _, ok, _ := db.GetAccountPreferences("google:bo"); ok {
				t.Fatal("a failed conflicting claim wrote bo's preferences")
			}

			got.Profile.Handle = ""
			if err := db.PutAccountPreferences("google:ada", got); err != nil {
				t.Fatalf("clearing ada handle: %v", err)
			}
			if err := db.PutAccountPreferences("google:bo", AccountPreferences{Profile: AccountProfilePreferences{Handle: "ada_1"}}); err != nil {
				t.Fatalf("bo claiming released handle: %v", err)
			}
			bo, ok, err := db.GetAccountPreferences("google:bo")
			if err != nil || !ok || bo.Profile.Handle != "ada_1" {
				t.Fatalf("bo profile = %+v, ok %v, err %v; want released handle", bo, ok, err)
			}
		})
	}
}

func TestM241FileDBRebuildsHandleIndexOnRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.jsonl")
	db, err := NewFileChatDatabase(path)
	if err != nil {
		t.Fatalf("NewFileChatDatabase: %v", err)
	}
	if err := db.PutAccountPreferences("google:ada", AccountPreferences{Profile: AccountProfilePreferences{Handle: "ada"}}); err != nil {
		t.Fatalf("put ada: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	reopened, err := NewFileChatDatabase(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	if err := reopened.PutAccountPreferences("google:bo", AccountPreferences{Profile: AccountProfilePreferences{Handle: "ADA"}}); !errors.Is(err, ErrProfileHandleTaken) {
		t.Fatalf("reopened conflicting claim = %v, want ErrProfileHandleTaken", err)
	}
}

func TestM241PreferencesEndpointSavesProfileAndPreservesFields(t *testing.T) {
	db := NewMemChatDatabase()
	secret := []byte("test-cookie-secret")
	account := AuthenticatedAccount{ID: "google:ada", Email: "ada@example.test", Name: "Ada"}
	bo := AuthenticatedAccount{ID: "google:bo", Email: "bo@example.test", Name: "Bo"}

	server, _ := m193Server(t, "ALPHA", db, secret)
	api := &WebAPI{RoomManager: server.RoomManager, Server: server, Auth: server.Auth}
	httpServer := httptest.NewServer(api.Handler())
	defer httpServer.Close()
	cookie := signedAuthCookie(t, server.Auth, account)

	if code, _ := m193PutPreferences(t, httpServer.URL, nil, `{"profile":{"handle":"ada","displayName":"Ada"}}`); code != http.StatusUnauthorized {
		t.Fatalf("guest profile PUT = %d, want 401", code)
	}
	if code, body := m193PutPreferences(t, httpServer.URL, cookie, `{"profile":{"handle":"Ada","displayName":"Ada","about":["I build rooms."]}}`); code != http.StatusOK || body.Profile.Handle != "ada" || body.Profile.DisplayName != "Ada" {
		t.Fatalf("profile PUT = %d %+v", code, body)
	}
	if code, body := m193PutPreferences(t, httpServer.URL, cookie, `{"color":"#445566","hints":{"players":true}}`); code != http.StatusOK || body.Profile.Handle != "ada" || body.Color != "#445566" || !body.Hints.Players {
		t.Fatalf("color/hints PUT did not preserve profile: %d %+v", code, body)
	}
	if code, _ := m193PutPreferences(t, httpServer.URL, signedAuthCookie(t, server.Auth, bo), `{"profile":{"handle":"ADA"}}`); code != http.StatusConflict {
		t.Fatalf("conflicting profile PUT = %d, want 409", code)
	}
	if code, _ := m193PutPreferences(t, httpServer.URL, cookie, `{"profile":{"handle":"no spaces"}}`); code != http.StatusBadRequest {
		t.Fatalf("invalid handle PUT = %d, want 400", code)
	}
}

func TestM241ProfileRequestUsesPlayerIDAndDoesNotLeakAccountID(t *testing.T) {
	db := NewMemChatDatabase()
	secret := []byte("test-cookie-secret")
	ada := AuthenticatedAccount{ID: "google:ada", Email: "ada@example.test", Name: "Ada"}
	if err := db.PutAccountPreferences(ada.ID, AccountPreferences{Profile: AccountProfilePreferences{
		Handle:      "ada",
		DisplayName: "Ada",
		About:       []string{"I build rooms."},
	}}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	server, url := m193Server(t, "ALPHA", db, secret)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	adaConn, adaSnap := dialJoinWithCookie(t, ctx, url, JoinMessage{Type: MessageTypeJoin, Name: "ignored", Board: 1}, signedAuthCookie(t, server.Auth, ada))
	defer adaConn.Close(websocket.StatusNormalClosure, "")
	boConn, boSnap := dialJoinWithCookie(t, ctx, url, JoinMessage{Type: MessageTypeJoin, Name: "Bo", Board: 1}, nil)
	defer boConn.Close(websocket.StatusNormalClosure, "")

	foundAda := false
	for _, player := range boSnap.Players {
		if player.ID == adaSnap.You.ID {
			foundAda = true
			if player.Handle != "ada" || !player.HasProfile {
				t.Fatalf("Ada roster summary = %+v, want @ada with profile", player)
			}
		}
	}
	if !foundAda {
		t.Fatalf("Bo's roster did not include Ada: %+v", boSnap.Players)
	}

	if err := wsjson.Write(ctx, boConn, ProfileRequestMessage{Type: MessageTypeProfileRequest, PlayerID: adaSnap.You.ID}); err != nil {
		t.Fatalf("write profile request: %v", err)
	}
	result := readProfileResult(t, ctx, boConn)
	if result.Handle != "ada" || !strings.Contains(strings.Join(result.Lines, "\n"), "I build rooms.") {
		t.Fatalf("profile result = %+v", result)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if strings.Contains(string(data), ada.ID) {
		t.Fatalf("profile result leaked account id: %s", data)
	}
}

func readProfileResult(t *testing.T, ctx context.Context, conn *websocket.Conn) ProfileResultMessage {
	t.Helper()
	for i := 0; i < 10; i++ {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, conn, &raw); err != nil {
			t.Fatalf("read profile result: %v", err)
		}
		var env struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("decode envelope: %v", err)
		}
		if env.Type != MessageTypeProfileResult {
			continue
		}
		var result ProfileResultMessage
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatalf("decode profile result: %v", err)
		}
		return result
	}
	t.Fatal("no profile result received")
	return ProfileResultMessage{}
}
