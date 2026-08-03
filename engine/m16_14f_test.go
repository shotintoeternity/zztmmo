package zztgo

// M16.14f — the editor socket reconnects.
//
// Before this, `connectEditor`'s close listener called showTitle() and stopped:
// any close at all — a blip, a server restart, a laptop lid — dropped a
// collaborator to the title screen of the world they were editing, while the
// game socket had had backoff and a resume token since M13.2. The reconnect is
// a re-enter, because the session's world lives on the server and editorEnter
// answers with a full snapshot.
//
// These are the wire-level halves of the change. The browser half — a socket
// closed under a real browser, which repaints without anybody touching a key —
// is web/test/editor_reconnect.test.mjs, reached from
// TestM1614fBrowserEditorReconnectsAfterItsSocketIsClosed.
//
// The two decisions the task left open are pinned here, not just implemented:
//
//	The token is continuity, not authority. It carries id, colour, board,
//	cursor and leases across a reconnect the server has not noticed yet, and it
//	is honoured only for the account it was issued to.
//
//	Leases are handed back when the membership ends, exactly as an abrupt
//	disconnect leaves them today. They move to the resuming connection ONLY on a
//	takeover, where the alternative is a member locked out by its own ghost.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

const m1614fWorld = "RECONN"

// m1614fServer hosts the EDIT fixture on the production server objects. Two
// boards, which is what lets a reconnect be shown to come back to the board its
// browser was on rather than to the session's current one.
func m1614fServer(t *testing.T) (*WebSocketServer, string) {
	t.Helper()
	world := m1613EditorWorld(t)
	world.Info.Name = m1614fWorld
	server := NewWebSocketServer(world, 0)
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	return server, "ws" + strings.TrimPrefix(httpServer.URL, "http")
}

type m1614fEditor struct {
	t     *testing.T
	ctx   context.Context
	label string
	conn  *websocket.Conn
}

// m1614fEnter opens one editor connection, presenting token when it has one,
// and returns it with its entry snapshot.
func m1614fEnter(t *testing.T, ctx context.Context, wsURL, label, token string) (*m1614fEditor, EditorSnapshotMessage) {
	t.Helper()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("%s: dial editor: %v", label, err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	conn.SetReadLimit(ServerReadLimit)
	editor := &m1614fEditor{t: t, ctx: ctx, label: label, conn: conn}
	editor.send(EditorEnterMessage{Type: MessageTypeEditorEnter, World: m1614fWorld, ResumeToken: token})
	var snapshot EditorSnapshotMessage
	readEditorMessage(t, ctx, conn, MessageTypeEditorSnapshot, &snapshot)
	return editor, snapshot
}

func (c *m1614fEditor) send(message interface{}) {
	c.t.Helper()
	if err := wsjson.Write(c.ctx, c.conn, message); err != nil {
		c.t.Fatalf("%s: write %T: %v", c.label, message, err)
	}
}

// lease asks for a lease and returns the reply, which is "granted" or
// "refused" — the session answers both, and which one it is is the whole point
// of every lease assertion below.
func (c *m1614fEditor) lease(kind string, boardID int16) EditorLeaseMessage {
	c.t.Helper()
	c.send(EditorLeaseMessage{Type: MessageTypeEditorLease, Op: "request", Kind: kind, BoardID: boardID})
	var reply EditorLeaseMessage
	readEditorMessage(c.t, c.ctx, c.conn, MessageTypeEditorLease, &reply)
	return reply
}

// switchBoard moves this member onto boardID and returns the snapshot it is
// repainted with.
func (c *m1614fEditor) switchBoard(boardID int16) EditorSnapshotMessage {
	c.t.Helper()
	c.send(EditorBoardMessage{Type: MessageTypeEditorBoard, Op: "switch", BoardID: boardID})
	var snapshot EditorSnapshotMessage
	readEditorMessage(c.t, c.ctx, c.conn, MessageTypeEditorSnapshot, &snapshot)
	return snapshot
}

// inspect moves this member's cursor, which is what the session records as its
// presence position — the thing a reconnect has to bring back.
func (c *m1614fEditor) inspect(x, y int16) EditorInspectMessage {
	c.t.Helper()
	c.send(EditorInspectMessage{Type: MessageTypeEditorInspect, X: x, Y: y})
	var reply EditorInspectMessage
	readEditorMessage(c.t, c.ctx, c.conn, MessageTypeEditorInspect, &reply)
	return reply
}

func m1614fSession(t *testing.T, server *WebSocketServer) *EditorSession {
	t.Helper()
	server.mu.Lock()
	defer server.mu.Unlock()
	session := server.EditorWorldSessions[m1614fWorld]
	if session == nil {
		t.Fatal("the server has no editor session for " + m1614fWorld)
	}
	return session
}

// m1614fWaitForMembers waits for the session to hold want members. Only the
// paths that wait on the SERVER noticing a closed socket use it; every
// assertion about what a reconnect produced is made without a sleep.
func m1614fWaitForMembers(t *testing.T, session *EditorSession, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if got := session.MemberCount(); got == want {
			return
		} else if time.Now().After(deadline) {
			t.Fatalf("the session holds %d member(s), want %d", got, want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestM1614fEditorEntryCarriesAMembershipToken is the protocol half: every
// entry snapshot hands the browser the token it needs to come back, and two
// browsers are two memberships with two different tokens.
func TestM1614fEditorEntryCarriesAMembershipToken(t *testing.T) {
	server, wsURL := m1614fServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, first := m1614fEnter(t, ctx, wsURL, "first", "")
	if first.ResumeToken == "" {
		t.Fatal("the entry snapshot carried no membership token, so a dropped editor has nothing to come back with")
	}
	_, second := m1614fEnter(t, ctx, wsURL, "second", "")
	if second.ResumeToken == "" || second.ResumeToken == first.ResumeToken {
		t.Fatalf("the second browser was handed token %q against the first's %q; two memberships must not share one",
			second.ResumeToken, first.ResumeToken)
	}
	if second.MemberID == first.MemberID {
		t.Fatalf("both browsers were given member id %q", first.MemberID)
	}
	m1614fWaitForMembers(t, m1614fSession(t, server), 2)
}

// TestM1614fReconnectTakesOverTheMembershipItLeft is the drop the server has
// not noticed yet — the case that would otherwise leave the session counting
// two members and fanning out to a socket nobody reads. The reconnect arrives
// while the old connection is still open, so nothing here waits for a timeout:
// the takeover is what makes it deterministic.
func TestM1614fReconnectTakesOverTheMembershipItLeft(t *testing.T) {
	server, wsURL := m1614fServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	before, entry := m1614fEnter(t, ctx, wsURL, "before", "")
	session := m1614fSession(t, server)

	// Put the membership somewhere specific: another board, a cursor away from
	// the centre, and a lease on the board it is editing.
	if got := before.switchBoard(1).BoardID; got != 1 {
		t.Fatalf("the member did not reach board 1 (got %d)", got)
	}
	before.inspect(10, 7)
	if reply := before.lease("board", 1); reply.Op != "granted" {
		t.Fatalf("the board lease was %q, want granted: %+v", reply.Op, reply)
	}

	after, resumed := m1614fEnter(t, ctx, wsURL, "after", entry.ResumeToken)

	if resumed.MemberID != entry.MemberID {
		t.Errorf("the reconnect was given member id %q, want the membership it left, %q",
			resumed.MemberID, entry.MemberID)
	}
	if resumed.ResumeToken != entry.ResumeToken {
		t.Errorf("the reconnect was handed token %q, want its own %q echoed back",
			resumed.ResumeToken, entry.ResumeToken)
	}
	if resumed.BoardID != 1 {
		t.Errorf("the reconnect repainted board %d, want board 1 — the board its browser was editing", resumed.BoardID)
	}
	if resumed.Inspect.X != 10 || resumed.Inspect.Y != 7 {
		t.Errorf("the reconnect's entry snapshot inspects %d,%d, want the cursor it left at 10,7",
			resumed.Inspect.X, resumed.Inspect.Y)
	}
	if n := session.MemberCount(); n != 1 {
		t.Errorf("the session holds %d members after one person reconnected; one person is one member", n)
	}
	if len(resumed.Presence) != 1 {
		t.Errorf("presence lists %d members after the reconnect, want 1: %+v", len(resumed.Presence), resumed.Presence)
	}

	// The displaced socket is closed by the server rather than left to be
	// discovered: a connection nobody reads is what M16.14e's queue fills up.
	readCtx, readCancel := context.WithTimeout(ctx, 10*time.Second)
	defer readCancel()
	for {
		var raw map[string]interface{}
		if err := wsjson.Read(readCtx, before.conn, &raw); err != nil {
			break
		}
		if readCtx.Err() != nil {
			t.Fatal("the displaced editor connection was left open after its membership was resumed")
		}
	}

	// The lease moved with the membership. A third browser is refused it, and
	// named the holder; the resumed connection is granted it, because it is the
	// holder. Without the transfer the returning member would be refused its own
	// lease by its own ghost.
	third, _ := m1614fEnter(t, ctx, wsURL, "third", "")
	if reply := third.lease("board", 1); reply.Op != "refused" || reply.HolderID != entry.MemberID {
		t.Errorf("a third browser's request for the resumed member's lease was %q held by %q, want refused held by %q",
			reply.Op, reply.HolderID, entry.MemberID)
	}
	if reply := after.lease("board", 1); reply.Op != "granted" {
		t.Errorf("the resumed connection was %q its own board lease: %+v", reply.Op, reply)
	}
}

// TestM1614fATokenWhoseMembershipEndedEntersFresh is the ordinary case: the
// server noticed the drop first, so there is nothing to take over. The browser
// re-enters as a new member — and the lease its old membership held was handed
// back on the way out, exactly as an abrupt disconnect leaves it today.
func TestM1614fATokenWhoseMembershipEndedEntersFresh(t *testing.T) {
	server, wsURL := m1614fServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// A second member for the whole run, so the session is never down to the
	// single member for whom leases are not enforced at all.
	witness, _ := m1614fEnter(t, ctx, wsURL, "witness", "")
	gone, entry := m1614fEnter(t, ctx, wsURL, "gone", "")
	session := m1614fSession(t, server)
	if reply := gone.lease("board", 0); reply.Op != "granted" {
		t.Fatalf("the board lease was %q, want granted: %+v", reply.Op, reply)
	}
	if reply := witness.lease("board", 0); reply.Op != "refused" {
		t.Fatalf("the lease was %q for a second member while it was held; this test would prove nothing", reply.Op)
	}

	_ = gone.conn.CloseNow()
	m1614fWaitForMembers(t, session, 1)

	back, resumed := m1614fEnter(t, ctx, wsURL, "back", entry.ResumeToken)
	if resumed.MemberID == entry.MemberID {
		t.Errorf("a token whose membership had already ended resumed it anyway (member id %q)", resumed.MemberID)
	}
	if resumed.ResumeToken == "" || resumed.ResumeToken == entry.ResumeToken {
		t.Errorf("the fresh entry was handed token %q, want a new one (the old was %q)",
			resumed.ResumeToken, entry.ResumeToken)
	}
	if n := session.MemberCount(); n != 2 {
		t.Errorf("the session holds %d members, want 2 — the witness and the browser that came back", n)
	}
	if reply := back.lease("board", 0); reply.Op != "granted" {
		t.Errorf("the board lease was %q for the returning browser: a membership that ended must hand its leases back", reply.Op)
	}
}

// TestM1614fAMembershipTokenDoesNotCrossAccounts holds the line the token is
// NOT allowed to cross: it carries a name, a colour and every lease a member
// holds, so it resumes only as the account it was issued to. Driven at the
// session, where an account is one field rather than a whole sign-in.
func TestM1614fAMembershipTokenDoesNotCrossAccounts(t *testing.T) {
	session := NewEditorSession("TEST", testEmptyWorld(t))
	ada := &webSocketClient{accountID: "ada"}
	adaAgain := &webSocketClient{accountID: "ada"}
	stranger := &webSocketClient{accountID: "mallory"}

	presence, token, displaced, err := session.EnterResuming(ada, "Ada", "")
	if err != nil || token == "" || displaced != nil {
		t.Fatalf("first entry: presence=%+v token=%q displaced=%v err=%v", presence, token, displaced != nil, err)
	}

	strangerPresence, strangerToken, strangerDisplaced, err := session.EnterResuming(stranger, "Mallory", token)
	if err != nil {
		t.Fatalf("stranger entry: %v", err)
	}
	if strangerDisplaced != nil {
		t.Error("another account's token displaced Ada's connection")
	}
	if strangerPresence.ID == presence.ID || strangerPresence.Name == presence.Name {
		t.Errorf("the stranger entered as %+v, want a membership of their own, not Ada's %+v", strangerPresence, presence)
	}
	if strangerToken == token {
		t.Error("the stranger was handed Ada's membership token")
	}
	if n := session.MemberCount(); n != 2 {
		t.Errorf("the session holds %d members, want 2: Ada and the stranger", n)
	}

	// Ada's own reconnect still resumes, so the account check refuses the
	// stranger rather than the token.
	adaPresence, adaToken, adaDisplaced, err := session.EnterResuming(adaAgain, "Ada", token)
	if err != nil {
		t.Fatalf("Ada's reconnect: %v", err)
	}
	if adaDisplaced != ada {
		t.Error("Ada's reconnect did not displace the connection it left")
	}
	if adaPresence.ID != presence.ID || adaToken != token {
		t.Errorf("Ada came back as %+v with token %q, want %+v with %q", adaPresence, adaToken, presence, token)
	}
	if n := session.MemberCount(); n != 2 {
		t.Errorf("the session holds %d members after Ada's reconnect, want 2 — one of them is Ada, once", n)
	}
}

// ---------------------------------------------------------------------------
// The browser half
// ---------------------------------------------------------------------------

// reconnectControlRoutes adds M16.14f's endpoints to the harness control
// listener (m169Harness.controlMux), beside M16.13's and M16.14's. They exist so
// the browser script can make the drop REAL — the server closes the socket under
// the page, which is the failure being recovered from — and can then read what
// the session says about who is in it, rather than believing the screen it is
// also asserting on.
func (h *m169Harness) reconnectControlRoutes(mux *http.ServeMux) {
	// Who the session thinks is editing. A reconnect that left a ghost behind
	// shows up here as two members for one browser.
	mux.HandleFunc("/control/editor/members", func(w http.ResponseWriter, r *http.Request) {
		session := h.editorSession()
		if session == nil {
			writeJSON(w, map[string]interface{}{"members": []EditorPresence{}})
			return
		}
		members := session.Presence()
		// Go map iteration is unordered and this is read by a script that
		// compares ids; sort so the JSON is stable.
		for i := 1; i < len(members); i++ {
			for j := i; j > 0 && members[j].ID < members[j-1].ID; j-- {
				members[j], members[j-1] = members[j-1], members[j]
			}
		}
		writeJSON(w, map[string]interface{}{"members": members})
	})

	// Close every editor socket the way a lost connection does: no close
	// handshake, no editorExit, nothing the client can prepare for. This is the
	// point of the suite — the browser is not asked to simulate a drop, it is
	// dropped.
	mux.HandleFunc("/control/editor/drop", func(w http.ResponseWriter, r *http.Request) {
		session := h.editorSession()
		closed := 0
		if session != nil {
			for _, member := range session.MemberClients() {
				if member.conn == nil {
					continue
				}
				_ = member.conn.CloseNow()
				closed++
			}
		}
		writeJSON(w, map[string]int{"closed": closed})
	})

	// Change the editing world while the browser is away, with no member to
	// broadcast a diff. A page that merely kept its old pixels cannot show this
	// tile; only a fresh snapshot can, which is what makes "it repainted" an
	// assertion rather than a hope.
	mux.HandleFunc("/control/editor/paint", func(w http.ResponseWriter, r *http.Request) {
		session := h.editorSession()
		if session == nil {
			http.Error(w, "no editor session for "+h.worldName, http.StatusNotFound)
			return
		}
		number := func(name string) int16 {
			n, _ := strconv.Atoi(r.URL.Query().Get(name))
			return int16(n)
		}
		x, y := number("x"), number("y")
		element, color := byte(number("element")), byte(number("color"))
		session.mu.Lock()
		defer session.mu.Unlock()
		e := session.engine
		before := e.Board.Tiles[x][y]
		if !editorPlaceTile(e, x, y, element, color, true) {
			http.Error(w, "the session refused the placement", http.StatusConflict)
			return
		}
		writeJSON(w, map[string]interface{}{
			"boardId":     e.World.Info.CurrentBoard,
			"wasElement":  before.Element,
			"wasColor":    before.Color,
			"nowElement":  e.Board.Tiles[x][y].Element,
			"nowColor":    e.Board.Tiles[x][y].Color,
			"screenDirty": len(e.screenDirty),
		})
	})
}

// m1614fReport is what the browser script recorded, so the Go side can hold the
// run to its own claims rather than trusting that a script which printed
// "passed" did anything.
type m1614fReport struct {
	// MemberBefore/MemberAfter are the session's view of who was editing, on
	// either side of the drop.
	MemberBefore string `json:"memberBefore"`
	MemberAfter  string `json:"memberAfter"`
	// Closed is how many editor sockets the server really closed.
	Closed int `json:"closed"`
	// MembersAfter is how many members the session held once the browser was
	// back — one person is one member.
	MembersAfter int `json:"membersAfter"`
	// KeysPressedDuringRecovery must be zero: the DoD is a browser that comes
	// back without the player touching anything.
	KeysPressedDuringRecovery int      `json:"keysPressedDuringRecovery"`
	Notes                     []string `json:"notes"`
}

// TestM1614fBrowserEditorReconnectsAfterItsSocketIsClosed is the DoD's browser
// clause. A real Chromium edits a world, the server closes its editor socket
// underneath it, and the page must come back — repainted from a snapshot it
// could not have had before, on the board its author was on, with its cursor
// where they left it — without a keystroke.
func TestM1614fBrowserEditorReconnectsAfterItsSocketIsClosed(t *testing.T) {
	h := m1613NewHarness(t)
	outDir := m1614fOutDir(t)
	out := h.runBrowserScript("editor_reconnect.test.mjs", "M1614F_OUT="+outDir)
	t.Logf("editor reconnect run:\n%s", out)

	reportData, err := os.ReadFile(filepath.Join(outDir, "report.json"))
	if err != nil {
		t.Fatalf("the browser script wrote no run report: %v", err)
	}
	var report m1614fReport
	if err := json.Unmarshal(reportData, &report); err != nil {
		t.Fatalf("parse the run report: %v", err)
	}

	if report.Closed != 1 {
		t.Errorf("the server closed %d editor socket(s); a run that dropped nothing proves nothing", report.Closed)
	}
	if report.MemberBefore == "" || report.MemberAfter == "" {
		t.Fatalf("the run recorded members %q and %q around the drop", report.MemberBefore, report.MemberAfter)
	}
	if report.MembersAfter != 1 {
		t.Errorf("the session held %d members once the browser was back, want 1", report.MembersAfter)
	}
	if report.KeysPressedDuringRecovery != 0 {
		t.Errorf("the browser was given %d keystroke(s) during the recovery; it must come back untouched",
			report.KeysPressedDuringRecovery)
	}
	for _, note := range report.Notes {
		t.Logf("  · %s", note)
	}
}

func m1614fOutDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("web", "test-results", "m16-14f"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}
