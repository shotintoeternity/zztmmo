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
	return worldListEntries(worlds, playerCounts)
}

// WorldListEntriesInDir has the same metadata-only catalog policy as
// WorldListEntries. Its directory argument remains part of the API because the
// caller already uses it while enumerating local world files.
func WorldListEntriesInDir(_ string, worlds []string, playerCounts map[string]int) []WorldListEntry {
	return worldListEntries(worlds, playerCounts)
}

func worldListEntries(worlds []string, playerCounts map[string]int) []WorldListEntry {
	out := make([]WorldListEntry, 0, len(worlds))
	for _, world := range worlds {
		meta, ok := museumMetadataForWorld(world)
		if !ok {
			// The picker is a curated Museum catalog. Files without metadata
			// remain on disk but are not presented as join targets.
			continue
		}
		entry := WorldListEntry{
			World:   world,
			ID:      meta.ID,
			Title:   world,
			Author:  meta.Author,
			Created: meta.Created,
			Players: playerCounts[world],
		}
		if meta.Title != "" {
			entry.Title = meta.Title
		}
		if entry.Created == "" && meta.Year != 0 {
			entry.Created = strconv.Itoa(meta.Year)
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
