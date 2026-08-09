// M29.1 — LOBBY is a real first-party world, and its transit gates are
// interpreted by the server as cross-world joins.

import assert from "node:assert/strict";
import { chromium } from "playwright";
import { at, walkOnto, assertObserver } from "./lib/walk.mjs";
import { gridToArt, hasText, installDecoder, installImageProbe, readGrid } from "./lib/canvas.mjs";

const baseURL = process.env.BASE_URL || "http://127.0.0.1:8080";

async function waitFor(page, pred, describe, timeoutMs = 20000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    if (await pred()) return;
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${describe}`);
    await page.waitForTimeout(80);
  }
}

async function screenHas(needle) {
  return hasText(await readGrid(page), needle);
}

const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 1280, height: 720 } });
const page = await context.newPage();

const c = {
  label: "lobby",
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
  walkDefaults: { maxSteps: 20, stallLimit: 4 },
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
    if (msg.type === "event" && msg.event) c.events.push(msg.event);
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

try {
  await installImageProbe(page);
  const response = await page.goto(`${baseURL}/play/LOBBY`);
  assert.equal(response?.status(), 200, "the lobby deep link must serve the client");
  await page.waitForLoadState("domcontentloaded");
  await page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await installDecoder(page);
  if (await waitFor(page, () => screenHas("Type your name"), "possible launch name prompt", 3000).then(() => true, () => false)) {
    await page.keyboard.type("Lobbygoer");
    await page.keyboard.press("Enter");
  }
  await waitFor(page, async () => (await screenHas("LOBBY")) && (await screenHas("PRESS P TO JOIN")), "LOBBY title screen");

  await page.keyboard.press("KeyP");
  await waitFor(page, () => c.snapshots > 0 && c.world === "LOBBY" && c.boardId === 1 && c.you, "LOBBY join snapshot");
  assert.ok(c.sockets.some((url) => url.includes("world=LOBBY")), "the first socket must join LOBBY");
  const playerID = c.you.id;

  await walkOnto(c, 30, 9, "TOWN transit gate", { maxSteps: 12, until: () => c.world === "TOWN" });
  await waitFor(page, () => c.world === "TOWN" && c.you?.id === playerID, `TOWN snapshot after gate from ${at(c)}`);

  assert.match(page.url(), /\/play\/TOWN$/, "the address bar follows the server-side transit target");
  assert.equal(c.pageErrors.length, 0, `page errors: ${c.pageErrors.join("\n")}`);
  assert.equal(c.consoleErrors.length, 0, `console errors: ${c.consoleErrors.join("\n")}`);
  assert.deepEqual(c.httpErrors, [], "no server errors should leak through the journey");

  console.log(`M29.1 lobby journey: /play/LOBBY -> P -> gate (30,9) -> ${c.world} with player ${playerID}`);
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
