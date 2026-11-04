// M36.25 — a scroll never replaces the chat window, or loses its draft.
//
// Driven by engine/m36_25_browser_test.go, which hosts fixtures/scrolls.zwd: one
// board with an object that idles ~25 cycles and then speaks two lines to the
// whole board, forever. A board-wide scroll freezes nobody, so it can arrive
// while chat is open. Before M36.25, enqueueScroll then called openModal
// directly, replacing the chat and its unsent draft. Now the scroll waits until
// the chat closes.
//
// The cap, de-duplication and board-change rules are asserted in
// scroll_queue.test.mjs: a board change cannot happen while any modal blocks
// movement, so a browser cannot be made to reach it on purpose.

import assert from "node:assert/strict";
import { chromium } from "playwright";
import { gridToArt, hasText, installDecoder, installImageProbe, launchOpensPicker, markProfileWarm, readGrid } from "./lib/canvas.mjs";

const baseURL = process.env.BASE_URL || "http://127.0.0.1:8080";
const TALK = "The talker says hello";

const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 1280, height: 720 } });
await markProfileWarm(context);
const page = await context.newPage();
const pageErrors = [];
let scrollsReceived = 0;

page.on("pageerror", (err) => pageErrors.push(String(err)));
page.on("websocket", (ws) => {
  ws.on("framereceived", (frame) => {
    if (String(frame.payload).includes('"type":"scroll"')) scrollsReceived += 1;
  });
});

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
    if (pred()) return;
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${describe}`);
    await page.waitForTimeout(40);
  }
}

try {
  await installImageProbe(page);
  const response = await page.goto(baseURL + "/play/SCROLLS");
  assert.equal(response?.status(), 200, "client index must load");
  await page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await installDecoder(page);
  await page.keyboard.type("Reader");
  await page.keyboard.press("Enter");
  await page.waitForTimeout(600);
  if (launchOpensPicker(page)) {
    await page.keyboard.type("SCROLLS");
    await page.waitForTimeout(600);
    await page.keyboard.press("Enter");
  }
  await waitForCells((cells) => hasText(cells, "P  Play"), "SCROLLS title menu");
  await page.keyboard.press("KeyP");
  await waitForCells((cells) => hasText(cells, "Health:"), "play");

  // The talker's first scroll, read and dismissed; the next is ~2.7s away.
  await waitForCells((cells) => hasText(cells, TALK), "the talker's first scroll", 20000);
  await page.keyboard.press("Escape");
  await waitForCells((cells) => !hasText(cells, TALK), "the first scroll to close");
  const seen = scrollsReceived;

  await page.keyboard.press("KeyC");
  await waitForCells((cells) => hasText(cells, "Global Chat"), "the chat window");
  await page.keyboard.type("my draft", { delay: 8 });
  await waitFor(() => scrollsReceived > seen, "the talker to speak while chat is open", 10000);
  await page.waitForTimeout(500);
  const during = await readGrid(page);
  assert.ok(hasText(during, "Global Chat"), `a scroll must not replace the chat window\n${gridToArt(during)}`);
  assert.ok(hasText(during, "my draft"), `the draft survives the scroll\n${gridToArt(during)}`);
  assert.ok(!hasText(during, TALK), "the scroll is not drawn over the chat");
  console.log("  - a board-wide scroll waits behind an open chat, and the draft survives");

  await page.keyboard.press("Escape");
  await waitForCells((cells) => hasText(cells, TALK) && !hasText(cells, "Global Chat"), "the waiting scroll once chat closes", 3000);
  console.log("  - the waiting scroll opens when the chat closes");

  assert.deepEqual(pageErrors, [], `page errors: ${pageErrors.join("\n")}`);
  console.log("scroll browser journey passed");
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
