package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestTownTitleGolden(t *testing.T) {
	// Read the committed fixture (byte-identical to any engine-dir copy), not an
	// untracked world, so the golden runs and fails closed in a clean clone
	// (task M16.1).
	world := filepath.Join("..", "..", "..", "fixtures", "TOWN.ZZT")
	if _, err := os.Stat(world); err != nil {
		t.Fatalf("required fixture %s is missing: %v (it is committed; do not skip past it)", world, err)
	}
	out := filepath.Join(t.TempDir(), "town-title.png")
	if err := shot(world, out, 0); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	got := sha256.Sum256(b)
	const want = "4746ad6f9276c665f100d5d4587e84931b3aa32b93e26106020cbb7637474276"
	if actual := fmt.Sprintf("%x", got); actual != want {
		t.Fatalf("TOWN title PNG hash = %s, want %s", actual, want)
	}
}
