package zztgo

import "testing"

// TestEditorElementMenusExposeCategoriesAndShortcuts asserts the F1/F2/F3
// element category tables (M5.8) are built from ElementDefs the way EditorLoop
// lists them (EDITOR.PAS:702-726): three categories, every member in its
// category, each with its EditorShortcut and a section header on the first of a
// run. Without these menus the browser could only place the five patterns.
func TestEditorElementMenusExposeCategoriesAndShortcuts(t *testing.T) {
	InitElementDefs()
	menus := editorElementMenus()
	if len(menus) != 3 {
		t.Fatalf("menus=%d, want 3", len(menus))
	}
	want := []struct {
		key, title string
		cat        int16
	}{
		{"f1", "Item", CATEGORY_ITEM},
		{"f2", "Creature", CATEGORY_CREATURE},
		{"f3", "Terrain", CATEGORY_TERRAIN},
	}
	find := func(m EditorElementMenu, id byte) (EditorElementItem, bool) {
		for _, it := range m.Items {
			if it.ElementID == id {
				return it, true
			}
		}
		return EditorElementItem{}, false
	}
	for i, w := range want {
		m := menus[i]
		if m.Key != w.key || m.Title != w.title || m.Category != w.cat {
			t.Fatalf("menu %d = %+v, want key=%s title=%s cat=%d", i, m, w.key, w.title, w.cat)
		}
		if len(m.Items) == 0 {
			t.Fatalf("menu %s has no items", m.Key)
		}
		hasHeader := false
		for _, it := range m.Items {
			if ElementDefs[it.ElementID].EditorCategory != w.cat {
				t.Fatalf("menu %s carries item %d from category %d", m.Key, it.ElementID, ElementDefs[it.ElementID].EditorCategory)
			}
			if it.CategoryName != "" {
				hasHeader = true
			}
		}
		if !hasHeader {
			t.Fatalf("menu %s has no section header", m.Key)
		}
	}
	// Known members reachable by their original keystroke.
	if it, ok := find(menus[0], E_GEM); !ok || it.Shortcut != "G" {
		t.Fatalf("Item menu gem=%+v ok=%v, want shortcut G", it, ok)
	}
	if it, ok := find(menus[1], E_LION); !ok || it.Shortcut != "L" {
		t.Fatalf("Creature menu lion=%+v ok=%v, want shortcut L", it, ok)
	}
	if it, ok := find(menus[2], E_FOREST); !ok || it.Shortcut != "F" {
		t.Fatalf("Terrain menu forest=%+v ok=%v, want shortcut F", it, ok)
	}
}

// TestEditorSessionPlaceMenuElement drives the "element" edit op (M5.8): a menu
// element with no cycle sets a coloured tile and adds no stat, while a
// stat-backed creature adds exactly one stat seeded with the element's cycle,
// porting the placement half of EditorLoop's switch (EDITOR.PAS:746-766).
func TestEditorSessionPlaceMenuElement(t *testing.T) {
	InitElementDefs()
	session := NewEditorSession("TEST", testEmptyWorld(t))
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(member)

	place := func(element, color byte, x, y int16) {
		t.Helper()
		diff, err := session.Edit(member, EditorEditMessage{
			Type: MessageTypeEditorEdit, Op: "element", X: x, Y: y, Element: element, Color: color,
		})
		if err != nil {
			t.Fatalf("Edit element %d: %v", element, err)
		}
		if diff.Type != MessageTypeEditorDiff || len(diff.Cells) == 0 {
			t.Fatalf("place %d diff=%+v, want dirty cells", element, diff)
		}
	}

	// Non-stat item: a gem sets the tile at the resolved colour, no stat.
	place(E_GEM, 0x0e, 10, 10)
	if err := session.Apply(member, func(e *Engine) {
		wantColor := byte(editorResolveElementColor(E_GEM, 0x0e))
		if tile := e.Board.Tiles[10][10]; tile != (TTile{Element: E_GEM, Color: wantColor}) {
			t.Fatalf("gem tile=%+v, want gem colour %#x", tile, wantColor)
		}
		if e.GetStatIdAt(10, 10) != -1 {
			t.Fatalf("gem placement added a stat")
		}
	}); err != nil {
		t.Fatal(err)
	}

	// Stat-backed creature: a lion adds one stat carrying the element cycle.
	var before int16
	if err := session.Apply(member, func(e *Engine) { before = e.Board.StatCount }); err != nil {
		t.Fatal(err)
	}
	place(E_LION, 0x0e, 12, 12)
	if err := session.Apply(member, func(e *Engine) {
		if tile := e.Board.Tiles[12][12]; tile.Element != E_LION {
			t.Fatalf("lion tile=%+v", tile)
		}
		id := e.GetStatIdAt(12, 12)
		if id < 0 {
			t.Fatal("lion placement added no stat")
		}
		if e.Board.StatCount != before+1 {
			t.Fatalf("statCount=%d, want %d", e.Board.StatCount, before+1)
		}
		if e.Board.Stats[id].Cycle != ElementDefs[E_LION].Cycle {
			t.Fatalf("lion cycle=%d, want %d", e.Board.Stats[id].Cycle, ElementDefs[E_LION].Cycle)
		}
	}); err != nil {
		t.Fatal(err)
	}
}

// TestEditorSessionPlaceMenuPlayerMovesSingleStat asserts placing E_PLAYER moves
// the one player stat rather than adding a second (EDITOR.PAS:731-734): the
// board holds a single player, and a second stat-0 would break tick dispatch.
func TestEditorSessionPlaceMenuPlayerMovesSingleStat(t *testing.T) {
	InitElementDefs()
	session := NewEditorSession("TEST", testEmptyWorld(t))
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(member)

	// testEmptyWorld strips every stat (StatCount -1); seed a player at stat 0 so
	// MoveStat(0, ...) has something to move.
	if err := session.Apply(member, func(e *Engine) {
		e.AddStat(1, 1, E_PLAYER, 0x1f, 0, StatTemplateDefault)
		e.Board.Tiles[1][1] = TTile{Element: E_PLAYER, Color: 0x1f}
		e.BoardClose()
		e.DrainScreenDirty()
	}); err != nil {
		t.Fatal(err)
	}
	var before int16
	if err := session.Apply(member, func(e *Engine) { before = e.Board.StatCount }); err != nil {
		t.Fatal(err)
	}

	if _, err := session.Edit(member, EditorEditMessage{
		Type: MessageTypeEditorEdit, Op: "element", X: 5, Y: 5, Element: E_PLAYER, Color: 0x0e,
	}); err != nil {
		t.Fatal(err)
	}

	if err := session.Apply(member, func(e *Engine) {
		if e.Board.StatCount != before {
			t.Fatalf("player placement added a stat: %d -> %d", before, e.Board.StatCount)
		}
		if id := e.GetStatIdAt(5, 5); id != 0 {
			t.Fatalf("player stat at 5,5 = %d, want 0", id)
		}
		if tile := e.Board.Tiles[5][5]; tile.Element != E_PLAYER {
			t.Fatalf("player tile at 5,5 = %+v", tile)
		}
		if e.Board.Tiles[1][1].Element == E_PLAYER {
			t.Fatal("player left behind at its old square")
		}
	}); err != nil {
		t.Fatal(err)
	}
}

// TestEditorSessionPlaceTextTile drives the F4 text-entry op (M5.8): a printable
// character becomes a text tile whose element is the cursor-colour variant and
// whose Color byte carries the typed character, porting EditorLoop's text branch
// (EDITOR.PAS:459-467). A non-printable byte draws nothing.
func TestEditorSessionPlaceTextTile(t *testing.T) {
	InitElementDefs()
	session := NewEditorSession("TEST", testEmptyWorld(t))
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(member)

	diff, err := session.Edit(member, EditorEditMessage{
		Type: MessageTypeEditorEdit, Op: "text", X: 8, Y: 8, Char: 'A', Color: 0x0e,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Cells) == 0 {
		t.Fatalf("text placement produced no dirty cells")
	}
	if err := session.Apply(member, func(e *Engine) {
		tile := e.Board.Tiles[8][8]
		wantElem := byte((0x0e&0x0f)-9) + E_TEXT_MIN
		if tile.Element != wantElem {
			t.Fatalf("text element=%d, want %d", tile.Element, wantElem)
		}
		if tile.Color != 'A' {
			t.Fatalf("text tile Color=%#x, want the character 'A'", tile.Color)
		}
	}); err != nil {
		t.Fatal(err)
	}

	// A non-printable byte is refused with no draw.
	before := int16(0)
	if err := session.Apply(member, func(e *Engine) { before = int16(len(e.DrainScreenDirty())) }); err != nil {
		t.Fatal(err)
	}
	_ = before
	diff, err = session.Edit(member, EditorEditMessage{
		Type: MessageTypeEditorEdit, Op: "text", X: 9, Y: 8, Char: 0x03, Color: 0x0e,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Cells) != 0 {
		t.Fatalf("non-printable text byte drew %d cells, want 0", len(diff.Cells))
	}
}

// TestEditorSessionClearBoard asserts 'Z' empties the board (EDITOR.PAS:591):
// every non-player stat is removed and the interior returns to empty with the
// player recentred.
func TestEditorSessionClearBoard(t *testing.T) {
	InitElementDefs()
	session := NewEditorSession("TEST", testMultiplayerSmokeWorld(t))
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(member)

	// Place a creature so the board has a stat above the player to clear.
	if _, err := session.Edit(member, EditorEditMessage{
		Type: MessageTypeEditorEdit, Op: "element", X: 10, Y: 10, Element: E_LION, Color: 0x0e,
	}); err != nil {
		t.Fatal(err)
	}
	var hadStats bool
	if err := session.Apply(member, func(e *Engine) { hadStats = e.Board.StatCount >= 1 }); err != nil {
		t.Fatal(err)
	}
	if !hadStats {
		t.Fatal("setup failed: board has no stats to clear")
	}

	snapshot, err := session.ClearBoard(member)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Type != MessageTypeEditorSnapshot || len(snapshot.Screen) == 0 {
		t.Fatalf("ClearBoard reply=%+v, want a full snapshot", snapshot.Type)
	}
	if err := session.Apply(member, func(e *Engine) {
		if e.Board.StatCount != 0 {
			t.Fatalf("cleared board StatCount=%d, want 0 (player only)", e.Board.StatCount)
		}
		if tile := e.Board.Tiles[10][10]; tile.Element != E_EMPTY {
			t.Fatalf("cleared tile 10,10=%+v, want empty", tile)
		}
		if id := e.GetStatIdAt(BOARD_WIDTH/2, BOARD_HEIGHT/2); id != 0 {
			t.Fatalf("player stat after clear=%d at centre, want 0", id)
		}
	}); err != nil {
		t.Fatal(err)
	}
}

// TestEditorSessionNewWorld asserts 'N' resets the session to a fresh one-board
// world (EDITOR.PAS:600): the board count and current board return to zero and
// the board is an empty room with the single player.
func TestEditorSessionNewWorld(t *testing.T) {
	InitElementDefs()
	session := NewEditorSession("TEST", testMultiplayerSmokeWorld(t))
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(member)

	snapshot, err := session.NewWorld(member)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Screen) == 0 {
		t.Fatalf("NewWorld reply has no screen")
	}
	if err := session.Apply(member, func(e *Engine) {
		if e.World.BoardCount != 0 {
			t.Fatalf("new world BoardCount=%d, want 0", e.World.BoardCount)
		}
		if e.World.Info.CurrentBoard != 0 {
			t.Fatalf("new world CurrentBoard=%d, want 0", e.World.Info.CurrentBoard)
		}
		if e.Board.StatCount != 0 {
			t.Fatalf("new world StatCount=%d, want 0", e.Board.StatCount)
		}
		if id := e.GetStatIdAt(BOARD_WIDTH/2, BOARD_HEIGHT/2); id != 0 {
			t.Fatalf("new world player stat=%d at centre, want 0", id)
		}
	}); err != nil {
		t.Fatal(err)
	}
}
