package zztgo

import (
	"path/filepath"
	"testing"
)

// townRoomManager loads fixtures/TOWN.ZZT and returns a fresh RoomManager.
// Tests that need TOWN as a real multi-board world call this instead of
// loadTownWorldForM45 so they can drive the RoomManager protocol path.
func townRoomManager(t *testing.T) *RoomManager {
	t.Helper()

	setup := NewEngine()
	setup.Headless = true
	setup.WorldCreate()
	worldBase := filepath.Join("..", "fixtures", "TOWN")
	if !setup.WorldLoad(worldBase, ".ZZT", false) {
		t.Skipf("fixtures/TOWN.ZZT unavailable")
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
