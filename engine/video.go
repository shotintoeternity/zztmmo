package zztgo

// video.go is the headless screen buffer. Every Video* call the simulation
// makes lands here — in the 80x25 e.Screen array, never in a terminal. The
// tcell presenter (present_tcell.go) reads e.Screen and draws it, and only when
// !e.Headless. This is the M0.2 seam swap: thousands of VideoWriteText /
// BoardDrawTile call sites are unchanged; only where the bytes *go* changed
// (ANALYSIS.md §1).

// e.Screen is the text-mode buffer: e.Screen[x][y] holds the character byte and
// DOS attribute byte for column x (0..79), row y (0..24).

// e.Headless disables the presenter. When true, no Video* call touches tcell:
// installs/uninstalls are skipped and VideoShow just drops the dirty list.
// The simulation runs identically either way.

// dirtyCell records a e.Screen coordinate changed since the last present, so the
// presenter (and, later, netcode diffs) only touches what moved.
type dirtyCell struct{ x, y int16 }

// videoPut writes one cell and marks it dirty, clipping out-of-range writes
// exactly as tcell's SetContent silently did before (VideoMoveToVideo can
// address off-screen columns; see the loop bound note below).
func (e *Engine) videoPut(x, y int16, ch, color byte) {
	if x < 0 || x >= 80 || y < 0 || y >= 25 {
		return
	}
	e.Screen[x][y] = struct{ Ch, Color byte }{ch, color}
	e.videoDirty = append(e.videoDirty, dirtyCell{x, y})
}

// videoClear blanks the whole buffer (space on black) without presenting.
func (e *Engine) videoClear() {
	for x := int16(0); x < 80; x++ {
		for y := int16(0); y < 25; y++ {
			e.Screen[x][y] = struct{ Ch, Color byte }{' ', 0x00}
		}
	}
	e.videoDirty = e.videoDirty[:0]
}

func (e *Engine) VideoInstall() {
	e.videoClear()
	if !e.Headless {
		presentInstall()
	}
}

func (e *Engine) VideoClrScr() {
	e.videoClear()
	if !e.Headless {
		presentClrScr()
	}
}

func (e *Engine) VideoWriteText(x, y int16, color byte, text string) {
	for i := 0; i < len(text); i++ {
		e.videoPut(x+int16(i), y, text[i], color)
	}
	e.VideoShow() // TODO: is this inefficient?
}

// VideoCell is a saved character/attribute pair, used by txtwind.go to stash
// and restore the region under a scroll window. (Formerly held a tcell rune +
// style; now the raw DOS bytes, so it no longer drags tcell into the sim.)
type VideoCell struct {
	Ch, Color byte
}

func (e *Engine) VideoMoveToVideo(x, y, width int16, cells []VideoCell) {
	// ZZT-QUIRK: the loop bound is x+width, not width — a zztgo conversion
	// oddity preserved verbatim (M0 = no behavior change). Save/restore use
	// the same bound so it stays symmetric; the extra columns land off the
	// window and are clipped by videoPut.
	for i := 0; i < int(x+width); i++ {
		cell := cells[i]
		e.videoPut(x+int16(i), y, cell.Ch, cell.Color)
	}
	e.VideoShow()
}

func (e *Engine) VideoMoveToBuffer(x, y, width int16, cells []VideoCell) {
	for i := 0; i < int(x+width); i++ {
		cx := x + int16(i)
		if cx < 0 || cx >= 80 || y < 0 || y >= 25 {
			cells[i] = VideoCell{}
			continue
		}
		c := e.Screen[cx][y]
		cells[i] = VideoCell{c.Ch, c.Color}
	}
}

func (e *Engine) VideoShow() {
	if e.Headless {
		e.videoDirty = e.videoDirty[:0]
		return
	}
	presentFlush(e)
}

func (e *Engine) VideoHideCursor() {
	if !e.Headless {
		presentHideCursor()
	}
}

func (e *Engine) VideoUninstall() {
	if !e.Headless {
		presentUninstall()
	}
}

// --- Global Wrappers ---

func VideoClrScr() {
	E.VideoClrScr()
}

func VideoHideCursor() {
	E.VideoHideCursor()
}

func VideoInstall() {
	E.VideoInstall()
}

func VideoMoveToBuffer(x, y, width int16, cells []VideoCell) {
	E.VideoMoveToBuffer(x, y, width, cells)
}

func VideoMoveToVideo(x, y, width int16, cells []VideoCell) {
	E.VideoMoveToVideo(x, y, width, cells)
}

func VideoShow() {
	E.VideoShow()
}

func VideoUninstall() {
	E.VideoUninstall()
}

func VideoWriteText(x, y int16, color byte, text string) {
	E.VideoWriteText(x, y, color, text)
}

func videoClear() {
	E.videoClear()
}

func videoPut(x, y int16, ch, color byte) {
	E.videoPut(x, y, ch, color)
}
