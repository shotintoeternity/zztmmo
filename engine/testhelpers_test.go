package zztgo

import (
	"os"
	"path/filepath"
	"testing"
)

// parityRegenEnv gates the one-time (re)generation of committed parity
// fixtures. When it is unset — the required, certified path — a missing
// required fixture is a hard failure, never a silent t.Skip or an auto-write
// that would dirty a clean tree. Set ZZT_PARITY_REGEN=1 (the maintainer
// command) to deliberately rewrite a fixture. This mirrors the PARITY_SCAFFOLD
// convention the manifest validator uses (task M16.1).
const parityRegenEnv = "ZZT_PARITY_REGEN"

func parityRegen() bool { return os.Getenv(parityRegenEnv) != "" }

// requireFixture fails the test when a committed fixture required for
// certification is absent. Required parity fixtures must fail closed: a clean
// clone has them, so their absence is a real defect, not a reason to pass
// silently by skipping (task M16.1).
func requireFixture(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("required parity fixture %s is missing: %v (it is committed; do not skip past it)", path, err)
	}
}

// townRoomManager loads fixtures/TOWN.ZZT and returns a fresh RoomManager.
// Tests that need TOWN as a real multi-board world call this instead of
// loadTownWorldForM45 so they can drive the RoomManager protocol path.
func townRoomManager(t *testing.T) *RoomManager {
	t.Helper()

	setup := NewEngine()
	setup.Headless = true
	setup.WorldCreate()
	worldBase := filepath.Join("..", "fixtures", "TOWN")
	requireFixture(t, worldBase+".ZZT")
	if !setup.WorldLoad(worldBase, ".ZZT", false) {
		t.Fatalf("loading required fixture %s.ZZT failed", worldBase)
	}
	return NewRoomManager(setup.World)
}

// findEvent scans a ProtocolEvent slice for the first event whose Type matches
// and returns it plus a found flag — the same shape as a map lookup.
func findEvent(events []ProtocolEvent, eventType string) (ProtocolEvent, bool) {
	for _, e := range events {
		if e.Type == eventType {
			return e, true
		}
	}
	return ProtocolEvent{}, false
}
