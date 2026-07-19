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
	Kind   string // "boot", "play", "move", "shoot", "key", "settle", "capture"
	Label  string // capture label
	DX, DY int16  // move/shoot deltas
	Key    byte   // key byte carried into PlayerInput.Key
	Ticks  int    // boot/settle PIT ticks
}

func oracleDirDeltas(t *testing.T, path string, lineNo int, dir string) (int16, int16, byte) {
	t.Helper()
	switch dir {
	case "up":
		return 0, -1, KEY_UP
	case "down":
		return 0, 1, KEY_DOWN
	case "left":
		return -1, 0, KEY_LEFT
	case "right":
		return 1, 0, KEY_RIGHT
	}
	t.Fatalf("%s:%d: bad direction %q", path, lineNo, dir)
	return 0, 0, 0
}

func parseOracleScenario(t *testing.T, path string) (string, []oracleOp) {
	t.Helper()
	requireFixture(t, path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read scenario %s: %v", path, err)
	}
	world := ""
	var ops []oracleOp
	for lineNo, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		switch fields[0] {
		case "seed":
			// provenance only; the compared path is RNG-free
		case "world":
			world = fields[1]
		case "boot":
			// The oracle boots the real ZZT to its title screen; the adapter
			// runs the same span in title (monitor) state so title-screen
			// checkpoints and the virtual clock line up.
			n, err := strconv.Atoi(fields[1])
			if err != nil {
				t.Fatalf("%s:%d: bad boot %q", path, lineNo+1, line)
			}
			ops = append(ops, oracleOp{Kind: "boot", Ticks: n})
		case "play":
			ops = append(ops, oracleOp{Kind: "play"})
		case "settle":
			n, err := strconv.Atoi(fields[1])
			if err != nil {
				t.Fatalf("%s:%d: bad settle %q", path, lineNo+1, line)
			}
			ops = append(ops, oracleOp{Kind: "settle", Ticks: n})
		case "move", "shoot":
			op := oracleOp{Kind: fields[0]}
			op.DX, op.DY, op.Key = oracleDirDeltas(t, path, lineNo+1, fields[1])
			ops = append(ops, op)
		case "key":
			// key CH SC: the engine's input path reads characters, not
			// scancodes, so only CH crosses the seam.
			ch, err := strconv.Atoi(fields[1])
			if err != nil || ch < 0 || ch > 255 {
				t.Fatalf("%s:%d: bad key %q", path, lineNo+1, line)
			}
			ops = append(ops, oracleOp{Kind: "key", Key: byte(ch)})
		case "capture":
			ops = append(ops, oracleOp{Kind: "capture", Label: fields[1]})
		default:
			t.Fatalf("%s:%d: unknown scenario directive %q", path, lineNo+1, fields[0])
		}
	}
	if world == "" {
		t.Fatalf("%s: missing world directive", path)
	}
	return world, ops
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

// oracleSoundMatcher walks the oracle's tone onsets through the engine's
// queued melodies across the whole scenario. Vanilla's ISR plays one melody
// at a time and a newly accepted SoundQueue REPLACES whatever is still
// sounding (SOUNDS.PAS SoundQueue), so the oracle may play only a prefix of
// any melody; and a melody can still be mid-play when a checkpoint is taken,
// in which case its remaining onsets land in the next interval. The engine
// has no ISR — it emits each whole melody as a SoundEvent at queue time — so
// the matcher lets a melody be cut short exactly where the next one begins,
// and lets trailing melodies go unplayed (still sounding, or dropped by
// vanilla's priority rule). That leniency is one-sided: every tone the
// oracle DID play must appear, in order, at the engine's queue positions.
type oracleSoundMatcher struct {
	melodies [][]int
	mi, ni   int
}

func (m *oracleSoundMatcher) queue(notes string) {
	m.melodies = append(m.melodies, soundEventFreqs(notes))
}

func (m *oracleSoundMatcher) match(tones []int) error {
	for _, f := range tones {
		for {
			if m.mi >= len(m.melodies) {
				return fmt.Errorf("oracle played %d Hz with no engine melody left in the queue", f)
			}
			notes := m.melodies[m.mi]
			if m.ni < len(notes) && notes[m.ni] == f {
				m.ni++
				break
			}
			if m.ni < len(notes) {
				// Mid-melody mismatch: only a preemption by the next queued
				// melody explains it.
				if m.mi+1 < len(m.melodies) && len(m.melodies[m.mi+1]) > 0 && m.melodies[m.mi+1][0] == f {
					m.mi++
					m.ni = 1
					break
				}
				return fmt.Errorf("oracle tone %d Hz: engine melody %d expected %d Hz next (%v)",
					f, m.mi, notes[m.ni], notes)
			}
			// Melody fully played: move on to the next queued one.
			m.mi++
			m.ni = 0
		}
	}
	return nil
}

// oracleAdapterRun drives the engine through scenario ops, comparing each
// checkpoint against the oracle capture. mutate, if non-nil, runs right before
// the named checkpoint's comparison (the perturbation seam for the fail-closed
// test). Returns the first mismatch as an error; nil means full parity.
func oracleAdapterRun(t *testing.T, scenario, capture string, mutate func(label string)) error {
	t.Helper()

	world, ops := parseOracleScenario(t, filepath.Join("..", "fixtures", "oracle", scenario))
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

	worldBase := filepath.Join("..", "fixtures", "oracle", world)
	requireFixture(t, worldBase+".ZZT")

	RandomSeed(0)
	WorldCreate()
	if !WorldLoad(worldBase, ".ZZT", false) {
		t.Fatalf("WorldLoad(%q) failed", worldBase)
	}

	// Start in title state, as the booted ZZT does: the monitor sits on the
	// player square (GAME.PAS GamePlayLoop stamps GameStateElement) and the
	// title board simulates under it during `boot`.
	E.GameStateElement = E_MONITOR
	E.GamePlayExitRequested = false
	E.Board.Tiles[E.Board.Stats[0].X][E.Board.Stats[0].Y].Element = E_MONITOR
	E.Board.Tiles[E.Board.Stats[0].X][E.Board.Stats[0].Y].Color = ElementDefs[E_MONITOR].Color
	E.GenerateTransitionTable() // vanilla builds it at startup; TransitionDrawToBoard walks it
	E.TransitionDrawToBoard()
	E.CurrentTick = 0
	// Start ready to tick stats immediately: vanilla's pause branch acts on the
	// unpausing keypress at once, so the adapter's first step must tick stat 0
	// rather than spend the call opening a fresh cycle (replay_test's
	// StatCount+1 convention would put the engine one move behind the oracle).
	E.CurrentStatTicked = 0
	E.Events = nil
	inTitle := true

	var (
		sounds          oracleSoundMatcher
		intervalScrolls []ScrollEvent // scroll events this interval
		checkpointIdx   int
	)

	drainEvents := func() {
		for _, ev := range E.Events {
			switch ev := ev.(type) {
			case SoundEvent:
				sounds.queue(ev.Notes)
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
		case "boot":
			// Title simulation: the monitor consumes no input and the oracle
			// micro-worlds keep their title boards static, so these steps only
			// advance the clocks in lockstep with the oracle's boot span.
			for i := 0; i < op.Ticks/2; i++ {
				step(PlayerInput{})
			}
		case "play":
			// Vanilla's 'P': BoardEnter, stamp the player over the monitor
			// square, then enter play paused; nothing steps until the first
			// input (the oracle side spends 30 PIT ticks on the same paused
			// span, so only the virtual clock advances).
			E.GameStateElement = E_PLAYER
			BoardEnter(0)
			E.Board.Tiles[E.Board.Stats[0].X][E.Board.Stats[0].Y].Element = E_PLAYER
			E.Board.Tiles[E.Board.Stats[0].X][E.Board.Stats[0].Y].Color = ElementDefs[E_PLAYER].Color
			E.PlayerFor(0).Paused = true
			E.TransitionDrawToBoard()
			// The oracle's `play` spends 30 PIT ticks, but its first player
			// tick lands 2 ticks earlier on the board-clock grid than a
			// straight tick count predicts (keyboard IRQ delivery and the
			// pause loop's stale cycle gate both fire inside the emulator's
			// tick, measured against the recorded ORCLTIME captures — the
			// timing model in the header). 28 puts the engine's first player
			// tick on the same grid, which the ORCLTIME board-second
			// boundaries and their message flash phases pin exactly.
			E.TimerTicks += 28
			E.CurrentTick = 0
			E.CurrentStatTicked = 0
			drainEvents()
			inTitle = false
		case "move":
			// One `move` = 8 PIT ticks = 4 game cycles: input, then 3 idle
			// (see the timing model above).
			step(PlayerInput{DeltaX: op.DX, DeltaY: op.DY, Key: op.Key})
			for i := 0; i < 3; i++ {
				step(PlayerInput{})
			}
		case "shoot":
			// Shift+direction: shoots without moving (ELEMENTS.PAS PlayerTick).
			step(PlayerInput{DeltaX: op.DX, DeltaY: op.DY, Key: op.Key, Shift: true})
			for i := 0; i < 3; i++ {
				step(PlayerInput{})
			}
		case "key":
			step(PlayerInput{Key: op.Key})
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
			if err := compareCheckpoint(cp, intervalScrolls, inTitle); err != nil {
				return fmt.Errorf("checkpoint %s: %w", cp.Label, err)
			}
			var oracleTones []int
			for _, f := range cp.SoundOn {
				if f == 110 {
					continue // vanilla walk click: direct Sound(110), stubbed in the port
				}
				oracleTones = append(oracleTones, f)
			}
			if err := sounds.match(oracleTones); err != nil {
				return fmt.Errorf("checkpoint %s: %w", cp.Label, err)
			}
			intervalScrolls = nil
			checkpointIdx++
		}
	}
	if checkpointIdx != len(checkpoints) {
		t.Fatalf("capture %s holds %d checkpoints, scenario replayed %d", capture, len(checkpoints), checkpointIdx)
	}
	return nil
}

// compareCheckpoint holds the cell/counter/scroll comparison rules for one
// checkpoint (sounds are matched by the caller's oracleSoundMatcher). The
// error message pins the first mismatching cell/field precisely: that message
// is the seam's entire diagnostic value. Title checkpoints (taken before
// `play`) compare board cells only: the sidebar shows the title menu, not
// counters.
func compareCheckpoint(cp *oracleCheckpoint, scrolls []ScrollEvent, title bool) error {
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
	if title {
		return nil
	}
	p := E.PlayerFor(0)
	counters := []struct {
		row   int
		label string
		got   int16
	}{
		{7, "Health:", p.Health},
		{8, "Ammo:", p.Ammo},
		{9, "Torches:", p.Torches},
		{10, "Gems:", p.Gems},
		{11, "Score:", p.Score},
	}
	if E.Board.Info.TimeLimitSec > 0 {
		// Sidebar row 6 shows remaining board time on timed boards.
		counters = append(counters, struct {
			row   int
			label string
			got   int16
		}{6, "Time:", E.Board.Info.TimeLimitSec - p.BoardTimeSec})
	}
	for _, c := range counters {
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

// The M16.3 sweep scenarios (fixtures/oracle/*.scn document each one's
// coverage; the micro-worlds are authored in the matching .zwd files).

func TestOracleParityMoveScenario(t *testing.T) {
	if err := oracleAdapterRun(t, "move.scn", "move.capture.txt", nil); err != nil {
		t.Fatalf("oracle divergence: %v", err)
	}
}

func TestOracleParityItemScenario(t *testing.T) {
	if err := oracleAdapterRun(t, "item.scn", "item.capture.txt", nil); err != nil {
		t.Fatalf("oracle divergence: %v", err)
	}
}

func TestOracleParityDarkScenario(t *testing.T) {
	if err := oracleAdapterRun(t, "dark.scn", "dark.capture.txt", nil); err != nil {
		t.Fatalf("oracle divergence: %v", err)
	}
}

func TestOracleParityEnergizerScenario(t *testing.T) {
	if err := oracleAdapterRun(t, "nrg.scn", "nrg.capture.txt", nil); err != nil {
		t.Fatalf("oracle divergence: %v", err)
	}
}

func TestOracleParityShotScenario(t *testing.T) {
	if err := oracleAdapterRun(t, "shot.scn", "shot.capture.txt", nil); err != nil {
		t.Fatalf("oracle divergence: %v", err)
	}
}

func TestOracleParityPassageScenario(t *testing.T) {
	if err := oracleAdapterRun(t, "pass.scn", "pass.capture.txt", nil); err != nil {
		t.Fatalf("oracle divergence: %v", err)
	}
}

func TestOracleParityTimeScenario(t *testing.T) {
	if err := oracleAdapterRun(t, "time.scn", "time.capture.txt", nil); err != nil {
		t.Fatalf("oracle divergence: %v", err)
	}
}

// TestMonitorTickExitKeys pins ElementMonitorTick's semantics (ELEMENTS.PAS:
// the title-screen monitor requests a play-loop exit for exactly the title
// menu keys and consumes no other input). The monitor's on-screen behavior is
// covered by every sweep scenario's `title` checkpoint against the oracle's
// booted title screen.
func TestMonitorTickExitKeys(t *testing.T) {
	prevE := E
	defer func() { E = prevE }()
	E = NewEngine()
	prevKey := InputKeyPressed
	defer func() { InputKeyPressed = prevKey }()

	exitKeys := []byte{'\x1b', 'A', 'E', 'H', 'N', 'P', 'Q', 'R', 'S', 'W', '|',
		'a', 'e', 'h', 'n', 'p', 'q', 'r', 's', 'w'}
	for _, k := range exitKeys {
		E.GamePlayExitRequested = false
		InputKeyPressed = k
		E.ElementMonitorTick(0)
		if !E.GamePlayExitRequested {
			t.Errorf("monitor did not request exit for title key %q", k)
		}
	}
	for _, k := range []byte{0, ' ', 'x', '1', KEY_UP} {
		E.GamePlayExitRequested = false
		InputKeyPressed = k
		E.ElementMonitorTick(0)
		if E.GamePlayExitRequested {
			t.Errorf("monitor requested exit for non-title key %q", k)
		}
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
