// M16.16 — the auth and Museum journey, in a real browser.
//
// One headless Chromium drives the Vite-built client against the M16.9 harness
// (engine/m16_16_test.go), whose identity provider and Museum of ZZT are both
// served by the test binary. The route is the one a tester walks:
//
//   G on the title screen        → sign in through the identity provider
//   W → the world picker         → type a query, and the Museum answers
//   Enter on a Museum row        → the archive holds two worlds, so choose one
//   the chosen world's title     → P joins it
//   C → the chat window          → the line comes back under the ACCOUNT's name
//
// WHAT THIS SUITE IS EVIDENCE FOR:
//   service.auth        — the browser half: G signs in, and the sidebar names
//                         the account the identity provider issued
//   service.world-picker— the picker's own listing and metadata rows
//   mode.modal-picker   — the picker window, read off the canvas
//   mode.modal-museum   — the Museum rows and the "Choose World" selection
//   input.title-world   — W is what opens the picker
//   service.museum      — the browser half of search → select → host → join
//   service.chat        — the identity a chat line carries, on screen
//
// WHAT A BROWSER CANNOT PROVE, AND WHO PROVES IT. That the archive was fetched
// once and cached, that only the selected world was written to the hosting
// directory, that the instance has exactly this client, and that the PERSISTED
// chat record is attributed to the account — all of that is asserted by the Go
// test that runs this script, and the chat record is also read back here
// through the control listener so the two agree.
//
// TWO THINGS THAT SILENTLY PRODUCE A "PASSING" TEST:
//   1. The sidebar carries its own menu rows; every canvas search here is
//      confined to the 60-column board area where the windows live, so a
//      sidebar match can never stand in for a window.
//   2. `go test` caches this test and the .mjs is not a tracked dependency —
//      iterate with -count=1 or you will read a stale pass.

import assert from "node:assert/strict";
import {
  baseURL,
  controlURL,
  gridToArt,
  hasText,
  installDecoder,
  installImageProbe,
  launchGoldenBrowser,
  launchOpensPicker,
  markProfileWarm,
  pickListRow,
  readGrid,
  saveText,
  textAt,
} from "./lib/canvas.mjs";

const LOCAL_WORLD = process.env.M1616_WORLD || "M1616";
const HOSTED_WORLD = process.env.M1616_HOSTED || "CAVERN1";
const ARCHIVE_TITLE = "The Cavern Collection";
const ARCHIVE_AUTHOR = "Ada Tester";
const ACCOUNT = "ada";
const DISPLAY_NAME = "Ada Lovelace";
const CHAT_TEXT = "hello from the caverns";

// Columns 60..79 are the sidebar. Windows live in the board area.
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

/** The whole board area as one lower-cased string, for a case-blind read. */
function boardTextLower(cells) {
  let text = "";
  for (let row = 0; row < 25; row += 1) text += boardText(cells, row) + "\n";
  return text.toLowerCase();
}

async function waitForBoard(page, pred, describe, timeoutMs = 30000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const cells = await readGrid(page);
    if (pred(cells)) return cells;
    if (Date.now() > deadline) {
      saveText(`m1616-timeout-${describe.replace(/\W+/g, "-")}.txt`, gridToArt(cells));
      throw new Error(`timed out waiting for ${describe}; the screen was:\n${gridToArt(cells)}`);
    }
    await page.waitForTimeout(100);
  }
}

async function control(route) {
  const response = await fetch(`${controlURL}${route}`, { signal: AbortSignal.timeout(20000) });
  if (!response.ok) throw new Error(`${route} failed (${response.status}): ${(await response.text()).trim()}`);
  return response.json();
}

// ---------------------------------------------------------------------------

const { browser, context, page, pageErrors, consoleErrors } = await launchGoldenBrowser();
await markProfileWarm(context);

// The client is a canvas; the live join is only observable on the wire.
const seen = { you: null, sockets: [] };
page.on("websocket", (ws) => {
  seen.sockets.push(ws.url());
  ws.on("framereceived", (frame) => {
    let message;
    try {
      message = JSON.parse(frame.payload);
    } catch {
      return;
    }
    const body = message.type === "boardChange" ? message.snapshot : message;
    if (body.you) seen.you = body.you;
  });
});
page.on("response", async (response) => {
  if (response.status() >= 400) consoleErrors.push(`HTTP ${response.status()} ${response.url()}`);
});

await installImageProbe(page);

/** The production launch flow: the name popup, the world picker, the title. */
async function reachTitle(playerName) {
  const response = await page.goto(baseURL, { waitUntil: "domcontentloaded" });
  assert.equal(response?.status(), 200, "the client index must be served");
  await page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await installDecoder(page);

  await waitForBoard(page, (c) => onBoard(c, "Type your name"), "the launch name prompt");
  await page.keyboard.type(playerName, { delay: 8 });
  await page.keyboard.press("Enter");
  await waitForBoard(page, (c) => onBoard(c, "Choose a World"), "the launch world picker");
  await page.keyboard.type(LOCAL_WORLD, { delay: 8 });
  await waitForBoard(page, (c) => onBoard(c, LOCAL_WORLD), `the picker to match ${LOCAL_WORLD}`);
  await page.keyboard.press("Enter");
  return waitForBoard(
    page,
    (c) => textAt(c, 69, 8, LOCAL_WORLD.length) === LOCAL_WORLD && textAt(c, 62, 11, 3) === " P ",
    `the ${LOCAL_WORLD} title screen`,
  );
}

// --- act 0: a guest reaches the title screen --------------------------------

const guestTitle = await reachTitle("Ada");
// The sign-in row is row 24 as of M19.2: ' C  Your colour' took row 23, and the
// two sit together as the menu's identity block (title.ts).
assert.equal(
  textAt(guestTitle, 62, 24, 3),
  " G ",
  "the title menu must offer G — Google sign-in (service.auth's only affordance)",
);
assert.equal(
  textAt(guestTitle, 65, 24, 15).trim(),
  "Google sign-in",
  "before signing in the row invites a sign-in rather than naming somebody",
);
assert.equal(textAt(guestTitle, 62, 7, 3), " W ", "the title menu must offer W — World (input.title-world)");

// --- act 1: G signs in through the identity provider ------------------------
//
// Nothing is injected into the browser: G navigates to /api/auth/google/start,
// the provider redirects back through the real callback, and the client reloads
// carrying the session cookie HandleCallback set.

await control(`/control/auth/next?account=${encodeURIComponent(ACCOUNT)}`);
await page.evaluate(() => {
  window.__m1616BeforeLogin = true;
});
await page.keyboard.press("KeyG");
// M24.1 made G open the Account menu rather than redirect, and M31.1 put a row
// above the sign-in one (M33.1). The Sign in row is walked to rather than
// counted; everything after this is the same claim.
await waitForBoard(page, (c) => hasText(c, "Sign in"), "the guest account menu");
await pickListRow(page, "Sign in", "the guest account menu");
// A fresh window means the whole redirect chain completed and the client
// reloaded; the marker cannot survive a navigation.
await page.waitForFunction(() => !window.__m1616BeforeLogin, null, { timeout: 30000 });
await page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
await installDecoder(page);

await waitForBoard(page, (c) => onBoard(c, "Type your name"), "the launch prompt after the sign-in redirect");
await page.keyboard.type("Ada", { delay: 8 });
await page.keyboard.press("Enter");
// M20.1: the sign-in returned to the path the client was on, which is the deep
// link enterWorld left in the address bar — so this load selects the world itself
// and no picker opens. Before M20.1 the return was always "/".
if (launchOpensPicker(page)) {
  await waitForBoard(page, (c) => onBoard(c, "Choose a World"), "the world picker after signing in");
  await page.keyboard.type(LOCAL_WORLD, { delay: 8 });
  await waitForBoard(page, (c) => onBoard(c, LOCAL_WORLD), `the picker to match ${LOCAL_WORLD}`);
  await page.keyboard.press("Enter");
}

const signedIn = await waitForBoard(
  page,
  (c) => textAt(c, 65, 24, 15).trim() === DISPLAY_NAME,
  `the title sidebar to name ${DISPLAY_NAME}`,
);
assert.equal(textAt(signedIn, 62, 24, 3), " G ", "the sign-in row keeps its badge once signed in");

// --- act 2: W opens the picker, and typing reaches the Museum ---------------

await page.keyboard.press("KeyW");
const picker = await waitForBoard(page, (c) => onBoard(c, "Type to search"), "the world picker window");
assert.ok(
  // Read case-insensitively (M33.1). The claim is that the picker tells a
  // player typing reaches the Museum; it was written against M18.9's lower-case
  // "the museum", and M27.1's front page reworded the same line to "Type to
  // search all/Museum; shelves". Only the capital M moved, so the claim is
  // asked of the words rather than of the casing.
  boardTextLower(picker).includes("museum"),
  "the picker must say that typing searches the Museum, or nobody will type",
);

await page.keyboard.type("cavern", { delay: 20 });
const results = await waitForBoard(
  page,
  (c) => onBoard(c, ARCHIVE_TITLE),
  "the Museum result row",
  40000,
);
// The row is a real listing, not just a title: the picker's second line credits
// the author and dates the release, and marks where the entry came from.
const detail = findOnBoard(results, `by ${ARCHIVE_AUTHOR}`);
assert.ok(detail, `the Museum row must credit its author:\n${gridToArt(results)}`);
const detailLine = boardText(results, detail.y);
assert.ok(detailLine.includes("1996-05-04"), `the Museum row must date the release: ${JSON.stringify(detailLine)}`);
assert.ok(detailLine.includes("Museum"), `the Museum row must say it is from the Museum: ${JSON.stringify(detailLine)}`);

// --- act 3: the archive holds two worlds, so the player chooses one ---------

await page.keyboard.press("Enter");
const choices = await waitForBoard(page, (c) => onBoard(c, "Choose World"), "the world-selection window", 40000);
assert.ok(onBoard(choices, "CAVERN1.ZZT"), "the selection window must offer the first world");
assert.ok(onBoard(choices, "CAVERN2.ZZT"), "the selection window must offer the second world");
assert.ok(
  !onBoard(choices, "README.TXT"),
  `only .ZZT worlds may be offered:\n${gridToArt(choices)}`,
);

// The cursor starts on the first entry (openSelectList), so Enter takes it.
await page.keyboard.press("Enter");
const hosted = await waitForBoard(
  page,
  (c) => textAt(c, 69, 8, HOSTED_WORLD.length) === HOSTED_WORLD,
  `the title screen of the imported ${HOSTED_WORLD}`,
  60000,
);
assert.ok(!onBoard(hosted, "Not playable"), `the import was refused:\n${gridToArt(hosted)}`);
assert.ok(!onBoard(hosted, "Choose World"), "the selection window must close once a world is hosted");
assert.equal(
  textAt(hosted, 65, 24, 15).trim(),
  DISPLAY_NAME,
  "the import must not have signed the player out",
);

// --- act 4: P joins the imported world -------------------------------------

await page.keyboard.press("KeyP");
await waitForBoard(page, () => seen.you !== null, `the join snapshot for ${HOSTED_WORLD}`, 30000);
assert.ok(
  seen.sockets.some((url) => url.includes(`world=${HOSTED_WORLD}`)),
  `the browser joined ${seen.sockets.join(", ")}, not the world it imported`,
);

// --- act 5: the chat line comes back under the account's name ---------------

await page.keyboard.press("KeyC");
const chatWindow = await waitForBoard(page, (c) => onBoard(c, "Global Chat"), "the chat window");
assert.ok(
  onBoard(chatWindow, "Enter sends"),
  "the chat window must say how to send; it has no other affordance",
);
await page.keyboard.type(CHAT_TEXT, { delay: 8 });
await waitForBoard(page, (c) => onBoard(c, "caverns"), "the message echoed in the chat window");
await page.keyboard.press("Enter");

// The transient line the client prints under the board (row 24) is the first
// place the broadcast lands, and it carries the name the SERVER attributed.
const broadcast = await waitForBoard(
  page,
  (c) => onBoard(c, `<${DISPLAY_NAME}> ${CHAT_TEXT}`),
  "the broadcast chat line under the account's name",
);
assert.ok(
  !onBoard(broadcast, "<Ada>"),
  "the chat line was attributed to the typed nickname instead of the account",
);

// And the history the window keeps says the same thing.
await page.keyboard.press("KeyC");
const history = await waitForBoard(
  page,
  (c) => onBoard(c, "Global Chat") && onBoard(c, `<${DISPLAY_NAME}> ${CHAT_TEXT}`),
  "the chat history under the account's name",
);
assert.ok(history, "the chat window reopened without its history");
await page.keyboard.press("Escape");

// --- act 6: the server agrees ----------------------------------------------

const instances = await control("/control/instances");
const hostedInstance = instances.find((entry) => entry.name === HOSTED_WORLD);
assert.ok(hostedInstance, `the server hosts ${instances.map((i) => i.name).join(", ")}, not ${HOSTED_WORLD}`);
assert.equal(hostedInstance.clients, 1, `${HOSTED_WORLD} must hold exactly the browser that joined it`);

const chat = await control("/control/chat");
assert.equal(chat.length, 1, `the server persisted ${chat.length} chat lines, want one`);
assert.equal(chat[0].from, DISPLAY_NAME, "the persisted line must carry the account's display name");
assert.equal(chat[0].text, CHAT_TEXT, "the persisted line must carry the text that was typed");

// ---------------------------------------------------------------------------

assert.deepEqual(pageErrors, [], "the page threw");
assert.deepEqual(consoleErrors, [], "the console carried errors");

console.log(
  `M16.16 browser journey: G → ${DISPLAY_NAME} → W → "${ARCHIVE_TITLE}" → ${HOSTED_WORLD} hosted, joined, and chatted in` +
    `\n  picker row: ${boardText(results, detail.y).trim()}` +
    `\n  chat: <${chat[0].from}> ${chat[0].text}`,
);

await context.close();
await browser.close();
