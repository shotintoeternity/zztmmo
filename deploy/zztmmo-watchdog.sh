#!/usr/bin/env bash
# zztmmo-watchdog: application-level health probe for the zzt-server game server.
#
# systemd already restarts the process if it *exits*, but the outage we actually
# hit was a live process that stayed "active" while every HTTP handler hung on a
# leaked lock (a panic under inst.mu that skipped its Unlock). A process-liveness
# check cannot see that. This probes an endpoint that exercises the global server
# lock (/api/worlds) with a hard timeout and restarts zztmmo after several
# consecutive hangs, so an unforeseen wedge self-heals within ~90s instead of
# staying down until a human notices.
#
# Invoked every 30s by zztmmo-watchdog.timer. State persists across invocations in
# STATE_DIR so the "consecutive failures" streak and the post-restart cooldown
# survive between one-shot runs.

set -u

URL="http://127.0.0.1:8080/api/worlds"
PROBE_TIMEOUT=5          # seconds; a healthy /api/worlds answers in well under 1s
FAIL_THRESHOLD=3         # consecutive bad probes before acting (~90s at 30s cadence)
COOLDOWN=300             # seconds to wait after a restart before restarting again
UNIT="zztmmo.service"
STATE_DIR="/run/zztmmo-watchdog"
FAIL_FILE="$STATE_DIR/consecutive_failures"
LAST_RESTART_FILE="$STATE_DIR/last_restart_epoch"

mkdir -p "$STATE_DIR"
now=$(date +%s)

# -f fails on a non-2xx status; -m caps total time so a hung handler is counted as
# a failure instead of blocking the watchdog itself.
if curl -sf -m "$PROBE_TIMEOUT" -o /dev/null "$URL"; then
	echo 0 >"$FAIL_FILE"
	exit 0
fi

fails=$(( $(cat "$FAIL_FILE" 2>/dev/null || echo 0) + 1 ))
echo "$fails" >"$FAIL_FILE"
echo "zztmmo-watchdog: health probe failed ($fails/$FAIL_THRESHOLD): $URL"

if [ "$fails" -lt "$FAIL_THRESHOLD" ]; then
	exit 0
fi

# Threshold reached. Respect a cooldown so a persistent problem does not become a
# restart storm; systemd's StartLimitBurst on zztmmo.service is the final backstop.
last_restart=$(cat "$LAST_RESTART_FILE" 2>/dev/null || echo 0)
if [ $(( now - last_restart )) -lt "$COOLDOWN" ]; then
	echo "zztmmo-watchdog: within ${COOLDOWN}s cooldown since last restart; holding off"
	exit 0
fi

echo "zztmmo-watchdog: $fails consecutive failures — restarting $UNIT"
if systemctl restart "$UNIT"; then
	echo "$now" >"$LAST_RESTART_FILE"
	echo 0 >"$FAIL_FILE"
	echo "zztmmo-watchdog: restart issued"
else
	echo "zztmmo-watchdog: restart command failed" >&2
fi
exit 0
