package zztgo

import (
	"os"
	"strings"
	"testing"
)

func m1219Grid(cells map[[2]int]byte) string {
	rows := make([][]byte, BOARD_HEIGHT)
	for y := range rows {
		rows[y] = []byte(strings.Repeat(".", int(BOARD_WIDTH)))
	}
	for pos, glyph := range cells {
		rows[pos[1]-1][pos[0]-1] = glyph
	}
	var b strings.Builder
	for _, row := range rows {
		b.WriteString("  ")
		b.Write(row)
		b.WriteByte('\n')
	}
	return b.String()
}

// The grounded Apollo run reached the compiler with a grid full of dots but no
// dot legend entry. The preprocessor treated its default empty byte as exempt,
// leaving the exact undefined key that exhausted the repair budget.
func TestM1219PreprocessAddsMissingDefaultEmptyLegend(t *testing.T) {
	section := `board "Pacific Splashdown"
  start player at 1,1
  grid
` + m1219Grid(map[[2]int]byte{{1, 1}: '@'}) + `  end
  legend
    @ = Player color 0x1F under Empty color 0x00
  end
end`

	preprocessed := preprocessZWDGrid(section)
	if !strings.Contains(preprocessed, "cp437:0x2E = Empty color 0x00") {
		t.Fatalf("missing default empty legend was not injected:\n%s", preprocessed)
	}
	if _, err := CompileZWDWorld("zwd 1\nworld \"APOLLO\"\n" + preprocessed); err != nil {
		t.Fatalf("preprocessed missing-dot candidate did not compile: %v\n%s", err, preprocessed)
	}
}

// The Morning Light/Lunar Liftoff candidates omitted OOP's structural end.
// Later stat declarations were then consumed as OOP text, including passages
// in column 60, and the compiler reported their grid tiles as orphaned.
func TestM1219PreprocessClosesOOPBeforeFollowingStatAtLastColumn(t *testing.T) {
	section := `board "The Fading Gardens"
  start player at 1,1
  grid
` + m1219Grid(map[[2]int]byte{
		{1, 1}:   '@',
		{10, 5}:  'o',
		{60, 11}: 'p',
		{60, 12}: 'p',
	}) + `  end
  legend
    . = Empty color 0x00
    @ = Player color 0x1F under Empty color 0x00
    o = Object color 0x0F
    p = Passage color 0x1F
  end
  stats
    stat at 10,5 element Object cycle 3
    oop
    @first
    #end
    stat at 60,11 element Passage cycle 0 p3 0
    stat at 60,12 element Passage cycle 0 p3 0
  end
end`

	preprocessed, warnings := preprocessZWDGridWithWarnings(section)
	if !strings.Contains(strings.Join(warnings, "; "), "auto-closed oop block before stat declaration") {
		t.Fatalf("warnings = %q, want OOP auto-close notice", warnings)
	}
	world, err := CompileZWDWorld("zwd 1\nworld \"GARDENS\"\n" + preprocessed)
	if err != nil {
		t.Fatalf("preprocessed orphan candidate did not compile: %v\n%s", err, preprocessed)
	}
	e := NewEngine()
	e.Headless = true
	e.World = world
	e.BoardOpen(0)
	for _, pos := range [][2]int{{60, 11}, {60, 12}} {
		found := false
		for i := int16(1); i <= e.Board.StatCount; i++ {
			stat := e.Board.Stats[i]
			if int(stat.X) == pos[0] && int(stat.Y) == pos[1] {
				found = true
			}
		}
		if !found {
			t.Fatalf("stat at (%d,%d) was lost after preprocessing:\n%s", pos[0], pos[1], preprocessed)
		}
	}
}

// CASTLERA's approved plan promised a finale, but its accepted assembled ZWD
// had no #endgame. Cross-board validation must send that omission back to the
// finale board for repair rather than hosting an unwinnable world.
func TestM1219CrossBoardProblemsRequirePromisedReachableEndgame(t *testing.T) {
	planText, err := os.ReadFile("../llmworld/eval/baseline/worlds/CASTLERA.plan.md")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ParsePlan(string(planText))
	if err != nil {
		t.Fatal(err)
	}
	full, err := os.ReadFile("../fixtures/gen/CASTLERA.zwd")
	if err != nil {
		t.Fatal(err)
	}
	problems := crossBoardProblems(plan, string(full))
	if got := strings.Join(problems["Dawn Breaks"], "; "); !strings.Contains(got, "missing reachable #endgame promised by progression spine") {
		t.Fatalf("finale omission was not routed to Dawn Breaks: %#v", problems)
	}
}
