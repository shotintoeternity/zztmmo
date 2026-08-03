//go:build canary

// Whole-world topology extractor (task M12.15d, phase "world-architecture
// norms"). TestGenWorldTopology walks every untracked .ZZT world in the engine
// directory and writes ../llmworld/topology.json: one compact record per world
// holding only architecture facts (board counts, edge-exit degrees, exit
// reciprocity, passage and object density, connectivity).
//
// Like TestGenLLMWorldExamples it depends on untracked worlds and writes a
// committed file, so it is a maintainer generator behind the `canary` build
// tag, out of the required `go test ./...` path. The committed topology.json is
// consumed in the required path by the deterministic miner in stylepriors.go
// (TestStylePriorsRegenerateFromCorpus), which mines the norms from it.
//
// Run with: cd engine && go test -tags canary -run TestGenWorldTopology -v
package zztgo

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestGenWorldTopology(t *testing.T) {
	paths, err := filepath.Glob("*.ZZT")
	if err != nil || len(paths) == 0 {
		t.Skip("no .ZZT files in engine directory")
	}
	sort.Strings(paths)

	corpus := WorldTopologyCorpus{Version: stylePriorsVersion}
	for _, worldPath := range paths {
		stem := strings.TrimSuffix(filepath.Base(worldPath), ".ZZT")
		topology, ok := extractWorldTopology(t, stem)
		if !ok {
			continue
		}
		corpus.Worlds = append(corpus.Worlds, topology)
	}
	if len(corpus.Worlds) == 0 {
		t.Fatal("no world yielded topology")
	}
	sort.Slice(corpus.Worlds, func(i, j int) bool { return corpus.Worlds[i].Name < corpus.Worlds[j].Name })

	out, err := marshalStableJSON(corpus)
	if err != nil {
		t.Fatalf("marshal topology: %v", err)
	}
	outPath := filepath.Join("..", "llmworld", "topology.json")
	if err := os.WriteFile(outPath, out, 0644); err != nil {
		t.Fatalf("write %s: %v", outPath, err)
	}
	t.Logf("%d worlds → %s (%d bytes)", len(corpus.Worlds), outPath, len(out))
}

// extractWorldTopology reads one world's architecture. Downloaded community
// worlds carry corrupt boards that panic BoardOpen, so — as in
// genWorldExamples — a recover skips the world rather than killing the run.
func extractWorldTopology(t *testing.T, stem string) (topology WorldTopology, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			t.Logf("skip %s: panic while processing (corrupt board?): %v", stem, r)
			topology, ok = WorldTopology{}, false
		}
	}()

	e := NewEngine()
	e.Headless = true
	e.WorldCreate()
	if !e.WorldLoad(stem, ".ZZT", false) {
		t.Logf("skip %s: WorldLoad failed", stem)
		return WorldTopology{}, false
	}

	count := int(e.World.BoardCount)
	topology = WorldTopology{Name: stem, Boards: count + 1}
	// The world file's start board, captured before BoardOpen starts moving it.
	startBoard := int(e.World.Info.CurrentBoard)
	if startBoard < 1 || startBoard > count {
		startBoard = 1
	}

	// neighbors[board] holds that board's [north, south, west, east] targets;
	// links[board] holds every board it can reach (edges and passages both).
	neighbors := make([][4]int, count+1)
	links := make([][]int, count+1)

	for i := 0; i <= count; i++ {
		e.BoardOpen(int16(i))
		if e.Board.Info.IsDark {
			topology.DarkBoards++
		}
		for d := 0; d < 4; d++ {
			target := int(e.Board.Info.NeighborBoards[d])
			if target == 0 || target > count {
				continue
			}
			neighbors[i][d] = target
			topology.EdgeLinks++
			links[i] = append(links[i], target)
		}
		degree := 0
		for d := 0; d < 4; d++ {
			if neighbors[i][d] != 0 {
				degree++
			}
		}
		topology.Degrees[degree]++

		topology.Stats += int(e.Board.StatCount)
		for s := int16(0); s <= e.Board.StatCount; s++ {
			stat := e.Board.Stats[s]
			element := e.Board.Tiles[stat.X][stat.Y].Element
			switch element {
			case E_OBJECT:
				topology.Objects++
			case E_PASSAGE:
				topology.Passages++
				if target := int(stat.P3); target > 0 && target <= count {
					links[i] = append(links[i], target)
				}
			}
		}
	}

	// Exit reciprocity: north/south and west/east must point back at each other.
	opposite := [4]int{1, 0, 3, 2}
	for i := 0; i <= count; i++ {
		for d := 0; d < 4; d++ {
			target := neighbors[i][d]
			if target == 0 {
				continue
			}
			if neighbors[target][opposite[d]] == i {
				topology.ReciprocalEdgeLinks++
			}
		}
	}

	// Connectivity from the world's own start board. Board 0 is the title
	// screen, which nothing links to; the miner divides by playable boards.
	if count >= 1 {
		seen := map[int]bool{startBoard: true}
		queue := []int{startBoard}
		for len(queue) > 0 {
			board := queue[0]
			queue = queue[1:]
			for _, next := range links[board] {
				if !seen[next] {
					seen[next] = true
					queue = append(queue, next)
				}
			}
		}
		topology.ReachableBoards = len(seen)
	}
	return topology, true
}
