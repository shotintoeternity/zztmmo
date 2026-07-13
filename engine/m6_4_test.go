package zztgo

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

func TestM64SignedInPlayerStatePersistsAcrossServerRestart(t *testing.T) {
	world := testEmptyWorld(t)
	db := NewMemChatDatabase()
	secret := []byte("test-cookie-secret")
	account := AuthenticatedAccount{ID: "google:stateful", Email: "stateful@example.test", Name: "Stateful"}

	server1 := NewWebSocketServer(world, 1)
	server1.ChatDB = db
	server1.Auth = NewAuthService("client-id", "", "", secret)
	httpServer1 := httptestServer(t, server1)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cookie := signedAuthCookie(t, server1.Auth, account)
	conn1, snap1 := dialJoinWithCookie(t, ctx, "ws"+strings.TrimPrefix(httpServer1.URL, "http"), JoinMessage{Type: MessageTypeJoin, Name: "ignored", Board: 1}, cookie)

	server1.DefaultInstance.mu.Lock()
	state1, ok := server1.DefaultInstance.RoomManager.PlayerState(snap1.You.ID)
	if !ok {
		server1.DefaultInstance.mu.Unlock()
		t.Fatal("joined player has no state")
	}
	state1.Keys[0] = true
	state1.Ammo = 12
	state1.Gems = 3
	server1.DefaultInstance.mu.Unlock()

	_ = conn1.Close(websocket.StatusNormalClosure, "")
	waitForStoredState(t, db, account.ID, server1.DefaultInstance.Name)
	httpServer1.Close()

	server2 := NewWebSocketServer(world, 1)
	server2.ChatDB = db
	server2.Auth = NewAuthService("client-id", "", "", secret)
	httpServer2 := httptestServer(t, server2)
	defer httpServer2.Close()

	cookie = signedAuthCookie(t, server2.Auth, account)
	conn2, snap2 := dialJoinWithCookie(t, ctx, "ws"+strings.TrimPrefix(httpServer2.URL, "http"), JoinMessage{Type: MessageTypeJoin, Name: "ignored", Board: 1}, cookie)
	defer conn2.Close(websocket.StatusNormalClosure, "")
	if !snap2.HUD.Keys[0] || snap2.HUD.Ammo != 12 || snap2.HUD.Gems != 3 {
		t.Fatalf("restored HUD keys/ammo/gems=%v/%d/%d, want blue key/12/3", snap2.HUD.Keys, snap2.HUD.Ammo, snap2.HUD.Gems)
	}

	guestConn, guestSnap := dialJoinWithCookie(t, ctx, "ws"+strings.TrimPrefix(httpServer2.URL, "http"), JoinMessage{Type: MessageTypeJoin, Name: "guest", Board: 1}, nil)
	defer guestConn.Close(websocket.StatusNormalClosure, "")
	if guestSnap.HUD.Keys[0] || guestSnap.HUD.Ammo != 0 || guestSnap.HUD.Gems != 0 {
		t.Fatalf("guest HUD keys/ammo/gems=%v/%d/%d, want fresh", guestSnap.HUD.Keys, guestSnap.HUD.Ammo, guestSnap.HUD.Gems)
	}
}

func TestM64SaveSnapshotWritesAccountStateSidecar(t *testing.T) {
	rm := NewRoomManager(testEmptyWorld(t))
	playerID := rm.JoinPlayer(1, 0, 0)
	rm.SetPlayerIdentity(playerID, "google:saver", "Saver")
	state, ok := rm.PlayerState(playerID)
	if !ok {
		t.Fatal("joined player has no state")
	}
	state.Keys[1] = true
	state.Score = 42

	dir := t.TempDir()
	path, err := rm.SaveSnapshot(dir, "ACCT", playerID)
	if err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	sidecarPath := snapshotPlayerStateSidecarPath(path)
	data, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatalf("read sidecar %s: %v", filepath.Base(sidecarPath), err)
	}
	var sidecar snapshotPlayerStateSidecar
	if err := json.Unmarshal(data, &sidecar); err != nil {
		t.Fatalf("decode sidecar: %v", err)
	}
	if sidecar.AccountID != "google:saver" || !sidecar.State.Keys[1] || sidecar.State.Score != 42 {
		t.Fatalf("sidecar=%+v, want account google:saver green key score 42", sidecar)
	}
}

func httptestServer(t *testing.T, server *WebSocketServer) *httptest.Server {
	t.Helper()
	return httptest.NewServer(server)
}

func waitForStoredState(t *testing.T, db ChatDatabase, accountID, worldName string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state, ok, err := db.GetPlayerState(accountID, worldName)
		if err != nil {
			t.Fatalf("GetPlayerState: %v", err)
		}
		if ok && state.Keys[0] && state.Ammo == 12 && state.Gems == 3 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for persisted player state")
}
