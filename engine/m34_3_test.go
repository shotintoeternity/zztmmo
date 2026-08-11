package zztgo

// M34.3 — the board: the day's paper posted where people loiter.
//
// The claims these tests exist to keep, in the order the spec makes them:
//
//  1. The stand is a real ZZT object standing on the tile the server's table
//     names, and its own program is a working two-line fallback — a one-line
//     body would be DisplayMessage and would never emit the ScrollEvent the
//     whole feature hangs on (oop.go's LineCount == 1 branch).
//  2. Touching it delivers the day's edition and delivers the world's own
//     window to nobody; a world with no notice table still shows the object's
//     own scroll, which is what vanilla means by a scroll.
//  3. The reader is frozen while reading and unfrozen by the ordinary reply,
//     because the pushed scroll carries the object's stat id.
//  4. The consent rule is the read-time one: a consented name appears and an
//     account that has not set one reads as a stranger.
//  5. The route and the board share ONE editor, so the day's spend bound is
//     the day's spend bound however the paper is asked for.
//  6. Nothing the board adds can write through the text window's border or
//     start a line with a byte ZZT-OOP reads as markup.

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// m343LobbyServer is the harness the board's journeys share: the shipped lobby
// world, a ledger stopped on a known day, and an account store to name it from.
func m343LobbyServer(t *testing.T) (*WebSocketServer, *GazetteLedger) {
	t.Helper()
	server := NewWebSocketServer(m291LobbyWorld(t), 1)
	server.ChatDB = NewMemChatDatabase()
	ledger, _ := m341Ledger(t, "")
	server.Gazette = ledger
	return server, ledger
}

// m343WalkToTheStand walks a player from the lobby's spawn to the newsstand:
// west along the empty row 13, then south down the empty column 13. The last
// step is the touch — an object is solid, so the player never stands on it.
func m343WalkToTheStand(t *testing.T, ctx context.Context, server *WebSocketServer, inst *WorldInstance, playerID PlayerID, conn *websocket.Conn) {
	t.Helper()
	tile := m343NoticeTile(t)
	board, x, y, ok := m343Location(server, inst, playerID)
	if !ok || board != tile.BoardID {
		t.Fatalf("player is on board %d at %d,%d (ok=%v), want board %d", board, x, y, ok, tile.BoardID)
	}
	// Driven by observed movement rather than by a guessed number of holds
	// (M16.11e): a walk that counts presses is a walk that breaks the next time
	// somebody moves a wall.
	press := func(mask uint16) (int16, int16) {
		m291SendInputAndTick(t, ctx, server, inst, playerID, conn, InputMessage{Type: MessageTypeInput, Keymask: mask})
		_, x, y, ok := m343Location(server, inst, playerID)
		if !ok {
			t.Fatalf("the walker left the board at %d,%d", x, y)
		}
		return x, y
	}
	for steps := 0; x > tile.X; steps++ {
		if steps > 60 {
			t.Fatalf("stalled walking west at %d,%d, want column %d", x, y, tile.X)
		}
		x, y = press(InputMaskLeft)
	}
	for steps := 0; y < tile.Y-1; steps++ {
		if steps > 60 {
			t.Fatalf("stalled walking south at %d,%d, want row %d", x, y, tile.Y-1)
		}
		x, y = press(InputMaskDown)
	}
	// Standing beside the stand is not touching it, and an object answers on
	// its own cycle rather than on the tick the player pushed. Keep pushing
	// south until the room says the reader is reading.
	for steps := 0; !m343ScrollOpen(server, inst, playerID); steps++ {
		if steps > 20 {
			t.Fatalf("the stand at %+v never opened for the reader standing at %d,%d", tile, x, y)
		}
		x, y = press(InputMaskDown)
	}
}

func m343Location(server *WebSocketServer, inst *WorldInstance, playerID PlayerID) (board, x, y int16, ok bool) {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	board, statID, ok := inst.RoomManager.PlayerLocation(playerID)
	if !ok {
		return 0, 0, 0, false
	}
	room, found := inst.RoomManager.Room(board)
	if !found {
		return 0, 0, 0, false
	}
	stat := room.Engine.Board.Stats[statID]
	return board, int16(stat.X), int16(stat.Y), true
}

func m343NoticeTile(t *testing.T) TransitGateKey {
	t.Helper()
	for tile, kind := range defaultLobbyNotices {
		if kind == GazetteNoticeKind {
			return tile
		}
	}
	t.Fatal("the lobby has no Gazette notice tile")
	return TransitGateKey{}
}

func m343ScrollOpen(server *WebSocketServer, inst *WorldInstance, playerID PlayerID) bool {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	player := inst.RoomManager.players[playerID]
	return player != nil && player.scrollOpen
}

// ---------------------------------------------------------------------------
// The stand
// ---------------------------------------------------------------------------

// The world file and the server's table are two halves of one decision, and
// nothing in either one can tell when the other moves. So: the keyed tile holds
// an Object, that object is the Gazette, and its fallback body is long enough
// to actually open a window.
func TestM343TheKeyedTileHoldsTheGazetteStand(t *testing.T) {
	tile := m343NoticeTile(t)
	rm := NewRoomManager(m291LobbyWorld(t))
	rm.JoinPlayer(tile.BoardID, tile.X, tile.Y-2)
	room, ok := rm.Room(tile.BoardID)
	if !ok {
		t.Fatalf("the lobby has no board %d", tile.BoardID)
	}
	board := room.Engine.Board

	var program string
	found := false
	for i := int16(0); i <= board.StatCount; i++ {
		stat := board.Stats[i]
		if int16(stat.X) != tile.X || int16(stat.Y) != tile.Y {
			continue
		}
		if board.Tiles[tile.X][tile.Y].Element != E_OBJECT {
			t.Fatalf("the notice tile %+v holds element %d, want an Object",
				tile, board.Tiles[tile.X][tile.Y].Element)
		}
		program = stat.Data
		found = true
	}
	if !found {
		t.Fatalf("no stat stands on the notice tile %+v; fixtures/lobby.zwd and lobby.go disagree", tile)
	}
	if !strings.Contains(program, "@The ZZT Gazette") {
		t.Errorf("the object on the notice tile is not the Gazette stand:\n%s", program)
	}
	if !strings.Contains(program, ":touch") {
		t.Errorf("the stand has no :touch label, so nothing can ever open it:\n%s", program)
	}
	// The trap this test exists for: a one-line body is DisplayMessage, not a
	// ScrollEvent, and the whole notice path hangs on the event.
	body := strings.SplitN(program, ":touch", 2)[1]
	text := 0
	for _, line := range strings.Split(strings.TrimSpace(body), "\r") {
		line = strings.TrimSpace(line)
		if line == "" || strings.ContainsAny(line[:1], "@#:'/?$!") {
			continue
		}
		text++
	}
	if text < 2 {
		t.Errorf("the stand's fallback body is %d text lines; a one-line body is a message, not a scroll:\n%s", text, body)
	}
}

// A world with no notice table is every world but the lobby. Its objects keep
// their own windows: the suppression is the server's, not the element's.
func TestM343AWorldWithNoNoticeTableShowsTheStandsOwnWindow(t *testing.T) {
	rm := NewRoomManager(m291LobbyWorld(t))
	rm.NoticeTiles = nil
	tile := m343NoticeTile(t)
	player := rm.JoinPlayer(tile.BoardID, tile.X, tile.Y-1)

	title, seen := m343StepUntilScroll(rm, player)

	if notices := rm.DrainNotices(); len(notices) != 0 {
		t.Fatalf("a world with no notice table queued notices: %+v", notices)
	}
	if !seen || title != "The ZZT Gazette" {
		t.Fatalf("the stand's own window never reached the reader (title %q, seen %v)", title, seen)
	}
}

// The other half of the pair, and the claim the whole mechanism rests on: with
// the table installed, the world's own window reaches nobody. It is asserted
// here rather than through the socket because a room event travels inside a
// diff, and "the server's scroll arrived" would not notice a second one riding
// underneath it.
func TestM343ANoticeTileSuppressesTheWorldsOwnWindow(t *testing.T) {
	tile := m343NoticeTile(t)
	rm := NewRoomManager(m291LobbyWorld(t))
	rm.NoticeTiles = map[TransitGateKey]string{tile: GazetteNoticeKind}
	player := rm.JoinPlayer(tile.BoardID, tile.X, tile.Y-1)

	title, seen := m343StepUntilScroll(rm, player)
	if seen {
		t.Errorf("the world's own window was broadcast anyway (title %q); the reader would read it and then be covered by the paper", title)
	}
	notices := rm.DrainNotices()
	if len(notices) != 1 || notices[0].Kind != GazetteNoticeKind {
		t.Fatalf("notices = %+v, want exactly one Gazette notice", notices)
	}
	if notices[0].ObjectStatID <= 0 {
		t.Errorf("the notice names object stat %d; without it the pushed scroll cannot be dismissed", notices[0].ObjectStatID)
	}
	if !rm.players[player].scrollOpen {
		t.Error("the reader is not frozen; a suppressed window must still stop the player who opened it")
	}
	// An object that walks off the tile stops being the notice: the tile is the
	// server's, not the object's.
	if _, ok := rm.noticeKindFor(rm.rooms[tile.BoardID], tile.BoardID, notices[0].ObjectStatID+1); ok {
		t.Error("a stat that is not standing on the notice tile was treated as the notice")
	}
}

// m343StepUntilScroll walks the player south into the stand and returns the
// title of the first scroll event the room broadcast.
func m343StepUntilScroll(rm *RoomManager, playerID PlayerID) (string, bool) {
	for i := 0; i < 6; i++ {
		diffs := rm.StepDiffs(map[PlayerID]PlayerInput{playerID: {DeltaY: 1, Key: KEY_DOWN}})
		for _, event := range diffs[playerID].Events {
			if event.Type == "scroll" {
				return event.Title, true
			}
		}
	}
	return "", false
}

// ---------------------------------------------------------------------------
// The touch
// ---------------------------------------------------------------------------

// The journey the product is: a player walks up to the stand in the shipped
// lobby, reads today's paper, and walks away again.
func TestM343TouchingTheStandPostsTheDayAndSuppressesTheWorldsOwnWindow(t *testing.T) {
	server, ledger := m343LobbyServer(t)
	if err := server.ChatDB.PutAccountPreferences("google:ada", AccountPreferences{
		Profile: AccountProfilePreferences{DisplayName: "Ada L"},
	}); err != nil {
		t.Fatalf("PutAccountPreferences: %v", err)
	}
	m341Record(t, ledger, GazetteKindDream, "TOWN", "google:ada")
	m341Record(t, ledger, GazetteKindDeath, "CAVES", "google:bo")

	httpServer := httptestServer(t, server)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, snap := dialJoinWithCookie(t, ctx,
		"ws"+strings.TrimPrefix(httpServer.URL, "http"),
		JoinMessage{Type: MessageTypeJoin, Name: "Reader", Board: 1}, nil)
	defer conn.Close(websocket.StatusNormalClosure, "")

	inst := server.DefaultInstance
	m343WalkToTheStand(t, ctx, server, inst, snap.You.ID, conn)

	event := m291ReadEvent(t, ctx, conn, "scroll")
	if event.Title != "THE ZZT GAZETTE, 2026-08-10" {
		t.Errorf("scroll title = %q, want the day's headline", event.Title)
	}
	body := strings.Join(event.Lines, "\n")
	if strings.Contains(body, "Nobody is printing a paper") {
		t.Errorf("the stand's fallback window was posted over the real paper:\n%s", body)
	}
	if !strings.Contains(body, "Ada L dreamed up TOWN.") {
		t.Errorf("Ada consented to a name and is not in the paper:\n%s", body)
	}
	// "a stranger" is capitalized by the renderer when it lands at a line start,
	// which is the renderer doing its job rather than a name appearing.
	if !strings.Contains(strings.ToLower(body), gazetteUnnamedActor+" died in caves.") {
		t.Errorf("an account with no profile must read as a stranger:\n%s", body)
	}
	if !strings.Contains(body, "The ZZT Gazette, 2026-08-10") {
		t.Errorf("the dateline is missing:\n%s", body)
	}
	if strings.Contains(body, "google:") || strings.Contains(body, "[P") {
		t.Errorf("the window leaks server-side state:\n%s", body)
	}
	// The pushed scroll is a touch, not an announcement: it names the reader,
	// and it names the object so the dismissal below can land.
	if event.PlayerStatID < 0 {
		t.Errorf("the scroll has no reader (playerStatId %d), so every screen on the board would show it", event.PlayerStatID)
	}
	if event.StatID <= 0 {
		t.Errorf("the scroll names no object (statId %d), so dismissing it would never unfreeze the reader", event.StatID)
	}

	// Frozen while reading, moving again once it is dismissed — through the
	// ordinary reply, which is the point of carrying the object's stat id.
	if !m343ScrollOpen(server, inst, snap.You.ID) {
		t.Fatal("the reader is not frozen; a scroll on screen must stop the player")
	}
	if err := wsjson.Write(ctx, conn, ScrollReplyMessage{
		Type:     MessageTypeScrollReply,
		PlayerID: snap.You.ID,
		StatID:   event.StatID,
	}); err != nil {
		t.Fatalf("write scroll reply: %v", err)
	}
	waitFor(t, "the reader is unfrozen", func() bool {
		return !m343ScrollOpen(server, inst, snap.You.ID)
	})
}

// A server with no ledger still owes the reader a window: the step that queued
// the notice already froze them, and a frozen player with no scroll on screen
// is a player who cannot move.
func TestM343ATouchWithNoLedgerStillAnswersTheReader(t *testing.T) {
	server, _ := m343LobbyServer(t)
	server.Gazette = nil

	httpServer := httptestServer(t, server)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, snap := dialJoinWithCookie(t, ctx,
		"ws"+strings.TrimPrefix(httpServer.URL, "http"),
		JoinMessage{Type: MessageTypeJoin, Name: "Reader", Board: 1}, nil)
	defer conn.Close(websocket.StatusNormalClosure, "")

	m343WalkToTheStand(t, ctx, server, server.DefaultInstance, snap.You.ID, conn)
	event := m291ReadEvent(t, ctx, conn, "scroll")
	if !strings.Contains(strings.Join(event.Lines, "\n"), "Nobody is printing a paper") {
		t.Fatalf("a server with no ledger posted %+v, want the honest empty stand", event)
	}
	if event.StatID <= 0 {
		t.Errorf("even the empty window must name the object, or the reader stays frozen: %+v", event)
	}
}

// ---------------------------------------------------------------------------
// One editor
// ---------------------------------------------------------------------------

// m343Author is a counting author safe to read while a refresh is in flight.
type m343Author struct {
	mu    sync.Mutex
	reply string
	calls int
}

func (a *m343Author) WriteGazetteEdition(_ context.Context, _, _ string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	return a.reply, nil
}

func (a *m343Author) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

// Two editors would be two caches racing one file and two DailyWrites budgets —
// which is the one spend bound a restart does not forgive. The board and the
// route must be the same editor.
func TestM343TheBoardAndTheRouteShareOneEditor(t *testing.T) {
	server, ledger := m343LobbyServer(t)
	m341Record(t, ledger, GazetteKindDream, "TOWN", "google:ada")

	// M34.2's fake author is written by the refresh goroutine and read here, so
	// this one counts under a lock: the board's refresh is asynchronous by
	// design and a test that races it is a test that reports a race.
	author := &m343Author{reply: m342GoodReply}
	editor, err := NewGazetteEditor(ledger, author, "")
	if err != nil {
		t.Fatalf("NewGazetteEditor: %v", err)
	}
	editor.DailyWrites = 1
	server.gazetteEditor = editor

	api := &WebAPI{RoomManager: server.RoomManager, Server: server}
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/gazette/edition", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/gazette/edition = %d: %s", rec.Code, rec.Body.String())
	}
	if api.gazetteEditor() != editor {
		t.Fatal("the route built its own editor instead of the server's; the day's budget is now two budgets")
	}
	if got := server.GazetteEditions(nil); got != editor {
		t.Fatal("the board built its own editor instead of the server's")
	}

	// The route already spent the day's single write. The board asking again
	// must not buy a second one.
	waitFor(t, "the route's refresh finishes", func() bool { return author.count() == 1 })
	server.postNotice(context.Background(), server.DefaultInstance, RoomNotice{
		PlayerID: 1, Kind: GazetteNoticeKind, ObjectStatID: 4,
	})
	if got := author.count(); got != 1 {
		t.Fatalf("author calls after the board read the paper = %d, want the day's cap of 1", got)
	}
}


// The identity assertions above are the accessors' claim; this is the board's.
// Seeding the shared editor with an edition an AUTHOR wrote makes the two
// distinguishable at the window: a postNotice that built its own editor would
// have no author, and would post the server's fallback headline instead.
func TestM343TheBoardPostsTheSharedEditorsEdition(t *testing.T) {
	server, ledger := m343LobbyServer(t)
	m341Record(t, ledger, GazetteKindDream, "TOWN", "google:ada")
	editor, err := NewGazetteEditor(ledger, &m343Author{reply: m342GoodReply}, "")
	if err != nil {
		t.Fatalf("NewGazetteEditor: %v", err)
	}
	if err := editor.Refresh(context.Background(), ""); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	server.gazetteEditor = editor

	httpServer := httptestServer(t, server)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, snap := dialJoinWithCookie(t, ctx,
		"ws"+strings.TrimPrefix(httpServer.URL, "http"),
		JoinMessage{Type: MessageTypeJoin, Name: "Reader", Board: 1}, nil)
	defer conn.Close(websocket.StatusNormalClosure, "")

	m343WalkToTheStand(t, ctx, server, server.DefaultInstance, snap.You.ID, conn)
	event := m291ReadEvent(t, ctx, conn, "scroll")
	if event.Title != "A QUIET DAY, MOSTLY" {
		t.Fatalf("the stand posted %q, want the headline the shared editor bought", event.Title)
	}
}

// ---------------------------------------------------------------------------
// What the board adds
// ---------------------------------------------------------------------------

// The edition's own lines are already wrapped and already guarded (M34.2). This
// asserts the board does not undo that with what it adds around them.
func TestM343TheWindowFitsTheTextWindowAndNeverStartsWithMarkup(t *testing.T) {
	long := make([]string, gazetteEditionMaxRenderedLines)
	for i := range long {
		long[i] = strings.Repeat("x", gazetteEditionStoryWidth)
	}
	title, lines := gazetteBoardWindow(GazetteRenderedEdition{
		Day:      "2026-08-10",
		Headline: strings.Repeat("H", gazetteEditionHeadlineWidth),
		Lines:    long,
		Source:   GazetteEditionSourceServer,
	})
	if len(title) > zztTextWindowTitleMax {
		t.Errorf("title is %d characters, want at most %d", len(title), zztTextWindowTitleMax)
	}
	if len(lines) > gazetteBoardMaxLines {
		t.Errorf("the window is %d lines, want at most %d", len(lines), gazetteBoardMaxLines)
	}
	for _, line := range lines {
		if len(line) > zztTextWindowLineWidth {
			t.Errorf("line is %d characters, want at most %d: %q", len(line), zztTextWindowLineWidth, line)
		}
		if line != "" && strings.ContainsAny(line[:1], gazetteOOPLeadingBytes) {
			t.Errorf("line begins with OOP markup: %q", line)
		}
	}
	// An empty day still has a title, because a window with none is untitled
	// rather than honest.
	emptyTitle, emptyLines := gazetteBoardWindow(GazetteRenderedEdition{Day: "2026-08-10"})
	if emptyTitle == "" || len(emptyLines) == 0 {
		t.Errorf("an empty edition rendered %q / %v", emptyTitle, emptyLines)
	}
}

// The notice is derived from the simulation and nothing about the paper is
// recorded, so a replay reproduces the freeze and the unfreeze without ever
// seeing an edition. This is the M34 preamble's first boundary, asserted where
// it would break: the ledger is not the simulation.
func TestM343TheBoardMovesNoSimulationState(t *testing.T) {
	tile := m343NoticeTile(t)

	withTable := NewRoomManager(m291LobbyWorld(t))
	withTable.NoticeTiles = map[TransitGateKey]string{tile: GazetteNoticeKind}
	withoutTable := NewRoomManager(m291LobbyWorld(t))

	a := withTable.JoinPlayer(tile.BoardID, tile.X, tile.Y-1)
	b := withoutTable.JoinPlayer(tile.BoardID, tile.X, tile.Y-1)
	for i := 0; i < 3; i++ {
		withTable.StepDiffs(map[PlayerID]PlayerInput{a: {DeltaY: 1, Key: KEY_DOWN}})
		withoutTable.StepDiffs(map[PlayerID]PlayerInput{b: {DeltaY: 1, Key: KEY_DOWN}})
	}
	if len(withTable.DrainNotices()) == 0 {
		t.Fatal("the notice table produced no notice; this test is not testing anything")
	}
	got := StateHash(withTable.rooms[tile.BoardID].Engine)
	want := StateHash(withoutTable.rooms[tile.BoardID].Engine)
	if got != want {
		t.Fatalf("the notice table moved the simulation: hash %v with the table, %v without", got, want)
	}
}

// ---------------------------------------------------------------------------
// The board, in a real browser
// ---------------------------------------------------------------------------

// The half of the claim only a browser can make: the window that appears is a
// ZZT text window a player reads and dismisses, the copy in it is the server's
// rather than the stand's own, and the reader is frozen while it is open.
func TestM343GazetteBoardBrowserJourney(t *testing.T) {
	m169RequireBrowserHarness(t)

	lobbyBytes := m291LobbyWorldBytes(t)
	welcomeBytes := m232WelcomeWorldBytes(t)

	m169RequireClientBuild(t)
	binPath := getM1619ServerBinary(t)

	rootDir := t.TempDir()
	webDir, err := filepath.Abs(m169ClientDir())
	if err != nil {
		t.Fatalf("resolve %s: %v", m169ClientDir(), err)
	}
	savesDir := filepath.Join(rootDir, "saves")
	worldsDir := filepath.Join(rootDir, "worlds")
	for _, dir := range []string{savesDir, worldsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	for _, dir := range []string{worldsDir, rootDir} {
		for name, data := range map[string][]byte{
			"LOBBY.ZZT":   lobbyBytes,
			"WELCOME.ZZT": welcomeBytes,
		} {
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
				t.Fatalf("write %s into %s: %v", name, dir, err)
			}
		}
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	cmd := exec.Command(binPath,
		"-world", LobbyWorldName,
		"-addr", addr,
		"-web", webDir,
		"-saves", savesDir,
		"-worlds", worldsDir,
		"-help", ".",
		"-shutdown-grace", "0s",
		"-fresh",
	)
	cmd.Dir = rootDir
	var logBuf bytes.Buffer
	cmd.Stdout = &logBuf
	cmd.Stderr = &logBuf
	if err := cmd.Start(); err != nil {
		t.Fatalf("start zzt-server: %v", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGKILL)
			_ = cmd.Wait()
		}
	}()

	baseURL := "http://" + addr
	ready := false
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		resp, err := http.Get(baseURL + "/api/worlds")
		if err == nil && resp.StatusCode == http.StatusOK {
			_ = resp.Body.Close()
			ready = true
			break
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("server on %s failed to become ready. Logs:\n%s", addr, logBuf.String())
	}

	nodeCmd := exec.Command("node", filepath.Join("test", "gazette_journey.test.mjs"))
	nodeCmd.Dir = "web"
	nodeCmd.Env = append(os.Environ(), "BASE_URL="+baseURL)
	out, err := nodeCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gazette browser journey failed: %v\n--- script output ---\n%s\n--- server log ---\n%s",
			err, string(out), logBuf.String())
	}
	t.Logf("gazette browser journey output:\n%s", string(out))
}
