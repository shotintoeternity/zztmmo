package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash"
	"hash/fnv"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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

// StateHash is the replay safety net: a deterministic FNV-1a digest of the
// simulation state M0 has made headless. TStat's unknown pointer fields are
// deliberately excluded because pointer addresses are runtime artifacts, not
// serialized game state.
func StateHash(e *Engine) uint64 {
	h := fnv.New64a()

	if pState, exists := e.Players[0]; exists {
		e.World.Info.Health = pState.Health
		e.World.Info.Ammo = pState.Ammo
		e.World.Info.Gems = pState.Gems
		e.World.Info.Torches = pState.Torches
		e.World.Info.TorchTicks = pState.TorchTicks
		e.World.Info.EnergizerTicks = pState.EnergizerTicks
		e.World.Info.Score = pState.Score
		e.World.Info.Keys = pState.Keys
		e.World.Info.BoardTimeSec = pState.BoardTimeSec
		e.World.Info.BoardTimeHsec = pState.BoardTimeHsec
	}

	for x := 0; x <= BOARD_WIDTH+1; x++ {
		for y := 0; y <= BOARD_HEIGHT+1; y++ {
			hashByte(h, e.Board.Tiles[x][y].Element)
			hashByte(h, e.Board.Tiles[x][y].Color)
		}
	}

	hashInt16(h, e.Board.StatCount)
	for i := int16(0); i <= e.Board.StatCount; i++ {
		stat := &e.Board.Stats[i]
		hashByte(h, stat.X)
		hashByte(h, stat.Y)
		hashInt16(h, stat.StepX)
		hashInt16(h, stat.StepY)
		hashInt16(h, stat.Cycle)
		hashByte(h, stat.P1)
		hashByte(h, stat.P2)
		hashByte(h, stat.P3)
		hashInt16(h, stat.Follower)
		hashInt16(h, stat.Leader)
		hashByte(h, stat.Under.Element)
		hashByte(h, stat.Under.Color)
		hashString(h, stat.Data)
		hashInt16(h, stat.DataPos)
		hashInt16(h, stat.DataLen)
	}

	hashWorldInfo(h, &e.World.Info)
	hashUint32(h, e.RandSeed)

	return h.Sum64()
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
	E.GamePaused = false
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
	E.GamePaused = false
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

func hashWorldInfo(h hash.Hash64, info *TWorldInfo) {
	hashInt16(h, info.Ammo)
	hashInt16(h, info.Gems)
	for _, key := range info.Keys {
		hashBool(h, key)
	}
	hashInt16(h, info.Health)
	hashInt16(h, info.CurrentBoard)
	hashInt16(h, info.Torches)
	hashInt16(h, info.TorchTicks)
	hashInt16(h, info.EnergizerTicks)
	hashInt16(h, info.padding1)
	hashInt16(h, info.Score)
	hashString(h, info.Name)
	for _, flag := range info.Flags {
		hashString(h, flag)
	}
	hashInt16(h, info.BoardTimeSec)
	hashInt16(h, info.BoardTimeHsec)
	hashBool(h, info.IsSave)
	for _, b := range info.padding2 {
		hashByte(h, b)
	}
}

func hashString(h hash.Hash64, s string) {
	hashUint32(h, uint32(len(s)))
	_, _ = h.Write([]byte(s))
}

func hashBool(h hash.Hash64, b bool) {
	if b {
		hashByte(h, 1)
		return
	}
	hashByte(h, 0)
}

func hashByte(h hash.Hash64, b byte) {
	_, _ = h.Write([]byte{b})
}

func hashInt16(h hash.Hash64, n int16) {
	var buf [2]byte
	binary.LittleEndian.PutUint16(buf[:], uint16(n))
	_, _ = h.Write(buf[:])
}

func hashUint32(h hash.Hash64, n uint32) {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], n)
	_, _ = h.Write(buf[:])
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
	e.PendingScrollReply = "label"
	e.PendingScrollStatId = statId

	// Run next step
	e.GameStep(nil)

	// Assert flag is set (ZZT-OOP flags are stored in uppercase)
	if e.WorldGetFlagPosition("FLAG") <= 0 {
		t.Errorf("expected flag 'FLAG' to be set after reply send")
	}
}

// RoomManager is a minimal test helper that owns two Engine instances (one per
// board) and routes TransferEvents between them. It mirrors what the M3 server
// will do: when a player crosses a passage or board edge, the RoomManager
// despawns them from the source engine and spawns them on the destination engine
// at the entry coordinates carried by the TransferEvent.
type RoomManager struct {
	engines      map[int16]*Engine
	playerBoard  map[*PlayerState]int16
	playerStatId map[*PlayerState]int16
}

func newRoomManager() *RoomManager {
	return &RoomManager{
		engines:      make(map[int16]*Engine),
		playerBoard:  make(map[*PlayerState]int16),
		playerStatId: make(map[*PlayerState]int16),
	}
}

func (rm *RoomManager) addEngine(boardId int16, e *Engine) {
	rm.engines[boardId] = e
}

func (rm *RoomManager) spawnPlayer(boardId int16) *PlayerState {
	e := rm.engines[boardId]
	statId := e.SpawnPlayer()
	ps := e.PlayerFor(statId)
	rm.playerBoard[ps] = boardId
	rm.playerStatId[ps] = statId
	return ps
}

func (rm *RoomManager) step(inputs map[*PlayerState]PlayerInput) {
	engineInputs := make(map[int16]map[int16]PlayerInput)
	for ps, inp := range inputs {
		boardId := rm.playerBoard[ps]
		statId := rm.playerStatId[ps]
		if _, ok := engineInputs[boardId]; !ok {
			engineInputs[boardId] = make(map[int16]PlayerInput)
		}
		engineInputs[boardId][statId] = inp
	}
	for _, boardId := range rm.boardIds() {
		e := rm.engines[boardId]
		m := engineInputs[boardId]
		if m == nil {
			m = map[int16]PlayerInput{}
		}
		e.GameStepWithInputs(m)
	}
	for _, boardId := range rm.boardIds() {
		e := rm.engines[boardId]
		remaining := e.Events[:0]
		for _, ev := range e.Events {
			if te, ok := ev.(TransferEvent); ok {
				rm.transfer(boardId, te)
			} else {
				remaining = append(remaining, ev)
			}
		}
		e.Events = remaining
	}
}

func (rm *RoomManager) boardIds() []int16 {
	ids := make([]int16, 0, len(rm.engines))
	for boardId := range rm.engines {
		ids = append(ids, boardId)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (rm *RoomManager) transfer(srcBoardId int16, te TransferEvent) {
	srcEngine := rm.engines[srcBoardId]
	dstEngine := rm.engines[te.ToBoard]
	if dstEngine == nil {
		return
	}
	psCopy := *srcEngine.PlayerFor(te.StatId)
	var ps *PlayerState
	for p, bid := range rm.playerBoard {
		if bid == srcBoardId && rm.playerStatId[p] == te.StatId {
			ps = p
			break
		}
	}
	srcEngine.RemovePlayer(te.StatId)
	for p, bid := range rm.playerBoard {
		if bid == srcBoardId && rm.playerStatId[p] > te.StatId {
			rm.playerStatId[p]--
		}
	}
	dstEngine.Board.Info.StartPlayerX = byte(te.EntryX)
	dstEngine.Board.Info.StartPlayerY = byte(te.EntryY)
	newStatId := dstEngine.SpawnPlayer()
	*dstEngine.PlayerFor(newStatId) = psCopy
	if ps != nil {
		rm.playerBoard[ps] = te.ToBoard
		rm.playerStatId[ps] = newStatId
	}
}

// TestRoomManagerPassageTransfer is the M2.5 definition of done:
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

	// Spin up engine A: shares the world, opens board A.
	eA := NewEngine()
	eA.Headless = true
	eA.MultiRoom = true
	eA.SetInputSource(&ScriptedInput{})
	eA.World = setup.World
	eA.BoardOpen(boardA)

	// Spin up engine B: shares the same world, opens board B.
	eB := NewEngine()
	eB.Headless = true
	eB.MultiRoom = true
	eB.SetInputSource(&ScriptedInput{})
	eB.World = setup.World
	eB.BoardOpen(boardB)

	rm := newRoomManager()
	rm.addEngine(boardA, eA)
	rm.addEngine(boardB, eB)

	// Spawn P1 at (9,12) on board A.
	eA.Board.Info.StartPlayerX = 9
	eA.Board.Info.StartPlayerY = 12
	p1 := rm.spawnPlayer(boardA)
	p1.Health = 100

	// Spawn P2 at (30,12) on board A.
	eA.Board.Info.StartPlayerX = 30
	eA.Board.Info.StartPlayerY = 12
	p2 := rm.spawnPlayer(boardA)
	p2.Health = 100
	p2.Ammo = 5
	p2.Score = 200

	boardADataBefore := append([]byte(nil), eA.World.BoardData[boardA]...)
	boardBDataBefore := append([]byte(nil), eA.World.BoardData[boardB]...)

	// Tick: P1 moves right onto the passage at (10,12).
	rm.step(map[*PlayerState]PlayerInput{
		p1: {DeltaX: 1, DeltaY: 0},
		p2: {DeltaX: 0, DeltaY: 0},
	})

	// P1 should now be on board B.
	if rm.playerBoard[p1] != boardB {
		t.Errorf("P1 board = %d, want %d (boardB)", rm.playerBoard[p1], boardB)
	}

	// P1 should be at (5,5) on board B.
	p1Stat := rm.playerStatId[p1]
	p1X := int16(eB.Board.Stats[p1Stat].X)
	p1Y := int16(eB.Board.Stats[p1Stat].Y)
	if p1X != 5 || p1Y != 5 {
		t.Errorf("P1 on board B at (%d,%d), want (5,5)", p1X, p1Y)
	}

	// Board A should have exactly one E_PLAYER left (P2).
	playerCount := int16(0)
	for i := int16(0); i <= eA.Board.StatCount; i++ {
		if eA.Board.Tiles[eA.Board.Stats[i].X][eA.Board.Stats[i].Y].Element == E_PLAYER {
			playerCount++
		}
	}
	if playerCount != 1 {
		t.Errorf("board A has %d E_PLAYER stats, want 1 (P2 only)", playerCount)
	}

	// P2 stays on board A with inventory intact.
	if rm.playerBoard[p2] != boardA {
		t.Errorf("P2 board = %d, want %d (boardA)", rm.playerBoard[p2], boardA)
	}
	if p2.Ammo != 5 || p2.Score != 200 || p2.Health != 100 {
		t.Errorf("P2 inventory changed: ammo=%d score=%d health=%d", p2.Ammo, p2.Score, p2.Health)
	}

	if !bytes.Equal(eA.World.BoardData[boardA], boardADataBefore) || !bytes.Equal(eA.World.BoardData[boardB], boardBDataBefore) {
		t.Error("passage transfer mutated serialized world board data")
	}
}
