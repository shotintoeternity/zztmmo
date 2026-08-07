// M22.3 — /replay/<id> is a shareable read-only playback link, in a real browser.

import assert from "node:assert/strict";
import { chromium } from "playwright";
import { baseURL, gridToArt, hasText, installDecoder, installImageProbe, readGrid, saveText } from "./lib/canvas.mjs";

const REPLAY_ID = process.env.REPLAY_ID || "TOWN-20260807-120000";
const FINAL_TICK = Number(process.env.REPLAY_FINAL_TICK || "47");

async function waitForPage(c, pred, describe, timeoutMs = 30000) {
  const deadline = Date.now() + timeoutMs;
  let cells = null;
  for (;;) {
    cells = await readGrid(c.page);
    if (pred(cells)) return cells;
    if (Date.now() > deadline) {
      saveText(`m223-timeout-${c.label}-${describe.replace(/\W+/g, "-")}.txt`, gridToArt(cells));
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

async function openReplayClient(label) {
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
  const response = await page.goto(`${baseURL}/replay/${REPLAY_ID}`, { waitUntil: "domcontentloaded" });
  assert.equal(response?.status(), 200, `${label}: /replay/${REPLAY_ID} must serve the client`);
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
  const a = await openReplayClient("ViewerA");
  clients.push(a);
  const b = await openReplayClient("ViewerB");
  clients.push(b);

  for (const c of clients) {
    await waitForPage(c, (cells) => hasText(cells, "Watching") && hasText(cells, "2 watching") && !hasText(cells, "Health:100"), "the replay watch screen");
    await waitFor(() => c.sent.some((m) => m && m.type === "join" && m.spectate === true), "the replay watcher to send a spectate join");
    assert.equal(new URL(c.page.url()).pathname, `/replay/${REPLAY_ID}`, "the replay link must stay in the address bar");
    const join = c.sent.find((m) => m && m.type === "join");
    assert.deepEqual(join, { type: "join", spectate: true }, `replay join must be read-only: ${JSON.stringify(join)}`);
  }
  console.log(`  - /replay/${REPLAY_ID}: two read-only browsers joined the playback`);

  await waitFor(
    () => clients.every((c) => c.received.some((m) => m && m.type === "diff" && m.tick === FINAL_TICK)),
    `both replay watchers to receive final tick ${FINAL_TICK}`,
    30000,
  );
  console.log(`  - /replay/${REPLAY_ID}: playback reached final tick ${FINAL_TICK}`);

  await a.page.keyboard.press("KeyP");
  await waitFor(() => a.sent.some((m) => m && m.type === "replayControl" && m.op === "pause"), "pause control to reach the socket");
  await a.page.keyboard.press("KeyR");
  await waitFor(() => a.sent.some((m) => m && m.type === "replayControl" && m.op === "restart"), "restart control to reach the socket");
  console.log("  - replay controls are sent only from the replay viewer");

  for (const c of clients) {
    assert.deepEqual(c.pageErrors, [], `${c.label} page errors`);
    assert.deepEqual(c.consoleErrors, [], `${c.label} console errors`);
  }
  console.log("ALL REPLAY-VIEWER ACTS PASSED");
  await closeAll(clients);
  process.exit(0);
} catch (err) {
  console.error("replay-viewer journey FAILED:", err);
  for (const c of clients) {
    console.error(`${c.label} sent:`, JSON.stringify(c.sent));
    console.error(`${c.label} received types/ticks:`, JSON.stringify(c.received.map((m) => `${m.type}:${m.tick ?? ""}`)));
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
