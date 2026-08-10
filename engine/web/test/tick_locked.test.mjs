// M16.9 — the tick-locked acceptance run.
//
// This is M16.11's carried-over DoD clause (NOTES.md M18.0b): "the
// acceptance-world run is deterministic and catches a client/server tick-order
// change". M16.11's journey could not be, because a browser driving real keys
// against a 110ms wall-clock ticker lands its inputs on whichever tick happens
// to be next — the behaviour was right, the StateHash was different every run.
//
// Here nothing is injected: every input below is a real keystroke in a real
// browser, delivered by the client's own sampler over the real socket. What
// changed is that the SERVER only takes a tick once the frame the browser sent
// for that tick has arrived (walk()/command() in lib/canvas.mjs, /control/step's
// `await` in engine/m16_9_test.go), and the page's clock is a fake one, so the
// 55ms sampler fires only when this script advances it. One browser frame, one
// tick — and therefore one StateHash, run after run.
//
// The route's per-room hashes are committed in
// fixtures/browser-goldens/tick-locked-run.json. Change how the client encodes a
// held arrow, or when the server applies input relative to stepping, and the
// checkpoint hashes move. Re-record only with TICK_RUN_UPDATE=1, and say why in
// the commit message.

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

import {
  baseURL,
  command,
  gridToArt,
  hasText,
  idle,
  installDecoder,
  installImageProbe,
  launchGoldenBrowser,
  markProfileWarm,
  pauseClock,
  readGrid,
  runClock,
  resultsDir,
  saveText,
  serverState,
  tickUntilGrid,
  waitForGrid,
  waitForQuiet,
  walk,
} from "./lib/canvas.mjs";

const { browser, context, page, pageErrors, consoleErrors } = await launchGoldenBrowser();
await markProfileWarm(context);
const checkpoints = [];
// Retained and dumped on failure: when a tick-locked run desynchronises, the
// question is always "what did the browser actually put on the wire", and this
// is the only place that answer exists.
const transcript = [];
page.on("websocket", (ws) => {
  transcript.push({ dir: "open", url: ws.url() });
  ws.on("framesent", (f) => transcript.push({ dir: "send", payload: String(f.payload).slice(0, 200) }));
  ws.on("close", () => transcript.push({ dir: "close" }));
});
let failed = false;

async function checkpoint(label) {
  const state = await serverState();
  checkpoints.push({ label, ticks: state.ticks, hashes: state.hashes });
  console.log(`  · ${label}: tick ${state.ticks}, hashes ${JSON.stringify(state.hashes)}`);
  return state;
}

try {
  await installImageProbe(page);
  await context.tracing.start({ screenshots: true, snapshots: true });

  const response = await page.goto(baseURL);
  assert.equal(response?.status(), 200, "the client index must be served, not the build-me 404 page");
  await page.waitForSelector("canvas[data-screen]", { timeout: 15000 });
  await installDecoder(page);

  // Through the production launch flow, exactly as a player arrives.
  await waitForGrid(page, (cells) => hasText(cells, "Type your name"), "the launch name prompt");
  await page.keyboard.type("Ticklock");
  await page.keyboard.press("Enter");
  await waitForGrid(page, (cells) => hasText(cells, "Choose a World"), "the world picker");
  await page.keyboard.type("GOLDEN");
  await waitForGrid(page, (cells) => hasText(cells, "GOLDEN"), "the picker to match GOLDEN");
  await page.keyboard.press("Enter");
  await waitForGrid(page, (cells) => hasText(cells, "P  Play"), "the title screen");

  // Freeze the page clock: from here the client's 55ms input sampler fires only
  // when this script advances it, which is what makes one frame per tick
  // possible at all.
  await pauseClock(page);
  await page.keyboard.press("KeyP");
  await waitForGrid(page, (cells) => hasText(cells, "Health:100"), "the joined board");
  await checkpoint("joined");

  await idle(2);
  await waitForQuiet(page);
  await checkpoint("settled");

  // The showcase board's pickup row: torch at tile 9, gem at 12, energizer at 15.
  await walk(page, "ArrowRight", 3);
  await waitForGrid(page, (cells) => hasText(cells, "Torches:1"), "the torch");
  await checkpoint("torch");

  await walk(page, "ArrowRight", 3);
  await waitForGrid(page, (cells) => hasText(cells, "Gems:1"), "the gem");
  await checkpoint("gem");

  await walk(page, "ArrowRight", 3);
  await idle(3);
  await waitForQuiet(page);
  await checkpoint("energised");

  // Up out of the pickup row, east past the vendor's column, then down onto the
  // passage at tile (24,20). The energizer left the player at tile 15.
  await walk(page, "ArrowUp", 1);
  await walk(page, "ArrowRight", 9);
  const before = await serverState();
  await walk(page, "ArrowDown", 1);
  const after = await serverState();
  assert.notEqual(after.players[0].boardId, before.players[0].boardId, "the passage must transfer the player");
  await checkpoint("transferred");

  // A boardChange makes the client drop whatever is held (main.ts applyMessage
  // calls stopHeldInput), so the next keystroke must not be pressed until that
  // message has actually been applied — otherwise the keydown is cancelled by a
  // zero frame arriving behind it and the tick lock desynchronises. Run the fade
  // out on the fake clock and wait for the dark board to be on screen: that hatch
  // can only appear once the new board's snapshot has been drawn.
  await runClock(page, 2000);
  await waitForGrid(
    page,
    (cells) => cells.filter((cell) => cell.ch === 0xb0).length > 100,
    "the dark board to finish fading in",
  );

  // The dark board: collect its torch and light it.
  await idle(2);
  await walk(page, "ArrowRight", 3);
  await waitForGrid(page, (cells) => hasText(cells, "Torches:1"), "the dark board's torch");
  await command(page, "KeyT", "T".charCodeAt(0));
  await tickUntilGrid(page, (cells) => hasText(cells, "Torches:0"), "the torch to be spent");
  await idle(5);
  await waitForQuiet(page);
  const final = await checkpoint("torch-lit");

  assert.deepEqual(pageErrors, [], "the client must not raise page errors");
  assert.deepEqual(consoleErrors, [], "the client must not log console errors");

  fs.mkdirSync(resultsDir, { recursive: true });
  fs.writeFileSync(
    path.join(resultsDir, "tick-locked-run.json"),
    JSON.stringify({ ticks: final.ticks, checkpoints }, null, 2) + "\n",
  );
  console.log(`TICK-LOCKED RUN COMPLETE — ${final.ticks} ticks, ${checkpoints.length} checkpoints`);
} catch (err) {
  failed = true;
  console.error("tick-locked run FAILED:", err);
  console.error("checkpoints so far:", JSON.stringify(checkpoints, null, 2));
  saveText("tick-locked-transcript.json", JSON.stringify(transcript, null, 2));
  console.error("last 25 frames the browser sent:", JSON.stringify(transcript.slice(-25), null, 2));
  try {
    saveText("tick-locked-failure.txt", gridToArt(await readGrid(page)));
    console.error("screen at failure:\n" + gridToArt(await readGrid(page)));
  } catch (readErr) {
    console.error("could not read the canvas at failure:", readErr);
  }
  await page.screenshot({ path: path.join(resultsDir, "tick-locked-failure.png") }).catch(() => {});
} finally {
  await context.tracing.stop({ path: path.join(resultsDir, "tick-locked-trace.zip") }).catch(() => {});
  await browser.close();
  process.exit(failed ? 1 : 0);
}
