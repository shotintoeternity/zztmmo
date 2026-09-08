package zztgo // unit: Placement

// M4.3b. One policy for every site that puts a player on a square: join
// (RoomManager.roomSpawn), re-enter after a ReenterWhenZapped hit (DamageStat),
// and respawn after death (ElementPlayerTick).
//
// Only join used to check the destination. Re-enter and respawn wrote E_PLAYER
// over whatever stood there. That is not cosmetic: GameStepWithInputs dispatches
// a stat's tick proc by reading the element of the tile the stat stands on
// (game.go:1632), so a stat whose square is overwritten by E_PLAYER starts
// ticking as a player. A lion re-entered upon stops being a lion, and the tile
// that described it survives only as the arriving player's stat.Under.
//
// Every scan runs in stat-index / ring order and touches no map, so placement
// is deterministic (CLAUDE.md rule 2).

// StatAt returns the id of a stat standing on (x, y), ignoring exceptStatId.
// Pass exceptStatId = -1 to consider every stat.
func (e *Engine) StatAt(x, y int16, exceptStatId int16) (int16, bool) {
	for statId := int16(0); statId <= e.Board.StatCount; statId++ {
		if statId == exceptStatId {
			continue
		}
		stat := &e.Board.Stats[statId]
		if int16(stat.X) == x && int16(stat.Y) == y {
			return statId, true
		}
	}
	return 0, false
}

// PlayerCanStandOn reports whether a player may be written onto a square
// holding this element. Empty and a fake wall are walkable outright; a passage
// is the one unwalkable square a player is meant to arrive on, because arriving
// on it is what taking a passage means, and the tile survives in the arriving
// stat's Under.
//
// Everything else is a square the player would be standing *inside*. That is
// not a cosmetic complaint: E_PLAYER written over E_NORMAL leaves a player in
// the middle of a wall with all four neighbours wall and no key that moves
// them, which is what a player reported on 2026-09-08.
func PlayerCanStandOn(element byte) bool {
	return ElementDefs[element].Walkable || element == E_PASSAGE
}

// playerSealedBy lists the elements a walk can never get past. Touching one
// prints a message or does nothing at all, and the square is still there next
// turn, so a neighbour made of these is not a way out.
//
// Everything absent from this list is counted as a way out, including elements
// that are not walkable: forest is cleared by the touch, an item is picked up,
// a creature dies of the hit, a boulder shifts if there is room behind it, and
// a passage takes you somewhere else entirely. Objects are absent too — one can
// sit there like a wall, but it can also walk away or be pushed, and a board's
// only exit being an object is not a reason to move a player somewhere else.
// The optimistic cases cost a candidate square; they never cost a trapped
// player, because the square being tested is open either way.
func playerSealedBy(element byte) bool {
	switch element {
	case E_WATER, E_SOLID, E_NORMAL, E_BREAKABLE, E_INVISIBLE, E_LINE,
		E_RICOCHET, E_BLINK_WALL, E_BLINK_RAY_EW, E_BLINK_RAY_NS:
		return true
	}
	return false
}

// PlacementReachable reports whether a player put on (x, y) could walk off it:
// at least one of the four orthogonal neighbours is on the board and is not
// something a walk can never get past. ZZT has no diagonal movement, so the
// four are the whole question.
//
// Off-board neighbours do not count as a way out. A board edge with a neighbour
// board is a real exit, but it is one this engine cannot see from here — the
// neighbour board lives in World.BoardData, not in Board.Tiles — and a square
// whose only exit is off the edge of the world is a poor place to arrive
// anyway.
func (e *Engine) PlacementReachable(x, y int16) bool {
	for dir := int16(0); dir < 4; dir++ {
		nx := x + NeighborDeltaX[dir]
		ny := y + NeighborDeltaY[dir]
		if nx < 1 || nx > BOARD_WIDTH || ny < 1 || ny > BOARD_HEIGHT {
			continue
		}
		if !playerSealedBy(e.Board.Tiles[nx][ny].Element) {
			return true
		}
	}
	return false
}

// PlacementUnoccupied reports whether (x, y) is on the board, holds something a
// player may stand on, and is not held by any stat.
func (e *Engine) PlacementUnoccupied(x, y int16, exceptStatId int16) bool {
	if x < 1 || x > BOARD_WIDTH || y < 1 || y > BOARD_HEIGHT {
		return false
	}
	if !PlayerCanStandOn(e.Board.Tiles[x][y].Element) {
		return false
	}
	_, held := e.StatAt(x, y, exceptStatId)
	return !held
}

// PlacementOpen reports whether a player may be placed on (x, y) outright: on
// the board, empty, and with no other stat standing there. The stat check is
// what makes overlap impossible — a square can read E_EMPTY and still be held
// by a stat whose tile some earlier write clobbered.
//
// The cheap tile tests run first so a full board costs no stat scans.
func (e *Engine) PlacementOpen(x, y int16, exceptStatId int16) bool {
	if x < 1 || x > BOARD_WIDTH || y < 1 || y > BOARD_HEIGHT {
		return false
	}
	if e.Board.Tiles[x][y].Element != E_EMPTY {
		return false
	}
	_, held := e.StatAt(x, y, exceptStatId)
	return !held
}

// FindPlacement returns the open square nearest (x, y), searching outward in
// square rings. ok is false when the board holds no open square at all, and the
// caller must then leave the stat where it is rather than overlap another.
//
// It runs the same ring twice: once taking only squares a player could walk off
// again, and if the board offers none, once taking any open square at all. An
// empty square sealed inside a wall satisfied the old single pass, which is how
// a search whose whole job is to find somewhere to stand could answer with
// somewhere you cannot leave. The second pass keeps the old answer for a board
// that has nothing better — a bad square still beats overlapping another stat.
func (e *Engine) FindPlacement(x, y int16, exceptStatId int16) (int16, int16, bool) {
	if px, py, ok := e.findPlacement(x, y, exceptStatId, true); ok {
		return px, py, true
	}
	return e.findPlacement(x, y, exceptStatId, false)
}

// findPlacement is FindPlacement's one pass. reachableOnly says whether a
// candidate must also be a square the player can walk off. The scan is in ring
// order and touches no map, so placement stays deterministic (CLAUDE.md rule 2).
func (e *Engine) findPlacement(x, y int16, exceptStatId int16, reachableOnly bool) (int16, int16, bool) {
	takeable := func(cx, cy int16) bool {
		if !e.PlacementOpen(cx, cy, exceptStatId) {
			return false
		}
		return !reachableOnly || e.PlacementReachable(cx, cy)
	}
	if takeable(x, y) {
		return x, y, true
	}
	for radius := int16(1); radius <= BOARD_WIDTH || radius <= BOARD_HEIGHT; radius++ {
		for dy := -radius; dy <= radius; dy++ {
			for dx := -radius; dx <= radius; dx++ {
				if absInt16(dx) != radius && absInt16(dy) != radius {
					continue
				}
				if takeable(x+dx, y+dy) {
					return x + dx, y + dy, true
				}
			}
		}
	}
	return x, y, false
}
