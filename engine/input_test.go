package zztgo

import "testing"

// TestScriptedInput20Ticks is the M0.3 definition of done: drive 20 ticks of
// input through InputUpdate with no terminal and no key poller, proving the
// InputSource seam feeds the engine's input fields.
func TestScriptedInput20Ticks(t *testing.T) {
	ticks := make([]ScriptedTick, 20)
	for i := range ticks {
		ticks[i] = ScriptedTick{DeltaX: 1, DeltaY: 0, Key: KEY_RIGHT}
	}
	// A couple of different ticks to prove all fields flow through.
	ticks[5] = ScriptedTick{DeltaX: 0, DeltaY: -1, Shift: true, Key: KEY_UP}
	ticks[12] = ScriptedTick{DeltaX: 0, DeltaY: 0, Key: ' '}

	prev := E.ActiveInput
	SetInputSource(&ScriptedInput{Ticks: ticks})
	defer SetInputSource(prev)

	for i := 0; i < 20; i++ {
		InputUpdate()
		w := ticks[i]
		if E.InputDeltaX != w.DeltaX || E.InputDeltaY != w.DeltaY ||
			E.InputShiftPressed != w.Shift || E.InputKeyPressed != w.Key {
			t.Fatalf("tick %d: got dx=%d dy=%d shift=%v key=%#x; want dx=%d dy=%d shift=%v key=%#x",
				i, E.InputDeltaX, E.InputDeltaY, E.InputShiftPressed, E.InputKeyPressed,
				w.DeltaX, w.DeltaY, w.Shift, w.Key)
		}
	}

	// LastDelta tracks the most recent non-zero movement (tick 19 moved right).
	if E.InputLastDeltaX != 1 || E.InputLastDeltaY != 0 {
		t.Errorf("InputLastDelta = (%d,%d), want (1,0)", E.InputLastDeltaX, E.InputLastDeltaY)
	}

	// Past the end of the script the source idles rather than blocking on a
	// terminal that isn't there.
	InputUpdate()
	if E.InputDeltaX != 0 || E.InputDeltaY != 0 || E.InputShiftPressed || E.InputKeyPressed != 0 {
		t.Errorf("exhausted script should idle; got dx=%d dy=%d shift=%v key=%#x",
			E.InputDeltaX, E.InputDeltaY, E.InputShiftPressed, E.InputKeyPressed)
	}
}
