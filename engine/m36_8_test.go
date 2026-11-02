package zztgo

import "testing"

// Task M36.8 — client movement input is clamped at the boundary.
//
// inputMessageToPlayerInput used to pass the wire's raw int16 dx/dy straight
// into the sim whenever keymask was 0. Vanilla's keyboard can only ever produce
// a delta of -1, 0 or 1 (INPUT.PAS InputUpdate), and every engine site that
// reads InputDeltaX/Y assumes that: ElementPlayerTick indexes the tile at
// X+dx, BoardShoot indexes X+dx, and the paused branch of GameStepWithInputs
// does the same. So {dx:5} stepped a player five squares through a wall, and
// {dx:200} with shift indexed Tiles[x+200] and panicked — which stepRoom
// contains by dropping the room and everyone in it. Anonymous, one message.
//
// These tests go through the exact conversion the read loop uses and then the
// RoomManager step, so the claim being tested is "a hostile message", not "a
// hostile engine input".

// m368World is a one-board world: an empty field with a wall square at
// (21,12), so a player at (20,12) moving east is blocked by it.
func m368World(t *testing.T) TWorld {
	t.Helper()
	setup := NewEngine()
	setup.Headless = true
	setup.WorldCreate()
	setup.World.BoardCount = 1
	setup.World.Info.CurrentBoard = 1
	setup.BoardCreate()
	for ix := int16(1); ix <= BOARD_WIDTH; ix++ {
		for iy := int16(1); iy <= BOARD_HEIGHT; iy++ {
			setup.Board.Tiles[ix][iy] = TTile{Element: E_EMPTY}
		}
	}
	setup.Board.Tiles[10][5] = TTile{Element: E_PLAYER, Color: ElementDefs[E_PLAYER].Color}
	setup.Board.Stats[0].X = 10
	setup.Board.Stats[0].Y = 5
	setup.Board.Stats[0].Under = TTile{Element: E_EMPTY}
	setup.Board.Info.StartPlayerX = 10
	setup.Board.Info.StartPlayerY = 5
	setup.Board.Info.MaxShots = 255
	setup.Board.Tiles[21][12] = TTile{Element: E_NORMAL, Color: 0x07}
	setup.Board.Name = "M36.8"
	setup.BoardClose()
	return setup.World
}

// m368Join puts a player at (x,y) on board 1 and returns its id and position.
func m368Join(t *testing.T, rm *RoomManager, x, y int16) PlayerID {
	t.Helper()
	id := rm.JoinPlayer(1, x, y)
	if gx, gy := m368Pos(t, rm, id); gx != x || gy != y {
		t.Fatalf("player %d joined at (%d,%d), want (%d,%d)", id, gx, gy, x, y)
	}
	return id
}

func m368Pos(t *testing.T, rm *RoomManager, id PlayerID) (int16, int16) {
	t.Helper()
	board, stat, ok := rm.PlayerLocation(id)
	if !ok {
		t.Fatalf("player %d is gone", id)
	}
	room, ok := rm.Room(board)
	if !ok {
		t.Fatalf("room %d is gone", board)
	}
	s := room.Engine.Board.Stats[stat]
	return int16(s.X), int16(s.Y)
}

func TestM368InputDeltaIsClampedAtTheBoundary(t *testing.T) {
	for _, tc := range []struct {
		in             InputMessage
		wantDX, wantDY int16
	}{
		{InputMessage{DeltaX: 5}, 1, 0},
		{InputMessage{DeltaX: -5}, -1, 0},
		{InputMessage{DeltaY: 200}, 0, 1},
		{InputMessage{DeltaY: -32768}, 0, -1},
		{InputMessage{DeltaX: 32767, DeltaY: -2}, 1, -1},
		{InputMessage{DeltaX: 1}, 1, 0},
		{InputMessage{DeltaX: -1}, -1, 0},
	} {
		got := inputMessageToPlayerInput(tc.in)
		if got.DeltaX != tc.wantDX || got.DeltaY != tc.wantDY {
			t.Errorf("dx=%d dy=%d -> (%d,%d), want (%d,%d)",
				tc.in.DeltaX, tc.in.DeltaY, got.DeltaX, got.DeltaY, tc.wantDX, tc.wantDY)
		}
	}
}

// {dx:5} is one square east, and the wall at (21,12) blocks it.
func TestM368BigDeltaDoesNotWalkThroughAWall(t *testing.T) {
	rm := NewRoomManager(m368World(t))
	p := m368Join(t, rm, 20, 12)

	msg := InputMessage{Type: MessageTypeInput, DeltaX: 5}
	for i := 0; i < 4; i++ {
		rm.Step(map[PlayerID]PlayerInput{p: inputMessageToPlayerInput(msg)})
	}
	if x, y := m368Pos(t, rm, p); x != 20 || y != 12 {
		t.Fatalf("player at (%d,%d) after pushing dx:5 into a wall, want (20,12)", x, y)
	}
}

// {dx:200, shift:true} with ammo is a shot east, not BoardShoot at x+200; and
// {dx:100} from a paused player is a one-square unpause, not Tiles[x+100]. In
// both cases the room and the bystander in it survive.
func TestM368BigDeltaDoesNotDropTheRoom(t *testing.T) {
	for _, tc := range []struct {
		name   string
		msg    InputMessage
		paused bool
	}{
		{"shoot dx:200", InputMessage{Type: MessageTypeInput, DeltaX: 200, Shift: true}, false},
		{"shoot dy:-300", InputMessage{Type: MessageTypeInput, DeltaY: -300, Shift: true}, false},
		{"paused dx:100", InputMessage{Type: MessageTypeInput, DeltaX: 100}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rm := NewRoomManager(m368World(t))
			attacker := m368Join(t, rm, 30, 12)
			bystander := m368Join(t, rm, 40, 20)
			state, _ := rm.PlayerState(attacker)
			state.Ammo = 20
			state.Paused = tc.paused

			for i := 0; i < 4; i++ {
				rm.Step(map[PlayerID]PlayerInput{attacker: inputMessageToPlayerInput(tc.msg)})
				if _, ok := rm.Room(1); !ok {
					t.Fatalf("step %d: room 1 was dropped after a simulation panic", i)
				}
			}
			for _, id := range []PlayerID{attacker, bystander} {
				if _, ok := rm.PlayerState(id); !ok {
					t.Fatalf("player %d was disconnected", id)
				}
			}
		})
	}
}
