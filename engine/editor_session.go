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
	})
	return snapshot, err
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
	inspect := EditorTileInspect{
		X:       x,
		Y:       y,
		Element: ElementDefs[tile.Element].Name,
		Color:   tile.Color,
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
