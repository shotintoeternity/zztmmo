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

// M4.2 — full keyboard/control parity.
//
// These are PROTOCOL-level tests: every one drives the engine through an
// InputMessage, the exact struct the browser puts on the wire, converted by
// inputMessageToPlayerInput. A test that called ElementPlayerTick directly
// would pass even if the wire format dropped the key on the floor — which is
// precisely the bug class M4.2 exists to close ('S', 'P', 'B' and 'T' were all
// implemented in the engine and unreachable from a browser).
//
// One test per row of the M4.2 table in TASKS.md.

// keymaskMsg is what the client sends for movement/shooting: a held-key mask.
func keymaskMsg(mask uint16) InputMessage {
	return InputMessage{Type: MessageTypeInput, Keymask: mask}
}

// commandMsg is what the client's sendKey() sends for a play-mode command key:
// a bare key byte, no mask.
func commandMsg(key byte) InputMessage {
	return InputMessage{Type: MessageTypeInput, Key: key}
}

// stepMsg pushes one client message through the protocol boundary into the sim.
func stepMsg(e *Engine, statId int16, msg InputMessage) []Event {
	return step(e, map[int16]PlayerInput{statId: inputMessageToPlayerInput(msg)})
}

// findBullet locates the bullet fired by statId. A bullet is a stat appended to
// the end of the list, so it takes its OWN tick within the same cycle that
// created it and has already left the muzzle square by the time step() returns
// — do not look for it adjacent to the player. P1 carries the owner (M2.4).
func findBullet(e *Engine, ownerStatId int16) (TStat, bool) {
	for i := int16(0); i <= e.Board.StatCount; i++ {
		stat := e.Board.Stats[i]
		if e.Board.Tiles[stat.X][stat.Y].Element != E_BULLET {
			continue
		}
		if stat.P1 == byte(ownerStatId+SHOT_SOURCE_PLAYER_BASE) {
			return stat, true
		}
	}
	return TStat{}, false
}

// go.mod pins go1.13, so these are one-liners rather than a generic helper.
func findPauseEvent(events []Event) (PauseEvent, bool) {
	for _, ev := range events {
		if typed, ok := ev.(PauseEvent); ok {
			return typed, true
		}
	}
	return PauseEvent{}, false
}

func findSavePromptEvent(events []Event) (SavePromptEvent, bool) {
	for _, ev := range events {
		if typed, ok := ev.(SavePromptEvent); ok {
			return typed, true
		}
	}
	return SavePromptEvent{}, false
}

func findHelpEvent(events []Event) (HelpEvent, bool) {
	for _, ev := range events {
		if typed, ok := ev.(HelpEvent); ok {
			return typed, true
		}
	}
	return HelpEvent{}, false
}

func findDebugPromptEvent(events []Event) (DebugPromptEvent, bool) {
	for _, ev := range events {
		if typed, ok := ev.(DebugPromptEvent); ok {
			return typed, true
		}
	}
	return DebugPromptEvent{}, false
}

func hasQuitPromptEvent(events []Event) bool {
	for _, ev := range events {
		if _, ok := ev.(QuitPromptEvent); ok {
			return true
		}
	}
	return false
}

// --- Row: arrows / numpad -> move (keymask) ---------------------------------

func TestM42MovementKeymask(t *testing.T) {
	cases := []struct {
		name   string
		mask   uint16
		dx, dy int16
	}{
		{"up", InputMaskUp, 0, -1},
		{"down", InputMaskDown, 0, 1},
		{"left", InputMaskLeft, -1, 0},
		{"right", InputMaskRight, 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, p1, _ := twoPlayerBoard(t)
			x0, y0 := int16(e.Board.Stats[p1].X), int16(e.Board.Stats[p1].Y)

			stepMsg(e, p1, keymaskMsg(tc.mask))

			gotX, gotY := int16(e.Board.Stats[p1].X), int16(e.Board.Stats[p1].Y)
			if gotX != x0+tc.dx || gotY != y0+tc.dy {
				t.Errorf("moved to (%d,%d), want (%d,%d)", gotX, gotY, x0+tc.dx, y0+tc.dy)
			}
		})
	}
}

// --- Row: Shift+dir -> shoot ------------------------------------------------

func TestM42ShiftDirectionShoots(t *testing.T) {
	e, p1, _ := twoPlayerBoard(t)
	pState := e.PlayerFor(p1)
	pState.Ammo = 5
	x0, y0 := int16(e.Board.Stats[p1].X), int16(e.Board.Stats[p1].Y)

	stepMsg(e, p1, keymaskMsg(InputMaskShift|InputMaskRight))

	if pState.Ammo != 4 {
		t.Errorf("ammo = %d, want 4: Shift+dir must fire a shot", pState.Ammo)
	}
	// Shooting does not also move the player: the shift branch is exclusive.
	if int16(e.Board.Stats[p1].X) != x0 || int16(e.Board.Stats[p1].Y) != y0 {
		t.Errorf("player moved while shooting, to (%d,%d)", e.Board.Stats[p1].X, e.Board.Stats[p1].Y)
	}
	if pState.DirX != 1 || pState.DirY != 0 {
		t.Errorf("firing direction = (%d,%d), want (1,0)", pState.DirX, pState.DirY)
	}
	bullet, ok := findBullet(e, p1)
	if !ok {
		t.Fatalf("no bullet owned by stat %d on the board", p1)
	}
	if int16(bullet.Y) != y0 || int16(bullet.X) <= x0 {
		t.Errorf("bullet at (%d,%d); want it travelling right along row %d from x=%d",
			bullet.X, bullet.Y, y0, x0)
	}
}

// --- Row: Space -> shoot last direction --------------------------------------

func TestM42SpaceShootsLastDirection(t *testing.T) {
	e, p1, _ := twoPlayerBoard(t)
	pState := e.PlayerFor(p1)
	pState.Ammo = 5

	// Walk left once to establish a facing, then fire with Space alone.
	stepMsg(e, p1, keymaskMsg(InputMaskLeft))
	if pState.DirX != -1 {
		t.Fatalf("precondition: walking left should face left, got DirX=%d", pState.DirX)
	}
	x0, y0 := int16(e.Board.Stats[p1].X), int16(e.Board.Stats[p1].Y)

	stepMsg(e, p1, keymaskMsg(InputMaskShoot))

	if pState.Ammo != 4 {
		t.Errorf("ammo = %d, want 4: Space must fire", pState.Ammo)
	}
	bullet, ok := findBullet(e, p1)
	if !ok {
		t.Fatalf("no bullet owned by stat %d: Space must fire without a direction key", p1)
	}
	if int16(bullet.Y) != y0 || int16(bullet.X) >= x0 {
		t.Errorf("bullet at (%d,%d); Space must shoot LEFT, the last direction walked (player at %d,%d)",
			bullet.X, bullet.Y, x0, y0)
	}
}

// --- Row: 'T' -> light torch -------------------------------------------------

func TestM42TorchKey(t *testing.T) {
	e, p1, _ := twoPlayerBoard(t)
	e.Board.Info.IsDark = true
	pState := e.PlayerFor(p1)
	pState.Torches = 2

	stepMsg(e, p1, commandMsg('T'))

	if pState.Torches != 1 {
		t.Errorf("torches = %d, want 1: 'T' must consume one", pState.Torches)
	}
	// ZZT-QUIRK: the torch is lit to TORCH_DURATION and then decremented by the
	// countdown at the tail of the SAME ElementPlayerTick, so it is one short
	// immediately after lighting. Faithful to ELEMENTS.PAS.
	if pState.TorchTicks != TORCH_DURATION-1 {
		t.Errorf("torchTicks = %d, want %d", pState.TorchTicks, TORCH_DURATION-1)
	}
}

func TestM42TorchKeyRefusedInLitRoom(t *testing.T) {
	e, p1, _ := twoPlayerBoard(t)
	e.Board.Info.IsDark = false
	pState := e.PlayerFor(p1)
	pState.Torches = 2

	stepMsg(e, p1, commandMsg('T'))

	if pState.Torches != 2 || pState.TorchTicks != 0 {
		t.Errorf("a lit room must not consume a torch: torches=%d ticks=%d",
			pState.Torches, pState.TorchTicks)
	}
}

// --- Row: 'P' -> pause (per-player) ------------------------------------------

func TestM42PauseKeyReachesEngine(t *testing.T) {
	e, p1, p2 := twoPlayerBoard(t)

	events := stepMsg(e, p1, commandMsg('P'))

	if !e.PlayerFor(p1).Paused {
		t.Errorf("'P' over the protocol must pause the sender")
	}
	if e.PlayerFor(p2).Paused {
		t.Errorf("'P' must not pause anybody else")
	}
	ev, ok := findPauseEvent(events)
	if !ok || !ev.Paused || ev.StatId != p1 {
		t.Errorf("want PauseEvent{StatId:%d,Paused:true}, got %+v (ok=%v)", p1, ev, ok)
	}
}

// --- Row: 'B' -> sound toggle (per-player) -----------------------------------

func TestM42SoundToggleKeyReachesEngine(t *testing.T) {
	e, p1, p2 := twoPlayerBoard(t)

	stepMsg(e, p1, commandMsg('B'))

	if e.PlayerFor(p1).SoundEnabled {
		t.Errorf("'B' over the protocol must mute the sender")
	}
	if !e.PlayerFor(p2).SoundEnabled {
		t.Errorf("'B' must not mute anybody else")
	}
	// The sidebar line the client draws comes from the HUD, so it must follow.
	if hudSnapshot(e, p1).SoundEnabled {
		t.Errorf("sender's HUD should report sound disabled")
	}
}

// --- Row: 'S' -> save game ---------------------------------------------------

func TestM42SaveKeyReachesEngine(t *testing.T) {
	e, p1, p2 := twoPlayerBoard(t)

	// If 'S' still reached GameWorldSave -> InputReadWaitKey this would hang.
	events := stepMsg(e, p1, commandMsg('S'))

	ev, ok := findSavePromptEvent(events)
	if !ok || ev.StatId != p1 {
		t.Errorf("want SavePromptEvent{StatId:%d}, got %+v (ok=%v)", p1, ev, ok)
	}
	if e.PlayerFor(p2).Paused {
		t.Errorf("'S' must not disturb the other player")
	}
}

// --- Row: 'Q' / Esc -> quit prompt -------------------------------------------

func TestM42QuitKeysReachEngine(t *testing.T) {
	for _, key := range []byte{'Q', KEY_ESCAPE} {
		t.Run(string(rune(key)), func(t *testing.T) {
			e, p1, _ := twoPlayerBoard(t)

			events := stepMsg(e, p1, commandMsg(key))

			if !hasQuitPromptEvent(events) {
				t.Errorf("key %q must emit QuitPromptEvent; got %+v", key, events)
			}
			if e.GamePlayExitRequested {
				t.Errorf("a live player's quit must only PROMPT, not exit play")
			}
		})
	}
}

// --- Row: 'H' -> help window --------------------------------------------------

func TestM42HelpKeyReachesEngine(t *testing.T) {
	e, p1, _ := twoPlayerBoard(t)

	events := stepMsg(e, p1, commandMsg('H'))

	ev, ok := findHelpEvent(events)
	if !ok || ev.StatId != p1 || ev.Filename != "GAME.HLP" {
		t.Errorf("want HelpEvent{StatId:%d,Filename:GAME.HLP}, got %+v (ok=%v)", p1, ev, ok)
	}
}

// --- Row: '?' -> debug prompt --------------------------------------------------

func TestM42DebugKeyReachesEngine(t *testing.T) {
	e, p1, _ := twoPlayerBoard(t)

	events := stepMsg(e, p1, commandMsg('?'))

	ev, ok := findDebugPromptEvent(events)
	if !ok || ev.StatId != p1 {
		t.Errorf("want DebugPromptEvent{StatId:%d}, got %+v (ok=%v)", p1, ev, ok)
	}
}

// --- Lowercase parity ----------------------------------------------------------

// The engine switches on UpCase(InputKeyPressed), so a client that sent 'p'
// instead of 'P' must still pause. The browser uppercases via KeyboardEvent.code,
// but the protocol accepts either and this pins that down.
func TestM42CommandKeysAreCaseInsensitive(t *testing.T) {
	e, p1, _ := twoPlayerBoard(t)

	stepMsg(e, p1, commandMsg('p'))

	if !e.PlayerFor(p1).Paused {
		t.Errorf("lowercase 'p' must pause: the engine UpCases the key byte")
	}
}

// --- The collision M4.2 resolved ------------------------------------------------

// WASD movement (a M3.5 client invention) made 'S' mean both "move down" and
// "save game" in a single InputKeyPressed byte. The client now sends the
// original's movement vocabulary only — arrows and numpad 8/4/6/2, both of
// which arrive as a keymask — so a bare 'S' byte is unambiguously a save.
// This test is the engine-side half of that contract.
func TestM42BareSKeyIsSaveNotMovement(t *testing.T) {
	e, p1, _ := twoPlayerBoard(t)
	y0 := int16(e.Board.Stats[p1].Y)

	events := stepMsg(e, p1, commandMsg('S'))

	if int16(e.Board.Stats[p1].Y) != y0 {
		t.Errorf("'S' moved the player: it must be the save key, not move-down")
	}
	if _, ok := findSavePromptEvent(events); !ok {
		t.Errorf("'S' must emit SavePromptEvent")
	}
}

// Movement never rides the Key field, so it can never be mistaken for a command.
func TestM42MovementCarriesNoCommandKey(t *testing.T) {
	for _, mask := range []uint16{InputMaskUp, InputMaskDown, InputMaskLeft, InputMaskRight} {
		input := inputMessageToPlayerInput(keymaskMsg(mask))
		switch UpCase(input.Key) {
		case 'T', 'P', 'B', 'S', 'Q', 'H', '?', KEY_ESCAPE:
			t.Errorf("mask %d produced command key %q", mask, input.Key)
		}
	}
}

// --- End to end, over a real socket -------------------------------------------

// The tests above stop at inputMessageToPlayerInput. This one goes over an
// actual WebSocket so the JSON encoding of the key byte is exercised too: a
// command key that survives Go's own struct but not `{"key":80}` would be a
// browser-only failure no other test here could see.
func TestM42CommandKeyOverWebSocket(t *testing.T) {
	world := testEmptyWorld(t)
	server := NewWebSocketServer(world, 1)
	server.TickDuration = 10 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go server.Run(ctx)

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(ServerReadLimit)

	if err := wsjson.Write(ctx, conn, JoinMessage{Type: MessageTypeJoin, Name: "tester", Board: 1}); err != nil {
		t.Fatalf("write join: %v", err)
	}
	var snapshot SnapshotMessage
	if err := wsjson.Read(ctx, conn, &snapshot); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}

	// Exactly what the browser's sendKey('P') puts on the wire.
	if err := wsjson.Write(ctx, conn, InputMessage{
		Type:     MessageTypeInput,
		PlayerID: snapshot.You.ID,
		Seq:      1,
		Key:      'P',
	}); err != nil {
		t.Fatalf("write input: %v", err)
	}

	// The pause event must come back addressed to our stat, with Paused=true.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("no pause event arrived after pressing 'P' over the socket")
		default:
		}
		var diff DiffMessage
		if err := wsjson.Read(ctx, conn, &diff); err != nil {
			t.Fatalf("read diff: %v", err)
		}
		for _, ev := range diff.Events {
			if ev.Type != "pause" {
				continue
			}
			if !ev.Paused {
				t.Fatalf("pause event says Paused=false; want true")
			}
			if ev.StatID != snapshot.You.StatID {
				t.Fatalf("pause event for stat %d, want our stat %d", ev.StatID, snapshot.You.StatID)
			}
			return
		}
	}
}
