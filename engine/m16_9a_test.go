package zztgo

import (
	"fmt"
	"testing"
)

// M16.9a. The browser's "New high score for <world>" window is drawn from
// RoomManager.HighScoreLines. The terminal path (game.go's HighScoreEntryEvent,
// EDITOR.PAS HighScoresAdd) shifts the list down from the earned slot and
// writes the player's score into it *before* HighScoresInitTextWindow renders,
// so the "-- You! --" row shows the score just earned and the rows under it are
// the ones the entry displaces. HighScoreLines used to only rename a slot,
// which printed the slot's own score beside the marker — -1 for an empty one.
//
// terminalPlacementLines is that terminal path, run for its lines only: the
// same mutation and the same HighScoresInitTextWindow the interactive client
// calls. It is the oracle, so a matching HighScoreLines cannot be wrong in a
// way a hand-written expectation would hide.
func terminalPlacementLines(list THighScoreList, listPos, score int16) []string {
	e := NewEngine()
	e.Headless = true
	e.HighScoreList = list

	// game.go:2222-2226 / EDITOR.PAS:1049-1052.
	for i := int16(HIGH_SCORE_COUNT - 1); i >= listPos; i-- {
		e.HighScoreList[i+1-1] = e.HighScoreList[i-1]
	}
	e.HighScoreList[listPos-1].Score = score
	e.HighScoreList[listPos-1].Name = "-- You! --"

	var window TTextWindowState
	e.HighScoresInitTextWindow(&window)
	return window.Lines[:window.LineCount]
}

func TestM169aPlacementWindowMatchesTerminalPath(t *testing.T) {
	// An empty list is HighScoresLoad's failure state: every slot -1, which is
	// what NewRoomManager installs. It is also the case the M16.9 golden caught,
	// where the marked row printed "-1".
	empty := THighScoreList{}
	for i := range empty {
		empty[i] = THighScoreEntry{Name: "", Score: -1}
	}

	// A full list: 30 named entries, descending, so a placing score displaces
	// every row beneath it and pushes the last one off the window entirely.
	full := THighScoreList{}
	for i := range full {
		full[i] = THighScoreEntry{Name: fmt.Sprintf("PLAYER%02d", i+1), Score: int16(3000 - i*100)}
	}

	cases := []struct {
		name  string
		list  THighScoreList
		score int16
	}{
		{"empty table, first slot", empty, 175},
		{"full table, top slot", full, 5000},
		{"full table, middle slot", full, 1550},
		{"full table, last slot", full, 250},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rm := NewRoomManager(testEmptyWorld(t))
			rm.highScores = tc.list

			listPos := rm.rankScore(tc.score)
			if listPos == 0 {
				t.Fatalf("score %d did not place", tc.score)
			}

			want := terminalPlacementLines(tc.list, listPos, tc.score)
			got := rm.HighScoreLines(listPos, tc.score)
			if len(got) != len(want) {
				t.Fatalf("line count %d, want %d\ngot:  %q\nwant: %q", len(got), len(want), got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("line %d = %q, want %q", i, got[i], want[i])
				}
			}

			// The regression itself, stated directly: the marked row carries the
			// score that earned it, never the slot's old one.
			marked := StrWidth(tc.score, 5) + "  -- You! --"
			found := false
			for _, line := range got {
				if line == marked {
					found = true
				}
			}
			if !found {
				t.Errorf("no %q row among %q", marked, got)
			}

			// Rendering must not disturb the stored list: the real write happens
			// when the name comes back, in RecordHighScore.
			if rm.HighScores() != tc.list {
				t.Error("HighScoreLines mutated the stored list")
			}
		})
	}
}

// The window the player sees and the list they end up in must agree: what
// RecordHighScore writes is what the placement window showed, one row renamed.
func TestM169aPlacementWindowAgreesWithRecordedList(t *testing.T) {
	rm := NewRoomManager(testEmptyWorld(t))
	for i, seed := range []struct {
		name  string
		score int16
	}{{"ALICE", 900}, {"BOB", 400}, {"CARA", 100}} {
		rm.highScores[i] = THighScoreEntry{Name: seed.name, Score: seed.score}
	}

	playerID := rm.JoinPlayer(1, 0, 0)
	state, _ := rm.PlayerState(playerID)
	state.Score = 500
	rm.SubmitQuitReply(playerID, true)
	rm.StepDiffs(nil)
	quits := rm.DrainQuits()
	if len(quits) != 1 || quits[0].ListPos != 2 {
		t.Fatalf("500 should place second: %+v", quits)
	}

	placement := rm.HighScoreLines(quits[0].ListPos, quits[0].Score)
	if !rm.RecordHighScore(playerID, "DAVE") {
		t.Fatal("RecordHighScore refused the slot it offered")
	}
	final := rm.HighScoreLines(0, 0)

	if len(placement) != len(final) {
		t.Fatalf("placement %q and final %q differ in length", placement, final)
	}
	for i := range placement {
		want := placement[i]
		if want == "  500  -- You! --" {
			want = "  500  DAVE"
		}
		if final[i] != want {
			t.Errorf("line %d: placement showed %q, final list has %q", i, placement[i], final[i])
		}
	}
}
