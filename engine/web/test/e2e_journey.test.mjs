// M16.11 — browser end-to-end player journey.
//
// This drives the real Vite-built client in headless Chromium against the real
// zzt-server subprocess, and asserts on the protocol traffic that browser
// actually exchanges (page.on("websocket") frames) plus the rendered canvas.
//
// WHY THE PROTOCOL FRAMES: the client renders to a single <canvas> via the
// CP437 atlas and exposes no DOM state, so there is nothing meaningful to
// assert against in the page. The frames are the client's real observable
// behaviour — a keystroke that never reached the server, or a server reply the
// client never got, shows up here immediately. Asserting on them keeps the
// test honest without adding test-only hooks to production code.
//
// TWO THINGS THIS TEST LEARNED THE HARD WAY, both of which silently produced a
// "passing" test that never played the game at all:
//
//  1. Input is SAMPLED, not latched. connect() starts a 55ms timer that reads
//     the currently-held key set (main.ts sendInput/currentMask). An
//     instantaneous page.keyboard.press() is usually gone before the next
//     sample, so movement never happens. Every movement here holds the key
//     down across at least one sample — see step().
//  2. The vendor Object at x=26 BLOCKS the corridor on row 12. The east half
//     of the board (bear, passage) is only reachable by walking around it.
//
// NOT COVERED HERE (deliberately — see NOTES.md M16.11): save/quit/restore and
// disconnect/resume. Those need their own modal-driven flows and are filed as
// follow-up work rather than asserted loosely.

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { chromium } from "playwright";

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
/** Latest server-authoritative view, as the client itself received it. */
const seen = {
  hud: null,
  you: null,
  boardId: null,
  resumeToken: null,
  events: [],
  snapshots: 0,
  boardChanges: 0,
  diffs: 0,
};

page.on("pageerror", (err) => pageErrors.push(String(err)));
page.on("console", (msg) => {
  if (msg.type() === "error") consoleErrors.push(msg.text());
});
page.on("websocket", (ws) => {
  sockets.push(ws.url());
  ws.on("framereceived", (frame) => {
    let msg;
    try {
      msg = JSON.parse(frame.payload);
    } catch {
      return;
    }
    const body = msg.type === "boardChange" ? msg.snapshot : msg;
    if (msg.type === "snapshot") seen.snapshots++;
    if (msg.type === "boardChange") seen.boardChanges++;
    if (msg.type === "diff") seen.diffs++;
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

/** Poll until pred() holds, or fail with what was actually observed. */
async function waitFor(pred, describe, timeoutMs = 8000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    if (pred()) return;
    if (Date.now() > deadline) {
      throw new Error(
        `timed out waiting for ${describe}\n  last seen: board=${seen.boardId} ` +
          `pos=(${seen.you?.x},${seen.you?.y}) hp=${seen.you?.health} ` +
          `hud=${JSON.stringify(seen.hud)}\n  events: ${JSON.stringify(eventTypes())}`,
      );
    }
    await sleep(60);
  }
}

const eventTypes = () => [...new Set(seen.events.map((e) => e.type))];
const hasEvent = (type, match) =>
  seen.events.some((e) => e.type === type && (!match || match(e)));

/** Hold a key across at least one 55ms input sample (see header note 1). */
async function step(code, holdMs = 95) {
  await page.keyboard.down(code);
  await sleep(holdMs);
  await page.keyboard.up(code);
  await sleep(70);
}

/**
 * Walk in one direction until `done()` is true. Returns when it is; throws if
 * the player stops making progress, so a blocked route fails loudly here
 * instead of silently sitting still for the rest of the journey.
 */
async function walkUntil(code, done, describe, maxSteps = 30) {
  let stalled = 0;
  for (let i = 0; i < maxSteps; i++) {
    if (done()) return;
    const before = `${seen.you?.x},${seen.you?.y},${seen.boardId}`;
    await step(code);
    if (`${seen.you?.x},${seen.you?.y},${seen.boardId}` === before) {
      if (++stalled >= 4) {
        throw new Error(
          `stuck walking ${code} toward ${describe} at (${seen.you?.x},${seen.you?.y}) on board ${seen.boardId}`,
        );
      }
    } else {
      stalled = 0;
    }
  }
  if (!done()) {
    throw new Error(
      `never reached ${describe}; stopped at (${seen.you?.x},${seen.you?.y}) on board ${seen.boardId}`,
    );
  }
}

const atLeastX = (x) => () => (seen.you?.x ?? 0) >= x;

// --- journey ----------------------------------------------------------------

try {
  console.log(`=== JOURNEY: Acceptance World (ACCEPT.ZZT) against ${baseURL} ===`);

  const response = await page.goto(baseURL);
  assert.equal(response?.status(), 200, "the client index must be served, not the build-me 404 page");
  await page.waitForLoadState("domcontentloaded");

  // The client mounts exactly one screen canvas; if the bundle failed to boot
  // this is where the journey stops rather than typing into the void.
  await page.waitForSelector("canvas[data-screen]", { timeout: 10000 });
  assert.equal(await page.locator("canvas[data-screen]").count(), 1, "screen canvas must be mounted");

  // Title screen: name, then the world picker, then Play.
  await sleep(700);
  await page.keyboard.type("AcceptTester");
  await page.keyboard.press("Enter");
  await sleep(700);
  console.log("  - named the player; world picker open");

  await page.keyboard.type("ACCEPT");
  await sleep(400);
  await page.keyboard.press("Enter");
  await sleep(700);
  await page.keyboard.press("KeyP");
  console.log("  - selected ACCEPT and pressed Play");

  // Joining is what proves the title flow actually did something.
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
  console.log(`  - joined: board ${seen.boardId} at (${seen.you.x},${seen.you.y}), token issued`);

  // Gem at x=10: +1 gem, +10 score, +1 health (vanilla ZZT).
  await walkUntil("ArrowRight", atLeastX(10), "the gem at x=10");
  await waitFor(() => seen.hud.gems === 1, "the gem to be collected");
  assert.equal(seen.hud.score, 10, "a gem scores 10");
  console.log(`  - gem collected: gems=${seen.hud.gems} score=${seen.hud.score}`);

  // Ammo at x=14: +5 shots.
  await walkUntil("ArrowRight", atLeastX(14), "the ammo at x=14");
  await waitFor(() => seen.hud.ammo === 5, "the ammo to be collected");
  console.log(`  - ammo collected: ammo=${seen.hud.ammo}`);

  // Key at x=18: the cyan key lights up in the HUD.
  await walkUntil("ArrowRight", atLeastX(18), "the key at x=18");
  await waitFor(() => seen.hud.keys.some(Boolean), "the key to be collected");
  const heldKey = seen.hud.keys.findIndex(Boolean);
  console.log(`  - key collected: slot ${heldKey}`);

  // Door at x=22: opening it spends the key.
  await walkUntil("ArrowRight", atLeastX(23), "past the door at x=22");
  await waitFor(() => !seen.hud.keys.some(Boolean), "the door to consume the key");
  console.log("  - door opened and the key was spent");

  // Vendor Object at x=26: touching it sends a scroll to this player.
  await walkUntil("ArrowRight", atLeastX(25), "the square west of the vendor");
  await step("ArrowRight", 150);
  await waitFor(
    () => hasEvent("scroll", (e) => (e.lines || []).some((l) => l.includes("Acceptance Vendor"))),
    "the vendor scroll",
  );
  const scroll = seen.events.find((e) => e.type === "scroll");
  assert.equal(scroll.playerStatId, 0, "the scroll belongs to the touching player");
  assert.ok(
    scroll.lines.some((l) => l.includes("!ba;")),
    `vendor scroll must offer the !ba hyperlink, got ${JSON.stringify(scroll.lines)}`,
  );
  console.log(`  - vendor scroll opened: ${JSON.stringify(scroll.title)}`);

  // Buy: move onto the !ba hyperlink line and take it. #take gems 1 / #give ammo 5.
  const gemsBefore = seen.hud.gems;
  const ammoBefore = seen.hud.ammo;
  await page.keyboard.press("ArrowDown");
  await sleep(250);
  await page.keyboard.press("Enter");
  await waitFor(() => seen.hud.ammo === ammoBefore + 5, "the purchased ammo");
  assert.equal(seen.hud.gems, gemsBefore - 1, "the purchase spends exactly one gem");
  console.log(`  - bought ammo: gems ${gemsBefore}->${seen.hud.gems}, ammo ${ammoBefore}->${seen.hud.ammo}`);

  // The vendor blocks row 12, so the passage is reached around it (header note 2).
  await step("ArrowUp", 150);
  assert.ok(seen.you.y < 12, `stepping up must leave row 12, at y=${seen.you.y}`);
  await walkUntil("ArrowRight", atLeastX(34), "the passage column");
  console.log(`  - walked around the vendor to x=${seen.you.x}, y=${seen.you.y}`);

  // Passage at (34,12): a board transfer. M16.8a made the "transfer" event
  // reachable on the wire for the traveller; assert it actually arrives.
  const boardBefore = seen.boardId;
  await walkUntil("ArrowDown", () => seen.boardId !== boardBefore, "the passage board change", 8);
  await waitFor(() => seen.boardChanges > 0, "a boardChange message");
  assert.notEqual(seen.boardId, boardBefore, "the passage must move the player to another board");
  assert.ok(
    hasEvent("transfer"),
    `the traveller must receive a "transfer" event (M16.8a); saw ${JSON.stringify(eventTypes())}`,
  );
  console.log(`  - passage taken: board ${boardBefore} -> ${seen.boardId}, transfer event delivered`);

  // Inventory survives the transfer.
  assert.equal(seen.hud.ammo, ammoBefore + 5, "ammo survives the board change");

  // The client kept streaming and never threw.
  assert.ok(seen.diffs > 0, "the client must have received diff frames");
  assert.deepEqual(pageErrors, [], "the client must not raise page errors");
  assert.deepEqual(consoleErrors, [], "the client must not log console errors");

  console.log(
    `JOURNEY PASSED — ${seen.snapshots} snapshot(s), ${seen.diffs} diffs, ` +
      `${seen.boardChanges} board change(s), events: ${JSON.stringify(eventTypes())}`,
  );

  await context.tracing.stop();
  await browser.close();
  process.exit(0);
} catch (err) {
  console.error("E2E journey FAILED:", err);
  console.error("observed:", JSON.stringify({ ...seen, events: eventTypes() }, null, 2));
  console.error("pageErrors:", pageErrors);
  console.error("consoleErrors:", consoleErrors);
  const tracePath = path.join(resultsDir, "e2e_journey_trace.zip");
  const screenshotPath = path.join(resultsDir, "e2e_journey_failure.png");
  await context.tracing.stop({ path: tracePath });
  await page.screenshot({ path: screenshotPath });
  console.error(`Saved failure trace to ${tracePath} and screenshot to ${screenshotPath}`);
  await browser.close();
  process.exit(1);
}
