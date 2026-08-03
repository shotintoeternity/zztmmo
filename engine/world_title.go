package zztgo

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// M14.4 — a world's identity is not its name.
//
// The 8-character DOS stem was doing two jobs: it was the primary key (the file
// path, the ?world= parameter, the .access.json key, the Instances map key, the
// .SAV and .HI stems, the recording filename, the backup manifest entry) and it
// was also the name a player reads. Every collision defect in the M16.17 family
// came out of that conflation — M16.17b (a dream overwrote a world), M16.17d (a
// player refused over a name they never chose), M18.11 (the classics needed a
// carve-out) — and each was closed with its own patch on the same seam.
//
// This file separates the two halves, on the shape the owner chose 2026-08-02:
//
//   - The IDENTITY stays the 8-character stem. Nothing on disk moves, so every
//     existing world, save, high-score file, recording, backup archive and
//     ?world= URL keeps resolving exactly as it did. What changes is how a stem
//     is CHOSEN for content nobody named: it is minted against the hosting
//     directory rather than derived from a title and hoped to be free.
//   - The TITLE becomes metadata in a NAME.meta.json sidecar, free to collide
//     with any other title, read by the picker (M18.9 already carries a Title
//     field) and by nothing that resolves a path.
//
// The two are deliberately not stored together with ownership: M16.17b decided
// that a world with no .access.json belongs to nobody and stays open, which is
// what keeps the ~100 shipped classics and every pre-M16.17b dream writable by
// their authors. Putting a title in that file would give an untitled-but-owned
// world an access record and flip it, so titles live in their own sidecar.

// WorldMeta is the display half of a world: what a player reads in the picker.
// It never reaches a path — SanitizeSaveName has nothing to say about it and
// two worlds may carry the same title.
type WorldMeta struct {
	Title  string `json:"title,omitempty"`
	Author string `json:"author,omitempty"`
}

func worldMetaPath(dir, worldName string) string {
	return filepath.Join(dir, worldName+".meta.json")
}

func loadWorldMeta(dir, worldName string) (WorldMeta, bool, error) {
	if dir == "" {
		return WorldMeta{}, false, nil
	}
	data, err := os.ReadFile(worldMetaPath(dir, worldName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return WorldMeta{}, false, nil
		}
		return WorldMeta{}, false, err
	}
	var meta WorldMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return WorldMeta{}, false, err
	}
	return meta, true, nil
}

// writeWorldMeta records a world's title beside it, atomically (write-temp-then-
// rename), the way writeWorldAccess records its ownership. A meta with nothing
// in it writes no file: an absent sidecar and an empty one mean the same thing,
// and the picker's fallback covers both.
func writeWorldMeta(dir, worldName string, meta WorldMeta) error {
	if dir == "" {
		return ErrSavesDisabled
	}
	if meta.Title == "" && meta.Author == "" {
		return nil
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	path := worldMetaPath(dir, worldName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// Minting
// ---------------------------------------------------------------------------

// ErrNoFreeWorldName is a stem whose whole numbered family is taken. Reaching
// it needs thousands of worlds sharing one title, so in practice it is the
// signal that something is looping, not that the namespace is full.
var ErrNoFreeWorldName = errors.New("no free world name")

// worldNameReservations holds the names minted but not yet persisted. Two
// dreams painting at once (the shipped ZZT_GENERATION_CONCURRENCY=2) can carry
// the same plan title, and neither has written a file for the other to see for
// the several minutes they spend painting — so without this, "collision-checked"
// would only be true of names that already exist on disk. A reservation is
// process-local, which is all it needs to be: one server owns one hosting
// directory.
var worldNameReservations = struct {
	mu   sync.Mutex
	held map[string]bool
}{held: make(map[string]bool)}

func worldNameReservationKey(dir, name string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	return abs + string(filepath.Separator) + name
}

// releaseWorldName gives a minted name back. On the success path the world's
// file now blocks the name anyway; this matters on the failure path, where a
// dream that never persisted must not hold its name until the process exits.
func releaseWorldName(dir, name string) {
	worldNameReservations.mu.Lock()
	defer worldNameReservations.mu.Unlock()
	delete(worldNameReservations.held, worldNameReservationKey(dir, name))
}

// reserveWorldName re-takes a name a caller already minted (RetryBoard resumes
// a generation whose first attempt released its reservation). It reports
// whether the name was free.
func reserveWorldName(dir, name string) bool {
	worldNameReservations.mu.Lock()
	defer worldNameReservations.mu.Unlock()
	key := worldNameReservationKey(dir, name)
	if worldNameReservations.held[key] {
		return false
	}
	worldNameReservations.held[key] = true
	return true
}

// worldNameIsFreeLocked answers whether minting may take this stem. It is deliberately
// stricter than any of the write guards: a minted name is one the server can
// create from nothing, so ANY existing claim disqualifies it rather than being
// weighed. That is what lets minting be the answer to a collision instead of a
// refusal — there is nothing here to refuse over.
//
// Callers must hold worldNameReservations.mu. Lock order is
// worldNameReservations.mu → WebSocketServer.mu → WorldInstance.mu: minting is
// the only thing that spans them, and nothing takes the reservation lock while
// holding a server lock. Keep it that way.
func worldNameIsFreeLocked(dir, name string, server *WebSocketServer) bool {
	if worldNameReservations.held[worldNameReservationKey(dir, name)] {
		return false
	}
	// M18.11: never mint onto a classic, downloaded or not.
	if WorldIsCanonical(name) {
		return false
	}
	// The picker drops names with no alphanumeric (web_api.go ListWorlds), so
	// minting one would make a world nobody can see or join.
	if !hasAlphanumeric(name) {
		return false
	}
	if server != nil && server.WorldIsOccupied(name) {
		return false
	}
	if dir == "" {
		return true
	}
	// The world file, and the ownership record — a sidecar outliving its world
	// still names an account, and inheriting it would hand a stranger's world
	// to whoever mints the stem next.
	for _, ext := range []string{".ZZT", ".access.json"} {
		if _, err := os.Stat(filepath.Join(dir, name+ext)); err == nil {
			return false
		}
	}
	return true
}

// mintWorldName picks the identity for a world nobody named, and reserves it.
//
// It walks a deterministic family around the caller's preferred stem — SEED,
// then SEED2, SEED3, … with the stem truncated to keep the whole inside the
// 8-character DOS name — and takes the first member no other world, sidecar,
// classic, live instance or in-flight mint has a claim on. The scan and the
// reservation happen under one lock, so two concurrent mints cannot agree on
// the same answer.
//
// This replaces "derive a name from the title and hope": generatedFallbackSaveName's
// GEN%05X is 20 bits, which at ~1000 worlds is a near-even-odds birthday
// collision. It survives here only as a SEED — one whose family is then checked
// like any other.
//
// The caller must releaseWorldName when the world is persisted or abandoned.
func mintWorldName(dir, seed string, server *WebSocketServer) (string, error) {
	safe, err := SanitizeSaveName(seed)
	if err != nil {
		return "", err
	}
	worldNameReservations.mu.Lock()
	defer worldNameReservations.mu.Unlock()
	for _, candidate := range worldNameFamily(safe) {
		if worldNameIsFreeLocked(dir, candidate, server) {
			worldNameReservations.held[worldNameReservationKey(dir, candidate)] = true
			return candidate, nil
		}
	}
	return "", fmt.Errorf("every name around %q is taken: %w", safe, ErrNoFreeWorldName)
}

// worldNameFamily is the stem and its numbered variants, in the order minting
// tries them. The stem is truncated — not extended — so every member is a legal
// 8-character DOS name and the family stays inside the namespace the file
// layout, the ?world= parameter and vanilla's .SAV/.HI stems all share.
func worldNameFamily(stem string) []string {
	family := []string{stem}
	for n := 2; n <= 9999; n++ {
		suffix := strconv.Itoa(n)
		if len(suffix) >= SaveNameMaxLength {
			break
		}
		base := stem
		if len(base) > SaveNameMaxLength-len(suffix) {
			base = base[:SaveNameMaxLength-len(suffix)]
		}
		family = append(family, base+suffix)
	}
	return family
}
