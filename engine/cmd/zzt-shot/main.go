// Command zzt-shot renders a ZZT board to a pixel-faithful CP437 PNG.
//
// Usage:
//
//	go run ./cmd/zzt-shot -world /path/to/WORLD.ZZT -out title.png
//
// The default board is 0 (the title screen). Output is the 60x25 playfield;
// it intentionally omits the client-owned sidebar. Rendering itself lives in
// the engine package (RenderBoardImage) so cmd/zzt-eval shares it.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/benhoyt/zztgo"
)

func main() {
	worldPath := flag.String("world", "", "path to a .ZZT world")
	outPath := flag.String("out", "", "output PNG path")
	board := flag.Int("board", 0, "board number to render (0 is title screen)")
	flag.Parse()
	if *worldPath == "" || *outPath == "" {
		fmt.Fprintln(os.Stderr, "usage: zzt-shot -world WORLD.ZZT -out title.png [-board 0]")
		os.Exit(2)
	}
	if err := shot(*worldPath, *outPath, int16(*board)); err != nil {
		fmt.Fprintf(os.Stderr, "zzt-shot: %v\n", err)
		os.Exit(1)
	}
}

func shot(worldPath, outPath string, board int16) error {
	if !strings.EqualFold(filepath.Ext(worldPath), ".zzt") {
		return fmt.Errorf("world %q does not have a .ZZT extension", worldPath)
	}
	e := zztgo.NewEngine()
	e.Headless = true
	e.VideoInstall()
	// WorldLoad assumes normal game startup has initialized the shared element
	// definitions. WorldCreate supplies that setup without affecting the file
	// subsequently read from disk.
	e.WorldCreate()
	base := strings.TrimSuffix(worldPath, filepath.Ext(worldPath))
	if !e.WorldLoad(base, filepath.Ext(worldPath), false) {
		return fmt.Errorf("load %s", worldPath)
	}
	e.BoardOpen(board)
	if boardHasZeroRun(e, board) {
		// Vanilla ZZT encodes an RLE count of zero as 256 cells. The runtime
		// loader predates support for that historical encoding, so reconstruct
		// this static board snapshot locally before using its ordinary tile
		// renderer. This is deliberately a capture-only compatibility shim: it
		// does not change simulation or world data.
		if err := openBoardWithVanillaRLE(e, board); err != nil {
			return err
		}
	}
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	return zztgo.WriteBoardPNG(e, f)
}

func boardHasZeroRun(e *zztgo.Engine, board int16) bool {
	if board < 0 || int(board) >= len(e.World.BoardData) {
		return false
	}
	data := e.World.BoardData[board]
	if len(data) < zztgo.SizeOfBoardName {
		return false
	}
	pos, cells := zztgo.SizeOfBoardName, 0
	for cells < int(zztgo.BOARD_WIDTH*zztgo.BOARD_HEIGHT) && pos+zztgo.SizeOfRleTile <= len(data) {
		count := int(data[pos])
		if count == 0 {
			return true
		}
		cells += count
		pos += zztgo.SizeOfRleTile
	}
	return false
}

// openBoardWithVanillaRLE is BoardOpen's board-data decode with one historical
// compatibility detail: a zero RLE count represents 256 tiles in a .ZZT file.
func openBoardWithVanillaRLE(e *zztgo.Engine, board int16) error {
	if board < 0 || board > e.World.BoardCount {
		return fmt.Errorf("board %d out of range", board)
	}
	ptr := e.World.BoardData[board]
	if len(ptr) < zztgo.SizeOfBoardName {
		return fmt.Errorf("board %d is truncated", board)
	}
	e.Board.Name = zztgo.LoadString(ptr[:zztgo.SizeOfBoardName])
	ptr = ptr[zztgo.SizeOfBoardName:]
	for y := int16(1); y <= zztgo.BOARD_HEIGHT; y++ {
		for x := int16(1); x <= zztgo.BOARD_WIDTH; {
			if len(ptr) < zztgo.SizeOfRleTile {
				return fmt.Errorf("board %d has truncated tile RLE", board)
			}
			count := int(ptr[0])
			if count == 0 {
				count = 256
			}
			tile := zztgo.LoadRleTile(ptr[:zztgo.SizeOfRleTile]).Tile
			ptr = ptr[zztgo.SizeOfRleTile:]
			for ; count > 0; count-- {
				e.Board.Tiles[x][y] = tile
				x++
				if x > zztgo.BOARD_WIDTH {
					x = 1
					y++
					if y > zztgo.BOARD_HEIGHT {
						break
					}
				}
			}
			if y > zztgo.BOARD_HEIGHT {
				break
			}
		}
	}
	if len(ptr) < zztgo.SizeOfBoardInfo+2 {
		return fmt.Errorf("board %d has truncated metadata", board)
	}
	zztgo.LoadBoardInfo(ptr[:zztgo.SizeOfBoardInfo], &e.Board.Info)
	ptr = ptr[zztgo.SizeOfBoardInfo:]
	e.Board.StatCount = zztgo.LoadInt16(ptr[:2])
	ptr = ptr[2:]
	if e.Board.StatCount < 0 || int(e.Board.StatCount) >= len(e.Board.Stats) {
		return fmt.Errorf("board %d has invalid stat count %d", board, e.Board.StatCount)
	}
	for i := int16(0); i <= e.Board.StatCount; i++ {
		if len(ptr) < zztgo.SizeOfStat {
			return fmt.Errorf("board %d has truncated stat %d", board, i)
		}
		stat := &e.Board.Stats[i]
		zztgo.LoadStat(ptr[:zztgo.SizeOfStat], stat)
		ptr = ptr[zztgo.SizeOfStat:]
		if stat.DataLen > 0 {
			if int(stat.DataLen) > len(ptr) {
				return fmt.Errorf("board %d has truncated stat data", board)
			}
			stat.Data = string(ptr[:stat.DataLen])
			ptr = ptr[stat.DataLen:]
		}
	}
	return nil
}
