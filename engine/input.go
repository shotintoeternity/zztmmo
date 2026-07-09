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

var (
	InputDeltaX, InputDeltaY int16
	InputShiftPressed        bool
	InputKeyPressed          byte

	KeysShiftHeld bool

	keyChan chan byte
)

// implementation uses: Dos, Crt, Keys, Sounds

var (
	InputLastDeltaX, InputLastDeltaY int16
	InputKeyBuffer                   string
)

func (e *Engine) InputUpdate() {
	InputDeltaX = e.InputDeltaX
	InputDeltaY = e.InputDeltaY
	InputShiftPressed = e.InputShiftPressed
	InputKeyPressed = e.InputKeyPressed
	InputLastDeltaX = e.InputLastDeltaX
	InputLastDeltaY = e.InputLastDeltaY
	InputKeyBuffer = e.InputKeyBuffer

	defer func() {
		e.InputDeltaX = InputDeltaX
		e.InputDeltaY = InputDeltaY
		e.InputShiftPressed = InputShiftPressed
		e.InputKeyPressed = InputKeyPressed
		e.InputLastDeltaX = InputLastDeltaX
		e.InputLastDeltaY = InputLastDeltaY
		e.InputKeyBuffer = InputKeyBuffer
	}()

	InputDeltaX, InputDeltaY, InputShiftPressed, InputKeyPressed = e.ActiveInput.Poll()
	if InputDeltaX != 0 || InputDeltaY != 0 {
		InputLastDeltaX = InputDeltaX
		InputLastDeltaY = InputDeltaY
	}
}

func InputUpdateWithKey(keyRead byte) {
	InputDeltaX = 0
	InputDeltaY = 0
	InputShiftPressed = false

	if keyRead == 0 {
		checkForKeys := true
		for checkForKeys {
			select {
			case key := <-keyChan:
				InputKeyPressed = key
				InputKeyBuffer += string([]byte{InputKeyPressed})
			default:
				checkForKeys = false
			}
		}
	} else {
		InputKeyPressed = keyRead
		InputKeyBuffer += string([]byte{InputKeyPressed})
	}

	if Length(InputKeyBuffer) != 0 {
		InputKeyPressed = InputKeyBuffer[0]
		if Length(InputKeyBuffer) == 1 {
			InputKeyBuffer = ""
		} else {
			InputKeyBuffer = Copy(InputKeyBuffer, Length(InputKeyBuffer)-1, 1)
		}
		switch InputKeyPressed {
		case KEY_UP, '8':
			InputDeltaX = 0
			InputDeltaY = -1
		case KEY_LEFT, '4':
			InputDeltaX = -1
			InputDeltaY = 0
		case KEY_RIGHT, '6':
			InputDeltaX = 1
			InputDeltaY = 0
		case KEY_DOWN, '2':
			InputDeltaX = 0
			InputDeltaY = 1
		}
	} else {
		InputKeyPressed = '\x00'
	}
	if InputDeltaX != 0 || InputDeltaY != 0 {
		InputShiftPressed = KeysShiftHeld
	}
	if InputDeltaX != 0 || InputDeltaY != 0 {
		InputLastDeltaX = InputDeltaX
		InputLastDeltaY = InputDeltaY
	}
}

func InputReadWaitKey() {
	key := <-keyChan
	InputUpdateWithKey(key)
}

// InputSource is the M0.3 seam that lets InputUpdate take a tick's input from
// something other than a live keyboard. Poll returns the same shape the input
// globals hold — movement delta, shift, and the raw key — and InputUpdate
// copies it into InputDeltaX/InputDeltaY/InputShiftPressed/InputKeyPressed,
// which the simulation reads unchanged (do NOT rename those globals — rule 6).
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
// reports the resulting globals, so InputUpdate behaves exactly as it did
// before M0.3. With no poller running (headless) the channel drain hits the
// select's default branch, so this is safe to call without a terminal too.
type TcellInput struct{}

func (TcellInput) Poll() (dx, dy int16, shift bool, key byte) {
	InputUpdateWithKey(0)
	return InputDeltaX, InputDeltaY, InputShiftPressed, InputKeyPressed
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

func init() {
	InputLastDeltaX = 0
	InputLastDeltaY = 0
	InputDeltaX = 0
	InputDeltaY = 0
	InputShiftPressed = false
	InputKeyBuffer = ""
}

// --- Global Wrappers ---

func InputUpdate() {
	E.InputUpdate()
}

func SetInputSource(s InputSource) {
	E.SetInputSource(s)
}
