package main

import (
	"context"
	"flag"
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

	// A cancelable context so SIGINT/SIGTERM shuts the tick loop down cleanly.
	// The tick loop flushes and closes any session recordings on ctx.Done, so a
	// clean stop does not lose the buffered tail of a recording (M14.2).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "build the browser client with: npm --prefix web run build", http.StatusNotFound)
		})
		log.Printf("browser client directory %s not found", *webDir)
	}

	httpServer := &http.Server{Addr: *addr, Handler: mux}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	log.Printf("listening on %s", *addr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
	// Give the tick loop a moment to flush recordings before the process exits.
	stop()
	time.Sleep(200 * time.Millisecond)
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
