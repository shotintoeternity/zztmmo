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

	WorldName  string
	engine     *Engine
	Members    map[*webSocketClient]struct{}
	memberInfo map[*webSocketClient]EditorPresence
	readOnly   map[*webSocketClient]bool
	nextMember int
	leases     map[editorLeaseKey]*webSocketClient
	// memberBoard is each member's own current board (M17.12). The session has
	// one shared engine, so `engine.World.Info.CurrentBoard` is whichever board
	// was last operated on — it is engine bookkeeping, not any member's view.
	// Apply switches the engine onto the acting member's board before running
	// their operation, which is why every vanilla-derived path can keep reading
	// the implicit current board unchanged.
	memberBoard map[*webSocketClient]int16
}

type editorLeaseKey struct {
	kind    string
	boardID int16
	statID  int16
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
		WorldName:   worldName,
		engine:      e,
		Members:     make(map[*webSocketClient]struct{}),
		memberInfo:  make(map[*webSocketClient]EditorPresence),
		readOnly:    make(map[*webSocketClient]bool),
		leases:      make(map[editorLeaseKey]*webSocketClient),
		memberBoard: make(map[*webSocketClient]int16),
	}
}

func (s *EditorSession) Enter(member *webSocketClient) error {
	_, err := s.EnterNamed(member, "")
	return err
}

func (s *EditorSession) EnterNamed(member *webSocketClient, name string) (EditorPresence, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Members[member]; ok {
		return s.memberInfo[member], nil
	}
	s.nextMember++
	if name == "" {
		name = fmt.Sprintf("Player %d", s.nextMember)
	}
	boardID := int16(0)
	if s.engine != nil {
		boardID = s.engine.World.Info.CurrentBoard
	}
	presence := EditorPresence{
		ID:      fmt.Sprintf("editor-%d", s.nextMember),
		Name:    name,
		Color:   editorPresenceColor(s.nextMember),
		BoardID: boardID,
		X:       BOARD_WIDTH / 2,
		Y:       BOARD_HEIGHT / 2,
	}
	s.Members[member] = struct{}{}
	s.memberInfo[member] = presence
	s.readOnly[member] = false
	s.memberBoard[member] = boardID
	return presence, nil
}

func (s *EditorSession) Exit(member *webSocketClient) {
	s.mu.Lock()
	delete(s.Members, member)
	delete(s.memberInfo, member)
	delete(s.memberBoard, member)
	delete(s.readOnly, member)
	for key, holder := range s.leases {
		if holder == member {
			delete(s.leases, key)
		}
	}
	s.mu.Unlock()
}

func (s *EditorSession) MemberID(member *webSocketClient) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.memberInfo[member].ID
}

func (s *EditorSession) SetMemberReadOnly(member *webSocketClient, readOnly bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Members[member]; !ok {
		return
	}
	s.readOnly[member] = readOnly
	if readOnly {
		for key, holder := range s.leases {
			if holder == member {
				delete(s.leases, key)
			}
		}
	}
}

func (s *EditorSession) SetAccountReadOnly(accountID string, readOnly bool) {
	if accountID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for member := range s.Members {
		if member.accountID == accountID {
			s.readOnly[member] = readOnly
		}
	}
}

func (s *EditorSession) CanEdit(member *webSocketClient) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.canEditLocked(member)
}

func (s *EditorSession) Name() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.WorldName
}

func (s *EditorSession) SetWorldName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.WorldName = name
}

func (s *EditorSession) canEditLocked(member *webSocketClient) bool {
	if _, ok := s.Members[member]; !ok {
		return false
	}
	return !s.readOnly[member]
}

func (s *EditorSession) UpdatePresence(member *webSocketClient, x, y int16) {
	s.mu.Lock()
	defer s.mu.Unlock()
	presence, ok := s.memberInfo[member]
	if !ok {
		return
	}
	presence.X, presence.Y = editorClamp(x, y)
	s.memberInfo[member] = presence
}

func (s *EditorSession) Presence() []EditorPresence {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]EditorPresence, 0, len(s.memberInfo))
	for _, presence := range s.memberInfo {
		out = append(out, presence)
	}
	return out
}

// MemberCount reports how many people are editing this world (M17.11). Members
// is mutex-guarded, so callers outside the session must come through here rather
// than reading the map directly.
func (s *EditorSession) MemberCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.Members)
}

func (s *EditorSession) MemberClients() []*webSocketClient {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*webSocketClient, 0, len(s.Members))
	for member := range s.Members {
		out = append(out, member)
	}
	return out
}

// editorPresenceColor picks a collaborator's cursor colour. M17.9: these are
// foreground-only attributes (high nibble 0 = black background) so a remote
// cursor renders exactly like the local one — the cross glyph over the board
// tile — differing only in hue. The previous palette used background-filled
// attributes (0x1e = blue background), which painted the whole cell as a solid
// block and hid the tile underneath.
//
// 0x0F is deliberately absent: it is EDITOR_CURSOR_COLOR, the local player's own
// cursor colour, and the old palette's eighth entry made the eighth
// collaborator indistinguishable from yourself.
func editorPresenceColor(n int) byte {
	colors := []byte{0x0e, 0x0b, 0x0a, 0x0d, 0x0c, 0x09, 0x06, 0x05}
	return colors[(n-1)%len(colors)]
}

func (s *EditorSession) AcquireLease(member *webSocketClient, request EditorLeaseMessage) (EditorLeaseMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Members[member]; !ok {
		return EditorLeaseMessage{}, fmt.Errorf("editor session membership required")
	}
	key, ok := s.leaseKeyLocked(request)
	if !ok {
		return EditorLeaseMessage{}, nil
	}
	reply := EditorLeaseMessage{
		Type:    MessageTypeEditorLease,
		Op:      "granted",
		Kind:    key.kind,
		BoardID: key.boardID,
		StatID:  key.statID,
	}
	if !s.canEditLocked(member) {
		reply.Op = "refused"
		reply.Error = "read-only"
		return reply, nil
	}
	if holder, ok := s.leases[key]; ok && holder != member {
		holderInfo := s.memberInfo[holder]
		reply.Op = "refused"
		reply.HolderID = holderInfo.ID
		reply.HolderName = holderInfo.Name
		return reply, nil
	}
	s.leases[key] = member
	return reply, nil
}

func (s *EditorSession) ReleaseLease(member *webSocketClient, request EditorLeaseMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.leaseKeyLocked(request)
	if !ok {
		return
	}
	if s.leases[key] == member {
		delete(s.leases, key)
	}
}

func (s *EditorSession) leaseKeyLocked(request EditorLeaseMessage) (editorLeaseKey, bool) {
	boardID := s.engine.World.Info.CurrentBoard
	if request.BoardID >= 0 && request.BoardID <= s.engine.World.BoardCount {
		boardID = request.BoardID
	}
	switch request.Kind {
	case "board":
		return editorLeaseKey{kind: "board", boardID: boardID}, true
	case "stat":
		if boardID != s.engine.World.Info.CurrentBoard || request.StatID < 0 || request.StatID > s.engine.Board.StatCount {
			return editorLeaseKey{}, false
		}
		return editorLeaseKey{kind: "stat", boardID: boardID, statID: request.StatID}, true
	default:
		return editorLeaseKey{}, false
	}
}

func (s *EditorSession) hasCurrentBoardLeaseLocked(member *webSocketClient, e *Engine) bool {
	if !s.canEditLocked(member) {
		return false
	}
	if len(s.Members) <= 1 {
		return true
	}
	key := editorLeaseKey{kind: "board", boardID: e.World.Info.CurrentBoard}
	return s.leases[key] == member
}

func (s *EditorSession) hasCurrentStatLeaseLocked(member *webSocketClient, e *Engine, statID int16) bool {
	if !s.canEditLocked(member) {
		return false
	}
	if len(s.Members) <= 1 {
		return true
	}
	key := editorLeaseKey{kind: "stat", boardID: e.World.Info.CurrentBoard, statID: statID}
	return s.leases[key] == member
}

// Apply is the sole serialized session boundary. M5.0 is read-only, but later
// editor tasks must make every world mutation inside this callback.
func (s *EditorSession) Apply(member *webSocketClient, fn func(*Engine)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Members[member]; !ok {
		return fmt.Errorf("editor session membership required")
	}
	s.focusMemberBoardLocked(member)
	fn(s.engine)
	// The operation itself may have moved the engine to another board — a board
	// add, or an edge/passage transition during test-play — so the member
	// follows it. Without this, the next Apply would drag them back to the board
	// they were on before, undoing an engine-driven move.
	if s.engine != nil {
		s.setMemberBoardLocked(member, s.engine.World.Info.CurrentBoard)
	}
	return nil
}

// focusMemberBoardLocked points the shared engine at the acting member's own
// board before their operation runs (M17.12). Members edit different boards of
// one world, but the session holds a single engine whose converted-from-Pascal
// paths all read the implicit current board (Edit/Apply, leaseKeyLocked,
// hasCurrentBoardLeaseLocked, editorSnapshot, and the sidebar's pattern/copied
// tile globals). Rather than thread an explicit board through all of that, put
// the engine on the right board first, so each of those paths stays unchanged
// and faithful.
//
// The switch is lossless and is what vanilla already does on a board change:
// BoardChange closes the open board (RLE-serialising it back into BoardData)
// and opens the target. Apply holds s.mu, so no other member can observe or
// interleave with the intermediate state.
//
// Callers must hold s.mu.
func (s *EditorSession) focusMemberBoardLocked(member *webSocketClient) {
	e := s.engine
	if e == nil {
		return
	}
	boardID, ok := s.memberBoard[member]
	if !ok {
		// A member with no recorded board adopts whatever is open; this is the
		// pre-M17.12 behaviour and covers members who joined before their first
		// board switch.
		s.memberBoard[member] = e.World.Info.CurrentBoard
		return
	}
	if boardID < 0 || boardID > e.World.BoardCount {
		boardID = 0
		s.memberBoard[member] = boardID
	}
	if boardID == e.World.Info.CurrentBoard {
		return
	}
	e.BoardChange(boardID)
	e.TransitionDrawToBoard()
}

func (s *EditorSession) Snapshot(member *webSocketClient, x, y int16) (EditorSnapshotMessage, error) {
	var snapshot EditorSnapshotMessage
	err := s.Apply(member, func(e *Engine) {
		snapshot = editorSnapshot(e, x, y)
	})
	snapshot.MemberID = s.MemberID(member)
	snapshot.ReadOnly = !s.CanEdit(member)
	snapshot.Presence = s.Presence()
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
	memberID := s.MemberID(member)
	err := s.Apply(member, func(e *Engine) {
		if !s.canEditLocked(member) {
			return
		}
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
		case "element":
			if editorPlaceElement(e, x, y, edit.Element, edit.Color) {
				e.BoardClose()
			}
		case "text":
			if editorPlaceText(e, x, y, edit.Char, edit.Color) {
				e.BoardClose()
			}
		}
		reply = EditorDiffMessage{
			Type:     MessageTypeEditorDiff,
			MemberID: memberID,
			Cells:    e.DrainScreenDirty(),
			Inspect:  editorTileInspect(e, x, y),
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

// editorPlaceElement places an F1/F2/F3 category-menu element (M5.8), porting
// the placement half of EditorLoop's element switch (EDITOR.PAS:731-772). Unlike
// editorPlaceTile (pattern brush only), this accepts any category element and
// adds a stat when the element needs one, seeding its parameters from
// EditorStatSettings exactly as the Pascal does after AddStat. cursorColor is
// the client's current brush colour byte; the element's own colour rule decides
// whether it is honoured. Returns whether the board changed.
func editorPlaceElement(e *Engine, x, y int16, element, cursorColor byte) bool {
	if int16(element) > MAX_ELEMENT {
		return false
	}
	// E_PLAYER is category-less: placing it moves the single player stat, never
	// adds a second one (EDITOR.PAS:731-734).
	if element == E_PLAYER {
		if !e.BoardPrepareTileForPlacement(x, y) {
			return false
		}
		e.MoveStat(0, x, y)
		editorDrawTileAndNeighbors(e, x, y)
		return true
	}
	def := ElementDefs[element]
	if def.EditorCategory != CATEGORY_ITEM && def.EditorCategory != CATEGORY_CREATURE && def.EditorCategory != CATEGORY_TERRAIN {
		return false
	}
	color := editorResolveElementColor(element, cursorColor)
	if def.Cycle == -1 {
		// No stat: a plain tile, coloured and copied (EDITOR.PAS:746-749).
		if !e.BoardPrepareTileForPlacement(x, y) {
			return false
		}
		e.Board.Tiles[x][y].Element = element
		e.Board.Tiles[x][y].Color = byte(color)
		editorDrawTileAndNeighbors(e, x, y)
		return true
	}
	// Stat-backed: guard MAX_STAT (EditorPrepareModifyStatAtCursor), prepare the
	// tile, then AddStat and seed defaults (EDITOR.PAS:751-766).
	if e.Board.StatCount >= MAX_STAT || !e.BoardPrepareTileForPlacement(x, y) {
		return false
	}
	e.AddStat(x, y, element, color, def.Cycle, StatTemplateDefault)
	stat := &e.Board.Stats[e.Board.StatCount]
	if def.Param1Name != "" {
		stat.P1 = e.World.EditorStatSettings[element].P1
	}
	if def.Param2Name != "" {
		stat.P2 = e.World.EditorStatSettings[element].P2
	}
	if def.ParamDirName != "" {
		stat.StepX = e.World.EditorStatSettings[element].StepX
		stat.StepY = e.World.EditorStatSettings[element].StepY
	}
	if def.ParamBoardName != "" {
		stat.P3 = e.World.EditorStatSettings[element].P3
	}
	editorDrawTileAndNeighbors(e, x, y)
	return true
}

// editorPlaceText places one F4 text-entry character (M5.8), porting the text
// branch of EditorLoop (EDITOR.PAS:459-467, editor.go:552-560): the tile's
// element is the text-colour variant chosen by the cursor foreground colour and
// its Color byte carries the typed character. cursorColor is the editor's fg
// colour (guaranteed 9..15 by the C selector, clamped here in case the client
// sends otherwise); char is a printable ASCII byte, exactly the range vanilla
// accepts (>= ' ' and < 0x80). Returns whether it drew.
func editorPlaceText(e *Engine, x, y int16, char, cursorColor byte) bool {
	if char < ' ' || char >= 0x80 {
		return false
	}
	fg := int16(cursorColor & 0x0f)
	if fg < 9 {
		fg = 9
	} else if fg > 15 {
		fg = 15
	}
	if !e.BoardPrepareTileForPlacement(x, y) {
		return false
	}
	e.Board.Tiles[x][y].Element = byte(fg - 9 + E_TEXT_MIN)
	e.Board.Tiles[x][y].Color = char
	editorDrawTileAndNeighbors(e, x, y)
	return true
}

// editorResolveElementColor ports the placement colour rule (EDITOR.PAS:853-857):
// the CHOICE sentinels blend the element with the cursor colour; any other value
// is the element's fixed colour.
func editorResolveElementColor(element, cursorColor byte) int16 {
	switch ElementDefs[element].Color {
	case COLOR_CHOICE_ON_BLACK:
		return int16(cursorColor)
	case COLOR_WHITE_ON_CHOICE:
		return int16(cursorColor)*0x10 - 0x71
	case COLOR_CHOICE_ON_CHOICE:
		return (int16(cursorColor)-8)*0x11 + 8
	default:
		return int16(ElementDefs[element].Color)
	}
}

// editorElementMenus builds the three F1/F2/F3 category tables from ElementDefs,
// mirroring EditorLoop's listing loop (EDITOR.PAS:702-726): every element in the
// category, in element order, with its EditorShortcut key, glyph, and the
// section header (CategoryName) that precedes it. ElementDefs is immutable after
// init, so the menus are identical across sessions and ride the entry snapshot.
func editorElementMenus() []EditorElementMenu {
	cats := []struct {
		cat   int16
		key   string
		title string
	}{
		{CATEGORY_ITEM, "f1", "Item"},
		{CATEGORY_CREATURE, "f2", "Creature"},
		{CATEGORY_TERRAIN, "f3", "Terrain"},
	}
	menus := make([]EditorElementMenu, 0, len(cats))
	for _, c := range cats {
		menu := EditorElementMenu{Category: c.cat, Key: c.key, Title: c.title}
		for el := int16(0); el <= MAX_ELEMENT; el++ {
			def := ElementDefs[el]
			if def.EditorCategory != c.cat {
				continue
			}
			color := def.Color
			// The CHOICE sentinels are not renderable swatch colours; show a
			// neutral white glyph in the menu (the real colour is resolved at
			// placement from the cursor colour).
			if color == COLOR_CHOICE_ON_BLACK || color == COLOR_WHITE_ON_CHOICE || color == COLOR_CHOICE_ON_CHOICE {
				color = 0x0F
			}
			item := EditorElementItem{
				ElementID:    byte(el),
				Name:         def.Name,
				Character:    def.Character,
				Color:        color,
				CategoryName: def.CategoryName,
			}
			if def.EditorShortcut != 0 {
				item.Shortcut = string([]byte{UpCase(def.EditorShortcut)})
			}
			menu.Items = append(menu.Items, item)
		}
		menus = append(menus, menu)
	}
	return menus
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
		if !s.hasCurrentBoardLeaseLocked(member, e) {
			return
		}
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
		if !s.hasCurrentStatLeaseLocked(member, e, edit.StatID) {
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
		if !s.hasCurrentStatLeaseLocked(member, e, statId) {
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
		if !s.hasCurrentStatLeaseLocked(member, e, statId) {
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
		if !s.canEditLocked(member) {
			return
		}
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
		// M17.12: the switch belongs to this member, not the session. Record it
		// so their later operations refocus here, and so other members stay on
		// their own boards instead of being dragged along.
		if boardId >= 0 && boardId <= e.World.BoardCount {
			s.setMemberBoardLocked(member, boardId)
		}
		reply = editorSnapshot(e, BOARD_WIDTH/2, BOARD_HEIGHT/2)
	})
	return reply, err
}

// setMemberBoardLocked records a member's current board and keeps their
// broadcast presence in step, so collaborators can tell which board a cursor is
// on. Callers must hold s.mu (Apply does).
func (s *EditorSession) setMemberBoardLocked(member *webSocketClient, boardID int16) {
	s.memberBoard[member] = boardID
	if presence, ok := s.memberInfo[member]; ok {
		presence.BoardID = boardID
		s.memberInfo[member] = presence
	}
}

// ClearBoard empties the current board, mirroring EditorLoop's 'Z' branch
// (EDITOR.PAS:591-598, editor.go:645-654): every stat above the player is
// removed, then BoardCreate resets the board to an empty bordered room with the
// player at centre. BoardClose persists the cleared board into BoardData so a
// later board switch does not resurrect the old contents, matching how every
// other session edit closes the board. Replies a full snapshot: a cleared board
// repaints the whole frame, exactly as EditorDrawRefresh does after 'Z'.
func (s *EditorSession) ClearBoard(member *webSocketClient) (EditorSnapshotMessage, error) {
	var reply EditorSnapshotMessage
	err := s.Apply(member, func(e *Engine) {
		if !s.hasCurrentBoardLeaseLocked(member, e) {
			return
		}
		for i := e.Board.StatCount; i >= 1; i-- {
			e.RemoveStat(i)
		}
		e.BoardCreate()
		e.BoardClose()
		e.TransitionDrawToBoard()
		reply = editorSnapshot(e, BOARD_WIDTH/2, BOARD_HEIGHT/2)
	})
	return reply, err
}

// NewWorld resets the whole session to a fresh one-board world, mirroring
// EditorLoop's 'N' branch (EDITOR.PAS:600-609, editor.go:655-665): WorldCreate
// clears World.Info and builds an empty board 0. BoardClose serializes that board
// into BoardData so the session's BoardOpen/BoardClose invariant holds, then
// BoardOpen reopens it. The regenerated transition table keeps board-change fades
// deterministic. Shared world flags reset with the rest of World.Info, which is
// correct: a new world starts with no puzzle progress. Replies a full snapshot.
func (s *EditorSession) NewWorld(member *webSocketClient) (EditorSnapshotMessage, error) {
	var reply EditorSnapshotMessage
	err := s.Apply(member, func(e *Engine) {
		if !s.hasCurrentBoardLeaseLocked(member, e) {
			return
		}
		e.WorldCreate()
		e.BoardClose()
		e.BoardOpen(e.World.Info.CurrentBoard)
		e.GenerateTransitionTable()
		e.TransitionDrawToBoard()
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
		if !s.hasCurrentBoardLeaseLocked(member, e) {
			return
		}
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
		if !s.canEditLocked(member) {
			return
		}
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
