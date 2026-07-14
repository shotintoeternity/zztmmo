package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestTownTitleGolden(t *testing.T) {
	world := filepath.Join("..", "..", "TOWN.ZZT")
	if _, err := os.Stat(world); err != nil {
		t.Skipf("TOWN fixture unavailable: %v", err)
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
