#!/usr/bin/env bash
# zztmmo-backup: daily snapshot of the two kinds of state a redeploy cannot rebuild.
#
# saves/ holds players' .SAV files, autosave/ and chat.jsonl. The deployment
# directory itself holds the worlds players make: "Dream a world" writes
# NAME.ZZT plus NAME.zwd / NAME.plan.md / NAME.prompt.txt, and the editor
# publishes NAME.ZZT (plus NAME.access.json, which records who owns it). Both
# land beside the ~100 shipped worlds that come out of the deployment bundle, so
# telling them apart takes a manifest — see "Which worlds" below. Losing either
# is a beta-ending first impression, so this runs unattended once a day
# (zztmmo-backup.timer) and keeps a rolling window of dated archives.
#
# Two archives per run, saves first because it is the one that matters most:
#   saves-<stamp>.tar.gz    the saves/ directory
#   worlds-<stamp>.tar.gz   player-created worlds and their companion files
# Each is written to a .partial name and renamed into place, so an interrupted
# run never leaves a truncated file that looks like a good backup, and pruning
# only happens after the new archives have been read back. zzt-server writes
# each autosave and each world atomically (write-temp-then-rename), so tarring a
# live directory captures whole files, never a half-written one.
#
# Which worlds: every top-level NAME.ZZT that is *not* listed in
# $SRC_DIR/SHIPPED_WORLDS (written by the deploy, one basename per line), plus
# any world that has a companion file beside it even if it is named there — a
# shipped name can be overwritten from the editor, and the companion is the
# evidence a player touched it. A missing manifest backs up every world with a
# warning: an oversized archive is a nuisance, a missing world is data loss.
# The Museum's .museum-cache/ subdirectory is excluded (top-level files only);
# worlds committed by a Museum play do ride along, because nothing on disk
# distinguishes them from an editor-published world. They are re-downloadable,
# so that costs space and nothing else.
#
# Overridable by the caller (the systemd unit passes none of these):
#   SRC_DIR         the deployment directory holding saves/   [/opt/zztmmo]
#   DEST_DIR        where archives accumulate                 [/var/backups/zztmmo]
#   RETENTION_DAYS  archives older than this are deleted      [14]

set -euo pipefail

SRC_DIR=${SRC_DIR:-/opt/zztmmo}
DEST_DIR=${DEST_DIR:-/var/backups/zztmmo}
RETENTION_DAYS=${RETENTION_DAYS:-14}
MANIFEST="$SRC_DIR/SHIPPED_WORLDS"

# Companion files a player-created world can have beside its .ZZT: the three
# "Dream a world" writes (generation.go persistGeneratedWorld) and the editor's
# ownership record (world_access.go).
COMPANION_EXTS=(.zwd .plan.md .prompt.txt .access.json)

if [ ! -d "$SRC_DIR/saves" ]; then
	echo "zztmmo-backup: no $SRC_DIR/saves directory — nothing to back up" >&2
	exit 1
fi

mkdir -p "$DEST_DIR"
stamp=$(date -u +%Y%m%dT%H%M%SZ)

# 1. Saved games.
archive="$DEST_DIR/saves-$stamp.tar.gz"
tar -czf "$archive.partial" -C "$SRC_DIR" saves
mv "$archive.partial" "$archive"
# Read the archive back before anything is pruned: a backup that cannot be
# listed is not a backup, and finding that out now beats finding it out during
# a restore.
tar -tzf "$archive" >/dev/null

# 2. Player-created worlds.
if [ ! -f "$MANIFEST" ]; then
	echo "zztmmo-backup: no $MANIFEST — backing up every world (see AWS.md, Saved-Game Backups)" >&2
fi
# Member names are collected into an array and passed after tar's `--`, not
# through a -T list file: real worlds are named `-.ZZT`, `--.ZZT` and `---.ZZT`,
# and both grep and GNU tar read a leading dash in a list file as an option.
members=()
world_count=0
for path in "$SRC_DIR"/*.ZZT; do
	[ -f "$path" ] || continue   # the glob itself when the directory has none
	world=${path##*/}
	base=${world%.ZZT}
	companions=()
	for ext in "${COMPANION_EXTS[@]}"; do
		[ -f "$SRC_DIR/$base$ext" ] && companions+=("$base$ext")
	done
	if [ -f "$MANIFEST" ] && [ ${#companions[@]} -eq 0 ] && grep -Fxq -- "$world" "$MANIFEST"; then
		continue   # shipped with the bundle and untouched since
	fi
	members+=("$world" "${companions[@]+"${companions[@]}"}")
	world_count=$((world_count + 1))
done

worlds_archive="$DEST_DIR/worlds-$stamp.tar.gz"
if [ ${#members[@]} -gt 0 ]; then
	tar -czf "$worlds_archive.partial" -C "$SRC_DIR" -- "${members[@]}"
	mv "$worlds_archive.partial" "$worlds_archive"
	tar -tzf "$worlds_archive" >/dev/null
	worlds_note="wrote $worlds_archive ($(du -h "$worlds_archive" | cut -f1), $world_count world(s))"
else
	worlds_note="no player-created worlds yet, so no worlds archive"
fi

# Retention. A leftover .partial from a killed run is swept on the next day's
# pass rather than left to accumulate.
for prefix in saves worlds; do
	find "$DEST_DIR" -maxdepth 1 -type f -name "$prefix-*.tar.gz" -mtime +"$RETENTION_DAYS" -delete
	find "$DEST_DIR" -maxdepth 1 -type f -name "$prefix-*.tar.gz.partial" -mmin +120 -delete
done

kept=$(find "$DEST_DIR" -maxdepth 1 -type f \( -name 'saves-*.tar.gz' -o -name 'worlds-*.tar.gz' \) | wc -l | tr -d ' ')
echo "zztmmo-backup: wrote $archive ($(du -h "$archive" | cut -f1)); $worlds_note; $kept archive(s) retained"
