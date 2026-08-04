package zztgo

// M21.1 — block: a player can stop hearing another player.
//
// Every claim below is about what one recipient's SOCKET receives, because that
// is the whole substance of the feature: the filter is at the fan-out on the
// server, and a test that only checked a data structure would pass for a client
// that still had the text delivered to it.
//
// The five things these tests exist to stop:
//   1. a blocked line reaching the blocker anyway (the feature)
//   2. a block leaking to anyone else — the target being told, or a third party
//      losing the line (per-recipient, silent)
//   3. a signed-in block evaporating over a restart, or a guest's pretending to
//      survive one (durability follows identity, and the UI is told which)
//   4. a server announcement being swallowed by a block (a shutdown warning is
//      not chat)
//   5. a blocked account's PERSISTED history coming back on the next join

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

// --- helpers ---------------------------------------------------------------

type m211Chat struct {
	Type     string   `json:"type"`
	From     string   `json:"from"`
	PlayerID PlayerID `json:"playerId"`
	Text     string   `json:"text"`
	History  bool     `json:"history"`
}

// m211ReadChat waits for the next chat line on a connection, skipping the
// snapshot/diff traffic. Unlike readChatMessage it keeps the PlayerID, which is
// the field M21.1 adds and the only addressable thing on the line.
func m211ReadChat(t *testing.T, ctx context.Context, conn *websocket.Conn) m211Chat {
	t.Helper()
	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, conn, &raw); err != nil {
			t.Fatalf("read chat: %v", err)
		}
		var chat m211Chat
		if err := json.Unmarshal(raw, &chat); err != nil {
			continue
		}
		if chat.Type == "chat" {
			return chat
		}
	}
}

// m211AssertFencedOut proves a NEGATIVE without a timeout, which matters twice
// over: a read whose context expires CLOSES the connection under this websocket
// library, and a timeout is the kind of proof that becomes a flake on a loaded
// machine.
//
// The shape is M16.16a's, one step further. The line under test has already been
// demonstrably fanned out — a third party received it — so if it were coming to
// this recipient it is already queued on their socket. A second line is then sent
// by a sender this recipient does NOT block; each client has one writer, so its
// socket is FIFO. Reading the fence first is therefore proof the first line was
// dropped, not proof that we did not wait long enough.
func m211AssertFencedOut(t *testing.T, ctx context.Context, recipient, fencer *websocket.Conn, fence string) {
	t.Helper()
	m211Say(t, ctx, fencer, fence)
	got := m211ReadChat(t, ctx, recipient)
	if got.Text != fence {
		t.Fatalf("a blocked line was delivered anyway: got %q ahead of the fence %q", got.Text, fence)
	}
	// The fencer's own copy, so the next fence in the same test does not read it.
	_ = m211ReadChat(t, ctx, fencer)
}

func m211ReadBlockResult(t *testing.T, ctx context.Context, conn *websocket.Conn) BlockResultMessage {
	t.Helper()
	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, conn, &raw); err != nil {
			t.Fatalf("read blockResult: %v", err)
		}
		var result BlockResultMessage
		if err := json.Unmarshal(raw, &result); err != nil {
			continue
		}
		if result.Type == MessageTypeBlockResult {
			return result
		}
	}
}

func m211Block(t *testing.T, ctx context.Context, conn *websocket.Conn, target PlayerID, blocked bool) {
	t.Helper()
	if err := wsjson.Write(ctx, conn, BlockMessage{Type: MessageTypeBlock, PlayerID: target, Blocked: blocked}); err != nil {
		t.Fatalf("write block: %v", err)
	}
}

func m211Say(t *testing.T, ctx context.Context, conn *websocket.Conn, text string) {
	t.Helper()
	if err := wsjson.Write(ctx, conn, map[string]string{"type": "chat", "text": text}); err != nil {
		t.Fatalf("write chat %q: %v", text, err)
	}
}

// --- the headline claim ----------------------------------------------------

// TestM211BlockedLineIsDroppedAtFanOutForOneRecipient is the DoD's first
// sentence: A blocks B, B talks, A does not receive the line and C does.
func TestM211BlockedLineIsDroppedAtFanOutForOneRecipient(t *testing.T) {
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.ChatDB = NewMemChatDatabase()
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	a, aSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Ada", Board: 1}, nil)
	defer a.Close(websocket.StatusNormalClosure, "")
	b, bSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Bob", Board: 1}, nil)
	defer b.Close(websocket.StatusNormalClosure, "")
	c, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Cy", Board: 1}, nil)
	defer c.Close(websocket.StatusNormalClosure, "")

	// Before the block: the line reaches A, and it carries something addressable.
	m211Say(t, ctx, b, "before")
	first := m211ReadChat(t, ctx, a)
	if first.Text != "before" || first.From != "Bob" {
		t.Fatalf("pre-block line to A = %+v, want Bob/before", first)
	}
	if first.PlayerID != bSnap.You.ID {
		t.Fatalf("chat line carried playerId=%d, want B's %d — a block cannot key on a display name",
			first.PlayerID, bSnap.You.ID)
	}
	_ = m211ReadChat(t, ctx, c)
	_ = m211ReadChat(t, ctx, b)

	m211Block(t, ctx, a, bSnap.You.ID, true)
	result := m211ReadBlockResult(t, ctx, a)
	if !result.Blocked || result.PlayerID != bSnap.You.ID {
		t.Fatalf("block result = %+v, want blocked=true for B", result)
	}
	if result.Durable {
		t.Fatalf("a guest target cannot be blocked durably, but the server said it was: %+v", result)
	}
	if !strings.Contains(result.Text, "session") {
		t.Fatalf("the UI must be told a guest block is session-only, got %q", result.Text)
	}

	m211Say(t, ctx, b, "after")
	// C first: a third party receiving it is what proves the line was broadcast
	// at all, and it bounds A's negative wait below.
	third := m211ReadChat(t, ctx, c)
	if third.Text != "after" {
		t.Fatalf("third party line = %q, want %q — the block must be per-recipient", third.Text, "after")
	}
	// B is told nothing: their own line still comes back to them, and no block
	// message ever arrives.
	own := m211ReadChat(t, ctx, b)
	if own.Text != "after" {
		t.Fatalf("B's own line = %q; a blocked speaker must not notice anything", own.Text)
	}
	m211AssertFencedOut(t, ctx, a, c, "fence")
	_ = m211ReadChat(t, ctx, b) // C's fence, which B is not blocking

	// And it lifts.
	m211Block(t, ctx, a, bSnap.You.ID, false)
	lifted := m211ReadBlockResult(t, ctx, a)
	if lifted.Blocked {
		t.Fatalf("unblock result = %+v, want blocked=false", lifted)
	}
	m211Say(t, ctx, b, "again")
	back := m211ReadChat(t, ctx, a)
	if back.Text != "again" {
		t.Fatalf("after unblocking, A received %q, want %q", back.Text, "again")
	}
	if aSnap.You.ID == bSnap.You.ID {
		t.Fatal("A and B must be different players for any of this to mean anything")
	}
}

// TestM211BlockNeverSuppressesAServerAnnouncement — a shutdown warning is not
// chat, and it does not travel through the chat fan-out. Asserted rather than
// assumed: this is the one message a player must not be able to lose.
func TestM211BlockNeverSuppressesAServerAnnouncement(t *testing.T) {
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.ChatDB = NewMemChatDatabase()
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	a, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Ada", Board: 1}, nil)
	defer a.Close(websocket.StatusNormalClosure, "")
	b, bSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Bob", Board: 1}, nil)
	defer b.Close(websocket.StatusNormalClosure, "")

	m211Block(t, ctx, a, bSnap.You.ID, true)
	_ = m211ReadBlockResult(t, ctx, a)

	// Block everything blockable: the sender of the announcement is the server,
	// which has no PlayerID and no account, so there is nothing a block could
	// even name.
	if n := server.AnnounceShutdown(ctx, 30, "server restarting"); n != 2 {
		t.Fatalf("AnnounceShutdown reached %d players, want 2", n)
	}
	for _, conn := range []*websocket.Conn{a, b} {
		found := false
		readCtx, readCancel := context.WithTimeout(ctx, 5*time.Second)
		for !found {
			var raw json.RawMessage
			if err := wsjson.Read(readCtx, conn, &raw); err != nil {
				t.Fatalf("a blocker must still receive announcements: %v", err)
			}
			var announce struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if err := json.Unmarshal(raw, &announce); err != nil {
				continue
			}
			if announce.Type == "announce" {
				if announce.Text != "server restarting" {
					t.Fatalf("announce text=%q", announce.Text)
				}
				found = true
			}
		}
		readCancel()
	}
}

// TestM211UnknownTargetIsANoOpNotAnError — the roster a player read a moment ago
// is always slightly out of date, and blocking somebody who has just left must
// not look like a failure.
func TestM211UnknownTargetIsANoOpNotAnError(t *testing.T) {
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.ChatDB = NewMemChatDatabase()
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	a, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Ada", Board: 1}, nil)
	defer a.Close(websocket.StatusNormalClosure, "")

	m211Block(t, ctx, a, PlayerID(4242), true)
	result := m211ReadBlockResult(t, ctx, a)
	if result.Blocked {
		t.Fatalf("a vanished target must not be reported as blocked: %+v", result)
	}
	if result.Text == "" {
		t.Fatal("a no-op still owes the player an explanation")
	}

	// The connection is still usable afterwards — a no-op, not an error that
	// tore anything down.
	b, bSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Bob", Board: 1}, nil)
	defer b.Close(websocket.StatusNormalClosure, "")
	m211Say(t, ctx, b, "still here")
	if line := m211ReadChat(t, ctx, a); line.Text != "still here" || line.PlayerID != bSnap.You.ID {
		t.Fatalf("after a no-op block, A received %+v", line)
	}
}

// TestM211SignedInBlockSurvivesARestartAndAGuestBlockDoesNot is the durability
// split, both halves in one test because the claim is the CONTRAST: the same
// gesture is durable or not according to whether the two parties have durable
// identities, and the player is told which one they got.
func TestM211SignedInBlockSurvivesARestartAndAGuestBlockDoesNot(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chat.jsonl")
	db, err := NewFileChatDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewFileChatDatabase: %v", err)
	}
	auth := NewAuthService("client-id", "", "", []byte("m21-1-cookie-secret"))
	adaCookie := signedAuthCookie(t, auth, AuthenticatedAccount{ID: "google:ada", Email: "ada@example.test", Name: "Ada"})
	bobCookie := signedAuthCookie(t, auth, AuthenticatedAccount{ID: "google:bob", Email: "bob@example.test", Name: "Bob"})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// --- session 1: Ada blocks the signed-in Bob, and a guest -------------
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.ChatDB = db
	server.Auth = auth
	httpServer := httptest.NewServer(server)
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	ada, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Board: 1}, adaCookie)
	bob, bobSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Board: 1}, bobCookie)
	guest, guestSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Gus", Board: 1}, nil)
	// Cy is nobody's block: the fence every negative below is proved against.
	cy, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Cy", Board: 1}, nil)

	m211Block(t, ctx, ada, bobSnap.You.ID, true)
	bobResult := m211ReadBlockResult(t, ctx, ada)
	if !bobResult.Durable {
		t.Fatalf("blocking a signed-in player must be durable: %+v", bobResult)
	}
	if strings.Contains(bobResult.Text, "session") {
		t.Fatalf("a durable block must not be described as session-only: %q", bobResult.Text)
	}
	m211Block(t, ctx, ada, guestSnap.You.ID, true)
	guestResult := m211ReadBlockResult(t, ctx, ada)
	if guestResult.Durable {
		t.Fatalf("a guest target has no durable id to key on: %+v", guestResult)
	}
	if !strings.Contains(guestResult.Text, "session") {
		t.Fatalf("the player must be TOLD a guest block is session-only, got %q", guestResult.Text)
	}

	// Both are live now. Each line is read on an unblocked socket first, which is
	// what proves it was broadcast at all before the fence proves Ada was skipped.
	m211Say(t, ctx, bob, "from bob")
	_ = m211ReadChat(t, ctx, bob)
	_ = m211ReadChat(t, ctx, cy)
	m211Say(t, ctx, guest, "from gus")
	_ = m211ReadChat(t, ctx, guest)
	_ = m211ReadChat(t, ctx, cy)
	m211AssertFencedOut(t, ctx, ada, cy, "fence one")
	_ = m211ReadChat(t, ctx, bob)
	_ = m211ReadChat(t, ctx, guest)

	// The durable half, and only that half, is written down.
	prefs, ok, err := db.GetAccountPreferences("google:ada")
	if err != nil || !ok {
		t.Fatalf("GetAccountPreferences(ada) = (_, %v, %v), want a stored document", ok, err)
	}
	if len(prefs.BlockedAccounts) != 1 || prefs.BlockedAccounts[0] != "google:bob" {
		t.Fatalf("stored blocks=%v, want exactly [google:bob] — a guest has no id to store",
			prefs.BlockedAccounts)
	}

	ada.Close(websocket.StatusNormalClosure, "")
	bob.Close(websocket.StatusNormalClosure, "")
	guest.Close(websocket.StatusNormalClosure, "")
	cy.Close(websocket.StatusNormalClosure, "")
	httpServer.Close()
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	// --- session 2: a different process, and a different browser ----------
	// A fresh server, a fresh store handle and freshly minted PlayerIDs: the
	// only thing carried over is the account id, which is the point.
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

	ada2, _ := dialJoinWithCookie(t, ctx, wsURL2, JoinMessage{Type: MessageTypeJoin, Board: 1}, adaCookie)
	defer ada2.Close(websocket.StatusNormalClosure, "")
	bob2, _ := dialJoinWithCookie(t, ctx, wsURL2, JoinMessage{Type: MessageTypeJoin, Board: 1}, bobCookie)
	defer bob2.Close(websocket.StatusNormalClosure, "")
	guest2, _ := dialJoinWithCookie(t, ctx, wsURL2, JoinMessage{Type: MessageTypeJoin, Name: "Gus", Board: 1}, nil)
	defer guest2.Close(websocket.StatusNormalClosure, "")
	// The history replay: Bob's persisted line is suppressed by the durable block
	// — on his ACCOUNT, since his PlayerID did not survive the process — and the
	// two lines from unblocked speakers are not. Read up to the last line session
	// 1 wrote, which is a definite end rather than a wait for nothing.
	var replayed []string
	for {
		line := m211ReadChat(t, ctx, ada2)
		if !line.History {
			t.Fatalf("expected the persisted backlog, got a live line %+v", line)
		}
		replayed = append(replayed, line.Text)
		if line.Text == "fence one" {
			break
		}
	}
	for _, text := range replayed {
		if text == "from bob" {
			t.Fatalf("a durable block must cover the backlog too; replay was %v", replayed)
		}
	}
	if len(replayed) != 2 || replayed[0] != "from gus" {
		t.Fatalf("history replay to Ada = %v, want the two unblocked lines", replayed)
	}

	// And live: Bob is still blocked, and the guest — whose block could only ever
	// have been session-scoped — is heard again. The guest's line IS the fence.
	m211Say(t, ctx, bob2, "bob again")
	_ = m211ReadChat(t, ctx, bob2)
	m211Say(t, ctx, guest2, "gus again")
	_ = m211ReadChat(t, ctx, guest2)
	if line := m211ReadChat(t, ctx, ada2); line.Text != "gus again" {
		t.Fatalf("Ada received %q: either Bob's durable block was lost, or the guest's block wrongly survived",
			line.Text)
	}
}

// TestM211BlocksAreNeverASharedBucket — the M19.3 guard, re-checked one field
// down: an unauthenticated blocker stores nothing at all, rather than writing
// into a document every guest would then read.
func TestM211BlocksAreNeverASharedBucket(t *testing.T) {
	db := NewMemChatDatabase()
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.ChatDB = db
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	a, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Ada", Board: 1}, nil)
	defer a.Close(websocket.StatusNormalClosure, "")
	b, bSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Bob", Board: 1}, nil)
	defer b.Close(websocket.StatusNormalClosure, "")

	m211Block(t, ctx, a, bSnap.You.ID, true)
	_ = m211ReadBlockResult(t, ctx, a)

	if _, _, err := db.GetAccountPreferences(""); err == nil {
		t.Fatal("the empty account key must be refused, not read")
	}
	// Nothing was written under any key: a guest's block has nowhere to live.
	db.mu.Lock()
	stored := len(db.accountPrefs)
	db.mu.Unlock()
	if stored != 0 {
		t.Fatalf("a guest's block wrote %d preference documents, want 0", stored)
	}
}

// TestM211BlockRegistryRules covers the registry's own edges directly, which the
// socket tests above cannot reach: self-blocking, an unaddressable sender (the
// shape of a server announcement), and a departed blocker's set being dropped.
func TestM211BlockRegistryRules(t *testing.T) {
	var blocks chatBlocks

	blocks.set(1, 2, "google:bob", true)
	if !blocks.suppresses(1, 2, "google:bob") {
		t.Fatal("a recorded block must suppress")
	}
	if blocks.suppresses(2, 1, "") {
		t.Fatal("a block is one-directional: B did not block A")
	}
	if blocks.suppresses(3, 2, "google:bob") {
		t.Fatal("a block is per-recipient: C never blocked B")
	}
	// Self-blocking is refused at the WRITE, which is what keeps a mistake out of
	// the stored document — the one place it would outlive the session. Proved on
	// its own registry so the sets above cannot mask it.
	var solo chatBlocks
	if solo.set(9, 9, "google:me", true) {
		t.Fatal("blocking yourself cannot be durable, because it cannot happen")
	}
	if got := solo.blockedAccounts(9); len(got) != 0 {
		t.Fatalf("a refused self-block left %v in the durable half, where it would outlive the mistake", got)
	}
	blocks.set(1, 1, "google:ada", true)
	if blocks.suppresses(1, 1, "google:ada") {
		t.Fatal("nobody is blocked out of their own chat window, even having asked for it")
	}
	// And refused at the READ independently, proved through state the write path
	// would not create: blocker 1 blocks player 2, who shares 1's own account (the
	// same person in a second browser). A line from 1 must still reach 1.
	blocks.set(1, 2, "google:ada", true)
	if blocks.suppresses(1, 1, "google:ada") {
		t.Fatal("a player must hear themselves even when their own account is in their block set")
	}
	// An unaddressable sender — no id, no account — is the shape of a server
	// announcement AND of every history line whose id the loader cleared. Refused
	// at the write, and the read guards it too: reach past `set` to say so, since
	// nothing else can put a 0 in there.
	if blocks.set(1, 0, "", true) {
		t.Fatal("an unaddressable target cannot be blocked, let alone durably")
	}
	blocks.mu.Lock()
	blocks.blockers[1].players[0] = true
	blocks.mu.Unlock()
	if blocks.suppresses(1, 0, "") {
		t.Fatal("an unaddressable sender must never be suppressible")
	}
	blocks.mu.Lock()
	delete(blocks.blockers[1].players, 0)
	blocks.mu.Unlock()

	// The account outlives the connection: a new id for the same account is
	// still blocked.
	if !blocks.suppresses(1, 99, "google:bob") {
		t.Fatal("a durable block must follow the account, not the connection")
	}
	// And the connection is blocked even when the target has no account.
	blocks.set(1, 7, "", true)
	if !blocks.suppresses(1, 7, "") {
		t.Fatal("a guest target must be blockable by connection id")
	}
	// The durable half only: google:bob and google:ada (blocked above through
	// player 2), and never the connection ids or the refused entries.
	if got := blocks.blockedAccounts(1); len(got) != 2 {
		t.Fatalf("blockedAccounts=%v, want only the two durable entries", got)
	}

	blocks.forget(1)
	if blocks.suppresses(1, 2, "google:bob") {
		t.Fatal("a departed player's set must be dropped, not kept")
	}

	// Seeding is additive: a reconnect must not hand somebody back their
	// audience by replacing a block made this session.
	blocks.set(5, 6, "", true)
	blocks.seedAccounts(5, []string{"google:bob", ""})
	if !blocks.suppresses(5, 6, "") {
		t.Fatal("seeding stored blocks must not clear a session block")
	}
	if !blocks.suppresses(5, 0, "google:bob") {
		t.Fatal("a seeded account block must suppress")
	}
	if got := blocks.blockedAccounts(5); len(got) != 1 {
		t.Fatalf("an empty account id must not be seeded: %v", got)
	}
}

// TestM211ChatRecordCarriesTheAuthorButNotAcrossProcessesForTheID — the record
// gains both ids, and the process-scoped one is deliberately dropped on load: a
// later process re-mints the same numbers for different people, and a stale id
// would suppress an innocent line.
func TestM211ChatRecordCarriesTheAuthorButNotAcrossProcessesForTheID(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chat.jsonl")
	db, err := NewFileChatDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewFileChatDatabase: %v", err)
	}
	if _, err := db.AddMessage(ChatAuthor{Name: "Bob", PlayerID: 7, AccountID: "google:bob"}, "hi"); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}
	if _, err := db.AddMessage(ChatAuthor{Name: "Gus", PlayerID: 8}, "hey"); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}
	live, _ := db.GetRecentMessages(10)
	if len(live) != 2 || live[0].PlayerID != 7 || live[0].AccountID != "google:bob" {
		t.Fatalf("in-process records=%+v, want the author's ids intact", live)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := NewFileChatDatabase(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	loaded, _ := reopened.GetRecentMessages(10)
	if len(loaded) != 2 {
		t.Fatalf("loaded records=%d, want 2", len(loaded))
	}
	for i, rec := range loaded {
		if rec.PlayerID != 0 {
			t.Errorf("record %d kept PlayerID %d across a restart; ids do not survive the process that minted them",
				i, rec.PlayerID)
		}
	}
	if loaded[0].AccountID != "google:bob" || loaded[1].AccountID != "" {
		t.Fatalf("account ids must survive: %+v", loaded)
	}
}

// TestM211AccountIDNeverReachesAnotherPlayersBrowser — the record keeps the
// author's account id so a durable block can filter history, and that id must
// never be on the wire: a block is silent, and another player's account id is not
// theirs to see.
func TestM211AccountIDNeverReachesAnotherPlayersBrowser(t *testing.T) {
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.ChatDB = NewMemChatDatabase()
	auth := NewAuthService("client-id", "", "", []byte("m21-1-wire-secret"))
	server.Auth = auth
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	watcher, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Watch", Board: 1}, nil)
	defer watcher.Close(websocket.StatusNormalClosure, "")
	cookie := signedAuthCookie(t, auth, AuthenticatedAccount{ID: "google:secret", Email: "s@example.test", Name: "Sam"})
	signedIn, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Board: 1}, cookie)
	defer signedIn.Close(websocket.StatusNormalClosure, "")

	m211Say(t, ctx, signedIn, "hello")
	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, watcher, &raw); err != nil {
			t.Fatalf("read: %v", err)
		}
		if strings.Contains(string(raw), "google:secret") {
			t.Fatalf("an account id reached another player's browser: %s", raw)
		}
		var chat m211Chat
		if err := json.Unmarshal(raw, &chat); err == nil && chat.Type == "chat" {
			if chat.From != "Sam" || chat.PlayerID == 0 {
				t.Fatalf("chat line = %+v, want Sam with an addressable id", chat)
			}
			return
		}
	}
}

// TestM211BlockRouteIsNotAnHTTPRoute keeps the surface honest: blocking is a
// socket message, so it cannot be reached by an unauthenticated HTTP request
// that names somebody else's player id.
func TestM211BlockRouteIsNotAnHTTPRoute(t *testing.T) {
	api := &WebAPI{}
	handler := api.Handler()
	req := httptest.NewRequest(http.MethodPost, "/api/block", strings.NewReader(`{"playerId":1}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/block status=%d, want 404 — blocking is not an HTTP surface", rec.Code)
	}
}

// TestM211PickingAColourDoesNotDeleteYourBlocks is the defect this task found in
// the store it was told to reuse.
//
// PUT /api/preferences owns exactly one field, and it used to write a whole fresh
// document — `AccountPreferences{Color: ...}` — so the first colour a player
// picked after blocking somebody erased every block they held. M19.3's own notes
// predicted this shape of loss for a field that arrived later. The field arrived.
func TestM211PickingAColourDoesNotDeleteYourBlocks(t *testing.T) {
	db := NewMemChatDatabase()
	auth := NewAuthService("client-id", "", "", []byte("m21-1-prefs-secret"))
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.ChatDB = db
	server.Auth = auth
	api := &WebAPI{Server: server, Auth: auth}
	handler := api.Handler()

	if err := db.PutAccountPreferences("google:ada", AccountPreferences{
		Color:           "#ff0000",
		BlockedAccounts: []string{"google:bob"},
	}); err != nil {
		t.Fatalf("seed preferences: %v", err)
	}

	cookie := signedAuthCookie(t, auth, AuthenticatedAccount{ID: "google:ada", Name: "Ada"})
	req := httptest.NewRequest(http.MethodPut, "/api/preferences", strings.NewReader(`{"color":"#00ff00"}`))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/preferences status=%d body=%q", rec.Code, rec.Body.String())
	}

	prefs, ok, err := db.GetAccountPreferences("google:ada")
	if err != nil || !ok {
		t.Fatalf("GetAccountPreferences = (_, %v, %v)", ok, err)
	}
	if prefs.Color != "#00ff00" {
		t.Fatalf("the colour must be stored: %+v", prefs)
	}
	if len(prefs.BlockedAccounts) != 1 || prefs.BlockedAccounts[0] != "google:bob" {
		t.Fatalf("picking a colour deleted the player's blocks: %+v", prefs)
	}
}
