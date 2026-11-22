package zztgo

// M37.7: sign-in is optional. A server without it (no Google variables, so
// Auth is nil) still serves guests everything, and never tells one to sign in:
// the account features say they are for accounts instead. With sign-in on, a
// guest is still pointed at it.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func TestM377GuestTextDependsOnWhetherSignInExists(t *testing.T) {
	if got := guestRunReason(true); got != "Sign in to post a time." {
		t.Fatalf("guestRunReason(true) = %q", got)
	}
	if got := guestFollowReply(true); got != "Sign in to follow players." {
		t.Fatalf("guestFollowReply(true) = %q", got)
	}
	for _, got := range []string{guestRunReason(false), guestFollowReply(false)} {
		if strings.Contains(strings.ToLower(got), "sign in") {
			t.Fatalf("a server without sign-in tells a guest %q", got)
		}
	}
}

func m377ReadChallengeResult(t *testing.T, ctx context.Context, conn *websocket.Conn) ChallengeResultMessage {
	t.Helper()
	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, conn, &raw); err != nil {
			t.Fatalf("read the challenge result: %v", err)
		}
		var msg ChallengeResultMessage
		if err := json.Unmarshal(raw, &msg); err == nil && msg.Type == MessageTypeChallengeResult {
			return msg
		}
	}
}

// A guest's finished run, on a server with sign-in and on one without.
func TestM377GuestChallengeResultWithAndWithoutSignIn(t *testing.T) {
	for _, tc := range []struct {
		name   string
		signIn bool
		want   string
	}{
		{"sign-in on", true, "Sign in to post a time."},
		{"no sign-in", false, "Guest runs are not posted."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := m321Server(t)
			if !tc.signIn {
				h.server.Auth = nil
			}
			conn, snap := h.run(t, "?challenge=gem-dash", nil)
			defer conn.Close(websocket.StatusNormalClosure, "")
			inst, _ := h.server.ChallengeRunInstance(snap.ChallengeRun)
			m321Finish(t, h, inst, snap.You.ID)
			if got := m377ReadChallengeResult(t, h.ctx, conn).Reason; got != tc.want {
				t.Fatalf("a guest's result says %q, want %q", got, tc.want)
			}
		})
	}
}

// A guest asking to follow someone, on a server without sign-in.
func TestM377GuestFollowWithoutSignIn(t *testing.T) {
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.ChatDB = NewMemChatDatabase()
	if server.Auth != nil {
		t.Fatal("a server built without Google variables has sign-in")
	}
	httpServer := httptestServer(t, server)
	t.Cleanup(httpServer.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	conn, snap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Guest"}, nil)
	defer conn.Close(websocket.StatusNormalClosure, "")
	m261Follow(t, ctx, conn, snap.You.ID+1, true)
	if got := string(m261ReadFollowResult(t, ctx, conn).Text); got != "Following needs an account." {
		t.Fatalf("a guest's follow on a server without sign-in says %q", got)
	}
}
