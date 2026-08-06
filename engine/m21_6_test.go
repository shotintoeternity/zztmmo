package zztgo

// M21.6 — the operator console: list and lift refusals without a restart.
//
// M21.2 shipped refusals and left one hole in them, which this closes: nothing
// inside the game can name the account a refusal is addressed to, so lifting one
// meant editing saves/refused.json and restarting the server. A mis-aimed refusal
// was therefore permanent until the next restart, which is exactly when an
// operator most wants it gone.
//
// The four things these tests exist to stop:
//   1. a lift that only changes the process — the refusal has to leave the
//      document too, or the next restart re-imposes a sanction an operator
//      believes they lifted
//   2. a lift that is asserted against the store instead of proved: the claim is
//      that the account can PLAY again, so the proof is a socket that gets a
//      snapshot, in the same process, with no restart in between
//   3. a console that answers anybody — it shows account ids, which M21.1 keeps
//      out of every player's browser, so a guest and a signed-in non-operator
//      must each get nothing from all three routes
//   4. a lift nobody can review afterwards: it is the one moderation action that
//      undoes another, so an unaudited one is a gap in the record precisely where
//      the record is being questioned

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// --- helpers ---------------------------------------------------------------

// m216Console stands up M21.2's server and mounts the web API on the same
// process, so the console reads and writes the very store the socket path uses.
// Two handlers, one server: that sharing is the feature.
func m216Console(t *testing.T, refusalPath, auditPath string) (*WebSocketServer, string, *AuthService, http.Handler) {
	t.Helper()
	server, wsURL, auth := m212Server(t, refusalPath, auditPath)
	api := &WebAPI{RoomManager: server.RoomManager, Server: server, Auth: auth}
	return server, wsURL, auth, api.Handler()
}

func m216Do(t *testing.T, handler http.Handler, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func m216DecodeJSON(t *testing.T, rec *httptest.ResponseRecorder, into interface{}) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
}

func m216AuditFor(t *testing.T, server *WebSocketServer, action, result string) (ModerationAuditEntry, bool) {
	t.Helper()
	entries := server.Audit.Entries()
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Action == action && entries[i].Result == result {
			return entries[i], true
		}
	}
	return ModerationAuditEntry{}, false
}

// --- the headline claim -----------------------------------------------------

// TestM216ListAndLiftWithoutARestart is the DoD, end to end and in one process:
// Ada refuses Bob from the game, sees him on the console with the record of who
// refused him and where, lifts it, and Bob plays again — over a socket, because
// "the store no longer contains him" is not the claim being made.
func TestM216ListAndLiftWithoutARestart(t *testing.T) {
	dir := t.TempDir()
	refusalPath := filepath.Join(dir, "refused.json")
	server, wsURL, auth, console := m216Console(t, refusalPath, filepath.Join(dir, "moderation.jsonl"))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	adaCookie := signedAuthCookie(t, auth, AuthenticatedAccount{ID: m212OperatorAccount, Email: "ada@example.test", Name: "Ada"})
	bobCookie := signedAuthCookie(t, auth, AuthenticatedAccount{ID: m212TargetAccount, Email: "bob@example.test", Name: "Bob"})

	ada, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Board: 1}, adaCookie)
	defer ada.Close(websocket.StatusNormalClosure, "")
	bob, bobSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Bob", Board: 1}, bobCookie)
	defer bob.Close(websocket.StatusNormalClosure, "")

	// A tick or two first, so the refusal's audit line carries a tick that had to
	// come from the room it happened in.
	for i := 0; i < 3; i++ {
		server.Tick(ctx)
	}

	m212Moderate(t, ctx, ada, bobSnap.You.ID, ModerationActionRefuse)
	if result := m212ReadResult(t, ctx, ada); !result.Applied {
		t.Fatalf("refuse result = %+v", result)
	}

	// The state this task exists to get out of: refused, and unreachable from
	// inside the game.
	blocked, blockedRaw := m212DialFirst(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Board: 1}, bobCookie)
	defer blocked.Close(websocket.StatusNormalClosure, "")
	if got := m212MessageType(t, blockedRaw); got != MessageTypeModerationNotice {
		t.Fatalf("the refusal is not in force, so this test proves nothing: rejoin got %q", got)
	}

	// --- list ---------------------------------------------------------------
	var listed moderationRefusalsResponse
	m216DecodeJSON(t, m216Do(t, console, http.MethodGet, "/api/moderation/refusals", "", adaCookie), &listed)
	if len(listed.Refusals) != 1 {
		t.Fatalf("console listed %d refusals, want 1: %+v", len(listed.Refusals), listed.Refusals)
	}
	row := listed.Refusals[0]
	if row.Account != m212TargetAccount {
		t.Fatalf("the console must name the account a refusal is addressed to, got %q", row.Account)
	}
	// Who imposed it, when, and where. Without these the row is an id and a
	// shrug, and an operator deciding whether to lift it has nothing to go on.
	if row.By != m212OperatorAccount {
		t.Fatalf("the row does not say who imposed the refusal: %+v", row)
	}
	if row.World == "" || row.At.IsZero() {
		t.Fatalf("the row does not say where or when: %+v", row)
	}

	// --- lift ---------------------------------------------------------------
	var lift moderationLiftResponse
	m216DecodeJSON(t, m216Do(t, console, http.MethodPost, "/api/moderation/refusals/lift",
		`{"account":"`+m212TargetAccount+`"}`, adaCookie), &lift)
	if !lift.Lifted || lift.Account != m212TargetAccount {
		t.Fatalf("lift = %+v, want the refusal lifted", lift)
	}
	if lift.Was == nil || lift.Was.By != m212OperatorAccount {
		t.Fatalf("the lift must return what it removed, so a wrong one can be put back: %+v", lift.Was)
	}

	// --- and he plays, in this process, with no restart ----------------------
	back, backRaw := m212DialFirst(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Bob", Board: 1}, bobCookie)
	defer back.Close(websocket.StatusNormalClosure, "")
	if got := m212MessageType(t, backRaw); got != MessageTypeSnapshot {
		t.Fatalf("after the lift the account still could not rejoin: %q", got)
	}

	// The list is empty now, and — the half that a memory-only lift would fail —
	// so is the document. A restart must not re-impose it.
	var after moderationRefusalsResponse
	m216DecodeJSON(t, m216Do(t, console, http.MethodGet, "/api/moderation/refusals", "", adaCookie), &after)
	if len(after.Refusals) != 0 {
		t.Fatalf("the console still lists a lifted refusal: %+v", after.Refusals)
	}
	if NewRefusalStore(refusalPath).Refuses(m212TargetAccount) {
		t.Fatal("the lift did not reach the document, so the next restart would re-impose the refusal")
	}
}

// TestM216LiftingWhatIsNotThereIsNotAnError — the list an operator read is
// already a moment out of date, and two operators reaching for the same row is
// the normal case rather than a fault. It is still recorded, as a no-op.
func TestM216LiftingWhatIsNotThereIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	server, _, auth, console := m216Console(t, filepath.Join(dir, "refused.json"), "")
	adaCookie := signedAuthCookie(t, auth, AuthenticatedAccount{ID: m212OperatorAccount, Name: "Ada"})

	var lift moderationLiftResponse
	m216DecodeJSON(t, m216Do(t, console, http.MethodPost, "/api/moderation/refusals/lift",
		`{"account":"google:nobody"}`, adaCookie), &lift)
	if lift.Lifted {
		t.Fatalf("the console lifted a refusal that was never imposed: %+v", lift)
	}
	if lift.Text == "" {
		t.Fatal("a no-op with no explanation is indistinguishable from a success")
	}
	entry, ok := m216AuditFor(t, server, ModerationActionLift, ModerationResultNoOp)
	if !ok || entry.TargetAccount != "google:nobody" {
		t.Fatalf("an attempted lift must be in the record: %+v (found=%v)", entry, ok)
	}

	// An empty account is a bad request, not a no-op: it would otherwise be an
	// audit line pointing at nobody, which every guest's empty accountID matches.
	if rec := m216Do(t, console, http.MethodPost, "/api/moderation/refusals/lift", `{"account":"  "}`, adaCookie); rec.Code != http.StatusBadRequest {
		t.Fatalf("POST with an empty account status=%d, want 400", rec.Code)
	}
}

// TestM216ConsoleAnswersNobodyButAnOperator — the console shows account ids,
// which M21.1 keeps out of every player's browser. So a guest, a signed-in
// non-operator and a server with no auth at all must each get nothing from every
// route, and the refusal must still stand afterwards: a 404 that lifted something
// on the way past would be worse than a 200.
func TestM216ConsoleAnswersNobodyButAnOperator(t *testing.T) {
	routes := []struct{ method, path, body string }{
		{http.MethodGet, "/api/moderation/refusals", ""},
		{http.MethodPost, "/api/moderation/refusals/lift", `{"account":"` + m212TargetAccount + `"}`},
		{http.MethodGet, "/api/moderation/audit", ""},
	}

	for _, tc := range []struct {
		name    string
		cookie  func(*testing.T, *AuthService) *http.Cookie
		noAuth  bool
		noAllow bool
	}{
		{name: "a guest, with no cookie at all"},
		{name: "a signed-in player who is not an operator", cookie: func(t *testing.T, auth *AuthService) *http.Cookie {
			return signedAuthCookie(t, auth, AuthenticatedAccount{ID: "google:mallory", Name: "Mallory"})
		}},
		{name: "an operator's own cookie against an empty allowlist", noAllow: true, cookie: func(t *testing.T, auth *AuthService) *http.Cookie {
			return signedAuthCookie(t, auth, AuthenticatedAccount{ID: m212OperatorAccount, Name: "Ada"})
		}},
		{name: "a server with no auth service, so nobody is anybody", noAuth: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			server, _, auth, console := m216Console(t, filepath.Join(dir, "refused.json"), "")
			if tc.noAuth {
				console = (&WebAPI{RoomManager: server.RoomManager, Server: server}).Handler()
			}
			if tc.noAllow {
				server.Moderators = map[string]bool{}
			}
			if err := server.Refusals.Refuse(RefusedAccount{Account: m212TargetAccount, By: m212OperatorAccount}); err != nil {
				t.Fatalf("seed refusal: %v", err)
			}

			var cookie *http.Cookie
			if tc.cookie != nil {
				cookie = tc.cookie(t, auth)
			}
			for _, route := range routes {
				rec := m216Do(t, console, route.method, route.path, route.body, cookie)
				if rec.Code != http.StatusNotFound {
					t.Fatalf("%s %s status=%d, want 404 — the console must not confirm it exists: %q",
						route.method, route.path, rec.Code, rec.Body.String())
				}
				if strings.Contains(rec.Body.String(), m212TargetAccount) {
					t.Fatalf("%s %s leaked an account id to a non-operator: %q", route.method, route.path, rec.Body.String())
				}
			}

			// And nothing happened on the way through.
			if !server.Refusals.Refuses(m212TargetAccount) {
				t.Fatal("a refused request lifted the refusal anyway")
			}
			for _, entry := range server.Audit.Entries() {
				if entry.Action == ModerationActionLift {
					t.Fatalf("an unauthorized request wrote to the operator's own audit: %+v", entry)
				}
			}
		})
	}
}

// TestM216LiftIsAudited — the lift is the one moderation action that undoes
// another, so the record has to hold both lines and they have to join up: same
// account, same world, and a lift that names who imposed what it undid.
func TestM216LiftIsAudited(t *testing.T) {
	dir := t.TempDir()
	auditPath := filepath.Join(dir, "moderation.jsonl")
	server, wsURL, auth, console := m216Console(t, filepath.Join(dir, "refused.json"), auditPath)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	adaCookie := signedAuthCookie(t, auth, AuthenticatedAccount{ID: m212OperatorAccount, Email: "ada@example.test", Name: "Ada"})
	bobCookie := signedAuthCookie(t, auth, AuthenticatedAccount{ID: m212TargetAccount, Email: "bob@example.test", Name: "Bob"})

	ada, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Board: 1}, adaCookie)
	defer ada.Close(websocket.StatusNormalClosure, "")
	bob, bobSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Bob", Board: 1}, bobCookie)
	defer bob.Close(websocket.StatusNormalClosure, "")

	m212Moderate(t, ctx, ada, bobSnap.You.ID, ModerationActionRefuse)
	if result := m212ReadResult(t, ctx, ada); !result.Applied {
		t.Fatalf("refuse result = %+v", result)
	}
	refusal, ok := m212AuditFor(t, server, ModerationActionRefuse)
	if !ok {
		t.Fatal("the refusal is not in the audit")
	}

	var lift moderationLiftResponse
	m216DecodeJSON(t, m216Do(t, console, http.MethodPost, "/api/moderation/refusals/lift",
		`{"account":"`+m212TargetAccount+`"}`, adaCookie), &lift)
	if !lift.Lifted {
		t.Fatalf("lift = %+v", lift)
	}

	entry, ok := m216AuditFor(t, server, ModerationActionLift, ModerationResultApplied)
	if !ok {
		t.Fatal("the lift is not in the audit, so the one action that undoes another is unreviewable")
	}
	if entry.Operator != m212OperatorAccount || entry.OperatorName == "" {
		t.Fatalf("the lift does not name who did it: %+v", entry)
	}
	if entry.TargetAccount != refusal.TargetAccount {
		t.Fatalf("the lift and the refusal name different accounts: %q vs %q", entry.TargetAccount, refusal.TargetAccount)
	}
	if entry.World != refusal.World {
		t.Fatalf("the lift does not carry the refusal's world: %q vs %q", entry.World, refusal.World)
	}
	if !strings.Contains(entry.Detail, m212OperatorAccount) {
		t.Fatalf("the lift does not say whose refusal it undid: %q", entry.Detail)
	}
	if entry.At.IsZero() {
		t.Fatal("the lift is audited with no timestamp")
	}

	// The file is the record, and it now holds both halves of the story.
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	var actions []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var written ModerationAuditEntry
		if err := json.Unmarshal([]byte(line), &written); err != nil {
			t.Fatalf("audit line is not JSON: %q (%v)", line, err)
		}
		actions = append(actions, written.Action+"/"+written.Result)
	}
	want := []string{
		ModerationActionRefuse + "/" + ModerationResultApplied,
		ModerationActionLift + "/" + ModerationResultApplied,
	}
	if len(actions) != len(want) || actions[0] != want[0] || actions[1] != want[1] {
		t.Fatalf("audit file records %v, want %v", actions, want)
	}
}

// TestM216AuditTailIsReadableFromTheConsole — the third route. An operator
// opening a moderation console is usually asking "what has been done here", and
// the answer must not require reading a file off the host.
func TestM216AuditTailIsReadableFromTheConsole(t *testing.T) {
	dir := t.TempDir()
	server, wsURL, auth, console := m216Console(t, filepath.Join(dir, "refused.json"), filepath.Join(dir, "moderation.jsonl"))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	adaCookie := signedAuthCookie(t, auth, AuthenticatedAccount{ID: m212OperatorAccount, Name: "Ada"})
	ada, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Board: 1}, adaCookie)
	defer ada.Close(websocket.StatusNormalClosure, "")
	bob, bobSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Bob", Board: 1}, nil)
	defer bob.Close(websocket.StatusNormalClosure, "")

	for _, action := range []string{ModerationActionMute, ModerationActionUnmute} {
		m212Moderate(t, ctx, ada, bobSnap.You.ID, action)
		if result := m212ReadResult(t, ctx, ada); !result.Applied {
			t.Fatalf("%s result = %+v", action, result)
		}
	}

	var tail moderationAuditResponse
	m216DecodeJSON(t, m216Do(t, console, http.MethodGet, "/api/moderation/audit", "", adaCookie), &tail)
	if len(tail.Entries) != 2 {
		t.Fatalf("audit tail has %d entries, want 2: %+v", len(tail.Entries), tail.Entries)
	}
	if tail.Entries[0].Action != ModerationActionMute || tail.Entries[1].Action != ModerationActionUnmute {
		t.Fatalf("the tail is not in the order the actions happened: %+v", tail.Entries)
	}
	// The tail size is reported so an operator can tell a complete record from a
	// truncated one, and go to the file when it matters.
	if tail.Tail != ModerationAuditTail {
		t.Fatalf("the console does not say how much of the record it is showing: tail=%d", tail.Tail)
	}

	var limited moderationAuditResponse
	m216DecodeJSON(t, m216Do(t, console, http.MethodGet, "/api/moderation/audit?limit=1", "", adaCookie), &limited)
	if len(limited.Entries) != 1 || limited.Entries[0].Action != ModerationActionUnmute {
		t.Fatalf("?limit=1 must return the most recent entry: %+v", limited.Entries)
	}

	// A mistyped limit shows the whole tail rather than nothing: this is a console
	// driven by hand.
	var junk moderationAuditResponse
	m216DecodeJSON(t, m216Do(t, console, http.MethodGet, "/api/moderation/audit?limit=banana", "", adaCookie), &junk)
	if len(junk.Entries) != 2 {
		t.Fatalf("a mistyped limit changed the answer: %+v", junk.Entries)
	}

	_ = server
}

// TestM216RefusalStoreListAndLift covers the store directly on the two points a
// socket cannot reach: an entry hand-written into the document without the
// redundant "account" field must still be listable and liftable (Refuses tests
// membership, so such an entry really does refuse somebody), and the list must
// come back in the same order twice.
func TestM216RefusalStoreListAndLift(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "refused.json")
	handWritten := `{
	  "google:zed": {"by": "google:ada", "at": "2026-08-04T10:00:00Z"},
	  "google:amy": {"account": "google:amy", "by": "google:ada", "at": "2026-08-03T10:00:00Z"},
	  "google:mid": {"by": "google:ada", "at": "2026-08-03T10:00:00Z"}
	}`
	if err := os.WriteFile(path, []byte(handWritten), 0o600); err != nil {
		t.Fatalf("write refusals: %v", err)
	}

	store := NewRefusalStore(path)
	listed := store.List()
	got := make([]string, 0, len(listed))
	for _, rec := range listed {
		got = append(got, rec.Account)
	}
	// Oldest first, ties broken by account — the same answer every time, because a
	// console whose rows move between reads is a console you cannot click.
	want := []string{"google:amy", "google:mid", "google:zed"}
	if len(got) != len(want) {
		t.Fatalf("List() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("List() = %v, want %v", got, want)
		}
	}

	// The entry with no "account" field of its own refuses somebody, so it must be
	// liftable by the id it is filed under.
	rec, lifted, err := store.Lift("google:zed")
	if err != nil || !lifted {
		t.Fatalf("Lift(google:zed) = (%+v, %v, %v)", rec, lifted, err)
	}
	if rec.Account != "google:zed" {
		t.Fatalf("Lift must return a record that names its account: %+v", rec)
	}
	if store.Refuses("google:zed") {
		t.Fatal("the store still refuses a lifted account")
	}
	if !store.Refuses("google:amy") {
		t.Fatal("lifting one refusal lifted another")
	}
	if _, lifted, err := store.Lift("google:zed"); lifted || err != nil {
		t.Fatalf("lifting twice = (%v, %v), want a quiet no-op", lifted, err)
	}
	if _, lifted, err := store.Lift(""); lifted || err != nil {
		t.Fatalf("lifting the empty account = (%v, %v) — it must never match", lifted, err)
	}

	// And it reached the document, which is the whole difference between this and
	// a restart.
	if NewRefusalStore(path).Refuses("google:zed") {
		t.Fatal("the lift did not survive a reload of the document")
	}
}
