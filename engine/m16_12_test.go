package zztgo

// M16.12 — multiplayer projection and invariants.
//
// THE CLAIM THIS SWEEP MAKES. A player's `V` experience — the vanilla-behaviour
// contract of PARITY.md §2 — must not change because other people are in the
// world. Everything multiplayer adds is either invisible to that player or is
// one of the owner-approved deviations in PARITY.md §4, and each of those is
// pinned here at its boundary rather than left as a general claim.
//
// HOW IT IS TESTED. The 24 committed oracle scenarios (fixtures/oracle/*.scn)
// are already the certified schedules: M16.3-M16.7a proved them against the
// vanilla oracle, and M16.8 proved Engine and RoomManager agree on them. This
// task runs them a third time — unchanged, through the same driver
// (runM168RoomPass, which grew an afterJoin hook for exactly this) — with two
// more players in the world, and requires the subject's checkpoints to be
// IDENTICAL. Not "close", not "except for the other players": identical,
// because the bystanders are in other rooms and a room is the isolation unit.
//
// Same-room effects cannot be identical by construction — another player is a
// stat, occupies a square, and is a target — so they are covered separately and
// deliberately, one test per declared deviation and per listed invariant.
//
// The randomized schedules at the end are the part no fixed route can give:
// they explore orderings nobody thought to write down, and they record the seed
// that produced them so a failure is reproducible rather than a story.

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
)

// ---------------------------------------------------------------------------
// Part A — the committed schedules, solo versus with company
// ---------------------------------------------------------------------------

// WHY THE BYSTANDERS SHARE THE SUBJECT'S BOARD. "Put them in another room" was
// the first design, and it is not expressible here: every committed oracle
// world runs its schedule on board 0 (`World.Info.CurrentBoard` is 0 for all 24
// — the title monitor and the play board are the same board), ten of them have
// no second board at all, and the scenarios that do cross a passage cross into
// exactly the boards a bystander would have been parked on. So the bystanders
// stand on the subject's own board, parked in the far corner, and the claim
// becomes the sharper one: *another player standing in the room changes nothing
// the subject sees except the square they are standing on.*

// m1612Park is the square hunt: the last free floor tile scanning up from the
// bottom-right corner, keeping well clear of the subject. Far corner, not
// random, because these are creature worlds and the one thing a bystander must
// not do is become the nearest player — vanilla's seek logic would then chase
// them instead, and the divergence would be real rather than a fault.
func m1612Park(engine *Engine, awayFromX, awayFromY int16, taken [][2]int16) (int16, int16, bool) {
	const clearance = 10
	for y := int16(BOARD_HEIGHT); y >= 1; y-- {
		for x := int16(BOARD_WIDTH); x >= 1; x-- {
			if engine.Board.Tiles[x][y].Element != E_EMPTY {
				continue
			}
			if abs16(x-awayFromX) < clearance && abs16(y-awayFromY) < clearance {
				continue
			}
			if engine.GetStatIdAt(x, y) >= 0 {
				continue
			}
			busy := false
			for _, t := range taken {
				if t[0] == x && t[1] == y {
					busy = true
				}
			}
			if !busy {
				return x, y, true
			}
		}
	}
	return 0, 0, false
}

func abs16(v int16) int16 {
	if v < 0 {
		return -v
	}
	return v
}

// m1612Bystanders joins `count` idle players onto the subject's own board and
// reports the squares they occupy, which are the only cells the subject's board
// is allowed to differ in. They are given no input for the whole run, so those
// squares never move.
func m1612Bystanders(t *testing.T, count int, parked *[][2]int16) func(rm *RoomManager, subject PlayerID, startBoard int16) {
	return func(rm *RoomManager, subject PlayerID, startBoard int16) {
		room, ok := rm.Room(startBoard)
		if !ok {
			t.Fatalf("subject's room %d does not exist", startBoard)
		}
		_, subjectStat, _ := rm.PlayerLocation(subject)
		sx := int16(room.Engine.Board.Stats[subjectStat].X)
		sy := int16(room.Engine.Board.Stats[subjectStat].Y)

		for i := 0; i < count; i++ {
			x, y, found := m1612Park(room.Engine, sx, sy, *parked)
			if !found {
				t.Fatalf("no free parking square on board %d for bystander %d", startBoard, i)
			}
			id := rm.JoinPlayer(startBoard, x, y)
			_, statID, _ := rm.PlayerLocation(id)
			// JoinPlayer may push a colliding arrival aside (deviation
			// collision-pushout); record where it actually landed, not where it
			// was asked to go.
			*parked = append(*parked, [2]int16{
				int16(room.Engine.Board.Stats[statID].X),
				int16(room.Engine.Board.Stats[statID].Y),
			})
		}
	}
}

// TestM1612ProjectionUnchangedByOtherPlayers is the sweep's headline: every
// committed schedule, replayed with one and then two more players standing in
// the room, must give the subject the same board, the same HUD counters, and
// the same events — the same certified single-player experience.
//
// StateHash is deliberately NOT compared. It is a hash of the whole board
// INCLUDING every stat, and a second player is a stat: a matching hash would
// mean the extra player was not there. What must match is what the subject
// sees, which is what the board/counter/event comparison covers.
func TestM1612ProjectionUnchangedByOtherPlayers(t *testing.T) {
	for _, name := range m168ScenarioNames {
		name := name
		t.Run(name, func(t *testing.T) {
			scenarioFile := name + ".scn"
			world, ops, _ := parseOracleScenario(t, filepath.Join("..", "fixtures", "oracle", scenarioFile))

			// The Engine pass seeds the room pass, exactly as M16.8 does: the
			// scenario's `boot` span has no room counterpart, so the player's
			// post-title state and the board's timer are carried across.
			initialState, initialTimerTicks, _ := runM168EnginePass(t, scenarioFile, world, ops)

			solo, err := runM168RoomPass(t, scenarioFile, world, ops, initialState, initialTimerTicks, nil, nil)
			if err != nil {
				t.Fatalf("solo pass: %v", err)
			}
			for _, company := range []int{1, 2} {
				company := company
				t.Run(fmt.Sprintf("with-%d-others", company), func(t *testing.T) {
					var parked [][2]int16
					shared, err := runM168RoomPass(t, scenarioFile, world, ops, initialState, initialTimerTicks, nil,
						m1612Bystanders(t, company, &parked))
					if err != nil {
						t.Fatalf("shared pass: %v", err)
					}
					if len(parked) != company {
						t.Fatalf("expected %d parked bystanders, got %v", company, parked)
					}
					if len(solo) != len(shared) {
						t.Fatalf("%s: solo recorded %d checkpoints, shared recorded %d",
							scenarioFile, len(solo), len(shared))
					}
					for i := range solo {
						if solo[i].Label != shared[i].Label {
							t.Fatalf("%s: checkpoint %d label %q vs %q",
								scenarioFile, i, solo[i].Label, shared[i].Label)
						}
						m1612CompareProjection(t, scenarioFile, solo[i], shared[i], parked, name == "nrg")
					}
				})
			}
		})
	}
}

// m1612CompareProjection is m168Compare's board/counter/event comparison with
// two changes: the parked squares are exempted (that is where the other players
// are standing, and seeing them is the whole of what multiplayer adds to this
// board), and each exempted square must actually HOLD a player — otherwise the
// exemption would be a licence to differ anywhere the test happened to park.
// allowPlayerGlyphBlink exempts cells that differ ONLY by the player glyph
// alternating between 0x01 and 0x02 at the same colour. That is the known gap
// M16.12a: `Engine.PlayerCharacter` is engine-global, so a second player in the
// room resets it every tick and the energised blink stops. Passed only for
// nrg.scn, the one scenario that energises, and only for the glyph — every
// other cell, the colour cycle included, is still compared exactly. Remove this
// argument when M16.12a lands; the test tightens on its own.
func m1612CompareProjection(t *testing.T, scenario string, solo, shared m168NamedCheckpoint, parked [][2]int16, allowPlayerGlyphBlink bool) {
	t.Helper()

	if solo.CP.Counters != shared.CP.Counters {
		t.Fatalf("%s checkpoint %s: counters = %+v, want %+v", scenario, solo.Label, shared.CP.Counters, solo.CP.Counters)
	}
	if solo.Tainted || shared.Tainted {
		return
	}

	exempt := func(x, y int16) bool {
		if x == solo.PauseX && y == solo.PauseY {
			return true
		}
		for _, p := range parked {
			if p[0]-1 == x && p[1]-1 == y {
				return true
			}
		}
		return false
	}

	for x := int16(0); x < BOARD_WIDTH; x++ {
		for y := int16(0); y < BOARD_HEIGHT; y++ {
			if exempt(x, y) {
				continue
			}
			if solo.CP.Board[x][y] == shared.CP.Board[x][y] {
				continue
			}
			blink := allowPlayerGlyphBlink &&
				solo.CP.Board[x][y].Color == shared.CP.Board[x][y].Color &&
				isPlayerGlyph(solo.CP.Board[x][y].Ch) && isPlayerGlyph(shared.CP.Board[x][y].Ch)
			if !blink {
				t.Fatalf("%s checkpoint %s: board cell (%d,%d) = %+v, want %+v",
					scenario, solo.Label, x, y, shared.CP.Board[x][y], solo.CP.Board[x][y])
			}
		}
	}
	for _, p := range parked {
		cell := shared.CP.Board[p[0]-1][p[1]-1]
		if cell.Ch != byte(ElementDefs[E_PLAYER].Character) {
			t.Fatalf("%s checkpoint %s: parked bystander square (%d,%d) holds %+v, not a player — "+
				"the exemption must cover a player, not an arbitrary cell", scenario, solo.Label, p[0]-1, p[1]-1, cell)
		}
	}

	soloKeys, sharedKeys := m168EventKeys(solo.CP.Events), m168EventKeys(shared.CP.Events)
	if !reflect.DeepEqual(soloKeys, sharedKeys) {
		t.Fatalf("%s checkpoint %s: events = %v, want %v", scenario, solo.Label, sharedKeys, soloKeys)
	}
}

// isPlayerGlyph covers both phases of ElementPlayerTick's energised alternation.
func isPlayerGlyph(ch byte) bool { return ch == 0x01 || ch == 0x02 }

// ---------------------------------------------------------------------------
// The gap this sweep found — pinned so its fix is detectable
// ---------------------------------------------------------------------------

// TestM1612aEnergizedBlinkIsCancelledByCompany pins M16.12a.
//
// `Engine.PlayerCharacter` is ONE byte on the Engine, and ElementPlayerTick
// writes it on every player's tick: the energised branch flips it, and the
// ordinary branch forces it back to 0x02. Vanilla had exactly one player, so a
// global was harmless. Here a second, unenergised player in the same room
// resets the byte every tick — and the energised player's blink, which vanilla
// draws as an unmissable "you are invincible" signal, simply stops.
//
// The solo half of this test asserts the CORRECT behaviour and must never
// change. The company half asserts the DEFECT, so that M16.12a's fix reddens it
// rather than passing silently: when the blink is made per-player, invert it to
// require both glyphs and drop the Part A exemption above.
func TestM1612aEnergizedBlinkIsCancelledByCompany(t *testing.T) {
	blinkPhases := func(others int) map[byte]int {
		rm := NewRoomManager(testEmptyWorld(t))
		subject := rm.JoinPlayer(1, 10, 10)
		for i := 0; i < others; i++ {
			rm.JoinPlayer(1, 40, 20)
		}
		state, _ := rm.PlayerState(subject)
		state.EnergizerTicks = 60

		room, _ := rm.Room(1)
		_, statID, _ := rm.PlayerLocation(subject)
		seen := map[byte]int{}
		for tick := 0; tick < 8; tick++ {
			rm.StepDiffs(nil)
			stat := room.Engine.Board.Stats[statID]
			_, ch := room.Engine.TileToColorAndChar(int16(stat.X), int16(stat.Y))
			seen[ch]++
		}
		return seen
	}

	solo := blinkPhases(0)
	if solo[0x01] == 0 || solo[0x02] == 0 {
		t.Fatalf("solo: an energised player must alternate 0x01/0x02, saw %v", solo)
	}

	// KNOWN GAP — M16.12a. Assert the defect exactly, so the fix cannot land
	// unnoticed and so this test says what is wrong rather than merely failing.
	withCompany := blinkPhases(1)
	if withCompany[0x01] != 0 {
		t.Fatalf("M16.12a appears to be FIXED (the blink survived company: %v) — "+
			"invert this assertion to require both phases and drop the nrg exemption "+
			"in m1612CompareProjection", withCompany)
	}
	if withCompany[0x02] == 0 {
		t.Fatalf("with company: the player square should be stuck on 0x02, saw %v", withCompany)
	}
}
