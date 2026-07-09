package zztgo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const (
	townReplaySeed     uint32 = 42
	townReplaySteps           = 600
	townReplayInterval        = 100
)

type replayFixture struct {
	World    string   `json:"world"`
	Seed     uint32   `json:"seed"`
	Steps    int      `json:"steps"`
	Interval int      `json:"interval"`
	Hashes   []string `json:"hashes"`
}

func TestTownReplayDeterminism(t *testing.T) {
	first := runTownReplay(t)
	second := runTownReplay(t)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("TOWN replay is not deterministic:\nfirst:  %v\nsecond: %v", first, second)
	}

	want := replayFixture{
		World:    "TOWN.ZZT",
		Seed:     townReplaySeed,
		Steps:    townReplaySteps,
		Interval: townReplayInterval,
		Hashes:   first,
	}

	path := filepath.Join("..", "fixtures", "town.replay.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		writeReplayFixture(t, path, want)
		return
	}
	if err != nil {
		t.Fatalf("read replay fixture: %v", err)
	}

	var got replayFixture
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode replay fixture: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("replay fixture mismatch:\ngot:  %+v\nwant: %+v", got, want)
	}
}

func runTownReplay(t *testing.T) []string {
	t.Helper()

	prevHeadless := E.Headless
	prevInput := E.ActiveInput
	defer func() {
		E.Headless = prevHeadless
		SetInputSource(prevInput)
	}()

	E.Headless = true
	VideoInstall()
	TextWindowInit(5, 3, 50, 18)

	InputDeltaX = 0
	InputDeltaY = 0
	InputShiftPressed = false
	InputKeyPressed = 0
	InputLastDeltaX = 0
	InputLastDeltaY = 0
	InputKeyBuffer = ""
	E.GamePlayExitRequested = false
	E.PlayerFor(0).Paused = false
	E.TickSpeed = 4
	E.TickTimeDuration = int16(E.TickSpeed) * 2
	E.SoundBlockQueueing = false
	SoundClearQueue()

	worldBase := filepath.Join("..", "fixtures", "TOWN")
	if _, err := os.Stat(worldBase + ".ZZT"); err != nil {
		t.Fatalf("TOWN replay world is missing: %v", err)
	}

	RandomSeed(townReplaySeed)
	WorldCreate()
	if !WorldLoad(worldBase, ".ZZT", false) {
		t.Fatalf("WorldLoad(%q, %q) failed", worldBase, ".ZZT")
	}

	E.GameStateElement = E_PLAYER
	E.PlayerFor(0).Paused = false
	E.GamePlayExitRequested = false
	E.Board.Tiles[E.Board.Stats[0].X][E.Board.Stats[0].Y].Element = E_PLAYER
	E.Board.Tiles[E.Board.Stats[0].X][E.Board.Stats[0].Y].Color = ElementDefs[E_PLAYER].Color
	BoardEnter(0)
	E.CurrentTick = Random(100)
	E.CurrentStatTicked = E.Board.StatCount + 1
	SetInputSource(&ScriptedInput{Ticks: townReplayScript()})

	hashes := make([]string, 0, townReplaySteps/townReplayInterval)
	for step := 1; step <= townReplaySteps; step++ {
		GameStep(nil)
		if E.GamePlayExitRequested {
			t.Fatalf("replay requested exit at step %d", step)
		}
		if step%townReplayInterval == 0 {
			hashes = append(hashes, fmt.Sprintf("%016x", StateHash(E)))
		}
	}
	return hashes
}

func townReplayScript() []ScriptedTick {
	ticks := make([]ScriptedTick, townReplaySteps)
	pattern := []ScriptedTick{
		{DeltaX: 1, Key: KEY_RIGHT},
		{DeltaX: 1, Key: KEY_RIGHT},
		{DeltaY: 1, Key: KEY_DOWN},
		{DeltaY: 1, Key: KEY_DOWN},
		{DeltaX: -1, Key: KEY_LEFT},
		{DeltaX: -1, Key: KEY_LEFT},
		{DeltaY: -1, Key: KEY_UP},
		{DeltaY: -1, Key: KEY_UP},
		{},
		{},
		{},
		{},
	}
	for i := range ticks {
		ticks[i] = pattern[i%len(pattern)]
	}
	return ticks
}

func writeReplayFixture(t *testing.T, path string, fixture replayFixture) {
	t.Helper()

	data, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatalf("encode replay fixture: %v", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create replay fixture dir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write replay fixture: %v", err)
	}
}

func TestTwoEnginesOneProcess(t *testing.T) {
	worldBase := filepath.Join("..", "fixtures", "TOWN")

	// 1. Run single engine on board 1, collect hashes
	eSingle1 := NewEngine()
	eSingle1.Headless = true
	eSingle1.WorldCreate()
	if !eSingle1.WorldLoad(worldBase, ".ZZT", false) {
		t.Fatalf("failed to load world for eSingle1")
	}
	eSingle1.RandomSeed(42)
	eSingle1.BoardOpen(1)
	eSingle1.GameStateElement = E_PLAYER
	eSingle1.BoardEnter(0)
	eSingle1.CurrentTick = eSingle1.Random(100)
	eSingle1.CurrentStatTicked = eSingle1.Board.StatCount + 1
	eSingle1.SetInputSource(&ScriptedInput{Ticks: townReplayScript()})

	single1Hashes := make([]uint64, 100)
	for i := 0; i < 100; i++ {
		eSingle1.GameStep(nil)
		single1Hashes[i] = StateHash(eSingle1)
	}

	// 2. Run single engine on board 2, collect hashes
	eSingle2 := NewEngine()
	eSingle2.Headless = true
	eSingle2.WorldCreate()
	if !eSingle2.WorldLoad(worldBase, ".ZZT", false) {
		t.Fatalf("failed to load world for eSingle2")
	}
	eSingle2.RandomSeed(100) // use a different seed to test independence
	eSingle2.BoardOpen(2)
	eSingle2.GameStateElement = E_PLAYER
	eSingle2.BoardEnter(0)
	eSingle2.CurrentTick = eSingle2.Random(100)
	eSingle2.CurrentStatTicked = eSingle2.Board.StatCount + 1
	eSingle2.SetInputSource(&ScriptedInput{Ticks: townReplayScript()})

	single2Hashes := make([]uint64, 100)
	for i := 0; i < 100; i++ {
		eSingle2.GameStep(nil)
		single2Hashes[i] = StateHash(eSingle2)
	}

	// 3. Now run two engines concurrently (interleaved steps) and check hashes
	e1 := NewEngine()
	e1.Headless = true
	e1.WorldCreate()
	if !e1.WorldLoad(worldBase, ".ZZT", false) {
		t.Fatalf("failed to load world for e1")
	}
	e1.RandomSeed(42)
	e1.BoardOpen(1)
	e1.GameStateElement = E_PLAYER
	e1.BoardEnter(0)
	e1.CurrentTick = e1.Random(100)
	e1.CurrentStatTicked = e1.Board.StatCount + 1
	e1.SetInputSource(&ScriptedInput{Ticks: townReplayScript()})

	e2 := NewEngine()
	e2.Headless = true
	e2.WorldCreate()
	if !e2.WorldLoad(worldBase, ".ZZT", false) {
		t.Fatalf("failed to load world for e2")
	}
	e2.RandomSeed(100)
	e2.BoardOpen(2)
	e2.GameStateElement = E_PLAYER
	e2.BoardEnter(0)
	e2.CurrentTick = e2.Random(100)
	e2.CurrentStatTicked = e2.Board.StatCount + 1
	e2.SetInputSource(&ScriptedInput{Ticks: townReplayScript()})

	for i := 0; i < 100; i++ {
		// Interleave steps
		e1.GameStep(nil)
		e2.GameStep(nil)

		// Verify no cross-talk and exact matching
		h1 := StateHash(e1)
		h2 := StateHash(e2)

		if h1 != single1Hashes[i] {
			t.Fatalf("Step %d: e1 StateHash mismatch: got %016x, want %016x", i+1, h1, single1Hashes[i])
		}
		if h2 != single2Hashes[i] {
			t.Fatalf("Step %d: e2 StateHash mismatch: got %016x, want %016x", i+1, h2, single2Hashes[i])
		}
	}
}

func TestScrollEventAndReply(t *testing.T) {
	e := NewEngine()
	e.Headless = true
	e.WorldCreate()
	e.BoardCreate()

	// Add an object at (10, 10)
	e.Board.Tiles[10][10] = TTile{Element: E_OBJECT, Color: 0x0F}
	e.AddStat(10, 10, E_OBJECT, 0x0F, 1, StatTemplateDefault)
	statId := e.Board.StatCount

	stat := &e.Board.Stats[statId]
	stat.Data = "@obj\r:touch\rHello!\r!label;link\r:label\r#set flag\r"
	stat.DataLen = int16(len(stat.Data))
	stat.DataPos = -1

	// Send touch message
	e.OopSend(statId, "touch", false)

	// Run GameStep to trigger scroll display
	e.GameStep(nil)

	// Assert ScrollEvent was emitted
	if len(e.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(e.Events))
	}
	ev, ok := e.Events[0].(ScrollEvent)
	if !ok {
		t.Fatalf("expected ScrollEvent, got %T", e.Events[0])
	}
	if ev.Title != "obj" {
		t.Errorf("expected ScrollEvent Title 'obj', got %q", ev.Title)
	}
	if len(ev.Lines) != 2 || ev.Lines[0] != "Hello!" || ev.Lines[1] != "!label;link" {
		t.Errorf("unexpected ScrollEvent Lines: %v", ev.Lines)
	}

	// Send reply
	e.SubmitScrollReply(statId, "label")

	// Run next step
	e.GameStep(nil)

	// Assert flag is set (ZZT-OOP flags are stored in uppercase)
	if e.WorldGetFlagPosition("FLAG") <= 0 {
		t.Errorf("expected flag 'FLAG' to be set after reply send")
	}
}

// TestRoomManagerPassageTransfer is the M2.5/M3.1 definition of done:
// P1 walks through a passage from board A to board B while P2 keeps playing
// board A undisturbed.
//
// Both engines share the same TWorld; passage lookup must not serialize live
// player state back into that shared world data.
func TestRoomManagerPassageTransfer(t *testing.T) {
	const passageColor = byte(0x0E)
	const boardA = int16(1)
	const boardB = int16(2)

	// Use one engine to build and serialize both boards into a shared world.
	setup := NewEngine()
	setup.Headless = true
	setup.WorldCreate()

	// Build board A: passage at (10,12) color 0x0E → board B.
	setup.BoardCreate()
	for ix := int16(1); ix <= BOARD_WIDTH; ix++ {
		for iy := int16(1); iy <= BOARD_HEIGHT; iy++ {
			setup.Board.Tiles[ix][iy] = TTile{Element: E_EMPTY}
		}
	}
	setup.Board.Tiles[setup.Board.Stats[0].X][setup.Board.Stats[0].Y] = TTile{Element: E_EMPTY}
	setup.Board.StatCount = -1
	setup.Board.Tiles[10][12] = TTile{Element: E_PASSAGE, Color: passageColor}
	setup.AddStat(10, 12, E_PASSAGE, int16(passageColor), 0, StatTemplateDefault)
	setup.Board.Stats[setup.Board.StatCount].P3 = byte(boardB)
	setup.Board.Name = "Board A"
	setup.BoardClose()
	setup.World.BoardData[boardA] = make([]byte, len(setup.IoTmpBuf))
	copy(setup.World.BoardData[boardA], setup.IoTmpBuf[:])

	// Build board B: matching passage at (5,5) color 0x0E → board A.
	setup.BoardCreate()
	for ix := int16(1); ix <= BOARD_WIDTH; ix++ {
		for iy := int16(1); iy <= BOARD_HEIGHT; iy++ {
			setup.Board.Tiles[ix][iy] = TTile{Element: E_EMPTY}
		}
	}
	setup.Board.Tiles[setup.Board.Stats[0].X][setup.Board.Stats[0].Y] = TTile{Element: E_EMPTY}
	setup.Board.StatCount = -1
	setup.Board.Tiles[5][5] = TTile{Element: E_PASSAGE, Color: passageColor}
	setup.AddStat(5, 5, E_PASSAGE, int16(passageColor), 0, StatTemplateDefault)
	setup.Board.Stats[setup.Board.StatCount].P3 = byte(boardA)
	setup.Board.Name = "Board B"
	setup.BoardClose()
	setup.World.BoardData[boardB] = make([]byte, len(setup.IoTmpBuf))
	copy(setup.World.BoardData[boardB], setup.IoTmpBuf[:])
	setup.World.BoardCount = 2

	rm := NewRoomManager(setup.World)

	// Spawn P1 at (9,12) on board A.
	p1 := rm.JoinPlayer(boardA, 9, 12)
	p1State, ok := rm.PlayerState(p1)
	if !ok {
		t.Fatal("P1 missing after join")
	}
	p1State.Health = 100

	// Spawn P2 at (30,12) on board A.
	p2 := rm.JoinPlayer(boardA, 30, 12)
	p2State, ok := rm.PlayerState(p2)
	if !ok {
		t.Fatal("P2 missing after join")
	}
	p2State.Health = 100
	p2State.Ammo = 5
	p2State.Score = 200

	frozenWorld := rm.FrozenWorld()
	boardADataBefore := append([]byte(nil), frozenWorld.BoardData[boardA]...)
	boardBDataBefore := append([]byte(nil), frozenWorld.BoardData[boardB]...)

	// Tick: P1 moves right onto the passage at (10,12).
	rm.Step(map[PlayerID]PlayerInput{
		p1: {DeltaX: 1, DeltaY: 0},
		p2: {DeltaX: 0, DeltaY: 0},
	})

	// P1 should now be on board B.
	p1Board, p1Stat, ok := rm.PlayerLocation(p1)
	if !ok {
		t.Fatal("P1 missing after transfer")
	}
	if p1Board != boardB {
		t.Errorf("P1 board = %d, want %d (boardB)", p1Board, boardB)
	}

	// P1 should be at (5,5) on board B.
	roomB, ok := rm.Room(boardB)
	if !ok {
		t.Fatal("board B room missing after transfer")
	}
	p1X := int16(roomB.Engine.Board.Stats[p1Stat].X)
	p1Y := int16(roomB.Engine.Board.Stats[p1Stat].Y)
	if p1X != 5 || p1Y != 5 {
		t.Errorf("P1 on board B at (%d,%d), want (5,5)", p1X, p1Y)
	}

	// Board A should have exactly one E_PLAYER left (P2).
	roomA, ok := rm.Room(boardA)
	if !ok {
		t.Fatal("board A room missing while P2 remains")
	}
	if playerCount := roomA.Engine.PlayerCount(); playerCount != 1 {
		t.Errorf("board A has %d E_PLAYER stats, want 1 (P2 only)", playerCount)
	}

	// P2 stays on board A with inventory intact.
	p2Board, _, ok := rm.PlayerLocation(p2)
	if !ok {
		t.Fatal("P2 missing after P1 transfer")
	}
	if p2Board != boardA {
		t.Errorf("P2 board = %d, want %d (boardA)", p2Board, boardA)
	}
	if p2State.Ammo != 5 || p2State.Score != 200 || p2State.Health != 100 {
		t.Errorf("P2 inventory changed: ammo=%d score=%d health=%d", p2State.Ammo, p2State.Score, p2State.Health)
	}

	frozenWorld = rm.FrozenWorld()
	if !bytes.Equal(frozenWorld.BoardData[boardA], boardADataBefore) || !bytes.Equal(frozenWorld.BoardData[boardB], boardBDataBefore) {
		t.Error("passage transfer mutated serialized world board data")
	}

	if !rm.LeavePlayer(p2) {
		t.Fatal("LeavePlayer(P2) failed")
	}
	if _, ok := rm.Room(boardA); ok {
		t.Fatal("board A room still active after last player left")
	}
	if rm.ActiveRoomCount() != 1 {
		t.Errorf("active rooms = %d, want 1", rm.ActiveRoomCount())
	}

	frozen := NewEngine()
	frozen.Headless = true
	frozen.World = rm.FrozenWorld()
	frozen.BoardOpen(boardA)
	if playerCount := frozen.PlayerCount(); playerCount != 0 {
		t.Errorf("frozen board A has %d players, want 0", playerCount)
	}
}
