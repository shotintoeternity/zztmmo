package zztgo

// M16.14e — a collaborator is no longer dropped to the title screen because the
// server could not hand them one message inside a second.
//
// What M16.20's clean-clone run kept hitting was M16.14's act 11 timing out on a
// browser showing the TITLE SCREEN of the world it had been editing. The cause,
// instrumented rather than argued (NOTES.md 2026-08-01): `webSocketClient.write`
// gave every message a one-second context, and nhooyr.io/websocket CLOSES the
// connection when a write's context expires — there is no "this message failed,
// the socket lives" path. A member whose browser was busy for one second lost
// its editor socket, its read loop ended, `serveEditor`'s defer ran
// `session.Exit`, and the client's `close` listener drew the title screen. The
// measured shape, before the fix:
//
//	write #60 took 1.001s err=... use of closed network connection
//	MemberCount 2 -> 1
//
// Every write now goes through a bounded per-client queue drained by one writer
// goroutine, so no broadcaster waits on a browser and a stall costs the stalled
// member nothing but latency. These tests FORCE the stall rather than waiting
// for load to supply one (the M16.14b/M16.18c shape): the reader's kernel
// receive buffer is shrunk, it stops reading, and each test asserts the stall
// was really provoked — a green that provoked nothing would prove nothing.

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// m1614eStallPayload is big enough that a handful of messages exceed any
// plausible loopback socket buffer, so a reader that stops reading blocks the
// server in a bounded number of writes rather than a fragile many.
const m1614eStallPayload = 64 << 10

// m1614eProbe is a message the test can count and order in the stalled member's
// stream. It goes out through the same `write` every broadcast uses.
type m1614eProbe struct {
	Type string `json:"type"`
	Seq  int    `json:"seq"`
	Pad  string `json:"pad"`
}

const m1614eProbeType = "m1614eProbe"

// m1614eSmallSendListener shrinks the kernel send buffer on every accepted
// connection. With the receiver's buffer shrunk too, a member that stops reading
// blocks the server after a few messages instead of after a megabyte, which is
// what makes the stall reproducible on any machine rather than only a loaded one.
type m1614eSmallSendListener struct {
	net.Listener
}

func (l *m1614eSmallSendListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetWriteBuffer(4096)
	}
	return conn, nil
}

// m1614eServer starts the real server behind a listener whose sockets cannot
// hide a stalled reader behind kernel buffering.
func m1614eServer(t *testing.T) (*WebSocketServer, string) {
	t.Helper()
	world := m1613EditorWorld(t)
	world.Info.Name = m1614World
	server := NewWebSocketServer(world, 0)
	httpServer := httptest.NewUnstartedServer(server)
	httpServer.Listener = &m1614eSmallSendListener{Listener: httpServer.Listener}
	httpServer.Start()
	t.Cleanup(httpServer.Close)
	return server, "ws" + strings.TrimPrefix(httpServer.URL, "http")
}

// m1614eSlowReaderClient dials with a tiny receive buffer, the client half of
// the same arrangement.
func m1614eSlowReaderClient() *http.Client {
	dialer := &net.Dialer{}
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			if tcp, ok := conn.(*net.TCPConn); ok {
				_ = tcp.SetReadBuffer(4096)
			}
			return conn, nil
		},
	}}
}

// m1614eDialStalled enters the editor as a member that reads its entry snapshot
// and then reads nothing at all — a browser whose thread is busy.
func m1614eDialStalled(t *testing.T, ctx context.Context, wsURL, worldName string) (*websocket.Conn, EditorSnapshotMessage) {
	t.Helper()
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: m1614eSlowReaderClient()})
	if err != nil {
		t.Fatalf("dial stalled member: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	conn.SetReadLimit(ServerReadLimit)
	if err := wsjson.Write(ctx, conn, EditorEnterMessage{Type: MessageTypeEditorEnter, World: worldName}); err != nil {
		t.Fatalf("stalled member editorEnter: %v", err)
	}
	var entry EditorSnapshotMessage
	readEditorMessage(t, ctx, conn, MessageTypeEditorSnapshot, &entry)
	return conn, entry
}

// m1614eMember finds the session's client object for one member id.
func m1614eMember(t *testing.T, session *EditorSession, memberID string) *webSocketClient {
	t.Helper()
	for _, member := range session.MemberClients() {
		if session.MemberID(member) == memberID {
			return member
		}
	}
	t.Fatalf("the session holds no member %q", memberID)
	return nil
}

// m1614eStall pushes probes at a member that is not reading until its socket
// actually blocks, and returns how many it took.
//
// It keeps pushing until the member's queue is holding something, which is what
// a wedged socket looks like from the server. It is deliberately not satisfied
// by one write that merely took a while: a shallow stall the kernel absorbs is
// not the condition this bug needs, and stopping there would let a run pass
// having provoked nothing.
//
// A write that FAILS ends the run here, and that is the bug itself stated as an
// assertion — a member finished off for reading slowly. That is how these tests
// go red against the synchronous write they replaced.
func m1614eStall(t *testing.T, ctx context.Context, client *webSocketClient, limit int) int {
	t.Helper()
	pad := strings.Repeat("x", m1614eStallPayload)
	waited := false
	for seq := 1; seq <= limit; seq++ {
		start := time.Now()
		err := client.write(ctx, m1614eProbe{Type: m1614eProbeType, Seq: seq, Pad: pad})
		elapsed := time.Since(start)
		if elapsed > 100*time.Millisecond {
			waited = true
		}
		if err != nil {
			t.Fatalf("probe %d to a member that had stopped reading failed after %v (%v): a busy browser must cost it latency, not its connection",
				seq, elapsed.Round(time.Millisecond), err)
		}
		if client.queued() > 0 {
			return seq
		}
	}
	if waited {
		t.Fatalf("wrote %d probes of %d bytes at a member that had stopped reading and every one of them waited on its socket instead of being queued",
			limit, m1614eStallPayload)
	}
	t.Fatalf("wrote %d probes of %d bytes and the reader never stalled: this run proves nothing",
		limit, m1614eStallPayload)
	return 0
}

// TestM1614eStalledCollaboratorKeepsItsSessionAndCatchesUp is the inversion of
// the bug: a member that has stopped reading keeps its session and its socket,
// the session keeps serving everyone else at full speed, and the stalled member
// is handed every message it missed, in order, once it reads again.
func TestM1614eStalledCollaboratorKeepsItsSessionAndCatchesUp(t *testing.T) {
	server, wsURL := m1614eServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ada, _ := m1614DialEditor(t, ctx, wsURL, "Ada", m1614World, nil)
	stalledConn, stalledEntry := m1614eDialStalled(t, ctx, wsURL, m1614World)

	session := server.editorSessionForWorld(m1614World, TWorld{})
	if session == nil || session.MemberCount() != 2 {
		t.Fatalf("the session holds %d members, want the two that just entered", session.MemberCount())
	}
	stalled := m1614eMember(t, session, stalledEntry.MemberID)

	adaDiffs := m1614bPumpDiffs(ctx, ada.conn)

	// The stall, forced. Every one of these probes went out through the same
	// write a broadcast uses; the queue depth is the proof the socket blocked.
	probes := m1614eStall(t, ctx, stalled, clientOutboundQueue/2)
	t.Logf("the reader stalled after %d probes; %d messages are queued for it", probes, stalled.queued())

	// (a) Nobody was evicted. This is the assertion that was red before the fix:
	// the member used to lose its socket on the first write that ran past a
	// second, and its browser to draw the title screen of the world it was
	// editing.
	if got := session.MemberCount(); got != 2 {
		t.Fatalf("the session holds %d members after one of them stalled, want both: a stalled browser must not be ejected", got)
	}
	if err := stalled.write(ctx, m1614eProbe{Type: m1614eProbeType, Seq: probes + 1, Pad: strings.Repeat("x", m1614eStallPayload)}); err != nil {
		t.Fatalf("the stalled member's connection was already finished: %v", err)
	}

	// (b) The session keeps serving everyone else. The fan-out gate orders
	// enqueues now, not network writes, so the stalled member is not in anyone's
	// way — this is the property M16.14's act 11 needs.
	const cellX, cellY = 40, 9
	for i := 0; i < 3; i++ {
		start := time.Now()
		ada.send(EditorEditMessage{Type: MessageTypeEditorEdit, Op: "place",
			X: cellX, Y: int16(cellY + i), Element: E_SOLID, Color: 0x0e})
		diff := m1614bNextDiff(t, ctx, adaDiffs, "Ada")
		if elapsed := time.Since(start); elapsed > clientDrainTimeout {
			t.Fatalf("Ada's edit %d took %v to come back while another member was stalled, want well under the %v a write used to cost",
				i, elapsed.Round(time.Millisecond), clientDrainTimeout)
		}
		if len(diff.Cells) == 0 {
			t.Fatalf("Ada's edit %d came back with no cells: %+v", i, diff)
		}
	}

	// (c) Nothing was lost or reordered. The stalled member reads again and is
	// handed every probe, in the order the server queued them.
	want := probes + 1
	seen := 0
	readCtx, readCancel := context.WithTimeout(ctx, 30*time.Second)
	defer readCancel()
	for seen < want {
		var raw json.RawMessage
		if err := wsjson.Read(readCtx, stalledConn, &raw); err != nil {
			t.Fatalf("the stalled member had been handed %d of %d probes when its socket failed: %v", seen, want, err)
		}
		var probe m1614eProbe
		if json.Unmarshal(raw, &probe) != nil || probe.Type != m1614eProbeType {
			continue // the diffs and presence Ada generated, interleaved
		}
		seen++
		if probe.Seq != seen {
			t.Fatalf("the stalled member was handed probe %d where %d was queued next: the queue must deliver in order", probe.Seq, seen)
		}
	}

	// And it is still a member of the session it never left.
	if got := session.MemberCount(); got != 2 {
		t.Fatalf("the session holds %d members once the stall cleared, want both", got)
	}
}

// TestM1614eHopelessClientIsDisconnectedAloneAndSaysSo pins the other half of
// the owner's decision (2026-08-02): the queue is bounded, and a client that has
// stopped reading for longer than that budget IS disconnected — deliberately,
// with a log line, and without touching anybody else.
func TestM1614eHopelessClientIsDisconnectedAloneAndSaysSo(t *testing.T) {
	server, wsURL := m1614eServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ada, _ := m1614DialEditor(t, ctx, wsURL, "Ada", m1614World, nil)
	_, stalledEntry := m1614eDialStalled(t, ctx, wsURL, m1614World)

	session := server.editorSessionForWorld(m1614World, TWorld{})
	stalled := m1614eMember(t, session, stalledEntry.MemberID)
	adaDiffs := m1614bPumpDiffs(ctx, ada.conn)

	m1614eStall(t, ctx, stalled, clientOutboundQueue/2)

	// Push past the budget. Small messages from here: the socket is already
	// wedged, so nothing drains, and there is no reason to hold megabytes in a
	// test to prove a message count. The overflow is refused at the queue, not at
	// the wire: the caller is told, and never waits.
	var refusal error
	for seq := 0; seq <= clientOutboundQueue*2 && refusal == nil; seq++ {
		start := time.Now()
		refusal = stalled.write(ctx, m1614eProbe{Type: m1614eProbeType, Seq: seq})
		if elapsed := time.Since(start); elapsed > clientDrainTimeout {
			t.Fatalf("a write to a stalled client blocked its caller for %v; the whole point of the queue is that it does not",
				elapsed.Round(time.Millisecond))
		}
	}
	if refusal == nil {
		t.Fatalf("wrote %d messages past a %d-message queue and the client was never refused: the bound is not a bound",
			clientOutboundQueue*2, clientOutboundQueue)
	}
	if !strings.Contains(refusal.Error(), "slow client") {
		t.Fatalf("the overflow was refused with %v, want it named as the slow client it is", refusal)
	}

	// The hopeless client is dropped the same way a closed tab is: its socket is
	// closed, its read loop ends, and serveEditor's defer takes it out of the
	// session.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && session.MemberCount() > 1 {
		time.Sleep(20 * time.Millisecond)
	}
	if got := session.MemberCount(); got != 1 {
		t.Fatalf("the session holds %d members after the hopeless one overflowed, want only Ada", got)
	}

	// Alone: Ada is untouched and still editing.
	ada.send(EditorEditMessage{Type: MessageTypeEditorEdit, Op: "place", X: 41, Y: 9, Element: E_SOLID, Color: 0x0e})
	if diff := m1614bNextDiff(t, ctx, adaDiffs, "Ada"); len(diff.Cells) == 0 {
		t.Fatalf("Ada's edit came back with no cells after the other member was dropped: %+v", diff)
	}
}

// TestM1614eStalledPlayerDoesNotStallTheTick covers the same defect on the path
// that costs the most: WorldInstance.Tick writes to every client of every hosted
// world, serially, on the one tick goroutine. A single stalled browser used to
// cost that goroutine up to a second — every world, every player, once per
// stalled client — which is a beta-scale problem and not only a collaborative
// editing one.
func TestM1614eStalledPlayerDoesNotStallTheTick(t *testing.T) {
	world := testFightWorld(t)
	server := NewWebSocketServer(world, 1)
	httpServer := httptest.NewUnstartedServer(server)
	httpServer.Listener = &m1614eSmallSendListener{Listener: httpServer.Listener}
	httpServer.Start()
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// The tick loop is driven by hand here, so what is measured is the tick's own
	// cost rather than the server's cadence.
	healthy, _ := joinTestClient(t, ctx, httpServer.URL, "healthy")
	defer healthy.Close(websocket.StatusNormalClosure, "")
	go func() {
		for {
			var raw json.RawMessage
			if wsjson.Read(ctx, healthy, &raw) != nil {
				return
			}
		}
	}()

	slowWS := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	slowConn, _, err := websocket.Dial(ctx, slowWS, &websocket.DialOptions{HTTPClient: m1614eSlowReaderClient()})
	if err != nil {
		t.Fatalf("dial stalled player: %v", err)
	}
	defer slowConn.CloseNow()
	slowConn.SetReadLimit(ServerReadLimit)
	if err := wsjson.Write(ctx, slowConn, JoinMessage{Type: MessageTypeJoin, Name: "stalled", Board: 1}); err != nil {
		t.Fatalf("stalled player join: %v", err)
	}
	var snapshot SnapshotMessage
	if err := wsjson.Read(ctx, slowConn, &snapshot); err != nil {
		t.Fatalf("stalled player snapshot: %v", err)
	}

	inst := server.DefaultInstance
	var stalled *webSocketClient
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && stalled == nil {
		inst.mu.Lock()
		stalled = inst.Clients[snapshot.You.ID]
		inst.mu.Unlock()
		if stalled == nil {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if stalled == nil {
		t.Fatal("the stalled player never reached the instance")
	}

	m1614eStall(t, ctx, stalled, clientOutboundQueue/2)

	// Ten ticks with a stalled player on the board. Each one used to pay that
	// player's one-second write deadline.
	start := time.Now()
	for i := 0; i < 10; i++ {
		server.Tick(ctx)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("ten ticks took %v with one stalled player; the tick goroutine must not wait on a browser",
			elapsed.Round(time.Millisecond))
	}

	// And the stalled player is still in the game, not evicted for being slow.
	inst.mu.Lock()
	_, present := inst.Clients[snapshot.You.ID]
	inst.mu.Unlock()
	if !present {
		t.Fatalf("the stalled player was dropped from the instance by a stall of %d queued messages", stalled.queued())
	}
	if stalled.queued() == 0 {
		t.Fatal("the stalled player's queue drained during the ticks, so this run never held the tick loop against a blocked socket")
	}
}
