package zztgo

// M36.6 — one encoding policy at the protocol boundary.
//
// cp437.go names three kinds of text, and every string a protocol message
// carries must be one of them:
//
//   - world bytes (scrolls, programs, board and world titles) are CP437 and
//     travel as CP437String / CP437Lines, widened byte for rune;
//   - text a person typed (chat, names, prompts) is folded to printable ASCII
//     where it is admitted, so it is one glyph per byte by the time anything
//     stores or sends it;
//   - protocol tokens (message types, enums, ids, base64, sanitized file
//     stems, hex colours) are ASCII by construction.
//
// A plain Go string is only safe for the last two. This file makes that a
// checked rule rather than a habit: TestM366ProtocolStringFieldsAreClassified
// fails on any string field in protocol.go that is not on the allowlist below
// with the reason it is ASCII, so the next field that carries world bytes has
// to opt into CP437String or say why it need not.
//
// It walks protocol.go's syntax (go/ast) rather than reflecting from a list of
// root types: a list is one more thing to forget to extend, while a parse sees
// a new struct the moment it is written. The cost is that a message built as an
// anonymous struct elsewhere is invisible to it, which is why the chat frames
// that used to be anonymous are now ChatMessage, here.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"
)

// Reasons a plain string is safe. Each allowlist entry cites one.
const (
	m366Token    = "protocol token: a MessageType*/enum value the server writes or validates"
	m366ID       = "identifier the server mints or validates (account/member/run ids, resume tokens)"
	m366Base64   = "base64 of world or board bytes"
	m366SaveName = "SanitizeSaveName stem or hosted instance name (1-8 of [A-Z0-9_-])"
	m366Color    = "SanitizePlayerColor: '#' plus six hex digits, or empty"
	m366Handle   = "NormalizeProfileHandle: [a-z0-9_] only"
	m366Literal  = "server-authored ASCII message, at most interpolating other fields on this list"
	m366Element  = "ElementDefs string, ASCII (TestM366ElementDefsAreASCII)"
	m366Name     = "a player name, folded to printable ASCII at join (admitPlayerName)"
	m366Typed    = "typed text, folded to printable ASCII where it is admitted"
	m366Profile  = "profile text: sanitizeProfileLine admits printable ASCII only, plus a folded name"
)

// m366StringFields is every plain string / []string field in protocol.go and
// why it may stay one. Key: "Struct.Field".
var m366StringFields = map[string]string{
	"ChallengeResultMessage.ChallengeID": m366ID,
	"ChallengeResultMessage.RecordingID": m366ID,
	"ChallengeResultMessage.Reason":      m366Literal,
	"ChallengeErrorMessage.ChallengeID":  m366ID,
	"ChallengeErrorMessage.Reason":       m366Literal,

	"ModerateMessage.Action":                m366Token,
	"ModerateResultMessage.Action":          m366Token,
	"ModerateResultMessage.Name":            m366Name,
	"ModerateResultMessage.Text":            m366Literal,
	"ModerationNoticeMessage.Action":        m366Token,
	"ModerationNoticeMessage.Text":          m366Literal,
	"ReplayControlMessage.Op":               m366Token,
	"ReplayErrorMessage.Text":               m366Literal,
	"ProfileResultMessage.Name":             m366Name,
	"ProfileResultMessage.Handle":           m366Handle,
	"ProfileResultMessage.Lines":            m366Profile,
	"PrivateMessage.From":                   m366Name,
	"PrivateMessage.To":                     m366Name,
	"PrivateMessage.Text":                   m366Typed + " (admitChatText)",
	"PrivateResultMessage.Text":             m366Literal,
	"FollowResultMessage.Name":              m366Name,
	"FollowResultMessage.Text":              m366Literal,
	"BlockResultMessage.Name":               m366Name,
	"BlockResultMessage.Text":               m366Literal,
	"AnnounceMessage.Text":                  m366Literal,
	"ChatMessage.From":                      m366Name,
	"ChatMessage.Text":                      m366Typed + " (admitChatText)",
	"JoinMessage.Name":                      m366Name,
	"JoinMessage.World":                     m366SaveName,
	"JoinMessage.Color":                     m366Color,
	"JoinMessage.ResumeToken":               m366ID,
	"EditorEnterMessage.World":              m366SaveName,
	"EditorEnterMessage.ResumeToken":        m366ID,
	"EditorPresence.ID":                     m366ID,
	"EditorPresence.Name":                   m366Name,
	"EditorLeaseMessage.Op":                 m366Token,
	"EditorLeaseMessage.Kind":               m366Token,
	"EditorLeaseMessage.HolderID":           m366ID,
	"EditorLeaseMessage.HolderName":         m366Name,
	"EditorLeaseMessage.Error":              m366Literal,
	"EditorTileInspect.Element":             m366Element,
	"EditorTileInspect.Param1Name":          m366Element,
	"EditorTileInspect.Param2Name":          m366Element,
	"EditorTileInspect.ParamBulletTypeName": m366Element,
	"EditorTileInspect.ParamDirName":        m366Element,
	"EditorElementItem.Name":                m366Element,
	"EditorElementItem.Shortcut":            m366Element,
	"EditorElementItem.CategoryName":        m366Element,
	"EditorElementMenu.Key":                 m366Token,
	"EditorElementMenu.Title":               m366Literal + " (\"Item\", \"Creature\", \"Terrain\")",
	"EditorSnapshotMessage.MemberID":        m366ID,
	"EditorSnapshotMessage.ResumeToken":     m366ID,
	"EditorEditMessage.Op":                  m366Token,
	"EditorDiffMessage.MemberID":            m366ID,
	"EditorPropertyMessage.Field":           m366Token,
	"EditorStatMessage.Field":               m366Token,
	"EditorProgramMessage.Prompt":           m366Element,
	"EditorBoardMessage.Op":                 m366Token,
	"EditorBoardMessage.Data":               m366Base64,
	"EditorBoardDataMessage.Name":           m366SaveName,
	"EditorBoardDataMessage.Data":           m366Base64,
	"EditorWorldMessage.Op":                 m366Token,
	"EditorWorldMessage.Name":               m366SaveName,
	"EditorWorldMessage.Data":               m366Base64,
	"EditorWorldMessage.AccountID":          m366ID,
	"EditorWorldDataMessage.Name":           m366SaveName,
	"EditorWorldDataMessage.Data":           m366Base64,
	"EditorSaveResultMessage.World":         m366SaveName,
	"EditorSaveResultMessage.Error":         m366Literal,
	"EditorTestPlayMessage.World":           m366SaveName,
	"EditorTestPlayMessage.Error":           m366Literal,
	"DebugCommandMessage.Text":              m366Typed + " (admitDebugCommandText)",
	"HighScoreNameMessage.Name":             m366Typed + " (admitHighScoreName)",
	"SaveFilenameMessage.Name":              m366SaveName,
	"SnapshotMessage.World":                 m366SaveName,
	"SnapshotMessage.ResumeToken":           m366ID,
	"SnapshotMessage.Challenge":             m366ID,
	"SnapshotMessage.ChallengeRun":          m366ID,
	"PlayerSnapshot.Name":                   m366Name,
	"PlayerSnapshot.Color":                  m366Color,
	"PlayerSnapshot.Handle":                 m366Handle,
	"ProtocolEvent.Filename":                m366Literal + " (only ever \"GAME.HLP\")",
	"ProtocolEvent.Error":                   m366Literal,
}

// m366ProtocolStringFields parses protocol.go and returns every plain string
// field ("Struct.Field"), skipping each struct's Type, which
// TestM366MessageTypesAreASCII covers as one rule.
func m366ProtocolStringFields(t *testing.T) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "protocol.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}
		st, ok := spec.Type.(*ast.StructType)
		if !ok {
			return true
		}
		for _, field := range st.Fields.List {
			if !m366IsPlainString(field.Type) {
				continue
			}
			for _, name := range field.Names {
				if name.Name == "Type" {
					continue
				}
				out = append(out, spec.Name.Name+"."+name.Name)
			}
			if len(field.Names) == 0 {
				t.Errorf("%s embeds a plain string; name the field so it can be classified", spec.Name.Name)
			}
		}
		return true
	})
	sort.Strings(out)
	return out
}

// m366IsPlainString reports whether a field's type puts a Go string on the
// wire with no codec: string, or any slice, array, map or pointer of one.
func m366IsPlainString(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name == "string"
	case *ast.ArrayType:
		return m366IsPlainString(t.Elt)
	case *ast.StarExpr:
		return m366IsPlainString(t.X)
	case *ast.MapType:
		return m366IsPlainString(t.Key) || m366IsPlainString(t.Value)
	case *ast.StructType:
		for _, f := range t.Fields.List {
			if m366IsPlainString(f.Type) {
				return true
			}
		}
	}
	return false
}

func TestM366ProtocolStringFieldsAreClassified(t *testing.T) {
	found := m366ProtocolStringFields(t)
	seen := make(map[string]bool, len(found))
	for _, key := range found {
		seen[key] = true
		if _, ok := m366StringFields[key]; !ok {
			t.Errorf("%s is a plain string on the wire with no stated reason it is ASCII. "+
				"If it can carry world bytes make it CP437String/CP437Lines; if it is typed text, fold it where "+
				"it is admitted; otherwise add it to m366StringFields with the reason (M36.6).", key)
		}
	}
	var stale []string
	for key := range m366StringFields {
		if !seen[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	for _, key := range stale {
		t.Errorf("m366StringFields lists %s, which protocol.go no longer has as a plain string; remove it", key)
	}
}

// Every struct's Type field carries one of the MessageType* constants or a
// ProtocolEvent type literal; both are ASCII, which this checks once.
func TestM366MessageTypesAreASCII(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "protocol.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		checked++
		for i := 0; i < len(lit.Value); i++ {
			if lit.Value[i] >= 0x80 {
				t.Errorf("protocol.go string literal %s holds a non-ASCII byte", lit.Value)
				break
			}
		}
		return true
	})
	if checked == 0 {
		t.Fatal("no string literals found in protocol.go; the check is watching nothing")
	}
}

// The editor menus and inspector send ElementDefs' names as plain strings, so
// they must stay ASCII. Vanilla's are; this keeps them so.
func TestM366ElementDefsAreASCII(t *testing.T) {
	ascii := func(s string) bool {
		for i := 0; i < len(s); i++ {
			if s[i] < 0x20 || s[i] >= 0x7F {
				return false
			}
		}
		return true
	}
	for el, def := range ElementDefs {
		for field, s := range map[string]string{
			"Name": def.Name, "CategoryName": def.CategoryName,
			"Param1Name": def.Param1Name, "Param2Name": def.Param2Name,
			"ParamBulletTypeName": def.ParamBulletTypeName, "ParamDirName": def.ParamDirName,
			"ParamTextName": def.ParamTextName, "ParamBoardName": def.ParamBoardName,
		} {
			if !ascii(s) {
				t.Errorf("ElementDefs[%d].%s = %q is not printable ASCII, but goes on the wire as a plain string", el, field, s)
			}
		}
		if c := def.EditorShortcut; c != 0 && (c < 0x20 || c >= 0x7F) {
			t.Errorf("ElementDefs[%d].EditorShortcut = %#x is not printable ASCII", el, c)
		}
	}
}

func TestM366PlayerNamesAreFoldedAtJoin(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"José", "Jos"},
		{"Ada", "Ada"},
		{"  Bo  ", "Bo"},
		{"Zoë “Z” Smith", "Zo \"Z\" Smith"},
		{"李明", m366NameFallback},
		{"", ""},
		{"line\nbreak\x00", "linebreak"},
		{strings.Repeat("x", 100), strings.Repeat("x", playerNameMax)},
	}
	for _, c := range cases {
		if got := admitPlayerName(c.raw); got != c.want {
			t.Errorf("admitPlayerName(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

// m366NameFallback is what a name with nothing printable becomes.
const m366NameFallback = "Player"

func TestM366DebugCommandTextIsFolded(t *testing.T) {
	for raw, want := range map[string]string{
		"+café":    "+caf",
		"zap":      "zap",
		"gems\x01": "gems",
	} {
		if got := admitDebugCommandText(raw); got != want {
			t.Errorf("admitDebugCommandText(%q) = %q, want %q", raw, got, want)
		}
	}
}

// The seam itself: whichever way a player is named — a guest's join name or an
// account's display name — the roster every client is sent carries the folded
// form, so a high byte cannot reach the CP437 canvas through a name.
func TestM366RosterCarriesFoldedNames(t *testing.T) {
	rm := NewRoomManager(townWorld(t))
	guest := rm.JoinPlayer(1, 0, 0)
	member := rm.JoinPlayer(1, 0, 0)
	rm.SetPlayerName(guest, "José")
	rm.SetPlayerIdentity(member, "google:zoe", "Zoë")
	snap, ok := rm.Snapshot(guest)
	if !ok {
		t.Fatal("no snapshot")
	}
	names := map[PlayerID]string{}
	for _, p := range snap.Players {
		names[p.ID] = p.Name
	}
	if names[guest] != "Jos" || names[member] != "Zo" {
		t.Fatalf("roster names = %q, want guest %q and member %q", names, "Jos", "Zo")
	}
	if _, name, _ := rm.PlayerIdentity(member); name != "Zo" {
		t.Fatalf("stored identity name = %q, want the folded %q", name, "Zo")
	}
}
