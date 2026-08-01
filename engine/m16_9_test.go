package zztgo

// M16.9 — the real-browser visual parity harness.
//
// WHAT THIS IS. A pinned Playwright Chromium drives the *built* client against
// the production server objects, and every golden is read back out of the
// browser's own <canvas> backing store — never from Go's PNG renderer
// (render_png.go), which shares this repo's font and palette tables and would
// therefore agree with the client's bugs. The comparison unit is the CP437
// cell, decoded from the canvas pixels by matching each 8x14 block against the
// client's own font atlas (web/test/lib/canvas.mjs), so a one-cell glyph or
// colour regression names the cell rather than reporting "N pixels differ".
//
// WHY THE SERVER RUNS IN-PROCESS. Goldens of a *running* game are only stable
// if the tick is stable: objects move, energizers blink, and the title board
// animates, all on the server's 110ms wall-clock ticker. So this harness wires
// up exactly what cmd/zzt-server's main() wires up — the same WebSocketServer,
// the same WebAPI mux, the same web/dist file server — and then simply does not
// start the ticker. Ticks come from a control listener on a second port that
// only this test binary serves; the browser cannot see it and no production
// file learns it exists. What is deliberately NOT covered here is main()'s flag
// parsing and process lifecycle; M16.11 and M16.19 drive the real binary.
//
// WHY THAT ALSO SETTLES M16.11's CARRIED-OVER DoD CLAUSE. "The acceptance-world
// run is deterministic and catches a client/server tick-order change" was left
// unmet by M16.11 because real key-hold timing decides how many ticks a held
// arrow spans (NOTES.md M18.0b). Here the browser still produces every input —
// nothing is injected server-side — but a tick is only taken once the input
// frame the browser sent for it has arrived (`await` on /control/step), and the
// page clock is a fake one, so the 55ms sampler fires only when the script says
// so. One browser frame, one tick. TestM169TickLockedAcceptanceRun replays a
// committed route and asserts the resulting per-room StateHashes byte for byte.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	m169World      = "GOLDEN"
	m169StartBoard = 1
	// The glyph and colour sweeps live in the top half of "Golden Showcase",
	// 56 tiles per row starting at tile column 2. Rows 2-6 carry CP437 0..255
	// as Text-White tiles; rows 8-12 carry DOS attributes 0x00..0xFF on the
	// Normal wall glyph (0xB2), which shows foreground and background at once.
	m169SweepWidth    = 56
	m169SweepX        = 2
	m169GlyphSweepY   = 2
	m169ColorSweepY   = 8
	m169SweepCapacity = 256
)

// ---------------------------------------------------------------------------
// The golden world
// ---------------------------------------------------------------------------

// m169GoldenWorld compiles fixtures/golden.zwd and paints the two sweeps into
// "Golden Showcase". The sweeps are generated rather than authored because ZWD
// maps one legend entry per grid character: 512 distinct tiles cannot be spelled
// in a 60x25 ASCII grid, and hand-listing them would be less readable than the
// two loops below, not more.
func m169GoldenWorld(t *testing.T) TWorld {
	t.Helper()

	src, err := os.ReadFile(filepath.Join("..", "fixtures", "golden.zwd"))
	if err != nil {
		t.Fatalf("read fixtures/golden.zwd: %v", err)
	}
	world, err := CompileZWDWorld(string(src))
	if err != nil {
		t.Fatalf("CompileZWDWorld(fixtures/golden.zwd): %v", err)
	}

	e := NewEngine()
	e.Headless = true
	e.World = world
	e.BoardOpen(m169StartBoard)
	if e.Board.Name != "Golden Showcase" {
		t.Fatalf("board %d is %q, want \"Golden Showcase\" — fixtures/golden.zwd changed shape", m169StartBoard, e.Board.Name)
	}
	for i := 0; i < m169SweepCapacity; i++ {
		gx := int16(m169SweepX + i%m169SweepWidth)
		gy := int16(m169GlyphSweepY + i/m169SweepWidth)
		// A text tile's Color field IS the character code (TileToColorAndChar,
		// game.go:318) — this is the only way to show CP437 0x00..0xFF without
		// 256 stats.
		e.Board.Tiles[gx][gy] = TTile{Element: E_TEXT_WHITE, Color: byte(i)}

		cx := int16(m169SweepX + i%m169SweepWidth)
		cy := int16(m169ColorSweepY + i/m169SweepWidth)
		e.Board.Tiles[cx][cy] = TTile{Element: E_NORMAL, Color: byte(i)}
	}
	e.BoardClose()
	return e.World
}

// ---------------------------------------------------------------------------
// The harness: production server objects, test-driven ticks
// ---------------------------------------------------------------------------

type m169Harness struct {
	t *testing.T
	// worldName is the picker/instance name of the world being hosted. It is a
	// field rather than the m169World constant because M16.10 hosts CONTROL on
	// this same harness (engine/m16_10_test.go).
	worldName  string
	server     *WebSocketServer
	ctx        context.Context
	cancel     context.CancelFunc
	baseURL    string
	controlURL string

	// The directories the server was given. M16.14 reads the published .ZZT and
	// its access sidecar straight off disk, which is where the ownership rules
	// it certifies actually live.
	rootDir   string
	savesDir  string
	worldsDir string
	// auth is set by the M16.14 option below; nil keeps the server anonymous,
	// which is what M16.9/M16.10/M16.13 want.
	auth *AuthService

	stepMu sync.Mutex
	ticks  int
}

func m169NewHarness(t *testing.T) *m169Harness {
	t.Helper()
	return m169NewHarnessFor(t, m169World, m169GoldenWorld(t))
}

// m169HarnessOption customizes the production objects after they are built and
// before either listener starts serving. M16.14 uses it to give the same
// WebSocketServer and WebAPI an AuthService, so a browser can sign in the way
// the product does; nothing else about the harness changes. h.baseURL and
// h.controlURL are already filled in when an option runs — both ports are bound
// first — so an option may use them, and it is the ONLY safe place to write
// anything a handler goroutine will read (M16.14c).
type m169HarnessOption func(h *m169Harness, server *WebSocketServer, api *WebAPI)

// m169NewHarnessFor hosts one world on the production server objects with the
// tick loop under test control. Everything a browser can see is production; the
// control listener on the second port is served only by this test binary.
func m169NewHarnessFor(t *testing.T, worldName string, world TWorld, options ...m169HarnessOption) *m169Harness {
	t.Helper()
	m169RequireBrowserHarness(t)
	m169RequireClientBuild(t)

	rootDir := t.TempDir()
	savesDir := filepath.Join(rootDir, "saves")
	worldsDir := filepath.Join(rootDir, "worlds")
	if err := os.MkdirAll(savesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worldsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The world picker lists .ZZT files in -worlds, so the browser can only
	// reach this world through the production picker if the file is really there.
	m169WriteWorldFile(t, world, filepath.Join(worldsDir, worldName+".ZZT"))
	// The client's very first /api/title call asks for its default world, TOWN,
	// before the player has chosen anything. Without it the browser console
	// carries a 500 the suite would either have to whitelist or ignore — which
	// is what a clean clone got, because this used to read the gitignored
	// engine/TOWN.ZZT and copy it only when it happened to be there (M16.20).
	if err := os.WriteFile(filepath.Join(worldsDir, "TOWN.ZZT"), committedTownBytes(t), 0o644); err != nil {
		t.Fatal(err)
	}

	server := NewWebSocketServer(world, m169StartBoard)
	server.SavesDir = savesDir
	server.WorldsDir = worldsDir
	server.RoomManager.HighScorePath = filepath.Join(rootDir, worldName+".HI")

	api := &WebAPI{
		RoomManager: server.RoomManager,
		World:       world,
		SavesDir:    savesDir,
		Server:      server,
	}

	ctx, cancel := context.WithCancel(context.Background())
	h := &m169Harness{
		t: t, worldName: worldName, server: server, ctx: ctx, cancel: cancel,
		rootDir: rootDir, savesDir: savesDir, worldsDir: worldsDir,
	}
	// Bind both ports before anything is served, so an option that needs the
	// harness's own URLs (M16.14 hands the AuthService absolute IdP endpoints)
	// can write them while this goroutine is still the only one running. Filling
	// them in after the accept loops had started was a data race (M16.14c).
	baseListener := m169Listen(t)
	controlListener := m169Listen(t)
	h.baseURL = m169URL(baseListener)
	h.controlURL = m169URL(controlListener)

	for _, option := range options {
		option(h, server, api)
	}

	mux := http.NewServeMux()
	mux.Handle("/ws", server)
	mux.Handle("/api/", api.Handler())
	mux.Handle("/", http.FileServer(http.Dir(m169ClientDir())))

	m169ServeOn(t, baseListener, mux)
	m169ServeOn(t, controlListener, h.controlMux())

	t.Cleanup(func() {
		cancel()
		server.CloseRecorders()
	})
	return h
}

// m169Listen binds a loopback port without accepting on it yet. Splitting bind
// from serve is what lets the harness know its own URLs before any handler
// goroutine exists (M16.14c).
func m169Listen(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	// Serve's Shutdown closes the listener too; this only matters when the
	// harness fails between binding and serving.
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func m169URL(l net.Listener) string { return "http://" + l.Addr().String() }

// m169ServeOn starts an http.Server on an already-bound listener.
func m169ServeOn(t *testing.T, l net.Listener, handler http.Handler) {
	t.Helper()
	srv := &http.Server{Handler: handler}
	go func() {
		if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Logf("http server stopped: %v", err)
		}
	}()
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	})
}

func m169WriteWorldFile(t *testing.T, world TWorld, path string) {
	t.Helper()
	e := NewEngine()
	e.Headless = true
	e.World = world
	e.BoardOpen(m169StartBoard)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := e.worldWriteTo(f); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func m169ClientDir() string { return filepath.Join("web", "dist") }

// The two environment variables that decide whether the real-browser suites
// run (owner decision 2026-08-01).
//
// The eleven Playwright suites live inside `go test ./...`, so before this every
// one-line engine change paid 7-10 minutes of real browsers, and the race gate
// paid them a second time for no finding — the races that matter are in the
// server, and the wire-level tests cover those. They are now OPT-IN for everyday
// work and MANDATORY for certification:
//
//	(neither set)                      declared skip — the fast everyday run
//	ZZT_BROWSER=1                      run them; an absent harness still skips
//	ZZT_PARITY_REQUIRE_BROWSER=1       run them; an absent harness is a FAILURE
//
// This is not the silent-skip hole M16.20 closed. The certification run sets the
// second variable, `cmd/zzt-parity` records every skipped test by name with the
// reason it printed, and a skip that does not declare itself blocks
// certification — so a run that did not execute these suites cannot certify, and
// the report says which ones sat out.
const (
	browserOptInEnv   = "ZZT_BROWSER"
	browserRequireEnv = "ZZT_PARITY_REQUIRE_BROWSER"
)

func browserSuitesRequired() bool { return os.Getenv(browserRequireEnv) != "" }
func browserSuitesRequested() bool {
	return browserSuitesRequired() || os.Getenv(browserOptInEnv) != ""
}

// m169RequireBrowserHarness gates every real-browser suite. It skips rather than
// fails when the harness is absent or the suites were not asked for; under the
// certification run an absent harness is a hole in the claim, not an
// environment fact, and m169BrowserAbsent fails instead.
func m169RequireBrowserHarness(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("declared skip: browser suites do not run in short mode")
	}
	if !browserSuitesRequested() {
		t.Skipf("declared skip: the real-browser suites are opt-in — set %s=1, or run `make certify`, which requires them", browserOptInEnv)
	}
	if _, err := os.Stat(filepath.Join("web", "node_modules", "playwright")); err != nil {
		m169BrowserAbsent(t, "browser harness unavailable: run `npm ci` in engine/web (and `npx playwright install chromium`)")
	}
}

// m169BrowserAbsent decides what an absent browser harness means. Ordinarily it
// is an environment fact and the suite skips: a checkout that never asked for a
// browser should not go red. Under the certification run (task M16.20) it is a
// hole in the claim — `make parity` installs the pinned engines before the Go
// gates precisely so these suites run, and a clean clone that certified itself
// with every browser suite silently skipped is the failure M16.20 exists to
// prevent — so it fails instead.
func m169BrowserAbsent(t *testing.T, reason string) {
	t.Helper()
	if browserSuitesRequired() {
		t.Fatalf("%s (%s is set: the certification run requires the real browser, not a skip)", reason, browserRequireEnv)
	}
	t.Skipf("declared skip: %s", reason)
}

// m169RequireClientBuild builds web/dist when it is missing. Keyed on
// index.html, the file the server actually serves: an empty or partial dist
// makes every page 404, which a browser test can mistake for a working client
// (M18.0a's failure).
func m169RequireClientBuild(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(m169ClientDir(), "index.html")); err == nil {
		return
	}
	cmd := exec.Command("npm", "--prefix", "web", "run", "build")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("npm run build failed: %v\n%s", err, out)
	}
}

// ---------------------------------------------------------------------------
// The control listener (test binary only)
// ---------------------------------------------------------------------------

type m169Await struct {
	PlayerID *PlayerID `json:"playerId,omitempty"`
	DeltaX   int16     `json:"dx"`
	DeltaY   int16     `json:"dy"`
	Key      byte      `json:"key"`
	Shift    bool      `json:"shift"`
}

type m169StepRequest struct {
	N         int        `json:"n"`
	Await     *m169Await `json:"await,omitempty"`
	TimeoutMs int        `json:"timeoutMs"`
}

type m169PlayerState struct {
	ID      PlayerID `json:"id"`
	BoardID int16    `json:"boardId"`
	StatID  int16    `json:"statId"`
	X       int16    `json:"x"`
	Y       int16    `json:"y"`
	Health  int16    `json:"health"`
	Score   int16    `json:"score"`
	// Ammo is the server's own count of shots left. The sidebar carries the same
	// number, but the sidebar is a picture of a diff that has landed — so a script
	// that wants to know whether a shot FIRED, as distinct from whether its frame
	// has been painted yet, has to ask here (M16.10a).
	Ammo int16 `json:"ammo"`
	// ScrollOpen is the room-level read freeze: a player with a scroll open
	// cannot move until the client's reply lands (RoomManager.SubmitScrollReply).
	// The browser script waits on it rather than guessing when the reply arrived.
	ScrollOpen bool `json:"scrollOpen"`
}

type m169StateResponse struct {
	Ticks int `json:"ticks"`
	// Hashes are hex STRINGS, not numbers: a uint64 StateHash does not survive
	// JSON.parse in the browser script (a double loses the low bits), and a
	// silently rounded hash would compare equal to a different world.
	Hashes  map[string]string `json:"hashes"`
	Players []m169PlayerState `json:"players"`
	Pending []string          `json:"pending"`
}

func (h *m169Harness) controlMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/control/state", func(w http.ResponseWriter, r *http.Request) {
		h.stepMu.Lock()
		defer h.stepMu.Unlock()
		writeJSON(w, h.state())
	})
	mux.HandleFunc("/control/step", h.handleStep)
	// M16.13 adds its editor-session routes here rather than standing up a
	// second listener; they are defined in engine/m16_13_test.go. M16.14 adds
	// the collaboration routes and its hermetic identity provider the same way
	// (engine/m16_14_test.go).
	h.editorControlRoutes(mux)
	h.collabControlRoutes(mux)
	// M16.16 adds the chat-record route its auth/Museum journey reads
	// (engine/m16_16_test.go).
	h.museumControlRoutes(mux)
	return mux
}

func (h *m169Harness) handleStep(w http.ResponseWriter, r *http.Request) {
	var req m169StepRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.N <= 0 {
		req.N = 1
	}
	if req.TimeoutMs <= 0 {
		req.TimeoutMs = 10000
	}

	h.stepMu.Lock()
	defer h.stepMu.Unlock()

	for i := 0; i < req.N; i++ {
		if req.Await != nil {
			if err := h.waitForInput(*req.Await, time.Duration(req.TimeoutMs)*time.Millisecond); err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
		} else if pending := h.pendingNonZero(); len(pending) > 0 {
			// A step that expected no input found one: the script and the
			// browser have lost sync, and every later tick would be off by one.
			// Failing here names the frame instead of leaving a hash mismatch.
			http.Error(w, "unexpected pending input at an idle step: "+strings.Join(pending, ", "), http.StatusConflict)
			return
		}
		h.server.Tick(h.ctx)
		h.ticks++
	}
	writeJSON(w, h.state())
}

// waitForInput blocks until the browser's input frame for this tick has landed
// in the instance's pending map. This is the whole tick-locking mechanism: the
// input still comes from a real keystroke in a real browser, but the tick that
// consumes it is taken only once it has arrived.
func (h *m169Harness) waitForInput(want m169Await, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		inst := h.instance()
		inst.mu.Lock()
		var (
			found   bool
			pending []string
		)
		for playerID, input := range inst.Inputs {
			pending = append(pending, fmt.Sprintf("player %d dx=%d dy=%d key=%d shift=%v",
				playerID, input.DeltaX, input.DeltaY, input.Key, input.Shift))
			if want.PlayerID != nil && *want.PlayerID != playerID {
				continue
			}
			if input.DeltaX == want.DeltaX && input.DeltaY == want.DeltaY &&
				input.Key == want.Key && input.Shift == want.Shift {
				found = true
			}
		}
		inst.mu.Unlock()
		if found {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for input dx=%d dy=%d key=%d shift=%v; pending: [%s]",
				timeout, want.DeltaX, want.DeltaY, want.Key, want.Shift, strings.Join(pending, "; "))
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (h *m169Harness) pendingNonZero() []string {
	inst := h.instance()
	inst.mu.Lock()
	defer inst.mu.Unlock()
	var pending []string
	for playerID, input := range inst.Inputs {
		if input.DeltaX == 0 && input.DeltaY == 0 && input.Key == 0 && !input.Shift {
			continue
		}
		pending = append(pending, fmt.Sprintf("player %d dx=%d dy=%d key=%d", playerID, input.DeltaX, input.DeltaY, input.Key))
	}
	return pending
}

func (h *m169Harness) instance() *WorldInstance {
	h.server.mu.Lock()
	defer h.server.mu.Unlock()
	if inst := h.server.Instances[h.worldName]; inst != nil {
		return inst
	}
	return h.server.DefaultInstance
}

func (h *m169Harness) state() m169StateResponse {
	state := m169StateResponse{Ticks: h.ticks, Hashes: map[string]string{}}
	inst := h.instance()
	rm := inst.RoomManager
	for boardID, hash := range rm.RoomStateHashes() {
		state.Hashes[fmt.Sprintf("%d", boardID)] = fmt.Sprintf("%016x", hash)
	}
	// pendingNonZero takes inst.mu itself, so it must not be called while this
	// function holds it — inst.mu is not reentrant.
	state.Pending = append(state.Pending, h.pendingNonZero()...)
	inst.mu.Lock()
	var ids []PlayerID
	for playerID := range inst.Clients {
		ids = append(ids, playerID)
	}
	inst.mu.Unlock()
	// Sorted so the JSON is stable run to run: a JS-visible ordering that
	// depended on Go map iteration would be a determinism hole of our own.
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j] < ids[j-1]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
	for _, playerID := range ids {
		entry := m169PlayerState{ID: playerID}
		inst.mu.Lock()
		if p := rm.players[playerID]; p != nil {
			entry.ScrollOpen = p.scrollOpen
		}
		inst.mu.Unlock()
		if boardID, statID, ok := rm.PlayerLocation(playerID); ok {
			entry.BoardID = boardID
			entry.StatID = statID
			if room, ok := rm.Room(boardID); ok && statID >= 0 && statID <= room.Engine.Board.StatCount {
				stat := room.Engine.Board.Stats[statID]
				entry.X = int16(stat.X)
				entry.Y = int16(stat.Y)
				ps := room.Engine.PlayerFor(statID)
				entry.Health = ps.Health
				entry.Score = ps.Score
				entry.Ammo = ps.Ammo
			}
		}
		state.Players = append(state.Players, entry)
	}
	return state
}

// ---------------------------------------------------------------------------
// Running the browser scripts
// ---------------------------------------------------------------------------

// runBrowserScript runs one Playwright script under engine/web with the harness
// URLs in its environment, and returns its stdout. Artifacts (actual/expected/
// diff PNGs, the cell diff, traces) are written by the script into
// web/test-results, which CI uploads on failure.
func (h *m169Harness) runBrowserScript(script string, extraEnv ...string) string {
	h.t.Helper()
	cmd := exec.Command("node", filepath.Join("test", script))
	cmd.Dir = "web"
	cmd.Env = append(os.Environ(),
		"BASE_URL="+h.baseURL,
		"CONTROL_URL="+h.controlURL,
		"GOLDEN_DIR="+m169GoldenDir(h.t),
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("%s failed: %v\n--- script output ---\n%s\n--- artifacts ---\n%s",
			script, err, out, filepath.Join("engine", "web", "test-results"))
	}
	return string(out)
}

func m169GoldenDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "fixtures", "browser-goldens"))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// ---------------------------------------------------------------------------
// The tests
// ---------------------------------------------------------------------------

// TestM169BrowserCanvasGoldens is the golden suite: title, board + authentic
// sidebar, the CP437 and DOS-colour sweeps, text tiles, dark and torch-lit
// boards, the energizer blink, the player-identity overlay, every CP437 window
// family, and the board-transition end state — each captured from the real
// canvas and compared cell by cell. It also proves the harness itself: a
// deliberately corrupted single cell must produce a named diff and three PNGs.
func TestM169BrowserCanvasGoldens(t *testing.T) {
	h := m169NewHarness(t)
	out := h.runBrowserScript("visual_golden.test.mjs")
	t.Logf("browser golden suite:\n%s", out)
}

// TestM169TickLockedAcceptanceRun is M16.11's carried-over DoD clause: a browser
// journey whose every input lands on a named tick, so the run has one StateHash
// rather than one per timing accident. The route and its expected per-room
// hashes are committed in fixtures/browser-goldens/tick-locked-run.json.
//
// A client/server tick-order change reddens this: move input application to
// after the step, change what the client puts on the wire for a held arrow, or
// drop a frame, and the hashes move.
func TestM169TickLockedAcceptanceRun(t *testing.T) {
	h := m169NewHarness(t)
	out := h.runBrowserScript("tick_locked.test.mjs")
	t.Logf("tick-locked run:\n%s", out)

	got := m169ObservedTickRun(t)
	// The script reports what the control listener told it; this is the harness
	// checking the same thing from its own side, so a script that merely printed
	// plausible numbers would not agree with the server it claims to have driven.
	final := h.state()
	if len(got.Checkpoints) == 0 {
		t.Fatalf("the browser script recorded no checkpoints")
	}
	last := got.Checkpoints[len(got.Checkpoints)-1]
	if last.Ticks != final.Ticks {
		t.Errorf("the script's last checkpoint is tick %d, the server is at %d", last.Ticks, final.Ticks)
	}
	for board, hash := range final.Hashes {
		if last.Hashes[board] != hash {
			t.Errorf("board %s: server hash %s, script reported %s", board, hash, last.Hashes[board])
		}
	}

	if os.Getenv("TICK_RUN_UPDATE") != "" {
		m169SaveTickRun(t, got)
		t.Logf("re-recorded %s — justify the change in the commit message", m169TickRunPath(t))
		return
	}

	want := m169LoadTickRun(t)
	if got.Ticks != want.Ticks {
		t.Errorf("the run took %d ticks, want %d — the route or the tick lock changed", got.Ticks, want.Ticks)
	}
	if len(got.Checkpoints) != len(want.Checkpoints) {
		t.Fatalf("the run has %d checkpoints, want %d", len(got.Checkpoints), len(want.Checkpoints))
	}
	for i, wantPoint := range want.Checkpoints {
		gotPoint := got.Checkpoints[i]
		if gotPoint.Label != wantPoint.Label {
			t.Errorf("checkpoint %d is %q, want %q", i, gotPoint.Label, wantPoint.Label)
			continue
		}
		if gotPoint.Ticks != wantPoint.Ticks {
			t.Errorf("checkpoint %q is at tick %d, want %d", wantPoint.Label, gotPoint.Ticks, wantPoint.Ticks)
		}
		if !m169SameHashes(gotPoint.Hashes, wantPoint.Hashes) {
			t.Errorf("checkpoint %q hashes %v, want %v", wantPoint.Label, gotPoint.Hashes, wantPoint.Hashes)
		}
	}
	if t.Failed() {
		t.Logf("observed run: %s", m169MustJSON(got))
		t.Log("a differing hash means the simulation this route produces changed. If that was intended, " +
			"re-record with TICK_RUN_UPDATE=1 and justify it in the commit message (CLAUDE.md rule 3).")
	}
}

type m169TickCheckpoint struct {
	Label  string            `json:"label"`
	Ticks  int               `json:"ticks"`
	Hashes map[string]string `json:"hashes"`
}

type m169TickRun struct {
	Note        string               `json:"note,omitempty"`
	Ticks       int                  `json:"ticks"`
	Checkpoints []m169TickCheckpoint `json:"checkpoints"`
}

func m169SameHashes(got, want map[string]string) bool {
	if len(got) != len(want) {
		return false
	}
	for board, hash := range want {
		if got[board] != hash {
			return false
		}
	}
	return true
}

func m169TickRunPath(t *testing.T) string {
	return filepath.Join(m169GoldenDir(t), "tick-locked-run.json")
}

// m169ObservedTickRun reads what the browser script recorded for this run.
func m169ObservedTickRun(t *testing.T) m169TickRun {
	t.Helper()
	reportPath := filepath.Join("web", "test-results", "tick-locked-run.json")
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("the browser script wrote no run report at %s: %v", reportPath, err)
	}
	var run m169TickRun
	if err := json.Unmarshal(data, &run); err != nil {
		t.Fatalf("parse %s: %v", reportPath, err)
	}
	return run
}

func m169LoadTickRun(t *testing.T) m169TickRun {
	t.Helper()
	data, err := os.ReadFile(m169TickRunPath(t))
	if err != nil {
		t.Fatalf("read the committed tick-locked run: %v (record it with TICK_RUN_UPDATE=1)", err)
	}
	var run m169TickRun
	if err := json.Unmarshal(data, &run); err != nil {
		t.Fatalf("parse the committed tick-locked run: %v", err)
	}
	return run
}

func m169SaveTickRun(t *testing.T, run m169TickRun) {
	t.Helper()
	run.Note = "M16.9's tick-locked acceptance run: the per-room StateHash at each checkpoint of the route in " +
		"engine/web/test/tick_locked.test.mjs, driven through a real browser with one input frame per server " +
		"tick. Re-record only with TICK_RUN_UPDATE=1, and justify the change (CLAUDE.md rule 3)."
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m169TickRunPath(t), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func m169MustJSON(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%#v", v)
	}
	return string(data)
}
