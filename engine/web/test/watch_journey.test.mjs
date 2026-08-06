// M22.2 — /watch/<world> is a shareable read-only link, in a real browser.

import assert from "node:assert/strict";
import { chromium } from "playwright";
import { baseURL, gridToArt, hasText, installDecoder, installImageProbe, readGrid, saveText, textAt } from "./lib/canvas.mjs";

const WORLD = "TOWN";
const EMPTY_WORLD = "ACCEPT";
const BOARD_COLS = 60;

const boardText = (cells, row) => textAt(cells, 0, row, BOARD_COLS);
const onBoard = (cells, needle) => {
  for (let row = 0; row < 25; row += 1) {
    if (boardText(cells, row).includes(needle)) return true;
  }
  return false;
};
const latestPlayerPositions = (c) => {
  for (let i = c.received.length - 1; i >= 0; i -= 1) {
    const msg = c.received[i];
    const body = msg?.type === "boardChange" ? msg.snapshot : msg;
    if (Array.isArray(body?.players) && body.players.length > 0) {
      return body.players.map((p) => `${p.id}:${p.x},${p.y}`).sort().join("|");
    }
  }
  return "";
};

async function waitForPage(c, pred, describe, timeoutMs = 30000) {
  const deadline = Date.now() + timeoutMs;
  let cells = null;
  for (;;) {
    cells = await readGrid(c.page);
    if (pred(cells)) return cells;
    if (Date.now() > deadline) {
      saveText(`m222-timeout-${c.label}-${describe.replace(/\W+/g, "-")}.txt`, gridToArt(cells));
      throw new Error(`${c.label}: timed out waiting for ${describe}; the screen was:\n${gridToArt(cells)}`);
    }
    await c.page.waitForTimeout(80);
  }
}

async function waitFor(pred, describe, timeoutMs = 20000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const value = await pred();
    if (value) return value;
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${describe}`);
    await new Promise((resolve) => setTimeout(resolve, 80));
  }
}

async function waitForPositionChange(c, before, timeoutMs = 3000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const current = latestPlayerPositions(c);
    if (current && current !== before) return current;
    if (Date.now() > deadline) return "";
    await c.page.waitForTimeout(80);
  }
}

async function playersIn(world) {
  const response = await fetch(`${baseURL}/api/worlds`);
  assert.ok(response.ok, `/api/worlds answered ${response.status}`);
  const data = await response.json();
  const entry = (data.worlds || []).find((w) => (w.world || w).toUpperCase() === world.toUpperCase());
  assert.ok(entry, `${world} must be listed by /api/worlds: ${JSON.stringify(data)}`);
  return entry.players ?? 0;
}

async function openClient(label, path) {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1280, height: 720 } });
  const page = await context.newPage();
  const c = { label, browser, context, page, pageErrors: [], consoleErrors: [], sent: [], received: [] };
  page.on("pageerror", (err) => c.pageErrors.push(String(err)));
  page.on("console", (msg) => {
    if (msg.type() === "error") c.consoleErrors.push(msg.text());
  });
  page.on("websocket", (ws) => {
    ws.on("framesent", (frame) => {
      try {
        c.sent.push(JSON.parse(String(frame.payload)));
      } catch {
        c.sent.push(String(frame.payload));
      }
    });
    ws.on("framereceived", (frame) => {
      try {
        c.received.push(JSON.parse(String(frame.payload)));
      } catch {
        /* ignore binary/non-JSON frames */
      }
    });
  });
  await installImageProbe(page);
  const response = await page.goto(baseURL + path, { waitUntil: "domcontentloaded" });
  assert.equal(response?.status(), 200, `${label}: ${path} must serve the client`);
  await page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await installDecoder(page);
  await waitForPage(c, (cells) => hasText(cells, "Type your name"), "the launch name prompt");
  await page.keyboard.type(label, { delay: 8 });
  await page.keyboard.press("Enter");
  return c;
}

async function closeAll(clients) {
  for (const c of clients.reverse()) {
    await c.browser.close();
  }
}

const clients = [];

try {
  // ACT 1 — an idle hosted world is watchable and not counted as occupied.
  assert.equal(await playersIn(EMPTY_WORLD), 0, `${EMPTY_WORLD} should start empty`);
  const idleWatcher = await openClient("IdleWatcher", `/watch/${EMPTY_WORLD}`);
  clients.push(idleWatcher);
  await waitForPage(idleWatcher, (cells) => hasText(cells, "Watching") && hasText(cells, "1 watching"), "the idle watch screen");
  await waitFor(() => idleWatcher.sent.some((m) => m && m.type === "join" && m.spectate === true), "the idle watcher to send a spectate join");
  assert.equal(await playersIn(EMPTY_WORLD), 0, "watching an idle hosted world must not occupy it");
  assert.equal(new URL(idleWatcher.page.url()).pathname, `/watch/${EMPTY_WORLD}`, "an idle watch link must stay shareable");
  console.log(`  - /watch/${EMPTY_WORLD}: idle board rendered, picker occupancy stayed 0`);

  // ACT 2 — a player in TOWN occupies the picker once, and a watcher does not
  // add a second occupant.
  const player = await openClient("Ada", `/play/${WORLD}`);
  clients.push(player);
  await waitForPage(player, (cells) => hasText(cells, "P  Play"), `${WORLD}'s title screen for the player`);
  await player.page.keyboard.press("KeyP");
  await waitForPage(player, (cells) => hasText(cells, "Health:100"), "the player joined the room");
  await waitFor(() => playersIn(WORLD).then((n) => n === 1), `${WORLD} to have one player`);
  console.log(`  - /play/${WORLD}: player joined and picker occupancy is 1`);

  const watcher = await openClient("Watcher", `/watch/${WORLD}`);
  clients.push(watcher);
  const watchStart = await waitForPage(
    watcher,
    (cells) => hasText(cells, "Watching") && hasText(cells, "1 watching") && !hasText(cells, "Health:100"),
    "the live watch screen",
  );
  await waitFor(() => latestPlayerPositions(watcher), "the watcher to receive the room roster");
  await waitFor(() => watcher.sent.some((m) => m && m.type === "join" && m.spectate === true), "the live watcher to send a spectate join");
  const watchJoin = watcher.sent.find((m) => m && m.type === "join");
  assert.deepEqual(watchJoin, { type: "join", spectate: true }, `watcher join must claim nothing else: ${JSON.stringify(watchJoin)}`);
  assert.equal(await playersIn(WORLD), 1, "a watcher must not change the picker occupancy");
  assert.equal(new URL(watcher.page.url()).pathname, `/watch/${WORLD}`, "the live watch link must stay in the address bar");

  // ACT 3 — the watcher is attached to the live room and sees the player move.
  const beforePlayers = latestPlayerPositions(watcher);
  let afterPlayers = "";
  for (const key of ["ArrowRight", "ArrowLeft", "ArrowDown", "ArrowUp"]) {
    await player.page.keyboard.down(key);
    await player.page.waitForTimeout(800);
    await player.page.keyboard.up(key);
    afterPlayers = await waitForPositionChange(watcher, beforePlayers);
    if (afterPlayers) break;
  }
  assert.ok(afterPlayers, `the watcher never saw the player's roster position move from ${beforePlayers}`);
  const afterMove = await readGrid(watcher.page);
  assert.ok(!onBoard(afterMove, "Choose a World"), `a watcher must not land in the picker:\n${gridToArt(afterMove)}`);
  assert.ok(hasText(afterMove, "Watching"), `a watcher must stay read-only after the room moves:\n${gridToArt(afterMove)}`);
  console.log(`  - /watch/${WORLD}: watcher saw the live player move without joining`);

  for (const c of clients) {
    assert.deepEqual(c.pageErrors, [], `${c.label} page errors`);
    assert.deepEqual(c.consoleErrors, [], `${c.label} console errors`);
  }
  console.log("ALL WATCH-LINK ACTS PASSED");
  await closeAll(clients);
  process.exit(0);
} catch (err) {
  console.error("watch-link journey FAILED:", err);
  for (const c of clients) {
    console.error(`${c.label} sent:`, JSON.stringify(c.sent));
    console.error(`${c.label} received types:`, JSON.stringify(c.received.map((m) => m.type)));
    console.error(`${c.label} pageErrors:`, JSON.stringify(c.pageErrors));
    console.error(`${c.label} consoleErrors:`, JSON.stringify(c.consoleErrors));
    try {
      console.error(`${c.label} screen:\n${gridToArt(await readGrid(c.page))}`);
    } catch {
      /* ignore */
    }
  }
  await closeAll(clients);
  process.exit(1);
}
