// view3d.test.mjs — M35: the 3D view inside the regular client.
//
// The claim under test is narrow and load-bearing: pressing V turns the board
// columns of the text screen into a window onto a 3D scene, and pressing it
// again puts the text screen back exactly as it was. Everything around the
// board -- the sidebar, the text windows, the notices -- is still drawn by
// drawScreen either way.
//
// It is asserted three ways, because any one of them alone can be satisfied by
// a client that is not actually drawing:
//
//   1. The 2D canvas's board columns go TRANSPARENT while the sidebar keeps its
//      pixels. That is drawScreen's 3D branch having run, read back off the
//      real canvas rather than inferred from a flag.
//   2. The lazy chunk is fetched. three.js is behind an await import(), so a V
//      that never loaded it never built a scene.
//   3. The page changes. A screenshot of the board region before and after must
//      differ -- a transparent canvas over a black page would satisfy (1) while
//      showing nothing at all.
//
// The clock is deliberately NOT paused here, unlike control_keys.test.mjs: the
// 3D view renders on requestAnimationFrame, and a frozen clock is a view that
// never draws its first frame.

import assert from "node:assert/strict";
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";

import {
  baseURL,
  hasText,
  installDecoder,
  installImageProbe,
  launchGoldenBrowser,
  markProfileWarm,
  readGrid,
  waitForGrid,
  waitForQuiet,
} from "./lib/canvas.mjs";

assert.ok(baseURL, "BASE_URL must be set by the harness");

const OUT_DIR = process.env.M35_OUT || join("test-results", "view3d");
mkdirSync(OUT_DIR, { recursive: true });

const BOARD_COLS = 60;
const COLS = 80;
const ROWS = 25;

/** Alpha of one screen cell's centre, off the 2D canvas itself. */
async function cellAlpha(page, cx, cy) {
  return page.evaluate(
    ({ cx, cy, COLS, ROWS }) => {
      const canvas = document.querySelector("canvas[data-screen]");
      const ctx = canvas.getContext("2d");
      const cw = canvas.width / COLS;
      const ch = canvas.height / ROWS;
      const x = Math.floor((cx + 0.5) * cw);
      const y = Math.floor((cy + 0.5) * ch);
      return ctx.getImageData(x, y, 1, 1).data[3];
    },
    { cx, cy, COLS, ROWS },
  );
}

async function shot(page, name) {
  const buffer = await page.screenshot();
  const path = join(OUT_DIR, `${name}.png`);
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, buffer);
  return buffer;
}

const { browser, context, page, pageErrors, consoleErrors } = await launchGoldenBrowser();
await markProfileWarm(context);
let failed = false;

try {
  // The font atlas is inlined by the bundler, so there is no .png resource for
  // the decoder to wait on: it needs the Image probe installed before the page
  // is navigated, exactly as control_keys.test.mjs does.
  await installImageProbe(page);

  const chunks = [];
  page.on("response", (response) => {
    const url = response.url();
    if (url.endsWith(".js")) {
      chunks.push(url.split("/").pop());
    }
    if (response.status() >= 400) {
      consoleErrors.push(`HTTP ${response.status()} ${url}`);
    }
  });

  // ---------------------------------------------------------------------
  // Join, through the production launch flow
  // ---------------------------------------------------------------------
  const response = await page.goto(baseURL);
  assert.equal(response?.status(), 200, "the client index must be served");
  await page.waitForSelector("canvas[data-screen]", { timeout: 15000 });
  await installDecoder(page);

  await waitForGrid(page, (cells) => hasText(cells, "Type your name"), "the launch name prompt");
  await page.keyboard.type("Solid");
  await page.keyboard.press("Enter");
  await waitForGrid(page, (cells) => hasText(cells, "Choose a World"), "the world picker");
  await page.keyboard.type("CONTROL");
  await waitForGrid(page, (cells) => hasText(cells, "CONTROL"), "the picker to match CONTROL");
  await page.keyboard.press("Enter");
  await waitForGrid(page, (cells) => hasText(cells, "P  Play"), "the title screen for CONTROL");
  await page.keyboard.press("KeyP");
  await waitForGrid(page, (cells) => hasText(cells, "Health:100"), "the joined board");
  await waitForQuiet(page);

  const chunksBeforeV = chunks.length;
  const classicShot = await shot(page, "a-classic");

  // The board is opaque on the text screen: every cell is filled, always.
  assert.equal(
    await cellAlpha(page, 10, 12),
    255,
    "on the text screen the board columns must be painted, not transparent",
  );
  assert.equal(
    await page.locator("canvas[data-view3d]").evaluate((el) => el.hidden),
    true,
    "the 3D canvas must stay hidden until somebody asks for it",
  );

  // ---------------------------------------------------------------------
  // V: the board becomes a window
  // ---------------------------------------------------------------------
  await page.keyboard.press("KeyV");
  await page.waitForFunction(
    () => document.querySelector("canvas[data-view3d]")?.hidden === false,
    undefined,
    { timeout: 15000 },
  );
  await page.waitForTimeout(1200); // a few frames of the rig settling
  const worldShot = await shot(page, "b-world");

  // 1. drawScreen's 3D branch ran: the board is a hole, the sidebar is not.
  assert.equal(await cellAlpha(page, 10, 12), 0, "the board columns must be cleared to transparent in 3D");
  assert.equal(
    await cellAlpha(page, BOARD_COLS + 8, 12),
    255,
    "the sidebar must still be painted in 3D — it is not part of the world",
  );

  // 2. The lazy chunk arrived. Without it there is no scene to look at.
  assert.ok(
    chunks.length > chunksBeforeV,
    `pressing V must fetch the 3D chunk; scripts seen: ${chunks.join(", ")}`,
  );

  // 3. The page actually changed. A transparent canvas over nothing would pass
  //    the first two assertions and show a black screen.
  assert.notEqual(
    Buffer.compare(classicShot, worldShot),
    0,
    "the screen must look different in 3D — a cleared board over an empty scene is not a view",
  );

  // ---------------------------------------------------------------------
  // V again: the text screen comes back
  // ---------------------------------------------------------------------
  await page.keyboard.press("KeyV");
  await page.waitForFunction(
    () => document.querySelector("canvas[data-view3d]")?.hidden === true,
    undefined,
    { timeout: 15000 },
  );
  await waitForQuiet(page);
  const backShot = await shot(page, "c-back-to-classic");
  assert.equal(await cellAlpha(page, 10, 12), 255, "V again must put the painted board back");

  // The assertion that matters, and the one this suite was missing when it
  // first went green: the SCREEN must look like the text screen again. An
  // earlier build repainted the board opaquely and hid the 3D canvas by its
  // hidden attribute, and both of those were true while the world was still
  // sitting on top of them -- `canvas { display: block }` beats the user
  // agent's `[hidden] { display: none }`, so the attribute did nothing. Reading
  // one canvas back cannot see that; comparing the rendered page can.
  assert.equal(
    Buffer.compare(classicShot, backShot),
    0,
    "after V again the page must be pixel-identical to the text screen it started on",
  );

  const grid = await readGrid(page);
  assert.ok(hasText(grid, "Health:100"), "the sidebar survives the round trip");

  console.log(`view3d.test.mjs: V opens the world and closes it again; shots in ${OUT_DIR}`);
} catch (error) {
  failed = true;
  console.error(error);
} finally {
  await browser.close();
}

if (pageErrors.length > 0 || consoleErrors.length > 0) {
  console.error("page errors:", pageErrors, consoleErrors);
  failed = true;
}
process.exit(failed ? 1 : 0);
