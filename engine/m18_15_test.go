package zztgo

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell"
)

// M18.15. The interactive title line was `err.Error()[:40]`, a Go slice with no
// clamp, so an error message shorter than 40 bytes panicked with a slice-bounds
// error instead of opening the window. `write TOWN.ZZT: no space left on
// device` (38 bytes) is exactly the disk-full case the window's own text is
// about. GAME.PAS:664 builds the title from Str(IOResult, ...) — an error
// *number* — and truncates nothing, so the 40 is the machine conversion's
// invention and there is no vanilla behavior to be faithful to; the fix is the
// clamp the slice always needed.
func TestDisplayIOErrorTitleClampsShortMessages(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{
			name: "ten characters",
			err:  errors.New("disk full!"),
			want: "Error: disk full!",
		},
		{
			name: "the disk-full case the window is about",
			err:  errors.New("write TOWN.ZZT: no space left on device"),
			want: "Error: write TOWN.ZZT: no space left on device",
		},
		{
			name: "empty message",
			err:  errors.New(""),
			want: "Error: ",
		},
		{
			// Exactly 40: the boundary the old slice happened to survive, which
			// is the only reason M18.14's report did not panic.
			name: "exactly forty characters",
			err:  errors.New("open TOWN.ZZT: no such file or directory"),
			want: "Error: open TOWN.ZZT: no such file or directory",
		},
		{
			// Longer than 40 must truncate at 40 exactly as it does today.
			name: "longer than forty characters",
			err:  errors.New("open SOMEWHERE/ANYWHERE.ZZT: no such file or directory"),
			want: "Error: open SOMEWHERE/ANYWHERE.ZZT: no such fil",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := displayIOErrorTitle(tc.err); got != tc.want {
				t.Fatalf("displayIOErrorTitle(%q) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}

	if n := len("open TOWN.ZZT: no such file or directory"); n != 40 {
		t.Fatalf("the boundary case is %d bytes, not the 40 this test is about", n)
	}
	long := displayIOErrorTitle(errors.New(strings.Repeat("x", 200)))
	if n := len(long) - len("Error: "); n != 40 {
		t.Fatalf("a long message truncated to %d bytes, want the 40 the conversion has always cut at", n)
	}
}

// End to end on the path the panic actually lived on: a short error reaches the
// terminal build's DisplayIOError and gets the window, not a slice-bounds
// runtime error. Before the clamp this test did not fail — it took the test
// binary down with it.
func TestDisplayIOErrorInteractiveShortMessageOpensWindow(t *testing.T) {
	prevE, prevKeyChan, prevRejected := E, keyChan, TextWindowRejected
	defer func() { E, keyChan, TextWindowRejected = prevE, prevKeyChan, prevRejected }()

	e := NewEngine()
	// Same harness as M18.14's interactive test: install the buffer headless so
	// presentInstall never opens a real terminal, then drop the flag so
	// DisplayIOError takes the interactive branch and draws through tcell's
	// simulation screen.
	e.Headless = true
	e.VideoInstall()
	e.Headless = false
	prevScreen := screen
	defer func() { screen = prevScreen }()
	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatalf("simulation screen init: %v", err)
	}
	defer sim.Fini()
	screen = sim
	E = e

	keyChan = make(chan byte, 1)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case keyChan <- KEY_ESCAPE:
			case <-stop:
				return
			}
		}
	}()

	TextWindowRejected = false
	done := make(chan bool, 1)
	panicked := make(chan string, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				panicked <- fmt.Sprint(r)
			}
		}()
		// 10 characters, well under the 40 the title line used to slice at.
		done <- e.DisplayIOError(errors.New("disk full!"))
	}()

	select {
	case ok := <-done:
		if !ok {
			t.Fatal("DisplayIOError returned false for a real error")
		}
	case msg := <-panicked:
		t.Fatalf("DisplayIOError panicked on a 10-character error instead of showing the window: %s", msg)
	case <-time.After(20 * time.Second):
		t.Fatal("interactive DisplayIOError never returned even with ESC available")
	}

	if !TextWindowRejected {
		t.Fatal("the window never opened: TextWindowSelect did not run")
	}
	if e.LastIOError != nil {
		t.Fatalf("the interactive path took the headless branch: LastIOError = %v", e.LastIOError)
	}
}
