// M32.1 — a signed-in player runs the daily challenge in a real browser.
//
// The journey the DoD asks for, end to end: /challenge opens the landing inside
// the playable client, starting a run joins an isolated instance, finishing it
// produces a server-observed result and a leaderboard row, the row opens the
// M22 replay, and a second attempt runs against that run's ghost.
//
// What makes this a claim rather than a screenshot: the run's socket is watched,
// so "the browser started a challenge" means a `challenge=` socket really was
// opened; the result window's numbers come from the server's own message; and
// the ghost is asserted as a CELL on this browser's canvas that no other client
// could have been sent.

import assert from "node:assert/strict";
import { chromium } from "playwright";
import { gridToArt, hasText, installDecoder, installImageProbe, markProfileWarm, readGrid } from "./lib/canvas.mjs";

const baseURL = process.env.BASE_URL || "http://127.0.0.1:8080";
const authCookie = process.env.ZZT_AUTH_COOKIE || "";

async function waitFor(pred, describe, timeoutMs = 30000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const value = await pred();
    if (value) return value;
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${describe}`);
    await new Promise((resolve) => setTimeout(resolve, 80));
  }
}

async function waitForPage(c, pred, describe, timeoutMs = 30000) {
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
  if (authCookie) {
    const [name, ...rest] = authCookie.split("=");
    await context.addCookies([
      { name, value: rest.join("="), url: baseURL, httpOnly: true, sameSite: "Lax" },
    ]);
  }
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
    challengeResults: [],
    challengeErrors: [],
    snapshots: 0,
    world: null,
    you: null,
    hud: null,
    boardId: null,
    challenge: null,
    challengeRun: null,
  };

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
      if (msg.type === "challengeResult") c.challengeResults.push(msg);
      if (msg.type === "challengeError") c.challengeErrors.push(msg);
      if (body.world) c.world = body.world;
      if (body.hud) c.hud = body.hud;
      if (body.you) c.you = body.you;
      if (body.challenge) c.challenge = body.challenge;
      if (body.challengeRun) c.challengeRun = body.challengeRun;
      if (typeof body.boardId === "number") c.boardId = body.boardId;
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

// runEast holds the arrow key until the server says the run finished. The walk
// is the whole course: three gems in a straight line east of the spawn.
async function runEast(c, describe) {
  await c.page.keyboard.down("ArrowRight");
  try {
    await waitFor(() => c.challengeResults.length > 0, describe, 40000);
  } finally {
    await c.page.keyboard.up("ArrowRight");
  }
  return c.challengeResults[c.challengeResults.length - 1];
}

const clients = [];

try {
  assert.ok(authCookie, "this journey needs a signed-in browser: ZZT_AUTH_COOKIE must be set");

  const ada = await openClient("Ada", "/challenge");
  clients.push(ada);

  // 1. The landing opens inside the ZZT shell, not on a page about the game.
  await waitForPage(ada, (cells) => hasText(cells, "Daily Challenge") && hasText(cells, "Gem Dash"), "the challenge landing");
  await waitForPage(ada, (cells) => hasText(cells, "Start a run"), "the start action");
  assert.equal(ada.sockets.length, 0, "the landing must not join anything");
  console.log("  - /challenge opens the daily challenge landing in the playable client");

  // 2. Starting a run joins a challenge instance — asserted from the socket the
  //    browser actually opened, not from the screen.
  await ada.page.keyboard.press("Enter");
  await waitFor(() => ada.snapshots > 0 && ada.you && ada.challenge === "gem-dash", "the challenge join snapshot");
  assert.ok(
    ada.sockets.some((url) => url.includes("challenge=gem-dash")),
    `the run must be joined through the challenge socket, got ${JSON.stringify(ada.sockets)}`,
  );
  assert.ok(ada.challengeRun, "the run's key must reach the browser so a reconnect can reclaim it");
  assert.equal(ada.world, "GEMDASH", "the frame names the source world, not the run key");
  assert.equal(ada.boardId, 1, "a run starts on the catalogue's board");
  console.log(`  - the run joined ${ada.challengeRun} on ${ada.world} board ${ada.boardId}`);

  // 3. The player plays it, and the SERVER decides it is finished.
  const result = await runEast(ada, "the server to observe the goal met");
  assert.equal(result.challengeId, "gem-dash");
  assert.ok(result.ticks > 0, `the result must carry a tick count, got ${JSON.stringify(result)}`);
  assert.equal(result.gems, 3, "the run completes on the third gem");
  assert.equal(result.durable, true, "a signed-in run posts a time");
  assert.equal(result.rank, 1, "the first time on the board ranks first");
  await waitForPage(ada, (cells) => hasText(cells, "Challenge Result") && hasText(cells, `${result.ticks} ticks`), "the result window");
  console.log(`  - finished in ${result.ticks} ticks, rank ${result.rank}, recording ${result.recordingId}`);

  // 4. The leaderboard shows the row, and the row opens the M22 replay viewer.
  await waitForPage(ada, (cells) => hasText(cells, "Leaderboard"), "the leaderboard action");
  await ada.page.keyboard.press("ArrowDown");
  await ada.page.keyboard.press("Enter");
  await waitForPage(ada, (cells) => hasText(cells, "Challenge Times") && hasText(cells, "Ada"), "the leaderboard window");
  console.log("  - the leaderboard carries the run this browser just made");

  await ada.page.keyboard.press("Enter");
  await waitForPage(ada, (cells) => hasText(cells, "Watch the replay") && hasText(cells, "Race this ghost"), "the row's actions");

  // 5. The row opens the M22 replay viewer on the run that was just recorded.
  const socketsBeforeReplay = ada.sockets.length;
  await ada.page.keyboard.press("Enter");
  await waitFor(() => ada.sockets.length > socketsBeforeReplay, "the replay socket");
  assert.ok(
    ada.sockets.some((url) => url.includes(`replay=${result.recordingId}`)),
    `the row must open its own recording, got ${JSON.stringify(ada.sockets)}`,
  );
  await waitFor(() => ada.page.url().includes(`/replay/${result.recordingId}`), "the replay address");
  console.log("  - the row opens the run in the M22 replay viewer");

  // 6. Back to the challenge, and race that run's ghost. The reload is the
  //    honest way back: a run key lives only in the page, so leaving the client
  //    leaves the challenge.
  await ada.page.goto(`${baseURL}/challenge`, { waitUntil: "domcontentloaded" });
  await ada.page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await installDecoder(ada.page);
  await waitForPage(ada, (cells) => hasText(cells, "Type your name"), "the name prompt on the way back");
  await ada.page.keyboard.type("Ada", { delay: 8 });
  await ada.page.keyboard.press("Enter");
  await waitForPage(ada, (cells) => hasText(cells, "Daily Challenge") && hasText(cells, "Leaderboard"), "the landing again");
  await ada.page.keyboard.press("ArrowDown");
  await ada.page.keyboard.press("Enter");
  await waitForPage(ada, (cells) => hasText(cells, "Challenge Times"), "the leaderboard again");
  await ada.page.keyboard.press("Enter");
  await waitForPage(ada, (cells) => hasText(cells, "Race this ghost"), "the row's actions again");
  await ada.page.keyboard.press("ArrowDown");
  await ada.page.keyboard.press("Enter");
  await waitForPage(ada, (cells) => hasText(cells, "Ghost") && hasText(cells, "Race it now"), "the ghost confirmation");
  console.log("  - a leaderboard row loads its ghost");

  // 7. A second attempt runs against it, started from the ghost window itself.
  //    The ghost is a CELL on this canvas — asserted as the glyph, on the board
  //    columns — and the run's socket carries nothing about it.
  const socketsBefore = ada.sockets.length;
  const firstRun = ada.challengeRun;
  await ada.page.keyboard.press("Enter");
  await waitFor(() => ada.sockets.length > socketsBefore, "the second attempt's socket");
  await waitFor(() => ada.challengeRun && ada.challengeRun !== firstRun, "the second attempt's own run key");
  const secondRun = ada.challengeRun;
  assert.ok(
    ada.sockets.filter((url) => url.includes("challenge=gem-dash")).length >= 2,
    "the second attempt must start a fresh run rather than reusing the first",
  );
  console.log(`  - a second attempt started as ${secondRun}`);

  // The player stands still, so the ghost — which advances with the ticks this
  // attempt has drawn — walks away from them and is unmistakably a second thing
  // on the board. (It is deliberately not drawn while it is standing on you,
  // which is every run's first tile.)
  const GHOST_CH = 0x09;
  await waitForPage(
    ada,
    (cells) => cells.some((cell, i) => cell.ch === GHOST_CH && i % 80 < 60),
    "the ghost to appear on the board",
    20000,
  );
  console.log("  - the ghost is drawn as a local overlay cell during the second attempt");

  assert.deepEqual(ada.challengeErrors, [], "no challenge refusal reached the browser");
  for (const c of clients) {
    assert.equal(c.pageErrors.length, 0, `${c.label} page errors: ${c.pageErrors.join("\n")}`);
    assert.deepEqual(c.httpErrors, [], `${c.label} saw no server errors`);
    assert.equal(c.consoleErrors.length, 0, `${c.label} console errors: ${c.consoleErrors.join("\n")}`);
  }
  console.log("challenge browser journey passed");
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
  for (const c of clients.reverse()) {
    await c.browser.close();
  }
}
