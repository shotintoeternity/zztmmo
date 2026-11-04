package zztgo

import "testing"

// M36.26e: StateHash, the replay safety net, covers every player in a room,
// not just stat 0's, and hashing reads the engine without writing it.

// m3626TwoPlayerRoom joins two players to board 1 of a fresh TOWN and returns
// the room's engine and the second player's stat index.
func m3626TwoPlayerRoom(t *testing.T) (*Engine, int16) {
	t.Helper()
	rm := NewRoomManager(townWorld(t))
	a := rm.JoinPlayer(1, 0, 0)
	b := rm.JoinPlayer(1, 0, 0)
	rm.Snapshot(a)
	rm.Snapshot(b)
	room := rm.rooms[1]
	second := rm.players[b].statID
	if second == rm.players[a].statID || room.Engine.Players[second] == nil {
		t.Fatalf("second player stat %d has no PlayerState", second)
	}
	return room.Engine, second
}

func TestM3626StateHashCoversEveryPlayer(t *testing.T) {
	mutations := []struct {
		name string
		edit func(*PlayerState)
	}{
		{"ammo", func(p *PlayerState) { p.Ammo++ }},
		{"keys", func(p *PlayerState) { p.Keys[3] = !p.Keys[3] }},
		{"respawn", func(p *PlayerState) { p.RespawnTicks = 7 }},
		{"health", func(p *PlayerState) { p.Health-- }},
		{"reentry", func(p *PlayerState) { p.ReenterX++ }},
	}
	for _, m := range mutations {
		e, second := m3626TwoPlayerRoom(t)
		before := StateHash(e)
		m.edit(e.Players[second])
		if StateHash(e) == before {
			t.Errorf("changing the second player's %s left StateHash unchanged", m.name)
		}
	}
}

func TestM3626StateHashIgnoresPresentationAndLookups(t *testing.T) {
	e, second := m3626TwoPlayerRoom(t)
	before := StateHash(e)

	e.Players[second].SoundEnabled = !e.Players[second].SoundEnabled
	e.Players[second].MessageAmmoNotShown = !e.Players[second].MessageAmmoNotShown
	// A lookup of a stat that is not a player creates a map entry
	// (BoardAttack does this); the game has not changed.
	e.PlayerFor(e.Board.StatCount + 5)
	if got := StateHash(e); got != before {
		t.Fatalf("presentation flags or a PlayerFor lookup moved StateHash: %016x -> %016x", before, got)
	}
}

func TestM3626StateHashDoesNotMutateTheWorld(t *testing.T) {
	e, _ := m3626TwoPlayerRoom(t)
	e.PlayerFor(0).Ammo = 42
	e.PlayerFor(0).Score = 1234
	info := e.World.Info
	StateHash(e)
	if e.World.Info.Ammo != info.Ammo || e.World.Info.Score != info.Score || e.World.Info != info {
		t.Fatalf("StateHash wrote World.Info: ammo %d -> %d, score %d -> %d",
			info.Ammo, e.World.Info.Ammo, info.Score, e.World.Info.Score)
	}
}
