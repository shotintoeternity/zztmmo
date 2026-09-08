package zztgo

import "testing"

// M35.1 — the 3D view lives in the regular client.
//
// The 3D client was a separate app with a separate socket and no world picker,
// no chat and no profile; it is now a second painter for the board half of the
// text screen this client already draws. What has to be true is small and easy
// to get wrong in a way that still looks plausible: pressing V must turn the
// board columns into a window onto a scene, and pressing it again must put the
// text screen back, with the sidebar drawn by drawScreen throughout.
//
// The script asserts that three ways at once -- the 2D canvas's board columns
// going transparent while the sidebar keeps its pixels, the lazily-imported
// three.js chunk actually arriving, and the rendered page changing -- because
// each of them alone is satisfiable by a client that draws nothing. See
// web/test/view3d.test.mjs.
func TestM351BrowserView3DTogglesInTheRegularClient(t *testing.T) {
	h := m1610NewHarness(t)
	out := h.runBrowserScript("view3d.test.mjs")
	t.Logf("3D view in the regular client:\n%s", out)
}
