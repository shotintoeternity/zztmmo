package zztgo

import (
	_ "embed"
	"encoding/json"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

//go:embed worlds.manifest.json
var worldManifestJSON []byte

type WorldListEntry struct {
	World   string `json:"world"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	Author  string `json:"author"`
	Created string `json:"created"`
	Players int    `json:"players,omitempty"`
}

type museumWorldManifest struct {
	Worlds []museumWorldEntry `json:"worlds"`
}

type museumWorldEntry struct {
	ID       string   `json:"id"`
	Zip      string   `json:"zip"`
	Title    string   `json:"title"`
	Author   string   `json:"author"`
	Year     int      `json:"year"`
	Created  string   `json:"created"`
	ZZTFiles []string `json:"zzt_files"`
}

var (
	worldMetadataOnce sync.Once
	worldMetadataBy   map[string]museumWorldEntry
)

func WorldListEntries(worlds []string, playerCounts map[string]int) []WorldListEntry {
	out := make([]WorldListEntry, 0, len(worlds))
	for _, world := range worlds {
		entry := WorldListEntry{
			World:   world,
			ID:      world,
			Title:   world,
			Author:  "Unknown",
			Created: "",
			Players: playerCounts[world],
		}
		if meta, ok := museumMetadataForWorld(world); ok {
			entry.ID = meta.ID
			entry.Title = meta.Title
			entry.Author = meta.Author
			entry.Created = meta.Created
			if entry.Created == "" && meta.Year != 0 {
				entry.Created = strconv.Itoa(meta.Year)
			}
		}
		out = append(out, entry)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].World == "TOWN" {
			return true
		}
		if out[j].World == "TOWN" {
			return false
		}
		return strings.ToUpper(out[i].Title) < strings.ToUpper(out[j].Title)
	})
	return out
}

func museumMetadataForWorld(world string) (museumWorldEntry, bool) {
	worldMetadataOnce.Do(loadWorldMetadata)
	meta, ok := worldMetadataBy[strings.ToUpper(world)]
	return meta, ok
}

func loadWorldMetadata() {
	worldMetadataBy = make(map[string]museumWorldEntry)
	var manifest museumWorldManifest
	if json.Unmarshal(worldManifestJSON, &manifest) != nil {
		return
	}
	for _, entry := range manifest.Worlds {
		addWorldMetadataKey(entry.ID, entry)
		addWorldMetadataKey(strings.TrimSuffix(entry.Zip, filepath.Ext(entry.Zip)), entry)
		for _, zztFile := range entry.ZZTFiles {
			addWorldMetadataKey(strings.TrimSuffix(zztFile, filepath.Ext(zztFile)), entry)
		}
	}
}

func addWorldMetadataKey(key string, entry museumWorldEntry) {
	safe, err := SanitizeSaveName(key)
	if err != nil {
		return
	}
	if _, exists := worldMetadataBy[safe]; !exists {
		worldMetadataBy[safe] = entry
	}
}
