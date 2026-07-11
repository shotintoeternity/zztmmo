package zztgo

import (
	"embed"
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
var promptKitFS embed.FS

// fewShotArchetypes labels each embedded few-shot by the board archetype it
// demonstrates. CUTLASS_board27 and SEWERS_board17 are the M12.3 spec's own
// action-arena and texture-showcase picks; DUNGEONS_board20 (a framed cavern
// interior) and RAEKUUL_board1 (text lettering + playful #zap dialogue) replace
// the spec's ONAMOON/OBELISK candidates, which carry decompiler artifacts
// (raw `element 33` legend entries, an off-board `respawn 98,98`) that the
// compiler rejects — a few-shot must itself be valid ZWD or it teaches the
// model invalid tokens. See NOTES.md.
var fewShotArchetypes = map[string]string{
	"CUTLASS_board27":  "action arena",
	"SEWERS_board17":   "texture showcase",
	"DUNGEONS_board20": "interior scene",
	"RAEKUUL_board1":   "story board",
}

// FewShot is one embedded example board section, shown to the model as a style
// reference for its archetype.
type FewShot struct {
	Name      string // corpus filename stem, e.g. "CUTLASS_board27"
	Archetype string // "action arena", "interior scene", ...
	ZWD       string // the board section text
}

// PromptKit holds the assembled generation ingredients.
type PromptKit struct {
	Spec     string // ZWD format grammar + limits table (spec.md)
	Style    string // STYLE.md
	FewShots []FewShot
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
	kit := &PromptKit{Spec: string(spec), Style: string(style)}
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
	return kit, nil
}

// SystemPrompt assembles the full generation system prompt. It is identical
// across every board call in a world (M12.4 relies on that for prompt caching):
// the per-request specifics — the premise, the world plan, and the edge rows of
// adjacent boards — are the caller's user message, not this system prompt.
func (k *PromptKit) SystemPrompt() string {
	var b strings.Builder
	b.WriteString(promptRolePreamble)
	b.WriteString("\n\n# ZWD format specification\n\n")
	b.WriteString("Everything below, including the Limits table, is authoritative. Emitted ZWD that violates it will not compile.\n\n")
	b.WriteString(k.Spec)
	b.WriteString("\n\n# House style\n\n")
	b.WriteString("How good ZZT boards actually look and read. Follow these idioms; they are what separates a composed scene from tile soup.\n\n")
	b.WriteString(k.Style)
	b.WriteString("\n\n# Worked examples\n\n")
	b.WriteString("Real boards decompiled from shipped games, one per archetype. Study their framing, shading, legend density, and OOP voice — then write your own scene, do not copy these.\n")
	for _, fsx := range k.FewShots {
		fmt.Fprintf(&b, "\n## Example — %s (`%s`)\n\n```zwd\n%s\n```\n", fsx.Archetype, fsx.Name, strings.TrimRight(fsx.ZWD, "\n"))
	}
	b.WriteString("\n")
	b.WriteString(promptOutputContract)
	return b.String()
}

const promptRolePreamble = `You are a master ZZT world author. ZZT is the 1991 DOS creation kit; a world is
a set of 60x25 text-mode boards painted from CP437 glyphs and DOS color
attributes, populated with creatures, items, and scripted objects. You write in
ZWD ("ZZT World Description"), a plain-text format that a compiler turns into a
real .ZZT file. Your job: given a premise (and, in the full pipeline, a world
plan plus the edge rows of already-painted neighbor boards), paint boards that
are visually composed, playable, and stylistically in the ZZT tradition.`

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
- Literal Text Strings: Avoid writing readable English text strings directly inside the grid rows (e.g. ` + "`Press P to begin`" + ` or ` + "`The gate is locked`" + `) unless you define every single letter key in the legend. Instead, prefer to use interactive **Objects** that display these messages in their OOP code on touch, bump, or enter. This is cleaner and is the idiomatic ZZT way.
- Text Windows: Pre-wrap OOP dialogue at word boundaries: ordinary lines are at most 42 characters, centered ` + "`$`" + ` lines at most 45 characters after the marker, and ` + "`!label;`" + ` choice captions at most 38 characters after the semicolon. Keep ` + "`@`" + ` titles at most 45 characters. The compiler is a safety net, not a substitute for intentional pacing.
- Stay within the Limits table: <=150 stats per board, <=100 non-title boards,
  60x25 grids, colors 0..15 per nibble.
- Prefer the house-style idioms: a framed playfield, one idea per board,
  gray-family shading, fake-wall floors, monumental lettering used sparingly,
  and the short, wry, second-person OOP voice with ` + "`#play`" + ` punctuation.`
