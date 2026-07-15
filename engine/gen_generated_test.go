package zztgo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGeneratedZWDWorldsCompileAndValidate compiles every ZWD world in
// ../llmworld/generated/ and runs the M7.5-style gate (load + 200 headless
// steps). These are LLM/hand-authored worlds — the forward direction of the
// pipeline, where the decompiled corpus in ../llmworld/examples/ is the
// reverse. Each compiled world is also written as <NAME>.ZZT into the engine
// directory (gitignored) so the server's world picker can host it.
func TestGeneratedZWDWorldsCompileAndValidate(t *testing.T) {
	paths, _ := filepath.Glob(filepath.Join("..", "llmworld", "generated", "*.zwd"))
	if len(paths) == 0 {
		t.Fatal("no generated worlds under llmworld/generated — these are committed and required")
	}

	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".zwd")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			data, err := CompileZWD(string(src))
			if err != nil {
				t.Fatalf("CompileZWD: %v", err)
			}
			validateCompiledZWD(t, data)

			// Compiling and validating the committed ZWD world is the required
			// check. Writing the .ZZT so the server's world picker can host it
			// is a maintainer side effect, gated behind the regen flag so the
			// required path performs no auto-write (task M16.1).
			if parityRegen() {
				outPath := strings.ToUpper(name) + ".ZZT"
				if err := os.WriteFile(outPath, data, 0644); err != nil {
					t.Fatalf("write %s: %v", outPath, err)
				}
				t.Logf("%s → %s (%d bytes, %s set)", path, outPath, len(data), parityRegenEnv)
			}
		})
	}
}
