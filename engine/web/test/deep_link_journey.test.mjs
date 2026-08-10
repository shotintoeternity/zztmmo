// M20.1 — the /play/<world> deep link, in a real browser.
//
// One headless Chromium drives the Vite-built client against the PRODUCTION
// zzt-server binary (engine/m20_1_browser_test.go). The claim is about a URL a
// player sends someone else, so it has to be a real navigation to a real path:
// nothing here is injected, and no test-only hook exists in the client.
//
// The server's STARTUP world is TOWN and the deep link's target is ACCEPT, on
// purpose. A boot with no deep link paints the startup world's board 0, so a
// suite that deep-linked to the startup world would pass without the feature
// existing at all. The same trap in the other direction is why the lowercase
// case asserts the /api/title parameter: the client asking for world=TOWN after
// /play/town is what proves the name was resolved through /api/worlds (M18.13's
// identity) rather than passed along as typed.
//
// WHAT THIS SUITE IS EVIDENCE FOR:
//   mode.deep-link      — /play/<world> lands on that world's title screen,
//                         refuses a name nothing answers to, and keeps the
//                         address bar shareable
//   mode.title          — the title-screen pause a deep link must not bypass
//
// WHAT A BROWSER CANNOT PROVE HERE, AND WHO PROVES IT. That /api/worlds lists
// one entry per joinable world is M18.13's own (Go) claim; that the server
// carries a /play/… return path through the OAuth state cookie is
// TestM201SignInReturnsToTheDeepLinkedTitleScreen. The Google leg of act 4 is
// stubbed at the browser: what is asserted here is which return path the CLIENT
// asks for, and that arriving back on it lands on the same title screen.
//
// TWO THINGS THAT SILENTLY PRODUCE A "PASSING" TEST:
//   1. Columns 60..79 are the sidebar. Every window search below is confined to
//      the 60-column board area, so a sidebar match cannot stand in for a window.
//   2. `go test` caches this test and the .mjs is not a tracked dependency —
//      iterate with -count=1 or you will read a stale pass.

import assert from "node:assert/strict";
import { chromium } from "playwright";
import { baseURL, gridToArt, hasText, installDecoder, installImageProbe, markProfileWarm, pickListRow, readGrid, saveText, textAt } from "./lib/canvas.mjs";

const TARGET = "ACCEPT"; // deep-linked to; NOT the server's startup world
const STARTUP = "TOWN"; // the server's -world, so a bare load lands here
const MISSING = "nosuch";

const BOARD_COLS = 60;
const boardText = (cells, row) => textAt(cells, 0, row, BOARD_COLS);

function findOnBoard(cells, needle) {
  for (let row = 0; row < 25; row += 1) {
    const col = boardText(cells, row).indexOf(needle);
    if (col >= 0) return { x: col, y: row };
  }
  return null;
}
const onBoard = (cells, needle) => findOnBoard(cells, needle) !== null;

/**
 * The title sidebar's world row (title.ts: write(69, 8, ...)).
 *
 * It carries the world's DISPLAY name, which is not its filename: ACCEPT.ZZT
 * calls itself "ACCEPTANCE". So the two names each world answers to are read off
 * /api/title below rather than assumed — the deep link is keyed on the filename
 * and the screen shows the title, and conflating them is how this suite would
 * come to assert nothing.
 */
const titledWorld = (cells) => textAt(cells, 69, 8, 11).trim();

async function displayName(world) {
  const response = await fetch(`${baseURL}/api/title?world=${encodeURIComponent(world)}`);
  assert.ok(response.ok, `/api/title?world=${world} answered ${response.status}`);
  const title = await response.json();
  assert.ok(title.world, `/api/title?world=${world} named no world: ${JSON.stringify(title)}`);
  return title.world.slice(0, 11).trim();
}

/**
 * Poll a plain predicate — the address bar, a request the client made — with the
 * same patience as waitForBoard. Every one of those is written by code that runs
 * AFTER the frame it belongs to (rememberWorldInPath runs after showTitle has
 * painted), so a bare assert right behind a screen wait is a race that passes
 * until it does not.
 */
async function waitFor(pred, describe, timeoutMs = 20000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    if (await pred()) return;
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${describe}`);
    await page.waitForTimeout(80);
  }
}

async function waitForBoard(page, pred, describe, timeoutMs = 30000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const cells = await readGrid(page);
    if (pred(cells)) return cells;
    if (Date.now() > deadline) {
      saveText(`m201-timeout-${describe.replace(/\W+/g, "-")}.txt`, gridToArt(cells));
      throw new Error(`timed out waiting for ${describe}; the screen was:\n${gridToArt(cells)}`);
    }
    await page.waitForTimeout(100);
  }
}

const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 1280, height: 720 } });
await markProfileWarm(context);
const page = await context.newPage();

const pageErrors = [];
const consoleErrors = [];
page.on("pageerror", (err) => pageErrors.push(String(err)));
page.on("console", (msg) => {
  if (msg.type() === "error") consoleErrors.push(msg.text());
});

// The wire is where "did not join" is observable: the title screen has no socket
// by design, so a snapshot or an open socket is the whole of the failure.
const sockets = [];
let snapshots = 0;
page.on("websocket", (ws) => {
  sockets.push(ws.url());
  ws.on("framereceived", (frame) => {
    try {
      if (JSON.parse(frame.payload).type === "snapshot") snapshots += 1;
    } catch {
      /* binary or partial frames are not snapshots */
    }
  });
});
// Every /api/title request, in order: the only wire evidence of WHOSE board 0 is
// on screen, and of the name the client resolved before asking for it.
const titleRequests = [];
page.on("request", (request) => {
  const url = request.url();
  if (url.includes("/api/title")) titleRequests.push(url);
});

// The Google leg, stubbed at the browser: the start endpoint answers the
// redirect Google would have answered with, straight back to the return path the
// client asked for. The server is never asked to reach accounts.google.com, and
// the client is never told anything it would not have been told for real.
const authStarts = [];
await page.route("**/api/auth/google/start**", async (route) => {
  const url = new URL(route.request().url());
  authStarts.push(url.href);
  await route.fulfill({ status: 302, headers: { location: url.searchParams.get("return") || "/" }, body: "" });
});

await installImageProbe(page);

// The names the screen will show. Read from the server, and required to differ:
// if the deep link's target displayed the same title as the startup world, every
// "landed on the right world" assertion below would hold with the feature gone.
const TITLE_OF = { [TARGET]: await displayName(TARGET), [STARTUP]: await displayName(STARTUP) };
assert.notEqual(
  TITLE_OF[TARGET],
  TITLE_OF[STARTUP],
  `${TARGET} and ${STARTUP} must show different titles for this suite to discriminate`,
);

/** A fresh page load at `path`, through the launch name prompt. */
async function load(path, name = "Linker") {
  const response = await page.goto(baseURL + path, { waitUntil: "domcontentloaded" });
  assert.equal(response?.status(), 200, `${path} must serve the client, not a 404`);
  await page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await installDecoder(page);
  await waitForBoard(page, (c) => onBoard(c, "Type your name"), `the launch name prompt at ${path}`);
  await page.keyboard.type(name, { delay: 8 });
  await page.keyboard.press("Enter");
}

try {
  // =========================================================================
  // ACT 1 — /play/ACCEPT lands on ACCEPT's title screen, and joins nothing
  // =========================================================================
  const beforeLink = { sockets: sockets.length, snapshots, titles: titleRequests.length };
  await load(`/play/${TARGET}`);

  const landed = await waitForBoard(
    page,
    (c) => titledWorld(c) === TITLE_OF[TARGET] && textAt(c, 62, 11, 3) === " P ",
    `${TARGET}'s title screen from the deep link`,
  );
  assert.notEqual(
    titledWorld(landed),
    TITLE_OF[STARTUP],
    "the deep link must leave the startup world behind, or this suite proves nothing",
  );
  // "instead of opening the picker": the picker is the launch flow's default and
  // it is not on screen.
  assert.ok(
    !onBoard(landed, "Choose a World"),
    `a deep link must not open the picker:\n${gridToArt(landed)}`,
  );
  // The title-screen pause, in the three claims M16.11 asserts at every picker
  // selection. A deep link must not become the path that bypasses it.
  assert.equal(
    sockets.length,
    beforeLink.sockets,
    `a deep link must open no socket; opened ${JSON.stringify(sockets.slice(beforeLink.sockets))}`,
  );
  assert.equal(snapshots, beforeLink.snapshots, "a deep link must not start play, but a snapshot arrived");
  const linkTitles = titleRequests.slice(beforeLink.titles);
  assert.ok(
    linkTitles.some((url) => url.includes(`world=${TARGET}`)),
    `the board must come from ${TARGET}'s own /api/title; requests: ${JSON.stringify(linkTitles)}`,
  );
  await waitFor(
    () => new URL(page.url()).pathname === `/play/${TARGET}`,
    "the deep link to stay in the address bar",
  );
  console.log(`  - /play/${TARGET}: its title screen, no socket, no snapshot, no picker`);

  // P still starts play, exactly as from the picker — and joins that world only.
  await page.keyboard.press("KeyP");
  await waitForBoard(page, () => snapshots > beforeLink.snapshots, `the ${TARGET} join snapshot`);
  const joined = sockets.slice(beforeLink.sockets);
  assert.equal(joined.length, 1, `P must open exactly one socket; opened ${JSON.stringify(joined)}`);
  assert.ok(
    joined[0].includes(`world=${TARGET}`),
    `P must join ${TARGET}'s instance and no other; opened ${joined[0]}`,
  );
  console.log(`  - P from the deep-linked title joined ${TARGET} and nothing else`);

  // =========================================================================
  // ACT 2 — a dead link refuses, in a window, and leaves a usable picker
  // =========================================================================
  const beforeMissing = { sockets: sockets.length, snapshots };
  await load(`/play/${MISSING}`);

  const refused = await waitForBoard(
    page,
    (c) => onBoard(c, "Deep link"),
    `the refusal window for /play/${MISSING}`,
  );
  assert.ok(
    onBoard(refused, MISSING),
    `the refusal must name the world that failed:\n${gridToArt(refused)}`,
  );
  assert.equal(
    sockets.length,
    beforeMissing.sockets,
    `a dead link must be refused before any socket; opened ${JSON.stringify(sockets.slice(beforeMissing.sockets))}`,
  );
  assert.equal(snapshots, beforeMissing.snapshots, "a dead link must not join anything");
  assert.notEqual(titledWorld(refused), "Untitled", "a refusal must not leave the client on Untitled");

  // Closing the refusal drops the visitor into the normal picker, which then
  // works: this is what makes a dead link a detour rather than a dead end.
  await page.keyboard.press("Escape");
  await waitForBoard(page, (c) => onBoard(c, "Choose a World"), "the picker after the refusal");
  await page.keyboard.type(TARGET, { delay: 8 });
  await waitForBoard(page, (c) => onBoard(c, TARGET), `the picker to match ${TARGET}`);
  await page.keyboard.press("Enter");
  await waitForBoard(page, (c) => titledWorld(c) === TITLE_OF[TARGET], `${TARGET}'s title screen from the picker`);
  console.log(`  - /play/${MISSING}: refused in a window, and the picker still works`);

  // The address bar is the share link: what the picker selected is now the URL.
  await waitFor(
    () => new URL(page.url()).pathname === `/play/${TARGET}`,
    "the picker's selection to leave its deep link in the address bar",
  );
  // And it is a working link, not just a decoration.
  await page.reload({ waitUntil: "domcontentloaded" });
  await page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await installDecoder(page);
  await waitForBoard(page, (c) => onBoard(c, "Type your name"), "the launch prompt after reloading the deep link");
  await page.keyboard.type("Linker", { delay: 8 });
  await page.keyboard.press("Enter");
  await waitForBoard(page, (c) => titledWorld(c) === TITLE_OF[TARGET], `${TARGET}'s title screen after a reload`);
  console.log(`  - the picker's URL reloads to the same title screen`);

  // =========================================================================
  // ACT 3 — /play/town and /play/TOWN are one world, the one the join opens
  // =========================================================================
  const listing = await (await fetch(`${baseURL}/api/worlds`)).json();
  const townEntries = (listing.worlds || []).filter((w) => (w.world || w).toUpperCase() === STARTUP);
  assert.equal(
    townEntries.length,
    1,
    `${STARTUP} must be one entry to resolve to (M18.13): ${JSON.stringify(townEntries)}`,
  );

  for (const spelling of [STARTUP.toLowerCase(), STARTUP]) {
    const before = titleRequests.length;
    await load(`/play/${spelling}`);
    // The screen cannot tell us this link resolved: a boot with no deep link
    // already shows the startup world. Only the client's own board request can,
    // and WHICH name it carries is the whole claim — world=TOWN means the link
    // was resolved through /api/worlds, world=town means it was passed as typed.
    await waitFor(
      () => titleRequests.slice(before).some((url) => url.includes("/api/title?world=")),
      `the board /play/${spelling} asks for`,
    );
    const asked = titleRequests.slice(before).filter((url) => url.includes("/api/title?world="));
    assert.ok(
      asked.every((url) => url.endsWith(`world=${STARTUP}`)),
      `/play/${spelling} must resolve to the listed identity before asking for a board; asked ${JSON.stringify(asked)}`,
    );
    await waitForBoard(
      page,
      (c) => titledWorld(c) === TITLE_OF[STARTUP],
      `${STARTUP}'s title screen via /play/${spelling}`,
    );
    await waitFor(
      () => new URL(page.url()).pathname === `/play/${STARTUP}`,
      `/play/${spelling} to leave the canonical link in the address bar`,
    );
  }
  console.log(`  - /play/${STARTUP.toLowerCase()} and /play/${STARTUP} both resolved to the single ${STARTUP} entry`);

  // =========================================================================
  // ACT 4 — signing in from a deep-linked title screen comes back to it
  // =========================================================================
  const signInFrom = new URL(page.url()).pathname;
  assert.equal(signInFrom, `/play/${STARTUP}`, "act 4 must start from a deep-linked title screen");
  // A marker that cannot survive a navigation: its disappearance is how we know
  // the whole redirect chain completed and the client reloaded (M16.16's shape).
  await page.evaluate(() => {
    window.__m201BeforeLogin = true;
  });
  // M24.1 made G open the Account menu instead of redirecting straight to the
  // provider (M33.1 found this suite still expecting the old shape), and M31.1
  // then put "Comfort settings" above the sign-in row. The menu is opened and
  // the Sign in row is WALKED to rather than counted, so the next row anyone
  // adds fails here instead of quietly choosing something else; the claim below
  // — that the round trip returns to this path — is untouched.
  await page.keyboard.press("KeyG");
  await waitForBoard(page, (c) => hasText(c, "Sign in"), "the guest account menu");
  await pickListRow(page, "Sign in", "the guest account menu");
  await page.waitForFunction(() => !window.__m201BeforeLogin, null, { timeout: 30000 });
  await page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await installDecoder(page);
  await waitForBoard(page, (c) => onBoard(c, "Type your name"), "the launch prompt after the sign-in round trip");
  assert.equal(authStarts.length, 1, `G must start sign-in exactly once; saw ${JSON.stringify(authStarts)}`);
  assert.equal(
    new URL(authStarts[0]).searchParams.get("return"),
    signInFrom,
    "the sign-in must ask to come back to the deep-linked path it started from",
  );
  await page.keyboard.type("Linker", { delay: 8 });
  await page.keyboard.press("Enter");
  await waitForBoard(
    page,
    (c) => titledWorld(c) === TITLE_OF[STARTUP],
    `${STARTUP}'s title screen after returning from sign-in`,
  );
  await waitFor(() => new URL(page.url()).pathname === signInFrom, "the return to land on the same deep link");
  console.log("  - a sign-in started from a deep-linked title screen came back to it");

  // =========================================================================
  // ACT 5 — leaving puts the path back to the app root
  // =========================================================================
  await page.keyboard.press("KeyQ");
  // The title screen's quit is a SIDEBAR prompt (SidebarPromptYesNo), not a board
  // window, so this is the one search here that must look outside the board.
  await waitForBoard(
    page,
    (c) => Array.from({ length: 25 }, (_, row) => textAt(c, 0, row)).some((line) => line.includes("Quit ZZT?")),
    "the quit prompt",
  );
  await page.keyboard.press("KeyY");
  await waitFor(() => new URL(page.url()).pathname === "/", "the path to return to the app root on leaving");
  console.log("  - leaving the world put the address bar back to /");

  assert.deepEqual(pageErrors, [], "the client must not raise page errors");
  assert.deepEqual(consoleErrors, [], "the client must not log console errors");
  console.log(`ALL DEEP-LINK ACTS PASSED — sockets=${sockets.length}, snapshots=${snapshots}`);
  await browser.close();
  process.exit(0);
} catch (err) {
  console.error("deep-link journey FAILED:", err);
  console.error("sockets:", JSON.stringify(sockets));
  console.error("titleRequests:", JSON.stringify(titleRequests.slice(-12)));
  console.error("authStarts:", JSON.stringify(authStarts));
  console.error("pageErrors:", pageErrors);
  console.error("consoleErrors:", consoleErrors);
  try {
    saveText("m201-failure.txt", gridToArt(await readGrid(page)));
  } catch {
    /* the canvas may not decode at all, which the error above already says */
  }
  await browser.close();
  process.exit(1);
}
