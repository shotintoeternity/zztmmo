package main

// video.go is the headless screen buffer. Every Video* call the simulation
// makes lands here — in the 80x25 Screen array, never in a terminal. The
// tcell presenter (present_tcell.go) reads Screen and draws it, and only when
// !Headless. This is the M0.2 seam swap: thousands of VideoWriteText /
// BoardDrawTile call sites are unchanged; only where the bytes *go* changed
// (ANALYSIS.md §1).

// Screen is the text-mode buffer: Screen[x][y] holds the character byte and
// DOS attribute byte for column x (0..79), row y (0..24).
var Screen [80][25]struct{ Ch, Color byte }

// Headless disables the presenter. When true, no Video* call touches tcell:
// installs/uninstalls are skipped and VideoShow just drops the dirty list.
// The simulation runs identically either way.
var Headless bool

// dirtyCell records a Screen coordinate changed since the last present, so the
// presenter (and, later, netcode diffs) only touches what moved.
type dirtyCell struct{ x, y int16 }

var videoDirty []dirtyCell

// videoPut writes one cell and marks it dirty, clipping out-of-range writes
// exactly as tcell's SetContent silently did before (VideoMoveToVideo can
// address off-screen columns; see the loop bound note below).
func videoPut(x, y int16, ch, color byte) {
	if x < 0 || x >= 80 || y < 0 || y >= 25 {
		return
	}
	Screen[x][y] = struct{ Ch, Color byte }{ch, color}
	videoDirty = append(videoDirty, dirtyCell{x, y})
}

// videoClear blanks the whole buffer (space on black) without presenting.
func videoClear() {
	for x := int16(0); x < 80; x++ {
		for y := int16(0); y < 25; y++ {
			Screen[x][y] = struct{ Ch, Color byte }{' ', 0x00}
		}
	}
	videoDirty = videoDirty[:0]
}

func VideoInstall() {
	videoClear()
	if !Headless {
		presentInstall()
	}
}

func VideoClrScr() {
	videoClear()
	if !Headless {
		presentClrScr()
	}
}

func VideoWriteText(x, y int16, color byte, text string) {
	for i := 0; i < len(text); i++ {
		videoPut(x+int16(i), y, text[i], color)
	}
	VideoShow() // TODO: is this inefficient?
}

// VideoCell is a saved character/attribute pair, used by txtwind.go to stash
// and restore the region under a scroll window. (Formerly held a tcell rune +
// style; now the raw DOS bytes, so it no longer drags tcell into the sim.)
type VideoCell struct {
	Ch, Color byte
}

func VideoMoveToVideo(x, y, width int16, cells []VideoCell) {
	// ZZT-QUIRK: the loop bound is x+width, not width — a zztgo conversion
	// oddity preserved verbatim (M0 = no behavior change). Save/restore use
	// the same bound so it stays symmetric; the extra columns land off the
	// window and are clipped by videoPut.
	for i := 0; i < int(x+width); i++ {
		cell := cells[i]
		videoPut(x+int16(i), y, cell.Ch, cell.Color)
	}
	VideoShow()
}

func VideoMoveToBuffer(x, y, width int16, cells []VideoCell) {
	for i := 0; i < int(x+width); i++ {
		cx := x + int16(i)
		if cx < 0 || cx >= 80 || y < 0 || y >= 25 {
			cells[i] = VideoCell{}
			continue
		}
		c := Screen[cx][y]
		cells[i] = VideoCell{c.Ch, c.Color}
	}
}

func VideoShow() {
	if Headless {
		videoDirty = videoDirty[:0]
		return
	}
	presentFlush()
}

func VideoHideCursor() {
	if !Headless {
		presentHideCursor()
	}
}

func VideoUninstall() {
	if !Headless {
		presentUninstall()
	}
}
