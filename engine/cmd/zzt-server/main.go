package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/benhoyt/zztgo"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	worldName := flag.String("world", "TOWN", "world basename to load")
	boardID := flag.Int("board", 1, "default board id")
	webDir := flag.String("web", "web/dist", "built browser client directory")
	helpDir := flag.String("help", ".", "directory holding the .HLP help files")
	savesDir := flag.String("saves", "saves", "directory for saved-game snapshots; empty disables saving")
	worldsDir := flag.String("worlds", ".", "directory holding hosted .ZZT worlds; where the editor publishes")
	autosaveSecs := flag.Int("autosave", 60, "seconds between autosaves of occupied rooms; 0 disables")
	fresh := flag.Bool("fresh", false, "skip restoring autosaves at boot for a deliberately clean start")
	recordDir := flag.String("record", "", "directory for deterministic session recordings; empty disables recording")
	shutdownGrace := flag.Duration("shutdown-grace", 60*time.Second, "on SIGINT/SIGTERM, warn connected players and wait this long before stopping so they can save; 0 stops immediately")
	flag.Parse()

	zztgo.HelpDir = *helpDir

	e := zztgo.NewEngine()
	e.Headless = true
	zztgo.E = e
	zztgo.WorldCreate()
	if !zztgo.WorldLoad(*worldName, ".ZZT", false) {
		log.Fatalf("load %s.ZZT failed", *worldName)
	}

	server := zztgo.NewWebSocketServer(zztgo.E.World, int16(*boardID))
	// Vanilla keeps <world>.HI beside the world file. Empty would keep the list
	// in memory, which is what the tests want but not what a server does.
	server.RoomManager.HighScorePath = *worldName + ".HI"
	server.RoomManager.LoadHighScores()
	// The only directory a client's save name can reach (M4.3a).
	server.SavesDir = *savesDir
	// Where the browser editor publishes worlds and the picker lists them (M5.6).
	server.WorldsDir = *worldsDir
	if *savesDir != "" {
		_ = os.MkdirAll(*savesDir, 0755)
		chatDB, err := zztgo.NewFileChatDatabase(filepath.Join(*savesDir, "chat.jsonl"))
		if err != nil {
			log.Printf("failed to initialize chat database: %v", err)
		} else {
			server.ChatDB = chatDB
		}
	}
	auth, err := zztgo.NewAuthServiceFromEnv()
	if err != nil {
		log.Printf("google auth disabled: %v", err)
	} else if auth != nil {
		server.Auth = auth
		log.Printf("google auth enabled")
	}

	// Autosave + restore-on-boot (M13.3). Drive the cadence off the tick clock, so
	// there is one clock and tests can step it. Restore before serving: a crash
	// recovery must be in place before the first client can join.
	if *autosaveSecs > 0 {
		server.AutosaveEveryTicks = *autosaveSecs * 1000 / int(zztgo.ServerTickDuration/time.Millisecond)
	}
	if !*fresh {
		server.RestoreAutosaves()
	}

	// Session recording (M14.2). Enable after restore so the recording's header
	// captures the actual starting world, then every join/input/submit is logged
	// for deterministic replay via cmd/zzt-replay.
	if *recordDir != "" {
		if err := server.EnableRecording(*recordDir); err != nil {
			log.Printf("session recording disabled: %v", err)
		}
	}

	// ctx stops the tick loop and HTTP server. It is deliberately NOT wired
	// straight to the OS signal: on SIGINT/SIGTERM we first warn connected players
	// and drain for shutdownGrace so they can save, then cancel ctx. The tick loop
	// flushes and closes any session recordings on ctx.Done (M14.2).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Buffered so a second signal (force-now) is never dropped while draining.
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go server.Run(ctx)

	api := &zztgo.WebAPI{
		RoomManager: server.RoomManager,
		World:       zztgo.E.World,
		SavesDir:    *savesDir,
		Server:      server,
		Auth:        server.Auth,
	}
	if generator, err := zztgo.GenerationServiceFromEnv(); err != nil {
		log.Printf("world generation unavailable: %v", err)
	} else {
		generator.SetProgressReporter(func(progress zztgo.GenerationProgress) {
			log.Printf("generation stage=%s board=%q index=%d total=%d attempt=%d detail=%s",
				progress.Stage, progress.Board, progress.Index, progress.Total, progress.Attempt, progress.Detail)
		})
		api.Generator = generator
	}

	mux := http.NewServeMux()
	mux.Handle("/ws", server)
	mux.Handle("/api/", api.Handler())
	if _, err := os.Stat(*webDir); err == nil {
		mux.Handle("/", spaFileServer(http.Dir(*webDir)))
		log.Printf("serving browser client from %s", *webDir)
		warnIfClientStale(*webDir)
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "build the browser client with: npm --prefix web run build", http.StatusNotFound)
		})
		log.Printf("browser client directory %s not found", *webDir)
	}

	httpServer := &http.Server{Addr: *addr, Handler: mux}
	go func() {
		<-sigCh
		drainAndAnnounce(server, *shutdownGrace, sigCh)
		cancel()
		shutdownCtx, sc := context.WithTimeout(context.Background(), 5*time.Second)
		defer sc()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	log.Printf("listening on %s", *addr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
	// Give the tick loop a moment to flush recordings before the process exits.
	cancel()
	time.Sleep(200 * time.Millisecond)
}

// drainAndAnnounce warns every reachable player that the server is about to
// restart, then waits `grace` so they can save with S. It re-broadcasts the
// remaining time every 15s so the on-screen banner counts down. A second signal
// on sigCh cuts the wait short; grace <= 0 returns immediately without warning.
func drainAndAnnounce(server *zztgo.WebSocketServer, grace time.Duration, sigCh <-chan os.Signal) {
	if grace <= 0 {
		return
	}
	bg := context.Background()
	deadline := time.Now().Add(grace)
	announce := func() int {
		remaining := int(time.Until(deadline).Round(time.Second).Seconds())
		if remaining < 0 {
			remaining = 0
		}
		return server.AnnounceShutdown(bg, remaining,
			fmt.Sprintf("SERVER RESTARTING IN %d SEC - PRESS S TO SAVE YOUR GAME", remaining))
	}
	// Skip the drain entirely when nobody is connected (the common case for a
	// deploy) so an empty server restarts immediately.
	if announce() == 0 {
		log.Printf("shutdown requested — no players connected, stopping now")
		return
	}
	log.Printf("shutdown requested — warning players, draining for %s", grace)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-time.After(time.Until(deadline)):
			return
		case <-ticker.C:
			announce()
		case <-sigCh:
			log.Printf("second signal — shutting down now")
			return
		}
	}
}

// warnIfClientStale logs a loud warning when the built browser bundle (webDir,
// e.g. web/dist) is older than the client source next to it (web/src). The
// server serves the BUILT bundle, so editing web/src without re-running
// `npm run build` silently ships stale UI — the trap behind the M17.1/M17.4
// "already fixed but still broken" reports. Catching it at boot turns a
// confusing runtime symptom into an actionable one-line message. It is advisory
// only: a deploy that shipped just dist/ (no src/ beside it) is not flagged.
func warnIfClientStale(webDir string) {
	srcDir := filepath.Join(filepath.Dir(webDir), "src")
	srcMod, okSrc := newestModTime(srcDir)
	distMod, okDist := newestModTime(webDir)
	if !okSrc || !okDist {
		return
	}
	if srcMod.After(distMod) {
		log.Printf("WARNING: %s is older than %s — the browser is being served a STALE build. Rebuild it: npm --prefix %s run build",
			webDir, srcDir, filepath.Dir(webDir))
	}
}

// newestModTime returns the most recent file modification time under dir.
func newestModTime(dir string) (time.Time, bool) {
	var newest time.Time
	found := false
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
			found = true
		}
		return nil
	})
	return newest, found
}

func spaFileServer(root http.FileSystem) http.Handler {
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
