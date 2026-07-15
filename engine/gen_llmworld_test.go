//go:build canary

// LLM few-shot corpus generator (task M16.1): TestGenLLMWorldExamples decompiles
// boards from the untracked .ZZT worlds in the engine directory and writes the
// committed ../llmworld/examples/ corpus. It depends on untracked worlds and
// writes committed files, so it is a maintainer generator kept behind the
// `canary` build tag, out of the required `go test ./...` path. The committed
// corpus is verified in the required path by TestLLMWorldExamplesCompile.
package zztgo

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestGenLLMWorldExamples decompiles the two most representative boards from
// every .ZZT world in the engine directory into ../llmworld/examples/.
//
// "Representative" means the board with the most non-empty tiles plus a heavy
// bonus for stats (objects, scrolls, text elements), which selects boards that
// are visually composed AND have authored text — exactly the style targets for
// few-shot LLM prompting. Board 0 (title screen) is skipped; it is sparse by
// design and not a useful layout example.
//
// Run with: cd engine && go test -run TestGenLLMWorldExamples -v
// The generated .zwd files are committed as the llmworld corpus.
func TestGenLLMWorldExamples(t *testing.T) {
	outDir := filepath.Join("..", "llmworld", "examples")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", outDir, err)
	}

	paths, err := filepath.Glob("*.ZZT")
	if err != nil || len(paths) == 0 {
		t.Skip("no .ZZT files in engine directory")
	}

	for _, worldPath := range paths {
		stem := strings.TrimSuffix(filepath.Base(worldPath), ".ZZT")
		genWorldExamples(t, stem, outDir)
	}
}

// genWorldExamples decompiles the two best boards of one world. Downloaded
// community worlds can carry corrupt boards that panic BoardOpen; a recover
// here skips those worlds instead of killing the whole corpus run.
func genWorldExamples(t *testing.T, stem, outDir string) {
	defer func() {
		if r := recover(); r != nil {
			t.Logf("skip %s: panic while processing (corrupt board?): %v", stem, r)
		}
	}()

	e := NewEngine()
	e.Headless = true
	e.WorldCreate()
	if !e.WorldLoad(stem, ".ZZT", false) {
		t.Logf("skip %s: WorldLoad failed", stem)
		return
	}

	fullZWD, diagnostics := DecompileZWDAuthorable(&e.World)
	if fullZWD == "" {
		t.Logf("skip %s: not authorable (%s)", stem, formatZWDDecompileDiagnostics(diagnostics))
		return
	}
	if len(diagnostics) > 0 {
		t.Logf("%s: %s", stem, formatZWDDecompileDiagnostics(diagnostics))
	}
	for _, boardIdx := range pickRepresentativeBoards(e, 2) {
		zwd := zwdExtractBoardSection(fullZWD, boardIdx)

		outPath := filepath.Join(outDir, fmt.Sprintf("%s_board%d.zwd", stem, boardIdx))
		if err := os.WriteFile(outPath, []byte(zwd), 0644); err != nil {
			t.Fatalf("write %s: %v", outPath, err)
		}
		t.Logf("%s → board %d (%q) → %s (%d bytes)",
			stem, boardIdx, boardNameAt(e, boardIdx), outPath, len(zwd))
	}
}

func formatZWDDecompileDiagnostics(diagnostics []ZWDDecompileDiagnostic) string {
	parts := make([]string, 0, len(diagnostics))
	for _, d := range diagnostics {
		parts = append(parts, fmt.Sprintf("board %d %s: %s", d.Board, d.Severity, d.Message))
	}
	return strings.Join(parts, "; ")
}

// pickRepresentativeBoards returns the indices of the n boards in e.World that
// make the best few-shot examples: visually dense (many non-empty tiles),
// authored (many stats — objects, scrolls, passages carry story and design
// intent), with text elements and color variety (shading, composed scenes).
// Board 0 (title screen) is always skipped.
func pickRepresentativeBoards(e *Engine, n int) []int {
	if e.World.BoardCount == 0 {
		return nil
	}

	type scored struct {
		board int
		score int
	}
	var all []scored

	for i := int16(1); i <= e.World.BoardCount; i++ {
		e.BoardOpen(i)

		nonEmpty := 0
		textCells := 0
		colors := map[byte]bool{}
		for x := int16(1); x <= BOARD_WIDTH; x++ {
			for y := int16(1); y <= BOARD_HEIGHT; y++ {
				tile := e.Board.Tiles[x][y]
				if tile.Element == E_EMPTY {
					continue
				}
				nonEmpty++
				colors[tile.Color] = true
				if tile.Element >= E_TEXT_BLUE && tile.Element <= E_TEXT_WHITE {
					textCells++
				}
			}
		}

		// Stats carry authored meaning: objects have OOP scripts, scrolls have
		// text, passages gate progress. Weight them heavily so boards with actual
		// content beat large-but-empty maze boards. Text cells show lettering and
		// signage; distinct colors reward shading and deliberate palettes.
		stats := int(e.Board.StatCount)
		score := nonEmpty + stats*25 + textCells*3 + len(colors)*20

		all = append(all, scored{board: int(i), score: score})
	}

	sort.Slice(all, func(a, b int) bool { return all[a].score > all[b].score })
	if n > len(all) {
		n = len(all)
	}
	picks := make([]int, 0, n)
	for _, s := range all[:n] {
		picks = append(picks, s.board)
	}
	sort.Ints(picks)
	return picks
}

// boardNameAt returns the name of board i without permanently changing e's state.
func boardNameAt(e *Engine, i int) string {
	e.BoardOpen(int16(i))
	return e.Board.Name
}

// decompileSingleBoard decompiles board boardIdx from e into a standalone ZWD
// board section string (just the board block, no world header line).
func decompileSingleBoard(e *Engine, boardIdx int) string {
	// DecompileZWD requires all boards to be closed/serialized first.
	e.BoardClose()
	full := DecompileZWD(&e.World)
	return zwdExtractBoardSection(full, boardIdx)
}

// zwdExtractBoardSection extracts the Nth board section (0-indexed) from a
// full ZWD document, returning just the "board ... end" block.
func zwdExtractBoardSection(zwd string, boardIndex int) string {
	lines := strings.Split(zwd, "\n")
	boardCount := -1
	startLine := -1
	endLine := -1
	depth := 0

	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "board ") && depth == 0 {
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
			if strings.HasPrefix(trimmed, "grid") || strings.HasPrefix(trimmed, "legend") ||
				strings.HasPrefix(trimmed, "stats") || strings.HasPrefix(trimmed, "oop") {
				depth++
			} else if trimmed == "end" && depth > 1 {
				depth--
			}
		}
	}

	if startLine < 0 || endLine < 0 {
		return ""
	}
	return strings.Join(lines[startLine:endLine], "\n") + "\n"
}
