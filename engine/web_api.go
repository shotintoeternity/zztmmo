package zztgo

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
)

// The title screen (M4.3) exists before the player has a WebSocket: they have
// not joined a room yet, and pressing 'P' is what joins one. So the data it
// draws — the title board, the world name, the high-score list, About ZZT —
// arrives over plain HTTP instead of the snapshot stream.
//
// DEVIATION from vanilla: the title board is a STATIC render. In ZZT the title
// screen is GamePlayLoop running board 0 with GameStateElement = E_MONITOR
// (GAME.PAS:1610-1622), so its objects animate. Here the world is shared: a
// title room that ticked would run board 0's objects — and any `#set` its
// objects perform touches World.Info.Flags, which every room shares — for as
// long as any browser anywhere sat on the title screen. A per-client screen in
// vanilla is server-wide state here, so it does not tick.

// WebAPI serves the title screen's read-only data for one hosted world.
type WebAPI struct {
	RoomManager *RoomManager
	// World is the pristine world, used to render the title board. It is not
	// the live one: RoomManager.FrozenWorld() mutates as boards freeze.
	World TWorld
}

// Handler mounts the title-screen endpoints under /api/.
func (a *WebAPI) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/title", a.handleTitle)
	mux.HandleFunc("/api/worlds", a.handleWorlds)
	mux.HandleFunc("/api/highscores", a.handleHighScores)
	mux.HandleFunc("/api/help", a.handleHelp)
	return mux
}

func writeJSON(w http.ResponseWriter, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

// handleTitle renders board 0 the way ZZT's title screen shows it.
func (a *WebAPI) handleTitle(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, struct {
		World  string       `json:"world"`
		Screen []ScreenCell `json:"screen"`
	}{
		World:  a.RoomManager.WorldName(),
		Screen: TitleScreenCells(a.World),
	})
}

// handleWorlds lists the worlds a client may join. This server hosts exactly
// one (zzt-server -world), so the title screen's 'W' shows a single entry
// rather than a directory: switching worlds would mean switching RoomManagers,
// and RoomManager mints PlayerIDs from 1, so two of them would collide on the
// wire. Multi-world hosting is its own task.
func (a *WebAPI) handleWorlds(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, struct {
		Worlds []string `json:"worlds"`
	}{Worlds: []string{a.RoomManager.WorldName()}})
}

func (a *WebAPI) handleHighScores(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, struct {
		Title string   `json:"title"`
		Lines []string `json:"lines"`
	}{
		Title: "High scores for " + a.RoomManager.WorldName(),
		Lines: a.RoomManager.HighScoreLines(0),
	})
}

// handleHelp serves a .HLP file as text-window lines. The name comes from the
// client, so it is confined to a bare basename with the right extension inside
// HelpDir — never a path.
func (a *WebAPI) handleHelp(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("file")
	if !validHelpFile(name) {
		http.Error(w, "bad help file", http.StatusBadRequest)
		return
	}
	lines := HelpFileLines(name)
	if len(lines) == 0 {
		http.Error(w, "no such help file", http.StatusNotFound)
		return
	}
	writeJSON(w, struct {
		Title string   `json:"title"`
		Lines []string `json:"lines"`
	}{Title: r.URL.Query().Get("title"), Lines: lines})
}

func validHelpFile(name string) bool {
	if name == "" || filepath.Base(name) != name {
		return false
	}
	if strings.Contains(name, "..") {
		return false
	}
	return strings.HasSuffix(name, ".HLP")
}

// TitleScreenCells renders board 0 of world as the title screen sees it: the
// board drawn, and stat 0's tile replaced by E_MONITOR, which is what
// GamePlayLoop does when GameStateElement is E_MONITOR (game.go, GAME.PAS:1604).
func TitleScreenCells(world TWorld) []ScreenCell {
	e := NewEngine()
	e.Headless = true
	e.MultiRoom = true
	e.SetInputSource(&ScriptedInput{})
	e.World = world
	e.GameStateElement = E_MONITOR
	e.BoardOpen(0)
	e.GenerateTransitionTable()
	e.TransitionDrawToBoard()

	stat := e.Board.Stats[0]
	e.Board.Tiles[stat.X][stat.Y].Element = E_MONITOR
	e.Board.Tiles[stat.X][stat.Y].Color = ElementDefs[E_MONITOR].Color
	e.BoardDrawTile(int16(stat.X), int16(stat.Y))

	return screenCells(e)
}
