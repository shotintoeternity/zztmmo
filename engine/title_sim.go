package zztgo

import "sync"

// TitleSim animates the title board.
//
// Vanilla's title screen is GamePlayLoop running board 0 with GameStateElement
// = E_MONITOR (GAME.PAS:1610-1622), so board 0's monsters mill about behind the
// menu. The obstacle to reproducing that here was never the tick — it was
// ownership: board 0's objects `#set` flags on World.Info, and a title room cut
// from the live RoomManager would push those writes into every play room, for
// as long as any browser anywhere sat on the title screen.
//
// So the title runs on an engine of its own, built from a deep copy of the
// pristine world and never written back: it shares no Board, no World.Info, and
// no Stats with the rooms. It is a screensaver, not a room. Nothing it does can
// be observed by a player, and BoardClose is never called, so the copy stays a
// copy.
//
// It ticks only while somebody is watching (Subscribe), because an idle title
// board is pure waste on the t4g.nano this runs on.
type TitleSim struct {
	mu     sync.Mutex
	engine *Engine
	subs   map[*titleSub]struct{}
}

// titleSub is one watching browser. Dirty cells are merged into pending rather
// than pushed down a channel: a slow client must fall behind in TIME, never
// lose a cell, or its screen would keep a stale glyph forever.
type titleSub struct {
	mu      sync.Mutex
	pending map[int32]ScreenCell
	signal  chan struct{}
}

func NewTitleSim(world TWorld) *TitleSim {
	return &TitleSim{
		engine: newTitleEngine(world),
		subs:   make(map[*titleSub]struct{}),
	}
}

// cloneWorld deep-copies the board bytes. TWorld's BoardData is an array of
// slices, so plain assignment would leave the title engine aliasing the rooms'
// board images.
func cloneWorld(world TWorld) TWorld {
	clone := world
	for i := range world.BoardData {
		if world.BoardData[i] != nil {
			clone.BoardData[i] = append([]byte(nil), world.BoardData[i]...)
		}
	}
	return clone
}

// newTitleEngine is TitleScreenCells' setup, kept alive to be ticked.
func newTitleEngine(world TWorld) *Engine {
	e := NewEngine()
	e.Headless = true
	e.MultiRoom = true
	e.SetInputSource(&ScriptedInput{})
	e.World = cloneWorld(world)
	e.GameStateElement = E_MONITOR
	e.BoardOpen(0)
	e.GenerateTransitionTable()
	e.TransitionDrawToBoard()

	stat := e.Board.Stats[0]
	e.Board.Tiles[stat.X][stat.Y].Element = E_MONITOR
	e.Board.Tiles[stat.X][stat.Y].Color = ElementDefs[E_MONITOR].Color
	e.BoardDrawTile(int16(stat.X), int16(stat.Y))

	// The opening paint is delivered as a full Screen() snapshot, not as a diff.
	e.DrainScreenDirty()
	return e
}

// Screen is the whole title board, for a client that has just arrived.
func (t *TitleSim) Screen() []ScreenCell {
	t.mu.Lock()
	defer t.mu.Unlock()
	return screenCells(t.engine)
}

// Subscribe registers a watcher and returns its signal channel plus a cancel.
// Cancel is idempotent and must run before the caller drops the subscription.
func (t *TitleSim) Subscribe() (*titleSub, func()) {
	sub := &titleSub{
		pending: make(map[int32]ScreenCell),
		signal:  make(chan struct{}, 1),
	}
	t.mu.Lock()
	t.subs[sub] = struct{}{}
	t.mu.Unlock()

	var once sync.Once
	return sub, func() {
		once.Do(func() {
			t.mu.Lock()
			delete(t.subs, sub)
			t.mu.Unlock()
		})
	}
}

// Signal fires whenever cells are waiting.
func (s *titleSub) Signal() <-chan struct{} { return s.signal }

// Drain takes the cells accumulated since the last call.
func (s *titleSub) Drain() []ScreenCell {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return nil
	}
	cells := make([]ScreenCell, 0, len(s.pending))
	for _, cell := range s.pending {
		cells = append(cells, cell)
	}
	s.pending = make(map[int32]ScreenCell)
	return cells
}

func (s *titleSub) merge(cells []ScreenCell) {
	s.mu.Lock()
	for _, cell := range cells {
		s.pending[int32(cell.X)*25+int32(cell.Y)] = cell
	}
	s.mu.Unlock()

	select {
	case s.signal <- struct{}{}:
	default: // already signalled; Drain will pick these up too
	}
}

// Tick advances the title board one engine tick and fans the changed cells out
// to every watcher. It is a no-op when nobody is watching.
func (t *TitleSim) Tick() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.subs) == 0 {
		return
	}

	// ElementMonitorTick sets GamePlayExitRequested when it sees a menu key in
	// the *package-global* InputKeyPressed (elements.go:1498). Nothing feeds
	// that global here, but a latched true would stall the sim forever, so it
	// is cleared every tick rather than trusted.
	t.engine.GamePlayExitRequested = false
	t.engine.GameStep(nil)
	// Objects on board 0 may `#play`. Nobody is listening; without this the
	// event slice grows without bound.
	t.engine.DrainEvents()

	dirty := t.engine.DrainScreenDirty()
	if len(dirty) == 0 {
		return
	}
	for sub := range t.subs {
		sub.merge(dirty)
	}
}
