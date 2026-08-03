package zztgo

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// M19.1 — the player color on the wire.
//
// The milestone's governing constraint is that a 24-bit background must never
// reach the simulation: not Board.Tiles, not StateHash, not a recording. These
// tests are the proof of that constraint rather than a demonstration of the
// feature — the feature itself (a smiley drawn on a colored square) is proved
// on a canvas, by web/test/player_color.test.mjs.
//
// The inverse of M16.15a is the thing to keep in view. There, an account
// sidecar the player was JOINED WITH changed simulation state, so it had to
// become a recorded op and recordVersion had to move. A color changes nothing
// the simulation can observe, so it must ride the wire WITHOUT being recorded —
// and TestM191AColoredSessionRecordsByteIdenticallyIsTheProof.

func TestM191SanitizePlayerColorAcceptsOnlySixHexDigits(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
		why  string
	}{
		{"#000000", "#000000", "black is a color, not an absence"},
		{"#ffffff", "#ffffff", "lowercase hex"},
		{"#A1B2C3", "#A1B2C3", "uppercase hex, passed through unchanged"},
		{"#aF09bE", "#aF09bE", "mixed case"},
		{"", "", "unset — the vanilla white-on-blue player"},
		{"#fff", "", "CSS shorthand is not the wire format"},
		{"#1234567", "", "too long"},
		{"#12345", "", "too short"},
		{"1234567", "", "no leading #"},
		{"#12345g", "", "not hex"},
		{"#12345 ", "", "trailing space"},
		{"red", "", "a color keyword is not accepted"},
		{"rgb(1,2,3)", "", "a CSS function is not accepted"},
		{"#12345\"", "", "a quote must never reach another browser's fillStyle"},
		{"#123456;background:url(x)", "", "no injection through the style"},
		{"\x00#123456", "", "a NUL prefix is not a color"},
	} {
		if got := SanitizePlayerColor(tc.in); got != tc.want {
			t.Errorf("SanitizePlayerColor(%q) = %q, want %q (%s)", tc.in, got, tc.want, tc.why)
		}
	}
}

// An absent color must be absent on the wire, not an empty string: "" is what
// an old client, a replayed session and a player who has not picked one all
// send, and the client reads a missing field as vanilla white-on-blue.
func TestM191ColorAndNameAreOmittedWhenUnset(t *testing.T) {
	bare, err := json.Marshal(PlayerSnapshot{ID: 1, StatID: 0, X: 5, Y: 6, Health: 100})
	if err != nil {
		t.Fatalf("marshal bare snapshot: %v", err)
	}
	if strings.Contains(string(bare), "color") || strings.Contains(string(bare), "name") {
		t.Errorf("an unset color/name must be omitted from the roster, got %s", bare)
	}

	set, err := json.Marshal(PlayerSnapshot{ID: 1, Health: 100, Name: "Ada", Color: "#a1b2c3"})
	if err != nil {
		t.Fatalf("marshal colored snapshot: %v", err)
	}
	if !strings.Contains(string(set), `"color":"#a1b2c3"`) || !strings.Contains(string(set), `"name":"Ada"`) {
		t.Errorf("a set color/name must ride the roster, got %s", set)
	}
}

// The roster is the only carrier, and it has to be BOTH carriers: the snapshot a
// player joins on and every diff after it. A color that rode only the snapshot
// would show a newcomer in vanilla blue to everyone already in the room.
func TestM191ColorRidesTheSnapshotAndEveryDiff(t *testing.T) {
	rm := NewRoomManager(townWorld(t))

	ada := rm.JoinPlayer(1, 0, 0)
	rm.SetPlayerName(ada, "Ada")
	rm.SetPlayerColor(ada, "#ff0000")
	bo := rm.JoinPlayer(1, 0, 0)
	rm.SetPlayerName(bo, "Bo")
	rm.SetPlayerColor(bo, "#00ff00")

	snapshot, ok := rm.Snapshot(ada)
	if !ok {
		t.Fatal("Ada's join snapshot")
	}
	if snapshot.You.Color != "#ff0000" || snapshot.You.Name != "Ada" {
		t.Errorf("`you` must carry the joining player's own color and name, got %+v", snapshot.You)
	}
	assertRosterColors(t, "the join snapshot", snapshot.Players, map[PlayerID]string{ada: "#ff0000", bo: "#00ff00"})

	diffs := rm.StepDiffs(map[PlayerID]PlayerInput{})
	assertRosterColors(t, "Ada's diff", diffs[ada].Players, map[PlayerID]string{ada: "#ff0000", bo: "#00ff00"})
	assertRosterColors(t, "Bo's diff", diffs[bo].Players, map[PlayerID]string{ada: "#ff0000", bo: "#00ff00"})
}

func assertRosterColors(t *testing.T, where string, roster []PlayerSnapshot, want map[PlayerID]string) {
	t.Helper()
	got := map[PlayerID]string{}
	for _, p := range roster {
		got[p.ID] = p.Color
	}
	for id, color := range want {
		if got[id] != color {
			t.Errorf("%s: player %d color = %q, want %q (roster: %+v)", where, id, got[id], color, roster)
		}
	}
}

// The whole feature is only allowed to exist because of this: two rooms whose
// players differ in nothing but color are the same simulation, tick for tick.
func TestM191ColorNeverChangesStateHash(t *testing.T) {
	world := townWorld(t)

	plain := NewRoomManager(world)
	painted := NewRoomManager(world)

	var plainIDs, paintedIDs []PlayerID
	for _, rm := range []*RoomManager{plain, painted} {
		a := rm.JoinPlayer(1, 0, 0)
		b := rm.JoinPlayer(1, 0, 0)
		rm.SetPlayerName(a, "Ada")
		rm.SetPlayerName(b, "Bo")
		rm.Snapshot(a)
		rm.Snapshot(b)
		if rm == plain {
			plainIDs = []PlayerID{a, b}
		} else {
			paintedIDs = []PlayerID{a, b}
		}
	}
	// The ONLY difference between the two managers.
	painted.SetPlayerColor(paintedIDs[0], "#ff00ff")
	painted.SetPlayerColor(paintedIDs[1], "#0088ff")

	for k := 0; k < 200; k++ {
		dx := int16(1)
		if k%2 == 1 {
			dx = -1
		}
		input := PlayerInput{DeltaX: dx}
		plain.StepDiffs(map[PlayerID]PlayerInput{plainIDs[0]: input, plainIDs[1]: {DeltaY: dx}})
		painted.StepDiffs(map[PlayerID]PlayerInput{paintedIDs[0]: input, paintedIDs[1]: {DeltaY: dx}})

		ph, qh := plain.RoomStateHashes(), painted.RoomStateHashes()
		if len(ph) != len(qh) {
			t.Fatalf("tick %d: room count %d vs %d", k, len(ph), len(qh))
		}
		for board, want := range ph {
			if got := qh[board]; got != want {
				t.Fatalf("tick %d board %d: a color changed the simulation — %016x (plain) vs %016x (colored)",
					k, board, want, got)
			}
		}
	}
}

// The M16.15a inverse, shown rather than argued. Two sessions identical except
// that one's players picked colors must produce byte-identical recordings: a
// color is not a stimulus, so nothing about it may be captured, and the
// on-disk schema must not move for it.
func TestM191AColoredSessionRecordsByteIdentically(t *testing.T) {
	if recordVersion != 2 {
		t.Fatalf("recordVersion is %d: M19.1 adds no recorded op, so it must stay at 2 "+
			"(a bump here means a color reached the recording)", recordVersion)
	}

	world := townWorld(t)

	play := func(colored bool) ([]byte, []sessCheckpoint) {
		t.Helper()
		var buf bytes.Buffer
		rm, rec := recordedRoomManager(t, "TOWN", world, &buf)

		ada := rm.JoinPlayer(1, 0, 0)
		rm.SetPlayerName(ada, "Ada")
		bo := rm.JoinPlayer(2, 0, 0)
		rm.SetPlayerName(bo, "Bo")
		if colored {
			rm.SetPlayerColor(ada, "#ff0000")
			rm.SetPlayerColor(bo, "#00c0ff")
		}
		rm.Snapshot(ada)
		rm.Snapshot(bo)

		const totalTicks = 120
		var live []sessCheckpoint
		for k := 0; k < totalTicks; k++ {
			dx := int16(1)
			if k%2 == 1 {
				dx = -1
			}
			rm.StepDiffs(map[PlayerID]PlayerInput{ada: {DeltaX: dx}, bo: {DeltaY: dx}})
			recordCheckpoint(&live, k, rm)
		}
		live = append(live, sessCheckpoint{tick: totalTicks - 1, hashes: rm.RoomStateHashes()})
		rec.Close()
		return buf.Bytes(), live
	}

	plainBytes, plainLive := play(false)
	coloredBytes, coloredLive := play(true)

	if !bytes.Equal(plainBytes, coloredBytes) {
		t.Fatalf("a color reached the recording: %d bytes plain vs %d colored\nplain:   %s\ncolored: %s",
			len(plainBytes), len(coloredBytes),
			firstDifferingLine(plainBytes, coloredBytes), firstDifferingLine(coloredBytes, plainBytes))
	}
	if bytes.Contains(coloredBytes, []byte("#ff0000")) || bytes.Contains(coloredBytes, []byte("#00c0ff")) {
		t.Error("a recording must not contain a player color anywhere")
	}
	assertCheckpointsEqual(t, plainLive, coloredLive)

	// And it still replays: the recording of the colored session reproduces
	// the live session's per-room hashes exactly.
	var replay []sessCheckpoint
	var lastTick int
	replayed, err := ReplaySession(bytes.NewReader(coloredBytes), func(tick int, rm *RoomManager) {
		lastTick = tick
		recordCheckpoint(&replay, tick, rm)
	})
	if err != nil {
		t.Fatalf("replay the colored session: %v", err)
	}
	replay = append(replay, sessCheckpoint{tick: lastTick, hashes: replayed.RoomStateHashes()})
	assertCheckpointsEqual(t, coloredLive, replay)
}

// firstDifferingLine reports the first line of a that differs from b, so a
// failure names the op that leaked rather than a byte offset.
func firstDifferingLine(a, b []byte) string {
	al, bl := bytes.Split(a, []byte("\n")), bytes.Split(b, []byte("\n"))
	for i := range al {
		if i >= len(bl) {
			return string(al[i])
		}
		if !bytes.Equal(al[i], bl[i]) {
			return string(al[i])
		}
	}
	return "(no differing line)"
}
