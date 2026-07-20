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
	// Editors mirrors Players for people editing the world rather than playing
	// it (M17.11). omitempty keeps quiet worlds uncluttered and the JSON shape
	// backward-compatible.
	Editors int    `json:"editors,omitempty"`
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
	return worldListEntries(worlds, playerCounts, nil, false)
}

// WorldListEntriesInDir includes every joinable local world. Museum metadata
// enriches catalogued files, while generated and editor-published files use a
// safe local fallback so the picker never hides a world it can load.
func WorldListEntriesInDir(_ string, worlds []string, playerCounts map[string]int) []WorldListEntry {
	return worldListEntries(worlds, playerCounts, nil, true)
}

// WorldListEntriesInDirWithEditors is WorldListEntriesInDir plus editor
// occupancy (M17.11). The older signatures are kept so existing callers and
// tests are untouched.
func WorldListEntriesInDirWithEditors(_ string, worlds []string, playerCounts, editorCounts map[string]int) []WorldListEntry {
	return worldListEntries(worlds, playerCounts, editorCounts, true)
}

func worldListEntries(worlds []string, playerCounts map[string]int, editorCounts map[string]int, includeLocal bool) []WorldListEntry {
	out := make([]WorldListEntry, 0, len(worlds))
	for _, world := range worlds {
		meta, ok := museumMetadataForWorld(world)
		if !ok && !includeLocal {
			continue
		}
		entry := WorldListEntry{
			World:   world,
			Title:   world,
			Players: playerCounts[world],
			Editors: editorCounts[world],
		}
		if !ok {
			entry.ID = strings.ToLower(world)
			entry.Author = "Local"
			out = append(out, entry)
			continue
		}
		entry.ID = meta.ID
		entry.Author = meta.Author
		entry.Created = meta.Created
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
