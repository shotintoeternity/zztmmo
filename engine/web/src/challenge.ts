// challenge.ts — the /challenge landing, its leaderboard, and the local ghost
// (M32.1).
//
// Everything here is pure — a function of the payloads the server sent and the
// tick the client is on — in the deep_link.ts / comfort.ts shape: main.ts owns
// the fetches, the windows and the canvas.
//
// The boundary this file exists to keep is the ghost's. A ghost is a track of
// positions read out of a finished recording. It is drawn over this browser's
// own canvas and NOTHING here produces input, joins anything, or reaches the
// wire — ghostOverlayCell returns a cell to paint, and that is the whole of a
// ghost's power.

export type ChallengeSummary = {
  id: string;
  title: string;
  summary?: string[];
  world: string;
  version: number;
  goalLine: string;
  path: string;
  today?: boolean;
  date?: string;
  available: boolean;
  unavailable?: string;
};

export type ChallengeLeaderboardRow = {
  rank: number;
  name: string;
  ticks: number;
  score: number;
  recordingId?: string;
  you?: boolean;
};

export type ChallengeResponse = {
  challenge: ChallengeSummary;
  leaderboard?: ChallengeLeaderboardRow[];
  canSubmit: boolean;
  catalogue?: ChallengeSummary[];
};

export type ChallengeResultMessage = {
  type: "challengeResult";
  challengeId: string;
  version: number;
  ticks: number;
  score: number;
  gems: number;
  durable: boolean;
  rank?: number;
  recordingId?: string;
  reason?: string;
};

export type ChallengeErrorMessage = {
  type: "challengeError";
  challengeId?: string;
  reason: string;
};

export type ChallengeGhostPoint = { tick: number; board: number; x: number; y: number };

export type ChallengeGhostTrack = {
  challengeId: string;
  version: number;
  recordingId: string;
  name?: string;
  ticks: number;
  truncated?: boolean;
  points: ChallengeGhostPoint[];
};

/** The CP437 window width the landing and leaderboard clamp to. */
const WINDOW_WIDTH = 44;

/** The glyph a ghost is drawn as: a hollow ring, deliberately NOT the player's
 * ☻. A ghost that looked like a player would be read as one — and the reduced
 * -flashing filter (M31.1) rewrites the player glyph, which would then rewrite
 * the ghost too. */
export const GHOST_CHAR = 0x09;
/** Dark grey on black: present, and plainly not somebody in the room. */
export const GHOST_COLOR = 0x08;

function clamp(line: string): string {
  return line.slice(0, WINDOW_WIDTH);
}

/**
 * challengeLandingLines is the /challenge window: what today's challenge is,
 * whether it can be played, and the actions. Action lines use the text window's
 * "!key;label" hyperlink form, so Enter on one yields the key.
 */
export function challengeLandingLines(
  resp: ChallengeResponse,
  options: { signedIn: boolean; hasGhost: boolean } = { signedIn: false, hasGhost: false },
): string[] {
  const challenge = resp.challenge;
  const lines: string[] = ["", clamp(`  ${challenge.title}`)];
  if (challenge.date) {
    lines.push(clamp(`  ${challenge.today ? "Today" : "Challenge"}: ${challenge.date}`));
  }
  lines.push("");
  for (const line of challenge.summary ?? []) {
    lines.push(clamp(`  ${line}`));
  }
  lines.push(clamp(`  ${challenge.goalLine}`), "");
  if (!challenge.available) {
    lines.push(clamp(`  ${challenge.unavailable || "Unavailable."}`), "");
    return lines;
  }
  lines.push("!start;Start a run");
  if ((resp.leaderboard ?? []).length > 0) {
    lines.push("!board;Leaderboard");
  }
  if (options.hasGhost) {
    lines.push("!ghost;Race the ghost");
  }
  lines.push("");
  if (!options.signedIn) {
    lines.push(clamp("  Sign in to post a time."), "");
  }
  return lines;
}

/**
 * challengeLeaderboardLines renders the table. Rows are shown in the order the
 * server sent them — the ranking is the server's, and a client that re-sorted
 * would be inventing a second answer to the same question.
 */
export function challengeLeaderboardLines(rows: ChallengeLeaderboardRow[]): string[] {
  if (rows.length === 0) {
    return ["", "  No times yet. Be the first.", ""];
  }
  const lines = ["", "  #  Player            Ticks  Score", ""];
  for (const row of rows) {
    const rank = String(row.rank).padStart(2, " ");
    const name = (row.name || "player").slice(0, 16).padEnd(16, " ");
    const ticks = String(row.ticks).padStart(6, " ");
    const score = String(row.score).padStart(6, " ");
    const label = `${rank} ${name}${ticks}${score}${row.you ? " *" : ""}`;
    // A row with a recording is selectable: it is what opens the replay, the
    // postcard, and the ghost.
    lines.push(row.recordingId ? `!${row.recordingId};${label}` : `  ${label}`);
  }
  lines.push("", "  * your run. Enter opens a run.");
  return lines;
}

/** challengeRowActionLines is the menu one leaderboard row offers. */
export function challengeRowActionLines(row: ChallengeLeaderboardRow): string[] {
  return [
    "",
    clamp(`  ${row.name || "player"} - ${row.ticks} ticks`),
    "",
    "!watch;Watch the replay",
    "!ghost;Race this ghost",
    "!postcard;Share a postcard GIF",
    "",
  ];
}

/** challengeResultLines is what a finished run says. */
export function challengeResultLines(result: ChallengeResultMessage): string[] {
  const lines = ["", clamp(`  Finished in ${result.ticks} ticks.`), clamp(`  Score ${result.score}, gems ${result.gems}.`), ""];
  if (result.durable && result.rank) {
    lines.push(clamp(`  Leaderboard rank ${result.rank}.`));
  } else if (result.reason) {
    lines.push(clamp(`  ${result.reason}`));
  }
  lines.push("", "!again;Run it again", "!board;Leaderboard", "");
  return lines;
}

/** postcardURLForRun is the M22.4 postcard a leaderboard row can share. The
 * replay itself is opened through the client's own startReplay, which owns the
 * socket and the address bar. */
export function postcardURLForRun(recordingId: string, startTick = 0, ticks = 0): string {
  const params = new URLSearchParams({ id: recordingId });
  if (startTick > 0) {
    params.set("start", String(startTick));
  }
  if (ticks > 0) {
    params.set("ticks", String(ticks));
  }
  return "/api/replay/postcard.gif?" + params.toString();
}

/**
 * ghostTrackMatches is the staleness fence on the client side: a track from
 * another challenge, or from an older version of this one, is not raced. The
 * server refuses these too; this is what keeps a track the browser was already
 * holding from surviving a definition change.
 */
export function ghostTrackMatches(track: ChallengeGhostTrack | null, challenge: ChallengeSummary | null): boolean {
  if (!track || !challenge) {
    return false;
  }
  return track.challengeId === challenge.id && track.version === challenge.version && track.points.length > 0;
}

/**
 * ghostOverlayCell is where the ghost is at a given tick of THIS run, or null.
 *
 * The track is indexed by elapsed ticks rather than by the recorded tick number,
 * so a ghost races the run the player is making now: both start at zero. Past
 * the end of the track the ghost is gone — a finished ghost that lingered on its
 * last tile would read as a player standing there.
 */
export function ghostOverlayCell(
  track: ChallengeGhostTrack | null,
  elapsedTicks: number,
  boardId: number,
): { x: number; y: number; ch: number; color: number } | null {
  if (!track || track.points.length === 0 || elapsedTicks < 0) {
    return null;
  }
  if (elapsedTicks >= track.points.length) {
    return null;
  }
  const point = track.points[elapsedTicks];
  if (!point || point.board !== boardId) {
    return null;
  }
  // ZZT board coordinates are 1-based; the canvas is not.
  return { x: point.x - 1, y: point.y - 1, ch: GHOST_CHAR, color: GHOST_COLOR };
}

/** ghostStatusLine is the sidebar/status text while a ghost is loaded. */
export function ghostStatusLine(track: ChallengeGhostTrack | null): string {
  if (!track) {
    return "";
  }
  return `Ghost: ${(track.name || "record").slice(0, 9)} ${track.ticks}t`.slice(0, 20);
}
