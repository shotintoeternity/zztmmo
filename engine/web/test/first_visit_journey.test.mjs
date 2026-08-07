// M23.3 — a first visit lands on the welcome world, unless the URL already
// names a world. One Chromium profile proves the fresh and warm root flows; a
// second fresh profile proves a shared /play link wins over the welcome path.

import assert from "node:assert/strict";
import { chromium } from "playwright";
import { gridToArt, installDecoder, installImageProbe, readGrid, saveText, textAt } from "./lib/canvas.mjs";

const baseURL = process.env.BASE_URL || "http://127.0.0.1:8080";

const WELCOME = "WELCOME";
const TARGET = "ACCEPT";
const FIRST_VISIT_KEY = "zzt-first-visit-welcome";

const titledWorld = (cells) => textAt(cells, 69, 8, 11).trim();

function hasText(cells, needle) {
  for (let row = 0; row < 25; row += 1) {
    if (textAt(cells, 0, row).includes(needle)) return true;
  }
  return false;
}

async function displayName(world) {
  const response = await fetch(`${baseURL}/api/title?world=${encodeURIComponent(world)}`);
  assert.ok(response.ok, `/api/title?world=${world} answered ${response.status}`);
  const title = await response.json();
  return title.world.slice(0, 11).trim();
}

async function waitForBoard(page, pred, describe, timeoutMs = 30000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const cells = await readGrid(page);
    if (pred(cells)) return cells;
    if (Date.now() > deadline) {
      saveText(`m233-timeout-${describe.replace(/\W+/g, "-")}.txt`, gridToArt(cells));
      throw new Error(`timed out waiting for ${describe}; the screen was:\n${gridToArt(cells)}`);
    }
    await page.waitForTimeout(100);
  }
}

async function waitFor(pred, describe, timeoutMs = 20000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    if (await pred()) return;
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${describe}`);
    await new Promise((resolve) => setTimeout(resolve, 80));
  }
}

async function loadWithName(page, path, name) {
  const response = await page.goto(baseURL + path, { waitUntil: "domcontentloaded" });
  assert.equal(response?.status(), 200, `${path} must serve the client`);
  await page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await installDecoder(page);
  await waitForBoard(page, (c) => hasText(c, "Type your name"), `the launch name prompt at ${path}`);
  await page.keyboard.type(name, { delay: 8 });
  await page.keyboard.press("Enter");
}

async function openPage(context) {
  const page = await context.newPage();
  const pageErrors = [];
  const consoleErrors = [];
  const sockets = [];
  let snapshots = 0;
  page.on("pageerror", (err) => pageErrors.push(String(err)));
  page.on("console", (msg) => {
    if (msg.type() === "error") consoleErrors.push(msg.text());
  });
  page.on("websocket", (ws) => {
    sockets.push(ws.url());
    ws.on("framereceived", (frame) => {
      try {
        if (JSON.parse(frame.payload).type === "snapshot") snapshots += 1;
      } catch {
        /* not a JSON frame */
      }
    });
  });
  await installImageProbe(page);
  return { page, pageErrors, consoleErrors, sockets, snapshots: () => snapshots };
}

const TITLE_OF = {
  [WELCOME]: await displayName(WELCOME),
  [TARGET]: await displayName(TARGET),
};
assert.notEqual(TITLE_OF[WELCOME], TITLE_OF[TARGET], "the welcome and target title rows must discriminate this suite");

const browser = await chromium.launch({ headless: true });

try {
  const context = await browser.newContext({ viewport: { width: 1280, height: 720 } });
  const fresh = await openPage(context);

  await loadWithName(fresh.page, "/", "Newcomer");
  const welcomeTitle = await waitForBoard(
    fresh.page,
    (c) => titledWorld(c) === TITLE_OF[WELCOME] && textAt(c, 62, 11, 3) === " P ",
    "the first visit to land on WELCOME's title screen",
  );
  assert.ok(!hasText(welcomeTitle, "Choose a World"), "the first visit must not open the picker first");
  assert.equal(fresh.sockets.length, 0, "landing on WELCOME's title screen must not join yet");
  assert.equal(
    await fresh.page.evaluate((key) => window.localStorage.getItem(key), FIRST_VISIT_KEY),
    "1",
    "the welcome flow must mark this profile warm",
  );
  await waitFor(() => new URL(fresh.page.url()).pathname === "/play/WELCOME", "the welcome title to be shareable");
  console.log("  - fresh / routed to WELCOME's title screen without joining");

  await fresh.page.keyboard.press("KeyP");
  await waitFor(() => fresh.snapshots() > 0, "P from WELCOME's title to join");
  assert.ok(fresh.sockets.some((url) => url.includes("world=WELCOME")), `P must join WELCOME; sockets=${JSON.stringify(fresh.sockets)}`);
  console.log("  - P from the welcome title joined WELCOME");

  await loadWithName(fresh.page, "/", "Returner");
  await waitForBoard(fresh.page, (c) => hasText(c, "Choose a World"), "the warm profile to open today's picker");
  assert.equal(new URL(fresh.page.url()).pathname, "/", "the warm root visit must stay the ordinary root flow");
  console.log("  - the second root visit opened the ordinary picker");

  const deepContext = await browser.newContext({ viewport: { width: 1280, height: 720 } });
  const deep = await openPage(deepContext);
  await loadWithName(deep.page, `/play/${TARGET}`, "Linker");
  await waitForBoard(
    deep.page,
    (c) => titledWorld(c) === TITLE_OF[TARGET] && !hasText(c, "Welcome to ZZTMMO"),
    `${TARGET}'s title screen from a fresh deep link`,
  );
  assert.equal(await deep.page.evaluate((key) => window.localStorage.getItem(key), FIRST_VISIT_KEY), null);
  assert.equal(deep.sockets.length, 0, "a deep link must not join and must not be intercepted by welcome");
  await waitFor(() => new URL(deep.page.url()).pathname === `/play/${TARGET}`, "the deep link to remain in the address bar");
  console.log(`  - /play/${TARGET} bypassed the welcome redirect in a fresh profile`);

  assert.deepEqual(fresh.pageErrors, [], "fresh/warm page must not raise page errors");
  assert.deepEqual(fresh.consoleErrors, [], "fresh/warm page must not log console errors");
  assert.deepEqual(deep.pageErrors, [], "deep-link page must not raise page errors");
  assert.deepEqual(deep.consoleErrors, [], "deep-link page must not log console errors");

  await context.close();
  await deepContext.close();
  await browser.close();
  console.log("FIRST-VISIT JOURNEY PASSED");
  process.exit(0);
} catch (err) {
  await browser.close();
  throw err;
}
