package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/benhoyt/zztgo"
)

func TestDrawCellUsesDOSPaletteAndCP437Atlas(t *testing.T) {
	font, err := png.Decode(bytes.NewReader(pcEGA))
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, cellWidth, cellHeight))
	// 0x9E means yellow foreground, blue background with the DOS blink bit.
	// Static captures must retain blue, not turn it into bright blue.
	drawCell(img, font, 0, 0, 0x9e, 'A')
	if got := img.RGBAAt(0, 0); got != ega[1] {
		t.Fatalf("background = %#v, want %#v", got, ega[1])
	}
	ink := 0
	for y := 0; y < cellHeight; y++ {
		for x := 0; x < cellWidth; x++ {
			if img.RGBAAt(x, y) == ega[14] {
				ink++
			}
		}
	}
	if ink == 0 {
		t.Fatal("CP437 glyph A produced no foreground pixels")
	}
}

func TestExtendedSTKTextUsesLowNibbleForeground(t *testing.T) {
	e := zztgo.NewEngine()
	e.Board.Tiles[1][1] = zztgo.TTile{Element: 0x84, Color: 'M'}
	attr, ch, err := tileToColorAndChar(e, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if attr != 4 || ch != 'M' {
		t.Fatalf("STK text = (%#02x, %q), want (0x04, 'M')", attr, ch)
	}
}

func TestTownTitleGolden(t *testing.T) {
	world := filepath.Join("..", "..", "TOWN.ZZT")
	if _, err := os.Stat(world); err != nil {
		t.Skipf("TOWN fixture unavailable: %v", err)
	}
	out := filepath.Join(t.TempDir(), "town-title.png")
	if err := shot(world, out, 0); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	got := sha256.Sum256(b)
	const want = "4746ad6f9276c665f100d5d4587e84931b3aa32b93e26106020cbb7637474276"
	if actual := fmt.Sprintf("%x", got); actual != want {
		t.Fatalf("TOWN title PNG hash = %s, want %s", actual, want)
	}
}
