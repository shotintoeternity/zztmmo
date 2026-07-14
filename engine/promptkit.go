package zztgo

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// Generation prompt kit (M12.3). Assembles the system prompt that instructs a
// real LLM to author ZZT boards/worlds as ZWD, from three embedded ingredients:
//
//   - spec.md   — the ZWD format grammar AND the M12.0 limits table, verbatim
//                 (a byte-for-byte copy of the repo-root ZWD.md; a drift test
//                 in promptkit_test.go asserts they stay identical). STYLE.md is
//                 idiom-only and teaches no syntax, so the format spec is a
//                 required ingredient for the model to emit compilable ZWD.
//   - STYLE.md  — the distilled corpus idiom analysis (composition, shading,
//                 lettering, the OOP rituals), copied from llmworld/STYLE.md.
//   - few-shots — a hand-picked archetype spread of real decompiled boards.
//
// Nothing here calls an LLM or touches simulation state; it produces a string.
// The kit is embedded (not read from disk) so the M12.4 server carries its own
// prompt: llmworld/ and ZWD.md live outside this Go module and cannot be
// reached with go:embed, so promptkit_assets/ holds committed copies.

//go:embed promptkit_assets/spec.md
//go:embed promptkit_assets/STYLE.md
//go:embed promptkit_assets/fewshots/*.zwd
//go:embed promptkit_assets/captions/*.json
//go:embed promptkit_assets/fewshot_metadata.json
var promptKitFS embed.FS

// fewShotArchetypes labels each embedded few-shot by the board archetype it
// demonstrates. The original M12.3 action/interior/story spread remains, and
// M12.15a adds owner-curated title lettering, pictorial art, and playable-scene
// examples. Every entry is independently decompiled, link-neutralized, compiled,
// headlessly validated, and pixel-compared before it may enter this map.
var fewShotArchetypes = map[string]string{
	"CUTLASS_board27":                  "action arena",
	"SEWERS_board17":                   "texture showcase",
	"DUNGEONS_board20":                 "interior scene",
	"RAEKUUL_board1":                   "story board",
	"winter_board0":                    "title lettering — icy monumental",
	"scorchede_board0":                 "title lettering — rough block",
	"sudoku_board0":                    "title lettering — geometric",
	"zztv7_board0":                     "title lettering — neon abstract",
	"variety_board0":                   "title lettering — rainbow wordmark",
	"nyan_board0":                      "title art — cartoon pictorial",
	"rhygar2_arogans_range_1_board0":   "gameplay scene — sunset landscape",
	"gh2se0_ap_edge_se_board0":         "gameplay scene — road and trees",
	"gh2se0_mcqueen_heights_ne_board0": "gameplay scene — town architecture",
}

// FewShot is one embedded example board section, shown to the model as a style
// reference for its archetype.
type FewShot struct {
	Name      string // corpus filename stem, e.g. "CUTLASS_board27"
	Archetype string // "action arena", "interior scene", ...
	ZWD       string // the board section text
}

// BoardCaption is an offline, structured visual label for a few-shot. Summary
// is deliberately compact because it sits beside the board source in prompts;
// the other fields remain available for M12.15c's deterministic retrieval.
type BoardCaption struct {
	Title        string   `json:"title"`
	Archetype    string   `json:"archetype"`
	Technique    string   `json:"technique"`
	Palette      []string `json:"palette"`
	Composition  string   `json:"composition"`
	PictorialArt string   `json:"pictorial_art"`
	Quality      string   `json:"quality"`
	Summary      string   `json:"summary"`
}

// FewShotMetadata is the deliberately small, offline retrieval index for an
// authorable few-shot.  It is authored from the corpus (rather than inferred by
// a model at generation time), so selection is reproducible and cannot spend a
// request on classification.  Keywords are normalized by retrievalTerms.
type FewShotMetadata struct {
	Name      string   `json:"name"`
	World     string   `json:"world"`
	Archetype string   `json:"archetype"`
	Themes    []string `json:"themes"`
	Palette   []string `json:"palette"`
	Density   string   `json:"density"`
	Cohesion  string   `json:"cohesion,omitempty"`
}

// retrievalBudget is intentionally a source-byte budget rather than a token
// estimate: it is deterministic across tokenizer/model revisions and gives a
// hard upper bound on request growth.  Three examples keep the prompt focused.
const (
	retrievalMaxExamples = 3
	retrievalMaxBytes    = 24000
)

// PromptKit holds the assembled generation ingredients.
type PromptKit struct {
	Spec     string // ZWD format grammar + limits table (spec.md)
	Style    string // STYLE.md
	FewShots []FewShot
	Captions map[string]BoardCaption    // keyed by FewShot.Name
	Metadata map[string]FewShotMetadata // keyed by FewShot.Name
}

// LoadPromptKit reads the embedded assets into a PromptKit. It errors rather
// than returning a half-built kit so a misconfigured build fails loudly.
func LoadPromptKit() (*PromptKit, error) {
	spec, err := promptKitFS.ReadFile("promptkit_assets/spec.md")
	if err != nil {
		return nil, fmt.Errorf("promptkit: read spec.md: %w", err)
	}
	style, err := promptKitFS.ReadFile("promptkit_assets/STYLE.md")
	if err != nil {
		return nil, fmt.Errorf("promptkit: read STYLE.md: %w", err)
	}
	entries, err := fs.ReadDir(promptKitFS, "promptkit_assets/fewshots")
	if err != nil {
		return nil, fmt.Errorf("promptkit: read fewshots dir: %w", err)
	}
	kit := &PromptKit{Spec: string(spec), Style: string(style), Captions: map[string]BoardCaption{}, Metadata: map[string]FewShotMetadata{}}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".zwd") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".zwd")
		arch, ok := fewShotArchetypes[name]
		if !ok {
			return nil, fmt.Errorf("promptkit: few-shot %q has no archetype label", name)
		}
		b, err := promptKitFS.ReadFile("promptkit_assets/fewshots/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("promptkit: read few-shot %s: %w", e.Name(), err)
		}
		kit.FewShots = append(kit.FewShots, FewShot{Name: name, Archetype: arch, ZWD: string(b)})
	}
	if len(kit.FewShots) == 0 {
		return nil, fmt.Errorf("promptkit: no few-shots embedded")
	}
	// Stable order (embed.FS is already sorted, but make it explicit): group by
	// archetype spread in a deterministic sequence for prompt caching.
	sort.Slice(kit.FewShots, func(i, j int) bool { return kit.FewShots[i].Name < kit.FewShots[j].Name })
	for _, fsx := range kit.FewShots {
		b, err := promptKitFS.ReadFile("promptkit_assets/captions/" + fsx.Name + ".json")
		if err != nil {
			return nil, fmt.Errorf("promptkit: read caption for %s: %w", fsx.Name, err)
		}
		var caption BoardCaption
		if err := json.Unmarshal(b, &caption); err != nil {
			return nil, fmt.Errorf("promptkit: parse caption for %s: %w", fsx.Name, err)
		}
		if caption.Title == "" || caption.Archetype == "" || caption.Technique == "" ||
			len(caption.Palette) == 0 || caption.Composition == "" || caption.Quality == "" || caption.Summary == "" {
			return nil, fmt.Errorf("promptkit: caption for %s is incomplete", fsx.Name)
		}
		kit.Captions[fsx.Name] = caption
	}
	metadataBytes, err := promptKitFS.ReadFile("promptkit_assets/fewshot_metadata.json")
	if err != nil {
		return nil, fmt.Errorf("promptkit: read few-shot metadata: %w", err)
	}
	var metadata []FewShotMetadata
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
		return nil, fmt.Errorf("promptkit: parse few-shot metadata: %w", err)
	}
	for _, m := range metadata {
		if m.Name == "" || m.World == "" || m.Archetype == "" || len(m.Themes) == 0 || len(m.Palette) == 0 || m.Density == "" {
			return nil, fmt.Errorf("promptkit: metadata for %q is incomplete", m.Name)
		}
		if _, duplicate := kit.Metadata[m.Name]; duplicate {
			return nil, fmt.Errorf("promptkit: duplicate metadata for %q", m.Name)
		}
		kit.Metadata[m.Name] = m
	}
	for _, fsx := range kit.FewShots {
		if _, ok := kit.Metadata[fsx.Name]; !ok {
			return nil, fmt.Errorf("promptkit: few-shot %q has no retrieval metadata", fsx.Name)
		}
	}
	for name := range kit.Metadata {
		if _, ok := fewShotArchetypes[name]; !ok {
			return nil, fmt.Errorf("promptkit: metadata for unknown few-shot %q", name)
		}
	}
	return kit, nil
}

// SystemPrompt assembles the stable, cacheable generation system prompt.  The
// retrieved examples deliberately do not live here: only a small ordered subset
// varies per premise/board concept, and it is supplied in the user request.
func (k *PromptKit) SystemPrompt() string {
	var b strings.Builder
	b.WriteString(promptRolePreamble)
	b.WriteString("\n\n# ZWD format specification\n\n")
	b.WriteString("Everything below, including the Limits table, is authoritative. Emitted ZWD that violates it will not compile.\n\n")
	b.WriteString(k.Spec)
	b.WriteString("\n\n# House style\n\n")
	b.WriteString("How good ZZT boards actually look and read. Follow these idioms; they are what separates a composed scene from tile soup.\n\n")
	b.WriteString(k.Style)
	b.WriteString("\n")
	b.WriteString(promptOutputContract)
	return b.String()
}

// RetrievalContext returns the bounded retrieval-augmented part of a request.
// No network or model is involved: the same inputs always produce byte-identical
// output, including ties (which sort by corpus name).
func (k *PromptKit) RetrievalContext(premise, boardConcept string) string {
	type ranked struct {
		shot     FewShot
		metadata FewShotMetadata
		score    int
	}
	terms := retrievalTerms(premise + " " + boardConcept)
	rankedShots := make([]ranked, 0, len(k.FewShots))
	for _, shot := range k.FewShots {
		m := k.Metadata[shot.Name]
		score := retrievalScore(terms, m, k.Captions[shot.Name])
		rankedShots = append(rankedShots, ranked{shot, m, score})
	}
	sort.Slice(rankedShots, func(i, j int) bool {
		if rankedShots[i].score != rankedShots[j].score {
			return rankedShots[i].score > rankedShots[j].score
		}
		return rankedShots[i].shot.Name < rankedShots[j].shot.Name
	})
	var b strings.Builder
	b.WriteString("# Retrieved corpus examples\n\nThese are offline, authorable reference boards selected for this premise and board concept. Study them; do not copy them.\n")
	used := 0
	for _, r := range rankedShots {
		if used == retrievalMaxExamples {
			break
		}
		caption := k.Captions[r.shot.Name]
		entry := fmt.Sprintf("\n## Example — %s (`%s`)\n\nTags: %s; palette: %s; density: %s.\nVisual note: %s\n\n```zwd\n%s\n```\n", r.metadata.Archetype, r.shot.Name, strings.Join(r.metadata.Themes, ", "), strings.Join(r.metadata.Palette, ", "), r.metadata.Density, caption.Summary, strings.TrimRight(r.shot.ZWD, "\n"))
		if b.Len()+len(entry) > retrievalMaxBytes {
			continue
		}
		b.WriteString(entry)
		if r.metadata.Cohesion != "" {
			fmt.Fprintf(&b, "\nSame-world plan excerpt: %s\n", r.metadata.Cohesion)
		}
		used++
	}
	return b.String()
}

// TitleRetrievalContext is RetrievalContext specialized for the title board
// (board 0): it always places the curated title-lettering/art examples first so
// the model paints from clean monumental wordmarks instead of whatever gameplay
// board happens to rank highest, then fills any remaining slots with
// premise-ranked shots. Deterministic, like RetrievalContext.
func (k *PromptKit) TitleRetrievalContext(premise string) string {
	terms := retrievalTerms(premise)
	type ranked struct {
		shot     FewShot
		metadata FewShotMetadata
		score    int
	}
	var titleShots, otherShots []ranked
	for _, shot := range k.FewShots {
		m := k.Metadata[shot.Name]
		r := ranked{shot, m, retrievalScore(terms, m, k.Captions[shot.Name])}
		if strings.HasPrefix(fewShotArchetypes[shot.Name], "title") {
			titleShots = append(titleShots, r)
		} else {
			otherShots = append(otherShots, r)
		}
	}
	byScoreThenName := func(s []ranked) {
		sort.Slice(s, func(i, j int) bool {
			if s[i].score != s[j].score {
				return s[i].score > s[j].score
			}
			return s[i].shot.Name < s[j].shot.Name
		})
	}
	byScoreThenName(titleShots)
	byScoreThenName(otherShots)
	ordered := append(titleShots, otherShots...)

	var b strings.Builder
	b.WriteString("# Retrieved corpus examples\n\nThese are offline, authorable reference boards for a title screen; the title-lettering examples come first — study their monumental wordmarks. Do not copy them.\n")
	used := 0
	for _, r := range ordered {
		if used == retrievalMaxExamples {
			break
		}
		caption := k.Captions[r.shot.Name]
		entry := fmt.Sprintf("\n## Example — %s (`%s`)\n\nTags: %s; palette: %s; density: %s.\nVisual note: %s\n\n```zwd\n%s\n```\n", r.metadata.Archetype, r.shot.Name, strings.Join(r.metadata.Themes, ", "), strings.Join(r.metadata.Palette, ", "), r.metadata.Density, caption.Summary, strings.TrimRight(r.shot.ZWD, "\n"))
		if b.Len()+len(entry) > retrievalMaxBytes {
			continue
		}
		b.WriteString(entry)
		if r.metadata.Cohesion != "" {
			fmt.Fprintf(&b, "\nSame-world plan excerpt: %s\n", r.metadata.Cohesion)
		}
		used++
	}
	return b.String()
}

func retrievalTerms(s string) map[string]bool {
	terms := make(map[string]bool)
	for _, term := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return r < 'a' || r > 'z' }) {
		if len(term) > 1 {
			terms[term] = true
		}
	}
	return terms
}

func retrievalScore(terms map[string]bool, m FewShotMetadata, caption BoardCaption) int {
	score := 0
	for _, field := range append(append([]string{m.Archetype, m.Density, caption.Title, caption.Archetype, caption.Technique, caption.PictorialArt}, m.Themes...), m.Palette...) {
		for term := range retrievalTerms(field) {
			if terms[term] {
				score++
			}
		}
	}
	return score
}

const promptRolePreamble = `You are a master ZZT world author. ZZT is the 1991 DOS creation kit; a world is
a set of 60x25 text-mode boards painted from CP437 glyphs and DOS color
attributes, populated with creatures, items, and scripted objects. You write in
ZWD ("ZZT World Description"), a plain-text format that a compiler turns into a
real .ZZT file. Your job: given a premise (and, in the full pipeline, a world
plan plus the edge rows of already-painted neighbor boards), paint boards that
are visually composed, playable, and stylistically in the ZZT tradition.

Use ZZT-OOP fluently: objects, messages, labels, choices, flags, movement,
items, passages, and sound are narrative and gameplay tools, not decoration.
Write original dialogue, narration, signs, and scroll text as well as you can;
do not reduce it to templates or terse filler. Favor the dry, absurdist,
warmly observant Douglas Adams-style wit common in memorable ZZT worlds, while
keeping the voice specific to the world and its characters.`

const promptOutputContract = `# Output contract

- Emit ONLY a single fenced code block tagged ` + "`zwd`" + `. No prose before or
  after it.
- A complete world starts with a ` + "`zwd 1`" + ` line and a ` + "`world \"NAME\"`" + ` line,
  then one or more ` + "`board`" + ` sections. When you are asked to paint one board,
  emit just that board section.

- Grid Alignment Protocol: To ensure every grid row is exactly 60 characters and prevent column shifting, you MUST wrap your grid rows with leading and trailing pipe characters ('|') at columns 1 and 62, and prepend/append a 60-character numbered ruler at the top and bottom of the grid. Every row must align perfectly with the ruler. Example:
  grid
  |123456789012345678901234567890123456789012345678901234567890|
  |############################################################|
  |#..........................................................#|
  |123456789012345678901234567890123456789012345678901234567890|
  end
- Grid Run-Length Encoding (RLE) Support: To ensure mathematical certainty of your grid row lengths, you can use RLE syntax: ` + "`char*count`" + ` (for example, ` + "`.*58`" + ` expands to 58 empty dots, and ` + "`#*60`" + ` expands to 60 solid walls). This is highly recommended to prevent column shifting. Example:
  grid
  |123456789012345678901234567890123456789012345678901234567890|
  |#*60|
  |#.*58#|
  |123456789012345678901234567890123456789012345678901234567890|
  end
- Every board has exactly one ` + "`start player`" + `. Board 0 is the title screen.
- Exit targets and passage ` + "`board`" + ` fields name other boards by their exact
  ` + "`board \"NAME\"`" + ` string. Do not reference a board you have not defined
  (in a full-world emission).
- Every legend character used in the grid must have a legend entry, and every
  entry must be a valid element name from the spec with a two-hex-digit color.
  Never invent element names or ` + "`element <number>`" + ` entries.
- Literal Text Strings: EVERY character in EVERY grid row MUST have a legend entry — no exceptions. Never type prose, chat, words, or sentences straight into the grid; the compiler rejects any grid char that is not a legend key, one char per compile, so undefined prose is the single most common cause of generation failure. To show text, lettering, or a message ON a board, add a legend entry mapping each letter to a ` + "`Text-<Color>`" + ` element whose ` + "`color`" + ` value is the CP437 code of that letter (e.g. ` + "`cp437:0x48 = Text-White color 0x48`" + ` renders an on-board 'H'). Strongly prefer putting dialogue and messages in an interactive **Object's** scroll text (the ` + "`#`" + `/` + "`$`" + ` lines of its OOP) rather than drawing them on the board — this is cleaner and the idiomatic ZZT way.
- Text Windows: Pre-wrap OOP dialogue at word boundaries: ordinary lines are at most 42 characters, centered ` + "`$`" + ` lines at most 45 characters after the marker, and ` + "`!label;`" + ` choice captions at most 38 characters after the semicolon. Keep ` + "`@`" + ` titles at most 45 characters. The compiler is a safety net, not a substitute for intentional pacing.
- Stay within the Limits table: <=150 stats per board, <=100 non-title boards,
  60x25 grids, colors 0..15 per nibble.
- Prefer the house-style idioms: a framed playfield, one idea per board,
  gray-family shading, fake-wall floors, monumental lettering used sparingly,
  and the short, wry, second-person OOP voice with ` + "`#play`" + ` punctuation.`
