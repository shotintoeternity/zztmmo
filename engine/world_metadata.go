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
	// FriendsHere is a per-recipient M26.1 presence summary. It contains public
	// names only for followed accounts that opted in to sharing their location.
	FriendsHere []FriendPresenceSummary `json:"friendsHere,omitempty"`
}

type FriendPresenceSummary struct {
	Name   string `json:"name"`
	Handle string `json:"handle,omitempty"`
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

// collapseWorldNames keeps one name per identity the join path would resolve
// (M18.13). ListWorlds already emits sanitized names, so on the live picker
// path this is a no-op; it matters for every other caller, because the picker
// must not show two cards that open one file no matter who assembled the list.
//
// Where several names collapse, the one that already equals its identity wins
// ("TOWN" over "town"): that is the name the join path opens, the key the
// occupancy maps are built under, and the stem the sidecar lookups below want —
// a dream writes GEN123.zwd and GEN123.meta.json beside GEN123.ZZT, so a
// surviving "gen123" would reclassify the world as `local` and lose its title.
// A name outside SanitizeSaveName's charset has no identity to collapse onto
// and is passed through untouched, exactly as before.
func collapseWorldNames(worlds []string) []string {
	out := make([]string, 0, len(worlds))
	at := make(map[string]int, len(worlds))
	for _, world := range worlds {
		identity, err := SanitizeSaveName(world)
		if err != nil {
			out = append(out, world)
			continue
		}
		if i, seen := at[identity]; seen {
			if world == identity {
				out[i] = world
			}
			continue
		}
		at[identity] = len(out)
		out = append(out, world)
	}
	return out
}

func worldListEntries(dir string, worlds []string, playerCounts map[string]int, editorCounts map[string]int, includeLocal bool) []WorldListEntry {
	worlds = collapseWorldNames(worlds)
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
			// M14.4: a world made here has a title of its own — what the plan
			// called it, or what the editor's world-property dialog says — and
			// the stem is only its identity. Where a sidecar records one, it is
			// what the picker shows. The manifest's own titles are handled
			// below and are not overridden: a classic's title is the Museum's.
			if meta, hasMeta, err := loadWorldMeta(dir, world); err == nil && hasMeta {
				if meta.Title != "" {
					entry.Title = meta.Title
				}
				if meta.Author != "" {
					entry.Author = meta.Author
				}
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

// WorldIsCanonical reports whether a name belongs to a canonical Museum of ZZT
// world — the population worlds.manifest.json knows, which the picker groups as
// `classic`. It is the predicate behind M18.11 (owner decision 2026-07-31: the
// canonical worlds are the main world, and player-authored content never
// overwrites one).
//
// It exists because the ownership guard cannot answer this question. M16.17b
// decided — deliberately — that a world with no .access.json belongs to nobody
// and stays open, and the shipped classics are exactly that population: no
// access file, so without this they are writable by any dream or publish that
// lands on their name. This asks the manifest instead of the disk, so it is
// true of a classic that has not been downloaded yet as well as one that has,
// and it cannot be defeated by deleting a sidecar.
//
// The Museum's own cache-commit path is deliberately NOT gated on this: writing
// a classic's canonical bytes into the hosting directory is that cache doing its
// job, not a player overwriting anything (museum.go, MuseumService.Play).
func WorldIsCanonical(name string) bool {
	safe, err := SanitizeSaveName(name)
	if err != nil {
		// Not a name this server can host at all, so not a name that can
		// collide with a classic. The write paths reject it on their own.
		return false
	}
	_, ok := museumMetadataForWorld(safe)
	return ok
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
