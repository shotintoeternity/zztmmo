package zztgo

import (
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
		snapshot = EditorSnapshotMessage{
			Type:    MessageTypeEditorSnapshot,
			BoardID: e.World.Info.CurrentBoard,
			Screen:  screenCells(e),
			Inspect: editorTileInspect(e, x, y),
		}
		// The full frame supersedes all setup writes. Later edits can therefore
		// return just their dirty cells.
		e.DrainScreenDirty()
	})
	return snapshot, err
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
	}
	return inspect
}
