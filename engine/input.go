// INPUT.PAS, machine-converted by pas2go. Upstream this read a DOS keyboard
// into globals; in ZZTMMO the same state lives on each Engine (e.InputDeltaX
// and friends, M36.22) and the server sets it from keymasks sent by browser
// clients, so the simulation below cannot tell the difference. Process-global
// input let two engines stepping on different goroutines (a live room and
// generation's dry run) overwrite each other's moves.
//
// e.InputDeltaX and e.InputDeltaY are the trap in this file. They are the
// player's movement for the tick, and they are ALSO reused as throwaway
// out-params for TouchProc calls in elements.go, which take a delta pair they
// do not use. Renaming or repurposing them breaks call sites that look
// unrelated. See doc.go.

package zztgo // unit: Input

import (
	"github.com/gdamore/tcell"
)

const (
	KEY_BACKSPACE = '\x08'
	KEY_TAB       = '\t'
	KEY_ENTER     = '\r'
	KEY_CTRL_Y    = '\x19'
	KEY_ESCAPE    = '\x1b'
	KEY_F1        = '\xbb'
	KEY_F2        = '\xbc'
	KEY_F3        = '\xbd'
	KEY_F4        = '\xbe'
	KEY_UP        = '\xc8'
	KEY_PAGE_UP   = '\xc9'
	KEY_LEFT      = '\xcb'
	KEY_RIGHT     = '\xcd'
	KEY_DOWN      = '\xd0'
	KEY_PAGE_DOWN = '\xd1'
	KEY_INSERT    = '\xd2'
	KEY_DELETE    = '\xd3'
	KEY_HOME      = '\xd4'
	KEY_END       = '\xd5'
)

// The keyboard itself is still one per process: only the terminal build
// reads it, through E.
var (
	KeysShiftHeld bool

	keyChan chan byte
)

// implementation uses: Dos, Crt, Keys, Sounds

func (e *Engine) InputUpdate() {
	if _, live := e.ActiveInput.(TcellInput); live {
		// The live keyboard edits this engine's key buffer in place, which
		// Poll's return values cannot carry. Every engine starts on
		// TcellInput, room engines included, so routing it through E would
		// only move the M36.22 race onto E's fields.
		e.InputUpdateWithKey(0)
	} else {
		e.InputDeltaX, e.InputDeltaY, e.InputShiftPressed, e.InputKeyPressed = e.ActiveInput.Poll()
	}
	if e.InputDeltaX != 0 || e.InputDeltaY != 0 {
		e.InputLastDeltaX = e.InputDeltaX
		e.InputLastDeltaY = e.InputDeltaY
	}
}

func (e *Engine) InputUpdateWithKey(keyRead byte) {
	e.InputDeltaX = 0
	e.InputDeltaY = 0
	e.InputShiftPressed = false

	if keyRead == 0 {
		checkForKeys := true
		for checkForKeys {
			select {
			case key := <-keyChan:
				e.InputKeyPressed = key
				e.InputKeyBuffer += string([]byte{e.InputKeyPressed})
			default:
				checkForKeys = false
			}
		}
	} else {
		e.InputKeyPressed = keyRead
		e.InputKeyBuffer += string([]byte{e.InputKeyPressed})
	}

	if Length(e.InputKeyBuffer) != 0 {
		e.InputKeyPressed = e.InputKeyBuffer[0]
		if Length(e.InputKeyBuffer) == 1 {
			e.InputKeyBuffer = ""
		} else {
			e.InputKeyBuffer = Copy(e.InputKeyBuffer, Length(e.InputKeyBuffer)-1, 1)
		}
		switch e.InputKeyPressed {
		case KEY_UP, '8':
			e.InputDeltaX = 0
			e.InputDeltaY = -1
		case KEY_LEFT, '4':
			e.InputDeltaX = -1
			e.InputDeltaY = 0
		case KEY_RIGHT, '6':
			e.InputDeltaX = 1
			e.InputDeltaY = 0
		case KEY_DOWN, '2':
			e.InputDeltaX = 0
			e.InputDeltaY = 1
		}
	} else {
		e.InputKeyPressed = '\x00'
	}
	if e.InputDeltaX != 0 || e.InputDeltaY != 0 {
		e.InputShiftPressed = KeysShiftHeld
	}
	if e.InputDeltaX != 0 || e.InputDeltaY != 0 {
		e.InputLastDeltaX = e.InputDeltaX
		e.InputLastDeltaY = e.InputDeltaY
	}
}

func InputReadWaitKey() {
	key := <-keyChan
	E.InputUpdateWithKey(key)
}

// InputSource is the M0.3 seam that lets InputUpdate take a tick's input from
// something other than a live keyboard. Poll returns the same shape the input
// state holds — movement delta, shift, and the raw key — and InputUpdate
// copies it into e.InputDeltaX/e.InputDeltaY/e.InputShiftPressed/
// e.InputKeyPressed, which the simulation reads unchanged (do NOT rename those
// fields — rule 6).
type InputSource interface {
	Poll() (dx, dy int16, shift bool, key byte)
}

// e.ActiveInput is where InputUpdate reads from. It defaults to the live tcell
// keyboard so interactive play is byte-for-byte unchanged; tests and the
// future server swap in a ScriptedInput via SetInputSource.

// SetInputSource selects where InputUpdate reads its input from.
func (e *Engine) SetInputSource(s InputSource) {
	e.ActiveInput = s
}

// TcellInput is the live-keyboard source. It runs the original keyboard
// processing (drain the poller channel, buffer keys, map arrows to deltas) and
// reports the result, so InputUpdate behaves exactly as it did before M0.3.
// With no poller running (headless) the channel drain hits the select's
// default branch, so this is safe to call without a terminal too.
// Engine.InputUpdate does not call this Poll: it runs InputUpdateWithKey on its
// own engine instead (M36.22). Poll is kept for the interface and reads E.
type TcellInput struct{}

func (TcellInput) Poll() (dx, dy int16, shift bool, key byte) {
	E.InputUpdateWithKey(0)
	return E.InputDeltaX, E.InputDeltaY, E.InputShiftPressed, E.InputKeyPressed
}

// ScriptedTick is one tick of pre-recorded input.
type ScriptedTick struct {
	DeltaX, DeltaY int16
	Shift          bool
	Key            byte
}

// ScriptedInput replays a fixed slice of ticks with no terminal — the basis of
// the deterministic replay harness (M0.6). Once the script is exhausted it
// reports idle input (no movement, no key) rather than blocking.
type ScriptedInput struct {
	Ticks []ScriptedTick
	Pos   int
}

func (s *ScriptedInput) Poll() (dx, dy int16, shift bool, key byte) {
	if s.Pos >= len(s.Ticks) {
		return 0, 0, false, 0
	}
	t := s.Ticks[s.Pos]
	s.Pos++
	return t.DeltaX, t.DeltaY, t.Shift, t.Key
}

func InputStartPoller(screen tcell.Screen) {
	keyChan = make(chan byte)
	go InputKeyPoller(screen, keyChan)
}

func InputKeyPoller(screen tcell.Screen, keyChan chan byte) {
	for {
		event := screen.PollEvent()
		switch event := event.(type) {
		case *tcell.EventKey:
			// TODO: this doesn't work for shift-up and shift-down in tcell :-(
			KeysShiftHeld = event.Modifiers()&tcell.ModShift != 0

			switch event.Key() {
			case tcell.KeyRune:
				r := event.Rune()
				if r >= 32 && r <= 126 {
					keyChan <- byte(r)
				}
			default:
				key := tcellToKey[event.Key()]
				if key != 0 {
					keyChan <- key
				}
			}
		case *tcell.EventResize:
			screen.Sync()
		}
	}
}

var tcellToKey = map[tcell.Key]byte{
	tcell.KeyBackspace:  KEY_BACKSPACE,
	tcell.KeyBackspace2: KEY_BACKSPACE,
	tcell.KeyCtrlY:      KEY_CTRL_Y,
	tcell.KeyDelete:     KEY_DELETE,
	tcell.KeyDown:       KEY_DOWN,
	tcell.KeyEnd:        KEY_END,
	tcell.KeyEnter:      KEY_ENTER,
	tcell.KeyEscape:     KEY_ESCAPE,
	tcell.KeyF1:         KEY_F1,
	tcell.KeyF2:         KEY_F2,
	tcell.KeyF3:         KEY_F3,
	tcell.KeyF4:         KEY_F4,
	tcell.KeyHome:       KEY_HOME,
	tcell.KeyInsert:     KEY_INSERT,
	tcell.KeyLeft:       KEY_LEFT,
	tcell.KeyPgDn:       KEY_PAGE_DOWN,
	tcell.KeyPgUp:       KEY_PAGE_UP,
	tcell.KeyRight:      KEY_RIGHT,
	tcell.KeyTab:        KEY_TAB,
	tcell.KeyUp:         KEY_UP,
}

// --- Global Wrappers ---

func InputUpdate() {
	E.InputUpdate()
}

func SetInputSource(s InputSource) {
	E.SetInputSource(s)
}
