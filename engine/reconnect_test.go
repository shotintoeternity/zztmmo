package zztgo

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// reconnectServer builds a server whose tick is NOT running, so tests drive
// grace expiry deterministically with explicit server.Tick calls (M13.2).
func reconnectServer(t *testing.T) (*WebSocketServer, string, context.Context, context.CancelFunc) {
	t.Helper()
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	return server, wsURL, ctx, cancel
}

func dialJoin(t *testing.T, ctx context.Context, wsURL string, join JoinMessage) (*websocket.Conn, SnapshotMessage) {
	t.Helper()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
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

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func detachedCount(server *WebSocketServer, playerID PlayerID) (int, bool) {
	inst := server.DefaultInstance
	inst.mu.Lock()
	defer inst.mu.Unlock()
	ticks, ok := inst.Detached[playerID]
	return ticks, ok
}

// (a) A drop inside the grace window followed by a resume reclaims the same
// PlayerID and statID with inventory intact.
func TestReconnectResumeWithinGracePreservesRun(t *testing.T) {
	server, wsURL, ctx, _ := reconnectServer(t)
	inst := server.DefaultInstance

	conn1, snap1 := dialJoin(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "tester", Board: 1})
	playerID := snap1.You.ID
	statID := snap1.You.StatID
	token := snap1.ResumeToken
	if playerID == 0 || token == "" {
		t.Fatalf("join snapshot missing id/token: id=%d token=%q", playerID, token)
	}

	// Give the player something to lose. Set it directly: the tick is not running,
	// and the only concern is that detach/resume must not disturb the stat/state.
	inst.mu.Lock()
	state, ok := inst.RoomManager.PlayerState(playerID)
	if !ok {
		inst.mu.Unlock()
		t.Fatal("player state missing")
	}
	state.Keys[2] = true
	state.Gems = 7
	inst.mu.Unlock()

	// An abrupt drop, not a quit.
	conn1.Close(websocket.StatusAbnormalClosure, "wifi blip")
	waitFor(t, "detach", func() bool {
		_, ok := detachedCount(server, playerID)
		return ok
	})

	// The stat is still on the board during grace.
	inst.mu.Lock()
	if inst.RoomManager.players[playerID] == nil {
		inst.mu.Unlock()
		t.Fatal("detached player was removed during grace")
	}
	inst.mu.Unlock()

	conn2, snap2 := dialJoin(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "tester", Board: 1, ResumeToken: token})
	defer conn2.Close(websocket.StatusNormalClosure, "")

	if snap2.You.ID != playerID {
		t.Fatalf("resume got a new player: id=%d want %d", snap2.You.ID, playerID)
	}
	if snap2.You.StatID != statID {
		t.Fatalf("resume statID=%d want %d", snap2.You.StatID, statID)
	}
	if !snap2.HUD.Keys[2] || snap2.HUD.Gems != 7 {
		t.Fatalf("inventory lost across resume: keys=%v gems=%d", snap2.HUD.Keys, snap2.HUD.Gems)
	}
	// The resume cleared the detach entry.
	if _, ok := detachedCount(server, playerID); ok {
		t.Fatal("player still marked detached after resume")
	}
}

// (b) Grace expiry removes the stat, frees the square (the room freezes), and
// freezeRoomIfEmpty fires.
func TestReconnectGraceExpiryRemovesPlayer(t *testing.T) {
	server, wsURL, ctx, _ := reconnectServer(t)
	inst := server.DefaultInstance

	conn1, snap1 := dialJoin(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "tester", Board: 1})
	playerID := snap1.You.ID

	conn1.Close(websocket.StatusAbnormalClosure, "gone")
	waitFor(t, "detach", func() bool {
		_, ok := detachedCount(server, playerID)
		return ok
	})

	// One tick short of expiry: still present.
	for i := 0; i < ReconnectGraceTicks-1; i++ {
		server.Tick(ctx)
	}
	inst.mu.Lock()
	present := inst.RoomManager.players[playerID] != nil
	inst.mu.Unlock()
	if !present {
		t.Fatalf("player removed before grace elapsed")
	}

	// The tick that trips the grace boundary.
	server.Tick(ctx)
	inst.mu.Lock()
	_, stillDetached := inst.Detached[playerID]
	stillPlayer := inst.RoomManager.players[playerID] != nil
	rooms := inst.RoomManager.ActiveRoomCount()
	inst.mu.Unlock()

	if stillPlayer {
		t.Fatal("player survived grace expiry")
	}
	if stillDetached {
		t.Fatal("detach entry survived expiry")
	}
	if rooms != 0 {
		t.Fatalf("freezeRoomIfEmpty did not fire: %d active rooms remain", rooms)
	}
}

// (c) An unknown token falls through to a fresh join rather than erroring.
func TestReconnectUnknownTokenJoinsFresh(t *testing.T) {
	_, wsURL, ctx, _ := reconnectServer(t)

	conn, snap := dialJoin(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "tester", Board: 1, ResumeToken: "deadbeefdeadbeefdeadbeefdeadbeef"})
	defer conn.Close(websocket.StatusNormalClosure, "")

	if snap.You.ID == 0 {
		t.Fatal("fresh join produced no player")
	}
	if snap.ResumeToken == "" || snap.ResumeToken == "deadbeefdeadbeefdeadbeefdeadbeef" {
		t.Fatalf("fresh join did not mint a new token: %q", snap.ResumeToken)
	}
}

// (d) A second connection presenting a live player's token wins: it takes over
// the stat and the old socket is dropped (newest-wins).
func TestReconnectSecondConnectionWins(t *testing.T) {
	server, wsURL, ctx, _ := reconnectServer(t)
	inst := server.DefaultInstance

	conn1, snap1 := dialJoin(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "tester", Board: 1})
	defer conn1.Close(websocket.StatusNormalClosure, "")
	playerID := snap1.You.ID
	token := snap1.ResumeToken

	conn2, snap2 := dialJoin(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "tester", Board: 1, ResumeToken: token})
	defer conn2.Close(websocket.StatusNormalClosure, "")

	if snap2.You.ID != playerID {
		t.Fatalf("second connection got a new player: id=%d want %d", snap2.You.ID, playerID)
	}

	// The displaced socket is closed by the server: its next read fails.
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var discard SnapshotMessage
	if err := wsjson.Read(readCtx, conn1, &discard); err == nil {
		t.Fatal("displaced connection was not closed")
	}

	// The player is neither detached nor duplicated: exactly one live player.
	inst.mu.Lock()
	players := len(inst.RoomManager.players)
	_, detached := inst.Detached[playerID]
	inst.mu.Unlock()
	if players != 1 {
		t.Fatalf("newest-wins left %d players, want 1", players)
	}
	if detached {
		t.Fatal("winning resume left the player marked detached")
	}
}
