package zztgo

// M16.3 — the oracle micro-worlds are authored as ZWD and compiled ONCE; the
// committed .ZZT bytes are the pinned input to both the real ZZT.EXE (via
// `make oracle-regen`) and this engine's tests. This lock proves the committed
// bytes still correspond to their .zwd source, so neither can drift silently:
// editing a .zwd without re-pinning the .ZZT (or changing the ZWD compiler in a
// way that alters output) reddens the build. Re-pinning is an explicit
// maintainer act (ZZT_PARITY_REGEN=1), after which `make oracle-regen` must be
// run so the captures are re-recorded against the new world bytes.

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestOracleWorldsMatchZWDSources(t *testing.T) {
	dir := filepath.Join("..", "fixtures", "oracle")
	zwds, err := filepath.Glob(filepath.Join(dir, "*.zwd"))
	if err != nil || len(zwds) == 0 {
		t.Fatalf("no oracle .zwd sources found: %v", err)
	}
	sort.Strings(zwds)
	for _, zwdPath := range zwds {
		base := zwdPath[:len(zwdPath)-len(".zwd")]
		src, err := os.ReadFile(zwdPath)
		if err != nil {
			t.Fatalf("read %s: %v", zwdPath, err)
		}
		compiled, err := CompileZWD(string(src))
		if err != nil {
			t.Fatalf("%s does not compile: %v", zwdPath, err)
		}
		zztPath := base + ".ZZT"
		committed, err := os.ReadFile(zztPath)
		if os.IsNotExist(err) {
			if parityRegen() {
				if err := os.WriteFile(zztPath, compiled, 0o644); err != nil {
					t.Fatalf("write %s: %v", zztPath, err)
				}
				t.Logf("compiled %s → %s (%d bytes, %s set)", zwdPath, zztPath, len(compiled), parityRegenEnv)
				continue
			}
			t.Fatalf("oracle world %s is missing; compile once with %s=1, then run `make oracle-regen`", zztPath, parityRegenEnv)
		}
		if err != nil {
			t.Fatalf("read %s: %v", zztPath, err)
		}
		if !bytes.Equal(compiled, committed) {
			t.Errorf("%s no longer matches its committed %s (source or ZWD compiler drifted); re-pin with %s=1 and re-run `make oracle-regen`",
				zwdPath, zztPath, parityRegenEnv)
		}
	}
}
