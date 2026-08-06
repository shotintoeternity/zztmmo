package zztgo

// M22.1 — a read-only client: the browser can render a room it is not in.
//
// The recorder shipped and the viewer did not, and the reason the viewer could
// not be built is that there was no way for a connection to receive a room
// without joining it. This task is that way: a `spectate: true` join.
//
// The claims these tests exist to keep, in the order the DoD makes them:
//
//  1. a watcher and a player looking at the same room see the same board — one
//     step, one dirty-cell drain, two audiences;
//  2. a watcher's arrival and departure change no state hash on any tick, which
//     is the whole reason a watcher may exist in a fork whose determinism is
//     sacred;
//  3. a watcher's input is DISCARDED, proved by the messages arriving and being
//     counted rather than by the room happening not to move;
//  4. the watcher count reaches the players in the room and the other watchers;
//  5. a watcher is invisible everywhere a player is visible — the roster, the
//     picker's occupancy, the resume table — by construction.
//
// The inversions worth naming, because each of them passes every other
// assertion here: a watcher rendered from a SECOND pass over the room (claim 1
// still holds tick by tick and diverges the moment a cell is drawn between
// ticks); a watcher spawned as a stat with its input zeroed (claim 3 holds,
// claim 2 does not); and a watcher whose input is never read off the wire at all
// (claim 3 holds for the wrong reason, and the first oversized message wedges
// the connection instead).

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// m221Server is reconnectServer's shape — no tick goroutine, so every tick in
// these tests is one this test asked for — over the real multi-board TOWN rather
// than an empty board, because a blank room renders identically however it is
// rendered and would prove claim 1 by accident.
func m221Server(t *testing.T) (*WebSocketServer, string, context.Context) {
	t.Helper()
	rm := townRoomManager(t)
	server := NewWebSocketServer(rm.FrozenWorld(), 1)
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return server, "ws" + strings.TrimPrefix(httpServer.URL, "http"), ctx
}

// m221Watch dials a spectating connection and returns it with its first frame.
func m221Watch(t *testing.T, ctx context.Context, wsURL string, board int16) (*websocket.Conn, SnapshotMessage) {
	t.Helper()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial watcher: %v", err)
	}
	conn.SetReadLimit(ServerReadLimit)
	if err := wsjson.Write(ctx, conn, JoinMessage{Type: MessageTypeJoin, Spectate: true, Board: board}); err != nil {
		t.Fatalf("write watch join: %v", err)
	}
	var snapshot SnapshotMessage
	if err := wsjson.Read(ctx, conn, &snapshot); err != nil {
		t.Fatalf("read watch snapshot: %v", err)
	}
	if !snapshot.Spectator {
		t.Fatalf("a watcher's first frame is not marked as one: %+v", snapshot.Type)
	}
	return conn, snapshot
}

// m221Step walks the player one square and ticks, waiting for the input to be
// registered first so the run is a function of the script rather than of when a
// goroutine happened to be scheduled. TOWN's entry board simulates nothing on
// its own, so without a player who moves, "the hashes matched" would be a
// statement about a room that never changed.
func m221Step(t *testing.T, ctx context.Context, server *WebSocketServer, inst *WorldInstance, conn *websocket.Conn, dx int16, mask uint16) {
	t.Helper()
	if err := wsjson.Write(ctx, conn, InputMessage{Type: MessageTypeInput, DeltaX: dx, Keymask: mask}); err != nil {
		t.Fatalf("player input: %v", err)
	}
	waitFor(t, "the player's input to reach the instance", func() bool {
		inst.mu.Lock()
		defer inst.mu.Unlock()
		return len(inst.Inputs) == 1
	})
	server.Tick(ctx)
}

// m221Screen is a client's mirror of the board: exactly what main.ts keeps, so
// that "they render the same board" is asserted against the thing a browser
// would have drawn rather than against the messages that produced it.
type m221Screen map[int32]ScreenCell

func newM221Screen(cells []ScreenCell) m221Screen {
	screen := make(m221Screen, len(cells))
	screen.apply(cells)
	return screen
}

func (s m221Screen) apply(cells []ScreenCell) {
	for _, cell := range cells {
		s[int32(cell.X)*25+int32(cell.Y)] = cell
	}
}

func (s m221Screen) differs(other m221Screen) (int32, bool) {
	for key, cell := range s {
		if other[key] != cell {
			return key, true
		}
	}
	for key, cell := range other {
		if s[key] != cell {
			return key, true
		}
	}
	return 0, false
}

// m221ReadFrame reads one message and returns it decoded as both shapes, since
// a watcher is handed a diff most ticks and a whole snapshot when the room it is
// watching is created or replaced.
func m221ReadFrame(t *testing.T, ctx context.Context, conn *websocket.Conn) (string, SnapshotMessage, DiffMessage) {
	t.Helper()
	readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var raw json.RawMessage
	if err := wsjson.Read(readCtx, conn, &raw); err != nil {
		t.Fatalf("read frame: %v", err)
	}
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode frame envelope: %v", err)
	}
	var snapshot SnapshotMessage
	var diff DiffMessage
	switch envelope.Type {
	case MessageTypeSnapshot:
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			t.Fatalf("decode snapshot: %v", err)
		}
	case MessageTypeDiff:
		if err := json.Unmarshal(raw, &diff); err != nil {
			t.Fatalf("decode diff: %v", err)
		}
	}
	return envelope.Type, snapshot, diff
}

// (1) A watcher and a player looking at the same room render the same board.
//
// The comparison is made over TWENTY ticks and cell by cell, not once at the
// join: a watcher that re-rendered the room from its own pass would agree on
// every tick boundary and disagree only about the cells drawn BETWEEN ticks,
// which is exactly the class of bug M16.12a spent a task on from the other side.
func TestM221WatcherAndPlayerRenderTheSameBoard(t *testing.T) {
	server, wsURL, ctx := m221Server(t)
	inst := server.DefaultInstance

	playerConn, playerSnap := dialJoin(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "player", Board: 1})
	defer playerConn.Close(websocket.StatusNormalClosure, "")

	watcherConn, watchSnap := m221Watch(t, ctx, wsURL, 1)
	defer watcherConn.Close(websocket.StatusNormalClosure, "")

	if watchSnap.BoardID != playerSnap.BoardID {
		t.Fatalf("watcher opened board %d, player is on %d", watchSnap.BoardID, playerSnap.BoardID)
	}

	playerScreen := newM221Screen(playerSnap.Screen)
	watcherScreen := newM221Screen(watchSnap.Screen)
	if key, bad := playerScreen.differs(watcherScreen); bad {
		t.Fatalf("the joining frames already disagree at cell key %d", key)
	}

	for tick := 0; tick < 20; tick++ {
		// Right for ten ticks, then left, so the walk paints and repaints cells
		// rather than settling against a wall and leaving the two mirrors equal
		// because nothing is being drawn at all.
		if tick < 10 {
			m221Step(t, ctx, server, inst, playerConn, 1, InputMaskRight)
		} else {
			m221Step(t, ctx, server, inst, playerConn, -1, InputMaskLeft)
		}

		playerType, _, playerDiff := m221ReadFrame(t, ctx, playerConn)
		if playerType != MessageTypeDiff {
			t.Fatalf("tick %d: player got %q, want a diff", tick, playerType)
		}
		playerScreen.apply(playerDiff.Cells)

		watcherType, watcherFull, watcherDiff := m221ReadFrame(t, ctx, watcherConn)
		switch watcherType {
		case MessageTypeDiff:
			watcherScreen.apply(watcherDiff.Cells)
			if watcherDiff.Hash != playerDiff.Hash {
				t.Fatalf("tick %d: watcher hash %d, player hash %d", tick, watcherDiff.Hash, playerDiff.Hash)
			}
			if watcherDiff.HUD != nil {
				t.Fatalf("tick %d: a watcher was sent a HUD, which is one player's inventory", tick)
			}
			if len(watcherDiff.Events) != 0 {
				t.Fatalf("tick %d: a watcher was sent events it cannot answer: %+v", tick, watcherDiff.Events)
			}
		case MessageTypeSnapshot:
			watcherScreen = newM221Screen(watcherFull.Screen)
		default:
			t.Fatalf("tick %d: watcher got %q", tick, watcherType)
		}

		if key, bad := playerScreen.differs(watcherScreen); bad {
			t.Fatalf("tick %d: the two screens disagree at cell key %d", tick, key)
		}
	}

	// And the roster the watcher is drawn over is the room's, not a copy of it
	// missing the people in it.
	if inst.SpectatorCount() != 1 {
		t.Fatalf("instance holds %d watchers, want 1", inst.SpectatorCount())
	}
}

// m221HashRun plays one scripted run and returns the state hash of every tick,
// optionally letting a watcher come and go partway through. The two runs differ
// in nothing else — same world bytes, same fresh server, same single player, no
// wall-clock anywhere — so any difference in the returned hashes is the
// watcher's doing.
func m221HashRun(t *testing.T, ticks int, withWatcher bool) []uint64 {
	t.Helper()
	server, wsURL, ctx := m221Server(t)
	inst := server.DefaultInstance

	playerConn, _ := dialJoin(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "player", Board: 1})
	defer playerConn.Close(websocket.StatusNormalClosure, "")

	var watcherConn *websocket.Conn
	hashes := make([]uint64, 0, ticks)
	for tick := 0; tick < ticks; tick++ {
		if withWatcher && tick == 3 {
			conn, _ := m221Watch(t, ctx, wsURL, 1)
			watcherConn = conn
			// The watcher also SENDS, so this run covers the arrival, the traffic
			// and the departure in one.
			if err := wsjson.Write(ctx, conn, InputMessage{Type: MessageTypeInput, DeltaX: 1, Keymask: InputMaskRight}); err != nil {
				t.Fatalf("watcher input: %v", err)
			}
			waitFor(t, "the watcher's message to be dropped", func() bool { return inst.SpectatorDrops() > 0 })
		}
		if withWatcher && tick == 9 {
			watcherConn.Close(websocket.StatusNormalClosure, "")
			waitFor(t, "the watcher to be forgotten", func() bool { return inst.SpectatorCount() == 0 })
		}

		if tick < 7 {
			m221Step(t, ctx, server, inst, playerConn, 1, InputMaskRight)
		} else {
			m221Step(t, ctx, server, inst, playerConn, -1, InputMaskLeft)
		}
		frameType, _, diff := m221ReadFrame(t, ctx, playerConn)
		if frameType != MessageTypeDiff {
			t.Fatalf("tick %d: player got %q, want a diff", tick, frameType)
		}
		hashes = append(hashes, diff.Hash)
		if withWatcher && tick > 3 && tick < 9 {
			// While it is here it must be visible as a count and as nothing else.
			if diff.Watchers != 1 {
				t.Fatalf("tick %d: player was told %d watchers, want 1", tick, diff.Watchers)
			}
		}
		for _, player := range diff.Players {
			if player.StatID < 0 {
				t.Fatalf("tick %d: a watcher reached the roster: %+v", tick, player)
			}
		}
		if len(diff.Players) != 1 {
			t.Fatalf("tick %d: roster has %d entries, want the one player", tick, len(diff.Players))
		}
	}
	return hashes
}

// (2) and (3) together, because they are one claim seen from two sides: a
// watcher that arrives, sends input for six ticks and leaves must leave the
// room's tick-by-tick history byte-identical to a run in which nobody watched.
func TestM221WatchingChangesNoStateHash(t *testing.T) {
	const ticks = 14
	quiet := m221HashRun(t, ticks, false)
	watched := m221HashRun(t, ticks, true)

	if len(quiet) != len(watched) {
		t.Fatalf("different tick counts: %d and %d", len(quiet), len(watched))
	}
	for i := range quiet {
		if quiet[i] != watched[i] {
			t.Fatalf("tick %d diverged: unwatched %d, watched %d", i, quiet[i], watched[i])
		}
	}
	// A run whose hashes never move would pass the loop above without proving
	// anything, so the fixture has to be a room that actually simulates.
	moved := false
	for i := 1; i < len(quiet); i++ {
		if quiet[i] != quiet[0] {
			moved = true
			break
		}
	}
	if !moved {
		t.Fatal("the room never changed state, so equal hashes prove nothing")
	}
}

// (3) again, on its own terms: the messages ARRIVE and are discarded. A test
// that only checked the room did not move would pass just as well against a
// build that never read the socket.
func TestM221WatcherInputIsDroppedOnTheFloor(t *testing.T) {
	server, wsURL, ctx := m221Server(t)
	inst := server.DefaultInstance

	playerConn, _ := dialJoin(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "player", Board: 1})
	defer playerConn.Close(websocket.StatusNormalClosure, "")
	watcherConn, _ := m221Watch(t, ctx, wsURL, 1)
	defer watcherConn.Close(websocket.StatusNormalClosure, "")

	// Everything a client can say, including the messages that are not input at
	// all: a chat line (no PlayerID to attribute it to, so no seat at the table),
	// an operator action (nothing to act as), and a save (a watcher must not be
	// able to write a file).
	sent := []interface{}{
		InputMessage{Type: MessageTypeInput, DeltaX: -1, Keymask: InputMaskLeft},
		InputMessage{Type: MessageTypeInput, Keymask: InputMaskShoot, Shift: true},
		map[string]string{"type": "chat", "text": "let me in"},
		ModerateMessage{Type: MessageTypeModerate, Action: ModerationActionKick, PlayerID: 1},
		SaveFilenameMessage{Type: MessageTypeSaveFilename, Name: "WATCHER"},
	}
	for _, message := range sent {
		if err := wsjson.Write(ctx, watcherConn, message); err != nil {
			t.Fatalf("watcher write: %v", err)
		}
	}
	waitFor(t, "every watcher message to be dropped", func() bool { return inst.SpectatorDrops() >= len(sent) })

	inst.mu.Lock()
	pendingInputs := len(inst.Inputs)
	clients := len(inst.Clients)
	players := len(inst.RoomManager.players)
	inst.mu.Unlock()
	if pendingInputs != 0 {
		t.Fatalf("a watcher queued %d inputs for the next tick", pendingInputs)
	}
	if clients != 1 || players != 1 {
		t.Fatalf("the watcher reached the room: %d clients, %d players", clients, players)
	}

	// And the chat line reached nobody: the player in the room hears nothing but
	// its own diffs.
	server.Tick(ctx)
	frameType, _, _ := m221ReadFrame(t, ctx, playerConn)
	if frameType != MessageTypeDiff {
		t.Fatalf("player got %q after the watcher spoke, want a diff", frameType)
	}
}

// (4) The count reaches the players in the room and the other watchers, and it
// goes back down.
func TestM221WatcherCountReachesTheRoom(t *testing.T) {
	server, wsURL, ctx := m221Server(t)

	playerConn, _ := dialJoin(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "player", Board: 1})
	defer playerConn.Close(websocket.StatusNormalClosure, "")

	first, firstSnap := m221Watch(t, ctx, wsURL, 1)
	defer first.Close(websocket.StatusNormalClosure, "")
	if firstSnap.Watchers != 1 {
		t.Fatalf("the first watcher was told %d watchers, want itself", firstSnap.Watchers)
	}
	second, secondSnap := m221Watch(t, ctx, wsURL, 1)
	if secondSnap.Watchers != 2 {
		t.Fatalf("the second watcher was told %d watchers, want 2", secondSnap.Watchers)
	}

	server.Tick(ctx)
	if _, _, diff := m221ReadFrame(t, ctx, playerConn); diff.Watchers != 2 {
		t.Fatalf("the player was told %d watchers, want 2", diff.Watchers)
	}
	if _, _, diff := m221ReadFrame(t, ctx, first); diff.Watchers != 2 {
		t.Fatalf("the first watcher was told %d watchers, want 2", diff.Watchers)
	}
	if _, _, diff := m221ReadFrame(t, ctx, second); diff.Watchers != 2 {
		t.Fatalf("the second watcher was told %d watchers, want 2", diff.Watchers)
	}

	second.Close(websocket.StatusNormalClosure, "")
	waitFor(t, "the second watcher to be forgotten", func() bool {
		return server.DefaultInstance.SpectatorCount() == 1
	})
	server.Tick(ctx)
	if _, _, diff := m221ReadFrame(t, ctx, playerConn); diff.Watchers != 1 {
		t.Fatalf("the player was told %d watchers after one left, want 1", diff.Watchers)
	}
	if _, _, diff := m221ReadFrame(t, ctx, first); diff.Watchers != 1 {
		t.Fatalf("the remaining watcher was told %d watchers, want 1", diff.Watchers)
	}
}

// (5) A watcher is invisible everywhere a player is visible. Each of these is a
// place that walks the world's PLAYERS, which is why a watcher registered
// anywhere but its own table would show up in all of them at once.
func TestM221WatcherIsInvisibleToEveryPlayerCount(t *testing.T) {
	server, wsURL, ctx := m221Server(t)
	inst := server.DefaultInstance

	watcher, snapshot := m221Watch(t, ctx, wsURL, 1)
	defer watcher.Close(websocket.StatusNormalClosure, "")

	if snapshot.ResumeToken != "" {
		t.Fatalf("a watcher was minted a resume token: %q", snapshot.ResumeToken)
	}
	if snapshot.You.ID != 0 || snapshot.You.StatID != -1 {
		t.Fatalf("a watcher was handed a player: %+v", snapshot.You)
	}
	if len(snapshot.Screen) == 0 {
		t.Fatal("a watcher of an idle world got no board to render")
	}

	// The picker's occupancy, and the question every overwrite path asks before
	// it writes: watching is not playing, so a watched-but-unplayed world is
	// still free to be republished over. That is a consequence worth stating
	// rather than an oversight — a watcher holds no claim on a world.
	if server.WorldIsOccupied(inst.Name) {
		t.Fatal("a watcher made the world read as occupied")
	}

	inst.mu.Lock()
	clients := len(inst.Clients)
	tokens := len(inst.ResumeTokens)
	rooms := inst.RoomManager.ActiveRoomCount()
	inst.mu.Unlock()
	if clients != 0 || tokens != 0 {
		t.Fatalf("a watcher reached the client tables: %d clients, %d tokens", clients, tokens)
	}
	// The load-bearing one: watching does not START a room. An idle board is
	// rendered from the frozen world, so a watcher cannot make a world simulate
	// — which is what keeps claim 2 true for boards nobody is playing at all.
	if rooms != 0 {
		t.Fatalf("watching created %d live rooms", rooms)
	}
}

// An idle board is watchable, and it comes to life under the watcher: the first
// player to arrive builds a room with a new engine, and an incremental diff
// stream has no past to build on across that boundary, so the watcher is re-sent
// a whole screen.
func TestM221AnIdleBoardIsWatchableAndWakesUp(t *testing.T) {
	server, wsURL, ctx := m221Server(t)

	watcher, idle := m221Watch(t, ctx, wsURL, 1)
	defer watcher.Close(websocket.StatusNormalClosure, "")
	if idle.Tick != 0 || idle.Hash != 0 {
		t.Fatalf("an idle frame claimed to be a tick: tick=%d hash=%d", idle.Tick, idle.Hash)
	}
	idleScreen := newM221Screen(idle.Screen)

	// Nothing is running, so a tick sends the watcher nothing at all.
	server.Tick(ctx)

	playerConn, playerJoin := dialJoin(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "player", Board: 1})
	defer playerConn.Close(websocket.StatusNormalClosure, "")

	server.Tick(ctx)
	frameType, full, _ := m221ReadFrame(t, ctx, watcher)
	if frameType != MessageTypeSnapshot {
		t.Fatalf("the board woke up and the watcher got %q, want a whole screen", frameType)
	}
	if !full.Spectator || full.Watchers != 1 {
		t.Fatalf("the wake-up frame is malformed: spectator=%v watchers=%d", full.Spectator, full.Watchers)
	}
	if len(full.Players) != 1 {
		t.Fatalf("the wake-up frame has %d players, want the one who arrived", len(full.Players))
	}

	if _, _, diff := m221ReadFrame(t, ctx, playerConn); diff.BoardID != 1 {
		t.Fatalf("player is on board %d, want 1", diff.BoardID)
	}

	// The wake-up frame is a WHOLE SCREEN rather than a diff for a load-bearing
	// reason: the room drained its opening dirty cells into the arriving player's
	// own join snapshot (RoomManager.Snapshot, when they are alone), so a watcher
	// handed only diffs across that boundary would render the frozen picture
	// forever. Prove it by moving — the watcher's mirror must track the player's
	// from the frame it was just given, and must end up somewhere the idle
	// picture never was.
	playerScreen := newM221Screen(playerJoin.Screen)
	watcherScreen := newM221Screen(full.Screen)
	for tick := 0; tick < 6; tick++ {
		m221Step(t, ctx, server, server.DefaultInstance, playerConn, 1, InputMaskRight)
		_, _, playerDiff := m221ReadFrame(t, ctx, playerConn)
		playerScreen.apply(playerDiff.Cells)
		_, _, watcherDiff := m221ReadFrame(t, ctx, watcher)
		watcherScreen.apply(watcherDiff.Cells)
		if key, bad := playerScreen.differs(watcherScreen); bad {
			t.Fatalf("tick %d after wake-up: the screens disagree at cell key %d", tick, key)
		}
	}
	if _, bad := idleScreen.differs(watcherScreen); !bad {
		t.Fatal("the watched board never changed after the player arrived and walked")
	}
}
