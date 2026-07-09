package zztgo

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	// SavesDir is the -saves directory the title screen's 'R' lists. Empty
	// means saved games are unavailable.
	SavesDir string
	// Server, when set, serializes a restore against the tick loop. Without it
	// (in tests, where nothing ticks) the RoomManager is driven directly.
	Server *WebSocketServer
}

// Handler mounts the title-screen endpoints under /api/.
func (a *WebAPI) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/title", a.handleTitle)
	mux.HandleFunc("/api/worlds", a.handleWorlds)
	mux.HandleFunc("/api/highscores", a.handleHighScores)
	mux.HandleFunc("/api/help", a.handleHelp)
	mux.HandleFunc("/api/saves", a.handleSaves)
	mux.HandleFunc("/api/restore", a.handleRestore)
	mux.HandleFunc("/api/loadworld", a.handleLoadWorld)
	return mux
}

// handleSaves lists the snapshots the title screen's 'R' can restore.
func (a *WebAPI) handleSaves(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, struct {
		Saves []string `json:"saves"`
	}{Saves: ListSnapshots(a.SavesDir)})
}

// handleRestore swaps the hosted world for a snapshot.
func (a *WebAPI) handleRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		World string `json:"world"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}

	worldName := body.World
	if worldName == "" {
		worldName = "TOWN"
	}
	safeWorld, err := SanitizeSaveName(worldName)
	if err != nil {
		http.Error(w, "invalid world name", http.StatusBadRequest)
		return
	}

	var rm *RoomManager
	if a.Server != nil {
		inst, err := a.Server.GetOrCreateInstance(safeWorld)
		if err != nil {
			http.Error(w, "failed to load world: "+err.Error(), http.StatusInternalServerError)
			return
		}
		rm = inst.RoomManager
	} else {
		rm = a.RoomManager
	}

	err = rm.RestoreSnapshot(a.SavesDir, body.Name)

	switch {
	case err == nil:
		writeJSON(w, struct {
			World string `json:"world"`
		}{World: rm.WorldName()})
	case errors.Is(err, ErrWorldOccupied):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, ErrInvalidSaveName), errors.Is(err, ErrSavesDisabled):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, os.ErrNotExist):
		http.Error(w, "no such saved game", http.StatusNotFound)
	default:
		http.Error(w, "restore failed", http.StatusInternalServerError)
	}
}

func writeJSON(w http.ResponseWriter, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

// handleTitle renders board 0 the way ZZT's title screen shows it.
func (a *WebAPI) handleTitle(w http.ResponseWriter, r *http.Request) {
	worldName := r.URL.Query().Get("world")
	if worldName == "" {
		worldName = "TOWN"
	}
	safeWorld, err := SanitizeSaveName(worldName)
	if err != nil {
		http.Error(w, "invalid world name", http.StatusBadRequest)
		return
	}

	var rm *RoomManager
	var pristineWorld TWorld
	if a.Server != nil {
		inst, err := a.Server.GetOrCreateInstance(safeWorld)
		if err != nil {
			http.Error(w, "failed to load world: "+err.Error(), http.StatusInternalServerError)
			return
		}
		rm = inst.RoomManager
		pristineWorld = inst.RoomManager.FrozenWorld()
	} else {
		rm = a.RoomManager
		pristineWorld = a.World
	}

	writeJSON(w, struct {
		World    string       `json:"world"`
		Filename string       `json:"filename"`
		Screen   []ScreenCell `json:"screen"`
	}{
		World:    rm.WorldName(),
		Filename: safeWorld,
		Screen:   TitleScreenCells(pristineWorld),
	})
}

// handleWorlds lists the worlds a client may join.
func (a *WebAPI) handleWorlds(w http.ResponseWriter, r *http.Request) {
	dir := "."
	if E != nil && E.LoadedGameFileName != "" {
		dir = filepath.Dir(E.LoadedGameFileName)
	}
	worlds := ListWorlds(dir)
	if len(worlds) == 0 {
		worlds = []string{a.RoomManager.WorldName()}
	}

	formatted := make([]string, len(worlds))
	for i, name := range worlds {
		count := 0
		if a.Server != nil {
			a.Server.mu.Lock()
			inst := a.Server.Instances[name]
			if inst != nil {
				inst.mu.Lock()
				count = len(inst.Clients)
				inst.mu.Unlock()
			}
			a.Server.mu.Unlock()
		}
		if count > 0 {
			if count == 1 {
				formatted[i] = name + " (1 player)"
			} else {
				formatted[i] = name + " (" + strconv.Itoa(count) + " players)"
			}
		} else {
			formatted[i] = name
		}
	}

	writeJSON(w, struct {
		Worlds []string `json:"worlds"`
	}{Worlds: formatted})
}

func (a *WebAPI) handleLoadWorld(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}

	safeName, err := SanitizeSaveName(body.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var rm *RoomManager
	if a.Server != nil {
		inst, err := a.Server.GetOrCreateInstance(safeName)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		rm = inst.RoomManager
		writeJSON(w, struct {
			World string `json:"world"`
		}{World: rm.WorldName()})
		return
	} else {
		rm = a.RoomManager
	}

	dir := "."
	if E != nil && E.LoadedGameFileName != "" {
		dir = filepath.Dir(E.LoadedGameFileName)
	}
	err = rm.LoadWorld(dir, safeName)

	switch {
	case err == nil:
		writeJSON(w, struct {
			World string `json:"world"`
		}{World: rm.WorldName()})
	case errors.Is(err, ErrWorldOccupied):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, ErrInvalidSaveName):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, os.ErrNotExist):
		http.Error(w, "no such world file", http.StatusNotFound)
	default:
		http.Error(w, "load world failed", http.StatusInternalServerError)
	}
}

func ListWorlds(dir string) []string {
	if dir == "" {
		dir = "."
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var worlds []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(strings.ToUpper(name), ".ZZT") {
			worlds = append(worlds, strings.TrimSuffix(name, filepath.Ext(name)))
		}
	}
	sort.Strings(worlds)
	return worlds
}

func (a *WebAPI) handleHighScores(w http.ResponseWriter, r *http.Request) {
	worldName := r.URL.Query().Get("world")
	if worldName == "" {
		worldName = "TOWN"
	}
	safeWorld, err := SanitizeSaveName(worldName)
	if err != nil {
		http.Error(w, "invalid world name", http.StatusBadRequest)
		return
	}

	var rm *RoomManager
	if a.Server != nil {
		inst, err := a.Server.GetOrCreateInstance(safeWorld)
		if err != nil {
			http.Error(w, "failed to load world: "+err.Error(), http.StatusInternalServerError)
			return
		}
		rm = inst.RoomManager
	} else {
		rm = a.RoomManager
	}

	writeJSON(w, struct {
		Title string   `json:"title"`
		Lines []string `json:"lines"`
	}{
		Title: "High scores for " + rm.WorldName(),
		Lines: rm.HighScoreLines(0),
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
