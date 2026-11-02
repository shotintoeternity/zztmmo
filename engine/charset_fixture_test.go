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
// keeps the sheet honest: all 256 codes present once each; every board, and
// floor beside every glyph, reachable on foot from board 1's player start
// (flood fill across the east-west board exits); the player still on its start
// after idle ticks; and the committed CHARSET.ZZT built from the source it
// claims.
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

	// Snapshot boards 1..4: their tiles and their edge exits.
	const sheets = 4
	type sheet struct {
		tiles     [BOARD_WIDTH + 2][BOARD_HEIGHT + 2]TTile
		neighbors [4]byte // north, south, west, east; 0 = none
	}
	boards := make(map[int16]*sheet)
	for b := int16(1); b <= sheets; b++ {
		e.BoardOpen(b)
		s := &sheet{neighbors: e.Board.Info.NeighborBoards}
		for x := 0; x < BOARD_WIDTH+2; x++ {
			for y := 0; y < BOARD_HEIGHT+2; y++ {
				s.tiles[x][y] = e.Board.Tiles[x][y]
			}
		}
		boards[b] = s
	}

	// Flood fill what a player can walk to, starting at board 1's player start
	// and crossing board edges the way ZZT does: step off column 60 of a board
	// with an east exit and arrive at column 1 of that board, same row (and the
	// mirror for west), provided the arrival cell is floor.
	type cell struct{ b, x, y int16 }
	reached := make(map[cell]bool)
	e.BoardOpen(1)
	startCell := cell{1, int16(e.Board.Info.StartPlayerX), int16(e.Board.Info.StartPlayerY)}
	queue := []cell{startCell}
	reached[startCell] = true
	walkable := func(c cell) bool {
		s, ok := boards[c.b]
		if !ok || c.x < 1 || c.x > BOARD_WIDTH || c.y < 1 || c.y > BOARD_HEIGHT {
			return false
		}
		el := s.tiles[c.x][c.y].Element
		return el == E_EMPTY || el == E_PLAYER
	}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		next := []cell{{c.b, c.x + 1, c.y}, {c.b, c.x - 1, c.y}, {c.b, c.x, c.y + 1}, {c.b, c.x, c.y - 1}}
		if n := boards[c.b].neighbors[3]; c.x == BOARD_WIDTH && n != 0 {
			next = append(next, cell{int16(n), 1, c.y})
		}
		if n := boards[c.b].neighbors[2]; c.x == 1 && n != 0 {
			next = append(next, cell{int16(n), BOARD_WIDTH, c.y})
		}
		for _, n := range next {
			if !reached[n] && walkable(n) {
				reached[n] = true
				queue = append(queue, n)
			}
		}
	}

	boardReached := make(map[int16]bool)
	for c := range reached {
		boardReached[c.b] = true
	}
	for b := int16(1); b <= sheets; b++ {
		if !boardReached[b] {
			t.Errorf("board %d cannot be walked to from board 1's player start", b)
		}
	}

	seen := make(map[byte]bool)
	for b := int16(1); b <= sheets; b++ {
		s := boards[b]
		for x := int16(1); x <= BOARD_WIDTH; x++ {
			for y := int16(1); y <= BOARD_HEIGHT; y++ {
				tile := s.tiles[x][y]
				if tile.Element != E_TEXT_WHITE {
					continue
				}
				if seen[tile.Color] {
					t.Errorf("glyph 0x%02X appears twice (board %d at %d,%d)", tile.Color, b, x, y)
				}
				seen[tile.Color] = true
				if !reached[cell{b, x + 1, y}] && !reached[cell{b, x - 1, y}] &&
					!reached[cell{b, x, y + 1}] && !reached[cell{b, x, y - 1}] {
					t.Errorf("glyph 0x%02X on board %d at %d,%d has no reachable floor beside it", tile.Color, b, x, y)
				}
			}
		}
	}
	if len(seen) != 256 {
		t.Fatalf("charset boards hold %d distinct glyphs; want all 256", len(seen))
	}

	// It must also play: enter the first sheet, tick with no input, and the
	// player is still standing on its start.
	e.BoardChange(1)
	for i := 0; i < 50; i++ {
		e.GameStepWithInputs(nil)
	}
	p := e.Board.Stats[0]
	if int16(p.X) != startCell.x || int16(p.Y) != startCell.y {
		t.Errorf("after 50 idle ticks the player is at %d,%d; want the start %d,%d", p.X, p.Y, startCell.x, startCell.y)
	}
	if el := e.Board.Tiles[p.X][p.Y].Element; el != E_PLAYER {
		t.Errorf("tile under stat 0 is element %d; want the player", el)
	}
}
