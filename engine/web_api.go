// The HTTP surface beside the WebSocket: health and metrics for the operator,
// the world search and museum proxies, account preferences, replay postcards,
// and the static file server that hands the browser its client.
//
// SPAFileServer is the one to know about — it serves the built client and falls
// back to index.html so a deep link into the app reaches the router instead of
// a 404.
//
// Long-running work started here (world generation, above all) does not block a
// request: it becomes a generationJob the client polls, so a two-minute dream
// does not sit on an open connection.

package zztgo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
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
	// Museum proxies the Museum of ZZT API and hosts downloaded worlds on
	// demand. Nil is initialized lazily from Server.
	Museum *MuseumService
	// Auth serves browser-facing Google OAuth endpoints. Nil keeps the server in
	// guest-only mode.
	Auth *AuthService
	// GazetteEditor writes and caches the day's edition (M34.2). Nil is built
	// on first use from the ledger and whatever author the environment offers,
	// so cmd/ needs no wiring and a server without credentials still has a
	// paper.
	GazetteEditor  *GazetteEditor
	gazetteMu      sync.Mutex
	generationMu   sync.Mutex
	generationJobs map[string]*generationJob
	generationSeq  uint64

	thumbnailMu    sync.Mutex
	thumbnailCache map[string]WorldTitleThumbnail
	thumbnailOrder []string
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
	// account is who asked for this dream, captured on the request goroutine
	// that started the job because the job outlives the request. The Gazette
	// credits it (M34.1a), and it is deliberately the ORIGINAL requester rather
	// than whoever POSTs a retry: nothing authorizes a retry against a job id,
	// and the world itself is owned by, claimed for, and refused on behalf of
	// this account (refuseIfNotOurs, claimGeneratedWorld) no matter who asks for
	// the repaint. News about a world must name the person who owns it.
	account AuthenticatedAccount
	// recorded is set once this job's world has been filed in the Gazette. A
	// salvaged job is "complete" and still retryable (M17.13), so it has already
	// been news before its retry runs — and a retry may salvage again, so the
	// flag is the direct evidence rather than an inference from Status.
	recorded bool
}

// Handler mounts the title-screen endpoints under /api/.
func (a *WebAPI) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", a.handleHealth)
	mux.HandleFunc("/api/metrics", a.handleMetrics)
	mux.HandleFunc("/api/title", a.handleTitle)
	mux.HandleFunc("/api/title/stream", a.handleTitleStream)
	mux.HandleFunc("/api/worlds", a.handleWorlds)
	mux.HandleFunc("/api/watch/live", a.handleWatchLive)
	mux.HandleFunc("/api/gazette", a.handleGazette)
	mux.HandleFunc("/api/gazette/edition", a.handleGazetteEdition)
	mux.HandleFunc("/api/highscores", a.handleHighScores)
	mux.HandleFunc("/api/help", a.handleHelp)
	mux.HandleFunc("/api/saves", a.handleSaves)
	mux.HandleFunc("/api/restore", a.handleRestore)
	mux.HandleFunc("/api/loadworld", a.handleLoadWorld)
	mux.HandleFunc("/api/generate", a.handleGenerate)
	mux.HandleFunc("/api/museum/search", a.handleMuseumSearch)
	mux.HandleFunc("/api/museum/play", a.handleMuseumPlay)
	mux.HandleFunc("/api/replay/postcard.gif", a.handleReplayPostcard)
	// M32.1: one handler for /api/challenge, /api/challenge/<id> and
	// /api/challenge/ghost. The subtree pattern is what makes an id addressable
	// without a route per catalogue row.
	mux.HandleFunc("/api/challenge", a.handleChallenge)
	mux.HandleFunc("/api/challenge/", a.handleChallenge)
	mux.HandleFunc("/api/preferences", a.handlePreferences)
	mux.HandleFunc("/api/moderation/refusals", a.handleModerationRefusals)
	mux.HandleFunc("/api/moderation/refusals/lift", a.handleModerationLift)
	mux.HandleFunc("/api/moderation/audit", a.handleModerationAudit)
	mux.HandleFunc("/api/auth/me", a.handleAuthMe)
	mux.HandleFunc("/api/auth/logout", a.handleAuthLogout)
	mux.HandleFunc("/api/auth/google/start", a.handleAuthStart)
	mux.HandleFunc("/api/auth/google/callback", a.handleAuthCallback)
	return mux
}

func (a *WebAPI) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "use GET", http.StatusMethodNotAllowed)
		return
	}
	if a.Server == nil {
		writeJSON(w, struct {
			Status string               `json:"status"`
			Build  ServiceBuildSnapshot `json:"build"`
		}{Status: "ok", Build: ServiceBuildSnapshot{Commit: BuildCommitID(), Short: BuildCommitShort()}})
		return
	}
	status := a.Server.ServiceStatus()
	writeJSON(w, struct {
		Status        string               `json:"status"`
		Build         ServiceBuildSnapshot `json:"build"`
		UptimeSeconds int64                `json:"uptimeSeconds"`
		Totals        ServiceTotals        `json:"totals"`
	}{Status: status.Status, Build: status.Build, UptimeSeconds: status.UptimeSeconds, Totals: status.Totals})
}

func (a *WebAPI) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if !a.metricsReader(r) {
		log.Printf("zztgo: refusing service metrics to %s %s from %s (forwarded=%q)",
			r.Method, r.URL.Path, r.RemoteAddr, r.Header.Get("X-Forwarded-For"))
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "use GET", http.StatusMethodNotAllowed)
		return
	}
	if a.Server == nil {
		http.Error(w, "metrics are unavailable", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, a.Server.ServiceStatus())
}

func (a *WebAPI) metricsReader(r *http.Request) bool {
	if requestIsLocalMaintenance(r) {
		return true
	}
	account, authenticated := a.authenticatedAccount(r)
	return a.Server != nil && authenticated && a.Server.isOperator(account.ID)
}

func requestIsLocalMaintenance(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return false
	}
	forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	if forwarded == "" {
		return true
	}
	hops := strings.Split(forwarded, ",")
	last := strings.TrimSpace(hops[len(hops)-1])
	if last == "" {
		return false
	}
	if forwardedHost, _, err := net.SplitHostPort(last); err == nil {
		last = forwardedHost
	}
	ip := net.ParseIP(last)
	return ip != nil && ip.IsLoopback()
}

func (a *WebAPI) handleReplayPostcard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "use GET", http.StatusMethodNotAllowed)
		return
	}
	if a.Server == nil {
		http.Error(w, "replay postcards are unavailable", http.StatusServiceUnavailable)
		return
	}
	id, err := sanitizeReplayID(r.URL.Query().Get("id"))
	if err != nil {
		http.Error(w, "invalid replay id", http.StatusBadRequest)
		return
	}
	opt, err := replayPostcardOptionsFromQuery(r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	client := postcardClientKey(r)
	if !a.Server.postcardLimiter.allow(client, a.Server.clockNow()) {
		http.Error(w, "postcard rate limit: try again later", http.StatusTooManyRequests)
		return
	}

	key := replayPostcardKey{ID: id, Start: opt.StartTick, Ticks: opt.Ticks, Board: opt.BoardID}
	postcard, ok := a.Server.postcardCache.get(key)
	if !ok {
		path, err := replayPath(a.Server.ReplayDir, id)
		if err != nil {
			http.Error(w, "replay postcards are disabled", http.StatusServiceUnavailable)
			return
		}
		f, err := os.Open(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "could not open recording", http.StatusInternalServerError)
			return
		}
		postcard, err = RenderReplayPostcardGIF(f, opt)
		_ = f.Close()
		if err != nil {
			status := http.StatusBadRequest
			if strings.Contains(err.Error(), "unsupported recording version") || strings.Contains(err.Error(), "bad ") {
				status = http.StatusUnprocessableEntity
			}
			http.Error(w, err.Error(), status)
			return
		}
		if _, err := LoadPristineWorld(a.Server.worldsDir(), postcard.WorldName); err != nil {
			http.Error(w, "recorded world is not hosted", http.StatusNotFound)
			return
		}
		a.Server.postcardCache.put(key, postcard)
	}

	replayLink := "/replay/" + url.PathEscape(id)
	playLink := "/play/" + url.PathEscape(postcard.WorldName)
	w.Header().Set("Content-Type", "image/gif")
	w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	w.Header().Set("Link", fmt.Sprintf("<%s>; rel=\"replay\", <%s>; rel=\"play\"", replayLink, playLink))
	w.Header().Set("X-ZZT-Replay", replayLink)
	w.Header().Set("X-ZZT-Play", playLink)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(postcard.GIF)
}

func replayPostcardOptionsFromQuery(q url.Values) (ReplayPostcardOptions, error) {
	start, err := queryInt(q, "start", 0)
	if err != nil || start < 0 {
		return ReplayPostcardOptions{}, fmt.Errorf("start must be a non-negative integer")
	}
	ticks, err := queryInt(q, "ticks", PostcardDefaultTicks)
	if err != nil || ticks <= 0 {
		return ReplayPostcardOptions{}, fmt.Errorf("ticks must be a positive integer")
	}
	if ticks > PostcardMaxTicks {
		return ReplayPostcardOptions{}, fmt.Errorf("ticks exceeds cap %d", PostcardMaxTicks)
	}
	board, err := queryInt(q, "board", 1)
	if err != nil {
		return ReplayPostcardOptions{}, fmt.Errorf("board must be an integer")
	}
	if board < 0 || board > 32767 {
		return ReplayPostcardOptions{}, fmt.Errorf("board out of range")
	}
	return ReplayPostcardOptions{StartTick: start, Ticks: ticks, BoardID: int16(board)}, nil
}

func queryInt(q url.Values, name string, fallback int) (int, error) {
	raw := strings.TrimSpace(q.Get(name))
	if raw == "" {
		return fallback, nil
	}
	return strconv.Atoi(raw)
}

func postcardClientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
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
	Authenticated              bool                      `json:"authenticated"`
	Stored                     bool                      `json:"stored"`
	Color                      string                    `json:"color,omitempty"`
	Hints                      AccountHintPreferences    `json:"hints,omitempty"`
	Profile                    AccountProfilePreferences `json:"profile,omitempty"`
	ShareLocationWithFollowers bool                      `json:"shareLocationWithFollowers,omitempty"`
	FavoriteWorlds             []string                  `json:"favoriteWorlds,omitempty"`
	Comfort                    ComfortPreferences        `json:"comfort,omitempty"`
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
		writeJSON(w, preferencesResponse{Authenticated: true, Stored: stored, Color: prefs.Color, Hints: prefs.Hints, Profile: prefs.Profile, ShareLocationWithFollowers: prefs.ShareLocationWithFollowers, FavoriteWorlds: prefs.FavoriteWorlds, Comfort: prefs.Comfort})
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
			Color                      *string                    `json:"color"`
			Hints                      *AccountHintPreferences    `json:"hints"`
			Profile                    *AccountProfilePreferences `json:"profile"`
			ShareLocationWithFollowers *bool                      `json:"shareLocationWithFollowers"`
			FavoriteWorlds             *[]string                  `json:"favoriteWorlds"`
			FavoriteWorld              *string                    `json:"favoriteWorld"`
			Favorite                   *bool                      `json:"favorite"`
			Comfort                    *ComfortPreferences        `json:"comfort"`
		}
		// Capped like every other body this API decodes (handleGenerate): a
		// profile is still only a handle, a display line and a few bio lines.
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&body); err != nil {
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
		if body.Color != nil {
			prefs.Color = SanitizePlayerColor(*body.Color)
		}
		if body.Hints != nil {
			prefs.Hints.Players = prefs.Hints.Players || body.Hints.Players
			prefs.Hints.Death = prefs.Hints.Death || body.Hints.Death
			prefs.Hints.Chat = prefs.Hints.Chat || body.Hints.Chat
		}
		if body.Profile != nil {
			profile, err := SanitizeAccountProfile(*body.Profile)
			if err != nil {
				http.Error(w, "invalid profile", http.StatusBadRequest)
				return
			}
			prefs.Profile = profile
		}
		if body.ShareLocationWithFollowers != nil {
			prefs.ShareLocationWithFollowers = *body.ShareLocationWithFollowers
		}
		if body.FavoriteWorlds != nil {
			prefs.FavoriteWorlds = a.validFavoriteWorlds(*body.FavoriteWorlds)
		}
		if body.FavoriteWorld != nil {
			safe, err := SanitizeSaveName(*body.FavoriteWorld)
			if err != nil || !a.joinableWorldSet()[safe] {
				http.Error(w, "invalid favorite world", http.StatusBadRequest)
				return
			}
			want := body.Favorite == nil || *body.Favorite
			prefs.FavoriteWorlds = toggleFavoriteWorld(prefs.FavoriteWorlds, safe, want)
		}
		if body.Comfort != nil {
			comfort, err := SanitizeComfortPreferences(*body.Comfort)
			if err != nil {
				http.Error(w, "invalid comfort preferences", http.StatusBadRequest)
				return
			}
			prefs.Comfort = comfort
		}
		if err := a.Server.ChatDB.PutAccountPreferences(account.ID, prefs); err != nil {
			if errors.Is(err, ErrProfileHandleTaken) {
				http.Error(w, "handle already claimed", http.StatusConflict)
				return
			}
			if errors.Is(err, ErrInvalidProfileHandle) || errors.Is(err, ErrInvalidProfileText) {
				http.Error(w, "invalid profile", http.StatusBadRequest)
				return
			}
			if errors.Is(err, ErrInvalidComfort) {
				http.Error(w, "invalid comfort preferences", http.StatusBadRequest)
				return
			}
			http.Error(w, "could not store preferences", http.StatusInternalServerError)
			return
		}
		a.Server.refreshAccountProfile(account.ID, prefs.Profile)
		writeJSON(w, preferencesResponse{Authenticated: true, Stored: true, Color: prefs.Color, Hints: prefs.Hints, Profile: prefs.Profile, ShareLocationWithFollowers: prefs.ShareLocationWithFollowers, FavoriteWorlds: prefs.FavoriteWorlds, Comfort: prefs.Comfort})
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

func (a *WebAPI) validFavoriteWorlds(worlds []string) []string {
	joinable := a.joinableWorldSet()
	if len(joinable) == 0 {
		return nil
	}
	filtered := make([]string, 0, len(worlds))
	for _, world := range worlds {
		safe, err := SanitizeSaveName(world)
		if err != nil || !joinable[safe] {
			continue
		}
		filtered = append(filtered, safe)
	}
	return sanitizeFavoriteWorlds(filtered)
}

func (a *WebAPI) joinableWorldSet() map[string]bool {
	dir, worlds := a.worldDirectoryAndNames()
	entries := WorldListEntriesInDirWithEditors(dir, worlds, nil, nil)
	out := make(map[string]bool, len(entries))
	for _, entry := range entries {
		out[entry.World] = true
	}
	return out
}

func toggleFavoriteWorld(worlds []string, world string, favorite bool) []string {
	current := sanitizeFavoriteWorlds(worlds)
	out := current[:0]
	found := false
	for _, existing := range current {
		if existing == world {
			found = true
			if favorite {
				out = append(out, existing)
			}
			continue
		}
		out = append(out, existing)
	}
	if favorite && !found {
		if len(out) >= MaxFavoriteWorlds {
			copy(out, out[1:])
			out = out[:len(out)-1]
		}
		out = append(out, world)
	}
	return sanitizeFavoriteWorlds(out)
}

// --- the operator console (M21.6) -------------------------------------------
//
// M21.2 shipped refusals and left exactly one hole in them: a refusal is
// addressed by accountID, an account id never reaches another player's browser
// (M21.1), and a refused account is by definition not connected to be picked out
// of a roster — so nothing inside the game can name one to lift it, and lifting
// meant editing saves/refused.json and restarting. That is survivable for
// granting operator status, which nobody does in a hurry, and wrong for a
// mis-aimed refusal, which is exactly when an operator most wants it gone.
//
// So the console is an HTTP surface, and that is the decision the rest follows
// from: the one screen that has to show account ids cannot be a screen inside the
// game. It is served to an operator's own browser or curl, gated on the same
// allowlist the socket actions are gated on, and it deliberately does the two
// things a restart used to do and nothing more — list the standing refusals, lift
// one — plus a read of the audit tail, because "what has been done here" is the
// question somebody opening a moderation console is usually asking.
//
// Imposing a refusal is NOT here. That happens against a connected player from
// the game, where the operator can see who they are acting on; a route that
// refuses an account id typed into a text field is a route that refuses a typo.

// moderationRefusalsResponse is the standing refusals, oldest first. Each carries
// who imposed it, when, and in which world, which is the whole reason the record
// holds more than a set of ids.
type moderationRefusalsResponse struct {
	Refusals []RefusedAccount `json:"refusals"`
}

// moderationLiftResponse reports what the lift did. Was is the record that was
// removed, so an operator who lifted the wrong one has what they need to put it
// back rather than only the id they typed.
type moderationLiftResponse struct {
	Lifted  bool            `json:"lifted"`
	Account string          `json:"account"`
	Was     *RefusedAccount `json:"was,omitempty"`
	Text    string          `json:"text"`
}

// moderationAuditResponse carries the in-memory tail. Tail is reported alongside
// it because the FILE is the record: an operator has to be able to tell "this is
// everything" from "this is the last ModerationAuditTail entries, read
// saves/moderation.jsonl for the rest".
type moderationAuditResponse struct {
	Entries []ModerationAuditEntry `json:"entries"`
	Tail    int                    `json:"tail"`
}

// moderationOperator gates every console route, and writes the denial itself.
//
// A denial is a 404 rather than a 403, and that is a choice: a 403 confirms the
// console exists to anybody who guesses the path, and tells a signed-in
// non-operator that there is an allowlist — which is the first half of learning
// who is on it. The cost is real and worth naming: an operator whose allowlist
// entry is mistyped gets a 404 and may conclude the build has no console. The
// server log below is what answers them, and it is why the log records the
// account that asked.
//
// Denials are logged and deliberately NOT audited. M21.2 records a denied socket
// action because that path is behind chat's rate limiter; this route is behind
// nothing, so auditing its denials would let anyone holding the URL append to the
// operator's own record until it is unreadable. That is the same reasoning that
// keeps refusedAtTheDoor out of the audit.
func (a *WebAPI) moderationOperator(w http.ResponseWriter, r *http.Request) (AuthenticatedAccount, bool) {
	account, authenticated := a.authenticatedAccount(r)
	if a.Server == nil || !authenticated || !a.Server.isOperator(account.ID) {
		log.Printf("zztgo: refusing the operator console to %s %s (authenticated=%v account=%q)",
			r.Method, r.URL.Path, authenticated, account.ID)
		http.NotFound(w, r)
		return AuthenticatedAccount{}, false
	}
	return account, true
}

func (a *WebAPI) handleModerationRefusals(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.moderationOperator(w, r); !ok {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "use GET to list refusals, or POST to /api/moderation/refusals/lift", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, moderationRefusalsResponse{Refusals: a.Server.Refusals.List()})
}

// handleModerationLift is the whole point of the console: the refusal goes away
// now, not at the next restart.
//
// The order is M21.2's, unchanged, because it is the property that makes the
// audit worth keeping: authority first, then the action, then the audit, then the
// operator's confirmation — so there is no outcome an operator can have seen that
// the record does not contain.
func (a *WebAPI) handleModerationLift(w http.ResponseWriter, r *http.Request) {
	operator, ok := a.moderationOperator(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Account string `json:"account"`
	}
	// Capped like every other body this API decodes (handlePreferences): the whole
	// document is one account id.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&body); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}
	accountID := strings.TrimSpace(body.Account)
	if accountID == "" {
		http.Error(w, "name the account whose refusal to lift", http.StatusBadRequest)
		return
	}

	audit := ModerationAuditEntry{
		At:            a.Server.clockNow(),
		Action:        ModerationActionLift,
		Operator:      operator.ID,
		OperatorName:  operator.DisplayName(),
		TargetAccount: accountID,
	}

	rec, lifted, err := a.Server.Refusals.Lift(accountID)
	switch {
	case err != nil:
		// The document did not change, so neither did the server (RefusalStore.Lift
		// puts the entry back). Telling the operator it worked would leave them
		// expecting somebody who is still refused.
		audit.Result = ModerationResultFailed
		audit.Detail = err.Error()
		a.Server.Audit.Record(audit)
		log.Printf("zztgo: lift of the refusal on account %q not recorded: %v", accountID, err)
		http.Error(w, "could not record the lift", http.StatusInternalServerError)
		return

	case !lifted:
		// Not an error. The list an operator read a moment ago is already slightly
		// out of date, and a second operator lifting the same refusal must be told
		// what is true rather than that something broke — submitModeration takes the
		// same view of a target who has already left.
		audit.Result = ModerationResultNoOp
		audit.Detail = "no standing refusal for that account"
		a.Server.Audit.Record(audit)
		writeJSON(w, moderationLiftResponse{
			Account: accountID,
			Text:    "No standing refusal for that account.",
		})
		return
	}

	// The lift carries the refusal's own world and target name, so the two lines
	// read as one story: this account was refused there, by them, and this is who
	// undid it.
	audit.Result = ModerationResultApplied
	audit.TargetName = rec.Name
	audit.World = rec.World
	if rec.By != "" {
		audit.Detail = "lifting a refusal imposed by " + rec.By
	}
	a.Server.Audit.Record(audit)

	writeJSON(w, moderationLiftResponse{
		Lifted:  true,
		Account: accountID,
		Was:     &rec,
		Text:    "Lifted the refusal on " + accountID + ". They can rejoin.",
	})
}

func (a *WebAPI) handleModerationAudit(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.moderationOperator(w, r); !ok {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "use GET", http.StatusMethodNotAllowed)
		return
	}
	entries := a.Server.Audit.Entries()
	// ?limit=N is the last N, because the interesting end of an audit is the
	// recent one. A limit that is not a number, or is bigger than the tail, is the
	// whole tail rather than an error: this is a console an operator drives by
	// hand, and refusing to show them anything over a mistyped query string would
	// be the wrong kind of strict.
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 && n < len(entries) {
			entries = entries[len(entries)-n:]
		}
	}
	writeJSON(w, moderationAuditResponse{Entries: entries, Tail: ModerationAuditTail})
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
		a.generationJobs[jobID] = &generationJob{Status: "running", account: req.Account}
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
	a.recordDream(req.Account, result)
	writeJSON(w, struct {
		World string `json:"world"`
	}{World: result.Name})
}

// recordDream files a finished dream in the Gazette's ledger (M34.1).
//
// It lives here rather than in finishGenerationJob because that function is the
// one site in this path that has the RESULT but not the account that asked for
// it: the job carries status and world, and GenerationRequest carries who. A
// dream that produced no world — a failure, or a salvage that named nothing —
// is not news, and the ledger's own admission would refuse it anyway.
//
// The synchronous path calls this directly, because a request that answers with
// a world has no job to record against. Every asynchronous path goes through
// recordJobDream, which is the same call with the job's once-only guard in
// front of it (M34.1a).
func (a *WebAPI) recordDream(account AuthenticatedAccount, result GenerationResult) {
	if a == nil || a.Server == nil || result.Name == "" {
		return
	}
	a.Server.recordGazette(GazetteKindDream, result.Name, account.ID)
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
	if err == nil {
		a.recordJobDream(id, result)
	}
}

// recordJobDream files an async job's world in the Gazette at most once, no
// matter which of this job's paths finished it (M34.1a). Both callers —
// runGenerationJob and the retry goroutine — call it unconditionally on
// success; the once-only decision lives here rather than at either call site,
// so a third path that finishes a job cannot be the one that forgets, or the
// one that prints the same world twice.
func (a *WebAPI) recordJobDream(id string, result GenerationResult) {
	if a == nil || result.Name == "" {
		// A generation that produced no world is not news, and it must not
		// spend the job's one recording either: a later retry may still name a
		// world.
		return
	}
	a.generationMu.Lock()
	job := a.generationJobs[id]
	if job == nil || job.recorded {
		a.generationMu.Unlock()
		return
	}
	job.recorded = true
	account := job.account
	a.generationMu.Unlock()
	a.recordDream(account, result)
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
		if err == nil {
			// M34.1a: a retry that rescues a job which never landed a world is
			// the moment that world became news. A retry that repaints a
			// salvaged job's stub rooms is not — that world was already news
			// when it was salvaged — and recordJobDream is what tells the two
			// apart, so this call is the same unconditional one runGenerationJob
			// makes.
			a.recordJobDream(id, result)
		}
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
		worldName = a.defaultWorldName()
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
			writeWorldLoadError(w, safeWorld, err)
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

// defaultWorldName is the world a request with no `?world` means: the one this
// server was actually started with. M34.3a: this used to be a second hardcoded
// "TOWN" in each title handler, which M29.1 left stale when it made LOBBY the
// server's default — so the client's very first paint (main.ts sends a bare
// /api/title while its world is still "Untitled") asked for a world the
// deployment may not host at all. serveEditor already defaults this way
// (websocket_server.go); a name that cannot go stale is the point. M34.3b
// brought the last two copies here — handleRestore and handleHighScores, which
// no player reaches unnamed but a direct API caller does.
func (a *WebAPI) defaultWorldName() string {
	if a.Server != nil && a.Server.DefaultInstance != nil && a.Server.DefaultInstance.Name != "" {
		return a.Server.DefaultInstance.Name
	}
	if a.RoomManager != nil {
		return a.RoomManager.WorldIdentity
	}
	return ""
}

// writeWorldLoadError answers a world that could not be opened. A world nobody
// hosts is the client asking for something absent, not a server fault, so it is
// a 404 with a message rather than the 500 this used to be (M34.3a).
func writeWorldLoadError(w http.ResponseWriter, worldName string, err error) {
	if errors.Is(err, os.ErrNotExist) {
		http.Error(w, "no such world: "+worldName, http.StatusNotFound)
		return
	}
	http.Error(w, "failed to load world: "+err.Error(), http.StatusInternalServerError)
}

// handleTitle renders board 0 the way ZZT's title screen shows it.
func (a *WebAPI) handleTitle(w http.ResponseWriter, r *http.Request) {
	worldName := r.URL.Query().Get("world")
	if worldName == "" {
		worldName = a.defaultWorldName()
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
			writeWorldLoadError(w, safeWorld, err)
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
		worldName = a.defaultWorldName()
	}
	safeWorld, err := SanitizeSaveName(worldName)
	if err != nil {
		http.Error(w, "invalid world name", http.StatusBadRequest)
		return
	}
	inst, err := a.Server.GetOrCreateInstance(safeWorld)
	if err != nil {
		writeWorldLoadError(w, safeWorld, err)
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
	dir, worlds := a.worldDirectoryAndNames()

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

	entries := WorldListEntriesInDirWithEditors(dir, worlds, counts, editorCounts)
	playCounts := map[string]int(nil)
	if a.Server != nil && a.Server.Activity != nil {
		playCounts = a.Server.Activity.Counts()
	}
	account, authenticated := a.authenticatedAccount(r)
	var prefs AccountPreferences
	if authenticated {
		prefs, _ = a.storedPreferences(account.ID)
	}
	favoriteSet := make(map[string]bool, len(prefs.FavoriteWorlds))
	for _, world := range prefs.FavoriteWorlds {
		favoriteSet[world] = true
	}
	if authenticated && a.Server != nil {
		friendsByWorld := a.Server.friendPresenceByWorld(account.ID)
		for i := range entries {
			entries[i].FriendsHere = friendsByWorld[entries[i].World]
		}
	}
	for i := range entries {
		entries[i].PlayCount = playCounts[entries[i].World]
		if authenticated {
			entries[i].Favorite = favoriteSet[entries[i].World]
		}
		entries[i].Thumbnail = a.titleThumbnail(dir, entries[i].World)
	}
	shelves := buildWorldShelves(entries, authenticated, prefs.FavoriteWorlds)
	writeJSON(w, struct {
		Worlds  []WorldListEntry `json:"worlds"`
		Shelves []WorldShelf     `json:"shelves,omitempty"`
	}{Worlds: entries, Shelves: shelves})
}

func (a *WebAPI) worldDirectoryAndNames() (string, []string) {
	dir := "."
	if a.Server != nil {
		dir = a.Server.worldsDir()
	} else if E != nil && E.LoadedGameFileName != "" {
		dir = filepath.Dir(E.LoadedGameFileName)
	}
	worlds := ListWorlds(dir)
	if len(worlds) == 0 && a.RoomManager != nil {
		worlds = []string{a.RoomManager.WorldName()}
	}
	return dir, worlds
}

func buildWorldShelves(entries []WorldListEntry, authenticated bool, favorites []string) []WorldShelf {
	byWorld := make(map[string]WorldListEntry, len(entries))
	for _, entry := range entries {
		byWorld[entry.World] = entry
	}
	var shelves []WorldShelf
	addShelf := func(id, title string, worlds []string) {
		seen := make(map[string]struct{}, len(worlds))
		out := make([]string, 0, len(worlds))
		for _, world := range worlds {
			if _, ok := byWorld[world]; !ok {
				continue
			}
			if _, dup := seen[world]; dup {
				continue
			}
			seen[world] = struct{}{}
			out = append(out, world)
		}
		if len(out) > 0 {
			shelves = append(shelves, WorldShelf{ID: id, Title: title, Worlds: out})
		}
	}

	if authenticated {
		addShelf("favorites", "Favorites", favorites)
	}

	active := make([]string, 0)
	for _, entry := range entries {
		if len(entry.FriendsHere) > 0 || entry.Players > 0 {
			active = append(active, entry.World)
		}
	}
	sort.SliceStable(active, func(i, j int) bool {
		a, b := byWorld[active[i]], byWorld[active[j]]
		if len(a.FriendsHere) != len(b.FriendsHere) {
			return len(a.FriendsHere) > len(b.FriendsHere)
		}
		return a.Players > b.Players
	})
	addShelf("active", "Friends here / Active now", active)

	played := make([]string, 0)
	for _, entry := range entries {
		if entry.PlayCount > 0 {
			played = append(played, entry.World)
		}
	}
	sort.SliceStable(played, func(i, j int) bool {
		a, b := byWorld[played[i]], byWorld[played[j]]
		if a.PlayCount != b.PlayCount {
			return a.PlayCount > b.PlayCount
		}
		return strings.ToUpper(a.Title) < strings.ToUpper(b.Title)
	})
	addShelf("played", "Most played", played)

	// Classics sit above Recent dreams (owner 2026-08-11): the archive is what
	// the picker is for, and a day of dreaming used to push it under the fold.
	classics := make([]string, 0)
	for _, entry := range entries {
		if entry.Kind == WorldKindClassic {
			classics = append(classics, entry.World)
		}
	}
	addShelf("classics", "Classics", classics)

	dreams := make([]string, 0)
	for _, entry := range entries {
		if entry.Kind == WorldKindDreamed {
			dreams = append(dreams, entry.World)
		}
	}
	addShelf("dreams", "Recent dreams", dreams)
	return shelves
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
		worldName = a.defaultWorldName()
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
			writeWorldLoadError(w, safeWorld, err)
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

const maxTitleThumbnailCacheEntries = 128

func (a *WebAPI) titleThumbnail(dir, world string) *WorldTitleThumbnail {
	key, ok := titleThumbnailKey(dir, world)
	if !ok {
		return nil
	}
	a.thumbnailMu.Lock()
	if cached, hit := a.thumbnailCache[key]; hit {
		a.thumbnailMu.Unlock()
		thumb := cached
		return &thumb
	}
	a.thumbnailMu.Unlock()

	pristine, err := LoadPristineWorld(dir, world)
	if err != nil {
		return nil
	}
	cells, ok := safeTitleScreenCells(pristine)
	if !ok {
		return nil
	}
	thumb := WorldTitleThumbnail{Key: key, Cells: titleThumbnailCells(cells)}

	a.thumbnailMu.Lock()
	if a.thumbnailCache == nil {
		a.thumbnailCache = make(map[string]WorldTitleThumbnail)
	}
	if _, exists := a.thumbnailCache[key]; !exists {
		a.thumbnailOrder = append(a.thumbnailOrder, key)
	}
	a.thumbnailCache[key] = thumb
	for len(a.thumbnailOrder) > maxTitleThumbnailCacheEntries {
		old := a.thumbnailOrder[0]
		copy(a.thumbnailOrder, a.thumbnailOrder[1:])
		a.thumbnailOrder = a.thumbnailOrder[:len(a.thumbnailOrder)-1]
		delete(a.thumbnailCache, old)
	}
	a.thumbnailMu.Unlock()
	return &thumb
}

func titleThumbnailKey(dir, world string) (string, bool) {
	safe, err := SanitizeSaveName(world)
	if err != nil || dir == "" {
		return "", false
	}
	path := filepath.Join(dir, safe+".ZZT")
	stat, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	metaPart := "nometa"
	if metaStat, err := os.Stat(worldMetaPath(dir, safe)); err == nil {
		metaPart = fmt.Sprintf("%d:%d", metaStat.Size(), metaStat.ModTime().UnixNano())
	}
	return fmt.Sprintf("%s:%d:%d:%s", safe, stat.Size(), stat.ModTime().UnixNano(), metaPart), true
}

func safeTitleScreenCells(world TWorld) (cells []ScreenCell, ok bool) {
	defer func() {
		if recover() != nil {
			cells = nil
			ok = false
		}
	}()
	return TitleScreenCells(world), true
}

func titleThumbnailCells(cells []ScreenCell) []ScreenCell {
	out := make([]ScreenCell, 0, 20*9)
	for _, cell := range cells {
		if cell.X%3 != 0 || cell.Y%3 != 0 {
			continue
		}
		out = append(out, ScreenCell{
			X:     cell.X / 3,
			Y:     cell.Y / 3,
			Ch:    cell.Ch,
			Color: cell.Color,
		})
	}
	return out
}
