package zztgo

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// M36.27e: a dialogue line that already fits is the author's exact bytes.
// Reflowing it through strings.Fields collapsed its spaces, so ASCII art in a
// scroll compiled flat, silently.
func TestM3627ZWDKeepsLinesThatAlreadyFit(t *testing.T) {
	art := []string{
		"   /\\_/\\",
		"  ( o.o )   meow",
		"$  centered   with  gaps  ",
		"!pet;  Pet  the   cat",
	}
	got, err := wrapZWDTextWindowLines(strings.Join(art, "\r"))
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.Join(art, "\r") {
		t.Fatalf("lines within the limit were rewritten:\n got %q\nwant %q", strings.Split(got, "\r"), art)
	}

	// An overlong line is still wrapped to the window.
	long := strings.Repeat("word ", 20)
	got, err = wrapZWDTextWindowLines(long)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(got, "\r") {
		if len(line) > zztTextWindowLineWidth {
			t.Fatalf("an overlong line was not wrapped: %q", line)
		}
	}
}

// m3627WorldWithProgram compiles the one-room example and replaces its first
// object's program with data, returning the world.
func m3627WorldWithProgram(t *testing.T, data string) TWorld {
	t.Helper()
	world, err := CompileZWDWorld(zwdOneRoomExample)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine()
	e.Headless = true
	e.World = world
	for b := int16(0); b <= e.World.BoardCount; b++ {
		e.BoardOpen(b)
		for i := int16(1); i <= e.Board.StatCount; i++ {
			if e.Board.Stats[i].DataLen > 0 {
				e.Board.Stats[i].Data = data
				e.Board.Stats[i].DataLen = int16(len(data))
				e.BoardClose()
				return e.World
			}
		}
	}
	t.Fatal("the example world has no object with a program")
	return TWorld{}
}

// m3627FirstProgram compiles src and returns the first stat program it holds.
func m3627FirstProgram(t *testing.T, src string) string {
	t.Helper()
	world, err := CompileZWDWorld(src)
	if err != nil {
		t.Fatalf("recompile: %v\n%s", err, src)
	}
	e := NewEngine()
	e.Headless = true
	e.World = world
	for b := int16(0); b <= e.World.BoardCount; b++ {
		e.BoardOpen(b)
		for i := int16(1); i <= e.Board.StatCount; i++ {
			if e.Board.Stats[i].DataLen > 0 {
				return e.Board.Stats[i].Data
			}
		}
	}
	t.Fatal("no program after recompile")
	return ""
}

// M36.27e: decompile -> compile keeps an object's program byte for byte when
// its lines are indented. The decompiler wrote them at column 0 under an
// indented `oop`, and the parser strips that indent, so four leading spaces
// were lost on the way back in, silently.
func TestM3627DecompiledProgramKeepsIndentation(t *testing.T) {
	program := strings.Join([]string{"@cat", "    Indented  dialogue", "  two", "#end", ""}, "\r")
	world := m3627WorldWithProgram(t, program)
	src, diagnostics := DecompileZWDAuthorable(&world)
	if src == "" {
		t.Fatalf("decompile refused the world: %#v", diagnostics)
	}
	if got := m3627FirstProgram(t, src); got != program {
		t.Fatalf("program after decompile/compile:\n got %q\nwant %q", got, program)
	}
}

// M36.27e: a program line reading "end" closes a ZWD oop block wherever it is
// indented, so it cannot be written. The decompiler now refuses it by name;
// it used to emit the source and fail later on an unrelated-looking parse.
func TestM3627DecompileRefusesAProgramLineReadingEnd(t *testing.T) {
	program := strings.Join([]string{"@cat", "  end", "#end", ""}, "\r")
	world := m3627WorldWithProgram(t, program)
	src, diagnostics := DecompileZWDAuthorable(&world)
	if src != "" {
		t.Fatal("decompile emitted source for a program it cannot represent")
	}
	named := false
	for _, d := range diagnostics {
		if d.Severity == "error" && strings.Contains(d.Message, "program line 2") {
			named = true
		}
	}
	if !named {
		t.Fatalf("diagnostics do not name the offending line: %#v", diagnostics)
	}
}

// M36.27e: the compiler refuses what it would otherwise drop. A board message
// over the 58 bytes the board info stores was cut silently by StoreString, and
// a legend `to "BOARD"` naming no board compiled as if it were fine.
func TestM3627ZWDRefusesWhatItWouldTruncateOrIgnore(t *testing.T) {
	withMessage := func(msg string) string {
		return strings.Replace(zwdOneRoomExample, "  time-limit 0\n", "  time-limit 0\n  message \""+msg+"\"\n", 1)
	}
	if _, err := CompileZWD(withMessage(strings.Repeat("m", SizeOfBoardInfoMessage-1))); err != nil {
		t.Fatalf("a %d-byte message must compile: %v", SizeOfBoardInfoMessage-1, err)
	}
	if _, err := CompileZWD(withMessage(strings.Repeat("m", SizeOfBoardInfoMessage))); err == nil ||
		!strings.Contains(err.Error(), "message") {
		t.Errorf("a %d-byte message compiled (err %v); it would be cut to %d", SizeOfBoardInfoMessage, err, SizeOfBoardInfoMessage-1)
	}

	unresolved := strings.Replace(zwdOneRoomExample, "legend\n", "legend\n  p = Passage color 0x2F to \"Nowhere\"\n", 1)
	if unresolved == zwdOneRoomExample {
		t.Fatal("could not add a legend entry to the example")
	}
	if _, err := CompileZWD(unresolved); err == nil || !strings.Contains(err.Error(), "Nowhere") {
		t.Errorf("a legend `to` naming no board compiled (err %v)", err)
	}
}

// M36.27e: decompile keeps a turning conveyor's stat. Conveyors may carry a
// stat (elementMayHaveStat) and turn only when they do, but the decompiler
// asked elementNeedsStat and dropped it, so the recompiled conveyor froze.
func TestM3627DecompileKeepsConveyorStats(t *testing.T) {
	world, err := CompileZWDWorld(zwdOneRoomExample)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine()
	e.Headless = true
	e.World = world
	e.BoardOpen(0)
	e.Board.Tiles[5][5] = TTile{Element: E_CONVEYOR_CW, Color: 0x0F}
	e.AddStat(5, 5, E_CONVEYOR_CW, 0x0F, 3, TStat{Follower: -1, Leader: -1})
	e.BoardClose()

	src, diagnostics := DecompileZWDAuthorable(&e.World)
	if src == "" {
		t.Fatalf("decompile refused: %#v", diagnostics)
	}
	back, err := CompileZWDWorld(src)
	if err != nil {
		t.Fatal(err)
	}
	r := NewEngine()
	r.Headless = true
	r.World = back
	r.BoardOpen(0)
	if r.GetStatIdAt(5, 5) < 0 {
		t.Fatalf("the conveyor at 5,5 lost its stat in decompile (diagnostics %#v)", diagnostics)
	}
}

// M36.27e: two boards with one name both stay reachable. The compiler binds a
// name to its first board, so a link to the second came back pointing at the
// first after decompile -> compile.
func TestM3627DecompileKeepsLinksToDuplicateBoardNames(t *testing.T) {
	world, err := CompileZWDWorld(zwdTwoRoomsExample)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine()
	e.Headless = true
	e.World = world
	// Append a copy of board 1 as board 2, under board 1's name, and point
	// board 1's east exit at it.
	e.World.BoardCount++
	last := e.World.BoardCount
	e.World.BoardData[last] = append([]byte(nil), e.World.BoardData[1]...)
	e.World.BoardLen[last] = e.World.BoardLen[1]
	e.BoardOpen(1)
	firstName := e.Board.Name
	e.Board.Info.NeighborBoards[3] = byte(last) // east
	e.BoardClose()
	e.BoardOpen(last)
	e.Board.Name = firstName
	e.BoardClose()

	src, diagnostics := DecompileZWDAuthorable(&e.World)
	if src == "" {
		t.Fatalf("decompile refused: %#v", diagnostics)
	}
	back, err := CompileZWDWorld(src)
	if err != nil {
		t.Fatalf("recompile: %v", err)
	}
	r := NewEngine()
	r.Headless = true
	r.World = back
	r.BoardOpen(1)
	if got := r.Board.Info.NeighborBoards[3]; got != byte(last) {
		t.Fatalf("board 1's east exit leads to board %d after the round trip, want %d", got, last)
	}
}

// M36.27: postcards are paced per player, not per proxy. Behind Caddy every
// request arrives from 127.0.0.1, so a key taken from RemoteAddr made the
// per-client limit one global limit any single visitor could exhaust.
func TestM3627PostcardLimitIsPerPlayerBehindTheProxy(t *testing.T) {
	req := func(remote, forwarded string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/replay/postcard.gif", nil)
		r.RemoteAddr = remote
		if forwarded != "" {
			r.Header.Set("X-Forwarded-For", forwarded)
		}
		return r
	}
	a := postcardClientKey(req("127.0.0.1:5000", "203.0.113.7"))
	b := postcardClientKey(req("127.0.0.1:5001", "198.51.100.9"))
	if a == b {
		t.Fatalf("two players behind the proxy share the postcard key %q", a)
	}
	// A header from a client that reached the server directly is not trusted.
	if got := postcardClientKey(req("203.0.113.7:9", "10.0.0.1")); got != "203.0.113.7" {
		t.Fatalf("a direct client's forged X-Forwarded-For was honoured: key %q", got)
	}
}

// M36.27: neither the postcard limiter nor the postcard cache grows without
// bound. The limiter kept a slice for every client it ever saw; the cache kept
// a GIF for every distinct (id, start, ticks, board) ever requested.
func TestM3627PostcardStateIsBounded(t *testing.T) {
	var l postcardRateLimiter
	start := time.Unix(0, 0)
	for i := 0; i < 5000; i++ {
		l.allow(fmt.Sprintf("client-%d", i), start.Add(time.Duration(i)*time.Second))
	}
	l.mu.Lock()
	held := len(l.accepted)
	l.mu.Unlock()
	if held > 2*int(postcardRateWindow/time.Second)+postcardRateMax {
		t.Fatalf("the limiter holds %d clients after 5000 one-off visitors across 5000s; expired ones must go", held)
	}

	var c replayPostcardCache
	for i := 0; i < 10*postcardCacheMax; i++ {
		c.put(replayPostcardKey{ID: "r", Start: i}, ReplayPostcard{GIF: []byte{1}})
	}
	c.mu.Lock()
	cached := len(c.items)
	c.mu.Unlock()
	if cached > postcardCacheMax {
		t.Fatalf("the postcard cache holds %d entries, want at most %d", cached, postcardCacheMax)
	}
}

// m3627RacingDB is a preferences store with a second writer, a concurrent
// /api/preferences storing the account's Color, that lands at the caller's
// first touch of the store. After a Get it lands between the read and the
// write, so a read-modify-write caller writes back the document it read,
// without that Color. An atomic update has no such gap: the writer lands
// before it, and the update sees the Color.
type m3627RacingDB struct {
	*MemChatDatabase
	account string
	raced   bool
}

func (db *m3627RacingDB) GetAccountPreferences(accountID string) (AccountPreferences, bool, error) {
	prefs, ok, err := db.MemChatDatabase.GetAccountPreferences(accountID)
	if accountID == db.account && !db.raced {
		db.raced = true
		concurrent := prefs
		concurrent.Color = "#ff00ff"
		if err := db.MemChatDatabase.PutAccountPreferences(accountID, concurrent); err != nil {
			return prefs, ok, err
		}
	}
	return prefs, ok, err
}

func (db *m3627RacingDB) UpdateAccountPreferences(accountID string, change func(*AccountPreferences, bool) error) error {
	if accountID == db.account && !db.raced {
		db.raced = true
		concurrent, _, _ := db.MemChatDatabase.GetAccountPreferences(accountID)
		concurrent.Color = "#ff00ff"
		if err := db.MemChatDatabase.PutAccountPreferences(accountID, concurrent); err != nil {
			return err
		}
	}
	return db.MemChatDatabase.UpdateAccountPreferences(accountID, change)
}

// M36.27: a preferences write changes its own field of the stored document and
// nothing else. Persisting a block read the whole document, set
// BlockedAccounts, and wrote the whole document back, so a colour chosen in
// between was lost.
func TestM3627PreferenceWritesDoNotLoseAConcurrentChange(t *testing.T) {
	const account = "google:ada"
	db := &m3627RacingDB{MemChatDatabase: NewMemChatDatabase(), account: account}
	if err := db.MemChatDatabase.PutAccountPreferences(account, AccountPreferences{}); err != nil {
		t.Fatal(err)
	}
	s := NewWebSocketServer(townWorld(t), 1)
	s.ChatDB = db
	s.persistBlockedAccounts(account, PlayerID(1))

	prefs, _, err := db.MemChatDatabase.GetAccountPreferences(account)
	if err != nil {
		t.Fatal(err)
	}
	if prefs.Color != "#ff00ff" {
		t.Fatalf("persisting a block wrote back a stale document: the concurrent Color is %q, want #ff00ff", prefs.Color)
	}
}
