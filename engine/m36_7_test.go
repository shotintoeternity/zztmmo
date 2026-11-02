package zztgo

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// Task M36.7 — a replay spectator cannot deadlock every world.
//
// serveReplaySpectator used to keep any non-zero join.Board, take replay.mu
// without a defer, and open that board directly. Board -1 indexed
// BoardData[-1] and panicked; net/http recovered the goroutine but replay.mu
// stayed held, so the next WebSocketServer.Tick blocked in replay.Tick and no
// hosted world ticked again. Anonymous, one message.

func m367ReplayServer(t *testing.T) (*WebSocketServer, string, context.Context, string) {
	t.Helper()
	id := "TOWN-20261102-120000"
	data, _, _ := m223Recording(t, id)
	root := t.TempDir()
	replayDir := filepath.Join(root, "replays")
	worldsDir := filepath.Join(root, "worlds")
	for _, dir := range []string{replayDir, worldsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(replayDir, id+".jsonl"), data, 0o644); err != nil {
		t.Fatalf("write recording: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worldsDir, "TOWN.ZZT"), committedTownBytes(t), 0o644); err != nil {
		t.Fatalf("write TOWN.ZZT: %v", err)
	}
	server, wsURL, ctx := m223Server(t, replayDir, worldsDir)
	return server, wsURL, ctx, id
}

// m367TickWithin runs one server tick and fails if it does not return: a held
// replay.mu shows up as a Tick that never comes back.
func m367TickWithin(t *testing.T, ctx context.Context, server *WebSocketServer, label string) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		server.Tick(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s: server.Tick did not return; a replay lock is held", label)
	}
}

func TestM367ReplaySpectatorOffTheBoardTableDoesNotStopTheServer(t *testing.T) {
	for _, board := range []int16{-1, -32768, 32767} {
		server, wsURL, ctx, id := m367ReplayServer(t)
		count := server.DefaultInstance.RoomManager.FrozenWorld().BoardCount

		watcher, _, err := websocket.Dial(ctx, wsURL+"?replay="+id, nil)
		if err != nil {
			t.Fatalf("board %d: dial replay watcher: %v", board, err)
		}
		watcher.SetReadLimit(ServerReadLimit)
		if err := wsjson.Write(ctx, watcher, JoinMessage{Type: MessageTypeJoin, Spectate: true, Board: board}); err != nil {
			t.Fatalf("board %d: write replay watch join: %v", board, err)
		}
		// Errorf, not Fatalf: before the fix the read fails here, and the tick
		// below is what shows the damage that does to every other world.
		readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		var snap SnapshotMessage
		err = wsjson.Read(readCtx, watcher, &snap)
		cancel()
		switch {
		case err != nil:
			t.Errorf("board %d: replay watcher got no snapshot: %v", board, err)
		case snap.Type != MessageTypeSnapshot || !snap.Spectator:
			t.Errorf("board %d: replay watcher got %q, want a spectator snapshot", board, snap.Type)
		case snap.BoardID < 0 || snap.BoardID > count:
			t.Errorf("board %d: replay watcher was put on board %d, outside 0..%d", board, snap.BoardID, count)
		}

		// The DoD: the server still ticks a hosted world afterwards.
		player, playerSnap := dialJoin(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "player", Board: 1})
		startTick := playerSnap.Tick
		m367TickWithin(t, ctx, server, "first tick")
		m367TickWithin(t, ctx, server, "second tick")
		advanced := false
		for i := 0; i < 2 && !advanced; i++ {
			typ, s, d := m221ReadFrame(t, ctx, player)
			switch typ {
			case MessageTypeSnapshot:
				advanced = s.Tick > startTick
			case MessageTypeDiff:
				advanced = d.Tick > startTick
			}
		}
		if !advanced {
			t.Fatalf("board %d: hosted world did not tick past %d", board, startTick)
		}
		player.Close(websocket.StatusNormalClosure, "")
		watcher.Close(websocket.StatusNormalClosure, "")
	}
}

// BoardOpen's own clamp handled only an id off the top of the table.
func TestM367BoardOpenRejectsANegativeBoard(t *testing.T) {
	e := NewEngine()
	e.World = townWorld(t)
	e.World.Info.CurrentBoard = 1
	e.BoardOpen(-1)
	want := NewEngine()
	want.World = townWorld(t)
	want.BoardOpen(1)
	if e.Board.Name != want.Board.Name {
		t.Fatalf("BoardOpen(-1) opened %q, want the current board %q", e.Board.Name, want.Board.Name)
	}
}
