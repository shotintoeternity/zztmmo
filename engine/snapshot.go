package zztgo

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// M4.3a — savable, rejoinable room snapshots. A snapshot is the whole world:
// every live room's board re-serialized, every frozen board as it was left, and
// the union of the flags the rooms have set. The decisions behind it (players
// are dropped, flags are unioned, restore is refused while anyone plays) are
// written out in NOTES.md.

// SaveNameMaxLength is the width of vanilla's save-game field
// (SidebarPromptString passes 8 to PromptString, game.go).
const SaveNameMaxLength = 8

var (
	// ErrInvalidSaveName is a filename a client may not have.
	ErrInvalidSaveName = errors.New("save name must be 1-8 characters of A-Z, 0-9 or -")
	// ErrSavesDisabled is a server started without a -saves directory.
	ErrSavesDisabled = errors.New("saving is disabled on this server")
	// ErrNoSuchPlayer is a save requested by somebody who is not in a room.
	ErrNoSuchPlayer = errors.New("no such player")
	// ErrWorldOccupied refuses a restore while players are still in rooms.
	ErrWorldOccupied = errors.New("someone is still playing")
)

// SanitizeSaveName is the whole defense for a filename that arrives from a
// client, so it is a whitelist. It accepts only what vanilla's PROMPT_ALPHANUM
// save prompt can produce: 1-8 characters of A-Z, 0-9 and '-' (game.go:504),
// upper-cased as that prompt upper-cases them. Path separators, '.', and hence
// ".." and every absolute path fail the charset rather than a pattern match.
func SanitizeSaveName(name string) (string, error) {
	if len(name) == 0 || len(name) > SaveNameMaxLength {
		return "", ErrInvalidSaveName
	}
	out := make([]byte, len(name))
	for i := 0; i < len(name); i++ {
		c := UpCase(name[i])
		switch {
		case c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-':
			out[i] = c
		default:
			return "", ErrInvalidSaveName
		}
	}
	return string(out), nil
}

// snapshotPath resolves dir/<NAME>.SAV for a client-supplied name.
func snapshotPath(dir, name string) (string, error) {
	if dir == "" {
		return "", ErrSavesDisabled
	}
	safe, err := SanitizeSaveName(name)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, safe+".SAV")
	// Belt and braces. SanitizeSaveName cannot emit a separator, so this can
	// only fire if that charset is ever loosened.
	if filepath.Dir(path) != filepath.Clean(dir) {
		return "", ErrInvalidSaveName
	}
	return path, nil
}

// ListSnapshots returns the save names in dir, without the .SAV extension, in
// sorted order. Names that could not have been written by SaveSnapshot are
// skipped rather than offered back to a client.
func ListSnapshots(dir string) []string {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".SAV") {
			continue
		}
		name, err := SanitizeSaveName(strings.TrimSuffix(entry.Name(), ".SAV"))
		if err != nil {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// SaveSnapshot writes the world to dir/<NAME>.SAV, with playerID's inventory in
// the World.Info fields vanilla keeps it in. It does not disturb the live game:
// every board is serialized out of a copy.
func (rm *RoomManager) SaveSnapshot(dir, name string, playerID PlayerID) (string, error) {
	path, err := snapshotPath(dir, name)
	if err != nil {
		return "", err
	}
	world, ok := rm.snapshotWorld(playerID)
	if !ok {
		return "", ErrNoSuchPlayer
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	scratch := newSnapshotEngine()
	scratch.World = world

	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	if err := scratch.worldWriteTo(f); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

// RestoreSnapshot replaces the world with dir/<NAME>.SAV. It refuses while any
// player is in a room: a restore rewrites every board, and a player standing on
// one would be left on a board that no longer exists. The RoomManager is reused
// rather than replaced so that nextPlayerID keeps climbing and no PlayerID is
// ever handed out twice.
func (rm *RoomManager) RestoreSnapshot(dir, name string) error {
	path, err := snapshotPath(dir, name)
	if err != nil {
		return err
	}
	if len(rm.players) != 0 {
		return ErrWorldOccupied
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scratch := newSnapshotEngine()
	if err := scratch.worldReadFrom(f, false, nil); err != nil {
		return err
	}

	rm.world = scratch.World
	rm.rooms = make(map[int16]*Room)
	return nil
}

// LoadWorld replaces the world with dir/<NAME>.ZZT. It refuses while any player
// is in a room.
func (rm *RoomManager) LoadWorld(dir, name string) error {
	if len(rm.players) != 0 {
		return ErrWorldOccupied
	}
	safe, err := SanitizeSaveName(name)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, safe+".ZZT")
	if filepath.Dir(path) != filepath.Clean(dir) {
		return ErrInvalidSaveName
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scratch := newSnapshotEngine()
	if err := scratch.worldReadFrom(f, false, nil); err != nil {
		return err
	}

	rm.world = scratch.World
	rm.rooms = make(map[int16]*Room)
	rm.HighScorePath = filepath.Join(dir, safe+".HI")
	rm.LoadHighScores()

	if E != nil {
		E.LoadedGameFileName = filepath.Join(dir, safe)
	}

	return nil
}

// snapshotWorld is the world as it stands right now: frozen boards from
// rm.world, live boards re-serialized from their engines with the players taken
// off them, and the union of everybody's flags.
func (rm *RoomManager) snapshotWorld(playerID PlayerID) (TWorld, bool) {
	player := rm.players[playerID]
	if player == nil {
		return TWorld{}, false
	}

	// TWorld's BoardData is an array of slices: copying the struct aliases them.
	// Copy the bytes so the file can never be written from a board a live room
	// is mutating.
	world := rm.world
	for boardID := int16(0); boardID <= world.BoardCount; boardID++ {
		world.BoardData[boardID] = append([]byte(nil), rm.world.BoardData[boardID]...)
	}
	for _, boardID := range rm.roomIDs() {
		data, length := snapshotRoomBoard(rm.rooms[boardID])
		world.BoardData[boardID] = data
		world.BoardLen[boardID] = length
	}

	world.Info.Flags = rm.snapshotFlags()
	world.Info.CurrentBoard = player.boardID
	world.Info.IsSave = true

	// Vanilla stores the one player's inventory in World.Info (GAME.PAS:763).
	// Here it is the saver's, which keeps the file loadable by real ZZT and by
	// the terminal client. The server ignores it: JoinPlayer resets a joiner.
	state := player.state
	world.Info.Health = state.Health
	world.Info.Ammo = state.Ammo
	world.Info.Gems = state.Gems
	world.Info.Torches = state.Torches
	world.Info.TorchTicks = state.TorchTicks
	world.Info.EnergizerTicks = state.EnergizerTicks
	world.Info.Score = state.Score
	world.Info.Keys = state.Keys
	world.Info.BoardTimeSec = state.BoardTimeSec
	world.Info.BoardTimeHsec = state.BoardTimeHsec

	return world, true
}

// snapshotRoomBoard serializes a live room's board with every player stat
// removed, without touching the room. TBoard is a value type all the way down
// (Stats is an array, Data a string), so the copy shares nothing with the room.
func snapshotRoomBoard(room *Room) ([]byte, int16) {
	scratch := newSnapshotEngine()
	scratch.Board = room.Engine.Board
	// BoardClose serializes into World.BoardData[World.Info.CurrentBoard].
	scratch.World.Info.CurrentBoard = room.BoardID

	// Players are dropped, exactly the way LeavePlayer drops them, so a saved
	// board is indistinguishable from one everybody walked out of. Downwards:
	// RemoveStat shifts the stats above the one it removes.
	for statID := scratch.Board.StatCount; statID >= 0; statID-- {
		stat := scratch.Board.Stats[statID]
		if scratch.Board.Tiles[stat.X][stat.Y].Element == E_PLAYER {
			scratch.RemovePlayer(statID)
		}
	}

	scratch.BoardClose()
	return scratch.World.BoardData[room.BoardID], scratch.World.BoardLen[room.BoardID]
}

// snapshotFlags unions the flags of rm.world with those of every live room.
// Each room engine holds its own copy of World.Info, and freezeRoomIfEmpty only
// pushes flags out when a room empties, so no single copy is authoritative
// while the rooms are running. First-seen wins and MAX_FLAG caps the result,
// exactly as WorldSetFlag caps it. Sorted board order keeps it deterministic.
func (rm *RoomManager) snapshotFlags() [MAX_FLAG]string {
	var flags [MAX_FLAG]string
	next := 0

	add := func(name string) {
		if Length(name) == 0 || next >= MAX_FLAG {
			return
		}
		for i := 0; i < next; i++ {
			if flags[i] == name {
				return
			}
		}
		flags[next] = name
		next++
	}

	for _, name := range rm.world.Info.Flags {
		add(name)
	}
	for _, boardID := range rm.roomIDs() {
		for _, name := range rm.rooms[boardID].Engine.World.Info.Flags {
			add(name)
		}
	}
	return flags
}

// newSnapshotEngine is a throwaway engine used only to reach BoardClose and the
// world reader/writer. It never ticks, so it needs no transition table.
func newSnapshotEngine() *Engine {
	e := NewEngine()
	e.Headless = true
	e.MultiRoom = true
	e.SetInputSource(&ScriptedInput{})
	return e
}
