package zztgo

import (
	"strings"
	"testing"
)

func TestInvalidDoorColorsDoNotPanicWhenTouched(t *testing.T) {
	e := NewEngine()
	e.Headless = true
	e.WorldCreate()

	for _, tc := range []struct {
		name  string
		color byte
	}{
		{name: "0x0E", color: 0x0E},
		{name: "0x8E", color: 0x8E},
	} {
		t.Run(tc.name, func(t *testing.T) {
			color := tc.color
			e.Board.Tiles[10][12] = TTile{Element: E_DOOR, Color: color}
			dx, dy := int16(1), int16(0)
			func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						t.Fatalf("touching door color %#02x panicked: %v", color, recovered)
					}
				}()
				e.ElementDoorTouch(10, 12, 0, &dx, &dy)
			}()
			if got := e.Board.Tiles[10][12].Element; got != E_DOOR {
				t.Errorf("invalid door color %#02x opened door: element=%d", color, got)
			}
		})
	}
}

func TestZWDRejectsDoorsWithoutAKeyColor(t *testing.T) {
	for _, color := range []string{"0x0E", "0x8E"} {
		t.Run(color, func(t *testing.T) {
			src := strings.Replace(zwdOneRoomExample, "o = Object color 0x0F", "o = Door color "+color, 1)
			_, err := CompileZWDWorld(src)
			if err == nil || !strings.Contains(err.Error(), "door color must have a key background nibble") {
				t.Fatalf("CompileZWDWorld error = %v, want invalid-door-color error", err)
			}
		})
	}

	color, err := parseLegendColor(E_DOOR, "yellow")
	if err != nil {
		t.Fatalf("named door color rejected: %v", err)
	}
	if color != 0x6F {
		t.Fatalf("Door color yellow = %#02x, want 0x6F", color)
	}
}

func TestRoomManagerIsolatesPanickedRoom(t *testing.T) {
	rm := NewRoomManager(testMultiplayerSmokeWorld(t))
	badPlayer := rm.JoinPlayer(1, 0, 0)
	goodPlayer := rm.JoinPlayer(2, 0, 0)

	badBoard, badStat, ok := rm.PlayerLocation(badPlayer)
	if !ok || badBoard != 1 {
		t.Fatalf("bad player location = board %d stat %d ok=%v", badBoard, badStat, ok)
	}
	badRoom, ok := rm.Room(badBoard)
	if !ok {
		t.Fatal("bad room missing")
	}
	badRoom.Engine.Board.Tiles[badRoom.Engine.Board.Stats[badStat].X][badRoom.Engine.Board.Stats[badStat].Y].Element = E_BEAR

	oldTick := ElementDefs[E_BEAR].TickProc
	ElementDefs[E_BEAR].TickProc = func(*Engine, int16) { panic("test room panic") }
	defer func() { ElementDefs[E_BEAR].TickProc = oldTick }()

	diffs := rm.StepDiffs(nil)
	if _, ok := rm.Room(1); ok {
		t.Fatal("panicked room was retained")
	}
	if _, ok := rm.PlayerState(badPlayer); ok {
		t.Fatal("player in panicked room was retained")
	}
	if _, ok := rm.PlayerState(goodPlayer); !ok {
		t.Fatal("healthy room player was removed")
	}
	if _, ok := diffs[goodPlayer]; !ok {
		t.Fatal("healthy room did not produce a diff after another room panicked")
	}
}
