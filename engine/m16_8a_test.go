package zztgo

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

// M16.8a, gap 1 — filed by M16.8's side-by-side harness (NOTES.md 2026-07-30):
// ElementDefs[E_PLAYER].Character (elements.go ElementPlayerTick's
// energizer-flash/steady-state toggle) was a package-level global, not
// Engine-scoped, despite gamevars.go documenting ElementDefs as "immutable
// after init" (M1.1) and M1.2's own DoD claiming interleaved Engines have "no
// cross-talk". Two Engines ticking a player in the same process could stomp
// each other's rendered glyph. Fixed by moving the toggle onto
// Engine.PlayerCharacter (gamevars.go) and reading it from
// TileToColorAndChar's E_PLAYER case (game.go) instead of the shared table.
//
// This interleaves two Engines — one with an energized player (glyph
// alternates '\x01'/'\x02' every tick), one without (glyph must stay the
// steady '\x02') — ticking back and forth, and asserts neither's rendered
// player glyph is affected by the other.
func TestM168aEnergizerBlinkIsPerEngineNotSharedGlobal(t *testing.T) {
	newSinglePlayerEngine := func() *Engine {
		e := NewEngine()
		e.Headless = true
		e.WorldCreate()
		e.BoardCreate()
		e.SetInputSource(&ScriptedInput{})
		return e
	}

	energized := newSinglePlayerEngine()
	energized.PlayerFor(0).EnergizerTicks = 20

	steady := newSinglePlayerEngine()
	// steady's player is never energized: EnergizerTicks stays 0 throughout.

	ex, ey := int16(energized.Board.Stats[0].X), int16(energized.Board.Stats[0].Y)
	sx, sy := int16(steady.Board.Stats[0].X), int16(steady.Board.Stats[0].Y)

	for i := 0; i < 10; i++ {
		step(energized, nil)
		_, ch := energized.TileToColorAndChar(ex, ey)
		want := byte('\x01')
		if i%2 != 0 {
			want = '\x02'
		}
		if ch != want {
			t.Fatalf("tick %d: energized engine's player glyph = %#x, want %#x", i, ch, want)
		}

		step(steady, nil)
		if _, ch := steady.TileToColorAndChar(sx, sy); ch != '\x02' {
			t.Fatalf("tick %d: steady engine's un-energized player glyph = %#x, want steady \\x02 — the energized engine's blink leaked across engines", i, ch)
		}
	}
}

// M16.8a, gap 2 — filed by M16.8 while inventorying the protocol surface
// (NOTES.md 2026-07-30): RoomManager.StepDiffs's TransferEvent case resolved
// the traveler and queued the board transfer itself, but — unlike every
// sibling case (SoundEvent, WalkClickEvent, ScrollEvent, the default branch)
// — never appended anything to pendingPlayerEvents. So the wire "transfer"
// ProtocolEvent (protocol.go's ProtocolEvents already converts a
// TransferEvent; main.ts already has a `case "transfer":` waiting for it) was
// dead code: never delivered to any client. Fixed by queuing the traveler's
// own TransferEvent alongside the sound it already queues.
//
// Two players start on testEdgeWorld's board 1 ("West"); only "mover" walks
// into the east edge. Asserts mover's arriving BoardChangeMessage carries a
// "transfer" event naming board 2, and "stayer" — who never crosses — sees no
// "transfer" event at all during the same window: traveler-only, not
// room-wide, the same shape M7.4 already established for per-player sound.
func TestM168aTransferEventReachesOnlyTheTraveler(t *testing.T) {
	world := testEdgeWorld(t)
	server := NewWebSocketServer(world, 1)
	server.TickDuration = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	dial := func(name string) (*websocket.Conn, SnapshotMessage) {
		t.Helper()
		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			t.Fatalf("dial %s: %v", name, err)
		}
		conn.SetReadLimit(ServerReadLimit)
		if err := wsjson.Write(ctx, conn, JoinMessage{Type: MessageTypeJoin, Name: name, Board: 1}); err != nil {
			t.Fatalf("write join %s: %v", name, err)
		}
		var snap SnapshotMessage
		if err := wsjson.Read(ctx, conn, &snap); err != nil {
			t.Fatalf("read snapshot %s: %v", name, err)
		}
		return conn, snap
	}

	moverConn, moverSnap := dial("mover")
	defer moverConn.Close(websocket.StatusNormalClosure, "")
	stayerConn, _ := dial("stayer")
	defer stayerConn.Close(websocket.StatusNormalClosure, "")

	if err := wsjson.Write(ctx, moverConn, InputMessage{
		Type: MessageTypeInput, PlayerID: moverSnap.You.ID, Seq: 1, Keymask: InputMaskRight,
	}); err != nil {
		t.Fatalf("write mover input: %v", err)
	}

	// readEvents reads at most one already-buffered message and returns its
	// ProtocolEvents regardless of message shape: a DiffMessage carries them
	// in its own top-level "events", a BoardChangeMessage nests them under
	// "snapshot.events" instead. A read timeout (nothing buffered this tick)
	// is not a failure — it just means try again next tick.
	readEvents := func(conn *websocket.Conn) []ProtocolEvent {
		readCtx, cancelRead := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancelRead()
		var raw json.RawMessage
		if err := wsjson.Read(readCtx, conn, &raw); err != nil {
			return nil
		}
		var envelope struct {
			Type   string          `json:"type"`
			Events []ProtocolEvent `json:"events"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil
		}
		if envelope.Type == MessageTypeBoardChange {
			var bc BoardChangeMessage
			if err := json.Unmarshal(raw, &bc); err != nil {
				return nil
			}
			return bc.Snapshot.Events
		}
		return envelope.Events
	}

	moverSawTransfer := false
	stayerSawTransfer := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !moverSawTransfer {
		time.Sleep(5 * time.Millisecond)
		server.Tick(ctx)

		for _, ev := range readEvents(moverConn) {
			if ev.Type == "transfer" && ev.ToBoard == 2 {
				moverSawTransfer = true
			}
		}
		for _, ev := range readEvents(stayerConn) {
			if ev.Type == "transfer" {
				stayerSawTransfer = true
			}
		}
	}
	if !moverSawTransfer {
		t.Fatal("mover never received a \"transfer\" event for the board-edge crossing")
	}
	if stayerSawTransfer {
		t.Fatal("stayer received a \"transfer\" event meant only for the mover — not traveler-scoped")
	}
}
