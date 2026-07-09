package main

import "testing"

// TestBoardDrawTileHeadless is the M0.2 definition of done: with Headless set,
// a sim draw call (BoardDrawTile → VideoWriteText) lands in the Screen buffer
// and never touches tcell. The presenter's `screen` is nil here; if any tcell
// path were reached it would panic, so a clean pass proves the guard holds.
func TestBoardDrawTileHeadless(t *testing.T) {
	Headless = true
	defer func() { Headless = false }()

	videoClear()

	// A white-text tile carries its character byte in Color; TileToColorAndChar
	// returns (0x0F, that byte). Zero-value Board/World/ElementDefs suffice:
	// IsDark is false, so the dark-room branch and element lookups are skipped.
	Board.Info.IsDark = false
	Board.Tiles[5][7] = TTile{Element: E_TEXT_WHITE, Color: 'Q'}

	BoardDrawTile(5, 7) // draws at (x-1, y-1)

	if got := Screen[4][6]; got.Ch != 'Q' || got.Color != 0x0F {
		t.Errorf("Screen[4][6] = {Ch:%q Color:%#02x}, want {Ch:'Q' Color:0x0f}",
			got.Ch, got.Color)
	}

	// VideoShow ran headless above; confirm it drained the dirty list rather
	// than leaving work for a presenter that will never run.
	if len(videoDirty) != 0 {
		t.Errorf("videoDirty not drained headless: len=%d", len(videoDirty))
	}
}

// TestVideoMoveRoundTrip covers the scroll-window save/restore path: a region
// captured with VideoMoveToBuffer restores byte-for-byte with VideoMoveToVideo.
func TestVideoMoveRoundTrip(t *testing.T) {
	Headless = true
	defer func() { Headless = false }()

	videoClear()
	VideoWriteText(3, 4, 0x1E, "HELLO")

	var saved [80]VideoCell
	VideoMoveToBuffer(3, 4, 5, saved[:])

	// Overwrite, then restore from the saved copy.
	VideoWriteText(3, 4, 0x4F, "xxxxx")
	VideoMoveToVideo(3, 4, 5, saved[:])

	want := "HELLO"
	for i := 0; i < len(want); i++ {
		if got := Screen[3+int16(i)][4]; got.Ch != want[i] || got.Color != 0x1E {
			t.Errorf("col %d: Screen = {Ch:%q Color:%#02x}, want {Ch:%q Color:0x1e}",
				3+i, got.Ch, got.Color, want[i])
		}
	}
}
