package zztgo

// M42.7: the join snapshot is compressed on the wire. A player far from the
// server waited about 0.8 s after the join for a 65 KB snapshot that takes
// several round trips to arrive; deflated it is about 5 KB.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

const (
	m427Chrome  = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	m427Safari  = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15"
	m427IOSChr  = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/131.0.6778.73 Mobile/15E148 Safari/604.1"
	m427Firefox = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:133.0) Gecko/20100101 Firefox/133.0"
	m427IOSFox  = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) FxiOS/133.0 Mobile/15E148 Safari/605.1.15"
)

// WebKit's permessage-deflate fails against this server, and every iOS
// browser is WebKit, so only Chromium and Gecko are offered compression.
func TestM427CompressionIsOfferedOnlyToChromiumAndGecko(t *testing.T) {
	for ua, want := range map[string]bool{m427Chrome: true, m427Firefox: true, m427Safari: false, m427IOSChr: false, m427IOSFox: false, "": false} {
		if got := compressingBrowser(ua); got != want {
			t.Errorf("compressingBrowser(%q) = %v, want %v", ua, got, want)
		}
	}
}

// A Safari client that offers compression is answered without it.
func TestM427SafariIsNotAnsweredWithCompression(t *testing.T) {
	server := NewWebSocketServer(testSoakWorld(t), 1)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http"), &websocket.DialOptions{
		CompressionMode: websocket.CompressionContextTakeover,
		HTTPHeader:      http.Header{"User-Agent": []string{m427Safari}},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if ext := resp.Header.Get("Sec-WebSocket-Extensions"); ext != "" {
		t.Fatalf("Safari was answered with %q", ext)
	}
}

func TestM427TheServerNegotiatesCompressionAndStillJoins(t *testing.T) {
	server := NewWebSocketServer(testSoakWorld(t), 1)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http"), &websocket.DialOptions{
		CompressionMode: websocket.CompressionContextTakeover,
		HTTPHeader:      http.Header{"User-Agent": []string{m427Chrome}},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if ext := resp.Header.Get("Sec-WebSocket-Extensions"); !strings.Contains(ext, "permessage-deflate") {
		t.Fatalf("the server did not agree to compression: Sec-WebSocket-Extensions = %q", ext)
	}
	conn.SetReadLimit(ServerReadLimit)
	if err := wsjson.Write(ctx, conn, JoinMessage{Type: MessageTypeJoin, Name: "Zip", Board: 1}); err != nil {
		t.Fatalf("join: %v", err)
	}
	var snapshot SnapshotMessage
	if err := wsjson.Read(ctx, conn, &snapshot); err != nil {
		t.Fatalf("read the compressed snapshot: %v", err)
	}
	if snapshot.Type != MessageTypeSnapshot || len(snapshot.Screen) == 0 {
		t.Fatalf("got %q with %d cells, want a full snapshot", snapshot.Type, len(snapshot.Screen))
	}
}
