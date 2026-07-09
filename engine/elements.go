package zztgo // unit: Elements

// interface uses: GameVars

// implementation uses: Crt, Video, Sounds, Input, TxtWind, Editor, Oop, Game

const (
	TransporterNSChars string = "^~^-v_v-"
	TransporterEWChars string = "(<(\xb3)>)\xb3"
	StarAnimChars      string = "\xb3/\xc4\\"
)

func (e *Engine) ElementDefaultTick(statId int16) {
}

func (e *Engine) ElementDefaultTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
}

func (e *Engine) ElementDefaultDraw(x, y int16, ch *byte) {
	*ch = Ord('?')
}

func (e *Engine) ElementMessageTimerTick(statId int16) {
	stat := &e.Board.Stats[statId]
	switch stat.X {
	case 0:
		e.VideoWriteText((60-Length(e.Board.Info.Message))/2, 24, byte(9+int16(stat.P2)%7), " "+e.Board.Info.Message+" ")
		stat.P2--
		if stat.P2 <= 0 {
			e.RemoveStat(statId)
			e.CurrentStatTicked--
			e.BoardDrawBorder()
			e.Board.Info.Message = ""
		}
	}
}

func (e *Engine) ElementDamagingTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	e.BoardAttack(sourceStatId, x, y)
}

func (e *Engine) ElementLionTick(statId int16) {
	var deltaX, deltaY int16
	stat := &e.Board.Stats[statId]
	if int16(stat.P1) < e.Random(10) {
		e.CalcDirectionRnd(&deltaX, &deltaY)
	} else {
		e.CalcDirectionSeek(int16(stat.X), int16(stat.Y), &deltaX, &deltaY)
	}
	if ElementDefs[e.Board.Tiles[int16(stat.X)+deltaX][int16(stat.Y)+deltaY].Element].Walkable {
		e.MoveStat(statId, int16(stat.X)+deltaX, int16(stat.Y)+deltaY)
	} else if e.Board.Tiles[int16(stat.X)+deltaX][int16(stat.Y)+deltaY].Element == E_PLAYER {
		e.BoardAttack(statId, int16(stat.X)+deltaX, int16(stat.Y)+deltaY)
	}

}

func (e *Engine) ElementTigerTick(statId int16) {
	var (
		shot    bool
		element byte
	)
	stat := &e.Board.Stats[statId]
	element = E_BULLET
	if stat.P2 >= 0x80 {
		element = E_STAR
	}
	pId := e.NearestPlayer(int16(stat.X), int16(stat.Y))
	target := &e.Board.Stats[pId]
	if e.Random(10)*3 <= int16(stat.P2)%0x80 {
		if Difference(int16(stat.X), int16(target.X)) <= 2 {
			shot = e.BoardShoot(element, int16(stat.X), int16(stat.Y), 0, Signum(int16(target.Y)-int16(stat.Y)), SHOT_SOURCE_ENEMY)
		} else {
			shot = false
		}
		if !shot {
			if Difference(int16(stat.Y), int16(target.Y)) <= 2 {
				shot = e.BoardShoot(element, int16(stat.X), int16(stat.Y), Signum(int16(target.X)-int16(stat.X)), 0, SHOT_SOURCE_ENEMY)
			}
		}
	}
	e.ElementLionTick(statId)
}

func (e *Engine) ElementRuffianTick(statId int16) {
	stat := &e.Board.Stats[statId]
	if stat.StepX == 0 && stat.StepY == 0 {
		if int16(stat.P2)+8 <= e.Random(17) {
			if int16(stat.P1) >= e.Random(9) {
				e.CalcDirectionSeek(int16(stat.X), int16(stat.Y), &stat.StepX, &stat.StepY)
			} else {
				e.CalcDirectionRnd(&stat.StepX, &stat.StepY)
			}
		}
	} else {
		pId := e.NearestPlayer(int16(stat.X), int16(stat.Y))
		target := &e.Board.Stats[pId]
		if (stat.Y == target.Y || stat.X == target.X) && e.Random(9) <= int16(stat.P1) {
			e.CalcDirectionSeek(int16(stat.X), int16(stat.Y), &stat.StepX, &stat.StepY)
		}
		tile := &e.Board.Tiles[int16(stat.X)+stat.StepX][int16(stat.Y)+stat.StepY]
		if tile.Element == E_PLAYER {
			e.BoardAttack(statId, int16(stat.X)+stat.StepX, int16(stat.Y)+stat.StepY)
		} else if ElementDefs[tile.Element].Walkable {
			e.MoveStat(statId, int16(stat.X)+stat.StepX, int16(stat.Y)+stat.StepY)
			if int16(stat.P2)+8 <= e.Random(17) {
				stat.StepX = 0
				stat.StepY = 0
			}
		} else {
			stat.StepX = 0
			stat.StepY = 0
		}

	}
}

func (e *Engine) ElementBearTick(statId int16) {
	var deltaX, deltaY int16
	stat := &e.Board.Stats[statId]
	pId := e.NearestPlayer(int16(stat.X), int16(stat.Y))
	target := &e.Board.Stats[pId]
	if stat.X != target.X {
		if Difference(int16(stat.Y), int16(target.Y)) <= 8-int16(stat.P1) {
			deltaX = Signum(int16(target.X) - int16(stat.X))
			deltaY = 0
			goto Movement
		}
	}
	if Difference(int16(stat.X), int16(target.X)) <= 8-int16(stat.P1) {
		deltaY = Signum(int16(target.Y) - int16(stat.Y))
		deltaX = 0
	} else {
		deltaX = 0
		deltaY = 0
	}
Movement:
	tile := &e.Board.Tiles[int16(stat.X)+deltaX][int16(stat.Y)+deltaY]
	if ElementDefs[tile.Element].Walkable {
		e.MoveStat(statId, int16(stat.X)+deltaX, int16(stat.Y)+deltaY)
	} else if tile.Element == E_PLAYER || tile.Element == E_BREAKABLE {
		e.BoardAttack(statId, int16(stat.X)+deltaX, int16(stat.Y)+deltaY)
	}

}

func (e *Engine) ElementCentipedeHeadTick(statId int16) {
	var (
		ix, iy int16
		tx, ty int16
		tmp    int16
	)
	stat := &e.Board.Stats[statId]
	pId := e.NearestPlayer(int16(stat.X), int16(stat.Y))
	target := &e.Board.Stats[pId]
	if stat.X == target.X && e.Random(10) < int16(stat.P1) {
		stat.StepY = Signum(int16(target.Y) - int16(stat.Y))
		stat.StepX = 0
	} else if stat.Y == target.Y && e.Random(10) < int16(stat.P1) {
		stat.StepX = Signum(int16(target.X) - int16(stat.X))
		stat.StepY = 0
	} else if e.Random(10)*4 < int16(stat.P2) || stat.StepX == 0 && stat.StepY == 0 {
		e.CalcDirectionRnd(&stat.StepX, &stat.StepY)
	}

	if !ElementDefs[e.Board.Tiles[int16(stat.X)+stat.StepX][int16(stat.Y)+stat.StepY].Element].Walkable && e.Board.Tiles[int16(stat.X)+stat.StepX][int16(stat.Y)+stat.StepY].Element != E_PLAYER {
		ix = stat.StepX
		iy = stat.StepY
		tmp = (e.Random(2)*2 - 1) * stat.StepY
		stat.StepY = (e.Random(2)*2 - 1) * stat.StepX
		stat.StepX = tmp
		if !ElementDefs[e.Board.Tiles[int16(stat.X)+stat.StepX][int16(stat.Y)+stat.StepY].Element].Walkable && e.Board.Tiles[int16(stat.X)+stat.StepX][int16(stat.Y)+stat.StepY].Element != E_PLAYER {
			stat.StepX = -stat.StepX
			stat.StepY = -stat.StepY
			if !ElementDefs[e.Board.Tiles[int16(stat.X)+stat.StepX][int16(stat.Y)+stat.StepY].Element].Walkable && e.Board.Tiles[int16(stat.X)+stat.StepX][int16(stat.Y)+stat.StepY].Element != E_PLAYER {
				if ElementDefs[e.Board.Tiles[int16(stat.X)-ix][int16(stat.Y)-iy].Element].Walkable || e.Board.Tiles[int16(stat.X)-ix][int16(stat.Y)-iy].Element == E_PLAYER {
					stat.StepX = -ix
					stat.StepY = -iy
				} else {
					stat.StepX = 0
					stat.StepY = 0
				}
			}
		}
	}
	if stat.StepX == 0 && stat.StepY == 0 {
		e.Board.Tiles[stat.X][stat.Y].Element = E_CENTIPEDE_SEGMENT
		stat.Leader = -1
		for e.Board.Stats[statId].Follower > 0 {
			tmp = e.Board.Stats[statId].Follower
			e.Board.Stats[statId].Follower = e.Board.Stats[statId].Leader
			e.Board.Stats[statId].Leader = tmp
			statId = tmp
		}
		e.Board.Stats[statId].Follower = e.Board.Stats[statId].Leader
		e.Board.Tiles[e.Board.Stats[statId].X][e.Board.Stats[statId].Y].Element = E_CENTIPEDE_HEAD
	} else if e.Board.Tiles[int16(stat.X)+stat.StepX][int16(stat.Y)+stat.StepY].Element == E_PLAYER {
		if stat.Follower != -1 {
			e.Board.Tiles[e.Board.Stats[stat.Follower].X][e.Board.Stats[stat.Follower].Y].Element = E_CENTIPEDE_HEAD
			e.Board.Stats[stat.Follower].StepX = stat.StepX
			e.Board.Stats[stat.Follower].StepY = stat.StepY
			e.BoardDrawTile(int16(e.Board.Stats[stat.Follower].X), int16(e.Board.Stats[stat.Follower].Y))
		}
		e.BoardAttack(statId, int16(stat.X)+stat.StepX, int16(stat.Y)+stat.StepY)
	} else {
		e.MoveStat(statId, int16(stat.X)+stat.StepX, int16(stat.Y)+stat.StepY)
		tx = int16(stat.X) - stat.StepX
		ty = int16(stat.Y) - stat.StepY
		ix = stat.StepX
		iy = stat.StepY
		for {
			stat2 := &e.Board.Stats[statId]
			tx = int16(stat2.X) - stat2.StepX
			ty = int16(stat2.Y) - stat2.StepY
			ix = stat2.StepX
			iy = stat2.StepY
			if stat2.Follower < 0 {
				if e.Board.Tiles[tx-ix][ty-iy].Element == E_CENTIPEDE_SEGMENT && e.Board.Stats[e.GetStatIdAt(tx-ix, ty-iy)].Leader < 0 {
					stat2.Follower = e.GetStatIdAt(tx-ix, ty-iy)
				} else if e.Board.Tiles[tx-iy][ty-ix].Element == E_CENTIPEDE_SEGMENT && e.Board.Stats[e.GetStatIdAt(tx-iy, ty-ix)].Leader < 0 {
					stat2.Follower = e.GetStatIdAt(tx-iy, ty-ix)
				} else if e.Board.Tiles[tx+iy][ty+ix].Element == E_CENTIPEDE_SEGMENT && e.Board.Stats[e.GetStatIdAt(tx+iy, ty+ix)].Leader < 0 {
					stat2.Follower = e.GetStatIdAt(tx+iy, ty+ix)
				}

			}
			if stat2.Follower > 0 {
				e.Board.Stats[stat2.Follower].Leader = statId
				e.Board.Stats[stat2.Follower].P1 = stat2.P1
				e.Board.Stats[stat2.Follower].P2 = stat2.P2
				e.Board.Stats[stat2.Follower].StepX = tx - int16(e.Board.Stats[stat2.Follower].X)
				e.Board.Stats[stat2.Follower].StepY = ty - int16(e.Board.Stats[stat2.Follower].Y)
				e.MoveStat(stat2.Follower, tx, ty)
			}
			statId = stat2.Follower
			if statId == -1 {
				break
			}
		}
	}

}

func (e *Engine) ElementCentipedeSegmentTick(statId int16) {
	stat := &e.Board.Stats[statId]
	if stat.Leader < 0 {
		if stat.Leader < -1 {
			e.Board.Tiles[stat.X][stat.Y].Element = E_CENTIPEDE_HEAD
		} else {
			stat.Leader--
		}
	}
}

func (e *Engine) ElementBulletTick(statId int16) {
	var (
		ix, iy   int16
		iStat    int16
		iElem    byte
		firstTry bool
	)
	stat := &e.Board.Stats[statId]
	firstTry = true
TryMove:
	ix = int16(stat.X) + stat.StepX

	iy = int16(stat.Y) + stat.StepY
	iElem = e.Board.Tiles[ix][iy].Element
	if ElementDefs[iElem].Walkable || iElem == E_WATER {
		e.MoveStat(statId, ix, iy)
		return
	}
	if iElem == E_RICOCHET && firstTry {
		stat.StepX = -stat.StepX
		stat.StepY = -stat.StepY
		e.SoundQueue(1, "\xf9\x01")
		firstTry = false
		goto TryMove
		return
	}
	if iElem == E_BREAKABLE || ElementDefs[iElem].Destructible && (iElem == E_PLAYER || int16(stat.P1) >= SHOT_SOURCE_PLAYER_BASE) {
		// For player-owned bullets hitting a player tile: check FriendlyFire
		// and self-shot rules before allowing damage.
		if iElem == E_PLAYER {
			targetStatId := e.GetStatIdAt(ix, iy)
			ownerStatId := int16(stat.P1) - SHOT_SOURCE_PLAYER_BASE // valid when P1 >= base
			if int16(stat.P1) >= SHOT_SOURCE_PLAYER_BASE {
				// Player-owned bullet hitting a player.
				if !e.FriendlyFire {
					// Friendly fire off: player bullets never damage players.
					e.RemoveStat(statId)
					e.CurrentStatTicked--
					return
				}
				if targetStatId == ownerStatId {
					// A player's bullet never damages themselves.
					e.RemoveStat(statId)
					e.CurrentStatTicked--
					return
				}
			}
		}
		// Credit score to the bullet's owner (player bullet: owner statId;
		// enemy bullet: stat 0 as fallback, matching vanilla behavior).
		if ElementDefs[iElem].ScoreValue != 0 {
			var ownerStatId int16
			if int16(stat.P1) >= SHOT_SOURCE_PLAYER_BASE {
				ownerStatId = int16(stat.P1) - SHOT_SOURCE_PLAYER_BASE
			} else {
				ownerStatId = 0 // ZZT-QUIRK: enemy kill score goes to player 0
			}
			e.PlayerFor(ownerStatId).Score += ElementDefs[iElem].ScoreValue
			e.GameUpdateSidebar()
		}
		e.BoardAttack(statId, ix, iy)
		return
	}
	if e.Board.Tiles[int16(stat.X)+stat.StepY][int16(stat.Y)+stat.StepX].Element == E_RICOCHET && firstTry {
		ix = stat.StepX
		stat.StepX = -stat.StepY
		stat.StepY = -ix
		e.SoundQueue(1, "\xf9\x01")
		firstTry = false
		goto TryMove
		return
	}
	if e.Board.Tiles[int16(stat.X)-stat.StepY][int16(stat.Y)-stat.StepX].Element == E_RICOCHET && firstTry {
		ix = stat.StepX
		stat.StepX = stat.StepY
		stat.StepY = ix
		e.SoundQueue(1, "\xf9\x01")
		firstTry = false
		goto TryMove
		return
	}
	e.RemoveStat(statId)
	e.CurrentStatTicked--
	if iElem == E_OBJECT || iElem == E_SCROLL {
		iStat = e.GetStatIdAt(ix, iy)
		if e.OopSend(-iStat, "SHOT", false) {
		}
	}
}

func (e *Engine) ElementSpinningGunDraw(x, y int16, ch *byte) {
	switch e.CurrentTick % 8 {
	case 0, 1:
		*ch = 24
	case 2, 3:
		*ch = 26
	case 4, 5:
		*ch = 25
	default:
		*ch = 27
	}
}

func (e *Engine) ElementLineDraw(x, y int16, ch *byte) {
	var i, v, shift int16
	v = 1
	shift = 1
	for i = 0; i <= 3; i++ {
		switch e.Board.Tiles[x+NeighborDeltaX[i]][y+NeighborDeltaY[i]].Element {
		case E_LINE, E_BOARD_EDGE:
			v += shift
		}
		shift = shift << 1
	}
	*ch = Ord(LineChars[v-1])
}

func (e *Engine) ElementSpinningGunTick(statId int16) {
	var (
		shot           bool
		deltaX, deltaY int16
		element        byte
	)
	stat := &e.Board.Stats[statId]
	e.BoardDrawTile(int16(stat.X), int16(stat.Y))
	element = E_BULLET
	if stat.P2 >= 0x80 {
		element = E_STAR
	}
	if e.Random(9) < int16(stat.P2)%0x80 {
		if e.Random(9) <= int16(stat.P1) {
			pId := e.NearestPlayer(int16(stat.X), int16(stat.Y))
			target := &e.Board.Stats[pId]
			if Difference(int16(stat.X), int16(target.X)) <= 2 {
				shot = e.BoardShoot(element, int16(stat.X), int16(stat.Y), 0, Signum(int16(target.Y)-int16(stat.Y)), SHOT_SOURCE_ENEMY)
			} else {
				shot = false
			}
			if !shot {
				if Difference(int16(stat.Y), int16(target.Y)) <= 2 {
					shot = e.BoardShoot(element, int16(stat.X), int16(stat.Y), Signum(int16(target.X)-int16(stat.X)), 0, SHOT_SOURCE_ENEMY)
				}
			}
		} else {
			e.CalcDirectionRnd(&deltaX, &deltaY)
			shot = e.BoardShoot(element, int16(stat.X), int16(stat.Y), deltaX, deltaY, SHOT_SOURCE_ENEMY)
		}
	}
}

func (e *Engine) ElementConveyorTick(x, y int16, direction int16) {
	var (
		i          int16
		iStat      int16
		ix, iy     int16
		canMove    bool
		tiles      [8]TTile
		iMin, iMax int16
		tmpTile    TTile
	)
	if direction == 1 {
		iMin = 0
		iMax = 8
	} else {
		iMin = 7
		iMax = -1
	}
	canMove = true
	i = iMin
	for {
		tiles[i] = e.Board.Tiles[x+DiagonalDeltaX[i]][y+DiagonalDeltaY[i]]
		tile := &tiles[i]
		if tile.Element == E_EMPTY {
			canMove = true
		} else if !ElementDefs[tile.Element].Pushable {
			canMove = false
		}

		i += direction
		if i == iMax {
			break
		}
	}
	i = iMin
	for {
		tile2 := &tiles[i]
		if canMove {
			if ElementDefs[tile2.Element].Pushable {
				ix = x + DiagonalDeltaX[(i-direction+8)%8]
				iy = y + DiagonalDeltaY[(i-direction+8)%8]
				if ElementDefs[tile2.Element].Cycle > -1 {
					tmpTile = e.Board.Tiles[x+DiagonalDeltaX[i]][y+DiagonalDeltaY[i]]
					iStat = e.GetStatIdAt(x+DiagonalDeltaX[i], y+DiagonalDeltaY[i])
					e.Board.Tiles[x+DiagonalDeltaX[i]][y+DiagonalDeltaY[i]] = tiles[i]
					e.Board.Tiles[ix][iy].Element = E_EMPTY
					e.MoveStat(iStat, ix, iy)
					e.Board.Tiles[x+DiagonalDeltaX[i]][y+DiagonalDeltaY[i]] = tmpTile
				} else {
					e.Board.Tiles[ix][iy] = tiles[i]
					e.BoardDrawTile(ix, iy)
				}
				if !ElementDefs[tiles[(i+direction+8)%8].Element].Pushable {
					e.Board.Tiles[x+DiagonalDeltaX[i]][y+DiagonalDeltaY[i]].Element = E_EMPTY
					e.BoardDrawTile(x+DiagonalDeltaX[i], y+DiagonalDeltaY[i])
				}
			} else {
				canMove = false
			}
		} else if tile2.Element == E_EMPTY {
			canMove = true
		} else if !ElementDefs[tile2.Element].Pushable {
			canMove = false
		}

		i += direction
		if i == iMax {
			break
		}
	}
}

func (e *Engine) ElementConveyorCWDraw(x, y int16, ch *byte) {
	switch e.CurrentTick / ElementDefs[E_CONVEYOR_CW].Cycle % 4 {
	case 0:
		*ch = 179
	case 1:
		*ch = 47
	case 2:
		*ch = 196
	default:
		*ch = 92
	}
}

func (e *Engine) ElementConveyorCWTick(statId int16) {
	stat := &e.Board.Stats[statId]
	e.BoardDrawTile(int16(stat.X), int16(stat.Y))
	e.ElementConveyorTick(int16(stat.X), int16(stat.Y), 1)
}

func (e *Engine) ElementConveyorCCWDraw(x, y int16, ch *byte) {
	switch e.CurrentTick / ElementDefs[E_CONVEYOR_CCW].Cycle % 4 {
	case 3:
		*ch = 179
	case 2:
		*ch = 47
	case 1:
		*ch = 196
	default:
		*ch = 92
	}
}

func (e *Engine) ElementConveyorCCWTick(statId int16) {
	stat := &e.Board.Stats[statId]
	e.BoardDrawTile(int16(stat.X), int16(stat.Y))
	e.ElementConveyorTick(int16(stat.X), int16(stat.Y), -1)
}

func (e *Engine) ElementBombDraw(x, y int16, ch *byte) {
	stat := &e.Board.Stats[e.GetStatIdAt(x, y)]
	if stat.P1 <= 1 {
		*ch = 11
	} else {
		*ch = byte(48 + int16(stat.P1))
	}
}

func (e *Engine) ElementBombTick(statId int16) {
	var oldX, oldY int16
	stat := &e.Board.Stats[statId]
	if stat.P1 > 0 {
		stat.P1--
		e.BoardDrawTile(int16(stat.X), int16(stat.Y))
		if stat.P1 == 1 {
			e.SoundQueue(1, "`\x01P\x01@\x010\x01 \x01\x10\x01")
			e.DrawPlayerSurroundings(int16(stat.X), int16(stat.Y), 1)
		} else if stat.P1 == 0 {
			oldX = int16(stat.X)
			oldY = int16(stat.Y)
			e.RemoveStat(statId)
			e.DrawPlayerSurroundings(oldX, oldY, 2)
		} else {
			if int16(stat.P1)%2 == 0 {
				e.SoundQueue(1, "\xf8\x01")
			} else {
				e.SoundQueue(1, "\xf5\x01")
			}
		}

	}
}

func (e *Engine) ElementBombTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	stat := &e.Board.Stats[e.GetStatIdAt(x, y)]
	if stat.P1 == 0 {
		stat.P1 = 9
		e.BoardDrawTile(int16(stat.X), int16(stat.Y))
		e.DisplayMessage(200, "Bomb activated!")
		e.SoundQueue(4, "0\x015\x01@\x01E\x01P\x01")
	} else {
		e.ElementPushablePush(int16(stat.X), int16(stat.Y), *deltaX, *deltaY)
	}
}

func (e *Engine) ElementTransporterMove(x, y, deltaX, deltaY int16) {
	var (
		ix, iy       int16
		newX, newY   int16
		iStat        int16
		finishSearch bool
		isValidDest  bool
	)
	stat := &e.Board.Stats[e.GetStatIdAt(x+deltaX, y+deltaY)]
	if deltaX == stat.StepX && deltaY == stat.StepY {
		ix = int16(stat.X)
		iy = int16(stat.Y)
		newX = -1
		finishSearch = false
		isValidDest = true
		for {
			ix += deltaX
			iy += deltaY
			tile := &e.Board.Tiles[ix][iy]
			if tile.Element == E_BOARD_EDGE {
				finishSearch = true
			} else if isValidDest {
				isValidDest = false
				if !ElementDefs[tile.Element].Walkable {
					e.ElementPushablePush(ix, iy, deltaX, deltaY)
				}
				if ElementDefs[tile.Element].Walkable {
					finishSearch = true
					newX = ix
					newY = iy
				} else {
					newX = -1
				}
			}

			if tile.Element == E_TRANSPORTER {
				iStat = e.GetStatIdAt(ix, iy)
				if e.Board.Stats[iStat].StepX == -deltaX && e.Board.Stats[iStat].StepY == -deltaY {
					isValidDest = true
				}
			}
			if finishSearch {
				break
			}
		}
		if newX != -1 {
			e.ElementMove(int16(stat.X)-deltaX, int16(stat.Y)-deltaY, newX, newY)
			e.SoundQueue(3, "0\x01B\x014\x01F\x018\x01J\x01@\x01R\x01")
		}
	}
}

func (e *Engine) ElementTransporterTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	e.ElementTransporterMove(x-*deltaX, y-*deltaY, *deltaX, *deltaY)
	*deltaX = 0
	*deltaY = 0
}

func (e *Engine) ElementTransporterTick(statId int16) {
	stat := &e.Board.Stats[statId]
	e.BoardDrawTile(int16(stat.X), int16(stat.Y))
}

func (e *Engine) ElementTransporterDraw(x, y int16, ch *byte) {
	stat := &e.Board.Stats[e.GetStatIdAt(x, y)]
	if stat.StepX == 0 {
		*ch = Ord(TransporterNSChars[stat.StepY*2+3+e.CurrentTick/stat.Cycle%4-1])
	} else {
		*ch = Ord(TransporterEWChars[stat.StepX*2+3+e.CurrentTick/stat.Cycle%4-1])
	}
}

func (e *Engine) ElementStarDraw(x, y int16, ch *byte) {
	*ch = Ord(StarAnimChars[e.CurrentTick%4])
	e.Board.Tiles[x][y].Color++
	if e.Board.Tiles[x][y].Color > 15 {
		e.Board.Tiles[x][y].Color = 9
	}
}

func (e *Engine) ElementStarTick(statId int16) {
	stat := &e.Board.Stats[statId]
	stat.P2--
	if stat.P2 <= 0 {
		e.RemoveStat(statId)
	} else if int16(stat.P2)%2 == 0 {
		e.CalcDirectionSeek(int16(stat.X), int16(stat.Y), &stat.StepX, &stat.StepY)
		tile := &e.Board.Tiles[int16(stat.X)+stat.StepX][int16(stat.Y)+stat.StepY]
		if tile.Element == E_PLAYER || tile.Element == E_BREAKABLE {
			e.BoardAttack(statId, int16(stat.X)+stat.StepX, int16(stat.Y)+stat.StepY)
		} else {
			if !ElementDefs[tile.Element].Walkable {
				e.ElementPushablePush(int16(stat.X)+stat.StepX, int16(stat.Y)+stat.StepY, stat.StepX, stat.StepY)
			}
			if ElementDefs[tile.Element].Walkable || tile.Element == E_WATER {
				e.MoveStat(statId, int16(stat.X)+stat.StepX, int16(stat.Y)+stat.StepY)
			}
		}
	} else {
		e.BoardDrawTile(int16(stat.X), int16(stat.Y))
	}

}

func (e *Engine) ElementEnergizerTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	e.SoundQueue(9, " \x03#\x03$\x03%\x035\x03%\x03#\x03 \x03"+"0\x03#\x03$\x03%\x035\x03%\x03#\x03 \x03"+"0\x03#\x03$\x03%\x035\x03%\x03#\x03 \x03"+"0\x03#\x03$\x03%\x035\x03%\x03#\x03 \x03"+"0\x03#\x03$\x03%\x035\x03%\x03#\x03 \x03"+"0\x03#\x03$\x03%\x035\x03%\x03#\x03 \x03"+"0\x03#\x03$\x03%\x035\x03%\x03#\x03 \x03")
	e.Board.Tiles[x][y].Element = E_EMPTY
	e.BoardDrawTile(x, y)
	pState := e.PlayerFor(sourceStatId)
	pState.EnergizerTicks = 75
	e.GameUpdateSidebar()
	if pState.MessageEnergizerNotShown {
		e.DisplayMessage(200, "Energizer - You are invincible")
		pState.MessageEnergizerNotShown = false
	}
	if e.OopSend(0, "ALL:ENERGIZE", false) {
	}
}

func (e *Engine) ElementSlimeTick(statId int16) {
	var (
		dir, color, changedTiles int16
		startX, startY           int16
	)
	stat := &e.Board.Stats[statId]
	if stat.P1 < stat.P2 {
		stat.P1++
	} else {
		color = int16(e.Board.Tiles[stat.X][stat.Y].Color)
		stat.P1 = 0
		startX = int16(stat.X)
		startY = int16(stat.Y)
		changedTiles = 0
		for dir = 0; dir <= 3; dir++ {
			if ElementDefs[e.Board.Tiles[startX+NeighborDeltaX[dir]][startY+NeighborDeltaY[dir]].Element].Walkable {
				if changedTiles == 0 {
					e.MoveStat(statId, startX+NeighborDeltaX[dir], startY+NeighborDeltaY[dir])
					e.Board.Tiles[startX][startY].Color = byte(color)
					e.Board.Tiles[startX][startY].Element = E_BREAKABLE
					e.BoardDrawTile(startX, startY)
				} else {
					e.AddStat(startX+NeighborDeltaX[dir], startY+NeighborDeltaY[dir], E_SLIME, color, ElementDefs[E_SLIME].Cycle, StatTemplateDefault)
					e.Board.Stats[e.Board.StatCount].P2 = stat.P2
				}
				changedTiles++
			}
		}
		if changedTiles == 0 {
			e.RemoveStat(statId)
			e.Board.Tiles[startX][startY].Element = E_BREAKABLE
			e.Board.Tiles[startX][startY].Color = byte(color)
			e.BoardDrawTile(startX, startY)
		}
	}
}

func (e *Engine) ElementSlimeTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	var color int16
	color = int16(e.Board.Tiles[x][y].Color)
	e.DamageStat(e.GetStatIdAt(x, y))
	e.Board.Tiles[x][y].Element = E_BREAKABLE
	e.Board.Tiles[x][y].Color = byte(color)
	e.BoardDrawTile(x, y)
	e.SoundQueue(2, " \x01#\x01")
}

func (e *Engine) ElementSharkTick(statId int16) {
	var deltaX, deltaY int16
	stat := &e.Board.Stats[statId]
	if int16(stat.P1) < e.Random(10) {
		e.CalcDirectionRnd(&deltaX, &deltaY)
	} else {
		e.CalcDirectionSeek(int16(stat.X), int16(stat.Y), &deltaX, &deltaY)
	}
	if e.Board.Tiles[int16(stat.X)+deltaX][int16(stat.Y)+deltaY].Element == E_WATER {
		e.MoveStat(statId, int16(stat.X)+deltaX, int16(stat.Y)+deltaY)
	} else if e.Board.Tiles[int16(stat.X)+deltaX][int16(stat.Y)+deltaY].Element == E_PLAYER {
		e.BoardAttack(statId, int16(stat.X)+deltaX, int16(stat.Y)+deltaY)
	}

}

func (e *Engine) ElementBlinkWallDraw(x, y int16, ch *byte) {
	*ch = 206
}

func (e *Engine) ElementBlinkWallTick(statId int16) {
	var (
		ix, iy       int16
		hitBoundary  bool
		playerStatId int16
		el           int16
	)
	stat := &e.Board.Stats[statId]
	if stat.P3 == 0 {
		stat.P3 = byte(int16(stat.P1) + 1)
	}
	if stat.P3 == 1 {
		ix = int16(stat.X) + stat.StepX
		iy = int16(stat.Y) + stat.StepY
		if stat.StepX != 0 {
			el = E_BLINK_RAY_EW
		} else {
			el = E_BLINK_RAY_NS
		}
		for int16(e.Board.Tiles[ix][iy].Element) == el && e.Board.Tiles[ix][iy].Color == e.Board.Tiles[stat.X][stat.Y].Color {
			e.Board.Tiles[ix][iy].Element = E_EMPTY
			e.BoardDrawTile(ix, iy)
			ix += stat.StepX
			iy += stat.StepY
			stat.P3 = byte(int16(stat.P2)*2 + 1)
		}
		if int16(stat.X)+stat.StepX == ix && int16(stat.Y)+stat.StepY == iy {
			hitBoundary = false
			for {
				if e.Board.Tiles[ix][iy].Element != E_EMPTY && ElementDefs[e.Board.Tiles[ix][iy].Element].Destructible {
					e.BoardDamageTile(ix, iy)
				}
				if e.Board.Tiles[ix][iy].Element == E_PLAYER {
					playerStatId = e.GetStatIdAt(ix, iy)
					if stat.StepX != 0 {
						if e.Board.Tiles[ix][iy-1].Element == E_EMPTY {
							e.MoveStat(playerStatId, ix, iy-1)
						} else if e.Board.Tiles[ix][iy+1].Element == E_EMPTY {
							e.MoveStat(playerStatId, ix, iy+1)
						}

					} else {
						if e.Board.Tiles[ix+1][iy].Element == E_EMPTY {
							e.MoveStat(playerStatId, ix+1, iy)
						} else if e.Board.Tiles[ix-1][iy].Element == E_EMPTY {
							e.MoveStat(playerStatId, ix+1, iy)
						}

					}
					if e.Board.Tiles[ix][iy].Element == E_PLAYER {
						for e.PlayerFor(playerStatId).Health > 0 {
							e.DamageStat(playerStatId)
						}
						hitBoundary = true
					}
				}
				if e.Board.Tiles[ix][iy].Element == E_EMPTY {
					e.Board.Tiles[ix][iy].Element = byte(el)
					e.Board.Tiles[ix][iy].Color = e.Board.Tiles[stat.X][stat.Y].Color
					e.BoardDrawTile(ix, iy)
				} else {
					hitBoundary = true
				}
				ix += stat.StepX
				iy += stat.StepY
				if hitBoundary {
					break
				}
			}
			stat.P3 = byte(int16(stat.P2)*2 + 1)
		}
	} else {
		stat.P3--
	}
}

func (e *Engine) ElementMove(oldX, oldY, newX, newY int16) {
	var statId int16
	statId = e.GetStatIdAt(oldX, oldY)
	if statId >= 0 {
		e.MoveStat(statId, newX, newY)
	} else {
		e.Board.Tiles[newX][newY] = e.Board.Tiles[oldX][oldY]
		e.BoardDrawTile(newX, newY)
		e.Board.Tiles[oldX][oldY].Element = E_EMPTY
		e.BoardDrawTile(oldX, oldY)
	}
}

func (e *Engine) ElementPushablePush(x, y int16, deltaX, deltaY int16) {
	tile := &e.Board.Tiles[x][y]
	if tile.Element == E_SLIDER_NS && deltaX == 0 || tile.Element == E_SLIDER_EW && deltaY == 0 || ElementDefs[tile.Element].Pushable {
		if e.Board.Tiles[x+deltaX][y+deltaY].Element == E_TRANSPORTER {
			e.ElementTransporterMove(x, y, deltaX, deltaY)
		} else if e.Board.Tiles[x+deltaX][y+deltaY].Element != E_EMPTY {
			e.ElementPushablePush(x+deltaX, y+deltaY, deltaX, deltaY)
		}

		if !ElementDefs[e.Board.Tiles[x+deltaX][y+deltaY].Element].Walkable && ElementDefs[e.Board.Tiles[x+deltaX][y+deltaY].Element].Destructible && e.Board.Tiles[x+deltaX][y+deltaY].Element != E_PLAYER {
			e.BoardDamageTile(x+deltaX, y+deltaY)
		}
		if ElementDefs[e.Board.Tiles[x+deltaX][y+deltaY].Element].Walkable {
			e.ElementMove(x, y, x+deltaX, y+deltaY)
		}
	}
}

func (e *Engine) ElementDuplicatorDraw(x, y int16, ch *byte) {
	stat := &e.Board.Stats[e.GetStatIdAt(x, y)]
	switch stat.P1 {
	case 1:
		*ch = 250
	case 2:
		*ch = 249
	case 3:
		*ch = 248
	case 4:
		*ch = 111
	case 5:
		*ch = 79
	default:
		*ch = 250
	}
}

func (e *Engine) ElementObjectTick(statId int16) {
	stat := &e.Board.Stats[statId]
	if stat.DataPos >= 0 {
		e.OopExecute(statId, &stat.DataPos, "Interaction")
	}
	if stat.StepX != 0 || stat.StepY != 0 {
		if ElementDefs[e.Board.Tiles[int16(stat.X)+stat.StepX][int16(stat.Y)+stat.StepY].Element].Walkable {
			e.MoveStat(statId, int16(stat.X)+stat.StepX, int16(stat.Y)+stat.StepY)
		} else {
			e.OopSend(-statId, "THUD", false)
		}
	}
}

func (e *Engine) ElementObjectDraw(x, y int16, ch *byte) {
	*ch = e.Board.Stats[e.GetStatIdAt(x, y)].P1
}

func (e *Engine) ElementObjectTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	var (
		statId int16
	)
	statId = e.GetStatIdAt(x, y)
	// The object executes #TOUCH on its own later tick; remember who knocked so
	// the resulting scroll is shown only to them.
	e.SetScrollAudience(statId, sourceStatId)
	e.OopSend(-statId, "TOUCH", false)
}

func (e *Engine) ElementDuplicatorTick(statId int16) {
	var sourceStatId int16
	stat := &e.Board.Stats[statId]
	if stat.P1 <= 4 {
		stat.P1++
		e.BoardDrawTile(int16(stat.X), int16(stat.Y))
	} else {
		stat.P1 = 0
		if e.Board.Tiles[int16(stat.X)-stat.StepX][int16(stat.Y)-stat.StepY].Element == E_PLAYER {
			ElementDefs[e.Board.Tiles[int16(stat.X)+stat.StepX][int16(stat.Y)+stat.StepY].Element].TouchProc(e, int16(stat.X)+stat.StepX, int16(stat.Y)+stat.StepY, 0, &InputDeltaX, &InputDeltaY)
		} else {
			if e.Board.Tiles[int16(stat.X)-stat.StepX][int16(stat.Y)-stat.StepY].Element != E_EMPTY {
				e.ElementPushablePush(int16(stat.X)-stat.StepX, int16(stat.Y)-stat.StepY, -stat.StepX, -stat.StepY)
			}
			if e.Board.Tiles[int16(stat.X)-stat.StepX][int16(stat.Y)-stat.StepY].Element == E_EMPTY {
				sourceStatId = e.GetStatIdAt(int16(stat.X)+stat.StepX, int16(stat.Y)+stat.StepY)
				if sourceStatId > 0 {
					if e.Board.StatCount < 174 {
						e.AddStat(int16(stat.X)-stat.StepX, int16(stat.Y)-stat.StepY, e.Board.Tiles[int16(stat.X)+stat.StepX][int16(stat.Y)+stat.StepY].Element, int16(e.Board.Tiles[int16(stat.X)+stat.StepX][int16(stat.Y)+stat.StepY].Color), e.Board.Stats[sourceStatId].Cycle, e.Board.Stats[sourceStatId])
						e.BoardDrawTile(int16(stat.X)-stat.StepX, int16(stat.Y)-stat.StepY)
					}
				} else if sourceStatId != 0 {
					e.Board.Tiles[int16(stat.X)-stat.StepX][int16(stat.Y)-stat.StepY] = e.Board.Tiles[int16(stat.X)+stat.StepX][int16(stat.Y)+stat.StepY]
					e.BoardDrawTile(int16(stat.X)-stat.StepX, int16(stat.Y)-stat.StepY)
				}

				e.SoundQueue(3, "0\x022\x024\x025\x027\x02")
			} else {
				e.SoundQueue(3, "\x18\x01\x16\x01")
			}
		}
		stat.P1 = 0
		e.BoardDrawTile(int16(stat.X), int16(stat.Y))
	}
	stat.Cycle = (9 - int16(stat.P2)) * 3
}

func (e *Engine) ElementScrollTick(statId int16) {
	stat := &e.Board.Stats[statId]
	e.Board.Tiles[stat.X][stat.Y].Color++
	if e.Board.Tiles[stat.X][stat.Y].Color > 0x0F {
		e.Board.Tiles[stat.X][stat.Y].Color = 0x09
	}
	e.BoardDrawTile(int16(stat.X), int16(stat.Y))
}

func (e *Engine) ElementScrollTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	var (
		statId int16
	)
	statId = e.GetStatIdAt(x, y)
	stat := &e.Board.Stats[statId]
	e.SoundQueue(2, SoundParse("c-c+d-d+e-e+f-f+g-g"))
	stat.DataPos = 0
	e.SetScrollAudience(statId, sourceStatId)
	e.OopExecute(statId, &stat.DataPos, "Scroll")
	e.RemoveStat(e.GetStatIdAt(x, y))
}

func (e *Engine) ElementKeyTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	var key int16
	key = int16(e.Board.Tiles[x][y].Color) % 8
	pState := e.PlayerFor(sourceStatId)
	if pState.Keys[key-1] {
		e.DisplayMessage(200, "You already have a "+ColorNames[key-1]+" key!")
		e.SoundQueue(2, "0\x02 \x02")
	} else {
		pState.Keys[key-1] = true
		e.Board.Tiles[x][y].Element = E_EMPTY
		e.GameUpdateSidebar()
		e.DisplayMessage(200, "You now have the "+ColorNames[key-1]+" key.")
		e.SoundQueue(2, "@\x01D\x01G\x01@\x01D\x01G\x01@\x01D\x01G\x01P\x02")
	}
}

func (e *Engine) ElementAmmoTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	pState := e.PlayerFor(sourceStatId)
	pState.Ammo += 5
	e.Board.Tiles[x][y].Element = E_EMPTY
	e.GameUpdateSidebar()
	e.SoundQueue(2, "0\x011\x012\x01")
	if pState.MessageAmmoNotShown {
		e.DisplayMessage(200, "Ammunition - 5 shots per container.")
		pState.MessageAmmoNotShown = false
	}
}

func (e *Engine) ElementGemTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	pState := e.PlayerFor(sourceStatId)
	pState.Gems++
	pState.Health++
	pState.Score += 10
	e.Board.Tiles[x][y].Element = E_EMPTY
	e.GameUpdateSidebar()
	e.SoundQueue(2, "@\x017\x014\x010\x01")
	if pState.MessageGemNotShown {
		pState.MessageGemNotShown = false
		e.DisplayMessage(200, "Gems give you Health!")
	}
}

func (e *Engine) ElementPassageTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	if e.MultiRoom || e.PlayerCount() > 1 {
		// MultiRoom / multi-player: emit a TransferEvent instead of swapping the
		// board in-place. Find the destination board and matching entry tile by
		// decoding the destination board into a temporary engine. BoardChange
		// would serialize this live board back into World.BoardData, including
		// players, which is not a neutral read in multi-room mode.
		passageStatId := e.GetStatIdAt(x, y)
		destBoard := int16(e.Board.Stats[passageStatId].P3)
		col := e.Board.Tiles[x][y].Color
		var entryX, entryY int16
		destEngine := NewEngine()
		destEngine.Headless = true
		destEngine.World = e.World
		destEngine.BoardOpen(destBoard)
		for ix := int16(1); ix <= BOARD_WIDTH; ix++ {
			for iy := int16(1); iy <= BOARD_HEIGHT; iy++ {
				if destEngine.Board.Tiles[ix][iy].Element == E_PASSAGE && destEngine.Board.Tiles[ix][iy].Color == col {
					entryX = ix
					entryY = iy
				}
			}
		}
		e.Events = append(e.Events, TransferEvent{
			StatId:  sourceStatId,
			ToBoard: destBoard,
			EntryX:  entryX,
			EntryY:  entryY,
		})
		*deltaX = 0
		*deltaY = 0
		return
	}
	// Single-player / single-board: keep vanilla behavior.
	e.BoardPassageTeleport(x, y, sourceStatId)
	*deltaX = 0
	*deltaY = 0
}

func (e *Engine) ElementDoorTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	var key int16
	key = int16(e.Board.Tiles[x][y].Color) / 16 % 8
	pState := e.PlayerFor(sourceStatId)
	if pState.Keys[key-1] {
		e.Board.Tiles[x][y].Element = E_EMPTY
		e.BoardDrawTile(x, y)
		pState.Keys[key-1] = false
		e.GameUpdateSidebar()
		e.DisplayMessage(200, "The "+ColorNames[key-1]+" door is now open.")
		e.SoundQueue(3, "0\x017\x01;\x010\x017\x01;\x01@\x04")
	} else {
		e.DisplayMessage(200, "The "+ColorNames[key-1]+" door is locked!")
		e.SoundQueue(3, "\x17\x01\x10\x01")
	}
}

func (e *Engine) ElementPushableTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	e.ElementPushablePush(x, y, *deltaX, *deltaY)
	e.SoundQueue(2, "\x15\x01")
}

func (e *Engine) ElementPusherDraw(x, y int16, ch *byte) {
	stat := &e.Board.Stats[e.GetStatIdAt(x, y)]
	if stat.StepX == 1 {
		*ch = 16
	} else if stat.StepX == -1 {
		*ch = 17
	} else if stat.StepY == -1 {
		*ch = 30
	} else {
		*ch = 31
	}

}

func (e *Engine) ElementPusherTick(statId int16) {
	var i, startX, startY int16
	stat := &e.Board.Stats[statId]
	startX = int16(stat.X)
	startY = int16(stat.Y)
	if !ElementDefs[e.Board.Tiles[int16(stat.X)+stat.StepX][int16(stat.Y)+stat.StepY].Element].Walkable {
		e.ElementPushablePush(int16(stat.X)+stat.StepX, int16(stat.Y)+stat.StepY, stat.StepX, stat.StepY)
	}
	statId = e.GetStatIdAt(startX, startY)
	stat2 := &e.Board.Stats[statId]
	if ElementDefs[e.Board.Tiles[int16(stat2.X)+stat2.StepX][int16(stat2.Y)+stat2.StepY].Element].Walkable {
		e.MoveStat(statId, int16(stat2.X)+stat2.StepX, int16(stat2.Y)+stat2.StepY)
		e.SoundQueue(2, "\x15\x01")
		if e.Board.Tiles[int16(stat2.X)-stat2.StepX*2][int16(stat2.Y)-stat2.StepY*2].Element == E_PUSHER {
			i = e.GetStatIdAt(int16(stat2.X)-stat2.StepX*2, int16(stat2.Y)-stat2.StepY*2)
			if e.Board.Stats[i].StepX == stat2.StepX && e.Board.Stats[i].StepY == stat2.StepY {
				ElementDefs[E_PUSHER].TickProc(e, i)
			}
		}
	}
}

func (e *Engine) ElementTorchTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	pState := e.PlayerFor(sourceStatId)
	pState.Torches++
	e.Board.Tiles[x][y].Element = E_EMPTY
	e.BoardDrawTile(x, y)
	e.GameUpdateSidebar()
	if pState.MessageTorchNotShown {
		e.DisplayMessage(200, "Torch - used for lighting in the underground.")
		pState.MessageTorchNotShown = false
	}
	e.SoundQueue(3, "0\x019\x014\x02")
}

func (e *Engine) ElementInvisibleTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	tile := &e.Board.Tiles[x][y]
	tile.Element = E_NORMAL
	e.BoardDrawTile(x, y)
	e.SoundQueue(3, "\x12\x01\x10\x01")
	e.DisplayMessage(100, "You are blocked by an invisible wall.")
}

func (e *Engine) ElementForestTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	pState := e.PlayerFor(sourceStatId)
	e.Board.Tiles[x][y].Element = E_EMPTY
	e.BoardDrawTile(x, y)
	e.SoundQueue(3, "9\x01")
	if pState.MessageForestNotShown {
		e.DisplayMessage(200, "A path is cleared through the forest.")
		pState.MessageForestNotShown = false
	}
}

func (e *Engine) ElementFakeTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	pState := e.PlayerFor(sourceStatId)
	if pState.MessageFakeNotShown {
		e.DisplayMessage(150, "A fake wall - secret passage!")
		pState.MessageFakeNotShown = false
	}
}

func (e *Engine) ElementBoardEdgeTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	var (
		neighborId     int16
		boardId        int16
		entryX, entryY int16
	)
	entryX = int16(e.Board.Stats[sourceStatId].X)
	entryY = int16(e.Board.Stats[sourceStatId].Y)
	if *deltaY == -1 {
		neighborId = 0
		entryY = BOARD_HEIGHT
	} else if *deltaY == 1 {
		neighborId = 1
		entryY = 1
	} else if *deltaX == -1 {
		neighborId = 2
		entryX = BOARD_WIDTH
	} else {
		neighborId = 3
		entryX = 1
	}

	if e.Board.Info.NeighborBoards[neighborId] != 0 {
		if e.MultiRoom || e.PlayerCount() > 1 {
			// MultiRoom / multi-player: emit TransferEvent with destination board
			// and entry tile at the edge. The caller (RoomManager) handles the
			// actual stat transfer. No board swap occurs.
			e.Events = append(e.Events, TransferEvent{
				StatId:  sourceStatId,
				ToBoard: int16(e.Board.Info.NeighborBoards[neighborId]),
				EntryX:  entryX,
				EntryY:  entryY,
			})
			*deltaX = 0
			*deltaY = 0
			return
		}
		// Single-player / single-board: keep vanilla behavior.
		boardId = e.World.Info.CurrentBoard
		e.BoardChange(int16(e.Board.Info.NeighborBoards[neighborId]))
		if e.Board.Tiles[entryX][entryY].Element != E_PLAYER {
			ElementDefs[e.Board.Tiles[entryX][entryY].Element].TouchProc(e, entryX, entryY, sourceStatId, &InputDeltaX, &InputDeltaY)
		}
		if ElementDefs[e.Board.Tiles[entryX][entryY].Element].Walkable || e.Board.Tiles[entryX][entryY].Element == E_PLAYER {
			if e.Board.Tiles[entryX][entryY].Element != E_PLAYER {
				e.MoveStat(sourceStatId, entryX, entryY)
			}
			e.TransitionDrawBoardChange()
			*deltaX = 0
			*deltaY = 0
			e.BoardEnter(sourceStatId)
		} else {
			e.BoardChange(boardId)
		}
	}
}

func (e *Engine) ElementWaterTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	e.SoundQueue(3, "@\x01P\x01")
	e.DisplayMessage(100, "Your way is blocked by water.")
}

func (e *Engine) DrawPlayerSurroundings(x, y int16, bombPhase int16) {
	var (
		ix, iy int16
		istat  int16
	)
	for ix = x - TORCH_DX - 1; ix <= x+TORCH_DX+1; ix++ {
		if ix >= 1 && ix <= BOARD_WIDTH {
			for iy = y - TORCH_DY - 1; iy <= y+TORCH_DY+1; iy++ {
				if iy >= 1 && iy <= BOARD_HEIGHT {
					tile := &e.Board.Tiles[ix][iy]
					if bombPhase > 0 && Sqr(ix-x)+Sqr(iy-y)*2 < TORCH_DIST_SQR {
						if bombPhase == 1 {
							if Length(ElementDefs[tile.Element].ParamTextName) != 0 {
								istat = e.GetStatIdAt(ix, iy)
								if istat > 0 {
									e.OopSend(-istat, "BOMBED", false)
								}
							}
							if ElementDefs[tile.Element].Destructible || tile.Element == E_STAR {
								e.BoardDamageTile(ix, iy)
							}
							if tile.Element == E_EMPTY || tile.Element == E_BREAKABLE {
								tile.Element = E_BREAKABLE
								tile.Color = byte(0x09 + e.Random(7))
								e.BoardDrawTile(ix, iy)
							}
						} else {
							if tile.Element == E_BREAKABLE {
								tile.Element = E_EMPTY
							}
						}
					}
					e.BoardDrawTile(ix, iy)
				}
			}
		}
	}
}

func (e *Engine) GamePromptEndPlay() {
	if e.PlayerFor(0).Health <= 0 {
		e.GamePlayExitRequested = true
		e.BoardDrawBorder()
	} else {
		e.Events = append(e.Events, QuitPromptEvent{})
	}
	InputKeyPressed = '\x00'
}

func (e *Engine) ElementPlayerTick(statId int16) {
	var (
		i           int16
		bulletCount int16
	)
	stat := &e.Board.Stats[statId]
	pState := e.PlayerFor(statId)
	if pState.EnergizerTicks > 0 {
		if ElementDefs[E_PLAYER].Character == '\x02' {
			ElementDefs[E_PLAYER].Character = '\x01'
		} else {
			ElementDefs[E_PLAYER].Character = '\x02'
		}
		if e.CurrentTick%2 != 0 {
			e.Board.Tiles[stat.X][stat.Y].Color = 0x0F
		} else {
			e.Board.Tiles[stat.X][stat.Y].Color = byte((e.CurrentTick%7+1)*16 + 0x0F)
		}
		e.BoardDrawTile(int16(stat.X), int16(stat.Y))
	} else if e.Board.Tiles[stat.X][stat.Y].Color != 0x1F || ElementDefs[E_PLAYER].Character != '\x02' {
		e.Board.Tiles[stat.X][stat.Y].Color = 0x1F
		ElementDefs[E_PLAYER].Character = '\x02'
		e.BoardDrawTile(int16(stat.X), int16(stat.Y))
	}

	// Respawn countdown: when RespawnTicks > 0 the player is dead and waiting
	// to reappear. Decrement each tick; at 0 place them back at the entry point
	// with invulnerability and emit RespawnEvent. Skip all input handling while dead.
	if pState.RespawnTicks > 0 {
		pState.RespawnTicks--
		if pState.RespawnTicks == 0 {
			// Place player at their own board entry point. Board.Info.StartPlayerX/Y
			// is the world file's stale value on the server (RoomManager never
			// calls BoardEnter) and can be a wall — TOWN board 19 stores (30,25),
			// an E_NORMAL tile. See Engine.ReenterPoint.
			spawnX, spawnY := e.ReenterPoint(statId)
			oldX := int16(stat.X)
			oldY := int16(stat.Y)
			e.Board.Tiles[stat.X][stat.Y].Element = E_EMPTY
			e.BoardDrawTile(oldX, oldY)
			e.DrawPlayerSurroundings(oldX, oldY, 0)
			stat.X = byte(spawnX)
			stat.Y = byte(spawnY)
			e.Board.Tiles[stat.X][stat.Y] = TTile{Element: E_PLAYER, Color: ElementDefs[E_PLAYER].Color}
			e.DrawPlayerSurroundings(spawnX, spawnY, 0)
			e.BoardDrawTile(spawnX, spawnY)
			pState.Health = 100
			pState.EnergizerTicks = RESPAWN_INVULN_TICKS
			e.GameUpdateSidebar()
			e.Events = append(e.Events, RespawnEvent{StatId: statId, X: spawnX, Y: spawnY})
		}
		return
	}
	if pState.Health <= 0 {
		// Health is 0 but RespawnTicks hasn't been set yet (e.g. very first tick
		// after DamageStat ran this same cycle). Zero input and wait.
		InputDeltaX = 0
		InputDeltaY = 0
		InputShiftPressed = false
		return
	}
	if InputShiftPressed || InputKeyPressed == ' ' {
		if InputShiftPressed && (InputDeltaX != 0 || InputDeltaY != 0) {
			pState.DirX = InputDeltaX
			pState.DirY = InputDeltaY
		}
		if pState.DirX != 0 || pState.DirY != 0 {
			if e.Board.Info.MaxShots == 0 {
				if pState.MessageNoShootingNotShown {
					e.DisplayMessage(200, "Can't shoot in this place!")
				}
				pState.MessageNoShootingNotShown = false
			} else if pState.Ammo == 0 {
				if pState.MessageOutOfAmmoNotShown {
					e.DisplayMessage(200, "You don't have any ammo!")
				}
				pState.MessageOutOfAmmoNotShown = false
			} else {
				bulletCount = 0
				for i = 0; i <= e.Board.StatCount; i++ {
					if e.Board.Tiles[e.Board.Stats[i].X][e.Board.Stats[i].Y].Element == E_BULLET && e.Board.Stats[i].P1 == 0 {
						bulletCount++
					}
				}
				if bulletCount < int16(e.Board.Info.MaxShots) {
					if e.BoardShoot(E_BULLET, int16(stat.X), int16(stat.Y), pState.DirX, pState.DirY, statId+SHOT_SOURCE_PLAYER_BASE) {
						pState.Ammo--
						e.GameUpdateSidebar()
						e.SoundQueue(2, "@\x010\x01 \x01")
						InputDeltaX = 0
						InputDeltaY = 0
					}
				}
			}

		}
	} else if InputDeltaX != 0 || InputDeltaY != 0 {
		pState.DirX = InputDeltaX
		pState.DirY = InputDeltaY
		targetX := int16(stat.X) + InputDeltaX
		targetY := int16(stat.Y) + InputDeltaY
		if targetX < 0 || targetX > BOARD_WIDTH+1 || targetY < 0 || targetY > BOARD_HEIGHT+1 {
			InputDeltaX = 0
			InputDeltaY = 0
		} else {
			ElementDefs[e.Board.Tiles[targetX][targetY].Element].TouchProc(e, targetX, targetY, statId, &InputDeltaX, &InputDeltaY)
		}
		if InputDeltaX != 0 || InputDeltaY != 0 {
			if pState.SoundEnabled && !SoundIsPlaying {
				Sound(110)
			}
			targetX = int16(stat.X) + InputDeltaX
			targetY = int16(stat.Y) + InputDeltaY
			if targetX >= 0 && targetX <= BOARD_WIDTH+1 && targetY >= 0 && targetY <= BOARD_HEIGHT+1 && ElementDefs[e.Board.Tiles[targetX][targetY].Element].Walkable {
				if pState.SoundEnabled && !SoundIsPlaying {
					NoSound()
				}
				e.MoveStat(statId, targetX, targetY)
			} else if pState.SoundEnabled && !SoundIsPlaying {
				NoSound()
			}

		}
	}

	switch UpCase(InputKeyPressed) {
	case 'T':
		if pState.TorchTicks <= 0 {
			if pState.Torches > 0 {
				if e.Board.Info.IsDark {
					pState.Torches--
					pState.TorchTicks = TORCH_DURATION
					e.DrawPlayerSurroundings(int16(stat.X), int16(stat.Y), 0)
					e.GameUpdateSidebar()
				} else {
					if pState.MessageRoomNotDarkNotShown {
						e.DisplayMessage(200, "Don't need torch - room is not dark!")
						pState.MessageRoomNotDarkNotShown = false
					}
				}
			} else {
				if pState.MessageOutOfTorchesNotShown {
					e.DisplayMessage(200, "You don't have any torches!")
					pState.MessageOutOfTorchesNotShown = false
				}
			}
		}
	case '\x1b', 'Q':
		e.GamePromptEndPlay()
	case 'S':
		// Never call GameWorldSave here: it prompts via InputReadWaitKey, which
		// blocks forever headless (the M3.9 '?' bug). Emit and return.
		e.Events = append(e.Events, SavePromptEvent{StatId: statId})
		InputKeyPressed = '\x00'
	case 'P':
		if pState.Health > 0 {
			pState.Paused = true
			e.Events = append(e.Events, PauseEvent{StatId: statId, Paused: true})
		}
	case 'B':
		pState.SoundEnabled = !pState.SoundEnabled
		// The package-global SoundEnabled gates SoundTimerHandler, i.e. whether
		// this *process* makes noise. That is a terminal concept: mirror player
		// 0's flag into it only when not headless, so that on a server one
		// player pressing 'B' can never silence anybody else.
		if !e.Headless {
			SoundEnabled = pState.SoundEnabled
		}
		SoundClearQueue()
		e.GameUpdateSidebar()
		InputKeyPressed = ' '
	case 'H':
		e.Events = append(e.Events, HelpEvent{
			Filename: "GAME.HLP",
			Title:    "Playing ZZT",
			StatId:   statId,
		})
	case '?':
		e.Events = append(e.Events, DebugPromptEvent{StatId: statId})
		InputKeyPressed = '\x00'
	}
	if pState.TorchTicks > 0 {
		pState.TorchTicks--
		if pState.TorchTicks <= 0 {
			e.DrawPlayerSurroundings(int16(stat.X), int16(stat.Y), 0)
			e.SoundQueue(3, "0\x01 \x01\x10\x01")
		}
		if pState.TorchTicks%40 == 0 {
			e.GameUpdateSidebar()
		}
	}
	if pState.EnergizerTicks > 0 {
		pState.EnergizerTicks--
		if pState.EnergizerTicks == 10 {
			e.SoundQueue(9, " \x03\x1a\x03\x17\x03\x16\x03\x15\x03\x13\x03\x10\x03")
		} else if pState.EnergizerTicks <= 0 {
			e.Board.Tiles[stat.X][stat.Y].Color = ElementDefs[E_PLAYER].Color
			e.BoardDrawTile(int16(stat.X), int16(stat.Y))
		}

	}
	if e.Board.Info.TimeLimitSec > 0 && pState.Health > 0 {
		if SoundHasTimeElapsed(&pState.BoardTimeHsec, 100) {
			pState.BoardTimeSec++
			if e.Board.Info.TimeLimitSec-10 == pState.BoardTimeSec {
				e.DisplayMessage(200, "Running out of time!")
				e.SoundQueue(3, "@\x06E\x06@\x065\x06@\x06E\x06@\n")
			} else if pState.BoardTimeSec > e.Board.Info.TimeLimitSec {
				e.DamageStat(statId)
			}

			e.GameUpdateSidebar()
		}
	}
}

func (e *Engine) ElementMonitorTick(statId int16) {
	switch UpCase(InputKeyPressed) {
	case '\x1b', 'A', 'E', 'H', 'N', 'P', 'Q', 'R', 'S', 'W', '|':
		e.GamePlayExitRequested = true
	}
}

func (e *Engine) ResetMessageNotShownFlags() {
	pState := e.PlayerFor(0)
	pState.MessageAmmoNotShown = true
	pState.MessageOutOfAmmoNotShown = true
	pState.MessageNoShootingNotShown = true
	pState.MessageTorchNotShown = true
	pState.MessageOutOfTorchesNotShown = true
	pState.MessageRoomNotDarkNotShown = true
	pState.MessageHintTorchNotShown = true
	pState.MessageForestNotShown = true
	pState.MessageFakeNotShown = true
	pState.MessageGemNotShown = true
	pState.MessageEnergizerNotShown = true
}

func (e *Engine) InitElementDefs() {
	var i int16
	for i = 0; i <= MAX_ELEMENT; i++ {
		def := &ElementDefs[i]
		def.Character = ' '
		def.Color = COLOR_CHOICE_ON_BLACK
		def.Destructible = false
		def.Pushable = false
		def.VisibleInDark = false
		def.PlaceableOnTop = false
		def.Walkable = false
		def.HasDrawProc = false
		def.Cycle = -1
		def.TickProc = (*Engine).ElementDefaultTick
		def.DrawProc = (*Engine).ElementDefaultDraw
		def.TouchProc = (*Engine).ElementDefaultTouch
		def.EditorCategory = 0
		def.EditorShortcut = '\x00'
		def.Name = ""
		def.CategoryName = ""
		def.Param1Name = ""
		def.Param2Name = ""
		def.ParamBulletTypeName = ""
		def.ParamBoardName = ""
		def.ParamDirName = ""
		def.ParamTextName = ""
		def.ScoreValue = 0
	}
	ElementDefs[0].Character = ' '
	ElementDefs[0].Color = 0x70
	ElementDefs[0].Pushable = true
	ElementDefs[0].Walkable = true
	ElementDefs[0].Name = "Empty"
	ElementDefs[3].Character = ' '
	ElementDefs[3].Color = 0x07
	ElementDefs[3].Cycle = 1
	ElementDefs[3].TickProc = (*Engine).ElementMonitorTick
	ElementDefs[3].Name = "Monitor"
	ElementDefs[19].Character = '\xb0'
	ElementDefs[19].Color = 0xF9
	ElementDefs[19].PlaceableOnTop = true
	ElementDefs[19].EditorCategory = CATEGORY_TERRAIN
	ElementDefs[19].TouchProc = (*Engine).ElementWaterTouch
	ElementDefs[19].EditorShortcut = 'W'
	ElementDefs[19].Name = "Water"
	ElementDefs[19].CategoryName = "Terrains:"
	ElementDefs[20].Character = '\xb0'
	ElementDefs[20].Color = 0x20
	ElementDefs[20].Walkable = false
	ElementDefs[20].TouchProc = (*Engine).ElementForestTouch
	ElementDefs[20].EditorCategory = CATEGORY_TERRAIN
	ElementDefs[20].EditorShortcut = 'F'
	ElementDefs[20].Name = "Forest"
	ElementDefs[4].Character = '\x02'
	ElementDefs[4].Color = 0x1F
	ElementDefs[4].Destructible = true
	ElementDefs[4].Pushable = true
	ElementDefs[4].VisibleInDark = true
	ElementDefs[4].Cycle = 1
	ElementDefs[4].TickProc = (*Engine).ElementPlayerTick
	ElementDefs[4].EditorCategory = CATEGORY_ITEM
	ElementDefs[4].EditorShortcut = 'Z'
	ElementDefs[4].Name = "Player"
	ElementDefs[4].CategoryName = "Items:"
	ElementDefs[41].Character = '\xea'
	ElementDefs[41].Color = 0x0C
	ElementDefs[41].Destructible = true
	ElementDefs[41].Pushable = true
	ElementDefs[41].Cycle = 2
	ElementDefs[41].TickProc = (*Engine).ElementLionTick
	ElementDefs[41].TouchProc = (*Engine).ElementDamagingTouch
	ElementDefs[41].EditorCategory = CATEGORY_CREATURE
	ElementDefs[41].EditorShortcut = 'L'
	ElementDefs[41].Name = "Lion"
	ElementDefs[41].CategoryName = "Beasts:"
	ElementDefs[41].Param1Name = "Intelligence?"
	ElementDefs[41].ScoreValue = 1
	ElementDefs[42].Character = '\xe3'
	ElementDefs[42].Color = 0x0B
	ElementDefs[42].Destructible = true
	ElementDefs[42].Pushable = true
	ElementDefs[42].Cycle = 2
	ElementDefs[42].TickProc = (*Engine).ElementTigerTick
	ElementDefs[42].TouchProc = (*Engine).ElementDamagingTouch
	ElementDefs[42].EditorCategory = CATEGORY_CREATURE
	ElementDefs[42].EditorShortcut = 'T'
	ElementDefs[42].Name = "Tiger"
	ElementDefs[42].Param1Name = "Intelligence?"
	ElementDefs[42].Param2Name = "Firing rate?"
	ElementDefs[42].ParamBulletTypeName = "Firing type?"
	ElementDefs[42].ScoreValue = 2
	ElementDefs[44].Character = '\xe9'
	ElementDefs[44].Destructible = true
	ElementDefs[44].Cycle = 2
	ElementDefs[44].TickProc = (*Engine).ElementCentipedeHeadTick
	ElementDefs[44].TouchProc = (*Engine).ElementDamagingTouch
	ElementDefs[44].EditorCategory = CATEGORY_CREATURE
	ElementDefs[44].EditorShortcut = 'H'
	ElementDefs[44].Name = "Head"
	ElementDefs[44].CategoryName = "Centipedes"
	ElementDefs[44].Param1Name = "Intelligence?"
	ElementDefs[44].Param2Name = "Deviance?"
	ElementDefs[44].ScoreValue = 1
	ElementDefs[45].Character = 'O'
	ElementDefs[45].Destructible = true
	ElementDefs[45].Cycle = 2
	ElementDefs[45].TickProc = (*Engine).ElementCentipedeSegmentTick
	ElementDefs[45].TouchProc = (*Engine).ElementDamagingTouch
	ElementDefs[45].EditorCategory = CATEGORY_CREATURE
	ElementDefs[45].EditorShortcut = 'S'
	ElementDefs[45].Name = "Segment"
	ElementDefs[45].ScoreValue = 3
	ElementDefs[18].Character = '\xf8'
	ElementDefs[18].Color = 0x0F
	ElementDefs[18].Destructible = true
	ElementDefs[18].Cycle = 1
	ElementDefs[18].TickProc = (*Engine).ElementBulletTick
	ElementDefs[18].TouchProc = (*Engine).ElementDamagingTouch
	ElementDefs[18].Name = "Bullet"
	ElementDefs[15].Character = 'S'
	ElementDefs[15].Color = 0x0F
	ElementDefs[15].Destructible = false
	ElementDefs[15].Cycle = 1
	ElementDefs[15].TickProc = (*Engine).ElementStarTick
	ElementDefs[15].TouchProc = (*Engine).ElementDamagingTouch
	ElementDefs[15].HasDrawProc = true
	ElementDefs[15].DrawProc = (*Engine).ElementStarDraw
	ElementDefs[15].Name = "Star"
	ElementDefs[8].Character = '\x0c'
	ElementDefs[8].Pushable = true
	ElementDefs[8].TouchProc = (*Engine).ElementKeyTouch
	ElementDefs[8].EditorCategory = CATEGORY_ITEM
	ElementDefs[8].EditorShortcut = 'K'
	ElementDefs[8].Name = "Key"
	ElementDefs[5].Character = '\x84'
	ElementDefs[5].Color = 0x03
	ElementDefs[5].Pushable = true
	ElementDefs[5].TouchProc = (*Engine).ElementAmmoTouch
	ElementDefs[5].EditorCategory = CATEGORY_ITEM
	ElementDefs[5].EditorShortcut = 'A'
	ElementDefs[5].Name = "Ammo"
	ElementDefs[7].Character = '\x04'
	ElementDefs[7].Pushable = true
	ElementDefs[7].TouchProc = (*Engine).ElementGemTouch
	ElementDefs[7].Destructible = true
	ElementDefs[7].EditorCategory = CATEGORY_ITEM
	ElementDefs[7].EditorShortcut = 'G'
	ElementDefs[7].Name = "Gem"
	ElementDefs[11].Character = '\xf0'
	ElementDefs[11].Color = COLOR_WHITE_ON_CHOICE
	ElementDefs[11].Cycle = 0
	ElementDefs[11].VisibleInDark = true
	ElementDefs[11].TouchProc = (*Engine).ElementPassageTouch
	ElementDefs[11].EditorCategory = CATEGORY_ITEM
	ElementDefs[11].EditorShortcut = 'P'
	ElementDefs[11].Name = "Passage"
	ElementDefs[11].ParamBoardName = "Room thru passage?"
	ElementDefs[9].Character = '\n'
	ElementDefs[9].Color = COLOR_WHITE_ON_CHOICE
	ElementDefs[9].TouchProc = (*Engine).ElementDoorTouch
	ElementDefs[9].EditorCategory = CATEGORY_ITEM
	ElementDefs[9].EditorShortcut = 'D'
	ElementDefs[9].Name = "Door"
	ElementDefs[10].Character = '\xe8'
	ElementDefs[10].Color = 0x0F
	ElementDefs[10].TouchProc = (*Engine).ElementScrollTouch
	ElementDefs[10].TickProc = (*Engine).ElementScrollTick
	ElementDefs[10].Pushable = true
	ElementDefs[10].Cycle = 1
	ElementDefs[10].EditorCategory = CATEGORY_ITEM
	ElementDefs[10].EditorShortcut = 'S'
	ElementDefs[10].Name = "Scroll"
	ElementDefs[10].ParamTextName = "Edit text of scroll"
	ElementDefs[12].Character = '\xfa'
	ElementDefs[12].Color = 0x0F
	ElementDefs[12].Cycle = 2
	ElementDefs[12].TickProc = (*Engine).ElementDuplicatorTick
	ElementDefs[12].HasDrawProc = true
	ElementDefs[12].DrawProc = (*Engine).ElementDuplicatorDraw
	ElementDefs[12].EditorCategory = CATEGORY_ITEM
	ElementDefs[12].EditorShortcut = 'U'
	ElementDefs[12].Name = "Duplicator"
	ElementDefs[12].ParamDirName = "Source direction?"
	ElementDefs[12].Param2Name = "Duplication rate?;SF"
	ElementDefs[6].Character = '\x9d'
	ElementDefs[6].Color = 0x06
	ElementDefs[6].VisibleInDark = true
	ElementDefs[6].TouchProc = (*Engine).ElementTorchTouch
	ElementDefs[6].EditorCategory = CATEGORY_ITEM
	ElementDefs[6].EditorShortcut = 'T'
	ElementDefs[6].Name = "Torch"
	ElementDefs[39].Character = '\x18'
	ElementDefs[39].Cycle = 2
	ElementDefs[39].TickProc = (*Engine).ElementSpinningGunTick
	ElementDefs[39].HasDrawProc = true
	ElementDefs[39].DrawProc = (*Engine).ElementSpinningGunDraw
	ElementDefs[39].EditorCategory = CATEGORY_CREATURE
	ElementDefs[39].EditorShortcut = 'G'
	ElementDefs[39].Name = "Spinning gun"
	ElementDefs[39].Param1Name = "Intelligence?"
	ElementDefs[39].Param2Name = "Firing rate?"
	ElementDefs[39].ParamBulletTypeName = "Firing type?"
	ElementDefs[35].Character = '\x05'
	ElementDefs[35].Color = 0x0D
	ElementDefs[35].Destructible = true
	ElementDefs[35].Pushable = true
	ElementDefs[35].Cycle = 1
	ElementDefs[35].TickProc = (*Engine).ElementRuffianTick
	ElementDefs[35].TouchProc = (*Engine).ElementDamagingTouch
	ElementDefs[35].EditorCategory = CATEGORY_CREATURE
	ElementDefs[35].EditorShortcut = 'R'
	ElementDefs[35].Name = "Ruffian"
	ElementDefs[35].Param1Name = "Intelligence?"
	ElementDefs[35].Param2Name = "Resting time?"
	ElementDefs[35].ScoreValue = 2
	ElementDefs[34].Character = '\x99'
	ElementDefs[34].Color = 0x06
	ElementDefs[34].Destructible = true
	ElementDefs[34].Pushable = true
	ElementDefs[34].Cycle = 3
	ElementDefs[34].TickProc = (*Engine).ElementBearTick
	ElementDefs[34].TouchProc = (*Engine).ElementDamagingTouch
	ElementDefs[34].EditorCategory = CATEGORY_CREATURE
	ElementDefs[34].EditorShortcut = 'B'
	ElementDefs[34].Name = "Bear"
	ElementDefs[34].CategoryName = "Creatures:"
	ElementDefs[34].Param1Name = "Sensitivity?"
	ElementDefs[34].ScoreValue = 1
	ElementDefs[37].Character = '*'
	ElementDefs[37].Color = COLOR_CHOICE_ON_BLACK
	ElementDefs[37].Destructible = false
	ElementDefs[37].Cycle = 3
	ElementDefs[37].TickProc = (*Engine).ElementSlimeTick
	ElementDefs[37].TouchProc = (*Engine).ElementSlimeTouch
	ElementDefs[37].EditorCategory = CATEGORY_CREATURE
	ElementDefs[37].EditorShortcut = 'V'
	ElementDefs[37].Name = "Slime"
	ElementDefs[37].Param2Name = "Movement speed?;FS"
	ElementDefs[38].Character = '^'
	ElementDefs[38].Color = 0x07
	ElementDefs[38].Destructible = false
	ElementDefs[38].Cycle = 3
	ElementDefs[38].TickProc = (*Engine).ElementSharkTick
	ElementDefs[38].EditorCategory = CATEGORY_CREATURE
	ElementDefs[38].EditorShortcut = 'Y'
	ElementDefs[38].Name = "Shark"
	ElementDefs[38].Param1Name = "Intelligence?"
	ElementDefs[16].Character = '/'
	ElementDefs[16].Cycle = 3
	ElementDefs[16].HasDrawProc = true
	ElementDefs[16].TickProc = (*Engine).ElementConveyorCWTick
	ElementDefs[16].DrawProc = (*Engine).ElementConveyorCWDraw
	ElementDefs[16].EditorCategory = CATEGORY_ITEM
	ElementDefs[16].EditorShortcut = '1'
	ElementDefs[16].Name = "Clockwise"
	ElementDefs[16].CategoryName = "Conveyors:"
	ElementDefs[17].Character = '\\'
	ElementDefs[17].Cycle = 2
	ElementDefs[17].HasDrawProc = true
	ElementDefs[17].DrawProc = (*Engine).ElementConveyorCCWDraw
	ElementDefs[17].TickProc = (*Engine).ElementConveyorCCWTick
	ElementDefs[17].EditorCategory = CATEGORY_ITEM
	ElementDefs[17].EditorShortcut = '2'
	ElementDefs[17].Name = "Counter"
	ElementDefs[21].Character = '\xdb'
	ElementDefs[21].EditorCategory = CATEGORY_TERRAIN
	ElementDefs[21].CategoryName = "Walls:"
	ElementDefs[21].EditorShortcut = 'S'
	ElementDefs[21].Name = "Solid"
	ElementDefs[22].Character = '\xb2'
	ElementDefs[22].EditorCategory = CATEGORY_TERRAIN
	ElementDefs[22].EditorShortcut = 'N'
	ElementDefs[22].Name = "Normal"
	ElementDefs[31].Character = '\xce'
	ElementDefs[31].HasDrawProc = true
	ElementDefs[31].DrawProc = (*Engine).ElementLineDraw
	ElementDefs[31].Name = "Line"
	ElementDefs[43].Character = '\xba'
	ElementDefs[33].Character = '\xcd'
	ElementDefs[32].Character = '*'
	ElementDefs[32].Color = 0x0A
	ElementDefs[32].EditorCategory = CATEGORY_TERRAIN
	ElementDefs[32].EditorShortcut = 'R'
	ElementDefs[32].Name = "Ricochet"
	ElementDefs[23].Character = '\xb1'
	ElementDefs[23].Destructible = false
	ElementDefs[23].EditorCategory = CATEGORY_TERRAIN
	ElementDefs[23].EditorShortcut = 'B'
	ElementDefs[23].Name = "Breakable"
	ElementDefs[24].Character = '\xfe'
	ElementDefs[24].Pushable = true
	ElementDefs[24].TouchProc = (*Engine).ElementPushableTouch
	ElementDefs[24].EditorCategory = CATEGORY_TERRAIN
	ElementDefs[24].EditorShortcut = 'O'
	ElementDefs[24].Name = "Boulder"
	ElementDefs[25].Character = '\x12'
	ElementDefs[25].TouchProc = (*Engine).ElementPushableTouch
	ElementDefs[25].EditorCategory = CATEGORY_TERRAIN
	ElementDefs[25].EditorShortcut = '1'
	ElementDefs[25].Name = "Slider (NS)"
	ElementDefs[26].Character = '\x1d'
	ElementDefs[26].TouchProc = (*Engine).ElementPushableTouch
	ElementDefs[26].EditorCategory = CATEGORY_TERRAIN
	ElementDefs[26].EditorShortcut = '2'
	ElementDefs[26].Name = "Slider (EW)"
	ElementDefs[30].Character = '\xc5'
	ElementDefs[30].TouchProc = (*Engine).ElementTransporterTouch
	ElementDefs[30].HasDrawProc = true
	ElementDefs[30].DrawProc = (*Engine).ElementTransporterDraw
	ElementDefs[30].Cycle = 2
	ElementDefs[30].TickProc = (*Engine).ElementTransporterTick
	ElementDefs[30].EditorCategory = CATEGORY_TERRAIN
	ElementDefs[30].EditorShortcut = 'T'
	ElementDefs[30].Name = "Transporter"
	ElementDefs[30].ParamDirName = "Direction?"
	ElementDefs[40].Character = '\x10'
	ElementDefs[40].Color = COLOR_CHOICE_ON_BLACK
	ElementDefs[40].HasDrawProc = true
	ElementDefs[40].DrawProc = (*Engine).ElementPusherDraw
	ElementDefs[40].Cycle = 4
	ElementDefs[40].TickProc = (*Engine).ElementPusherTick
	ElementDefs[40].EditorCategory = CATEGORY_CREATURE
	ElementDefs[40].EditorShortcut = 'P'
	ElementDefs[40].Name = "Pusher"
	ElementDefs[40].ParamDirName = "Push direction?"
	ElementDefs[13].Character = '\x0b'
	ElementDefs[13].HasDrawProc = true
	ElementDefs[13].DrawProc = (*Engine).ElementBombDraw
	ElementDefs[13].Pushable = true
	ElementDefs[13].Cycle = 6
	ElementDefs[13].TickProc = (*Engine).ElementBombTick
	ElementDefs[13].TouchProc = (*Engine).ElementBombTouch
	ElementDefs[13].EditorCategory = CATEGORY_ITEM
	ElementDefs[13].EditorShortcut = 'B'
	ElementDefs[13].Name = "Bomb"
	ElementDefs[14].Character = '\x7f'
	ElementDefs[14].Color = 0x05
	ElementDefs[14].TouchProc = (*Engine).ElementEnergizerTouch
	ElementDefs[14].EditorCategory = CATEGORY_ITEM
	ElementDefs[14].EditorShortcut = 'E'
	ElementDefs[14].Name = "Energizer"
	ElementDefs[29].Character = '\xce'
	ElementDefs[29].Cycle = 1
	ElementDefs[29].TickProc = (*Engine).ElementBlinkWallTick
	ElementDefs[29].HasDrawProc = true
	ElementDefs[29].DrawProc = (*Engine).ElementBlinkWallDraw
	ElementDefs[29].EditorCategory = CATEGORY_TERRAIN
	ElementDefs[29].EditorShortcut = 'L'
	ElementDefs[29].Name = "Blink wall"
	ElementDefs[29].Param1Name = "Starting time"
	ElementDefs[29].Param2Name = "Period"
	ElementDefs[29].ParamDirName = "Wall direction"
	ElementDefs[27].Character = '\xb2'
	ElementDefs[27].EditorCategory = CATEGORY_TERRAIN
	ElementDefs[27].PlaceableOnTop = true
	ElementDefs[27].Walkable = true
	ElementDefs[27].TouchProc = (*Engine).ElementFakeTouch
	ElementDefs[27].EditorShortcut = 'A'
	ElementDefs[27].Name = "Fake"
	ElementDefs[28].Character = ' '
	ElementDefs[28].EditorCategory = CATEGORY_TERRAIN
	ElementDefs[28].TouchProc = (*Engine).ElementInvisibleTouch
	ElementDefs[28].EditorShortcut = 'I'
	ElementDefs[28].Name = "Invisible"
	ElementDefs[36].Character = '\x02'
	ElementDefs[36].EditorCategory = CATEGORY_CREATURE
	ElementDefs[36].Cycle = 3
	ElementDefs[36].HasDrawProc = true
	ElementDefs[36].DrawProc = (*Engine).ElementObjectDraw
	ElementDefs[36].TickProc = (*Engine).ElementObjectTick
	ElementDefs[36].TouchProc = (*Engine).ElementObjectTouch
	ElementDefs[36].EditorShortcut = 'O'
	ElementDefs[36].Name = "Object"
	ElementDefs[36].Param1Name = "Character?"
	ElementDefs[36].ParamTextName = "Edit Program"
	ElementDefs[2].TickProc = (*Engine).ElementMessageTimerTick
	ElementDefs[1].TouchProc = (*Engine).ElementBoardEdgeTouch
	e.EditorPatternCount = 5
	e.EditorPatterns[0] = E_SOLID
	e.EditorPatterns[1] = E_NORMAL
	e.EditorPatterns[2] = E_BREAKABLE
	e.EditorPatterns[3] = E_EMPTY
	e.EditorPatterns[4] = E_LINE
}

func (e *Engine) InitElementsEditor() {
	e.InitElementDefs()
	ElementDefs[28].Character = '\xb0'
	ElementDefs[28].Color = COLOR_CHOICE_ON_BLACK
	e.ForceDarknessOff = true
}

func (e *Engine) InitElementsGame() {
	e.InitElementDefs()
	e.ForceDarknessOff = false
}

func (e *Engine) InitEditorStatSettings() {
	var i int16
	for i = 0; i <= MAX_ELEMENT; i++ {
		setting := &e.World.EditorStatSettings[i]
		setting.P1 = 4
		setting.P2 = 4
		setting.P3 = 0
		setting.StepX = 0
		setting.StepY = -1
	}
	e.World.EditorStatSettings[E_OBJECT].P1 = 1
	e.World.EditorStatSettings[E_BEAR].P1 = 8
}

// --- Global Wrappers ---

func DrawPlayerSurroundings(x, y int16, bombPhase int16) {
	E.DrawPlayerSurroundings(x, y, bombPhase)
}

func ElementAmmoTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	E.ElementAmmoTouch(x, y, sourceStatId, deltaX, deltaY)
}

func ElementBearTick(statId int16) {
	E.ElementBearTick(statId)
}

func ElementBlinkWallDraw(x, y int16, ch *byte) {
	E.ElementBlinkWallDraw(x, y, ch)
}

func ElementBlinkWallTick(statId int16) {
	E.ElementBlinkWallTick(statId)
}

func ElementBoardEdgeTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	E.ElementBoardEdgeTouch(x, y, sourceStatId, deltaX, deltaY)
}

func ElementBombDraw(x, y int16, ch *byte) {
	E.ElementBombDraw(x, y, ch)
}

func ElementBombTick(statId int16) {
	E.ElementBombTick(statId)
}

func ElementBombTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	E.ElementBombTouch(x, y, sourceStatId, deltaX, deltaY)
}

func ElementBulletTick(statId int16) {
	E.ElementBulletTick(statId)
}

func ElementCentipedeHeadTick(statId int16) {
	E.ElementCentipedeHeadTick(statId)
}

func ElementCentipedeSegmentTick(statId int16) {
	E.ElementCentipedeSegmentTick(statId)
}

func ElementConveyorCCWDraw(x, y int16, ch *byte) {
	E.ElementConveyorCCWDraw(x, y, ch)
}

func ElementConveyorCCWTick(statId int16) {
	E.ElementConveyorCCWTick(statId)
}

func ElementConveyorCWDraw(x, y int16, ch *byte) {
	E.ElementConveyorCWDraw(x, y, ch)
}

func ElementConveyorCWTick(statId int16) {
	E.ElementConveyorCWTick(statId)
}

func ElementConveyorTick(x, y int16, direction int16) {
	E.ElementConveyorTick(x, y, direction)
}

func ElementDamagingTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	E.ElementDamagingTouch(x, y, sourceStatId, deltaX, deltaY)
}

func ElementDefaultDraw(x, y int16, ch *byte) {
	E.ElementDefaultDraw(x, y, ch)
}

func ElementDefaultTick(statId int16) {
	E.ElementDefaultTick(statId)
}

func ElementDefaultTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	E.ElementDefaultTouch(x, y, sourceStatId, deltaX, deltaY)
}

func ElementDoorTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	E.ElementDoorTouch(x, y, sourceStatId, deltaX, deltaY)
}

func ElementDuplicatorDraw(x, y int16, ch *byte) {
	E.ElementDuplicatorDraw(x, y, ch)
}

func ElementDuplicatorTick(statId int16) {
	E.ElementDuplicatorTick(statId)
}

func ElementEnergizerTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	E.ElementEnergizerTouch(x, y, sourceStatId, deltaX, deltaY)
}

func ElementFakeTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	E.ElementFakeTouch(x, y, sourceStatId, deltaX, deltaY)
}

func ElementForestTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	E.ElementForestTouch(x, y, sourceStatId, deltaX, deltaY)
}

func ElementGemTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	E.ElementGemTouch(x, y, sourceStatId, deltaX, deltaY)
}

func ElementInvisibleTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	E.ElementInvisibleTouch(x, y, sourceStatId, deltaX, deltaY)
}

func ElementKeyTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	E.ElementKeyTouch(x, y, sourceStatId, deltaX, deltaY)
}

func ElementLineDraw(x, y int16, ch *byte) {
	E.ElementLineDraw(x, y, ch)
}

func ElementLionTick(statId int16) {
	E.ElementLionTick(statId)
}

func ElementMessageTimerTick(statId int16) {
	E.ElementMessageTimerTick(statId)
}

func ElementMonitorTick(statId int16) {
	E.ElementMonitorTick(statId)
}

func ElementMove(oldX, oldY, newX, newY int16) {
	E.ElementMove(oldX, oldY, newX, newY)
}

func ElementObjectDraw(x, y int16, ch *byte) {
	E.ElementObjectDraw(x, y, ch)
}

func ElementObjectTick(statId int16) {
	E.ElementObjectTick(statId)
}

func ElementObjectTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	E.ElementObjectTouch(x, y, sourceStatId, deltaX, deltaY)
}

func ElementPassageTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	E.ElementPassageTouch(x, y, sourceStatId, deltaX, deltaY)
}

func ElementPlayerTick(statId int16) {
	E.ElementPlayerTick(statId)
}

func ElementPushablePush(x, y int16, deltaX, deltaY int16) {
	E.ElementPushablePush(x, y, deltaX, deltaY)
}

func ElementPushableTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	E.ElementPushableTouch(x, y, sourceStatId, deltaX, deltaY)
}

func ElementPusherDraw(x, y int16, ch *byte) {
	E.ElementPusherDraw(x, y, ch)
}

func ElementPusherTick(statId int16) {
	E.ElementPusherTick(statId)
}

func ElementRuffianTick(statId int16) {
	E.ElementRuffianTick(statId)
}

func ElementScrollTick(statId int16) {
	E.ElementScrollTick(statId)
}

func ElementScrollTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	E.ElementScrollTouch(x, y, sourceStatId, deltaX, deltaY)
}

func ElementSharkTick(statId int16) {
	E.ElementSharkTick(statId)
}

func ElementSlimeTick(statId int16) {
	E.ElementSlimeTick(statId)
}

func ElementSlimeTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	E.ElementSlimeTouch(x, y, sourceStatId, deltaX, deltaY)
}

func ElementSpinningGunDraw(x, y int16, ch *byte) {
	E.ElementSpinningGunDraw(x, y, ch)
}

func ElementSpinningGunTick(statId int16) {
	E.ElementSpinningGunTick(statId)
}

func ElementStarDraw(x, y int16, ch *byte) {
	E.ElementStarDraw(x, y, ch)
}

func ElementStarTick(statId int16) {
	E.ElementStarTick(statId)
}

func ElementTigerTick(statId int16) {
	E.ElementTigerTick(statId)
}

func ElementTorchTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	E.ElementTorchTouch(x, y, sourceStatId, deltaX, deltaY)
}

func ElementTransporterDraw(x, y int16, ch *byte) {
	E.ElementTransporterDraw(x, y, ch)
}

func ElementTransporterMove(x, y, deltaX, deltaY int16) {
	E.ElementTransporterMove(x, y, deltaX, deltaY)
}

func ElementTransporterTick(statId int16) {
	E.ElementTransporterTick(statId)
}

func ElementTransporterTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	E.ElementTransporterTouch(x, y, sourceStatId, deltaX, deltaY)
}

func ElementWaterTouch(x, y int16, sourceStatId int16, deltaX, deltaY *int16) {
	E.ElementWaterTouch(x, y, sourceStatId, deltaX, deltaY)
}

func GamePromptEndPlay() {
	E.GamePromptEndPlay()
}

func InitEditorStatSettings() {
	E.InitEditorStatSettings()
}

func InitElementDefs() {
	E.InitElementDefs()
}

func InitElementsEditor() {
	E.InitElementsEditor()
}

func InitElementsGame() {
	E.InitElementsGame()
}

func ResetMessageNotShownFlags() {
	E.ResetMessageNotShownFlags()
}
