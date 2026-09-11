package zztgo

import (
	"os"
	"path/filepath"
	"testing"
)

// M35.2 — the 3D view's whole feature vocabulary, in the regular client.
//
// M35.1 proves the toggle: V turns the board columns into a window and V again
// puts the text screen back. That is the smallest true thing, and it is not the
// feature. What a player actually meets in there is a vocabulary — which keys
// walk, which keys only move the camera, which keys must never reach the wire,
// what the sidebar promises while they are standing up, and what the view says
// that the text screen cannot say at all.
//
// The world is fixtures/view3d.zwd, authored for this: one walking row with a
// sign, a forest, a fake wall, a lake and the normal wall the fake imitates.
// Nothing on it moves, so a tick changes nothing and every assertion is about
// the view rather than about the game.
const m352World = "VIEW3D"

func m352ViewWorld(t *testing.T) TWorld {
	t.Helper()

	src, err := os.ReadFile(filepath.Join("..", "fixtures", "view3d.zwd"))
	if err != nil {
		t.Fatalf("read fixtures/view3d.zwd: %v", err)
	}
	world, err := CompileZWDWorld(string(src))
	if err != nil {
		t.Fatalf("CompileZWDWorld(fixtures/view3d.zwd): %v", err)
	}

	// The script walks by coordinate and reads a sign by name. If the fixture is
	// reshaped, fail here with the reason rather than forty tiles later with
	// "timed out waiting for the sign readout".
	e := NewEngine()
	e.Headless = true
	e.World = world
	e.BoardOpen(m169StartBoard)
	if e.Board.Name != "View Field" {
		t.Fatalf("board %d is %q, want \"View Field\" — fixtures/view3d.zwd changed shape", m169StartBoard, e.Board.Name)
	}
	if got := e.Board.Stats[0]; got.X != 6 || got.Y != 12 {
		t.Fatalf("player starts at %d,%d, want 6,12 — fixtures/view3d.zwd changed shape", got.X, got.Y)
	}
	// The two tiles the whole `element` disclosure exists for: a fake wall and
	// the normal wall it imitates draw with the SAME glyph and the same colour,
	// so a script that meets them cannot tell them apart by looking. Assert the
	// board really holds one of each before the browser is asked to.
	if got := e.Board.Tiles[18][12].Element; got != E_FAKE {
		t.Fatalf("tile 18,12 is element %d, want E_FAKE (%d)", got, E_FAKE)
	}
	if got := e.Board.Tiles[26][12].Element; got != E_NORMAL {
		t.Fatalf("tile 26,12 is element %d, want E_NORMAL (%d)", got, E_NORMAL)
	}
	if got := e.Board.Tiles[20][13].Element; got != E_WATER {
		t.Fatalf("tile 20,13 is element %d, want E_WATER (%d)", got, E_WATER)
	}
	e.BoardClose()
	return world
}

func m352NewHarness(t *testing.T) *m169Harness {
	t.Helper()
	return m169NewHarnessFor(t, m352World, m352ViewWorld(t))
}

// TestM352BrowserView3DFeatures drives the 3D view's vocabulary through real
// KeyboardEvents on the built client: the two toggles, the arrows that walk in
// every view, the four camera keys that must never reach the wire, F, G, the
// sidebar rows that advertise all of it, and the sign that reads itself out at
// eye level.
func TestM352BrowserView3DFeatures(t *testing.T) {
	h := m352NewHarness(t)
	out := h.runBrowserScript("view3d_features.test.mjs")
	t.Logf("3D feature vocabulary:\n%s", out)
}
