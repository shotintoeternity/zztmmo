package zztgo // unit: the friendly-fire policy, from the flag to the recording

// The friendly-fire deviation (PARITY.md §4, "friendly-fire-policy") has three
// live surfaces, and only one of them was ever a world's business:
//
//  1. Engine.FriendlyFire gates damage in BoardShoot's point-blank branch —
//     covered by m8_1_test.go — and in ElementBulletTick's moving-bullet
//     branch, which nothing else exercises.
//  2. RoomManager.FriendlyFire is the policy for a whole world, and rooms are
//     created lazily: it has to reach a room thawed long after the manager was
//     built, and a room the player walks into through a passage.
//  3. A recording has to carry it, because playback rebuilds the manager.
//
// M30.1 owned all three through the ARENA world, whose identity implied the
// policy. That world and its tests were deleted on 2026-08-11; the mechanism
// was kept on purpose. These tests drive it the way anything drives it now —
// by setting the flag — so the deviation stays covered without a world to
// stand in for it.

import (
	"bytes"
	"testing"
)

// movingBulletSetup builds a headless board with the shooter (stat 0) at
// (10,10) and a second player two squares to its right at (12,10), so a bullet
// fired east is placed as a real stat at (11,10) and reaches the target on its
// own tick rather than through the point-blank branch.
func movingBulletSetup(t *testing.T, friendlyFire bool) (*Engine, int16) {
	t.Helper()
	e := NewEngine()
	e.Headless = true
	e.FriendlyFire = friendlyFire
	e.WorldCreate()
	e.BoardCreate()

	e.Board.Tiles[10][10] = TTile{Element: E_PLAYER, Color: ElementDefs[E_PLAYER].Color}
	e.Board.Stats[0].X = 10
	e.Board.Stats[0].Y = 10
	e.AddStat(12, 10, E_PLAYER, int16(ElementDefs[E_PLAYER].Color), 1, StatTemplateDefault)
	target := e.Board.StatCount
	e.Board.Tiles[12][10] = TTile{Element: E_PLAYER, Color: ElementDefs[E_PLAYER].Color}
	e.PlayerFor(target).EnergizerTicks = 0
	return e, target
}

// A bullet already in flight obeys the same policy the point-blank branch does,
// and never damages the player who fired it either way.
func TestFriendlyFireGatesMovingBulletDamage(t *testing.T) {
	for _, tc := range []struct {
		name         string
		friendlyFire bool
		wantHit      bool
	}{
		{"on", true, true},
		{"off", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, target := movingBulletSetup(t, tc.friendlyFire)
			start := e.PlayerFor(target).Health
			shooterStart := e.PlayerFor(0).Health

			if ok := e.BoardShoot(E_BULLET, 10, 10, 1, 0, SHOT_SOURCE_PLAYER_BASE); !ok {
				t.Fatal("the bullet was not placed")
			}
			bullet := e.GetStatIdAt(11, 10)
			if bullet <= 0 {
				t.Fatalf("bullet stat not found at (11,10); GetStatIdAt = %d", bullet)
			}
			e.ElementBulletTick(bullet)

			want := start
			if tc.wantHit {
				want = start - 10
			}
			if got := e.PlayerFor(target).Health; got != want {
				t.Errorf("target health = %d, want %d", got, want)
			}
			if got := e.PlayerFor(0).Health; got != shooterStart {
				t.Errorf("shooter health = %d, want untouched %d", got, shooterStart)
			}
		})
	}
}

// The policy belongs to the manager, and rooms are made one at a time: the room
// the player starts in, the room a passage delivers them to, and the room
// rebuilt from the frozen world after everyone left all have to be created
// under it.
func TestFriendlyFireReachesEveryRoomTheManagerMakes(t *testing.T) {
	world := twoBoardPassageWorld(t)
	rm := NewRoomManagerForWorld(world, "TOWN")
	rm.FriendlyFire = true

	player := rm.JoinPlayer(1, 9, 12)
	room, ok := rm.Room(1)
	if !ok || !room.Engine.FriendlyFire {
		t.Fatalf("board 1 room FriendlyFire = %v ok=%v, want true", room != nil && room.Engine.FriendlyFire, ok)
	}

	for i := 0; i < 4; i++ {
		rm.StepDiffs(map[PlayerID]PlayerInput{player: {DeltaX: 1, Key: KEY_RIGHT}})
		if board, _, _ := rm.PlayerLocation(player); board == 2 {
			break
		}
	}
	if board, _, ok := rm.PlayerLocation(player); !ok || board != 2 {
		t.Fatalf("player location after passage = board %d ok=%v, want board 2", board, ok)
	}
	room, ok = rm.Room(2)
	if !ok || !room.Engine.FriendlyFire {
		t.Fatalf("board 2 room FriendlyFire = %v ok=%v, want true", room != nil && room.Engine.FriendlyFire, ok)
	}

	// Freeze: the last player leaves and every room goes back into the world.
	if !rm.LeavePlayer(player) {
		t.Fatal("LeavePlayer failed")
	}
	if rm.ActiveRoomCount() != 0 {
		t.Fatalf("rooms after leave = %d, want a frozen world with no live rooms", rm.ActiveRoomCount())
	}
	rejoined := rm.JoinPlayer(2, 0, 0)
	if room, ok = rm.Room(2); !ok || !room.Engine.FriendlyFire {
		t.Fatalf("thawed room FriendlyFire = %v ok=%v, want true", room != nil && room.Engine.FriendlyFire, ok)
	}
	rm.LeavePlayer(rejoined)
}

// A recording carries the policy. Nothing at playback can re-derive it — the
// world identity that used to imply it is gone — so an unrecorded flag would
// replay a friendly-fire session with the damage silently switched off.
func TestFriendlyFireRoundTripsThroughARecording(t *testing.T) {
	world := twoBoardPassageWorld(t)

	record := func(friendlyFire bool) []byte {
		t.Helper()
		var buf bytes.Buffer
		header, _, err := newSessionHeader("TOWN", world)
		if err != nil {
			t.Fatalf("newSessionHeader: %v", err)
		}
		header.FriendlyFire = friendlyFire
		rec, err := NewSessionRecorder(&buf, header)
		if err != nil {
			t.Fatalf("NewSessionRecorder: %v", err)
		}
		rm := NewRoomManagerForWorld(world, "TOWN")
		rm.FriendlyFire = friendlyFire
		rm.SetRecorder(rec)
		rm.JoinPlayerWithID(7, 1, 9, 12)
		rm.StepDiffs(nil)
		rec.Close()
		return buf.Bytes()
	}

	for _, friendlyFire := range []bool{true, false} {
		data := record(friendlyFire)

		playback, err := NewReplayPlayback(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("NewReplayPlayback: %v", err)
		}
		if playback.RoomManager() == nil {
			t.Fatal("playback built no room manager")
		}
		if got := playback.RoomManager().FriendlyFire; got != friendlyFire {
			t.Errorf("viewer playback FriendlyFire = %v, want %v", got, friendlyFire)
		}

		rm, err := ReplaySession(bytes.NewReader(data), nil)
		if err != nil {
			t.Fatalf("ReplaySession: %v", err)
		}
		if got := rm.FriendlyFire; got != friendlyFire {
			t.Errorf("ReplaySession FriendlyFire = %v, want %v", got, friendlyFire)
		}
	}
}
