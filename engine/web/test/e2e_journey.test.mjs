// M16.11 — browser end-to-end player journeys, without state staging.
//
// Drives the real Vite-built client in headless Chromium against the real
// zzt-server subprocess, entirely through the production title screen and
// world picker (never stageTownPlayer), and asserts on the protocol traffic
// that browser actually exchanges (page.on("websocket")) plus the canvas.
//
// WHY THE PROTOCOL FRAMES: the client renders to a single <canvas> via the
// CP437 atlas and exposes no DOM state, so there is nothing meaningful to
// assert against in the page itself. The frames are its real observable
// behaviour — a keystroke that never reached the server, or a server reply the
// client never applied, shows up here immediately. This keeps the test honest
// without adding test-only hooks to production code.
//
// FIVE THINGS THAT SILENTLY PRODUCE A "PASSING" TEST THAT NEVER PLAYS AT ALL:
//
//  1. An instantaneous keyboard.press() usually moves nobody. The client sends
//     a frame on the key edges (handleKeyDown/handleKeyUp) and re-sends the
//     held mask every 55ms (connect's inputTimer); the server consumes each
//     frame on exactly one 110ms tick and clears it. A press that begins and
//     ends between two tick boundaries is therefore simply never seen.
//     Movement and shooting hold the key down — see lib/walk.mjs.
//  2. The vendor Object at x=26 BLOCKS row 12. The east half of board 1 (bear,
//     passage) is only reachable by walking around it.
//  3. Modal-opening events arrive BEFORE the client has drawn the modal, so
//     typing immediately after the event races it. settle() after each.
//  4. `go test` caches this test and the .mjs is not a tracked dependency —
//     iterate with `-count=1` or you will read a stale pass.
//  5. step() is NOT one tile. Tiles moved = server ticks that fell inside the
//     hold, so a hold that spans two boundaries moves two tiles. step() now
//     ends its hold when the tile is OBSERVED to land instead of after a fixed
//     95ms (M16.11e), which makes one tile the common case — but not a
//     guarantee, because the release still races the next boundary. Any walk
//     whose target is a single tile must re-aim after every step (walkOnto),
//     never satisfy a one-way inequality (M16.11a). The mechanism is stated in
//     full at the top of lib/walk.mjs; earlier versions of this note said the
//     server latched the mask, which it does not.

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { chromium } from "playwright";
import { at, step, walkOnto, walkUntil, assertObserver } from "./lib/walk.mjs";

const baseURL = process.env.BASE_URL || "http://127.0.0.1:8080";
const resultsDir = path.resolve("test-results");
fs.mkdirSync(resultsDir, { recursive: true });

const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 1280, height: 720 } });
await context.tracing.start({ screenshots: true, snapshots: true });
const page = await context.newPage();

// --- observed state, rebuilt from the frames the browser receives ------------

const pageErrors = [];
const consoleErrors = [];
const sockets = [];
// Every /api/title request the page makes, in order. The title screen has no
// socket by design, so this is the only wire evidence of WHOSE board 0 is on
// screen after a world is picked.
const titleRequests = [];
const transcript = []; // retained and dumped on failure (DoD: protocol transcript)

const seen = {
  hud: null,
  you: null,
  boardId: null,
  resumeToken: null,
  stateHash: null, // DoD: final server StateHash on failure
  events: [],
  snapshots: 0,
  boardChanges: 0,
  diffs: 0,
  closes: 0,
};

function resetRunState() {
  seen.events.length = 0;
  seen.snapshots = 0;
  seen.boardChanges = 0;
  seen.diffs = 0;
}

page.on("pageerror", (err) => pageErrors.push(String(err)));
page.on("console", (msg) => {
  if (msg.type() === "error") consoleErrors.push(msg.text());
});
page.on("request", (request) => {
  const url = request.url();
  if (url.includes("/api/title")) titleRequests.push(url);
});
page.on("websocket", (ws) => {
  sockets.push(ws.url());
  transcript.push({ dir: "open", url: ws.url() });
  ws.on("close", () => {
    seen.closes++;
    transcript.push({ dir: "close" });
  });
  ws.on("framesent", (f) => transcript.push({ dir: "send", payload: String(f.payload).slice(0, 400) }));
  ws.on("framereceived", (frame) => {
    let msg;
    try {
      msg = JSON.parse(frame.payload);
    } catch {
      return;
    }
    transcript.push({ dir: "recv", type: msg.type, payload: String(frame.payload).slice(0, 400) });

    // "saveResult"/"highScoreEntry"/"highScores"/quit outcomes ride the bare
    // EventMessage envelope, not a snapshot/diff events array.
    if (msg.type === "event" && msg.event) seen.events.push(msg.event);

    const body = msg.type === "boardChange" ? msg.snapshot : msg;
    if (msg.type === "snapshot") seen.snapshots++;
    if (msg.type === "boardChange") seen.boardChanges++;
    if (msg.type === "diff") seen.diffs++;
    if (typeof body.hash === "number") seen.stateHash = body.hash;
    if (body.hud) seen.hud = body.hud;
    if (body.you) seen.you = body.you;
    if (body.players?.length) {
      const me = body.players.find((p) => p.id === seen.you?.id);
      if (me) seen.you = me;
    }
    if (typeof body.boardId === "number") seen.boardId = body.boardId;
    if (body.resumeToken) seen.resumeToken = body.resumeToken;
    for (const ev of body.events || []) seen.events.push(ev);
  });
});

// --- helpers ----------------------------------------------------------------

const sleep = (ms) => page.waitForTimeout(ms);
/** Give the client time to draw a modal the server has just announced (note 3). */
const settle = () => sleep(700);
const eventTypes = () => [...new Set(seen.events.map((e) => e.type))];
const has = (type, match) => seen.events.some((e) => e.type === type && (!match || match(e)));

async function waitFor(pred, describe, timeoutMs = 10000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    if (pred()) return;
    if (Date.now() > deadline) {
      throw new Error(
        `timed out waiting for ${describe}\n  last seen: board=${seen.boardId} ` +
          `pos=(${seen.you?.x},${seen.you?.y}) hp=${seen.you?.health} hud=${JSON.stringify(seen.hud)}\n` +
          `  events: ${JSON.stringify(eventTypes())}`,
      );
    }
    await sleep(60);
  }
}

/**
 * This journey's state is page-global (`seen`, rebuilt from the frames above),
 * but lib/walk.mjs walks an observer BAG. `you` and `boardId` are getters so the
 * helpers read the live values rather than a copy taken when this was built.
 *
 * coop_journey.test.mjs's per-client object is already this shape, which is what
 * lets both journeys share one walk implementation instead of carrying a copy
 * each (M16.11e). The defaults below are this driver's own: one player, no
 * contention, so a blocked walk is called stuck sooner than in the cutline.
 */
const player = {
  label: "the player",
  page,
  get you() {
    return seen.you;
  },
  get boardId() {
    return seen.boardId;
  },
  walkDefaults: { maxSteps: 30, stallLimit: 4 },
};
assertObserver(player);

const atLeastX = (x) => () => (seen.you?.x ?? 0) >= x;

/**
 * The counters that must not move while a world is merely being selected.
 * A mark rather than a bare zero: the journeys run one after another in one
 * page, so by journey 2 both counters are already well past zero.
 */
const titleMark = () => ({ sockets: sockets.length, snapshots: seen.snapshots, titles: titleRequests.length });

/**
 * Selecting a world opens that world's title screen and joins NOTHING.
 *
 * This is the whole of the "open selected worlds to their title screen before
 * play" contract, and it is three claims, not one: no socket, no snapshot, and
 * the board now on screen came from /api/title for the world just chosen.
 * main.ts enterWorld stops after showTitle because selectWorldForTitle returns
 * `startPlay: false` (title_flow.ts) — pressing P is the only thing that joins.
 */
function expectTitleScreenPause(worldFilter, mark) {
  const joined = sockets.slice(mark.sockets);
  assert.equal(
    joined.length,
    0,
    `selecting ${worldFilter} must stop at its title screen, not join a room; opened ${JSON.stringify(joined)}`,
  );
  assert.equal(
    seen.snapshots,
    mark.snapshots,
    `selecting ${worldFilter} must not start play, but a snapshot arrived`,
  );
  const titles = titleRequests.slice(mark.titles);
  assert.ok(
    titles.some((url) => url.includes(`world=${worldFilter}`)),
    `the title screen must be ${worldFilter}'s own board 0; /api/title since selection: ${JSON.stringify(titles)}`,
  );
}

/**
 * Title screen -> world picker -> Play, exactly as a player does it.
 *
 * Passing `name` means we are on a fresh page load, where the launch name
 * prompt is showing: submitting it opens the world picker itself, so KeyW
 * would only type a "w" into the picker's search box. Without `name` we are
 * already at the title and have to open the picker ourselves.
 */
async function titleToPlay(worldFilter, { name } = {}) {
  if (name) {
    await page.keyboard.type(name);
    await page.keyboard.press("Enter");
    await settle();
  } else {
    await page.keyboard.press("KeyW");
    await settle();
  }
  await page.keyboard.type(worldFilter);
  await sleep(400);
  const mark = titleMark();
  await page.keyboard.press("Enter");
  await settle();
  expectTitleScreenPause(worldFilter, mark);
  await page.keyboard.press("KeyP");
}

try {
  // =========================================================================
  // JOURNEY 1 — the committed acceptance world (fixtures/accept.zwd)
  // =========================================================================
  console.log(`=== JOURNEY 1: Acceptance World (ACCEPT.ZZT) against ${baseURL} ===`);

  const response = await page.goto(baseURL);
  assert.equal(response?.status(), 200, "the client index must be served, not the build-me 404 page");
  await page.waitForLoadState("domcontentloaded");
  await page.waitForSelector("canvas[data-screen]", { timeout: 10000 });
  assert.equal(await page.locator("canvas[data-screen]").count(), 1, "screen canvas must be mounted");

  // The name prompt opens the world picker itself, so no KeyW on this first pass.
  await sleep(800);
  await page.keyboard.type("AcceptTester");
  await page.keyboard.press("Enter");
  await settle();
  await page.keyboard.type("ACCEPT");
  await sleep(400);
  const acceptMark = titleMark();
  await page.keyboard.press("Enter");
  await settle();
  // The launch flow reaches a world the same way the picker does, so it owes
  // the same pause: this is the first selection of the run, and nothing has
  // been joined yet at all.
  expectTitleScreenPause("ACCEPT", acceptMark);
  console.log("  - selected ACCEPT: its title screen, no socket, no snapshot");
  await page.keyboard.press("KeyP");

  await waitFor(() => seen.snapshots > 0, "the join snapshot");
  assert.ok(
    sockets.some((u) => u.includes("world=ACCEPT")),
    `client must open a socket for ACCEPT, opened: ${JSON.stringify(sockets)}`,
  );
  assert.equal(seen.boardId, 1, "play starts on board 1 (Acceptance Main)");
  assert.deepEqual(
    { x: seen.you.x, y: seen.you.y, health: seen.you.health },
    { x: 6, y: 12, health: 100 },
    "player spawns at the board's start square with full health",
  );
  assert.equal(seen.hud.gems, 0, "starts with no gems");
  assert.equal(seen.hud.ammo, 0, "starts with no ammo");
  assert.ok(seen.resumeToken, "join snapshot must carry a resume token (M13.2)");
  const firstPlayerId = seen.you.id;
  console.log(`  - joined: board ${seen.boardId} at (${seen.you.x},${seen.you.y}), token issued`);

  // Torch at x=8, then light it (board 1 is dark).
  await walkUntil(player, "ArrowRight", atLeastX(8), "the torch at x=8");
  await waitFor(() => seen.hud.torches === 1, "the torch to be collected");
  await page.keyboard.press("KeyT");
  await waitFor(() => seen.hud.torchTicks > 0, "the torch to be lit");
  assert.equal(seen.hud.torches, 0, "lighting spends the carried torch");
  console.log(`  - torch collected and lit: torchTicks=${seen.hud.torchTicks}`);

  // Gem at x=10: +1 gem, +10 score.
  await walkUntil(player, "ArrowRight", atLeastX(10), "the gem at x=10");
  await waitFor(() => seen.hud.gems === 1, "the gem to be collected");
  assert.equal(seen.hud.score, 10, "a gem scores 10");
  console.log(`  - gem collected: gems=${seen.hud.gems} score=${seen.hud.score}`);

  // Ammo at x=14: +5 shots.
  await walkUntil(player, "ArrowRight", atLeastX(14), "the ammo at x=14");
  await waitFor(() => seen.hud.ammo === 5, "the ammo to be collected");
  console.log(`  - ammo collected: ammo=${seen.hud.ammo}`);

  // Shoot: spends ammo. Held, like movement — a tap that ends between two tick
  // boundaries is never seen (note 1). The hold is FORCED here rather than
  // closed on observed movement, because a shot moves nobody: there is no
  // movement for step() to wait on.
  const ammoBeforeShot = seen.hud.ammo;
  await step(player, "Space", { hold: 120 });
  await waitFor(() => seen.hud.ammo < ammoBeforeShot, "a shot to spend ammo");
  console.log(`  - shot fired: ammo ${ammoBeforeShot} -> ${seen.hud.ammo}`);

  // Key at x=18, door at x=22 spends it.
  await walkUntil(player, "ArrowRight", atLeastX(18), "the key at x=18");
  await waitFor(() => seen.hud.keys.some(Boolean), "the key to be collected");
  console.log(`  - key collected: slot ${seen.hud.keys.findIndex(Boolean)}`);
  await walkUntil(player, "ArrowRight", atLeastX(23), "past the door at x=22");
  await waitFor(() => !seen.hud.keys.some(Boolean), "the door to consume the key");
  console.log("  - door opened and the key was spent");

  // Vendor Object at x=26: touching it sends this player a scroll. The hold is
  // FORCED, like the shot above: the vendor blocks row 12, so this step touches
  // it without moving anybody and there is no movement to close the loop on.
  await walkUntil(player, "ArrowRight", atLeastX(25), "the square west of the vendor");
  await step(player, "ArrowRight", { hold: 150 });
  await waitFor(
    () => has("scroll", (e) => (e.lines || []).some((l) => l.includes("Acceptance Vendor"))),
    "the vendor scroll",
  );
  const scroll = seen.events.find((e) => e.type === "scroll");
  assert.equal(scroll.playerStatId, 0, "the scroll belongs to the touching player");
  assert.ok(scroll.lines.some((l) => l.includes("!ba;")), "vendor scroll must offer the !ba hyperlink");
  console.log(`  - vendor scroll opened: ${JSON.stringify(scroll.title)}`);

  // Take the !ba hyperlink: #take gems 1 / #give ammo 5.
  const gemsBefore = seen.hud.gems;
  const ammoBefore = seen.hud.ammo;
  await page.keyboard.press("ArrowDown");
  await sleep(250);
  await page.keyboard.press("Enter");
  await waitFor(() => seen.hud.ammo === ammoBefore + 5, "the purchased ammo");
  assert.equal(seen.hud.gems, gemsBefore - 1, "the purchase spends exactly one gem");
  console.log(`  - bought ammo: gems ${gemsBefore}->${seen.hud.gems}, ammo ${ammoBefore}->${seen.hud.ammo}`);

  // Around the vendor (note 2), then take a hit from the bear at (30,12).
  // The bear chases, so it often lands the hit during the approach itself —
  // capture health BEFORE leaving the vendor square, and only go looking for
  // the bear if the walk east did not already cost health.
  const healthBeforeBear = seen.you.health;
  // Exactly one row up, and it matters: the loop below touches the bear on row
  // 12 by stepping down from row 11, so a step that covered two rows would
  // oscillate between 10 and 11 and never reach it. A closed loop is what makes
  // that one row rather than the old fixed 110ms, which under load could span
  // two tick boundaries.
  await step(player, "ArrowUp");
  assert.ok(seen.you.y < 12, `stepping up must leave row 12, at y=${seen.you.y}`);
  await walkUntil(player, "ArrowRight", atLeastX(30), "the bear's column");
  for (let i = 0; i < 14 && seen.you.health === healthBeforeBear; i++) {
    // Step onto the bear's row to touch it, then back off and try again.
    await step(player, "ArrowDown");
    if (seen.you.health !== healthBeforeBear) break;
    await step(player, "ArrowUp");
  }
  assert.ok(
    seen.you.health < healthBeforeBear,
    `the bear must damage the player; health stayed ${seen.you.health}`,
  );
  console.log(`  - bear damage taken: health ${healthBeforeBear} -> ${seen.you.health}`);

  // Passage at (34,12): a board transfer. M16.8a made "transfer" reachable.
  // The bear loop may have left the player on row 12, in which case walking
  // east crosses the passage directly; otherwise drop onto it at x=34.
  const boardBefore = seen.boardId;
  await walkUntil(
    player,
    "ArrowRight",
    () => seen.boardId !== boardBefore || (seen.you?.x ?? 0) >= 34,
    "the passage column",
  );
  if (seen.boardId === boardBefore) {
    await walkUntil(player, "ArrowDown", () => seen.boardId !== boardBefore, "the passage board change", { maxSteps: 8 });
  }
  await waitFor(() => seen.boardChanges > 0, "a boardChange message");
  assert.ok(has("transfer"), `the traveller must receive a "transfer" event (M16.8a); saw ${JSON.stringify(eventTypes())}`);
  assert.equal(seen.hud.ammo, ammoBefore + 5, "ammo survives the board change");
  console.log(`  - passage taken: board ${boardBefore} -> ${seen.boardId}, transfer event delivered`);

  // Reaper Object on board 2 runs #endgame on touch: death, then respawn.
  // #endgame routes through the same death/respawn path as damage (M16.6a).
  await walkUntil(player, "ArrowRight", () => has("death"), "the reaper's #endgame death", { maxSteps: 14 });
  await waitFor(() => has("death"), "the death event");
  await waitFor(() => seen.you.health <= 0, "health to reach zero on death");
  console.log("  - died to the reaper's #endgame");
  await waitFor(() => has("respawn"), "the respawn event", 12000);
  await waitFor(() => seen.you.health === 100, "health restored on respawn");
  const respawn = seen.events.find((e) => e.type === "respawn");
  assert.deepEqual(
    { x: seen.you.x, y: seen.you.y },
    { x: respawn.x, y: respawn.y },
    "the player stands where the respawn event said",
  );
  console.log(`  - respawned at (${seen.you.x},${seen.you.y}) with health ${seen.you.health}`);

  // Death costs RESPAWN_SCORE_PENALTY (100), which floors this run's score at
  // zero. Score again on board 2's gem — off the reaper's row — so the quit
  // below actually exercises the high-score entry instead of skipping it.
  assert.equal(seen.hud.score, 0, "death zeroes the score (RESPAWN_SCORE_PENALTY)");
  // Board 2's gem is one specific tile, (12,10), and the respawn is at (6,12),
  // so this is the journey's only two-axis walk. walkOnto, not two one-way
  // walkUntils: the row leg has to land ON row 10, and a step that covers two
  // tiles overshoots it (note 5).
  await walkOnto(player, 12, 10, "board 2's gem", { maxSteps: 16 });
  await waitFor(() => seen.hud.score > 0, "a score that qualifies for the high-score table");
  console.log(`  - scored again after respawn: score=${seen.hud.score}`);

  // --- save -----------------------------------------------------------------
  await page.keyboard.press("KeyS");
  await waitFor(() => has("savePrompt"), "the save prompt");
  await settle();
  await page.keyboard.type("ACCSAVE");
  await sleep(300);
  await page.keyboard.press("Enter");
  await waitFor(() => has("saveResult"), "the save result");
  const saveResult = seen.events.find((e) => e.type === "saveResult");
  assert.ok(!saveResult.error, `save must succeed, got error ${JSON.stringify(saveResult.error)}`);
  assert.equal(saveResult.filename, "ACCSAVE", "the save must use the typed name");
  console.log(`  - saved as ${saveResult.filename}.SAV`);
  await page.keyboard.press("Escape"); // dismiss the "Saving" window
  await settle();

  // --- quit, through the high-score flow, back to the title -----------------
  await page.keyboard.press("KeyQ");
  await waitFor(() => has("quitPrompt"), "the quit prompt");
  await settle();
  await page.keyboard.press("KeyY");
  // A qualifying score opens the name entry first; a zero score goes straight
  // back to the title. Handle both rather than assuming one.
  await waitFor(() => has("highScoreEntry") || seen.closes > 0, "the quit outcome");
  const scoreQualified = has("highScoreEntry");
  if (scoreQualified) {
    await settle();
    await page.keyboard.type("ACC");
    await sleep(400);
    await page.keyboard.press("Enter");
    await settle();
  }
  // The quit flow leaves a stack of windows (the score table, then notices);
  // the client only drops its socket once it is actually back at the title.
  for (let i = 0; i < 6 && seen.closes === 0; i++) {
    await page.keyboard.press("Escape");
    await sleep(600);
  }
  await waitFor(() => seen.closes > 0, "the socket to close on returning to the title");
  if (scoreQualified) {
    assert.ok(has("highScores"), `a recorded high score must show the table; saw ${JSON.stringify(eventTypes())}`);
  }
  await page.keyboard.press("Escape"); // ensure no window is left over the title
  await settle();
  console.log(`  - quit to title: high score ${scoreQualified ? "recorded" : "skipped (score 0)"}, socket closed`);

  // --- restore --------------------------------------------------------------
  // The title has no socket by design, so the restore is proven by rejoining
  // the restored world rather than by reading the title screen.
  resetRunState();
  await page.keyboard.press("KeyR");
  await sleep(1500); // /api/saves then the select list
  await page.keyboard.press("Enter"); // take the single saved game
  await sleep(2000); // /api/restore, then showTitle + the "Restore game" window
  await page.keyboard.press("Escape");
  await settle();
  await page.keyboard.press("KeyP");
  await waitFor(() => seen.snapshots > 0, "a snapshot after restoring and pressing Play");

  // DEVIATION snapshot-player-drop / account-sidecar-restore (PARITY.md):
  // World.Info carries one player's stats, so a joiner into a restored world
  // arrives fresh at the start square rather than inheriting the saved run.
  // Asserted as the documented contract, not as an accident.
  assert.deepEqual(
    { x: seen.you.x, y: seen.you.y, health: seen.you.health, ammo: seen.hud.ammo, gems: seen.hud.gems },
    { x: 6, y: 12, health: 100, ammo: 0, gems: 0 },
    "per PARITY.md snapshot-player-drop, a joiner into a restored world starts fresh at the start square",
  );
  assert.equal(seen.boardId, 1, "the restored world rejoins on board 1");
  console.log("  - restored ACCSAVE.SAV and rejoined (fresh joiner, per snapshot-player-drop)");

  // --- disconnect and resume ------------------------------------------------
  // Move off the spawn square first, so a resumed run is distinguishable from
  // a fresh join. Position is the discriminator here rather than inventory:
  // the restored save already consumed board 1's pickups.
  await walkUntil(player, "ArrowRight", atLeastX(12), "a square well clear of the spawn");
  const beforeReload = { x: seen.you.x, y: seen.you.y, health: seen.you.health };
  assert.notEqual(beforeReload.x, 6, "must have left the start square before disconnecting");
  const socketsBeforeReload = sockets.length;

  await page.reload(); // hard disconnect; sessionStorage keeps the resume token
  await page.waitForSelector("canvas[data-screen]", { timeout: 10000 });
  resetRunState();
  await sleep(900);
  // A reload re-runs the launch sequence, name prompt and all.
  await titleToPlay("ACCEPT", { name: "AcceptTester" });
  await waitFor(() => seen.snapshots > 0, "the snapshot after reconnecting");
  assert.ok(sockets.length > socketsBeforeReload, "the reload must open a new socket");
  assert.deepEqual(
    { x: seen.you.x, y: seen.you.y, health: seen.you.health },
    beforeReload,
    `resume must reclaim the run in place, not spawn a fresh player (was ${JSON.stringify(beforeReload)})`,
  );
  console.log(`  - disconnected and resumed in place at (${seen.you.x},${seen.you.y})`);

  console.log("JOURNEY 1 PASSED");

  // =========================================================================
  // JOURNEY 2 — a shipped world, started normally (never stageTownPlayer)
  // =========================================================================
  console.log("=== JOURNEY 2: TOWN route without state staging ===");
  resetRunState();
  const townSocketsBefore = sockets.length;

  // Leave the current run the way a player does, then pick TOWN from the picker.
  await page.keyboard.press("KeyQ");
  await waitFor(() => has("quitPrompt"), "the quit prompt leaving ACCEPT");
  await settle();
  await page.keyboard.press("KeyY");
  await sleep(1500);
  for (let i = 0; i < 4; i++) {
    await page.keyboard.press("Escape"); // clear whatever score windows appeared
    await sleep(400);
  }
  resetRunState();
  await titleToPlay("TOWN");
  await waitFor(() => seen.snapshots > 0, "the TOWN join snapshot", 15000);
  assert.ok(
    sockets.slice(townSocketsBefore).some((u) => u.includes("world=TOWN")),
    `client must open a socket for TOWN, opened: ${JSON.stringify(sockets.slice(townSocketsBefore))}`,
  );
  assert.equal(seen.you.health, 100, "a fresh TOWN player starts at full health");
  console.log(`  - joined TOWN at (${seen.you.x},${seen.you.y}) on board ${seen.boardId}`);

  // Prove the shipped world is genuinely traversable under real input.
  const townStart = { x: seen.you.x, y: seen.you.y };
  let moved = false;
  for (const dir of ["ArrowRight", "ArrowDown", "ArrowLeft", "ArrowUp"]) {
    for (let i = 0; i < 6 && !moved; i++) {
      await step(player, dir);
      if (seen.you.x !== townStart.x || seen.you.y !== townStart.y) moved = true;
    }
    if (moved) break;
  }
  assert.ok(moved, `the TOWN player must be able to move from ${JSON.stringify(townStart)}`);
  assert.ok(seen.diffs > 0, "TOWN must stream diffs to the client");
  console.log(`  - TOWN traversed: (${townStart.x},${townStart.y}) -> (${seen.you.x},${seen.you.y})`);

  console.log("JOURNEY 2 PASSED");

  assert.deepEqual(pageErrors, [], "the client must not raise page errors");
  assert.deepEqual(consoleErrors, [], "the client must not log console errors");

  console.log(`ALL JOURNEYS PASSED — sockets=${sockets.length}, final StateHash=${seen.stateHash}`);
  await context.tracing.stop();
  await browser.close();
  process.exit(0);
} catch (err) {
  console.error("E2E journey FAILED:", err);
  console.error("observed:", JSON.stringify({ ...seen, events: eventTypes() }, null, 2));
  console.error("final server StateHash:", seen.stateHash);
  console.error("pageErrors:", pageErrors);
  console.error("consoleErrors:", consoleErrors);
  const tracePath = path.join(resultsDir, "e2e_journey_trace.zip");
  const screenshotPath = path.join(resultsDir, "e2e_journey_failure.png");
  const transcriptPath = path.join(resultsDir, "e2e_journey_transcript.json");
  fs.writeFileSync(
    transcriptPath,
    JSON.stringify({ finalStateHash: seen.stateHash, sockets, transcript }, null, 2),
  );
  await context.tracing.stop({ path: tracePath });
  await page.screenshot({ path: screenshotPath });
  console.error(`Saved trace ${tracePath}, screenshot ${screenshotPath}, transcript ${transcriptPath}`);
  await browser.close();
  process.exit(1);
}
