#!/usr/bin/env bash
# zztmmo-backup: daily snapshot of the saved-game directory.
#
# saves/ is the only player-authored state on the host that a redeploy cannot
# rebuild: worlds, help files, and the binary all come out of the deployment
# bundle, but a tester's .SAV is theirs alone. Losing it is a beta-ending first
# impression, so this runs unattended once a day (zztmmo-backup.timer) and keeps
# a rolling window of dated archives.
#
# The archive is written to a .partial name and renamed into place, so an
# interrupted run never leaves a truncated file that looks like a good backup,
# and pruning only happens after the new archive has been read back. zzt-server
# writes each autosave atomically (write-temp-then-rename), so tarring a live
# saves/ captures whole files, never a half-written one.
#
# Overridable by the caller (the systemd unit passes none of these):
#   SRC_DIR         the deployment directory holding saves/   [/opt/zztmmo]
#   DEST_DIR        where archives accumulate                 [/var/backups/zztmmo]
#   RETENTION_DAYS  archives older than this are deleted      [14]

set -euo pipefail

SRC_DIR=${SRC_DIR:-/opt/zztmmo}
DEST_DIR=${DEST_DIR:-/var/backups/zztmmo}
RETENTION_DAYS=${RETENTION_DAYS:-14}

if [ ! -d "$SRC_DIR/saves" ]; then
	echo "zztmmo-backup: no $SRC_DIR/saves directory — nothing to back up" >&2
	exit 1
fi

mkdir -p "$DEST_DIR"
stamp=$(date -u +%Y%m%dT%H%M%SZ)
archive="$DEST_DIR/saves-$stamp.tar.gz"

tar -czf "$archive.partial" -C "$SRC_DIR" saves
mv "$archive.partial" "$archive"
# Read the archive back before anything is pruned: a backup that cannot be
# listed is not a backup, and finding that out now beats finding it out during
# a restore.
tar -tzf "$archive" >/dev/null

# Retention. A leftover .partial from a killed run is swept on the next day's
# pass rather than left to accumulate.
find "$DEST_DIR" -maxdepth 1 -type f -name 'saves-*.tar.gz' -mtime +"$RETENTION_DAYS" -delete
find "$DEST_DIR" -maxdepth 1 -type f -name 'saves-*.tar.gz.partial' -mmin +120 -delete

kept=$(find "$DEST_DIR" -maxdepth 1 -type f -name 'saves-*.tar.gz' | wc -l | tr -d ' ')
echo "zztmmo-backup: wrote $archive ($(du -h "$archive" | cut -f1)); $kept archive(s) retained"
