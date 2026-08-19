package zztgo // unit: the Gazette newsstand mechanism, without a world to host it

// M34.3 built a newsstand: a server-owned tile that, when an object standing on
// it is touched, swallows the world's own scroll and posts the day's edition
// instead. The LOBBY world that stood the stand was deleted on 2026-08-11 and
// took the whole M34.3 suite with it, because every one of those tests reached
// the mechanism through that world's newsstand.
//
// The mechanism itself was kept: RoomManager.NoticeTiles is still read on every
// touch, websocket_server.go still dispatches postNotice for every drained
// notice, and gazetteBoardWindow still renders the edition into a text window.
// Nothing populates NoticeTiles in production today, so these tests are the
// only thing standing between that code and silent rot.
//
// The world below is built here rather than shipped: it is a test fixture, not
// a world anybody can join. Its newsstand object — the tile, the cycle, the
// character and the two-line fallback program — is copied verbatim from the
// deleted fixtures/lobby.zwd, because the fallback body has to be long enough
// to be a scroll rather than a one-line DisplayMessage (oop.go's LineCount == 1
// branch), and that is a ZZT-OOP fact rather than something to re-derive.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const gazetteStandWorldSource = `zwd 1
world "STAND"

board "Stand Title"
  start player at 30,13
  max-shots 10
  dark false
  reenter false
  time-limit 0
  exits north none south none west none east none

  grid
############################################################
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#............................@.............................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
############################################################
  end

  legend
    # = Solid color 0x01
    . = Empty color 0x00
    @ = Player color 0x1F under Empty color 0x00
  end
end

board "Central Hall"
  start player at 30,13
  max-shots 10
  dark false
  reenter false
  time-limit 0
  exits north none south none west none east none

  grid
############################################################
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#............................@.............................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#...........n..............................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
############################################################
  end

  legend
    # = Solid color 0x01
    . = Empty color 0x00
    @ = Player color 0x1F under Empty color 0x00
    n = Object color 0x0F
  end

  stats
    stat at 13,20 element Object cycle 3 p1 cp437:0xF0 under Empty color 0x00
    oop
@The ZZT Gazette
#end
:touch
The ZZT Gazette
Nobody is printing a paper
here today.
#end
    end
  end
end
`

// gazetteStandTile is the tile the stand occupies, in the coordinates
// NoticeTiles is keyed by. It matches the stat declared in the world above.
var gazetteStandTile = TransitGateKey{BoardID: 1, X: 13, Y: 20}

func gazetteStandWorld(t *testing.T) TWorld {
	t.Helper()
	world, err := CompileZWDWorld(gazetteStandWorldSource)
	if err != nil {
		t.Fatalf("CompileZWDWorld(gazetteStandWorldSource): %v", err)
	}
	return world
}

// gazetteStepUntilScroll walks the player south into the stand and returns the
// title of the first scroll event the room broadcast.
func gazetteStepUntilScroll(rm *RoomManager, playerID PlayerID) (string, bool) {
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

// The control: with no notice table the stand is an ordinary ZZT object and its
// own window reaches the reader, which is what makes the suppression below mean
// anything at all.
func TestGazetteNoNoticeTableShowsTheStandsOwnWindow(t *testing.T) {
	rm := NewRoomManager(gazetteStandWorld(t))
	rm.NoticeTiles = nil
	player := rm.JoinPlayer(gazetteStandTile.BoardID, gazetteStandTile.X, gazetteStandTile.Y-1)

	title, seen := gazetteStepUntilScroll(rm, player)

	if notices := rm.DrainNotices(); len(notices) != 0 {
		t.Fatalf("a world with no notice table queued notices: %+v", notices)
	}
	if !seen || title != "The ZZT Gazette" {
		t.Fatalf("the stand's own window never reached the reader (title %q, seen %v)", title, seen)
	}
}

// The claim the whole mechanism rests on: with the table installed, the world's
// own window reaches nobody. It is asserted here rather than through a socket
// because a room event travels inside a diff, and "the server's scroll arrived"
// would not notice a second one riding underneath it.
func TestGazetteNoticeTileSuppressesTheWorldsOwnWindow(t *testing.T) {
	tile := gazetteStandTile
	rm := NewRoomManager(gazetteStandWorld(t))
	rm.NoticeTiles = map[TransitGateKey]string{tile: GazetteNoticeKind}
	player := rm.JoinPlayer(tile.BoardID, tile.X, tile.Y-1)

	title, seen := gazetteStepUntilScroll(rm, player)
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

// The notice is derived from the simulation and nothing about the paper is
// recorded, so a replay reproduces the freeze and the unfreeze without ever
// seeing an edition. The ledger is not the simulation, asserted where it would
// break.
func TestGazetteBoardMovesNoSimulationState(t *testing.T) {
	tile := gazetteStandTile

	withTable := NewRoomManager(gazetteStandWorld(t))
	withTable.NoticeTiles = map[TransitGateKey]string{tile: GazetteNoticeKind}
	withoutTable := NewRoomManager(gazetteStandWorld(t))

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

// The edition's own lines are already wrapped and already guarded (M34.2). This
// asserts the board does not undo that with what it adds around them.
func TestGazetteBoardWindowFitsTheTextWindowAndNeverStartsWithMarkup(t *testing.T) {
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

// gazetteCountingAuthor is a counting author safe to read while a refresh is in
// flight.
type gazetteCountingAuthor struct {
	mu    sync.Mutex
	reply string
	calls int
}

func (a *gazetteCountingAuthor) WriteGazetteEdition(_ context.Context, _, _ string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	return a.reply, nil
}

func (a *gazetteCountingAuthor) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

// Two editors would be two caches racing one file and two DailyWrites budgets —
// which is the one spend bound a restart does not forgive. The board and the
// route must be the same editor.
func TestGazetteBoardAndRouteShareOneEditor(t *testing.T) {
	server := NewWebSocketServer(gazetteStandWorld(t), 1)
	server.ChatDB = NewMemChatDatabase()
	ledger, _ := m341Ledger(t, "")
	server.Gazette = ledger
	m341Record(t, ledger, GazetteKindDream, "TOWN", "google:ada")

	// M34.2's fake author is written by the refresh goroutine and read here, so
	// this one counts under a lock: the board's refresh is asynchronous by
	// design and a test that races it is a test that reports a race.
	author := &gazetteCountingAuthor{reply: m342GoodReply}
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
