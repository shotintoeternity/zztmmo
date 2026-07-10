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
		t.Skip("no generated worlds")
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

			outPath := strings.ToUpper(name) + ".ZZT"
			if err := os.WriteFile(outPath, data, 0644); err != nil {
				t.Fatalf("write %s: %v", outPath, err)
			}
			t.Logf("%s → %s (%d bytes)", path, outPath, len(data))
		})
	}
}
