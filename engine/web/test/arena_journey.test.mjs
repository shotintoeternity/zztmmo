// M30.1 — ARENA is the explicit first-party PvP opt-in.

import assert from "node:assert/strict";
import { chromium } from "playwright";
import { at, step, walkOnto, assertObserver } from "./lib/walk.mjs";
import { gridToArt, hasText, installDecoder, installImageProbe, markProfileWarm, readGrid } from "./lib/canvas.mjs";

const baseURL = process.env.BASE_URL || "http://127.0.0.1:8080";

async function waitFor(pred, describe, timeoutMs = 20000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const value = await pred();
    if (value) return value;
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${describe}`);
    await new Promise((resolve) => setTimeout(resolve, 80));
  }
}

async function waitForPage(c, pred, describe, timeoutMs = 20000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const cells = await readGrid(c.page);
    if (pred(cells)) return cells;
    if (Date.now() > deadline) throw new Error(`${c.label}: timed out waiting for ${describe}\n${gridToArt(cells)}`);
    await c.page.waitForTimeout(80);
  }
}

async function openClient(label, path) {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1280, height: 720 } });
  await markProfileWarm(context);
  const page = await context.newPage();
  const c = {
    label,
    browser,
    context,
    page,
    pageErrors: [],
    consoleErrors: [],
    httpErrors: [],
    sockets: [],
    snapshots: 0,
    boardChanges: 0,
    world: null,
    you: null,
    hud: null,
    boardId: null,
    roster: [],
    events: [],
    walkDefaults: { maxSteps: 26, stallLimit: 4 },
  };
  assertObserver(c);

  page.on("pageerror", (err) => c.pageErrors.push(String(err)));
  page.on("console", (msg) => {
    if (msg.type() === "error") c.consoleErrors.push(msg.text());
  });
  page.on("response", (response) => {
    if (response.status() >= 500) c.httpErrors.push({ url: response.url().replace(baseURL, ""), status: response.status() });
  });
  page.on("websocket", (ws) => {
    c.sockets.push(ws.url());
    ws.on("framereceived", (frame) => {
      let msg;
      try {
        msg = JSON.parse(String(frame.payload));
      } catch {
        return;
      }
      const body = msg.type === "boardChange" ? msg.snapshot : msg;
      if (msg.type === "snapshot") c.snapshots++;
      if (msg.type === "boardChange") c.boardChanges++;
      if (body.world) c.world = body.world;
      if (body.hud) c.hud = body.hud;
      if (body.you) c.you = body.you;
      if (Array.isArray(body.players)) {
        c.roster = body.players;
        const me = body.players.find((p) => p.id === c.you?.id);
        if (me) c.you = me;
      }
      if (typeof body.boardId === "number") c.boardId = body.boardId;
      for (const ev of body.events || []) c.events.push(ev);
    });
  });

  await installImageProbe(page);
  const response = await page.goto(`${baseURL}${path}`, { waitUntil: "domcontentloaded" });
  assert.equal(response?.status(), 200, `${label}: ${path} must serve the client`);
  await page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await installDecoder(page);
  await waitForPage(c, (cells) => hasText(cells, "Type your name"), "the launch name prompt");
  await page.keyboard.type(label, { delay: 8 });
  await page.keyboard.press("Enter");
  return c;
}

async function closeAll(clients) {
  for (const c of clients.reverse()) await c.browser.close();
}

const clients = [];

try {
  const ada = await openClient("Ada", "/play/ARENA");
  clients.push(ada);
  await waitForPage(ada, (cells) => hasText(cells, "ARENA") && hasText(cells, "PRESS P TO FIGHT"), "ARENA title screen");
  assert.equal(ada.sockets.length, 0, "/play/ARENA must pause on the title screen without joining");
  console.log("  - /play/ARENA lands on ARENA's title screen before joining");

  await ada.page.keyboard.press("KeyP");
  await waitFor(() => ada.snapshots > 0 && ada.world === "ARENA" && ada.boardId === 1 && ada.you, "Ada's ARENA join snapshot");
  await waitForPage(ada, (cells) => hasText(cells, "SAFE READY ROOM"), "Ada's arena floor");
  assert.ok(ada.sockets.some((url) => url.includes("world=ARENA")), "P must join the ARENA instance");

  await walkOnto(ada, 32, 5, "ready-room ammo");
  await waitFor(() => ada.hud?.ammo > 0, "Ada to collect arena ammo");
  await walkOnto(ada, 31, 5, "the firing square beside the spawn");

  const bo = await openClient("Bo", "/play/LOBBY");
  clients.push(bo);
  await waitForPage(bo, (cells) => hasText(cells, "LOBBY") && hasText(cells, "PRESS P TO JOIN"), "LOBBY title screen");
  await bo.page.keyboard.press("KeyP");
  await waitFor(() => bo.snapshots > 0 && bo.world === "LOBBY" && bo.boardId === 1 && bo.you, "Bo's LOBBY join snapshot");
  await walkOnto(bo, 48, 13, "the ARENA gate column", { maxSteps: 30 });
  await walkOnto(bo, 48, 9, "ARENA transit gate", { maxSteps: 30, until: () => bo.world === "ARENA" });
  await waitFor(() => bo.world === "ARENA" && bo.boardId === 1 && bo.you, `Bo to enter ARENA from ${at(bo)}`);
  assert.match(bo.page.url(), /\/play\/ARENA$/, "the address bar follows the LOBBY -> ARENA transit");
  console.log("  - LOBBY gate (48,9) transfers directly into active ARENA play");

  await walkOnto(bo, 30, 5, "the arena target square", { maxSteps: 12 });
  await waitFor(() => ada.roster.length === 2 && bo.roster.length === 2, "both players to see the ARENA roster");
  assert.equal(`${bo.you.x},${bo.you.y}`, "30,5", `Bo should stand on the target square for the PvP shot, got ${at(bo)}`);
  assert.equal(`${ada.you.x},${ada.you.y}`, "31,5", `Ada should stand beside Bo for the PvP shot, got ${at(ada)}`);
  const boHealth = bo.hud.health;
  const adaHealth = ada.hud.health;
  const ammoBefore = ada.hud.ammo;
  await step(ada, "Space", { hold: 180 });
  await waitFor(() => bo.hud.health < boHealth, "Bo's health to drop from Ada's shot");
  assert.equal(ada.hud.health, adaHealth, "the arena shot must not hurt the shooter");
  assert.ok(ada.hud.ammo < ammoBefore, "the arena shot must spend Ada's ammo");
  console.log(`  - real Chromium PvP hit: Bo health ${boHealth} -> ${bo.hud.health}, Ada health stayed ${ada.hud.health}`);

  for (const c of clients) {
    assert.equal(c.pageErrors.length, 0, `${c.label} page errors: ${c.pageErrors.join("\n")}`);
    assert.equal(c.consoleErrors.length, 0, `${c.label} console errors: ${c.consoleErrors.join("\n")}`);
    assert.deepEqual(c.httpErrors, [], `${c.label} saw no server errors`);
  }
} catch (err) {
  for (const c of clients) {
    try {
      console.error(`${c.label} screen at failure:\n${gridToArt(await readGrid(c.page))}`);
    } catch (dumpErr) {
      console.error(`${c.label} screen dump failed: ${dumpErr}`);
    }
  }
  throw err;
} finally {
  await closeAll(clients);
}
