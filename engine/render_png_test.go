package zztgo

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func TestRenderDrawCellUsesDOSPaletteAndCP437Atlas(t *testing.T) {
	font, err := png.Decode(bytes.NewReader(renderPCEGA))
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, renderCellWidth, renderCellHeight))
	// 0x9E means yellow foreground, blue background with the DOS blink bit.
	// Static captures must retain blue, not turn it into bright blue.
	renderDrawCell(img, font, 0, 0, 0x9e, 'A')
	if got := img.RGBAAt(0, 0); got != renderEGA[1] {
		t.Fatalf("background = %#v, want %#v", got, renderEGA[1])
	}
	ink := 0
	for y := 0; y < renderCellHeight; y++ {
		for x := 0; x < renderCellWidth; x++ {
			if img.RGBAAt(x, y) == renderEGA[14] {
				ink++
			}
		}
	}
	if ink == 0 {
		t.Fatal("CP437 glyph A produced no foreground pixels")
	}
}

func TestRenderExtendedSTKTextUsesLowNibbleForeground(t *testing.T) {
	e := NewEngine()
	e.Board.Tiles[1][1] = TTile{Element: 0x84, Color: 'M'}
	attr, ch, err := renderTileToColorAndChar(e, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if attr != 4 || ch != 'M' {
		t.Fatalf("STK text = (%#02x, %q), want (0x04, 'M')", attr, ch)
	}
}
