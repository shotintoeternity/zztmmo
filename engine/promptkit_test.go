package zztgo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestLoadPromptKit is the M12.3 DoD "prompt kit loads from Go": the kit reads
// its embedded assets and exposes the three ingredients.
func TestLoadPromptKit(t *testing.T) {
	kit, err := LoadPromptKit()
	if err != nil {
		t.Fatalf("LoadPromptKit: %v", err)
	}
	if len(kit.Spec) == 0 {
		t.Error("spec is empty")
	}
	if len(kit.Style) == 0 {
		t.Error("style is empty")
	}
	want := map[string]string{
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
	if len(kit.FewShots) != len(want) {
		t.Fatalf("want %d few-shots, got %d", len(want), len(kit.FewShots))
	}
	for _, fsx := range kit.FewShots {
		if fsx.ZWD == "" {
			t.Errorf("few-shot %s has empty ZWD", fsx.Name)
		}
		archetype, ok := want[fsx.Name]
		if !ok {
			t.Errorf("unexpected few-shot %s", fsx.Name)
			continue
		}
		if fsx.Archetype != archetype {
			t.Errorf("few-shot %s archetype = %q, want %q", fsx.Name, fsx.Archetype, archetype)
		}
		delete(want, fsx.Name)
		caption, ok := kit.Captions[fsx.Name]
		if !ok || caption.Summary == "" {
			t.Errorf("few-shot %s has no loaded caption", fsx.Name)
		}
	}
	if len(want) != 0 {
		t.Errorf("missing few-shots: %v", want)
	}
}

// TestPromptKitSystemPrompt checks the assembled prompt carries every ingredient
// and the output contract.
func TestPromptKitSystemPrompt(t *testing.T) {
	kit, err := LoadPromptKit()
	if err != nil {
		t.Fatal(err)
	}
	p := kit.SystemPrompt()

	// The limits table must appear verbatim (it lives inside spec.md).
	for _, want := range []string{
		"master ZZT world author",        // role preamble
		"# ZWD format specification",     // spec section
		"## Limits",                      // the M12.0 limits table heading
		"MAX_STAT = 150",                 // a specific limits row, verbatim
		"# House style",                  // style section
		"composed scenes, not tile soup", // a STYLE.md heading, verbatim
		"# Worked examples",              // few-shot section
		"# Output contract",              // contract
		"single fenced code block",       // contract rule
	} {
		if !strings.Contains(p, want) {
			t.Errorf("system prompt missing %q", want)
		}
	}
	// Every few-shot's name and body must be embedded in the prompt.
	for _, fsx := range kit.FewShots {
		if !strings.Contains(p, fsx.Name) {
			t.Errorf("system prompt missing few-shot name %q", fsx.Name)
		}
		if !strings.Contains(p, strings.TrimRight(fsx.ZWD, "\n")) {
			t.Errorf("system prompt missing few-shot body for %q", fsx.Name)
		}
		if !strings.Contains(p, kit.Captions[fsx.Name].Summary) {
			t.Errorf("system prompt missing visual caption for %q", fsx.Name)
		}
	}
}

// TestPromptKitAssetsMatchSource guards against drift: the embedded copies must
// stay byte-identical to their single source of truth, so editing ZWD.md or
// STYLE.md and forgetting to refresh the kit is caught in CI.
func TestPromptKitAssetsMatchSource(t *testing.T) {
	kit, err := LoadPromptKit()
	if err != nil {
		t.Fatal(err)
	}
	assertMatchesFile(t, "spec.md", kit.Spec, filepath.Join("..", "ZWD.md"))
	assertMatchesFile(t, "STYLE.md", kit.Style, filepath.Join("..", "llmworld", "STYLE.md"))
	for _, fsx := range kit.FewShots {
		src := filepath.Join("..", "llmworld", "examples", fsx.Name+".zwd")
		assertMatchesFile(t, "fewshots/"+fsx.Name+".zwd", fsx.ZWD, src)
		captionSrc := filepath.Join("..", "llmworld", "captions", fsx.Name+".json")
		embedded, err := promptKitFS.ReadFile("promptkit_assets/captions/" + fsx.Name + ".json")
		if err != nil {
			t.Fatalf("read embedded caption %s: %v", fsx.Name, err)
		}
		assertMatchesFile(t, "captions/"+fsx.Name+".json", string(embedded), captionSrc)
	}
}

func assertMatchesFile(t *testing.T, label, embedded, srcPath string) {
	t.Helper()
	want, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("read source %s: %v", srcPath, err)
	}
	if embedded != string(want) {
		t.Errorf("embedded %s has drifted from %s; re-copy the source into promptkit_assets/", label, srcPath)
	}
}

// TestPromptKitFewShotsCompile ensures no embedded few-shot teaches the model
// invalid ZWD: each board section compiles when wrapped as a one-board world
// (its cross-board exit references neutralized, since a fragment names boards
// that do not exist in isolation).
func TestPromptKitFewShotsCompile(t *testing.T) {
	kit, err := LoadPromptKit()
	if err != nil {
		t.Fatal(err)
	}
	exitRe := regexp.MustCompile(`(?m)^\s*exits .*$`)
	for _, fsx := range kit.FewShots {
		section := exitRe.ReplaceAllString(fsx.ZWD, "  exits north none south none west none east none")
		doc := "zwd 1\nworld \"FEWSHOT\"\n\n" + section
		if _, err := CompileZWD(doc); err != nil {
			t.Errorf("few-shot %s does not compile: %v", fsx.Name, err)
		}
	}
}
