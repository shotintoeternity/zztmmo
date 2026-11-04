package zztgo

// M36.23 — a full board refuses a join instead of hijacking a stat.
//
// SpawnPlayer ignored AddStat's silent no-op at MAX_STAT and returned
// Board.StatCount anyway: the last stat on the board, usually an object. The
// joiner then drove that object, an E_PLAYER tile with no stat was painted at
// the spawn, and when they left RemovePlayer deleted the object from the world.
// Every way onto a board (a join, a passage or edge transfer, a world transit)
// went through the same spawn, so a full board now refuses all of them, says
// so, and touches nothing.

import (
	"bytes"
	"context"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

const (
	m3623Full    = int16(1) // passage + 150 objects: StatCount == MAX_STAT
	m3623Open    = int16(2) // one passage into the full board
	m3623Crowded = int16(3) // a player stat + 150 objects: StatCount == MAX_STAT
	m3623Passage = byte(0x0E)
)

// m3623ObjectAt is where object n (1..150) of a full board stands: rows 16..18,
// columns 1..50, well away from every spawn and passage square.
func m3623ObjectAt(n int) (int16, int16) {
	return int16((n-1)%50 + 1), int16((n-1)/50 + 16)
}

func m3623ClearBoard(e *Engine) {
	for ix := int16(1); ix <= BOARD_WIDTH; ix++ {
		for iy := int16(1); iy <= BOARD_HEIGHT; iy++ {
			e.Board.Tiles[ix][iy] = TTile{Element: E_EMPTY}
		}
	}
	e.Board.Tiles[e.Board.Stats[0].X][e.Board.Stats[0].Y] = TTile{Element: E_EMPTY}
	e.Board.StatCount = -1
}

func m3623AddObjects(e *Engine) {
	for n := 1; e.Board.StatCount < MAX_STAT; n++ {
		x, y := m3623ObjectAt(n)
		e.AddStat(x, y, E_OBJECT, 0x0F, 1, StatTemplateDefault)
		e.Board.Stats[e.Board.StatCount].P1 = byte('A' + n%26)
	}
}

func m3623World(t *testing.T) TWorld {
	t.Helper()
	setup := NewEngine()
	setup.Headless = true
	setup.WorldCreate()

	// Board 1: no player stat to claim, so every arrival needs a new stat.
	setup.BoardCreate()
	m3623ClearBoard(setup)
	setup.Board.Tiles[10][12] = TTile{Element: E_PASSAGE, Color: m3623Passage}
	setup.AddStat(10, 12, E_PASSAGE, int16(m3623Passage), 0, StatTemplateDefault)
	setup.Board.Stats[setup.Board.StatCount].P3 = byte(m3623Open)
	m3623AddObjects(setup)
	setup.Board.Name = "Full"
	setup.World.Info.CurrentBoard = m3623Full
	setup.BoardClose()

	// Board 2: room to spare, and a passage into board 1.
	setup.BoardCreate()
	m3623ClearBoard(setup)
	setup.Board.Tiles[5][5] = TTile{Element: E_PASSAGE, Color: m3623Passage}
	setup.AddStat(5, 5, E_PASSAGE, int16(m3623Passage), 0, StatTemplateDefault)
	setup.Board.Stats[setup.Board.StatCount].P3 = byte(m3623Full)
	setup.Board.Name = "Open"
	setup.World.Info.CurrentBoard = m3623Open
	setup.BoardClose()

	// Board 3: full, but its stat 0 is a player the first arrival can claim.
	setup.BoardCreate()
	m3623ClearBoard(setup)
	setup.Board.Tiles[30][12] = TTile{Element: E_PLAYER, Color: ElementDefs[E_PLAYER].Color}
	setup.AddStat(30, 12, E_PLAYER, int16(ElementDefs[E_PLAYER].Color), 1, StatTemplateDefault)
	m3623AddObjects(setup)
	setup.Board.Name = "Crowded"
	setup.World.Info.CurrentBoard = m3623Crowded
	setup.BoardClose()

	setup.World.BoardCount = 3
	setup.World.Info.CurrentBoard = m3623Full
	return setup.World
}

// m3623AssertUnharmed opens the board as the world now holds it and checks
// that every object is still there, still an object, and that nothing on the
// board is a player tile without a player stat.
func m3623AssertUnharmed(t *testing.T, world TWorld, boardID int16, firstObject int16) {
	t.Helper()
	e := NewEngine()
	e.Headless = true
	e.World = world
	e.BoardOpen(boardID)
	m3623AssertBoardUnharmed(t, e, boardID, firstObject)
}

func m3623AssertBoardUnharmed(t *testing.T, e *Engine, boardID int16, firstObject int16) {
	t.Helper()
	if e.Board.StatCount != MAX_STAT {
		t.Errorf("board %d StatCount = %d, want %d", boardID, e.Board.StatCount, MAX_STAT)
	}
	for statID := firstObject; statID <= MAX_STAT; statID++ {
		n := int(statID - firstObject + 1)
		x, y := m3623ObjectAt(n)
		stat := e.Board.Stats[statID]
		if int16(stat.X) != x || int16(stat.Y) != y {
			t.Errorf("board %d stat %d at (%d,%d), want object %d at (%d,%d)", boardID, statID, stat.X, stat.Y, n, x, y)
			continue
		}
		if el := e.Board.Tiles[x][y].Element; el != E_OBJECT {
			t.Errorf("board %d object %d at (%d,%d) is element %d, want E_OBJECT", boardID, n, x, y, el)
		}
	}
	for ix := int16(1); ix <= BOARD_WIDTH; ix++ {
		for iy := int16(1); iy <= BOARD_HEIGHT; iy++ {
			if e.Board.Tiles[ix][iy].Element == E_PLAYER && e.GetStatIdAt(ix, iy) < 0 {
				t.Errorf("board %d has a player tile at (%d,%d) with no stat", boardID, ix, iy)
			}
		}
	}
}

func m3623FullNotice(t *testing.T, events []Event) bool {
	t.Helper()
	for _, event := range events {
		if ev, ok := event.(ScrollEvent); ok && strings.Contains(strings.Join(ev.Lines, "\n"), "full") {
			return true
		}
	}
	return false
}

// The DoD: join a 150-stat board; no object is harmed.
func TestM3623JoinIntoAFullBoardIsRefused(t *testing.T) {
	rm := NewRoomManager(m3623World(t))
	if got := rm.JoinPlayer(m3623Full, 0, 0); got != 0 {
		t.Errorf("JoinPlayer into a %d-stat board = player %d, want 0 (refused)", MAX_STAT, got)
		if room, ok := rm.Room(m3623Full); ok {
			if _, statID, ok := rm.PlayerLocation(got); ok {
				t.Logf("joiner holds stat %d, element %d", statID,
					room.Engine.Board.Tiles[room.Engine.Board.Stats[statID].X][room.Engine.Board.Stats[statID].Y].Element)
			}
		}
		rm.LeavePlayer(got)
	}
	if rm.ActiveRoomCount() != 0 {
		t.Errorf("a refused join left %d live rooms, want the board frozen again", rm.ActiveRoomCount())
	}
	m3623AssertUnharmed(t, rm.FrozenWorld(), m3623Full, 1)
}

// A full board whose player stat is free to claim still takes its first
// player, because claiming adds no stat. The second is refused.
func TestM3623FullBoardStillTakesAClaimablePlayer(t *testing.T) {
	rm := NewRoomManager(m3623World(t))
	first := rm.JoinPlayer(m3623Crowded, 0, 0)
	if first == 0 {
		t.Fatal("first join into a full board with a claimable player stat was refused")
	}
	if _, statID, ok := rm.PlayerLocation(first); !ok || statID != 0 {
		t.Fatalf("first joiner holds stat %d (ok=%v), want the claimed stat 0", statID, ok)
	}
	if second := rm.JoinPlayer(m3623Crowded, 0, 0); second != 0 {
		t.Errorf("second join = player %d, want 0 (refused)", second)
	}
	// Checked live, with the first player still in: a last leave removes the
	// claimed player stat, which is the ordinary leave and not this bug.
	room, ok := rm.Room(m3623Crowded)
	if !ok {
		t.Fatal("the crowded board has no live room")
	}
	m3623AssertBoardUnharmed(t, room.Engine, m3623Crowded, 1)
}

// A passage into a full board leaves the traveller where they were, tells
// them, and leaves the full board alone.
func TestM3623TransferIntoAFullBoardIsRefused(t *testing.T) {
	rm := NewRoomManager(m3623World(t))
	player := rm.JoinPlayer(m3623Open, 6, 5)
	if player == 0 {
		t.Fatal("join into the open board was refused")
	}
	rm.DrainPlayerEvents(player)
	// A scroll read on this board before the refused transfer is still the
	// traveller's to answer afterwards: they never left.
	rm.players[player].noteOpenScroll(0, nil, false)
	var events []Event
	for k := 0; k < 4; k++ {
		rm.StepDiffs(map[PlayerID]PlayerInput{player: {DeltaX: -1}})
		events = append(events, rm.DrainPlayerEvents(player)...)
	}
	if boardID, _, ok := rm.PlayerLocation(player); !ok || boardID != m3623Open {
		t.Errorf("traveller is on board %d (ok=%v), want still on board %d", boardID, ok, m3623Open)
	}
	if !m3623FullNotice(t, events) {
		t.Errorf("traveller was not told the board is full; events = %#v", events)
	}
	for _, event := range events {
		if _, ok := event.(TransferEvent); ok {
			t.Errorf("a refused transfer still sent the traveller a transfer event")
		}
	}
	if _, ok := rm.Room(m3623Full); ok {
		t.Error("the full board was left thawed by a refused transfer")
	}
	// The notice answers to no object: dismissing it reaches nothing.
	if rm.SubmitScrollReply(player, -1, "") {
		t.Error("dismissing the full-board notice was taken as a scroll reply")
	}
	if !rm.SubmitScrollReply(player, 0, "") {
		t.Error("a refused transfer discarded the scroll the traveller had open")
	}
	notices := 0
	for _, event := range events {
		if ev, ok := event.(ScrollEvent); ok && ev.Title == "Board full" {
			notices++
		}
	}
	t.Logf("%d held steps against the full board queued %d notices", 4, notices)
	if !rm.LeavePlayer(player) {
		t.Fatal("LeavePlayer failed")
	}
	m3623AssertUnharmed(t, rm.FrozenWorld(), m3623Full, 1)
}

// The socket join says why and spawns nobody.
func TestM3623SocketJoinIntoAFullBoardIsRefused(t *testing.T) {
	world := m3623World(t)
	world.Info.Name = "FULL"
	server := NewWebSocketServer(world, m3623Full)
	app := httptest.NewServer(server)
	defer app.Close()
	wsURL := "ws" + strings.TrimPrefix(app.URL, "http") + "/"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(ServerReadLimit)
	if err := wsjson.Write(ctx, conn, JoinMessage{Type: MessageTypeJoin, Name: "late"}); err != nil {
		t.Fatalf("write join: %v", err)
	}
	var message map[string]interface{}
	if err := wsjson.Read(ctx, conn, &message); err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if message["type"] != MessageTypeEvent {
		t.Fatalf("first reply is %v, want an event telling the joiner the board is full: %#v", message["type"], message)
	}
	event, _ := message["event"].(map[string]interface{})
	if event["type"] != "scroll" || !strings.Contains(strings.ToLower(strings.Join(m3623Strings(event["lines"]), "\n")), "full") {
		t.Fatalf("reply event = %#v, want a scroll saying the board is full", event)
	}
	// And the server closes the connection rather than leaving a player-less
	// socket open.
	if err := wsjson.Read(ctx, conn, &message); err == nil {
		t.Errorf("after the refusal the server sent %#v, want the connection closed", message)
	}

	inst := server.DefaultInstance
	inst.mu.Lock()
	clients := len(inst.Clients)
	players := len(inst.RoomManager.players)
	frozen := inst.RoomManager.FrozenWorld()
	inst.mu.Unlock()
	if clients != 0 || players != 0 {
		t.Errorf("after a refused join the instance holds %d clients and %d players, want none", clients, players)
	}
	m3623AssertUnharmed(t, frozen, m3623Full, 1)
}

// A refused join is recorded, and playback refuses it the same way: the
// replayed world matches the live one board for board.
func TestM3623RefusedJoinReplays(t *testing.T) {
	var buf bytes.Buffer
	rm, rec := recordedRoomManager(t, "FULL", m3623World(t), &buf)
	player := rm.JoinPlayer(m3623Open, 6, 5)
	if rm.JoinPlayer(m3623Full, 0, 0) != 0 {
		t.Fatal("join into the full board was not refused")
	}
	for k := 0; k < 4; k++ {
		rm.StepDiffs(map[PlayerID]PlayerInput{player: {DeltaX: -1}})
	}
	live := rm.RoomStateHashes()
	liveFull := append([]byte(nil), rm.FrozenWorld().BoardData[m3623Full]...)
	rec.Close()

	replayed, err := ReplaySession(bytes.NewReader(buf.Bytes()), nil)
	if err != nil {
		t.Fatalf("ReplaySession: %v", err)
	}
	if got := replayed.RoomStateHashes(); !reflect.DeepEqual(got, live) {
		t.Errorf("replayed room hashes = %v, want %v", got, live)
	}
	if !bytes.Equal(replayed.FrozenWorld().BoardData[m3623Full], liveFull) {
		t.Error("replayed full board differs from the live one")
	}
	if len(replayed.players) != 1 {
		t.Errorf("replay holds %d players, want the one that was admitted", len(replayed.players))
	}
}

func m3623Strings(v interface{}) []string {
	items, _ := v.([]interface{})
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, _ := item.(string)
		out = append(out, s)
	}
	return out
}
