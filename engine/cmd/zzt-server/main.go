package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/benhoyt/zztgo"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	worldName := flag.String("world", "TOWN", "world basename to load")
	boardID := flag.Int("board", 1, "default board id")
	webDir := flag.String("web", "web/dist", "built browser client directory")
	helpDir := flag.String("help", ".", "directory holding the .HLP help files")
	savesDir := flag.String("saves", "saves", "directory for saved-game snapshots; empty disables saving")
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
	if *savesDir != "" {
		_ = os.MkdirAll(*savesDir, 0755)
		chatDB, err := zztgo.NewFileChatDatabase(filepath.Join(*savesDir, "chat.jsonl"))
		if err != nil {
			log.Printf("failed to initialize chat database: %v", err)
		} else {
			server.ChatDB = chatDB
		}
	}
	go server.Run(context.Background())

	api := &zztgo.WebAPI{
		RoomManager: server.RoomManager,
		World:       zztgo.E.World,
		SavesDir:    *savesDir,
		Server:      server,
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

	log.Printf("listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
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
