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
//   - Vanilla randomizes CurrentTick at play start (and again on unpause), which
//     the adapter pins to 0 for scenarios written to be phase-insensitive
//     (settle margins longer than the largest stat cycle) and RNG-free on the
//     compared path. A scenario that declares `phase` instead has both spans
//     SOLVED for — see oracleSolvePhases — which is what lets M16.4 compare
//     devices whose glyph and scheduling ride CurrentTick.
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
//   - modal hyperlink: vanilla draws a `!label;text` line as its caption alone
//     and runs the chosen label inside the same modal OopExecute. The engine
//     emits the raw line and re-enters on the reply, so a window checkpoint
//     compares captions and oracleTextWindow below plays the client half.

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
	Scan   byte   // scancode, needed only for the zero-char keys a text window reads
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

func parseOracleScenario(t *testing.T, path string) (string, []oracleOp, bool) {
	t.Helper()
	requireFixture(t, path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read scenario %s: %v", path, err)
	}
	world := ""
	solvePhase := false
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
		case "phase":
			// Declares the scenario phase-sensitive: it contains elements whose
			// glyph or scheduling depends on CurrentTick, which vanilla picks with
			// Random(100) and the adapter must therefore solve for rather than
			// assume. Ignored by the oracle frontend (the real ZZT needs no help
			// picking its own phase).
			solvePhase = true
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
			// key CH SC: gameplay reads characters, not scancodes, so CH is
			// normally all that crosses the seam. The exception is an open text
			// window, whose cursor keys arrive as char 0 (INPUT.PAS reads the
			// scancode); the adapter's window emulation needs SC for those.
			ch, err := strconv.Atoi(fields[1])
			if err != nil || ch < 0 || ch > 255 {
				t.Fatalf("%s:%d: bad key %q", path, lineNo+1, line)
			}
			op := oracleOp{Kind: "key", Key: byte(ch)}
			if len(fields) > 2 {
				sc, err := strconv.ParseUint(fields[2], 16, 8)
				if err != nil {
					t.Fatalf("%s:%d: bad key scancode %q", path, lineNo+1, line)
				}
				op.Scan = byte(sc)
			}
			ops = append(ops, op)
		case "capture":
			ops = append(ops, oracleOp{Kind: "capture", Label: fields[1]})
		default:
			t.Fatalf("%s:%d: unknown scenario directive %q", path, lineNo+1, fields[0])
		}
	}
	if world == "" {
		t.Fatalf("%s: missing world directive", path)
	}
	return world, ops, solvePhase
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
// Sound(SoundFreqTable[note-1]) per tone note; rests excluded). A drum note
// (>= 240) is SoundPlayDrum's burst of Len rapid onsets; several drums draw
// their frequencies from Random() at ZZT startup, seeded by the oracle's boot
// clock, so drum onsets match by count as wildcards (-1), not by frequency.
func soundEventFreqs(notes string) []int {
	var freqs []int
	for i := 0; i+1 < len(notes); i += 2 {
		note := notes[i]
		switch {
		case note >= 16 && note < 240:
			freqs = append(freqs, int(SoundFreqTable[note-1]))
		case note >= 240:
			for j := int16(0); j < SoundDrumTable[note-240].Len; j++ {
				freqs = append(freqs, -1)
			}
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
	// Vanilla's live speaker state (SOUNDS.PAS SoundIsPlaying /
	// SoundCurrentPriority / SoundDurationCounter), carried across cycles so a
	// queue attempt can be refused by something still sounding from an earlier
	// one. remaining counts PIT ticks, the unit note durations are measured in.
	playing   bool
	current   int16
	remaining int
}

// oracleTicksPerCycle is the ISR's advance per game cycle: vanilla paces one
// cycle per TickTimeDuration = TickSpeed*2 hundredths of a second, about two
// 18.2 Hz timer ticks at the pinned speed 4 (the same mapping the whole harness
// rests on — see the timing model at the top of this file).
const oracleTicksPerCycle = 2

// soundEventTicks is how long a queued pattern occupies vanilla's speaker: the
// sum of its note durations, which SoundTimerHandler counts down one per PIT
// tick (SoundDurationMultiplier is 1).
func soundEventTicks(notes string) int {
	total := 0
	for i := 0; i+1 < len(notes); i += 2 {
		total += int(notes[i+1])
	}
	return total
}

// queueCycle takes every SoundEvent one game cycle emitted and reduces it to
// what vanilla's speaker would actually have carried, by running vanilla's own
// arbitration (SOUNDS.PAS SoundQueue) over them.
//
// The port's Engine.SoundQueue emits EVERY queue attempt as an event with its
// priority and leaves the arbitration to the client (M1.5/M4.4), so the engine's
// stream is a superset of vanilla's. Two rules decide what survives:
//
//   - Priority. A queue attempt is refused outright while something with a
//     higher priority is still sounding, and a #play (priority -1) both appends
//     instead of replacing and locks out normal sounds until it finishes.
//   - Replacement. An accepted attempt overwrites the buffer, so the previous
//     pattern loses whatever the timer ISR had not played yet.
//
// Within ONE cycle no ISR tick can intervene — the cycle's code runs in a single
// burst, and at the pinned speed 4 the timer fires only about twice per cycle —
// so every attempt but the last accepted one is overwritten before it makes a
// single onset. M16.4's pusher train is exactly this: a moving pusher ticks the
// pusher behind it immediately, out of cycle order, so two identical clicks are
// queued microseconds apart and vanilla's speaker clicks once.
//
// The priority half of the rule also has to survive ACROSS cycles, because
// vanilla's SoundIsPlaying stays true for as long as the pattern's note
// durations last (SoundTimerHandler counts SoundDurationCounter down one per PIT
// tick), and every attempt below the sounding pattern's priority is refused for
// that whole span. M16.5's energizer is the case that needs it: its melody is
// 168 ticks long at priority 9, so the attack clicks (priority 2) the player
// makes while energized never reach the speaker at all. So the matcher carries
// (playing, current, remaining) between cycles and ages it by one cycle's worth
// of PIT ticks each time.
//
// None of this is leniency: it removes melodies the matcher would otherwise have
// demanded the oracle play, using vanilla's own arbitration rather than a fudge.
// What survives is still required, in order, by match's one-sided prefix rule.
//
// A WalkClickEvent (M16.6b) rides alongside the cycle's SoundEvents in true
// dispatch order but is not queued at all: ELEMENTS.PAS's Sound(110)/NoSound
// is a direct hardware poke gated purely by SoundIsPlaying, never by priority,
// and NoSound cancels it before the timer ISR could ever observe it as
// "playing" — so it never itself becomes the thing a later check in the same
// cycle sees as sounding. It is therefore resolved in place, reading m.playing
// as of that point in the loop (aging carried over, plus any
// earlier-in-this-cycle SoundEvent acceptance) without mutating any
// arbitration state, and becomes its own one-tone melody entry when audible —
// positioned correctly relative to same-cycle SoundEvents because a melody's
// own onsets never sound until a later cycle's ISR tick regardless.
func (m *oracleSoundMatcher) queueCycle(events []Event) {
	if m.playing {
		m.remaining -= oracleTicksPerCycle
		if m.remaining <= 0 {
			m.playing = false
			m.remaining = 0
		}
	}
	var (
		buffer   string
		accepted bool
	)
	for _, event := range events {
		switch ev := event.(type) {
		case SoundEvent:
			if m.playing && !((ev.Priority >= m.current && m.current != -1) || ev.Priority == -1) {
				continue // refused: something more important is still sounding
			}
			if ev.Priority >= 0 || !m.playing {
				buffer, m.current, m.remaining = ev.Notes, ev.Priority, soundEventTicks(ev.Notes)
			} else {
				// A #play queues behind what is sounding rather than replacing it.
				buffer += ev.Notes
				m.remaining += soundEventTicks(ev.Notes)
			}
			m.playing = true
			accepted = true
		case WalkClickEvent:
			if !m.playing {
				m.melodies = append(m.melodies, []int{int(ev.FreqHz)})
			}
		}
	}
	if accepted {
		m.melodies = append(m.melodies, soundEventFreqs(buffer))
	}
}

// sameTone compares two frequencies as the PC speaker actually distinguishes
// them: by the PIT divisor. Nothing about the note survives the hardware except
// that one 16-bit number, and both sides round-trip through it differently, so
// equal notes routinely differ by a hertz or two as integers.
//
// The round trip is exact and worth modelling exactly rather than fudging:
//
//	Turbo Pascal's Crt.Sound(Hz) programs the timer with 1193181 div Hz,
//	truncating — so the engine's table entry becomes a divisor.
//	Zeta reports 1193181.66 / divisor back to the frontend, which prints it
//	rounded (oracle/frontend_oracle.c speaker_on).
//
// So the engine's 2048 Hz explosion note is divisor 582, which the oracle logs
// as 2050 Hz; its 1149 Hz transporter note is divisor 1038, logged as 1150.
// Running the trip forward and comparing what the oracle WOULD have printed
// keeps a genuinely different note a failure — adjacent notes are always
// separate divisors in this range. -1 is the wildcard the drum tables use
// (M16.3: several drum frequencies are seeded from the oracle's boot RNG).
func sameTone(engine, oracle int) bool {
	if engine == oracle || engine == -1 {
		return true
	}
	if engine <= 0 || oracle <= 0 {
		return false
	}
	divisor := 1193181 / engine // Turbo Pascal Crt.Sound: truncating integer divide
	if divisor <= 0 {
		return false
	}
	return int(1193181.66/float64(divisor)+0.5) == oracle
}

func (m *oracleSoundMatcher) match(tones []int) error {
	for _, f := range tones {
		for {
			if m.mi >= len(m.melodies) {
				return fmt.Errorf("oracle played %d Hz with no engine melody left in the queue", f)
			}
			notes := m.melodies[m.mi]
			if m.ni < len(notes) && sameTone(notes[m.ni], f) {
				m.ni++
				break
			}
			if m.ni < len(notes) {
				// Mid-melody mismatch: only a preemption by the next queued
				// melody explains it.
				if m.mi+1 < len(m.melodies) && len(m.melodies[m.mi+1]) > 0 &&
					sameTone(m.melodies[m.mi+1][0], f) {
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

// oracleTextWindow is the client half of a de-modalized scroll (M1.3): the
// engine emits a ScrollEvent and keeps ticking, so somebody has to hold the
// window state vanilla keeps on its stack in TextWindowSelect. It models only
// what TXTWIND.PAS:273-362 does with a keystroke — cursor up/down clamped to
// the line range, ENTER selecting a `!label;text` line, ESCAPE rejecting — and
// leaves the reply routing to SubmitScrollReply.
type oracleTextWindow struct {
	Scroll    ScrollEvent
	LinePos   int    // 1-based, as TXTWIND.PAS counts
	Hyperlink string // label chosen with ENTER; "" for a dismissal
}

// oracleHyperlinkLabel returns the label of a `!label;text` line, or "".
func oracleHyperlinkLabel(line string) string {
	if !strings.HasPrefix(line, "!") {
		return ""
	}
	rest := line[1:]
	if i := strings.Index(rest, ";"); i >= 0 {
		return rest[:i]
	}
	return rest
}

// oracleWindowCaption is what the oracle DRAWS for one window line: a
// `!label;text` line shows only the caption (TXTWIND.PAS TextWindowDrawLine
// copies from the ';'), everything else shows verbatim.
func oracleWindowCaption(line string) string {
	if strings.HasPrefix(line, "!") {
		if i := strings.Index(line, ";"); i >= 0 {
			return line[i+1:]
		}
	}
	return line
}

// key applies one keystroke and reports whether the window closed.
func (w *oracleTextWindow) key(ch, scan byte) bool {
	switch {
	case scan == 0x48: // up
		if w.LinePos > 1 {
			w.LinePos--
		}
	case scan == 0x50: // down
		if w.LinePos < len(w.Scroll.Lines) {
			w.LinePos++
		}
	case ch == '\x1b': // ESCAPE: TextWindowRejected, no hyperlink
		w.Hyperlink = ""
		return true
	case ch == KEY_ENTER:
		w.Hyperlink = ""
		if w.LinePos >= 1 && w.LinePos <= len(w.Scroll.Lines) {
			w.Hyperlink = oracleHyperlinkLabel(w.Scroll.Lines[w.LinePos-1])
		}
		return true
	}
	return false
}

// oracleAdapterRun drives the engine through scenario ops, comparing each
// checkpoint against the oracle capture. mutate, if non-nil, runs right before
// the named checkpoint's comparison (the perturbation seam for the fail-closed
// test). Returns the first mismatch as an error; nil means full parity.
// oraclePhaseRange is the number of CurrentTick phases vanilla can start a
// GamePlayLoop on: GAME.PAS:1515 picks it with Random(100).
const oraclePhaseRange = 100

// oracleAdapterRun replays a scenario against its capture. For a scenario that
// declares `phase`, it first SOLVES for the two CurrentTick phases vanilla chose
// (see oracleSolvePhases); otherwise both are pinned to 0, exactly as before.
func oracleAdapterRun(t *testing.T, scenario, capture string, mutate func(label string)) error {
	t.Helper()

	world, ops, solvePhase := parseOracleScenario(t, filepath.Join("..", "fixtures", "oracle", scenario))
	checkpoints := parseOracleCapture(t, filepath.Join("..", "fixtures", "oracle", capture))

	// Run on a fresh Engine swapped into the package global so the adapter
	// cannot pollute the shared E other tests (notably the replay fixture)
	// depend on — PlayerState hint flags, world, and screen all stay isolated.
	prevE := E
	defer func() { E = prevE }()

	if !solvePhase {
		return oracleAdapterReplay(t, scenario, world, ops, checkpoints, mutate, 0, 0)
	}
	return oracleSolvePhases(t, scenario, world, ops, checkpoints, mutate)
}

// oracleSolvePhases recovers the two unknown CurrentTick phases in a
// phase-sensitive scenario, rather than importing them from the capture by hand.
//
// Vanilla runs `CurrentTick := Random(100)` on entering a GamePlayLoop
// (GAME.PAS:1515) and AGAIN when the player unpauses (GAME.PAS:1564), so the
// title span and the play span start on independent, unknowable phases. That
// phase is not cosmetic: conveyor, transporter, and spinning-gun glyphs are
// drawn straight off CurrentTick, and the cycle gate
// `CurrentTick mod Cycle = statId mod Cycle` decides which stats tick when.
//
// The engine side is directly settable — the headless unpause deliberately does
// NOT re-randomize CurrentTick (a documented multiplayer deviation; re-rolling
// it would perturb every other player's scheduling), so a phase assigned at
// `play` survives the unpause.
//
// The two spans are searched one after the other rather than jointly, which
// costs at most 100+100 replays instead of 100*100. That is sound because the
// title checkpoint is reached before `play`, so no play phase can affect it —
// and the play search then replays the scenario from the very beginning with the
// solved title phase, so a title span that MOVED things (a conveyor turning its
// neighbours for the whole boot span) still hands the play span the board it
// actually produced.
//
// This imports nothing: it recovers one scalar per span and then requires that
// scalar to reproduce EVERY remaining checkpoint. A wrong draw or a wrong
// stagger still fails, because no phase satisfies all of them at once — which is
// why a phase-sensitive world should carry devices with different periods.
func oracleSolvePhases(t *testing.T, scenario, world string, ops []oracleOp, checkpoints []oracleCheckpoint, mutate func(label string)) error {
	t.Helper()

	// The title span ends at the first checkpoint, so replay only up to it.
	titleOps := ops
	for i, op := range ops {
		if op.Kind == "play" {
			titleOps = ops[:i]
			break
		}
	}
	titlePhase := -1
	var titleErr error
	if len(titleOps) > 0 && len(checkpoints) > 0 {
		for phase := 0; phase < oraclePhaseRange; phase++ {
			err := oracleAdapterReplay(t, scenario, world, titleOps, checkpoints[:1], mutate, int16(phase), 0)
			if err == nil {
				titlePhase = phase
				break
			}
			if phase == 0 {
				titleErr = err
			}
		}
		if titlePhase < 0 {
			return fmt.Errorf("no CurrentTick phase in 0..%d reproduces the title checkpoint of %s; at phase 0: %w",
				oraclePhaseRange-1, scenario, titleErr)
		}
	} else {
		titlePhase = 0
	}

	var playErr error
	var solutions []int
	for phase := 0; phase < oraclePhaseRange; phase++ {
		err := oracleAdapterReplay(t, scenario, world, ops, checkpoints, mutate, int16(titlePhase), int16(phase))
		if err == nil {
			solutions = append(solutions, phase)
			continue
		}
		if phase == 0 {
			playErr = err
		}
	}
	if len(solutions) == 0 {
		return fmt.Errorf("no CurrentTick phase in 0..%d reproduces %s; at phase 0: %w",
			oraclePhaseRange-1, scenario, playErr)
	}
	// Logged, not asserted: a phase-sensitive scenario that many phases satisfy
	// is not wrong, it is weak — it means the world's devices are not actually
	// pinning the phase, and the scenario should carry devices whose periods
	// disagree. Visible in `go test -v` rather than silently passing as strong.
	t.Logf("%s: title phase %d; %d of %d play phases reproduce every checkpoint (%v)",
		scenario, titlePhase, len(solutions), oraclePhaseRange, solutions)
	return nil
}

func oracleAdapterReplay(t *testing.T, scenario, world string, ops []oracleOp, checkpoints []oracleCheckpoint, mutate func(label string), titlePhase, playPhase int16) error {
	t.Helper()

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
	E.CurrentTick = titlePhase
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
		window          *oracleTextWindow
	)

	drainEvents := func() {
		var cycleEvents []Event
		for _, ev := range E.Events {
			switch ev := ev.(type) {
			case SoundEvent:
				cycleEvents = append(cycleEvents, ev)
			case WalkClickEvent:
				cycleEvents = append(cycleEvents, ev)
			case ScrollEvent:
				intervalScrolls = append(intervalScrolls, ev)
				window = &oracleTextWindow{Scroll: ev, LinePos: 1}
			}
		}
		sounds.queueCycle(cycleEvents)
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
			E.TimerTicks += 30
			E.CurrentTick = playPhase
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
			// A key pressed while a text window is open belongs to the window,
			// not to the game: vanilla is inside TextWindowSelect's modal loop
			// (TXTWIND.PAS:273) and ElementPlayerTick never sees it. The engine
			// has no modal loop — it emitted a ScrollEvent and kept running — so
			// the adapter plays the client the fork expects (M1.3/M17.4): move
			// the cursor, and on ENTER/ESC send the selection back through
			// SubmitScrollReply, which is exactly what web/src does on dismiss.
			if window != nil {
				if closed := window.key(op.Key, op.Scan); closed {
					E.SubmitScrollReply(window.Scroll.StatId, window.Hyperlink)
					window = nil
				}
				for i := 0; i < 4; i++ {
					step(PlayerInput{})
				}
				break
			}
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
				t.Fatalf("scenario %s captures more checkpoints than its capture holds", scenario)
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
			if err := sounds.match(cp.SoundOn); err != nil {
				return fmt.Errorf("checkpoint %s: %w", cp.Label, err)
			}
			intervalScrolls = nil
			checkpointIdx++
		}
	}
	if checkpointIdx != len(checkpoints) {
		t.Fatalf("capture for %s holds %d checkpoints, scenario replayed %d", scenario, len(checkpoints), checkpointIdx)
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
		for _, line := range sc.Lines {
			// A hyperlink line is stored raw (`!label;caption`) and drawn as its
			// caption alone, so compare what the window actually shows.
			want := strings.TrimSpace(oracleWindowCaption(line))
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

// The M16.4 sweep scenarios: movers and devices.

func TestOracleParityPushScenario(t *testing.T) {
	if err := oracleAdapterRun(t, "push.scn", "push.capture.txt", nil); err != nil {
		t.Fatalf("oracle divergence: %v", err)
	}
}

// TestOracleParityDeviceScenario is the first phase-solved scenario: conveyors,
// a transporter, and a spinning gun all draw off CurrentTick, which vanilla
// picks with Random(100). See oracleSolvePhases.
func TestOracleParityDeviceScenario(t *testing.T) {
	if err := oracleAdapterRun(t, "dev.scn", "dev.capture.txt", nil); err != nil {
		t.Fatalf("oracle divergence: %v", err)
	}
}

// TestOracleParityMechScenario covers the devices that act on their own the
// moment their board loads — pusher, duplicator, and bomb. They live on boards
// reached by walking off a board edge rather than on board 0, because the title
// span is not a cycle-accurate model of real ZZT booting and the phase search
// absorbs that error only for animation, never for accumulated state
// (fixtures/oracle/mech.scn documents the whole design).
func TestOracleParityMechScenario(t *testing.T) {
	if err := oracleAdapterRun(t, "mech.scn", "mech.capture.txt", nil); err != nil {
		t.Fatalf("oracle divergence: %v", err)
	}
}

// TestOracleParityBlinkScenario covers the blink wall and both blink rays,
// including the asymmetric player-push branch and the vanilla bug in its
// north-south half (fixtures/oracle/blink.scn documents the design). Every stat
// on the board has cycle 1, so the scenario is phase-insensitive and declares no
// `phase` directive.
func TestOracleParityBlinkScenario(t *testing.T) {
	if err := oracleAdapterRun(t, "blink.scn", "blink.capture.txt", nil); err != nil {
		t.Fatalf("oracle divergence: %v", err)
	}
}

// TestOracleParityRideScenario closes the acting paths ORCLDEV left open:
// conveyors carrying cargo round their ring (free and obstructed) and spinning
// guns actually firing, with both random draws forced so the aim is the only
// thing left to compare (fixtures/oracle/ride.scn documents the design).
func TestOracleParityRideScenario(t *testing.T) {
	if err := oracleAdapterRun(t, "ride.scn", "ride.capture.txt", nil); err != nil {
		t.Fatalf("oracle divergence: %v", err)
	}
}

// The M16.5 sweep scenarios: creatures, combat, and projectiles.

// TestOracleParityBeastScenario covers the seeking creatures and what contact
// costs: ruffian, lion, bear (including the bear's E_BREAKABLE attack branch and
// its sensitivity range), and the player's own damaging touch. Every seek is
// forced onto one axis by keeping the hunt in the player's row — see
// fixtures/oracle/beast.scn for why that is what makes a creature comparable.
func TestOracleParityBeastScenario(t *testing.T) {
	if err := oracleAdapterRun(t, "beast.scn", "beast.capture.txt", nil); err != nil {
		t.Fatalf("oracle divergence: %v", err)
	}
}

// TestOracleParityFireScenario covers bullets by source and stars: an enemy
// stream that damages the player, a player bullet that annihilates one and kills
// a bear for score, both perpendicular ricochet branches, and a star tiger whose
// stars attack a breakable and then the player. Its Blank Bay act, added by
// M16.5a, covers the point-blank half of the same ownership rule: a player
// killing an adjacent monster in two axes, an enemy point-blanking the player,
// and a spinning gun in the player's own row refusing its zero-delta shot at
// itself instead of destroying itself (fixtures/oracle/fire.scn documents why
// each shooter can be held still).
func TestOracleParityFireScenario(t *testing.T) {
	if err := oracleAdapterRun(t, "fire.scn", "fire.capture.txt", nil); err != nil {
		t.Fatalf("oracle divergence: %v", err)
	}
}

// TestOracleParityOozeScenario covers the shark (swims only through water,
// attacks only the player, refuses a dry seek) and the slime (both spread
// branches, the free touch, and both ways it can die).
func TestOracleParityOozeScenario(t *testing.T) {
	if err := oracleAdapterRun(t, "ooze.scn", "ooze.capture.txt", nil); err != nil {
		t.Fatalf("oracle divergence: %v", err)
	}
}

// TestOracleParityPedeScenario covers the centipede as a whole animal: the
// head's follower adoption, its reversal against a dead end, its promote-then-
// attack bite, and the segment countdown that turns an orphaned segment into a
// new head after the train is cut in half.
func TestOracleParityPedeScenario(t *testing.T) {
	if err := oracleAdapterRun(t, "pede.scn", "pede.capture.txt", nil); err != nil {
		t.Fatalf("oracle divergence: %v", err)
	}
}

// TestOracleParityHuntScenario covers the energizer inversion — fleeing seekers,
// both BoardAttack score branches, and the CurrentTick-driven player flash that
// M16.3 had to exclude before the phase solver existed — plus a duplicator whose
// source is a creature, so the copies hunt with the original's stat.
func TestOracleParityHuntScenario(t *testing.T) {
	if err := oracleAdapterRun(t, "hunt.scn", "hunt.capture.txt", nil); err != nil {
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

// The M16.6 sweep scenarios: ZZT-OOP, scrolls, sound, and modals.

// TestOracleParityTalkScenario covers the talking half of ZZT-OOP — labels and
// #send, the three commands that rewrite another object's program (#zap,
// #restore, #bind), #lock/#unlock, #restart, the 33-instruction budget, #play,
// one-line messages vs. multi-line windows, an unknown command's ERR, and the
// hyperlink selection that re-enters the program at a chosen label
// (fixtures/oracle/talk.scn documents the design, including why every modal
// window lives on a board with nothing else running).
func TestOracleParityTalkScenario(t *testing.T) {
	if err := oracleAdapterRun(t, "talk.scn", "talk.capture.txt", nil); err != nil {
		t.Fatalf("oracle divergence: %v", err)
	}
}

// TestOracleParityWalkScenario covers every ZZT-OOP direction word and the
// commands that consume one: the eight compass names and their aliases, CW/CCW
// /OPP as rotations of a name, IDLE as the object's own square, FLOW read back
// out of a #walk, a SEEK forced onto one axis by pulling its lever from the
// seeker's own row, and #go/#try//dir/?dir told apart by what each does with a
// refused move. The four random directions are pinned by SET rather than by
// draw — see fixtures/oracle/walk.scn for the chamber design and why the draw
// itself cannot cross this seam.
func TestOracleParityWalkScenario(t *testing.T) {
	if err := oracleAdapterRun(t, "walk.scn", "walk.capture.txt", nil); err != nil {
		t.Fatalf("oracle divergence: %v", err)
	}
}

// TestOracleParityCondScenario covers what ZZT-OOP can ask and what it can
// change: #set/#clear and the flag condition around them, #if with NOT,
// ALLIGNED, CONTACT and ANY answered from both sides, ENERGIZED read after the
// energizer wore off (so no checkpoint sits inside the flash), THEN skipped
// before a label, and #give/#take over all six counters including a refusal
// falling through to the rest of its line and the TIME counter spending a
// timed board's remaining minute.
func TestOracleParityCondScenario(t *testing.T) {
	if err := oracleAdapterRun(t, "cond.scn", "cond.capture.txt", nil); err != nil {
		t.Fatalf("oracle divergence: %v", err)
	}
}

// TestOracleParityMorfScenario covers the ZZT-OOP commands that rewrite the
// board and fire from it: #become and #die replacing the object's own square,
// #char refusing 0 and 256, #put writing a neighbour (and erroring on a zero
// direction), #change rewriting every matching tile at once, #shoot, a #play
// carrying an octave shift, a rest and a drum, #cycle retiming a stat onto the
// cycle gate, and #throwstar down the player's own row so the star's seek is
// RNG-free. Phase-solved: `#cycle 4` makes the walker's position ride
// CurrentTick, which vanilla picks with Random(100).
func TestOracleParityMorfScenario(t *testing.T) {
	if err := oracleAdapterRun(t, "morf.scn", "morf.capture.txt", nil); err != nil {
		t.Fatalf("oracle divergence: %v", err)
	}
}
