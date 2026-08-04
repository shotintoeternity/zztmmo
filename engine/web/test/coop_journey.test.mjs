// The co-op product cutline — three real browsers in one shared world.
//
// Driven by engine/coop_cutline_test.go, which hosts ACCEPT (fixtures/accept.zwd)
// and TOWN (fixtures/TOWN.ZZT) on the production zzt-server binary and runs this
// script against it. CUTLINE.md is the manual form of the same journey.
//
// WHY THIS SUITE EXISTS. Every other browser suite drives ONE player, or drives
// several through the editor. Nothing asserted that a small group can play a
// world together and see the same world while they do it — which is the whole
// product. The cutline is that claim, written down once and kept green: before
// another roadmap system is promoted, this journey still passes.
//
// THE CAST. Ada, Bo and Cy, in three separate Chromium instances rather than
// three tabs of one. Separate browsers give each player their own sessionStorage
// (so each has their own resume token and their own identity) and keep all three
// pages foreground — a background tab has its timers throttled, and the client
// samples held keys on a 55ms interval, so a backgrounded player would simply
// stop walking.
//
// THE FOUR CLAIMS, and where each is asserted:
//   1. one world, not three copies — a pickup one player takes is gone for the
//      others, and a door one player unlocks stays open for the group;
//   2. one authoritative result — clients in the same room report the SAME
//      server StateHash on the same tick (agreeOnWorld);
//   3. the group survives a reconnect — a reload resumes in place, with the same
//      player id, and leaves no ghost in anybody else's roster;
//   4. the group survives save/restore — a restore is refused while the world is
//      occupied, and once taken it rolls the shared world back for everyone.
//
// PLUS a leg in a shipped classic (TOWN), because a purpose-built fixture is not
// evidence that a real ZZT world is playable by a group: three players meet in
// Room One, agree on the world, and watch one of them leave through a board edge.
//
// FOUR THINGS THAT SILENTLY PRODUCE A "PASSING" TEST THAT NEVER PLAYS AT ALL —
// inherited from e2e_journey.test.mjs, and all four still bite here:
//  1. Input is SAMPLED, not latched: an instantaneous keyboard.press() is
//     usually gone before the next 55ms sample. Movement holds the key down.
//  2. The vendor Object at x=26 BLOCKS row 12 of ACCEPT's main board; the east
//     half is only reachable by walking around it.
//  3. Modal-opening events arrive BEFORE the client has drawn the modal, so
//     typing immediately after the event races it. settle() after each.
//  4. `go test` caches the Go driver and this .mjs is not a tracked dependency —
//     iterate with `-count=1` or you will read a stale pass.
//
// ON THE HASH COMPARISON. StateHash is a uint64 and JSON.parse would round it
// through a double, so the digits are pulled out of the raw frame text and
// compared as strings. Equal servers therefore compare equal exactly, and a
// difference in any bit shows.

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { chromium } from "playwright";

const baseURL = process.env.BASE_URL || "http://127.0.0.1:8080";
const resultsDir = path.resolve(process.env.COOP_OUT || "test-results/coop");
fs.mkdirSync(resultsDir, { recursive: true });

const SAVE_NAME = "COOPSAVE";

// ACCEPT's main board, row 12 (fixtures/accept.zwd, "Acceptance Main").
const ROW = 12;
const TORCH_X = 8;
const GEM_X = 10;
const AMMO_X = 14;
const KEY_X = 18;
const DOOR_X = 22;
const VENDOR_X = 26; // blocks row 12 (note 2)
const PASSAGE_X = 34;

const clients = [];

// --- one client --------------------------------------------------------------

async function openClient(label, name) {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1280, height: 720 } });
  const page = await context.newPage();
  const c = {
    label,
    name,
    browser,
    context,
    page,
    pageErrors: [],
    consoleErrors: [],
    sockets: [],
    titleRequests: [],
    // The status of every save/restore call the CLIENT made. A refused restore
    // is drawn as a "Not restored: ..." window and nothing else — no event, no
    // socket — so without this the script would press on and test the world it
    // was already in, which looks exactly like a restore that worked.
    apiCalls: [],
    transcript: [],
    snapshots: 0,
    diffs: 0,
    boardChanges: 0,
    closes: 0,
    you: null,
    hud: null,
    boardId: null,
    roster: [],
    events: [],
    // {tick, hash} in arrival order, trimmed: see agreeOnWorld for why the
    // window has to stay short.
    hashes: [],
  };
  clients.push(c);

  page.on("pageerror", (err) => c.pageErrors.push(String(err)));
  page.on("console", (msg) => {
    if (msg.type() === "error") c.consoleErrors.push(msg.text());
  });
  page.on("request", (request) => {
    const url = request.url();
    if (url.includes("/api/title")) c.titleRequests.push(url);
  });
  page.on("response", (response) => {
    const url = response.url();
    if (url.includes("/api/restore") || url.includes("/api/saves")) {
      c.apiCalls.push({ url: url.replace(baseURL, ""), status: response.status() });
    }
  });
  // The client's own view of the browser is a single canvas, so "is a window
  // still open over the title?" has no DOM answer. It has an HTTP one: see
  // ensureCleanTitle.
  page.on("websocket", (ws) => {
    c.sockets.push(ws.url());
    c.transcript.push({ dir: "open", url: ws.url() });
    ws.on("close", () => {
      c.closes++;
      c.transcript.push({ dir: "close" });
    });
    ws.on("framesent", (f) => c.transcript.push({ dir: "send", payload: String(f.payload).slice(0, 300) }));
    ws.on("framereceived", (frame) => {
      const raw = String(frame.payload);
      let msg;
      try {
        msg = JSON.parse(raw);
      } catch {
        return;
      }
      c.transcript.push({ dir: "recv", type: msg.type, payload: raw.slice(0, 300) });

      // saveResult/quit outcomes ride the bare EventMessage envelope.
      if (msg.type === "event" && msg.event) c.events.push(msg.event);

      const body = msg.type === "boardChange" ? msg.snapshot : msg;
      if (msg.type === "snapshot") c.snapshots++;
      if (msg.type === "boardChange") c.boardChanges++;
      if (msg.type === "diff") c.diffs++;
      if (body.hud) c.hud = body.hud;
      if (body.you) c.you = body.you;
      if (Array.isArray(body.players)) {
        c.roster = body.players;
        const me = body.players.find((p) => p.id === c.you?.id);
        if (me) c.you = me;
      }
      if (typeof body.boardId === "number") c.boardId = body.boardId;
      for (const ev of body.events || []) c.events.push(ev);

      // The exact hash digits, taken from the frame text rather than from the
      // parsed number (see the header). boardChange nests its snapshot, and the
      // first "hash" in the payload is that snapshot's — which is the one whose
      // tick `body.tick` names.
      const digits = /"hash":(\d+)/.exec(raw);
      if (digits && typeof body.tick === "number") {
        c.hashes.push({ tick: body.tick, hash: digits[1] });
        if (c.hashes.length > 200) c.hashes.shift();
      }
    });
  });

  const response = await page.goto(baseURL);
  assert.equal(response?.status(), 200, `${label}: the client index must be served, not the build-me 404 page`);
  await page.waitForLoadState("domcontentloaded");
  await page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await context.tracing.start({ screenshots: true, snapshots: true });
  return c;
}

// --- helpers -----------------------------------------------------------------

const sleep = (c, ms) => c.page.waitForTimeout(ms);
/** Give the client time to draw a modal the server has just announced (note 3). */
const settle = (c) => sleep(c, 700);
const eventTypes = (c) => [...new Set(c.events.map((e) => e.type))];
const has = (c, type, match) => c.events.some((e) => e.type === type && (!match || match(e)));
/**
 * The same, for the events a room broadcasts to EVERYONE in it even though they
 * belong to one player — quitPrompt, savePrompt, highScoreEntry all carry the
 * stat id of the player who raised them. Without this filter a second player's
 * prompt reads as your own, and the script would answer a dialog that is not on
 * its screen.
 *
 * ProtocolEvent.StatID is `omitempty`, so stat 0 — the first player to join a
 * board, who claims its existing player stat — arrives as no field at all.
 * Both sides are normalised, or the one player most likely to be driving the
 * dialog would never match it.
 */
const hasMine = (c, type) => has(c, type, (e) => (e.statId ?? 0) === (c.you?.statId ?? 0));
const at = (c) => `${c.label} board=${c.boardId} pos=(${c.you?.x},${c.you?.y}) hp=${c.you?.health}`;

async function waitFor(c, pred, describe, timeoutMs = 12000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    if (pred()) return;
    if (Date.now() > deadline) {
      throw new Error(
        `timed out waiting for ${describe}\n  last seen: ${at(c)} hud=${JSON.stringify(c.hud)}\n` +
          `  roster: ${JSON.stringify(c.roster.map((p) => p.id))}\n` +
          `  events: ${JSON.stringify(eventTypes(c))}`,
      );
    }
    await sleep(c, 60);
  }
}

/** Hold a key across at least one 55ms input sample (note 1). */
async function step(c, code, holdMs = 95) {
  await c.page.keyboard.down(code);
  await sleep(c, holdMs);
  await c.page.keyboard.up(code);
  await sleep(c, 70);
}

/**
 * Walk until done(); throws if the player stops making progress.
 *
 * `hold` is here so a run can FORCE the long hold that makes a step cover
 * several tiles at once: a 330ms hold moves exactly three tiles, deterministically
 * (see walkOnto). That is how M16.11a, M16.11b and M16.11d were each watched
 * failing before their fix was trusted, instead of waiting for load to supply the
 * overshoot. No caller needs it in the shipped form.
 */
async function walkUntil(c, code, done, describe, maxSteps = 40, hold = 95) {
  let stalled = 0;
  for (let i = 0; i < maxSteps; i++) {
    if (done()) return;
    const before = `${c.you?.x},${c.you?.y},${c.boardId}`;
    await step(c, code, hold);
    if (`${c.you?.x},${c.you?.y},${c.boardId}` === before) {
      if (++stalled >= 5) throw new Error(`${c.label} is stuck walking ${code} toward ${describe} at ${at(c)}`);
    } else {
      stalled = 0;
    }
  }
  if (!done()) throw new Error(`${c.label} never reached ${describe}; stopped at ${at(c)}`);
}

const atLeastX = (c, x) => () => (c.you?.x ?? 0) >= x;
const ids = (c) => c.roster.map((p) => p.id).sort();

/**
 * Walk onto one exact tile, recomputing the direction after every step.
 *
 * A step is NOT one tile. The client samples the held key every 55ms, but the
 * server latches that mask until the key-up sample arrives, so one hold spans
 * one server tick or two depending on load — one move or two. Along a row that
 * is harmless: an extra tile still passes THROUGH whatever was being collected,
 * which is why the eastward walks in this file can stay one-way inequalities.
 * Across rows it is not. A detour that assumes "one step up, then one step back
 * down" lands a row low under load, and every step taken afterwards along the
 * assumed row happens on the wrong one. Re-aiming after every step turns an
 * overshoot into a correction instead of a miss (M16.11b).
 *
 * Vertical first, then horizontal: the caller's target is reached by clearing
 * the starting row before travelling along the target's own row.
 *
 * `until` ends the walk on something other than arrival, because walking onto a
 * passage changes the board and the coordinates on the far side are not this
 * walk's coordinates.
 *
 * AND WHY THE HOLD GROWS AFTER AN OVERSHOOT. How many tiles a step covers is a
 * function of where the hold falls across the server's 110ms tick, so under
 * steady conditions it is a steady ANSWER, not a coin flip — a forced 330ms hold
 * moves exactly three tiles, every time, which is how this was measured. While
 * the step size k does not change, every square this walk stands on stays in one
 * residue class mod k, so a target in another class is not merely missed, it is
 * unreachable: k=3 from row 12 visits 12, 9, 12, 9 and never 11, which is what
 * this helper was watched doing until its steps ran out. So a step that did not
 * close the distance holds ~110ms longer next time — one more server tick with
 * the key latched, so a different k and a different class. Nothing changes for a
 * walk that is making progress: the hold only grows after a step that failed to.
 *
 * e2e_journey.test.mjs carries the same helper, added by M16.11a for the same
 * cause one row and one journey away. The two files are separate drivers with
 * separate observed state (this one is per-client, that one page-global), so
 * the shape is shared and the code is not.
 */
async function walkOnto(c, tx, ty, describe, { maxSteps = 24, hold = 95, until = null } = {}) {
  let stalled = 0;
  let extra = 0; // added to the hold after a step that did not close the distance
  for (let i = 0; i < maxSteps; i++) {
    if (until?.()) return;
    const { x, y } = c.you ?? {};
    if (x === tx && y === ty) return;
    const vertical = y !== ty;
    const was = vertical ? Math.abs(y - ty) : Math.abs(x - tx);
    const before = `${x},${y},${c.boardId}`;
    if (vertical) await step(c, y > ty ? "ArrowUp" : "ArrowDown", hold + extra);
    else await step(c, x > tx ? "ArrowLeft" : "ArrowRight", hold + extra);
    const now = vertical ? Math.abs((c.you?.y ?? y) - ty) : Math.abs((c.you?.x ?? x) - tx);
    extra = now > 0 && now >= was ? (extra + 110) % 330 : 0;
    if (`${c.you?.x},${c.you?.y},${c.boardId}` === before) {
      if (++stalled >= 5) {
        throw new Error(`${c.label} is stuck walking onto ${describe} (${tx},${ty}) at ${at(c)}`);
      }
    } else {
      stalled = 0;
    }
  }
  if (until?.()) return;
  throw new Error(`${c.label} never stood on ${describe} (${tx},${ty}); stopped at ${at(c)}`);
}

/**
 * Put a player on `row`.
 *
 * Joiners do NOT all land on the start square: the first one claims the board's
 * player stat, and everybody after it is placed by FindPlacement on whatever
 * square near it happens to be free — which is how Bo and Cy arrive on row 11
 * with the pickups they are here to test one row below them. A vertical step
 * that does not move is almost always another player standing there, so sidestep
 * once and try again rather than declaring the board impassable.
 */
async function alignToRow(c, row) {
  for (let attempt = 0; attempt < 10 && (c.you?.y ?? row) !== row; attempt++) {
    const before = `${c.you.x},${c.you.y}`;
    await step(c, c.you.y > row ? "ArrowUp" : "ArrowDown");
    if (`${c.you.x},${c.you.y}` === before) await step(c, "ArrowRight");
  }
  assert.equal(c.you.y, row, `${c.label} must be able to reach row ${row}, stuck at ${at(c)}`);
}

/**
 * The counters that must not move while a world is merely being selected —
 * the M16.11 title-screen contract, re-proved for each of the three players.
 */
const titleMark = (c) => ({ sockets: c.sockets.length, snapshots: c.snapshots, titles: c.titleRequests.length });

function expectTitleScreenPause(c, world, mark) {
  const joined = c.sockets.slice(mark.sockets);
  assert.equal(joined.length, 0, `${c.label}: selecting ${world} must stop at its title screen; opened ${JSON.stringify(joined)}`);
  assert.equal(c.snapshots, mark.snapshots, `${c.label}: selecting ${world} must not start play, but a snapshot arrived`);
  const titles = c.titleRequests.slice(mark.titles);
  assert.ok(
    titles.some((url) => url.includes(`world=${world}`)),
    `${c.label}: the title screen must be ${world}'s own board 0; /api/title since selection: ${JSON.stringify(titles)}`,
  );
}

/**
 * Leave the title screen with nothing standing over it.
 *
 * Quitting a run leaves a variable stack of windows behind — the high-score
 * table when the score qualified, a notice when it did not — and every one of
 * them swallows the next keystroke (main.ts handleKeyDown routes to the modal
 * before the title menu). A test that guesses how many Escapes to press either
 * leaves a window up, so the next R or W does nothing at all, or presses one
 * Escape too many, which the TITLE reads as "quit ZZT?" and answers with a
 * modal of its own (title.ts titleCommand).
 *
 * So ask instead of guessing. R at a clean title always calls /api/saves and R
 * swallowed by a window calls nothing, which makes one HTTP request the oracle
 * for a question the canvas cannot answer. The window R itself opens is then
 * closed, leaving a title screen whose state is known rather than assumed.
 *
 * /api/saves is the right probe precisely because nothing else asks for it: the
 * title screen polls /api/worlds every five seconds for its occupancy line
 * (M17.11), so a probe keyed on THAT would report success for a keystroke that
 * went nowhere.
 */
async function ensureCleanTitle(c) {
  for (let attempt = 0; attempt < 8; attempt++) {
    c.apiCalls.length = 0;
    await c.page.keyboard.press("KeyR");
    await sleep(c, 700);
    if (c.apiCalls.some((call) => call.url.startsWith("/api/saves"))) {
      await c.page.keyboard.press("Escape"); // close the window R opened
      await sleep(c, 400);
      return;
    }
    await c.page.keyboard.press("Escape"); // close the window that ate the R
    await sleep(c, 500);
  }
  throw new Error(`${c.label}: never reached a title screen with nothing over it`);
}

/**
 * Title screen -> world picker -> that world's title screen. Does NOT join:
 * pressing P is startPlay(c), so that a caller can select for all three and
 * then choose the join order.
 *
 * `name` means we are on a fresh page load, where the launch name prompt is
 * showing: submitting it opens the picker itself, so KeyW would only type a "w"
 * into the picker's search box.
 */
async function selectWorld(c, world, { name } = {}) {
  if (name) {
    await c.page.keyboard.type(name);
    await c.page.keyboard.press("Enter");
    await settle(c);
  } else {
    await c.page.keyboard.press("KeyW");
    await settle(c);
  }
  await c.page.keyboard.type(world);
  await sleep(c, 400);
  const mark = titleMark(c);
  await c.page.keyboard.press("Enter");
  await settle(c);
  expectTitleScreenPause(c, world, mark);
}

async function startPlay(c, world) {
  const before = c.snapshots;
  await c.page.keyboard.press("KeyP");
  await waitFor(c, () => c.snapshots > before, `${c.label}'s join snapshot for ${world}`, 20000);
  assert.ok(
    c.sockets.some((u) => u.includes(`world=${world}`)),
    `${c.label} must open a socket for ${world}, opened: ${JSON.stringify(c.sockets)}`,
  );
}

/** Leave the world the way a player does, ending back at the title with no socket. */
async function quitToTitle(c) {
  const closesBefore = c.closes;
  c.events.length = 0; // a prompt raised by whoever quit before must not read as this one
  await c.page.keyboard.press("KeyQ");
  await waitFor(c, () => hasMine(c, "quitPrompt"), `${c.label}'s own quit prompt`);
  await settle(c);
  await c.page.keyboard.press("KeyY");
  // A qualifying score opens name entry first; a zero score goes straight back.
  await waitFor(c, () => has(c, "highScoreEntry") || c.closes > closesBefore, `${c.label}'s quit outcome`);
  if (has(c, "highScoreEntry")) {
    await settle(c);
    await c.page.keyboard.type(c.label.slice(0, 3).toUpperCase());
    await sleep(c, 400);
    await c.page.keyboard.press("Enter");
    await settle(c);
  }
  for (let i = 0; i < 6 && c.closes === closesBefore; i++) {
    await c.page.keyboard.press("Escape");
    await sleep(c, 600);
  }
  await waitFor(c, () => c.closes > closesBefore, `${c.label}'s socket to close on returning to the title`);
  await ensureCleanTitle(c);
  c.events.length = 0; // the next run asks fresh questions
}

/**
 * CLAIM 2 — one authoritative result.
 *
 * Everyone in a room is served the same broadcast, so at a given tick their
 * StateHash must be identical: that is what "server-authoritative" means on the
 * wire. Clearing the window first and letting the room tick for a moment keeps
 * every sample inside one 420-tick wrap, so a tick number cannot mean two
 * different instants — and it makes the comparison about NOW rather than about
 * whatever the clients happened to be doing minutes ago.
 */
async function agreeOnWorld(group, describe) {
  for (const c of group) c.hashes.length = 0;
  await sleep(group[0], 1600); // ~14 server ticks at 110ms

  const board = group[0].boardId;
  for (const c of group) {
    assert.equal(c.boardId, board, `${c.label} is on board ${c.boardId}, not ${board} with the rest of the group (${describe})`);
  }

  let compared = 0;
  for (let i = 1; i < group.length; i++) {
    const mine = new Map(group[i].hashes.map((h) => [h.tick, h.hash]));
    for (const { tick, hash } of group[0].hashes) {
      const other = mine.get(tick);
      if (other === undefined) continue;
      compared++;
      assert.equal(
        hash,
        other,
        `${group[0].label} and ${group[i].label} disagree about the world at tick ${tick}: ` +
          `${hash} vs ${other} (${describe})`,
      );
    }
  }
  assert.ok(
    compared >= 5,
    `only ${compared} shared ticks to compare across ${group.length} players (${describe}) — ` +
      `the room may not be ticking, which would make the agreement vacuous`,
  );
  return compared;
}

/**
 * Walk east along row 12 of ACCEPT's main board and out through the passage at
 * (PASSAGE_X, ROW), rounding the vendor on the row above it (note 2).
 *
 * Every leg names the tile it ends on. This detour used to be a bare step up, an
 * eastward walk and a bare step down, which assumed a step is one tile: under
 * load the step down covered two, the eastward walk then travelled the row BELOW
 * the passage, and the down-step fallback that existed for a player left ABOVE
 * row 12 walked them further from it until its steps ran out — M16.11b, filed
 * from a run that ended eight rows past the passage and still walking away.
 * There is no fallback now, because there is no longer a row to guess at.
 */
async function crossMainBoard(c) {
  const boardBefore = c.boardId;
  const detour = ROW - 1; // empty for the board's whole width (fixtures/accept.zwd)
  await walkOnto(c, VENDOR_X - 1, detour, "the square above and west of the vendor");
  await walkOnto(c, VENDOR_X + 2, detour, "the square above and east of the vendor");
  await walkOnto(c, PASSAGE_X, ROW, "the passage", {
    maxSteps: 30,
    until: () => c.boardId !== boardBefore,
  });
  await waitFor(c, () => c.boardId !== boardBefore, `${c.label}'s board change through the passage`);
  return boardBefore;
}

async function dumpFailure(err) {
  console.error("CO-OP cutline journey FAILED:", err);
  for (const c of clients) {
    console.error(
      `--- ${c.label}: ${at(c)} hud=${JSON.stringify(c.hud)} roster=${JSON.stringify(ids(c))} ` +
        `events=${JSON.stringify(eventTypes(c))} pageErrors=${JSON.stringify(c.pageErrors)} ` +
        `consoleErrors=${JSON.stringify(c.consoleErrors)}`,
    );
    const stem = path.join(resultsDir, `coop_${c.label.toLowerCase()}`);
    try {
      fs.writeFileSync(`${stem}_transcript.json`, JSON.stringify({ sockets: c.sockets, transcript: c.transcript }, null, 2));
      await c.page.screenshot({ path: `${stem}_failure.png` });
      await c.context.tracing.stop({ path: `${stem}_trace.zip` });
    } catch (dumpErr) {
      console.error(`  (could not dump ${c.label}: ${dumpErr})`);
    }
  }
  console.error(`Saved per-player traces, screenshots and transcripts under ${resultsDir}`);
}

// --- the journey --------------------------------------------------------------

let ada, bo, cy, group;

try {
  console.log(`=== CO-OP CUTLINE: three players in one world, against ${baseURL} ===`);

  ada = await openClient("Ada", "Ada");
  bo = await openClient("Bo", "Bo");
  cy = await openClient("Cy", "Cy");
  group = [ada, bo, cy];

  // ==========================================================================
  // ACT 1 — the group gathers in one world
  // ==========================================================================
  for (const c of group) {
    await sleep(c, 800);
    await selectWorld(c, "ACCEPT", { name: c.name });
  }
  console.log("  - all three stopped at ACCEPT's title screen (nobody joined by selecting)");

  for (const c of group) await startPlay(c, "ACCEPT");

  for (const c of group) {
    await waitFor(c, () => c.roster.length === 3, `${c.label} to see all three players in the room`);
    assert.equal(c.boardId, 1, `${c.label} must start on board 1 (Acceptance Main), not ${c.boardId}`);
  }
  const cast = ids(ada);
  assert.equal(new Set(cast).size, 3, `the three players must have distinct ids, got ${JSON.stringify(cast)}`);
  for (const c of group) {
    assert.deepEqual(ids(c), cast, `${c.label} sees a different room roster (${JSON.stringify(ids(c))}) than Ada (${JSON.stringify(cast)})`);
    assert.ok(cast.includes(c.you.id), `${c.label} must see itself in the roster it is given`);
  }
  console.log(`  - three players, one room, one roster: ${JSON.stringify(cast)}`);

  console.log(`  - authoritative agreement on arrival: ${await agreeOnWorld(group, "on arrival")} shared ticks`);

  // ==========================================================================
  // ACT 2 — save the pristine world, so the restore has something to roll back to
  // ==========================================================================
  await ada.page.keyboard.press("KeyS");
  await waitFor(ada, () => hasMine(ada, "savePrompt"), "Ada's save prompt");
  await settle(ada);
  await ada.page.keyboard.type(SAVE_NAME);
  await sleep(ada, 300);
  await ada.page.keyboard.press("Enter");
  await waitFor(ada, () => has(ada, "saveResult"), "the save result");
  const saveResult = ada.events.find((e) => e.type === "saveResult");
  assert.ok(!saveResult.error, `the group's save must succeed, got ${JSON.stringify(saveResult.error)}`);
  assert.equal(saveResult.filename, SAVE_NAME, "the save must use the typed name");
  await ada.page.keyboard.press("Escape"); // dismiss the "Saving" window
  await settle(ada);
  console.log(`  - Ada saved the shared world as ${SAVE_NAME}.SAV while all three were in it`);

  // ==========================================================================
  // ACT 3 — one world, not three copies (claim 1)
  // ==========================================================================
  // Ada walks the row of pickups. Everything she takes is taken from the world.
  await alignToRow(ada, ROW);
  await walkUntil(ada, "ArrowRight", atLeastX(ada, TORCH_X), "the torch");
  await waitFor(ada, () => ada.hud.torches === 1, "Ada to collect the torch");
  await walkUntil(ada, "ArrowRight", atLeastX(ada, GEM_X), "the gem");
  await waitFor(ada, () => ada.hud.gems === 1, "Ada to collect the gem");
  await walkUntil(ada, "ArrowRight", atLeastX(ada, AMMO_X), "the ammo");
  await waitFor(ada, () => ada.hud.ammo === 5, "Ada to collect the ammo");
  await walkUntil(ada, "ArrowRight", atLeastX(ada, KEY_X), "the key");
  await waitFor(ada, () => ada.hud.keys.some(Boolean), "Ada to collect the key");
  console.log(`  - Ada collected torch, gem, ammo and key: ${JSON.stringify(ada.hud)}`);

  // The door at x=22 spends her key and stays open — for everybody. She then
  // walks on out of the way: a player is a solid tile, and three players queued
  // on one row would read as a wall to whoever is behind. Each of the three ends
  // this act on a square of their own.
  //
  // AND SHE LEAVES ROW 12 TO DO IT (M16.11d). A step is not one tile, and
  // `walkUntil` stops as soon as the player is SEEN at its target — so aiming an
  // eastward walk at row 12's x=25 means the step that arrives there may have two
  // tiles left in it, and the second one walks INTO the vendor at (26,12). The
  // vendor is a solid Object, so she does not move; she touches it, its scroll
  // opens, and from then on every arrow goes to the text window instead of to the
  // game — ACT 4 then reports Ada stuck at (25,12) with the vendor's window over
  // her screen. So this leg ends on the detour row instead, where nothing east of
  // her is touchable for the board's whole width, and `walkOnto` re-aims an
  // overshoot there into a correction. It is also exactly the tile ACT 4's
  // crossMainBoard aims at first, so nothing is walked twice.
  //
  // ACT 3's OTHER eastward walks are not this: they end beside PICKUPS (torch,
  // gem, ammo, key), which vanish the moment they are touched, so an overshoot
  // through one collects it and walks on — which is the whole point of the walk.
  // Only the vendor is permanently solid, and only this leg finishes next to it.
  // Bo's and Cy's walks stop at x>=24 and x>=23, two and three tiles short of it,
  // so reaching the vendor would take a step of three tiles or more from one
  // exact square rather than the two-tile step that suffices here.
  await walkUntil(ada, "ArrowRight", atLeastX(ada, DOOR_X + 1), "past the door");
  await waitFor(ada, () => !ada.hud.keys.some(Boolean), "the door to consume Ada's key");
  await walkOnto(ada, VENDOR_X - 1, ROW - 1, "the square above and west of the vendor");
  console.log("  - Ada unlocked the door, spent the key, and walked on");

  // Bo walks the same row. Every square Ada emptied is empty for him, and the
  // door she opened lets him through with no key at all.
  const boBefore = { gems: bo.hud.gems, torches: bo.hud.torches, ammo: bo.hud.ammo };
  assert.deepEqual(boBefore, { gems: 0, torches: 0, ammo: 0 }, "Bo starts with nothing");
  await alignToRow(bo, ROW);
  assert.ok(bo.you.x <= TORCH_X, `Bo must start west of the pickups to walk over them, at ${at(bo)}`);
  await walkUntil(bo, "ArrowRight", atLeastX(bo, DOOR_X + 2), "past the door Ada opened");
  assert.deepEqual(
    { gems: bo.hud.gems, torches: bo.hud.torches, ammo: bo.hud.ammo },
    boBefore,
    "the pickups Ada took must be gone from the world: Bo walked the same squares and got nothing",
  );
  assert.ok(!bo.hud.keys.some(Boolean), "Bo never held a key, yet passed the door Ada unlocked");
  console.log("  - Bo crossed the same row: no pickups left, and no key needed for Ada's door");

  await alignToRow(cy, ROW);
  await walkUntil(cy, "ArrowRight", atLeastX(cy, DOOR_X + 1), "past the door Ada opened");
  assert.ok(!cy.hud.keys.some(Boolean), "Cy never held a key either");
  console.log("  - Cy followed through the same open door");

  // ==========================================================================
  // ACT 4 — the group completes a multi-board segment together
  // ==========================================================================
  for (const c of group) {
    const from = await crossMainBoard(c);
    assert.ok(has(c, "transfer"), `${c.label} must receive a "transfer" event; saw ${JSON.stringify(eventTypes(c))}`);
    console.log(`  - ${c.label} took the passage: board ${from} -> ${c.boardId}`);
  }
  for (const c of group) {
    assert.equal(c.boardId, 2, `${c.label} must arrive on board 2 (Acceptance Target)`);
    await waitFor(c, () => c.roster.length === 3, `${c.label} to see the whole group re-form on board 2`);
  }
  assert.deepEqual(ids(cy), cast, "the group that arrived is the group that set out");
  console.log(`  - the whole group is on board 2: ${await agreeOnWorld(group, "after the board transfer")} shared ticks agree`);

  // ==========================================================================
  // ACT 5 — the group survives a reconnect (claim 3)
  // ==========================================================================
  await walkUntil(cy, "ArrowUp", () => (cy.you?.y ?? 99) <= 10, "a square Cy can be recognised by", 8);
  const cyBefore = { id: cy.you.id, x: cy.you.x, y: cy.you.y, board: cy.boardId };
  const cySockets = cy.sockets.length;

  await cy.page.reload(); // hard disconnect; sessionStorage keeps the resume token
  await cy.page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await sleep(cy, 900);
  await selectWorld(cy, "ACCEPT", { name: cy.name });
  await startPlay(cy, "ACCEPT");
  assert.ok(cy.sockets.length > cySockets, "the reload must open a new socket");
  assert.deepEqual(
    { id: cy.you.id, x: cy.you.x, y: cy.you.y, board: cy.boardId },
    cyBefore,
    `Cy must reclaim the same run in place, not spawn a fresh player (was ${JSON.stringify(cyBefore)})`,
  );
  console.log(`  - Cy reconnected in place at (${cy.you.x},${cy.you.y}) on board ${cy.boardId}, same id`);

  for (const c of group) {
    await waitFor(c, () => c.roster.length === 3, `${c.label} to see exactly three players after Cy's reconnect`);
    assert.deepEqual(
      ids(c),
      cast,
      `${c.label} sees ${JSON.stringify(ids(c))} after the reconnect: a resumed player must not leave a ghost behind`,
    );
  }
  console.log(`  - no ghost: all three rosters are still ${JSON.stringify(cast)}`);
  console.log(`  - authoritative agreement after the reconnect: ${await agreeOnWorld(group, "after Cy's reconnect")} shared ticks`);

  // ==========================================================================
  // ACT 6 — the group survives save/restore (claim 4)
  // ==========================================================================
  // A restore rewrites every board, so it is refused while anybody is still in
  // the world: that refusal is what stops one player wiping the room out from
  // under the others. Asked of the server directly — the client only offers
  // restore from the title screen, which is exactly where nobody is playing.
  const occupied = await fetch(`${baseURL}/api/restore`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ world: "ACCEPT", name: SAVE_NAME }),
  });
  assert.equal(occupied.status, 409, `a restore of an occupied world must be refused with 409, got ${occupied.status}`);
  console.log("  - restore refused (409) while the group is still playing");

  for (const c of group) await quitToTitle(c);
  console.log("  - all three quit to the title");

  // Ada restores from the client's own R flow — the path a player uses.
  ada.apiCalls.length = 0;
  await ada.page.keyboard.press("KeyR");
  await sleep(ada, 1500); // /api/saves, then the select list
  const saves = ada.apiCalls.filter((call) => call.url.startsWith("/api/saves")).pop();
  assert.ok(saves && saves.status === 200, `R must open the saved games list, got ${JSON.stringify(ada.apiCalls)}`);
  await ada.page.keyboard.press("Enter"); // the single saved game
  await sleep(ada, 2500); // /api/restore, then showTitle + the "Restore game" window
  const restore = ada.apiCalls.filter((call) => call.url.startsWith("/api/restore")).pop();
  assert.ok(restore, `the R flow must reach /api/restore; it called ${JSON.stringify(ada.apiCalls)}`);
  assert.equal(restore.status, 200, `the restore must be accepted now that the world is empty, got ${restore.status}`);
  await ada.page.keyboard.press("Escape");
  await settle(ada);
  await startPlay(ada, "ACCEPT");
  assert.equal(ada.boardId, 1, "the restored world rejoins on the board the save was taken from");
  assert.deepEqual(
    { x: ada.you.x, y: ada.you.y, health: ada.you.health, gems: ada.hud.gems, ammo: ada.hud.ammo },
    { x: 6, y: 12, health: 100, gems: 0, ammo: 0 },
    "per PARITY.md snapshot-player-drop, a joiner into a restored world starts fresh at the start square",
  );

  // The rollback proof, and it is the exact experiment Bo failed in act 3: the
  // torch and the gem Ada consumed are back in the world, on the same squares.
  await walkUntil(ada, "ArrowRight", atLeastX(ada, TORCH_X), "the restored torch");
  await waitFor(ada, () => ada.hud.torches === 1, "the torch the save still had");
  await walkUntil(ada, "ArrowRight", atLeastX(ada, GEM_X), "the restored gem");
  await waitFor(ada, () => ada.hud.gems === 1, "the gem the save still had");
  console.log("  - the restore rolled the shared world back: the torch and gem Ada spent are on their squares again");

  for (const c of [bo, cy]) await startPlay(c, "ACCEPT");
  for (const c of group) {
    await waitFor(c, () => c.roster.length === 3, `${c.label} to see the group re-form in the restored world`);
    assert.equal(c.boardId, 1, `${c.label} must rejoin the restored world on board 1`);
  }
  console.log(`  - the group re-formed in the restored world: ${await agreeOnWorld(group, "in the restored world")} shared ticks agree`);

  console.log("ACCEPT CUTLINE PASSED");

  // ==========================================================================
  // ACT 7 — the same group, in a shipped classic
  // ==========================================================================
  console.log("=== CO-OP CUTLINE: the TOWN leg ===");
  for (const c of group) await quitToTitle(c);
  for (const c of group) {
    await selectWorld(c, "TOWN");
    await startPlay(c, "TOWN");
  }
  for (const c of group) {
    await waitFor(c, () => c.roster.length === 3, `${c.label} to meet the others in TOWN`, 20000);
    assert.equal(c.you.health, 100, `${c.label} must start TOWN at full health`);
  }
  const townCast = ids(ada);
  for (const c of group) {
    assert.deepEqual(ids(c), townCast, `${c.label} sees a different TOWN roster than Ada`);
  }
  const townBoard = ada.boardId;
  console.log(`  - three players met on TOWN board ${townBoard}: ${JSON.stringify(townCast)}`);
  console.log(`  - authoritative agreement in TOWN: ${await agreeOnWorld(group, "in TOWN")} shared ticks`);

  // One player's movement is visible to the others as the server's account of
  // where she is, not as a guess each client draws for itself.
  const adaWas = { x: ada.you.x, y: ada.you.y };
  await walkUntil(ada, "ArrowDown", () => ada.you.x !== adaWas.x || ada.you.y !== adaWas.y, "any square Ada can reach", 8);
  for (const c of [bo, cy]) {
    await waitFor(
      c,
      () => {
        const seenAda = c.roster.find((p) => p.id === ada.you.id);
        return seenAda && seenAda.x === ada.you.x && seenAda.y === ada.you.y;
      },
      `${c.label} to be told where Ada actually is`,
    );
  }
  console.log(`  - Ada moved to (${ada.you.x},${ada.you.y}) and both other players were told so`);

  // She then leaves the room through a board edge: a real classic's geography,
  // and the moment the group is split across two rooms.
  let crossed = false;
  for (const dir of ["ArrowDown", "ArrowLeft", "ArrowRight", "ArrowUp"]) {
    try {
      await walkUntil(ada, dir, () => ada.boardId !== townBoard, `a board edge going ${dir}`, 14);
    } catch {
      // A wall or an unwalkable edge in that direction: try the next one.
    }
    if (ada.boardId !== townBoard) {
      crossed = true;
      console.log(`  - Ada left Room One going ${dir}: board ${townBoard} -> ${ada.boardId}`);
      break;
    }
  }
  assert.ok(crossed, `Ada must be able to reach another board of TOWN from board ${townBoard}, stopped at ${at(ada)}`);

  await waitFor(ada, () => ada.roster.length === 1, "Ada to be alone in the board she walked into");
  for (const c of [bo, cy]) {
    await waitFor(c, () => c.roster.length === 2, `${c.label} to see Ada leave the room`);
    assert.ok(!ids(c).includes(ada.you.id), `${c.label} must no longer list Ada, who is on another board`);
  }
  console.log(`  - the room split correctly: ${await agreeOnWorld([bo, cy], "for the two left behind")} shared ticks agree`);

  console.log("TOWN LEG PASSED");

  // --------------------------------------------------------------------------
  for (const c of group) {
    assert.deepEqual(c.pageErrors, [], `${c.label} must not raise page errors`);
    assert.deepEqual(c.consoleErrors, [], `${c.label} must not log console errors`);
  }

  console.log("CO-OP CUTLINE PASSED — three players, one world, one authoritative result");
  for (const c of clients) {
    await c.context.tracing.stop();
    await c.browser.close();
  }
  process.exit(0);
} catch (err) {
  await dumpFailure(err);
  for (const c of clients) {
    try {
      await c.browser.close();
    } catch {
      // already gone
    }
  }
  process.exit(1);
}
