package zztgo

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"sync"
)

// EditorSession is an isolated, never-ticked editing copy of one world. It is
// deliberately separate from RoomManager: opening an editor can neither join a
// live room nor observe its mutable board state.
//
// Members is a set, rather than an owner field, because M10 raises the member
// cap and fans updates out from this same session model. M5.0 caps it at one.
// Every future edit must use Apply so mutations stay serialized when that cap
// changes.
type EditorSession struct {
	mu sync.Mutex

	WorldName string
	engine    *Engine
	Members   map[*webSocketClient]struct{}
}

func NewEditorSession(worldName string, world TWorld) *EditorSession {
	e := NewEngine()
	e.Headless = true
	e.MultiRoom = true
	e.SetInputSource(&ScriptedInput{})
	e.World = cloneWorld(world)

	boardID := e.World.Info.CurrentBoard
	if boardID < 0 || boardID > e.World.BoardCount {
		boardID = 0
	}
	e.BoardOpen(boardID)
	e.GenerateTransitionTable()
	e.TransitionDrawToBoard()
	// An editor snapshot is always a complete frame; do not leak setup dirty
	// cells into an eventual M5.1 edit diff.
	e.DrainScreenDirty()

	return &EditorSession{
		WorldName: worldName,
		engine:    e,
		Members:   make(map[*webSocketClient]struct{}),
	}
}

func (s *EditorSession) Enter(member *webSocketClient) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.Members) >= 1 {
		return fmt.Errorf("editor session is already in use")
	}
	s.Members[member] = struct{}{}
	return nil
}

func (s *EditorSession) Exit(member *webSocketClient) {
	s.mu.Lock()
	delete(s.Members, member)
	s.mu.Unlock()
}

// Apply is the sole serialized session boundary. M5.0 is read-only, but later
// editor tasks must make every world mutation inside this callback.
func (s *EditorSession) Apply(member *webSocketClient, fn func(*Engine)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Members[member]; !ok {
		return fmt.Errorf("editor session membership required")
	}
	fn(s.engine)
	return nil
}

func (s *EditorSession) Snapshot(member *webSocketClient, x, y int16) (EditorSnapshotMessage, error) {
	var snapshot EditorSnapshotMessage
	err := s.Apply(member, func(e *Engine) {
		snapshot = editorSnapshot(e, x, y)
	})
	return snapshot, err
}

// editorSnapshot builds a full-frame editor snapshot and drains the dirty list,
// so the caller's next edit can return just its dirty cells. A board change
// (add/switch/import) reuses it: repainting the whole board is exactly what
// EditorDrawRefresh does after those operations in the Pascal editor.
func editorSnapshot(e *Engine, x, y int16) EditorSnapshotMessage {
	snapshot := EditorSnapshotMessage{
		Type:       MessageTypeEditorSnapshot,
		BoardID:    e.World.Info.CurrentBoard,
		Screen:     screenCells(e),
		Inspect:    editorTileInspect(e, x, y),
		Properties: editorProperties(e),
	}
	e.DrainScreenDirty()
	return snapshot
}

// Edit applies EditorPlaceTile's placement semantics to the isolated session.
// It deliberately calls BoardPrepareTileForPlacement, which is where vanilla
// removes an existing non-player stat and decides whether a tile may be
// overwritten. The browser never writes board state directly.
func (s *EditorSession) Edit(member *webSocketClient, edit EditorEditMessage) (EditorDiffMessage, error) {
	var reply EditorDiffMessage
	err := s.Apply(member, func(e *Engine) {
		x, y := editorClamp(edit.X, edit.Y)
		switch edit.Op {
		case "place":
			if editorPlaceTile(e, x, y, edit.Element, edit.Color, edit.Copied) {
				e.BoardClose()
			}
		case "erase":
			if e.BoardPrepareTileForPlacement(x, y) {
				e.Board.Tiles[x][y].Element = E_EMPTY
				e.Board.Tiles[x][y].Color = 0
				editorDrawTileAndNeighbors(e, x, y)
				e.BoardClose()
			}
		case "fill":
			if editorFloodFill(e, x, y, e.Board.Tiles[x][y], edit.Element, edit.Color, edit.Copied) {
				e.BoardClose()
			}
		}
		reply = EditorDiffMessage{
			Type:    MessageTypeEditorDiff,
			Cells:   e.DrainScreenDirty(),
			Inspect: editorTileInspect(e, x, y),
		}
	})
	return reply, err
}

func editorClamp(x, y int16) (int16, int16) {
	if x < 1 {
		x = 1
	} else if x > BOARD_WIDTH {
		x = BOARD_WIDTH
	}
	if y < 1 {
		y = 1
	} else if y > BOARD_HEIGHT {
		y = BOARD_HEIGHT
	}
	return x, y
}

func editorDrawTileAndNeighbors(e *Engine, x, y int16) {
	e.BoardDrawTile(x, y)
	for i := 0; i <= 3; i++ {
		nx, ny := x+NeighborDeltaX[i], y+NeighborDeltaY[i]
		if nx >= 1 && nx <= BOARD_WIDTH && ny >= 1 && ny <= BOARD_HEIGHT {
			e.BoardDrawTile(nx, ny)
		}
	}
}

func editorPlaceTile(e *Engine, x, y int16, element, color byte, copied bool) bool {
	if (!copied && !editorPatternElement(element)) || !e.BoardPrepareTileForPlacement(x, y) {
		return false
	}
	e.Board.Tiles[x][y].Element = element
	e.Board.Tiles[x][y].Color = color
	editorDrawTileAndNeighbors(e, x, y)
	return true
}

func editorPatternElement(element byte) bool {
	return element == E_SOLID || element == E_NORMAL || element == E_BREAKABLE || element == E_EMPTY || element == E_LINE
}

// editorFloodFill is EditorFloodFill with its selected pattern passed across
// the protocol. The 256-cell queue and the Empty-tile color exception preserve
// the Pascal editor's fill boundary rules.
func editorFloodFill(e *Engine, x, y int16, from TTile, element, color byte, copied bool) bool {
	if !copied && !editorPatternElement(element) {
		return false
	}
	var xPosition, yPosition [256]int16
	toFill, filled := byte(1), byte(0)
	changed := false
	for toFill != filled {
		tileAt := e.Board.Tiles[x][y]
		if editorPlaceTile(e, x, y, element, color, copied) {
			changed = true
			if e.Board.Tiles[x][y].Element != tileAt.Element || e.Board.Tiles[x][y].Color != tileAt.Color {
				for i := 0; i <= 3; i++ {
					nx, ny := x+NeighborDeltaX[i], y+NeighborDeltaY[i]
					tile := e.Board.Tiles[nx][ny]
					if tile.Element == from.Element && (from.Element == E_EMPTY || tile.Color == from.Color) {
						xPosition[toFill] = nx
						yPosition[toFill] = ny
						toFill++
					}
				}
			}
		}
		filled++
		x, y = xPosition[filled], yPosition[filled]
	}
	return changed
}

func (s *EditorSession) Inspect(member *webSocketClient, x, y int16) (EditorInspectMessage, error) {
	var reply EditorInspectMessage
	err := s.Apply(member, func(e *Engine) {
		reply = EditorInspectMessage{
			Type:    MessageTypeEditorInspect,
			Inspect: editorTileInspect(e, x, y),
		}
	})
	return reply, err
}

// Properties returns the currently-open board's editable metadata. This is
// read through Apply even though it does not mutate: one serialized boundary
// makes M10's eventual multi-editor session safe by construction.
func (s *EditorSession) Properties(member *webSocketClient) (EditorPropertiesMessage, error) {
	var reply EditorPropertiesMessage
	err := s.Apply(member, func(e *Engine) {
		reply = EditorPropertiesMessage{
			Type:       MessageTypeEditorProperties,
			Properties: editorProperties(e),
			Screen:     screenCells(e),
		}
	})
	return reply, err
}

// SetProperty is the sole mutation path for Board Information and world-name
// dialogs. BoardClose is intentional: editor sessions retain their editable
// world as serialized board data, just like vanilla's editor does between
// BoardOpen calls.
func (s *EditorSession) SetProperty(member *webSocketClient, edit EditorPropertyMessage) (EditorPropertiesMessage, error) {
	var reply EditorPropertiesMessage
	err := s.Apply(member, func(e *Engine) {
		switch edit.Field {
		case "boardTitle":
			e.Board.Name = editorString(edit.Text, SizeOfBoardName-1)
		case "worldName":
			e.World.Info.Name = editorString(edit.Text, 20)
		case "maxShots":
			if edit.Value < 0 || edit.Value > 255 {
				return
			}
			e.Board.Info.MaxShots = byte(edit.Value)
		case "dark":
			e.Board.Info.IsDark = edit.Bool
		case "exit":
			if edit.Exit < 0 || edit.Exit >= int16(len(e.Board.Info.NeighborBoards)) || edit.Value < 0 || edit.Value > e.World.BoardCount {
				return
			}
			e.Board.Info.NeighborBoards[edit.Exit] = byte(edit.Value)
		case "reenter":
			e.Board.Info.ReenterWhenZapped = edit.Bool
		case "timeLimit":
			if edit.Value < 0 {
				return
			}
			e.Board.Info.TimeLimitSec = edit.Value
		default:
			return
		}

		e.BoardClose()
		e.TransitionDrawToBoard()
		reply = EditorPropertiesMessage{
			Type:       MessageTypeEditorProperties,
			Properties: editorProperties(e),
			Screen:     screenCells(e),
		}
		e.DrainScreenDirty()
	})
	return reply, err
}

// SetStat changes one of EditorEditStat's parameters. It does not accept
// follower/leader fields: vanilla's stat dialog leaves centipede chains alone.
// Likewise it never reads or writes object Data/DataLen, so an object's bound
// program remains bound until M5.4 implements the program editor.
func (s *EditorSession) SetStat(member *webSocketClient, edit EditorStatMessage) (EditorStatSettingsMessage, error) {
	var reply EditorStatSettingsMessage
	err := s.Apply(member, func(e *Engine) {
		if edit.StatID < 0 || edit.StatID > e.Board.StatCount {
			return
		}
		stat := &e.Board.Stats[edit.StatID]
		tile := e.Board.Tiles[stat.X][stat.Y]
		element := tile.Element
		def := ElementDefs[element]
		changed := false

		switch edit.Field {
		case "p1":
			if def.Param1Name == "" || edit.Value < 0 || edit.Value > 255 || (def.ParamTextName == "" && edit.Value > 8) {
				return
			}
			stat.P1 = byte(edit.Value)
			e.World.EditorStatSettings[element].P1 = stat.P1
			changed = true
		case "p2":
			if def.Param2Name == "" || edit.Value < 0 || edit.Value > 8 {
				return
			}
			stat.P2 = stat.P2&0x80 | byte(edit.Value)
			e.World.EditorStatSettings[element].P2 = stat.P2
			changed = true
		case "bulletType":
			if def.ParamBulletTypeName == "" || edit.Value < 0 || edit.Value > 1 {
				return
			}
			stat.P2 = stat.P2&0x7f | byte(edit.Value<<7)
			e.World.EditorStatSettings[element].P2 = stat.P2
			changed = true
		case "direction":
			if def.ParamDirName == "" || edit.Value < 0 || edit.Value > 3 {
				return
			}
			stat.StepX = NeighborDeltaX[edit.Value]
			stat.StepY = NeighborDeltaY[edit.Value]
			e.World.EditorStatSettings[element].StepX = stat.StepX
			e.World.EditorStatSettings[element].StepY = stat.StepY
			changed = true
		case "p3":
			if def.ParamBoardName == "" || edit.Value < 0 || edit.Value > e.World.BoardCount {
				return
			}
			stat.P3 = byte(edit.Value)
			e.World.EditorStatSettings[element].P3 = stat.P3
			changed = true
		case "cycle":
			if edit.Value < 0 || edit.Value > 32767 {
				return
			}
			stat.Cycle = edit.Value
			changed = true
		default:
			return
		}

		if changed {
			e.BoardDrawTile(int16(stat.X), int16(stat.Y))
			e.BoardClose()
		}
		reply = EditorStatSettingsMessage{
			Type:    MessageTypeEditorStatSettings,
			Inspect: editorTileInspect(e, int16(stat.X), int16(stat.Y)),
			Cells:   e.DrainScreenDirty(),
		}
	})
	return reply, err
}

// ProgramText returns an object/scroll's ZZT-OOP program as lines for the M5.4
// browser code editor. Only text-backed elements (ParamTextName set) have one;
// anything else returns an empty message, which the client ignores.
func (s *EditorSession) ProgramText(member *webSocketClient, statId int16) (EditorProgramMessage, error) {
	var reply EditorProgramMessage
	err := s.Apply(member, func(e *Engine) {
		if statId < 0 || statId > e.Board.StatCount {
			return
		}
		stat := &e.Board.Stats[statId]
		def := ElementDefs[e.Board.Tiles[stat.X][stat.Y].Element]
		if def.ParamTextName == "" {
			return
		}
		labels, warnings := e.OopAnalyze(statId)
		reply = EditorProgramMessage{
			Type:     MessageTypeEditorProgramText,
			StatID:   statId,
			Prompt:   def.ParamTextName,
			Lines:    editorProgramLines(e, statId),
			Labels:   labels,
			Warnings: warnings,
		}
	})
	return reply, err
}

// editorProgramLines is CopyStatDataToTextWindow: the stat's Data, up to DataLen
// bytes, split on carriage returns. A trailing partial with no final CR is
// dropped, exactly as the vanilla routine does. A negative DataLen means the
// stat's program is shared with an earlier stat (BoardClose deduplicates
// identical programs and rewrites DataLen in place); resolve it the way
// BoardOpen does so shared objects still edit.
func editorProgramLines(e *Engine, statId int16) []string {
	stat := &e.Board.Stats[statId]
	data := stat.Data
	dataLen := int(stat.DataLen)
	if stat.DataLen < 0 {
		src := &e.Board.Stats[-stat.DataLen]
		data = src.Data
		dataLen = int(src.DataLen)
	}
	if dataLen > len(data) {
		dataLen = len(data)
	}
	lines := []string{}
	var buf []byte
	for i := 0; i < dataLen; i++ {
		if data[i] == KEY_ENTER {
			lines = append(lines, string(buf))
			buf = buf[:0]
		} else {
			buf = append(buf, data[i])
		}
	}
	return lines
}

// SaveProgram writes an edited program back to a stat, mirroring the save half
// of EditorEditStatText: Data becomes each line followed by a carriage return,
// and DataLen is their total length. BoardClose serializes the board so the
// text round-trips through the vanilla format, and re-shares identical programs.
// Rebuilding Data fresh sidesteps the shared-data (negative DataLen) quirk: an
// edited program is by definition no longer identical to the one it shared.
func (s *EditorSession) SaveProgram(member *webSocketClient, statId int16, lines []string) (EditorStatSettingsMessage, error) {
	var reply EditorStatSettingsMessage
	err := s.Apply(member, func(e *Engine) {
		if statId < 0 || statId > e.Board.StatCount {
			return
		}
		stat := &e.Board.Stats[statId]
		def := ElementDefs[e.Board.Tiles[stat.X][stat.Y].Element]
		if def.ParamTextName != "" {
			editorUnbindSharers(e, statId)
			if len(lines) > MAX_TEXT_WINDOW_LINES {
				lines = lines[:MAX_TEXT_WINDOW_LINES]
			}
			total := 0
			for _, line := range lines {
				total += len(line) + 1
			}
			// DataLen is an int16 in the vanilla stat record; refuse a program
			// that cannot be represented rather than wrapping it.
			if total <= 0x7FFF {
				var buf []byte
				for _, line := range lines {
					buf = append(buf, line...)
					buf = append(buf, KEY_ENTER)
				}
				stat.Data = string(buf)
				stat.DataLen = int16(total)
				e.BoardDrawTile(int16(stat.X), int16(stat.Y))
				e.BoardClose()
			}
		}
		reply = EditorStatSettingsMessage{
			Type:    MessageTypeEditorStatSettings,
			Inspect: editorTileInspect(e, int16(stat.X), int16(stat.Y)),
			Cells:   e.DrainScreenDirty(),
		}
	})
	return reply, err
}

// AddBoard appends a new named board and makes it current, mirroring
// EditorAppendBoard (EDITOR.PAS:51). Board names are free text in vanilla — only
// .BRD filenames are sanitized — so Name is only trimmed to the record width.
// At MAX_BOARD it is a no-op that returns the unchanged frame, exactly as the
// Pascal guard does. The reply is a full snapshot: a new board repaints all of it.
func (s *EditorSession) AddBoard(member *webSocketClient, name string) (EditorSnapshotMessage, error) {
	var reply EditorSnapshotMessage
	err := s.Apply(member, func(e *Engine) {
		if e.World.BoardCount < MAX_BOARD {
			e.BoardClose()
			e.World.BoardCount++
			e.World.Info.CurrentBoard = e.World.BoardCount
			e.World.BoardLen[e.World.BoardCount] = 0
			e.BoardCreate()
			e.Board.Name = editorString(name, SizeOfBoardName-1)
			e.BoardClose()
			e.TransitionDrawToBoard()
		}
		reply = editorSnapshot(e, BOARD_WIDTH/2, BOARD_HEIGHT/2)
	})
	return reply, err
}

// SwitchBoard makes boardId the current board via BoardChange semantics
// (EDITOR.PAS:668 'B'). boardId 0 is the title board; anything past BoardCount is
// rejected (the "Add new board" sentinel is resolved client-side into an add).
func (s *EditorSession) SwitchBoard(member *webSocketClient, boardId int16) (EditorSnapshotMessage, error) {
	var reply EditorSnapshotMessage
	err := s.Apply(member, func(e *Engine) {
		if boardId >= 0 && boardId <= e.World.BoardCount && boardId != e.World.Info.CurrentBoard {
			e.BoardChange(boardId)
			e.TransitionDrawToBoard()
		}
		reply = editorSnapshot(e, BOARD_WIDTH/2, BOARD_HEIGHT/2)
	})
	return reply, err
}

// ExportBoard serializes the current board to vanilla .BRD bytes, mirroring
// EditorTransferBoard's export branch (EDITOR.PAS:556): a 2-byte little-endian
// length prefix followed by the serialized board. BoardClose flushes pending
// edits into BoardData first, exactly as the Pascal does before BlockWrite.
func (s *EditorSession) ExportBoard(member *webSocketClient) (EditorBoardDataMessage, error) {
	var reply EditorBoardDataMessage
	err := s.Apply(member, func(e *Engine) {
		e.BoardClose()
		cur := e.World.Info.CurrentBoard
		data := e.World.BoardData[cur]
		brd := make([]byte, 2+len(data))
		StoreInt16(brd[:2], int16(len(data)))
		copy(brd[2:], data)
		name, nameErr := SanitizeSaveName(e.Board.Name)
		if nameErr != nil {
			name = "BOARD"
		}
		reply = EditorBoardDataMessage{
			Type: MessageTypeEditorBoardData,
			Name: name,
			Data: base64.StdEncoding.EncodeToString(brd),
		}
	})
	return reply, err
}

// ImportBoard replaces the current board with .BRD bytes, mirroring
// EditorTransferBoard's import branch (EDITOR.PAS:534): read the 2-byte length,
// swap in the board data, reopen it, and clear the four edge exits (they name
// boards that need not exist in this world). The bytes come from a client file,
// so a malformed board must be rejected, never crash the server: the length is
// bounded and BoardOpen is guarded, rolling the previous board back on any panic.
func (s *EditorSession) ImportBoard(member *webSocketClient, data []byte) (EditorSnapshotMessage, error) {
	var reply EditorSnapshotMessage
	err := s.Apply(member, func(e *Engine) {
		reply = editorSnapshot(e, BOARD_WIDTH/2, BOARD_HEIGHT/2)
		if len(data) < 2 {
			return
		}
		length := LoadInt16(data[:2])
		if length < 0 || int(length) != len(data)-2 || int(length) > len(e.IoTmpBuf) {
			return
		}

		e.BoardClose()
		cur := e.World.Info.CurrentBoard
		prevData, prevLen := e.World.BoardData[cur], e.World.BoardLen[cur]
		e.World.BoardData[cur] = append([]byte(nil), data[2:]...)
		e.World.BoardLen[cur] = length
		if !safeBoardOpen(e, cur) {
			e.World.BoardData[cur], e.World.BoardLen[cur] = prevData, prevLen
			e.BoardOpen(cur)
			e.TransitionDrawToBoard()
			reply = editorSnapshot(e, BOARD_WIDTH/2, BOARD_HEIGHT/2)
			return
		}
		for i := 0; i <= 3; i++ {
			e.Board.Info.NeighborBoards[i] = 0
		}
		e.TransitionDrawToBoard()
		reply = editorSnapshot(e, BOARD_WIDTH/2, BOARD_HEIGHT/2)
	})
	return reply, err
}

// safeBoardOpen runs BoardOpen under a recover. BoardOpen has no bounds checks —
// a truncated or internally inconsistent .BRD would slice past its data and
// panic. The editor session is isolated and never ticked, so recovering here
// only rejects a bad import; it cannot affect any live room or the sim.
func safeBoardOpen(e *Engine, boardId int16) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	e.BoardOpen(boardId)
	return true
}

// editorUnbindSharers gives every stat that shares statId's program (DataLen ==
// -statId, the negative reference BoardClose writes in place after a prior edit)
// its own copy of the current program, before statId's program is overwritten.
// Vanilla never hits this because its editor closes the board only at save time;
// the fork's per-edit BoardClose means a sibling can be left bound to the object
// being edited, and editing it would otherwise silently rewrite that sibling.
func editorUnbindSharers(e *Engine, statId int16) {
	stat := &e.Board.Stats[statId]
	data, dataLen := stat.Data, stat.DataLen
	if dataLen < 0 {
		src := &e.Board.Stats[-dataLen]
		data, dataLen = src.Data, src.DataLen
	}
	for i := int16(0); i <= e.Board.StatCount; i++ {
		if i != statId && e.Board.Stats[i].DataLen == -statId {
			e.Board.Stats[i].Data = data
			e.Board.Stats[i].DataLen = dataLen
		}
	}
}

// WorldBytes serializes the whole session world to vanilla .ZZT bytes through
// worldWriteTo — the same seam WorldSave and SaveSnapshot use — so the file loads
// in DOS ZZT/zeta and through WorldLoad here alike. BoardClose flushes the open
// board's edits into BoardData first, then BoardOpen restores the in-memory board
// exactly as WorldSave does. When name is non-empty it is written into
// World.Info.Name, mirroring GameWorldSave's .ZZT behavior. IsSave is cleared so
// the result loads as an authored world, not a saved game.
func (s *EditorSession) WorldBytes(member *webSocketClient, name string) ([]byte, error) {
	var out []byte
	err := s.Apply(member, func(e *Engine) {
		e.BoardClose()
		if name != "" {
			e.World.Info.Name = editorString(name, 20)
		}
		e.World.Info.IsSave = false
		var buf bytes.Buffer
		if e.worldWriteTo(&buf) == nil {
			out = buf.Bytes()
		}
		e.BoardOpen(e.World.Info.CurrentBoard)
	})
	return out, err
}

// UploadWorld replaces the entire session world with data, vanilla .ZZT bytes
// from a client file, after the M7.5 gate: the bytes must load and survive 200
// headless GameSteps without a panic. It mirrors ImportBoard for a whole world.
// A world that fails the gate leaves the session untouched, and the returned gate
// string tells the client why. The returned snapshot is always a complete frame
// (unchanged on refusal, the uploaded board on success).
func (s *EditorSession) UploadWorld(member *webSocketClient, data []byte) (EditorSnapshotMessage, string, error) {
	var reply EditorSnapshotMessage
	var gate string
	err := s.Apply(member, func(e *Engine) {
		reply = editorSnapshot(e, BOARD_WIDTH/2, BOARD_HEIGHT/2)
		if verr := validateGeneratedZWD(data); verr != nil {
			gate = verr.Error()
			return
		}
		scratch := newSnapshotEngine()
		if rerr := scratch.worldReadFrom(bytes.NewReader(data), false, nil); rerr != nil {
			gate = rerr.Error()
			return
		}
		e.World = scratch.World
		boardID := e.World.Info.CurrentBoard
		if boardID < 0 || boardID > e.World.BoardCount {
			boardID = 0
		}
		e.BoardOpen(boardID)
		e.GenerateTransitionTable()
		e.TransitionDrawToBoard()
		reply = editorSnapshot(e, BOARD_WIDTH/2, BOARD_HEIGHT/2)
	})
	return reply, gate, err
}

func editorProperties(e *Engine) EditorProperties {
	options := make([]EditorBoardOption, 0, e.World.BoardCount+1)
	options = append(options, EditorBoardOption{ID: 0, Name: "None"})
	for boardID := int16(1); boardID <= e.World.BoardCount; boardID++ {
		name := LoadString(e.World.BoardData[boardID][:SizeOfBoardName])
		if name == "" {
			name = "Untitled"
		}
		options = append(options, EditorBoardOption{ID: boardID, Name: name})
	}
	return EditorProperties{
		BoardID:           e.World.Info.CurrentBoard,
		BoardName:         e.Board.Name,
		WorldName:         e.World.Info.Name,
		MaxShots:          e.Board.Info.MaxShots,
		IsDark:            e.Board.Info.IsDark,
		NeighborBoards:    e.Board.Info.NeighborBoards,
		ReenterWhenZapped: e.Board.Info.ReenterWhenZapped,
		TimeLimitSec:      e.Board.Info.TimeLimitSec,
		Boards:            options,
	}
}

func editorString(value string, max int) string {
	if len(value) > max {
		return value[:max]
	}
	return value
}

func editorTileInspect(e *Engine, x, y int16) EditorTileInspect {
	if x < 1 {
		x = 1
	}
	if x > BOARD_WIDTH {
		x = BOARD_WIDTH
	}
	if y < 1 {
		y = 1
	}
	if y > BOARD_HEIGHT {
		y = BOARD_HEIGHT
	}

	tile := e.Board.Tiles[x][y]
	_, char := e.TileToColorAndChar(x, y)
	inspect := EditorTileInspect{
		X:         x,
		Y:         y,
		ElementID: tile.Element,
		Element:   ElementDefs[tile.Element].Name,
		Character: char,
		Color:     tile.Color,
	}
	if inspect.Element == "" {
		inspect.Element = fmt.Sprintf("Element %d", tile.Element)
	}
	if statID := e.GetStatIdAt(x, y); statID >= 0 {
		stat := e.Board.Stats[statID]
		inspect.HasStat = true
		inspect.StatID = statID
		inspect.P1 = stat.P1
		inspect.P2 = stat.P2
		inspect.P3 = stat.P3
		inspect.StepX = stat.StepX
		inspect.StepY = stat.StepY
		inspect.Cycle = stat.Cycle
		def := ElementDefs[tile.Element]
		inspect.Param1Name = def.Param1Name
		inspect.Param2Name = def.Param2Name
		inspect.ParamBulletTypeName = def.ParamBulletTypeName
		inspect.ParamBoardName = def.ParamBoardName
		inspect.ParamDirName = def.ParamDirName
		inspect.ParamTextName = def.ParamTextName
	}
	return inspect
}
