package zztgo

import (
	"encoding/json"
	"testing"
)

// Task M36.2 — inbound editor names are CP437, not UTF-8.
//
// 8c97450 widened board and world names on the way OUT (EditorProperties
// BoardName/WorldName are CP437String), but the fields that bring a name back
// IN were plain strings. The browser's name prompt is pre-filled with the name
// exactly as received and only lets a person type ASCII (modal.ts entry
// handler), so what returns is the widened runes it was sent. Read as UTF-8,
// the widened 0x82 (U+0082) became the two bytes C2 82, and the name grew on
// every edit.
//
// These tests play the client's part at the real boundary: json.Marshal the
// server's reply (what goes on the wire), decode it as the browser does (a
// plain string), send that string back as JSON, and json.Unmarshal it into the
// inbound struct exactly as the socket handlers do.

// m362Wire returns s as the browser would hold it after receiving it in a
// server reply: the JSON for a CP437String, decoded as an ordinary string.
func m362Wire(t *testing.T, s CP437String) string {
	t.Helper()
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var got string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestM362PropertyNamesSurviveRepeatedEdits(t *testing.T) {
	cases := []struct {
		field string
		name  string // CP437 bytes
		read  func(EditorProperties) CP437String
	}{
		{"boardTitle", "Caf\x82 \xEC", func(p EditorProperties) CP437String { return p.BoardName }},
		{"worldName", "Z\x82T\xDB", func(p EditorProperties) CP437String { return p.WorldName }},
	}
	for _, c := range cases {
		t.Run(c.field, func(t *testing.T) {
			session := NewEditorSession("TEST", testEdgeWorld(t))
			member := &webSocketClient{}
			if err := session.Enter(member); err != nil {
				t.Fatal(err)
			}
			defer session.Exit(member)

			// The first send carries the name as the server would have sent it.
			text := m362Wire(t, CP437String(c.name))
			for edit := 1; edit <= 2; edit++ {
				raw, err := json.Marshal(map[string]string{"type": MessageTypeEditorProperty, "field": c.field, "text": text})
				if err != nil {
					t.Fatal(err)
				}
				var msg EditorPropertyMessage
				if err := json.Unmarshal(raw, &msg); err != nil {
					t.Fatal(err)
				}
				reply, err := session.SetProperty(member, msg)
				if err != nil {
					t.Fatal(err)
				}
				if got := string(c.read(reply.Properties)); got != c.name {
					t.Fatalf("edit %d: stored %q (% x), want %q (% x)", edit, got, got, c.name, c.name)
				}
				// The browser re-opens the prompt pre-filled with what it got back.
				text = m362Wire(t, c.read(reply.Properties))
			}
		})
	}
}

// editorString cuts at a byte count. With names now one byte per character it
// can no longer split a character: a name at the limit plus one high byte keeps
// exactly the first limit bytes.
func TestM362LongNameTruncatesOnACharacterBoundary(t *testing.T) {
	session := NewEditorSession("TEST", testEdgeWorld(t))
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(member)

	long := ""
	for i := 0; i < 19; i++ {
		long += "A"
	}
	long += "\x82\x82" // 21 bytes: one over the 20-byte world-name limit
	raw, _ := json.Marshal(map[string]string{"type": MessageTypeEditorProperty, "field": "worldName", "text": m362Wire(t, CP437String(long))})
	var msg EditorPropertyMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatal(err)
	}
	reply, err := session.SetProperty(member, msg)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(reply.Properties.WorldName), long[:20]; got != want {
		t.Fatalf("world name = % x, want % x", got, want)
	}
}

func TestM362AddBoardNameIsCP437(t *testing.T) {
	session := NewEditorSession("TEST", testEdgeWorld(t))
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(member)

	const name = "Caf\x82"
	raw, _ := json.Marshal(map[string]string{"type": MessageTypeEditorBoard, "op": "add", "name": m362Wire(t, CP437String(name))})
	var msg EditorBoardMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AddBoard(member, string(msg.Name)); err != nil {
		t.Fatal(err)
	}
	if err := session.Apply(member, func(e *Engine) {
		if e.Board.Name != name {
			t.Fatalf("new board name = % x, want % x", e.Board.Name, name)
		}
	}); err != nil {
		t.Fatal(err)
	}
}
