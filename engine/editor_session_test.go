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

func TestEditorSessionStatSettingsPreserveVanillaStatSemantics(t *testing.T) {
	InitElementDefs()
	session := NewEditorSession("TEST", testEmptyWorld(t))
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(member)

	var gunID, passageID, objectID int16
	if err := session.Apply(member, func(e *Engine) {
		e.AddStat(10, 10, E_SPINNING_GUN, 0x0e, 3, StatTemplateDefault)
		gunID = e.Board.StatCount
		e.AddStat(11, 10, E_PASSAGE, 0x0e, 0, StatTemplateDefault)
		passageID = e.Board.StatCount
		e.AddStat(12, 10, E_OBJECT, 0x0f, 3, StatTemplateDefault)
		objectID = e.Board.StatCount
		object := &e.Board.Stats[objectID]
		object.Data, object.DataLen = "shared program", -1 // a #BIND-style object
		e.BoardClose()
		e.DrainScreenDirty()
	}); err != nil {
		t.Fatal(err)
	}

	set := func(statID int16, field string, value int16) EditorStatSettingsMessage {
		t.Helper()
		reply, err := session.SetStat(member, EditorStatMessage{Type: MessageTypeEditorStat, StatID: statID, Field: field, Value: value})
		if err != nil {
			t.Fatalf("SetStat(%s): %v", field, err)
		}
		if reply.Type != MessageTypeEditorStatSettings {
			t.Fatalf("SetStat(%s) reply=%+v", field, reply)
		}
		return reply
	}

	gun := set(gunID, "p2", 5)
	set(gunID, "bulletType", 1)
	set(gunID, "cycle", 7)
	passage := set(passageID, "p3", 0)
	object := set(objectID, "p1", '@')
	if gun.Inspect.Param2Name != "Firing rate?" || gun.Inspect.ParamBulletTypeName != "Firing type?" {
		t.Fatalf("spinning gun labels=%+v", gun.Inspect)
	}
	if passage.Inspect.ParamBoardName == "" || object.Inspect.Param1Name != "Character?" || object.Inspect.ParamTextName != "Edit Program" {
		t.Fatalf("element parameter meanings missing: passage=%+v object=%+v", passage.Inspect, object.Inspect)
	}

	if err := session.Apply(member, func(e *Engine) {
		gun := e.Board.Stats[gunID]
		if gun.P2 != 0x85 || gun.Cycle != 7 {
			t.Fatalf("gun settings=%+v, want firing rate 5, stars, cycle 7", gun)
		}
		if got := e.Board.Stats[passageID].P3; got != 0 {
			t.Fatalf("passage destination=%d, want none", got)
		}
		obj := e.Board.Stats[objectID]
		if obj.P1 != '@' || obj.Data != "shared program" || obj.DataLen != -1 {
			t.Fatalf("object edit changed bind/program: %+v", obj)
		}
		if obj.Follower != -1 || obj.Leader != -1 {
			t.Fatalf("object edit touched centipede links: %+v", obj)
		}
	}); err != nil {
		t.Fatal(err)
	}
}

func TestEditorSessionBoardAndWorldPropertiesRoundTripIntoLiveRoom(t *testing.T) {
	// Board 2 of this fixture starts at its right edge, so a newly configured
	// right exit gives the live-room half of this test a real edge crossing.
	session := NewEditorSession("TEST", testEdgeWorld(t))
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(member)

	set := func(field string, edit EditorPropertyMessage) {
		t.Helper()
		edit.Type = MessageTypeEditorProperty
		edit.Field = field
		if _, err := session.SetProperty(member, edit); err != nil {
			t.Fatalf("SetProperty(%s): %v", field, err)
		}
	}
	set("boardTitle", EditorPropertyMessage{Text: "Edited East"})
	set("worldName", EditorPropertyMessage{Text: "Property Test"})
	set("maxShots", EditorPropertyMessage{Value: 7})
	set("dark", EditorPropertyMessage{Bool: true})
	set("exit", EditorPropertyMessage{Exit: 3, Value: 1})
	set("reenter", EditorPropertyMessage{Bool: true})
	set("timeLimit", EditorPropertyMessage{Value: 42})

	var saved TWorld
	if err := session.Apply(member, func(e *Engine) { saved = cloneWorld(e.World) }); err != nil {
		t.Fatal(err)
	}
	reloaded := NewEditorSession("TEST", saved)
	if err := reloaded.Enter(member); err != nil {
		t.Fatal(err)
	}
	defer reloaded.Exit(member)
	properties, err := reloaded.Properties(member)
	if err != nil {
		t.Fatal(err)
	}
	p := properties.Properties
	if p.BoardName != "Edited East" || p.WorldName != "Property Test" || p.MaxShots != 7 || !p.IsDark || !p.ReenterWhenZapped || p.TimeLimitSec != 42 || p.NeighborBoards[3] != 1 {
		t.Fatalf("properties did not survive BoardClose/BoardOpen: %+v", p)
	}

	// A fresh RoomManager is what M5.6 will host from the saved session world.
	// These assertions prove the three M5.2 gameplay-relevant settings survive
	// that boundary: dark and time limit are visible to the room; the exit
	// actually transfers a player to its selected board.
	rm := NewRoomManager(saved)
	playerID := rm.JoinPlayer(2, BOARD_WIDTH, BOARD_HEIGHT/2)
	snapshot, ok := rm.Snapshot(playerID)
	if !ok {
		t.Fatal("live room did not produce a snapshot")
	}
	if snapshot.HUD.TimeLimitSec != 42 {
		t.Fatalf("live HUD time limit=%d, want 42", snapshot.HUD.TimeLimitSec)
	}
	room, ok := rm.Room(2)
	if !ok || !room.Engine.Board.Info.IsDark {
		t.Fatal("saved dark board did not take effect in live room")
	}
	rm.StepDiffs(map[PlayerID]PlayerInput{playerID: {DeltaX: 1}})
	if got := rm.players[playerID].boardID; got != 1 {
		t.Fatalf("edited right exit transferred to board %d, want 1", got)
	}
}

func TestEditorSessionProgramTextEditRoundTrip(t *testing.T) {
	InitElementDefs()
	session := NewEditorSession("TEST", testEmptyWorld(t))
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(member)

	// A bear (stat 0, no ParamTextName), an object whose program has a leading
	// @name, a #command and a :label (stat 1), and a duplicate object that shares
	// its Data via a negative DataLen (stat 2), so ProgramText must resolve the
	// binding. The bear proves a non-text element is refused. Stat 0 can never be
	// a bind target — BoardClose's dedup loop starts at index 1 — so the object
	// with the program must not be stat 0.
	program := "@Vendor\r#end\r:shop\r\"Hello\"\r"
	var bearID, objectID, sharedID int16
	if err := session.Apply(member, func(e *Engine) {
		e.AddStat(5, 5, E_BEAR, 0x06, 3, StatTemplateDefault)
		bearID = e.Board.StatCount
		e.AddStat(10, 10, E_OBJECT, 0x0f, 3, StatTemplateDefault)
		objectID = e.Board.StatCount
		e.Board.Stats[objectID].Data = program
		e.Board.Stats[objectID].DataLen = int16(len(program))
		e.AddStat(12, 10, E_OBJECT, 0x0f, 3, StatTemplateDefault)
		sharedID = e.Board.StatCount
		e.Board.Stats[sharedID].Data = program
		e.Board.Stats[sharedID].DataLen = -objectID
		e.BoardClose()
		e.DrainScreenDirty()
	}); err != nil {
		t.Fatal(err)
	}

	msg, err := session.ProgramText(member, objectID)
	if err != nil {
		t.Fatalf("ProgramText: %v", err)
	}
	if msg.Type != MessageTypeEditorProgramText || msg.Prompt != "Edit Program" {
		t.Fatalf("program message=%+v, want Edit Program", msg)
	}
	wantLines := []string{"@Vendor", "#end", ":shop", "\"Hello\""}
	if len(msg.Lines) != len(wantLines) {
		t.Fatalf("lines=%q, want %q", msg.Lines, wantLines)
	}
	for i, line := range wantLines {
		if msg.Lines[i] != line {
			t.Fatalf("line %d=%q, want %q", i, msg.Lines[i], line)
		}
	}

	// The shared object (negative DataLen) reads the same program.
	shared, err := session.ProgramText(member, sharedID)
	if err != nil {
		t.Fatalf("ProgramText(shared): %v", err)
	}
	if len(shared.Lines) != len(wantLines) {
		t.Fatalf("shared object program not resolved: %q", shared.Lines)
	}

	// A non-text element (the bear) has no program and is refused unchanged.
	empty, err := session.ProgramText(member, bearID)
	if err != nil {
		t.Fatalf("ProgramText(bear): %v", err)
	}
	if empty.Type != "" || len(empty.Lines) != 0 {
		t.Fatalf("non-text stat returned a program: %+v", empty)
	}

	// Rewrite the program and prove the save rebuilds Data/DataLen the vanilla way.
	newLines := []string{"@NewVendor", "#end"}
	reply, err := session.SaveProgram(member, objectID, newLines)
	if err != nil {
		t.Fatalf("SaveProgram: %v", err)
	}
	if reply.Type != MessageTypeEditorStatSettings {
		t.Fatalf("save reply=%+v", reply)
	}
	wantData := "@NewVendor\r#end\r"
	if err := session.Apply(member, func(e *Engine) {
		stat := e.Board.Stats[objectID]
		if stat.Data != wantData || int(stat.DataLen) != len(wantData) {
			t.Fatalf("saved program data=%q len=%d, want %q len=%d", stat.Data, stat.DataLen, wantData, len(wantData))
		}
	}); err != nil {
		t.Fatal(err)
	}

	// The sibling that shared the old program must keep it: editing one object
	// cannot rewrite the identical program of another (editorUnbindSharers).
	sibling, err := session.ProgramText(member, sharedID)
	if err != nil {
		t.Fatalf("ProgramText(shared after edit): %v", err)
	}
	if len(sibling.Lines) != len(wantLines) || sibling.Lines[0] != wantLines[0] {
		t.Fatalf("editing an object corrupted its shared sibling: %q", sibling.Lines)
	}

	// Saving a program to a non-text stat leaves it untouched.
	if _, err := session.SaveProgram(member, bearID, []string{"@ghost"}); err != nil {
		t.Fatalf("SaveProgram(bear): %v", err)
	}
	if err := session.Apply(member, func(e *Engine) {
		if e.Board.Stats[bearID].DataLen != 0 || e.Board.Stats[bearID].Data != "" {
			t.Fatalf("non-text stat gained a program: %+v", e.Board.Stats[bearID])
		}
	}); err != nil {
		t.Fatal(err)
	}

	// The edited program survives BoardClose/BoardOpen through the serializer.
	var saved TWorld
	if err := session.Apply(member, func(e *Engine) { saved = cloneWorld(e.World) }); err != nil {
		t.Fatal(err)
	}
	reloaded := NewEditorSession("TEST", saved)
	if err := reloaded.Enter(member); err != nil {
		t.Fatal(err)
	}
	defer reloaded.Exit(member)
	roundTrip, err := reloaded.ProgramText(member, objectID)
	if err != nil {
		t.Fatalf("reloaded ProgramText: %v", err)
	}
	if len(roundTrip.Lines) != len(newLines) || roundTrip.Lines[0] != newLines[0] || roundTrip.Lines[1] != newLines[1] {
		t.Fatalf("program did not round-trip through serializer: %q", roundTrip.Lines)
	}
}
