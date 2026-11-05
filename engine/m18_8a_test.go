package zztgo

// M18.8a — a dreamed object written `@name` / `:touch` / text, with no #end
// above the label, talks the moment the board opens. ZZT reads past label lines
// (OOP.PAS:559-563), so the run falls straight into the :touch body. This is
// every object in TIMSWEEN ("Tim Sweeney Buys AWS", owner-reported
// 2026-11-04). The audit now follows the program as ZZT runs it, and the
// pipeline parks this shape itself before the audit sees it.

import (
	"strings"
	"testing"
)

// fallThroughZWD is one board in the TIMSWEEN shape: a talker whose first line
// after @name is :touch, a sign with a hyperlink menu in the same shape, and a
// correctly parked object that must be left exactly as it is.
func fallThroughZWD() string {
	rows := []string{"@" + strings.Repeat(".", 59)}
	for len(rows) < 25 {
		rows = append(rows, strings.Repeat(".", 60))
	}
	row := []byte(rows[9])
	for _, x := range []int{10, 20, 30, 40} {
		row[x-1] = 'o'
	}
	rows[9] = string(row)
	return "zwd 1\nworld \"FALLTHRU\"\n\nboard \"Lobby\"\n  start player at 1,1\n  dark false\n" +
		"  exits north none south none west none east none\n  grid\n" +
		strings.Join(rows, "\n") + "\n  end\n  legend\n" +
		"    @ = Player color 0x1F\n    . = Empty color 0x00\n    o = Object color 0x0F\n  end\n" +
		"  stats\n" +
		"    stat at 10,10 element Object\n    oop\n" +
		"    @receptionist\n    :touch\n    Welcome back to the hub, Mr. Sweeney.\n    $The quarterly directory:\n    #end\n    end\n" +
		"    stat at 20,10 element Object\n    oop\n" +
		"    @Tim\n    :touch\n    $Tim Sweeney:\n    !plan;And how do you pay for a cloud?\n    #end\n" +
		"    :plan\n    \"Fortnite paid for a LOT of gems.\"\n    #end\n    end\n" +
		"    stat at 30,10 element Object\n    oop\n" +
		"    @parked\n    #end\n    :touch\n    Already correct.\n    #end\n    end\n" +
		"    stat at 40,10 element Object\n    oop\n" +
		"    @guard\n    :loop\n    /n\n    /s\n    #loop\n    :touch\n    Halt!\n    #end\n    end\n" +
		"  end\nend\n"
}

// scrollsOnEntry opens board 0 of a compiled world, ticks it with nobody at the
// controls, and returns the object names whose scroll opened.
func scrollsOnEntry(t *testing.T, data []byte) []string {
	t.Helper()
	e := NewEngine()
	e.Headless = true
	e.VideoInstall()
	if err := e.worldReadFrom(strings.NewReader(string(data)), false, nil); err != nil {
		t.Fatalf("load: %v", err)
	}
	e.BoardOpen(0)
	e.BoardEnter(0)
	e.GameStateElement = E_PLAYER
	e.PlayerFor(0).Paused = false
	e.SetInputSource(&ScriptedInput{})
	var spoke []string
	for i := 0; i < 60; i++ {
		e.GameStep(nil)
		for _, ev := range e.Events {
			if scroll, ok := ev.(ScrollEvent); ok {
				spoke = append(spoke, oopObjectLabel(e.Board.Stats[scroll.StatId].Data))
			}
		}
		e.Events = nil
	}
	return spoke
}

// The defect, end to end in the engine: without the park, both TIMSWEEN-shaped
// objects open their scroll on entry and the parked one and the guard do not.
// This is vanilla behaviour and the reason the park exists.
func TestM188aFallThroughTalksOnEntry(t *testing.T) {
	data, err := CompileZWD(fallThroughZWD())
	if err != nil {
		t.Fatalf("CompileZWD: %v", err)
	}
	spoke := scrollsOnEntry(t, data)
	joined := strings.Join(spoke, ",")
	if !strings.Contains(joined, "@receptionist") || !strings.Contains(joined, "@Tim") {
		t.Fatalf("expected both fall-through objects to talk on entry, got %q", spoke)
	}
	if strings.Contains(joined, "@parked") || strings.Contains(joined, "@guard") {
		t.Fatalf("a parked object or a patroller talked on entry: %q", spoke)
	}
	failures, err := auditObjectPreludes(data)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if len(failures) != 1 || !strings.Contains(failures[0].Err, "@receptionist") || !strings.Contains(failures[0].Err, "@Tim") {
		t.Fatalf("the audit must name both talkers on the board, got %+v", failures)
	}
	if strings.Contains(failures[0].Err, "@parked") || strings.Contains(failures[0].Err, "@guard") {
		t.Fatalf("the audit flagged an object that does not talk on entry: %s", failures[0].Err)
	}
}

// The fix: the park adds #end above each fall-through :touch, touches nothing
// else, and after it nobody talks on entry and the audit is quiet.
func TestM188aParkSilencesTheBoard(t *testing.T) {
	src := fallThroughZWD()
	parked, n := parkFallThroughObjects(src)
	if n != 2 {
		t.Fatalf("parked %d objects, want 2 (the receptionist and Tim)", n)
	}
	for _, want := range []string{
		"    @receptionist\n    #end\n    :touch\n    Welcome back",
		"    @Tim\n    #end\n    :touch\n",
		"    @parked\n    #end\n    :touch\n    Already correct.",
		"    @guard\n    :loop\n",
	} {
		if !strings.Contains(parked, want) {
			t.Fatalf("parked source lacks %q:\n%s", want, parked)
		}
	}
	if strings.Count(parked, "#end") != strings.Count(src, "#end")+2 {
		t.Fatalf("the park added more than the two #end lines")
	}
	data, err := CompileZWD(parked)
	if err != nil {
		t.Fatalf("CompileZWD(parked): %v", err)
	}
	if spoke := scrollsOnEntry(t, data); len(spoke) != 0 {
		t.Fatalf("after the park nobody may talk on entry, got %q", spoke)
	}
	if failures, _ := auditObjectPreludes(data); len(failures) != 0 {
		t.Fatalf("after the park the audit must be quiet, got %+v", failures)
	}
	// Idempotent: a second pass finds nothing left to park.
	if again, n := parkFallThroughObjects(parked); n != 0 || again != parked {
		t.Fatalf("a second park changed the source (%d objects)", n)
	}
}

// The park is narrow: a custom first label, or a :touch whose body does
// nothing a player sees, is left alone.
func TestM188aParkLeavesOtherShapesAlone(t *testing.T) {
	for _, program := range []string{
		"    @a\n    :start\n    Hello.\n    #end\n",         // custom first label: intent unreadable
		"    @a\n    :touch\n    #play c\n    #end\n",        // :touch body shows nothing
		"    @a\n    Hello at load.\n    :touch\n    #end\n", // M18.8's own shape: audit's job
		"    @a\n    :touch\n    #die\n",                     // ends before any text
	} {
		src := "  stats\n    stat at 1,1 element Object\n    oop\n" + program + "    end\n  end\n"
		if out, n := parkFallThroughObjects(src); n != 0 || out != src {
			t.Errorf("parked a shape it must leave alone:\n%s", program)
		}
	}
}
