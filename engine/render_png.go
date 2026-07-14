// Pixel-faithful CP437 board rendering (M12.15 phase 5 / M12.17).
//
// This is the Screen→PNG half of the caption/eval pipeline: it renders the
// current board of an Engine to an image using the embedded CP437 atlas and
// the browser client's DOS/EGA palette, via the engine's own
// TileToColorAndChar. It moved here from cmd/zzt-shot so cmd/zzt-eval can use
// the same renderer; it is presentation-only and never touches simulation
// state.

package zztgo

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
)

const (
	renderCellWidth  = 8
	renderCellHeight = 14
	renderGlyphCols  = 32
)

//go:embed pc_ega.png
var renderPCEGA []byte

// renderEGA is the browser client's DOS/EGA palette. Keep this byte-for-byte in
// colour values with web/src/main.ts so offline captures match canvas.
var renderEGA = [16]color.RGBA{
	{0x00, 0x00, 0x00, 0xff}, {0x00, 0x00, 0xaa, 0xff},
	{0x00, 0xaa, 0x00, 0xff}, {0x00, 0xaa, 0xaa, 0xff},
	{0xaa, 0x00, 0x00, 0xff}, {0xaa, 0x00, 0xaa, 0xff},
	{0xaa, 0x55, 0x00, 0xff}, {0xaa, 0xaa, 0xaa, 0xff},
	{0x55, 0x55, 0x55, 0xff}, {0x55, 0x55, 0xff, 0xff},
	{0x55, 0xff, 0x55, 0xff}, {0x55, 0xff, 0xff, 0xff},
	{0xff, 0x55, 0x55, 0xff}, {0xff, 0x55, 0xff, 0xff},
	{0xff, 0xff, 0x55, 0xff}, {0xff, 0xff, 0xff, 0xff},
}

// RenderBoardImage renders the engine's currently open board (the 60x25
// playfield, no sidebar) to a pixel-faithful CP437 image.
func RenderBoardImage(e *Engine) (*image.RGBA, error) {
	font, err := png.Decode(bytes.NewReader(renderPCEGA))
	if err != nil {
		return nil, fmt.Errorf("decode embedded CP437 atlas: %w", err)
	}
	if font.Bounds().Dx() != renderGlyphCols*renderCellWidth || font.Bounds().Dy() != 8*renderCellHeight {
		return nil, fmt.Errorf("unexpected CP437 atlas dimensions %dx%d", font.Bounds().Dx(), font.Bounds().Dy())
	}

	out := image.NewRGBA(image.Rect(0, 0, int(BOARD_WIDTH)*renderCellWidth, int(BOARD_HEIGHT)*renderCellHeight))
	for y := int16(1); y <= BOARD_HEIGHT; y++ {
		for x := int16(1); x <= BOARD_WIDTH; x++ {
			attr, ch, err := renderTileToColorAndChar(e, x, y)
			if err != nil {
				return nil, err
			}
			renderDrawCell(out, font, int(x-1)*renderCellWidth, int(y-1)*renderCellHeight, attr, ch)
		}
	}
	return out, nil
}

// WriteBoardPNG renders the engine's currently open board as PNG bytes.
func WriteBoardPNG(e *Engine, w io.Writer) error {
	img, err := RenderBoardImage(e)
	if err != nil {
		return err
	}
	return png.Encode(w, img)
}

// RenderZWDBoardPNG compiles ZWD source and renders one board to PNG bytes,
// for cmd/zzt-eval's screenshot captures.
func RenderZWDBoardPNG(src string, board int16) ([]byte, error) {
	data, err := CompileZWD(src)
	if err != nil {
		return nil, err
	}
	e := NewEngine()
	e.Headless = true
	e.VideoInstall()
	if err := e.worldReadFrom(strings.NewReader(string(data)), false, nil); err != nil {
		return nil, err
	}
	if board < 0 || board > e.World.BoardCount {
		return nil, fmt.Errorf("board %d out of range 0..%d", board, e.World.BoardCount)
	}
	e.BoardOpen(board)
	var buf bytes.Buffer
	if err := WriteBoardPNG(e, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// renderTileToColorAndChar resolves one tile for a static capture. It is the
// engine's TileToColorAndChar plus one capture-only compatibility case for
// foreign STK-style text elements.
func renderTileToColorAndChar(e *Engine, x, y int16) (attr, ch byte, err error) {
	tile := e.Board.Tiles[x][y]
	if int(tile.Element) <= MAX_ELEMENT {
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

// renderDrawCell paints one text cell into dst at pixel offset (x, y).
func renderDrawCell(dst *image.RGBA, font image.Image, x, y int, attr, ch byte) {
	fg := renderEGA[attr&0x0f]
	// Bit 7 is blink in DOS text mode. A static shot uses the underlying
	// three-bit background colour, rather than inventing a bright background.
	bg := renderEGA[(attr>>4)&0x07]
	for py := 0; py < renderCellHeight; py++ {
		for px := 0; px < renderCellWidth; px++ {
			dst.SetRGBA(x+px, y+py, bg)
		}
	}
	gx, gy := int(ch%renderGlyphCols)*renderCellWidth, int(ch/renderGlyphCols)*renderCellHeight
	for py := 0; py < renderCellHeight; py++ {
		for px := 0; px < renderCellWidth; px++ {
			r, g, b, _ := font.At(gx+px, gy+py).RGBA()
			// pc_ega.png is black ink on white. The browser punches black out
			// and tints the remaining white pixels with the foreground colour.
			if r+g+b > 3*0x7fff {
				dst.SetRGBA(x+px, y+py, fg)
			}
		}
	}
}
