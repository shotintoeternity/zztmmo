// M19.2 — the colour picker, in a real browser.
//
// Driven by engine/m19_2_browser_test.go, which hosts ACCEPT on the PRODUCTION
// zzt-server binary and runs this script against it (the M19.1 shape: no control
// listener, no test hooks — the claim is about the software we ship).
//
// WHAT THIS PROVES THAT color_picker.test.mjs CANNOT. The unit test owns the
// rules; only a browser can show that the window is on the screen, that its keys
// go to it and not to the title menu underneath, that the pick reaches
// localStorage and survives a reload, and that the join the server reads carries
// the colour the player chose. The 24-bit swatch is read as RAW PIXELS for the
// M19.1 reason: the M16.9 decoder maps a background to the nearest EGA index,
// which is exactly what an arbitrary colour is not.
//
// THE TOUCH LEG is the DoD's "reachable on a touch profile". A phone reaches the
// title menu's ' C ' through the on-screen bar (M16.18a) and then drives the
// window with the pad and Enter, which is why the picker's whole vocabulary is
// arrows plus Enter plus Escape — and why the hex field is typed rather than
// dragged.

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { chromium } from "playwright";
import { cellAt, hasText, installDecoder, installImageProbe, textAt } from "./lib/canvas.mjs";

const baseURL = process.env.BASE_URL || "http://127.0.0.1:8080";
const resultsDir = path.resolve(process.env.PICKER_OUT || "test-results/color-picker");
fs.mkdirSync(resultsDir, { recursive: true });

const CELL_W = 8;
const CELL_H = 14;
const WORLD = "ACCEPT";
const COLOR_KEY = "zzt-color";

// The title menu's ' C ' row and its swatch (title.ts: TITLE_COLOR_SWATCH).
const COLOR_ROW = 23;
const SWATCH_X = 78;

const QUICK_PICK = "#ff5555"; // DOS "Red", picked with the arrows alone
const TYPED_PICK = "#7f3fbf"; // and a colour no DOS palette has ever had

const clients = [];

async function openClient(label, { hasTouch = false } = {}) {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1280, height: 720 }, hasTouch });
  const page = await context.newPage();
  const c = { label, browser, context, page, pageErrors: [] };
  clients.push(c);

  page.on("pageerror", (err) => c.pageErrors.push(String(err)));

  // The wire is recorded IN THE PAGE, not through Playwright's websocket events:
  // this suite reloads twice (that is the reload and the reconnect it exists to
  // prove) and an init script is re-installed on every navigation, where a
  // listener attached to the first socket is not. Each load starts a fresh
  // record, which is what the assertions want anyway — "the join THIS load sent".
  await page.addInitScript(() => {
    window.__wire = { joins: [], frames: [] };
    const OriginalWS = window.WebSocket;
    const Wrapped = function (...args) {
      const ws = new OriginalWS(...args);
      const send = ws.send.bind(ws);
      ws.send = (data) => {
        if (typeof data === "string" && data.includes('"type":"join"')) window.__wire.joins.push(data);
        return send(data);
      };
      ws.addEventListener("message", (event) => {
        const raw = String(event.data);
        // Only the frames that carry a roster; a room sends a diff every tick.
        if (raw.includes('"you"') || raw.includes('"players"')) window.__wire.frames.push(raw);
      });
      return ws;
    };
    Wrapped.prototype = OriginalWS.prototype;
    Object.assign(Wrapped, OriginalWS);
    window.WebSocket = Wrapped;
  });

  await installImageProbe(page);
  const response = await page.goto(baseURL);
  assert.equal(response?.status(), 200, `${label}: the client index must be served, not the build-me 404 page`);
  await page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await installDecoder(page);
  return c;
}

const sleep = (c, ms) => c.page.waitForTimeout(ms);
const settle = (c) => sleep(c, 700);

/** The launch flow up to the title menu of `world` — name, then world, no play. */
async function reachTitle(c, name, world) {
  await c.page.keyboard.type(name);
  await c.page.keyboard.press("Enter");
  await settle(c);
  await c.page.keyboard.type(world);
  await sleep(c, 400);
  await c.page.keyboard.press("Enter");
  await settle(c);
  await waitForCells(c.page, (cells) => hasText(cells, "P  Play"), `${c.label}'s title menu for ${world}`);
}

const storedColor = (c) => c.page.evaluate((key) => window.localStorage.getItem(key), COLOR_KEY);

/** What this page load sent as a join, and which player the server says is ours. */
const wireOf = (c) =>
  c.page.evaluate(() => {
    const w = window.__wire || { joins: [], frames: [] };
    let you = null;
    for (const raw of w.frames) {
      let msg;
      try {
        msg = JSON.parse(raw);
      } catch {
        continue;
      }
      const body = msg.type === "boardChange" ? msg.snapshot : msg;
      if (body.you) you = body.you;
      if (Array.isArray(body.players) && you) {
        const me = body.players.find((p) => p.id === you.id);
        if (me) you = me;
      }
    }
    return { joins: w.joins, you };
  });

/**
 * The screen as cells, TOLERATING cells the EGA decoder cannot express.
 *
 * This is the M19.1 observation used deliberately rather than worked around: a
 * 24-bit tint is exactly what the M16.9 decoder maps no index onto, so the
 * library's readGrid throws on the very cells this feature exists to paint. The
 * cells it cannot read come back as '?' and are read as pixels instead — every
 * text assertion below is about ordinary text, and every colour assertion is
 * about pixels.
 */
async function readCells(page) {
  const grid = await page.evaluate(() => window.__m169.readGrid());
  return grid.cells;
}

async function waitForCells(page, pred, describe, timeoutMs = 15000) {
  const deadline = Date.now() + timeoutMs;
  let last = null;
  for (;;) {
    last = await readCells(page);
    if (pred(last)) return last;
    if (Date.now() > deadline) {
      throw new Error(`timed out waiting for ${describe}\n${last.map((cell, i) => (i % 80 === 79 ? String.fromCharCode(cell.ch) + "\n" : String.fromCharCode(cell.ch))).join("")}`);
    }
    await page.waitForTimeout(80);
  }
}

/** The colour of one cell's corners, which the ☻ glyph never inks (M19.1). */
async function cellCorners(c, col, row) {
  return c.page.evaluate(
    ([col, row, cellW, cellH]) => {
      const canvas = document.querySelector("canvas[data-screen]");
      const ctx = canvas.getContext("2d", { willReadFrequently: true });
      const x = col * cellW;
      const y = row * cellH;
      const hex = (px, py) => {
        const d = ctx.getImageData(px, py, 1, 1).data;
        return "#" + [d[0], d[1], d[2]].map((v) => v.toString(16).padStart(2, "0")).join("");
      };
      return [hex(x, y), hex(x + cellW - 1, y), hex(x, y + cellH - 1), hex(x + cellW - 1, y + cellH - 1)];
    },
    [col, row, CELL_W, CELL_H],
  );
}

async function assertCellIs(c, col, row, expected, describe) {
  const corners = await cellCorners(c, col, row);
  for (const corner of corners) {
    assert.equal(
      corner,
      expected,
      `${describe}: ${c.label}'s canvas shows ${corner} at (${col},${row}) — corners ${JSON.stringify(corners)}`,
    );
  }
}

const pickerIsOpen = (cells) => hasText(cells, "Your Player Colour");

async function openPicker(c) {
  await c.page.keyboard.press("KeyC");
  await waitForCells(c.page, pickerIsOpen, `${c.label}'s colour picker to open`);
}

async function dumpFailure(err) {
  console.error("M19.2 colour-picker suite FAILED:", err);
  for (const c of clients) {
    let wire = "unavailable";
    try {
      wire = JSON.stringify((await wireOf(c)).joins);
    } catch {
      // The page may already be gone; the error above is what matters.
    }
    console.error(`--- ${c.label}: joins=${wire} pageErrors=${JSON.stringify(c.pageErrors)}`);
    try {
      await c.page.screenshot({ path: path.join(resultsDir, `m19_2_${c.label.toLowerCase()}_failure.png`) });
    } catch (dumpErr) {
      console.error(`  (could not screenshot ${c.label}: ${dumpErr})`);
    }
  }
  console.error(`Saved screenshots under ${resultsDir}`);
}

try {
  console.log(`=== M19.2: the colour picker, against ${baseURL} ===`);

  // -------------------------------------------------------------------------
  // 1. The menu row, before anything is picked.
  // -------------------------------------------------------------------------
  const ada = await openClient("Ada");
  await reachTitle(ada, "Ada", WORLD);

  let cells = await readCells(ada.page);
  assert.equal(
    textAt(cells, 62, COLOR_ROW, 15),
    " C  Your colour",
    `the title menu must offer the picker; row ${COLOR_ROW} reads "${textAt(cells, 62, COLOR_ROW, 20)}"`,
  );
  assert.equal(cellAt(cells, SWATCH_X, COLOR_ROW).ch, 32, "an unpicked player gets no swatch on the menu");
  assert.equal(await storedColor(ada), null, "nothing is stored before a pick");

  // -------------------------------------------------------------------------
  // 2. The window opens, and it is a CP437 window like every other one.
  // -------------------------------------------------------------------------
  await openPicker(ada);
  cells = await readCells(ada.page);
  for (const needle of ["Dark Blue", "Yellow", "Any colour:", "No colour (the vanilla ZZT player)", "Esc cancels"]) {
    assert.ok(hasText(cells, needle), `the picker must show "${needle}"`);
  }

  // -------------------------------------------------------------------------
  // 3. No keystroke leaks to the menu underneath (the M16.18a Fire lesson).
  //    'P' would start the game and 'W' would open the world picker.
  // -------------------------------------------------------------------------
  for (const code of ["KeyP", "KeyW", "KeyD", "KeyE"]) {
    await ada.page.keyboard.press(code);
    await sleep(ada, 150);
  }
  cells = await readCells(ada.page);
  assert.ok(pickerIsOpen(cells), "the picker must still be the window on screen after the menu keys");
  assert.ok(!hasText(cells, "Health:"), "no key may have started the game behind the window");
  assert.deepEqual((await wireOf(ada)).joins, [], "no key may have joined a room behind the window");

  // -------------------------------------------------------------------------
  // 4. Escape cancels: the window closes and nothing has been picked.
  // -------------------------------------------------------------------------
  await ada.page.keyboard.press("ArrowUp");
  await ada.page.keyboard.press("ArrowUp");
  await ada.page.keyboard.press("Escape");
  await waitForCells(ada.page, (cells) => !pickerIsOpen(cells), "the picker to close on Escape");
  assert.equal(await storedColor(ada), null, "Escape must change nothing");
  cells = await readCells(ada.page);
  assert.ok(hasText(cells, "P  Play"), "Escape returns to the title menu");

  // -------------------------------------------------------------------------
  // 5. A quick pick, with the arrows alone — the pad-only path a phone uses.
  //    The window opens on the vanilla row: up to the hex row, up again to the
  //    foot of the dark column (Grey), right to White, then up three to Red.
  // -------------------------------------------------------------------------
  await openPicker(ada);
  for (const code of ["ArrowUp", "ArrowUp", "ArrowRight", "ArrowUp", "ArrowUp", "ArrowUp"]) {
    await ada.page.keyboard.press(code);
    await sleep(ada, 60);
  }
  cells = await readCells(ada.page);
  const previewX = textAt(cells, 0, 18).indexOf("This is you:") + "This is you:  ".length;
  assert.ok(previewX > 0, `the picker must preview the selection; row 18 reads "${textAt(cells, 0, 18)}"`);
  assert.equal(cellAt(cells, previewX, 18).ch, 0x02, "the preview is the player glyph itself");
  // The preview is painted by the same per-cell override the board uses, so
  // this is the colour the room would give you — not a second drawing path.
  await assertCellIs(ada, previewX, 18, QUICK_PICK, "the preview shows the highlighted colour");

  await ada.page.keyboard.press("Enter");
  await waitForCells(ada.page, (cells) => !pickerIsOpen(cells), "the picker to close on Enter");
  assert.equal(await storedColor(ada), QUICK_PICK, "Enter stores the highlighted quick pick");
  cells = await readCells(ada.page);
  assert.equal(cellAt(cells, SWATCH_X, COLOR_ROW).ch, 0x02, "the menu now wears the player's own smiley");
  await assertCellIs(ada, SWATCH_X, COLOR_ROW, QUICK_PICK, "the menu swatch is the picked colour");

  // -------------------------------------------------------------------------
  // 6. It survives a reload: localStorage, not the run.
  // -------------------------------------------------------------------------
  await ada.page.reload();
  await ada.page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await installDecoder(ada.page);
  await reachTitle(ada, "Ada", WORLD);
  assert.equal(await storedColor(ada), QUICK_PICK, "a reload keeps the pick");
  await assertCellIs(ada, SWATCH_X, COLOR_ROW, QUICK_PICK, "and the menu still wears it after a reload");

  // -------------------------------------------------------------------------
  // 7. A typed colour — the half the 16 quick picks cannot express.
  // -------------------------------------------------------------------------
  await openPicker(ada);
  await ada.page.keyboard.type(TYPED_PICK.slice(1).toUpperCase());
  await sleep(ada, 150);
  cells = await readCells(ada.page);
  assert.ok(hasText(cells, "#" + TYPED_PICK.slice(1)), "the typed colour is shown in the field, in lower case");
  await ada.page.keyboard.press("Enter");
  await waitForCells(ada.page, (cells) => !pickerIsOpen(cells), "the picker to close on the typed colour");
  assert.equal(await storedColor(ada), TYPED_PICK, "a typed colour is stored like a quick pick");

  // -------------------------------------------------------------------------
  // 8. The pick reaches the room: the join carries it and the board shows it.
  // -------------------------------------------------------------------------
  await ada.page.keyboard.press("KeyP");
  await waitForCells(ada.page, (cells) => hasText(cells, "Health:"), "Ada's room");
  const played = await wireOf(ada);
  assert.equal(played.joins.length, 1, "pressing Play joins once");
  assert.ok(played.joins[0].includes(`"color":"${TYPED_PICK}"`), `the join must carry the pick, sent: ${played.joins[0]}`);
  await sleep(ada, 900);
  const you = (await wireOf(ada)).you;
  assert.ok(you, "the server told Ada which player is hers");
  await assertCellIs(ada, you.x - 1, you.y - 1, TYPED_PICK, "Ada's own smiley wears the colour she typed");

  // -------------------------------------------------------------------------
  // 9. And it survives a reconnect: the reclaimed run is joined in colour, the
  //    colour being the browser's current pick rather than a property of the run.
  // -------------------------------------------------------------------------
  await ada.page.reload();
  await ada.page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await installDecoder(ada.page);
  await reachTitle(ada, "Ada", WORLD);
  await ada.page.keyboard.press("KeyP");
  await waitForCells(ada.page, (cells) => hasText(cells, "Health:"), "Ada's reclaimed room");
  const rejoin = (await wireOf(ada)).joins.at(-1);
  assert.ok(rejoin.includes('"resumeToken"'), `the rejoin must reclaim the run, sent: ${rejoin}`);
  assert.ok(rejoin.includes(`"color":"${TYPED_PICK}"`), `the rejoin must still carry the pick, sent: ${rejoin}`);

  // -------------------------------------------------------------------------
  // 10. The way back to vanilla, which is a real state and not the absence of
  //     one: the key is removed, and an old client, a replay and an unpicked
  //     player all send exactly that.
  // -------------------------------------------------------------------------
  await ada.page.keyboard.press("KeyQ");
  await sleep(ada, 300);
  await ada.page.keyboard.press("KeyY");
  await waitForCells(ada.page, (cells) => hasText(cells, "P  Play"), "Ada back at the title menu");
  await openPicker(ada);
  // The window opens on the custom row holding her colour; the vanilla row is
  // one below it.
  await ada.page.keyboard.press("ArrowDown");
  await ada.page.keyboard.press("Enter");
  await waitForCells(ada.page, (cells) => !pickerIsOpen(cells), "the picker to close on the vanilla row");
  assert.equal(await storedColor(ada), null, "choosing no colour removes the key rather than writing an empty one");
  cells = await readCells(ada.page);
  assert.equal(cellAt(cells, SWATCH_X, COLOR_ROW).ch, 32, "and the menu stops claiming a pick");

  // -------------------------------------------------------------------------
  // 11. The touch profile: reachable, and drivable, with no keyboard at all
  //     once the name is in (M16.18a's bar sends the same key bytes).
  // -------------------------------------------------------------------------
  const pip = await openClient("Pip", { hasTouch: true });
  await reachTitle(pip, "Pip", WORLD);

  const colorButton = pip.page.locator('[data-touch="color"]');
  await colorButton.waitFor({ state: "visible", timeout: 10000 });
  await colorButton.tap();
  await waitForCells(pip.page, pickerIsOpen, "Pip's colour picker, opened by tapping the bar");

  // A gameplay control must not be on screen over the window — a Fire tap is a
  // space, and this window has a text field (M16.18a).
  assert.equal(await pip.page.locator('[data-touch="fire"]').isVisible(), false, "no Fire button over the picker");
  assert.equal(await colorButton.isVisible(), false, "and the title-only controls step aside too");

  // Up twice reaches the foot of the dark column: Grey, #aaaaaa.
  for (let i = 0; i < 2; i += 1) {
    await pip.page.locator('[data-touch="up"]').tap();
    await sleep(pip, 80);
  }
  await pip.page.locator('[data-touch="enter"]').tap();
  await waitForCells(pip.page, (cells) => !pickerIsOpen(cells), "Pip's picker to close");
  assert.equal(await storedColor(pip), "#aaaaaa", "a phone can pick a colour with the pad and Enter alone");
  await assertCellIs(pip, SWATCH_X, COLOR_ROW, "#aaaaaa", "and the menu wears it on a touch profile");

  for (const c of clients) {
    assert.deepEqual(c.pageErrors, [], `${c.label} must reach this point with no page errors`);
  }

  console.log("=== M19.2 colour picker: PASS ===");
} catch (err) {
  await dumpFailure(err);
  process.exitCode = 1;
} finally {
  for (const c of clients) {
    try {
      await c.browser.close();
    } catch {
      // closing a browser that already died is not a test failure
    }
  }
}
