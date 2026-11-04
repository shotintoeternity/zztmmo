package zztgo

import (
	"encoding/binary"
	"hash"
	"hash/fnv"
)

// StateHash is the replay safety net: a deterministic FNV-1a digest of the
// simulation state M0 made headless. TStat's unknown pointer fields are
// deliberately excluded because pointer addresses are runtime artifacts, not
// serialized game state.
//
// It reads the engine and writes nothing (M36.26e). It used to copy
// Players[0]'s inventory into World.Info before hashing it, which both mutated
// the world as a side effect of looking at it and left every other player's
// inventory out of the hash: a regression in player 2's ammo, keys or respawn
// passed every replay fixture. Every player is now hashed by hashPlayers.
func StateHash(e *Engine) uint64 {
	h := fnv.New64a()

	for x := 0; x <= BOARD_WIDTH+1; x++ {
		for y := 0; y <= BOARD_HEIGHT+1; y++ {
			hashByte(h, e.Board.Tiles[x][y].Element)
			hashByte(h, e.Board.Tiles[x][y].Color)
		}
	}

	hashInt16(h, e.Board.StatCount)
	for i := int16(0); i <= e.Board.StatCount; i++ {
		stat := &e.Board.Stats[i]
		hashByte(h, stat.X)
		hashByte(h, stat.Y)
		hashInt16(h, stat.StepX)
		hashInt16(h, stat.StepY)
		hashInt16(h, stat.Cycle)
		hashByte(h, stat.P1)
		hashByte(h, stat.P2)
		hashByte(h, stat.P3)
		hashInt16(h, stat.Follower)
		hashInt16(h, stat.Leader)
		hashByte(h, stat.Under.Element)
		hashByte(h, stat.Under.Color)
		hashString(h, stat.Data)
		hashInt16(h, stat.DataPos)
		hashInt16(h, stat.DataLen)
	}

	hashPlayers(h, e)
	hashWorldInfo(h, &e.World.Info)
	hashUint32(h, e.RandSeed)

	return h.Sum64()
}

// hashPlayers hashes the PlayerState of every stat that is a player, in stat
// order. A player is a stat standing on an E_PLAYER tile — the same test
// GameStepWithInputs dispatches by — rather than a key of e.Players, because
// PlayerFor creates an entry for any stat it is asked about (BoardAttack asks
// about non-player attackers), so the map's keys depend on who looked, not on
// the game. Nothing here calls PlayerFor, so hashing creates no entry either.
//
// The fields are the ones that decide what happens next: inventory, the aim a
// shot leaves in, the respawn countdown, pause, and the re-entry square. Left
// out, as presentation, are PlayerCharacter (the energizer-blink glyph phase,
// which is already in the tile it is drawn to), SoundEnabled, and the
// Message*NotShown flags, which only decide whether a hint is displayed.
func hashPlayers(h hash.Hash64, e *Engine) {
	for i := int16(0); i <= e.Board.StatCount; i++ {
		stat := &e.Board.Stats[i]
		// This runs on every diff, so a stat off the tile grid is skipped
		// rather than allowed to panic the tick.
		if int(stat.X) > BOARD_WIDTH+1 || int(stat.Y) > BOARD_HEIGHT+1 ||
			e.Board.Tiles[stat.X][stat.Y].Element != E_PLAYER {
			continue
		}
		hashInt16(h, i)
		pState := e.Players[i]
		if pState == nil {
			hashBool(h, false)
			continue
		}
		hashBool(h, true)
		hashInt16(h, pState.Health)
		hashInt16(h, pState.Ammo)
		hashInt16(h, pState.Gems)
		hashInt16(h, pState.Torches)
		hashInt16(h, pState.TorchTicks)
		hashInt16(h, pState.EnergizerTicks)
		hashInt16(h, pState.Score)
		for _, key := range pState.Keys {
			hashBool(h, key)
		}
		hashInt16(h, pState.BoardTimeSec)
		hashInt16(h, pState.BoardTimeHsec)
		hashInt16(h, pState.DirX)
		hashInt16(h, pState.DirY)
		hashInt16(h, pState.RespawnTicks)
		hashBool(h, pState.Paused)
		hashByte(h, pState.ReenterX)
		hashByte(h, pState.ReenterY)
	}
	// End marker: a player list cannot run on into the world info.
	hashInt16(h, -1)
}

func hashWorldInfo(h hash.Hash64, info *TWorldInfo) {
	hashInt16(h, info.Ammo)
	hashInt16(h, info.Gems)
	for _, key := range info.Keys {
		hashBool(h, key)
	}
	hashInt16(h, info.Health)
	hashInt16(h, info.CurrentBoard)
	hashInt16(h, info.Torches)
	hashInt16(h, info.TorchTicks)
	hashInt16(h, info.EnergizerTicks)
	hashInt16(h, info.padding1)
	hashInt16(h, info.Score)
	hashString(h, info.Name)
	for _, flag := range info.Flags {
		hashString(h, flag)
	}
	hashInt16(h, info.BoardTimeSec)
	hashInt16(h, info.BoardTimeHsec)
	hashBool(h, info.IsSave)
	for _, b := range info.padding2 {
		hashByte(h, b)
	}
}

func hashString(h hash.Hash64, s string) {
	hashUint32(h, uint32(len(s)))
	_, _ = h.Write([]byte(s))
}

func hashBool(h hash.Hash64, b bool) {
	if b {
		hashByte(h, 1)
		return
	}
	hashByte(h, 0)
}

func hashByte(h hash.Hash64, b byte) {
	_, _ = h.Write([]byte{b})
}

func hashInt16(h hash.Hash64, n int16) {
	var buf [2]byte
	binary.LittleEndian.PutUint16(buf[:], uint16(n))
	_, _ = h.Write(buf[:])
}

func hashUint32(h hash.Hash64, n uint32) {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], n)
	_, _ = h.Write(buf[:])
}
