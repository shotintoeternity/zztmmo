package zztgo

import (
	_ "embed"
	"encoding/json"
	"io"
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
	return worldListEntries(worlds, playerCounts, nil)
}

// WorldListEntriesInDir is WorldListEntries plus a fallback title read from each
// world's own .ZZT header (its stored Name), so worlds absent from the metadata
// manifest still show their real in-game title instead of the bare filename.
// The header title never overrides curated manifest metadata; it only beats the
// filename fallback.
func WorldListEntriesInDir(dir string, worlds []string, playerCounts map[string]int) []WorldListEntry {
	headerTitles := make(map[string]string, len(worlds))
	for _, world := range worlds {
		if title := worldHeaderTitle(filepath.Join(dir, world+".ZZT")); title != "" {
			headerTitles[world] = title
		}
	}
	return worldListEntries(worlds, playerCounts, headerTitles)
}

func worldListEntries(worlds []string, playerCounts map[string]int, headerTitles map[string]string) []WorldListEntry {
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
		if title := headerTitles[world]; title != "" {
			entry.Title = title
		}
		if meta, ok := museumMetadataForWorld(world); ok {
			entry.ID = meta.ID
			if meta.Title != "" {
				entry.Title = meta.Title
			}
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

// worldHeaderTitle reads the world Name stored in a .ZZT header (the same field
// LoadWorldInfo parses from offset 25 of the world-info block). It returns "" if
// the file cannot be read, is not a recognized world version, or the name is
// empty or contains control characters (i.e. is not a displayable title). Only
// the first bytes are read, so this stays cheap across a large worlds directory.
func worldHeaderTitle(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	var buf [96]byte
	n, err := io.ReadFull(f, buf[:])
	if err != nil && err != io.ErrUnexpectedEOF {
		return ""
	}
	b := buf[:n]

	// Mirror worldReadFrom's board-count/version skip to find the world-info
	// block: normally 2 bytes, or 4 for the -1 version marker.
	if len(b) < 2 {
		return ""
	}
	off := 2
	if bc := LoadInt16(b[:2]); bc < 0 {
		if bc != -1 {
			return "" // a newer, unrecognized version
		}
		off = 4
	}
	if len(b) < off+46 {
		return ""
	}
	name := LoadString(b[off+25 : off+46])
	name = strings.TrimRight(name, " \x00")
	if name == "" {
		return ""
	}
	for i := 0; i < len(name); i++ {
		if name[i] < 0x20 {
			return "" // control byte: not a real title
		}
	}
	return name
}
