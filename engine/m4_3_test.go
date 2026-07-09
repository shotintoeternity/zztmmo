package zztgo

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// M4.3 — title, world, save, and high-score flows.
//
// Three bugs this task names, all of the same shape: a non-gameplay flow that
// vanilla could hard-code to "the player" because there was only ever one.
//
//   * QuitPromptEvent carried no StatId, so a room broadcast it to everybody.
//   * GamePromptEndPlay read PlayerFor(0).Health — the wrong player's.
//   * HighScoresAdd took a bare score, read from PlayerFor(0).
//
// The quit prompt is the interesting one, because answering it must remove a
// player from a shared board without disturbing anyone else.

func quitPromptEventFor(events []Event, statId int16) (QuitPromptEvent, bool) {
	for _, ev := range events {
		if qp, ok := ev.(QuitPromptEvent); ok && qp.StatId == statId {
			return qp, true
		}
	}
	return QuitPromptEvent{}, false
}

func findQuitEvent(events []Event) (QuitEvent, bool) {
	for _, ev := range events {
		if qe, ok := ev.(QuitEvent); ok {
			return qe, true
		}
	}
	return QuitEvent{}, false
}

func findHighScoreEntryEvent(events []Event) (HighScoreEntryEvent, bool) {
	for _, ev := range events {
		if hs, ok := ev.(HighScoreEntryEvent); ok {
			return hs, true
		}
	}
	return HighScoreEntryEvent{}, false
}

// 'Q' from player 2 must name player 2. Driven through the wire format, because
// a QuitPromptEvent whose StatId never reached ProtocolEvent would still pass a
// test that read the engine's event slice directly.
func TestM43QuitPromptCarriesTriggeringPlayer(t *testing.T) {
	e, p1, p2 := twoPlayerBoard(t)
	e.MultiRoom = true

	events := stepMsg(e, p2, commandMsg('Q'))
	if _, ok := quitPromptEventFor(events, p2); !ok {
		t.Fatalf("want QuitPromptEvent{StatId:%d}, got %+v", p2, events)
	}
	if _, ok := quitPromptEventFor(events, p1); ok {
		t.Errorf("player %d's quit must not prompt player %d", p2, p1)
	}

	for _, event := range ProtocolEvents(events) {
		if event.Type == "quitPrompt" && event.StatID != p2 {
			t.Errorf("protocol quitPrompt.statId=%d, want %d", event.StatID, p2)
		}
	}
}

// Escape reaches the same switch as 'Q' (ELEMENTS.PAS:1431 `#27, 'Q'`).
func TestM43EscapeOpensQuitPrompt(t *testing.T) {
	e, _, p2 := twoPlayerBoard(t)
	e.MultiRoom = true

	events := stepMsg(e, p2, commandMsg(KEY_ESCAPE))
	if _, ok := quitPromptEventFor(events, p2); !ok {
		t.Fatalf("Escape must emit QuitPromptEvent{StatId:%d}, got %+v", p2, events)
	}
}

// GamePromptEndPlay reads the health of the player who asked, not stat 0's.
//
// Called directly rather than through a key, because ElementPlayerTick returns
// before its key switch while a player is dead (elements.go, M2.4's respawn).
// Vanilla instead falls through — that is how "Game over - Press ESCAPE" works
// (ELEMENTS.PAS:1340-1350) — so the dead branch is latent here, not dead code.
func TestM43EndPlayReadsOwnHealth(t *testing.T) {
	e, p1, p2 := twoPlayerBoard(t)
	e.PlayerFor(p1).Health = 0
	e.PlayerFor(p2).Health = 100

	e.Events = e.Events[:0]
	e.GamePromptEndPlay(p2)
	if _, ok := quitPromptEventFor(e.Events, p2); !ok {
		t.Errorf("a living player must be prompted; got %+v", e.Events)
	}
	if e.GamePlayExitRequested {
		t.Error("a living player's quit prompt must not request exit")
	}
}

// The single-player terminal keeps vanilla's behavior: a dead player's Escape
// exits play at once, with no prompt (ELEMENTS.PAS:1304-1306).
func TestM43DeadSinglePlayerExitsWithoutPrompt(t *testing.T) {
	e, p1, _ := twoPlayerBoard(t)
	e.MultiRoom = false
	e.PlayerFor(p1).Health = 0

	e.Events = e.Events[:0]
	e.GamePromptEndPlay(p1)
	if !e.GamePlayExitRequested {
		t.Error("dead single player must exit play immediately")
	}
	if _, ok := quitPromptEventFor(e.Events, p1); ok {
		t.Error("dead single player must not be prompted")
	}
}

// The freeze this guard exists to prevent: GamePlayExitRequested halts
// GameStepWithInputs' stat loop (game.go) and nothing resets it, so a dead
// player setting it in a room would stop the board for everyone, forever.
func TestM43DeadPlayerQuitDoesNotFreezeRoom(t *testing.T) {
	e, p1, p2 := twoPlayerBoard(t)
	e.MultiRoom = true
	e.PlayerFor(p1).Health = 0

	e.GamePromptEndPlay(p1)
	if e.GamePlayExitRequested {
		t.Fatal("a dead player's quit must never set GamePlayExitRequested in a room")
	}

	// The other player still ticks: prove the board is alive by moving them.
	before := e.Board.Stats[p2].X
	step(e, map[int16]PlayerInput{p2: {DeltaX: -1, Key: KEY_LEFT}})
	if e.Board.Stats[p2].X == before {
		t.Errorf("player %d stopped ticking: x stayed %d", p2, before)
	}
}

// A confirmed reply is a QuitEvent in a room, and GamePlayExitRequested alone
// in single-player. Declining does neither.
func TestM43QuitReplyRoutesByMode(t *testing.T) {
	t.Run("multi room emits QuitEvent", func(t *testing.T) {
		e, _, p2 := twoPlayerBoard(t)
		e.MultiRoom = true

		e.SubmitQuitReply(p2, true)
		events := step(e, nil)

		quit, ok := findQuitEvent(events)
		if !ok || quit.StatId != p2 {
			t.Fatalf("want QuitEvent{StatId:%d}, got %+v", p2, events)
		}
		if e.GamePlayExitRequested {
			t.Error("a room must never request exit")
		}
	})

	t.Run("single player exits", func(t *testing.T) {
		e, p1, _ := twoPlayerBoard(t)
		e.MultiRoom = false

		e.SubmitQuitReply(p1, true)
		events := step(e, nil)

		if _, ok := findQuitEvent(events); ok {
			t.Error("single player must not emit QuitEvent")
		}
		if !e.GamePlayExitRequested {
			t.Error("single player must request exit")
		}
	})

	t.Run("declining does nothing", func(t *testing.T) {
		e, _, p2 := twoPlayerBoard(t)
		e.MultiRoom = true

		e.SubmitQuitReply(p2, false)
		events := step(e, nil)

		if _, ok := findQuitEvent(events); ok {
			t.Error("a declined quit must not emit QuitEvent")
		}
		if e.GamePlayExitRequested {
			t.Error("a declined quit must not request exit")
		}
	})
}

// HighScoresAdd credits the player named, not stat 0.
func TestM43HighScoresAddUsesNamedPlayer(t *testing.T) {
	e, p1, p2 := twoPlayerBoard(t)
	e.PlayerFor(p1).Score = 0
	e.PlayerFor(p2).Score = 500
	for i := 0; i < HIGH_SCORE_COUNT; i++ {
		e.HighScoreList[i] = THighScoreEntry{Score: -1}
	}

	e.Events = e.Events[:0]
	e.HighScoresAdd(p2)

	entry, ok := findHighScoreEntryEvent(e.Events)
	if !ok {
		t.Fatalf("want HighScoreEntryEvent, got %+v", e.Events)
	}
	if entry.StatId != p2 || entry.Score != 500 {
		t.Errorf("got StatId=%d Score=%d, want %d and 500", entry.StatId, entry.Score, p2)
	}

	// Stat 0's score of zero does not qualify (HighScoresAdd requires score > 0),
	// which is exactly what the old signature would have read.
	e.Events = e.Events[:0]
	e.HighScoresAdd(p1)
	if _, ok := findHighScoreEntryEvent(e.Events); ok {
		t.Errorf("a zero score must not qualify; got %+v", e.Events)
	}
}

// One player quitting must leave the other playing, on the same board.
func TestM43RoomQuitLeavesOthersUndisturbed(t *testing.T) {
	rm := NewRoomManager(testEmptyWorld(t))
	quitter := rm.JoinPlayer(1, 0, 0)
	stayer := rm.JoinPlayer(1, 0, 0)

	stayerState, _ := rm.PlayerState(stayer)
	stayerState.Score = 42
	quitterState, _ := rm.PlayerState(quitter)
	quitterState.Score = 500

	if !rm.SubmitQuitReply(quitter, true) {
		t.Fatal("SubmitQuitReply refused a joined player")
	}
	diffs := rm.StepDiffs(nil)

	quits := rm.DrainQuits()
	if len(quits) != 1 || quits[0].PlayerID != quitter {
		t.Fatalf("want one quit by %d, got %+v", quitter, quits)
	}
	if quits[0].Score != 500 || quits[0].ListPos != 1 {
		t.Errorf("got Score=%d ListPos=%d, want 500 and 1", quits[0].Score, quits[0].ListPos)
	}
	if rm.DrainQuits() != nil {
		t.Error("DrainQuits must clear")
	}

	if _, ok := rm.PlayerState(quitter); ok {
		t.Error("the quitter must have left their room")
	}
	if _, ok := diffs[quitter]; ok {
		t.Error("the quitter must receive no diff")
	}

	// The stayer keeps their room, their score, and their diffs.
	if _, ok := diffs[stayer]; !ok {
		t.Fatal("the remaining player stopped receiving diffs")
	}
	state, ok := rm.PlayerState(stayer)
	if !ok || state.Score != 42 {
		t.Errorf("the remaining player's state changed: ok=%v state=%+v", ok, state)
	}

	// And the board keeps stepping for them.
	boardID, statID, ok := rm.PlayerLocation(stayer)
	if !ok || boardID != 1 {
		t.Fatalf("stayer location board=%d statID=%d ok=%v", boardID, statID, ok)
	}
	if diffs := rm.StepDiffs(nil); len(diffs) != 1 {
		t.Errorf("want 1 diff after the quit, got %d", len(diffs))
	}
}

// A score that does not beat the list still ends the game — it just carries no
// list position, so the client goes straight back to the title screen.
func TestM43QuitWithoutQualifyingScore(t *testing.T) {
	rm := NewRoomManager(testEmptyWorld(t))
	playerID := rm.JoinPlayer(1, 0, 0)
	state, _ := rm.PlayerState(playerID)
	state.Score = 0

	rm.SubmitQuitReply(playerID, true)
	rm.StepDiffs(nil)

	quits := rm.DrainQuits()
	if len(quits) != 1 {
		t.Fatalf("want one quit, got %+v", quits)
	}
	if quits[0].ListPos != 0 {
		t.Errorf("a zero score must not qualify; ListPos=%d", quits[0].ListPos)
	}
	if ok := rm.RecordHighScore(playerID, "NOBODY"); ok {
		t.Error("RecordHighScore must refuse a player who was never offered a slot")
	}
}

// The list is the RoomManager's, not an Engine's: rooms are per-board and a
// world has one list. Ranking and insertion follow HighScoresAdd's search and
// GamePlayLoop's shift.
func TestM43HighScoreListOrdering(t *testing.T) {
	rm := NewRoomManager(testEmptyWorld(t))

	record := func(score int16, name string) {
		playerID := rm.JoinPlayer(1, 0, 0)
		state, _ := rm.PlayerState(playerID)
		state.Score = score
		rm.SubmitQuitReply(playerID, true)
		rm.StepDiffs(nil)
		quits := rm.DrainQuits()
		if len(quits) != 1 || quits[0].ListPos == 0 {
			t.Fatalf("score %d did not qualify: %+v", score, quits)
		}
		if !rm.RecordHighScore(playerID, name) {
			t.Fatalf("RecordHighScore(%q) refused", name)
		}
	}

	record(100, "LOW")
	record(300, "HIGH")
	record(200, "MID")

	scores := rm.HighScores()
	want := []struct {
		name  string
		score int16
	}{{"HIGH", 300}, {"MID", 200}, {"LOW", 100}}
	for i, expected := range want {
		if scores[i].Name != expected.name || scores[i].Score != expected.score {
			t.Errorf("slot %d = %q/%d, want %q/%d", i, scores[i].Name, scores[i].Score, expected.name, expected.score)
		}
	}

	lines := rm.HighScoreLines(0)
	if len(lines) != 5 || !strings.Contains(lines[2], "HIGH") {
		t.Errorf("HighScoreLines = %q", lines)
	}
	// highlightPos is vanilla's "-- You! --" marker on the entry being written.
	if lines := rm.HighScoreLines(2); !strings.Contains(lines[3], "-- You! --") {
		t.Errorf("HighScoreLines(2) did not mark slot 2: %q", lines)
	}
}

// End to end over the wire: press Q, answer the prompt, get offered a slot,
// send a name, see the finished list. Nothing here touches terminal UI.
func TestM43QuitAndHighScoreOverWebSocket(t *testing.T) {
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(ServerReadLimit)

	if err := wsjson.Write(ctx, conn, JoinMessage{Type: MessageTypeJoin, Name: "quitter", Board: 1}); err != nil {
		t.Fatalf("write join: %v", err)
	}
	var snapshot SnapshotMessage
	if err := wsjson.Read(ctx, conn, &snapshot); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	playerID := snapshot.You.ID

	// Earn a score worth recording.
	state, ok := server.RoomManager.PlayerState(playerID)
	if !ok {
		t.Fatal("joined player has no state")
	}
	state.Score = 750

	// 'Q' opens the prompt, addressed to this player's stat.
	if err := wsjson.Write(ctx, conn, InputMessage{Type: MessageTypeInput, PlayerID: playerID, Seq: 1, Key: 'Q'}); err != nil {
		t.Fatalf("write Q: %v", err)
	}
	prompt, ok := readUntilProtocolEvent(ctx, t, conn, server, "quitPrompt", 20)
	if !ok {
		t.Fatal("no quitPrompt event")
	}
	if prompt.StatID != snapshot.You.StatID {
		t.Errorf("quitPrompt.statId=%d, want %d", prompt.StatID, snapshot.You.StatID)
	}

	// Answering yes ends this player's game and offers them the list.
	if err := wsjson.Write(ctx, conn, QuitReplyMessage{Type: MessageTypeQuitReply, PlayerID: playerID, Quit: true}); err != nil {
		t.Fatalf("write quitReply: %v", err)
	}
	entry, ok := readUntilProtocolEvent(ctx, t, conn, server, "highScoreEntry", 20)
	if !ok {
		t.Fatal("no highScoreEntry event after confirming quit")
	}
	if entry.Score != 750 || entry.ListPos != 1 {
		t.Errorf("highScoreEntry score=%d listPos=%d, want 750 and 1", entry.Score, entry.ListPos)
	}
	if _, ok := server.RoomManager.PlayerState(playerID); ok {
		t.Error("the quitter is still in a room")
	}

	// The name closes the loop and comes back as the finished list.
	if err := wsjson.Write(ctx, conn, HighScoreNameMessage{Type: MessageTypeHighScoreName, PlayerID: playerID, Name: "ACE"}); err != nil {
		t.Fatalf("write highScoreName: %v", err)
	}
	list, ok := readUntilProtocolEvent(ctx, t, conn, server, "highScores", 20)
	if !ok {
		t.Fatal("no highScores event after submitting a name")
	}
	joined := strings.Join(list.Lines, "\n")
	if !strings.Contains(joined, "ACE") || !strings.Contains(joined, "750") {
		t.Errorf("finished list missing the entry: %q", list.Lines)
	}
	if got := server.RoomManager.HighScores()[0]; got.Name != "ACE" || got.Score != 750 {
		t.Errorf("stored entry = %q/%d, want ACE/750", got.Name, got.Score)
	}
}

// The ordinary case: quitting with nothing worth recording still ends the game,
// as a bare "quit" event that sends the client back to the title screen.
func TestM43QuitWithoutScoreOverWebSocket(t *testing.T) {
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(ServerReadLimit)

	if err := wsjson.Write(ctx, conn, JoinMessage{Type: MessageTypeJoin, Name: "quitter", Board: 1}); err != nil {
		t.Fatalf("write join: %v", err)
	}
	var snapshot SnapshotMessage
	if err := wsjson.Read(ctx, conn, &snapshot); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	playerID := snapshot.You.ID

	if err := wsjson.Write(ctx, conn, QuitReplyMessage{Type: MessageTypeQuitReply, PlayerID: playerID, Quit: true}); err != nil {
		t.Fatalf("write quitReply: %v", err)
	}
	if _, ok := readUntilProtocolEvent(ctx, t, conn, server, "quit", 20); !ok {
		t.Fatal("no quit event for a player whose score did not place")
	}
	if _, ok := server.RoomManager.PlayerState(playerID); ok {
		t.Error("the quitter is still in a room")
	}
}

// Declining the prompt keeps the player in their room.
func TestM43DecliningQuitKeepsPlaying(t *testing.T) {
	rm := NewRoomManager(testEmptyWorld(t))
	playerID := rm.JoinPlayer(1, 0, 0)

	rm.SubmitQuitReply(playerID, false)
	rm.StepDiffs(nil)

	if quits := rm.DrainQuits(); len(quits) != 0 {
		t.Fatalf("declining must not quit anyone; got %+v", quits)
	}
	if _, ok := rm.PlayerState(playerID); !ok {
		t.Error("a player who said no left their room")
	}
}

// A quitter who closes the tab before naming their score must not leave the
// slot reserved for the life of the process.
func TestM43PendingScoreDiscardedOnDisconnect(t *testing.T) {
	rm := NewRoomManager(testEmptyWorld(t))
	playerID := rm.JoinPlayer(1, 0, 0)
	state, _ := rm.PlayerState(playerID)
	state.Score = 900

	rm.SubmitQuitReply(playerID, true)
	rm.StepDiffs(nil)
	if quits := rm.DrainQuits(); len(quits) != 1 || quits[0].ListPos != 1 {
		t.Fatalf("expected a qualifying quit, got %+v", quits)
	}

	rm.DiscardPendingScore(playerID)
	if rm.RecordHighScore(playerID, "GHOST") {
		t.Error("a discarded slot was still claimable")
	}
	if got := rm.HighScores()[0]; got.Name == "GHOST" {
		t.Error("a discarded slot was written")
	}
}

// HighScorePath is what zzt-server sets; an empty path must stay in memory so
// that no test writes a stray <world>.HI into the source tree.
func TestM43HighScorePersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "TOWN.HI")

	saver := NewRoomManager(testEmptyWorld(t))
	saver.HighScorePath = path
	playerID := saver.JoinPlayer(1, 0, 0)
	state, _ := saver.PlayerState(playerID)
	state.Score = 1234

	saver.SubmitQuitReply(playerID, true)
	saver.StepDiffs(nil)
	saver.DrainQuits()
	if !saver.RecordHighScore(playerID, "ACE") {
		t.Fatal("RecordHighScore refused a qualifying score")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("RecordHighScore did not write %s: %v", path, err)
	}

	// A fresh process reads it back.
	loader := NewRoomManager(testEmptyWorld(t))
	loader.HighScorePath = path
	loader.LoadHighScores()
	if got := loader.HighScores()[0]; got.Name != "ACE" || got.Score != 1234 {
		t.Errorf("round trip = %q/%d, want ACE/1234", got.Name, got.Score)
	}

	// No path: no file, no panic.
	memory := NewRoomManager(testEmptyWorld(t))
	memory.LoadHighScores()
	memory.saveHighScores()
	if got := memory.HighScores()[0]; got.Score != -1 {
		t.Errorf("an unset in-memory list must stay empty, got %+v", got)
	}
}

// readUntilProtocolEvent drives ticks and returns the first event of the given
// type, from either a diff's events array or a standalone event message.
func readUntilProtocolEvent(ctx context.Context, t *testing.T, conn *websocket.Conn, server *WebSocketServer, eventType string, maxTicks int) (ProtocolEvent, bool) {
	t.Helper()

	for i := 0; i < maxTicks; i++ {
		time.Sleep(5 * time.Millisecond)
		server.Tick(ctx)

		readCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		var raw json.RawMessage
		err := wsjson.Read(readCtx, conn, &raw)
		cancel()
		if err != nil {
			continue
		}

		var msg struct {
			Event  *ProtocolEvent  `json:"event"`
			Events []ProtocolEvent `json:"events"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		if msg.Event != nil && msg.Event.Type == eventType {
			return *msg.Event, true
		}
		for _, event := range msg.Events {
			if event.Type == eventType {
				return event, true
			}
		}
	}
	return ProtocolEvent{}, false
}

// The help filename reaches the server from the client, so it is confined to a
// bare basename — the same rule M4.3a will need for save filenames.
func TestM43HelpFilenameRejectsTraversal(t *testing.T) {
	valid := []string{"GAME.HLP", "ABOUT.HLP"}
	for _, name := range valid {
		if !validHelpFile(name) {
			t.Errorf("validHelpFile(%q) = false, want true", name)
		}
	}

	invalid := []string{
		"", "GAME.ZZT", "../GAME.HLP", "../../etc/passwd",
		"/etc/passwd", "sub/GAME.HLP", "..\\GAME.HLP", "..",
	}
	for _, name := range invalid {
		if validHelpFile(name) {
			t.Errorf("validHelpFile(%q) = true, want false", name)
		}
	}
}

// The title screen is the board the client draws behind its menu.
func TestM43TitleScreenCells(t *testing.T) {
	world := testEmptyWorld(t)
	cells := TitleScreenCells(world)

	if want := int(60 * 25); len(cells) != want {
		t.Fatalf("got %d cells, want %d (the 60x25 board area)", len(cells), want)
	}
	// The sidebar is the client's; the server must not stream into it.
	for _, cell := range cells {
		if cell.X >= 60 {
			t.Fatalf("cell at x=%d leaks into the sidebar", cell.X)
		}
	}
}
