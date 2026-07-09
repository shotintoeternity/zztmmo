package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/benhoyt/zztgo"
)

func main() {
	world := flag.String("world", "TOWN", "world basename to load")
	steps := flag.Int("steps", 20, "number of headless game steps to run")
	seed := flag.Uint("seed", 42, "random seed")
	flag.Parse()

	zztgo.E = zztgo.NewEngine()
	zztgo.E.Headless = true
	zztgo.E.TickSpeed = 4
	zztgo.E.TickTimeDuration = int16(zztgo.E.TickSpeed) * 2
	zztgo.E.GameStateElement = zztgo.E_PLAYER
	zztgo.E.PlayerFor(0).Paused = false
	zztgo.E.GamePlayExitRequested = false
	zztgo.E.SetInputSource(&zztgo.ScriptedInput{})

	zztgo.VideoInstall()
	zztgo.TextWindowInit(5, 3, 50, 18)
	zztgo.RandomSeed(uint32(*seed))
	zztgo.WorldCreate()
	if !zztgo.WorldLoad(*world, ".ZZT", false) {
		fmt.Fprintf(os.Stderr, "load %s.ZZT failed\n", *world)
		os.Exit(1)
	}

	zztgo.E.Board.Tiles[zztgo.E.Board.Stats[0].X][zztgo.E.Board.Stats[0].Y].Element = zztgo.E_PLAYER
	zztgo.E.Board.Tiles[zztgo.E.Board.Stats[0].X][zztgo.E.Board.Stats[0].Y].Color = zztgo.ElementDefs[zztgo.E_PLAYER].Color
	zztgo.BoardEnter(0)
	zztgo.E.CurrentTick = zztgo.Random(100)
	zztgo.E.CurrentStatTicked = zztgo.E.Board.StatCount + 1

	for step := 0; step < *steps; step++ {
		zztgo.GameStep(nil)
		if zztgo.E.GamePlayExitRequested {
			fmt.Fprintf(os.Stderr, "game requested exit at step %d\n", step+1)
			os.Exit(1)
		}
	}

	player := zztgo.E.Board.Stats[0]
	fmt.Printf("ok world=%s board=%d tick=%d statCount=%d player=(%d,%d)\n",
		*world,
		zztgo.E.World.Info.CurrentBoard,
		zztgo.E.CurrentTick,
		zztgo.E.Board.StatCount,
		player.X,
		player.Y,
	)
}
