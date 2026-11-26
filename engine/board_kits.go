package zztgo

import (
	"strconv"
	"strings"
)

// A board's starting kit (M42.6) is the run of plain #give lines an object's
// program opens with: no touch, no label, no condition in front of them, so
// they run by themselves the first time the board ticks and hand their items
// to whoever is nearest. Classic worlds use it to give the player what a board
// needs: COTS24HR's "Apartment" opens an object with `#give gems 20` /
// `#give torches 120`, and a second object takes a torch at a time and runs
// #endgame when there is none to take, so the torches are the clock.
//
// ZZT had one player, so a kit given once per board was a kit given once per
// player. Here a board's scripts are shared and persist (in the autosave too)
// while inventory is each player's own, so only the first arrival got the
// torches and everyone after them died on arrival. The fix keeps the board
// shared and makes the kit per player: a player who arrives on a board whose
// kit object has already run past its gives is handed the kit, once per board
// per player, and a player who arrives while it is still to run is marked as
// having had it, because the object will give it to them.

// kitGive is one #give in a kit.
type kitGive struct {
	counter string // HEALTH, AMMO, GEMS, TORCHES, SCORE or TIME
	amount  int16
}

// objectKit reads the kit an object's program opens with, and the offset in
// data just past its last #give. It skips a leading @name line, blank lines
// and ' comments, and stops at the first line that is anything else.
func objectKit(data string) ([]kitGive, int16) {
	var gives []kitGive
	end := int16(0)
	pos := 0
	first := true
	for pos < len(data) {
		next := strings.IndexByte(data[pos:], '\r')
		lineEnd := len(data)
		if next >= 0 {
			lineEnd = pos + next
		}
		line := strings.TrimSpace(data[pos:lineEnd])
		after := lineEnd + 1
		if after > len(data) {
			after = len(data)
		}
		switch {
		case first && strings.HasPrefix(line, "@"), line == "", strings.HasPrefix(line, "'"):
		default:
			give, ok := parseKitGive(line)
			if !ok {
				return gives, end
			}
			gives = append(gives, give)
			end = int16(after)
		}
		first = false
		pos = after
	}
	return gives, end
}

func parseKitGive(line string) (kitGive, bool) {
	fields := strings.Fields(strings.ToUpper(line))
	if len(fields) != 3 || fields[0] != "#GIVE" {
		return kitGive{}, false
	}
	switch fields[1] {
	case "HEALTH", "AMMO", "GEMS", "TORCHES", "SCORE", "TIME":
	default:
		return kitGive{}, false
	}
	n, err := strconv.Atoi(fields[2])
	if err != nil || n <= 0 || n > 32767 {
		return kitGive{}, false
	}
	return kitGive{counter: fields[1], amount: int16(n)}, true
}

// apply adds the give to state the way OOP #give does (oop.go GIVE/TAKE),
// refusing nothing: a give never fails.
func (g kitGive) apply(state *PlayerState) {
	switch g.counter {
	case "HEALTH":
		state.Health += g.amount
	case "AMMO":
		state.Ammo += g.amount
	case "GEMS":
		state.Gems += g.amount
	case "TORCHES":
		state.Torches += g.amount
	case "SCORE":
		state.Score += g.amount
	case "TIME":
		state.BoardTimeSec += g.amount
	}
}

// settleArrivalKits runs at the top of a tick, before any room steps, for each
// player who arrived on a board since the last one (a join, a passage, a board
// edge). It runs then rather than at the arrival because a returning account's
// stored inventory is applied straight after its join, and would overwrite a
// kit handed out at the join itself. Players are taken in PlayerID order and
// the board's stats in stat order, so the result is deterministic and a replay
// derives it from the same joins and transfers (CLAUDE.md rule 2).
func (rm *RoomManager) settleArrivalKits() {
	for _, id := range rm.playerIDs() {
		player := rm.players[id]
		if !player.kitPending {
			continue
		}
		player.kitPending = false
		if player.kitsReceived[player.boardID] {
			continue
		}
		room := rm.rooms[player.boardID]
		if room == nil {
			continue
		}
		board := &room.Engine.Board
		found := false
		for statID := int16(1); statID <= board.StatCount; statID++ {
			stat := &board.Stats[statID]
			if board.Tiles[stat.X][stat.Y].Element != E_OBJECT || stat.DataLen <= 0 {
				continue
			}
			gives, end := objectKit(stat.Data)
			if len(gives) == 0 {
				continue
			}
			found = true
			if stat.DataPos >= 0 && stat.DataPos < end {
				// Still to run: it gives to the nearest player, this arrival.
				continue
			}
			for _, give := range gives {
				give.apply(player.state)
			}
		}
		if found {
			if player.kitsReceived == nil {
				player.kitsReceived = make(map[int16]bool)
			}
			player.kitsReceived[player.boardID] = true
		}
	}
}
