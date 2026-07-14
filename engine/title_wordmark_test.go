package zztgo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// spliceStampedTitle stamps the given world's board-0 section in place and
// returns the reassembled full-world ZWD. Board 0 is the first board, so its
// section is a contiguous substring of the document.
func spliceStampedTitle(t *testing.T, full, board0Name, displayName string) string {
	t.Helper()
	section := boardSectionFromSource(full, board0Name)
	if section == "" {
		t.Fatalf("board %q not found in world", board0Name)
	}
	stamped, warnings := stampTitleWordmark(section, displayName)
	if stamped == section {
		t.Fatalf("stamp made no change; warnings: %v", warnings)
	}
	out := strings.Replace(full, section, stamped, 1)
	if out == full {
		t.Fatal("failed to splice stamped section back into world")
	}
	return out
}

// TestStampTitleWordmarkMakesGatePass is the measured proof of M12.20: a REAL
// recorded generation whose title-wordmark the M12.17 baseline scored 0-2 passes
// the gate once its board 0 is stamped, and no other tier-1 check regresses.
func TestStampTitleWordmarkMakesGatePass(t *testing.T) {
	name := "Castle of the Crimson Count"
	path := filepath.Join("..", "fixtures", "gen", "CASTLEOF.zwd")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("fixture unavailable: %v", err)
	}

	// Before: the recorded world fails title-wordmark (the baseline finding).
	before := EvalGeneratedZWD(string(src), name)
	if c := findCheck(before, "title-wordmark"); c == nil || c.Passed {
		t.Fatalf("expected recorded CASTLEOF to fail title-wordmark before stamping; got %+v", c)
	}

	// After: stamp board 0 and every tier-1 check passes.
	after := EvalGeneratedZWD(spliceStampedTitle(t, string(src), name, name), name)
	if !after.Passed() {
		t.Fatalf("stamped world failed the gate:\n%s", after)
	}
}

// TestStampTitleWordmarkStripsStrayText proves the stamp both spells the name on
// exactly one row and removes the model's stray title text (the block-letter
// clusters that read as garbage), leaving no other text row.
func TestStampTitleWordmarkStripsStrayText(t *testing.T) {
	name := "Castle of the Crimson Count"
	path := filepath.Join("..", "fixtures", "gen", "CASTLEOF.zwd")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("fixture unavailable: %v", err)
	}
	full := spliceStampedTitle(t, string(src), name, name)
	e := loadWorldFromZWD(t, full)
	e.BoardOpen(0)

	var textRows []int16
	var wordmarkRow int16 = -1
	for y := int16(1); y <= BOARD_HEIGHT; y++ {
		row := evalTextRow(e, y)
		if row == "" {
			continue
		}
		textRows = append(textRows, y)
		if evalNormalizeName(row) == evalNormalizeName(foldWordmark(name)) {
			wordmarkRow = y
		}
	}
	if wordmarkRow < 0 {
		t.Fatalf("no row spells the name; text rows: %v", textRows)
	}
	if len(textRows) != 1 {
		t.Fatalf("expected exactly one text row after stripping, got %d: %v", len(textRows), textRows)
	}
}

// TestStampTitleWordmarkPreservesPlayerAndObjects proves the stamp leaves the
// title's single player and its decorative Object stats intact — the strip only
// touches Text tiles, never stat-backed or player cells.
func TestStampTitleWordmarkPreservesPlayerAndObjects(t *testing.T) {
	name := "Castle of the Crimson Count"
	path := filepath.Join("..", "fixtures", "gen", "CASTLEOF.zwd")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("fixture unavailable: %v", err)
	}

	countStats := func(full string) (players, objects int) {
		e := loadWorldFromZWD(t, full)
		e.BoardOpen(0)
		for x := int16(1); x <= BOARD_WIDTH; x++ {
			for y := int16(1); y <= BOARD_HEIGHT; y++ {
				switch e.Board.Tiles[x][y].Element {
				case E_PLAYER:
					players++
				case E_OBJECT:
					objects++
				}
			}
		}
		return
	}

	p0, o0 := countStats(string(src))
	full := spliceStampedTitle(t, string(src), name, name)
	p1, o1 := countStats(full)
	if p1 != p0 || p1 != 1 {
		t.Fatalf("player count changed: before=%d after=%d (want 1)", p0, p1)
	}
	if o1 != o0 {
		t.Fatalf("object count changed: before=%d after=%d", o0, o1)
	}
}

// TestStampTitleWordmarkFoldsNonASCIIName proves an em-dash display name (the
// real APOLLO11 case) both stamps and passes the gate, because the stamp and the
// check fold the name to CP437 bytes the same way.
func TestStampTitleWordmarkFoldsNonASCIIName(t *testing.T) {
	displayName := "Apollo 11 — Sea of Tranquility"
	board0 := "Apollo Eleven" // the board's own name is not the display name
	path := filepath.Join("..", "fixtures", "gen", "APOLLO11.zwd")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("fixture unavailable: %v", err)
	}
	full := spliceStampedTitle(t, string(src), board0, displayName)
	report := EvalGeneratedZWD(full, displayName)
	if c := findCheck(report, "title-wordmark"); c == nil || !c.Passed {
		t.Fatalf("em-dash name must pass title-wordmark after stamp; got %+v\n%s", c, report)
	}
}

// TestStampTitleWordmarkStructuralSurprisesAreSafe proves the stamp never
// corrupts input it does not understand: an empty name, a name too wide for the
// board, and a section with no grid all return the input unchanged.
func TestStampTitleWordmarkStructuralSurprisesAreSafe(t *testing.T) {
	section := "board \"Nowhere\"\n  grid\n" + strings.Repeat("  "+strings.Repeat(".", 60)+"\n", 25) +
		"  end\n  legend\n    . = Empty color 0x00\n    @ = Player color 0x1F\n  end\nend"

	if got, _ := stampTitleWordmark(section, ""); got != section {
		t.Error("empty display name must leave the section unchanged")
	}
	if got, _ := stampTitleWordmark(section, strings.Repeat("W", 61)); got != section {
		t.Error("over-wide wordmark must leave the section unchanged")
	}
	noGrid := "board \"Nowhere\"\n  legend\n    . = Empty color 0x00\n  end\nend"
	if got, _ := stampTitleWordmark(noGrid, "Hello"); got != noGrid {
		t.Error("gridless section must be left unchanged")
	}
}

func findCheck(r EvalReport, name string) *EvalCheck {
	for i := range r.Checks {
		if r.Checks[i].Name == name {
			return &r.Checks[i]
		}
	}
	return nil
}

func loadWorldFromZWD(t *testing.T, src string) *Engine {
	t.Helper()
	data, err := CompileZWD(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	e := NewEngine()
	e.Headless = true
	e.VideoInstall()
	if err := e.worldReadFrom(strings.NewReader(string(data)), false, nil); err != nil {
		t.Fatalf("load: %v", err)
	}
	return e
}
