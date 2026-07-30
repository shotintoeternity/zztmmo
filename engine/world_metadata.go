package zztgo

import (
	_ "embed"
	"encoding/json"
	"os"
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
	Editors int `json:"editors,omitempty"`
	// Kind is how the picker groups a world (M18.9):
	//
	//   classic — worlds.manifest.json knows it, so it has a real title/author
	//   dreamed — this server generated it (a NAME.zwd sits beside NAME.ZZT)
	//   local   — neither: a community .ZZT the manifest does not cover, or an
	//             editor-published world
	//
	// The client shows classics and dreams on the empty-query first screen and
	// leaves `local` to search, so a tester's first click is not 69 entries of
	// "by Local ????".
	Kind string `json:"kind,omitempty"`
}

const (
	WorldKindClassic = "classic"
	WorldKindDreamed = "dreamed"
	WorldKindLocal   = "local"
)

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
	return worldListEntries("", worlds, playerCounts, nil, false)
}

// WorldListEntriesInDir includes every joinable local world. Museum metadata
// enriches catalogued files, while generated and editor-published files use a
// safe local fallback so the picker never hides a world it can load.
func WorldListEntriesInDir(dir string, worlds []string, playerCounts map[string]int) []WorldListEntry {
	return worldListEntries(dir, worlds, playerCounts, nil, true)
}

// WorldListEntriesInDirWithEditors is WorldListEntriesInDir plus editor
// occupancy (M17.11). The older signatures are kept so existing callers and
// tests are untouched.
func WorldListEntriesInDirWithEditors(dir string, worlds []string, playerCounts, editorCounts map[string]int) []WorldListEntry {
	return worldListEntries(dir, worlds, playerCounts, editorCounts, true)
}

// worldIsDreamed reports whether dir holds the ZWD source persistGeneratedWorld
// writes beside every world it generates (generation.go). It is the same
// discriminator M18.8 uses to find generated content: a filename pattern would
// misread a community world that happens to be called GEN-something, and the
// .ZZT alone carries nothing that says where it came from.
func worldIsDreamed(dir, world string) bool {
	if dir == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, world+".zwd")); err == nil {
		return true
	}
	return false
}

func worldListEntries(dir string, worlds []string, playerCounts map[string]int, editorCounts map[string]int, includeLocal bool) []WorldListEntry {
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
			if worldIsDreamed(dir, world) {
				entry.Kind = WorldKindDreamed
				entry.Author = "Dreamed here"
			} else {
				entry.Kind = WorldKindLocal
				entry.Author = "Local"
			}
			out = append(out, entry)
			continue
		}
		entry.Kind = WorldKindClassic
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
