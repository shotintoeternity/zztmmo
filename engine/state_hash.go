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
func StateHash(e *Engine) uint64 {
	h := fnv.New64a()

	if pState, exists := e.Players[0]; exists {
		e.World.Info.Health = pState.Health
		e.World.Info.Ammo = pState.Ammo
		e.World.Info.Gems = pState.Gems
		e.World.Info.Torches = pState.Torches
		e.World.Info.TorchTicks = pState.TorchTicks
		e.World.Info.EnergizerTicks = pState.EnergizerTicks
		e.World.Info.Score = pState.Score
		e.World.Info.Keys = pState.Keys
		e.World.Info.BoardTimeSec = pState.BoardTimeSec
		e.World.Info.BoardTimeHsec = pState.BoardTimeHsec
	}

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

	hashWorldInfo(h, &e.World.Info)
	hashUint32(h, e.RandSeed)

	return h.Sum64()
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
