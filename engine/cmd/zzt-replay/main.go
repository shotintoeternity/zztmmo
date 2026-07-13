// Command zzt-replay reconstructs a recorded multiplayer session and replays it
// headless through the same RoomManager entry points the live server used
// (M14.2). Because the simulation is deterministic, the replay reproduces every
// room's state exactly; the tool prints per-room StateHash at checkpoints so a
// recording can be verified against the run that produced it.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"sort"

	"github.com/benhoyt/zztgo"
)

func main() {
	every := flag.Int("every", 100, "print per-room StateHash every N ticks")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: zzt-replay [-every N] <recording.jsonl>")
		os.Exit(2)
	}

	path := flag.Arg(0)
	f, err := os.Open(path)
	if err != nil {
		log.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	lastTick := -1
	rm, err := zztgo.ReplaySession(f, func(tick int, rm *zztgo.RoomManager) {
		lastTick = tick
		if *every > 0 && tick%*every == 0 {
			printHashes(tick, rm)
		}
	})
	if err != nil {
		log.Fatalf("replay %s: %v", path, err)
	}
	if lastTick < 0 {
		fmt.Println("recording contained no ticks")
		return
	}
	fmt.Printf("final tick %d\n", lastTick)
	printHashes(lastTick, rm)
}

func printHashes(tick int, rm *zztgo.RoomManager) {
	hashes := rm.RoomStateHashes()
	boards := make([]int, 0, len(hashes))
	for boardID := range hashes {
		boards = append(boards, int(boardID))
	}
	sort.Ints(boards)
	for _, boardID := range boards {
		fmt.Printf("tick %6d  board %3d  hash %016x\n", tick, boardID, hashes[int16(boardID)])
	}
}
