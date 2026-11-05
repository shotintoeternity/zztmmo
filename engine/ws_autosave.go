package zztgo

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// maybeAutosave counts ticks toward the autosave cadence and fires Autosave when
// it comes due. It runs on the tick goroutine, so autosaveTicks needs no lock.
func (s *WebSocketServer) maybeAutosave() {
	if s.AutosaveEveryTicks <= 0 {
		return
	}
	s.autosaveTicks++
	if s.autosaveTicks < s.AutosaveEveryTicks {
		return
	}
	s.autosaveTicks = 0
	s.Autosave()
}

// autosaveDir is where restore-on-boot autosaves live: a subdirectory of
// SavesDir so an autosave never collides with a player's named 'S' save. Empty
// SavesDir means saving is disabled and there is no autosave directory.
func (s *WebSocketServer) autosaveDir() string {
	if s.SavesDir == "" {
		return ""
	}
	return filepath.Join(s.SavesDir, "autosave")
}

// Autosave snapshots every occupied instance, and every empty one whose play
// is not yet saved, to autosaveDir()/<NAME>.SAV. It is the M13.3 seam: the tick
// loop calls it on cadence, and tests call it directly.
//
// Concurrency (in autosaveWhere): each world copy is taken under that instance's lock and the file
// is written after the lock is released, so a running room never blocks on disk
// I/O (mirroring SaveSnapshot's "a save never disturbs the game it is a save
// of"). The snapshot carries no saver, so the vanilla one-player inventory
// fields are zero — the server ignores them on join (M4.3a decision 1).
func (s *WebSocketServer) Autosave() {
	// An occupied room, or an empty one whose last players' play is not saved
	// yet (M36.18): the reconnect grace and the window before the last leave
	// used to be skipped, because neither has clients.
	s.autosaveWhere(func(inst *WorldInstance) bool {
		return len(inst.Clients) != 0 || inst.unsaved
	})
}

// autosaveEnabled reports whether this server autosaves at all: a cadence and
// somewhere to write. The last-leave save follows the same switch, so
// -autosave 0 still means no autosave of any kind.
func (s *WebSocketServer) autosaveEnabled() bool {
	return s.AutosaveEveryTicks > 0 && s.autosaveDir() != ""
}

// autosaveDeparted is the last-leave autosave (M36.18): every room whose
// players have all gone (the reconnect grace included) and whose play since
// its last autosave is not on disk is written now, on the tick goroutine,
// rather than waiting up to a cadence. It runs before evictIdleInstances, and
// an unsaved instance is not evictable, so the file is current before the
// instance can leave s.Instances, and a rejoin that recreates it reads it. A
// room whose save failed waits for the next cadence rather than retrying (and
// logging) every tick.
func (s *WebSocketServer) autosaveDeparted() {
	if !s.autosaveEnabled() {
		return
	}
	s.autosaveWhere(func(inst *WorldInstance) bool {
		return inst.unsaved && !inst.autosaveFailed && len(inst.RoomManager.players) == 0
	})
}

// autosaveWhere snapshots each instance for which pick (called under its lock)
// says yes, then writes the files with no lock held.
func (s *WebSocketServer) autosaveWhere(pick func(inst *WorldInstance) bool) {
	dir := s.autosaveDir()
	if dir == "" {
		return
	}

	type autosaveJob struct {
		inst  *WorldInstance
		name  string
		world TWorld
	}
	var jobs []autosaveJob

	s.mu.Lock()
	instances := make([]*WorldInstance, 0, len(s.Instances))
	for _, inst := range s.Instances {
		instances = append(instances, inst)
	}
	s.mu.Unlock()

	// The default instance is registered in s.Instances (NewWebSocketServer), so
	// iterating the map already covers it.
	for _, inst := range instances {
		// M32.1: a challenge run is never autosaved. Its instance is one
		// player's measured attempt at a world it does not own, and an autosave
		// of it would be a snapshot nobody can restore under a name no world
		// answers to. (SanitizeSaveName below would refuse the key anyway; this
		// says so on purpose rather than leaving the rule to a log line.)
		if inst.Challenge != nil {
			continue
		}
		inst.mu.Lock()
		if !pick(inst) {
			// An empty, saved room is already frozen into rm.world with nothing
			// new to say.
			inst.mu.Unlock()
			continue
		}
		// Instance names are already sanitized on their hosting paths, but the
		// name becomes a filename here — re-verify and skip-with-log rather than
		// guess at a safe one.
		name, err := SanitizeSaveName(inst.Name)
		if err != nil {
			inst.mu.Unlock()
			log.Printf("zztgo: skipping autosave of instance %q: %v", inst.Name, err)
			continue
		}
		inst.autosaving = true
		inst.unsaved = false
		world := inst.RoomManager.snapshotWorldNoSaver()
		inst.mu.Unlock()
		jobs = append(jobs, autosaveJob{inst: inst, name: name, world: world})
	}

	for _, job := range jobs {
		_, err := writeWorldSnapshot(dir, job.name, job.world)
		if err != nil {
			log.Printf("zztgo: autosave of %q failed: %v", job.name, err)
		} else {
			s.mu.Lock()
			if s.autosavedThisBoot == nil {
				s.autosavedThisBoot = make(map[string]bool)
			}
			s.autosavedThisBoot[job.name] = true
			s.mu.Unlock()
		}
		job.inst.mu.Lock()
		job.inst.autosaving = false
		// A failed write leaves the progress unsaved, so the instance is still
		// not evictable and the next cadence tries again.
		job.inst.autosaveFailed = err != nil
		if err != nil {
			job.inst.unsaved = true
		}
		job.inst.mu.Unlock()
	}
}

// RestoreAutosaves runs at boot, before serving: for every autosave whose name
// matches a hostable world it restores that world as the instance's starting
// state. An autosave beats the pristine .ZZT — that is what crash recovery means
// (freshness policy in NOTES.md). A corrupt or truncated file is logged and
// skipped, never a boot failure. -fresh skips this entirely (caller's choice).
func (s *WebSocketServer) RestoreAutosaves() {
	dir := s.autosaveDir()
	if dir == "" {
		return
	}
	// From here on this boot trusts the autosaves on disk, so GetOrCreateInstance
	// loads one whenever it creates an instance: below, and again after an
	// eviction (M36.18).
	s.mu.Lock()
	s.restoreAutosaves = true
	s.mu.Unlock()
	for _, name := range ListSnapshots(dir) {
		s.mu.Lock()
		existed := s.Instances[name] != nil
		s.mu.Unlock()
		// GetOrCreateInstance returns the already-registered default instance for
		// its own name, and creates any other from its autosave (falling back to
		// the pristine world). A name with no hostable world is not ours to
		// restore — skip it.
		inst, err := s.GetOrCreateInstance(name)
		if err != nil {
			log.Printf("zztgo: autosave %q has no hostable world, skipping: %v", name, err)
			continue
		}
		if !existed {
			// Already restored (or logged as unrestorable) on creation.
			continue
		}
		// The occupancy refusal in RestoreSnapshot is vacuous at boot — nobody has
		// joined yet — but harmless to go through. A corrupt or truncated file may
		// error or even panic (a garbage board length reaches a make); recover so a
		// bad autosave is skipped, never a boot failure.
		inst.mu.Lock()
		err = safeRestoreSnapshot(inst.RoomManager, dir, name)
		inst.mu.Unlock()
		if err != nil {
			log.Printf("zztgo: autosave %q could not be restored, using pristine world: %v", name, err)
			continue
		}
		log.Printf("zztgo: restored autosave %q", name)
	}
}

// safeRestoreSnapshot restores rm from dir/<NAME>.SAV, turning a panic on a
// malformed file into an ordinary error so RestoreAutosaves can skip it.
func safeRestoreSnapshot(rm *RoomManager, dir, name string) error {
	return safeRestore(func() error { return rm.RestoreSnapshot(dir, name) })
}

// safeRestoreOwnSnapshot is safeRestoreSnapshot for a restore a client asked
// for: the save must be of rm's own world (M36.15).
func safeRestoreOwnSnapshot(rm *RoomManager, dir, name string) error {
	return safeRestore(func() error { return rm.RestoreOwnSnapshot(dir, name) })
}

func safeRestore(restore func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("malformed snapshot: %v", r)
		}
	}()
	return restore()
}

// restoreAutosaveOnCreateLocked starts a newly created instance from its
// autosave, if it has one this boot trusts (M36.18): every autosave when the
// boot restored autosaves, and otherwise (a -fresh boot) only the ones this
// process wrote. This is what brings an evicted world back with its progress;
// without it a rejoin after eviction got the pristine .ZZT, and the next
// reboot revived an older autosave over newer play. A bad file is logged and
// the pristine world kept, as at boot. The read happens under s.mu, as
// LoadPristineWorld's does. Caller holds s.mu; rm is not yet shared.
func (s *WebSocketServer) restoreAutosaveOnCreateLocked(rm *RoomManager, worldName string) {
	dir := s.autosaveDir()
	if dir == "" || !(s.restoreAutosaves || s.autosavedThisBoot[worldName]) {
		return
	}
	if path, err := snapshotPath(dir, worldName); err != nil {
		return
	} else if _, err := os.Stat(path); err != nil {
		return
	}
	if err := safeRestoreSnapshot(rm, dir, worldName); err != nil {
		log.Printf("zztgo: autosave %q could not be restored, using pristine world: %v", worldName, err)
		return
	}
	log.Printf("zztgo: restored autosave %q", worldName)
}
