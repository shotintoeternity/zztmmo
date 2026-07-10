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

// PlacementUnoccupied reports whether (x, y) is on the board, does not already
// show a player, and is not held by any stat.
func (e *Engine) PlacementUnoccupied(x, y int16, exceptStatId int16) bool {
	if x < 1 || x > BOARD_WIDTH || y < 1 || y > BOARD_HEIGHT {
		return false
	}
	if e.Board.Tiles[x][y].Element == E_PLAYER {
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
func (e *Engine) FindPlacement(x, y int16, exceptStatId int16) (int16, int16, bool) {
	if e.PlacementOpen(x, y, exceptStatId) {
		return x, y, true
	}
	for radius := int16(1); radius <= BOARD_WIDTH || radius <= BOARD_HEIGHT; radius++ {
		for dy := -radius; dy <= radius; dy++ {
			for dx := -radius; dx <= radius; dx++ {
				if absInt16(dx) != radius && absInt16(dy) != radius {
					continue
				}
				if e.PlacementOpen(x+dx, y+dy, exceptStatId) {
					return x + dx, y + dy, true
				}
			}
		}
	}
	return x, y, false
}
