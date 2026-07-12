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
