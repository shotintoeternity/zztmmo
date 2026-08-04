// M21.1 — one player stops hearing another, in two real browsers.
//
// Driven by engine/m21_1_browser_test.go, which hosts ACCEPT on the production
// zzt-server binary. Two separate Chromium instances rather than two tabs, for
// player_color.test.mjs's reason: separate storage per player, and no throttled
// timers in a background tab.
//
// WHAT THIS SUITE IS FOR, given that m21_1_test.go already proves the block at
// the socket. It proves the half a socket test cannot: that a player can actually
// DO it — that 'L' opens the list, that the person who just spoke is on it, that
// the confirmation says whether the block will last, and that the blocked lines
// then stop appearing in the chat window. A server that filtered perfectly behind
// an unreachable window would pass every Go test in the tree.
//
// HOW THE NEGATIVE IS PROVED, since "the line never arrives" cannot be waited
// for: Bo's blocked line is sent first and read back on Bo's OWN screen (so it
// was broadcast), and only then does Ada say something that nobody blocks. Each
// client's socket is FIFO, so Ada's chat window showing her own line without Bo's
// is proof Bo's was dropped — not proof that we did not wait long enough.

import assert from "node:assert/strict";
import { chromium } from "playwright";
import { findText, gridToArt, hasText, installDecoder, installImageProbe, readGrid, textAt } from "./lib/canvas.mjs";

const baseURL = process.env.BASE_URL || "http://127.0.0.1:8080";
const WORLD = "ACCEPT";
const BLOCKED_LINE = "you cannot silence me";
const FENCE_LINE = "still here myself";
const AFTER_LINE = "hello again";

const clients = [];

async function openClient(label, name) {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1280, height: 720 } });
  const page = await context.newPage();
  const c = { label, name, browser, context, page, pageErrors: [], snapshots: 0, you: null, chat: [] };
  clients.push(c);
  page.on("pageerror", (err) => c.pageErrors.push(String(err)));
  page.on("websocket", (ws) => {
    ws.on("framereceived", (frame) => {
      let msg;
      try {
        msg = JSON.parse(String(frame.payload));
      } catch {
        return;
      }
      if (msg.type === "snapshot") c.snapshots += 1;
      if (msg.type === "chat") c.chat.push(msg);
      const body = msg.type === "boardChange" ? msg.snapshot : msg;
      if (body.you) c.you = body.you;
      if (Array.isArray(body.players)) {
        const me = body.players.find((p) => p.id === c.you?.id);
        if (me) c.you = me;
      }
    });
  });

  await installImageProbe(page);
  const response = await page.goto(baseURL, { waitUntil: "domcontentloaded" });
  assert.equal(response?.status(), 200, `${label}: the client index must be served`);
  await page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await installDecoder(page);
  return c;
}

const sleep = (c, ms) => c.page.waitForTimeout(ms);
const settle = (c) => sleep(c, 700);

async function screen(c, pred, describe, timeoutMs = 20000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const cells = await readGrid(c.page);
    if (pred(cells)) return cells;
    if (Date.now() > deadline) {
      throw new Error(`timed out waiting for ${describe} (${c.label}); the screen was:\n${gridToArt(cells)}`);
    }
    await sleep(c, 100);
  }
}

async function waitFor(c, pred, describe, timeoutMs = 20000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    if (pred()) return;
    if (Date.now() > deadline) {
      throw new Error(`timed out waiting for ${describe} (${c.label}); chat=${JSON.stringify(c.chat)}`);
    }
    await sleep(c, 80);
  }
}

/** Title screen -> picker -> that world's title screen -> play. */
async function joinWorld(c) {
  await screen(c, (cells) => hasText(cells, "Type your name"), "the launch name prompt");
  await c.page.keyboard.type(c.name);
  await c.page.keyboard.press("Enter");
  await screen(c, (cells) => hasText(cells, "Choose a World"), "the world picker");
  await c.page.keyboard.type(WORLD);
  await sleep(c, 400);
  await c.page.keyboard.press("Enter");
  await settle(c);
  const before = c.snapshots;
  await c.page.keyboard.press("KeyP");
  await waitFor(c, () => c.snapshots > before, `${c.label}'s join snapshot`);
  await settle(c);
}

/**
 * Say something in the chat window. Enter submits AND closes (modal.ts chatKey),
 * so there is nothing to dismiss afterwards — an extra Escape here would reach
 * PLAY mode and open the quit prompt, which then swallows every later key.
 */
async function say(c, text) {
  await c.page.keyboard.press("KeyC");
  await screen(c, (cells) => hasText(cells, "Global Chat"), "the chat window");
  await c.page.keyboard.type(text, { delay: 8 });
  await c.page.keyboard.press("Enter");
  await screen(c, (cells) => !hasText(cells, "Global Chat"), "the chat window to close on Enter");
}

/**
 * The Players WINDOW, told apart from the sidebar row that advertises it (M21.3).
 * Both carry the word, and columns 0-59 are the board — every CP437 window is
 * drawn inside x=5..55 — so a whole-screen search would find the advertisement
 * and report the window open before a key was ever pressed.
 */
const blockWindowIsOpen = (cells) => {
  for (let row = 0; row < 25; row += 1) {
    if (textAt(cells, 0, row, 60).includes("Players")) return true;
  }
  return false;
};

/** The chat window's own contents, as one string. */
async function chatWindowText(c) {
  await c.page.keyboard.press("KeyC");
  const cells = await screen(c, (grid) => hasText(grid, "Global Chat"), "the chat window");
  let text = "";
  for (let row = 0; row < 25; row += 1) {
    text += textAt(cells, 0, row, 60) + "\n";
  }
  await c.page.keyboard.press("Escape");
  await screen(c, (grid) => !hasText(grid, "Global Chat"), "the chat window to close");
  return text;
}

let failed = false;
try {
  const ada = await openClient("Ada", "Ada");
  const bo = await openClient("Bo", "Bo");
  await joinWorld(ada);
  await joinWorld(bo);
  assert.ok(ada.you && bo.you, "both players must be in the room");
  assert.notEqual(ada.you.id, bo.you.id, "two players, two ids");

  // -------------------------------------------------------------------------
  // 1. Bo says something, and Ada hears it. The line is addressable.
  // -------------------------------------------------------------------------
  await say(bo, "nice world");
  await waitFor(ada, () => ada.chat.some((m) => m.text === "nice world"), "Bo's first line reaching Ada");
  const heard = ada.chat.find((m) => m.text === "nice world");
  assert.equal(heard.playerId, bo.you.id, `the line must carry Bo's id, got ${JSON.stringify(heard)}`);
  console.log("  - Bo's line reached Ada, carrying Bo's player id");

  // -------------------------------------------------------------------------
  // 2. The sidebar says the window is there, and says which key opens it (M21.3
  //    — before this, 'L' worked and nothing on screen mentioned it). The key
  //    pressed below is READ OFF the sidebar rather than typed in here: a row
  //    advertising a letter that does not open the window is the same failure as
  //    no row at all, and only deriving it can catch that.
  // -------------------------------------------------------------------------
  const playing = await readGrid(ada.page);
  const advert = findText(playing, " Players");
  assert.ok(
    advert && advert.x >= 60,
    `the play sidebar must advertise the Players window:\n${gridToArt(playing)}`,
  );
  const chip = textAt(playing, advert.x - 3, advert.y, 3);
  assert.match(chip, /^ [A-Z] $/, `the advertised row must name a key, got ${JSON.stringify(chip)}`);
  console.log(`  - the sidebar offers "${chip} ${" Players".trim()}" on row ${advert.y}`);

  // -------------------------------------------------------------------------
  //    That key lists the people Ada could stop hearing, and Bo is on it.
  // -------------------------------------------------------------------------
  await ada.page.keyboard.press(`Key${chip.trim()}`);
  const list = await screen(ada, blockWindowIsOpen, "the Players window");
  assert.ok(
    hasText(list, `Bo #${bo.you.id}`),
    `the window must offer Bo by name AND id:\n${gridToArt(list)}`,
  );
  assert.ok(
    hasText(list, "YOU hear"),
    `the window must say a block only changes what you hear:\n${gridToArt(list)}`,
  );
  console.log(`  - L listed "Bo #${bo.you.id}"`);

  // Take the row, confirm, and read what the server said back.
  await ada.page.keyboard.press("Enter");
  const confirm = await screen(ada, (cells) => hasText(cells, "Block Bo?"), "the block confirmation");
  assert.ok(confirm, "the pick must ask before blocking");
  await ada.page.keyboard.press("KeyY");
  const told = await screen(ada, (cells) => hasText(cells, "Blocked Bo"), "the block result window");
  // Both players are guests here, so the block CANNOT be durable — and the spec's
  // rule is that the UI must say so rather than silently forgetting it.
  assert.ok(
    hasText(told, "session"),
    `a guest block must be described as session-only:\n${gridToArt(told)}`,
  );
  await ada.page.keyboard.press("Escape");
  await settle(ada);
  console.log("  - Ada blocked Bo, and was told it lasts the session");

  // -------------------------------------------------------------------------
  // 3. Bo talks. Bo sees it, Ada does not.
  // -------------------------------------------------------------------------
  const adaChatBefore = ada.chat.length;
  await say(bo, BLOCKED_LINE);
  await waitFor(bo, () => bo.chat.some((m) => m.text === BLOCKED_LINE), "Bo's own copy of the blocked line");
  // The fence: Ada says something nobody blocks, so Ada's socket must deliver it
  // — and if Bo's line were coming it would already be queued ahead of it.
  await say(ada, FENCE_LINE);
  await waitFor(ada, () => ada.chat.some((m) => m.text === FENCE_LINE), "Ada's own line coming back");
  const delivered = ada.chat.slice(adaChatBefore).map((m) => m.text);
  assert.ok(
    !delivered.includes(BLOCKED_LINE),
    `a blocked line reached Ada's browser: ${JSON.stringify(delivered)}`,
  );
  const window1 = await chatWindowText(ada);
  assert.ok(!window1.includes(BLOCKED_LINE), `the blocked line is on Ada's screen:\n${window1}`);
  assert.ok(window1.includes(FENCE_LINE), `Ada's own line must be on her screen:\n${window1}`);
  // Bo is never told: Bo's own window shows the line as normal.
  const boWindow = await chatWindowText(bo);
  assert.ok(boWindow.includes(BLOCKED_LINE), `Bo must see their own line as normal:\n${boWindow}`);
  assert.ok(
    !boWindow.toLowerCase().includes("block"),
    `Bo must never be told they were blocked:\n${boWindow}`,
  );
  console.log("  - Bo's line reached Bo and not Ada, and Bo was told nothing");

  // -------------------------------------------------------------------------
  // 4. The list now says so, and the same row lifts it.
  // -------------------------------------------------------------------------
  await ada.page.keyboard.press("KeyL");
  const marked = await screen(ada, (cells) => hasText(cells, "[blocked]"), "the row marked as blocked");
  assert.ok(hasText(marked, `Bo #${bo.you.id}`), "the blocked row is still Bo's");
  await ada.page.keyboard.press("Enter");
  await screen(ada, (cells) => hasText(cells, "Unblock Bo?"), "the unblock confirmation");
  await ada.page.keyboard.press("KeyY");
  await screen(ada, (cells) => hasText(cells, "Unblocked Bo"), "the unblock result");
  await ada.page.keyboard.press("Escape");
  await settle(ada);

  await say(bo, AFTER_LINE);
  await waitFor(ada, () => ada.chat.some((m) => m.text === AFTER_LINE), "Bo's line after being unblocked");
  console.log("  - unblocking from the same row let Bo through again");

  for (const c of clients) {
    assert.deepEqual(c.pageErrors, [], `${c.label} must raise no page errors`);
  }
  console.log("BLOCK JOURNEY PASSED");
} catch (err) {
  failed = true;
  console.error("block journey FAILED:", err);
  for (const c of clients) {
    console.error(`${c.label}: chat=${JSON.stringify(c.chat)} pageErrors=${JSON.stringify(c.pageErrors)}`);
    try {
      console.error(`${c.label} screen:\n${gridToArt(await readGrid(c.page))}`);
    } catch {
      /* the canvas may not decode, which the error above already says */
    }
  }
} finally {
  for (const c of clients) {
    await c.browser.close();
  }
  process.exit(failed ? 1 : 0);
}
