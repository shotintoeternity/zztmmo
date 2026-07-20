package zztgo

import (
	"bytes"
	"encoding/base64"
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

func TestEditorSessionAllowsMembersAndRequiresMembership(t *testing.T) {
	session := NewEditorSession("TEST", testEmptyWorld(t))
	member := &webSocketClient{}
	other := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatalf("first Enter: %v", err)
	}
	defer session.Exit(member)
	if err := session.Enter(other); err != nil {
		t.Fatalf("second Enter: %v", err)
	}
	defer session.Exit(other)
	if len(session.Presence()) != 2 {
		t.Fatalf("presence len=%d, want 2", len(session.Presence()))
	}
	outsider := &webSocketClient{}
	if _, err := session.Inspect(other, 1, 1); err == nil {
		// other is a member; the assertion below checks a true outsider.
	} else {
		t.Fatalf("second member inspected editor session: %v", err)
	}
	if _, err := session.Inspect(outsider, 1, 1); err == nil {
		t.Fatal("outsider inspected editor session")
	}
}

func TestM102EditorSessionLeasesRefuseAndRelease(t *testing.T) {
	InitElementDefs()
	session := NewEditorSession("TEST", testEmptyWorld(t))
	alice := &webSocketClient{}
	bob := &webSocketClient{}
	if _, err := session.EnterNamed(alice, "Alice"); err != nil {
		t.Fatalf("EnterNamed(Alice): %v", err)
	}
	defer session.Exit(alice)
	if _, err := session.EnterNamed(bob, "Bob"); err != nil {
		t.Fatalf("EnterNamed(Bob): %v", err)
	}

	var boardID, objectID int16
	if err := session.Apply(alice, func(e *Engine) {
		boardID = e.World.Info.CurrentBoard
		e.AddStat(10, 10, E_OBJECT, 0x0f, 3, StatTemplateDefault)
		objectID = e.Board.StatCount
		e.BoardClose()
		e.DrainScreenDirty()
	}); err != nil {
		t.Fatal(err)
	}

	statLease := EditorLeaseMessage{Kind: "stat", BoardID: boardID, StatID: objectID}
	granted, err := session.AcquireLease(alice, statLease)
	if err != nil || granted.Op != "granted" {
		t.Fatalf("Alice stat lease=%+v err=%v, want granted", granted, err)
	}
	refused, err := session.AcquireLease(bob, statLease)
	if err != nil || refused.Op != "refused" || refused.HolderName != "Alice" {
		t.Fatalf("Bob stat lease=%+v err=%v, want refused by Alice", refused, err)
	}
	if reply, err := session.SetStat(bob, EditorStatMessage{Type: MessageTypeEditorStat, StatID: objectID, Field: "p1", Value: 65}); err != nil || reply.Type != "" {
		t.Fatalf("Bob SetStat without lease reply=%+v err=%v, want denied", reply, err)
	}
	session.ReleaseLease(alice, statLease)
	granted, err = session.AcquireLease(bob, statLease)
	if err != nil || granted.Op != "granted" {
		t.Fatalf("Bob stat lease after release=%+v err=%v, want granted", granted, err)
	}
	session.Exit(bob)
	granted, err = session.AcquireLease(alice, statLease)
	if err != nil || granted.Op != "granted" {
		t.Fatalf("Alice stat lease after disconnect=%+v err=%v, want granted", granted, err)
	}

	boardLease := EditorLeaseMessage{Kind: "board", BoardID: boardID}
	granted, err = session.AcquireLease(alice, boardLease)
	if err != nil || granted.Op != "granted" {
		t.Fatalf("Alice board lease=%+v err=%v, want granted", granted, err)
	}
	if _, err := session.EnterNamed(bob, "Bob"); err != nil {
		t.Fatalf("re-enter Bob: %v", err)
	}
	refused, err = session.AcquireLease(bob, boardLease)
	if err != nil || refused.Op != "refused" || refused.HolderName != "Alice" {
		t.Fatalf("Bob board lease=%+v err=%v, want refused by Alice", refused, err)
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

// clearEditorBoardInterior empties the currently-open board and drops its stats,
// matching testEdgeWorld's setup, so a live-room player can walk to any edge.
func clearEditorBoardInterior(e *Engine) {
	for ix := int16(1); ix <= BOARD_WIDTH; ix++ {
		for iy := int16(1); iy <= BOARD_HEIGHT; iy++ {
			e.Board.Tiles[ix][iy] = TTile{Element: E_EMPTY}
		}
	}
	e.Board.Tiles[e.Board.Stats[0].X][e.Board.Stats[0].Y] = TTile{Element: E_EMPTY}
	e.Board.StatCount = -1
}

// M5.5: add a second board, link the two boards' edges both ways in the editor,
// then host the saved world and walk a live player across the seam and back.
func TestEditorSessionAddBoardAndCrossBoardsInPlay(t *testing.T) {
	session := NewEditorSession("TEST", testEmptyWorld(t))
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(member)

	// Board 1 is current and empty. Give it a right (east) exit to board 2 and a
	// start square on its right edge.
	if err := session.Apply(member, func(e *Engine) {
		e.Board.Info.NeighborBoards[3] = 2
		e.Board.Info.StartPlayerX = BOARD_WIDTH
		e.Board.Info.StartPlayerY = 12
		e.BoardClose()
	}); err != nil {
		t.Fatal(err)
	}

	// EditorAppendBoard: a new board becomes current, is named, and the board
	// list grows to None + two boards.
	snap, err := session.AddBoard(member, "EAST")
	if err != nil {
		t.Fatalf("AddBoard: %v", err)
	}
	if snap.Type != MessageTypeEditorSnapshot || snap.BoardID != 2 || snap.Properties.BoardName != "EAST" {
		t.Fatalf("AddBoard snapshot=%+v, want board 2 named EAST", snap.Properties)
	}
	if len(snap.Properties.Boards) != 3 || snap.Properties.Boards[2].Name != "EAST" {
		t.Fatalf("board list=%+v, want None + two boards", snap.Properties.Boards)
	}

	// Board 2 (now current) gets an empty interior, a left (west) exit back to
	// board 1, and a start square on its left edge.
	if err := session.Apply(member, func(e *Engine) {
		clearEditorBoardInterior(e)
		e.Board.Info.NeighborBoards[2] = 1
		e.Board.Info.StartPlayerX = 1
		e.Board.Info.StartPlayerY = 12
		e.BoardClose()
	}); err != nil {
		t.Fatal(err)
	}

	// SwitchBoard returns to board 1 and reads its content, proving the switch
	// reopened the right board while the east exit survived.
	back, err := session.SwitchBoard(member, 1)
	if err != nil {
		t.Fatalf("SwitchBoard: %v", err)
	}
	if back.BoardID != 1 || back.Properties.NeighborBoards[3] != 2 {
		t.Fatalf("SwitchBoard(1) props=%+v, want board 1 east exit 2", back.Properties)
	}

	// Host the saved world and cross the seam in both directions.
	var saved TWorld
	if err := session.Apply(member, func(e *Engine) { saved = cloneWorld(e.World) }); err != nil {
		t.Fatal(err)
	}
	rm := NewRoomManager(saved)
	playerID := rm.JoinPlayer(1, BOARD_WIDTH, 12)
	rm.StepDiffs(map[PlayerID]PlayerInput{playerID: {DeltaX: 1}})
	if got := rm.players[playerID].boardID; got != 2 {
		t.Fatalf("walking east landed on board %d, want 2", got)
	}
	rm.StepDiffs(map[PlayerID]PlayerInput{playerID: {DeltaX: -1}})
	if got := rm.players[playerID].boardID; got != 1 {
		t.Fatalf("walking west back landed on board %d, want 1", got)
	}
}

// M5.5: EditorTransferBoard — export a board to .BRD bytes and re-import them
// into a different world's editor session. The board contents travel; the
// destination board's edge exits are cleared, matching the Pascal import.
func TestEditorSessionBoardExportImportRoundTrip(t *testing.T) {
	src := NewEditorSession("SRC", testMultiplayerSmokeWorld(t))
	member := &webSocketClient{}
	if err := src.Enter(member); err != nil {
		t.Fatal(err)
	}
	defer src.Exit(member)

	// Board 1 ("Smoke A") holds a gem at 11,12 and a passage stat at 12,12.
	if _, err := src.SwitchBoard(member, 1); err != nil {
		t.Fatalf("SwitchBoard(1): %v", err)
	}
	if _, err := src.SetProperty(member, EditorPropertyMessage{Field: "boardTitle", Text: "SMOKEA"}); err != nil {
		t.Fatalf("boardTitle: %v", err)
	}
	export, err := src.ExportBoard(member)
	if err != nil {
		t.Fatalf("ExportBoard: %v", err)
	}
	if export.Type != MessageTypeEditorBoardData || export.Name != "SMOKEA" {
		t.Fatalf("export=%+v, want SMOKEA .BRD", export)
	}
	data, err := base64.StdEncoding.DecodeString(export.Data)
	if err != nil {
		t.Fatalf("export data not base64: %v", err)
	}
	if len(data) < 2 || int(LoadInt16(data[:2])) != len(data)-2 {
		t.Fatalf("export data is not length-prefixed .BRD: %d bytes", len(data))
	}

	// Import into a different, empty world. Seed a stray edge exit first to prove
	// the import clears all four.
	dst := NewEditorSession("DST", testEmptyWorld(t))
	other := &webSocketClient{}
	if err := dst.Enter(other); err != nil {
		t.Fatal(err)
	}
	defer dst.Exit(other)
	if err := dst.Apply(other, func(e *Engine) {
		e.Board.Info.NeighborBoards[0] = 1
		e.BoardClose()
	}); err != nil {
		t.Fatal(err)
	}

	imported, err := dst.ImportBoard(other, data)
	if err != nil {
		t.Fatalf("ImportBoard: %v", err)
	}
	if imported.Type != MessageTypeEditorSnapshot || imported.Properties.BoardName != "SMOKEA" {
		t.Fatalf("import snapshot=%+v, want board named SMOKEA", imported.Properties)
	}
	if err := dst.Apply(other, func(e *Engine) {
		if e.Board.Tiles[11][12].Element != E_GEM {
			t.Fatalf("imported board missing gem at 11,12: %+v", e.Board.Tiles[11][12])
		}
		if e.Board.Tiles[12][12].Element != E_PASSAGE {
			t.Fatalf("imported board missing passage at 12,12: %+v", e.Board.Tiles[12][12])
		}
		for i := 0; i <= 3; i++ {
			if e.Board.Info.NeighborBoards[i] != 0 {
				t.Fatalf("import did not clear edge exits: %+v", e.Board.Info.NeighborBoards)
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
}

// M5.5: a world edited entirely through the browser editor must serialize to the
// vanilla on-disk .ZZT format, so it loads in DOS ZZT/zeta and ZZTMMO alike. The
// editor session never ticks, joins a player, or fires a bullet, so no
// multiplayer-only state (extra player stats, shot owners in bullet P1) can reach
// a board; StoreStat/BoardClose write only vanilla fields. This drives the real
// worldWriteTo -> worldReadFrom byte path to prove it.
func TestEditorSessionEditedWorldRoundTripsThroughVanillaFormat(t *testing.T) {
	session := NewEditorSession("TEST", testEmptyWorld(t))
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(member)

	if _, err := session.Edit(member, EditorEditMessage{Op: "place", X: 15, Y: 10, Element: E_SOLID, Color: 0x0e}); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if _, err := session.SetProperty(member, EditorPropertyMessage{Field: "boardTitle", Text: "EDITED"}); err != nil {
		t.Fatalf("boardTitle: %v", err)
	}
	if _, err := session.SetProperty(member, EditorPropertyMessage{Field: "worldName", Text: "MYWORLD"}); err != nil {
		t.Fatalf("worldName: %v", err)
	}
	if _, err := session.AddBoard(member, "SECOND"); err != nil {
		t.Fatalf("AddBoard: %v", err)
	}
	if _, err := session.SwitchBoard(member, 1); err != nil {
		t.Fatalf("SwitchBoard: %v", err)
	}
	if _, err := session.SetProperty(member, EditorPropertyMessage{Field: "exit", Exit: 3, Value: 2}); err != nil {
		t.Fatalf("exit: %v", err)
	}

	// Serialize exactly as WorldSave does (BoardClose then worldWriteTo).
	var buf bytes.Buffer
	if err := session.Apply(member, func(e *Engine) {
		e.BoardClose()
		if err := e.worldWriteTo(&buf); err != nil {
			t.Fatalf("worldWriteTo: %v", err)
		}
	}); err != nil {
		t.Fatal(err)
	}

	// Reload through the vanilla reader in a fresh engine.
	fresh := NewEngine()
	fresh.Headless = true
	if err := fresh.worldReadFrom(&buf, false, func() {}); err != nil {
		t.Fatalf("worldReadFrom: %v", err)
	}
	if fresh.World.BoardCount != 2 {
		t.Fatalf("reloaded BoardCount=%d, want 2", fresh.World.BoardCount)
	}
	if fresh.World.Info.Name != "MYWORLD" {
		t.Fatalf("reloaded world name=%q, want MYWORLD", fresh.World.Info.Name)
	}
	fresh.BoardOpen(1)
	if fresh.Board.Name != "EDITED" {
		t.Fatalf("reloaded board 1 name=%q, want EDITED", fresh.Board.Name)
	}
	if got := fresh.Board.Tiles[15][10]; got.Element != E_SOLID || got.Color != 0x0e {
		t.Fatalf("edited tile did not survive .ZZT round trip: %+v", got)
	}
	if fresh.Board.Info.NeighborBoards[3] != 2 {
		t.Fatalf("reloaded east exit=%d, want 2", fresh.Board.Info.NeighborBoards[3])
	}
}

// M5.5: an imported .BRD comes from a client file. A malformed one — wrong
// length prefix, or well-sized but internally garbage — must be rejected without
// panicking the server or disturbing the current board.
func TestEditorSessionImportRejectsMalformedBoard(t *testing.T) {
	session := NewEditorSession("TEST", testMultiplayerSmokeWorld(t))
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(member)

	snapshotBoard := func() []byte {
		var data []byte
		if err := session.Apply(member, func(e *Engine) {
			e.BoardClose()
			data = append([]byte(nil), e.World.BoardData[e.World.Info.CurrentBoard]...)
		}); err != nil {
			t.Fatal(err)
		}
		return data
	}

	before := snapshotBoard()

	// Length prefix claims 100 bytes but only 4 follow.
	if _, err := session.ImportBoard(member, []byte{100, 0, 1, 2, 3, 4}); err != nil {
		t.Fatalf("ImportBoard(short): %v", err)
	}
	if !bytes.Equal(before, snapshotBoard()) {
		t.Fatal("length-mismatched import altered the current board")
	}

	// Correctly length-prefixed but far too short to hold even a board name:
	// BoardOpen slices past the end of the buffer. The guard must catch that panic
	// and roll the board back.
	garbage := make([]byte, 2+20)
	StoreInt16(garbage[:2], 20)
	if _, err := session.ImportBoard(member, garbage); err != nil {
		t.Fatalf("ImportBoard(garbage): %v", err)
	}
	if !bytes.Equal(before, snapshotBoard()) {
		t.Fatal("garbage import altered the current board")
	}
}

// TestEditorSessionMembersEditDifferentBoards is the M17.12 core claim: two
// members of one session hold their own current board. Before M17.12 the
// session had a single shared board, so whoever switched dragged everyone with
// them — and each member's sidebar/palette state came from whichever board the
// shared engine happened to have open, which is how phantom elements appeared.
func TestEditorSessionMembersEditDifferentBoards(t *testing.T) {
	session := NewEditorSession("TEST", testEmptyWorld(t))
	alice := &webSocketClient{}
	bob := &webSocketClient{}
	if err := session.Enter(alice); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(alice)
	if err := session.Enter(bob); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(bob)

	// Give the world a second board for Bob to work on.
	if _, err := session.AddBoard(alice, "Board 2"); err != nil {
		t.Fatalf("AddBoard: %v", err)
	}
	// AddBoard leaves Alice on the new board; put her back on board 1.
	if _, err := session.SwitchBoard(alice, 1); err != nil {
		t.Fatalf("SwitchBoard(alice, 1): %v", err)
	}

	// Bob moves to board 2. Alice must not follow.
	if _, err := session.SwitchBoard(bob, 2); err != nil {
		t.Fatalf("SwitchBoard(bob, 2): %v", err)
	}

	var aliceBoard, bobBoard int16
	if err := session.Apply(alice, func(e *Engine) {
		aliceBoard = e.World.Info.CurrentBoard
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.Apply(bob, func(e *Engine) {
		bobBoard = e.World.Info.CurrentBoard
	}); err != nil {
		t.Fatal(err)
	}
	if aliceBoard != 1 {
		t.Errorf("alice edits board %d, want 1 (bob's switch dragged her along)", aliceBoard)
	}
	if bobBoard != 2 {
		t.Errorf("bob edits board %d, want 2", bobBoard)
	}

	// Each member's edit must land on their own board, not the other's.
	if err := session.Apply(alice, func(e *Engine) {
		e.Board.Tiles[5][5].Element = E_SOLID
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.Apply(bob, func(e *Engine) {
		if e.Board.Tiles[5][5].Element == E_SOLID {
			t.Errorf("alice's edit on board 1 is visible on bob's board 2")
		}
	}); err != nil {
		t.Fatal(err)
	}

	// Presence carries each member's board so collaborators can filter cursors.
	byID := map[string]EditorPresence{}
	for _, presence := range session.Presence() {
		byID[presence.ID] = presence
	}
	if got := byID[session.MemberID(alice)].BoardID; got != 1 {
		t.Errorf("alice presence BoardID = %d, want 1", got)
	}
	if got := byID[session.MemberID(bob)].BoardID; got != 2 {
		t.Errorf("bob presence BoardID = %d, want 2", got)
	}
}

// TestEditorPresenceColorsAreDistinctFromTheLocalCursor pins the M17.9 palette
// against the bug that shipped with it: bright cyan 0x0B is #55ffff in the
// client's EGA palette, white minus the red channel, and on the thin cross
// cursor glyph it is indistinguishable from the local cursor's white 0x0F. It
// was the second colour, so a two-person session hit it immediately — the first
// player saw the second's cursor as white while the second saw yellow.
func TestEditorPresenceColorsAreDistinctFromTheLocalCursor(t *testing.T) {
	const localCursorColor = 0x0f // EDITOR_CURSOR_COLOR in editor_cursor.ts

	seen := map[byte]int{}
	for n := 1; n <= 8; n++ {
		color := editorPresenceColor(n)

		if color == localCursorColor {
			t.Errorf("member %d gets 0x%02x, the local cursor's own colour", n, color)
		}
		for _, near := range editorPresenceColorsNearWhite {
			if color == near {
				t.Errorf("member %d gets 0x%02x, too close to the local cursor's white to tell apart", n, color)
			}
		}
		// Foreground-only: a background nibble would paint the whole cell and
		// hide the board tile under the cursor.
		if color&0xf0 != 0 {
			t.Errorf("member %d gets 0x%02x, which has a background nibble", n, color)
		}
		if prev, dup := seen[color]; dup {
			t.Errorf("member %d reuses member %d's colour 0x%02x", n, prev, color)
		}
		seen[color] = n
	}

	// The palette wraps rather than running out.
	if editorPresenceColor(9) != editorPresenceColor(1) {
		t.Errorf("palette should wrap: member 9 = 0x%02x, member 1 = 0x%02x",
			editorPresenceColor(9), editorPresenceColor(1))
	}
}
