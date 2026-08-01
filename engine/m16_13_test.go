package zztgo

// M16.13 — solo browser editor and portable-output parity.
//
// WHAT THIS IS. The editor is the one part of the product a player uses to
// produce a FILE, and a file is the only artifact that outlives this server. So
// this sweep has two halves that have to hold together:
//
//  1. THE VOCABULARY. Every editor key and every dialog is driven as a real
//     KeyboardEvent (or a real mouse drag, or a real file chooser) on the built
//     client, against the production server objects — the same harness M16.9
//     stood up and M16.10 reused. Unit tests over web/src/editor*.ts cannot see
//     a handler that never registered, a modal that ate the key first, or an
//     `op` string the server spells differently from the client that sent it.
//     m1613Commands below is the manifest of that vocabulary, and it is
//     FAIL-CLOSED: the key codes are scanned out of web/src/main.ts and the
//     operation names out of editor_session.go / websocket_server.go, so adding
//     an editor key or an editor op without covering it reddens this test.
//
//  2. THE OUTPUT. The browser downloads the .ZZT and .BRD the session produced,
//     and those bytes are then read back by m1613ReadVanillaWorld — a reader
//     written from the published ZZT file format (reference/fileformat.html;
//     the same record layout as reference/reconstruction-of-zzt SRC/GAME.PAS
//     WorldLoad and BoardOpen), NOT from this fork's worldReadFrom. A file that
//     only this engine can read is not portable, and a round trip through our
//     own reader could not tell the difference. The independent parse is then
//     compared field by field against the editor session's own live state,
//     which is the authority the browser was editing.
//
//     WHAT THAT DOES AND DOES NOT CLAIM. It claims the bytes conform to the
//     published format: every record boundary lands where the format says, no
//     board is short or long, no trailing bytes are left over, and every field
//     the editor believes it wrote is where a foreign reader would look for it.
//     It does not claim the real ZZT.EXE was asked to open the file. That is the
//     M16.2 oracle's job and it is not reachable here: a capture compares a whole
//     board after an unmodelled boot span, and this world is deliberately full of
//     creatures and devices, which have moved by then (fixtures/oracle/mech.scn
//     documents the same constraint). Handing vanilla ZZT a *static* world this
//     editor authored would close that last gap and is worth a later task.
//
// WHY THE EDITOR NEEDS NO TICK LOCK. An EditorSession is never ticked (it is
// deliberately not a RoomManager room), so nothing in the editor moves between
// keystrokes and there is no tick order to pin. The harness still runs the
// production server objects with the ticker stopped; ticks are taken only for
// the test-play half, where a live room really is running.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const m1613World = "EDIT"

// ---------------------------------------------------------------------------
// The world
// ---------------------------------------------------------------------------

// m1613EditorWorld compiles fixtures/editor.zwd. The editor session opens
// World.Info.CurrentBoard, which a ZWD-compiled world always leaves at 0 —
// BoardOpen does not move it — so board 0 is the draft board the browser edits.
func m1613EditorWorld(t *testing.T) TWorld {
	t.Helper()

	src, err := os.ReadFile(filepath.Join("..", "fixtures", "editor.zwd"))
	if err != nil {
		t.Fatalf("read fixtures/editor.zwd: %v", err)
	}
	world, err := CompileZWDWorld(string(src))
	if err != nil {
		t.Fatalf("CompileZWDWorld(fixtures/editor.zwd): %v", err)
	}

	// The browser script addresses this world by board name and by tile
	// coordinate. If the fixture is reshaped, say so here rather than forty
	// keystrokes later as "timed out waiting for the stat prompt".
	e := NewEngine()
	e.Headless = true
	e.World = world
	if world.Info.CurrentBoard != 0 {
		t.Fatalf("fixtures/editor.zwd compiles with CurrentBoard %d, want 0 — the editor opens that board",
			world.Info.CurrentBoard)
	}
	e.BoardOpen(0)
	if e.Board.Name != "Edit Draft" {
		t.Fatalf("board 0 is %q, want \"Edit Draft\" — fixtures/editor.zwd changed shape", e.Board.Name)
	}
	if world.BoardCount != 1 {
		t.Fatalf("fixtures/editor.zwd has %d boards past the first, want 1 (Edit Annex)", world.BoardCount)
	}
	for _, want := range []struct {
		x, y    int16
		element byte
		name    string
	}{
		{10, 5, E_OBJECT, "the object with a program"},
		{14, 5, E_LION, "the lion"},
		{18, 5, E_SPINNING_GUN, "the spinning gun"},
		{22, 5, E_PASSAGE, "the passage"},
		{26, 5, E_DUPLICATOR, "the duplicator"},
		{12, 15, E_NORMAL, "the coloured wall run"},
		{20, 15, E_TEXT_YELLOW, "the text tile"},
	} {
		if got := e.Board.Tiles[want.x][want.y].Element; got != want.element {
			t.Fatalf("%s at %d,%d is element %d, want %d — fixtures/editor.zwd changed shape",
				want.name, want.x, want.y, got, want.element)
		}
	}
	if got := e.Board.Stats[0]; got.X != 30 || got.Y != 20 {
		t.Fatalf("player starts at %d,%d, want 30,20 — fixtures/editor.zwd changed shape", got.X, got.Y)
	}
	e.BoardClose()
	return world
}

func m1613NewHarness(t *testing.T) *m169Harness {
	t.Helper()
	return m169NewHarnessFor(t, m1613World, m1613EditorWorld(t))
}

// ---------------------------------------------------------------------------
// Control routes: the editor session, seen from outside
// ---------------------------------------------------------------------------

// editorControlRoutes adds M16.13's endpoints to the harness control listener
// (m169Harness.controlMux). They exist so the browser script and this test can
// read the EDITING WORLD — the authority the browser is editing — without going
// through the browser's own screen, which is the thing under test.
func (h *m169Harness) editorControlRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/control/editor/world", func(w http.ResponseWriter, r *http.Request) {
		session := h.editorSession()
		if session == nil {
			http.Error(w, "no editor session for "+h.worldName, http.StatusNotFound)
			return
		}
		data := m1613SessionWorldBytes(session)
		if data == nil {
			http.Error(w, "the editor session would not serialize", http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]string{"data": base64.StdEncoding.EncodeToString(data)})
	})
	// The F1/F2/F3 tables, from ElementDefs. The browser script drives the
	// element sweep from these AND checks them against the picker the sidebar
	// actually draws, so "every placeable element" means every element the
	// engine says is placeable, not every element the client happened to list.
	mux.HandleFunc("/control/editor/menus", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, m1613MenusWithPrompts())
	})
	// /control/editor/board is the authority the browser is editing, read
	// straight off the session engine: the tiles it believes are there and the
	// stats behind them. The browser script asserts against THIS for every world
	// change, and against its own canvas for every piece of chrome — a client
	// that drew a convincing tile it never sent would satisfy only one of the two.
	mux.HandleFunc("/control/editor/board", func(w http.ResponseWriter, r *http.Request) {
		session := h.editorSession()
		if session == nil {
			http.Error(w, "no editor session for "+h.worldName, http.StatusNotFound)
			return
		}
		session.mu.Lock()
		defer session.mu.Unlock()
		e := session.engine
		var elements, colors strings.Builder
		for y := int16(1); y <= BOARD_HEIGHT; y++ {
			for x := int16(1); x <= BOARD_WIDTH; x++ {
				tile := e.Board.Tiles[x][y]
				fmt.Fprintf(&elements, "%02x", tile.Element)
				fmt.Fprintf(&colors, "%02x", tile.Color)
			}
		}
		stats := []map[string]interface{}{}
		for i := int16(0); i <= e.Board.StatCount; i++ {
			stat := e.Board.Stats[i]
			entry := map[string]interface{}{
				"id": i, "x": stat.X, "y": stat.Y,
				"stepX": stat.StepX, "stepY": stat.StepY, "cycle": stat.Cycle,
				"p1": stat.P1, "p2": stat.P2, "p3": stat.P3,
				"element": e.Board.Tiles[stat.X][stat.Y].Element,
				"dataLen": stat.DataLen,
			}
			if stat.DataLen > 0 {
				entry["program"] = stat.Data[:stat.DataLen]
			}
			stats = append(stats, entry)
		}
		writeJSON(w, map[string]interface{}{
			"members":    len(session.Members),
			"properties": editorProperties(e),
			"boardCount": e.World.BoardCount,
			"statCount":  e.Board.StatCount,
			"elements":   elements.String(),
			"colors":     colors.String(),
			"stats":      stats,
		})
	})
}

// m1613MenusWithPrompts is editorElementMenus plus, per item, WHICH dialog the
// browser will be looking at the moment that element lands — because vanilla
// runs EditorEditStat straight after AddStat, and the shape of that dialog is
// decided by the element's own parameter names (openEditorStatSettings builds
// its item list in exactly this order). Telling the script up front turns a
// race ("did a prompt open?") into a wait, and keeps the knowledge derived from
// ElementDefs rather than hand-listed in the script.
func m1613MenusWithPrompts() []map[string]interface{} {
	firstPrompt := func(id byte) string {
		def := ElementDefs[id]
		switch {
		case def.Cycle == -1 || id == E_PLAYER:
			return "" // no stat is added, so no parameter dialog follows
		case def.Param1Name != "":
			return "sidebar"
		case def.ParamTextName != "":
			return "program"
		case def.Param2Name != "", def.ParamBulletTypeName != "", def.ParamDirName != "":
			return "sidebar"
		case def.ParamBoardName != "":
			return "board"
		default:
			return ""
		}
	}
	out := []map[string]interface{}{}
	for _, menu := range editorElementMenus() {
		items := []map[string]interface{}{}
		for _, item := range menu.Items {
			items = append(items, map[string]interface{}{
				"elementId":   item.ElementID,
				"name":        item.Name,
				"shortcut":    item.Shortcut,
				"character":   item.Character,
				"color":       item.Color,
				"firstPrompt": firstPrompt(item.ElementID),
				// The program editor takes its window title from the element's
				// own ParamTextName ("Edit Program", "Edit text of scroll").
				"promptTitle": ElementDefs[item.ElementID].ParamTextName,
				// Whether placing it adds a stat at all. The client asks for a
				// stat lease after ANY stat-backed placement, even one with no
				// parameters to edit, and opening that (empty) dialog clears
				// whatever menu is on the sidebar — so the script has to let the
				// round trip finish before pressing the next F-key.
				"statBacked": ElementDefs[item.ElementID].Cycle != -1 && item.ElementID != E_PLAYER,
			})
		}
		out = append(out, map[string]interface{}{
			"key": menu.Key, "title": menu.Title, "items": items,
		})
	}
	return out
}

func (h *m169Harness) editorSession() *EditorSession {
	h.server.mu.Lock()
	defer h.server.mu.Unlock()
	return h.server.EditorWorldSessions[h.worldName]
}

// m1613SessionWorldBytes is EditorSession.WorldBytes without the membership
// check: this reads the authority from OUTSIDE the session, at moments when the
// browser has already left the editor (after test play, say) and there is no
// member to act as. It goes through the same worldWriteTo seam WorldSave and
// WorldBytes use, and it takes s.mu, so no member operation can interleave.
// TestM1613SessionWorldBytesMatchesTheSessionsOwnSerializer keeps the two from
// drifting apart.
func m1613SessionWorldBytes(s *EditorSession) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.engine
	e.BoardClose()
	e.World.Info.IsSave = false
	var buf bytes.Buffer
	err := e.worldWriteTo(&buf)
	e.BoardOpen(e.World.Info.CurrentBoard)
	if err != nil {
		return nil
	}
	return buf.Bytes()
}

func TestM1613SessionWorldBytesMatchesTheSessionsOwnSerializer(t *testing.T) {
	session := NewEditorSession("EDIT", m1613EditorWorld(t))
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(member)
	if _, err := session.Edit(member, EditorEditMessage{Op: "place", X: 40, Y: 18, Element: E_SOLID, Color: 0x0e}); err != nil {
		t.Fatalf("Edit: %v", err)
	}

	want, err := session.WorldBytes(member, "")
	if err != nil {
		t.Fatalf("WorldBytes: %v", err)
	}
	got := m1613SessionWorldBytes(session)
	if !bytes.Equal(got, want) {
		t.Fatalf("m1613SessionWorldBytes produced %d bytes, WorldBytes produced %d, and they differ — "+
			"the outside-the-session reader has drifted from the session's own serializer", len(got), len(want))
	}
}

// ---------------------------------------------------------------------------
// The editor command manifest
// ---------------------------------------------------------------------------

// m1613Command is one row of the editor command manifest: a key the browser
// binds or an operation the server accepts, and the evidence that it works.
//
// Evidence is one of:
//   - "browser" — engine/web/test/editor_solo.test.mjs records this id in its
//     run report, having actually pressed the key or driven the dialog.
//   - "go:TestName" — a Go test covers it, for the operations with no key of
//     their own. The named test must exist (existingGoTestNames).
type m1613Command struct {
	ID       string
	What     string
	Evidence string
}

// The manifest. Every key id is derived from web/src/main.ts and every op id
// from the Go server, so this list cannot fall behind the code: see
// TestM1613EditorCommandManifestHasNoUntestedKeyOrDialog.
var m1613Commands = []m1613Command{
	// --- getting in (web/src/title.ts) ------------------------------------
	{"key.title.KeyE", "E on the title screen opens an editor session", "browser"},

	// --- the editor board, top level (main.ts handleEditorKey) -------------
	{"key.editor.Escape", "Escape leaves the editor (and first closes the collaborator legend)", "browser"},
	{"key.editor.KeyQ", "Q leaves the editor, via EditorAskSaveChanged's \"Save first?\"", "browser"},
	{"key.editor.F1", "F1 opens the Item element menu", "browser"},
	{"key.editor.F2", "F2 opens the Creature element menu", "browser"},
	{"key.editor.F3", "F3 opens the Terrain element menu", "browser"},
	{"key.editor.F4", "F4 toggles text-entry mode", "browser"},
	{"key.editor.KeyZ", "Z clears the board after a yes/no prompt", "browser"},
	{"key.editor.KeyN", "N makes a new world after a yes/no prompt", "browser"},
	{"key.editor.KeyH", "H opens EDITOR.HLP", "browser"},
	{"key.editor.KeyW", "W toggles the collaborator legend", "browser"},
	{"key.editor.KeyI", "I opens Board Information", "browser"},
	{"key.editor.KeyB", "B opens the board switcher", "browser"},
	{"key.editor.KeyT", "T opens the .BRD transfer menu", "browser"},
	{"key.editor.KeyS", "S opens the world menu", "browser"},
	{"key.editor.KeyP", "P cycles the pattern brush", "browser"},
	{"key.editor.KeyC", "C cycles the brush colour", "browser"},
	{"key.editor.KeyX", "X flood-fills from the cursor", "browser"},
	{"key.editor.Tab", "Tab toggles draw mode", "browser"},
	{"key.editor.Space", "Space plots the brush (or edits the stat under the cursor)", "browser"},
	{"key.editor.Enter", "Enter copies the tile into the brush, or opens the stat editor", "browser"},
	{"key.editor.Delete", "Delete erases the tile under the cursor", "browser"},
	{"key.editor.Backspace", "Backspace erases the tile under the cursor", "browser"},
	{"key.editor.ArrowUp", "Up moves the cursor (and draws with Shift or draw mode)", "browser"},
	{"key.editor.ArrowDown", "Down moves the cursor", "browser"},
	{"key.editor.ArrowLeft", "Left moves the cursor", "browser"},
	{"key.editor.ArrowRight", "Right moves the cursor", "browser"},
	{"key.editor.Numpad8", "Numpad 8 moves the cursor up", "browser"},
	{"key.editor.Numpad2", "Numpad 2 moves the cursor down", "browser"},
	{"key.editor.Numpad4", "Numpad 4 moves the cursor left", "browser"},
	{"key.editor.Numpad6", "Numpad 6 moves the cursor right", "browser"},

	// --- text-entry mode (main.ts handleEditorTextKey) --------------------
	{"key.text.Enter", "Enter leaves text-entry mode", "browser"},
	{"key.text.Escape", "Escape leaves text-entry mode", "browser"},
	{"key.text.Backspace", "Backspace erases the tile to the left and steps back", "browser"},
	{"key.text.Delete", "Delete erases the tile to the left and steps back", "browser"},
	{"key.text.printable", "a printable key writes a text tile and advances the cursor", "browser"},

	// --- the stat parameter prompt (main.ts handleEditorStatPromptKey) ----
	{"key.stat.Escape", "Escape abandons the stat prompt", "browser"},
	{"key.stat.Enter", "Enter commits the parameter and advances to the next", "browser"},
	{"key.stat.Tab", "Tab steps a character parameter by nine", "browser"},
	{"key.stat.ArrowLeft", "Left decreases a slider/character/choice", "browser"},
	{"key.stat.ArrowRight", "Right increases a slider/character/choice", "browser"},
	{"key.stat.Numpad4", "Numpad 4 decreases a slider/character/choice", "browser"},
	{"key.stat.Numpad6", "Numpad 6 increases a slider/character/choice", "browser"},
	{"key.stat.digit", "1-9 set a slider directly", "browser"},

	// --- the sidebar action menu (main.ts handleEditorSidebarMenuKey) -----
	{"key.menu.Escape", "Escape closes the sidebar action menu", "browser"},
	{"key.menu.Enter", "Enter picks the selected item", "browser"},
	{"key.menu.Space", "Space picks the selected item", "browser"},
	{"key.menu.ArrowUp", "Up moves the selection", "browser"},
	{"key.menu.ArrowDown", "Down moves the selection", "browser"},
	{"key.menu.Numpad8", "Numpad 8 moves the selection up", "browser"},
	{"key.menu.Numpad2", "Numpad 2 moves the selection down", "browser"},
	{"key.menu.Home", "Home selects the first item", "browser"},
	{"key.menu.End", "End selects the last item", "browser"},
	{"key.menu.shortcut", "a shortcut letter picks its item directly", "browser"},

	// --- the F1/F2/F3 element picker (main.ts handleEditorCategoryKey) ----
	{"key.category.Escape", "Escape closes the element picker without placing", "browser"},
	{"key.category.shortcut", "an element's shortcut key places that element", "browser"},
	{"key.category.nomatch", "any other key closes the picker without placing", "browser"},

	// --- the mouse (main.ts handlePointerDown / handlePointerMove) --------
	{"pointer.place", "a click on the board places the brush at that cell", "browser"},
	{"pointer.drag", "dragging with the button down draws along the path", "browser"},

	// --- edit operations (editor_session.go Edit) -------------------------
	{"op.edit.place", "place the pattern/copied brush", "browser"},
	{"op.edit.erase", "erase to Empty", "browser"},
	{"op.edit.fill", "flood fill", "browser"},
	{"op.edit.element", "place a category element, seeding its stat", "browser"},
	{"op.edit.text", "place one text-entry character", "browser"},

	// --- board properties (editor_session.go SetProperty) -----------------
	{"op.property.boardTitle", "rename the board", "browser"},
	{"op.property.worldName", "rename the world", "browser"},
	{"op.property.maxShots", "set the board's maximum player shots", "browser"},
	{"op.property.dark", "toggle darkness", "browser"},
	{"op.property.exit", "point a board edge at another board", "browser"},
	{"op.property.reenter", "toggle re-enter when zapped", "browser"},
	{"op.property.timeLimit", "set the board time limit", "browser"},

	// --- stat parameters (editor_session.go SetStat) ----------------------
	{"op.stat.p1", "set parameter 1 (slider or character)", "browser"},
	{"op.stat.p2", "set parameter 2", "browser"},
	{"op.stat.bulletType", "set the Bullets/Stars choice", "browser"},
	{"op.stat.direction", "set the direction choice", "browser"},
	{"op.stat.p3", "set the board parameter", "browser"},
	// The client has no cycle control: EditorEditStat does not offer one, so
	// vanilla's own dialog never sets it either. The field exists on the wire
	// for completeness and is covered where it is implemented.
	{"op.stat.cycle", "set a stat's cycle (server-side only; no editor key)", "go:TestEditorSessionStatSettingsPreserveVanillaStatSemantics"},

	// --- board operations (websocket_server.go serveEditorBoard) ----------
	{"op.board.add", "append a new named board", "browser"},
	{"op.board.switch", "switch to another board", "browser"},
	{"op.board.export", "download the board as vanilla .BRD", "browser"},
	{"op.board.import", "replace the board from a .BRD file", "browser"},
	{"op.board.clear", "clear the board", "browser"},
	{"op.board.new", "reset the session to a new world", "browser"},

	// --- world operations (websocket_server.go serveEditorWorld) ----------
	{"op.world.save", "publish the world so others can play it", "browser"},
	{"op.world.download", "download the world as portable .ZZT", "browser"},
	{"op.world.upload", "replace the world from a .ZZT file, through the M7.5 gate", "browser"},
	{"op.world.invite", "invite a collaborator by account id", "browser"},

	// --- the remaining editor protocol messages --------------------------
	{"op.program.read", "read a stat's ZZT-OOP program into the code editor", "browser"},
	{"op.program.save", "write an edited ZZT-OOP program back to its stat", "browser"},
	{"op.testPlay", "host the edited world as an isolated test-play instance", "browser"},
}

// m1613CuratedIDs are the manifest rows that no scanner can derive, because the
// branch they name is not a literal key code or op string in the source. Each is
// listed with the branch it stands for, so a reader can check the claim.
var m1613CuratedIDs = map[string]string{
	"key.text.printable":    "handleEditorTextKey's `event.key.length === 1` branch",
	"key.stat.digit":        "handleEditorStatPromptKey's `event.key >= \"1\" && event.key <= \"9\"` branch",
	"key.menu.shortcut":     "handleEditorSidebarMenuKey's shortcut lookup over menu.items",
	"key.category.shortcut": "handleEditorCategoryKey's shortcut lookup over menu.items",
	"key.category.nomatch":  "handleEditorCategoryKey's no-match fall-through, which closes the picker",
	"pointer.place":         "handlePointerDown's editor branch",
	"pointer.drag":          "handlePointerMove's editor branch",
	"op.program.read":       "MessageTypeEditorProgram, whose handler takes no op string",
	"op.program.save":       "MessageTypeEditorProgramSave, whose handler takes no op string",
	"op.testPlay":           "MessageTypeEditorTestPlay, whose handler takes no op string",
	"key.title.KeyE":        "title.ts TITLE_CODES maps KeyE to the \"editor\" action — the door to every key above",
}

// ---------------------------------------------------------------------------
// Deriving the vocabulary from the source
// ---------------------------------------------------------------------------

// m1613FuncBody returns the brace-balanced body that follows header in src.
// Used on both TypeScript and Go sources; neither of the functions it is
// pointed at contains a brace inside a string or comment, and the callers
// assert on a sentinel from the body so a bad extraction is loud rather than
// silently empty.
func m1613FuncBody(t *testing.T, src, header, sentinel string) string {
	t.Helper()
	start := strings.Index(src, header)
	if start < 0 {
		t.Fatalf("%q no longer appears in the source — the M16.13 command scanner is looking at code that moved", header)
	}
	open := strings.Index(src[start:], "{")
	if open < 0 {
		t.Fatalf("%q has no body", header)
	}
	open += start
	depth := 0
	for i := open; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				body := src[open : i+1]
				if !strings.Contains(body, sentinel) {
					t.Fatalf("the body extracted for %q does not contain %q — the scanner mis-parsed", header, sentinel)
				}
				return body
			}
		}
	}
	t.Fatalf("%q has an unbalanced body", header)
	return ""
}

var (
	// A key or an op is spelled one of two ways in the sources this scans: as a
	// switch case (both languages) or as an `event.code ===` comparison, which
	// is how the text-entry handler reads Enter/Escape/Backspace/Delete.
	m1613CaseRE = regexp.MustCompile(`case "([A-Za-z0-9]+)":`)
	m1613CodeRE = regexp.MustCompile(`event\.code === "([A-Za-z0-9]+)"`)
)

func m1613ReadSource(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// m1613DerivedCommands scans the client and the server for every editor key
// code and every editor operation name. The result is the set of ids the
// manifest MUST cover.
func m1613DerivedCommands(t *testing.T) map[string]string {
	t.Helper()
	derived := map[string]string{}
	add := func(id, where string) {
		if prev, ok := derived[id]; ok && prev != where {
			t.Fatalf("id %s is derived twice, from %s and %s", id, prev, where)
		}
		derived[id] = where
	}

	main := m1613ReadSource(t, filepath.Join("web", "src", "main.ts"))
	for _, fn := range []struct{ header, prefix, sentinel string }{
		{"function handleEditorKey(", "key.editor", "leaveEditor()"},
		{"function handleEditorTextKey(", "key.text", "optimisticEditorTextCell"},
		{"function handleEditorStatPromptKey(", "key.stat", "advanceEditorStatPrompt()"},
		{"function handleEditorSidebarMenuKey(", "key.menu", "pickEditorSidebarItem"},
		{"function handleEditorCategoryKey(", "key.category", "selectEditorMenuItem"},
	} {
		body := m1613FuncBody(t, main, fn.header, fn.sentinel)
		for _, re := range []*regexp.Regexp{m1613CaseRE, m1613CodeRE} {
			for _, m := range re.FindAllStringSubmatch(body, -1) {
				add(fn.prefix+"."+m[1], fn.header)
			}
		}
	}

	session := m1613ReadSource(t, "editor_session.go")
	for _, fn := range []struct{ header, prefix, sentinel string }{
		// The edit switch moved into EditAndFanOut at M16.14b (Edit is now the
		// no-broadcast caller of it); the dispatch it derives op.edit.* from is
		// the same one.
		{"func (s *EditorSession) EditAndFanOut(", "op.edit", "editorPlaceTile"},
		{"func (s *EditorSession) SetProperty(", "op.property", "e.Board.Info.MaxShots"},
		{"func (s *EditorSession) SetStat(", "op.stat", "EditorStatSettings"},
	} {
		body := m1613FuncBody(t, session, fn.header, fn.sentinel)
		for _, m := range m1613CaseRE.FindAllStringSubmatch(body, -1) {
			add(fn.prefix+"."+m[1], fn.header)
		}
	}

	server := m1613ReadSource(t, "websocket_server.go")
	for _, fn := range []struct{ header, prefix, sentinel string }{
		{"func (s *WebSocketServer) serveEditorBoard(", "op.board", "session.AddBoard"},
		{"func (s *WebSocketServer) serveEditorWorld(", "op.world", "session.UploadWorld"},
	} {
		body := m1613FuncBody(t, server, fn.header, fn.sentinel)
		for _, m := range m1613CaseRE.FindAllStringSubmatch(body, -1) {
			add(fn.prefix+"."+m[1], fn.header)
		}
	}
	return derived
}

// TestM1613EditorCommandManifestHasNoUntestedKeyOrDialog is the DoD's first
// clause, made fail-closed. Bind a new editor key in main.ts, or accept a new
// `op` in the session, and this reddens until the manifest gains a row — and a
// row may not claim evidence that does not exist.
func TestM1613EditorCommandManifestHasNoUntestedKeyOrDialog(t *testing.T) {
	derived := m1613DerivedCommands(t)

	listed := map[string]m1613Command{}
	for _, cmd := range m1613Commands {
		if _, dup := listed[cmd.ID]; dup {
			t.Errorf("duplicate manifest row %s", cmd.ID)
		}
		if cmd.What == "" {
			t.Errorf("manifest row %s has no description", cmd.ID)
		}
		listed[cmd.ID] = cmd
	}

	var missing []string
	for id, where := range derived {
		if _, ok := listed[id]; !ok {
			missing = append(missing, fmt.Sprintf("%s (from %s)", id, where))
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("the editor command manifest does not cover %d derived key(s)/op(s):\n  %s\n"+
			"add a row to m1613Commands and exercise it in engine/web/test/editor_solo.test.mjs",
			len(missing), strings.Join(missing, "\n  "))
	}

	var stale []string
	for id := range listed {
		if _, ok := derived[id]; ok {
			continue
		}
		if _, curated := m1613CuratedIDs[id]; curated {
			continue
		}
		stale = append(stale, id)
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("manifest rows name nothing in the source and are not curated: %s", strings.Join(stale, ", "))
	}

	for id := range m1613CuratedIDs {
		if _, ok := listed[id]; !ok {
			t.Errorf("curated id %s has no manifest row", id)
		}
	}

	goTests := existingGoTestNames(t)
	for _, cmd := range m1613Commands {
		switch {
		case cmd.Evidence == "browser":
		case strings.HasPrefix(cmd.Evidence, "go:"):
			name := strings.TrimPrefix(cmd.Evidence, "go:")
			if !goTests[name] {
				t.Errorf("manifest row %s names Go test %s, which does not exist", cmd.ID, name)
			}
		default:
			t.Errorf("manifest row %s has unrecognised evidence %q", cmd.ID, cmd.Evidence)
		}
	}
}

// ---------------------------------------------------------------------------
// What this sweep found, and gap task M16.13a closed
// ---------------------------------------------------------------------------

// TestM1613aEditorSessionInstallsTheEditorElementTable is the inverted pin for
// M16.13a's first finding.
//
// EditorLoop's very first act is InitElementsEditor (editor.go:513): it sets
// ForceDarknessOff so a dark board is EDITED LIT, and gives E_INVISIBLE the
// 0xB0 glyph so invisible walls can be seen and moved. NewEditorSession did
// neither, so a board became unreadable the moment its "Board is dark" was
// turned on, and an invisible wall was invisible to the person placing it — you
// cannot edit what you cannot see.
//
// The override cannot be vanilla's write into ElementDefs: that table is shared
// with every live room here. It rides the Engine instead (Engine.EditorElements,
// read through Engine.ElementCharacter), and
// TestM1613aRoomKeepsGameElementsBesideAnEditorSession is the other half of that
// claim.
func TestM1613aEditorSessionInstallsTheEditorElementTable(t *testing.T) {
	session := NewEditorSession("EDIT", m1613EditorWorld(t))
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(member)

	lit, err := session.Snapshot(member, 30, 12)
	if err != nil {
		t.Fatal(err)
	}
	// A lit board draws its tiles: most of the 60x25 field is empty floor, and
	// the drawn furniture is a small minority. That is the shape to compare
	// against, so "the board went dark" would be measured rather than asserted
	// by eye.
	litDrawn := m1613DrawnBoardCells(lit.Screen)
	if litDrawn == 0 || litDrawn >= BOARD_WIDTH*BOARD_HEIGHT {
		t.Fatalf("the lit draft board drew %d of %d cells; the fixture is not what this test assumes",
			litDrawn, BOARD_WIDTH*BOARD_HEIGHT)
	}

	dark, err := session.SetProperty(member, EditorPropertyMessage{Field: "dark", Bool: true})
	if err != nil {
		t.Fatal(err)
	}
	if darkDrawn := m1613DrawnBoardCells(dark.Screen); darkDrawn != litDrawn {
		t.Errorf("turning the board dark drew %d of %d board cells, want the lit board's %d: "+
			"the editor sets ForceDarknessOff (editor.go:513), so darkness must not reach the screen",
			darkDrawn, BOARD_WIDTH*BOARD_HEIGHT, litDrawn)
	}
	// Stronger than the count: every board cell must be the cell it was.
	if diffs := m1613BoardCellDiffs(lit.Screen, dark.Screen); len(diffs) > 0 {
		t.Errorf("turning the board dark changed %d board cell(s), first: %s", len(diffs), diffs[0])
	}

	// An invisible wall, placed the way the browser places one, must be visible
	// to the person who placed it.
	const ix, iy = 5, 5
	diff, err := session.Edit(member, EditorEditMessage{
		Op: "element", X: ix, Y: iy, Element: E_INVISIBLE, Color: 0x0E,
	})
	if err != nil {
		t.Fatal(err)
	}
	cell, ok := screenCell(diff.Cells, ix-1, iy-1)
	if !ok {
		t.Fatalf("placing an invisible wall dirtied no cell at (%d,%d); cells=%v", ix-1, iy-1, diff.Cells)
	}
	if cell.Ch != EditorInvisibleChar {
		t.Errorf("an invisible wall drew %#02x in the editor, want %#02x (InitElementsEditor's glyph)",
			cell.Ch, byte(EditorInvisibleChar))
	}

	err = session.Apply(member, func(e *Engine) {
		if !e.ForceDarknessOff {
			t.Error("the editor session must set ForceDarknessOff, as InitElementsEditor does")
		}
		if !e.EditorElements {
			t.Error("the editor session must have the editor element table installed")
		}
		// The shared table is NOT where the override went.
		if ElementDefs[E_INVISIBLE].Character != ' ' {
			t.Errorf("ElementDefs[E_INVISIBLE].Character is %#02x: the editor override belongs on the "+
				"Engine, not on the table every live room shares", ElementDefs[E_INVISIBLE].Character)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestM1613aRoomKeepsGameElementsBesideAnEditorSession is the constraint that
// made M16.13a's first finding more than a one-line call: ElementDefs is one
// package-level table shared by every room, so installing the editor's element
// overrides into it would reach into worlds nobody is editing. A room ticking
// beside an editor session must still draw darkness as darkness and an invisible
// wall as nothing at all.
func TestM1613aRoomKeepsGameElementsBesideAnEditorSession(t *testing.T) {
	world := m1613aDarkRoomWorld(t)
	server := NewWebSocketServer(world, m1613aDarkBoard)

	dweller := server.RoomManager.JoinPlayer(m1613aDarkBoard, BOARD_WIDTH/2, BOARD_HEIGHT/2)
	visitor := server.RoomManager.JoinPlayer(m1613aLitBoard, BOARD_WIDTH/2, BOARD_HEIGHT/2)
	m1613aRequireGameRoom(t, server, dweller, visitor, "before an editor session exists")

	// The editor opens on the same world, on the same server, and on the same
	// two boards — the arrangement vanilla never has to survive.
	session := server.editorSessionForWorld("DARK", world)
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(member)

	dark, err := session.SwitchBoard(member, m1613aDarkBoard)
	if err != nil {
		t.Fatal(err)
	}
	// The editor sees the cellar LIT, walls and all.
	solid, ok := screenCell(dark.Screen, m1613aSolidX-1, m1613aSolidY-1)
	if !ok {
		t.Fatal("the editor snapshot has no cell where the cellar's wall stands")
	}
	if solid.Color != m1613aWallColor {
		t.Errorf("the editor drew the dark cellar's wall as {ch:%#02x color:%#02x}, want it lit at colour %#02x",
			solid.Ch, solid.Color, byte(m1613aWallColor))
	}

	lit, err := session.SwitchBoard(member, m1613aLitBoard)
	if err != nil {
		t.Fatal(err)
	}
	// ...and it sees the invisible wall in the hall.
	wall, ok := screenCell(lit.Screen, m1613aInvisibleX-1, m1613aInvisibleY-1)
	if !ok {
		t.Fatal("the editor snapshot has no cell where the invisible wall stands")
	}
	if wall.Ch != EditorInvisibleChar || wall.Color != m1613aWallColor {
		t.Errorf("the editor drew the invisible wall as {ch:%#02x color:%#02x}, want {ch:%#02x color:%#02x}",
			wall.Ch, wall.Color, byte(EditorInvisibleChar), byte(m1613aWallColor))
	}

	// The rooms, stepped while the editor session is alive, see none of it.
	for i := 0; i < 4; i++ {
		server.RoomManager.Step(map[PlayerID]PlayerInput{})
	}
	m1613aRequireGameRoom(t, server, dweller, visitor, "with an editor session open on the same world")
}

// m1613aRequireGameRoom asserts the two live rooms are drawn the GAME way: the
// cellar is dark everywhere its player cannot see, and the hall's invisible wall
// is a blank.
func m1613aRequireGameRoom(t *testing.T, server *WebSocketServer, dweller, visitor PlayerID, when string) {
	t.Helper()

	// A snapshot serves the room's screen buffer, so the probes have to be
	// REDRAWN to be a live reading of the element table rather than a recording
	// of the entry paint. Repainting a square is ordinary room business: any
	// tile change, any creature stepping past, any newcomer's arrival does it.
	for _, boardID := range []int16{m1613aDarkBoard, m1613aLitBoard} {
		room, ok := server.RoomManager.Room(boardID)
		if !ok {
			t.Fatalf("%s: board %d has no live room", when, boardID)
		}
		room.Engine.BoardDrawTile(m1613aInvisibleX, m1613aInvisibleY)
		room.Engine.BoardDrawTile(m1613aSolidX, m1613aSolidY)
	}

	cellar, ok := server.RoomManager.Snapshot(dweller)
	if !ok {
		t.Fatalf("%s: the dark room did not snapshot", when)
	}
	for _, probe := range []struct {
		x, y int16
		what string
	}{
		{m1613aSolidX, m1613aSolidY, "a normal wall"},
		{m1613aInvisibleX, m1613aInvisibleY, "the cellar's invisible wall"},
	} {
		cell, ok := screenCell(cellar.Screen, probe.x-1, probe.y-1)
		if !ok {
			t.Fatalf("%s: the cellar snapshot has no cell at (%d,%d)", when, probe.x, probe.y)
		}
		// TileToColorAndChar's darkness branch: 0xB0 on 0x07, whatever the tile.
		if cell.Ch != '\xb0' || cell.Color != 0x07 {
			t.Errorf("%s: %s drew {ch:%#02x color:%#02x}, want darkness {ch:0xb0 color:0x07} — "+
				"the editor's ForceDarknessOff must not reach a live room",
				when, probe.what, cell.Ch, cell.Color)
		}
	}

	hall, ok := server.RoomManager.Snapshot(visitor)
	if !ok {
		t.Fatalf("%s: the lit room did not snapshot", when)
	}
	cell, ok := screenCell(hall.Screen, m1613aInvisibleX-1, m1613aInvisibleY-1)
	if !ok {
		t.Fatalf("%s: the hall snapshot has no cell where the invisible wall stands", when)
	}
	if cell.Ch != ' ' {
		t.Errorf("%s: the hall's invisible wall drew %#02x, want a blank — the editor's element table "+
			"must not reach a live room", when, cell.Ch)
	}
}

const (
	m1613aDarkBoard, m1613aLitBoard    = 1, 2
	m1613aInvisibleX, m1613aInvisibleY = 10, 6
	m1613aSolidX, m1613aSolidY         = 12, 6
	m1613aWallColor                    = 0x0E
)

// m1613aDarkRoomWorld is a three-board world: board 1 is a dark cellar, board 2
// a lit hall, and both hold an invisible wall and a normal one far enough from
// the player's spawn that no torch could light them. Board 0 is the title board
// WorldCreate leaves behind.
func m1613aDarkRoomWorld(t *testing.T) TWorld {
	t.Helper()
	e := NewEngine()
	e.Headless = true
	e.VideoInstall()
	e.WorldCreate()
	e.World.Info.Name = "DARK"

	for _, board := range []struct {
		id   int16
		name string
		dark bool
	}{
		{m1613aDarkBoard, "Cellar", true},
		{m1613aLitBoard, "Hall", false},
	} {
		// EditorAppendBoard's sequence (editor.go:21-28): close the open board,
		// grow the count, point CurrentBoard at the new slot, then create it.
		e.BoardClose()
		e.World.BoardCount = board.id
		e.World.Info.CurrentBoard = board.id
		e.World.BoardLen[board.id] = 0
		e.BoardCreate()
		e.Board.Name = board.name
		e.Board.Info.IsDark = board.dark
		e.Board.Tiles[m1613aInvisibleX][m1613aInvisibleY] = TTile{Element: E_INVISIBLE, Color: m1613aWallColor}
		e.Board.Tiles[m1613aSolidX][m1613aSolidY] = TTile{Element: E_SOLID, Color: m1613aWallColor}
		e.BoardClose()
	}
	return e.World
}

// m1613DrawnBoardCells counts the board-area cells a snapshot paints with
// something other than blank floor. Empty floor is (0x20, 0x0F) — the first
// branch of TileToColorAndChar — and a darkened cell is not.
func m1613DrawnBoardCells(screen []ScreenCell) int {
	drawn := 0
	for _, cell := range screen {
		if cell.X >= BOARD_WIDTH {
			continue
		}
		if cell.Ch == ' ' && cell.Color == 0x0F {
			continue
		}
		drawn++
	}
	return drawn
}

// m1613BoardCellDiffs reports the board-area cells on which two full-screen
// frames disagree, described for a failure message.
func m1613BoardCellDiffs(want, got []ScreenCell) []string {
	index := func(cells []ScreenCell) map[[2]int16]ScreenCell {
		m := make(map[[2]int16]ScreenCell, len(cells))
		for _, cell := range cells {
			if cell.X < BOARD_WIDTH {
				m[[2]int16{cell.X, cell.Y}] = cell
			}
		}
		return m
	}
	a, b := index(want), index(got)
	var diffs []string
	for y := int16(0); y < BOARD_HEIGHT; y++ {
		for x := int16(0); x < BOARD_WIDTH; x++ {
			key := [2]int16{x, y}
			if a[key] != b[key] {
				diffs = append(diffs, fmt.Sprintf("(%d,%d) %+v vs %+v", x, y, a[key], b[key]))
			}
		}
	}
	return diffs
}

// TestM1613aSwitchBoardsReachesTheTitleBoard is the inverted pin for M16.13a's
// second finding.
//
// Vanilla's 'B' is EditorSelectBoard("Switch boards", CurrentBoard, false)
// (editor.go:668-669): titleScreenIsNone is FALSE there, so board 0 is listed
// under its own name and can be selected like any other. The browser's list is
// built from EditorProperties.Boards, where board 0 used to be named "None"
// unconditionally, and main.ts openEditorBoardList then filtered it out — so an
// author who switched away from a world's first board could never switch back.
//
// The wire list now carries every board under its own name; "None" is applied by
// the two client pickers that are vanilla's titleScreenIsNone-TRUE call sites
// (a board's four edges, and a passage's target room).
func TestM1613aSwitchBoardsReachesTheTitleBoard(t *testing.T) {
	session := NewEditorSession("EDIT", m1613EditorWorld(t))
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(member)

	snapshot, err := session.Snapshot(member, 30, 12)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Properties.BoardName != "Edit Draft" {
		t.Fatalf("the session opened %q, want Edit Draft", snapshot.Properties.BoardName)
	}
	if len(snapshot.Properties.Boards) == 0 || snapshot.Properties.Boards[0].ID != 0 {
		t.Fatalf("board options do not start at board 0: %+v", snapshot.Properties.Boards)
	}
	if got := snapshot.Properties.Boards[0].Name; got != "Edit Draft" {
		t.Errorf("the board list names board 0 %q, want its own name \"Edit Draft\" — "+
			"EditorSelectBoard's switcher call passes titleScreenIsNone false (editor.go:668)", got)
	}

	// Away from the first board and back again, which is what the list has to be
	// able to express.
	away, err := session.SwitchBoard(member, 1)
	if err != nil {
		t.Fatal(err)
	}
	if away.Properties.BoardName != "Edit Annex" {
		t.Fatalf("SwitchBoard(1) landed on %q, want Edit Annex", away.Properties.BoardName)
	}
	// The open board is named from e.Board, not from the not-yet-rewritten
	// BoardData behind it, so board 1 keeps its name while it is the current one.
	if got := away.Properties.Boards[1].Name; got != "Edit Annex" {
		t.Errorf("the open board is listed as %q, want Edit Annex", got)
	}
	if got := away.Properties.Boards[0].Name; got != "Edit Draft" {
		t.Errorf("board 0 is listed as %q from the annex, want Edit Draft", got)
	}
	back, err := session.SwitchBoard(member, 0)
	if err != nil {
		t.Fatal(err)
	}
	if back.Properties.BoardName != "Edit Draft" {
		t.Fatalf("SwitchBoard(0) landed on %q, want Edit Draft", back.Properties.BoardName)
	}
}

// TestM1613aExitAndPassagePickersStillSayNone keeps the other half of the board
// list honest: the wire names board 0, and the client substitutes "None" exactly
// where vanilla passes titleScreenIsNone true — EditorGetBoardName at the board
// edges (editor.go:260) and at a passage's room (editor.go:377,393).
func TestM1613aExitAndPassagePickersStillSayNone(t *testing.T) {
	main := m1613ReadSource(t, filepath.Join("web", "src", "main.ts"))
	for _, want := range []string{
		// editorBoardName is the readout; both pickers build their entries with
		// titleScreenIsNone true.
		`function editorBoardName(id: number): string {
  if (id === 0) return "None";`,
		"function openEditorExitPicker(exit: number) {\n  const entries = editorBoardEntries(true);",
		"function openEditorStatBoardPicker(title: string) {\n  const entries = editorBoardEntries(true);",
		// ...and the switcher does not.
		"const entries = editorBoardEntries(false);",
	} {
		if !strings.Contains(main, want) {
			t.Errorf("web/src/main.ts no longer contains:\n%s\n"+
				"the title board must read \"None\" at vanilla's titleScreenIsNone-true call sites and "+
				"by its own name in \"Switch boards\"", want)
		}
	}
	if strings.Contains(main, ".filter((board) => board.id !== 0)") {
		t.Error("the board list filters board 0 out again; that is M16.13a's second finding")
	}
}

// TestM1613aLeavingTheEditorOffersToSave is the inverted pin for M16.13a's third
// finding.
//
// `leaveEditor` (web/src/main.ts) is a faithful transcription of
// EditorAskSaveChanged (editor.go:155-165): if the world was modified, offer
// "Save first?" on the way out. But nothing in the client ever set
// `editorModified` to true — the flag was declared false, reset to false when the
// editor opened and when a save succeeded, and never raised — so the branch was
// unreachable and an author who edited a world and pressed Q or Escape lost the
// work with no question asked.
//
// Vanilla raises its `wasModified` in EditorPrepareModifyTile (editor.go:169),
// on a board-info edit (242) and on a stat edit (401). The client raises it on
// the REPLY to each of those three classes rather than on the keystroke, so a
// refusal — a read-only member, an unheld lease, a placement the session
// declined — cannot dirty a world it never changed.
//
// This is a source-level pin because the behaviour IS a statement's presence.
// The browser half is in engine/web/test/editor_solo.test.mjs, which answers the
// prompt on the way out.
func TestM1613aLeavingTheEditorOffersToSave(t *testing.T) {
	main := m1613ReadSource(t, filepath.Join("web", "src", "main.ts"))
	if !strings.Contains(main, `openYesNo("Save first? "`) {
		t.Fatal("leaveEditor no longer has an EditorAskSaveChanged prompt at all; " +
			"if that was deliberate, rewrite this test")
	}
	// One raise site per vanilla raise site, each inside the handler for that
	// class of reply, and each gated so a refusal cannot reach it.
	for _, want := range []struct{ handler, raise string }{
		{"function applyEditorDiff(message: EditorDiffMessage) {", "if (message.cells.length > 0) editorModified = true;"},
		{"function applyEditorProperties(message: EditorPropertiesMessage) {", "editorModified = true;"},
		{"function applyEditorStatSettings(message: EditorStatSettingsMessage) {", "if (message.cells.length > 0) editorModified = true;"},
	} {
		body, ok := m1613FunctionBody(main, want.handler)
		if !ok {
			t.Errorf("web/src/main.ts no longer declares %q", want.handler)
			continue
		}
		if !strings.Contains(body, want.raise) {
			t.Errorf("%s does not raise editorModified (want %q): vanilla raises wasModified on this "+
				"class of change (editor.go:169/242/401)", want.handler, want.raise)
		}
	}
	// The flag must not be raised optimistically on the keystroke: the senders
	// fire before the session has accepted anything.
	for _, sender := range []string{
		"function sendEditorEdit(",
		"function sendEditorProperty(",
		"function sendEditorStat(",
		"function sendEditorProgramSave(",
	} {
		body, ok := m1613FunctionBody(main, sender)
		if !ok {
			t.Errorf("web/src/main.ts no longer declares %q", sender)
			continue
		}
		if strings.Contains(body, "editorModified = true") {
			t.Errorf("%s raises editorModified before the session has accepted the change; a refused "+
				"edit would then dirty a world it never changed", sender)
		}
	}
	// Still cleared where vanilla clears it: on entering the editor, and on a
	// successful save.
	if strings.Count(main, "editorModified = false") < 2 {
		t.Fatal("editorModified is no longer reset where this test expects; re-read startEditor " +
			"and applyEditorSaveResult")
	}
}

// m1613FunctionBody returns the source between a function's opening line and the
// next top-level "\n}" — enough to tell which handler a statement sits in.
func m1613FunctionBody(source, header string) (string, bool) {
	start := strings.Index(source, header)
	if start < 0 {
		return "", false
	}
	rest := source[start:]
	end := strings.Index(rest, "\n}")
	if end < 0 {
		return rest, true
	}
	return rest[:end], true
}

// ---------------------------------------------------------------------------
// An independent reader for the vanilla .ZZT format
// ---------------------------------------------------------------------------
//
// Written from the published file format (reference/fileformat.html) and the
// Pascal records it documents, deliberately NOT from this fork's worldReadFrom:
// a file that only round-trips through the reader that wrote it is not portable,
// and reusing worldReadFrom here could not tell those two things apart.

type m1613VanillaStat struct {
	X, Y                 int
	StepX, StepY         int
	Cycle                int
	P1, P2, P3           int
	Follower, Leader     int
	UnderID, UnderColor  int
	CurrentInstruction   int
	Length               int
	Program              string
}

type m1613VanillaBoard struct {
	Name              string
	Tiles             [][2]int // 1500 entries of {element, colour}, left to right, top to bottom
	MaxShots          int
	IsDark            bool
	Exits             [4]int
	ReenterWhenZapped bool
	Message           string
	TimeLimit         int
	Stats             []m1613VanillaStat
	// Record is the board's own bytes as they sit in the file, length prefix
	// included — which is exactly the .BRD format (EDITOR.PAS:556).
	Record []byte
}

type m1613VanillaWorld struct {
	BoardCount   int
	Name         string
	CurrentBoard int
	Ammo         int
	Gems         int
	Health       int
	Torches      int
	Score        int
	Locked       bool
	Boards       []m1613VanillaBoard
}

type m1613Cursor struct {
	data []byte
	at   int
}

func (c *m1613Cursor) u8() int {
	v := int(c.data[c.at])
	c.at++
	return v
}

func (c *m1613Cursor) i16() int {
	v := int(c.data[c.at]) | int(c.data[c.at+1])<<8
	c.at += 2
	if v >= 0x8000 {
		v -= 0x10000
	}
	return v
}

func (c *m1613Cursor) skip(n int) { c.at += n }

// pstr reads a length-prefixed, fixed-width Pascal string: one length byte
// followed by `width` bytes of storage.
func (c *m1613Cursor) pstr(width int) string {
	n := c.u8()
	if n > width {
		n = width
	}
	s := string(c.data[c.at : c.at+n])
	c.at += width
	return s
}

func m1613ReadVanillaWorld(data []byte) (*m1613VanillaWorld, error) {
	if len(data) < 512 {
		return nil, fmt.Errorf("world is %d bytes, shorter than the 512-byte header", len(data))
	}
	c := &m1613Cursor{data: data}
	if kind := c.i16(); kind != -1 {
		return nil, fmt.Errorf("WorldType is %d, want -1 (a ZZT world)", kind)
	}
	world := &m1613VanillaWorld{}
	world.BoardCount = c.i16()
	world.Ammo = c.i16()
	world.Gems = c.i16()
	c.skip(7) // keys
	world.Health = c.i16()
	world.CurrentBoard = c.i16()
	world.Torches = c.i16()
	c.skip(2) // torch cycles
	c.skip(2) // energy cycles
	c.skip(2) // unused
	world.Score = c.i16()
	world.Name = c.pstr(20)
	for i := 0; i < 10; i++ {
		c.pstr(20) // flags
	}
	c.skip(2) // time passed
	c.skip(2) // time passed ticks
	world.Locked = c.u8() != 0

	if world.BoardCount < 0 || world.BoardCount > MAX_BOARD {
		return nil, fmt.Errorf("NumBoards is %d, outside 0..%d", world.BoardCount, MAX_BOARD)
	}

	at := 512
	for i := 0; i <= world.BoardCount; i++ {
		if at+2 > len(data) {
			return nil, fmt.Errorf("board %d: the file ends before its length prefix", i)
		}
		size := int(data[at]) | int(data[at+1])<<8
		if size < 0 || at+2+size > len(data) {
			return nil, fmt.Errorf("board %d: BoardSize %d runs past the end of the file", i, size)
		}
		board, err := m1613ReadVanillaBoard(data[at+2 : at+2+size])
		if err != nil {
			return nil, fmt.Errorf("board %d: %w", i, err)
		}
		board.Record = data[at : at+2+size]
		world.Boards = append(world.Boards, *board)
		at += 2 + size
	}
	if at != len(data) {
		return nil, fmt.Errorf("%d trailing bytes after the last board", len(data)-at)
	}
	return world, nil
}

func m1613ReadVanillaBoard(data []byte) (parsed *m1613VanillaBoard, err error) {
	// The explicit bounds checks below cover the variable-length parts; this
	// catches a truncation inside a fixed-width field (a board shorter than its
	// own name) and reports it as a parse failure rather than a panic in a test.
	defer func() {
		if r := recover(); r != nil {
			parsed, err = nil, fmt.Errorf("the board record ends mid-field: %v", r)
		}
	}()
	board := &m1613VanillaBoard{}
	c := &m1613Cursor{data: data}
	board.Name = c.pstr(50)

	// RLE: {count, element, colour} triples until 1500 tiles are filled. A count
	// of zero means 256 (the format's documented decoder quirk).
	for len(board.Tiles) < BOARD_WIDTH*BOARD_HEIGHT {
		if c.at+3 > len(data) {
			return nil, fmt.Errorf("the tile stream ends after %d of %d tiles", len(board.Tiles), BOARD_WIDTH*BOARD_HEIGHT)
		}
		count := c.u8()
		if count == 0 {
			count = 256
		}
		element := c.u8()
		colour := c.u8()
		for i := 0; i < count; i++ {
			board.Tiles = append(board.Tiles, [2]int{element, colour})
		}
	}
	if len(board.Tiles) != BOARD_WIDTH*BOARD_HEIGHT {
		return nil, fmt.Errorf("the tile stream decodes to %d tiles, want %d", len(board.Tiles), BOARD_WIDTH*BOARD_HEIGHT)
	}

	board.MaxShots = c.u8()
	board.IsDark = c.u8() != 0
	for i := 0; i < 4; i++ {
		board.Exits[i] = c.u8()
	}
	board.ReenterWhenZapped = c.u8() != 0
	board.Message = c.pstr(58)
	c.skip(2) // player enter x,y
	board.TimeLimit = c.i16()
	c.skip(16) // unused
	statCount := c.i16()
	if statCount < 0 || statCount > MAX_STAT {
		return nil, fmt.Errorf("StatElementCount is %d, outside 0..%d", statCount, MAX_STAT)
	}
	for i := 0; i <= statCount; i++ {
		if c.at+33 > len(data) {
			return nil, fmt.Errorf("stat %d: the record runs past the end of the board", i)
		}
		stat := m1613VanillaStat{}
		stat.X = c.u8()
		stat.Y = c.u8()
		stat.StepX = c.i16()
		stat.StepY = c.i16()
		stat.Cycle = c.i16()
		stat.P1 = c.u8()
		stat.P2 = c.u8()
		stat.P3 = c.u8()
		stat.Follower = c.i16()
		stat.Leader = c.i16()
		stat.UnderID = c.u8()
		stat.UnderColor = c.u8()
		c.skip(4) // Pointer, which ZZT writes as zero
		stat.CurrentInstruction = c.i16()
		stat.Length = c.i16()
		c.skip(8) // two unused pointers
		if stat.Length > 0 {
			if c.at+stat.Length > len(data) {
				return nil, fmt.Errorf("stat %d: a %d-byte program runs past the end of the board", i, stat.Length)
			}
			stat.Program = string(data[c.at : c.at+stat.Length])
			c.skip(stat.Length)
		}
		board.Stats = append(board.Stats, stat)
	}
	if c.at != len(data) {
		return nil, fmt.Errorf("%d trailing bytes after the last stat", len(data)-c.at)
	}
	return board, nil
}

// ---------------------------------------------------------------------------
// The session authority, in the same shape
// ---------------------------------------------------------------------------

// m1613SessionView reads the editor session's LIVE state — not a file — into
// the same shape the independent reader produces, so the two can be compared
// field by field. It works on a clone of the session world, so walking every
// board does not disturb the board the session has open.
func m1613SessionView(t *testing.T, s *EditorSession) *m1613VanillaWorld {
	t.Helper()
	s.mu.Lock()
	live := s.engine
	live.BoardClose()
	clone := cloneWorld(live.World)
	live.BoardOpen(live.World.Info.CurrentBoard)
	s.mu.Unlock()

	e := NewEngine()
	e.Headless = true
	e.MultiRoom = true
	e.SetInputSource(&ScriptedInput{})
	e.World = clone

	view := &m1613VanillaWorld{
		BoardCount:   int(clone.BoardCount),
		Name:         clone.Info.Name,
		CurrentBoard: int(clone.Info.CurrentBoard),
		Ammo:         int(clone.Info.Ammo),
		Gems:         int(clone.Info.Gems),
		Health:       int(clone.Info.Health),
		Torches:      int(clone.Info.Torches),
		Score:        int(clone.Info.Score),
		Locked:       clone.Info.IsSave,
	}
	for boardID := int16(0); boardID <= clone.BoardCount; boardID++ {
		e.BoardOpen(boardID)
		board := m1613VanillaBoard{
			Name:              e.Board.Name,
			MaxShots:          int(e.Board.Info.MaxShots),
			IsDark:            e.Board.Info.IsDark,
			ReenterWhenZapped: e.Board.Info.ReenterWhenZapped,
			Message:           e.Board.Info.Message,
			TimeLimit:         int(e.Board.Info.TimeLimitSec),
		}
		for i := 0; i < 4; i++ {
			board.Exits[i] = int(e.Board.Info.NeighborBoards[i])
		}
		for y := int16(1); y <= BOARD_HEIGHT; y++ {
			for x := int16(1); x <= BOARD_WIDTH; x++ {
				tile := e.Board.Tiles[x][y]
				board.Tiles = append(board.Tiles, [2]int{int(tile.Element), int(tile.Color)})
			}
		}
		for i := int16(0); i <= e.Board.StatCount; i++ {
			stat := e.Board.Stats[i]
			entry := m1613VanillaStat{
				X: int(stat.X), Y: int(stat.Y),
				StepX: int(stat.StepX), StepY: int(stat.StepY),
				Cycle: int(stat.Cycle),
				P1:    int(stat.P1), P2: int(stat.P2), P3: int(stat.P3),
				Follower: int(stat.Follower), Leader: int(stat.Leader),
				UnderID:  int(stat.Under.Element), UnderColor: int(stat.Under.Color),
				CurrentInstruction: int(stat.DataPos),
				Length:             int(stat.DataLen),
			}
			if stat.DataLen > 0 {
				entry.Program = stat.Data[:stat.DataLen]
			}
			board.Stats = append(board.Stats, entry)
		}
		view.Boards = append(view.Boards, board)
	}
	e.BoardClose()
	return view
}

// m1613CompareWorlds reports every semantic difference between a world parsed
// out of a downloaded file and the session's live state.
func m1613CompareWorlds(file, session *m1613VanillaWorld) []string {
	var out []string
	note := func(format string, args ...interface{}) {
		out = append(out, fmt.Sprintf(format, args...))
	}
	if file.BoardCount != session.BoardCount {
		note("BoardCount: file %d, session %d", file.BoardCount, session.BoardCount)
		return out
	}
	if file.Name != session.Name {
		note("world name: file %q, session %q", file.Name, session.Name)
	}
	if file.CurrentBoard != session.CurrentBoard {
		note("CurrentBoard: file %d, session %d", file.CurrentBoard, session.CurrentBoard)
	}
	if file.Locked != session.Locked {
		note("Locked: file %v, session %v — a world the editor wrote must not read as a saved game", file.Locked, session.Locked)
	}
	if file.Health != session.Health {
		note("player health: file %d, session %d", file.Health, session.Health)
	}
	for i := range file.Boards {
		f, s := file.Boards[i], session.Boards[i]
		if f.Name != s.Name {
			note("board %d name: file %q, session %q", i, f.Name, s.Name)
		}
		if f.MaxShots != s.MaxShots {
			note("board %d MaxShots: file %d, session %d", i, f.MaxShots, s.MaxShots)
		}
		if f.IsDark != s.IsDark {
			note("board %d IsDark: file %v, session %v", i, f.IsDark, s.IsDark)
		}
		if f.Exits != s.Exits {
			note("board %d exits: file %v, session %v", i, f.Exits, s.Exits)
		}
		if f.ReenterWhenZapped != s.ReenterWhenZapped {
			note("board %d ReenterWhenZapped: file %v, session %v", i, f.ReenterWhenZapped, s.ReenterWhenZapped)
		}
		if f.TimeLimit != s.TimeLimit {
			note("board %d TimeLimit: file %d, session %d", i, f.TimeLimit, s.TimeLimit)
		}
		if len(f.Tiles) != len(s.Tiles) {
			note("board %d tile count: file %d, session %d", i, len(f.Tiles), len(s.Tiles))
		} else {
			differing := 0
			for j := range f.Tiles {
				if f.Tiles[j] != s.Tiles[j] {
					if differing < 5 {
						note("board %d tile %d (x=%d,y=%d): file element %d colour 0x%02x, session element %d colour 0x%02x",
							i, j, j%BOARD_WIDTH+1, j/BOARD_WIDTH+1,
							f.Tiles[j][0], f.Tiles[j][1], s.Tiles[j][0], s.Tiles[j][1])
					}
					differing++
				}
			}
			if differing > 5 {
				note("board %d: and %d more differing tiles", i, differing-5)
			}
		}
		if len(f.Stats) != len(s.Stats) {
			note("board %d stat count: file %d, session %d", i, len(f.Stats), len(s.Stats))
			continue
		}
		for j := range f.Stats {
			if f.Stats[j] != s.Stats[j] {
				note("board %d stat %d: file %+v, session %+v", i, j, f.Stats[j], s.Stats[j])
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// The browser run
// ---------------------------------------------------------------------------

type m1613Report struct {
	Exercised []string          `json:"exercised"`
	Files     map[string]string `json:"files"`
	Notes     []string          `json:"notes"`
	// ExportedBoard is the board id the .BRD was exported from.
	ExportedBoard int `json:"exportedBoard"`
}

func m1613OutDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("web", "test-results", "m16-13"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestM1613BrowserEditorAndPortableOutput is the sweep. One browser session
// drives the whole editor vocabulary, authors a world from nothing, downloads it
// as .ZZT and one board as .BRD, uploads the .ZZT back through the validation
// gate, test-plays it, and returns to the editor; this test then checks the
// downloaded bytes against the session authority through an independent reader,
// checks the .BRD really is the board's own record, and checks that test play
// left the editing world byte for byte where it was.
func TestM1613BrowserEditorAndPortableOutput(t *testing.T) {
	h := m1613NewHarness(t)
	outDir := m1613OutDir(t)
	out := h.runBrowserScript("editor_solo.test.mjs", "M1613_OUT="+outDir)
	t.Logf("browser editor vocabulary:\n%s", out)

	reportData, err := os.ReadFile(filepath.Join(outDir, "report.json"))
	if err != nil {
		t.Fatalf("the browser script wrote no run report: %v", err)
	}
	var report m1613Report
	if err := json.Unmarshal(reportData, &report); err != nil {
		t.Fatalf("parse the run report: %v", err)
	}

	t.Run("every manifest row was exercised", func(t *testing.T) {
		exercised := map[string]bool{}
		for _, id := range report.Exercised {
			exercised[id] = true
		}
		known := map[string]bool{}
		for _, cmd := range m1613Commands {
			known[cmd.ID] = true
		}
		var uncovered []string
		for _, cmd := range m1613Commands {
			if cmd.Evidence == "browser" && !exercised[cmd.ID] {
				uncovered = append(uncovered, cmd.ID)
			}
		}
		sort.Strings(uncovered)
		if len(uncovered) > 0 {
			t.Errorf("the browser run did not exercise %d manifest row(s):\n  %s",
				len(uncovered), strings.Join(uncovered, "\n  "))
		}
		var unknown []string
		for _, id := range report.Exercised {
			if !known[id] {
				unknown = append(unknown, id)
			}
		}
		sort.Strings(unknown)
		if len(unknown) > 0 {
			t.Errorf("the browser run recorded ids with no manifest row: %s", strings.Join(unknown, ", "))
		}
	})

	download := m1613ReadArtifact(t, outDir, report.Files["world"])
	authority := m1613ReadArtifact(t, outDir, report.Files["authority"])

	t.Run("the download is the session's own bytes", func(t *testing.T) {
		if !bytes.Equal(download, authority) {
			t.Fatalf("the browser downloaded %d bytes; the editor session serialized %d, and they differ",
				len(download), len(authority))
		}
	})

	t.Run("the .ZZT parses as vanilla and matches the session", func(t *testing.T) {
		file, err := m1613ReadVanillaWorld(download)
		if err != nil {
			t.Fatalf("the downloaded world does not parse as a vanilla .ZZT: %v", err)
		}
		session := h.editorSession()
		if session == nil {
			t.Fatal("the editor session is gone")
		}
		if diffs := m1613CompareWorlds(file, m1613SessionView(t, session)); len(diffs) > 0 {
			t.Errorf("the downloaded file and the editor session disagree:\n  %s", strings.Join(diffs, "\n  "))
		}
	})

	t.Run("the .BRD is the board's own vanilla record", func(t *testing.T) {
		brd := m1613ReadArtifact(t, outDir, report.Files["board"])
		file, err := m1613ReadVanillaWorld(download)
		if err != nil {
			t.Fatalf("the downloaded world does not parse: %v", err)
		}
		if report.ExportedBoard < 0 || report.ExportedBoard >= len(file.Boards) {
			t.Fatalf("the run exported board %d, which the world does not have", report.ExportedBoard)
		}
		want := file.Boards[report.ExportedBoard].Record
		if !bytes.Equal(brd, want) {
			t.Errorf("the exported .BRD is %d bytes; board %d's record inside the .ZZT is %d, and they differ — "+
				"a .BRD is that record verbatim (EDITOR.PAS:556)", len(brd), report.ExportedBoard, len(want))
		}
		if _, err := m1613ReadVanillaBoard(brd[2:]); err != nil {
			t.Errorf("the exported .BRD does not parse as a vanilla board: %v", err)
		}
	})

	t.Run("test play left the editing world byte-identical", func(t *testing.T) {
		before := m1613ReadArtifact(t, outDir, report.Files["beforeTestPlay"])
		after := m1613ReadArtifact(t, outDir, report.Files["afterTestPlay"])
		if len(before) == 0 {
			t.Fatal("the run recorded no pre-test-play snapshot")
		}
		if !bytes.Equal(before, after) {
			t.Errorf("the editing world changed across test play: %d bytes before, %d after",
				len(before), len(after))
		}
	})
}

func m1613ReadArtifact(t *testing.T, dir, name string) []byte {
	t.Helper()
	if name == "" {
		t.Fatalf("the run report names no artifact for this check")
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read the run artifact %s: %v", name, err)
	}
	return data
}
