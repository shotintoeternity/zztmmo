package zztgo

// M21.4 — a returning player is told who they have blocked.
//
// M21.1 made blocks durable for a signed-in player and enforced them from the
// moment they join, but the client only ever learned of a block by MAKING one:
// its mirror started empty on every connection, so the Players window showed a
// block made last week as unmarked. Nothing was broken — blocking twice is
// idempotent, and the enforcement was server-side either way — but the window
// misreported state the server knew.
//
// The three things these tests exist to stop:
//   1. the first frame arriving with nothing to say about blocks the server is
//      already enforcing (the feature)
//   2. the answer being computed from the RAW stored list, which is a set of
//      account ids the recipient has no other way to see — a block is silent,
//      and another player's account is not theirs to learn
//   3. the answer being computed before the returning player's stored blocks are
//      loaded, which is right only for a player who has never blocked anybody
//      and is therefore the inversion a careless refactor reaches for

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// m214JoinRaw is dialJoinWithCookie keeping the bytes as well as the message.
// The bytes are the point of one of the claims below: the field this task adds
// is derived from a set of account ids, and the proof that none of them travels
// has to look at the frame rather than at the struct it was decoded into.
func m214JoinRaw(t *testing.T, ctx context.Context, wsURL string, join JoinMessage, cookie *http.Cookie) (*websocket.Conn, SnapshotMessage, []byte) {
	t.Helper()
	opts := &websocket.DialOptions{}
	if cookie != nil {
		opts.HTTPHeader = http.Header{"Cookie": []string{cookie.String()}}
	}
	conn, _, err := websocket.Dial(ctx, wsURL, opts)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	conn.SetReadLimit(ServerReadLimit)
	if err := wsjson.Write(ctx, conn, join); err != nil {
		t.Fatalf("write join: %v", err)
	}
	var raw json.RawMessage
	if err := wsjson.Read(ctx, conn, &raw); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	var snapshot SnapshotMessage
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	return conn, snapshot, raw
}

func m214HasID(ids []PlayerID, want PlayerID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// TestM214ReturningPlayerSeesTheirBlocksOnTheFirstFrame is the DoD, all three
// clauses, in one run — because the claim is a CONTRAST between three joiners
// arriving at the same room: the returning account is told, the account it never
// blocked is not named, and the guest is told nothing at all.
//
// The second server is a genuinely fresh one, with freshly minted PlayerIDs, so
// the only thing that can carry a block across is the stored account id. And the
// blocked player joins FIRST: a block on somebody who is not in the room yet is
// not something a roster-scoped answer can report, and pretending otherwise
// would be testing the wrong claim.
func TestM214ReturningPlayerSeesTheirBlocksOnTheFirstFrame(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chat.jsonl")
	db, err := NewFileChatDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewFileChatDatabase: %v", err)
	}
	auth := NewAuthService("client-id", "", "", []byte("m21-4-cookie-secret"))
	adaCookie := signedAuthCookie(t, auth, AuthenticatedAccount{ID: "google:ada", Email: "ada@example.test", Name: "Ada"})
	bobCookie := signedAuthCookie(t, auth, AuthenticatedAccount{ID: "google:bob", Email: "bob@example.test", Name: "Bob"})
	cyCookie := signedAuthCookie(t, auth, AuthenticatedAccount{ID: "google:cy", Email: "cy@example.test", Name: "Cy"})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// --- session 1: Ada blocks Bob, and nobody else -----------------------
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.ChatDB = db
	server.Auth = auth
	httpServer := httptest.NewServer(server)
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	ada, adaSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Board: 1}, adaCookie)
	bob, bobSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Board: 1}, bobCookie)
	if len(adaSnap.BlockedPlayers) != 0 {
		t.Fatalf("Ada blocks nobody yet, but her first frame said %v", adaSnap.BlockedPlayers)
	}
	m211Block(t, ctx, ada, bobSnap.You.ID, true)
	if result := m211ReadBlockResult(t, ctx, ada); !result.Durable {
		t.Fatalf("blocking a signed-in player must be durable, or the rest of this test proves nothing: %+v", result)
	}
	firstBobID := bobSnap.You.ID

	ada.Close(websocket.StatusNormalClosure, "")
	bob.Close(websocket.StatusNormalClosure, "")
	httpServer.Close()
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	// --- session 2: a fresh process, and Bob is already in the room -------
	reopened, err := NewFileChatDatabase(dbPath)
	if err != nil {
		t.Fatalf("reopen FileChatDatabase: %v", err)
	}
	defer reopened.Close()
	restarted := NewWebSocketServer(testEmptyWorld(t), 1)
	restarted.ChatDB = reopened
	restarted.Auth = auth
	httpServer2 := httptest.NewServer(restarted)
	defer httpServer2.Close()
	wsURL2 := "ws" + strings.TrimPrefix(httpServer2.URL, "http")

	bob2, bob2Snap := dialJoinWithCookie(t, ctx, wsURL2, JoinMessage{Type: MessageTypeJoin, Board: 1}, bobCookie)
	defer bob2.Close(websocket.StatusNormalClosure, "")
	cy2, cy2Snap := dialJoinWithCookie(t, ctx, wsURL2, JoinMessage{Type: MessageTypeJoin, Board: 1}, cyCookie)
	defer cy2.Close(websocket.StatusNormalClosure, "")
	if bob2Snap.You.ID == firstBobID {
		t.Fatalf("Bob was re-minted the same id %d; this test needs fresh ids for the account to be the only carrier",
			firstBobID)
	}

	ada2, ada2Snap, adaFrame := m214JoinRaw(t, ctx, wsURL2, JoinMessage{Type: MessageTypeJoin, Board: 1}, adaCookie)
	defer ada2.Close(websocket.StatusNormalClosure, "")

	// Clause 1: the row is marked on the FIRST frame, before Ada does anything.
	if !m214HasID(ada2Snap.BlockedPlayers, bob2Snap.You.ID) {
		t.Fatalf("Ada's first frame reported blocked=%v, want Bob's id %d — a block made last session must not show as unmarked",
			ada2Snap.BlockedPlayers, bob2Snap.You.ID)
	}
	// And only him: the list is an answer about people, not a dump of a set.
	if m214HasID(ada2Snap.BlockedPlayers, cy2Snap.You.ID) {
		t.Fatalf("Cy, whom Ada never blocked, was reported as blocked: %v", ada2Snap.BlockedPlayers)
	}
	if m214HasID(ada2Snap.BlockedPlayers, ada2Snap.You.ID) {
		t.Fatalf("Ada was reported as blocking herself: %v", ada2Snap.BlockedPlayers)
	}
	if len(ada2Snap.BlockedPlayers) != 1 {
		t.Fatalf("Ada's first frame reported %v, want exactly Bob", ada2Snap.BlockedPlayers)
	}

	// Clause 2: it is an answer about ids, and no account travelled to produce it.
	// Scanned over the whole frame rather than over the one field, because a
	// leak's whole nature is being somewhere nobody looked.
	if strings.Contains(string(adaFrame), "google:") {
		t.Fatalf("an account id reached a browser on the join snapshot: %s", adaFrame)
	}

	// Clause 3: a guest is unaffected. Nothing was seeded for them, so there is
	// nothing to report — and reporting somebody would mean the answer had come
	// from a bucket shared between connections.
	guest, guestSnap, guestFrame := m214JoinRaw(t, ctx, wsURL2,
		JoinMessage{Type: MessageTypeJoin, Name: "Gus", Board: 1}, nil)
	defer guest.Close(websocket.StatusNormalClosure, "")
	if len(guestSnap.BlockedPlayers) != 0 {
		t.Fatalf("a guest with no stored blocks was told %v", guestSnap.BlockedPlayers)
	}
	if strings.Contains(string(guestFrame), "blockedPlayers") {
		t.Fatalf("an empty answer must be omitted from the wire, not sent as an empty list: %s", guestFrame)
	}
}

// TestM214BlockedRosterIsScopedToWhoIsVisible pins the two edges the socket test
// above cannot force, directly on the helper: a block on somebody who is NOT in
// the roster is not smuggled into the answer, and a block made THIS session (on
// a guest, so it can never be durable) is reported just the same on a later
// frame. Both players are guests here, which is the case that has no stored
// document at all — the answer must come from the live set, not from the store.
func TestM214BlockedRosterIsScopedToWhoIsVisible(t *testing.T) {
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.ChatDB = NewMemChatDatabase()
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	ada, adaSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Ada", Board: 1}, nil)
	defer ada.Close(websocket.StatusNormalClosure, "")
	bob, bobSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Bob", Board: 1}, nil)
	defer bob.Close(websocket.StatusNormalClosure, "")

	m211Block(t, ctx, ada, bobSnap.You.ID, true)
	_ = m211ReadBlockResult(t, ctx, ada)

	inst := server.DefaultInstance
	inst.mu.Lock()
	roster, ok := inst.RoomManager.Snapshot(adaSnap.You.ID)
	inst.mu.Unlock()
	if !ok {
		t.Fatal("Ada is not in the room she just joined")
	}

	got := server.blockedInRoster(adaSnap.You.ID, inst, roster.Players)
	if len(got) != 1 || got[0] != bobSnap.You.ID {
		t.Fatalf("blockedInRoster=%v, want [%d] — a session block is still a block", got, bobSnap.You.ID)
	}

	// A block on somebody the roster does not contain stays out of the answer:
	// the field is "which of the people you can SEE", and a client that received
	// an id it has no row for could only ignore it.
	m211Block(t, ctx, ada, PlayerID(4242), true)
	_ = m211ReadBlockResult(t, ctx, ada)
	if got := server.blockedInRoster(adaSnap.You.ID, inst, roster.Players); len(got) != 1 {
		t.Fatalf("blockedInRoster=%v after blocking an absent id, want Bob alone", got)
	}

	// An empty roster is an empty answer rather than a walk of the block set,
	// which is what keeps the join of a player who is alone free of the work.
	if got := server.blockedInRoster(adaSnap.You.ID, inst, nil); got != nil {
		t.Fatalf("an empty roster answered %v", got)
	}

	// And lifting it is reported by the same computation, so the two halves of
	// the mirror cannot disagree about which way a row reads.
	m211Block(t, ctx, ada, bobSnap.You.ID, false)
	_ = m211ReadBlockResult(t, ctx, ada)
	if got := server.blockedInRoster(adaSnap.You.ID, inst, roster.Players); len(got) != 0 {
		t.Fatalf("blockedInRoster=%v after the block was lifted, want nothing", got)
	}
}
