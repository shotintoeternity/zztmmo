package main

import (
	"context"
	"flag"
	"log"
	"net/http"

	"github.com/benhoyt/zztgo"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	worldName := flag.String("world", "TOWN", "world basename to load")
	boardID := flag.Int("board", 1, "default board id")
	flag.Parse()

	e := zztgo.NewEngine()
	e.Headless = true
	zztgo.E = e
	zztgo.WorldCreate()
	if !zztgo.WorldLoad(*worldName, ".ZZT", false) {
		log.Fatalf("load %s.ZZT failed", *worldName)
	}

	server := zztgo.NewWebSocketServer(zztgo.E.World, int16(*boardID))
	go server.Run(context.Background())

	log.Printf("listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, server))
}
