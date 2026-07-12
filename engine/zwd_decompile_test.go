package zztgo

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestZWDRoundTripTOWN decompiles TOWN.ZZT → ZWD → recompiles → reloads and
// compares the canonical ZWD representation of every board.
func TestZWDRoundTripTOWN(t *testing.T) {
	testZWDRoundTrip(t, "TOWN.ZZT")
}

func TestZWDRoundTripCAVES(t *testing.T) {
	testZWDRoundTrip(t, "CAVES.ZZT")
}

func TestZWDRoundTripCITY(t *testing.T) {
	testZWDRoundTrip(t, "CITY.ZZT")
}

func TestDecompileZWDAuthorableRejectsInvalidHistoricalState(t *testing.T) {
	world, err := CompileZWDWorld(zwdOneRoomExample)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine()
	e.Headless = true
	e.World = world
	e.BoardOpen(0)
	e.Board.Info.StartPlayerX = 98
	e.Board.Info.StartPlayerY = 98
	e.BoardClose()

	src, diagnostics := DecompileZWDAuthorable(&e.World)
	if src == "" {
		t.Fatalf("invalid respawn should be safely omitted, diagnostics: %#v", diagnostics)
	}
	if len(diagnostics) == 0 || diagnostics[0].Severity != "warning" {
		t.Fatalf("diagnostics = %#v, want invalid-respawn warning", diagnostics)
	}
	if _, err := CompileZWD(src); err != nil {
		t.Fatalf("authorable source does not compile: %v", err)
	}
}

func TestDecompileZWDAuthorableRefusesUnrepresentableBoard(t *testing.T) {
	world, err := CompileZWDWorld(zwdOneRoomExample)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine()
	e.Headless = true
	e.World = world
	e.BoardOpen(0)
	// A second object tile without a stat has no safe interactive semantics.
	// The authorable exporter must report it rather than emit invalid ZWD.
	e.Board.Tiles[1][1] = TTile{Element: E_OBJECT, Color: 0x0F}
	e.BoardClose()

	src, diagnostics := DecompileZWDAuthorable(&e.World)
	if src != "" {
		t.Fatal("unrepresentable board unexpectedly returned authorable source")
	}
	if src := DecompileZWD(&e.World); src != "" {
		t.Fatal("DecompileZWD unexpectedly returned non-authorable source")
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return
		}
	}
	t.Fatalf("diagnostics = %#v, want an error", diagnostics)
}

// TestLLMWorldExamplesCompile is the corpus gate for M12.6. Each committed
// example is a board fragment, so compile it as a one-board world after
// neutralizing references that cannot be represented by a standalone board.
// This tests the authorability of the fragment itself, not the original
// world's cross-board topology.
func TestLLMWorldExamplesCompile(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("..", "llmworld", "examples"))
	if err != nil {
		t.Fatal(err)
	}

	exitRe := regexp.MustCompile(`(?m)^\s*exits .*$`)
	passageBoardRe := regexp.MustCompile(`\bp3\s+board\s+"(?:\\.|[^"])*"`)
	count := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".zwd" {
			continue
		}
		count++
		src, err := os.ReadFile(filepath.Join("..", "llmworld", "examples", entry.Name()))
		if err != nil {
			t.Errorf("read %s: %v", entry.Name(), err)
			continue
		}
		section := exitRe.ReplaceAllString(string(src), "  exits north none south none west none east none")
		section = passageBoardRe.ReplaceAllString(section, "p3 0")
		doc := "zwd 1\nworld \"CORPUS\"\n\n" + section
		if _, err := CompileZWD(doc); err != nil {
			t.Errorf("%s does not compile: %v", entry.Name(), err)
		}
	}
	// The original 200-board corpus included historical boards that cannot be
	// represented by authorable ZWD. The remaining 125 are regenerated through
	// DecompileZWDAuthorable; changing that accepted set must be intentional.
	if count != 125 {
		t.Fatalf("corpus has %d authorable examples, want 125", count)
	}
}

func testZWDRoundTrip(t *testing.T, filename string) {
	t.Helper()

	path := filepath.Join(".", filename)
	if _, err := os.Stat(path); err != nil {
		path = filepath.Join("fixtures", filename)
	}
	if _, err := os.Stat(path); err != nil {
		path = filepath.Join("../fixtures", filename)
	}

	// Load the original world.
	orig := loadTestWorld(t, path)

	// ZWD deliberately excludes save-game state (world flags/current board and
	// player runtime fields), raw unnamed elements, and off-board sentinel
	// stats. Its canonical board sections are therefore the round-trip contract,
	// rather than StateHash, which includes all of those fields.
	origBoards := collectCanonicalZWDBoards(t, orig)

	// Decompile.
	zwd := DecompileZWD(orig)
	if len(zwd) == 0 {
		t.Fatal("DecompileZWD returned empty string")
	}

	// Recompile.
	recompiled, err := CompileZWDWorld(zwd)
	if err != nil {
		t.Fatalf("CompileZWDWorld failed on decompiled output:\n%s\n\nerror: %v", truncateForLog(zwd, 2000), err)
	}

	// Serialize the recompiled world to bytes, then reload.
	reloadE := NewEngine()
	reloadE.Headless = true
	reloadE.VideoInstall()
	reloadE.World = recompiled
	var buf bytes.Buffer
	if err := reloadE.worldWriteTo(&buf); err != nil {
		t.Fatalf("worldWriteTo failed: %v", err)
	}
	reloadE2 := NewEngine()
	reloadE2.Headless = true
	reloadE2.VideoInstall()
	if err := reloadE2.worldReadFrom(bytes.NewReader(buf.Bytes()), false, nil); err != nil {
		t.Fatalf("worldReadFrom on recompiled bytes failed: %v", err)
	}

	// Collect the canonical source again after serialize/reload.
	reloadBoards := collectCanonicalZWDBoards(t, &reloadE2.World)

	// Compare board counts.
	if len(origBoards) != len(reloadBoards) {
		t.Fatalf("board count mismatch: orig=%d, reloaded=%d", len(origBoards), len(reloadBoards))
	}

	// Compare canonical board source. This includes all ZWD-defined board,
	// tile, stat, and OOP fields while excluding save-game-only state.
	mismatches := 0
	for i := range origBoards {
		if origBoards[i] != reloadBoards[i] {
			t.Errorf("board %d canonical ZWD mismatch: %s", i, firstZWDTextDifference(origBoards[i], reloadBoards[i]))
			mismatches++
			if mismatches >= 5 {
				t.Fatalf("too many mismatches, stopping")
			}
		}
	}
}

func collectCanonicalZWDBoards(t *testing.T, world *TWorld) []string {
	t.Helper()
	full := DecompileZWD(world)
	boards := make([]string, world.BoardCount+1)
	for i := range boards {
		boards[i] = extractBoardSection(full, i)
		if boards[i] == "" {
			t.Fatalf("decompiler omitted board %d", i)
		}
	}
	return boards
}

func firstZWDTextDifference(want, got string) string {
	wantLines, gotLines := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(wantLines) && i < len(gotLines); i++ {
		if wantLines[i] != gotLines[i] {
			return fmt.Sprintf("line %d = %q, want %q", i+1, gotLines[i], wantLines[i])
		}
	}
	return fmt.Sprintf("line count = %d, want %d", len(gotLines), len(wantLines))
}

// TestTOWNBoard1DecompiledFixture writes the decompiled TOWN board 1 to
// fixtures/town_board1.zwd and verifies it on subsequent runs.
func TestTOWNBoard1DecompiledFixture(t *testing.T) {
	path := filepath.Join(".", "TOWN.ZZT")
	if _, err := os.Stat(path); err != nil {
		path = filepath.Join("fixtures", "TOWN.ZZT")
	}
	if _, err := os.Stat(path); err != nil {
		path = filepath.Join("../fixtures", "TOWN.ZZT")
	}

	world := loadTestWorld(t, path)
	e := NewEngine()
	e.Headless = true
	e.World = *world
	e.InitElementsGame()

	// Decompile just board 1 into a standalone ZWD snippet for the fixture.
	// We use the full DecompileZWD for correctness, then extract board 1.
	fullZWD := DecompileZWD(world)

	// For the fixture, write the full decompiled output for board 1 only.
	// We'll extract board 1 by decompiling a world with just boards 0 and 1.
	// Actually, let's write the full decompiled world and note board 1 in
	// the fixture name. But the task says "the decompiled TOWN board 1" so
	// let's extract it. Actually, for round-trip the full world is needed.
	// Let's just save the full ZWD as the fixture.
	_ = fullZWD

	// Extract board 1 section from the decompiled output.
	board1ZWD := extractBoardSection(fullZWD, 1)
	if board1ZWD == "" {
		t.Fatal("could not extract board 1 from decompiled TOWN")
	}

	fixturePath := filepath.Join(".", "fixtures", "town_board1.zwd")
	if _, err := os.Stat(filepath.Dir(fixturePath)); err != nil {
		fixturePath = filepath.Join("..", "fixtures", "town_board1.zwd")
	}

	existing, err := os.ReadFile(fixturePath)
	if err != nil {
		// Write the fixture for the first time.
		if err := os.WriteFile(fixturePath, []byte(board1ZWD), 0644); err != nil {
			t.Fatalf("writing fixture: %v", err)
		}
		t.Logf("wrote fixture %s (%d bytes)", fixturePath, len(board1ZWD))
		return
	}

	// Compare.
	if string(existing) != board1ZWD {
		t.Fatalf("fixture %s has changed; delete it and re-run to regenerate\n\ngot length %d, want length %d",
			fixturePath, len(board1ZWD), len(existing))
	}
}

// loadTestWorld loads a .ZZT file into a TWorld with all boards serialized.
func loadTestWorld(t *testing.T, path string) *TWorld {
	t.Helper()
	e := NewEngine()
	e.Headless = true
	e.VideoInstall()
	e.InitElementsGame()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if err := e.worldReadFrom(bytes.NewReader(data), false, nil); err != nil {
		t.Fatalf("worldReadFrom %s: %v", path, err)
	}
	return &e.World
}

// extractBoardSection extracts the Nth board section (0-indexed) from ZWD text.
func extractBoardSection(zwd string, boardIndex int) string {
	lines := splitLines(zwd)
	boardCount := -1
	startLine := -1
	endLine := -1
	depth := 0

	for i, line := range lines {
		trimmed := trimLeftSpace(line)
		if hasPrefix(trimmed, "board ") && depth == 0 {
			boardCount++
			if boardCount == boardIndex {
				startLine = i
				depth = 1
			}
		} else if startLine >= 0 {
			if trimmed == "end" && depth == 1 {
				endLine = i + 1
				break
			}
			// Track nesting for grid/legend/stats/oop end markers.
			if hasPrefix(trimmed, "grid") || hasPrefix(trimmed, "legend") ||
				hasPrefix(trimmed, "stats") || hasPrefix(trimmed, "oop") {
				depth++
			} else if trimmed == "end" && depth > 1 {
				depth--
			}
		}
	}

	if startLine < 0 || endLine < 0 {
		return ""
	}

	var b bytes.Buffer
	for i := startLine; i < endLine; i++ {
		b.WriteString(lines[i])
		b.WriteByte('\n')
	}
	return b.String()
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func trimLeftSpace(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\t' {
			return s[i:]
		}
	}
	return ""
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + fmt.Sprintf("\n... (%d more bytes)", len(s)-maxLen)
}
