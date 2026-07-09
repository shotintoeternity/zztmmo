package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash"
	"hash/fnv"
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

// StateHash is the replay safety net: a deterministic FNV-1a digest of the
// simulation state M0 has made headless. TStat's unknown pointer fields are
// deliberately excluded because pointer addresses are runtime artifacts, not
// serialized game state.
func StateHash() uint64 {
	h := fnv.New64a()

	for x := 0; x <= BOARD_WIDTH+1; x++ {
		for y := 0; y <= BOARD_HEIGHT+1; y++ {
			hashByte(h, Board.Tiles[x][y].Element)
			hashByte(h, Board.Tiles[x][y].Color)
		}
	}

	hashInt16(h, Board.StatCount)
	for i := int16(0); i <= Board.StatCount; i++ {
		stat := &Board.Stats[i]
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

	hashWorldInfo(h, &World.Info)
	hashUint32(h, RandSeed)

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

	prevHeadless := Headless
	prevInput := activeInput
	defer func() {
		Headless = prevHeadless
		SetInputSource(prevInput)
	}()

	Headless = true
	VideoInstall()
	TextWindowInit(5, 3, 50, 18)

	InputDeltaX = 0
	InputDeltaY = 0
	InputShiftPressed = false
	InputKeyPressed = 0
	InputLastDeltaX = 0
	InputLastDeltaY = 0
	InputKeyBuffer = ""
	PlayerDirX = 0
	PlayerDirY = 0
	GamePlayExitRequested = false
	GamePaused = false
	TickSpeed = 4
	TickTimeDuration = int16(TickSpeed) * 2
	SoundBlockQueueing = false
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

	GameStateElement = E_PLAYER
	GamePaused = false
	GamePlayExitRequested = false
	Board.Tiles[Board.Stats[0].X][Board.Stats[0].Y].Element = E_PLAYER
	Board.Tiles[Board.Stats[0].X][Board.Stats[0].Y].Color = ElementDefs[E_PLAYER].Color
	BoardEnter()
	CurrentTick = Random(100)
	CurrentStatTicked = Board.StatCount + 1
	SetInputSource(&ScriptedInput{Ticks: townReplayScript()})

	hashes := make([]string, 0, townReplaySteps/townReplayInterval)
	for step := 1; step <= townReplaySteps; step++ {
		GameStep()
		if GamePlayExitRequested {
			t.Fatalf("replay requested exit at step %d", step)
		}
		if step%townReplayInterval == 0 {
			hashes = append(hashes, fmt.Sprintf("%016x", StateHash()))
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
