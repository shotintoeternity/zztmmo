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
// vanilla oracle, and M16.8 proved Engine and RoomManager agree on them. Part A
// runs them a third time — unchanged, through the same driver (runM168RoomPass,
// which grew an afterJoin hook for exactly this) — with one and then two more
// players standing in the subject's own room, and requires the subject's board,
// HUD and events to match the certified solo run cell for cell, exempting only
// the squares the bystanders stand on.
//
// Part B is the other half of the claim. Effects that reach ACROSS players in a
// shared room cannot be identical by construction — another player is a stat,
// occupies a square, and is a target — so each is pinned at its own boundary:
// one test per listed invariant and per declared multiplayer deviation, with
// TestM1612InvariantCoverage naming which test owns which invariant so an
// invariant cannot quietly lose its evidence.
//
// The randomized schedules at the end are the part no fixed route can give:
// they explore orderings nobody thought to write down, and they record the seed
// that produced them so a failure is reproducible rather than a story.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
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
						m1612CompareProjection(t, scenarioFile, solo[i], shared[i], parked)
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
// The comparison is exact everywhere else, glyph included. It once carried an
// allowPlayerGlyphBlink escape hatch for nrg.scn, the one scenario that
// energises: the blink phase was one byte on the Engine, so a second player
// reset it every tick and the subject's own square differed from solo. M16.12a
// moved the phase onto PlayerState and the exemption came out with it.
func m1612CompareProjection(t *testing.T, scenario string, solo, shared m168NamedCheckpoint, parked [][2]int16) {
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
			t.Fatalf("%s checkpoint %s: board cell (%d,%d) = %+v, want %+v",
				scenario, solo.Label, x, y, shared.CP.Board[x][y], solo.CP.Board[x][y])
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

// ---------------------------------------------------------------------------
// The gap this sweep found, and its fix
// ---------------------------------------------------------------------------

// TestM1612aEnergizedBlinkIsCancelledByCompany covers M16.12a.
//
// The blink phase used to be ONE byte on the Engine, written by
// ElementPlayerTick on every player's tick: the energised branch flipped it,
// the ordinary branch forced it back to 0x02. Vanilla had exactly one player,
// so a single byte was harmless. With company, the unenergised player reset it
// every tick and the energised player's square sat on 0x02 — the unmissable
// "you are invincible" signal simply stopped. M16.12a moved the phase onto
// PlayerState, so this now asserts what vanilla shows: the same alternation
// whether or not anyone else is in the room, and no blink on anyone else.
func TestM1612aEnergizedBlinkIsCancelledByCompany(t *testing.T) {
	// blinkPhases runs the subject energised for eight ticks with `others`
	// unenergised players parked nearby, returning the subject's rendered glyph
	// per tick and every glyph the bystanders rendered.
	blinkPhases := func(others int) (subject []byte, bystanders []byte) {
		rm := NewRoomManager(testEmptyWorld(t))
		subjectID := rm.JoinPlayer(1, 10, 10)
		var otherIDs []PlayerID
		for i := 0; i < others; i++ {
			otherIDs = append(otherIDs, rm.JoinPlayer(1, int16(40+i*2), 20))
		}
		state, _ := rm.PlayerState(subjectID)
		state.EnergizerTicks = 60

		room, _ := rm.Room(1)
		glyphOf := func(id PlayerID) byte {
			_, statID, _ := rm.PlayerLocation(id)
			stat := room.Engine.Board.Stats[statID]
			_, ch := room.Engine.TileToColorAndChar(int16(stat.X), int16(stat.Y))
			return ch
		}
		for tick := 0; tick < 8; tick++ {
			rm.StepDiffs(nil)
			subject = append(subject, glyphOf(subjectID))
			for _, id := range otherIDs {
				bystanders = append(bystanders, glyphOf(id))
			}
		}
		return subject, bystanders
	}

	// Solo is vanilla's blink and must never change: strict alternation,
	// starting from the steady 0x02 that the first energised tick flips.
	solo, _ := blinkPhases(0)
	want := []byte{0x01, 0x02, 0x01, 0x02, 0x01, 0x02, 0x01, 0x02}
	if !bytes.Equal(solo, want) {
		t.Fatalf("solo: energised glyph sequence = %v, want %v", solo, want)
	}

	for _, others := range []int{1, 2} {
		withCompany, bystanders := blinkPhases(others)
		if !bytes.Equal(withCompany, solo) {
			t.Fatalf("with %d other player(s): energised glyph sequence = %v, want the solo %v — "+
				"company must not cancel the energiser blink (M16.12a)", others, withCompany, solo)
		}
		for i, ch := range bystanders {
			if ch != 0x02 {
				t.Fatalf("with %d other player(s): unenergised bystander glyph %d = %#x, want the steady 0x02 — "+
					"the subject's blink must not leak onto anyone else", others, i, ch)
			}
		}
	}
}

// TestM1612aNewcomerSquareReachesTheRoom covers the defect M16.12a's fix
// un-masked (owner decision 2026-07-30: fix it here rather than file it).
//
// RoomManager.Snapshot drained the room's screen-dirty list, and that list is
// shared by everyone in the room. A newcomer's own square is drawn between
// ticks, so their arrival snapshot threw away the one notice the players
// already in the room would have got: they never saw the newcomer appear.
//
// It survived this long because ElementPlayerTick used to write an
// Engine-global glyph byte: with an energized player in the room, every other
// player's tick took the "force it back to \x02" branch and redrew their own
// square, restoring the dropped cell by accident. That is exactly the M16.9
// energizer golden's second player. Per-player blink phases removed the
// accident; the drain is now conditioned on the recipient being the only
// player who could be owed those cells.
func TestM1612aNewcomerSquareReachesTheRoom(t *testing.T) {
	rm := NewRoomManager(testEmptyWorld(t))
	resident := rm.JoinPlayer(1, 10, 10)
	if _, ok := rm.Snapshot(resident); !ok {
		t.Fatal("resident snapshot")
	}
	rm.StepDiffs(nil) // settle: nothing is owed to anyone after this

	newcomer := rm.JoinPlayer(1, 10, 10) // pushed out to a neighbouring square
	if _, ok := rm.Snapshot(newcomer); !ok {
		t.Fatal("newcomer snapshot")
	}

	room, _ := rm.Room(1)
	_, newcomerStat, _ := rm.PlayerLocation(newcomer)
	stat := room.Engine.Board.Stats[newcomerStat]
	wantX, wantY := int16(stat.X)-1, int16(stat.Y)-1

	diffs := rm.StepDiffs(nil)
	var got *ScreenCell
	for i, cell := range diffs[resident].Cells {
		if cell.X == wantX && cell.Y == wantY {
			got = &diffs[resident].Cells[i]
		}
	}
	if got == nil {
		t.Fatalf("the resident's diff (%d cells) never carried the newcomer's square (%d,%d) — "+
			"a player already in the room cannot see anyone arrive", len(diffs[resident].Cells), wantX, wantY)
	}
	if got.Ch != 0x02 {
		t.Fatalf("the newcomer's square arrived as %#x, want the player glyph 0x02", got.Ch)
	}
}

// ---------------------------------------------------------------------------
// Part B — the same-room invariant boundaries
// ---------------------------------------------------------------------------
//
// Part A's claim stops at the edge of the subject's own square. Everything a
// second player can reach ACROSS that edge — their input, their inventory, the
// creature they are nearer to, the stat slot they vacate, the square they die
// on, the passage they take, the flag they set, the tick they share — is a
// separate claim, and each gets its own boundary test below.
//
// These run at the RoomManager level on purpose. The Engine-level siblings
// (TestTwoPlayersIndependentInput, TestDeathRespawnInventoryIsolation,
// TestTigerChasesNearestPlayer) prove the simulation keeps players apart; what
// is unproven above them is the ROUTING — stable PlayerIDs onto shifting stat
// ids, per-player diffs, HUDs and event queues — which is where cross-talk
// would actually appear on a running server.

const (
	m1612SharedBoard  = 1 // "Shared Room" — where the boundary tests play
	m1612PassageBoard = 2 // reached through the passage at (20,12)
	m1612EdgeBoard    = 3 // reached over the east board edge

	m1612PassageColor = 0x0E
	m1612KeyColor     = 0x09 // 9%8 == 1 → the blue key (ElementKeyTouch)

	m1612AmmoX, m1612AmmoY     = 11, 12
	m1612GemX, m1612GemY       = 39, 12
	m1612TorchX, m1612TorchY   = 12, 14
	m1612KeyX, m1612KeyY       = 13, 16
	m1612PassX, m1612PassY     = 20, 12
	m1612EntryX, m1612EntryY   = 5, 5 // the far side of that passage
	m1612StartX, m1612StartY   = 10, 12
	m1612SecondX, m1612SecondY = 40, 12
)

// m1612World is one authored world for every Part B and Part C test: a shared
// room with one of each pickup, a passage, and a live board edge, plus the two
// rooms they lead to. It is deliberately small and hand-placed — every test
// below names the square it means, and a shared fixture keeps those names
// honest across tests instead of each rebuilding a board it half remembers.
func m1612World(t *testing.T) TWorld {
	t.Helper()

	setup := NewEngine()
	setup.Headless = true
	setup.WorldCreate()
	setup.World.BoardCount = 3

	// Board 1 — the shared room.
	setup.World.Info.CurrentBoard = m1612SharedBoard
	setup.BoardCreate()
	fillBoard(setup, TTile{Element: E_EMPTY})
	// BoardCreate leaves a player at stat 0. Clearing it makes every player in
	// these tests a JoinPlayer arrival, so no test accidentally depends on
	// claimablePlayerStat handing the first joiner the authored stat.
	setup.Board.Tiles[setup.Board.Stats[0].X][setup.Board.Stats[0].Y] = TTile{Element: E_EMPTY}
	setup.Board.StatCount = -1
	setup.Board.Tiles[m1612AmmoX][m1612AmmoY] = TTile{Element: E_AMMO, Color: ElementDefs[E_AMMO].Color}
	setup.Board.Tiles[m1612GemX][m1612GemY] = TTile{Element: E_GEM, Color: ElementDefs[E_GEM].Color}
	setup.Board.Tiles[m1612TorchX][m1612TorchY] = TTile{Element: E_TORCH, Color: ElementDefs[E_TORCH].Color}
	setup.Board.Tiles[m1612KeyX][m1612KeyY] = TTile{Element: E_KEY, Color: m1612KeyColor}
	setup.Board.Tiles[m1612PassX][m1612PassY] = TTile{Element: E_PASSAGE, Color: m1612PassageColor}
	setup.AddStat(m1612PassX, m1612PassY, E_PASSAGE, m1612PassageColor, 0, StatTemplateDefault)
	setup.Board.Stats[setup.Board.StatCount].P3 = m1612PassageBoard
	setup.Board.Info.NeighborBoards[3] = m1612EdgeBoard // east edge
	setup.Board.Info.StartPlayerX = m1612StartX
	setup.Board.Info.StartPlayerY = m1612StartY
	setup.Board.Info.MaxShots = 255
	setup.Board.Name = "Shared Room"
	setup.BoardClose()

	// Board 2 — the far side of the passage.
	setup.World.Info.CurrentBoard = m1612PassageBoard
	setup.BoardCreate()
	fillBoard(setup, TTile{Element: E_EMPTY})
	setup.Board.Tiles[setup.Board.Stats[0].X][setup.Board.Stats[0].Y] = TTile{Element: E_EMPTY}
	setup.Board.StatCount = -1
	setup.Board.Tiles[m1612EntryX][m1612EntryY] = TTile{Element: E_PASSAGE, Color: m1612PassageColor}
	setup.AddStat(m1612EntryX, m1612EntryY, E_PASSAGE, m1612PassageColor, 0, StatTemplateDefault)
	setup.Board.Stats[setup.Board.StatCount].P3 = m1612SharedBoard
	setup.Board.Info.StartPlayerX = m1612EntryX
	setup.Board.Info.StartPlayerY = m1612EntryY
	setup.Board.Info.MaxShots = 255
	setup.Board.Name = "Passage Room"
	setup.BoardClose()

	// Board 3 — over the east edge.
	setup.World.Info.CurrentBoard = m1612EdgeBoard
	setup.BoardCreate()
	fillBoard(setup, TTile{Element: E_EMPTY})
	setup.Board.Tiles[setup.Board.Stats[0].X][setup.Board.Stats[0].Y] = TTile{Element: E_EMPTY}
	setup.Board.StatCount = -1
	setup.Board.Info.NeighborBoards[2] = m1612SharedBoard // west edge, back again
	setup.Board.Info.StartPlayerX = 1
	setup.Board.Info.StartPlayerY = 12
	setup.Board.Info.MaxShots = 255
	setup.Board.Name = "Edge Room"
	setup.BoardClose()

	setup.World.Info.CurrentBoard = m1612SharedBoard
	return setup.World
}

// m1612Manager returns a manager on m1612World with `count` players joined on
// the shared board at the given squares.
func m1612Manager(t *testing.T, spawns ...[2]int16) (*RoomManager, []PlayerID) {
	t.Helper()

	rm := NewRoomManager(m1612World(t))
	ids := make([]PlayerID, 0, len(spawns))
	for _, spawn := range spawns {
		id := rm.JoinPlayer(m1612SharedBoard, spawn[0], spawn[1])
		boardID, statID, ok := rm.PlayerLocation(id)
		if !ok {
			t.Fatalf("player %d did not join", id)
		}
		room, _ := rm.Room(boardID)
		stat := room.Engine.Board.Stats[statID]
		if int16(stat.X) != spawn[0] || int16(stat.Y) != spawn[1] {
			t.Fatalf("player %d asked for (%d,%d) and landed on (%d,%d) — the fixture's spawn squares must be free",
				id, spawn[0], spawn[1], stat.X, stat.Y)
		}
		ids = append(ids, id)
	}
	return rm, ids
}

// m1612At reports where a player's stat currently stands.
func m1612At(t *testing.T, rm *RoomManager, id PlayerID) (boardID, x, y int16) {
	t.Helper()

	boardID, statID, ok := rm.PlayerLocation(id)
	if !ok {
		t.Fatalf("player %d is not in the world", id)
	}
	room, ok := rm.Room(boardID)
	if !ok {
		t.Fatalf("player %d's room %d is not live", id, boardID)
	}
	stat := room.Engine.Board.Stats[statID]
	return boardID, int16(stat.X), int16(stat.Y)
}

// TestM1612IndependentInputsInventoryAndAim is the first three DoD invariants
// at the RoomManager level: one tick, two players, opposite inputs; each keeps
// their own pickups; and each keeps their own aim, which is what decides where
// their next shot goes.
func TestM1612IndependentInputsInventoryAndAim(t *testing.T) {
	rm, ids := m1612Manager(t,
		[2]int16{m1612StartX, m1612StartY},   // A, one square west of the ammo
		[2]int16{m1612SecondX, m1612SecondY}, // B, one square west of the gem
	)
	a, b := ids[0], ids[1]

	// One tick, opposite inputs: A east onto the ammo, B... also east onto the
	// gem. Opposite DIRECTIONS come next; what this first tick proves is that
	// two inputs delivered in one map are each applied to their own player.
	rm.StepDiffs(map[PlayerID]PlayerInput{
		a: {DeltaX: 1},
		b: {DeltaX: -1},
	})

	if _, x, y := m1612At(t, rm, a); x != m1612AmmoX || y != m1612AmmoY {
		t.Errorf("A is at (%d,%d), want the ammo square (%d,%d)", x, y, m1612AmmoX, m1612AmmoY)
	}
	if _, x, y := m1612At(t, rm, b); x != m1612SecondX-1 || y != m1612SecondY {
		t.Errorf("B is at (%d,%d), want (%d,%d) — B's input must not be A's", x, y, m1612SecondX-1, m1612SecondY)
	}

	stateA, _ := rm.PlayerState(a)
	stateB, _ := rm.PlayerState(b)
	if stateA.Ammo != 5 {
		t.Errorf("A picked up the ammo but has %d shots, want 5", stateA.Ammo)
	}
	if stateB.Ammo != 0 {
		t.Errorf("B has %d ammo — A's pickup credited the wrong player", stateB.Ammo)
	}

	// B collects the gem; A must not gain a gem or the score that came with it.
	rm.StepDiffs(map[PlayerID]PlayerInput{b: {DeltaX: 1}})
	rm.StepDiffs(map[PlayerID]PlayerInput{b: {DeltaX: 1}})
	if stateB.Gems != 1 {
		t.Fatalf("B has %d gems after walking onto the gem square, want 1", stateB.Gems)
	}
	if stateA.Gems != 0 || stateA.Score != 0 {
		t.Errorf("A gained gems=%d score=%d from B's pickup", stateA.Gems, stateA.Score)
	}
	if stateB.Score == 0 {
		t.Errorf("B's gem paid no score, so the score comparison above proves nothing")
	}

	// Aim. A last moved east, B last moved west then east; give them each a
	// distinct final direction and fire with space.
	rm.StepDiffs(map[PlayerID]PlayerInput{a: {DeltaY: -1}, b: {DeltaY: 1}})
	if stateA.DirX != 0 || stateA.DirY != -1 {
		t.Errorf("A's aim is (%d,%d), want (0,-1)", stateA.DirX, stateA.DirY)
	}
	if stateB.DirX != 0 || stateB.DirY != 1 {
		t.Errorf("B's aim is (%d,%d), want (0,1) — aim is per-player, not per-engine", stateB.DirX, stateB.DirY)
	}

	stateB.Ammo = 5
	_, ax, ay := m1612At(t, rm, a)
	_, bx, by := m1612At(t, rm, b)
	_, statA, _ := rm.PlayerLocation(a)
	_, statB, _ := rm.PlayerLocation(b)
	rm.StepDiffs(map[PlayerID]PlayerInput{a: {Key: ' '}, b: {Key: ' '}})

	// A bullet is added at the end of the stat list, so it also flies during the
	// tick that fired it; what is asserted is the axis and the owner, not the
	// exact square. Ownership is P1 = statId+SHOT_SOURCE_PLAYER_BASE (M16.3), so
	// this cannot be satisfied by the other player's shot.
	room, _ := rm.Room(m1612SharedBoard)
	if !m1612HasOwnedBullet(room.Engine, statA, ax, ay, 0, -1) {
		t.Errorf("A's shot is not flying north from (%d,%d)", ax, ay)
	}
	if !m1612HasOwnedBullet(room.Engine, statB, bx, by, 0, 1) {
		t.Errorf("B's shot is not flying south from (%d,%d)", bx, by)
	}
	if stateA.Ammo != 4 || stateB.Ammo != 4 {
		t.Errorf("ammo after one shot each: A=%d B=%d, want 4 and 4", stateA.Ammo, stateB.Ammo)
	}
}

// m1612HasOwnedBullet reports whether a bullet fired by ownerStat is somewhere
// along the ray leaving (fromX,fromY) in direction (dx,dy).
func m1612HasOwnedBullet(engine *Engine, ownerStat, fromX, fromY, dx, dy int16) bool {
	for step := int16(1); step <= 4; step++ {
		x, y := fromX+dx*step, fromY+dy*step
		if x < 1 || x > BOARD_WIDTH || y < 1 || y > BOARD_HEIGHT {
			return false
		}
		if engine.Board.Tiles[x][y].Element != E_BULLET {
			continue
		}
		if statID := engine.GetStatIdAt(x, y); statID >= 0 &&
			int16(engine.Board.Stats[statID].P1) == ownerStat+SHOT_SOURCE_PLAYER_BASE {
			return true
		}
	}
	return false
}

// TestM1612PerPlayerEventsAndHUDDoNotCrossTalk is the DoD's "per-player
// screens/HUD/events cannot cross-talk", checked where it would actually break:
// the per-player DiffMessage. Each player's HUD must describe THEM — not the
// board's first player, and not whoever the stat ids happen to point at — and
// a per-player event queue must carry only its own player's events.
//
// It is also the boundary test for the manifest's `mode.identity-overlay` row:
// the overlay the browser draws is exactly `DiffMessage.Players` plus "which of
// them is me", and the client resolves that with the ids compared here.
func TestM1612PerPlayerEventsAndHUDDoNotCrossTalk(t *testing.T) {
	rm, ids := m1612Manager(t,
		[2]int16{m1612StartX, m1612StartY},
		[2]int16{m1612SecondX, m1612SecondY},
		[2]int16{m1612TorchX, m1612TorchY + 1}, // C, one square south of the torch
	)
	a, b, c := ids[0], ids[1], ids[2]

	// A takes the ammo, C takes the torch, B does nothing at all.
	diffs := rm.StepDiffs(map[PlayerID]PlayerInput{
		a: {DeltaX: 1},
		c: {DeltaY: -1},
	})

	for _, id := range ids {
		diff, ok := diffs[id]
		if !ok {
			t.Fatalf("player %d received no diff", id)
		}
		if diff.HUD == nil {
			t.Fatalf("player %d received a diff with no HUD", id)
		}
		state, _ := rm.PlayerState(id)
		if diff.HUD.Ammo != state.Ammo || diff.HUD.Torches != state.Torches ||
			diff.HUD.Gems != state.Gems || diff.HUD.Health != state.Health || diff.HUD.Score != state.Score {
			t.Errorf("player %d's HUD is %+v but their state is ammo=%d torches=%d gems=%d health=%d score=%d",
				id, *diff.HUD, state.Ammo, state.Torches, state.Gems, state.Health, state.Score)
		}
		// The identity half: every player on the board appears in the roster,
		// and this player's own entry is the stat the manager says is theirs.
		_, statID, _ := rm.PlayerLocation(id)
		var mine *PlayerSnapshot
		for i := range diff.Players {
			if diff.Players[i].ID == id {
				mine = &diff.Players[i]
			}
		}
		if mine == nil {
			t.Fatalf("player %d is missing from their own diff's player roster %+v", id, diff.Players)
		}
		if mine.StatID != statID {
			t.Errorf("player %d's roster entry names stat %d, the manager says %d", id, mine.StatID, statID)
		}
		if len(diff.Players) != len(ids) {
			t.Errorf("player %d sees %d players in the room, want %d", id, len(diff.Players), len(ids))
		}
	}

	stateA, _ := rm.PlayerState(a)
	stateB, _ := rm.PlayerState(b)
	stateC, _ := rm.PlayerState(c)
	if stateA.Ammo != 5 || stateC.Torches != 1 {
		t.Fatalf("preconditions: A ammo=%d (want 5), C torches=%d (want 1)", stateA.Ammo, stateC.Torches)
	}
	if stateB.Ammo != 0 || stateB.Torches != 0 {
		t.Errorf("idle B collected ammo=%d torches=%d from other players' pickups", stateB.Ammo, stateB.Torches)
	}

	// The per-player event queues. A and C each acted; B did not, so B's queue
	// must be empty of sounds — a pickup sound reaching B would be the exact
	// cross-talk deviation per-player-sound exists to prevent.
	aEvents := rm.DrainPlayerEvents(a)
	bEvents := rm.DrainPlayerEvents(b)
	cEvents := rm.DrainPlayerEvents(c)
	if !eventsHaveAnySound(aEvents) {
		t.Errorf("A's ammo pickup produced no sound of its own: %#v", aEvents)
	}
	if !eventsHaveAnySound(cEvents) {
		t.Errorf("C's torch pickup produced no sound of its own: %#v", cEvents)
	}
	if eventsHaveAnySound(bEvents) {
		t.Errorf("idle B received another player's sound: %#v", bEvents)
	}
	for _, ev := range append(append([]Event{}, aEvents...), cEvents...) {
		if sound, ok := ev.(SoundEvent); ok && sound.StatId < 0 {
			t.Errorf("a pickup sound was queued room-wide (StatId %d) but delivered per-player", sound.StatId)
		}
	}
}

// TestM1612NearestPlayerTargetingRetargets pins the one creature behaviour that
// genuinely changes in a shared room: NearestPlayer (gamevars.go) picks the
// closest E_PLAYER stat, so who a creature chases — and whose energizer makes
// it flee — depends on who else is standing there.
//
// The tiger, both players and the seek target all share row 12, which makes
// CalcDirectionSeek draw-free: its `e.Random(2) < 1 || target.Y == y` test takes
// the second branch whatever the RNG returns, so the step below is decided by
// the geometry alone (the same device the M16.5 oracle worlds use).
func TestM1612NearestPlayerTargetingRetargets(t *testing.T) {
	const tigerX, tigerY = 30, 12

	// A at (25,12) is 5 away; B at (50,12) is 20 away.
	newRun := func(t *testing.T) (*RoomManager, PlayerID, PlayerID, *Room, int16) {
		t.Helper()
		rm, ids := m1612Manager(t, [2]int16{25, 12}, [2]int16{50, 12})
		room, _ := rm.Room(m1612SharedBoard)
		room.Engine.AddStat(tigerX, tigerY, E_TIGER, int16(ElementDefs[E_TIGER].Color), 1, StatTemplateDefault)
		tiger := room.Engine.Board.StatCount
		room.Engine.Board.Stats[tiger].P1 = 10 // 10 < Random(10) is never true: always seek
		room.Engine.Board.Stats[tiger].P2 = 0  // never shoot
		room.Engine.Board.Tiles[tigerX][tigerY] = TTile{Element: E_TIGER, Color: ElementDefs[E_TIGER].Color}
		return rm, ids[0], ids[1], room, tiger
	}

	tigerX2 := func(room *Room, tiger int16) int16 { return int16(room.Engine.Board.Stats[tiger].X) }

	t.Run("chases the nearer player", func(t *testing.T) {
		rm, _, _, room, tiger := newRun(t)
		rm.StepDiffs(nil)
		if got := tigerX2(room, tiger); got >= tigerX {
			t.Fatalf("tiger x=%d after one tick (started at %d) — it must step west toward the nearer player at x=25", got, tigerX)
		}
	})

	t.Run("retargets when the nearer player leaves", func(t *testing.T) {
		rm, a, _, room, tiger := newRun(t)
		if !rm.LeavePlayer(a) {
			t.Fatal("A did not leave")
		}
		// LeavePlayer removed a stat below the tiger's, so the tiger's own id
		// shifted; find it again rather than trusting the old index.
		tiger = -1
		for i := int16(0); i <= room.Engine.Board.StatCount; i++ {
			stat := room.Engine.Board.Stats[i]
			if room.Engine.Board.Tiles[stat.X][stat.Y].Element == E_TIGER {
				tiger = i
			}
		}
		if tiger < 0 {
			t.Fatal("the tiger did not survive A's departure")
		}
		rm.StepDiffs(nil)
		if got := tigerX2(room, tiger); got <= tigerX {
			t.Fatalf("tiger x=%d after A left (started at %d) — with only B left it must step east toward x=50", got, tigerX)
		}
	})

	t.Run("only the nearest player's energizer inverts the seek", func(t *testing.T) {
		// Energizing the FAR player must change nothing: CalcDirectionSeek reads
		// PlayerFor(NearestPlayer(...)), so B's invulnerability is B's alone.
		rm, a, b, room, tiger := newRun(t)
		stateB, _ := rm.PlayerState(b)
		stateB.EnergizerTicks = 60
		rm.StepDiffs(nil)
		if got := tigerX2(room, tiger); got >= tigerX {
			t.Errorf("tiger x=%d — the FAR player's energizer must not turn it away from the near one", got)
		}

		// Energizing the NEAR player must reverse it.
		rm, a, _, room, tiger = newRun(t)
		stateA, _ := rm.PlayerState(a)
		stateA.EnergizerTicks = 60
		rm.StepDiffs(nil)
		if got := tigerX2(room, tiger); got <= tigerX {
			t.Errorf("tiger x=%d — the nearest player is energized, so it must flee east", got)
		}
	})
}

// TestM1612StatReindexingKeepsEachPlayerTheirOwnState is the join/leave/rejoin
// and reindexing invariant. Engine stat ids close up when a stat is removed
// (RemoveStat shifts every higher stat down one), so a player who leaves from
// the middle of the room renumbers everybody above them. If RoomManager's
// PlayerID→statID map did not follow, the survivors would silently inherit each
// other's inventory — the worst possible failure, because nothing crashes.
func TestM1612StatReindexingKeepsEachPlayerTheirOwnState(t *testing.T) {
	rm, ids := m1612Manager(t,
		[2]int16{10, 12},
		[2]int16{20, 20},
		[2]int16{30, 12},
		[2]int16{45, 20},
	)

	// Give each player a fingerprint no other player has.
	want := map[PlayerID]int16{}
	for i, id := range ids {
		state, _ := rm.PlayerState(id)
		state.Ammo = int16(11 + i)
		state.Gems = int16(21 + i)
		want[id] = state.Ammo
	}

	check := func(stage string) {
		t.Helper()
		for _, id := range ids {
			if _, live := rm.PlayerState(id); !live {
				continue
			}
			state, _ := rm.PlayerState(id)
			if state.Ammo != want[id] {
				t.Fatalf("%s: player %d holds %d ammo, want %d — stat ids and player states have come apart",
					stage, id, state.Ammo, want[id])
			}
			boardID, statID, _ := rm.PlayerLocation(id)
			room, _ := rm.Room(boardID)
			stat := room.Engine.Board.Stats[statID]
			if room.Engine.Board.Tiles[stat.X][stat.Y].Element != E_PLAYER {
				t.Fatalf("%s: player %d's stat %d does not stand on a player tile", stage, id, statID)
			}
			if room.Engine.PlayerFor(statID).Ammo != want[id] {
				t.Fatalf("%s: the engine's PlayerFor(%d) holds %d ammo, want player %d's %d",
					stage, statID, room.Engine.PlayerFor(statID).Ammo, id, want[id])
			}
		}
	}

	check("after joining")

	// Remove the SECOND of four: the two above it must shift down by one.
	if !rm.LeavePlayer(ids[1]) {
		t.Fatal("the second player did not leave")
	}
	delete(want, ids[1])
	ids = append(ids[:1], ids[2:]...)
	check("after a middle player leaves")

	rm.StepDiffs(map[PlayerID]PlayerInput{ids[0]: {DeltaY: 1}})
	check("after a tick")

	// A fresh joiner must not inherit the vacated slot's state.
	joiner := rm.JoinPlayer(m1612SharedBoard, 20, 20)
	fresh, _ := rm.PlayerState(joiner)
	if fresh.Ammo != 0 || fresh.Gems != 0 || fresh.Health != 100 {
		t.Errorf("a new player inherited ammo=%d gems=%d health=%d from the slot they were given",
			fresh.Ammo, fresh.Gems, fresh.Health)
	}
	ids = append(ids, joiner)
	want[joiner] = 0
	check("after a rejoin into the vacated slot")
}

// TestM1612DeathIsConfinedToTheDyingPlayer pins deviation mp-respawn at its
// multiplayer boundary. Vanilla's death ends the game; here it is a respawn,
// and the thing that must be true in a shared room is that it ends nothing for
// anybody else: the room keeps ticking, the survivor keeps their inventory and
// their square, and the invulnerability the respawn grants is the dead player's
// alone.
func TestM1612DeathIsConfinedToTheDyingPlayer(t *testing.T) {
	rm, ids := m1612Manager(t, [2]int16{10, 12}, [2]int16{40, 20})
	dying, survivor := ids[0], ids[1]

	stateDying, _ := rm.PlayerState(dying)
	stateSurvivor, _ := rm.PlayerState(survivor)
	stateDying.Health = 10
	stateDying.Score = 250
	stateSurvivor.Ammo = 9
	stateSurvivor.Score = 700

	_, statDying, _ := rm.PlayerLocation(dying)
	room, _ := rm.Room(m1612SharedBoard)
	room.Engine.DamageStat(statDying)

	if stateDying.Health != 0 || stateDying.RespawnTicks != RESPAWN_TICKS {
		t.Fatalf("the dying player is at health=%d respawnTicks=%d, want 0 and %d",
			stateDying.Health, stateDying.RespawnTicks, RESPAWN_TICKS)
	}
	if stateDying.Score != 250-RESPAWN_SCORE_PENALTY {
		t.Errorf("death took %d score, want the mp-respawn penalty of %d",
			250-stateDying.Score, RESPAWN_SCORE_PENALTY)
	}

	// The survivor walks back and forth for the whole countdown. A room that
	// froze on a death — vanilla's behaviour — would strand them.
	_, startX, startY := m1612At(t, rm, survivor)
	sawRespawn := false
	invulnAtRespawn := int16(-1)
	for tick := 0; tick < RESPAWN_TICKS+4; tick++ {
		delta := int16(1)
		if tick%2 == 1 {
			delta = -1
		}
		diffs := rm.StepDiffs(map[PlayerID]PlayerInput{survivor: {DeltaX: delta}})
		_, x, y := m1612At(t, rm, survivor)
		wantX := startX
		if tick%2 == 0 {
			wantX = startX + 1
		}
		if x != wantX || y != startY {
			t.Fatalf("tick %d: the survivor is at (%d,%d), want (%d,%d) — the room stopped ticking during another player's death",
				tick, x, y, wantX, startY)
		}
		if _, ok := findEvent(diffs[survivor].Events, "respawn"); ok && !sawRespawn {
			sawRespawn = true
			// Read the grant on the tick it is made: it is EnergizerTicks, so it
			// starts counting down again immediately.
			invulnAtRespawn = stateDying.EnergizerTicks
		}
	}

	if !sawRespawn {
		t.Errorf("no respawn event ever reached the room")
	}
	if stateDying.Health != 100 {
		t.Errorf("the respawned player is at health=%d, want 100", stateDying.Health)
	}
	if invulnAtRespawn != RESPAWN_INVULN_TICKS {
		t.Errorf("the respawn granted %d invulnerability ticks, want %d",
			invulnAtRespawn, RESPAWN_INVULN_TICKS)
	}
	if stateSurvivor.EnergizerTicks != 0 {
		t.Errorf("the survivor received %d invulnerability ticks from someone else's respawn",
			stateSurvivor.EnergizerTicks)
	}
	if stateSurvivor.Ammo != 9 || stateSurvivor.Score != 700 || stateSurvivor.Health != 100 {
		t.Errorf("the survivor's run changed across another player's death: ammo=%d score=%d health=%d",
			stateSurvivor.Ammo, stateSurvivor.Score, stateSurvivor.Health)
	}
}

// TestM1612PassageAndEdgeMoveOnlyTheTraveler is the passages/edges invariant.
// Both routes out of a board emit a TransferEvent that RoomManager applies to
// one player; the room they leave must keep running for whoever stays, and the
// traveler's own run must arrive intact.
func TestM1612PassageAndEdgeMoveOnlyTheTraveler(t *testing.T) {
	for _, route := range []struct {
		name        string
		start       [2]int16
		input       PlayerInput
		wantBoard   int16
		wantX       int16
		wantY       int16
		description string
	}{
		{
			name:      "passage",
			start:     [2]int16{m1612PassX - 1, m1612PassY},
			input:     PlayerInput{DeltaX: 1},
			wantBoard: m1612PassageBoard,
			wantX:     m1612EntryX,
			wantY:     m1612EntryY,
		},
		{
			name:      "board edge",
			start:     [2]int16{BOARD_WIDTH, 12},
			input:     PlayerInput{DeltaX: 1},
			wantBoard: m1612EdgeBoard,
			wantX:     1,
			wantY:     12,
		},
	} {
		t.Run(route.name, func(t *testing.T) {
			rm, ids := m1612Manager(t, route.start, [2]int16{m1612StartX, m1612StartY})
			traveler, stayer := ids[0], ids[1]

			state, _ := rm.PlayerState(traveler)
			state.Ammo = 6
			state.Gems = 4
			_, _, stayerY := m1612At(t, rm, stayer)

			rm.StepDiffs(map[PlayerID]PlayerInput{traveler: route.input})

			boardID, x, y := m1612At(t, rm, traveler)
			if boardID != route.wantBoard {
				t.Fatalf("the traveler is on board %d, want %d", boardID, route.wantBoard)
			}
			if x != route.wantX || y != route.wantY {
				t.Errorf("the traveler arrived at (%d,%d), want (%d,%d)", x, y, route.wantX, route.wantY)
			}
			after, _ := rm.PlayerState(traveler)
			if after.Ammo != 6 || after.Gems != 4 {
				t.Errorf("the traveler's inventory did not cross with them: ammo=%d gems=%d", after.Ammo, after.Gems)
			}

			stayerBoard, _, sy := m1612At(t, rm, stayer)
			if stayerBoard != m1612SharedBoard || sy != stayerY {
				t.Errorf("the player who stayed is on board %d at y=%d, want board %d at y=%d",
					stayerBoard, sy, m1612SharedBoard, stayerY)
			}

			// Both rooms are live and both players keep receiving their own.
			diffs := rm.StepDiffs(map[PlayerID]PlayerInput{stayer: {DeltaY: 1}})
			if diffs[stayer].BoardID != m1612SharedBoard {
				t.Errorf("the stayer's diff came from board %d, want %d", diffs[stayer].BoardID, m1612SharedBoard)
			}
			if diffs[traveler].BoardID != route.wantBoard {
				t.Errorf("the traveler's diff came from board %d, want %d", diffs[traveler].BoardID, route.wantBoard)
			}
			if _, _, sy := m1612At(t, rm, stayer); sy != stayerY+1 {
				t.Errorf("the stayer is at y=%d after moving south, want %d — their room stopped ticking", sy, stayerY+1)
			}
		})
	}
}

// TestM1612RoomFreezeThawPreservesTheBoard is the room freeze/thaw invariant
// for board content. TestRoomManagerFlagSurvivesFreezeThaw already covers
// world-scope flags; what is unproven beside it is that the BOARD a room was
// playing survives being put away and taken out again — a freeze that dropped
// the room's tiles would silently restock every item the moment the last player
// stepped out.
func TestM1612RoomFreezeThawPreservesTheBoard(t *testing.T) {
	rm, ids := m1612Manager(t, [2]int16{m1612StartX, m1612StartY}, [2]int16{40, 20})
	collector, other := ids[0], ids[1]

	rm.StepDiffs(map[PlayerID]PlayerInput{collector: {DeltaX: 1}})
	room, _ := rm.Room(m1612SharedBoard)
	if elem := room.Engine.Board.Tiles[m1612AmmoX][m1612AmmoY].Element; elem == E_AMMO {
		t.Fatal("precondition: the ammo was not collected")
	}

	// One player leaving is not a freeze.
	rm.LeavePlayer(collector)
	if rm.ActiveRoomCount() != 1 {
		t.Fatalf("the room froze with a player still in it (%d live rooms)", rm.ActiveRoomCount())
	}
	if _, ok := rm.Room(m1612SharedBoard); !ok {
		t.Fatal("the shared room disappeared while a player was still in it")
	}

	// The last player leaving is.
	rm.LeavePlayer(other)
	if rm.ActiveRoomCount() != 0 {
		t.Fatalf("the empty room stayed live: %d live rooms", rm.ActiveRoomCount())
	}

	// Thaw it by joining again. The collected ammo must still be gone, and the
	// items nobody touched must still be there.
	rejoined := rm.JoinPlayer(m1612SharedBoard, m1612StartX, m1612StartY)
	room, ok := rm.Room(m1612SharedBoard)
	if !ok {
		t.Fatal("the room did not thaw")
	}
	if elem := room.Engine.Board.Tiles[m1612AmmoX][m1612AmmoY].Element; elem == E_AMMO {
		t.Errorf("the thawed board restocked the collected ammo at (%d,%d)", m1612AmmoX, m1612AmmoY)
	}
	if elem := room.Engine.Board.Tiles[m1612GemX][m1612GemY].Element; elem != E_GEM {
		t.Errorf("the thawed board lost the untouched gem at (%d,%d): element %d", m1612GemX, m1612GemY, elem)
	}
	if elem := room.Engine.Board.Tiles[m1612PassX][m1612PassY].Element; elem != E_PASSAGE {
		t.Errorf("the thawed board lost the passage at (%d,%d): element %d", m1612PassX, m1612PassY, elem)
	}

	// And the rejoiner plays normally in it.
	rm.StepDiffs(map[PlayerID]PlayerInput{rejoined: {DeltaY: 1}})
	if _, _, y := m1612At(t, rm, rejoined); y != m1612StartY+1 {
		t.Errorf("the rejoined player is at y=%d, want %d — the thawed room does not tick", y, m1612StartY+1)
	}
}

// TestM1612SimultaneousActionsResolveInStableOrder is the DoD's "simultaneous
// actions in stable order". Two players reach for the same gem on the same
// tick; exactly one may have it, and WHICH one must not depend on Go's
// randomized map iteration over the input map or the player map.
//
// The rule the engine actually implements is stat order: GameStepWithInputs
// walks stats 0..StatCount, so the lower stat id acts first and takes the gem.
// The test asserts that outcome AND that repeated runs agree byte for byte,
// because a stable-but-wrong winner and an unstable winner fail differently.
func TestM1612SimultaneousActionsResolveInStableOrder(t *testing.T) {
	type outcome struct {
		hash   uint64
		firstX int16
		gems   [2]int16
	}

	run := func(t *testing.T, reversed bool) outcome {
		t.Helper()
		// Both players stand beside the gem at (39,12): first to its west,
		// second to its east. Each steps onto it in the same tick.
		rm, ids := m1612Manager(t, [2]int16{m1612GemX - 1, m1612GemY}, [2]int16{m1612GemX + 1, m1612GemY})
		inputs := map[PlayerID]PlayerInput{}
		if reversed {
			inputs[ids[1]] = PlayerInput{DeltaX: -1}
			inputs[ids[0]] = PlayerInput{DeltaX: 1}
		} else {
			inputs[ids[0]] = PlayerInput{DeltaX: 1}
			inputs[ids[1]] = PlayerInput{DeltaX: -1}
		}
		rm.StepDiffs(inputs)

		room, _ := rm.Room(m1612SharedBoard)
		first, _ := rm.PlayerState(ids[0])
		second, _ := rm.PlayerState(ids[1])
		_, x, _ := m1612At(t, rm, ids[0])
		return outcome{hash: StateHash(room.Engine), firstX: x, gems: [2]int16{first.Gems, second.Gems}}
	}

	want := run(t, false)
	if want.gems[0] != 1 || want.gems[1] != 0 {
		t.Fatalf("gems went %v — the lower stat id ticks first and must take the contended gem", want.gems)
	}
	if want.firstX != m1612GemX {
		t.Fatalf("the winner is at x=%d, want the gem square %d", want.firstX, m1612GemX)
	}

	// Twenty runs, alternating the order the inputs are inserted into the map.
	// Go randomizes map iteration per range, so a step that consumed inputs in
	// map order rather than stat order would diverge here within a few rounds.
	for i := 0; i < 20; i++ {
		got := run(t, i%2 == 1)
		if got != want {
			t.Fatalf("run %d resolved differently: %+v, want %+v — simultaneous actions are not in a stable order", i, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// The coverage index
// ---------------------------------------------------------------------------

// m1612Invariants maps each invariant the M16.12 DoD lists to the tests that
// certify it. Several were already pinned by M2.x/M4.x/M7.x/M8.x and are
// GATHERED here rather than rewritten — a second copy of an existing proof adds
// no evidence, but an invariant whose only proof is a test nobody remembers
// owns it is one rename away from being unproven. TestM1612InvariantCoverage
// turns that into a build failure.
//
// The PARITY.md §4 deviations NOT listed below are the ones whose boundary is
// not a shared room, and each is owned elsewhere by name: snapshot-player-drop
// and account-sidecar-restore belong to the persistence journey (M16.15),
// score-on-quit and omitted-game-speed to the title/quit surface (M4.3, pinned
// by M16.9/M16.10), wasd-removed and scroll-removal-timing to the browser
// control and modal sweeps (M16.10, M17.4), and mobile-touch-gap is an open gap
// task (M16.18a). Listing them here would claim evidence this task does not
// produce.
var m1612Invariants = []struct {
	invariant string
	tests     []string
}{
	{"independent inputs", []string{
		"TestTwoPlayersIndependentInput",
		"TestM1612IndependentInputsInventoryAndAim",
	}},
	{"independent inventory", []string{
		"TestDeathRespawnInventoryIsolation",
		"TestM1612IndependentInputsInventoryAndAim",
		"TestM1612PerPlayerEventsAndHUDDoNotCrossTalk",
	}},
	{"independent shoot direction", []string{
		"TestM1612IndependentInputsInventoryAndAim",
	}},
	{"independent pause (deviation per-player-modal-freeze)", []string{
		"TestM311PauseIsPerPlayer",
		"TestM311MovementResumesPlay",
	}},
	{"independent modals (deviation per-player-modal-freeze)", []string{
		"TestScrollFreezesReaderUntilDismissed",
		"TestVendorScrollTargetsOnlyToucher",
		"TestM43RoomQuitLeavesOthersUndisturbed",
		"TestM43DeadPlayerQuitDoesNotFreezeRoom",
		"TestM311SaveEmitsEventAndNeverBlocks",
	}},
	{"independent sound (deviation per-player-sound)", []string{
		"TestM74PerPlayerSoundAttribution",
		"TestM311SoundToggleIsPerPlayer",
		"TestM311DeathDoesNotUnmute",
	}},
	{"independent events, HUD and identity", []string{
		"TestM1612PerPlayerEventsAndHUDDoNotCrossTalk",
		"TestM168MisroutedPerPlayerEventFailsClosed",
		"TestM168aTransferEventReachesOnlyTheTraveler",
	}},
	{"nearest-player targeting", []string{
		"TestTigerChasesNearestPlayer",
		"TestM1612NearestPlayerTargetingRetargets",
	}},
	{"collision (deviation collision-pushout)", []string{
		"TestM71SecondPlayerDisplacedFromOccupiedStart",
		"TestM71ClobberedPlayerTileStillCountsOccupied",
		"TestM43bTwoPlayersReenterSameSquare",
		"TestM43bTwoPlayersRespawnSameSquare",
		"TestM43bNoOpenSquareStaysPut",
	}},
	{"stat reindexing", []string{
		"TestM1612StatReindexingKeepsEachPlayerTheirOwnState",
	}},
	{"death, respawn and invulnerability (deviation mp-respawn)", []string{
		"TestDeathRespawnInventoryIsolation",
		"TestM1612DeathIsConfinedToTheDyingPlayer",
		"TestOopEndgameIsolatesOtherPlayers",
	}},
	{"friendly-fire policy (deviation friendly-fire-policy)", []string{
		"TestPointBlankRespectsTargetEnergizer",
		"TestPointBlankDamagesUnenergizedTargetWithFriendlyFire",
		"TestPointBlankNoDamageWithoutFriendlyFire",
		"TestPointBlankNeverSelfDamage",
		"TestPointBlankCreatureVsEnergizedPlayer",
	}},
	{"passages and board edges", []string{
		"TestM1612PassageAndEdgeMoveOnlyTheTraveler",
		"TestM168aTransferEventReachesOnlyTheTraveler",
		"TestM72TorchLightArrivesWithTransferredPlayer",
	}},
	{"shared world flags (deviation shared-world-flags)", []string{
		"TestRoomManagerSharesWorldFlagsAcrossLiveRooms",
		"TestRoomManagerFlagVisibleToLaterRoomSameTick",
		"TestRoomManagerFlagSurvivesFreezeThaw",
	}},
	{"room freeze and thaw", []string{
		"TestM1612RoomFreezeThawPreservesTheBoard",
		"TestRoomManagerFlagSurvivesFreezeThaw",
	}},
	{"join, leave and rejoin", []string{
		"TestM1612StatReindexingKeepsEachPlayerTheirOwnState",
		"TestReconnectResumeWithinGracePreservesRun",
		"TestReconnectGraceExpiryRemovesPlayer",
		"TestReconnectUnknownTokenJoinsFresh",
		"TestReconnectSecondConnectionWins",
	}},
	{"simultaneous actions in a stable order", []string{
		"TestM1612SimultaneousActionsResolveInStableOrder",
		"TestM1612RandomizedSchedules",
	}},
	{"the certified solo projection is unchanged by company", []string{
		"TestM1612ProjectionUnchangedByOtherPlayers",
	}},
}

// TestM1612InvariantCoverage fails closed if an invariant's evidence goes
// missing. It reuses the parity validator's own test-name scanner, so "the test
// exists" means the same thing here as it does in fixtures/parity/manifest.json.
func TestM1612InvariantCoverage(t *testing.T) {
	goTests := existingGoTestNames(t)
	for _, entry := range m1612Invariants {
		if len(entry.tests) == 0 {
			t.Errorf("invariant %q lists no test", entry.invariant)
		}
		for _, name := range entry.tests {
			if !goTests[name] {
				t.Errorf("invariant %q names %s, which does not exist — the invariant has lost its evidence "+
					"(point it at the test that replaced it, do not delete the row)", entry.invariant, name)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Part C — deterministic randomized schedules
// ---------------------------------------------------------------------------
//
// Every test above drives a route somebody thought of. The interleavings that
// break a shared room are the ones nobody thought of: two players entering the
// same passage on the tick a third dies, a pause landing between a pickup and
// its sound. These schedules go looking for those, and they stay evidence
// rather than anecdote because the seed is part of the test name — a failure
// names the exact `-run` that reproduces it.

// m1612Rand is xorshift64*: a few lines, no dependency, and no relationship to
// the engine's own RNG, which these schedules must not perturb (CLAUDE.md rule
// 2 — the simulation's randomness stays the engine's seeded generator, and the
// schedule generator lives entirely outside it). math/rand is deliberately
// avoided so nothing in this package can reach for the global one.
type m1612Rand struct{ state uint64 }

func newM1612Rand(seed uint64) *m1612Rand {
	if seed == 0 {
		seed = 0x9E3779B97F4A7C15 // xorshift is stuck at zero
	}
	return &m1612Rand{state: seed}
}

func (r *m1612Rand) next() uint64 {
	x := r.state
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	r.state = x
	return x * 2685821657736338717
}

func (r *m1612Rand) intn(n int) int { return int(r.next() % uint64(n)) }

// m1612Vocabulary is what a random player may do. Quit and escape are excluded
// on purpose: they open a modal whose reply the schedule would have to invent,
// and TestM43RoomQuitLeavesOthersUndisturbed already owns that boundary.
var m1612Vocabulary = []PlayerInput{
	{},          // idle
	{DeltaX: 1}, // walk
	{DeltaX: -1},
	{DeltaY: 1},
	{DeltaY: -1},
	{DeltaX: 1, Shift: true}, // shoot in a named direction
	{DeltaX: -1, Shift: true},
	{DeltaY: 1, Shift: true},
	{DeltaY: -1, Shift: true},
	{Key: ' '}, // shoot the way you last moved
	{Key: 'T'}, // torch
	{Key: 'P'}, // pause (per-player)
	{Key: 'B'}, // sound toggle (per-player)
}

// m1612Schedule builds ticks x players of input from one seed. Building the
// whole schedule up front — rather than drawing inside the run — is what makes
// the two passes of a seed identical by construction rather than by luck.
func m1612Schedule(seed uint64, players, ticks int) [][]PlayerInput {
	r := newM1612Rand(seed)
	schedule := make([][]PlayerInput, ticks)
	for tick := range schedule {
		row := make([]PlayerInput, players)
		for p := range row {
			row[p] = m1612Vocabulary[r.intn(len(m1612Vocabulary))]
		}
		schedule[tick] = row
	}
	return schedule
}

// m1612RunSchedule plays a schedule and returns a transcript: one line per
// tick, carrying every room's StateHash and, for each player, their room,
// square, HUD and the events they were handed. Two runs of one seed must
// produce equal transcripts line for line, and the first differing line is the
// tick to look at.
//
// It also checks, on every tick, the invariants that must hold under ANY
// schedule — which is the half a replay comparison cannot give, since two
// identically-wrong runs agree perfectly.
func m1612RunSchedule(t *testing.T, schedule [][]PlayerInput, check bool) []string {
	t.Helper()

	spawns := [][2]int16{{10, 12}, {30, 20}, {50, 8}}
	players := len(schedule[0])
	if players > len(spawns) {
		t.Fatalf("m1612RunSchedule has %d spawn squares, the schedule wants %d", len(spawns), players)
	}
	rm, ids := m1612Manager(t, spawns[:players]...)

	transcript := make([]string, 0, len(schedule))
	for tick, row := range schedule {
		inputs := make(map[PlayerID]PlayerInput, players)
		for i, id := range ids {
			inputs[id] = row[i]
		}
		diffs := rm.StepDiffs(inputs)

		line := fmt.Sprintf("tick %3d rooms=%v", tick, m1612HashLine(rm))
		occupied := map[[3]int16]PlayerID{}
		for _, id := range ids {
			boardID, statID, live := rm.PlayerLocation(id)
			if !live {
				line += fmt.Sprintf(" | p%d gone", id)
				continue
			}
			room, ok := rm.Room(boardID)
			if !ok {
				t.Fatalf("tick %d: player %d is on board %d, which is not live", tick, id, boardID)
			}
			stat := room.Engine.Board.Stats[statID]
			x, y := int16(stat.X), int16(stat.Y)
			state, _ := rm.PlayerState(id)
			events := m168EventKeys(ProtocolEvents(rm.DrainPlayerEvents(id)))
			line += fmt.Sprintf(" | p%d b%d (%d,%d) h%d a%d g%d t%d s%d k%v paused=%v ev%v",
				id, boardID, x, y, state.Health, state.Ammo, state.Gems, state.Torches,
				state.Score, state.Keys, state.Paused, events)

			if !check {
				continue
			}
			if elem := room.Engine.Board.Tiles[x][y].Element; elem != E_PLAYER {
				t.Fatalf("tick %d: player %d's stat %d stands on element %d, not a player tile",
					tick, id, statID, elem)
			}
			if other, dup := occupied[[3]int16{boardID, x, y}]; dup {
				t.Fatalf("tick %d: players %d and %d are both on board %d square (%d,%d)",
					tick, other, id, boardID, x, y)
			}
			occupied[[3]int16{boardID, x, y}] = id

			diff, ok := diffs[id]
			if !ok {
				t.Fatalf("tick %d: player %d received no diff", tick, id)
			}
			if diff.BoardID != boardID {
				t.Fatalf("tick %d: player %d is on board %d but was sent board %d's diff",
					tick, id, boardID, diff.BoardID)
			}
			if diff.HUD == nil {
				t.Fatalf("tick %d: player %d received a diff with no HUD", tick, id)
			}
			if diff.HUD.Health != state.Health || diff.HUD.Ammo != state.Ammo ||
				diff.HUD.Gems != state.Gems || diff.HUD.Torches != state.Torches ||
				diff.HUD.Score != state.Score || diff.HUD.Keys != state.Keys {
				t.Fatalf("tick %d: player %d's HUD %+v does not describe them (h%d a%d g%d t%d s%d)",
					tick, id, *diff.HUD, state.Health, state.Ammo, state.Gems, state.Torches, state.Score)
			}
			var mine *PlayerSnapshot
			for i := range diff.Players {
				if diff.Players[i].ID == id {
					mine = &diff.Players[i]
				}
			}
			if mine == nil {
				t.Fatalf("tick %d: player %d is absent from their own roster %+v", tick, id, diff.Players)
			}
			if mine.StatID != statID || mine.X != x || mine.Y != y {
				t.Fatalf("tick %d: player %d's roster entry is %+v, they are stat %d at (%d,%d)",
					tick, id, *mine, statID, x, y)
			}
		}
		transcript = append(transcript, line)
	}
	return transcript
}

// m1612HashLine renders every live room's StateHash in board order.
func m1612HashLine(rm *RoomManager) string {
	hashes := rm.RoomStateHashes()
	boards := make([]int, 0, len(hashes))
	for boardID := range hashes {
		boards = append(boards, int(boardID))
	}
	sort.Ints(boards)
	out := ""
	for _, boardID := range boards {
		out += fmt.Sprintf("%d:%016x ", boardID, hashes[int16(boardID)])
	}
	return out
}

// m1612Seeds are the committed schedules. They are constants rather than a
// clock- or entropy-derived value so that "it passed" means the same thing on
// every machine and in every CI run; M1612_SEEDS adds more for a soak without
// changing what the committed suite covers.
var m1612Seeds = []uint64{
	0x0000000000000001,
	0x00000000DEADBEEF,
	0x5DEECE66D0000001,
	0x123456789ABCDEF0,
	0xFEEDFACECAFEBEEF,
	0x9E3779B97F4A7C15,
}

const (
	m1612SchedulePlayers = 3
	m1612ScheduleTicks   = 150
)

// TestM1612RandomizedSchedules is the DoD's "deterministic randomized schedules
// that record their seed". Each seed is replayed twice through two independent
// RoomManagers and must produce the same transcript — the same per-room
// StateHashes, the same squares, the same HUDs and the same event order — while
// the per-tick invariants above hold throughout.
//
// A failure names its seed twice over: in the subtest name (so `-run
// 'TestM1612RandomizedSchedules/seed-…'` replays exactly it) and in the log
// line. Extra seeds can be soaked with M1612_SEEDS=0x…,0x… without editing the
// committed list.
func TestM1612RandomizedSchedules(t *testing.T) {
	seeds := append([]uint64{}, m1612Seeds...)
	for _, extra := range splitList(os.Getenv("M1612_SEEDS")) {
		parsed, err := strconv.ParseUint(strings.TrimPrefix(extra, "0x"), 16, 64)
		if err != nil {
			t.Fatalf("M1612_SEEDS entry %q is not a hex seed: %v", extra, err)
		}
		seeds = append(seeds, parsed)
	}

	for _, seed := range seeds {
		seed := seed
		t.Run(fmt.Sprintf("seed-%016x", seed), func(t *testing.T) {
			t.Logf("schedule seed 0x%016x (%d players, %d ticks) — replay with -run 'TestM1612RandomizedSchedules/seed-%016x'",
				seed, m1612SchedulePlayers, m1612ScheduleTicks, seed)
			schedule := m1612Schedule(seed, m1612SchedulePlayers, m1612ScheduleTicks)
			first := m1612RunSchedule(t, schedule, true)
			second := m1612RunSchedule(t, schedule, true)
			m1612CompareTranscripts(t, seed, first, second)
		})
	}
}

// m1612CompareTranscripts reports the first tick two runs of a seed disagree on.
func m1612CompareTranscripts(t *testing.T, seed uint64, want, got []string) {
	t.Helper()

	if len(want) != len(got) {
		t.Fatalf("seed 0x%016x: replay produced %d ticks, the first run produced %d", seed, len(got), len(want))
	}
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("seed 0x%016x diverged at tick %d — replaying a seed must reproduce the same hashes and event order\n first run: %s\nsecond run: %s",
				seed, i, want[i], got[i])
		}
	}
}

// TestM1612RandomizedScheduleComparisonFailsClosed is the harness's own proof,
// in the idiom TestM168DroppedDirtyCellFailsClosed established: a comparison
// that never fails certifies nothing. Perturbing one tick of the replayed
// schedule must move the transcript, and it must move it at that tick.
func TestM1612RandomizedScheduleComparisonFailsClosed(t *testing.T) {
	const perturbedTick = 20

	seed := m1612Seeds[0]
	schedule := m1612Schedule(seed, m1612SchedulePlayers, 40)
	first := m1612RunSchedule(t, schedule, true)

	perturbed := make([][]PlayerInput, len(schedule))
	for i, row := range schedule {
		perturbed[i] = append([]PlayerInput{}, row...)
	}
	// On tick 20 of this seed the first player walks east across open floor.
	// Turning that one step around is the smallest change a client/server
	// ordering bug could produce. Asserting the original input first means a
	// changed generator reddens this loudly instead of quietly perturbing
	// nothing.
	if want := (PlayerInput{DeltaX: 1}); schedule[perturbedTick][0] != want {
		t.Fatalf("seed 0x%016x tick %d now gives player 1 %+v, not %+v — repoint the perturbation at a plain move",
			seed, perturbedTick, schedule[perturbedTick][0], want)
	}
	perturbed[perturbedTick][0] = PlayerInput{DeltaX: -1}
	second := m1612RunSchedule(t, perturbed, true)

	if len(first) != len(second) {
		t.Fatalf("the perturbed run has %d ticks, the original %d", len(second), len(first))
	}
	diverged := -1
	for i := range first {
		if first[i] != second[i] {
			diverged = i
			break
		}
	}
	if diverged < 0 {
		t.Fatalf("a changed input produced an identical transcript — the comparison proves nothing")
	}
	if diverged > perturbedTick {
		t.Fatalf("the transcript first differs at tick %d, but the input changed at tick %d — the comparison is looking at stale state",
			diverged, perturbedTick)
	}
}
