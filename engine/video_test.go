package zztgo

import "testing"

// TestBoardDrawTileHeadless is the M0.2 definition of done: with E.Headless set,
// a sim draw call (BoardDrawTile → VideoWriteText) lands in the E.Screen buffer
// and never touches tcell. The presenter's `screen` is nil here; if any tcell
// path were reached it would panic, so a clean pass proves the guard holds.
func TestBoardDrawTileHeadless(t *testing.T) {
	E.Headless = true
	defer func() { E.Headless = false }()

	videoClear()

	// A white-text tile carries its character byte in Color; TileToColorAndChar
	// returns (0x0F, that byte). Zero-value Board/World/ElementDefs suffice:
	// IsDark is false, so the dark-room branch and element lookups are skipped.
	E.Board.Info.IsDark = false
	E.Board.Tiles[5][7] = TTile{Element: E_TEXT_WHITE, Color: 'Q'}

	BoardDrawTile(5, 7) // draws at (x-1, y-1)

	if got := E.Screen[4][6]; got.Ch != 'Q' || got.Color != 0x0F {
		t.Errorf("E.Screen[4][6] = {Ch:%q Color:%#02x}, want {Ch:'Q' Color:0x0f}",
			got.Ch, got.Color)
	}

	// VideoShow ran headless above; confirm it drained the presenter dirty list
	// rather than leaving work for a presenter that will never run.
	if len(E.videoDirty) != 0 {
		t.Errorf("E.videoDirty not drained headless: len=%d", len(E.videoDirty))
	}

	cells := E.DrainScreenDirty()
	if len(cells) == 0 {
		t.Fatal("DrainScreenDirty returned no cells")
	}
	var found bool
	for _, cell := range cells {
		if cell.X == 4 && cell.Y == 6 && cell.Ch == 'Q' && cell.Color == 0x0F {
			found = true
		}
	}
	if !found {
		t.Fatalf("DrainScreenDirty missing drawn tile; cells=%v", cells)
	}
	if cells := E.DrainScreenDirty(); len(cells) != 0 {
		t.Fatalf("DrainScreenDirty second call len=%d, want 0", len(cells))
	}
}

// TestVideoMoveRoundTrip covers the scroll-window save/restore path: a region
// captured with VideoMoveToBuffer restores byte-for-byte with VideoMoveToVideo.
func TestVideoMoveRoundTrip(t *testing.T) {
	E.Headless = true
	defer func() { E.Headless = false }()

	videoClear()
	VideoWriteText(3, 4, 0x1E, "HELLO")

	var saved [80]VideoCell
	VideoMoveToBuffer(3, 4, 5, saved[:])

	// Overwrite, then restore from the saved copy.
	VideoWriteText(3, 4, 0x4F, "xxxxx")
	VideoMoveToVideo(3, 4, 5, saved[:])

	want := "HELLO"
	for i := 0; i < len(want); i++ {
		if got := E.Screen[3+int16(i)][4]; got.Ch != want[i] || got.Color != 0x1E {
			t.Errorf("col %d: E.Screen = {Ch:%q Color:%#02x}, want {Ch:%q Color:0x1e}",
				3+i, got.Ch, got.Color, want[i])
		}
	}
}
