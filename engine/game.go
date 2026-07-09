package main // unit: Game

import (
	"os"
	"path/filepath"
)

// interface uses: GameVars, TxtWind

const (
	PROMPT_NUMERIC  = 0
	PROMPT_ALPHANUM = 1
	PROMPT_ANY      = 2
)
const LineChars string = "\xf9\xd0Һ\xb5\xbc\xbb\xb9\xc6\xc8\xc9\xcc\xcd\xca\xcb\xce"

var (
	ProgressAnimColors  [8]byte   = [8]byte{0x14, 0x1C, 0x15, 0x1D, 0x16, 0x1E, 0x17, 0x1F}
	ProgressAnimStrings [8]string = [8]string{"....|", "...*/", "..*.-", ".*..\\", "*...|", "..../", "....-", "....\\"}
	ColorNames          [7]string = [7]string{"Blue", "Green", "Cyan", "Red", "Purple", "Yellow", "White"}
	DiagonalDeltaX      [8]int16  = [8]int16{-1, 0, 1, 1, 1, 0, -1, -1}
	DiagonalDeltaY      [8]int16  = [8]int16{1, 1, 1, 0, -1, -1, -1, 0}
	NeighborDeltaX      [4]int16  = [4]int16{0, 0, -1, 1}
	NeighborDeltaY      [4]int16  = [4]int16{-1, 1, 0, 0}
	TileBorder          TTile     = TTile{Element: E_NORMAL, Color: 0x0E}
	TileBoardEdge       TTile     = TTile{Element: E_BOARD_EDGE, Color: 0x00}
	StatTemplateDefault TStat     = TStat{X: 0, Y: 0, StepX: 0, StepY: 0, Cycle: 0, P1: 0, P2: 0, P3: 0, Follower: -1, Leader: -1}
)

// implementation uses: Dos, Crt, Video, Sounds, Input, Elements, Editor, Oop

func (e *Engine) SidebarClearLine(y int16) {
	e.VideoWriteText(60, y, 0x11, "\xb3                   ")
}

func (e *Engine) SidebarClear() {
	var i int16
	for i = 3; i <= 24; i++ {
		e.SidebarClearLine(i)
	}
}

func (e *Engine) GenerateTransitionTable() {
	var (
		ix, iy int16
		t      TCoord
	)
	e.TransitionTableSize = 0
	for iy = 1; iy <= BOARD_HEIGHT; iy++ {
		for ix = 1; ix <= BOARD_WIDTH; ix++ {
			e.TransitionTableSize++
			e.TransitionTable[e.TransitionTableSize-1].X = ix
			e.TransitionTable[e.TransitionTableSize-1].Y = iy
		}
	}
	for ix = 1; ix <= e.TransitionTableSize; ix++ {
		iy = e.Random(e.TransitionTableSize) + 1
		t = e.TransitionTable[iy-1]
		e.TransitionTable[iy-1] = e.TransitionTable[ix-1]
		e.TransitionTable[ix-1] = t
	}
}

func (e *Engine) BoardClose() {
	var (
		ix, iy int16
		ptr    []byte
		rle    TRleTile
	)
	ptr = e.IoTmpBuf[:]
	StoreString(ptr[:SizeOfBoardName], e.Board.Name)
	ptr = ptr[SizeOfBoardName:]

	ix = 1
	iy = 1
	rle.Count = 1
	rle.Tile = e.Board.Tiles[ix][iy]
	for {
		ix++
		if ix > BOARD_WIDTH {
			ix = 1
			iy++
		}
		if e.Board.Tiles[ix][iy].Color == rle.Tile.Color && e.Board.Tiles[ix][iy].Element == rle.Tile.Element && rle.Count < 255 && iy <= BOARD_HEIGHT {
			rle.Count++
		} else {
			StoreRleTile(ptr[:SizeOfRleTile], rle)
			ptr = ptr[SizeOfRleTile:]
			rle.Tile = e.Board.Tiles[ix][iy]
			rle.Count = 1
		}
		if iy > BOARD_HEIGHT {
			break
		}
	}

	StoreBoardInfo(ptr[:SizeOfBoardInfo], &e.Board.Info)
	ptr = ptr[SizeOfBoardInfo:]

	StoreInt16(ptr[:2], e.Board.StatCount)
	ptr = ptr[2:]

	for ix = 0; ix <= e.Board.StatCount; ix++ {
		stat := &e.Board.Stats[ix]
		if stat.DataLen > 0 {
			for iy = 1; iy <= ix-1; iy++ {
				if e.Board.Stats[iy].Data == stat.Data {
					stat.DataLen = -iy
				}
			}
		}
		StoreStat(ptr[:SizeOfStat], &e.Board.Stats[ix])
		ptr = ptr[SizeOfStat:]

		if stat.DataLen > 0 {
			copy(ptr[:stat.DataLen], stat.Data)
			ptr = ptr[stat.DataLen:]
		}
	}

	boardData := e.IoTmpBuf[:len(e.IoTmpBuf)-len(ptr)]
	e.World.BoardLen[e.World.Info.CurrentBoard] = int16(len(boardData))
	e.World.BoardData[e.World.Info.CurrentBoard] = make([]byte, len(boardData))
	copy(e.World.BoardData[e.World.Info.CurrentBoard], boardData)
}

func (e *Engine) BoardOpen(boardId int16) {
	var (
		ptr    []byte
		ix, iy int16
		rle    TRleTile
	)
	if boardId > e.World.BoardCount {
		boardId = e.World.Info.CurrentBoard
	}
	ptr = e.World.BoardData[boardId]
	e.Board.Name = LoadString(ptr[:SizeOfBoardName])
	ptr = ptr[SizeOfBoardName:]

	ix = 1
	iy = 1
	rle.Count = 0
	for {
		if rle.Count <= 0 {
			rle = LoadRleTile(ptr[:SizeOfRleTile])
			ptr = ptr[SizeOfRleTile:]
		}
		e.Board.Tiles[ix][iy] = rle.Tile
		ix++
		if ix > BOARD_WIDTH {
			ix = 1
			iy++
		}
		rle.Count--
		if iy > BOARD_HEIGHT {
			break
		}
	}

	LoadBoardInfo(ptr[:SizeOfBoardInfo], &e.Board.Info)
	ptr = ptr[SizeOfBoardInfo:]

	e.Board.StatCount = LoadInt16(ptr[:2])
	ptr = ptr[2:]

	for ix = 0; ix <= e.Board.StatCount; ix++ {
		stat := &e.Board.Stats[ix]
		LoadStat(ptr[:SizeOfStat], &e.Board.Stats[ix])
		ptr = ptr[SizeOfStat:]

		if stat.DataLen > 0 {
			stat.Data = string(ptr[:stat.DataLen])
			ptr = ptr[stat.DataLen:]
		} else if stat.DataLen < 0 {
			stat.Data = e.Board.Stats[-stat.DataLen].Data
			stat.DataLen = e.Board.Stats[-stat.DataLen].DataLen
		}

	}

	e.World.Info.CurrentBoard = boardId
}

func (e *Engine) BoardChange(boardId int16) {
	e.Board.Tiles[e.Board.Stats[0].X][e.Board.Stats[0].Y].Element = E_PLAYER
	e.Board.Tiles[e.Board.Stats[0].X][e.Board.Stats[0].Y].Color = ElementDefs[E_PLAYER].Color
	e.BoardClose()
	e.BoardOpen(boardId)
}

func (e *Engine) BoardCreate() {
	var ix, iy, i int16
	e.Board.Name = ""
	e.Board.Info.Message = ""
	e.Board.Info.MaxShots = 255
	e.Board.Info.IsDark = false
	e.Board.Info.ReenterWhenZapped = false
	e.Board.Info.TimeLimitSec = 0
	for i = 0; i <= 3; i++ {
		e.Board.Info.NeighborBoards[i] = 0
	}
	for ix = 0; ix <= BOARD_WIDTH+1; ix++ {
		e.Board.Tiles[ix][0] = TileBoardEdge
		e.Board.Tiles[ix][BOARD_HEIGHT+1] = TileBoardEdge
	}
	for iy = 0; iy <= BOARD_HEIGHT+1; iy++ {
		e.Board.Tiles[0][iy] = TileBoardEdge
		e.Board.Tiles[BOARD_WIDTH+1][iy] = TileBoardEdge
	}
	for ix = 1; ix <= BOARD_WIDTH; ix++ {
		for iy = 1; iy <= BOARD_HEIGHT; iy++ {
			e.Board.Tiles[ix][iy].Element = E_EMPTY
			e.Board.Tiles[ix][iy].Color = 0
		}
	}
	for ix = 1; ix <= BOARD_WIDTH; ix++ {
		e.Board.Tiles[ix][1] = TileBorder
		e.Board.Tiles[ix][BOARD_HEIGHT] = TileBorder
	}
	for iy = 1; iy <= BOARD_HEIGHT; iy++ {
		e.Board.Tiles[1][iy] = TileBorder
		e.Board.Tiles[BOARD_WIDTH][iy] = TileBorder
	}
	e.Board.Tiles[BOARD_WIDTH/2][BOARD_HEIGHT/2].Element = E_PLAYER
	e.Board.Tiles[BOARD_WIDTH/2][BOARD_HEIGHT/2].Color = ElementDefs[E_PLAYER].Color
	e.Board.StatCount = 0
	e.Board.Stats[0].X = BOARD_WIDTH / 2
	e.Board.Stats[0].Y = BOARD_HEIGHT / 2
	e.Board.Stats[0].Cycle = 1
	e.Board.Stats[0].Under.Element = E_EMPTY
	e.Board.Stats[0].Under.Color = 0
	e.Board.Stats[0].Data = ""
	e.Board.Stats[0].DataLen = 0
}

func (e *Engine) WorldCreate() {
	var i int16
	e.InitElementsGame()
	e.World.BoardCount = 0
	e.World.BoardLen[0] = 0
	e.InitEditorStatSettings()
	e.ResetMessageNotShownFlags()
	e.BoardCreate()
	e.World.Info.IsSave = false
	e.World.Info.CurrentBoard = 0
	e.World.Info.Ammo = 0
	e.World.Info.Gems = 0
	e.World.Info.Health = 100
	e.World.Info.EnergizerTicks = 0
	e.World.Info.Torches = 0
	e.World.Info.TorchTicks = 0
	e.World.Info.Score = 0
	e.World.Info.BoardTimeSec = 0
	e.World.Info.BoardTimeHsec = 0
	for i = 1; i <= 7; i++ {
		e.World.Info.Keys[i-1] = false
	}
	pState := e.PlayerFor(0)
	pState.Health = 100
	pState.Ammo = 0
	pState.Gems = 0
	pState.Torches = 0
	pState.TorchTicks = 0
	pState.EnergizerTicks = 0
	pState.Score = 0
	pState.BoardTimeSec = 0
	pState.BoardTimeHsec = 0
	for i = 1; i <= 7; i++ {
		pState.Keys[i-1] = false
	}
	for i = 1; i <= 10; i++ {
		e.World.Info.Flags[i-1] = ""
	}
	e.BoardChange(0)
	e.Board.Name = "Title screen"
	e.LoadedGameFileName = ""
	e.World.Info.Name = ""
}

func (e *Engine) TransitionDrawToFill(chr byte, color int16) {
	var i int16
	for i = 1; i <= e.TransitionTableSize; i++ {
		e.VideoWriteText(e.TransitionTable[i-1].X-1, e.TransitionTable[i-1].Y-1, byte(color), string([]byte{chr}))
	}
}

func (e *Engine) TileToColorAndChar(x, y int16) (color, char byte) {
	var ch byte
	tile := &e.Board.Tiles[x][y]
	pId := e.NearestPlayer(x, y)
	pStat := &e.Board.Stats[pId]
	if !e.Board.Info.IsDark || ElementDefs[e.Board.Tiles[x][y].Element].VisibleInDark || e.PlayerFor(pId).TorchTicks > 0 && Sqr(int16(pStat.X)-x)+Sqr(int16(pStat.Y)-y)*2 < TORCH_DIST_SQR || e.ForceDarknessOff {
		if tile.Element == E_EMPTY {
			return 0x0F, ' '
		} else if ElementDefs[tile.Element].HasDrawProc {
			ElementDefs[tile.Element].DrawProc(e, x, y, &ch)
			return tile.Color, ch
		} else if tile.Element < E_TEXT_MIN {
			return tile.Color, ElementDefs[tile.Element].Character
		} else {
			if tile.Element == E_TEXT_WHITE {
				return 0x0F, e.Board.Tiles[x][y].Color
			} else {
				return byte((int16(tile.Element-E_TEXT_MIN)+1)*16 + 0x0F), e.Board.Tiles[x][y].Color
			}
		}
	} else {
		return 0x07, '\xb0'
	}
}

func (e *Engine) BoardDrawTile(x, y int16) {
	color, char := e.TileToColorAndChar(x, y)
	e.VideoWriteText(x-1, y-1, color, string([]byte{char}))
}

func (e *Engine) BoardDrawBorder() {
	var ix, iy int16
	for ix = 1; ix <= BOARD_WIDTH; ix++ {
		e.BoardDrawTile(ix, 1)
		e.BoardDrawTile(ix, BOARD_HEIGHT)
	}
	for iy = 1; iy <= BOARD_HEIGHT; iy++ {
		e.BoardDrawTile(1, iy)
		e.BoardDrawTile(BOARD_WIDTH, iy)
	}
}

func (e *Engine) TransitionDrawToBoard() {
	var i int16
	e.BoardDrawBorder()
	for i = 1; i <= e.TransitionTableSize; i++ {
		table := &e.TransitionTable[i-1]
		e.BoardDrawTile(table.X, table.Y)
	}
}

func (e *Engine) SidebarPromptCharacter(editable bool, x, y int16, prompt string, value *byte) {
	var i, newValue int16
	e.SidebarClearLine(y)
	e.VideoWriteText(x, y, byte(BoolToInt(editable)+0x1E), prompt)
	e.SidebarClearLine(y + 1)
	e.VideoWriteText(x+5, y+1, 0x9F, "\x1f")
	e.SidebarClearLine(y + 2)
	for {
		for i = int16(*value) - 4; i <= int16(*value)+4; i++ {
			e.VideoWriteText(x+i-int16(*value)+5, y+2, 0x1E, Chr(byte((i+0x100)%0x100)))
		}
		if editable {
			InputReadWaitKey()
			if InputKeyPressed == KEY_TAB {
				InputDeltaX = 9
			}
			newValue = int16(*value) + InputDeltaX
			if int16(*value) != newValue {
				*value = byte((newValue + 0x100) % 0x100)
				e.SidebarClearLine(y + 2)
			}
		}
		if InputKeyPressed == KEY_ENTER || InputKeyPressed == KEY_ESCAPE || !editable || InputShiftPressed {
			break
		}
	}
	e.VideoWriteText(x+5, y+1, 0x1F, "\x1f")
}

func (e *Engine) SidebarPromptSlider(editable bool, x, y int16, prompt string, value *byte) {
	var (
		newValue           int16
		startChar, endChar byte
	)
	if prompt[Length(prompt)-3] == ';' {
		startChar = prompt[Length(prompt)-2]
		endChar = prompt[Length(prompt)-1]
		prompt = Copy(prompt, 1, Length(prompt)-3)
	} else {
		startChar = '1'
		endChar = '9'
	}
	e.SidebarClearLine(y)
	e.VideoWriteText(x, y, byte(BoolToInt(editable)+0x1E), prompt)
	e.SidebarClearLine(y + 1)
	e.SidebarClearLine(y + 2)
	e.VideoWriteText(x, y+2, 0x1E, string([]byte{startChar})+"....:...."+string([]byte{endChar}))
	for {
		if editable {
			e.VideoWriteText(x+int16(*value)+1, y+1, 0x9F, "\x1f")
			InputReadWaitKey()
			if InputKeyPressed >= '1' && InputKeyPressed <= '9' {
				*value = Ord(InputKeyPressed) - 49
				e.SidebarClearLine(y + 1)
			} else {
				newValue = int16(*value) + InputDeltaX
				if int16(*value) != newValue && newValue >= 0 && newValue <= 8 {
					*value = byte(newValue)
					e.SidebarClearLine(y + 1)
				}
			}
		}
		if InputKeyPressed == KEY_ENTER || InputKeyPressed == KEY_ESCAPE || !editable || InputShiftPressed {
			break
		}
	}
	e.VideoWriteText(x+int16(*value)+1, y+1, 0x1F, "\x1f")
}

func (e *Engine) SidebarPromptChoice(editable bool, y int16, prompt, choiceStr string, result *byte) {
	var (
		i, j, choiceCount int16
		newResult         int16
	)
	e.SidebarClearLine(y)
	e.SidebarClearLine(y + 1)
	e.SidebarClearLine(y + 2)
	e.VideoWriteText(63, y, byte(BoolToInt(editable)+0x1E), prompt)
	e.VideoWriteText(63, y+2, 0x1E, choiceStr)
	choiceCount = 1
	for i = 1; i <= Length(choiceStr); i++ {
		if choiceStr[i-1] == ' ' {
			choiceCount++
		}
	}
	for {
		j = 0
		i = 1
		for j < int16(*result) && i < Length(choiceStr) {
			if choiceStr[i-1] == ' ' {
				j++
			}
			i++
		}
		if editable {
			e.VideoWriteText(62+i, y+1, 0x9F, "\x1f")
			InputReadWaitKey()
			newResult = int16(*result) + InputDeltaX
			if int16(*result) != newResult && newResult >= 0 && newResult <= choiceCount-1 {
				*result = byte(newResult)
				e.SidebarClearLine(y + 1)
			}
		}
		if InputKeyPressed == KEY_ENTER || InputKeyPressed == KEY_ESCAPE || !editable || InputShiftPressed {
			break
		}
	}
	e.VideoWriteText(62+i, y+1, 0x1F, "\x1f")
}

func (e *Engine) SidebarPromptDirection(editable bool, y int16, prompt string, deltaX, deltaY *int16) {
	var choice byte
	if *deltaY == -1 {
		choice = 0
	} else if *deltaY == 1 {
		choice = 1
	} else if *deltaX == -1 {
		choice = 2
	} else {
		choice = 3
	}

	e.SidebarPromptChoice(editable, y, prompt, "\x18 \x19 \x1b \x1a", &choice)
	*deltaX = NeighborDeltaX[choice]
	*deltaY = NeighborDeltaY[choice]
}

func (e *Engine) PromptString(x, y, arrowColor, color, width int16, mode byte, buffer *string) {
	var (
		i             int16
		oldBuffer     string
		firstKeyPress bool
	)
	oldBuffer = *buffer
	firstKeyPress = true
	for {
		for i = 0; i <= width-1; i++ {
			e.VideoWriteText(x+i, y, byte(color), " ")
			e.VideoWriteText(x+i, y-1, byte(arrowColor), " ")
		}
		e.VideoWriteText(x+width, y-1, byte(arrowColor), " ")
		e.VideoWriteText(x+Length(*buffer), y-1, byte(arrowColor/0x10*16+0x0F), "\x1f")
		e.VideoWriteText(x, y, byte(color), *buffer)
		InputReadWaitKey()
		if Length(*buffer) < width && InputKeyPressed >= ' ' && InputKeyPressed < '\x80' {
			if firstKeyPress {
				*buffer = ""
			}
			switch mode {
			case PROMPT_NUMERIC:
				if InputKeyPressed >= '0' && InputKeyPressed <= '9' {
					*buffer += string([]byte{InputKeyPressed})
				}
			case PROMPT_ANY:
				*buffer += string([]byte{InputKeyPressed})
			case PROMPT_ALPHANUM:
				if UpCase(InputKeyPressed) >= 'A' && UpCase(InputKeyPressed) <= 'Z' || InputKeyPressed >= '0' && InputKeyPressed <= '9' || InputKeyPressed == '-' {
					*buffer += string([]byte{UpCase(InputKeyPressed)})
				}
			}
		} else if InputKeyPressed == KEY_LEFT || InputKeyPressed == KEY_BACKSPACE {
			*buffer = Copy(*buffer, 1, Length(*buffer)-1)
		}

		firstKeyPress = false
		if InputKeyPressed == KEY_ENTER || InputKeyPressed == KEY_ESCAPE {
			break
		}
	}
	if InputKeyPressed == KEY_ESCAPE {
		*buffer = oldBuffer
	}
}

func (e *Engine) SidebarPromptYesNo(message string, defaultReturn bool) (SidebarPromptYesNo bool) {
	e.SidebarClearLine(3)
	e.SidebarClearLine(4)
	e.SidebarClearLine(5)
	e.VideoWriteText(63, 5, 0x1F, message)
	e.VideoWriteText(63+Length(message), 5, 0x9E, "_")
	for {
		InputReadWaitKey()
		if UpCase(InputKeyPressed) == KEY_ESCAPE || UpCase(InputKeyPressed) == 'N' || UpCase(InputKeyPressed) == 'Y' {
			break
		}
	}
	if UpCase(InputKeyPressed) == 'Y' {
		defaultReturn = true
	} else {
		defaultReturn = false
	}
	e.SidebarClearLine(5)
	SidebarPromptYesNo = defaultReturn
	return
}

func (e *Engine) SidebarPromptString(prompt string, extension string, filename *string, promptMode byte) {
	e.SidebarClearLine(3)
	e.SidebarClearLine(4)
	e.SidebarClearLine(5)
	e.VideoWriteText(75-Length(prompt), 3, 0x1F, prompt)
	e.VideoWriteText(63, 5, 0x0F, "        "+extension)
	e.PromptString(63, 5, 0x1E, 0x0F, 8, promptMode, filename)
	e.SidebarClearLine(3)
	e.SidebarClearLine(4)
	e.SidebarClearLine(5)
}

func (e *Engine) PauseOnError() {
	e.SoundQueue(1, SoundParse("s004x114x9"))
	e.Delay(2000)
}

func DisplayIOError(err error) (DisplayIOError bool) {
	var (
		textWindow TTextWindowState
	)
	if err == nil {
		DisplayIOError = false
		return
	}
	DisplayIOError = true
	textWindow.Title = err.Error()[:40]
	textWindow.Title = "Error: " + textWindow.Title
	TextWindowInitState(&textWindow)
	TextWindowAppend(&textWindow, "OS Error:")
	TextWindowAppend(&textWindow, "")
	TextWindowAppend(&textWindow, "This may be caused by missing")
	TextWindowAppend(&textWindow, "ZZT files or a bad disk.  If")
	TextWindowAppend(&textWindow, "you are trying to save a game,")
	TextWindowAppend(&textWindow, "your disk may be full -- try")
	TextWindowAppend(&textWindow, "using a blank, formatted disk")
	TextWindowAppend(&textWindow, "for saving the game!")
	TextWindowDrawOpen(&textWindow)
	TextWindowSelect(&textWindow, false, false)
	TextWindowDrawClose(&textWindow)
	TextWindowFree(&textWindow)
	return
}

func (e *Engine) WorldUnload() {
	e.BoardClose()
}

func (e *Engine) WorldLoad(filename, extension string, titleOnly bool) (WorldLoad bool) {
	var (
		ptr          []byte
		boardId      int16
		loadProgress int16
	)
	SidebarAnimateLoading := func() {
		e.VideoWriteText(69, 5, ProgressAnimColors[loadProgress], ProgressAnimStrings[loadProgress])
		loadProgress = (loadProgress + 1) % 8
	}

	WorldLoad = false
	loadProgress = 0
	e.SidebarClearLine(4)
	e.SidebarClearLine(5)
	e.SidebarClearLine(5)
	e.VideoWriteText(62, 5, 0x1F, "Loading.....")

	f, err := os.Open(filename + extension)
	if DisplayIOError(err) {
		return
	}
	defer f.Close()

	e.WorldUnload()
	_, err = f.Read(e.IoTmpBuf[:512])
	if DisplayIOError(err) {
		return
	}

	ptr = e.IoTmpBuf[:]
	e.World.BoardCount = LoadInt16(ptr[:2])
	ptr = ptr[2:]

	if e.World.BoardCount < 0 {
		if e.World.BoardCount != -1 {
			e.VideoWriteText(63, 5, 0x1E, "You need a newer")
			e.VideoWriteText(63, 6, 0x1E, " version of ZZT!")
			return
		} else {
			e.World.BoardCount = LoadInt16(ptr[:2])
			ptr = ptr[2:]
		}
	}

	LoadWorldInfo(ptr[:SizeOfWorldInfo], &e.World.Info)
	ptr = ptr[SizeOfWorldInfo:]

	pState := e.PlayerFor(0)
	pState.Health = e.World.Info.Health
	pState.Ammo = e.World.Info.Ammo
	pState.Gems = e.World.Info.Gems
	pState.Torches = e.World.Info.Torches
	pState.TorchTicks = e.World.Info.TorchTicks
	pState.EnergizerTicks = e.World.Info.EnergizerTicks
	pState.Score = e.World.Info.Score
	pState.Keys = e.World.Info.Keys
	pState.BoardTimeSec = e.World.Info.BoardTimeSec
	pState.BoardTimeHsec = e.World.Info.BoardTimeHsec

	if titleOnly {
		e.World.BoardCount = 0
		e.World.Info.CurrentBoard = 0
		e.World.Info.IsSave = true
	}

	for boardId = 0; boardId <= e.World.BoardCount; boardId++ {
		SidebarAnimateLoading()

		lenBuf := make([]byte, 2)
		_, err = f.Read(lenBuf)
		if DisplayIOError(err) {
			return
		}
		e.World.BoardLen[boardId] = LoadInt16(lenBuf)

		e.World.BoardData[boardId] = make([]byte, e.World.BoardLen[boardId])
		_, err = f.Read(e.World.BoardData[boardId])
		if DisplayIOError(err) {
			return
		}
	}

	e.BoardOpen(e.World.Info.CurrentBoard)
	e.LoadedGameFileName = filename
	WorldLoad = true
	e.HighScoresLoad()
	e.SidebarClearLine(5)

	return
}

func (e *Engine) WorldSave(filename, extension string) {
	var (
		i   int16
		ptr []byte
	)

	e.BoardClose()
	defer func() {
		e.BoardOpen(e.World.Info.CurrentBoard)
		e.SidebarClearLine(5)
	}()

	e.VideoWriteText(63, 5, 0x1F, "Saving...")

	f, err := os.Create(filename + extension)
	if DisplayIOError(err) {
		return
	}
	defer f.Close()

	ptr = e.IoTmpBuf[:]
	for i := 0; i < 512; i++ {
		ptr[0] = 0
	}
	StoreInt16(ptr[:2], -1)
	ptr = ptr[2:]
	StoreInt16(ptr[:2], e.World.BoardCount)
	ptr = ptr[2:]
	pState := e.PlayerFor(0)
	e.World.Info.Health = pState.Health
	e.World.Info.Ammo = pState.Ammo
	e.World.Info.Gems = pState.Gems
	e.World.Info.Torches = pState.Torches
	e.World.Info.TorchTicks = pState.TorchTicks
	e.World.Info.EnergizerTicks = pState.EnergizerTicks
	e.World.Info.Score = pState.Score
	e.World.Info.Keys = pState.Keys
	e.World.Info.BoardTimeSec = pState.BoardTimeSec
	e.World.Info.BoardTimeHsec = pState.BoardTimeHsec

	StoreWorldInfo(ptr[:SizeOfWorldInfo], &e.World.Info)
	ptr = ptr[SizeOfWorldInfo:]
	_, err = f.Write(e.IoTmpBuf[:512])
	if DisplayIOError(err) {
		return
	}

	for i = 0; i <= e.World.BoardCount; i++ {
		lenBuf := make([]byte, 2)
		StoreInt16(lenBuf, e.World.BoardLen[i])
		_, err = f.Write(lenBuf)
		if DisplayIOError(err) {
			return
		}

		_, err = f.Write(e.World.BoardData[i])
		if DisplayIOError(err) {
			return
		}
	}
}

func (e *Engine) GameWorldSave(prompt string, filename *string, extension string) {
	var newFilename string
	newFilename = *filename
	e.SidebarPromptString(prompt, extension, &newFilename, PROMPT_ALPHANUM)
	if InputKeyPressed != KEY_ESCAPE && Length(newFilename) != 0 {
		*filename = newFilename
		if extension == ".ZZT" {
			e.World.Info.Name = *filename
		}
		e.WorldSave(*filename, extension)
	}
}

func (e *Engine) GameWorldLoad(extension string) (GameWorldLoad bool) {
	var (
		textWindow TTextWindowState
		entryName  string
		i          int16
	)

	TextWindowInitState(&textWindow)
	if extension == ".ZZT" {
		textWindow.Title = "ZZT Worlds"
	} else {
		textWindow.Title = "Saved Games"
	}
	GameWorldLoad = false
	textWindow.Selectable = true

	matches, err := filepath.Glob("*" + extension)
	if err == nil {
		for _, match := range matches {
			entryName = match[:len(match)-4]
			for i = 1; i <= e.WorldFileDescCount; i++ {
				if entryName == e.WorldFileDescKeys[i-1] {
					entryName = e.WorldFileDescValues[i-1]
				}
			}
			TextWindowAppend(&textWindow, entryName)
		}
	}

	TextWindowAppend(&textWindow, "Exit")
	TextWindowDrawOpen(&textWindow)
	TextWindowSelect(&textWindow, false, false)
	TextWindowDrawClose(&textWindow)

	if textWindow.LinePos < textWindow.LineCount && !TextWindowRejected {
		entryName = textWindow.Lines[textWindow.LinePos-1]
		if Pos(' ', entryName) != 0 {
			entryName = Copy(entryName, 1, Pos(' ', entryName)-1)
		}
		GameWorldLoad = e.WorldLoad(entryName, extension, false)
		e.TransitionDrawToFill('\xdb', 0x44)
	}

	return
}

func (e *Engine) HighScoresAdd(score int16) {
	var listPos int16
	listPos = 1
	for listPos <= 30 && score < e.HighScoreList[listPos-1].Score {
		listPos++
	}
	if listPos <= 30 && score > 0 {
		e.Events = append(e.Events, HighScoreEntryEvent{
			Score:   score,
			ListPos: listPos,
		})
	}
}

func (e *Engine) HighScoresLoad() {
	f, err := os.Open(e.World.Info.Name + ".HI")
	if err == nil {
		buf := make([]byte, SizeOfHighScoreList)
		_, err = f.Read(buf)
		if err == nil {
			LoadHighScoreList(buf, e.HighScoreList[:])
		}
		f.Close()
	}
	if err != nil {
		for i := 0; i < HIGH_SCORE_COUNT; i++ {
			e.HighScoreList[i].Name = ""
			e.HighScoreList[i].Score = -1
		}
	}
}

func (e *Engine) HighScoresSave() {
	f, err := os.Create(e.World.Info.Name + ".HI")
	if err != nil {
		DisplayIOError(err)
		return
	}
	buf := make([]byte, SizeOfHighScoreList)
	StoreHighScoreList(buf, e.HighScoreList[:])
	_, err = f.Write(buf)
	if err != nil {
		DisplayIOError(err)
		return
	}
	f.Close()
}

func (e *Engine) HighScoresInitTextWindow(state *TTextWindowState) {
	TextWindowInitState(state)
	TextWindowAppend(state, "Score  Name")
	TextWindowAppend(state, "-----  ----------------------------------")
	for i := 0; i < HIGH_SCORE_COUNT; i++ {
		if Length(e.HighScoreList[i].Name) != 0 {
			scoreStr := StrWidth(e.HighScoreList[i].Score, 5)
			TextWindowAppend(state, scoreStr+"  "+e.HighScoreList[i].Name)
		}
	}
}

func (e *Engine) HighScoresDisplay(linePos int16) {
	var state TTextWindowState
	state.LinePos = linePos
	e.HighScoresInitTextWindow(&state)
	if state.LineCount > 2 {
		state.Title = "High scores for " + e.World.Info.Name
		TextWindowDrawOpen(&state)
		TextWindowSelect(&state, false, true)
		TextWindowDrawClose(&state)
	}
	TextWindowFree(&state)
}

func (e *Engine) CopyStatDataToTextWindow(statId int16, state *TTextWindowState) {
	stat := &e.Board.Stats[statId]
	TextWindowInitState(state)

	var dataBuf []byte
	for i := 0; i < int(stat.DataLen); i++ {
		dataChr := stat.Data[i]
		if dataChr == KEY_ENTER {
			TextWindowAppend(state, string(dataBuf))
			dataBuf = dataBuf[:0]
		} else {
			dataBuf = append(dataBuf, dataChr)
		}
	}
}

func (e *Engine) AddStat(tx, ty int16, element byte, color, tcycle int16, template TStat) {
	if e.Board.StatCount < MAX_STAT {
		e.Board.StatCount++
		e.Board.Stats[e.Board.StatCount] = template
		stat := &e.Board.Stats[e.Board.StatCount]
		stat.X = byte(tx)
		stat.Y = byte(ty)
		stat.Cycle = tcycle
		stat.Under = e.Board.Tiles[tx][ty]
		stat.DataPos = 0
		if template.Data != "" {
			e.Board.Stats[e.Board.StatCount].Data = template.Data
		}
		if ElementDefs[e.Board.Tiles[tx][ty].Element].PlaceableOnTop {
			e.Board.Tiles[tx][ty].Color = byte(color&0x0F + int16(e.Board.Tiles[tx][ty].Color)&0x70)
		} else {
			e.Board.Tiles[tx][ty].Color = byte(color)
		}
		e.Board.Tiles[tx][ty].Element = element
		if ty > 0 {
			e.BoardDrawTile(tx, ty)
		}
	}
}

func (e *Engine) RemoveStat(statId int16) {
	var i int16
	stat := &e.Board.Stats[statId]
	if stat.DataLen != 0 {
		for i = 1; i <= e.Board.StatCount; i++ {
			if e.Board.Stats[i].Data == stat.Data && i != statId {
				goto StatDataInUse
			}
		}
		stat.Data = ""
	}
StatDataInUse:
	if statId < e.CurrentStatTicked {
		e.CurrentStatTicked--
	}

	e.Board.Tiles[stat.X][stat.Y] = stat.Under
	if stat.Y > 0 {
		e.BoardDrawTile(int16(stat.X), int16(stat.Y))
	}
	for i = 1; i <= e.Board.StatCount; i++ {
		if e.Board.Stats[i].Follower >= statId {
			if e.Board.Stats[i].Follower == statId {
				e.Board.Stats[i].Follower = -1
			} else {
				e.Board.Stats[i].Follower--
			}
		}
		if e.Board.Stats[i].Leader >= statId {
			if e.Board.Stats[i].Leader == statId {
				e.Board.Stats[i].Leader = -1
			} else {
				e.Board.Stats[i].Leader--
			}
		}
	}
	for i = statId + 1; i <= e.Board.StatCount; i++ {
		e.Board.Stats[i-1] = e.Board.Stats[i]
	}
	e.Board.StatCount--
}

func (e *Engine) GetStatIdAt(x, y int16) (GetStatIdAt int16) {
	var i int16
	i = -1
	for {
		i++
		if int16(e.Board.Stats[i].X) == x && int16(e.Board.Stats[i].Y) == y || i > e.Board.StatCount {
			break
		}
	}
	if i > e.Board.StatCount {
		GetStatIdAt = -1
	} else {
		GetStatIdAt = i
	}
	return
}

func (e *Engine) BoardPrepareTileForPlacement(x, y int16) (BoardPrepareTileForPlacement bool) {
	var (
		statId int16
		result bool
	)
	statId = e.GetStatIdAt(x, y)
	if statId > 0 {
		e.RemoveStat(statId)
		result = true
	} else if statId < 0 {
		if !ElementDefs[e.Board.Tiles[x][y].Element].PlaceableOnTop {
			e.Board.Tiles[x][y].Element = E_EMPTY
		}
		result = true
	} else {
		result = false
	}

	e.BoardDrawTile(x, y)
	BoardPrepareTileForPlacement = result
	return
}

func (e *Engine) MoveStat(statId int16, newX, newY int16) {
	var (
		iUnder     TTile
		ix, iy     int16
		oldX, oldY int16
	)
	stat := &e.Board.Stats[statId]
	iUnder = e.Board.Stats[statId].Under
	e.Board.Stats[statId].Under = e.Board.Tiles[newX][newY]
	if e.Board.Tiles[stat.X][stat.Y].Element == E_PLAYER {
		e.Board.Tiles[newX][newY].Color = e.Board.Tiles[stat.X][stat.Y].Color
	} else if e.Board.Tiles[newX][newY].Element == E_EMPTY {
		e.Board.Tiles[newX][newY].Color = byte(int16(e.Board.Tiles[stat.X][stat.Y].Color) & 0x0F)
	} else {
		e.Board.Tiles[newX][newY].Color = byte(int16(e.Board.Tiles[stat.X][stat.Y].Color)&0x0F + int16(e.Board.Tiles[newX][newY].Color)&0x70)
	}

	e.Board.Tiles[newX][newY].Element = e.Board.Tiles[stat.X][stat.Y].Element
	e.Board.Tiles[stat.X][stat.Y] = iUnder
	oldX = int16(stat.X)
	oldY = int16(stat.Y)
	stat.X = byte(newX)
	stat.Y = byte(newY)
	e.BoardDrawTile(int16(stat.X), int16(stat.Y))
	e.BoardDrawTile(oldX, oldY)
	if statId == 0 && e.Board.Info.IsDark && e.PlayerFor(statId).TorchTicks > 0 {
		if Sqr(oldX-int16(stat.X))+Sqr(oldY-int16(stat.Y)) == 1 {
			for ix = int16(stat.X) - TORCH_DX - 3; ix <= int16(stat.X)+TORCH_DX+3; ix++ {
				if ix >= 1 && ix <= BOARD_WIDTH {
					for iy = int16(stat.Y) - TORCH_DY - 3; iy <= int16(stat.Y)+TORCH_DY+3; iy++ {
						if iy >= 1 && iy <= BOARD_HEIGHT {
							if Sqr(ix-oldX)+Sqr(iy-oldY)*2 < TORCH_DIST_SQR != (Sqr(ix-newX)+Sqr(iy-newY)*2 < TORCH_DIST_SQR) {
								e.BoardDrawTile(ix, iy)
							}
						}
					}
				}
			}
		} else {
			e.DrawPlayerSurroundings(oldX, oldY, 0)
			e.DrawPlayerSurroundings(int16(stat.X), int16(stat.Y), 0)
		}
	}
}

func (e *Engine) PopupPromptString(question string, buffer *string) {
	var x, y int16
	e.VideoWriteText(3, 18, 0x4F, TextWindowStrTop)
	e.VideoWriteText(3, 19, 0x4F, TextWindowStrText)
	e.VideoWriteText(3, 20, 0x4F, TextWindowStrSep)
	e.VideoWriteText(3, 21, 0x4F, TextWindowStrText)
	e.VideoWriteText(3, 22, 0x4F, TextWindowStrText)
	e.VideoWriteText(3, 23, 0x4F, TextWindowStrBottom)
	e.VideoWriteText(4+(TextWindowWidth-Length(question))/2, 19, 0x4F, question)
	*buffer = ""
	e.PromptString(10, 22, 0x4F, 0x4E, TextWindowWidth-16, PROMPT_ANY, buffer)
	for y = 18; y <= 23; y++ {
		for x = 3; x <= TextWindowWidth+3; x++ {
			e.BoardDrawTile(x+1, y+1)
		}
	}
}

func Signum(val int16) (Signum int16) {
	if val > 0 {
		Signum = 1
	} else if val < 0 {
		Signum = -1
	} else {
		Signum = 0
	}
	return
}

func Difference(a, b int16) (Difference int16) {
	if a-b >= 0 {
		Difference = a - b
	} else {
		Difference = b - a
	}
	return
}

func (e *Engine) GameUpdateSidebar() {
	var (
		numStr string
		i      int16
	)
	if e.GameStateElement == E_PLAYER {
		pState := e.PlayerFor(0)
		if e.Board.Info.TimeLimitSec > 0 {
			e.VideoWriteText(64, 6, 0x1E, "   Time:")
			numStr = Str(e.Board.Info.TimeLimitSec - pState.BoardTimeSec)
			e.VideoWriteText(72, 6, 0x1E, numStr+" ")
		} else {
			e.SidebarClearLine(6)
		}
		if pState.Health < 0 {
			pState.Health = 0
		}
		numStr = Str(pState.Health)
		e.VideoWriteText(72, 7, 0x1E, numStr+" ")
		numStr = Str(pState.Ammo)
		e.VideoWriteText(72, 8, 0x1E, numStr+"  ")
		numStr = Str(pState.Torches)
		e.VideoWriteText(72, 9, 0x1E, numStr+" ")
		numStr = Str(pState.Gems)
		e.VideoWriteText(72, 10, 0x1E, numStr+" ")
		numStr = Str(pState.Score)
		e.VideoWriteText(72, 11, 0x1E, numStr+" ")
		if pState.TorchTicks == 0 {
			e.VideoWriteText(75, 9, 0x16, "    ")
		} else {
			for i = 2; i <= 5; i++ {
				if i <= pState.TorchTicks*5/TORCH_DURATION {
					e.VideoWriteText(73+i, 9, 0x16, "\xb1")
				} else {
					e.VideoWriteText(73+i, 9, 0x16, "\xb0")
				}
			}
		}
		for i = 1; i <= 7; i++ {
			if pState.Keys[i-1] {
				e.VideoWriteText(71+i, 12, byte(0x18+i), string([]byte{ElementDefs[E_KEY].Character}))
			} else {
				e.VideoWriteText(71+i, 12, 0x1F, " ")
			}
		}
		if SoundEnabled {
			e.VideoWriteText(65, 15, 0x1F, " Be quiet")
		} else {
			e.VideoWriteText(65, 15, 0x1F, " Be noisy")
		}
	}
}

func (e *Engine) DisplayMessage(time int16, message string) {
	if e.GetStatIdAt(0, 0) != -1 {
		e.RemoveStat(e.GetStatIdAt(0, 0))
		e.BoardDrawBorder()
	}
	if Length(message) != 0 {
		e.AddStat(0, 0, E_MESSAGE_TIMER, 0, 1, StatTemplateDefault)
		e.Board.Stats[e.Board.StatCount].P2 = byte(time / (e.TickTimeDuration + 1))
		e.Board.Info.Message = message
	}
}

func (e *Engine) DamageStat(attackerStatId int16) {
	var oldX, oldY int16
	stat := &e.Board.Stats[attackerStatId]
	if e.Board.Tiles[stat.X][stat.Y].Element == E_PLAYER {
		pState := e.PlayerFor(attackerStatId)
		if pState.Health > 0 {
			pState.Health -= 10
			e.GameUpdateSidebar()
			e.DisplayMessage(100, "Ouch!")
			e.Board.Tiles[stat.X][stat.Y].Color = byte(0x70 + int16(ElementDefs[E_PLAYER].Color)%0x10)
			if pState.Health > 0 {
				pState.BoardTimeSec = 0
				if e.Board.Info.ReenterWhenZapped {
					e.SoundQueue(4, " \x01#\x01'\x010\x01\x10\x01")
					e.Board.Tiles[stat.X][stat.Y].Element = E_EMPTY
					e.BoardDrawTile(int16(stat.X), int16(stat.Y))
					oldX = int16(stat.X)
					oldY = int16(stat.Y)
					stat.X = e.Board.Info.StartPlayerX
					stat.Y = e.Board.Info.StartPlayerY
					e.DrawPlayerSurroundings(oldX, oldY, 0)
					e.DrawPlayerSurroundings(int16(stat.X), int16(stat.Y), 0)
					e.GamePaused = true
				}
				e.SoundQueue(4, "\x10\x01 \x01\x13\x01#\x01")
			} else {
				// Health reached 0: start respawn countdown instead of game-over.
				e.SoundQueue(5, " \x03#\x03'\x030\x03'\x03*\x032\x037\x035\x038\x03@\x03E\x03\x10\n")
				if pState.Score >= RESPAWN_SCORE_PENALTY {
					pState.Score -= RESPAWN_SCORE_PENALTY
				} else {
					pState.Score = 0
				}
				pState.RespawnTicks = RESPAWN_TICKS
				e.Events = append(e.Events, DeathEvent{StatId: attackerStatId})
			}
		}
	} else {
		switch e.Board.Tiles[stat.X][stat.Y].Element {
		case E_BULLET:
			e.SoundQueue(3, " \x01")
		case E_OBJECT:
		default:
			e.SoundQueue(3, "@\x01\x10\x01P\x010\x01")
		}
		e.RemoveStat(attackerStatId)
	}
}

func (e *Engine) BoardDamageTile(x, y int16) {
	var statId int16
	statId = e.GetStatIdAt(x, y)
	if statId != -1 {
		e.DamageStat(statId)
	} else {
		e.Board.Tiles[x][y].Element = E_EMPTY
		e.BoardDrawTile(x, y)
	}
}

func (e *Engine) BoardAttack(attackerStatId int16, x, y int16) {
	attackerStat := &e.Board.Stats[attackerStatId]
	isPlayerAttacker := e.Board.Tiles[attackerStat.X][attackerStat.Y].Element == E_PLAYER
	pState := e.PlayerFor(attackerStatId)
	if isPlayerAttacker && pState.EnergizerTicks > 0 {
		pState.Score = ElementDefs[e.Board.Tiles[x][y].Element].ScoreValue + pState.Score
		e.GameUpdateSidebar()
	} else {
		e.DamageStat(attackerStatId)
	}
	if attackerStatId > 0 && attackerStatId <= e.CurrentStatTicked {
		e.CurrentStatTicked--
	}
	defenderStatId := e.GetStatIdAt(x, y)
	if e.Board.Tiles[x][y].Element == E_PLAYER && defenderStatId != -1 && e.PlayerFor(defenderStatId).EnergizerTicks > 0 {
		e.PlayerFor(defenderStatId).Score = ElementDefs[e.Board.Tiles[e.Board.Stats[attackerStatId].X][e.Board.Stats[attackerStatId].Y].Element].ScoreValue + e.PlayerFor(defenderStatId).Score
		e.GameUpdateSidebar()
	} else {
		e.BoardDamageTile(x, y)
		e.SoundQueue(2, "\x10\x01")
	}
}

func (e *Engine) BoardShoot(element byte, tx, ty, deltaX, deltaY int16, source int16) (BoardShoot bool) {
	if ElementDefs[e.Board.Tiles[tx+deltaX][ty+deltaY].Element].Walkable || e.Board.Tiles[tx+deltaX][ty+deltaY].Element == E_WATER {
		e.AddStat(tx+deltaX, ty+deltaY, element, int16(ElementDefs[element].Color), 1, StatTemplateDefault)
		stat := &e.Board.Stats[e.Board.StatCount]
		stat.P1 = byte(source)
		stat.StepX = deltaX
		stat.StepY = deltaY
		stat.P2 = 100
		BoardShoot = true
	} else if e.Board.Tiles[tx+deltaX][ty+deltaY].Element == E_BREAKABLE || ElementDefs[e.Board.Tiles[tx+deltaX][ty+deltaY].Element].Destructible && e.Board.Tiles[tx+deltaX][ty+deltaY].Element == E_PLAYER == (source >= SHOT_SOURCE_PLAYER_BASE) && e.PlayerFor(0).EnergizerTicks <= 0 {
		e.BoardDamageTile(tx+deltaX, ty+deltaY)
		e.SoundQueue(2, "\x10\x01")
		BoardShoot = true
	} else {
		BoardShoot = false
	}

	return
}

func (e *Engine) CalcDirectionRnd(deltaX, deltaY *int16) {
	*deltaX = e.Random(3) - 1
	if *deltaX == 0 {
		*deltaY = e.Random(2)*2 - 1
	} else {
		*deltaY = 0
	}
}

func (e *Engine) CalcDirectionSeek(x, y int16, deltaX, deltaY *int16) {
	*deltaX = 0
	*deltaY = 0
	pId := e.NearestPlayer(x, y)
	target := &e.Board.Stats[pId]
	if e.Random(2) < 1 || int16(target.Y) == y {
		*deltaX = Signum(int16(target.X) - x)
	}
	if *deltaX == 0 {
		*deltaY = Signum(int16(target.Y) - y)
	}
	if e.PlayerFor(pId).EnergizerTicks > 0 {
		*deltaX = -*deltaX
		*deltaY = -*deltaY
	}
}

func (e *Engine) TransitionDrawBoardChange() {
	e.TransitionDrawToFill('\xdb', 0x05)
	e.TransitionDrawToBoard()
}

func (e *Engine) BoardEnter() {
	e.Board.Info.StartPlayerX = e.Board.Stats[0].X
	e.Board.Info.StartPlayerY = e.Board.Stats[0].Y
	pState := e.PlayerFor(0)
	if e.Board.Info.IsDark && pState.MessageHintTorchNotShown {
		e.DisplayMessage(200, "Room is dark - you need to light a torch!")
		pState.MessageHintTorchNotShown = false
	}
	pState.BoardTimeSec = 0
	e.GameUpdateSidebar()
}

func (e *Engine) BoardPassageTeleport(x, y int16) {
	var (
		col        byte
		ix, iy     int16
		newX, newY int16
	)
	col = e.Board.Tiles[x][y].Color
	e.BoardChange(int16(e.Board.Stats[e.GetStatIdAt(x, y)].P3))
	newX = 0
	for ix = 1; ix <= BOARD_WIDTH; ix++ {
		for iy = 1; iy <= BOARD_HEIGHT; iy++ {
			if e.Board.Tiles[ix][iy].Element == E_PASSAGE && e.Board.Tiles[ix][iy].Color == col {
				newX = ix
				newY = iy
			}
		}
	}
	e.Board.Tiles[e.Board.Stats[0].X][e.Board.Stats[0].Y].Element = E_EMPTY
	e.Board.Tiles[e.Board.Stats[0].X][e.Board.Stats[0].Y].Color = 0
	if newX != 0 {
		e.Board.Stats[0].X = byte(newX)
		e.Board.Stats[0].Y = byte(newY)
	}
	e.GamePaused = true
	e.SoundQueue(4, "0\x014\x017\x011\x015\x018\x012\x016\x019\x013\x017\x01:\x014\x018\x01@\x01")
	e.TransitionDrawBoardChange()
	e.BoardEnter()
}

func (e *Engine) GameDebugPrompt() {
	var (
		input  string
		i      int16
		toggle bool
	)
	input = ""
	e.SidebarClearLine(4)
	e.SidebarClearLine(5)
	e.PromptString(63, 5, 0x1E, 0x0F, 11, PROMPT_ANY, &input)
	input = UpCaseString(input)
	toggle = true
	if len(input) > 0 && (input[0] == '+' || input[0] == '-') {
		if input[0] == '-' {
			toggle = false
		}
		input = Copy(input, 2, Length(input)-1)
		if toggle == true {
			e.WorldSetFlag(input)
		} else {
			e.WorldClearFlag(input)
		}
	}
	e.DebugEnabled = e.WorldGetFlagPosition("DEBUG") >= 0
	pState := e.PlayerFor(0)
	if input == "HEALTH" {
		pState.Health += 50
	} else if input == "AMMO" {
		pState.Ammo += 5
	} else if input == "KEYS" {
		for i = 1; i <= 7; i++ {
			pState.Keys[i-1] = true
		}
	} else if input == "TORCHES" {
		pState.Torches += 3
	} else if input == "TIME" {
		pState.BoardTimeSec -= 30
	} else if input == "GEMS" {
		pState.Gems += 5
	} else if input == "DARK" {
		e.Board.Info.IsDark = toggle
		e.TransitionDrawToBoard()
	} else if input == "ZAP" {
		for i = 0; i <= 3; i++ {
			e.BoardDamageTile(int16(e.Board.Stats[0].X)+NeighborDeltaX[i], int16(e.Board.Stats[0].Y)+NeighborDeltaY[i])
			e.Board.Tiles[int16(e.Board.Stats[0].X)+NeighborDeltaX[i]][int16(e.Board.Stats[0].Y)+NeighborDeltaY[i]].Element = E_EMPTY
			e.BoardDrawTile(int16(e.Board.Stats[0].X)+NeighborDeltaX[i], int16(e.Board.Stats[0].Y)+NeighborDeltaY[i])
		}
	}

	e.SoundQueue(10, "'\x04")
	e.SidebarClearLine(4)
	e.SidebarClearLine(5)
	e.GameUpdateSidebar()
}

func GameAboutScreen() {
	TextWindowDisplayFile("ABOUT.HLP", "About ZZT...")
}

// GameStepWithInputs runs one game cycle with per-player input injection.
// Before ticking each E_PLAYER stat, the matching PlayerInput from inputs is
// loaded into the global input variables; for non-player stats those globals
// are zeroed so no player's movement leaks into another element's tick
// (ANALYSIS.md §3d — the scratch-var trap: InputDeltaX/Y are also used as
// dummy pointer params in TouchProc calls, so we must only zero them between
// player ticks, never mid-tick).
//
// Faithfulness notes (this must not change behavior — ANALYSIS.md §3g):
//   - It iterates the GLOBAL e.CurrentStatTicked, not a fresh loop variable,
//     because RemoveStat/DamageStat decrement e.CurrentStatTicked when they
//     remove a stat mid-tick (GAME.PAS 942-943, 1233-1234); a local index
//     would desync the stat iteration order.
//   - e.Board.StatCount is re-read each iteration so stats added/removed during
//     the cycle are handled exactly as the original per-iteration loop did.
//   - With the caller's setup leaving e.CurrentStatTicked = StatCount+1, the
//     first GameStep ticks nothing and only advances (matching the original's
//     first loop iteration); every later call ticks a full cycle then advances.
//   - e.GamePlayExitRequested stops ticking mid-cycle and suppresses the advance,
//     mirroring the original loop's exit checks.
func (e *Engine) GameStepWithInputs(inputs map[int16]PlayerInput) {
	if e.PendingScrollReply != "" {
		e.OopSend(e.PendingScrollStatId, e.PendingScrollReply, false)
		e.PendingScrollReply = ""
	}

	// Snapshot engine-level input fields into globals for the tick loop.
	// Individual player inputs from the inputs map will override these for
	// E_PLAYER stats mid-loop; the defer restores the final globals back.
	InputDeltaX = e.InputDeltaX
	InputDeltaY = e.InputDeltaY
	InputShiftPressed = e.InputShiftPressed
	InputKeyPressed = e.InputKeyPressed
	InputLastDeltaX = e.InputLastDeltaX
	InputLastDeltaY = e.InputLastDeltaY
	InputKeyBuffer = e.InputKeyBuffer

	defer func() {
		e.InputDeltaX = InputDeltaX
		e.InputDeltaY = InputDeltaY
		e.InputShiftPressed = InputShiftPressed
		e.InputKeyPressed = InputKeyPressed
		e.InputLastDeltaX = InputLastDeltaX
		e.InputLastDeltaY = InputLastDeltaY
		e.InputKeyBuffer = InputKeyBuffer
	}()

	for e.CurrentStatTicked <= e.Board.StatCount && !e.GamePlayExitRequested {
		stat := &e.Board.Stats[e.CurrentStatTicked]
		if stat.Cycle != 0 && e.CurrentTick%stat.Cycle == e.CurrentStatTicked%stat.Cycle {
			// Per-player input injection: load this player's input before tick,
			// zero movement globals for non-player stats.
			if e.Board.Tiles[stat.X][stat.Y].Element == E_PLAYER {
				if inp, ok := inputs[e.CurrentStatTicked]; ok {
					InputDeltaX = inp.DeltaX
					InputDeltaY = inp.DeltaY
					InputShiftPressed = inp.Shift
					InputKeyPressed = inp.Key
				} else {
					InputDeltaX = 0
					InputDeltaY = 0
					InputShiftPressed = false
					InputKeyPressed = 0
				}
				if InputDeltaX != 0 || InputDeltaY != 0 {
					InputLastDeltaX = InputDeltaX
					InputLastDeltaY = InputDeltaY
				}
			} else {
				InputDeltaX = 0
				InputDeltaY = 0
				InputShiftPressed = false
				// InputKeyPressed is intentionally NOT zeroed here: non-player
				// elements like E_MONITOR read it in their own tick procs.
			}
			ElementDefs[e.Board.Tiles[stat.X][stat.Y].Element].TickProc(e, e.CurrentStatTicked)
		}
		e.CurrentStatTicked++
	}

	if e.CurrentStatTicked > e.Board.StatCount && !e.GamePlayExitRequested {
		// next cycle
		e.CurrentTick++
		if e.CurrentTick > 420 {
			e.CurrentTick = 1
		}
		e.CurrentStatTicked = 0
		e.InputUpdate()
	}
}

// GameStep runs one game cycle with per-player input.
//
// inputs maps statId → PlayerInput for each player stat that should receive
// input this tick. Pass nil to fall back to the engine's single active
// InputSource (backward-compatible single-player path: reads e.InputDeltaX/Y
// etc., which InputUpdate populated from ActiveInput at the end of the
// previous step). Multiplayer callers should construct the map explicitly so
// each player's input is independent.
func (e *Engine) GameStep(inputs map[int16]PlayerInput) {
	if inputs == nil {
		inputs = map[int16]PlayerInput{
			0: {
				DeltaX: e.InputDeltaX,
				DeltaY: e.InputDeltaY,
				Shift:  e.InputShiftPressed,
				Key:    e.InputKeyPressed,
			},
		}
	}
	e.GameStepWithInputs(inputs)
}

func (e *Engine) GamePlayLoop(boardChanged bool) {
	var pauseBlink bool

	GameDrawSidebar := func() {
		e.SidebarClear()
		e.SidebarClearLine(0)
		e.SidebarClearLine(1)
		e.SidebarClearLine(2)
		e.VideoWriteText(61, 0, 0x1F, "    - - - - -      ")
		e.VideoWriteText(62, 1, 0x70, "      ZZT      ")
		e.VideoWriteText(61, 2, 0x1F, "    - - - - -      ")
		if e.GameStateElement == E_PLAYER {
			e.VideoWriteText(64, 7, 0x1E, " Health:")
			e.VideoWriteText(64, 8, 0x1E, "   Ammo:")
			e.VideoWriteText(64, 9, 0x1E, "Torches:")
			e.VideoWriteText(64, 10, 0x1E, "   Gems:")
			e.VideoWriteText(64, 11, 0x1E, "  Score:")
			e.VideoWriteText(64, 12, 0x1E, "   Keys:")
			e.VideoWriteText(62, 7, 0x1F, string([]byte{ElementDefs[E_PLAYER].Character}))
			e.VideoWriteText(62, 8, 0x1B, string([]byte{ElementDefs[E_AMMO].Character}))
			e.VideoWriteText(62, 9, 0x16, string([]byte{ElementDefs[E_TORCH].Character}))
			e.VideoWriteText(62, 10, 0x1B, string([]byte{ElementDefs[E_GEM].Character}))
			e.VideoWriteText(62, 12, 0x1F, string([]byte{ElementDefs[E_KEY].Character}))
			e.VideoWriteText(62, 14, 0x70, " T ")
			e.VideoWriteText(65, 14, 0x1F, " Torch")
			e.VideoWriteText(62, 15, 0x30, " B ")
			e.VideoWriteText(62, 16, 0x70, " H ")
			e.VideoWriteText(65, 16, 0x1F, " Help")
			e.VideoWriteText(67, 18, 0x30, " \x18\x19\x1a\x1b ")
			e.VideoWriteText(72, 18, 0x1F, " Move")
			e.VideoWriteText(61, 19, 0x70, " Shift \x18\x19\x1a\x1b ")
			e.VideoWriteText(72, 19, 0x1F, " Shoot")
			e.VideoWriteText(62, 21, 0x70, " S ")
			e.VideoWriteText(65, 21, 0x1F, " Save game")
			e.VideoWriteText(62, 22, 0x30, " P ")
			e.VideoWriteText(65, 22, 0x1F, " Pause")
			e.VideoWriteText(62, 23, 0x70, " Q ")
			e.VideoWriteText(65, 23, 0x1F, " Quit")
		} else if e.GameStateElement == E_MONITOR {
			e.SidebarPromptSlider(false, 66, 21, "Game speed:;FS", &e.TickSpeed)
			e.VideoWriteText(62, 21, 0x70, " S ")
			e.VideoWriteText(62, 7, 0x30, " W ")
			e.VideoWriteText(65, 7, 0x1E, " World:")
			if Length(e.World.Info.Name) != 0 {
				e.VideoWriteText(69, 8, 0x1F, e.World.Info.Name)
			} else {
				e.VideoWriteText(69, 8, 0x1F, "Untitled")
			}
			e.VideoWriteText(62, 11, 0x70, " P ")
			e.VideoWriteText(65, 11, 0x1F, " Play")
			e.VideoWriteText(62, 12, 0x30, " R ")
			e.VideoWriteText(65, 12, 0x1E, " Restore game")
			e.VideoWriteText(62, 13, 0x70, " Q ")
			e.VideoWriteText(65, 13, 0x1E, " Quit")
			e.VideoWriteText(62, 16, 0x30, " A ")
			e.VideoWriteText(65, 16, 0x1F, " About ZZT!")
			e.VideoWriteText(62, 17, 0x70, " H ")
			e.VideoWriteText(65, 17, 0x1E, " High Scores")
			if e.EditorEnabled {
				e.VideoWriteText(62, 18, 0x30, " E ")
				e.VideoWriteText(65, 18, 0x1E, " Board Editor")
			}
		}
	}

	GameDrawSidebar()
	e.GameUpdateSidebar()

	if e.JustStarted {
		// TODO: GameAboutScreen()
		if Length(e.StartupWorldFileName) != 0 {
			e.SidebarClearLine(8)
			e.VideoWriteText(69, 8, 0x1F, e.StartupWorldFileName)
			if !e.WorldLoad(e.StartupWorldFileName, ".ZZT", true) {
				e.WorldCreate()
			}
		}
		e.ReturnBoardId = e.World.Info.CurrentBoard
		e.BoardChange(0)
		e.JustStarted = false
	}

	e.Board.Tiles[e.Board.Stats[0].X][e.Board.Stats[0].Y].Element = byte(e.GameStateElement)
	e.Board.Tiles[e.Board.Stats[0].X][e.Board.Stats[0].Y].Color = ElementDefs[e.GameStateElement].Color
	if e.GameStateElement == E_MONITOR {
		e.DisplayMessage(0, "")
		e.VideoWriteText(62, 5, 0x1B, "Pick a command:")
	}
	if boardChanged {
		e.TransitionDrawBoardChange()
	}
	e.TickTimeDuration = int16(e.TickSpeed) * 2
	e.GamePlayExitRequested = false
	e.CurrentTick = e.Random(100)
	e.CurrentStatTicked = e.Board.StatCount + 1

	for !e.GamePlayExitRequested {
		if e.GamePaused {
			if SoundHasTimeElapsed(&e.TickTimeCounter, 25) {
				pauseBlink = !pauseBlink
			}
			if pauseBlink {
				e.VideoWriteText(int16(e.Board.Stats[0].X)-1, int16(e.Board.Stats[0].Y)-1, ElementDefs[E_PLAYER].Color, string([]byte{ElementDefs[E_PLAYER].Character}))
			} else {
				if e.Board.Tiles[e.Board.Stats[0].X][e.Board.Stats[0].Y].Element == E_PLAYER {
					e.VideoWriteText(int16(e.Board.Stats[0].X)-1, int16(e.Board.Stats[0].Y)-1, 0x0F, " ")
				} else {
					e.BoardDrawTile(int16(e.Board.Stats[0].X), int16(e.Board.Stats[0].Y))
				}
			}
			e.VideoWriteText(64, 5, 0x1F, "Pausing...")

			e.InputUpdate()
			if InputKeyPressed == KEY_ESCAPE {
				e.GamePromptEndPlay()
			}
			if InputDeltaX != 0 || InputDeltaY != 0 {
				ElementDefs[e.Board.Tiles[int16(e.Board.Stats[0].X)+InputDeltaX][int16(e.Board.Stats[0].Y)+InputDeltaY].Element].TouchProc(e, int16(e.Board.Stats[0].X)+InputDeltaX, int16(e.Board.Stats[0].Y)+InputDeltaY, 0, &InputDeltaX, &InputDeltaY)
			}
			if (InputDeltaX != 0 || InputDeltaY != 0) && ElementDefs[e.Board.Tiles[int16(e.Board.Stats[0].X)+InputDeltaX][int16(e.Board.Stats[0].Y)+InputDeltaY].Element].Walkable {
				// Move player
				if e.Board.Tiles[e.Board.Stats[0].X][e.Board.Stats[0].Y].Element == E_PLAYER {
					e.MoveStat(0, int16(e.Board.Stats[0].X)+InputDeltaX, int16(e.Board.Stats[0].Y)+InputDeltaY)
				} else {
					e.BoardDrawTile(int16(e.Board.Stats[0].X), int16(e.Board.Stats[0].Y))
					e.Board.Stats[0].X += byte(InputDeltaX)
					e.Board.Stats[0].Y += byte(InputDeltaY)
					e.Board.Tiles[e.Board.Stats[0].X][e.Board.Stats[0].Y].Element = E_PLAYER
					e.Board.Tiles[e.Board.Stats[0].X][e.Board.Stats[0].Y].Color = ElementDefs[E_PLAYER].Color
					e.BoardDrawTile(int16(e.Board.Stats[0].X), int16(e.Board.Stats[0].Y))
					e.DrawPlayerSurroundings(int16(e.Board.Stats[0].X), int16(e.Board.Stats[0].Y), 0)
					e.DrawPlayerSurroundings(int16(e.Board.Stats[0].X)-InputDeltaX, int16(e.Board.Stats[0].Y)-InputDeltaY, 0)
				}

				// Unpause
				e.GamePaused = false
				e.SidebarClearLine(5)
				e.CurrentTick = e.Random(100)
				e.CurrentStatTicked = e.Board.StatCount + 1
				e.World.Info.IsSave = true
			}
		} else { // not e.GamePaused
			// Pace, then run one full game step (M0.5). Delay is a no-op when
			// e.Headless (M0.4); SoundHasTimeElapsed still gates the step so it
			// keeps pacing interactive play (currently stubbed to true — see
			// NOTES.md 2026-07-09). The per-cycle advance and input read now live
			// in GameStep. The pause branch keeps its own input handling and sets
			// e.CurrentTick on unpause, which the next GameStep's advance picks up,
			// so behavior is unchanged (the shared "all stats ticked" block that
			// used to follow both branches folded into GameStep).
			e.Delay(e.TickTimeDuration * 10)
			if SoundHasTimeElapsed(&e.TickTimeCounter, e.TickTimeDuration) {
				// Construct the single-player input map for the interactive loop.
				// The engine's cached InputDeltaX/Y etc. were filled by InputUpdate
				// at the end of the previous GameStep cycle.
				e.GameStep(map[int16]PlayerInput{
					0: {
						DeltaX: e.InputDeltaX,
						DeltaY: e.InputDeltaY,
						Shift:  e.InputShiftPressed,
						Key:    e.InputKeyPressed,
					},
				})
			}
		}

		// Drain and process events for interactive single-player mode
		for len(e.Events) > 0 {
			event := e.Events[0]
			e.Events = e.Events[1:]

			switch ev := event.(type) {
			case ScrollEvent:
				var textWindow TTextWindowState
				textWindow.Title = ev.Title
				textWindow.Selectable = true
				textWindow.LinePos = 1
				textWindow.LineCount = int16(len(ev.Lines))
				for idx, line := range ev.Lines {
					textWindow.Lines[idx] = line
				}

				TextWindowDrawOpen(&textWindow)
				TextWindowSelect(&textWindow, true, false)
				TextWindowDrawClose(&textWindow)
				TextWindowFree(&textWindow)

				if Length(textWindow.Hyperlink) != 0 {
					e.PendingScrollReply = textWindow.Hyperlink
					e.PendingScrollStatId = ev.StatId
				}
			case QuitPromptEvent:
				e.GamePlayExitRequested = e.SidebarPromptYesNo("End this game? ", true)
				if InputKeyPressed == '\x1b' {
					e.GamePlayExitRequested = false
				}
			case HelpEvent:
				TextWindowDisplayFile(ev.Filename, ev.Title)
			}
		}
	}

	SoundClearQueue()
	if e.GameStateElement == E_PLAYER {
		if e.PlayerFor(0).Health <= 0 {
			e.HighScoresAdd(e.PlayerFor(0).Score)
		}
	} else if e.GameStateElement == E_MONITOR {
		e.SidebarClearLine(5)
	}

	// Drain final events (such as HighScoreEntryEvent)
	for len(e.Events) > 0 {
		event := e.Events[0]
		e.Events = e.Events[1:]

		switch ev := event.(type) {
		case HighScoreEntryEvent:
			var textWindow TTextWindowState
			for i := int16(29); i >= ev.ListPos; i-- {
				e.HighScoreList[i+1-1] = e.HighScoreList[i-1]
			}
			e.HighScoreList[ev.ListPos-1].Score = ev.Score
			e.HighScoreList[ev.ListPos-1].Name = "-- You! --"

			e.HighScoresInitTextWindow(&textWindow)
			textWindow.LinePos = ev.ListPos
			textWindow.Title = "New high score for " + e.World.Info.Name

			TextWindowDrawOpen(&textWindow)
			TextWindowDraw(&textWindow, false, false)
			name := ""
			e.PopupPromptString("Congratulations!  Enter your name:", &name)
			e.HighScoreList[ev.ListPos-1].Name = name
			e.HighScoresSave()
			TextWindowDrawClose(&textWindow)
			e.TransitionDrawToBoard()
			TextWindowFree(&textWindow)
		}
	}

	e.Board.Tiles[e.Board.Stats[0].X][e.Board.Stats[0].Y].Element = E_PLAYER
	e.Board.Tiles[e.Board.Stats[0].X][e.Board.Stats[0].Y].Color = ElementDefs[E_PLAYER].Color
	e.SoundBlockQueueing = false
}

func (e *Engine) GameTitleLoop() {
	var (
		boardChanged bool
		startPlay    bool
	)
	e.GameTitleExitRequested = false
	e.JustStarted = true
	e.ReturnBoardId = 0
	boardChanged = true
	for {
		e.BoardChange(0)
		for {
			e.GameStateElement = E_MONITOR
			startPlay = false
			e.GamePaused = false
			e.GamePlayLoop(boardChanged)
			boardChanged = false
			switch UpCase(InputKeyPressed) {
			case 'W':
				if e.GameWorldLoad(".ZZT") {
					e.ReturnBoardId = e.World.Info.CurrentBoard
					boardChanged = true
				}
			case 'P':
				if e.World.Info.IsSave && !e.DebugEnabled {
					startPlay = e.WorldLoad(e.World.Info.Name, ".ZZT", false)
					e.ReturnBoardId = e.World.Info.CurrentBoard
				} else {
					startPlay = true
				}
				if startPlay {
					e.BoardChange(e.ReturnBoardId)
					e.BoardEnter()
				}
			case 'A':
				GameAboutScreen()
			case 'E':
				if e.EditorEnabled {
					EditorLoop()
					e.ReturnBoardId = e.World.Info.CurrentBoard
					boardChanged = true
				}
			case 'S':
				e.SidebarPromptSlider(true, 66, 21, "Game speed:;FS", &e.TickSpeed)
				InputKeyPressed = '\x00'
			case 'R':
				if e.GameWorldLoad(".SAV") {
					e.ReturnBoardId = e.World.Info.CurrentBoard
					e.BoardChange(e.ReturnBoardId)
					startPlay = true
				}
			case 'H':
				e.HighScoresLoad()
				e.HighScoresDisplay(1)
			case '|':
				e.GameDebugPrompt()
			case KEY_ESCAPE, 'Q':
				e.GameTitleExitRequested = e.SidebarPromptYesNo("Quit ZZT? ", true)
			}
			if startPlay {
				e.GameStateElement = E_PLAYER
				e.GamePaused = true
				e.GamePlayLoop(true)
				boardChanged = true
			}
			if boardChanged || e.GameTitleExitRequested {
				break
			}
		}
		if e.GameTitleExitRequested {
			break
		}
	}
}

// --- Global Wrappers ---

func AddStat(tx, ty int16, element byte, color, tcycle int16, template TStat)  {
	E.AddStat(tx, ty, element, color, tcycle, template)
}

func BoardAttack(attackerStatId int16, x, y int16)  {
	E.BoardAttack(attackerStatId, x, y)
}

func BoardChange(boardId int16)  {
	E.BoardChange(boardId)
}

func BoardClose()  {
	E.BoardClose()
}

func BoardCreate()  {
	E.BoardCreate()
}

func BoardDamageTile(x, y int16)  {
	E.BoardDamageTile(x, y)
}

func BoardDrawBorder()  {
	E.BoardDrawBorder()
}

func BoardDrawTile(x, y int16)  {
	E.BoardDrawTile(x, y)
}

func BoardEnter()  {
	E.BoardEnter()
}

func BoardOpen(boardId int16)  {
	E.BoardOpen(boardId)
}

func BoardPassageTeleport(x, y int16)  {
	E.BoardPassageTeleport(x, y)
}

func BoardPrepareTileForPlacement(x, y int16) (BoardPrepareTileForPlacement bool) {
	return E.BoardPrepareTileForPlacement(x, y)
}

func BoardShoot(element byte, tx, ty, deltaX, deltaY int16, source int16) (BoardShoot bool) {
	return E.BoardShoot(element, tx, ty, deltaX, deltaY, source)
}

func CalcDirectionRnd(deltaX, deltaY *int16)  {
	E.CalcDirectionRnd(deltaX, deltaY)
}

func CalcDirectionSeek(x, y int16, deltaX, deltaY *int16)  {
	E.CalcDirectionSeek(x, y, deltaX, deltaY)
}

func CopyStatDataToTextWindow(statId int16, state *TTextWindowState)  {
	E.CopyStatDataToTextWindow(statId, state)
}

func DamageStat(attackerStatId int16)  {
	E.DamageStat(attackerStatId)
}

func DisplayMessage(time int16, message string)  {
	E.DisplayMessage(time, message)
}

func GameDebugPrompt()  {
	E.GameDebugPrompt()
}

func GamePlayLoop(boardChanged bool)  {
	E.GamePlayLoop(boardChanged)
}

func GameStep(inputs map[int16]PlayerInput) {
	E.GameStep(inputs)
}

func GameTitleLoop()  {
	E.GameTitleLoop()
}

func GameUpdateSidebar()  {
	E.GameUpdateSidebar()
}

func GameWorldLoad(extension string) (GameWorldLoad bool) {
	return E.GameWorldLoad(extension)
}

func GameWorldSave(prompt string, filename *string, extension string)  {
	E.GameWorldSave(prompt, filename, extension)
}

func GenerateTransitionTable()  {
	E.GenerateTransitionTable()
}

func GetStatIdAt(x, y int16) (GetStatIdAt int16) {
	return E.GetStatIdAt(x, y)
}

func HighScoresAdd(score int16)  {
	E.HighScoresAdd(score)
}

func HighScoresDisplay(linePos int16)  {
	E.HighScoresDisplay(linePos)
}

func HighScoresInitTextWindow(state *TTextWindowState)  {
	E.HighScoresInitTextWindow(state)
}

func HighScoresLoad()  {
	E.HighScoresLoad()
}

func HighScoresSave()  {
	E.HighScoresSave()
}

func MoveStat(statId int16, newX, newY int16)  {
	E.MoveStat(statId, newX, newY)
}

func PauseOnError()  {
	E.PauseOnError()
}

func PopupPromptString(question string, buffer *string)  {
	E.PopupPromptString(question, buffer)
}

func PromptString(x, y, arrowColor, color, width int16, mode byte, buffer *string)  {
	E.PromptString(x, y, arrowColor, color, width, mode, buffer)
}

func RemoveStat(statId int16)  {
	E.RemoveStat(statId)
}

func SidebarClear()  {
	E.SidebarClear()
}

func SidebarClearLine(y int16)  {
	E.SidebarClearLine(y)
}

func SidebarPromptCharacter(editable bool, x, y int16, prompt string, value *byte)  {
	E.SidebarPromptCharacter(editable, x, y, prompt, value)
}

func SidebarPromptChoice(editable bool, y int16, prompt, choiceStr string, result *byte)  {
	E.SidebarPromptChoice(editable, y, prompt, choiceStr, result)
}

func SidebarPromptDirection(editable bool, y int16, prompt string, deltaX, deltaY *int16)  {
	E.SidebarPromptDirection(editable, y, prompt, deltaX, deltaY)
}

func SidebarPromptSlider(editable bool, x, y int16, prompt string, value *byte)  {
	E.SidebarPromptSlider(editable, x, y, prompt, value)
}

func SidebarPromptString(prompt string, extension string, filename *string, promptMode byte)  {
	E.SidebarPromptString(prompt, extension, filename, promptMode)
}

func SidebarPromptYesNo(message string, defaultReturn bool) (SidebarPromptYesNo bool) {
	return E.SidebarPromptYesNo(message, defaultReturn)
}

func TileToColorAndChar(x, y int16) (color, char byte) {
	return E.TileToColorAndChar(x, y)
}

func TransitionDrawBoardChange()  {
	E.TransitionDrawBoardChange()
}

func TransitionDrawToBoard()  {
	E.TransitionDrawToBoard()
}

func TransitionDrawToFill(chr byte, color int16)  {
	E.TransitionDrawToFill(chr, color)
}

func WorldCreate()  {
	E.WorldCreate()
}

func WorldLoad(filename, extension string, titleOnly bool) (WorldLoad bool) {
	return E.WorldLoad(filename, extension, titleOnly)
}

func WorldSave(filename, extension string)  {
	E.WorldSave(filename, extension)
}

func WorldUnload()  {
	E.WorldUnload()
}
