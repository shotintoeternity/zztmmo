// M32.1 — the challenge client's pure half: route precedence, the CP437
// windows, and the ghost overlay.
//
// The claim worth naming here is the ghost's. Every assertion below asks the
// same thing in a different way: a ghost is a CELL, indexed by the ticks THIS
// attempt has drawn, refused when it belongs to another challenge or another
// version, and gone once its track runs out. Nothing in this module can send
// anything — there is no socket in it to send with.

import assert from "node:assert/strict";
import { build } from "esbuild";

async function load(entry) {
  const output = await build({
    entryPoints: [entry],
    bundle: true,
    format: "esm",
    platform: "node",
    write: false,
  });
  const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
  return import(`data:text/javascript;base64,${source}`);
}

const deep = await load("src/deep_link.ts");
const challenge = await load("src/challenge.ts");

// --- routes ---------------------------------------------------------------

assert.equal(deep.isChallengePath("/challenge"), true);
assert.equal(deep.isChallengePath("/challenge/gem-dash"), true);
assert.equal(deep.isChallengePath("/challenger"), false, "/challenger is not the challenge route");
assert.equal(deep.isChallengePath("/play/TOWN"), false);

assert.equal(deep.challengeLinkID("/challenge"), "", "a bare /challenge names today's, which only the server knows");
assert.equal(deep.challengeLinkID("/challenge/gem-dash"), "gem-dash");
assert.equal(deep.challengeLinkID("/challenge/gem-dash/"), "gem-dash", "a tidied trailing slash is the same link");
assert.equal(deep.challengeLinkID("/play/TOWN"), "");

assert.equal(deep.challengePath(), "/challenge");
assert.equal(deep.challengePath("gem-dash"), "/challenge/gem-dash");

// A challenge route must never be read as a world deep link — the two prefixes
// are disjoint, which is what stops /challenge/<id> joining a world called <id>.
assert.equal(deep.deepLinkWorldName("/challenge/gem-dash"), "");
assert.equal(deep.watchLinkWorldName("/challenge/gem-dash"), "");
assert.equal(deep.replayLinkID("/challenge/gem-dash"), "");

// --- landing --------------------------------------------------------------

const summary = {
  id: "gem-dash",
  title: "Gem Dash",
  summary: ["Collect all three gems.", "Fewest ticks wins."],
  world: "GEMDASH",
  version: 1,
  goalLine: "Goal: collect 3 gems",
  path: "/challenge/gem-dash",
  today: true,
  date: "2026-08-09",
  available: true,
};

const guestLanding = challenge.challengeLandingLines(
  { challenge: summary, leaderboard: [], canSubmit: false },
  { signedIn: false, hasGhost: false },
);
assert.ok(guestLanding.some((line) => line.includes("Gem Dash")));
assert.ok(guestLanding.some((line) => line === "!start;Start a run"), "a guest can still start a run");
assert.ok(guestLanding.some((line) => line.includes("Sign in to post a time")));
assert.ok(!guestLanding.some((line) => line.startsWith("!board;")), "an empty leaderboard is not offered");
assert.ok(!guestLanding.some((line) => line.startsWith("!ghost;")), "a ghost nobody loaded is not offered");
assert.ok(guestLanding.every((line) => line.length <= 44), "landing lines must fit the CP437 window");

const rows = [
  { rank: 1, name: "ada", ticks: 30, score: 30, recordingId: "chal-gem-dash-1", you: true },
  { rank: 2, name: "bo", ticks: 44, score: 30, recordingId: "chal-gem-dash-2" },
];
const fullLanding = challenge.challengeLandingLines(
  { challenge: summary, leaderboard: rows, canSubmit: true },
  { signedIn: true, hasGhost: true },
);
assert.ok(fullLanding.some((line) => line === "!board;Leaderboard"));
assert.ok(fullLanding.some((line) => line === "!ghost;Race the ghost"));
assert.ok(!fullLanding.some((line) => line.includes("Sign in")), "a signed-in player is not told to sign in");

const unavailable = challenge.challengeLandingLines(
  {
    challenge: { ...summary, available: false, unavailable: "Challenges are off: no runs are recorded here." },
    canSubmit: false,
  },
  { signedIn: true, hasGhost: false },
);
assert.ok(!unavailable.some((line) => line.startsWith("!start;")), "an unavailable challenge offers no start");
assert.ok(unavailable.some((line) => line.includes("no runs are recorded")), "and says why");

// --- leaderboard ----------------------------------------------------------

const empty = challenge.challengeLeaderboardLines([]);
assert.ok(empty.some((line) => line.includes("No times yet")));

const table = challenge.challengeLeaderboardLines(rows);
assert.ok(table.some((line) => line.startsWith("!chal-gem-dash-1;")), "a row with a recording opens it");
assert.ok(table.join("\n").includes("ada"));
assert.ok(table.some((line) => line.includes(" *")), "the viewer's own row is marked");
// The client renders the server's order and never re-sorts it.
const order = table.filter((line) => line.startsWith("!")).map((line) => line.split(";")[0].slice(1));
assert.deepEqual(order, ["chal-gem-dash-1", "chal-gem-dash-2"]);

const noRecording = challenge.challengeLeaderboardLines([{ rank: 1, name: "ada", ticks: 30, score: 30 }]);
assert.ok(!noRecording.some((line) => line.startsWith("!")), "a row with no recording opens nothing");

const actions = challenge.challengeRowActionLines(rows[0]);
assert.deepEqual(
  actions.filter((line) => line.startsWith("!")),
  ["!watch;Watch the replay", "!ghost;Race this ghost", "!postcard;Share a postcard GIF"],
);

// --- results --------------------------------------------------------------

const durable = challenge.challengeResultLines({
  type: "challengeResult",
  challengeId: "gem-dash",
  version: 1,
  ticks: 30,
  score: 30,
  gems: 3,
  durable: true,
  rank: 2,
  recordingId: "chal-gem-dash-1",
});
assert.ok(durable.some((line) => line.includes("30 ticks")));
assert.ok(durable.some((line) => line.includes("rank 2")));
assert.ok(durable.some((line) => line === "!again;Run it again"));

const guestResult = challenge.challengeResultLines({
  type: "challengeResult",
  challengeId: "gem-dash",
  version: 1,
  ticks: 41,
  score: 30,
  gems: 3,
  durable: false,
  reason: "Sign in to post a time.",
});
assert.ok(guestResult.some((line) => line.includes("Sign in to post a time")));
assert.ok(!guestResult.some((line) => line.includes("rank")), "a guest run claims no rank");

assert.equal(
  challenge.postcardURLForRun("chal-gem-dash-1", 10, 30),
  "/api/replay/postcard.gif?id=chal-gem-dash-1&start=10&ticks=30",
);

// --- ghost ----------------------------------------------------------------

const track = {
  challengeId: "gem-dash",
  version: 1,
  recordingId: "chal-gem-dash-1",
  name: "ada",
  ticks: 3,
  points: [
    { tick: 0, board: 1, x: 6, y: 12 },
    { tick: 1, board: 1, x: 7, y: 12 },
    { tick: 2, board: 1, x: 8, y: 12 },
  ],
};

assert.equal(challenge.ghostTrackMatches(track, summary), true);
assert.equal(challenge.ghostTrackMatches(track, { ...summary, version: 2 }), false, "a bumped definition retires its ghosts");
assert.equal(challenge.ghostTrackMatches(track, { ...summary, id: "other" }), false);
assert.equal(challenge.ghostTrackMatches({ ...track, points: [] }, summary), false);
assert.equal(challenge.ghostTrackMatches(null, summary), false);

// The ghost is indexed by the ticks THIS attempt has drawn: both start at zero.
assert.deepEqual(challenge.ghostOverlayCell(track, 0, 1), {
  x: 5,
  y: 11,
  ch: challenge.GHOST_CHAR,
  color: challenge.GHOST_COLOR,
});
assert.deepEqual(challenge.ghostOverlayCell(track, 2, 1).x, 7);
assert.equal(challenge.ghostOverlayCell(track, 3, 1), null, "past the end of a track the ghost is gone");
assert.equal(challenge.ghostOverlayCell(track, 0, 2), null, "a ghost is not drawn onto another board");
assert.equal(challenge.ghostOverlayCell(track, -1, 1), null);
assert.equal(challenge.ghostOverlayCell(null, 0, 1), null);

// The ghost glyph is deliberately not the player's ☻ (0x02) or its blink phase
// (0x01) — those are what M31.1's reduced-flashing filter rewrites.
assert.notEqual(challenge.GHOST_CHAR, 0x01);
assert.notEqual(challenge.GHOST_CHAR, 0x02);

assert.ok(challenge.ghostStatusLine(track).includes("ada"));
assert.equal(challenge.ghostStatusLine(null), "");
assert.ok(challenge.ghostStatusLine(track).length <= 20, "the ghost status must fit the sidebar");

console.log("challenge client tests passed");
