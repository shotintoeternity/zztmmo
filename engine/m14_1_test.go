package zztgo

import "testing"

// TestServerScopedPlayerIDsAreDisjoint hosts two instances at once and
// interleaves joins between them, asserting every PlayerID is process-unique.
// Before M14.1, each RoomManager minted ids from its own counter starting at 1,
// so the two worlds handed out the same ids (1, 2, 3...) and any server-wide map
// keyed by bare PlayerID would collide.
func TestServerScopedPlayerIDsAreDisjoint(t *testing.T) {
	server := NewWebSocketServer(testEmptyWorld(t), 1)

	if err := server.HostGeneratedWorld("SECOND", testEmptyWorld(t)); err != nil {
		t.Fatalf("HostGeneratedWorld: %v", err)
	}
	instA := server.DefaultInstance
	instB := server.Instances["SECOND"]
	if instB == nil {
		t.Fatal("second instance was not registered")
	}
	if instA.RoomManager == instB.RoomManager {
		t.Fatal("the two instances share a RoomManager")
	}

	// Interleave joins the way ServeHTTP does: mint a server-scoped id, then join
	// that id into the instance's RoomManager under its own lock.
	joinInto := func(inst *WorldInstance) PlayerID {
		id := server.mintPlayerID()
		inst.mu.Lock()
		inst.RoomManager.JoinPlayerWithID(id, 1, 0, 0)
		inst.mu.Unlock()
		return id
	}

	seen := make(map[PlayerID]bool)
	var idsA, idsB []PlayerID
	for i := 0; i < 5; i++ {
		a := joinInto(instA)
		b := joinInto(instB)
		if a == 0 || b == 0 {
			t.Fatalf("round %d: minted the zero id, which is the client's unjoined sentinel", i)
		}
		if seen[a] {
			t.Fatalf("round %d: PlayerID %d reissued", i, a)
		}
		seen[a] = true
		if seen[b] {
			t.Fatalf("round %d: PlayerID %d reissued", i, b)
		}
		seen[b] = true
		idsA = append(idsA, a)
		idsB = append(idsB, b)
	}

	// Each instance holds exactly its own five players, and every id it holds is
	// one it was actually handed (no cross-talk between the RoomManagers).
	instA.mu.Lock()
	defer instA.mu.Unlock()
	instB.mu.Lock()
	defer instB.mu.Unlock()
	if got := len(instA.RoomManager.players); got != len(idsA) {
		t.Fatalf("instance A has %d players, want %d", got, len(idsA))
	}
	if got := len(instB.RoomManager.players); got != len(idsB) {
		t.Fatalf("instance B has %d players, want %d", got, len(idsB))
	}
	for _, id := range idsA {
		if instA.RoomManager.players[id] == nil {
			t.Fatalf("instance A missing its player %d", id)
		}
		if instB.RoomManager.players[id] != nil {
			t.Fatalf("PlayerID %d leaked into instance B", id)
		}
	}
	for _, id := range idsB {
		if instB.RoomManager.players[id] == nil {
			t.Fatalf("instance B missing its player %d", id)
		}
	}
}
