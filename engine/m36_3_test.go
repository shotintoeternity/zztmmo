package zztgo

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// Task M36.3 — a high-score name is typed text, so it is admitted as ASCII.
//
// HighScoreNameMessage.Name arrives as UTF-8 and was stored as-is, while
// HighScoreLines goes out one CP437 byte per glyph (CP437Lines). A typed
// "José" was therefore stored as 4A 6F 73 C3 A9 and drawn as "JosÃ©": two
// glyphs for one letter, and every column after it shifted.
//
// Our client cannot send that (its PopupPromptString entry takes printable
// ASCII only, up to POPUP_FIELD_WIDTH = TextWindowWidth-16 = 34, as vanilla's
// GAME.PAS:1067 does), so the server now holds every client to the same rule:
// admit the way chat is admitted and cap at 34. Since M37.5 that is
// encodeTypedCP437: curly quotes and dashes become ASCII, a letter CP437 has
// becomes its byte (é is 0x82), and anything else is dropped. The test name
// predates M37.5 and is kept so the manifest row still finds it.
func TestM363HighScoreNameIsAdmittedAsPrintableASCII(t *testing.T) {
	cases := []struct {
		typed, want string
	}{
		{"DAVE", "DAVE"},
		{"Kevin & co. #1", "Kevin & co. #1"},
		{"José", "Jos\x82"}, // M37.5: the CP437 é, not a dropped letter
		{"Zoë 李", "Zo\x89 "},
		{"O’Neil", "O'Neil"},
		{"A\nB\x00C\tD", "ABCD"},
		{strings.Repeat("X", 60), strings.Repeat("X", 34)},
	}
	for _, c := range cases {
		rm := NewRoomManager(testEmptyWorld(t))
		playerID := rm.JoinPlayer(1, 0, 0)
		state, _ := rm.PlayerState(playerID)
		state.Score = 100
		rm.SubmitQuitReply(playerID, true)
		rm.StepDiffs(nil)
		if quits := rm.DrainQuits(); len(quits) != 1 || quits[0].ListPos == 0 {
			t.Fatalf("score did not qualify: %+v", quits)
		}

		// The name comes in exactly as the socket handler reads it.
		raw, _ := json.Marshal(map[string]string{"type": MessageTypeHighScoreName, "name": c.typed})
		var msg HighScoreNameMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatal(err)
		}
		if !rm.RecordHighScore(playerID, msg.Name) {
			t.Fatalf("RecordHighScore(%q) refused", c.typed)
		}
		if got := rm.HighScores()[0].Name; got != c.want {
			t.Errorf("typed %q: stored %q (% x), want %q", c.typed, got, got, c.want)
		}

		// And it goes out as one glyph per stored byte, so the score column
		// stays where HighScoresInitTextWindow put it.
		data, err := json.Marshal(CP437Lines(rm.HighScoreLines(0, 0)))
		if err != nil {
			t.Fatal(err)
		}
		var wire []string
		if err := json.Unmarshal(data, &wire); err != nil {
			t.Fatal(err)
		}
		row := wire[2]
		if !strings.HasSuffix(cp437Decode(row), c.want) || utf8.RuneCountInString(row) != len("  100  ")+len(c.want) {
			t.Errorf("typed %q: wire row %q is not \"  100  \" + %q glyph for glyph", c.typed, row, c.want)
		}
	}
}
