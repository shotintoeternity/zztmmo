package zztgo

// Parity manifest validator and scaffold (task M16.0).
//
// The manifest at fixtures/parity/manifest.json is the machine-readable half of
// the M16 feature-parity contract (PARITY.md). This file is its validator: it
// derives the authoritative inventory of five *mechanical* surfaces from the
// code at test time — checked tasks, element procs, ZZT-OOP words, protocol
// message/event types, and HTTP routes — and proves the manifest carries
// exactly one row for each, plus internal-consistency rules for the curated
// dimensions. A newly added element/command/route/task therefore reddens the
// build until a manifest row is added; it cannot be silently unlisted.
//
// TestParityManifest             — the gate (runs under `go test ./...`).
// TestParityManifestScaffold     — regenerates the manifest from code; run with
//                                  PARITY_SCAFFOLD=1 to (re)write it, merging in
//                                  any status/test/fixture advancements already
//                                  recorded by later M16 tasks.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Schema
// ---------------------------------------------------------------------------

type parityRow struct {
	ID           string `json:"id"`
	Dimension    string `json:"dimension"`
	Subject      string `json:"subject"`
	Contract     string `json:"contract"`
	Authority    string `json:"authority"`
	Parity       string `json:"parity"`
	Deviation    string `json:"deviation,omitempty"`
	Test         string `json:"test,omitempty"`
	Fixture      string `json:"fixture,omitempty"`
	Status       string `json:"status"`
	AssignedTask string `json:"assignedTask,omitempty"`
	Notes        string `json:"notes,omitempty"`
}

type parityDeviation struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Contract  string `json:"contract"`
	Authority string `json:"authority"`
}

type parityManifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	Rows          []parityRow       `json:"rows"`
	Deviations    []parityDeviation `json:"deviations"`
}

const parityManifestPath = "../fixtures/parity/manifest.json"

var (
	validContracts  = map[string]bool{"V": true, "P": true, "E": true, "out-of-scope": true}
	validParity     = map[string]bool{"exact": true, "deviation": true}
	validStatus     = map[string]bool{"pass": true, "unverified": true, "deviation": true, "gap": true, "out-of-scope": true}
	mechanicalDims  = map[string]bool{"task": true, "element": true, "oop": true, "protocol": true, "route": true}
	validDimensions = map[string]bool{
		"task": true, "element": true, "oop": true, "protocol": true, "route": true,
		"oop-structural": true, "input": true, "browser-mode": true, "service": true,
	}
)

// validAssignedTasks are the later-M16 tasks a row may be assigned to (M16.1
// onward, including the audit sub-task M16.16a and the M16.0 touch-controls gap
// task M16.18a). M16.0 itself is not a valid target — a row cannot be verified
// by the task that only defines the contract.
func validAssignedTask(id string) bool {
	if id == "M16.16a" || id == "M16.18a" {
		return true
	}
	m := regexp.MustCompile(`^M16\.(\d+)$`).FindStringSubmatch(id)
	if m == nil {
		return false
	}
	n := 0
	for _, c := range m[1] {
		n = n*10 + int(c-'0')
	}
	return n >= 1 && n <= 20
}

// ---------------------------------------------------------------------------
// The gate
// ---------------------------------------------------------------------------

func TestParityManifest(t *testing.T) {
	manifest := loadParityManifest(t)
	expected := buildParityRows(t)

	byID := map[string]parityRow{}
	dimByID := map[string]string{}
	for _, r := range manifest.Rows {
		if _, dup := byID[r.ID]; dup {
			t.Errorf("duplicate manifest row id %q", r.ID)
		}
		byID[r.ID] = r
		dimByID[r.ID] = r.Dimension
	}

	// Completeness: every derived/curated inventory item has a row.
	expectedIDs := map[string]bool{}
	for _, e := range expected {
		expectedIDs[e.ID] = true
		if _, ok := byID[e.ID]; !ok {
			t.Errorf("inventory item %q (%s: %s) has no manifest row — regenerate with PARITY_SCAFFOLD=1", e.ID, e.Dimension, e.Subject)
		}
	}

	// No orphan/stale rows in a mechanical dimension: every such row must map to
	// a real derived item. (Curated dimensions may be extended by later tasks.)
	for _, r := range manifest.Rows {
		if mechanicalDims[r.Dimension] && !expectedIDs[r.ID] {
			t.Errorf("stale manifest row %q in mechanical dimension %q maps to no code surface", r.ID, r.Dimension)
		}
	}

	// Deviation catalog lookup.
	devByID := map[string]bool{}
	for _, d := range manifest.Deviations {
		if devByID[d.ID] {
			t.Errorf("duplicate deviation catalog id %q", d.ID)
		}
		devByID[d.ID] = true
	}

	goTests := existingGoTestNames(t)
	repoRoot := ".."

	// Per-row consistency.
	for _, r := range manifest.Rows {
		where := "row " + r.ID
		if !validDimensions[r.Dimension] {
			t.Errorf("%s: invalid dimension %q", where, r.Dimension)
		}
		if !validContracts[r.Contract] {
			t.Errorf("%s: invalid contract %q", where, r.Contract)
		}
		if !validParity[r.Parity] {
			t.Errorf("%s: invalid parity %q", where, r.Parity)
		}
		if !validStatus[r.Status] {
			t.Errorf("%s: invalid status %q", where, r.Status)
		}
		if r.Subject == "" {
			t.Errorf("%s: empty subject", where)
		}
		if r.Authority == "" {
			t.Errorf("%s: empty authority", where)
		}

		// parity=deviation must name a catalogued deviation, and vice versa.
		if r.Parity == "deviation" {
			if r.Deviation == "" {
				t.Errorf("%s: parity=deviation but no deviation id", where)
			} else if !devByID[r.Deviation] {
				t.Errorf("%s: references unknown deviation %q", where, r.Deviation)
			}
		} else if r.Deviation != "" {
			t.Errorf("%s: parity=%q but deviation id %q set", where, r.Parity, r.Deviation)
		}

		// Status rules.
		switch r.Status {
		case "unverified", "deviation", "gap":
			if !validAssignedTask(r.AssignedTask) {
				t.Errorf("%s: status=%q requires a later-M16 assignedTask, got %q", where, r.Status, r.AssignedTask)
			}
		case "pass":
			if r.Test == "" {
				t.Errorf("%s: status=pass requires a test name", where)
			}
		case "out-of-scope":
			if r.Contract != "out-of-scope" {
				t.Errorf("%s: status=out-of-scope requires contract=out-of-scope", where)
			}
			if r.Notes == "" {
				t.Errorf("%s: status=out-of-scope requires a justification note", where)
			}
		}

		// No stale test/fixture references. A Go test name (Test*) must exist; a
		// fixture path must point at a real file. TS/browser test descriptors
		// (anything else) are only required to be non-empty here.
		for _, tn := range splitList(r.Test) {
			if strings.HasPrefix(tn, "Test") && !strings.Contains(tn, " ") {
				if !goTests[tn] {
					t.Errorf("%s: references non-existent Go test %q (stale)", where, tn)
				}
			}
		}
		if r.Fixture != "" {
			if _, err := os.Stat(filepath.Join(repoRoot, r.Fixture)); err != nil {
				t.Errorf("%s: fixture %q does not exist (stale)", where, r.Fixture)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Scaffold / regeneration
// ---------------------------------------------------------------------------

func TestParityManifestScaffold(t *testing.T) {
	if os.Getenv("PARITY_SCAFFOLD") == "" {
		t.Skip("set PARITY_SCAFFOLD=1 to (re)generate fixtures/parity/manifest.json")
	}

	rows := buildParityRows(t)

	// Merge in any advancements (status/test/fixture/notes/contract) already
	// recorded on disk, so regeneration never discards a landed sweep's edits.
	if prev, err := os.ReadFile(parityManifestPath); err == nil {
		var old parityManifest
		if err := json.Unmarshal(prev, &old); err == nil {
			oldByID := map[string]parityRow{}
			for _, r := range old.Rows {
				oldByID[r.ID] = r
			}
			for i, r := range rows {
				if o, ok := oldByID[r.ID]; ok {
					if o.Status != "unverified" {
						rows[i].Status = o.Status
					}
					if o.Test != "" {
						rows[i].Test = o.Test
					}
					if o.Fixture != "" {
						rows[i].Fixture = o.Fixture
					}
					if o.Notes != "" && rows[i].Notes == "" {
						rows[i].Notes = o.Notes
					}
				}
			}
		}
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Dimension != rows[j].Dimension {
			return dimOrder(rows[i].Dimension) < dimOrder(rows[j].Dimension)
		}
		return rows[i].ID < rows[j].ID
	})

	manifest := parityManifest{SchemaVersion: 1, Rows: rows, Deviations: seededDeviations()}
	out, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	out = append(out, '\n')
	if err := os.MkdirAll(filepath.Dir(parityManifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(parityManifestPath, out, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s: %d rows, %d deviations", parityManifestPath, len(rows), len(manifest.Deviations))
}

func dimOrder(dim string) int {
	order := []string{"task", "element", "oop", "oop-structural", "input", "protocol", "route", "browser-mode", "service"}
	for i, d := range order {
		if d == dim {
			return i
		}
	}
	return len(order)
}

// ---------------------------------------------------------------------------
// Inventory derivation
// ---------------------------------------------------------------------------

// buildParityRows returns the complete expected row set: five mechanically
// derived dimensions plus the curated dimensions.
func buildParityRows(t *testing.T) []parityRow {
	t.Helper()
	var rows []parityRow
	rows = append(rows, deriveTaskRows(t)...)
	rows = append(rows, deriveElementRows(t)...)
	rows = append(rows, deriveOopRows(t)...)
	rows = append(rows, deriveProtocolRows(t)...)
	rows = append(rows, deriveRouteRows(t)...)
	rows = append(rows, curatedOopStructuralRows()...)
	rows = append(rows, curatedInputRows()...)
	rows = append(rows, curatedBrowserModeRows()...)
	rows = append(rows, curatedServiceRows()...)
	return rows
}

// --- tasks: checked [x] boxes in TASKS.md (M0–M15 + M17) ---

func deriveTaskRows(t *testing.T) []parityRow {
	t.Helper()
	data := mustRead(t, "../TASKS.md")
	re := regexp.MustCompile(`(?m)^- \[x\] \*\*(M(\d+)\.[0-9a-z]+)`)
	var rows []parityRow
	seen := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(data, -1) {
		id := m[1]
		milestone := m[2]
		// Inventory scope: M0–M15 landed work plus the M17 live fixes. M16 is
		// the certification milestone itself (unchecked) and never a task row.
		if milestone == "16" {
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		rows = append(rows, parityRow{
			ID:           "task." + id,
			Dimension:    "task",
			Subject:      "Completed task " + id + " — its DoD still holds",
			Contract:     taskContract(milestone),
			Authority:    "TASKS.md " + id,
			Parity:       "exact",
			Status:       "unverified",
			AssignedTask: "M16.20",
			Notes:        "task-claim row; behavioral parity carried by the element/oop/protocol/input/service rows this task touched; reconciled at M16.20",
		})
	}
	if len(rows) == 0 {
		t.Fatal("no checked tasks parsed from TASKS.md")
	}
	return rows
}

func taskContract(milestone string) string {
	switch milestone {
	case "0":
		return "V"
	case "4", "7", "17":
		return "P"
	default:
		return "E"
	}
}

// --- elements: ElementDefs procs that differ from the defaults (reflection) ---

func deriveElementRows(t *testing.T) []parityRow {
	t.Helper()
	InitElementDefs()

	defTick := reflect.ValueOf((*Engine).ElementDefaultTick).Pointer()
	defDraw := reflect.ValueOf((*Engine).ElementDefaultDraw).Pointer()
	defTouch := reflect.ValueOf((*Engine).ElementDefaultTouch).Pointer()

	// The authoritative element set is the E_ constants, not a "has a custom
	// proc" heuristic: text tiles (E_TEXT_*, drawn by the special case in
	// TileToColorAndChar rather than a DrawProc) and blink rays (registered but
	// unnamed) are real V surfaces that carry no proc, and would otherwise be
	// missed. Reflection only annotates which procs are custom.
	var rows []parityRow
	for _, i := range elementIndices() {
		def := ElementDefs[i]
		procs := []string{}
		if reflect.ValueOf(def.DrawProc).Pointer() != defDraw {
			procs = append(procs, "draw")
		}
		if reflect.ValueOf(def.TickProc).Pointer() != defTick {
			procs = append(procs, "tick")
		}
		if reflect.ValueOf(def.TouchProc).Pointer() != defTouch {
			procs = append(procs, "touch")
		}
		procDesc := "default draw/tick/touch"
		if len(procs) > 0 {
			procDesc = "custom " + strings.Join(procs, "+")
		}
		row := parityRow{
			ID:           "elem." + constSlug(i),
			Dimension:    "element",
			Subject:      elementConst(i) + " (" + defOrUnnamed(def.Name) + "): " + procDesc,
			Contract:     "V",
			Authority:    "reference/reconstruction-of-zzt/SRC ELEMENTS.PAS; ANALYSIS.md",
			Parity:       "exact",
			Status:       "unverified",
			AssignedTask: elementSweep(i),
		}
		if i >= E_TEXT_MIN {
			row.Notes = "text tiles are drawn by the special case in game.go TileToColorAndChar, not a DrawProc"
		}
		// The player carries the multiplayer respawn/collision divergences.
		if i == E_PLAYER {
			row.Parity = "deviation"
			row.Deviation = "mp-respawn"
			row.Notes = "also subject to collision-pushout and friendly-fire-policy; single-player V behavior is exact"
		}
		rows = append(rows, row)
	}
	return rows
}

// elementIndices returns every defined E_ element index (the elementConst keys),
// sorted. Index 46 (reserved black-text slot) has no constant and is omitted.
func elementIndices() []int {
	var idx []int
	for i := 0; i <= MAX_ELEMENT; i++ {
		if elementConst(i) != "E_#"+itoa(i) {
			idx = append(idx, i)
		}
	}
	return idx
}

// constSlug turns an E_ constant into a stable, readable id fragment:
// E_CONVEYOR_CW → "conveyor-cw", E_TEXT_BLUE → "text-blue".
func constSlug(i int) string {
	s := strings.TrimPrefix(elementConst(i), "E_")
	s = strings.ToLower(s)
	return strings.ReplaceAll(s, "_", "-")
}

func elementSweep(i int) string {
	switch {
	case i == E_SCROLL || i == E_OBJECT:
		return "M16.6"
	case i == E_DUPLICATOR || i == E_BOMB || i == E_CONVEYOR_CW || i == E_CONVEYOR_CCW ||
		i == E_BOULDER || i == E_SLIDER_NS || i == E_SLIDER_EW || i == E_BLINK_WALL ||
		i == E_TRANSPORTER || i == E_BLINK_RAY_EW || i == E_BLINK_RAY_NS ||
		i == E_SPINNING_GUN || i == E_PUSHER || i == E_PASSAGE:
		return "M16.4"
	case i == E_STAR || i == E_BULLET || i == E_BEAR || i == E_RUFFIAN || i == E_SLIME ||
		i == E_SHARK || i == E_LION || i == E_TIGER || i == E_CENTIPEDE_HEAD || i == E_CENTIPEDE_SEGMENT:
		return "M16.5"
	default:
		return "M16.3"
	}
}

// --- ZZT-OOP words scanned from oop.go, cross-checked against curated sets ---

func deriveOopRows(t *testing.T) []parityRow {
	t.Helper()
	src := mustRead(t, "oop.go")
	re := regexp.MustCompile(`OopWord == "([A-Z?]+)"`)
	scanned := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		scanned[m[1]] = true
	}

	classes := oopWordClasses()
	classified := map[string]bool{}
	for _, words := range classes {
		for _, w := range words {
			classified[w] = true
		}
	}
	// Fail closed both ways: a new dispatch literal must be classified, and a
	// curated word must still exist in the source.
	for w := range scanned {
		if !classified[w] {
			t.Errorf("oop word %q is dispatched in oop.go but not classified in oopWordClasses() — add it", w)
		}
	}
	for w := range classified {
		if !scanned[w] {
			t.Errorf("oop word %q is classified but no longer dispatched in oop.go — remove it", w)
		}
	}

	authority := "reference/reconstruction-of-zzt/SRC OOP.PAS; oop.go"
	var rows []parityRow
	for _, sub := range []string{"command", "condition", "direction", "counter", "keyword"} {
		words := append([]string(nil), classes[sub]...)
		sort.Strings(words)
		for _, w := range words {
			rows = append(rows, parityRow{
				ID:           "oop." + sub + "." + strings.ToLower(w),
				Dimension:    "oop",
				Subject:      "ZZT-OOP " + sub + " " + w,
				Contract:     "V",
				Authority:    authority,
				Parity:       "exact",
				Status:       "unverified",
				AssignedTask: "M16.6",
			})
		}
	}
	return rows
}

// oopWordClasses is the curated classification of every OopWord literal in
// oop.go. The validator asserts this exactly covers the dispatched set.
func oopWordClasses() map[string][]string {
	return map[string][]string{
		"command": {
			"GO", "TRY", "WALK", "SET", "CLEAR", "IF", "SHOOT", "THROWSTAR",
			"GIVE", "TAKE", "END", "ENDGAME", "IDLE", "ZAP", "RESTORE", "LOCK",
			"UNLOCK", "SEND", "BECOME", "PUT", "CHANGE", "PLAY", "CHAR", "DIE",
			"BIND", "CYCLE", "RESTART",
		},
		"condition": {"NOT", "ALLIGNED", "CONTACT", "BLOCKED", "ENERGIZED", "ANY"},
		"direction": {"N", "NORTH", "S", "SOUTH", "E", "EAST", "W", "WEST", "I", "IDLE", "SEEK", "FLOW", "RND", "RNDNS", "RNDNE", "CW", "CCW", "RNDP", "OPP"},
		"counter":   {"HEALTH", "AMMO", "GEMS", "TORCHES", "SCORE", "TIME"},
		"keyword":   {"THEN"},
	}
}

// --- protocol message types + event types scanned from protocol.go ---

func deriveProtocolRows(t *testing.T) []parityRow {
	t.Helper()
	src := mustRead(t, "protocol.go")

	msgRe := regexp.MustCompile(`MessageType\w+\s*=\s*"([a-zA-Z]+)"`)
	evtRe := regexp.MustCompile(`ProtocolEvent\{Type:\s*"([a-zA-Z]+)"`)

	msgs := distinctMatches(msgRe, src)
	evts := distinctMatches(evtRe, src)

	authority := "protocol.go; tasks M3.2–M3.4"
	var rows []parityRow
	for _, m := range msgs {
		rows = append(rows, parityRow{
			ID:           "proto.msg." + m,
			Dimension:    "protocol",
			Subject:      "protocol message type \"" + m + "\"",
			Contract:     "P",
			Authority:    authority,
			Parity:       "exact",
			Status:       "unverified",
			AssignedTask: protocolSweep(m),
		})
	}
	for _, e := range evts {
		rows = append(rows, parityRow{
			ID:           "proto.event." + e,
			Dimension:    "protocol",
			Subject:      "protocol event type \"" + e + "\"",
			Contract:     "P",
			Authority:    authority,
			Parity:       "exact",
			Status:       "unverified",
			AssignedTask: "M16.8",
		})
	}
	if len(rows) == 0 {
		t.Fatal("no protocol types scanned from protocol.go")
	}
	return rows
}

func protocolSweep(msg string) string {
	if strings.HasPrefix(msg, "editor") {
		return "M16.14"
	}
	return "M16.8"
}

// --- HTTP routes scanned from web_api.go, plus the /ws upgrade ---

func deriveRouteRows(t *testing.T) []parityRow {
	t.Helper()
	src := mustRead(t, "web_api.go")
	re := regexp.MustCompile(`mux\.HandleFunc\("(/[a-zA-Z0-9/_-]+)"`)
	routes := distinctMatches(re, src)
	routes = append(routes, "/ws") // registered on the server mux (websocket_server.go)
	sort.Strings(routes)

	var rows []parityRow
	for _, r := range routes {
		rows = append(rows, parityRow{
			ID:           "route." + routeSlug(r),
			Dimension:    "route",
			Subject:      "HTTP/WS route " + r,
			Contract:     "E",
			Authority:    "web_api.go / websocket_server.go",
			Parity:       "exact",
			Status:       "unverified",
			AssignedTask: routeSweep(r),
		})
	}
	return rows
}

func routeSweep(r string) string {
	switch {
	case strings.HasPrefix(r, "/api/auth"), strings.HasPrefix(r, "/api/museum"):
		return "M16.16"
	case r == "/api/generate":
		return "M16.17"
	case strings.HasPrefix(r, "/api/saves"), r == "/api/restore", r == "/api/loadworld":
		return "M16.15"
	case r == "/ws":
		return "M16.8"
	default:
		return "M16.16"
	}
}

// ---------------------------------------------------------------------------
// Curated dimensions
// ---------------------------------------------------------------------------

func curatedOopStructuralRows() []parityRow {
	items := []struct{ slug, subject string }{
		{"label", "ZZT-OOP :label target"},
		{"object-name", "ZZT-OOP @name object naming line"},
		{"command-prefix", "ZZT-OOP # command prefix vs bare text"},
		{"text-line", "ZZT-OOP plain text line (scroll content)"},
		{"hyperlink", "ZZT-OOP !label;text hyperlink choice"},
		{"play-sound", "ZZT-OOP #play / ; drum-and-note sound form"},
		{"comment", "ZZT-OOP ' comment line"},
		{"move-shorthand", "ZZT-OOP /dir and ?dir movement shorthands"},
	}
	var rows []parityRow
	for _, it := range items {
		rows = append(rows, parityRow{
			ID:           "oop-struct." + it.slug,
			Dimension:    "oop-structural",
			Subject:      it.subject,
			Contract:     "V",
			Authority:    "reference/reconstruction-of-zzt/SRC OOP.PAS",
			Parity:       "exact",
			Status:       "unverified",
			AssignedTask: "M16.6",
		})
	}
	return rows
}

func curatedInputRows() []parityRow {
	type in struct {
		slug, subject, mode, task string
		dev                       string
	}
	items := []in{
		{"play-move", "Arrow / numpad 8·4·6·2 movement", "play", "M16.10", ""},
		{"play-shoot-shift", "Shift+direction shoot", "play", "M16.10", ""},
		{"play-shoot-space", "Space shoot last direction", "play", "M16.10", ""},
		{"play-torch", "T light torch", "play", "M16.10", ""},
		{"play-save", "S save game (SavePromptEvent)", "play", "M16.10", ""},
		{"play-pause", "P per-player pause", "play", "M16.10", "per-player-modal-freeze"},
		{"play-sound-toggle", "B per-player sound toggle", "play", "M16.10", "per-player-sound"},
		{"play-quit", "Q / Esc quit prompt", "play", "M16.10", ""},
		{"play-help", "H help window", "play", "M16.10", ""},
		{"play-debug", "? debug prompt", "play", "M16.10", ""},
		{"play-wasd-removed", "WASD movement (removed)", "play", "M16.10", "wasd-removed"},
		{"title-world", "W world select (picker)", "title", "M16.16", "presentation-additions"},
		{"title-play", "P play", "title", "M16.11", ""},
		{"title-restore", "R restore snapshot", "title", "M16.15", ""},
		{"title-quit", "Q quit", "title", "M16.11", ""},
		{"title-highscores", "H high scores", "title", "M16.11", ""},
		{"title-about", "A about", "title", "M16.11", ""},
		{"title-editor", "E editor", "title", "M16.13", ""},
		{"title-speed-omitted", "S game speed (omitted, server owns tick)", "title", "M16.11", "omitted-game-speed"},
		{"textwin-nav", "Up/Down/PgUp/PgDn/Enter/Esc text-window navigation", "modal", "M16.10", ""},
		{"editor-keys", "Editor key/menu vocabulary (draw, pattern, color, fill, properties, stat/OOP, test-play)", "editor", "M16.13", ""},
	}
	var rows []parityRow
	for _, it := range items {
		r := parityRow{
			ID:           "input." + it.slug,
			Dimension:    "input",
			Subject:      it.subject + " [" + it.mode + "]",
			Contract:     "P",
			Authority:    "elements.go ElementPlayerTick / game.go title / editor.go; web/src/keys.ts",
			Parity:       "exact",
			Status:       "unverified",
			AssignedTask: it.task,
		}
		if it.dev != "" {
			r.Parity = "deviation"
			r.Deviation = it.dev
		}
		rows = append(rows, r)
	}
	return rows
}

func curatedBrowserModeRows() []parityRow {
	items := []struct{ slug, subject, contract, task, dev string }{
		{"title", "Title screen mode (animated title board + menu)", "P", "M16.9", ""},
		{"playing", "Playing mode (60x25 board + authentic sidebar)", "P", "M16.9", ""},
		{"editor", "Editor mode (browser editor UI)", "E", "M16.13", ""},
		{"modal-scroll", "CP437 scroll/text window", "P", "M16.9", ""},
		{"modal-help", "CP437 help window", "P", "M16.9", ""},
		{"modal-debug", "CP437 debug window", "P", "M16.9", ""},
		{"modal-save", "Save/name entry modal", "E", "M16.10", ""},
		{"modal-quit", "Quit confirmation modal", "P", "M16.9", ""},
		{"modal-highscore", "High-score entry/display modal", "P", "M16.9", ""},
		{"modal-picker", "World picker window", "E", "M16.16", "presentation-additions"},
		{"modal-dream", "Dream-a-world prompt/progress window", "E", "M16.17", "presentation-additions"},
		{"modal-museum", "Museum search/select window", "E", "M16.16", "presentation-additions"},
		{"identity-overlay", "Per-player identity overlay", "E", "M16.12", "presentation-additions"},
		{"mobile-textentry", "Mobile on-screen keyboard for text surfaces", "E", "M16.18", ""},
		{"mobile-touchplay", "Mobile touch movement/shoot/torch/pause controls", "E", "M16.18a", "mobile-touch-gap"},
	}
	var rows []parityRow
	for _, it := range items {
		r := parityRow{
			ID:           "mode." + it.slug,
			Dimension:    "browser-mode",
			Subject:      it.subject,
			Contract:     it.contract,
			Authority:    "web/src/*.ts",
			Parity:       "exact",
			Status:       "unverified",
			AssignedTask: it.task,
		}
		if it.dev != "" {
			r.Parity = "deviation"
			r.Deviation = it.dev
		}
		if it.slug == "mobile-touchplay" {
			r.Status = "gap"
			r.Notes = "touch gameplay not shipped; filed as gap task M16.18a (owner decision 2026-07-15)"
		}
		rows = append(rows, r)
	}
	return rows
}

func curatedServiceRows() []parityRow {
	items := []struct{ slug, subject, task, dev string }{
		{"save-restore", "Room snapshot save + rejoinable restore", "M16.15", "snapshot-player-drop"},
		{"account-persistence", "Account sidecar state persistence/restore", "M16.15", "account-sidecar-restore"},
		{"high-scores", "Per-world high-score list entry/display", "M16.15", "score-on-quit"},
		{"reconnect", "Reconnect within/after grace; competing resume", "M16.15", ""},
		{"session-replay", "Session recording + deterministic replay", "M16.15", ""},
		{"auth", "Google OIDC sign-in vs guest identity", "M16.16", ""},
		{"chat", "Chat filtering / rate-limit / history", "M16.16", ""},
		{"museum", "Museum search/select/host/join", "M16.16", ""},
		{"world-picker", "World-picker listing + metadata", "M16.16", "presentation-additions"},
		{"dream", "Dream plan→paint→repair generation + host/join", "M16.17", "presentation-additions"},
		{"editor-collab", "Collaborative editor leases/presence/publish", "M16.14", ""},
		{"editor-solo", "Solo editor + portable .ZZT/.BRD export", "M16.13", ""},
	}
	var rows []parityRow
	for _, it := range items {
		r := parityRow{
			ID:           "service." + it.slug,
			Dimension:    "service",
			Subject:      it.subject,
			Contract:     "E",
			Authority:    "completed task contracts (TASKS.md); NOTES.md",
			Parity:       "exact",
			Status:       "unverified",
			AssignedTask: it.task,
		}
		if it.dev != "" {
			r.Parity = "deviation"
			r.Deviation = it.dev
		}
		rows = append(rows, r)
	}
	return rows
}

// seededDeviations is the approved intentional-divergence catalog (PARITY.md §4).
func seededDeviations() []parityDeviation {
	return []parityDeviation{
		{"mp-respawn", "Death is a respawn (penalty + brief invulnerability), not game-over", "E", "tasks M2.4, M4.3; NOTES 2026-07-09"},
		{"collision-pushout", "Overlapping players: the arriver is pushed to a free adjacent square", "E", "task M4.3b; placement.go"},
		{"friendly-fire-policy", "Multiplayer projectile/contact damage follows an explicit friendly-fire policy", "E", "task M2.4; Engine.FriendlyFire"},
		{"per-player-modal-freeze", "Modals freeze only the acting player; the room keeps ticking", "E", "tasks M1.3, M3.11"},
		{"shared-world-flags", "World.Info.Flags are shared across players (co-op puzzle progress)", "E", "task M2.1; NOTES 2026-07-09"},
		{"snapshot-player-drop", "A restored snapshot drops other players; a joiner arrives fresh", "E", "task M4.3a"},
		{"account-sidecar-restore", "Per-player keys/inventory live in an account sidecar, not the snapshot", "E", "tasks M4.3a, persistence"},
		{"omitted-game-speed", "The menu omits vanilla's S game-speed — the server owns the tick", "E", "task M4.3"},
		{"score-on-quit", "A high score is entered on quit, not on death", "E", "task M4.3"},
		{"wasd-removed", "Movement is arrows + numpad 8/4/6/2 only; WASD removed (S collides with save)", "P", "task M4.2; NOTES"},
		{"per-player-sound", "Pickup/shot/damage sounds are per-acting-player; #play stays room-wide", "E", "task M7.4"},
		{"scroll-removal-timing", "A windowed scroll is consumed on reply, not on touch (de-modal)", "P", "task M17.4"},
		{"presentation-additions", "Presentation-only additions with no vanilla counterpart (name popup, picker, overlay, chat, sound UI, Dream, help/debug windows)", "E", "tasks M3.8–M3.10, M4.x, M6.x, M12.5"},
		{"mobile-touch-gap", "Mobile ships text entry only; touch gameplay is a filed gap task (M16.18a)", "E", "task M15.1; owner decision 2026-07-15"},
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func loadParityManifest(t *testing.T) parityManifest {
	t.Helper()
	data, err := os.ReadFile(parityManifestPath)
	if err != nil {
		t.Fatalf("read %s: %v (regenerate with PARITY_SCAFFOLD=1)", parityManifestPath, err)
	}
	var m parityManifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse %s: %v", parityManifestPath, err)
	}
	if m.SchemaVersion != 1 {
		t.Fatalf("unexpected schemaVersion %d", m.SchemaVersion)
	}
	return m
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func distinctMatches(re *regexp.Regexp, src string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out
}

// existingGoTestNames scans every *_test.go in the engine dir for func Test…
func existingGoTestNames(t *testing.T) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^func (Test\w+)\(`)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(e.Name())
		if err != nil {
			continue
		}
		for _, m := range re.FindAllStringSubmatch(string(data), -1) {
			names[m[1]] = true
		}
	}
	return names
}

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func defOrUnnamed(name string) string {
	if name == "" {
		return "unnamed"
	}
	return name
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func routeSlug(r string) string {
	s := strings.TrimPrefix(r, "/")
	s = strings.ReplaceAll(s, "/", ".")
	s = strings.ReplaceAll(s, "_", "-")
	if s == "" {
		return "root"
	}
	return s
}

// elementConst returns the E_ constant name for an index, for readable subjects.
func elementConst(i int) string {
	names := map[int]string{
		E_EMPTY: "E_EMPTY", E_BOARD_EDGE: "E_BOARD_EDGE", E_MESSAGE_TIMER: "E_MESSAGE_TIMER",
		E_MONITOR: "E_MONITOR", E_PLAYER: "E_PLAYER", E_AMMO: "E_AMMO", E_TORCH: "E_TORCH",
		E_GEM: "E_GEM", E_KEY: "E_KEY", E_DOOR: "E_DOOR", E_SCROLL: "E_SCROLL",
		E_PASSAGE: "E_PASSAGE", E_DUPLICATOR: "E_DUPLICATOR", E_BOMB: "E_BOMB",
		E_ENERGIZER: "E_ENERGIZER", E_STAR: "E_STAR", E_CONVEYOR_CW: "E_CONVEYOR_CW",
		E_CONVEYOR_CCW: "E_CONVEYOR_CCW", E_BULLET: "E_BULLET", E_WATER: "E_WATER",
		E_FOREST: "E_FOREST", E_SOLID: "E_SOLID", E_NORMAL: "E_NORMAL", E_BREAKABLE: "E_BREAKABLE",
		E_BOULDER: "E_BOULDER", E_SLIDER_NS: "E_SLIDER_NS", E_SLIDER_EW: "E_SLIDER_EW",
		E_FAKE: "E_FAKE", E_INVISIBLE: "E_INVISIBLE", E_BLINK_WALL: "E_BLINK_WALL",
		E_TRANSPORTER: "E_TRANSPORTER", E_LINE: "E_LINE", E_RICOCHET: "E_RICOCHET",
		E_BLINK_RAY_EW: "E_BLINK_RAY_EW", E_BEAR: "E_BEAR", E_RUFFIAN: "E_RUFFIAN",
		E_OBJECT: "E_OBJECT", E_SLIME: "E_SLIME", E_SHARK: "E_SHARK",
		E_SPINNING_GUN: "E_SPINNING_GUN", E_PUSHER: "E_PUSHER", E_LION: "E_LION",
		E_TIGER: "E_TIGER", E_BLINK_RAY_NS: "E_BLINK_RAY_NS", E_CENTIPEDE_HEAD: "E_CENTIPEDE_HEAD",
		E_CENTIPEDE_SEGMENT: "E_CENTIPEDE_SEGMENT", E_TEXT_BLUE: "E_TEXT_BLUE",
		E_TEXT_GREEN: "E_TEXT_GREEN", E_TEXT_CYAN: "E_TEXT_CYAN", E_TEXT_RED: "E_TEXT_RED",
		E_TEXT_PURPLE: "E_TEXT_PURPLE", E_TEXT_YELLOW: "E_TEXT_YELLOW", E_TEXT_WHITE: "E_TEXT_WHITE",
	}
	if n, ok := names[i]; ok {
		return n
	}
	return "E_#" + itoa(i)
}
