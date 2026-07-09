package main // unit: Oop

// interface uses: GameVars

// implementation uses: Sounds, TxtWind, Game, Elements

func (e *Engine) OopError(statId int16, message string) {
	stat := &e.Board.Stats[statId]
	e.DisplayMessage(200, "ERR: "+message)
	SoundQueue(5, "P\n")
	stat.DataPos = -1
}

func (e *Engine) OopReadChar(statId int16, position *int16) {
	stat := &e.Board.Stats[statId]
	if *position >= 0 && *position < stat.DataLen {
		e.OopChar = stat.Data[*position]
		*position++
	} else {
		e.OopChar = '\x00'
	}
}

func (e *Engine) OopReadWord(statId int16, position *int16) {
	e.OopWord = ""
	for {
		e.OopReadChar(statId, position)
		if e.OopChar != ' ' {
			break
		}
	}
	e.OopChar = UpCase(e.OopChar)
	if e.OopChar < '0' || e.OopChar > '9' {
		for e.OopChar >= 'A' && e.OopChar <= 'Z' || e.OopChar == ':' || e.OopChar >= '0' && e.OopChar <= '9' || e.OopChar == '_' {
			e.OopWord += string([]byte{e.OopChar})
			e.OopReadChar(statId, position)
			e.OopChar = UpCase(e.OopChar)
		}
	}
	if *position > 0 {
		*position--
	}
}

func (e *Engine) OopReadValue(statId int16, position *int16) {
	var (
		s    string
		code int16
	)
	s = ""
	for {
		e.OopReadChar(statId, position)
		if e.OopChar != ' ' {
			break
		}
	}
	e.OopChar = UpCase(e.OopChar)
	for e.OopChar >= '0' && e.OopChar <= '9' {
		s += string([]byte{e.OopChar})
		e.OopReadChar(statId, position)
		e.OopChar = UpCase(e.OopChar)
	}
	if *position > 0 {
		*position--
	}
	if Length(s) != 0 {
		e.OopValue = Val(s, &code)
	} else {
		e.OopValue = -1
	}
}

func (e *Engine) OopSkipLine(statId int16, position *int16) {
	for {
		e.OopReadChar(statId, position)
		if e.OopChar == '\x00' || e.OopChar == '\r' {
			break
		}
	}
}

func (e *Engine) OopParseDirection(statId int16, position *int16, dx, dy *int16) (OopParseDirection_ bool) {
	stat := &e.Board.Stats[statId]
	OopParseDirection_ = true
	if e.OopWord == "N" || e.OopWord == "NORTH" {
		*dx = 0
		*dy = -1
	} else if e.OopWord == "S" || e.OopWord == "SOUTH" {
		*dx = 0
		*dy = 1
	} else if e.OopWord == "E" || e.OopWord == "EAST" {
		*dx = 1
		*dy = 0
	} else if e.OopWord == "W" || e.OopWord == "WEST" {
		*dx = -1
		*dy = 0
	} else if e.OopWord == "I" || e.OopWord == "IDLE" {
		*dx = 0
		*dy = 0
	} else if e.OopWord == "SEEK" {
		e.CalcDirectionSeek(int16(stat.X), int16(stat.Y), dx, dy)
	} else if e.OopWord == "FLOW" {
		*dx = stat.StepX
		*dy = stat.StepY
	} else if e.OopWord == "RND" {
		e.CalcDirectionRnd(dx, dy)
	} else if e.OopWord == "RNDNS" {
		*dx = 0
		*dy = e.Random(2)*2 - 1
	} else if e.OopWord == "RNDNE" {
		*dx = e.Random(2)
		if *dx == 0 {
			*dy = -1
		} else {
			*dy = 0
		}
	} else if e.OopWord == "CW" {
		e.OopReadWord(statId, position)
		OopParseDirection_ = e.OopParseDirection(statId, position, dy, dx)
		*dx = -*dx
	} else if e.OopWord == "CCW" {
		e.OopReadWord(statId, position)
		OopParseDirection_ = e.OopParseDirection(statId, position, dy, dx)
		*dy = -*dy
	} else if e.OopWord == "RNDP" {
		e.OopReadWord(statId, position)
		OopParseDirection_ = e.OopParseDirection(statId, position, dy, dx)
		if e.Random(2) == 0 {
			*dx = -*dx
		} else {
			*dy = -*dy
		}
	} else if e.OopWord == "OPP" {
		e.OopReadWord(statId, position)
		OopParseDirection_ = e.OopParseDirection(statId, position, dx, dy)
		*dx = -*dx
		*dy = -*dy
	} else {
		*dx = 0
		*dy = 0
		OopParseDirection_ = false
	}

	return
}

func (e *Engine) OopReadDirection(statId int16, position *int16, dx, dy *int16) {
	e.OopReadWord(statId, position)
	if !e.OopParseDirection(statId, position, dx, dy) {
		e.OopError(statId, "Bad direction")
	}
}

func (e *Engine) OopFindString(statId int16, s string) (OopFindString int16) {
	var pos, wordPos, cmpPos int16
	stat := &e.Board.Stats[statId]
	pos = 0
	for pos <= stat.DataLen {
		wordPos = 1
		cmpPos = pos
		for {
			e.OopReadChar(statId, &cmpPos)
			if UpCase(s[wordPos-1]) != UpCase(e.OopChar) {
				goto NoMatch
			}
			wordPos++
			if wordPos > Length(s) {
				break
			}
		}
		e.OopReadChar(statId, &cmpPos)
		e.OopChar = UpCase(e.OopChar)
		if e.OopChar >= 'A' && e.OopChar <= 'Z' || e.OopChar == '_' {
		} else {
			OopFindString = pos
			return
		}
	NoMatch:
		pos++

	}
	OopFindString = -1
	return
}

func (e *Engine) OopIterateStat(statId int16, iStat *int16, lookup string) (OopIterateStat bool) {
	var (
		pos   int16
		found bool
	)
	*iStat++
	found = false
	if lookup == "ALL" {
		if *iStat <= e.Board.StatCount {
			found = true
		}
	} else if lookup == "OTHERS" {
		if *iStat <= e.Board.StatCount {
			if *iStat != statId {
				found = true
			} else {
				*iStat++
				found = *iStat <= e.Board.StatCount
			}
		}
	} else if lookup == "SELF" {
		if statId > 0 && *iStat <= statId {
			*iStat = statId
			found = true
		}
	} else {
		for *iStat <= e.Board.StatCount && !found {
			if e.Board.Stats[*iStat].Data != "" {
				pos = 0
				e.OopReadChar(*iStat, &pos)
				if e.OopChar == '@' {
					e.OopReadWord(*iStat, &pos)
					if e.OopWord == lookup {
						found = true
					}
				}
			}
			if !found {
				*iStat++
			}
		}
	}

	OopIterateStat = found
	return
}

func (e *Engine) OopFindLabel(statId int16, sendLabel string, iStat, iDataPos *int16, labelPrefix string) (OopFindLabel bool) {
	var (
		targetSplitPos int16
		targetLookup   string
		objectMessage  string
		foundStat      bool
	)
	foundStat = false
	targetSplitPos = Pos(':', sendLabel)
	if targetSplitPos <= 0 {
		if *iStat < statId {
			objectMessage = sendLabel
			*iStat = statId
			targetSplitPos = 0
			foundStat = true
		}
	} else {
		targetLookup = Copy(sendLabel, 1, targetSplitPos-1)
		objectMessage = Copy(sendLabel, targetSplitPos+1, Length(sendLabel)-targetSplitPos)
		foundStat = e.OopIterateStat(statId, iStat, targetLookup)
	}
FindNextStat:
	if foundStat {
		if objectMessage == "RESTART" {
			*iDataPos = 0
		} else {
			*iDataPos = e.OopFindString(*iStat, labelPrefix+objectMessage)
			if *iDataPos < 0 && targetSplitPos > 0 {
				foundStat = e.OopIterateStat(statId, iStat, targetLookup)
				goto FindNextStat
			}
		}
		foundStat = *iDataPos >= 0
	}
	OopFindLabel = foundStat
	return
}

func (e *Engine) WorldGetFlagPosition(name string) (WorldGetFlagPosition int16) {
	var i int16
	WorldGetFlagPosition = -1
	for i = 1; i <= 10; i++ {
		if e.World.Info.Flags[i-1] == name {
			WorldGetFlagPosition = i
		}
	}
	return
}

func (e *Engine) WorldSetFlag(name string) {
	var i int16
	if e.WorldGetFlagPosition(name) < 0 {
		i = 1
		for i < MAX_FLAG && Length(e.World.Info.Flags[i-1]) != 0 {
			i++
		}
		e.World.Info.Flags[i-1] = name
	}
}

func (e *Engine) WorldClearFlag(name string) {
	if e.WorldGetFlagPosition(name) >= 0 {
		e.World.Info.Flags[e.WorldGetFlagPosition(name)-1] = ""
	}
}

func OopStringToWord(input string) (OopStringToWord string) {
	var (
		output string
		i      int16
	)
	output = ""
	for i = 1; i <= Length(input); i++ {
		if input[i-1] >= 'A' && input[i-1] <= 'Z' || input[i-1] >= '0' && input[i-1] <= '9' {
			output += string([]byte{input[i-1]})
		} else if input[i-1] >= 'a' && input[i-1] <= 'z' {
			output += Chr(Ord(input[i-1]) - 0x20)
		}

	}
	OopStringToWord = output
	return
}

func (e *Engine) OopParseTile(statId, position *int16, tile *TTile) (OopParseTile bool) {
	var i int16
	OopParseTile = false
	tile.Color = 0
	e.OopReadWord(*statId, position)
	for i = 1; i <= 7; i++ {
		if e.OopWord == OopStringToWord(ColorNames[i-1]) {
			tile.Color = byte(i + 0x08)
			e.OopReadWord(*statId, position)
			goto ColorFound
		}
	}
ColorFound:
	for i = 0; i <= MAX_ELEMENT; i++ {
		if e.OopWord == OopStringToWord(ElementDefs[i].Name) {
			OopParseTile = true
			tile.Element = byte(i)
			return
		}
	}

	return
}

func GetColorForTileMatch(tile *TTile) (GetColorForTileMatch byte) {
	if ElementDefs[tile.Element].Color < COLOR_SPECIAL_MIN {
		GetColorForTileMatch = byte(int16(ElementDefs[tile.Element].Color) & 0x07)
	} else if ElementDefs[tile.Element].Color == COLOR_WHITE_ON_CHOICE {
		GetColorForTileMatch = byte(int16(tile.Color)>>4&0x0F + 8)
	} else {
		GetColorForTileMatch = byte(int16(tile.Color) & 0x0F)
	}

	return
}

func (e *Engine) FindTileOnBoard(x, y *int16, tile TTile) (FindTileOnBoard bool) {
	FindTileOnBoard = false
	for true {
		*x++
		if *x > BOARD_WIDTH {
			*x = 1
			*y++
			if *y > BOARD_HEIGHT {
				return
			}
		}
		if e.Board.Tiles[*x][*y].Element == tile.Element {
			if tile.Color == 0 || GetColorForTileMatch(&e.Board.Tiles[*x][*y]) == tile.Color {
				FindTileOnBoard = true
				return
			}
		}
	}
	return
}

func (e *Engine) OopPlaceTile(x, y int16, tile *TTile) {
	var color byte
	if e.Board.Tiles[x][y].Element != 4 {
		color = tile.Color
		if ElementDefs[tile.Element].Color < COLOR_SPECIAL_MIN {
			color = ElementDefs[tile.Element].Color
		} else {
			if color == 0 {
				color = e.Board.Tiles[x][y].Color
			}
			if color == 0 {
				color = 0x0F
			}
			if ElementDefs[tile.Element].Color == COLOR_WHITE_ON_CHOICE {
				color = byte((int16(color)-8)*0x10 + 0x0F)
			}
		}
		if e.Board.Tiles[x][y].Element == tile.Element {
			e.Board.Tiles[x][y].Color = color
		} else {
			e.BoardDamageTile(x, y)
			if ElementDefs[tile.Element].Cycle >= 0 {
				e.AddStat(x, y, tile.Element, int16(color), ElementDefs[tile.Element].Cycle, StatTemplateDefault)
			} else {
				e.Board.Tiles[x][y].Element = tile.Element
				e.Board.Tiles[x][y].Color = color
			}
		}
		e.BoardDrawTile(x, y)
	}
}

func (e *Engine) OopCheckCondition(statId int16, position *int16) (OopCheckCondition_ bool) {
	var (
		deltaX, deltaY int16
		tile           TTile
		ix, iy         int16
	)
	stat := &e.Board.Stats[statId]
	if e.OopWord == "NOT" {
		e.OopReadWord(statId, position)
		OopCheckCondition_ = !e.OopCheckCondition(statId, position)
	} else if e.OopWord == "ALLIGNED" {
		OopCheckCondition_ = stat.X == e.Board.Stats[0].X || stat.Y == e.Board.Stats[0].Y
	} else if e.OopWord == "CONTACT" {
		OopCheckCondition_ = Sqr(int16(stat.X)-int16(e.Board.Stats[0].X))+Sqr(int16(stat.Y)-int16(e.Board.Stats[0].Y)) == 1
	} else if e.OopWord == "BLOCKED" {
		e.OopReadDirection(statId, position, &deltaX, &deltaY)
		OopCheckCondition_ = !ElementDefs[e.Board.Tiles[int16(stat.X)+deltaX][int16(stat.Y)+deltaY].Element].Walkable
	} else if e.OopWord == "ENERGIZED" {
		OopCheckCondition_ = e.World.Info.EnergizerTicks > 0
	} else if e.OopWord == "ANY" {
		if !e.OopParseTile(&statId, position, &tile) {
			e.OopError(statId, "Bad object kind")
		}
		ix = 0
		iy = 1
		OopCheckCondition_ = e.FindTileOnBoard(&ix, &iy, tile)
	} else {
		OopCheckCondition_ = e.WorldGetFlagPosition(e.OopWord) >= 0
	}

	return
}

func (e *Engine) OopReadLineToEnd(statId int16, position *int16) (OopReadLineToEnd string) {
	var s string
	s = ""
	e.OopReadChar(statId, position)
	for e.OopChar != '\x00' && e.OopChar != '\r' {
		s += string([]byte{e.OopChar})
		e.OopReadChar(statId, position)
	}
	OopReadLineToEnd = s
	return
}

func (e *Engine) OopSend(statId int16, sendLabel string, ignoreLock bool) (OopSend bool) {
	var (
		iDataPos, iStat int16
		ignoreSelfLock  bool
	)
	if statId < 0 {
		statId = -statId
		ignoreSelfLock = true
	} else {
		ignoreSelfLock = false
	}
	OopSend = false
	iStat = 0
	for e.OopFindLabel(statId, sendLabel, &iStat, &iDataPos, "\r:") {
		if e.Board.Stats[iStat].P2 == 0 || ignoreLock || statId == iStat && !ignoreSelfLock {
			if iStat == statId {
				OopSend = true
			}
			e.Board.Stats[iStat].DataPos = iDataPos
		}
	}
	return
}

func (e *Engine) OopExecute(statId int16, position *int16, name string) {
	var (
		textWindow        TTextWindowState
		textLine          string
		deltaX, deltaY    int16
		ix, iy            int16
		stopRunning       bool
		replaceStat       bool
		endOfProgram      bool
		replaceTile       TTile
		namePosition      int16
		lastPosition      int16
		repeatInsNextTick bool
		lineFinished      bool
		labelDataPos      int16
		labelStatId       int16
		counterPtr        *int16
		counterSubtract   bool
		bindStatId        int16
		insCount          int16
		argTile           TTile
		argTile2          TTile
	)
	stat := &e.Board.Stats[statId]
StartParsing:
	TextWindowInitState(&textWindow)

	textWindow.Selectable = false
	stopRunning = false
	repeatInsNextTick = false
	replaceStat = false
	endOfProgram = false
	insCount = 0
	for {
	ReadInstruction:
		lineFinished = true

		lastPosition = *position
		e.OopReadChar(statId, position)
		for e.OopChar == ':' {
			for {
				e.OopReadChar(statId, position)
				if e.OopChar == '\x00' || e.OopChar == '\r' {
					break
				}
			}
			e.OopReadChar(statId, position)
		}
		if e.OopChar == '\'' {
			e.OopSkipLine(statId, position)
		} else if e.OopChar == '@' {
			e.OopSkipLine(statId, position)
		} else if e.OopChar == '/' || e.OopChar == '?' {
			if e.OopChar == '/' {
				repeatInsNextTick = true
			}
			e.OopReadWord(statId, position)
			if e.OopParseDirection(statId, position, &deltaX, &deltaY) {
				if deltaX != 0 || deltaY != 0 {
					if !ElementDefs[e.Board.Tiles[int16(stat.X)+deltaX][int16(stat.Y)+deltaY].Element].Walkable {
						e.ElementPushablePush(int16(stat.X)+deltaX, int16(stat.Y)+deltaY, deltaX, deltaY)
					}
					if ElementDefs[e.Board.Tiles[int16(stat.X)+deltaX][int16(stat.Y)+deltaY].Element].Walkable {
						e.MoveStat(statId, int16(stat.X)+deltaX, int16(stat.Y)+deltaY)
						repeatInsNextTick = false
					}
				} else {
					repeatInsNextTick = false
				}
				e.OopReadChar(statId, position)
				if e.OopChar != '\r' {
					*position--
				}
				stopRunning = true
			} else {
				e.OopError(statId, "Bad direction")
			}
		} else if e.OopChar == '#' {
		ReadCommand:
			e.OopReadWord(statId, position)

			if e.OopWord == "THEN" {
				e.OopReadWord(statId, position)
			}
			if Length(e.OopWord) == 0 {
				goto ReadInstruction
			}
			insCount++
			if Length(e.OopWord) != 0 {
				if e.OopWord == "GO" {
					e.OopReadDirection(statId, position, &deltaX, &deltaY)
					if !ElementDefs[e.Board.Tiles[int16(stat.X)+deltaX][int16(stat.Y)+deltaY].Element].Walkable {
						e.ElementPushablePush(int16(stat.X)+deltaX, int16(stat.Y)+deltaY, deltaX, deltaY)
					}
					if ElementDefs[e.Board.Tiles[int16(stat.X)+deltaX][int16(stat.Y)+deltaY].Element].Walkable {
						e.MoveStat(statId, int16(stat.X)+deltaX, int16(stat.Y)+deltaY)
					} else {
						repeatInsNextTick = true
					}
					stopRunning = true
				} else if e.OopWord == "TRY" {
					e.OopReadDirection(statId, position, &deltaX, &deltaY)
					if !ElementDefs[e.Board.Tiles[int16(stat.X)+deltaX][int16(stat.Y)+deltaY].Element].Walkable {
						e.ElementPushablePush(int16(stat.X)+deltaX, int16(stat.Y)+deltaY, deltaX, deltaY)
					}
					if ElementDefs[e.Board.Tiles[int16(stat.X)+deltaX][int16(stat.Y)+deltaY].Element].Walkable {
						e.MoveStat(statId, int16(stat.X)+deltaX, int16(stat.Y)+deltaY)
						stopRunning = true
					} else {
						goto ReadCommand
					}
				} else if e.OopWord == "WALK" {
					e.OopReadDirection(statId, position, &deltaX, &deltaY)
					stat.StepX = deltaX
					stat.StepY = deltaY
				} else if e.OopWord == "SET" {
					e.OopReadWord(statId, position)
					e.WorldSetFlag(e.OopWord)
				} else if e.OopWord == "CLEAR" {
					e.OopReadWord(statId, position)
					e.WorldClearFlag(e.OopWord)
				} else if e.OopWord == "IF" {
					e.OopReadWord(statId, position)
					if e.OopCheckCondition(statId, position) {
						goto ReadCommand
					}
				} else if e.OopWord == "SHOOT" {
					e.OopReadDirection(statId, position, &deltaX, &deltaY)
					if e.BoardShoot(E_BULLET, int16(stat.X), int16(stat.Y), deltaX, deltaY, SHOT_SOURCE_ENEMY) {
						SoundQueue(2, "0\x01&\x01")
					}
					stopRunning = true
				} else if e.OopWord == "THROWSTAR" {
					e.OopReadDirection(statId, position, &deltaX, &deltaY)
					if e.BoardShoot(E_STAR, int16(stat.X), int16(stat.Y), deltaX, deltaY, SHOT_SOURCE_ENEMY) {
					}
					stopRunning = true
				} else if e.OopWord == "GIVE" || e.OopWord == "TAKE" {
					if e.OopWord == "TAKE" {
						counterSubtract = true
					} else {
						counterSubtract = false
					}
					e.OopReadWord(statId, position)
					if e.OopWord == "HEALTH" {
						counterPtr = &e.World.Info.Health
					} else if e.OopWord == "AMMO" {
						counterPtr = &e.World.Info.Ammo
					} else if e.OopWord == "GEMS" {
						counterPtr = &e.World.Info.Gems
					} else if e.OopWord == "TORCHES" {
						counterPtr = &e.World.Info.Torches
					} else if e.OopWord == "SCORE" {
						counterPtr = &e.World.Info.Score
					} else if e.OopWord == "TIME" {
						counterPtr = &e.World.Info.BoardTimeSec
					} else {
						counterPtr = nil
					}

					if counterPtr != nil {
						e.OopReadValue(statId, position)
						if e.OopValue > 0 {
							if counterSubtract {
								e.OopValue = -e.OopValue
							}
							if *counterPtr+e.OopValue >= 0 {
								*counterPtr += e.OopValue
							} else {
								goto ReadCommand
							}
						}
					}
					e.GameUpdateSidebar()
				} else if e.OopWord == "END" {
					*position = -1
					e.OopChar = '\x00'
				} else if e.OopWord == "ENDGAME" {
					e.World.Info.Health = 0
				} else if e.OopWord == "IDLE" {
					stopRunning = true
				} else if e.OopWord == "RESTART" {
					*position = 0
					lineFinished = false
				} else if e.OopWord == "ZAP" {
					e.OopReadWord(statId, position)
					labelStatId = 0
					for e.OopFindLabel(statId, e.OopWord, &labelStatId, &labelDataPos, "\r:") {
						e.Board.Stats[labelStatId].Data = Replace(e.Board.Stats[labelStatId].Data, labelDataPos+1, '\'')
					}
				} else if e.OopWord == "RESTORE" {
					e.OopReadWord(statId, position)
					labelStatId = 0
					for e.OopFindLabel(statId, e.OopWord, &labelStatId, &labelDataPos, "\r'") {
						for {
							e.Board.Stats[labelStatId].Data = Replace(e.Board.Stats[labelStatId].Data, labelDataPos+1, ':')
							labelDataPos = e.OopFindString(labelStatId, "\r'"+e.OopWord+"\r")
							if labelDataPos <= 0 {
								break
							}
						}
					}
				} else if e.OopWord == "LOCK" {
					stat.P2 = 1
				} else if e.OopWord == "UNLOCK" {
					stat.P2 = 0
				} else if e.OopWord == "SEND" {
					e.OopReadWord(statId, position)
					if e.OopSend(statId, e.OopWord, false) {
						lineFinished = false
					}
				} else if e.OopWord == "BECOME" {
					if e.OopParseTile(&statId, position, &argTile) {
						replaceStat = true
						replaceTile.Element = argTile.Element
						replaceTile.Color = argTile.Color
					} else {
						e.OopError(statId, "Bad #BECOME")
					}
				} else if e.OopWord == "PUT" {
					e.OopReadDirection(statId, position, &deltaX, &deltaY)
					if deltaX == 0 && deltaY == 0 {
						e.OopError(statId, "Bad #PUT")
					} else if !e.OopParseTile(&statId, position, &argTile) {
						e.OopError(statId, "Bad #PUT")
					} else if int16(stat.X)+deltaX > 0 && int16(stat.X)+deltaX <= BOARD_WIDTH && int16(stat.Y)+deltaY > 0 && int16(stat.Y)+deltaY < BOARD_HEIGHT {
						if !ElementDefs[e.Board.Tiles[int16(stat.X)+deltaX][int16(stat.Y)+deltaY].Element].Walkable {
							e.ElementPushablePush(int16(stat.X)+deltaX, int16(stat.Y)+deltaY, deltaX, deltaY)
						}
						e.OopPlaceTile(int16(stat.X)+deltaX, int16(stat.Y)+deltaY, &argTile)
					}

				} else if e.OopWord == "CHANGE" {
					if !e.OopParseTile(&statId, position, &argTile) {
						e.OopError(statId, "Bad #CHANGE")
					}
					if !e.OopParseTile(&statId, position, &argTile2) {
						e.OopError(statId, "Bad #CHANGE")
					}
					ix = 0
					iy = 1
					if argTile2.Color == 0 && ElementDefs[argTile2.Element].Color < COLOR_SPECIAL_MIN {
						argTile2.Color = ElementDefs[argTile2.Element].Color
					}
					for e.FindTileOnBoard(&ix, &iy, argTile) {
						e.OopPlaceTile(ix, iy, &argTile2)
					}
				} else if e.OopWord == "PLAY" {
					textLine = SoundParse(e.OopReadLineToEnd(statId, position))
					if Length(textLine) != 0 {
						SoundQueue(-1, textLine)
					}
					lineFinished = false
				} else if e.OopWord == "CYCLE" {
					e.OopReadValue(statId, position)
					if e.OopValue > 0 {
						stat.Cycle = e.OopValue
					}
				} else if e.OopWord == "CHAR" {
					e.OopReadValue(statId, position)
					if e.OopValue > 0 && e.OopValue <= 255 {
						stat.P1 = byte(e.OopValue)
						e.BoardDrawTile(int16(stat.X), int16(stat.Y))
					}
				} else if e.OopWord == "DIE" {
					replaceStat = true
					replaceTile.Element = E_EMPTY
					replaceTile.Color = 0x0F
				} else if e.OopWord == "BIND" {
					e.OopReadWord(statId, position)
					bindStatId = 0
					if e.OopIterateStat(statId, &bindStatId, e.OopWord) {
						stat.Data = e.Board.Stats[bindStatId].Data
						stat.DataLen = e.Board.Stats[bindStatId].DataLen
						*position = 0
					}
				} else {
					textLine = e.OopWord
					if e.OopSend(statId, e.OopWord, false) {
						lineFinished = false
					} else {
						if Pos(':', textLine) <= 0 {
							e.OopError(statId, "Bad command "+textLine)
						}
					}
				}

			}
			if lineFinished {
				e.OopSkipLine(statId, position)
			}
		} else if e.OopChar == '\r' {
			if textWindow.LineCount > 0 {
				TextWindowAppend(&textWindow, "")
			}
		} else if e.OopChar == '\x00' {
			endOfProgram = true
		} else {
			textLine = string([]byte{e.OopChar})
			textLine += e.OopReadLineToEnd(statId, position)
			TextWindowAppend(&textWindow, textLine)
		}

		if endOfProgram || stopRunning || repeatInsNextTick || replaceStat || insCount > 32 {
			break
		}
	}
	if repeatInsNextTick {
		*position = lastPosition
	}
	if e.OopChar == '\x00' {
		*position = -1
	}
	if textWindow.LineCount > 1 {
		namePosition = 0
		e.OopReadChar(statId, &namePosition)
		if e.OopChar == '@' {
			name = e.OopReadLineToEnd(statId, &namePosition)
		}
		if Length(name) == 0 {
			name = "Interaction"
		}
		textWindow.Title = name
		TextWindowDrawOpen(&textWindow)
		TextWindowSelect(&textWindow, true, false)
		TextWindowDrawClose(&textWindow)
		TextWindowFree(&textWindow)
		if Length(textWindow.Hyperlink) != 0 {
			if e.OopSend(statId, textWindow.Hyperlink, false) {
				goto StartParsing
			}
		}
	} else if textWindow.LineCount == 1 {
		e.DisplayMessage(200, textWindow.Lines[0])
		TextWindowFree(&textWindow)
	}

	if replaceStat {
		ix = int16(stat.X)
		iy = int16(stat.Y)
		e.DamageStat(statId)
		e.OopPlaceTile(ix, iy, &replaceTile)
	}
}

// --- Global Wrappers ---

func FindTileOnBoard(x, y *int16, tile TTile) (FindTileOnBoard bool) {
	return E.FindTileOnBoard(x, y, tile)
}

func OopCheckCondition(statId int16, position *int16) (OopCheckCondition_ bool) {
	return E.OopCheckCondition(statId, position)
}

func OopError(statId int16, message string)  {
	E.OopError(statId, message)
}

func OopExecute(statId int16, position *int16, name string)  {
	E.OopExecute(statId, position, name)
}

func OopFindLabel(statId int16, sendLabel string, iStat, iDataPos *int16, labelPrefix string) (OopFindLabel bool) {
	return E.OopFindLabel(statId, sendLabel, iStat, iDataPos, labelPrefix)
}

func OopFindString(statId int16, s string) (OopFindString int16) {
	return E.OopFindString(statId, s)
}

func OopIterateStat(statId int16, iStat *int16, lookup string) (OopIterateStat bool) {
	return E.OopIterateStat(statId, iStat, lookup)
}

func OopParseDirection(statId int16, position *int16, dx, dy *int16) (OopParseDirection_ bool) {
	return E.OopParseDirection(statId, position, dx, dy)
}

func OopParseTile(statId, position *int16, tile *TTile) (OopParseTile bool) {
	return E.OopParseTile(statId, position, tile)
}

func OopPlaceTile(x, y int16, tile *TTile)  {
	E.OopPlaceTile(x, y, tile)
}

func OopReadChar(statId int16, position *int16)  {
	E.OopReadChar(statId, position)
}

func OopReadDirection(statId int16, position *int16, dx, dy *int16)  {
	E.OopReadDirection(statId, position, dx, dy)
}

func OopReadLineToEnd(statId int16, position *int16) (OopReadLineToEnd string) {
	return E.OopReadLineToEnd(statId, position)
}

func OopReadValue(statId int16, position *int16)  {
	E.OopReadValue(statId, position)
}

func OopReadWord(statId int16, position *int16)  {
	E.OopReadWord(statId, position)
}

func OopSend(statId int16, sendLabel string, ignoreLock bool) (OopSend bool) {
	return E.OopSend(statId, sendLabel, ignoreLock)
}

func OopSkipLine(statId int16, position *int16)  {
	E.OopSkipLine(statId, position)
}

func WorldClearFlag(name string)  {
	E.WorldClearFlag(name)
}

func WorldGetFlagPosition(name string) (WorldGetFlagPosition int16) {
	return E.WorldGetFlagPosition(name)
}

func WorldSetFlag(name string)  {
	E.WorldSetFlag(name)
}
