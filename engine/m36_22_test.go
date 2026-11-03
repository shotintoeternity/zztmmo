package zztgo

import (
	"os"
	"sync"
	"testing"
)

// M36.22: generation's dry run (simulateGeneratedBoards) steps a scratch
// engine on an HTTP goroutine while the tick loop steps live rooms. Run with
// -race: before the input state moved onto the Engine, the two shared the
// package-level Input* globals, so this was a data race and a live player's
// move could be dropped or replaced.
func TestM3622GeneratingWhileARoomTicksDoesNotRace(t *testing.T) {
	data, err := os.ReadFile("../fixtures/TOWN.ZZT")
	if err != nil {
		t.Fatal(err)
	}
	world, err := LoadWorldBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	rm := NewRoomManager(world)
	pid := rm.JoinPlayer(1, 0, 0)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := simulateGeneratedBoards(data); err != nil {
			t.Error(err)
		}
	}()
	for i := 0; i < 200; i++ {
		rm.StepDiffs(map[PlayerID]PlayerInput{pid: {DeltaX: 1}})
	}
	wg.Wait()
}
