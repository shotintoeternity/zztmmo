package zztgo

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// fixtures/charset.zwd is a walkable glyph sheet for rendering tests: every
// CP437 code 0x00..0xFF as a Text-White tile (whose color byte IS the
// character), 64 per board across boards 1..4, joined east-west. This test
// keeps the sheet honest: all 256 codes present, each next to floor the player
// can stand on, and the committed CHARSET.ZZT built from the source it claims.
//
// Regenerate the binary after editing the source with:
//
//	ZZT_CHARSET_WRITE=1 go test -run TestCharsetFixture .
func TestCharsetFixture(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "fixtures", "charset.zwd"))
	if err != nil {
		t.Fatal(err)
	}
	zztBytes, err := CompileZWD(string(src))
	if err != nil {
		t.Fatalf("CompileZWD(fixtures/charset.zwd): %v", err)
	}
	binPath := filepath.Join("..", "fixtures", "CHARSET.ZZT")
	if os.Getenv("ZZT_CHARSET_WRITE") == "1" {
		if err := os.WriteFile(binPath, zztBytes, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	committed, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("read %s: %v (regenerate with ZZT_CHARSET_WRITE=1)", binPath, err)
	}
	if !bytes.Equal(committed, zztBytes) {
		t.Fatalf("%s is stale against fixtures/charset.zwd; regenerate with ZZT_CHARSET_WRITE=1", binPath)
	}

	world, err := CompileZWDWorld(string(src))
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine()
	e.Headless = true
	e.World = world

	seen := make(map[byte]bool)
	for board := int16(1); board <= 4; board++ {
		e.BoardOpen(board)
		for x := int16(1); x <= BOARD_WIDTH; x++ {
			for y := int16(1); y <= BOARD_HEIGHT; y++ {
				tile := e.Board.Tiles[x][y]
				if tile.Element != E_TEXT_WHITE {
					continue
				}
				if seen[tile.Color] {
					t.Errorf("glyph 0x%02X appears twice (board %d at %d,%d)", tile.Color, board, x, y)
				}
				seen[tile.Color] = true
				if e.Board.Tiles[x][y+1].Element != E_EMPTY {
					t.Errorf("glyph 0x%02X on board %d at %d,%d has no floor below it to stand on", tile.Color, board, x, y)
				}
			}
		}
	}
	if len(seen) != 256 {
		t.Fatalf("charset boards hold %d distinct glyphs; want all 256", len(seen))
	}

	// It must also play: start on the first sheet and tick.
	e.BoardChange(1)
	for i := 0; i < 50; i++ {
		e.GameStepWithInputs(nil)
	}
}
