// M31.1 — comfort preferences in a real Chromium client.
//
// This runs through the production title/account path as a guest: settings are
// stored only in localStorage, remapped keys still send the old wire protocol,
// and palette/reduced-flash choices stay local presentation state.

import assert from "node:assert/strict";
import { chromium } from "playwright";
import { hasText, installDecoder, installImageProbe, launchOpensPicker, readGrid } from "./lib/canvas.mjs";

const baseURL = process.env.BASE_URL || "http://127.0.0.1:8080";
const COMFORT_KEY = "zztmmo.comfort";
const InputMaskUp = 1 << 0;

const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 1280, height: 720 } });
const page = await context.newPage();
const pageErrors = [];
const consoleErrors = [];
const failedResponses = [];

page.on("pageerror", (err) => pageErrors.push(String(err)));
page.on("console", (msg) => {
  if (msg.type() === "error") consoleErrors.push(msg.text());
});
page.on("response", (response) => {
  if (response.status() >= 500) failedResponses.push(`${response.status()} ${response.url()}`);
});

await page.addInitScript(() => {
  window.__m311Wire = { sent: [] };
  const OriginalWS = window.WebSocket;
  const Wrapped = function (...args) {
    const ws = new OriginalWS(...args);
    const send = ws.send.bind(ws);
    ws.send = (data) => {
      if (typeof data === "string") window.__m311Wire.sent.push(data);
      return send(data);
    };
    return ws;
  };
  Wrapped.prototype = OriginalWS.prototype;
  Object.assign(Wrapped, OriginalWS);
  window.WebSocket = Wrapped;
});

async function waitForCells(pred, describe, timeoutMs = 15000) {
  const deadline = Date.now() + timeoutMs;
  let cells = [];
  for (;;) {
    cells = await readGrid(page);
    if (pred(cells)) return cells;
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${describe}`);
    await page.waitForTimeout(80);
  }
}

async function settle() {
  await page.waitForTimeout(600);
}

async function reachTitle() {
  await installImageProbe(page);
  const response = await page.goto(baseURL + "/play/ACCEPT");
  assert.equal(response?.status(), 200, "client index must load");
  await page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await installDecoder(page);
  await page.keyboard.type("Comfort");
  await page.keyboard.press("Enter");
  await settle();
  if (launchOpensPicker(page)) {
    await page.keyboard.type("ACCEPT");
    await settle();
    await page.keyboard.press("Enter");
  }
  await waitForCells((cells) => hasText(cells, "P  Play"), "ACCEPT title menu");
}

async function openComfortMenu() {
  await page.keyboard.press("KeyG");
  await settle();
  await waitForCells((cells) => hasText(cells, "Comfort settings"), "guest account menu");
  await page.keyboard.press("Enter");
  await settle();
  await waitForCells((cells) => hasText(cells, "Key preset:"), "comfort menu");
}

async function selectRow(offset) {
  for (let i = 0; i < offset; i += 1) await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Enter");
  await settle();
}

function storedComfort() {
  return page.evaluate((key) => JSON.parse(window.localStorage.getItem(key) || "{}"), COMFORT_KEY);
}

function sentInputs() {
  return page.evaluate(() => (window.__m311Wire?.sent || []).map((raw) => {
    try { return JSON.parse(raw); } catch { return null; }
  }).filter(Boolean));
}

try {
  await reachTitle();

  await openComfortMenu();
  await selectRow(1); // Bind Up key
  await page.keyboard.press("KeyI");
  await settle();
  await page.keyboard.press("Escape");

  await openComfortMenu();
  await selectRow(2); // Bind Torch key
  await page.keyboard.press("KeyO");
  await settle();
  await page.keyboard.press("Escape");

  let prefs = await storedComfort();
  assert.deepEqual(prefs.keyBindings.up, ["KeyI"], "Up binding stored");
  assert.deepEqual(prefs.keyBindings.torch, ["KeyO"], "Torch binding stored");

  await page.keyboard.press("KeyP");
  await settle();
  await page.keyboard.down("KeyI");
  await page.waitForTimeout(180);
  await page.keyboard.up("KeyI");
  await page.keyboard.press("KeyO");
  await page.waitForTimeout(250);
  const inputs = await sentInputs();
  assert.ok(inputs.some((msg) => msg.type === "input" && (msg.keymask & InputMaskUp) !== 0), "custom Up sends the existing up keymask");
  assert.ok(inputs.some((msg) => msg.type === "input" && msg.key === "T".charCodeAt(0)), "custom Torch sends the existing T command byte");

  await page.keyboard.press("KeyQ");
  await settle();
  await page.keyboard.press("KeyY");
  await settle();

  await openComfortMenu();
  await selectRow(3); // Reset bindings
  prefs = await storedComfort();
  assert.equal(prefs.keyPreset, "vanilla");
  assert.deepEqual(prefs.keyBindings, {});

  await selectRow(4); // Reduce flashing
  prefs = await storedComfort();
  assert.equal(prefs.reduceFlashing, true, "reduce flashing is guest-local");
  await selectRow(5); // Palette
  prefs = await storedComfort();
  assert.equal(prefs.palette, "high-contrast", "alternate palette is guest-local");
  await page.keyboard.press("Escape");

  assert.deepEqual(pageErrors, [], "no page errors");
  assert.deepEqual(failedResponses, [], "no failed HTTP responses");
  assert.deepEqual(consoleErrors, [], "no console errors");
  console.log("comfort_journey.test.mjs: ok");
} finally {
  await context.close();
  await browser.close();
}
