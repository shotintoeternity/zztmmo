// M16.17 — the Dream journey, in a real browser.
//
// Drives the Vite-built client in headless Chromium against a real zzt-server
// subprocess whose model is a scripted fake (engine/m16_17_test.go): D on the
// title screen, the premise typed into the "Dream a world" window, the
// "Dreaming a world" progress window, and then the world itself — entered
// through the client's own title screen and played over a real WebSocket.
//
// WHAT THIS SUITE IS EVIDENCE FOR:
//   input.title-dream — D is what opens the flow
//   mode.modal-dream  — the prompt window and the progress window, read off
//                       the canvas rather than out of the module under Node
//                       (web/test/dream.test.mjs already does the latter)
//   service.dream     — the browser half of the journey: a premise becomes a
//                       world this same browser plays
//
// AND FOR ONE PINNED DEFECT (M16.17c). The scripted model refuses to paint the
// START board, so the server salvages it into a stub room and marks the job
// `complete` AND `retryable` with `stubbedBoards: ["Start"]` — M17.13's
// contract, whose stated purpose is "so the client can repaint the missing
// rooms while the player is already in the world". The client never reads
// either field: pollDreamJob only surfaces `retryable` when the job FAILED, and
// no failure path constructs that state any more. So the player is dropped into
// the failed room with no offer to repaint it. This script asserts that absence
// on purpose; when M16.17c lands it goes red and gets inverted.
//
// TWO THINGS THAT SILENTLY PRODUCE A "PASSING" TEST:
//   1. "Dream a world" is ALSO the title sidebar's own menu row (title.ts).
//      Every search here is confined to the 60-column board area, where the
//      modals live, so a sidebar match can never stand in for a window.
//   2. `go test` caches this test and the .mjs is not a tracked dependency —
//      iterate with -count=1 or you will read a stale pass.

import assert from "node:assert/strict";
import { chromium } from "playwright";
import {
  baseURL,
  gridToArt,
  installDecoder,
  installImageProbe,
  readGrid,
  saveText,
  textAt,
} from "./lib/canvas.mjs";

const PREMISE = process.env.M1617_PREMISE || "a lighthouse that keeps the tide's diary";
const WORLD = process.env.M1617_WORLD || "DREAMED";

// The board area only: columns 60..79 are the sidebar, which carries its own
// "Dream a world" menu row and would answer for the window that is under test.
const BOARD_COLS = 60;

function boardText(cells, row) {
  return textAt(cells, 0, row, BOARD_COLS);
}

function findOnBoard(cells, needle) {
  for (let row = 0; row < 25; row += 1) {
    const col = boardText(cells, row).indexOf(needle);
    if (col >= 0) return { x: col, y: row };
  }
  return null;
}

const onBoard = (cells, needle) => findOnBoard(cells, needle) !== null;

async function waitForBoard(page, pred, describe, timeoutMs = 30000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const cells = await readGrid(page);
    if (pred(cells)) return cells;
    if (Date.now() > deadline) {
      saveText(`m1617-timeout-${describe.replace(/\W+/g, "-")}.txt`, gridToArt(cells));
      throw new Error(`timed out waiting for ${describe}; the screen was:\n${gridToArt(cells)}`);
    }
    await page.waitForTimeout(100);
  }
}

// ---------------------------------------------------------------------------

const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 1280, height: 720 }, deviceScaleFactor: 1 });
const page = await context.newPage();

const pageErrors = [];
const consoleErrors = [];
page.on("pageerror", (error) => pageErrors.push(String(error)));
page.on("console", (message) => {
  if (message.type() === "error") consoleErrors.push(message.text());
});

// The client is a canvas; its live player state is only observable on the wire.
const seen = { you: null, boardId: null, hud: null, sockets: [] };
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
    if (body.hud) seen.hud = body.hud;
    if (typeof body.boardId === "number") seen.boardId = body.boardId;
    for (const player of body.players || []) {
      if (seen.you && player.id === seen.you.id) seen.you = player;
    }
  });
});

await installImageProbe(page);
await page.goto(baseURL, { waitUntil: "domcontentloaded" });
await installDecoder(page);

// --- act 0: the client's own way in -----------------------------------------
//
// The launch name popup and the world picker are presentation additions
// (deviation `presentation-additions`) that stand between a fresh page and the
// title screen. The journey goes through them rather than around them: this is
// the path a tester walks.

await waitForBoard(page, (cells) => onBoard(cells, "Type your name"), "the launch name prompt");
await page.keyboard.type("Dreamer", { delay: 8 });
await page.keyboard.press("Enter");
await waitForBoard(page, (cells) => onBoard(cells, "Choose a World"), "the world picker");
await page.keyboard.type("TOWN", { delay: 8 });
await waitForBoard(page, (cells) => onBoard(cells, "TOWN"), "the picker to match TOWN");
await page.keyboard.press("Enter");

// --- act 1: the title screen offers the dream -------------------------------

await waitForBoard(page, (cells) => textAt(cells, 62, 19, 3) === " D ", "the title menu");
const titleScreen = await readGrid(page);
assert.equal(
  textAt(titleScreen, 65, 19, 14),
  " Dream a world",
  "the title menu must offer D — Dream a world (input.title-dream)",
);

// --- act 2: D opens the premise window --------------------------------------

await page.keyboard.press("KeyD");
const prompt = await waitForBoard(
  page,
  (cells) => onBoard(cells, "Describe the world you want"),
  "the Dream a world window",
);
assert.ok(onBoard(prompt, "Dream a world"), "the window keeps its title");
assert.ok(
  onBoard(prompt, "Enter: dream"),
  "the window must say how to submit; the flow has no other affordance",
);

// --- act 3: the premise, and the progress window ----------------------------

await page.keyboard.type(PREMISE, { delay: 8 });
const typed = await waitForBoard(
  page,
  (cells) => onBoard(cells, "lighthouse"),
  "the premise echoed in the window",
);
assert.ok(onBoard(typed, "tide"), "the whole premise is in the buffer, wrapped by the window");

await page.keyboard.press("Enter");

const progress = await waitForBoard(
  page,
  (cells) => onBoard(cells, "Dreaming a world"),
  "the Dreaming a world progress window",
);
assert.ok(
  onBoard(progress, "Imagining the world"),
  "the first progress line is the planner's, in the client's own copy",
);

// The stages the server reports must reach the screen as sentences, not as raw
// tokens: this is the whole of mode.modal-dream's presentation contract.
//
// A window line is drawn inside a CP437 border, so the row a decoder reads is
// "<border> Painting board 1 of 2: Start   <border>". Cut at the first
// non-ASCII-printable cell to get the line the client actually composed.
const STAGE_PREFIXES = [
  "Imagining the world",
  "Painting board",
  "Repairing",
  "Checking every board",
  "Saving the new world",
  "Lost board",
  "Some rooms",
];

function windowLines(cells) {
  const found = [];
  for (let row = 0; row < 25; row += 1) {
    const line = boardText(cells, row);
    for (const prefix of STAGE_PREFIXES) {
      const at = line.indexOf(prefix);
      if (at < 0) continue;
      const text = line
        .slice(at)
        .replace(/[^ -~].*$/, "")
        .trimEnd();
      if (text) found.push(text);
    }
  }
  return found;
}

const stageLines = new Set();
const painting = await waitForBoard(
  page,
  (cells) => {
    for (const line of windowLines(cells)) stageLines.add(line);
    return [...stageLines].some((line) => line.startsWith("Painting board"));
  },
  "a Painting board progress line",
);
assert.ok(
  [...stageLines].some((line) => /^Painting board \d+ of \d+: /.test(line)),
  `a painting line must name its place in the world: ${[...stageLines].join(" | ")}`,
);
for (const line of stageLines) {
  assert.ok(line.length <= 42, `progress line wider than the window's 42 columns: ${JSON.stringify(line)}`);
}
assert.ok(!onBoard(painting, "planning"), "a raw stage token reached the window");
assert.ok(!onBoard(painting, "persisting"), "a raw stage token reached the window");

// --- act 4: the dream becomes a world this browser can enter ----------------

await waitForBoard(
  page,
  (cells) => textAt(cells, 69, 8, WORLD.length) === WORLD,
  `the title screen of ${WORLD}`,
  90000,
);
const arrived = await readGrid(page);
assert.ok(
  !onBoard(arrived, "Dreaming a world"),
  "the progress window must close when the world is ready",
);
assert.ok(!onBoard(arrived, "Dream failed"), "the dream reported a failure");

// M16.17c — PINNED DEFECT. The job that produced this world is `complete` and
// `retryable` with one stubbed board (the Go test asserts that against the
// server). Nothing offered to repaint it.
assert.ok(
  !onBoard(arrived, "Repaint"),
  "a repaint offer appeared: M16.17c has landed — INVERT this assertion and drive the offer",
);

await page.keyboard.press("KeyP");
await waitForBoard(page, () => seen.you !== null, "the join snapshot for the dreamed world", 30000);
assert.ok(
  seen.sockets.some((url) => url.includes(`world=${WORLD}`)),
  `the browser joined ${seen.sockets.join(", ")}, not the world it dreamed`,
);

// --- act 5: the room the dream lost, and the player standing in it ----------

const room = await waitForBoard(
  page,
  (cells) => onBoard(cells, "THIS BOARD FAILED"),
  "the stub room the salvaged board left behind",
);
assert.ok(
  onBoard(room, "PLEASE PROCEED TO THE NEXT BOARD"),
  "the stub must tell the player where to go",
);
assert.ok(
  !onBoard(room, "Repaint"),
  "a repaint offer appeared in the world: M16.17c has landed — INVERT this assertion",
);

// The player really is playing: hold a direction long enough for the client's
// 55ms input sampler to see it (main.ts sendInput), and watch the wire.
const before = { x: seen.you.x, y: seen.you.y };
for (const code of ["ArrowRight", "ArrowLeft", "ArrowUp", "ArrowDown"]) {
  await page.keyboard.down(code);
  await page.waitForTimeout(400);
  await page.keyboard.up(code);
  if (seen.you.x !== before.x || seen.you.y !== before.y) break;
}
assert.ok(
  seen.you.x !== before.x || seen.you.y !== before.y,
  `the player never moved in the dreamed world (still ${before.x},${before.y})`,
);
assert.ok(seen.hud, "the dreamed world drew no sidebar HUD");

// ---------------------------------------------------------------------------

assert.deepEqual(pageErrors, [], "the page threw");
assert.deepEqual(consoleErrors, [], "the console carried errors");

console.log(
  `M16.17 dream journey: D → premise → ${[...stageLines].length} progress lines → ${WORLD} entered and played` +
    `\n  progress window showed: ${[...stageLines].join(" | ")}` +
    `\n  PINNED (M16.17c): the salvaged board was never offered for repaint`,
);

await context.close();
await browser.close();
