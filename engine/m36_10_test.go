package zztgo

import "testing"

// Task M36.10 — input and bullet ownership follow the player, not a stale stat
// index.
//
// Two things name a player by stat id across a cycle, and RemoveStat shifts
// every id above the removed one down by one:
//
//   - the step's inputs map, keyed by the ids the room manager saw BEFORE the
//     step, and read with the live CurrentStatTicked inside the tick loop. A
//     stat below a player removed earlier in the cycle handed each later
//     player the input of whoever held its id before, including Key and Shift.
//   - a player bullet's P1 (statId+SHOT_SOURCE_PLAYER_BASE, set in
//     ElementPlayerTick). It was never reindexed, so score went to whoever
//     later held the index, a bullet could take its new "owner" for a
//     self-shot, and MaxShots stopped counting it.
//
// Vanilla's player was stat 0, which nothing below can shift.

// m3610Board: an open board with player C at stat 0, an enemy bullet at
// stat 1 that dies against a wall on its first tick, and players A (stat 2)
// and B (stat 3) in open ground.
func m3610Board(t *testing.T) (e *Engine, a, b int16) {
	t.Helper()
	e = NewEngine()
	e.Headless = true
	e.WorldCreate()
	e.BoardCreate()
	e.SetInputSource(&ScriptedInput{})
	for ix := int16(2); ix < BOARD_WIDTH; ix++ {
		for iy := int16(2); iy < BOARD_HEIGHT; iy++ {
			e.Board.Tiles[ix][iy] = TTile{Element: E_EMPTY}
		}
	}
	e.Board.Tiles[e.Board.Stats[0].X][e.Board.Stats[0].Y] = TTile{Element: E_EMPTY}
	e.Board.StatCount = -1

	e.Board.Info.StartPlayerX, e.Board.Info.StartPlayerY = 5, 5
	if c := e.SpawnPlayer(); c != 0 {
		t.Fatalf("player C is stat %d, want 0", c)
	}
	// An enemy bullet flying west into a solid wall: ElementBulletTick
	// removes it on its first tick.
	e.Board.Tiles[40][3] = TTile{Element: E_SOLID, Color: 0x0F}
	e.AddStat(41, 3, E_BULLET, int16(ElementDefs[E_BULLET].Color), 1, StatTemplateDefault)
	if e.Board.StatCount != 1 {
		t.Fatalf("bullet is stat %d, want 1", e.Board.StatCount)
	}
	e.Board.Stats[1].P1 = SHOT_SOURCE_ENEMY
	e.Board.Stats[1].StepX, e.Board.Stats[1].StepY = -1, 0

	e.Board.Info.StartPlayerX, e.Board.Info.StartPlayerY = 20, 10
	if a = e.SpawnPlayer(); a != 2 {
		t.Fatalf("player A is stat %d, want 2", a)
	}
	e.Board.Info.StartPlayerX, e.Board.Info.StartPlayerY = 30, 10
	if b = e.SpawnPlayer(); b != 3 {
		t.Fatalf("player B is stat %d, want 3", b)
	}
	for _, id := range []int16{0, a, b} {
		e.PlayerFor(id).Health = 100
	}
	e.GamePlayExitRequested = false
	return e, a, b
}

// The reviewer's repro: A presses down, B presses nothing, and the bullet
// below them both dies first. A must move down; B must not.
func TestM3610InputFollowsThePlayerAcrossALowerRemoval(t *testing.T) {
	e, a, b := m3610Board(t)
	pA, pB := e.Players[a], e.Players[b]
	ax, ay := int16(e.Board.Stats[a].X), int16(e.Board.Stats[a].Y)
	bx, by := int16(e.Board.Stats[b].X), int16(e.Board.Stats[b].Y)

	step(e, map[int16]PlayerInput{a: {DeltaY: 1}})

	if e.Board.StatCount != 2 {
		t.Fatalf("StatCount = %d, want 2: the bullet should have died on the wall", e.Board.StatCount)
	}
	newA, newB := e.statIdOfPlayer(pA), e.statIdOfPlayer(pB)
	if newA != 1 || newB != 2 {
		t.Fatalf("A is stat %d and B is stat %d after the bullet's removal, want 1 and 2", newA, newB)
	}
	if x, y := int16(e.Board.Stats[newA].X), int16(e.Board.Stats[newA].Y); x != ax || y != ay+1 {
		t.Errorf("player A at (%d,%d), want (%d,%d): it lost its own input", x, y, ax, ay+1)
	}
	if x, y := int16(e.Board.Stats[newB].X), int16(e.Board.Stats[newB].Y); x != bx || y != by {
		t.Errorf("player B at (%d,%d), want (%d,%d): it acted on A's input", x, y, bx, by)
	}
}

// The caller's map is the room manager's; reindexing for the step must not
// rewrite it.
func TestM3610StepDoesNotRewriteTheCallersInputs(t *testing.T) {
	e, a, _ := m3610Board(t)
	inputs := map[int16]PlayerInput{a: {DeltaY: 1}}
	step(e, inputs)
	if len(inputs) != 1 || inputs[a].DeltaY != 1 {
		t.Errorf("caller's inputs became %v, want only {%d: down}", inputs, a)
	}
}

// m3610Shot places a bullet owned by ownerStatId at (x,y), flying east, as
// stat StatCount+1, the way ElementPlayerTick's BoardShoot does.
func m3610Shot(t *testing.T, e *Engine, ownerStatId, x, y int16) int16 {
	t.Helper()
	e.AddStat(x, y, E_BULLET, int16(ElementDefs[E_BULLET].Color), 1, StatTemplateDefault)
	id := e.Board.StatCount
	e.Board.Stats[id].P1 = byte(ownerStatId + SHOT_SOURCE_PLAYER_BASE)
	e.Board.Stats[id].StepX, e.Board.Stats[id].StepY = 1, 0
	return id
}

// A's bullet kills a lion after a lower stat has died in the same cycle. The
// score is A's, not that of whoever inherited A's old index (B).
func TestM3610BulletScoreFollowsItsOwner(t *testing.T) {
	e, a, b := m3610Board(t)
	pA, pB := e.Players[a], e.Players[b]
	// The bullet is added before the lion so it ticks first: a lion that
	// moved first could step out of the line of fire.
	m3610Shot(t, e, a, 51, 15)
	e.AddStat(52, 15, E_LION, int16(ElementDefs[E_LION].Color), 2, StatTemplateDefault)
	want := ElementDefs[E_LION].ScoreValue
	if want == 0 {
		t.Fatalf("a lion is worth no score; the test needs a scoring target")
	}

	step(e, map[int16]PlayerInput{})

	if e.Board.Tiles[52][15].Element == E_LION {
		t.Fatalf("the lion survived: A's bullet should have killed it")
	}
	if pA.Score != want || pB.Score != 0 {
		t.Errorf("scores A=%d B=%d, want A=%d B=0: the kill was credited by stale index", pA.Score, pB.Score, want)
	}
}

// With FriendlyFire on, A's bullet hits B after a lower stat's removal moved
// B onto A's old index. Under the stale P1 the bullet took B for its own
// shooter and spared it.
func TestM3610BulletIsNotASelfShotForTheIndexsNewHolder(t *testing.T) {
	e, a, b := m3610Board(t)
	e.FriendlyFire = true
	pB := e.Players[b]
	bx, by := int16(e.Board.Stats[b].X), int16(e.Board.Stats[b].Y)
	m3610Shot(t, e, a, bx-1, by)
	before := pB.Health

	step(e, map[int16]PlayerInput{})

	if pB.Health >= before {
		t.Errorf("B's health %d -> %d: A's bullet treated B as its owner", before, pB.Health)
	}
}

// MaxShots counts a player's bullets by P1. After a lower removal, A's
// in-flight bullet must still count against A, not against nobody. The lower
// stat is removed between steps, so A's input is keyed by its new id and only
// the bullet's P1 can be stale.
func TestM3610MaxShotsStillCountsABulletAfterALowerRemoval(t *testing.T) {
	e, a, _ := m3610Board(t)
	pA := e.Players[a]
	e.Board.Info.MaxShots = 1
	pA.Ammo = 5
	ax, ay := int16(e.Board.Stats[a].X), int16(e.Board.Stats[a].Y)
	// A's bullet flies north along open ground, well clear of everyone.
	shot := m3610Shot(t, e, a, ax, ay-3)
	e.Board.Stats[shot].StepX, e.Board.Stats[shot].StepY = 0, -1

	e.RemoveStat(1)
	a = e.statIdOfPlayer(pA)
	step(e, map[int16]PlayerInput{a: {DeltaX: 1, Shift: true}})

	if pA.Ammo != 5 {
		t.Errorf("A's ammo %d, want 5: MaxShots 1 with one bullet in flight must refuse a second", pA.Ammo)
	}
}

// A bullet whose owner leaves the board belongs to nobody: it credits no one
// and is no one's self-shot, and the player who inherits the index gets
// nothing from it.
func TestM3610BulletOfADepartedOwnerCreditsNobody(t *testing.T) {
	e, a, b := m3610Board(t)
	pB := e.Players[b]
	shot := m3610Shot(t, e, a, 51, 15)
	e.AddStat(52, 15, E_LION, int16(ElementDefs[E_LION].Color), 2, StatTemplateDefault)

	e.RemovePlayer(a)
	shot-- // a removal below it shifted the bullet down
	if got := e.Board.Stats[shot].P1; got != SHOT_SOURCE_PLAYER_ORPHAN {
		t.Fatalf("the departed owner's bullet has P1=%d, want SHOT_SOURCE_PLAYER_ORPHAN (%d)", got, SHOT_SOURCE_PLAYER_ORPHAN)
	}
	statsBefore := len(e.Players)

	step(e, map[int16]PlayerInput{})

	if e.Board.Tiles[52][15].Element == E_LION {
		t.Fatalf("the lion survived: an orphan bullet is still a player's bullet")
	}
	if pB.Score != 0 {
		t.Errorf("B scored %d from a departed player's bullet", pB.Score)
	}
	if len(e.Players) > statsBefore {
		t.Errorf("crediting an orphan bullet created a PlayerState (%d -> %d entries)", statsBefore, len(e.Players))
	}
}
