package zztgo

import (
	"path/filepath"
	"testing"
)

// M35.3 — a runthrough of the 3D view in worlds nobody authored for it.
//
// M35.2 drives the vocabulary on a board built to be stood in: one walking row,
// a sign placed where the script can reach it, nothing that moves. That proves
// the keys. It cannot prove the thing a player actually does, which is to press
// 3 in a world written in 1991 for a text screen and see whether it holds up.
//
// So this walks three committed worlds. Each gets its own harness -- one server,
// one world, the tick lock intact -- because the control listener's idle check
// reads the harness's own instance, and a script playing a world the harness is
// not hosting would be stepping one world while asserting about another.
//
// What is asserted is deliberately shallow and hard to fake: the chunk loads,
// the board region goes transparent and stays that way while the player walks,
// the scene is not one flat colour, the page raises no errors, and the text
// screen comes back pixel-identical. What the world LOOKS like is not something
// a test can have an opinion about -- the shots are written out for a person to
// look at, which is the other half of this task.
// The worlds are the committed ones, and that is a real constraint rather than
// a preference: engine/*.ZZT is gitignored, so a runthrough of CAVES would be
// green on the machine that wrote it and red in CI.
//
// GEMDASH is deliberately absent. It is the board a challenge run is played on
// and 0bf54ee took it out of the picker on purpose ("not a world anybody
// published"), so a script that reached it through the picker would be testing
// a route the product does not have.
//
// The two oracle worlds are here because TOWN and ACCEPT between them have no
// creature standing up as a card and no dark room, and both are things the view
// has to do something sensible with.
// `dark` says the board discloses nothing: the server sends the dark, and the
// view is required to show it rather than to invent a lit room. A dark board
// therefore fails the "not one flat colour" floor every other world must clear,
// and asserting the opposite is the point of having one here.
var m353Worlds = []struct {
	name, dir string
	dark      bool
}{
	{"TOWN", "fixtures", false},            // the one everybody knows: signs, lakes, buildings
	{"ACCEPT", "fixtures", false},          // the acceptance world: open floor and a horizon
	{"ORCLPEDE", "fixtures/oracle", false}, // centipedes: a board whose contents move under the view
	{"ORCLDARK", "fixtures/oracle", true},  // a dark room, which discloses nothing by design
}

func TestM353BrowserView3DWorldRunthrough(t *testing.T) {
	for _, w := range m353Worlds {
		name := w.name
		t.Run(name, func(t *testing.T) {
			world, err := LoadPristineWorld(filepath.Join("..", filepath.FromSlash(w.dir)), name)
			if err != nil {
				t.Fatalf("load %s/%s.ZZT: %v", w.dir, name, err)
			}
			h := m169NewHarnessFor(t, name, world)
			env := []string{"M353_WORLD=" + name}
			if w.dark {
				env = append(env, "M353_DARK=1")
			}
			out := h.runBrowserScript("view3d_worlds.test.mjs", env...)
			t.Logf("3D runthrough of %s:\n%s", name, out)
		})
	}
}
