package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TOWN loads and simulates correctly but its static title screen does not
// dirty a board cell during ordinary GameSteps. This guards the explicit final
// render that makes the standalone validator agree with the generation gate.
func TestValidateRendersStaticTown(t *testing.T) {
	// The git-tracked fixture, so the test runs on a clean clone (CI). The
	// stat guard must come first: validate on a missing file reaches
	// DisplayIOError's modal window, which blocks forever headless.
	dir := filepath.Join("..", "..", "..", "fixtures")
	if _, err := os.Stat(filepath.Join(dir, "TOWN.ZZT")); err != nil {
		t.Skipf("fixtures/TOWN.ZZT unavailable")
	}
	ok, reason := validate("TOWN", dir, 200)
	if !ok {
		t.Fatalf("validate(TOWN) = false: %s", reason)
	}
}
