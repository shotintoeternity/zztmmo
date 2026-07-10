// cmd/zzt-validate runs the M7.5 validation gate on .ZZT files.
//
// For each world: loads headlessly, runs N GameSteps (default 200), asserts no
// panic, and checks that at least one screen cell renders non-empty.
//
// Usage:
//
//	go run ./cmd/zzt-validate [-dir .] [-steps 200] [WORLD ...]
//
// If no WORLD arguments are given, every .ZZT in -dir is validated.
// Exit 0 = all pass; 1 = one or more failures.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/benhoyt/zztgo"
)

func main() {
	dir := flag.String("dir", ".", "directory containing .ZZT files")
	steps := flag.Int("steps", 200, "number of headless GameSteps per world")
	flag.Parse()
	args := flag.Args()

	var worlds []string
	if len(args) > 0 {
		worlds = args
	} else {
		entries, err := os.ReadDir(*dir)
		if err != nil {
			fatalf("reading dir %s: %v", *dir, err)
		}
		for _, e := range entries {
			if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".zzt") {
				worlds = append(worlds, strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))
			}
		}
	}

	if len(worlds) == 0 {
		fmt.Fprintln(os.Stderr, "zzt-validate: no .ZZT files found")
		os.Exit(1)
	}

	pass, fail := 0, 0
	for _, name := range worlds {
		ok, msg := validate(name, *dir, *steps)
		if ok {
			fmt.Printf("PASS %s\n", name)
			pass++
		} else {
			fmt.Printf("FAIL %s: %s\n", name, msg)
			fail++
		}
	}

	fmt.Printf("\n%d passed, %d failed\n", pass, fail)
	if fail > 0 {
		os.Exit(1)
	}
}

// validate loads worldName from dir and runs steps headless game steps.
// Returns (true, "") on success, (false, reason) on failure.
func validate(worldName, dir string, steps int) (ok bool, reason string) {
	defer func() {
		if r := recover(); r != nil {
			ok = false
			reason = fmt.Sprintf("panic: %v\n%s", r, debug.Stack())
		}
	}()

	// Follow the zzt-smoke pattern: set zztgo.E before VideoInstall so the
	// Headless guard fires correctly and tcell is never touched.
	zztgo.E = zztgo.NewEngine()
	zztgo.E.Headless = true
	zztgo.E.MultiRoom = true
	zztgo.E.TickSpeed = 4
	zztgo.E.TickTimeDuration = int16(zztgo.E.TickSpeed) * 2
	zztgo.E.GameStateElement = zztgo.E_PLAYER
	zztgo.E.PlayerFor(0).Paused = false
	zztgo.E.GamePlayExitRequested = false
	zztgo.E.SetInputSource(&zztgo.ScriptedInput{})

	zztgo.VideoInstall()
	zztgo.TextWindowInit(5, 3, 50, 18)
	zztgo.RandomSeed(42)
	zztgo.WorldCreate()

	// WorldLoad resolves the file relative to the current directory.
	origDir, err := os.Getwd()
	if err != nil {
		return false, fmt.Sprintf("getwd: %v", err)
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false, fmt.Sprintf("abs(%s): %v", dir, err)
	}
	if absDir != origDir {
		if err := os.Chdir(absDir); err != nil {
			return false, fmt.Sprintf("chdir %s: %v", absDir, err)
		}
		defer func() { _ = os.Chdir(origDir) }()
	}

	if !zztgo.WorldLoad(worldName, ".ZZT", false) {
		return false, "WorldLoad failed (file not found or corrupt)"
	}

	stat0 := zztgo.E.Board.Stats[0]
	zztgo.E.Board.Tiles[stat0.X][stat0.Y].Element = zztgo.E_PLAYER
	zztgo.E.Board.Tiles[stat0.X][stat0.Y].Color = zztgo.ElementDefs[zztgo.E_PLAYER].Color
	zztgo.BoardEnter(0)
	zztgo.E.CurrentTick = zztgo.Random(100)
	zztgo.E.CurrentStatTicked = zztgo.E.Board.StatCount + 1

	for i := 0; i < steps; i++ {
		zztgo.GameStep(nil)
		// GamePlayExitRequested is acceptable — the world loaded and ran.
		if zztgo.E.GamePlayExitRequested {
			break
		}
	}

	// Non-empty render check: at least one board cell must be non-blank.
	for x := 0; x < 60; x++ {
		for y := 0; y < 25; y++ {
			ch := zztgo.E.Screen[x][y].Ch
			if ch != 0 && ch != ' ' {
				return true, ""
			}
		}
	}
	return false, "board render is empty (all spaces/zero bytes)"
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "zzt-validate: "+format+"\n", args...)
	os.Exit(1)
}
