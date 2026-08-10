// M23.2 — WELCOME is first-party content, not a harness-only toy.
//
// Three real Chromium instances select the world from the production title flow
// and complete the tour together: movement and scrolls, chat, darkness/torch,
// shared key progress, shooting, and the route back to the picker.

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { chromium } from "playwright";
import { at, step, walkOnto, walkUntil, assertObserver } from "./lib/walk.mjs";
import { launchOpensPicker, markProfileWarm } from "./lib/canvas.mjs";

const baseURL = process.env.BASE_URL || "http://127.0.0.1:8080";
const resultsDir = path.resolve(process.env.WELCOME_OUT || "test-results/welcome");
fs.mkdirSync(resultsDir, { recursive: true });

const WORLD = "WELCOME";
const ROW = 12;

const clients = [];

async function openClient(label, name) {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1280, height: 720 } });
  await markProfileWarm(context);
  const page = await context.newPage();
  const c = {
    label,
    name,
    browser,
    context,
    page,
    pageErrors: [],
    consoleErrors: [],
    sockets: [],
    titleRequests: [],
    apiWorlds: 0,
    httpErrors: [],
    transcript: [],
    snapshots: 0,
    boardChanges: 0,
    closes: 0,
    you: null,
    hud: null,
    boardId: null,
    roster: [],
    events: [],
    chat: [],
    walkDefaults: { maxSteps: 44, stallLimit: 6 },
  };
  assertObserver(c);
  clients.push(c);

  page.on("pageerror", (err) => c.pageErrors.push(String(err)));
  page.on("console", (msg) => {
    if (msg.type() === "error") c.consoleErrors.push(msg.text());
  });
  page.on("request", (request) => {
    const url = request.url();
    if (url.includes("/api/title")) c.titleRequests.push(url);
  });
  page.on("response", (response) => {
    if (response.url().includes("/api/worlds")) c.apiWorlds++;
    if (response.status() >= 500) c.httpErrors.push({ url: response.url().replace(baseURL, ""), status: response.status() });
  });
  page.on("websocket", (ws) => {
    c.sockets.push(ws.url());
    c.transcript.push({ dir: "open", url: ws.url() });
    ws.on("close", () => {
      c.closes++;
      c.transcript.push({ dir: "close" });
    });
    ws.on("framesent", (f) => c.transcript.push({ dir: "send", payload: String(f.payload).slice(0, 300) }));
    ws.on("framereceived", (frame) => {
      const raw = String(frame.payload);
      let msg;
      try {
        msg = JSON.parse(raw);
      } catch {
        return;
      }
      c.transcript.push({ dir: "recv", type: msg.type, payload: raw.slice(0, 300) });

      if (msg.type === "chat") c.chat.push(msg);
      if (msg.type === "event" && msg.event) c.events.push(msg.event);

      const body = msg.type === "boardChange" ? msg.snapshot : msg;
      if (msg.type === "snapshot") c.snapshots++;
      if (msg.type === "boardChange") c.boardChanges++;
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

  const response = await page.goto(baseURL);
  assert.equal(response?.status(), 200, `${label}: the client index must be served`);
  await page.waitForLoadState("domcontentloaded");
  await page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await context.tracing.start({ screenshots: true, snapshots: true });
  return c;
}

const sleep = (c, ms) => c.page.waitForTimeout(ms);
const settle = (c) => sleep(c, 700);
const eventTypes = (c) => [...new Set(c.events.map((e) => e.type))];
const has = (c, type, match) => c.events.some((e) => e.type === type && (!match || match(e)));
const hasMine = (c, type) => has(c, type, (e) => (e.statId ?? 0) === (c.you?.statId ?? 0));
const ids = (c) => c.roster.map((p) => p.id).sort();
const atLeastX = (c, x) => () => (c.you?.x ?? 0) >= x;
const eastFirst = (group) => [...group].sort((a, b) => (b.you?.x ?? 0) - (a.you?.x ?? 0));

async function waitFor(c, pred, describe, timeoutMs = 12000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    if (pred()) return;
    if (Date.now() > deadline) {
      throw new Error(
        `timed out waiting for ${describe} (${c.label})\n  last seen: ${at(c)} hud=${JSON.stringify(c.hud)}\n` +
          `  roster=${JSON.stringify(ids(c))} events=${JSON.stringify(eventTypes(c))} chat=${JSON.stringify(c.chat)}`,
      );
    }
    await sleep(c, 60);
  }
}

const titleMark = (c) => ({ sockets: c.sockets.length, snapshots: c.snapshots, titles: c.titleRequests.length });

function expectTitleScreenPause(c, world, mark) {
  assert.equal(c.sockets.length, mark.sockets, `${c.label}: selecting ${world} must not open a socket`);
  assert.equal(c.snapshots, mark.snapshots, `${c.label}: selecting ${world} must not receive a room snapshot`);
  assert.ok(
    c.titleRequests.slice(mark.titles).some((url) => url.includes(`world=${world}`)),
    `${c.label}: selecting ${world} must repaint that world's title board`,
  );
}

async function selectWorld(c, world, { name } = {}) {
  const deepLinked = Boolean(name) && !launchOpensPicker(c.page);
  const mark = titleMark(c);
  if (name) {
    await c.page.keyboard.type(name);
    await c.page.keyboard.press("Enter");
    await settle(c);
  } else {
    await c.page.keyboard.press("KeyW");
    await settle(c);
  }
  if (!deepLinked) {
    await c.page.keyboard.type(world);
    await sleep(c, 400);
    await c.page.keyboard.press("Enter");
    await settle(c);
  }
  expectTitleScreenPause(c, world, mark);
}

async function startPlay(c, world) {
  const before = c.snapshots;
  await c.page.keyboard.press("KeyP");
  await waitFor(c, () => c.snapshots > before, `${c.label}'s join snapshot for ${world}`, 20000);
  assert.ok(c.sockets.some((u) => u.includes(`world=${world}`)), `${c.label}: socket did not name ${world}`);
}

async function alignToRow(c, row) {
  for (let attempt = 0; attempt < 12 && (c.you?.y ?? row) !== row; attempt++) {
    const before = `${c.you.x},${c.you.y}`;
    await step(c, c.you.y > row ? "ArrowUp" : "ArrowDown");
    if (`${c.you.x},${c.you.y}` === before) await step(c, "ArrowRight");
  }
  assert.equal(c.you.y, row, `${c.label} must be able to reach row ${row}, stuck at ${at(c)}`);
}

async function say(c, text) {
  await c.page.keyboard.press("KeyC");
  await settle(c);
  await c.page.keyboard.type(text);
  await sleep(c, 250);
  await c.page.keyboard.press("Enter");
  await waitFor(c, () => c.chat.some((m) => m.text === text), `${c.label}'s chat echo`);
}

async function passage(c, x, describe, route = null) {
  const before = c.boardId;
  if (route) {
    for (const [tx, ty, label] of route) await walkOnto(c, tx, ty, label);
  }
  await walkOnto(c, x, ROW, describe, {
    maxSteps: 36,
    until: () => c.boardId !== before,
  });
  await waitFor(c, () => c.boardId !== before, `${c.label}'s board change through ${describe}`);
}

async function quitToTitle(c) {
  const before = c.closes;
  c.events.length = 0;
  await c.page.keyboard.press("KeyQ");
  await waitFor(c, () => hasMine(c, "quitPrompt"), `${c.label}'s quit prompt`);
  await settle(c);
  await c.page.keyboard.press("KeyY");
  await waitFor(c, () => c.closes > before, `${c.label}'s socket to close after quitting`, 12000);
}

async function assertGroupOnBoard(group, boardId, describe) {
  for (const c of group) {
    await waitFor(c, () => c.boardId === boardId, `${c.label} to reach ${describe}`, 20000);
    await waitFor(c, () => c.roster.length === group.length, `${c.label} to see the group on ${describe}`);
  }
}

async function dumpFailure(err) {
  console.error("WELCOME journey FAILED:", err);
  for (const c of clients) {
    console.error(
        `--- ${c.label}: ${at(c)} hud=${JSON.stringify(c.hud)} roster=${JSON.stringify(ids(c))} ` +
        `events=${JSON.stringify(eventTypes(c))} chat=${JSON.stringify(c.chat)} ` +
        `httpErrors=${JSON.stringify(c.httpErrors)} pageErrors=${JSON.stringify(c.pageErrors)} ` +
        `consoleErrors=${JSON.stringify(c.consoleErrors)}`,
    );
    const stem = path.join(resultsDir, `welcome_${c.label.toLowerCase()}`);
    try {
      fs.writeFileSync(`${stem}_transcript.json`, JSON.stringify({ sockets: c.sockets, transcript: c.transcript }, null, 2));
      await c.page.screenshot({ path: `${stem}_failure.png` });
      await c.context.tracing.stop({ path: `${stem}_trace.zip` });
    } catch (dumpErr) {
      console.error(`  (could not dump ${c.label}: ${dumpErr})`);
    }
  }
  console.error(`Saved per-player traces, screenshots and transcripts under ${resultsDir}`);
}

let ada, bo, cy, group;

try {
  console.log(`=== WELCOME JOURNEY: three players against ${baseURL} ===`);
  ada = await openClient("Ada", "Ada");
  bo = await openClient("Bo", "Bo");
  cy = await openClient("Cy", "Cy");
  group = [ada, bo, cy];

  for (const c of group) {
    await selectWorld(c, WORLD, { name: c.name });
    await startPlay(c, WORLD);
  }
  await assertGroupOnBoard(group, 1, "Meet The Room");
  const cast = ids(ada);
  for (const c of group) assert.deepEqual(ids(c), cast, `${c.label} sees a different starting roster`);
  console.log(`  - three players joined WELCOME together: ${JSON.stringify(cast)}`);

  await alignToRow(ada, ROW);
  await walkUntil(ada, "ArrowRight", atLeastX(ada, 9), "the guide");
  await step(ada, "ArrowRight", { hold: 150 });
  await waitFor(ada, () => has(ada, "scroll", (e) => (e.lines || []).some((l) => l.includes("Press C"))), "the guide scroll");
  await ada.page.keyboard.press("Escape");
  await settle(ada);
  await say(ada, "hello");
  for (const c of [bo, cy]) {
    await waitFor(c, () => c.chat.some((m) => m.text === "hello"), `${c.label} to receive Ada's hello`);
  }
  console.log("  - guide scroll opened, and chat reached the room");

  const guideRoute = [
    [9, ROW - 1, "above the guide"],
    [11, ROW - 1, "east of the guide"],
  ];
  for (const c of eastFirst(group)) await passage(c, 36, "the first passage", guideRoute);
  await assertGroupOnBoard(group, 2, "Torch Hall");

  await alignToRow(ada, ROW);
  await walkUntil(ada, "ArrowRight", atLeastX(ada, 8), "the torch");
  await waitFor(ada, () => ada.hud.torches === 1, "Ada to collect a torch");
  await ada.page.keyboard.press("KeyT");
  await waitFor(ada, () => ada.hud.torchTicks > 0, "Ada to light the torch");
  assert.equal(ada.hud.torches, 0, "lighting spends the torch");
  console.log(`  - dark room taught torches: torchTicks=${ada.hud.torchTicks}`);
  for (const c of eastFirst(group)) await passage(c, 30, "the torch-hall passage");
  await assertGroupOnBoard(group, 3, "Shared Door");

  await alignToRow(ada, ROW);
  await walkUntil(ada, "ArrowRight", atLeastX(ada, 10), "the shared key");
  await waitFor(ada, () => ada.hud.keys.some(Boolean), "Ada to collect the key");
  await walkUntil(ada, "ArrowRight", atLeastX(ada, 17), "past the shared door");
  await waitFor(ada, () => !ada.hud.keys.some(Boolean), "the shared door to consume Ada's key");
  await passage(ada, 30, "the shared-door passage");
  for (const c of eastFirst([bo, cy])) {
    await alignToRow(c, ROW);
    await walkUntil(c, "ArrowRight", atLeastX(c, 18), `past the door Ada opened for ${c.label}`);
    assert.ok(!c.hud.keys.some(Boolean), `${c.label} passed the door without holding a key`);
    await passage(c, 30, "the shared-door passage");
  }
  console.log("  - one key opened the door for all three players");
  await assertGroupOnBoard(group, 4, "Shooting Range");

  await alignToRow(ada, ROW);
  await walkUntil(ada, "ArrowRight", atLeastX(ada, 8), "the ammo");
  await waitFor(ada, () => ada.hud.ammo === 5, "Ada to collect ammo");
  const ammoBeforeShot = ada.hud.ammo;
  await step(ada, "Space", { hold: 180 });
  await waitFor(ada, () => ada.hud.ammo < ammoBeforeShot, "Ada's shot to spend ammo");
  await sleep(ada, 1200);
  console.log(`  - shooting taught ammo and Space: ${ammoBeforeShot} -> ${ada.hud.ammo}`);
  for (const c of eastFirst(group)) await passage(c, 34, "the shooting-range passage");
  await assertGroupOnBoard(group, 5, "Ready");

  await alignToRow(ada, ROW);
  await walkUntil(ada, "ArrowRight", atLeastX(ada, 21), "the ready sign");
  await step(ada, "ArrowRight", { hold: 150 });
  await waitFor(ada, () => has(ada, "scroll", (e) => (e.lines || []).some((l) => l.includes("Open W"))), "the ready scroll");
  await ada.page.keyboard.press("Escape");
  await settle(ada);
  console.log("  - final board told the player how to return to the picker");

  for (const c of group) await quitToTitle(c);
  for (const c of group) {
    const before = c.apiWorlds;
    await c.page.keyboard.press("KeyW");
    await waitFor(c, () => c.apiWorlds > before, `${c.label} to reopen the world picker from the title`);
  }
  console.log("  - all three returned to the title and reopened the picker");

  for (const c of group) {
    assert.deepEqual(c.pageErrors, [], `${c.label} must not raise page errors`);
    assert.deepEqual(c.consoleErrors, [], `${c.label} must not log console errors`);
    assert.deepEqual(c.httpErrors, [], `${c.label} must not receive HTTP 5xx responses`);
  }

  console.log("WELCOME JOURNEY PASSED");
  for (const c of clients) {
    await c.context.tracing.stop();
    await c.browser.close();
  }
  process.exit(0);
} catch (err) {
  await dumpFailure(err);
  for (const c of clients) {
    try {
      await c.browser.close();
    } catch {}
  }
  process.exit(1);
}
