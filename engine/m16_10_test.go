package zztgo

// M16.10 — real-browser control, modal, and audio parity.
//
// WHAT THIS ADDS TO M16.9. M16.9 proved what the client DRAWS. This proves what
// it DOES with input, and what it does with sound. The existing
// engine/web/test/{keys,modal,title,sound,mobile_text_input}.test.mjs bundle one
// client module under Node and call its exports; that is a useful unit net and
// it is not evidence that a key works, because it cannot see a handler that
// never registered, a modal that swallowed the key first, a keymask the server
// decodes differently from the client that built it, or an AudioContext that was
// never unlocked. Everything here is a real KeyboardEvent (or a real
// composition, or a real socket drop) delivered to the built application, with
// every assertion made on the decoded canvas or on the server's own view.
//
// The world is fixtures/control.zwd rather than GOLDEN: the sweeps M16.9 needs
// are scenery, and what this task needs instead is one row of things to walk
// into, shoot, read, and listen to. It is hosted on the same harness M16.9 built
// (m169NewHarnessFor), so the server objects, the tick lock, and the artifact
// handling are shared rather than reimplemented.

import (
	"os"
	"path/filepath"
	"testing"
)

const m1610World = "CONTROL"

// m1610ControlWorld compiles fixtures/control.zwd. Unlike M16.9's world it is
// authored end to end — nothing is painted in by the harness, so what a reader
// of the .zwd sees is exactly what the browser drives.
func m1610ControlWorld(t *testing.T) TWorld {
	t.Helper()

	src, err := os.ReadFile(filepath.Join("..", "fixtures", "control.zwd"))
	if err != nil {
		t.Fatalf("read fixtures/control.zwd: %v", err)
	}
	world, err := CompileZWDWorld(string(src))
	if err != nil {
		t.Fatalf("CompileZWDWorld(fixtures/control.zwd): %v", err)
	}

	// The scripts address boards and tiles by name and coordinate. If the
	// fixture is reshaped, fail here with the reason rather than fifty tiles
	// later with "timed out waiting for the lecture scroll".
	e := NewEngine()
	e.Headless = true
	e.World = world
	e.BoardOpen(m169StartBoard)
	if e.Board.Name != "Control Field" {
		t.Fatalf("board %d is %q, want \"Control Field\" — fixtures/control.zwd changed shape", m169StartBoard, e.Board.Name)
	}
	if got := e.Board.Stats[0]; got.X != 6 || got.Y != 12 {
		t.Fatalf("player starts at %d,%d, want 6,12 — fixtures/control.zwd changed shape", got.X, got.Y)
	}
	e.BoardClose()
	return world
}

func m1610NewHarness(t *testing.T) *m169Harness {
	t.Helper()
	return m169NewHarnessFor(t, m1610World, m1610ControlWorld(t))
}

// TestM1610BrowserControlVocabulary drives the whole play-mode key vocabulary
// through real KeyboardEvents on the built client: arrows and the numeric
// keypad, the removed WASD bindings, Shift+direction and Space shooting,
// torch/pause/sound/help/debug/save/quit, text-window navigation and a scroll
// link reply, and modal freeze. It closes on the DoD's snapshot/diff clause: the
// board region rendered from accumulated diffs must equal the same board
// rebuilt from a full snapshot after a passage round trip.
func TestM1610BrowserControlVocabulary(t *testing.T) {
	h := m1610NewHarness(t)
	out := h.runBrowserScript("control_keys.test.mjs")
	t.Logf("browser control vocabulary:\n%s", out)
}

// TestM1610BrowserAudioParity replaces window.AudioContext with an observable
// mock BEFORE the client loads, then makes the world produce sounds and asserts
// on the automation events the real ZztSound scheduled: that #play parsed into
// the right notes, that priority arbitration dropped what vanilla drops, that
// -1 appends, and that the 'B' toggle silences the synth.
func TestM1610BrowserAudioParity(t *testing.T) {
	h := m1610NewHarness(t)
	out := h.runBrowserScript("audio_parity.test.mjs")
	t.Logf("browser audio parity:\n%s", out)
}

// TestM1610BrowserFocusAndProtocol covers the input surfaces that are not keys
// on the board: chat capture isolation (typing must never become movement), IME
// composition commit and delete through the real hidden text control, and a
// dropped WebSocket resuming in place.
func TestM1610BrowserFocusAndProtocol(t *testing.T) {
	h := m1610NewHarness(t)
	out := h.runBrowserScript("focus_input.test.mjs")
	t.Logf("browser focus/protocol parity:\n%s", out)
}
