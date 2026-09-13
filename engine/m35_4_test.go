package zztgo

import "testing"

// M35.4 — a board change, seen from inside the board.
//
// A passage is the one moment the 3D view has to survive the whole model being
// replaced underneath it: new cells, a new player square, a fresh full snapshot,
// and M9.1's fill-then-reveal fade playing over the top of all of it. The text
// screen has drawn that fade since M9.1 and it is fine there. This asks what it
// looks like from eye level.
func TestM354BrowserView3DPassage(t *testing.T) {
	h := m352NewHarness(t)
	out := h.runBrowserScript("view3d_passage.test.mjs")
	t.Logf("3D board change:\n%s", out)
}
