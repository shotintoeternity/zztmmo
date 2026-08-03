package zztgo

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell"
)

// M18.14. Loading a world that is not there reaches DisplayIOError (game.go),
// whose window ends in TextWindowSelect → InputReadWaitKey → a receive on the
// key channel. Headless nothing ever feeds that channel, so before the fix this
// call did not return at all: the Go runtime aborted the process with "fatal
// error: all goroutines are asleep - deadlock!", and cmd/zzt-server's own
// log.Fatalf("load %s.ZZT failed") on the next line was unreachable.
func TestWorldLoadMissingHeadlessReturnsFalseWithoutBlocking(t *testing.T) {
	dir := t.TempDir() // deliberately empty: no world of any name is in it

	e := NewEngine()
	e.Headless = true
	e.VideoInstall()

	done := make(chan bool, 1)
	go func() {
		done <- e.WorldLoad(filepath.Join(dir, "NOSUCH"), ".ZZT", false)
	}()

	select {
	case ok := <-done:
		if ok {
			t.Fatal("WorldLoad reported success for a world that is not on disk")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WorldLoad on a missing world did not return within 5s: DisplayIOError is still waiting for a keypress that no headless process can supply")
	}

	if e.LastIOError == nil {
		t.Fatal("the load error was swallowed: nothing recorded on the engine")
	}
	if !strings.Contains(e.LastIOError.Error(), "NOSUCH") {
		t.Fatalf("LastIOError = %v, want the failed open of NOSUCH.ZZT", e.LastIOError)
	}
}

// The headless branch must leave the screen alone (there is no player to read
// the window and no keypress coming to close it) while still reporting the
// error through the bool its callers already read.
func TestDisplayIOErrorHeadlessRecordsInsteadOfDrawing(t *testing.T) {
	e := NewEngine()
	e.Headless = true
	e.VideoInstall()

	before := e.Screen
	if !e.DisplayIOError(errors.New("open TOWN.ZZT: no such file or directory")) {
		t.Fatal("DisplayIOError returned false for a real error; WorldLoad reads that bool to mean the load failed")
	}
	if e.Screen != before {
		t.Fatal("headless DisplayIOError drew its text window")
	}
	if e.LastIOError == nil {
		t.Fatal("headless DisplayIOError did not record the error anywhere observable")
	}

	e.LastIOError = nil
	if e.DisplayIOError(nil) {
		t.Fatal("DisplayIOError(nil) must stay false")
	}
	if e.LastIOError != nil {
		t.Fatalf("DisplayIOError(nil) recorded %v", e.LastIOError)
	}
}

// The interactive path is unchanged: the window still opens and still waits for
// the keypress that closes it, and it does not take the headless branch.
func TestDisplayIOErrorInteractiveStillOpensWindow(t *testing.T) {
	prevE, prevKeyChan, prevRejected := E, keyChan, TextWindowRejected
	defer func() { E, keyChan, TextWindowRejected = prevE, prevKeyChan, prevRejected }()

	e := NewEngine()
	// Install the buffer headless, then drop the flag: presentInstall opens a
	// real terminal (and os.Exit(1)s when there is none), so the drawing this
	// test wants to see goes to tcell's simulation screen instead. Everything
	// above present_tcell.go — DisplayIOError, TextWindow*, video.go — runs the
	// interactive path exactly as a player's terminal does.
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
	E = e // TextWindow* draw through the package-level video functions

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
	go func() {
		// At least 40 characters: the converted title line slices err.Error()
		// to exactly 40 with no clamp (GAME.PAS's Copy would have clamped).
		done <- e.DisplayIOError(errors.New("open SOMEWHERE/ANYWHERE.ZZT: no such file or directory"))
	}()

	select {
	case ok := <-done:
		if !ok {
			t.Fatal("DisplayIOError returned false for a real error")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("interactive DisplayIOError never returned even with ESC available")
	}

	if !TextWindowRejected {
		t.Fatal("the interactive path did not run TextWindowSelect: the window no longer opens")
	}
	if e.LastIOError != nil {
		t.Fatalf("the interactive path took the headless branch: LastIOError = %v", e.LastIOError)
	}
}

// The first-run experience the README's Quick Start produces on a clean clone:
// a missing startup world must print the message the code already has and exit
// non-zero, not dump a runtime deadlock trace.
func TestServerMissingStartupWorldReportsAndExits(t *testing.T) {
	binPath := getM1619ServerBinary(t)

	rootDir := t.TempDir() // no TOWN.ZZT, no world of any name
	webDir := filepath.Join(rootDir, "web")
	if err := os.MkdirAll(webDir, 0755); err != nil {
		t.Fatalf("mkdir web: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binPath,
		"-addr", "127.0.0.1:0",
		"-world", "MISSING",
		"-web", webDir,
		"-worlds", rootDir,
		"-saves", "",
		"-help", ".",
	)
	cmd.Dir = rootDir
	out, err := cmd.CombinedOutput()

	if ctx.Err() != nil {
		t.Fatalf("zzt-server hung on a missing world instead of exiting. Output:\n%s", string(out))
	}
	if err == nil {
		t.Fatalf("zzt-server exited 0 with no world to serve. Output:\n%s", string(out))
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("running zzt-server failed for another reason: %v\nOutput:\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "load MISSING.ZZT failed") {
		t.Fatalf("zzt-server did not print its own load failure. Output:\n%s", string(out))
	}
	if strings.Contains(string(out), "all goroutines are asleep") {
		t.Fatalf("zzt-server still deadlocks on a missing world. Output:\n%s", string(out))
	}
}
