// Command zzt-shot renders a ZZT board to a pixel-faithful CP437 PNG.
//
// Usage:
//
//	go run ./cmd/zzt-shot -world /path/to/WORLD.ZZT -out title.png
//
// The default board is 0 (the title screen). Output is the 60x25 playfield;
// it intentionally omits the client-owned sidebar.
package main

import (
	"bytes"
	_ "embed"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/benhoyt/zztgo"
)

const (
	cellWidth  = 8
	cellHeight = 14
	glyphCols  = 32
)

//go:embed pc_ega.png
var pcEGA []byte

// ega is the browser client's DOS/EGA palette. Keep this byte-for-byte in
// colour values with web/src/main.ts so offline title captures match canvas.
var ega = [16]color.RGBA{
	{0x00, 0x00, 0x00, 0xff}, {0x00, 0x00, 0xaa, 0xff},
	{0x00, 0xaa, 0x00, 0xff}, {0x00, 0xaa, 0xaa, 0xff},
	{0xaa, 0x00, 0x00, 0xff}, {0xaa, 0x00, 0xaa, 0xff},
	{0xaa, 0x55, 0x00, 0xff}, {0xaa, 0xaa, 0xaa, 0xff},
	{0x55, 0x55, 0x55, 0xff}, {0x55, 0x55, 0xff, 0xff},
	{0x55, 0xff, 0x55, 0xff}, {0x55, 0xff, 0xff, 0xff},
	{0xff, 0x55, 0x55, 0xff}, {0xff, 0x55, 0xff, 0xff},
	{0xff, 0xff, 0x55, 0xff}, {0xff, 0xff, 0xff, 0xff},
}

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
	img, err := renderBoard(e)
	if err != nil {
		return err
	}
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
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

func renderBoard(e *zztgo.Engine) (*image.RGBA, error) {
	font, err := png.Decode(bytes.NewReader(pcEGA))
	if err != nil {
		return nil, fmt.Errorf("decode embedded CP437 atlas: %w", err)
	}
	if font.Bounds().Dx() != glyphCols*cellWidth || font.Bounds().Dy() != 8*cellHeight {
		return nil, fmt.Errorf("unexpected CP437 atlas dimensions %dx%d", font.Bounds().Dx(), font.Bounds().Dy())
	}

	out := image.NewRGBA(image.Rect(0, 0, int(zztgo.BOARD_WIDTH)*cellWidth, int(zztgo.BOARD_HEIGHT)*cellHeight))
	for y := int16(1); y <= zztgo.BOARD_HEIGHT; y++ {
		for x := int16(1); x <= zztgo.BOARD_WIDTH; x++ {
			attr, ch, err := tileToColorAndChar(e, x, y)
			if err != nil {
				return nil, err
			}
			drawCell(out, font, int(x-1)*cellWidth, int(y-1)*cellHeight, attr, ch)
		}
	}
	return out, nil
}

func tileToColorAndChar(e *zztgo.Engine, x, y int16) (attr, ch byte, err error) {
	tile := e.Board.Tiles[x][y]
	if int(tile.Element) <= zztgo.MAX_ELEMENT {
		attr, ch = e.TileToColorAndChar(x, y)
		return attr, ch, nil
	}
	// Several historical editors stored all 16 text colours in the low nibble
	// of tile IDs 128..255, with the character byte in Color. Vanilla's native text range
	// only has seven foreground colours, and the engine intentionally rejects
	// these foreign element IDs. They are nevertheless unambiguous static text
	// in title art, so render their DOS foreground exactly here.
	if tile.Element >= 128 {
		return tile.Element & 0x0f, tile.Color, nil
	}
	return 0, 0, fmt.Errorf("unsupported element %d at %d,%d", tile.Element, x, y)
}

func drawCell(dst *image.RGBA, font image.Image, x, y int, attr, ch byte) {
	fg := ega[attr&0x0f]
	// Bit 7 is blink in DOS text mode. A static shot uses the underlying
	// three-bit background colour, rather than inventing a bright background.
	bg := ega[(attr>>4)&0x07]
	for py := 0; py < cellHeight; py++ {
		for px := 0; px < cellWidth; px++ {
			dst.SetRGBA(x+px, y+py, bg)
		}
	}
	gx, gy := int(ch%glyphCols)*cellWidth, int(ch/glyphCols)*cellHeight
	for py := 0; py < cellHeight; py++ {
		for px := 0; px < cellWidth; px++ {
			r, g, b, _ := font.At(gx+px, gy+py).RGBA()
			// pc_ega.png is black ink on white. The browser punches black out
			// and tints the remaining white pixels with the foreground colour.
			if r+g+b > 3*0x7fff {
				dst.SetRGBA(x+px, y+py, fg)
			}
		}
	}
}
