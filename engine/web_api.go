package zztgo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// The title screen (M4.3) exists before the player has a WebSocket: they have
// not joined a room yet, and pressing 'P' is what joins one. So the data it
// draws — the title board, the world name, the high-score list, About ZZT —
// arrives over plain HTTP instead of the snapshot stream.
//
// The board itself animates, as vanilla's does (GAME.PAS:1610-1622). It is not
// a room: it is WorldInstance.Title, an isolated engine over a copied world, so
// nothing board 0's objects do can reach a player. /api/title paints the first
// frame; /api/title/stream pushes changed cells after that. A server without a
// TitleSim (the tests, which never tick) still gets the static render.

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
	// Generator is optional so servers without Anthropic credentials keep all
	// existing API endpoints available. A nil generator is initialized lazily
	// from the environment by /api/generate.
	Generator *GenerationService

	generationMu   sync.Mutex
	generationJobs map[string]*generationJob
	generationSeq  uint64
}

type generationJob struct {
	Status   string               `json:"status"`
	World    string               `json:"world,omitempty"`
	Error    string               `json:"error,omitempty"`
	Progress []GenerationProgress `json:"progress"`
}

// Handler mounts the title-screen endpoints under /api/.
func (a *WebAPI) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/title", a.handleTitle)
	mux.HandleFunc("/api/title/stream", a.handleTitleStream)
	mux.HandleFunc("/api/worlds", a.handleWorlds)
	mux.HandleFunc("/api/highscores", a.handleHighScores)
	mux.HandleFunc("/api/help", a.handleHelp)
	mux.HandleFunc("/api/saves", a.handleSaves)
	mux.HandleFunc("/api/restore", a.handleRestore)
	mux.HandleFunc("/api/loadworld", a.handleLoadWorld)
	mux.HandleFunc("/api/generate", a.handleGenerate)
	return mux
}

// handleGenerate starts the M12.4 plan-then-paint pipeline. The browser only
// supplies a premise and optional save-safe name; the result is a hosted world
// name, never unvalidated model text.
func (a *WebAPI) handleGenerate(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if recovered := recover(); recovered != nil {
			http.Error(w, fmt.Sprintf("generation internal failure: %v", recovered), http.StatusInternalServerError)
		}
	}()
	if r.Method == http.MethodGet {
		a.handleGenerationStatus(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Prompt string `json:"prompt"`
		Name   string `json:"name"`
		Async  bool   `json:"async"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}
	generator := a.Generator
	if generator == nil {
		var err error
		generator, err = GenerationServiceFromEnv()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		a.Generator = generator
	}
	client, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		client = r.RemoteAddr
	}
	if body.Async {
		jobID := fmt.Sprintf("gen-%d", atomic.AddUint64(&a.generationSeq, 1))
		a.generationMu.Lock()
		if a.generationJobs == nil {
			a.generationJobs = make(map[string]*generationJob)
		}
		a.generationJobs[jobID] = &generationJob{Status: "running"}
		a.generationMu.Unlock()
		go a.runGenerationJob(jobID, generator, client, body.Prompt, body.Name)
		w.WriteHeader(http.StatusAccepted)
		writeJSON(w, struct {
			ID string `json:"id"`
		}{ID: jobID})
		return
	}
	result, err := generator.Generate(r.Context(), client, body.Prompt, body.Name, a.Server)
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "rate limit"):
			http.Error(w, err.Error(), http.StatusTooManyRequests)
		case errors.Is(err, ErrGenerationUnavailable):
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
		default:
			http.Error(w, "generation failed: "+err.Error(), http.StatusUnprocessableEntity)
		}
		return
	}
	writeJSON(w, struct {
		World string `json:"world"`
	}{World: result.Name})
}

func (a *WebAPI) runGenerationJob(id string, generator *GenerationService, client, prompt, name string) {
	progress := func(event GenerationProgress) {
		a.generationMu.Lock()
		if job := a.generationJobs[id]; job != nil {
			job.Progress = append(job.Progress, event)
		}
		a.generationMu.Unlock()
	}
	result, err := generator.GenerateWithProgress(context.Background(), client, prompt, name, a.Server, progress)
	a.generationMu.Lock()
	defer a.generationMu.Unlock()
	job := a.generationJobs[id]
	if job == nil {
		return
	}
	if err != nil {
		job.Status = "failed"
		job.Error = err.Error()
		return
	}
	job.Status = "complete"
	job.World = result.Name
}

func (a *WebAPI) handleGenerationStatus(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	a.generationMu.Lock()
	job := a.generationJobs[id]
	if job == nil {
		a.generationMu.Unlock()
		http.Error(w, "no such generation job", http.StatusNotFound)
		return
	}
	copy := *job
	copy.Progress = append([]GenerationProgress(nil), job.Progress...)
	a.generationMu.Unlock()
	writeJSON(w, copy)
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
	var title *TitleSim
	if a.Server != nil {
		inst, err := a.Server.GetOrCreateInstance(safeWorld)
		if err != nil {
			http.Error(w, "failed to load world: "+err.Error(), http.StatusInternalServerError)
			return
		}
		rm = inst.RoomManager
		pristineWorld = inst.RoomManager.FrozenWorld()
		title = inst.Title
	} else {
		rm = a.RoomManager
		pristineWorld = a.World
	}

	// The live sim's frame, so the first paint and the stream that follows
	// agree. Without one (tests), fall back to the static render.
	screen := TitleScreenCells(pristineWorld)
	if title != nil {
		screen = title.Screen()
	}

	writeJSON(w, struct {
		World    string       `json:"world"`
		Filename string       `json:"filename"`
		Screen   []ScreenCell `json:"screen"`
	}{
		World:    rm.WorldName(),
		Filename: safeWorld,
		Screen:   screen,
	})
}

// handleTitleStream pushes the title board's changed cells as Server-Sent
// Events. SSE rather than a WebSocket because the title screen deliberately has
// no socket (that is what 'P' is for), and the traffic is one-way.
func (a *WebAPI) handleTitleStream(w http.ResponseWriter, r *http.Request) {
	if a.Server == nil {
		http.Error(w, "title stream unavailable", http.StatusNotFound)
		return
	}
	worldName := r.URL.Query().Get("world")
	if worldName == "" {
		worldName = "TOWN"
	}
	safeWorld, err := SanitizeSaveName(worldName)
	if err != nil {
		http.Error(w, "invalid world name", http.StatusBadRequest)
		return
	}
	inst, err := a.Server.GetOrCreateInstance(safeWorld)
	if err != nil {
		http.Error(w, "failed to load world: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if inst.Title == nil {
		http.Error(w, "title stream unavailable", http.StatusNotFound)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Defeats proxy buffering, which would hold frames until the stream ended.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Subscribing is also what starts the sim ticking: it idles with no watchers.
	sub, cancel := inst.Title.Subscribe()
	defer cancel()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sub.Signal():
			cells := sub.Drain()
			if len(cells) == 0 {
				continue
			}
			payload, err := json.Marshal(cells)
			if err != nil {
				return
			}
			if _, err := w.Write([]byte("data: " + string(payload) + "\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// handleWorlds lists the worlds a client may join.
func (a *WebAPI) handleWorlds(w http.ResponseWriter, r *http.Request) {
	dir := "."
	if a.Server != nil {
		dir = a.Server.worldsDir()
	} else if E != nil && E.LoadedGameFileName != "" {
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
