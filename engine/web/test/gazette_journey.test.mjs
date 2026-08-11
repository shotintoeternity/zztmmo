// M34.3 — the ZZT Gazette's board, read by a real browser.
//
// The claim: a player standing in the shipped lobby walks up to the newsstand,
// reads the day's paper in an ordinary ZZT text window, dismisses it, and walks
// away again. Two halves of that are only observable from here — that the
// window the server writes is what appears (and NOT the stand's own fallback
// copy, which the server suppresses), and that the reader is frozen while it is
// open and moving again once it is closed.

import assert from "node:assert/strict";
import { chromium } from "playwright";
import { at, step, walkOnto, assertObserver } from "./lib/walk.mjs";
import { gridToArt, hasText, installDecoder, installImageProbe, markProfileWarm, readGrid } from "./lib/canvas.mjs";

const baseURL = process.env.BASE_URL || "http://127.0.0.1:8080";

// The stand and the empty square above it — fixtures/lobby.zwd and lobby.go's
// defaultLobbyNotices are the authority, and a Go test keeps them agreeing.
const STAND = { x: 13, y: 20 };

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
await markProfileWarm(context);
const page = await context.newPage();

const c = {
  label: "gazette",
  page,
  pageErrors: [],
  consoleErrors: [],
  httpErrors: [],
  snapshots: 0,
  world: null,
  you: null,
  boardId: null,
  walkDefaults: { maxSteps: 30, stallLimit: 4 },
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
  ws.on("framereceived", (frame) => {
    let msg;
    try {
      msg = JSON.parse(String(frame.payload));
    } catch {
      return;
    }
    const body = msg.type === "boardChange" ? msg.snapshot : msg;
    if (msg.type === "snapshot") c.snapshots++;
    if (body.world) c.world = body.world;
    if (body.you) c.you = body.you;
    if (Array.isArray(body.players)) {
      const me = body.players.find((p) => p.id === c.you?.id);
      if (me) c.you = me;
    }
    if (typeof body.boardId === "number") c.boardId = body.boardId;
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
    await page.keyboard.type("Reader");
    await page.keyboard.press("Enter");
  }
  await waitFor(page, async () => (await screenHas("LOBBY")) && (await screenHas("PRESS P TO JOIN")), "LOBBY title screen");

  await page.keyboard.press("KeyP");
  await waitFor(page, () => c.snapshots > 0 && c.world === "LOBBY" && c.boardId === 1 && c.you, "LOBBY join snapshot");

  // The signage is on the board, so the paper is findable without being told.
  assert.ok(await screenHas("THE ZZT GAZETTE"), "the lobby must sign its newsstand");

  // Two legs: west along the spawn row, then south down the empty column that
  // runs past the sign. Walking the other order would try to cross the sign.
  await walkOnto(c, STAND.x, c.you.y, "the newsstand column");
  await walkOnto(c, STAND.x, STAND.y - 1, "the square above the newsstand");

  // An object answers on its own cycle, so the touch is pressed until the
  // window is observed rather than pressed once and hoped for.
  for (let i = 0; ; i++) {
    if (await screenHas("The ZZT Gazette,")) break;
    assert.ok(i < 12, `the newsstand never opened for a reader standing at ${at(c)}`);
    await step(c, "ArrowDown");
  }

  const paper = await readGrid(page);
  assert.ok(
    !hasText(paper, "Nobody is printing a paper"),
    "the stand's own fallback copy was posted instead of the server's edition",
  );
  assert.ok(
    hasText(paper, "No news today") || hasText(paper, "died in") || hasText(paper, "dreamed up"),
    "the window carries no story from the day's ledger",
  );

  // Frozen while reading: an arrow press with a scroll open moves nobody.
  const frozenAt = { x: c.you.x, y: c.you.y };
  await page.keyboard.press("ArrowUp");
  await page.waitForTimeout(500);
  assert.deepEqual({ x: c.you.x, y: c.you.y }, frozenAt, "a reader with a scroll open must not move");

  // Dismissed with the ordinary reply, which is what unfreezes them.
  await page.keyboard.press("Escape");
  await waitFor(page, async () => !(await screenHas("The ZZT Gazette,")), "the paper to close");
  await step(c, "ArrowUp");
  assert.notDeepEqual({ x: c.you.x, y: c.you.y }, frozenAt, `the reader is still stuck at ${at(c)} after closing the paper`);

  assert.equal(c.pageErrors.length, 0, `page errors: ${c.pageErrors.join("\n")}`);
  assert.equal(c.consoleErrors.length, 0, `console errors: ${c.consoleErrors.join("\n")}`);
  assert.deepEqual(c.httpErrors, [], "no server errors should leak through the journey");

  console.log(`M34.3 gazette journey: /play/LOBBY -> P -> newsstand (${STAND.x},${STAND.y}) -> read -> dismissed, walked away from ${at(c)}`);
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
