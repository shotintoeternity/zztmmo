// M36.24 — the client keeps one socket, one player, and one title screen.
//
// Driven by engine/m36_24_browser_test.go, which hosts ACCEPT on the production
// zzt-server binary. Two races, each set up so it provably happens rather than
// hoping localhost is slow enough:
//
//  1. Double P. Two keydowns are dispatched in ONE page.evaluate, so the second
//     lands while the first socket is still CONNECTING. Before M36.24, connect()
//     refused only an OPEN socket, so this opened two sockets and joined two
//     players under one nickname; quitting closed only the second, leaving a
//     ghost in the room. Asserted from the sockets the page opened, the join
//     frames it sent, and the server's own player count.
//
//  2. P during the title fetch. Quitting repaints the title from /api/title,
//     which is held open here until P has been pressed. Before M36.24,
//     showTitle resumed after its await regardless, painting the title over
//     play and opening the title stream behind it.
//
// The repeated challenge retry (the stacked input sampler) is asserted in
// challenge_journey.test.mjs, which already hosts a challenge.

import assert from "node:assert/strict";
import { chromium } from "playwright";
import { gridToArt, hasText, installDecoder, installImageProbe, launchOpensPicker, markProfileWarm, readGrid } from "./lib/canvas.mjs";

const baseURL = process.env.BASE_URL || "http://127.0.0.1:8080";

const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 1280, height: 720 } });
await markProfileWarm(context);
const page = await context.newPage();
const pageErrors = [];
const sockets = [];
const joins = [];
const requests = [];

page.on("pageerror", (err) => pageErrors.push(String(err)));
page.on("request", (request) => requests.push({ at: Date.now(), path: new URL(request.url()).pathname }));
page.on("websocket", (ws) => {
  sockets.push(ws.url());
  ws.on("framesent", (frame) => {
    try {
      const msg = JSON.parse(String(frame.payload));
      if (msg.type === "join") joins.push(msg);
    } catch {
      // not JSON: nothing this suite counts
    }
  });
});

// The title fetch is released only when the test says so.
let holdTitle = false;
let titleHeld = null;
let releaseTitle = null;
await page.route(
  (url) => url.pathname === "/api/title",
  async (route) => {
    if (!holdTitle) {
      await route.continue();
      return;
    }
    holdTitle = false;
    const released = new Promise((resolve) => {
      releaseTitle = resolve;
    });
    titleHeld?.();
    await released;
    await route.continue();
  },
);

async function waitForCells(pred, describe, timeoutMs = 15000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const cells = await readGrid(page);
    if (pred(cells)) return cells;
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${describe}\n${gridToArt(cells)}`);
    await page.waitForTimeout(80);
  }
}

async function waitFor(pred, describe, timeoutMs = 15000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const value = await pred();
    if (value) return value;
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${describe}`);
    await page.waitForTimeout(80);
  }
}

async function serverPlayers() {
  const response = await fetch(baseURL + "/api/worlds");
  const data = await response.json();
  return (data.worlds ?? []).reduce((sum, entry) => sum + (typeof entry === "object" ? (entry.players ?? 0) : 0), 0);
}

const inPlay = (cells) => hasText(cells, "Health:") && !hasText(cells, "P  Play");

try {
  await installImageProbe(page);
  const response = await page.goto(baseURL + "/play/ACCEPT");
  assert.equal(response?.status(), 200, "client index must load");
  await page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await installDecoder(page);
  await page.keyboard.type("Sock");
  await page.keyboard.press("Enter");
  await page.waitForTimeout(600);
  if (launchOpensPicker(page)) {
    await page.keyboard.type("ACCEPT");
    await page.waitForTimeout(600);
    await page.keyboard.press("Enter");
  }
  await waitForCells((cells) => hasText(cells, "P  Play"), "ACCEPT title menu");
  assert.equal(sockets.length, 0, "the title screen opens no socket");

  // 1. Double P: the second keydown arrives while the first socket is CONNECTING.
  await page.evaluate(() => {
    const canvas = document.querySelector("canvas[data-screen]");
    for (let i = 0; i < 2; i += 1) {
      canvas.dispatchEvent(new KeyboardEvent("keydown", { code: "KeyP", key: "p", bubbles: true, cancelable: true }));
      canvas.dispatchEvent(new KeyboardEvent("keyup", { code: "KeyP", key: "p", bubbles: true, cancelable: true }));
    }
  });
  await waitForCells(inPlay, "play after a double P");
  await page.waitForTimeout(1500);
  assert.equal(sockets.length, 1, `a double P must open one socket, got ${JSON.stringify(sockets)}`);
  assert.equal(joins.length, 1, `a double P must send one join, got ${joins.length}`);
  assert.equal(await serverPlayers(), 1, "the server must hold one player, not a ghost beside them");
  console.log("  - a double P opens one socket and joins one player");

  // Quitting leaves nobody behind.
  holdTitle = true;
  const heldNow = new Promise((resolve) => {
    titleHeld = resolve;
  });
  await page.keyboard.press("KeyQ");
  await page.waitForTimeout(600);
  await page.keyboard.press("KeyY");
  await heldNow;
  await waitFor(async () => (await serverPlayers()) === 0, "the room to empty after quitting");
  console.log("  - quitting empties the room");

  // 2. P while /api/title is still in flight.
  const pressedAt = Date.now();
  await page.keyboard.press("KeyP");
  await waitFor(() => sockets.length === 2, "the socket P opened during the title fetch");
  await waitForCells(inPlay, "play while the title fetch is held");
  releaseTitle();
  await page.waitForTimeout(1500);
  const lateStream = requests.filter((r) => r.at >= pressedAt && r.path === "/api/title/stream");
  assert.deepEqual(lateStream, [], "a title fetch that resolves after P must not open the title stream");
  await waitForCells(inPlay, "play, with no title painted over it", 3000);
  assert.equal(sockets.length, 2, `P during the title fetch opens exactly one socket, got ${JSON.stringify(sockets)}`);
  assert.equal(joins.length, 2, "and sends exactly one join");
  assert.equal(await serverPlayers(), 1, "and the server holds exactly one player");
  console.log("  - P during the title fetch plays, and the late title paints nothing");

  assert.deepEqual(pageErrors, [], `page errors: ${pageErrors.join("\n")}`);
  console.log("socket browser journey passed");
} catch (err) {
  try {
    console.error(`screen at failure:\n${gridToArt(await readGrid(page))}`);
  } catch (dumpErr) {
    console.error(`screen dump failed: ${dumpErr}`);
  }
  throw err;
} finally {
  await browser.close();
}
