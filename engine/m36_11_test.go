package zztgo

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// Task M36.11 — no editor action can crash or wedge a session.
//
// Three ways in, one class: bytes from a client reach a board unchecked, the
// board accepts them, and a later draw, BoardClose, publish or download
// panics on them.
//
//  (a) editorPlaceTile/editorFloodFill took any element byte with copied:true.
//      54..255 panics in TileToColorAndChar, and EditAndFanOut had already
//      taken its fan-out ticket, so the ticket was never retired and every
//      later edit by every member blocked forever. copied:true with a
//      stat-backed element (an Object, a Scroll) placed a tile with no stat.
//  (b) SaveProgram capped only DataLen <= 0x7FFF, but BoardClose serializes
//      into the fixed 20000-byte IoTmpBuf. The oversized program stayed in the
//      board, so every later BoardClose panicked too.
//  (c) ImportBoard and UploadWorld rolled back only on a panic inside
//      safeBoardOpen. A board with an element byte above MAX_ELEMENT opens
//      cleanly and panics later, already committed. Ordinary world loads
//      (validateWorldBoards) are deliberately left as they were: owner
//      decision 2026-11-02, since seven community worlds carry one such tile.

// m3611Session is the editor fixture world with one member who may edit.
func m3611Session(t *testing.T) (*EditorSession, *webSocketClient) {
	t.Helper()
	session := NewEditorSession(m1614World, m1613EditorWorld(t))
	owner := &webSocketClient{name: "Ada"}
	if _, err := session.EnterNamed(owner, owner.name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Exit(owner) })
	return session, owner
}

// m3611NoPanic runs fn and fails the test, rather than the binary, if it
// panics: the panic IS the bug, and every refusal below must be a quiet one.
func m3611NoPanic(t *testing.T, what string, fn func()) (panicked bool) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked: %v", what, r)
			panicked = true
		}
	}()
	fn()
	return false
}

// m3611StillEditable proves the session is alive: a plain edit by a second
// member completes (it would block forever behind a leaked fan-out ticket),
// and the whole world still serializes (it would panic on a poisoned board).
func m3611StillEditable(t *testing.T, session *EditorSession) {
	t.Helper()
	bob := &webSocketClient{name: "Bob"}
	if _, err := session.EnterNamed(bob, bob.name); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(bob)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { recover() }()
		session.EditAndFanOut(bob, EditorEditMessage{Op: "place", X: 2, Y: 2, Element: E_SOLID, Color: 0x0e}, func(EditorDiffMessage) {})
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("a later member's edit is still blocked after 3s: the session is wedged")
	}
	m3611NoPanic(t, "serializing the world afterwards", func() {
		if _, err := session.WorldBytes(bob, ""); err != nil {
			t.Errorf("WorldBytes: %v", err)
		}
	})
}

// m3611Tile reads one tile of the member's open board.
func m3611Tile(t *testing.T, session *EditorSession, member *webSocketClient, x, y int16) TTile {
	t.Helper()
	var tile TTile
	if err := session.Apply(member, func(e *Engine) { tile = e.Board.Tiles[x][y] }); err != nil {
		t.Fatal(err)
	}
	return tile
}

// ---------------------------------------------------------------------------
// (a) element bytes and stat-backed copies
// ---------------------------------------------------------------------------

func TestM3611EditRefusesAnElementByteAboveMaxElement(t *testing.T) {
	for _, op := range []string{"place", "fill"} {
		t.Run(op, func(t *testing.T) {
			session, owner := m3611Session(t)
			before := m3611Tile(t, session, owner, 30, 12)
			m3611NoPanic(t, op+" of element 200", func() {
				session.EditAndFanOut(owner, EditorEditMessage{Op: op, X: 30, Y: 12, Element: 200, Color: 0x0e, Copied: true}, func(EditorDiffMessage) {})
			})
			if got := m3611Tile(t, session, owner, 30, 12); got != before {
				t.Errorf("tile became %+v, want it unchanged at %+v", got, before)
			}
			m3611StillEditable(t, session)
		})
	}
}

// A copied tile is only its element and colour: the fork's copy carries no
// stat, so a stat-backed element placed that way is a tile with no stat.
func TestM3611EditRefusesAStatBackedCopy(t *testing.T) {
	for _, element := range []byte{E_OBJECT, E_SCROLL, E_LION, E_PLAYER} {
		session, owner := m3611Session(t)
		before := m3611Tile(t, session, owner, 30, 12)
		var statsBefore, statsAfter int16
		session.Apply(owner, func(e *Engine) { statsBefore = e.Board.StatCount })
		m3611NoPanic(t, "a copied "+ElementDefs[element].Name, func() {
			session.Edit(owner, EditorEditMessage{Op: "place", X: 30, Y: 12, Element: element, Color: 0x0f, Copied: true})
		})
		session.Apply(owner, func(e *Engine) { statsAfter = e.Board.StatCount })
		if got := m3611Tile(t, session, owner, 30, 12); got != before || statsAfter != statsBefore {
			t.Errorf("copied %s: tile %+v (want %+v), StatCount %d -> %d: a stat-backed tile was placed without its stat",
				ElementDefs[element].Name, got, before, statsBefore, statsAfter)
		}
	}
	// A copied statless element still paints: the refusal is not a blanket one.
	session, owner := m3611Session(t)
	session.Edit(owner, EditorEditMessage{Op: "place", X: 30, Y: 12, Element: E_WATER, Color: 0x1f, Copied: true})
	if got := m3611Tile(t, session, owner, 30, 12); got.Element != E_WATER {
		t.Errorf("a copied water tile was refused (tile is %+v)", got)
	}
}

// A broadcast that panics has its ticket retired by inOrder itself. The guard
// EditAndFanOut puts around the edit must not retire it a second time: a
// served ticket never comes round again, so that would wait forever and the
// panic would never surface.
func TestM3611APanickingBroadcastRetiresItsTicketOnce(t *testing.T) {
	session, owner := m3611Session(t)
	done := make(chan interface{})
	go func() {
		defer func() { done <- recover() }()
		session.EditAndFanOut(owner, EditorEditMessage{Op: "place", X: 30, Y: 12, Element: E_SOLID, Color: 0x0e},
			func(EditorDiffMessage) { panic("broadcast failed") })
	}()
	select {
	case r := <-done:
		if r == nil {
			t.Errorf("the broadcast's panic was swallowed")
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("EditAndFanOut is still running 3s after its broadcast panicked: the ticket was retired twice")
	}
	m3611StillEditable(t, session)
}

// ---------------------------------------------------------------------------
// (b) program size
// ---------------------------------------------------------------------------

// m3611Objects adds n objects on row y of the open board and leases each to
// member, returning their stat ids.
func m3611Objects(t *testing.T, session *EditorSession, member *webSocketClient, n int, y int16) []int16 {
	t.Helper()
	var ids []int16
	var boardID int16
	session.Apply(member, func(e *Engine) {
		boardID = e.World.Info.CurrentBoard
		for i := 0; i < n; i++ {
			x := int16(2 + i)
			e.Board.Tiles[x][y] = TTile{Element: E_EMPTY}
			e.AddStat(x, y, E_OBJECT, 0x0f, 3, StatTemplateDefault)
			ids = append(ids, e.Board.StatCount)
		}
		e.BoardClose()
	})
	for _, id := range ids {
		if _, err := session.AcquireLease(member, EditorLeaseMessage{Kind: "stat", BoardID: boardID, StatID: id}); err != nil {
			t.Fatalf("lease stat %d: %v", id, err)
		}
	}
	return ids
}

func m3611Program(size int, tag string) []string {
	var lines []string
	total := 0
	for total < size {
		line := "'" + tag + strings.Repeat("x", 40)
		lines = append(lines, line)
		total += len(line) + 1
	}
	return lines
}

func m3611DataLen(session *EditorSession, member *webSocketClient, statId int16) int16 {
	var n int16
	session.Apply(member, func(e *Engine) { n = e.Board.Stats[statId].DataLen })
	return n
}

func TestM3611SaveProgramRefusesAProgramTheBoardCannotHold(t *testing.T) {
	session, owner := m3611Session(t)
	ids := m3611Objects(t, session, owner, 1, 20)
	m3611NoPanic(t, "saving a 25 KB program", func() {
		session.SaveProgram(owner, ids[0], m3611Program(25000, "a"))
	})
	if n := m3611DataLen(session, owner, ids[0]); n > 20000 {
		t.Errorf("the 25 KB program was kept (DataLen %d)", n)
	}
	m3611StillEditable(t, session)
}

// Each program fits on its own; together they do not. The one that would tip
// the board over is refused, the ones before it are kept.
func TestM3611SaveProgramRefusesTheProgramThatOverflowsTheBoard(t *testing.T) {
	session, owner := m3611Session(t)
	ids := m3611Objects(t, session, owner, 10, 20)
	accepted := 0
	for i, id := range ids {
		m3611NoPanic(t, "saving program "+string(rune('0'+i)), func() {
			session.SaveProgram(owner, id, m3611Program(3000, string(rune('a'+i))))
		})
		if m3611DataLen(session, owner, id) > 0 {
			accepted++
		}
	}
	if accepted == 0 || accepted == len(ids) {
		t.Errorf("%d of %d 3 KB programs accepted; want some kept and the overflow refused", accepted, len(ids))
	}
	m3611StillEditable(t, session)
}

// The guard on the fix itself: BoardClose stores a program once and points
// every identical one at it, so twenty objects sharing one 1500-byte program
// cost 1500 bytes, not 30000. Editing the shared source must be accepted.
func TestM3611SaveProgramCountsASharedProgramOnce(t *testing.T) {
	session, owner := m3611Session(t)
	ids := m3611Objects(t, session, owner, 21, 20)
	shared := m3611Program(1500, "shared")
	session.SaveProgram(owner, ids[0], shared)
	session.Apply(owner, func(e *Engine) {
		src := &e.Board.Stats[ids[0]]
		for _, id := range ids[1:] {
			e.Board.Stats[id].Data = src.Data
			e.Board.Stats[id].DataLen = -ids[0]
		}
		e.BoardClose()
	})
	edited := m3611Program(1500, "edited")
	m3611NoPanic(t, "editing the shared source", func() {
		session.SaveProgram(owner, ids[0], edited)
	})
	var got string
	session.Apply(owner, func(e *Engine) { got = e.Board.Stats[ids[0]].Data })
	if !strings.HasPrefix(got, "'edited") {
		t.Errorf("editing the shared source was refused: twenty bound copies were counted twenty times")
	}
	m3611StillEditable(t, session)
}

// ---------------------------------------------------------------------------
// (c) imported and uploaded boards
// ---------------------------------------------------------------------------

type m3611Poison struct {
	name   string
	poison func(e *Engine)
	// patch, if set, rewrites the serialized board: for a board BoardClose
	// itself could never have written.
	patch func(board []byte) []byte
}

// m3611PoisonedWorld is a one-board world whose board has been poisoned.
func m3611PoisonedWorld(t *testing.T, p m3611Poison) *Engine {
	t.Helper()
	e := newSnapshotEngine()
	e.WorldCreate()
	e.BoardCreate()
	p.poison(e)
	e.BoardClose()
	if p.patch != nil {
		cur := e.World.Info.CurrentBoard
		e.World.BoardData[cur] = p.patch(e.World.BoardData[cur])
		e.World.BoardLen[cur] = int16(len(e.World.BoardData[cur]))
	}
	return e
}

// m3611OversizedProgram gives the board's last stat, which must have no
// program, a 25000-byte one: more than BoardClose's 20000-byte buffer.
func m3611OversizedProgram(board []byte) []byte {
	const n = 25000
	out := append([]byte(nil), board...)
	StoreInt16(out[len(out)-SizeOfStat+23:len(out)-SizeOfStat+25], n)
	return append(out, bytes.Repeat([]byte{'\''}, n)...)
}

var m3611Poisons = []m3611Poison{
	{"tile element 200", func(e *Engine) { e.Board.Tiles[30][12] = TTile{Element: 200, Color: 0x0f} }, nil},
	{"stat standing on element 200", func(e *Engine) {
		e.AddStat(30, 12, E_OBJECT, 0x0f, 3, StatTemplateDefault)
		e.Board.Stats[e.Board.StatCount].Under = TTile{Element: 200, Color: 0x0f}
	}, nil},
	{"stat off the board", func(e *Engine) {
		e.AddStat(30, 12, E_OBJECT, 0x0f, 3, StatTemplateDefault)
		e.Board.Stats[e.Board.StatCount].X = 200
	}, nil},
}

// m3611UploadPoisons adds what only a whole-world upload can carry: ImportBoard
// already refuses a .BRD longer than the buffer, but worldReadFrom takes any
// int16 board length.
var m3611UploadPoisons = append(append([]m3611Poison(nil), m3611Poisons...), m3611Poison{
	"a 25 KB program", func(e *Engine) {
		e.AddStat(30, 12, E_OBJECT, 0x0f, 3, StatTemplateDefault)
	}, m3611OversizedProgram,
})

func TestM3611ImportBoardRefusesABoardThatCannotBeDrawn(t *testing.T) {
	for _, p := range m3611Poisons {
		t.Run(p.name, func(t *testing.T) {
			session, owner := m3611Session(t)
			if _, err := session.AcquireLease(owner, EditorLeaseMessage{Kind: "board", BoardID: 0}); err != nil {
				t.Fatal(err)
			}
			w := m3611PoisonedWorld(t, p)
			board := w.World.BoardData[w.World.Info.CurrentBoard]
			brd := make([]byte, 2, 2+len(board))
			StoreInt16(brd[:2], int16(len(board)))
			brd = append(brd, board...)
			before := m1613SessionWorldBytes(session)
			m3611NoPanic(t, "importing the board", func() { session.ImportBoard(owner, brd) })
			// The import must have been rolled back: the world is byte-identical.
			if after := m1613SessionWorldBytes(session); !bytes.Equal(before, after) {
				t.Errorf("the bad board was committed")
			}
			m3611StillEditable(t, session)
		})
	}
}

func TestM3611UploadWorldRefusesABoardThatCannotBeDrawn(t *testing.T) {
	for _, p := range m3611UploadPoisons {
		t.Run(p.name, func(t *testing.T) {
			session, owner := m3611Session(t)
			e := m3611PoisonedWorld(t, p)
			var buf bytes.Buffer
			if err := e.worldWriteTo(&buf); err != nil {
				t.Fatal(err)
			}
			before := m1613SessionWorldBytes(session)
			var gate string
			m3611NoPanic(t, "uploading the world", func() { _, gate, _ = session.UploadWorld(owner, buf.Bytes()) })
			if gate == "" {
				t.Errorf("the upload was not refused")
			}
			if after := m1613SessionWorldBytes(session); !bytes.Equal(before, after) {
				t.Errorf("the bad world was committed")
			}
			m3611StillEditable(t, session)
		})
	}
}
