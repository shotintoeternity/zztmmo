package zztgo

import (
	"bytes"
	"testing"
)

func TestEditorSessionReadOnlySnapshotAndInspect(t *testing.T) {
	world := testMultiplayerSmokeWorld(t)
	world.Info.CurrentBoard = 1
	sourceBoard := append([]byte(nil), world.BoardData[1]...)
	session := NewEditorSession("TEST", world)
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatalf("Enter: %v", err)
	}
	defer session.Exit(member)

	snapshot, err := session.Snapshot(member, 12, 12)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.Type != MessageTypeEditorSnapshot || snapshot.BoardID != 1 {
		t.Fatalf("bad editor snapshot: %+v", snapshot)
	}
	if len(snapshot.Screen) != BOARD_WIDTH*BOARD_HEIGHT {
		t.Fatalf("snapshot cells=%d, want %d", len(snapshot.Screen), BOARD_WIDTH*BOARD_HEIGHT)
	}
	if snapshot.Inspect.X != 12 || snapshot.Inspect.Y != 12 || snapshot.Inspect.Element != "Passage" {
		t.Fatalf("inspect=%+v, want passage at 12,12", snapshot.Inspect)
	}
	if !snapshot.Inspect.HasStat || snapshot.Inspect.P3 != 2 {
		t.Fatalf("passage stat inspect=%+v, want P3=2", snapshot.Inspect)
	}

	// A later edit will execute through Apply. Mutating the session copy here
	// proves that even that path cannot touch the source world or any live room.
	if err := session.Apply(member, func(e *Engine) {
		e.Board.Tiles[12][12] = TTile{Element: E_EMPTY}
		e.BoardClose()
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !bytes.Equal(world.BoardData[1], sourceBoard) {
		t.Fatal("editor session mutated its source world's serialized board")
	}
}

func TestEditorSessionCapsMembersAndRequiresMembership(t *testing.T) {
	session := NewEditorSession("TEST", testEmptyWorld(t))
	member := &webSocketClient{}
	other := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatalf("first Enter: %v", err)
	}
	defer session.Exit(member)
	if err := session.Enter(other); err == nil {
		t.Fatal("second editor member entered despite M5.0's one-member cap")
	}
	if _, err := session.Inspect(other, 1, 1); err == nil {
		t.Fatal("non-member inspected editor session")
	}
}

func TestEditorSessionEditsPlaceEraseFillAndRoundTrip(t *testing.T) {
	session := NewEditorSession("TEST", testEmptyWorld(t))
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatalf("Enter: %v", err)
	}
	defer session.Exit(member)

	place := func(op string, x, y int16, element, color byte, copied bool) EditorDiffMessage {
		t.Helper()
		diff, err := session.Edit(member, EditorEditMessage{
			Type: MessageTypeEditorEdit, Op: op, X: x, Y: y,
			Element: element, Color: color, Copied: copied,
		})
		if err != nil {
			t.Fatalf("Edit(%s): %v", op, err)
		}
		if diff.Type != MessageTypeEditorDiff || len(diff.Cells) == 0 {
			t.Fatalf("Edit(%s) diff=%+v, want dirty editor diff", op, diff)
		}
		return diff
	}

	place("place", 10, 10, E_SOLID, 0x0e, false)
	if err := session.Apply(member, func(e *Engine) {
		tile := e.Board.Tiles[10][10]
		if tile != (TTile{Element: E_SOLID, Color: 0x0e}) {
			t.Fatalf("placed tile=%+v, want solid yellow", tile)
		}
	}); err != nil {
		t.Fatal(err)
	}

	place("erase", 10, 10, 0, 0, false)
	if err := session.Apply(member, func(e *Engine) {
		if tile := e.Board.Tiles[10][10]; tile.Element != E_EMPTY {
			t.Fatalf("erased tile=%+v, want empty", tile)
		}
	}); err != nil {
		t.Fatal(err)
	}

	// Fence a small region so the original 256-entry flood-fill queue only
	// sees this three-cell area, then prove its color-sensitive boundary rule.
	if err := session.Apply(member, func(e *Engine) {
		for x := int16(19); x <= 23; x++ {
			for y := int16(19); y <= 23; y++ {
				e.Board.Tiles[x][y] = TTile{Element: E_SOLID, Color: 0x09}
			}
		}
		for x := int16(20); x <= 22; x++ {
			e.Board.Tiles[x][20] = TTile{Element: E_NORMAL, Color: 0x0a}
		}
		e.Board.Tiles[21][21] = TTile{Element: E_NORMAL, Color: 0x0b}
		e.BoardClose()
		e.DrainScreenDirty()
	}); err != nil {
		t.Fatal(err)
	}
	place("fill", 20, 20, E_BREAKABLE, 0x0c, false)
	if err := session.Apply(member, func(e *Engine) {
		for x := int16(20); x <= 22; x++ {
			if tile := e.Board.Tiles[x][20]; tile != (TTile{Element: E_BREAKABLE, Color: 0x0c}) {
				t.Fatalf("filled tile at %d,20=%+v, want breakable red", x, tile)
			}
		}
		if tile := e.Board.Tiles[21][21]; tile != (TTile{Element: E_NORMAL, Color: 0x0b}) {
			t.Fatalf("fill crossed different-color boundary: %+v", tile)
		}
	}); err != nil {
		t.Fatal(err)
	}

	// A copied tile may retain its own color rather than the current selector.
	place("place", 12, 10, E_FAKE, 0x1d, true)
	var saved TWorld
	if err := session.Apply(member, func(e *Engine) {
		if tile := e.Board.Tiles[12][10]; tile != (TTile{Element: E_FAKE, Color: 0x1d}) {
			t.Fatalf("copied tile=%+v, want fake magenta", tile)
		}
		e.BoardClose()
		saved = cloneWorld(e.World)
	}); err != nil {
		t.Fatal(err)
	}

	reloaded := NewEditorSession("TEST", saved)
	if err := reloaded.Enter(member); err != nil {
		t.Fatalf("reloaded Enter: %v", err)
	}
	defer reloaded.Exit(member)
	inspect, err := reloaded.Inspect(member, 12, 10)
	if err != nil {
		t.Fatalf("reloaded Inspect: %v", err)
	}
	if inspect.Inspect.ElementID != E_FAKE || inspect.Inspect.Color != 0x1d {
		t.Fatalf("round-trip inspect=%+v, want copied fake tile", inspect.Inspect)
	}
}

func TestEditorSessionPlacementRemovesExistingStat(t *testing.T) {
	session := NewEditorSession("TEST", testEmptyWorld(t))
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(member)
	if err := session.Apply(member, func(e *Engine) {
		// Stat 0 is the player/monitor stat that vanilla explicitly protects.
		// Put the object at stat 1 to exercise EditorPrepareModifyTile's removal.
		e.AddStat(2, 2, E_OBJECT, 0x0f, 3, StatTemplateDefault)
		e.AddStat(8, 8, E_OBJECT, 0x0f, 3, StatTemplateDefault)
		e.BoardClose()
		e.DrainScreenDirty()
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Edit(member, EditorEditMessage{Type: MessageTypeEditorEdit, Op: "place", X: 8, Y: 8, Element: E_NORMAL, Color: 0x0e}); err != nil {
		t.Fatal(err)
	}
	if err := session.Apply(member, func(e *Engine) {
		if e.Board.StatCount != 0 || e.GetStatIdAt(8, 8) != -1 {
			t.Fatalf("placing over stat left stats=%d id=%d", e.Board.StatCount, e.GetStatIdAt(8, 8))
		}
		if tile := e.Board.Tiles[8][8]; tile != (TTile{Element: E_NORMAL, Color: 0x0e}) {
			t.Fatalf("replacement tile=%+v", tile)
		}
	}); err != nil {
		t.Fatal(err)
	}
}
