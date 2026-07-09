package zztgo

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProtocolMessageRoundTrips(t *testing.T) {
	hud := HUDSnapshot{
		Health: 100,
		Ammo:   5,
		Gems:   2,
		Score:  200,
		Keys:   [7]bool{true, false, true},
	}
	snapshot := SnapshotMessage{
		Type:    MessageTypeSnapshot,
		BoardID: 1,
		Tick:    42,
		Seed:    1234,
		Hash:    0xfeedbeef,
		You:     PlayerSnapshot{ID: 7, StatID: 2, X: 10, Y: 12, Health: 100},
		Players: []PlayerSnapshot{
			{ID: 7, StatID: 2, X: 10, Y: 12, Health: 100},
			{ID: 8, StatID: 3, X: 11, Y: 12, Health: 90},
		},
		HUD:    hud,
		Screen: []ScreenCell{{X: 0, Y: 0, Ch: 'Z', Color: 0x1F}},
		Events: []ProtocolEvent{{Type: "sound", Notes: []uint16{'a', 'b', 'c'}, Priority: 2}},
	}

	roundTrip(t, JoinMessage{Type: MessageTypeJoin, Name: "tester", World: "TOWN", Board: 1}, &JoinMessage{})
	roundTrip(t, InputMessage{Type: MessageTypeInput, PlayerID: 7, Seq: 9, DeltaX: 1, Shift: true, Key: KEY_RIGHT, Keymask: InputMaskRight | InputMaskShoot}, &InputMessage{})
	roundTrip(t, snapshot, &SnapshotMessage{})
	roundTrip(t, DiffMessage{
		Type:    MessageTypeDiff,
		BoardID: 1,
		Tick:    43,
		Hash:    0xabc,
		Cells:   []ScreenCell{{X: 1, Y: 2, Ch: '!', Color: 0x0E}},
		Players: []PlayerSnapshot{{ID: 7, StatID: 2, X: 11, Y: 12, Health: 100}},
		HUD:     &hud,
		Events:  []ProtocolEvent{{Type: "scroll", StatID: 2, Title: "obj", Lines: []string{"hello"}}},
	}, &DiffMessage{})
	roundTrip(t, EventMessage{Type: MessageTypeEvent, BoardID: 1, Tick: 44, Event: ProtocolEvent{Type: "death", StatID: 2}}, &EventMessage{})
	roundTrip(t, BoardChangeMessage{Type: MessageTypeBoardChange, Snapshot: snapshot}, &BoardChangeMessage{})
}

func TestProtocolEvents(t *testing.T) {
	events := ProtocolEvents([]Event{
		ScrollEvent{Title: "obj", Lines: []string{"line"}, StatId: 2},
		QuitPromptEvent{},
		HelpEvent{Filename: "GAME.HLP", Title: "Help"},
		HighScoreEntryEvent{Score: 100, ListPos: 3},
		SoundEvent{Notes: "abc", Priority: 2},
		DeathEvent{StatId: 4},
		RespawnEvent{StatId: 5, X: 10, Y: 11},
		TransferEvent{StatId: 6, ToBoard: 2, EntryX: 3, EntryY: 4},
	})

	wantTypes := []string{"scroll", "quitPrompt", "help", "highScoreEntry", "sound", "death", "respawn", "transfer"}
	if len(events) != len(wantTypes) {
		t.Fatalf("events len=%d, want %d", len(events), len(wantTypes))
	}
	for i, wantType := range wantTypes {
		if events[i].Type != wantType {
			t.Fatalf("events[%d].Type=%q, want %q", i, events[i].Type, wantType)
		}
	}
	if events[0].Title != "obj" || !reflect.DeepEqual(events[0].Lines, []string{"line"}) {
		t.Fatalf("scroll event mismatch: %+v", events[0])
	}
	if events[7].ToBoard != 2 || events[7].EntryX != 3 || events[7].EntryY != 4 {
		t.Fatalf("transfer event mismatch: %+v", events[7])
	}
}

func TestProtocolSoundNotesAreBytes(t *testing.T) {
	events := ProtocolEvents([]Event{
		SoundEvent{Notes: "\xf9\x01", Priority: 1},
	})
	if len(events) != 1 {
		t.Fatalf("events len=%d, want 1", len(events))
	}
	want := []uint16{249, 1}
	if !reflect.DeepEqual(events[0].Notes, want) {
		t.Fatalf("sound notes=%v, want %v", events[0].Notes, want)
	}

	data, err := json.Marshal(events[0])
	if err != nil {
		t.Fatalf("marshal sound event: %v", err)
	}
	if !strings.Contains(string(data), `"notes":[249,1]`) {
		t.Fatalf("sound notes were not encoded as numeric bytes: %s", data)
	}
}

func TestRoomManagerSnapshotFromTown(t *testing.T) {
	setup := NewEngine()
	setup.Headless = true
	setup.WorldCreate()
	worldBase := filepath.Join("..", "fixtures", "TOWN")
	if !setup.WorldLoad(worldBase, ".ZZT", false) {
		t.Fatalf("WorldLoad(%q, %q) failed", worldBase, ".ZZT")
	}

	rm := NewRoomManager(setup.World)
	playerID := rm.JoinPlayer(1, 0, 0)
	pState, ok := rm.PlayerState(playerID)
	if !ok {
		t.Fatal("player missing after join")
	}
	pState.Ammo = 5
	pState.Score = 25

	snapshot, ok := rm.Snapshot(playerID)
	if !ok {
		t.Fatal("snapshot failed")
	}

	if snapshot.Type != MessageTypeSnapshot {
		t.Fatalf("snapshot.Type=%q, want %q", snapshot.Type, MessageTypeSnapshot)
	}
	if snapshot.BoardID != 1 {
		t.Fatalf("snapshot.BoardID=%d, want 1", snapshot.BoardID)
	}
	if snapshot.You.ID != playerID || snapshot.You.StatID < 0 {
		t.Fatalf("bad you snapshot: %+v", snapshot.You)
	}
	if len(snapshot.Players) != 1 {
		t.Fatalf("players len=%d, want 1", len(snapshot.Players))
	}
	if snapshot.HUD.Ammo != 5 || snapshot.HUD.Score != 25 {
		t.Fatalf("HUD mismatch: %+v", snapshot.HUD)
	}
	// Room engines transmit the 60-column board only; the sidebar columns
	// (60..79) are the legacy stat-0 sidebar and are replaced by HUDSnapshot.
	if len(snapshot.Screen) != BOARD_WIDTH*25 {
		t.Fatalf("screen cells=%d, want %d", len(snapshot.Screen), BOARD_WIDTH*25)
	}
	for _, cell := range snapshot.Screen {
		if cell.X >= BOARD_WIDTH {
			t.Fatalf("snapshot leaked sidebar cell at x=%d", cell.X)
		}
	}
	if snapshot.Hash == 0 {
		t.Fatal("snapshot hash is zero")
	}
	assertSnapshotHasNonBlankCell(t, snapshot)

	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	var decoded SnapshotMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	if !reflect.DeepEqual(decoded, snapshot) {
		t.Fatalf("snapshot round trip mismatch:\ngot:  %+v\nwant: %+v", decoded, snapshot)
	}
}

func roundTrip(t *testing.T, message interface{}, decoded interface{}) {
	t.Helper()

	data, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("marshal %T: %v", message, err)
	}
	if err := json.Unmarshal(data, decoded); err != nil {
		t.Fatalf("unmarshal %T: %v", message, err)
	}
	got := reflect.ValueOf(decoded).Elem().Interface()
	if !reflect.DeepEqual(got, message) {
		t.Fatalf("%T round trip mismatch:\ngot:  %+v\nwant: %+v", message, got, message)
	}
}

func assertSnapshotHasNonBlankCell(t *testing.T, snapshot SnapshotMessage) {
	t.Helper()

	for _, cell := range snapshot.Screen {
		if cell.Ch != 0 && cell.Ch != ' ' {
			return
		}
	}
	t.Fatal("snapshot screen has no non-blank cells")
}

func TestRoomManagerStepDiffFromTown(t *testing.T) {
	setup := NewEngine()
	setup.Headless = true
	setup.WorldCreate()
	worldBase := filepath.Join("..", "fixtures", "TOWN")
	if !setup.WorldLoad(worldBase, ".ZZT", false) {
		t.Fatalf("WorldLoad(%q, %q) failed", worldBase, ".ZZT")
	}

	rm := NewRoomManager(setup.World)
	playerID := rm.JoinPlayer(1, 30, 12)
	if _, ok := rm.Snapshot(playerID); !ok {
		t.Fatal("snapshot failed")
	}

	room, ok := rm.Room(1)
	if !ok {
		t.Fatal("room 1 missing")
	}
	room.Engine.DisplayMessage(100, "HELLO")
	room.Engine.Events = append(room.Engine.Events, SoundEvent{Notes: "abc", Priority: 2})
	diffs := rm.StepDiffs(map[PlayerID]PlayerInput{})

	diff, ok := diffs[playerID]
	if !ok {
		t.Fatal("missing diff for player")
	}
	if diff.Type != MessageTypeDiff {
		t.Fatalf("diff.Type=%q, want %q", diff.Type, MessageTypeDiff)
	}
	if diff.BoardID != 1 {
		t.Fatalf("diff.BoardID=%d, want 1", diff.BoardID)
	}
	if len(diff.Cells) == 0 {
		t.Fatal("diff has no dirty cells")
	}
	if len(diff.Cells) >= BOARD_WIDTH*25 {
		t.Fatalf("diff sent %d cells, want less than full snapshot", len(diff.Cells))
	}
	for _, cell := range diff.Cells {
		if cell.X >= BOARD_WIDTH {
			t.Fatalf("diff leaked sidebar cell at x=%d", cell.X)
		}
	}
	if len(diff.Players) != 1 || diff.Players[0].ID != playerID {
		t.Fatalf("diff players mismatch: %+v", diff.Players)
	}
	if diff.HUD == nil || diff.HUD.Health == 0 {
		t.Fatalf("diff HUD missing: %+v", diff.HUD)
	}
	if diff.Hash == 0 {
		t.Fatal("diff hash is zero")
	}
	if len(diff.Events) != 1 || diff.Events[0].Type != "sound" {
		t.Fatalf("diff events mismatch: %+v", diff.Events)
	}

	if cells := room.Engine.DrainScreenDirty(); len(cells) != 0 {
		t.Fatalf("room still has %d dirty cells after diff drain", len(cells))
	}
	if events := room.Engine.DrainEvents(); len(events) != 0 {
		t.Fatalf("room still has %d events after diff drain", len(events))
	}
}

// The engine keeps drawing the vanilla single-player sidebar into screen
// columns 60..79 from stat 0's PlayerState. In a room those columns belong to
// nobody, so they must never reach a client: each client draws its own sidebar
// from HUDSnapshot. Board columns 0..59 must still stream normally.
func TestRoomEngineDoesNotTransmitLegacySidebar(t *testing.T) {
	setup := NewEngine()
	setup.Headless = true
	setup.WorldCreate()
	worldBase := filepath.Join("..", "fixtures", "TOWN")
	if !setup.WorldLoad(worldBase, ".ZZT", false) {
		t.Fatalf("WorldLoad(%q, %q) failed", worldBase, ".ZZT")
	}

	rm := NewRoomManager(setup.World)
	playerID := rm.JoinPlayer(1, 30, 12)
	if _, ok := rm.Snapshot(playerID); !ok {
		t.Fatal("snapshot failed")
	}
	room, ok := rm.Room(1)
	if !ok {
		t.Fatal("room 1 missing")
	}

	// GameUpdateSidebar is the legacy writer; run it directly so the sidebar
	// columns are unambiguously dirty this tick.
	room.Engine.GameUpdateSidebar()
	if room.Engine.Screen[72][7].Ch == 0 {
		t.Fatal("GameUpdateSidebar did not write the health cell; test no longer proves anything")
	}

	diffs := rm.StepDiffs(map[PlayerID]PlayerInput{})
	diff, ok := diffs[playerID]
	if !ok {
		t.Fatal("missing diff for player")
	}
	for _, cell := range diff.Cells {
		if cell.X >= BOARD_WIDTH {
			t.Fatalf("diff leaked sidebar cell at (%d,%d)", cell.X, cell.Y)
		}
	}
	if diff.HUD == nil {
		t.Fatal("diff HUD missing")
	}
	if diff.HUD.TimeLimitSec != room.Engine.Board.Info.TimeLimitSec {
		t.Fatalf("HUD.TimeLimitSec=%d, want %d", diff.HUD.TimeLimitSec, room.Engine.Board.Info.TimeLimitSec)
	}
	if diff.HUD.SoundEnabled != SoundEnabled {
		t.Fatalf("HUD.SoundEnabled=%v, want %v", diff.HUD.SoundEnabled, SoundEnabled)
	}
}
