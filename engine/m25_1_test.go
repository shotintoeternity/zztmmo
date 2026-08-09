package zztgo

// M25.1 — in-session private messages.
//
// The transport is intentionally live-only: PlayerID names a connection in this
// process, so a PM can be delivered to somebody in the room roster without
// inventing offline identity or history. The tests assert the privacy at the
// socket boundary, where a client-side filter would be too late.

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

type m251Line struct {
	Type string
	Chat m211Chat
	PM   PrivateMessage
}

func m251ReadChatOrPM(t *testing.T, ctx context.Context, conn *websocket.Conn) m251Line {
	t.Helper()
	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, conn, &raw); err != nil {
			t.Fatalf("read chat/private: %v", err)
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			continue
		}
		switch envelope.Type {
		case "chat":
			var chat m211Chat
			if err := json.Unmarshal(raw, &chat); err != nil {
				t.Fatalf("unmarshal chat: %v", err)
			}
			return m251Line{Type: "chat", Chat: chat}
		case MessageTypePrivateMessage:
			var pm PrivateMessage
			if err := json.Unmarshal(raw, &pm); err != nil {
				t.Fatalf("unmarshal privateMessage: %v", err)
			}
			return m251Line{Type: MessageTypePrivateMessage, PM: pm}
		}
	}
}

func m251ReadPM(t *testing.T, ctx context.Context, conn *websocket.Conn) PrivateMessage {
	t.Helper()
	line := m251ReadChatOrPM(t, ctx, conn)
	if line.Type != MessageTypePrivateMessage {
		t.Fatalf("got %s, want privateMessage: %+v", line.Type, line.Chat)
	}
	return line.PM
}

func m251ReadPrivateResult(t *testing.T, ctx context.Context, conn *websocket.Conn) PrivateResultMessage {
	t.Helper()
	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, conn, &raw); err != nil {
			t.Fatalf("read privateMessageResult: %v", err)
		}
		var result PrivateResultMessage
		if err := json.Unmarshal(raw, &result); err != nil {
			continue
		}
		if result.Type == MessageTypePrivateResult {
			return result
		}
	}
}

func m251PM(t *testing.T, ctx context.Context, conn *websocket.Conn, target PlayerID, text string) {
	t.Helper()
	if err := wsjson.Write(ctx, conn, PrivateMessage{Type: MessageTypePrivateMessage, PlayerID: target, Text: text}); err != nil {
		t.Fatalf("write privateMessage: %v", err)
	}
}

func TestM251PrivateMessageOnlyReachesSenderAndRecipient(t *testing.T) {
	db := NewMemChatDatabase()
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.ChatDB = db
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	ada, adaSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Ada", Board: 1}, nil)
	defer ada.Close(websocket.StatusNormalClosure, "")
	bo, boSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Bo", Board: 1}, nil)
	defer bo.Close(websocket.StatusNormalClosure, "")
	cy, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Cy", Board: 1}, nil)
	defer cy.Close(websocket.StatusNormalClosure, "")

	m251PM(t, ctx, ada, boSnap.You.ID, "meet by the torch")
	incoming := m251ReadPM(t, ctx, bo)
	if incoming.From != "Ada" || incoming.FromID != adaSnap.You.ID || incoming.ToID != boSnap.You.ID || incoming.Text != "meet by the torch" || incoming.Outgoing {
		t.Fatalf("incoming PM = %+v", incoming)
	}
	outgoing := m251ReadPM(t, ctx, ada)
	if !outgoing.Outgoing || outgoing.To != "Bo" || outgoing.Text != incoming.Text {
		t.Fatalf("sender confirmation PM = %+v", outgoing)
	}

	m211Say(t, ctx, bo, "fence")
	if got := m251ReadChatOrPM(t, ctx, cy); got.Type != "chat" || got.Chat.Text != "fence" {
		t.Fatalf("PM leaked before the fence: %+v", got)
	}
	_ = m211ReadChat(t, ctx, ada)
	_ = m211ReadChat(t, ctx, bo)

	records, err := db.GetRecentMessages(10)
	if err != nil {
		t.Fatalf("read chat history: %v", err)
	}
	if len(records) != 1 || records[0].Text != "fence" {
		t.Fatalf("PM must not be persisted in global chat history, got %+v", records)
	}
}

func TestM251PrivateMessageHonorsRecipientBlock(t *testing.T) {
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.ChatDB = NewMemChatDatabase()
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	ada, adaSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Ada", Board: 1}, nil)
	defer ada.Close(websocket.StatusNormalClosure, "")
	bo, boSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Bo", Board: 1}, nil)
	defer bo.Close(websocket.StatusNormalClosure, "")
	cy, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Cy", Board: 1}, nil)
	defer cy.Close(websocket.StatusNormalClosure, "")

	m211Block(t, ctx, bo, adaSnap.You.ID, true)
	_ = m211ReadBlockResult(t, ctx, bo)

	m251PM(t, ctx, ada, boSnap.You.ID, "you should not see this")
	result := m251ReadPrivateResult(t, ctx, ada)
	if result.Delivered || result.PlayerID != boSnap.You.ID || !strings.Contains(result.Text, "not available") {
		t.Fatalf("blocked PM result = %+v", result)
	}

	m211Say(t, ctx, cy, "fence")
	if got := m251ReadChatOrPM(t, ctx, bo); got.Type != "chat" || got.Chat.Text != "fence" {
		t.Fatalf("blocked PM reached the recipient before the fence: %+v", got)
	}
	_ = m211ReadChat(t, ctx, ada)
	_ = m211ReadChat(t, ctx, cy)
}

func TestM251PrivateMessageSharesChatRateLimit(t *testing.T) {
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.ChatDB = NewMemChatDatabase()
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	ada, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Ada", Board: 1}, nil)
	defer ada.Close(websocket.StatusNormalClosure, "")
	bo, boSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Bo", Board: 1}, nil)
	defer bo.Close(websocket.StatusNormalClosure, "")
	cy, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Cy", Board: 1}, nil)
	defer cy.Close(websocket.StatusNormalClosure, "")

	for i := 0; i < chatRateLimitMax; i++ {
		m251PM(t, ctx, ada, boSnap.You.ID, "ping")
		_ = m251ReadPM(t, ctx, bo)
		_ = m251ReadPM(t, ctx, ada)
	}
	m251PM(t, ctx, ada, boSnap.You.ID, "too much")
	result := m251ReadPrivateResult(t, ctx, ada)
	if result.Delivered || !strings.Contains(result.Text, "quickly") {
		t.Fatalf("rate-limited PM result = %+v", result)
	}

	m211Say(t, ctx, cy, "fence")
	if got := m251ReadChatOrPM(t, ctx, bo); got.Type != "chat" || got.Chat.Text != "fence" {
		t.Fatalf("rate-limited PM reached the recipient before the fence: %+v", got)
	}
	_ = m211ReadChat(t, ctx, ada)
	_ = m211ReadChat(t, ctx, cy)
}

func TestM251MutedPlayerCannotSendPrivateMessages(t *testing.T) {
	_, wsURL, auth := m212Server(t, "", "")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	adaCookie := signedAuthCookie(t, auth, AuthenticatedAccount{ID: m212OperatorAccount, Email: "ada@example.test", Name: "Ada"})

	ada, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Board: 1}, adaCookie)
	defer ada.Close(websocket.StatusNormalClosure, "")
	bo, boSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Bo", Board: 1}, nil)
	defer bo.Close(websocket.StatusNormalClosure, "")
	cy, cySnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Cy", Board: 1}, nil)
	defer cy.Close(websocket.StatusNormalClosure, "")

	m212Moderate(t, ctx, ada, boSnap.You.ID, ModerationActionMute)
	_ = m212ReadResult(t, ctx, ada)
	_ = m212ReadNotice(t, ctx, bo)

	m251PM(t, ctx, bo, cySnap.You.ID, "muted whisper")
	notice := m212ReadNotice(t, ctx, bo)
	if notice.Action != ModerationActionMute || !strings.Contains(strings.ToLower(notice.Text), "muted") {
		t.Fatalf("muted PM notice = %+v", notice)
	}

	m211Say(t, ctx, ada, "fence")
	if got := m251ReadChatOrPM(t, ctx, cy); got.Type != "chat" || got.Chat.Text != "fence" {
		t.Fatalf("muted PM reached the target before the fence: %+v", got)
	}
	_ = m211ReadChat(t, ctx, ada)
	_ = m211ReadChat(t, ctx, bo)
}
