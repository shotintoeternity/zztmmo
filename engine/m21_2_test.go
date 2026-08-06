package zztgo

// M21.2 — operator actions: mute, kick and refuse.
//
// Every claim here is about what the SERVER does, because that is the whole
// substance of the feature: an operator action that a client could decline to
// perform, or perform on its own say-so, would be theatre. The tests speak over
// real sockets for the same reason M21.1's do — a test that checked a map would
// pass for a server that still fanned the muted line out.
//
// The six things these tests exist to stop:
//   1. a muted player's line reaching the room, or reaching it silently — a
//      sanction that does not announce itself to its target is indistinguishable
//      from a broken game (and that is exactly what separates a mute from a
//      block)
//   2. a kick that does not end the session, or that removes the player by some
//      path other than the one a closed tab already uses
//   3. a refusal that a restart lifts — the beta host restarts routinely, and
//      the whole reason refusal is stored on disk is that a sanction a reboot
//      undoes is not a sanction
//   4. the guest limit being hidden rather than shipped: a refused guest CAN
//      come back, and both the operator and this test say so out loud
//   5. an allowlist that fails open — a non-operator, an empty allowlist and an
//      unset one must each be refused all three actions
//   6. an action nobody can review afterwards: every one of them, and every
//      denial, is in the audit

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
	"nhooyr.io/websocket/wsjson"
)

// --- helpers ---------------------------------------------------------------

const (
	m212OperatorAccount = "google:ada"
	m212TargetAccount   = "google:bob"
)

// m212Server stands up a server with Ada on the moderator allowlist and Bob and
// Cy off it. refusalPath and auditPath may be empty for memory-only stores.
func m212Server(t *testing.T, refusalPath, auditPath string) (*WebSocketServer, string, *AuthService) {
	t.Helper()
	auth := NewAuthService("client-id", "", "", []byte("m21-2-cookie-secret"))
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.ChatDB = NewMemChatDatabase()
	server.Auth = auth
	server.Moderators = map[string]bool{m212OperatorAccount: true}
	server.Refusals = NewRefusalStore(refusalPath)
	server.Audit = NewModerationAudit(auditPath)
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	return server, "ws" + strings.TrimPrefix(httpServer.URL, "http"), auth
}

func m212Moderate(t *testing.T, ctx context.Context, conn *websocket.Conn, target PlayerID, action string) {
	t.Helper()
	if err := wsjson.Write(ctx, conn, ModerateMessage{Type: MessageTypeModerate, Action: action, PlayerID: target}); err != nil {
		t.Fatalf("write moderate %s: %v", action, err)
	}
}

func m212ReadResult(t *testing.T, ctx context.Context, conn *websocket.Conn) ModerateResultMessage {
	t.Helper()
	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, conn, &raw); err != nil {
			t.Fatalf("read moderateResult: %v", err)
		}
		var result ModerateResultMessage
		if err := json.Unmarshal(raw, &result); err != nil {
			continue
		}
		if result.Type == MessageTypeModerateResult {
			return result
		}
	}
}

func m212ReadNotice(t *testing.T, ctx context.Context, conn *websocket.Conn) ModerationNoticeMessage {
	t.Helper()
	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, conn, &raw); err != nil {
			t.Fatalf("read moderationNotice: %v", err)
		}
		var notice ModerationNoticeMessage
		if err := json.Unmarshal(raw, &notice); err != nil {
			continue
		}
		if notice.Type == MessageTypeModerationNotice {
			return notice
		}
	}
}

// m212DialFirst dials and returns the first message the server sends, whatever
// it is. dialJoinWithCookie insists on a snapshot, which is exactly what a
// refused account must NOT be given.
func m212DialFirst(t *testing.T, ctx context.Context, wsURL string, join JoinMessage, cookie *http.Cookie) (*websocket.Conn, json.RawMessage) {
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
		t.Fatalf("read first message: %v", err)
	}
	return conn, raw
}

func m212MessageType(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("unmarshal first message: %v", err)
	}
	return envelope.Type
}

// m212AuditFor finds the last audit entry for one action, which is what a test
// asserts against: the audit is append-only and earlier attempts stay in it.
func m212AuditFor(t *testing.T, server *WebSocketServer, action string) (ModerationAuditEntry, bool) {
	t.Helper()
	entries := server.Audit.Entries()
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Action == action {
			return entries[i], true
		}
	}
	return ModerationAuditEntry{}, false
}

// --- the headline claim: a mute is refused chat, and it is announced ---------

// TestM212MuteRefusesTheirChatAndTellsThem is the DoD's first sentence, and its
// second: a muted player's chat is refused and they are told; an unmuted one is
// not.
//
// The negative — the room does not receive the muted line — is proved with
// M21.1's fence rather than a timeout: the fencing line is sent by a player
// nobody is filtering, and each client has one writer, so reading the fence
// first is proof the muted line was dropped and not proof that we did not wait
// long enough.
func TestM212MuteRefusesTheirChatAndTellsThem(t *testing.T) {
	_, wsURL, auth := m212Server(t, "", "")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	adaCookie := signedAuthCookie(t, auth, AuthenticatedAccount{ID: m212OperatorAccount, Email: "ada@example.test", Name: "Ada"})

	ada, adaSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Board: 1}, adaCookie)
	defer ada.Close(websocket.StatusNormalClosure, "")
	bob, bobSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Bob", Board: 1}, nil)
	defer bob.Close(websocket.StatusNormalClosure, "")
	cy, cySnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Cy", Board: 1}, nil)
	defer cy.Close(websocket.StatusNormalClosure, "")

	if !adaSnap.Operator {
		t.Fatal("an account on the allowlist must be told it may moderate, or the window offers nothing")
	}
	if bobSnap.Operator || cySnap.Operator {
		t.Fatalf("an account off the allowlist was told it may moderate: bob=%v cy=%v", bobSnap.Operator, cySnap.Operator)
	}

	// Before the mute, Bob is heard.
	m211Say(t, ctx, bob, "before")
	if got := m211ReadChat(t, ctx, cy); got.Text != "before" {
		t.Fatalf("pre-mute line to Cy = %q, want %q", got.Text, "before")
	}
	_ = m211ReadChat(t, ctx, ada)
	_ = m211ReadChat(t, ctx, bob)

	m212Moderate(t, ctx, ada, bobSnap.You.ID, ModerationActionMute)
	result := m212ReadResult(t, ctx, ada)
	if !result.Applied || result.Action != ModerationActionMute {
		t.Fatalf("mute result = %+v, want an applied mute", result)
	}
	if result.Durable {
		t.Fatalf("a guest's mute cannot outlive their connection, but the server said it would: %+v", result)
	}
	if !strings.Contains(result.Text, "session") {
		t.Fatalf("the operator must be told a guest's mute is session-only, got %q", result.Text)
	}

	// The target is TOLD. This is the whole difference between a mute and a
	// block: a block is silent by design, a sanction announces itself.
	notice := m212ReadNotice(t, ctx, bob)
	if notice.Action != ModerationActionMute || notice.Text == "" {
		t.Fatalf("the muted player was not told: %+v", notice)
	}
	if notice.Ended {
		t.Fatal("a mute does not end the session; marking it so would send the browser back to the title screen")
	}

	// Now the refusal itself, from both sides: Bob is told his line was not
	// sent, and the room never sees it.
	m211Say(t, ctx, bob, "while muted")
	refusal := m212ReadNotice(t, ctx, bob)
	if !strings.Contains(strings.ToLower(refusal.Text), "muted") {
		t.Fatalf("a refused message must say why, got %q", refusal.Text)
	}
	m211AssertFencedOut(t, ctx, cy, ada, "fence")
	_ = m211ReadChat(t, ctx, bob) // Ada's fence, which Bob still receives: a mute is not a deafening

	// And it lifts.
	m212Moderate(t, ctx, ada, bobSnap.You.ID, ModerationActionUnmute)
	if lifted := m212ReadResult(t, ctx, ada); !lifted.Applied || lifted.Action != ModerationActionUnmute {
		t.Fatalf("unmute result = %+v", lifted)
	}
	if told := m212ReadNotice(t, ctx, bob); told.Action != ModerationActionUnmute {
		t.Fatalf("the unmuted player was not told: %+v", told)
	}
	m211Say(t, ctx, bob, "after")
	if got := m211ReadChat(t, ctx, cy); got.Text != "after" {
		t.Fatalf("after unmuting, Cy received %q, want %q", got.Text, "after")
	}
}

// TestM212KickEndsTheSessionByTheNormalLeavePath — the socket closes, the player
// is told first, and the room loses them exactly the way a closed tab loses
// them: detach, reconnect grace, then removal on the tick goroutine. No second
// removal path was written for moderation, which is the point of asserting the
// grace boundary here rather than just "they are gone".
func TestM212KickEndsTheSessionByTheNormalLeavePath(t *testing.T) {
	server, wsURL, auth := m212Server(t, "", "")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adaCookie := signedAuthCookie(t, auth, AuthenticatedAccount{ID: m212OperatorAccount, Email: "ada@example.test", Name: "Ada"})

	ada, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Board: 1}, adaCookie)
	defer ada.Close(websocket.StatusNormalClosure, "")
	bob, bobSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Bob", Board: 1}, nil)
	defer bob.Close(websocket.StatusNormalClosure, "")

	m212Moderate(t, ctx, ada, bobSnap.You.ID, ModerationActionKick)

	notice := m212ReadNotice(t, ctx, bob)
	if notice.Action != ModerationActionKick || !notice.Ended {
		t.Fatalf("a kicked player must be told the session ended: %+v", notice)
	}

	// The socket closes under them. Read until it fails: anything still queued
	// is in flight from before the kick.
	readCtx, readCancel := context.WithTimeout(ctx, 5*time.Second)
	defer readCancel()
	for {
		var raw json.RawMessage
		if err := wsjson.Read(readCtx, bob, &raw); err != nil {
			break
		}
	}

	inst := server.DefaultInstance
	waitFor(t, "the kicked player to detach", func() bool {
		_, ok := detachedCount(server, bobSnap.You.ID)
		return ok
	})

	// The normal path, not a new one: the stat lingers for the reconnect grace
	// and is removed by the tick that trips the boundary.
	for i := 0; i < ReconnectGraceTicks-1; i++ {
		server.Tick(ctx)
	}
	inst.mu.Lock()
	stillThere := inst.RoomManager.players[bobSnap.You.ID] != nil
	inst.mu.Unlock()
	if !stillThere {
		t.Fatal("a kick removed the player before the reconnect grace elapsed — that is a second removal path")
	}
	server.Tick(ctx)
	inst.mu.Lock()
	_, stillDetached := inst.Detached[bobSnap.You.ID]
	gone := inst.RoomManager.players[bobSnap.You.ID] == nil
	inst.mu.Unlock()
	if !gone || stillDetached {
		t.Fatalf("the room did not lose the kicked player: gone=%v stillDetached=%v", gone, stillDetached)
	}

	// A kick is not a refusal: the same person may come straight back.
	back, backRaw := m212DialFirst(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Bob", Board: 1}, nil)
	defer back.Close(websocket.StatusNormalClosure, "")
	if got := m212MessageType(t, backRaw); got != MessageTypeSnapshot {
		t.Fatalf("a kicked player must be able to return, but their rejoin got %q", got)
	}
}

// TestM212RefusedAccountCannotRejoinAndARefusedGuestCan is the honest limit,
// both halves in one test because the claim is the CONTRAST — and because a
// limit stated in a comment and not asserted is a limit that quietly stops being
// true.
func TestM212RefusedAccountCannotRejoinAndARefusedGuestCan(t *testing.T) {
	dir := t.TempDir()
	server, wsURL, auth := m212Server(t, filepath.Join(dir, "refused.json"), "")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adaCookie := signedAuthCookie(t, auth, AuthenticatedAccount{ID: m212OperatorAccount, Email: "ada@example.test", Name: "Ada"})
	bobCookie := signedAuthCookie(t, auth, AuthenticatedAccount{ID: m212TargetAccount, Email: "bob@example.test", Name: "Bob"})

	ada, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Board: 1}, adaCookie)
	defer ada.Close(websocket.StatusNormalClosure, "")
	bob, bobSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Board: 1}, bobCookie)
	defer bob.Close(websocket.StatusNormalClosure, "")
	guest, guestSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Gus", Board: 1}, nil)
	defer guest.Close(websocket.StatusNormalClosure, "")

	// --- the account half -------------------------------------------------
	m212Moderate(t, ctx, ada, bobSnap.You.ID, ModerationActionRefuse)
	result := m212ReadResult(t, ctx, ada)
	if !result.Applied || !result.Durable {
		t.Fatalf("refusing a signed-in account must be applied and durable: %+v", result)
	}
	if notice := m212ReadNotice(t, ctx, bob); !notice.Ended {
		t.Fatalf("a refused player must be told the session ended: %+v", notice)
	}
	if !server.Refusals.Refuses(m212TargetAccount) {
		t.Fatal("the refusal did not reach the store")
	}

	rejoin, rejoinRaw := m212DialFirst(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Board: 1}, bobCookie)
	defer rejoin.Close(websocket.StatusNormalClosure, "")
	if got := m212MessageType(t, rejoinRaw); got != MessageTypeModerationNotice {
		t.Fatalf("a refused account rejoined and got %q — refusal must be enforced at the door", got)
	}

	// The resume token is not a way back in either: the refusal is checked
	// before a resume is even considered.
	resume, resumeRaw := m212DialFirst(t, ctx, wsURL,
		JoinMessage{Type: MessageTypeJoin, Board: 1, ResumeToken: bobSnap.ResumeToken}, bobCookie)
	defer resume.Close(websocket.StatusNormalClosure, "")
	if got := m212MessageType(t, resumeRaw); got != MessageTypeModerationNotice {
		t.Fatalf("a refused account resumed its old run and got %q", got)
	}

	// --- the guest half, which is the limit -------------------------------
	m212Moderate(t, ctx, ada, guestSnap.You.ID, ModerationActionRefuse)
	guestResult := m212ReadResult(t, ctx, ada)
	if guestResult.Durable {
		t.Fatalf("a guest has no durable identity to refuse, but the server claimed one: %+v", guestResult)
	}
	if !strings.Contains(strings.ToLower(guestResult.Text), "guest") {
		t.Fatalf("the operator must be told refusing a guest only kicks them, got %q", guestResult.Text)
	}
	if notice := m212ReadNotice(t, ctx, guest); !notice.Ended {
		t.Fatalf("a refused guest is still kicked: %+v", notice)
	}
	// And here is the limit itself, asserted rather than hidden: they come back.
	backAgain, backRaw := m212DialFirst(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Gus", Board: 1}, nil)
	defer backAgain.Close(websocket.StatusNormalClosure, "")
	if got := m212MessageType(t, backRaw); got != MessageTypeSnapshot {
		t.Fatalf("a refused guest could not return, so this build's limit is not the one documented: %q", got)
	}
}

// TestM212RefusalSurvivesARestart is the owner's 2026-08-05 decision, asserted:
// the beta host restarts routinely, so a refusal that only lived in memory would
// be lifted by the next deploy.
func TestM212RefusalSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "refused.json")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	server, wsURL, auth := m212Server(t, path, "")
	adaCookie := signedAuthCookie(t, auth, AuthenticatedAccount{ID: m212OperatorAccount, Email: "ada@example.test", Name: "Ada"})
	bobCookie := signedAuthCookie(t, auth, AuthenticatedAccount{ID: m212TargetAccount, Email: "bob@example.test", Name: "Bob"})

	ada, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Board: 1}, adaCookie)
	defer ada.Close(websocket.StatusNormalClosure, "")
	bob, bobSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Board: 1}, bobCookie)
	defer bob.Close(websocket.StatusNormalClosure, "")

	m212Moderate(t, ctx, ada, bobSnap.You.ID, ModerationActionRefuse)
	if result := m212ReadResult(t, ctx, ada); !result.Applied {
		t.Fatalf("refuse result = %+v", result)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the refusal was not written to disk: %v", err)
	}
	_ = server

	// A whole new process's worth of state, reading the same document — and it
	// carries who imposed it, because a refusal read back later has to answer
	// "who did this".
	restarted := NewRefusalStore(path)
	if !restarted.Refuses(m212TargetAccount) {
		t.Fatal("a restart lifted the refusal, which is the one thing storing it on disk exists to prevent")
	}
	if restarted.Refuses("google:someone-else") {
		t.Fatal("the store refused an account nobody refused")
	}

	server2, wsURL2, auth2 := m212Server(t, path, "")
	_ = server2
	bobCookie2 := signedAuthCookie(t, auth2, AuthenticatedAccount{ID: m212TargetAccount, Email: "bob@example.test", Name: "Bob"})
	conn, raw := m212DialFirst(t, ctx, wsURL2, JoinMessage{Type: MessageTypeJoin, Board: 1}, bobCookie2)
	defer conn.Close(websocket.StatusNormalClosure, "")
	if got := m212MessageType(t, raw); got != MessageTypeModerationNotice {
		t.Fatalf("after a restart the refused account was admitted with %q", got)
	}
}

// TestM212NonOperatorsAreRefusedEveryAction — the allowlist fails closed, in all
// three of the ways it can be wrong: a signed-in player who is not on it, a
// guest (who has no account and so can never be on it), and an allowlist that is
// empty or unset.
func TestM212NonOperatorsAreRefusedEveryAction(t *testing.T) {
	actions := []string{ModerationActionMute, ModerationActionKick, ModerationActionRefuse}

	for _, allowlist := range []struct {
		name string
		set  map[string]bool
	}{
		{"configured allowlist, asker not on it", map[string]bool{m212OperatorAccount: true}},
		{"empty allowlist", map[string]bool{}},
		{"unset allowlist", nil},
	} {
		t.Run(allowlist.name, func(t *testing.T) {
			server, wsURL, auth := m212Server(t, "", "")
			// The last two cases put the operator's own account back in the
			// asker's seat: with no allowlist, even Ada is nobody.
			server.Moderators = allowlist.set
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			askerCookie := signedAuthCookie(t, auth, AuthenticatedAccount{ID: m212OperatorAccount, Email: "ada@example.test", Name: "Ada"})
			if allowlist.set[m212OperatorAccount] {
				// The "not on it" case: sign the asker in as somebody else.
				askerCookie = signedAuthCookie(t, auth, AuthenticatedAccount{ID: "google:mallory", Email: "m@example.test", Name: "Mallory"})
			}

			asker, askerSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Board: 1}, askerCookie)
			defer asker.Close(websocket.StatusNormalClosure, "")
			if askerSnap.Operator {
				t.Fatal("a player off the allowlist was told they may moderate")
			}
			target, targetSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Bob", Board: 1}, nil)
			defer target.Close(websocket.StatusNormalClosure, "")
			witness, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Cy", Board: 1}, nil)
			defer witness.Close(websocket.StatusNormalClosure, "")

			for _, action := range actions {
				m212Moderate(t, ctx, asker, targetSnap.You.ID, action)
				result := m212ReadResult(t, ctx, asker)
				if result.Applied {
					t.Fatalf("%s by a non-operator was applied: %+v", action, result)
				}
				if result.Text == "" {
					t.Fatalf("%s was refused with no explanation", action)
				}
			}

			// And none of it took effect: the target still speaks (so no mute
			// landed) and is still connected (so no kick or refusal did).
			m211Say(t, ctx, target, "still here")
			if got := m211ReadChat(t, ctx, witness); got.Text != "still here" {
				t.Fatalf("the target of a refused mute was silenced anyway: %+v", got)
			}
			if server.Refusals.Refuses("") || len(server.Audit.Entries()) == 0 {
				t.Fatal("a denied attempt must still be recorded")
			}
			for _, entry := range server.Audit.Entries() {
				if entry.Result != ModerationResultDenied {
					t.Fatalf("a non-operator's attempt was audited as %q: %+v", entry.Result, entry)
				}
			}
		})
	}
}

// TestM212EveryActionIsAudited — an unlogged moderation power is one nobody can
// review afterwards. The audit has to answer who, whom, which action, which
// world and the tick, and it has to reach the file, not just the process.
func TestM212EveryActionIsAudited(t *testing.T) {
	dir := t.TempDir()
	auditPath := filepath.Join(dir, "moderation.jsonl")
	server, wsURL, auth := m212Server(t, filepath.Join(dir, "refused.json"), auditPath)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adaCookie := signedAuthCookie(t, auth, AuthenticatedAccount{ID: m212OperatorAccount, Email: "ada@example.test", Name: "Ada"})
	bobCookie := signedAuthCookie(t, auth, AuthenticatedAccount{ID: m212TargetAccount, Email: "bob@example.test", Name: "Bob"})

	ada, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Board: 1}, adaCookie)
	defer ada.Close(websocket.StatusNormalClosure, "")
	bob, bobSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Board: 1}, bobCookie)
	defer bob.Close(websocket.StatusNormalClosure, "")

	// Run the world on a few ticks first, so the tick the audit records is a
	// number that had to come from the room the action landed in.
	for i := 0; i < 3; i++ {
		server.Tick(ctx)
	}

	m212Moderate(t, ctx, ada, bobSnap.You.ID, ModerationActionMute)
	_ = m212ReadResult(t, ctx, ada)
	m212Moderate(t, ctx, ada, bobSnap.You.ID, ModerationActionUnmute)
	_ = m212ReadResult(t, ctx, ada)
	m212Moderate(t, ctx, ada, bobSnap.You.ID, ModerationActionRefuse)
	_ = m212ReadResult(t, ctx, ada)

	for _, action := range []string{ModerationActionMute, ModerationActionUnmute, ModerationActionRefuse} {
		entry, ok := m212AuditFor(t, server, action)
		if !ok {
			t.Fatalf("%s is not in the audit", action)
		}
		if entry.Result != ModerationResultApplied {
			t.Fatalf("%s audited as %q, want applied", action, entry.Result)
		}
		if entry.Operator != m212OperatorAccount {
			t.Fatalf("%s audited with operator %q", action, entry.Operator)
		}
		if entry.Target != bobSnap.You.ID || entry.TargetAccount != m212TargetAccount {
			t.Fatalf("%s audited against target %d/%q", action, entry.Target, entry.TargetAccount)
		}
		if entry.World == "" {
			t.Fatalf("%s audited with no world", action)
		}
		if entry.Tick == 0 {
			t.Fatalf("%s audited at tick 0, so the record cannot say when it happened", action)
		}
		if entry.At.IsZero() {
			t.Fatalf("%s audited with no timestamp", action)
		}
	}

	// The file is the record; the in-memory tail is a convenience.
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("audit file has %d lines, want 3: %q", len(lines), string(data))
	}
	for _, line := range lines {
		var entry ModerationAuditEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("audit line is not JSON: %q (%v)", line, err)
		}
		if entry.Action == "" || entry.Result == "" {
			t.Fatalf("audit line says nothing: %q", line)
		}
	}
}

// TestM212ModeratorAllowlistParsing — the allowlist is the whole of operator
// status, so the ways it can be written (and mis-written) are worth pinning. The
// empty entry is the one that matters: a trailing comma putting "" in the set
// would match the empty accountID every guest carries, and hand the server to
// everybody.
func TestM212ModeratorAllowlistParsing(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{",,", nil},
		{"google:ada", []string{"google:ada"}},
		{"google:ada,google:bob", []string{"google:ada", "google:bob"}},
		{" google:ada , google:bob ", []string{"google:ada", "google:bob"}},
		{"google:ada google:bob", []string{"google:ada", "google:bob"}},
		{"google:ada,", []string{"google:ada"}},
	} {
		got := parseModeratorAccounts(tc.raw)
		if len(got) != len(tc.want) {
			t.Fatalf("parseModeratorAccounts(%q) has %d entries, want %d: %v", tc.raw, len(got), len(tc.want), got)
		}
		for _, want := range tc.want {
			if !got[want] {
				t.Fatalf("parseModeratorAccounts(%q) missing %q: %v", tc.raw, want, got)
			}
		}
		if got[""] {
			t.Fatalf("parseModeratorAccounts(%q) admitted the empty account — every guest would be an operator", tc.raw)
		}
	}
}
