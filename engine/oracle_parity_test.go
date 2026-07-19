package zztgo

// M16.2 — the independent vanilla oracle seam.
//
// fixtures/oracle/*.capture.txt are recorded from the REAL ZZT.EXE v3.2 running
// under a pinned Zeta emulator build (oracle/README.md; regenerate only via
// `make oracle-regen`). These tests replay the same scenario scripts
// (fixtures/oracle/*.scn) through this engine and compare checkpoints. Nothing
// here ever generates a capture: the oracle side is pinned bytes from a program
// zztmmo did not produce.
//
// Timing model (shared contract with oracle/frontend_oracle.c):
//   - Vanilla paces one game cycle per TickTimeDuration = TickSpeed*2 = 8
//     *hundredths of a second* (GAME.PAS:1511,1582 via SoundHasTimeElapsed) —
//     about 2 PIT ticks (~110ms) at the default speed 4, NOT 8 PIT ticks.
//     Measured against the real ZZT.EXE: the gem-hint message's color cycles
//     9+(P2 mod 7) with P2 dropping 4 per 8 PIT ticks, pinning 2 ticks/cycle.
//   - The oracle's `move` directive is one keypress followed by 8 PIT ticks =
//     4 cycles: the first consumes the keypress and moves, the following 3 are
//     idle. The adapter maps `move` to one GameStep carrying the direction
//     plus 3 empty GameSteps.
//   - `settle N` maps to N/2 empty GameSteps.
//   - `play` enters play paused, as vanilla does; the first move unpauses and
//     moves in the same tick on both sides.
//   - Vanilla randomizes CurrentTick at play start; the adapter pins 0. The
//     scenarios are written to be phase-insensitive (settle margins longer than
//     the largest stat cycle) and RNG-free on the compared path.
//
// Documented representation normalizations (each is a vanilla-presentation vs
// headless-engine difference, not a simulation difference — PARITY.md "Oracle"):
//   - pause blink: vanilla's interactive loop draws a paused player blinking
//     (char 0x02, attr 0x1F alternating with the square's own content). The
//     headless engine emits PauseEvent and leaves drawing to the client. At the
//     paused player's square the oracle cell may read as either blink phase.
//   - modal scroll: vanilla freezes the sim inside a modal text window drawn
//     over the board; the engine emits ScrollEvent (M1.3 deviation). A
//     checkpoint taken while the oracle shows a window is compared by content:
//     the window's text lines against the ScrollEvent's lines.
//   - walk click: vanilla pokes the speaker directly (Sound(110),
//     ELEMENTS.PAS) for each step onto a walkable tile. The port stubs
//     Sound()/NoSound() (lib.go:124), so no event exists to compare; 110 Hz
//     onsets are excluded from the sound comparison. Recorded in NOTES.md as a
//     sound-parity gap for the M16.6 sweep.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type oracleCell struct {
	Ch, Attr byte
}

type oracleCheckpoint struct {
	Label string
	// Cells is the full 80x25 text page: board columns 0..59, sidebar 60..79.
	Cells [25][80]oracleCell
	// SoundOn holds the "sound on" frequencies emitted since the previous
	// checkpoint, in emission order.
	SoundOn []int
}

type oracleOp struct {
	Kind   string // "play", "move", "settle", "capture"
	Label  string // capture label
	DX, DY int16  // move deltas
	Key    byte   // move key byte
	Ticks  int    // settle PIT ticks
}

func parseOracleScenario(t *testing.T, path string) []oracleOp {
	t.Helper()
	requireFixture(t, path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read scenario %s: %v", path, err)
	}
	var ops []oracleOp
	for lineNo, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		switch fields[0] {
		case "seed", "boot":
			// seed is provenance; boot settles the emulated title screen. The
			// adapter starts directly in play state.
		case "play":
			ops = append(ops, oracleOp{Kind: "play"})
		case "settle":
			n, err := strconv.Atoi(fields[1])
			if err != nil {
				t.Fatalf("%s:%d: bad settle %q", path, lineNo+1, line)
			}
			ops = append(ops, oracleOp{Kind: "settle", Ticks: n})
		case "move":
			op := oracleOp{Kind: "move"}
			switch fields[1] {
			case "up":
				op.DY, op.Key = -1, KEY_UP
			case "down":
				op.DY, op.Key = 1, KEY_DOWN
			case "left":
				op.DX, op.Key = -1, KEY_LEFT
			case "right":
				op.DX, op.Key = 1, KEY_RIGHT
			default:
				t.Fatalf("%s:%d: bad direction %q", path, lineNo+1, fields[1])
			}
			ops = append(ops, op)
		case "capture":
			ops = append(ops, oracleOp{Kind: "capture", Label: fields[1]})
		default:
			t.Fatalf("%s:%d: unknown scenario directive %q", path, lineNo+1, fields[0])
		}
	}
	return ops
}

func parseOracleCapture(t *testing.T, path string) []oracleCheckpoint {
	t.Helper()
	requireFixture(t, path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read capture %s: %v", path, err)
	}
	var (
		out     []oracleCheckpoint
		sounds  []int
		current *oracleCheckpoint
		row     int
	)
	for lineNo, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "sound on "):
			fields := strings.Fields(line)
			freq, err := strconv.Atoi(fields[2])
			if err != nil {
				t.Fatalf("%s:%d: bad sound line %q", path, lineNo+1, line)
			}
			sounds = append(sounds, freq)
		case strings.HasPrefix(line, "sound off"):
			// off transitions carry no comparison signal
		case strings.HasPrefix(line, "checkpoint "):
			out = append(out, oracleCheckpoint{Label: strings.TrimPrefix(line, "checkpoint "), SoundOn: sounds})
			sounds = nil
			current = &out[len(out)-1]
			row = 0
		default:
			if current == nil || row >= 25 || len(line) != 320 {
				t.Fatalf("%s:%d: unexpected capture line (len %d)", path, lineNo+1, len(line))
			}
			for x := 0; x < 80; x++ {
				ch, err1 := strconv.ParseUint(line[x*4:x*4+2], 16, 8)
				at, err2 := strconv.ParseUint(line[x*4+2:x*4+4], 16, 8)
				if err1 != nil || err2 != nil {
					t.Fatalf("%s:%d: bad hex cell at col %d", path, lineNo+1, x)
				}
				current.Cells[row][x] = oracleCell{Ch: byte(ch), Attr: byte(at)}
			}
			row++
		}
	}
	return out
}

// sidebarText renders one sidebar row of an oracle checkpoint as a string.
func (cp *oracleCheckpoint) sidebarText(row int) string {
	var b strings.Builder
	for x := 60; x < 80; x++ {
		b.WriteByte(cp.Cells[row][x].Ch)
	}
	return b.String()
}

// counter parses the integer following `label` in the checkpoint's sidebar.
func (cp *oracleCheckpoint) counter(t *testing.T, row int, label string) int {
	t.Helper()
	text := cp.sidebarText(row)
	i := strings.Index(text, label)
	if i < 0 {
		t.Fatalf("checkpoint %s: sidebar row %d %q has no %q", cp.Label, row, text, label)
	}
	rest := strings.TrimLeft(text[i+len(label):], " ")
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	n, err := strconv.Atoi(rest[:end])
	if err != nil {
		t.Fatalf("checkpoint %s: cannot parse %s from %q", cp.Label, label, text)
	}
	return n
}

// rowText renders board columns of a capture row (for modal-content matching).
func (cp *oracleCheckpoint) rowText(row int) string {
	var b strings.Builder
	for x := 0; x < 60; x++ {
		b.WriteByte(cp.Cells[row][x].Ch)
	}
	return b.String()
}

// soundEventFreqs expands a queued SoundEvent's (note, duration) pairs to the
// tone-onset frequencies vanilla's timer ISR would play (SOUNDS.PAS: one
// Sound(SoundFreqTable[note-1]) per tone note; drums and rests excluded).
func soundEventFreqs(notes string) []int {
	var freqs []int
	for i := 0; i+1 < len(notes); i += 2 {
		note := notes[i]
		if note >= 16 && note < 240 {
			freqs = append(freqs, int(SoundFreqTable[note-1]))
		}
	}
	return freqs
}

// oracleAdapterRun drives the engine through scenario ops, comparing each
// checkpoint against the oracle capture. mutate, if non-nil, runs right before
// the named checkpoint's comparison (the perturbation seam for the fail-closed
// test). Returns the first mismatch as an error; nil means full parity.
func oracleAdapterRun(t *testing.T, scenario, capture string, mutate func(label string)) error {
	t.Helper()

	ops := parseOracleScenario(t, filepath.Join("..", "fixtures", "oracle", scenario))
	checkpoints := parseOracleCapture(t, filepath.Join("..", "fixtures", "oracle", capture))

	// Run on a fresh Engine swapped into the package global so the adapter
	// cannot pollute the shared E other tests (notably the replay fixture)
	// depend on — PlayerState hint flags, world, and screen all stay isolated.
	prevE := E
	defer func() { E = prevE }()
	E = NewEngine()

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
	E.TickSpeed = 4
	E.TickTimeDuration = int16(E.TickSpeed) * 2
	E.SoundBlockQueueing = false
	SoundClearQueue()

	worldBase := filepath.Join("..", "fixtures", "oracle", "ORCLROOM")
	requireFixture(t, worldBase+".ZZT")

	RandomSeed(0)
	WorldCreate()
	if !WorldLoad(worldBase, ".ZZT", false) {
		t.Fatalf("WorldLoad(%q) failed", worldBase)
	}

	E.GameStateElement = E_PLAYER
	E.GamePlayExitRequested = false
	E.Board.Tiles[E.Board.Stats[0].X][E.Board.Stats[0].Y].Element = E_PLAYER
	E.Board.Tiles[E.Board.Stats[0].X][E.Board.Stats[0].Y].Color = ElementDefs[E_PLAYER].Color
	BoardEnter(0)
	E.CurrentTick = 0
	// Start ready to tick stats immediately: vanilla's pause branch acts on the
	// unpausing keypress at once, so the adapter's first step must tick stat 0
	// rather than spend the call opening a fresh cycle (replay_test's
	// StatCount+1 convention would put the engine one move behind the oracle).
	E.CurrentStatTicked = 0
	E.Events = nil

	var (
		intervalSounds  []string      // queued SoundEvent note strings this interval
		intervalScrolls []ScrollEvent // scroll events this interval
		checkpointIdx   int
	)

	drainEvents := func() {
		for _, ev := range E.Events {
			switch ev := ev.(type) {
			case SoundEvent:
				intervalSounds = append(intervalSounds, ev.Notes)
			case ScrollEvent:
				intervalScrolls = append(intervalScrolls, ev)
			}
		}
		E.Events = nil
	}

	step := func(input PlayerInput) {
		E.GameStepWithInputs(map[int16]PlayerInput{0: input})
		drainEvents()
	}

	for _, op := range ops {
		switch op.Kind {
		case "play":
			// Vanilla enters play paused; nothing steps until the first input.
			E.PlayerFor(0).Paused = true
			E.GenerateTransitionTable() // vanilla builds it at startup; TransitionDrawToBoard walks it
			E.TransitionDrawToBoard()
			drainEvents()
		case "move":
			// One `move` = 8 PIT ticks = 4 game cycles: input, then 3 idle
			// (see the timing model above).
			step(PlayerInput{DeltaX: op.DX, DeltaY: op.DY, Key: op.Key})
			for i := 0; i < 3; i++ {
				step(PlayerInput{})
			}
		case "settle":
			for i := 0; i < op.Ticks/2; i++ {
				step(PlayerInput{})
			}
		case "capture":
			if checkpointIdx >= len(checkpoints) {
				t.Fatalf("scenario %s captures more checkpoints than %s holds", scenario, capture)
			}
			cp := &checkpoints[checkpointIdx]
			if cp.Label != op.Label {
				t.Fatalf("checkpoint order mismatch: scenario %q vs capture %q", op.Label, cp.Label)
			}
			if mutate != nil {
				mutate(cp.Label)
			}
			if err := compareCheckpoint(cp, intervalSounds, intervalScrolls); err != nil {
				return fmt.Errorf("checkpoint %s: %w", cp.Label, err)
			}
			intervalSounds = nil
			intervalScrolls = nil
			checkpointIdx++
		}
	}
	if checkpointIdx != len(checkpoints) {
		t.Fatalf("capture %s holds %d checkpoints, scenario replayed %d", capture, len(checkpoints), checkpointIdx)
	}
	return nil
}

// compareCheckpoint holds every comparison rule for one checkpoint. The error
// message pins the first mismatching cell/field/frequency precisely: that
// message is the seam's entire diagnostic value.
func compareCheckpoint(cp *oracleCheckpoint, soundNotes []string, scrolls []ScrollEvent) error {
	// Modal scroll checkpoints (vanilla draws a window over the board; the
	// engine emitted ScrollEvent instead) compare by window content.
	if len(scrolls) > 0 {
		sc := scrolls[0]
		for _, want := range sc.Lines {
			want = strings.TrimSpace(want)
			if want == "" {
				continue
			}
			found := false
			for y := 0; y < 25; y++ {
				if strings.Contains(cp.rowText(y), want) {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("engine ScrollEvent line %q not shown in the oracle's text window", want)
			}
		}
		if sc.Title != "" {
			found := false
			for y := 0; y < 25; y++ {
				if strings.Contains(cp.rowText(y), sc.Title) {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("engine ScrollEvent title %q not shown in the oracle's text window", sc.Title)
			}
		}
		return nil
	}

	// Board cells, with the pause-blink normalization at the paused player.
	pauseX, pauseY := -1, -1
	if E.PlayerFor(0).Paused {
		pauseX, pauseY = int(E.Board.Stats[0].X)-1, int(E.Board.Stats[0].Y)-1
	}
	for y := 0; y < 25; y++ {
		for x := 0; x < 60; x++ {
			got := E.Screen[x][y]
			want := cp.Cells[y][x]
			if got.Ch == want.Ch && got.Color == want.Attr {
				continue
			}
			if x == pauseX && y == pauseY && want.Ch == 0x02 && want.Attr == 0x1F {
				continue // vanilla's visible pause-blink phase over the paused player
			}
			return fmt.Errorf("board cell (%d,%d): oracle ch=%02x attr=%02x, engine ch=%02x attr=%02x",
				x, y, want.Ch, want.Attr, got.Ch, got.Color)
		}
	}

	// Player counters, parsed from the oracle sidebar against engine state.
	p := E.PlayerFor(0)
	for _, c := range []struct {
		row   int
		label string
		got   int16
	}{
		{7, "Health:", p.Health},
		{8, "Ammo:", p.Ammo},
		{9, "Torches:", p.Torches},
		{10, "Gems:", p.Gems},
		{11, "Score:", p.Score},
	} {
		text := cp.sidebarText(c.row)
		i := strings.Index(text, c.label)
		if i < 0 {
			return fmt.Errorf("oracle sidebar row %d %q lacks %q", c.row, text, c.label)
		}
		rest := strings.TrimLeft(text[i+len(c.label):], " ")
		end := 0
		for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
			end++
		}
		want, err := strconv.Atoi(rest[:end])
		if err != nil {
			return fmt.Errorf("oracle sidebar row %d %q: unparsable %s", c.row, text, c.label)
		}
		if int(c.got) != want {
			return fmt.Errorf("counter %s oracle=%d engine=%d", strings.TrimSuffix(c.label, ":"), want, c.got)
		}
	}

	// Sound: the oracle's tone onsets (walk clicks excluded — see the header)
	// must be a prefix-consistent match of the engine's queued melodies.
	var oracleTones []int
	for _, f := range cp.SoundOn {
		if f == 110 {
			continue // vanilla walk click: direct Sound(110), stubbed in the port
		}
		oracleTones = append(oracleTones, f)
	}
	var expected []int
	for _, notes := range soundNotes {
		expected = append(expected, soundEventFreqs(notes)...)
	}
	if len(oracleTones) > len(expected) {
		return fmt.Errorf("oracle played %d tone onsets %v, engine queued only %d %v",
			len(oracleTones), oracleTones, len(expected), expected)
	}
	for i, f := range oracleTones {
		if f != expected[i] {
			return fmt.Errorf("tone onset %d: oracle %d Hz, engine %d Hz (oracle %v, engine %v)",
				i, f, expected[i], oracleTones, expected)
		}
	}
	if len(expected) > 0 && len(oracleTones) == 0 {
		return fmt.Errorf("engine queued melody %v but the oracle played no tones", expected)
	}
	return nil
}

func TestOracleParityMainScenario(t *testing.T) {
	if err := oracleAdapterRun(t, "main.scn", "main.capture.txt", nil); err != nil {
		t.Fatalf("oracle divergence: %v", err)
	}
}

func TestOracleParityScrollScenario(t *testing.T) {
	if err := oracleAdapterRun(t, "scroll.scn", "scroll.capture.txt", nil); err != nil {
		t.Fatalf("oracle divergence: %v", err)
	}
}

// TestOracleComparisonFailsClosed proves the seam detects a single perturbed
// engine cell and names it precisely (M16.2 DoD).
func TestOracleComparisonFailsClosed(t *testing.T) {
	err := oracleAdapterRun(t, "main.scn", "main.capture.txt", func(label string) {
		if label == "movement" {
			E.Screen[30][12] = struct{ Ch, Color byte }{'X', 0x4C}
		}
	})
	if err == nil {
		t.Fatalf("perturbed engine screen was not detected")
	}
	msg := err.Error()
	if !strings.Contains(msg, "checkpoint movement") || !strings.Contains(msg, "(30,12)") {
		t.Fatalf("mismatch not pinned to the perturbed checkpoint/cell: %v", err)
	}
}

// TestOracleCounterComparisonFailsClosed perturbs a player counter instead of
// a cell: the seam must catch non-visual state too.
func TestOracleCounterComparisonFailsClosed(t *testing.T) {
	err := oracleAdapterRun(t, "main.scn", "main.capture.txt", func(label string) {
		if label == "pickup" {
			E.PlayerFor(0).Score += 5
		}
	})
	if err == nil {
		t.Fatalf("perturbed score was not detected")
	}
	if !strings.Contains(err.Error(), "counter Score") {
		t.Fatalf("mismatch not pinned to the Score counter: %v", err)
	}
}
