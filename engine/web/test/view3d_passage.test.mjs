// view3d_passage.test.mjs — M35.4: what a board change looks like at eye level.
//
// Driven by engine/m35_4_test.go on fixtures/view3d.zwd, whose passage sits due
// north of the start, clear of the row M35.2 walks.
//
// The clock is fake and advanced by hand, so the "frames" below are real
// animation frames taken one at a time rather than samples of a race. A flicker
// is a frame sequence that goes back and forth -- A B A -- and that is a thing
// this can measure rather than describe.

import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";

import {
  baseURL, hasText, installDecoder, installImageProbe, launchGoldenBrowser,
  markProfileWarm, pauseClock, readGrid, runClock, serverState, step, textAt, waitForGrid, walk,
} from "./lib/canvas.mjs";

const OUT_DIR = process.env.M354_OUT || join("test-results", "view3d-passage");
mkdirSync(OUT_DIR, { recursive: true });
const COLS = 80, ROWS = 25, BOARD_COLS = 60;

const { browser, context, page, pageErrors, consoleErrors } = await launchGoldenBrowser();
await markProfileWarm(context);
let failed = false;

async function boardShot(page) {
  const box = await page.locator("canvas[data-screen]").boundingBox();
  return page.screenshot({ clip: { x: box.x, y: box.y, width: (box.width * BOARD_COLS) / COLS, height: box.height } });
}
const hash = (b) => createHash("sha1").update(b).digest("hex").slice(0, 8);

/** How many distinct colours the board region shows, sampled on a grid. */
async function colours(page, buffer) {
  return page.evaluate(async (bytes) => {
    const bitmap = await createImageBitmap(new Blob([new Uint8Array(bytes)], { type: "image/png" }));
    const c = document.createElement("canvas");
    c.width = bitmap.width; c.height = bitmap.height;
    const ctx = c.getContext("2d");
    ctx.drawImage(bitmap, 0, 0);
    const { data } = ctx.getImageData(0, 0, bitmap.width, bitmap.height);
    const seen = new Set();
    const sx = Math.max(1, Math.floor(bitmap.width / 60)), sy = Math.max(1, Math.floor(bitmap.height / 25));
    for (let y = 0; y < bitmap.height; y += sy) for (let x = 0; x < bitmap.width; x += sx) {
      const i = (y * bitmap.width + x) * 4;
      seen.add(`${data[i]},${data[i + 1]},${data[i + 2]}`);
    }
    return seen.size;
  }, Array.from(buffer));
}

try {
  await installImageProbe(page);
  const response = await page.goto(baseURL);
  assert.equal(response?.status(), 200);
  await page.waitForSelector("canvas[data-screen]", { timeout: 15000 });
  await installDecoder(page);
  await waitForGrid(page, (c) => hasText(c, "Type your name"), "the launch name prompt");
  await page.keyboard.type("Walker");
  await page.keyboard.press("Enter");
  await waitForGrid(page, (c) => hasText(c, "Choose a World"), "the world picker");
  await page.keyboard.type("VIEW3D");
  await waitForGrid(page, (c) => hasText(c, "VIEW3D"), "the picker to match VIEW3D");
  await page.keyboard.press("Enter");
  await waitForGrid(page, (c) => hasText(c, "P  Play"), "the title screen");
  await pauseClock(page);
  await page.keyboard.press("KeyP");
  await waitForGrid(page, (c) => hasText(c, "Health:100"), "the joined board");

  await page.keyboard.press("Digit3");
  await page.waitForFunction(() => document.querySelector("canvas[data-view3d]")?.hidden === false, undefined, { timeout: 15000 });
  await runClock(page, 600);

  // 6,12 -> 6,9, one square short of the passage at 6,8.
  await walk(page, "ArrowUp", 3);
  const before = await serverState();
  console.log("standing at", JSON.stringify(before.players[0]));

  // Step onto it, then take animation frames ONE AT A TIME through the change.
  await page.keyboard.down("ArrowUp");
  await step({ await: { dx: 0, dy: -1, key: 0xc8 } });
  await page.keyboard.up("ArrowUp");

  const frames = [];
  for (let i = 0; i < 40; i += 1) {
    await runClock(page, 33);
    const buf = await boardShot(page);
    frames.push({ i, hash: hash(buf), colours: await colours(page, buf) });
    if (i < 8) writeFileSync(join(OUT_DIR, `f${String(i).padStart(2, "0")}-${hash(buf)}.png`), buf);
  }
  await step({ n: 2 });

  const after = await serverState();
  console.log("arrived at", JSON.stringify(after.players[0]));
  console.log("board row 24:", JSON.stringify(textAt(await readGrid(page), 60, 24, 20).trimEnd()));

  // A flicker is a frame that comes back after leaving: A B A.
  const seq = frames.map((f) => f.hash);
  const revisits = [];
  for (let i = 2; i < seq.length; i += 1) {
    const earlier = seq.lastIndexOf(seq[i], i - 1);
    if (earlier !== -1 && earlier < i - 1) revisits.push(`frame ${i} repeats frame ${earlier} (${seq[i]})`);
  }
  const distinct = new Set(seq).size;
  console.log("distinct frames:", distinct, "of", seq.length);
  console.log("colour counts:", frames.map((f) => f.colours).join(","));
  console.log(revisits.length ? `ALTERNATION (${revisits.length}):\n  ` + revisits.slice(0, 12).join("\n  ") : "no alternation: the sequence never goes back");

  // You arrive; you do not fly there.
  //
  // The rig smooths the camera toward your body so a STEP looks like a step
  // (1 - exp(-dt*10) in CameraRig.update). After a passage the body is on
  // another board entirely, and smoothing toward it sweeps the camera across
  // everything in between -- forty distinct frames of walls and fog going past
  // the lens, which is what gets reported as the screen flickering. snap() has
  // existed for this since the port ("a new board, not a walk") and had exactly
  // one caller, in toggleView3D.
  //
  // Measured rather than described: these are real animation frames taken one
  // at a time off a fake clock, so the count is the count. Before the fix it was
  // 40 of 40; a couple is room for the scene to finish building.
  assert.ok(
    distinct <= 4,
    `the camera must ARRIVE on a new board, not fly to it: ${distinct} of ${seq.length} frames differ, ` +
      `which is the smoothing running across the board change`,
  );
  assert.deepEqual(revisits, [], "the view must not go back and forth after a board change");
  const tail = new Set(seq.slice(-15));
  assert.equal(tail.size, 1, `the view must be still once you have arrived; the last 15 frames show ${tail.size} states`);

  // And you are actually somewhere else, with the board drawn.
  assert.notEqual(after.players[0].boardId, before.players[0].boardId, "the passage must have changed the board");
  assert.ok(frames.every((f) => f.colours >= 3), "the new board must be drawn, not a flat fill");
  console.log(`shots in ${OUT_DIR}`);
} catch (error) {
  failed = true;
  console.error(error);
} finally {
  await browser.close();
}
if (pageErrors.length || consoleErrors.length) { console.error("page errors:", pageErrors, consoleErrors); failed = true; }
process.exit(failed ? 1 : 0);
