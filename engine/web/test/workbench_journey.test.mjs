// M38.6 — the TAS workbench in a real browser.
//
// The DoD's journey: a signed-in player makes a live run, opens the workbench
// from the challenge screen, imports that run, changes one tick's input, sees
// the board change at that tick, submits, and sees the assisted row.
//
// What makes it a claim rather than a screenshot: every tick the browser
// shows is the one the server's status names (the workbench socket is
// watched), the export is a real download parsed back, the edit is checked
// as a CELL that moved, and the assisted rank is the server's own answer.

import assert from "node:assert/strict";
import fs from "node:fs";
import { chromium } from "playwright";
import { gridToArt, hasText, installDecoder, installImageProbe, markProfileWarm, pickListRow, readGrid } from "./lib/canvas.mjs";

const baseURL = process.env.BASE_URL || "http://127.0.0.1:8080";
const authCookie = process.env.ZZT_AUTH_COOKIE || "";
const PLAYER_CH = 0x02;

async function waitFor(pred, describe, timeoutMs = 30000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const value = await pred();
    if (value) return value;
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${describe}`);
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
}

async function waitForPage(c, pred, describe, timeoutMs = 30000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const cells = await readGrid(c.page);
    if (pred(cells)) return cells;
    if (Date.now() > deadline) throw new Error(`${c.label}: timed out waiting for ${describe}\n${gridToArt(cells)}`);
    await c.page.waitForTimeout(60);
  }
}

// playerColumn is where the one player is drawn on the board, or -1.
function playerColumn(cells) {
  const at = cells.findIndex((cell, i) => cell.ch === PLAYER_CH && i % 80 < 60);
  return at < 0 ? -1 : at % 80;
}

async function openClient(label, path) {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1280, height: 720 }, acceptDownloads: true });
  await markProfileWarm(context);
  const [name, ...rest] = authCookie.split("=");
  await context.addCookies([{ name, value: rest.join("="), url: baseURL, httpOnly: true, sameSite: "Lax" }]);
  const page = await context.newPage();
  const c = {
    label, browser, page,
    pageErrors: [], consoleErrors: [], httpErrors: [], sockets: [],
    challengeResults: [], statuses: [], submits: [], benchErrors: [], runSnapshots: 0,
  };
  page.on("pageerror", (err) => c.pageErrors.push(String(err)));
  page.on("console", (msg) => {
    if (msg.type() === "error") c.consoleErrors.push(msg.text());
  });
  page.on("response", (response) => {
    if (response.status() >= 500) c.httpErrors.push({ url: response.url().replace(baseURL, ""), status: response.status() });
  });
  page.on("websocket", (ws) => {
    // Counted once it sends its first frame: the client opens its play socket
    // while the title screen shows (M42.8), and a socket that has sent nothing
    // has joined nothing.
    let counted = false;
    ws.on("framesent", () => {
      if (!counted) {
        counted = true;
        c.sockets.push(ws.url());
      }
    });
    ws.on("framereceived", (frame) => {
      let msg;
      try {
        msg = JSON.parse(String(frame.payload));
      } catch {
        return;
      }
      if (msg.type === "challengeResult") c.challengeResults.push(msg);
      if (msg.type === "snapshot" && msg.challenge === "gem-dash" && msg.you?.id) c.runSnapshots++;
      if (msg.type === "workbenchStatus") c.statuses.push(msg);
      if (msg.type === "workbenchSubmitResult") c.submits.push(msg);
      if (msg.type === "workbenchError") c.benchErrors.push(msg);
    });
  });
  await installImageProbe(page);
  const response = await page.goto(`${baseURL}${path}`, { waitUntil: "domcontentloaded" });
  assert.equal(response?.status(), 200, `${label}: ${path} must serve the client`);
  await page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await installDecoder(page);
  return c;
}

const last = (c) => c.statuses[c.statuses.length - 1];

// press sends one key and waits for the server to land on the tick it should,
// then for the canvas to show the status it was sent.
async function press(c, key, wantTick, describe) {
  await c.page.keyboard.press(key);
  await waitFor(() => last(c) && last(c).tick === wantTick, `${describe}: the server at tick ${wantTick} (at ${last(c)?.tick})`);
  const st = last(c);
  await waitForPage(c, (cells) => hasText(cells, `Tick ${st.tick}/${st.length}`), `${describe}: the timeline at ${st.tick}/${st.length}`);
  return st;
}

const clients = [];

try {
  assert.ok(authCookie, "this journey needs a signed-in browser: ZZT_AUTH_COOKIE must be set");
  const ada = await openClient("Ada", "/challenge");
  clients.push(ada);

  // 1. A live run, played: the run the workbench will import.
  await waitForPage(ada, (cells) => hasText(cells, "Daily Challenge") && hasText(cells, "Start a run"), "the challenge landing");
  await pickListRow(ada.page, "Start a run", "the landing's start");
  await waitFor(() => ada.sockets.some((url) => url.includes("challenge=gem-dash")), "the run's socket");
  // The key goes down only once the run's join snapshot has landed: a press
  // made before the client is in play mode is never seen again, and holding a
  // key sends no second keydown (the load-sensitive start M32.1 waits out too).
  await waitFor(() => ada.runSnapshots > 0, "the run's join snapshot");
  await waitForPage(ada, (cells) => hasText(cells, "Health"), "the play sidebar");
  await ada.page.keyboard.down("ArrowRight");
  try {
    await waitFor(() => ada.challengeResults.length > 0, "the live run to finish", 40000);
  } finally {
    await ada.page.keyboard.up("ArrowRight");
  }
  const live = ada.challengeResults[0];
  assert.equal(live.durable, true, "the live run posts a time");
  console.log(`  - a live run finished in ${live.ticks} ticks (${live.recordingId})`);

  // 2. The workbench, from the challenge screen.
  await ada.page.goto(`${baseURL}/challenge`, { waitUntil: "domcontentloaded" });
  await ada.page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await installDecoder(ada.page);
  await waitForPage(ada, (cells) => hasText(cells, "Daily Challenge") && hasText(cells, "Start a run"), "the landing again");
  await pickListRow(ada.page, "TAS workbench", "the landing's workbench row");
  await waitFor(() => ada.sockets.some((url) => url.includes("workbench=gem-dash")), "the workbench socket");
  await waitFor(() => last(ada), "the first workbench status");
  await waitForPage(ada, (cells) => hasText(cells, "TAS Workbench") && hasText(cells, "Tick 0/0"), "the empty workbench");
  console.log("  - the landing opens the workbench on its own socket");

  // 3. Import my run.
  await ada.page.keyboard.press("Escape");
  await waitForPage(ada, (cells) => hasText(cells, "Import my run"), "the workbench menu");
  await pickListRow(ada.page, "Import my run", "the menu's import");
  await waitFor(() => last(ada).length > 0, "the imported script");
  const imported = last(ada);
  assert.equal(imported.tick, 0, "an import opens at tick 0");
  assert.equal(imported.length, live.ticks, "the imported script is the live run, tick for tick");
  await waitForPage(ada, (cells) => hasText(cells, `Tick 0/${imported.length}`), "the timeline after the import");
  console.log(`  - imported the live run: ${imported.length} ticks`);

  // 4. Export, as a file the browser downloads.
  const downloadPromise = ada.page.waitForEvent("download");
  await ada.page.keyboard.press("Escape");
  await waitForPage(ada, (cells) => hasText(cells, "Export the script"), "the menu again");
  await pickListRow(ada.page, "Export the script", "the menu's export");
  const download = await downloadPromise;
  const script = JSON.parse(fs.readFileSync(await download.path(), "utf8"));
  assert.equal(script.challenge, "gem-dash");
  assert.equal(script.ticks.length, imported.length, "the export is the script the server holds");
  assert.match(download.suggestedFilename(), /^gem-dash-v1-tas\.json$/);
  console.log(`  - exported ${download.suggestedFilename()}`);

  // 5. Change one tick's input and see the board change at that tick. The
  //    tick changed is one the live run moved east on, so a wait there is a
  //    different square whatever the run did either side of it.
  const k = script.ticks.findIndex((tick) => tick.dx === 1);
  assert.ok(k >= 0, "the live run moved east on some tick");
  for (let i = 1; i <= k; i += 1) {
    await press(ada, "Period", i, `stepping to tick ${k}`);
  }
  const before = playerColumn(await readGrid(ada.page));
  await press(ada, "Period", k + 1, "the live tick");
  const moved = playerColumn(await readGrid(ada.page));
  assert.equal(moved, before + 1, `the live run's tick ${k} moves the player east`);
  await press(ada, "Comma", k, "back a tick");
  await waitForPage(ada, (cells) => playerColumn(cells) === before, "the board back at tick " + k);
  await press(ada, "KeyW", k + 1, "the edited tick");
  const edited = await waitForPage(ada, (cells) => playerColumn(cells) === before, "the player to stay put at the edited tick");
  assert.notEqual(playerColumn(edited), moved, "the edit changed the board at that tick");
  assert.equal(last(ada).length, imported.length, "an edit in place keeps the script's length");
  console.log(`  - tick ${k} changed from east to wait: the player stays at column ${before}`);

  // 6. The run is one step short now. Walk to the end, then extend it with the
  //    play keys until the server says the goal is met.
  for (let tick = k + 2; tick <= imported.length; tick += 1) {
    await press(ada, "Period", tick, "walking to the end");
  }
  assert.equal(last(ada).met, false, "a wait in place of a step misses the goal by the end");
  for (let extra = 1; !last(ada).met; extra += 1) {
    assert.ok(extra <= 10, "ten appended steps east must meet the goal");
    await press(ada, "ArrowRight", imported.length + extra, "appending a step east");
  }
  const met = last(ada);
  assert.equal(met.length, met.tick, "appended ticks extend the script");
  await waitForPage(ada, (cells) => hasText(cells, `Met: ${met.ticks} ticks`), "the met goal on the sidebar");
  console.log(`  - extended to ${met.length} ticks; goal met at ${met.ticks}`);

  // 7. Submit, and see the assisted row.
  await ada.page.keyboard.press("Escape");
  await waitForPage(ada, (cells) => hasText(cells, "Submit"), "the menu for submit");
  await pickListRow(ada.page, "Submit", "the menu's submit");
  await waitFor(() => ada.submits.length > 0, "the judged answer");
  const submitted = ada.submits[0];
  assert.equal(submitted.status, 200, `the submission is judged and met: ${JSON.stringify(submitted)}`);
  assert.equal(submitted.result.durable, true, "a signed-in submission is a row");
  assert.equal(submitted.result.rank, 1, "the first assisted time ranks first");
  assert.equal(submitted.result.ticks, met.ticks, "the judge measures what the workbench showed");
  await waitForPage(ada, (cells) => hasText(cells, "Assisted Run") && hasText(cells, "Assisted rank 1") && hasText(cells, `Judged: ${met.ticks} ticks`), "the assisted row");
  console.log(`  - submitted: assisted rank ${submitted.result.rank} in ${submitted.result.ticks} ticks`);

  // 8. M38.7: the assisted board in the open. From the landing, the assisted
  //    table under the live one, its row, and that row's replay, which says
  //    it is assisted.
  await ada.page.goto(`${baseURL}/challenge`, { waitUntil: "domcontentloaded" });
  await ada.page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await installDecoder(ada.page);
  await waitForPage(ada, (cells) => hasText(cells, "Daily Challenge") && hasText(cells, "Leaderboard"), "the landing after the submission");
  await pickListRow(ada.page, "Leaderboard", "the landing's leaderboard");
  await waitForPage(ada, (cells) => hasText(cells, "Challenge Times"), "the times");
  const assistedTicks = String(submitted.result.ticks);
  const picked = await pickListRow(
    ada.page,
    (line) => line.trim().split(/\s+/)[2] === assistedTicks && line.includes("ada"),
    "the assisted row",
  );
  await waitForPage(ada, (cells) => hasText(cells, "Assisted Run") && hasText(cells, `${assistedTicks} ticks, assisted`) && hasText(cells, "Race this assisted ghost"), "the assisted row's actions");
  console.log(`  - the landing's times list the assisted row: ${picked.trim()}`);
  const socketsBeforeReplay = ada.sockets.length;
  await pickListRow(ada.page, "Watch the replay", "the assisted row's replay");
  await waitFor(() => ada.sockets.slice(socketsBeforeReplay).some((url) => url.includes(`replay=${submitted.result.recordingId}`)), "the assisted replay's socket");
  await waitFor(() => ada.page.url().includes(`/replay/${submitted.result.recordingId}`), "the assisted replay's address");
  await waitForPage(ada, (cells) => hasText(cells, "Assisted run") && hasText(cells, "Watching"), "the replay saying it is assisted");
  console.log(`  - the assisted row opens its replay, which says "Assisted run"`);

  assert.deepEqual(ada.benchErrors, [], "no workbench op was refused");
  for (const c of clients) {
    assert.equal(c.pageErrors.length, 0, `${c.label} page errors: ${c.pageErrors.join("\n")}`);
    assert.deepEqual(c.httpErrors, [], `${c.label} saw no server errors`);
    assert.equal(c.consoleErrors.length, 0, `${c.label} console errors: ${c.consoleErrors.join("\n")}`);
  }
  console.log(`ASSISTED_TICKS=${submitted.result.ticks}`);
  console.log(`LIVE_TICKS=${live.ticks}`);
  console.log("workbench browser journey passed");
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
