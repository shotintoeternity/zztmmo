package zztgo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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
	// Museum proxies the Museum of ZZT API and hosts downloaded worlds on
	// demand. Nil is initialized lazily from Server.
	Museum *MuseumService
	// Auth serves browser-facing Google OAuth endpoints. Nil keeps the server in
	// guest-only mode.
	Auth *AuthService

	generationMu   sync.Mutex
	generationJobs map[string]*generationJob
	generationSeq  uint64
}

type generationJob struct {
	Status string `json:"status"`
	World  string `json:"world,omitempty"`
	Error  string `json:"error,omitempty"`
	// Retryable and FailedBoard are set when a failure kept resumable state
	// (M12.22): the client may POST {"retry": "<job id>"} to re-request the
	// failed board instead of starting the whole world over.
	Retryable   bool   `json:"retryable,omitempty"`
	FailedBoard string `json:"failedBoard,omitempty"`
	// StubbedBoards names the boards that failed and were salvaged as stub rooms
	// (M17.13). Set on a *complete* job: the world is playable but incomplete.
	StubbedBoards []string             `json:"stubbedBoards,omitempty"`
	Progress      []GenerationProgress `json:"progress"`

	resume    *GenerationBoardError
	generator *GenerationService
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
	mux.HandleFunc("/api/museum/search", a.handleMuseumSearch)
	mux.HandleFunc("/api/museum/play", a.handleMuseumPlay)
	mux.HandleFunc("/api/preferences", a.handlePreferences)
	mux.HandleFunc("/api/auth/me", a.handleAuthMe)
	mux.HandleFunc("/api/auth/logout", a.handleAuthLogout)
	mux.HandleFunc("/api/auth/google/start", a.handleAuthStart)
	mux.HandleFunc("/api/auth/google/callback", a.handleAuthCallback)
	return mux
}

// SPAFileServer serves the built browser client, falling back to the app for any
// path that is not a file on disk.
//
// That fallback is what makes a client-side address a real URL: /play/TOWN
// (M20.1) is not a file and never will be, so without it a deep link 404s and a
// reload of one lands on nothing. Which is why this lives here rather than in
// cmd/zzt-server, where it started: the client writes /play/<world> into the
// address bar, so every server that serves that client owes the fallback, and a
// test harness mounting a plain http.FileServer was a harness that could not
// reload the page the browser was on (found by M20.1).
func SPAFileServer(root http.FileSystem) http.Handler {
	files := http.FileServer(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Clean(r.URL.Path)
		if path == "." || path == string(filepath.Separator) {
			files.ServeHTTP(w, r)
			return
		}

		file, err := root.Open(path)
		if err == nil {
			_ = file.Close()
			files.ServeHTTP(w, r)
			return
		}

		r.URL.Path = "/"
		files.ServeHTTP(w, r)
	})
}

// preferencesResponse is what the title screen reads its own settings from
// (M19.3). Authenticated says whether an account was found at all; Stored says
// whether that account has a preferences document, which the client needs
// separately from Color because an existing document with an empty Color is a
// deliberate "no color" and an absent one is "never chose".
type preferencesResponse struct {
	Authenticated bool   `json:"authenticated"`
	Stored        bool   `json:"stored"`
	Color         string `json:"color,omitempty"`
}

// handlePreferences reads and writes the signed-in player's account-wide
// preferences. A guest is not an error: GET answers {authenticated:false} so
// the client falls back to localStorage without a failed request to interpret,
// and PUT is refused, because a preference with no account to key on has
// nowhere to go (ErrNoAccountID, one layer down).
func (a *WebAPI) handlePreferences(w http.ResponseWriter, r *http.Request) {
	account, authenticated := a.authenticatedAccount(r)
	switch r.Method {
	case http.MethodGet:
		if !authenticated {
			writeJSON(w, preferencesResponse{})
			return
		}
		prefs, stored := a.storedPreferences(account.ID)
		writeJSON(w, preferencesResponse{Authenticated: true, Stored: stored, Color: prefs.Color})
	case http.MethodPut:
		if !authenticated {
			http.Error(w, "sign in to store preferences", http.StatusUnauthorized)
			return
		}
		if a.Server == nil || a.Server.ChatDB == nil {
			http.Error(w, "preferences are unavailable", http.StatusServiceUnavailable)
			return
		}
		var body struct {
			Color string `json:"color"`
		}
		// Capped like every other body this API decodes (handleGenerate): the
		// whole document is a seven-character color, so a kilobyte is generous.
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
			http.Error(w, "bad request body", http.StatusBadRequest)
			return
		}
		// Validated here, at the edge, exactly as the join validates it
		// (SanitizePlayerColor): this value is broadcast to other players'
		// browsers, and it now also outlives the connection that sent it, so a
		// junk color stored once would be junk broadcast forever. Anything that
		// is not "#" plus six hex digits becomes "", which is the vanilla
		// player rather than a rejection — the picker's "No color" row sends
		// exactly that.
		// Read-modify-write, NOT a fresh document (M21.1). The store now holds a
		// second field — the accounts whose chat this player has blocked — and
		// this endpoint knows nothing about it; writing `AccountPreferences{Color}`
		// would delete every block a player holds the moment they picked a colour.
		// M19.3 predicted this shape of loss for a field arriving later; the field
		// arrived. Anything added to the document from now on is preserved here
		// for free, because only the field this endpoint owns is assigned.
		prefs, _ := a.storedPreferences(account.ID)
		prefs.Color = SanitizePlayerColor(body.Color)
		if err := a.Server.ChatDB.PutAccountPreferences(account.ID, prefs); err != nil {
			http.Error(w, "could not store preferences", http.StatusInternalServerError)
			return
		}
		writeJSON(w, preferencesResponse{Authenticated: true, Stored: true, Color: prefs.Color})
	default:
		http.Error(w, "use GET or PUT", http.StatusMethodNotAllowed)
	}
}

func (a *WebAPI) authenticatedAccount(r *http.Request) (AuthenticatedAccount, bool) {
	if a.Auth == nil {
		return AuthenticatedAccount{}, false
	}
	return a.Auth.AccountFromRequest(r)
}

func (a *WebAPI) storedPreferences(accountID string) (AccountPreferences, bool) {
	if a.Server == nil || a.Server.ChatDB == nil {
		return AccountPreferences{}, false
	}
	prefs, ok, err := a.Server.ChatDB.GetAccountPreferences(accountID)
	if err != nil {
		log.Printf("zztgo: failed to read preferences for account %q: %v", accountID, err)
		return AccountPreferences{}, false
	}
	return prefs, ok
}

func (a *WebAPI) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	if a.Auth == nil {
		writeJSON(w, struct {
			Enabled       bool `json:"enabled"`
			Authenticated bool `json:"authenticated"`
		}{})
		return
	}
	a.Auth.HandleMe(w, r)
}

func (a *WebAPI) handleAuthStart(w http.ResponseWriter, r *http.Request) {
	if a.Auth == nil {
		http.Error(w, ErrAuthDisabled.Error(), http.StatusServiceUnavailable)
		return
	}
	a.Auth.HandleStart(w, r)
}

func (a *WebAPI) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	if a.Auth == nil {
		http.Error(w, ErrAuthDisabled.Error(), http.StatusServiceUnavailable)
		return
	}
	a.Auth.HandleCallback(w, r)
}

func (a *WebAPI) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if a.Auth == nil {
		writeJSON(w, struct {
			OK bool `json:"ok"`
		}{OK: true})
		return
	}
	a.Auth.HandleLogout(w, r)
}

func (a *WebAPI) museumService() *MuseumService {
	if a.Museum == nil {
		a.Museum = NewMuseumService(a.Server)
	}
	return a.Museum
}

func (a *WebAPI) handleMuseumSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "use GET", http.StatusMethodNotAllowed)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, MuseumSearchResponse{})
		return
	}
	result, err := a.museumService().Search(r.Context(), q)
	if err != nil {
		http.Error(w, "museum search failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, result)
}

func (a *WebAPI) handleMuseumPlay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}
	var req MuseumPlayRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}
	result, err := a.museumService().Play(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	writeJSON(w, result)
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
		Ground bool   `json:"ground"`
		Retry  string `json:"retry"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}
	if body.Retry != "" {
		a.handleGenerationRetry(w, body.Retry)
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
	client := generationClientKey(r)
	// M16.17b: who is asking, so a dream cannot take a world another account
	// owns — and so a signed-in dreamer's own world is owned once it lands. A
	// guest is the zero account: unowned names only. Read here, on the request
	// goroutine, because the async job outlives r.
	req := GenerationRequest{
		Client: client, Account: a.requestAccount(r), Premise: body.Prompt,
		Name: body.Name, Server: a.Server, Ground: body.Ground,
	}
	if body.Async {
		jobID := fmt.Sprintf("gen-%d", atomic.AddUint64(&a.generationSeq, 1))
		a.generationMu.Lock()
		if a.generationJobs == nil {
			a.generationJobs = make(map[string]*generationJob)
		}
		a.generationJobs[jobID] = &generationJob{Status: "running"}
		a.generationMu.Unlock()
		go a.runGenerationJob(jobID, generator, req)
		w.WriteHeader(http.StatusAccepted)
		writeJSON(w, struct {
			ID string `json:"id"`
		}{ID: jobID})
		return
	}
	result, err := generator.GenerateRequest(r.Context(), req)
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "rate limit"), errors.Is(err, ErrGenerationBudget):
			http.Error(w, err.Error(), http.StatusTooManyRequests)
		case errors.Is(err, ErrGenerationUnavailable):
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
		case errors.Is(err, ErrGeneratedWorldOccupied), errors.Is(err, ErrGeneratedWorldNotYours),
			errors.Is(err, ErrGeneratedWorldIsCanonical):
			// M16.17b: a name conflict, not a failed generation. Nothing was
			// written — and nothing will be until that world empties out, or
			// never, if it is somebody else's or a classic (M18.11). Only a
			// name the player typed reaches here: a name the plan chose was
			// resolved to a minted one instead (M16.17d).
			http.Error(w, err.Error(), http.StatusConflict)
		default:
			http.Error(w, "generation failed: "+err.Error(), http.StatusUnprocessableEntity)
		}
		return
	}
	writeJSON(w, struct {
		World string `json:"world"`
	}{World: result.Name})
}

// generationClientKey names the caller that the per-client generation rate
// limit paces. Production runs zzt-server on 127.0.0.1 behind Caddy, so
// r.RemoteAddr is the loopback address on every request and a key taken from it
// alone would turn the per-player cooldown into one global cooldown that any
// single player could hold. Only for a loopback peer is X-Forwarded-For
// consulted, and only its last hop — the address the proxy itself observed.
// A header from a client that reached the server directly is ignored, so it
// cannot be spoofed to shed the limit.
func generationClientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return host
	}
	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded == "" {
		return host
	}
	hops := strings.Split(forwarded, ",")
	if last := strings.TrimSpace(hops[len(hops)-1]); last != "" {
		return last
	}
	return host
}

// requestAccount names the signed-in player behind a request, or the zero
// account for a guest — which is also what a server running without auth
// returns. Ownership decisions read it; nothing else about the request does.
func (a *WebAPI) requestAccount(r *http.Request) AuthenticatedAccount {
	if a.Server == nil {
		return AuthenticatedAccount{}
	}
	account, ok := a.Server.authAccount(r)
	if !ok {
		return AuthenticatedAccount{}
	}
	return account
}

func (a *WebAPI) runGenerationJob(id string, generator *GenerationService, req GenerationRequest) {
	req.Progress = a.jobProgress(id)
	result, err := generator.GenerateRequest(context.Background(), req)
	a.finishGenerationJob(id, generator, result, err)
}

func (a *WebAPI) jobProgress(id string) func(GenerationProgress) {
	return func(event GenerationProgress) {
		a.generationMu.Lock()
		if job := a.generationJobs[id]; job != nil {
			job.Progress = append(job.Progress, event)
		}
		a.generationMu.Unlock()
	}
}

func (a *WebAPI) finishGenerationJob(id string, generator *GenerationService, result GenerationResult, err error) {
	a.generationMu.Lock()
	defer a.generationMu.Unlock()
	job := a.generationJobs[id]
	if job == nil {
		return
	}
	if err != nil {
		job.Status = "failed"
		job.Error = err.Error()
		// A board-scoped failure keeps its resume state so the player can
		// re-request just the failed board (M12.22).
		var boardErr *GenerationBoardError
		if errors.As(err, &boardErr) {
			job.Retryable = true
			job.FailedBoard = boardErr.Board
			job.resume = boardErr
			job.generator = generator
		}
		return
	}
	job.Status = "complete"
	job.World = result.Name
	// M17.13: a salvaged world is playable now, but the boards that failed are
	// stub rooms. Keep the job retryable so the client can offer to repaint them
	// while the player is already in the world.
	job.StubbedBoards = result.Stubbed
	if result.Retry != nil {
		job.Retryable = true
		job.FailedBoard = result.Retry.Board
		job.resume = result.Retry
		job.generator = generator
	}
}

// handleGenerationRetry resumes a failed async job from its failed board. The
// job is flipped back to running under the lock before the goroutine starts,
// so a second concurrent retry of the same job is refused rather than racing
// the shared resume state.
func (a *WebAPI) handleGenerationRetry(w http.ResponseWriter, id string) {
	a.generationMu.Lock()
	job := a.generationJobs[id]
	if job == nil {
		a.generationMu.Unlock()
		http.Error(w, "no such generation job", http.StatusNotFound)
		return
	}
	// A salvaged job is "complete" and still retryable (M17.13), so the gate is
	// the resume state rather than the status. Flipping to running below still
	// makes a second concurrent retry impossible.
	if (job.Status != "failed" && job.Status != "complete") || job.resume == nil || job.generator == nil {
		a.generationMu.Unlock()
		http.Error(w, "generation job is not retryable", http.StatusConflict)
		return
	}
	resume, generator := job.resume, job.generator
	job.Status = "running"
	job.Error = ""
	job.Retryable = false
	job.FailedBoard = ""
	job.resume = nil
	a.generationMu.Unlock()
	go func() {
		result, err := generator.RetryBoard(context.Background(), resume, a.jobProgress(id))
		a.finishGenerationJob(id, generator, result, err)
	}()
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, struct {
		ID string `json:"id"`
	}{ID: id})
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

	counts := make(map[string]int, len(worlds))
	for _, name := range worlds {
		if a.Server != nil {
			a.Server.mu.Lock()
			inst := a.Server.Instances[name]
			if inst != nil {
				inst.mu.Lock()
				counts[name] = len(inst.Clients)
				inst.mu.Unlock()
			}
			a.Server.mu.Unlock()
		}
	}

	// M17.11: editor occupancy, gathered exactly as the player counts above.
	var editorCounts map[string]int
	if a.Server != nil {
		editorCounts = a.Server.EditorCounts()
	}

	writeJSON(w, struct {
		Worlds []WorldListEntry `json:"worlds"`
	}{Worlds: WorldListEntriesInDirWithEditors(dir, worlds, counts, editorCounts)})
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
	fileNames := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		fileNames = append(fileNames, entry.Name())
	}
	return joinableWorldNames(fileNames, func(world string) bool {
		info, err := os.Stat(filepath.Join(dir, world+".ZZT"))
		return err == nil && !info.IsDir()
	}, func(world, evidence string) {
		reportUnjoinableWorld(dir, world, evidence)
	})
}

// joinableWorldNames collapses a worlds directory's file names onto the world
// identities a player can actually join (M18.13).
//
// The two halves used to disagree. This loop matched the .ZZT suffix
// case-insensitively but listed the base name verbatim, while the join path
// (LoadPristineWorld) resolves a name through SanitizeSaveName and opens
// <NAME>.ZZT — so TOWN.ZZT and town.zzt listed as two entries that opened one
// file, and a directory holding only town.zzt listed an entry that opened
// nothing at all on a case-sensitive filesystem. The identity here is now the
// one the join path uses: SanitizeSaveName(base), one entry per distinct
// result. That also makes the name the picker reports the key the occupancy
// maps in handleWorlds are built under (instances are keyed on the sanitized
// name), which a lower-case duplicate never matched.
//
// joinable answers whether <NAME>.ZZT opens; it is a parameter so a test can
// state a collision a temp directory cannot hold — on a case-insensitive
// filesystem TOWN.ZZT and town.zzt are one file, which is exactly why this
// defect reproduces on the Linux host and not on a macOS workstation. It is
// also why the gate is a stat rather than a scan for an exactly-named file:
// where the filesystem folds case, town.zzt really does answer to TOWN.ZZT and
// the world stays listed.
//
// report names an identity dropped because nothing answers to it, so an
// unjoinable file is reported rather than silently listed.
func joinableWorldNames(fileNames []string, joinable func(world string) bool, report func(world, evidence string)) []string {
	var worlds []string
	seen := make(map[string]struct{}, len(fileNames))
	for _, name := range fileNames {
		if !strings.HasSuffix(strings.ToUpper(name), ".ZZT") {
			continue
		}
		base := strings.TrimSuffix(name, filepath.Ext(name))
		// A world is only listable if a client could actually join it: the join
		// path resolves the name through SanitizeSaveName (LoadPristineWorld),
		// so names outside that charset (e.g. "_DEATH_", "DOG!") are dead
		// entries and are dropped. Pure-separator junk ("-", "--") passes the
		// charset but has no real name, so also require an alphanumeric.
		world, err := SanitizeSaveName(base)
		if err != nil {
			continue
		}
		if !hasAlphanumeric(world) {
			continue
		}
		if _, dup := seen[world]; dup {
			continue
		}
		seen[world] = struct{}{}
		if joinable != nil && !joinable(world) {
			if report != nil {
				report(world, name)
			}
			continue
		}
		worlds = append(worlds, world)
	}
	sort.Strings(worlds)
	return worlds
}

// unjoinableWorldsReported keeps the report above to one line per name per
// process. The picker refetches every few seconds, and an operator needs to see
// "this file is spelled wrong" once, not once per poll.
var unjoinableWorldsReported sync.Map

func reportUnjoinableWorld(dir, world, evidence string) {
	if _, seen := unjoinableWorldsReported.LoadOrStore(dir+"/"+world, struct{}{}); seen {
		return
	}
	log.Printf("zztgo: not listing world %q: %q is in %s but nothing answers to %s.ZZT, which is what joining it opens",
		world, evidence, dir, world)
}

func hasAlphanumeric(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			return true
		}
	}
	return false
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
		Lines: rm.HighScoreLines(0, 0),
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
