package zztgo

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

type webSocketClient struct {
	playerID  PlayerID
	boardID   int16
	conn      *websocket.Conn
	mu        sync.Mutex
	worldName string
	accountID string
	name      string

	// The outbound queue (M16.14e). Every message for this client is handed to
	// out and written by one writer goroutine that owns the connection, so no
	// broadcaster ever waits on this browser: not the tick goroutine, which
	// walks every client of every hosted world, and not the editor's fan-out
	// gate, which serializes one session's broadcasts. Before this, a write
	// that could not complete inside a second closed the socket under the
	// browser — a collaborator was dropped to the title screen for being busy
	// for one second (M16.14e).
	//
	// out, quit, writerDone and writeErr are guarded by mu. A client built
	// without a connection (the session tests build these) has a nil out and
	// nothing to write to; its write is a no-op.
	out        chan interface{}
	quit       chan struct{}
	writerDone chan struct{}
	writeErr   error
}

const (
	// clientOutboundQueue is how many messages the server will hold for one
	// client that is not reading. A game client is handed roughly one message a
	// tick and an editor two per keystroke, so this is seconds of stall for the
	// editor and half a minute for a player — far more than the one second that
	// used to be fatal, and still bounded so a client that has genuinely stopped
	// reading cannot grow the server's memory without limit.
	clientOutboundQueue = 256
	// clientWriteTimeout bounds ONE message's time on the wire. Reaching it
	// means the connection is dead rather than slow: the queue, not this
	// deadline, is what absorbs a browser that stalls.
	clientWriteTimeout = 30 * time.Second
	// clientDrainTimeout bounds how long a connection teardown waits for the
	// messages already queued to reach the browser. It preserves what the old
	// synchronous write gave for free — a reply written just before the handler
	// returns still goes out — without letting a wedged socket hold the
	// handler open.
	clientDrainTimeout = time.Second
)

// errClientStopped ends the writer when its connection is being torn down. It
// is not a delivery failure: whatever is already queued is still flushed.
var errClientStopped = errors.New("client connection closed")

// newWebSocketClient wires a client to its connection and starts the single
// goroutine that writes to it. Callers must pair it with defer client.stop().
func newWebSocketClient(conn *websocket.Conn, worldName string) *webSocketClient {
	client := &webSocketClient{
		conn:       conn,
		worldName:  worldName,
		out:        make(chan interface{}, clientOutboundQueue),
		quit:       make(chan struct{}),
		writerDone: make(chan struct{}),
	}
	go client.writeLoop()
	return client
}

// writeLoop is the only goroutine that touches conn for writing, so messages
// reach this browser in the order they were queued.
func (c *webSocketClient) writeLoop() {
	defer close(c.writerDone)
	for {
		select {
		case message := <-c.out:
			if !c.writeNow(message) {
				return
			}
		case <-c.quit:
			// Flush what is already queued, so a teardown does not swallow the
			// reply that caused it. A dead connection fails the first of these
			// immediately, so this cannot linger.
			for {
				select {
				case message := <-c.out:
					if !c.writeNow(message) {
						return
					}
				default:
					return
				}
			}
		}
	}
}

// writeNow puts one message on the wire and reports whether the connection is
// still usable. The deadline is the client's own, never the caller's: a
// broadcast used to hand one member's request context to another member's
// write, so a member who had just left could expire an innocent recipient's
// deadline and close their socket.
func (c *webSocketClient) writeNow(message interface{}) bool {
	ctx, cancel := context.WithTimeout(context.Background(), clientWriteTimeout)
	defer cancel()
	if err := wsjson.Write(ctx, c.conn, message); err != nil {
		c.fail(err)
		return false
	}
	return true
}

// markDead records the first reason this client can take no more messages and
// releases the writer. It reports whether this call was that first reason.
func (c *webSocketClient) markDead(err error) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.out == nil || c.writeErr != nil {
		return false
	}
	c.writeErr = err
	close(c.quit)
	return true
}

// fail closes the socket under a client that cannot be written to. Closing it
// is what ends that connection's read loop, which runs the same teardown a
// browser closing its tab does — detach with reconnect grace for a player, and
// session.Exit for an editor member.
//
// CloseNow, not Close: fail runs on whichever goroutine was writing, including
// the tick goroutine, and a close handshake can take seconds.
func (c *webSocketClient) fail(err error) {
	if !c.markDead(err) {
		return
	}
	if c.conn != nil {
		_ = c.conn.CloseNow()
	}
}

// stop ends the writer when a connection is being torn down, giving the
// messages already queued a bounded chance to reach the browser first.
func (c *webSocketClient) stop() {
	c.markDead(errClientStopped)
	c.mu.Lock()
	done := c.writerDone
	c.mu.Unlock()
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(clientDrainTimeout):
	}
}

// queued reports how many messages are waiting for this client. Tests only: it
// is how a test proves it really did stall a reader rather than passing because
// the kernel happened to buffer everything.
func (c *webSocketClient) queued() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.out)
}

func (c *webSocketClient) currentWorldName() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.worldName
}

func (c *webSocketClient) setWorldName(worldName string) {
	c.mu.Lock()
	c.worldName = worldName
	c.mu.Unlock()
}

// write hands one message to this client's writer and returns without waiting
// for the network (M16.14e). An error means the client is finished — its queue
// overflowed, or an earlier message failed — never that this one message was
// slow.
//
// ctx is the caller's context and deliberately bounds nothing here: the message
// belongs to the client it is addressed to, not to whichever connection or tick
// produced it. The writer's own deadline is what bounds the wire.
func (c *webSocketClient) write(ctx context.Context, message interface{}) error {
	c.mu.Lock()
	if c.out == nil {
		// No connection to write to (the session tests build these).
		err := c.writeErr
		c.mu.Unlock()
		return err
	}
	if c.writeErr != nil {
		err := c.writeErr
		c.mu.Unlock()
		return err
	}
	select {
	case c.out <- message:
		c.mu.Unlock()
		return nil
	default:
	}
	c.mu.Unlock()

	// The queue is full: this client has not read clientOutboundQueue messages
	// worth of the game. That is no longer a browser being busy, and the
	// alternatives are worse — dropping messages would leave its screen holding
	// tiles the world no longer has, and blocking here would stall the tick
	// goroutine or the whole editor session behind it. Disconnect it, and say so.
	err := fmt.Errorf("slow client: %d queued messages unread", clientOutboundQueue)
	c.fail(err)
	log.Printf("zztgo: disconnecting slow client %q on world %q: %v", c.name, c.worldName, err)
	return err
}
