package zztgo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Corpus-mined style priors (M12.15d) — the offline "adapter weights" half of
// the M12.15 world-style adapter. We cannot fine-tune a closed API model, so
// everything a LoRA would bake into weights we instead compute deterministically
// from real ZZT worlds and inject at prompt time.
//
// Three artifact families, all mined by pure Go with no LLM anywhere:
//
//   - palette/tile priors — element and color frequencies, the adjacent
//     pairings that read as shading, and the lettering conventions, mined from
//     the committed authorable corpus in llmworld/examples/;
//   - architecture priors — board-count norms, edge-exit degree distribution,
//     exit reciprocity, passage/object/dark-board density and connectivity,
//     mined from llmworld/topology.json (written by the canary generator in
//     gen_world_topology_test.go from the untracked whole .ZZT worlds);
//   - ZZT-OOP idiom priors — command frequencies, the consecutive-command pairs
//     that recur, the labels authors actually define, and the named rituals.
//
// These regenerate STYLE.md's quantitative claims from data instead of asserting
// them. The mined artifact is versioned, committed at llmworld/style_priors.json,
// embedded under promptkit_assets/, and rendered into the stable (cacheable)
// system prompts — never into the per-request block, so it cannot move the
// prompt-cache key.
//
// Regenerate the mined artifact with:
//
//	cd engine && ZZT_MINE_PRIORS=1 go test -run TestStylePriorsRegenerateFromCorpus
//
// and the whole-world topology it consumes with:
//
//	cd engine && go test -tags canary -run TestGenWorldTopology

// stylePriorsVersion is the schema version of both mined artifacts. Bump it when
// the shape of a mined field changes; LoadPromptKit refuses a mismatched
// embedded artifact rather than feeding the model a prior it cannot read.
const stylePriorsVersion = 1

// Mining budgets. The prompt block must stay small enough to sit in the cached
// system prompt beside the spec and the house style, so each list is truncated
// to the head of its (deterministically sorted) ranking.
const (
	priorsMaxTiles      = 12
	priorsMaxColors     = 10
	priorsMaxPairs      = 10
	priorsMaxLettering  = 4
	priorsMaxCommands   = 12
	priorsMaxSequences  = 8
	priorsMaxLabels     = 8
	priorsColorsPerTile = 2
)

// WorldTopology is one whole world's architecture, reduced to counts. No tiles,
// no names beyond the world's own: this is the shape of the world graph only.
type WorldTopology struct {
	Name                string `json:"name"`
	Boards              int    `json:"boards"`
	DarkBoards          int    `json:"darkBoards"`
	Stats               int    `json:"stats"`
	Objects             int    `json:"objects"`
	Passages            int    `json:"passages"`
	EdgeLinks           int    `json:"edgeLinks"`
	ReciprocalEdgeLinks int    `json:"reciprocalEdgeLinks"`
	ReachableBoards     int    `json:"reachableBoards"`
	// Degrees[n] counts boards with exactly n of their four edge exits wired.
	Degrees [5]int `json:"degrees"`
}

// WorldTopologyCorpus is the committed llmworld/topology.json.
type WorldTopologyCorpus struct {
	Version int             `json:"version"`
	Worlds  []WorldTopology `json:"worlds"`
}

// StylePriors is the mined artifact. Every number in it is a count or a share
// derived from the corpus; nothing here is authored by hand.
type StylePriors struct {
	Version      int                `json:"version"`
	Corpus       PriorsCorpusFacts  `json:"corpus"`
	Palette      PalettePriors      `json:"palette"`
	Architecture ArchitecturePriors `json:"architecture"`
	OOP          OOPPriors          `json:"oop"`
}

// PriorsCorpusFacts records what the priors were mined from, so a reader can
// tell how much evidence stands behind them.
type PriorsCorpusFacts struct {
	Boards         int `json:"boards"`
	Worlds         int `json:"worlds"`
	TopologyWorlds int `json:"topologyWorlds"`
	Cells          int `json:"cells"`
	LegendEntries  int `json:"legendEntries"`
	Stats          int `json:"stats"`
	OOPPrograms    int `json:"oopPrograms"`
	OOPLines       int `json:"oopLines"`
}

// ColorPrior is one DOS color attribute and how much of the corpus wears it.
type ColorPrior struct {
	Color    string `json:"color"`
	PerMille int    `json:"perMille"`
}

// TilePrior is one element's share of the painted (non-Empty) corpus, the
// fraction of boards that use it, and the colors it is usually painted in.
type TilePrior struct {
	Element       string       `json:"element"`
	PerMille      int          `json:"perMille"`
	BoardPerMille int          `json:"boardPerMille"`
	Colors        []ColorPrior `json:"colors"`
}

// PairPrior is an adjacent pairing of two different painted tiles — the
// mechanism behind corpus shading and texture. Sides are sorted so that a
// pairing is counted once regardless of which cell comes first.
type PairPrior struct {
	A     string `json:"a"`
	B     string `json:"b"`
	Count int    `json:"count"`
}

// LetteringPrior is one Text-* element's on-board lettering usage. A Text
// element's color byte is the CP437 character it draws, not a palette color, so
// BlankPerMille — the share of its cells whose byte is 0x20, a space — measures
// how much of the corpus uses it as a colored blank block rather than a glyph.
type LetteringPrior struct {
	Element       string `json:"element"`
	Cells         int    `json:"cells"`
	Boards        int    `json:"boards"`
	BlankPerMille int    `json:"blankPerMille"`
}

// PalettePriors is the mined palette/tile codebook.
type PalettePriors struct {
	FillPerMille    int              `json:"fillPerMille"`
	Tiles           []TilePrior      `json:"tiles"`
	Colors          []ColorPrior     `json:"colors"`
	ShadingPairs    []PairPrior      `json:"shadingPairs"`
	Lettering       []LetteringPrior `json:"lettering"`
	LetteringBoards int              `json:"letteringBoards"`
}

// ArchitecturePriors is the mined world-architecture/topology norms.
type ArchitecturePriors struct {
	BoardsP25             int    `json:"boardsP25"`
	BoardsMedian          int    `json:"boardsMedian"`
	BoardsP75             int    `json:"boardsP75"`
	BoardsMax             int    `json:"boardsMax"`
	DegreePerMille        [5]int `json:"degreePerMille"`
	ReciprocityPerMille   int    `json:"reciprocityPerMille"`
	DarkBoardPerMille     int    `json:"darkBoardPerMille"`
	ReachablePerMille     int    `json:"reachablePerMille"`
	HubWorldPerMille      int    `json:"hubWorldPerMille"`
	PassagesPerWorldMed   int    `json:"passagesPerWorldMedian"`
	ObjectsPerBoardPerTen int    `json:"objectsPerBoardPerTen"`
	StatsPerBoardPerTen   int    `json:"statsPerBoardPerTen"`
}

// CommandPrior is one ZZT-OOP command and how widely the corpus uses it.
type CommandPrior struct {
	Command         string `json:"command"`
	Count           int    `json:"count"`
	ProgramPerMille int    `json:"programPerMille"`
}

// SequencePrior is one consecutive command pair — the mined n-gram that shows
// how commands are actually chained (e.g. `#play` then `#give`).
type SequencePrior struct {
	First string `json:"first"`
	Then  string `json:"then"`
	Count int    `json:"count"`
}

// LabelPrior is one `:label` authors define, and how often.
type LabelPrior struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

// RitualPrior is a named recurring shape (the pickup, the gate, progressive
// dialogue) measured as the share of corpus programs that exhibit it.
type RitualPrior struct {
	Name     string `json:"name"`
	PerMille int    `json:"perMille"`
}

// OOPPriors is the mined ZZT-OOP idiom library.
type OOPPriors struct {
	LinesPerProgram int             `json:"linesPerProgramPerTen"`
	Commands        []CommandPrior  `json:"commands"`
	Sequences       []SequencePrior `json:"sequences"`
	Labels          []LabelPrior    `json:"labels"`
	Rituals         []RitualPrior   `json:"rituals"`
}

// CorpusBoard is one parsed authorable corpus board: the raw material the
// palette and OOP miners read. Parsing is deliberately strict — the corpus is
// decompiler output, so an unexpected shape means the corpus, not the parser,
// needs looking at.
type CorpusBoard struct {
	Name   string
	World  string
	Legend map[byte]LegendEntry
	Grid   []string
	Stats  int
	OOP    [][]string // one []string of program lines per scripted stat
}

// LegendEntry is one legend key's element and color, as written in ZWD.
type LegendEntry struct {
	Element string
	Color   string // "0x0F"
}

// ParseCorpusBoard parses one committed llmworld/examples/*.zwd board section.
func ParseCorpusBoard(name, src string) (CorpusBoard, error) {
	board := CorpusBoard{Name: name, World: name, Legend: map[byte]LegendEntry{}}
	if idx := strings.LastIndex(name, "_board"); idx > 0 {
		board.World = name[:idx]
	}
	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case line == "  grid":
			for i++; i < len(lines) && lines[i] != "  end"; i++ {
				board.Grid = append(board.Grid, lines[i])
			}
		case line == "  legend":
			for i++; i < len(lines) && lines[i] != "  end"; i++ {
				key, entry, err := parseLegendLine(lines[i])
				if err != nil {
					return CorpusBoard{}, fmt.Errorf("%s: %w", name, err)
				}
				board.Legend[key] = entry
			}
		case line == "  stats":
			for i++; i < len(lines) && lines[i] != "  end"; i++ {
				if strings.HasPrefix(lines[i], "    stat at ") {
					board.Stats++
					continue
				}
				if lines[i] == "    oop" {
					var program []string
					for i++; i < len(lines) && lines[i] != "    end"; i++ {
						program = append(program, lines[i])
					}
					board.OOP = append(board.OOP, program)
				}
			}
		}
	}
	if len(board.Grid) == 0 {
		return CorpusBoard{}, fmt.Errorf("%s: no grid rows", name)
	}
	if len(board.Legend) == 0 {
		return CorpusBoard{}, fmt.Errorf("%s: no legend entries", name)
	}
	return board, nil
}

// parseLegendLine reads `    K = Element color 0xHH`. The key is one CP437
// byte, written literally (and it may be a space, so it is taken positionally)
// or as `cp437:0xNN` when the byte is not printable — see ZWD.md.
func parseLegendLine(line string) (byte, LegendEntry, error) {
	if !strings.HasPrefix(line, "    ") {
		return 0, LegendEntry{}, fmt.Errorf("malformed legend line %q", line)
	}
	var key byte
	rest := line[4:]
	if hex := strings.TrimPrefix(rest, "cp437:0x"); len(hex) != len(rest) && len(hex) >= 5 && hex[2:5] == " = " {
		code, err := strconv.ParseUint(hex[:2], 16, 8)
		if err != nil {
			return 0, LegendEntry{}, fmt.Errorf("legend line %q has a malformed cp437 key", line)
		}
		key, rest = byte(code), hex[5:]
	} else {
		if len(rest) < 4 || rest[1:4] != " = " {
			return 0, LegendEntry{}, fmt.Errorf("malformed legend line %q", line)
		}
		key, rest = rest[0], rest[4:]
	}
	// A legend entry may carry trailing clauses after its color — a passage's
	// `to "Board"`, for instance — so the color is read at a fixed width rather
	// than as the rest of the line.
	idx := strings.Index(rest, " color 0x")
	if idx < 0 {
		return 0, LegendEntry{}, fmt.Errorf("legend line %q has no color", line)
	}
	color := rest[idx+len(" color 0x"):]
	if len(color) < 2 {
		return 0, LegendEntry{}, fmt.Errorf("legend line %q has a malformed color", line)
	}
	color = color[:2]
	if _, err := strconv.ParseUint(color, 16, 8); err != nil {
		return 0, LegendEntry{}, fmt.Errorf("legend line %q has a non-hex color", line)
	}
	return key, LegendEntry{Element: rest[:idx], Color: "0x" + strings.ToUpper(color)}, nil
}

// MineStylePriors is the deterministic miner: same corpus in, byte-identical
// artifact out, independent of input order. No LLM, no clock, no randomness.
func MineStylePriors(boards []CorpusBoard, topology WorldTopologyCorpus) StylePriors {
	ordered := append([]CorpusBoard(nil), boards...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })

	priors := StylePriors{Version: stylePriorsVersion}
	priors.Corpus.Boards = len(ordered)
	priors.Corpus.TopologyWorlds = len(topology.Worlds)
	worlds := map[string]bool{}
	for _, b := range ordered {
		worlds[b.World] = true
		priors.Corpus.LegendEntries += len(b.Legend)
		priors.Corpus.Stats += b.Stats
	}
	priors.Corpus.Worlds = len(worlds)

	minePalettePriors(ordered, &priors)
	mineOOPPriors(ordered, &priors)
	priors.Architecture = mineArchitecturePriors(topology)
	return priors
}

// tileKey identifies a painted cell as the model would have to author it: an
// element name plus its color attribute.
type tileKey struct {
	element string
	color   string
}

func (t tileKey) String() string { return t.element + " " + t.color }

func minePalettePriors(boards []CorpusBoard, priors *StylePriors) {
	elementCells := map[string]int{}
	elementBoards := map[string]int{}
	elementColors := map[string]map[string]int{}
	colorCells := map[string]int{}
	pairCounts := map[PairPrior]int{}
	letteringCells := map[string]int{}
	letteringBoards := map[string]int{}
	letteringBlanks := map[string]int{}
	painted, terrain, cells, boardsWithLettering := 0, 0, 0, 0

	for _, board := range boards {
		seen := map[string]bool{}
		seenLettering := map[string]bool{}
		// Resolve the grid once so adjacency and frequency read the same cells.
		grid := make([][]*tileKey, len(board.Grid))
		for y, row := range board.Grid {
			grid[y] = make([]*tileKey, len(row))
			for x := 0; x < len(row); x++ {
				cells++
				entry, ok := board.Legend[row[x]]
				if !ok || entry.Element == "Empty" {
					continue
				}
				painted++
				// A Text element's color byte is the glyph it draws, not a
				// palette color, so lettering is measured on its own terms and
				// kept out of the terrain palette rankings it would distort.
				if strings.HasPrefix(entry.Element, "Text-") {
					letteringCells[entry.Element]++
					if entry.Color == "0x20" {
						letteringBlanks[entry.Element]++
					}
					seenLettering[entry.Element] = true
					continue
				}
				terrain++
				key := tileKey{entry.Element, entry.Color}
				grid[y][x] = &key
				elementCells[entry.Element]++
				colorCells[entry.Color]++
				seen[entry.Element] = true
				if elementColors[entry.Element] == nil {
					elementColors[entry.Element] = map[string]int{}
				}
				elementColors[entry.Element][entry.Color]++
			}
		}
		for element := range seen {
			elementBoards[element]++
		}
		for element := range seenLettering {
			letteringBoards[element]++
		}
		if len(seenLettering) > 0 {
			boardsWithLettering++
		}
		countAdjacentPairs(grid, pairCounts)
	}

	priors.Corpus.Cells = cells
	priors.Palette.FillPerMille = perMille(painted, cells)
	priors.Palette.LetteringBoards = boardsWithLettering

	type elementCount struct {
		element string
		count   int
	}
	ranked := make([]elementCount, 0, len(elementCells))
	for element, count := range elementCells {
		ranked = append(ranked, elementCount{element, count})
	}
	sort.Slice(ranked, func(i, j int) bool {
		return rankedBefore(ranked[i].count, ranked[j].count, ranked[i].element, ranked[j].element)
	})
	for _, r := range ranked {
		if len(priors.Palette.Tiles) == priorsMaxTiles {
			break
		}
		priors.Palette.Tiles = append(priors.Palette.Tiles, TilePrior{
			Element:       r.element,
			PerMille:      perMille(r.count, terrain),
			BoardPerMille: perMille(elementBoards[r.element], len(boards)),
			Colors:        topColors(elementColors[r.element], r.count, priorsColorsPerTile),
		})
	}
	priors.Palette.Colors = topColors(colorCells, terrain, priorsMaxColors)

	pairs := make([]PairPrior, 0, len(pairCounts))
	for pair, count := range pairCounts {
		pair.Count = count
		pairs = append(pairs, pair)
	}
	sort.Slice(pairs, func(i, j int) bool {
		return rankedBefore(pairs[i].Count, pairs[j].Count, pairs[i].A+"|"+pairs[i].B, pairs[j].A+"|"+pairs[j].B)
	})
	if len(pairs) > priorsMaxPairs {
		pairs = pairs[:priorsMaxPairs]
	}
	priors.Palette.ShadingPairs = pairs

	lettering := make([]LetteringPrior, 0, len(letteringCells))
	for element, count := range letteringCells {
		lettering = append(lettering, LetteringPrior{
			Element:       element,
			Cells:         count,
			Boards:        letteringBoards[element],
			BlankPerMille: perMille(letteringBlanks[element], count),
		})
	}
	sort.Slice(lettering, func(i, j int) bool {
		return rankedBefore(lettering[i].Cells, lettering[j].Cells, lettering[i].Element, lettering[j].Element)
	})
	if len(lettering) > priorsMaxLettering {
		lettering = lettering[:priorsMaxLettering]
	}
	priors.Palette.Lettering = lettering
}

// countAdjacentPairs tallies every horizontal and vertical neighbouring pair of
// two *different* painted tiles: the corpus mechanism for shading, texture, and
// terrain mixing. Sides are sorted so the pairing is orientation-free.
func countAdjacentPairs(grid [][]*tileKey, counts map[PairPrior]int) {
	record := func(a, b *tileKey) {
		if a == nil || b == nil || *a == *b {
			return
		}
		left, right := a.String(), b.String()
		if right < left {
			left, right = right, left
		}
		counts[PairPrior{A: left, B: right}]++
	}
	for y := range grid {
		for x := range grid[y] {
			if x+1 < len(grid[y]) {
				record(grid[y][x], grid[y][x+1])
			}
			if y+1 < len(grid) && x < len(grid[y+1]) {
				record(grid[y][x], grid[y+1][x])
			}
		}
	}
}

func mineOOPPriors(boards []CorpusBoard, priors *StylePriors) {
	commandCounts := map[string]int{}
	commandPrograms := map[string]int{}
	sequenceCounts := map[SequencePrior]int{}
	labelCounts := map[string]int{}
	rituals := map[string]int{}
	programs, lines := 0, 0

	for _, board := range boards {
		for _, program := range board.OOP {
			programs++
			seen := map[string]bool{}
			var commands []string
			hasChoice, hasGoto := false, false
			for _, line := range program {
				lines++
				trimmed := strings.TrimRight(line, "\r")
				switch {
				case strings.HasPrefix(trimmed, "#"):
					command := oopCommandName(trimmed)
					if command == "" {
						continue
					}
					// A `#word` that is not a builtin is a message send, which
					// for the object's own labels is ZZT-OOP's goto. Counting
					// those beside real commands would teach the model that
					// `#b` is a command it may emit.
					if !oopBuiltinCommands[command] {
						hasGoto = true
						continue
					}
					commandCounts[command]++
					seen[command] = true
					commands = append(commands, command)
				case strings.HasPrefix(trimmed, ":"):
					labelCounts[oopLabelName(trimmed)]++
				case strings.HasPrefix(trimmed, "!"):
					hasChoice = true
				}
			}
			for command := range seen {
				commandPrograms[command]++
			}
			// Self-pairs (`#play` after `#play`) are the commonest consecutive
			// pair and teach nothing about chaining, so the n-gram skips them.
			for i := 0; i+1 < len(commands); i++ {
				if commands[i] != commands[i+1] {
					sequenceCounts[SequencePrior{First: commands[i], Then: commands[i+1]}]++
				}
			}
			countRituals(program, seen, hasChoice, hasGoto, rituals)
		}
	}

	priors.Corpus.OOPPrograms = programs
	priors.Corpus.OOPLines = lines
	if programs > 0 {
		priors.OOP.LinesPerProgram = lines * 10 / programs
	}

	commands := make([]CommandPrior, 0, len(commandCounts))
	for command, count := range commandCounts {
		commands = append(commands, CommandPrior{Command: command, Count: count, ProgramPerMille: perMille(commandPrograms[command], programs)})
	}
	sort.Slice(commands, func(i, j int) bool {
		return rankedBefore(commands[i].Count, commands[j].Count, commands[i].Command, commands[j].Command)
	})
	if len(commands) > priorsMaxCommands {
		commands = commands[:priorsMaxCommands]
	}
	priors.OOP.Commands = commands

	sequences := make([]SequencePrior, 0, len(sequenceCounts))
	for sequence, count := range sequenceCounts {
		sequence.Count = count
		sequences = append(sequences, sequence)
	}
	sort.Slice(sequences, func(i, j int) bool {
		return rankedBefore(sequences[i].Count, sequences[j].Count, sequences[i].First+"|"+sequences[i].Then, sequences[j].First+"|"+sequences[j].Then)
	})
	if len(sequences) > priorsMaxSequences {
		sequences = sequences[:priorsMaxSequences]
	}
	priors.OOP.Sequences = sequences

	labels := make([]LabelPrior, 0, len(labelCounts))
	for label, count := range labelCounts {
		labels = append(labels, LabelPrior{Label: label, Count: count})
	}
	sort.Slice(labels, func(i, j int) bool {
		return rankedBefore(labels[i].Count, labels[j].Count, labels[i].Label, labels[j].Label)
	})
	if len(labels) > priorsMaxLabels {
		labels = labels[:priorsMaxLabels]
	}
	priors.OOP.Labels = labels

	// Ritual order is fixed (not ranked) so the prompt reads the same way every
	// time regardless of which shapes happen to be commonest this corpus.
	for _, name := range ritualNames {
		priors.OOP.Rituals = append(priors.OOP.Rituals, RitualPrior{Name: name, PerMille: perMille(rituals[name], programs)})
	}
}

// ritualNames fixes both the set of probed rituals and their reported order.
var ritualNames = []string{
	":touch handler",
	"#play as the first act of a handler",
	"#give then #die pickup",
	"#zap progressive dialogue",
	"#if gate",
	"!label; choice menu",
	"#send to another object",
	"#label as a goto",
	"spoken flavor text",
}

// oopBuiltinCommands is the ZZT-OOP command vocabulary the interpreter actually
// dispatches on (the `e.OopWord == "..."` chain in OopExecute, oop.go). Every
// other `#word` falls through to OopSend and is a message, not a command.
var oopBuiltinCommands = map[string]bool{
	"#become": true, "#bind": true, "#change": true, "#char": true, "#clear": true,
	"#cycle": true, "#die": true, "#end": true, "#endgame": true, "#give": true,
	"#go": true, "#idle": true, "#if": true, "#lock": true, "#play": true,
	"#put": true, "#restart": true, "#restore": true, "#send": true, "#set": true,
	"#shoot": true, "#take": true, "#throwstar": true, "#try": true, "#unlock": true,
	"#walk": true, "#zap": true,
}

// countRituals probes one program for each named recurring shape. Every probe
// is a structural test over the program's own lines, so the resulting shares are
// measurements rather than assertions.
func countRituals(program []string, seen map[string]bool, hasChoice, hasGoto bool, rituals map[string]int) {
	mark := func(name string, ok bool) {
		if ok {
			rituals[name]++
		}
	}
	touch, playFirst, gave, diedAfterGive, spoken := false, false, false, false, false
	inHandler := false
	for _, line := range program {
		switch {
		case strings.HasPrefix(line, ":"):
			if strings.EqualFold(oopLabelName(line), ":touch") {
				touch = true
			}
			inHandler = true
		case strings.HasPrefix(line, "#"):
			command := oopCommandName(line)
			if inHandler && command == "#play" {
				playFirst = true
			}
			if command == "#give" {
				gave = true
			}
			if command == "#die" && gave {
				diedAfterGive = true
			}
			inHandler = false
		case line == "" || strings.HasPrefix(line, "@") || strings.HasPrefix(line, "!"):
			// Object name, blank, and choice lines do not end a handler's head.
		default:
			spoken = true
			inHandler = false
		}
	}
	mark(":touch handler", touch)
	mark("#play as the first act of a handler", playFirst)
	mark("#give then #die pickup", diedAfterGive)
	mark("#zap progressive dialogue", seen["#zap"])
	mark("#if gate", seen["#if"])
	mark("!label; choice menu", hasChoice)
	mark("#send to another object", seen["#send"])
	mark("#label as a goto", hasGoto)
	mark("spoken flavor text", spoken)
}

// oopCommandName normalizes `#give score 200` to `#give`. ZZT-OOP is
// case-insensitive, so commands are lowercased before counting.
func oopCommandName(line string) string {
	name := line
	if idx := strings.IndexAny(line, " \t"); idx > 0 {
		name = line[:idx]
	}
	name = strings.ToLower(strings.TrimRight(name, "\r"))
	if len(name) < 2 {
		return ""
	}
	// `#:message` and `#/n` style lines are movement/label sugar, not commands.
	for _, r := range name[1:] {
		if (r < 'a' || r > 'z') && r != '_' {
			return ""
		}
	}
	return name
}

func oopLabelName(line string) string {
	name := line
	if idx := strings.IndexAny(line, " \t"); idx > 0 {
		name = line[:idx]
	}
	return strings.ToLower(strings.TrimRight(name, "\r"))
}

func mineArchitecturePriors(topology WorldTopologyCorpus) ArchitecturePriors {
	priors := ArchitecturePriors{}
	if len(topology.Worlds) == 0 {
		return priors
	}
	boardCounts := make([]int, 0, len(topology.Worlds))
	passages := make([]int, 0, len(topology.Worlds))
	var degrees [5]int
	boards, dark, edges, reciprocal, reachable, objects, stats, hubWorlds := 0, 0, 0, 0, 0, 0, 0, 0
	for _, world := range topology.Worlds {
		boardCounts = append(boardCounts, world.Boards)
		passages = append(passages, world.Passages)
		boards += world.Boards
		dark += world.DarkBoards
		edges += world.EdgeLinks
		reciprocal += world.ReciprocalEdgeLinks
		reachable += world.ReachableBoards
		objects += world.Objects
		stats += world.Stats
		for degree, count := range world.Degrees {
			degrees[degree] += count
		}
		if world.Degrees[3]+world.Degrees[4] > 0 {
			hubWorlds++
		}
	}
	sort.Ints(boardCounts)
	sort.Ints(passages)
	priors.BoardsP25 = percentileInt(boardCounts, 25)
	priors.BoardsMedian = percentileInt(boardCounts, 50)
	priors.BoardsP75 = percentileInt(boardCounts, 75)
	priors.BoardsMax = boardCounts[len(boardCounts)-1]
	for degree := range degrees {
		priors.DegreePerMille[degree] = perMille(degrees[degree], boards)
	}
	priors.ReciprocityPerMille = perMille(reciprocal, edges)
	priors.DarkBoardPerMille = perMille(dark, boards)
	// Board 0 is a title screen nothing links to, so reachability is measured
	// against playable boards only — one title board per world.
	priors.ReachablePerMille = perMille(reachable, boards-len(topology.Worlds))
	priors.HubWorldPerMille = perMille(hubWorlds, len(topology.Worlds))
	priors.PassagesPerWorldMed = percentileInt(passages, 50)
	priors.ObjectsPerBoardPerTen = objects * 10 / boards
	priors.StatsPerBoardPerTen = stats * 10 / boards
	return priors
}

// topColors ranks one count table of colors: commonest first, with the color
// attribute as the tiebreak so equal counts never reorder between runs.
func topColors(counts map[string]int, total, limit int) []ColorPrior {
	colors := make([]string, 0, len(counts))
	for color := range counts {
		colors = append(colors, color)
	}
	sort.Slice(colors, func(i, j int) bool {
		if counts[colors[i]] != counts[colors[j]] {
			return counts[colors[i]] > counts[colors[j]]
		}
		return colors[i] < colors[j]
	})
	if len(colors) > limit {
		colors = colors[:limit]
	}
	ranked := make([]ColorPrior, 0, len(colors))
	for _, color := range colors {
		ranked = append(ranked, ColorPrior{Color: color, PerMille: perMille(counts[color], total)})
	}
	return ranked
}

// rankedBefore is the miners' single ranking rule: commonest first, name
// ascending as the tiebreak. Every mined list uses it, so no list can reorder
// between two runs over the same corpus.
func rankedBefore(counti, countj int, namei, namej string) bool {
	if counti != countj {
		return counti > countj
	}
	return namei < namej
}

func perMille(part, total int) int {
	if total <= 0 {
		return 0
	}
	return part * 1000 / total
}

// percentileInt returns the p-th percentile of an already-sorted slice using
// nearest-rank, which needs no interpolation and so cannot drift on a rebuild.
func percentileInt(sorted []int, p int) int {
	if len(sorted) == 0 {
		return 0
	}
	rank := (p*len(sorted) + 99) / 100
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

// MineStylePriorsFromCorpus mines the artifact from the committed corpus on
// disk: every llmworld/examples/*.zwd board plus llmworld/topology.json.
func MineStylePriorsFromCorpus(examplesDir, topologyPath string) (StylePriors, error) {
	entries, err := os.ReadDir(examplesDir)
	if err != nil {
		return StylePriors{}, fmt.Errorf("style priors: read corpus dir: %w", err)
	}
	var boards []CorpusBoard
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".zwd" {
			continue
		}
		src, err := os.ReadFile(filepath.Join(examplesDir, entry.Name()))
		if err != nil {
			return StylePriors{}, fmt.Errorf("style priors: read %s: %w", entry.Name(), err)
		}
		board, err := ParseCorpusBoard(strings.TrimSuffix(entry.Name(), ".zwd"), string(src))
		if err != nil {
			return StylePriors{}, fmt.Errorf("style priors: %w", err)
		}
		boards = append(boards, board)
	}
	if len(boards) == 0 {
		return StylePriors{}, fmt.Errorf("style priors: corpus %s has no boards", examplesDir)
	}
	topologyBytes, err := os.ReadFile(topologyPath)
	if err != nil {
		return StylePriors{}, fmt.Errorf("style priors: read topology: %w", err)
	}
	var topology WorldTopologyCorpus
	if err := json.Unmarshal(topologyBytes, &topology); err != nil {
		return StylePriors{}, fmt.Errorf("style priors: parse topology: %w", err)
	}
	if topology.Version != stylePriorsVersion {
		return StylePriors{}, fmt.Errorf("style priors: topology version %d, want %d", topology.Version, stylePriorsVersion)
	}
	if len(topology.Worlds) == 0 {
		return StylePriors{}, fmt.Errorf("style priors: topology %s has no worlds", topologyPath)
	}
	return MineStylePriors(boards, topology), nil
}

// marshalStableJSON renders an artifact the same way every time, so a rebuild
// of unchanged inputs produces a byte-identical committed file.
func marshalStableJSON(v interface{}) ([]byte, error) {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// PromptBlock renders the whole mined artifact as the compact, stable prompt
// section. It is deterministic and carries no per-request material, so it lives
// in the cached system prompt.
func (p StylePriors) PromptBlock() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Corpus-mined style priors (v%d)\n\n", p.Version)
	fmt.Fprintf(&b, "Measured offline from %d authorable boards (%d worlds) and the architecture of %d whole worlds. These are what real ZZT authors actually did — gravity, not law. Match the distribution; do not imitate any single number.\n",
		p.Corpus.Boards, p.Corpus.Worlds, p.Corpus.TopologyWorlds)

	b.WriteString("\n## Palette and texture\n\n")
	fmt.Fprintf(&b, "Boards paint %s of their cells; the rest is empty floor.\n", pct(p.Palette.FillPerMille))
	b.WriteString("Painted-cell share by element (with the colors it usually wears, and how many boards use it at all):\n")
	for _, tile := range p.Palette.Tiles {
		colors := make([]string, 0, len(tile.Colors))
		for _, c := range tile.Colors {
			colors = append(colors, c.Color)
		}
		fmt.Fprintf(&b, "- %s %s — %s, on %s of boards\n", tile.Element, strings.Join(colors, "/"), pct(tile.PerMille), pct(tile.BoardPerMille))
	}
	if len(p.Palette.Colors) > 0 {
		colors := make([]string, 0, len(p.Palette.Colors))
		for _, c := range p.Palette.Colors {
			colors = append(colors, fmt.Sprintf("%s %s", c.Color, pct(c.PerMille)))
		}
		fmt.Fprintf(&b, "Most-used color attributes: %s.\n", strings.Join(colors, ", "))
	}
	if len(p.Palette.ShadingPairs) > 0 {
		pairs := make([]string, 0, len(p.Palette.ShadingPairs))
		for _, pair := range p.Palette.ShadingPairs {
			pairs = append(pairs, fmt.Sprintf("%s + %s", pair.A, pair.B))
		}
		fmt.Fprintf(&b, "Adjacent pairings that make the corpus's shading and texture: %s.\n", strings.Join(pairs, "; "))
	}
	if len(p.Palette.Lettering) > 0 {
		lettering := make([]string, 0, len(p.Palette.Lettering))
		for _, l := range p.Palette.Lettering {
			lettering = append(lettering, fmt.Sprintf("%s (%d boards, %s of its cells a blank 0x20 block rather than a glyph)", l.Element, l.Boards, pct(l.BlankPerMille)))
		}
		fmt.Fprintf(&b, "Text elements — whose color byte is the CP437 glyph, not a color — appear on %s of boards: %s. Authors use them for lettering AND as flat color blocks.\n",
			pct(perMille(p.Palette.LetteringBoards, p.Corpus.Boards)), strings.Join(lettering, ", "))
	}

	b.WriteString("\n## World architecture\n\n")
	b.WriteString(p.architectureLines())

	b.WriteString("\n## ZZT-OOP idioms\n\n")
	fmt.Fprintf(&b, "Programs run about %d.%d lines. Commands by frequency (share of programs using each):\n",
		p.OOP.LinesPerProgram/10, p.OOP.LinesPerProgram%10)
	for _, command := range p.OOP.Commands {
		fmt.Fprintf(&b, "- %s — %d uses, in %s of programs\n", command.Command, command.Count, pct(command.ProgramPerMille))
	}
	if len(p.OOP.Sequences) > 0 {
		sequences := make([]string, 0, len(p.OOP.Sequences))
		for _, s := range p.OOP.Sequences {
			sequences = append(sequences, fmt.Sprintf("%s then %s", s.First, s.Then))
		}
		fmt.Fprintf(&b, "Commands are chained: %s.\n", strings.Join(sequences, "; "))
	}
	if len(p.OOP.Labels) > 0 {
		labels := make([]string, 0, len(p.OOP.Labels))
		for _, l := range p.OOP.Labels {
			labels = append(labels, fmt.Sprintf("%s (%d)", l.Label, l.Count))
		}
		fmt.Fprintf(&b, "Labels authors define: %s.\n", strings.Join(labels, ", "))
	}
	for _, ritual := range p.OOP.Rituals {
		fmt.Fprintf(&b, "- %s: %s of programs\n", ritual.Name, pct(ritual.PerMille))
	}
	return b.String()
}

// ArchitectureBlock is the topology-only slice of the priors, for the planner —
// the step that decides board count, hubs, and how the graph is wired.
func (p StylePriors) ArchitectureBlock() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Corpus-mined architecture priors (v%d)\n\nMeasured offline from %d real ZZT worlds. Match the distribution; do not copy any single world.\n\n",
		p.Version, p.Corpus.TopologyWorlds)
	b.WriteString(p.architectureLines())
	return b.String()
}

func (p StylePriors) architectureLines() string {
	a := p.Architecture
	var b strings.Builder
	fmt.Fprintf(&b, "- Worlds hold %d–%d boards (median %d, largest %d), title board included.\n", a.BoardsP25, a.BoardsP75, a.BoardsMedian, a.BoardsMax)
	fmt.Fprintf(&b, "- Edge exits per board: none %s, one %s, two %s, three %s, all four %s. Most boards are corridors or corners, not crossroads.\n",
		pct(a.DegreePerMille[0]), pct(a.DegreePerMille[1]), pct(a.DegreePerMille[2]), pct(a.DegreePerMille[3]), pct(a.DegreePerMille[4]))
	fmt.Fprintf(&b, "- %s of edge exits are reciprocal: walking back the way you came returns you to the board you left.\n", pct(a.ReciprocityPerMille))
	fmt.Fprintf(&b, "- %s of worlds have at least one three- or four-way hub board.\n", pct(a.HubWorldPerMille))
	fmt.Fprintf(&b, "- %s of boards are reachable from the starting board; the rest are cut off or editor scratch. Yours must all be reachable.\n", pct(a.ReachablePerMille))
	fmt.Fprintf(&b, "- A world carries a median of %d passages, %d.%d objects and %d.%d stats per board, and %s of boards are dark.\n",
		a.PassagesPerWorldMed, a.ObjectsPerBoardPerTen/10, a.ObjectsPerBoardPerTen%10, a.StatsPerBoardPerTen/10, a.StatsPerBoardPerTen%10, pct(a.DarkBoardPerMille))
	return b.String()
}

// pct renders a per-mille share as a percentage with one decimal, so the prompt
// reads naturally while the stored artifact keeps integer precision.
func pct(perMille int) string {
	return fmt.Sprintf("%d.%d%%", perMille/10, perMille%10)
}
