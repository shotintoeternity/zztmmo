package main

import (
	"path/filepath"
	"testing"
)

// TOWN loads and simulates correctly but its static title screen does not
// dirty a board cell during ordinary GameSteps. This guards the explicit final
// render that makes the standalone validator agree with the generation gate.
func TestValidateRendersStaticTown(t *testing.T) {
	ok, reason := validate("TOWN", filepath.Join("..", ".."), 200)
	if !ok {
		t.Fatalf("validate(TOWN) = false: %s", reason)
	}
}
