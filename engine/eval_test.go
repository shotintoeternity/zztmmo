package zztgo

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// evalTestGrid renders a 25-row grid of 60 '.' cells with the given
// (x, y) → glyph overrides (1-based board coordinates).
func evalTestGrid(cells map[[2]int]byte) string {
	rows := make([][]byte, BOARD_HEIGHT)
	for y := range rows {
		rows[y] = []byte(strings.Repeat(".", int(BOARD_WIDTH)))
	}
	for pos, ch := range cells {
		rows[pos[1]-1][pos[0]-1] = ch
	}
	var b strings.Builder
	for _, row := range rows {
		b.WriteString("  ")
		b.Write(row)
		b.WriteString("\n")
	}
	return b.String()
}

// evalTestPlaceText writes s into cells starting at (x, y).
func evalTestPlaceText(cells map[[2]int]byte, x, y int, s string) {
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' {
			cells[[2]int{x + i, y}] = s[i]
		}
	}
}

// evalTestTextLegend emits one Text-White legend line per distinct non-space
// glyph in the given strings, keyed by the glyph itself.
func evalTestTextLegend(texts ...string) string {
	seen := map[byte]bool{}
	var b strings.Builder
	for _, s := range texts {
		for i := 0; i < len(s); i++ {
			ch := s[i]
			if ch == ' ' || seen[ch] {
				continue
			}
			seen[ch] = true
			fmt.Fprintf(&b, "    %c = Text-White color 0x%02X\n", ch, ch)
		}
	}
	return b.String()
}

type evalTestWorld struct {
	titleCells  map[[2]int]byte // extra glyphs on the title grid
	titleLegend string          // extra title legend lines
	titleStats  string          // optional title stats block content
	cavernExits string          // board 1 exits line ("" = all none)
	cavernOOP   string          // OOP body of board 1's object
	extraBoards string          // extra board sections appended verbatim
}

// build assembles a compilable two-board test world named GEMCAVE: a title
// board and a "Cavern" gameplay board holding one touchable object.
func (w evalTestWorld) build() string {
	titleCells := map[[2]int]byte{{30, 23}: '@'}
	for k, v := range w.titleCells {
		titleCells[k] = v
	}
	cavernCells := map[[2]int]byte{{30, 12}: '@', {32, 12}: 'o'}
	exits := w.cavernExits
	if exits == "" {
		exits = "exits north none south none west none east none"
	}
	oop := w.cavernOOP
	if oop == "" {
		oop = "@exit\n    #end\n    :touch\n    You made it out!\n    #endgame\n    #end"
	}
	stats := ""
	if w.titleStats != "" {
		stats = "\n  stats\n" + w.titleStats + "\n  end\n"
	}
	return fmt.Sprintf(`zwd 1
world "GEMCAVE"

board "Title Screen"
  max-shots 0
  dark false
  reenter false
  time-limit 0
  exits north none south none west none east none

  start player at 30,23
  grid
%s  end

  legend
    . = Empty color 0x00
    @ = Player color 0x1F
%s  end
%send

board "Cavern"
  max-shots 10
  dark false
  reenter false
  time-limit 0
  %s

  start player at 30,12
  grid
%s  end

  legend
    . = Empty color 0x00
    @ = Player color 0x1F
    o = Object color 0x0E
  end

  stats
    stat at 32,12 element Object cycle 3 p1 cp437:0x02 step idle under Empty color 0x00
    oop
    %s
    end
  end
end

%s`, evalTestGrid(titleCells), w.titleLegend, stats, exits, evalTestGrid(cavernCells), oop, w.extraBoards)
}

// evalGoodWorld is the baseline that satisfies every tier-1 check: a clean
// GEMCAVE wordmark, an optional-free title, and a touch #endgame on board 1.
func evalGoodWorld() evalTestWorld {
	cells := map[[2]int]byte{}
	evalTestPlaceText(cells, 27, 5, "GEMCAVE")
	return evalTestWorld{
		titleCells:  cells,
		titleLegend: evalTestTextLegend("GEMCAVE"),
	}
}

func TestEvalGateGoodWorldPasses(t *testing.T) {
	report := EvalGeneratedZWD(evalGoodWorld().build(), "GEMCAVE")
	if !report.Passed() {
		t.Fatalf("good world failed the gate:\n%s", report)
	}
}

func TestEvalGateWordmarkMissing(t *testing.T) {
	w := evalGoodWorld()
	w.titleCells = map[[2]int]byte{}
	evalTestPlaceText(w.titleCells, 27, 5, "GEMCOVE") // misspelled
	w.titleLegend = evalTestTextLegend("GEMCOVE")
	assertEvalCheckFails(t, EvalGeneratedZWD(w.build(), "GEMCAVE"), "title-wordmark", "no text row spells")
}

func TestEvalGateWordmarkDuplicated(t *testing.T) {
	w := evalGoodWorld()
	evalTestPlaceText(w.titleCells, 27, 9, "GEMCAVE")
	assertEvalCheckFails(t, EvalGeneratedZWD(w.build(), "GEMCAVE"), "title-wordmark", "duplicate wordmarks")
}

func TestEvalGateStrayTitleText(t *testing.T) {
	w := evalGoodWorld()
	evalTestPlaceText(w.titleCells, 25, 8, "A CAVE STORY")
	evalTestPlaceText(w.titleCells, 4, 20, "V") // stray letter far from the wordmark
	w.titleLegend = evalTestTextLegend("GEMCAVE", "A CAVE STORY")
	assertEvalCheckFails(t, EvalGeneratedZWD(w.build(), "GEMCAVE"), "title-wordmark", "stray text")
}

func TestEvalGateSubtitleBelowWordmarkAllowed(t *testing.T) {
	w := evalGoodWorld()
	evalTestPlaceText(w.titleCells, 25, 8, "A CAVE STORY")
	w.titleLegend = evalTestTextLegend("GEMCAVE", "A CAVE STORY")
	report := EvalGeneratedZWD(w.build(), "GEMCAVE")
	if !report.Passed() {
		t.Fatalf("one subtitle line below the wordmark must pass:\n%s", report)
	}
}

func TestEvalGateWordmarkWithSpacesInName(t *testing.T) {
	w := evalGoodWorld()
	w.titleCells = map[[2]int]byte{}
	evalTestPlaceText(w.titleCells, 25, 5, "GEM CAVE")
	w.titleLegend = evalTestTextLegend("GEM CAVE")
	report := EvalGeneratedZWD(w.build(), "GEM CAVE")
	if !report.Passed() {
		t.Fatalf("multi-word display name must match across the gap:\n%s", report)
	}
}

func TestEvalGateCreatureAndItemOnTitle(t *testing.T) {
	w := evalGoodWorld()
	w.titleCells[[2]int{10, 15}] = 'd'
	w.titleLegend += "    d = Gem color 0x0E\n"
	assertEvalCheckFails(t, EvalGeneratedZWD(w.build(), "GEMCAVE"), "title-no-creatures-or-items", "Gem at (10,15)")

	w = evalGoodWorld()
	w.titleCells[[2]int{12, 16}] = 'L'
	w.titleLegend += "    L = Lion color 0x0C\n"
	w.titleStats = "    stat at 12,16 element Lion cycle 2 p1 4 step idle under Empty color 0x00"
	assertEvalCheckFails(t, EvalGeneratedZWD(w.build(), "GEMCAVE"), "title-no-creatures-or-items", "Lion")
}

func TestEvalGateEndgameMissing(t *testing.T) {
	w := evalGoodWorld()
	w.cavernOOP = "@exit\n    #end\n    :touch\n    Nothing happens.\n    #end"
	assertEvalCheckFails(t, EvalGeneratedZWD(w.build(), "GEMCAVE"), "reachable-endgame", "no #endgame reachable")
}

func TestEvalGateEndgameUnreachable(t *testing.T) {
	w := evalGoodWorld()
	w.cavernOOP = "@exit\n    #end\n    :touch\n    Nothing happens.\n    #end"
	cells := map[[2]int]byte{{30, 12}: '@', {32, 12}: 'o'}
	w.extraBoards = fmt.Sprintf(`board "Far Chamber"
  max-shots 10
  dark false
  reenter false
  time-limit 0
  exits north none south none west none east none

  start player at 30,12
  grid
%s  end

  legend
    . = Empty color 0x00
    @ = Player color 0x1F
    o = Object color 0x0E
  end

  stats
    stat at 32,12 element Object cycle 3 p1 cp437:0x02 step idle under Empty color 0x00
    oop
    @finale
    #end
    :touch
    #endgame
    #end
    end
  end
end
`, evalTestGrid(cells))
	assertEvalCheckFails(t, EvalGeneratedZWD(w.build(), "GEMCAVE"), "reachable-endgame", "no #endgame reachable")

	// Linking the boards by an edge exit makes the same finale reachable.
	w.cavernExits = `exits north none south none west none east "Far Chamber"`
	report := EvalGeneratedZWD(w.build(), "GEMCAVE")
	if !report.Passed() {
		t.Fatalf("edge-linked finale must be reachable:\n%s", report)
	}
}

// The compiler rejects orphan glyphs and duplicate players at the source
// level, so those two checks are exercised on a mutated compiled world: the
// defect class they guard against arrives from assembly bugs, not from source.
func TestEvalGateChecksOnMutatedWorld(t *testing.T) {
	data, err := CompileZWD(evalGoodWorld().build())
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine()
	e.Headless = true
	e.VideoInstall()
	if err := e.worldReadFrom(strings.NewReader(string(data)), false, nil); err != nil {
		t.Fatal(err)
	}

	e.BoardOpen(1)
	e.Board.Tiles[5][5].Element = E_LION // no stat at (5,5)
	e.BoardClose()
	if check := evalNoOrphanStatTiles(e); check.Passed {
		t.Fatal("orphan Lion tile must fail no-orphan-stat-tiles")
	} else if !strings.Contains(check.Detail, "Lion at (5,5)") {
		t.Fatalf("unexpected orphan detail: %s", check.Detail)
	}

	e.BoardOpen(0)
	e.Board.Tiles[3][3].Element = E_PLAYER
	if check := evalTitleOnePlayerStart(e); check.Passed {
		t.Fatal("two player tiles on the title must fail title-one-player-start")
	}
}

// TestEvalGateFixtures is the CI regression gate over recorded generation
// outputs committed under fixtures/gen. Sibling files per fixture:
//   - NAME.title.txt — the display name the title wordmark must spell (the
//     plan's world name, not the sanitized file name).
//   - NAME.expect.txt — one check name per line that the recording is KNOWN
//     to fail (the honest-measurement shape of M12.7). Without it the fixture
//     must pass every check. With it the failing set must match EXACTLY:
//     a new failure is a prompting/pipeline regression, and a silently fixed
//     one means the expectation file must be updated so progress is recorded.
func TestEvalGateFixtures(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "fixtures", "gen", "*.zwd"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no recorded generation fixtures under fixtures/gen — these goldens are committed and required")
	}
	for _, path := range paths {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			displayName := ""
			if b, err := os.ReadFile(strings.TrimSuffix(path, ".zwd") + ".title.txt"); err == nil {
				displayName = strings.TrimSpace(string(b))
			}
			expected := map[string]bool{}
			if b, err := os.ReadFile(strings.TrimSuffix(path, ".zwd") + ".expect.txt"); err == nil {
				for _, line := range strings.Split(string(b), "\n") {
					if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
						expected[line] = true
					}
				}
			}
			report := EvalGeneratedZWD(string(src), displayName)
			for _, c := range report.Checks {
				switch {
				case !c.Passed && !expected[c.Name]:
					t.Errorf("check %s regressed: %s", c.Name, c.Detail)
				case c.Passed && expected[c.Name]:
					t.Errorf("check %s now passes; remove it from the .expect.txt so the improvement is locked in", c.Name)
				}
			}
			if t.Failed() {
				t.Logf("full gate report:\n%s", report)
			}
		})
	}
}

func assertEvalCheckFails(t *testing.T, report EvalReport, name, wantDetail string) {
	t.Helper()
	for _, c := range report.Checks {
		if c.Name != name {
			continue
		}
		if c.Passed {
			t.Fatalf("check %s passed, want failure containing %q\n%s", name, wantDetail, report)
		}
		if !strings.Contains(c.Detail, wantDetail) {
			t.Fatalf("check %s detail %q does not contain %q", name, c.Detail, wantDetail)
		}
		return
	}
	t.Fatalf("check %s not present in report:\n%s", name, report)
}
