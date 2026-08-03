package zztgo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// M12.15d: the mined style priors are an artifact, not an assertion. These
// tests hold the mining honest — it regenerates from the committed corpus, it
// is deterministic, every number it emits is grounded in corpus text, and no
// part of it reaches for an LLM at runtime.

const (
	corpusExamplesDir = "../llmworld/examples"
	corpusTopologyPth = "../llmworld/topology.json"
	stylePriorsPath   = "../llmworld/style_priors.json"
)

func mineTestPriors(t *testing.T) StylePriors {
	t.Helper()
	priors, err := MineStylePriorsFromCorpus(corpusExamplesDir, corpusTopologyPth)
	if err != nil {
		t.Fatalf("mine style priors: %v", err)
	}
	return priors
}

// TestStylePriorsRegenerateFromCorpus is the DoD's "regenerated from the
// corpus": mining the committed corpus reproduces the committed artifact
// byte-for-byte. Re-mine after changing the corpus or a miner with
//
//	ZZT_MINE_PRIORS=1 go test -run TestStylePriorsRegenerateFromCorpus
func TestStylePriorsRegenerateFromCorpus(t *testing.T) {
	priors := mineTestPriors(t)
	mined, err := marshalStableJSON(priors)
	if err != nil {
		t.Fatalf("marshal priors: %v", err)
	}
	if os.Getenv("ZZT_MINE_PRIORS") == "1" {
		if err := os.WriteFile(stylePriorsPath, mined, 0644); err != nil {
			t.Fatalf("write %s: %v", stylePriorsPath, err)
		}
		embedded := filepath.Join("promptkit_assets", "style_priors.json")
		if err := os.WriteFile(embedded, mined, 0644); err != nil {
			t.Fatalf("write %s: %v", embedded, err)
		}
		t.Logf("regenerated %s and %s (%d bytes)", stylePriorsPath, embedded, len(mined))
		return
	}
	committed, err := os.ReadFile(stylePriorsPath)
	if err != nil {
		t.Fatalf("read %s: %v", stylePriorsPath, err)
	}
	if string(committed) != string(mined) {
		t.Fatalf("%s is stale: mining the committed corpus produces a different artifact.\nRe-run: cd engine && ZZT_MINE_PRIORS=1 go test -run TestStylePriorsRegenerateFromCorpus", stylePriorsPath)
	}
}

// TestStylePriorsMiningIsDeterministic proves the artifact cannot drift with map
// iteration or input order: the same corpus mined twice, once in reverse file
// order, must marshal to identical bytes.
func TestStylePriorsMiningIsDeterministic(t *testing.T) {
	boards := loadCorpusBoards(t)
	topology := loadCorpusTopology(t)

	forward, err := marshalStableJSON(MineStylePriors(boards, topology))
	if err != nil {
		t.Fatal(err)
	}
	reversed := make([]CorpusBoard, 0, len(boards))
	for i := len(boards) - 1; i >= 0; i-- {
		reversed = append(reversed, boards[i])
	}
	backward, err := marshalStableJSON(MineStylePriors(reversed, topology))
	if err != nil {
		t.Fatal(err)
	}
	if string(forward) != string(backward) {
		t.Fatal("mining is order-dependent: reversing the corpus changed the artifact")
	}
	// A second pass over the same input must also be identical (map iteration
	// order is randomized per run, so this catches an unsorted ranking).
	again, err := marshalStableJSON(MineStylePriors(boards, topology))
	if err != nil {
		t.Fatal(err)
	}
	if string(forward) != string(again) {
		t.Fatal("mining is not reproducible within a run")
	}
}

// TestStylePriorsAreNonEmptyAndGrounded is the DoD's "non-empty, data-grounded":
// all three artifact families carry entries, and every element, color and
// command they name is one that literally occurs in the corpus text.
func TestStylePriorsAreNonEmptyAndGrounded(t *testing.T) {
	priors := mineTestPriors(t)

	if priors.Version != stylePriorsVersion {
		t.Errorf("artifact version = %d, want %d", priors.Version, stylePriorsVersion)
	}
	if priors.Corpus.Boards == 0 || priors.Corpus.Worlds == 0 || priors.Corpus.Cells == 0 ||
		priors.Corpus.LegendEntries == 0 || priors.Corpus.OOPPrograms == 0 {
		t.Fatalf("corpus facts are empty: %+v", priors.Corpus)
	}
	if len(priors.Palette.Tiles) == 0 || len(priors.Palette.Colors) == 0 ||
		len(priors.Palette.ShadingPairs) == 0 || len(priors.Palette.Lettering) == 0 {
		t.Fatal("palette priors are incomplete")
	}
	if len(priors.OOP.Commands) == 0 || len(priors.OOP.Sequences) == 0 ||
		len(priors.OOP.Labels) == 0 || len(priors.OOP.Rituals) == 0 {
		t.Fatal("OOP priors are incomplete")
	}
	architecture := priors.Architecture
	if architecture.BoardsMedian == 0 || architecture.ReciprocityPerMille == 0 ||
		architecture.ReachablePerMille == 0 || architecture.StatsPerBoardPerTen == 0 {
		t.Fatalf("architecture priors are empty: %+v", architecture)
	}
	if architecture.BoardsP25 > architecture.BoardsMedian || architecture.BoardsMedian > architecture.BoardsP75 ||
		architecture.BoardsP75 > architecture.BoardsMax {
		t.Errorf("board-count quartiles are not ordered: %+v", architecture)
	}
	for _, share := range []int{architecture.ReciprocityPerMille, architecture.ReachablePerMille,
		architecture.DarkBoardPerMille, architecture.HubWorldPerMille, priors.Palette.FillPerMille} {
		if share < 0 || share > 1000 {
			t.Errorf("share %d is not a per-mille value", share)
		}
	}

	// Grounding: the corpus text must actually contain what the priors claim.
	// ZZT-OOP is case-insensitive and the miner normalizes case, so the search
	// is done with both sides lowered.
	corpus := strings.ToLower(readCorpusText(t))
	for _, tile := range priors.Palette.Tiles {
		if tile.PerMille <= 0 || len(tile.Colors) == 0 {
			t.Errorf("tile prior %q has no measured share or colors", tile.Element)
		}
		for _, color := range tile.Colors {
			legend := strings.ToLower(" = " + tile.Element + " color " + color.Color)
			if !strings.Contains(corpus, legend) {
				t.Errorf("tile prior %q %s appears in no corpus legend line", tile.Element, color.Color)
			}
		}
	}
	for _, command := range priors.OOP.Commands {
		if command.Count <= 0 {
			t.Errorf("command prior %q has no uses", command.Command)
		}
		if !strings.Contains(corpus, "\n"+command.Command) {
			t.Errorf("command prior %q appears nowhere in the corpus", command.Command)
		}
	}
	for _, label := range priors.OOP.Labels {
		if !strings.Contains(corpus, "\n"+label.Label) {
			t.Errorf("label prior %q appears nowhere in the corpus", label.Label)
		}
	}
	for _, ritual := range priors.OOP.Rituals {
		if ritual.PerMille <= 0 {
			t.Errorf("ritual %q was measured at zero; it is not an idiom of this corpus", ritual.Name)
		}
	}

	// The counts must match the corpus that is actually on disk, not a number
	// baked into the miner.
	boards := loadCorpusBoards(t)
	if priors.Corpus.Boards != len(boards) {
		t.Errorf("priors mined %d boards, corpus has %d", priors.Corpus.Boards, len(boards))
	}
	if priors.Corpus.TopologyWorlds != len(loadCorpusTopology(t).Worlds) {
		t.Error("priors and topology.json disagree on the world count")
	}
}

// TestCorpusBoardsParse guards the miner's front door: every committed example
// parses, and parsing yields the grid, legend and OOP the miners read.
func TestCorpusBoardsParse(t *testing.T) {
	boards := loadCorpusBoards(t)
	if len(boards) != 134 {
		t.Fatalf("parsed %d corpus boards, want 134", len(boards))
	}
	withOOP, withStats := 0, 0
	for _, board := range boards {
		if len(board.Grid) != BOARD_HEIGHT {
			t.Errorf("%s has %d grid rows, want %d", board.Name, len(board.Grid), BOARD_HEIGHT)
		}
		for i, row := range board.Grid {
			if len(row) != BOARD_WIDTH {
				t.Errorf("%s grid row %d is %d bytes, want %d", board.Name, i, len(row), BOARD_WIDTH)
			}
			for x := 0; x < len(row); x++ {
				if _, ok := board.Legend[row[x]]; !ok {
					t.Errorf("%s grid row %d uses key %q with no legend entry", board.Name, i, row[x:x+1])
					break
				}
			}
		}
		if board.World == board.Name {
			t.Errorf("%s did not yield a world name", board.Name)
		}
		if len(board.OOP) > 0 {
			withOOP++
		}
		if board.Stats > 0 {
			withStats++
		}
	}
	if withOOP == 0 || withStats == 0 {
		t.Fatalf("corpus parse found %d boards with OOP and %d with stats", withOOP, withStats)
	}
}

// TestParseCorpusBoardRejectsMalformed keeps the parser strict: the corpus is
// decompiler output, so a shape it cannot read is a corpus problem to look at,
// never a line to skip silently.
func TestParseCorpusBoardRejectsMalformed(t *testing.T) {
	cases := map[string]string{
		"no grid":             "board \"X\"\n  legend\n    . = Empty color 0x0F\n  end\nend\n",
		"truncated cp437 key": "board \"X\"\n  grid\n.\n  end\n  legend\n    cp437:0x\n  end\nend\n",
		"no legend":           "board \"X\"\n  grid\n" + strings.Repeat(".", BOARD_WIDTH) + "\n  end\nend\n",
		"bad legend":          "board \"X\"\n  grid\n.\n  end\n  legend\n    . = Empty\n  end\nend\n",
		"non-hex color":       "board \"X\"\n  grid\n.\n  end\n  legend\n    . = Empty color 0xZZ\n  end\nend\n",
		"short color":         "board \"X\"\n  grid\n.\n  end\n  legend\n    . = Empty color 0x0\n  end\nend\n",
	}
	for name, src := range cases {
		if _, err := ParseCorpusBoard("MALFORMED_board1", src); err == nil {
			t.Errorf("%s: parse succeeded, want an error", name)
		}
	}
}

// TestStylePriorsPromptBlocks checks the rendered prompt sections carry the
// mined numbers (not prose about them), stay compact, and are byte-stable.
func TestStylePriorsPromptBlocks(t *testing.T) {
	priors := mineTestPriors(t)

	block := priors.PromptBlock()
	for _, want := range []string{
		"# Corpus-mined style priors (v1)",
		"## Palette and texture",
		"## World architecture",
		"## ZZT-OOP idioms",
		priors.Palette.Tiles[0].Element,
		priors.Palette.ShadingPairs[0].A,
		priors.OOP.Commands[0].Command,
	} {
		if !strings.Contains(block, want) {
			t.Errorf("style priors prompt block is missing %q", want)
		}
	}
	if !strings.Contains(block, pct(priors.Architecture.ReciprocityPerMille)) {
		t.Error("style priors prompt block omits the measured exit reciprocity")
	}
	if len(block) > 4_000 {
		t.Errorf("style priors prompt block is %d bytes; it must stay compact enough for the cached system prompt", len(block))
	}
	if again := priors.PromptBlock(); again != block {
		t.Error("style priors prompt block is not byte-stable")
	}

	architecture := priors.ArchitectureBlock()
	if !strings.Contains(architecture, "# Corpus-mined architecture priors") ||
		!strings.Contains(architecture, pct(priors.Architecture.ReachablePerMille)) {
		t.Errorf("architecture prompt block is incomplete:\n%s", architecture)
	}
	if strings.Contains(architecture, "## ZZT-OOP idioms") {
		t.Error("architecture block should carry topology norms only")
	}
	if len(architecture) > 1_500 {
		t.Errorf("architecture prompt block is %d bytes; the planner prompt must stay small", len(architecture))
	}
}

func loadCorpusBoards(t *testing.T) []CorpusBoard {
	t.Helper()
	entries, err := os.ReadDir(corpusExamplesDir)
	if err != nil {
		t.Fatal(err)
	}
	var boards []CorpusBoard
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".zwd" {
			continue
		}
		src, err := os.ReadFile(filepath.Join(corpusExamplesDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		board, err := ParseCorpusBoard(strings.TrimSuffix(entry.Name(), ".zwd"), string(src))
		if err != nil {
			t.Fatalf("parse %s: %v", entry.Name(), err)
		}
		boards = append(boards, board)
	}
	return boards
}

func loadCorpusTopology(t *testing.T) WorldTopologyCorpus {
	t.Helper()
	raw, err := os.ReadFile(corpusTopologyPth)
	if err != nil {
		t.Fatal(err)
	}
	var topology WorldTopologyCorpus
	if err := json.Unmarshal(raw, &topology); err != nil {
		t.Fatal(err)
	}
	if len(topology.Worlds) == 0 {
		t.Fatal("topology.json has no worlds")
	}
	return topology
}

func readCorpusText(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir(corpusExamplesDir)
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".zwd" {
			continue
		}
		src, err := os.ReadFile(filepath.Join(corpusExamplesDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		all.Write(src)
	}
	return all.String()
}
