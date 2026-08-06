package zztgo

// M18.17 — a resume does not wait on the socket it displaces.
//
// Two paths supersede a connection and close the one they took over from: the
// game resume (M13.2's newest-wins, websocket_server.go) and the editor's
// re-enter (M16.14f). Both used to close it GRACEFULLY, and a graceful close
// does not return until the peer answers with a close frame of its own or
// nhooyr's five-second timeout expires. The displaced socket is precisely the
// one least likely to answer — a browser that has already lost the network, or
// a tab nobody is reading — and both closes sit on the RESUMING connection's
// own join path, before its snapshot is written. So a returning player paid up
// to five seconds of blank screen for the drop that brought them back.
//
// These tests time the join rather than read the code, which is the point: the
// stall is invisible in the source (the close is one line and correctly outside
// inst.mu) and obvious on a stopwatch. Pre-fix both measured ~5.0s; the
// threshold is two seconds, wide on both sides so nothing here is a load-
// sensitive assertion.
//
// The displaced connection deliberately has no reader. That is what makes it a
// real displaced socket instead of a cooperative one: nhooyr answers a close
// frame from its read loop, so a peer nobody is reading never answers at all.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// m1817ResumeBudget is the wall-clock a displacing join is allowed. The failure
// it guards against costs five seconds, and the fix costs microseconds, so any
// value in between reads the same; two seconds is far enough from both that
// neither a slow machine nor a -race run can move the verdict.
const m1817ResumeBudget = 2 * time.Second

func TestM1817GameResumeDoesNotWaitOnTheSocketItDisplaces(t *testing.T) {
	_, wsURL, ctx, _ := reconnectServer(t)

	first, snapshot := dialJoin(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "tester", Board: 1})
	t.Cleanup(func() { _ = first.CloseNow() })

	// Nothing reads `first` from here on, so it never answers a close frame.
	start := time.Now()
	second, resumed := dialJoin(t, ctx, wsURL, JoinMessage{
		Type:        MessageTypeJoin,
		Name:        "tester",
		Board:       1,
		ResumeToken: snapshot.ResumeToken,
	})
	elapsed := time.Since(start)
	t.Cleanup(func() { _ = second.CloseNow() })

	if resumed.You.ID != snapshot.You.ID {
		t.Fatalf("the resume was given player %d, want the one it displaced, %d", resumed.You.ID, snapshot.You.ID)
	}
	if elapsed > m1817ResumeBudget {
		t.Errorf("the resuming player waited %v for their own snapshot, want under %v — the join is waiting on the close handshake of the socket it displaced",
			elapsed.Round(time.Millisecond), m1817ResumeBudget)
	}

	// The socket it displaced is still closed; only the manner changed.
	m1817ExpectClosed(t, ctx, first, "the displaced connection")
}

// m1817ExpectClosed drains conn until a read fails, which is how a closed
// connection announces itself. It drains rather than reading once because a
// displaced socket can still be holding messages that were queued for it before
// it was superseded — reading one of those proves nothing either way.
func m1817ExpectClosed(t *testing.T, ctx context.Context, conn *websocket.Conn, what string) {
	t.Helper()
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for {
		var discard json.RawMessage
		if err := wsjson.Read(readCtx, conn, &discard); err != nil {
			if readCtx.Err() != nil {
				t.Fatalf("%s was still open after two seconds of draining", what)
			}
			return
		}
	}
}

func TestM1817EditorResumeDoesNotWaitOnTheSocketItDisplaces(t *testing.T) {
	_, wsURL := m1614fServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	before, entry := m1614fEnter(t, ctx, wsURL, "before", "")

	// As above: nobody reads `before`, so the takeover's close frame goes
	// unanswered.
	start := time.Now()
	_, resumed := m1614fEnter(t, ctx, wsURL, "after", entry.ResumeToken)
	elapsed := time.Since(start)

	if resumed.MemberID != entry.MemberID {
		t.Fatalf("the re-enter was given member %q, want the membership it displaced, %q", resumed.MemberID, entry.MemberID)
	}
	if elapsed > m1817ResumeBudget {
		t.Errorf("the returning collaborator waited %v for their editor snapshot, want under %v — the re-enter is waiting on the close handshake of the socket it displaced",
			elapsed.Round(time.Millisecond), m1817ResumeBudget)
	}

	m1817ExpectClosed(t, ctx, before.conn, "the displaced editor connection")
}
