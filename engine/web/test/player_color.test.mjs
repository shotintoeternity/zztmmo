// M19.1 — two browsers in one room, two different smileys.
//
// Driven by engine/m19_1_browser_test.go, which hosts ACCEPT on the production
// zzt-server binary and runs this script against it. Two separate Chromium
// instances rather than two tabs, for the coop_journey.test.mjs reason: separate
// storage per player, and no throttled timers in a background tab.
//
// WHAT THIS SUITE PROVES, and why it has to be a browser at all. The Go tests
// prove the negative — that a colour reaches no tile, no hash and no recording.
// Only a canvas can prove the positive: that the pixels behind one player's ☻
// are that player's colour, in BOTH players' browsers, and that they differ. The
// glyph never changes; the whole feature is the background.
//
// READING THE CANVAS. The M16.9 decoder maps a background to the nearest EGA
// index, which is exactly what a 24-bit tint is not, so this suite reads raw
// pixels instead: the corners of a cell, which the ☻ glyph never inks. That is
// also the honest measurement — "what colour is the square that player is
// standing on" is a question about pixels.
//
// THE COLOUR ARRIVES FROM localStorage (the M19.2 picker writes it; until then a
// hand-set key or a test does). It is set through addInitScript so it is in
// place before the client reads it at socket open.

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { chromium } from "playwright";

const baseURL = process.env.BASE_URL || "http://127.0.0.1:8080";
const resultsDir = path.resolve(process.env.COLOR_OUT || "test-results/player-color");
fs.mkdirSync(resultsDir, { recursive: true });

const CELL_W = 8;
const CELL_H = 14;
const WORLD = "ACCEPT";
const COLOR_KEY = "zzt-color";

const ADA_COLOR = "#ff0000";
const BO_COLOR = "#00c0ff";

const clients = [];

async function openClient(label, name, color) {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1280, height: 720 } });
  const page = await context.newPage();
  if (color) {
    await page.addInitScript(
      ([key, value]) => {
        try {
          window.localStorage.setItem(key, value);
        } catch {
          // A sandboxed storage would make this test meaningless, and the
          // assertions below say so far more clearly than a throw here.
        }
      },
      [COLOR_KEY, color],
    );
  }

  const c = { label, name, color, browser, context, page, pageErrors: [], joins: [], roster: [], you: null, snapshots: 0, diffs: 0 };
  clients.push(c);

  page.on("pageerror", (err) => c.pageErrors.push(String(err)));
  page.on("websocket", (ws) => {
    ws.on("framesent", (f) => {
      const raw = String(f.payload);
      if (raw.includes('"type":"join"')) c.joins.push(raw);
    });
    ws.on("framereceived", (frame) => {
      let msg;
      try {
        msg = JSON.parse(String(frame.payload));
      } catch {
        return;
      }
      const body = msg.type === "boardChange" ? msg.snapshot : msg;
      if (msg.type === "snapshot") c.snapshots++;
      if (msg.type === "diff") c.diffs++;
      if (body.you) c.you = body.you;
      if (Array.isArray(body.players)) {
        c.roster = body.players;
        const me = body.players.find((p) => p.id === c.you?.id);
        if (me) c.you = me;
      }
    });
  });

  const response = await page.goto(baseURL);
  assert.equal(response?.status(), 200, `${label}: the client index must be served, not the build-me 404 page`);
  await page.waitForLoadState("domcontentloaded");
  await page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  return c;
}

const sleep = (c, ms) => c.page.waitForTimeout(ms);
const settle = (c) => sleep(c, 700);

async function waitFor(c, pred, describe, timeoutMs = 15000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    if (pred()) return;
    if (Date.now() > deadline) {
      throw new Error(
        `timed out waiting for ${describe}\n  ${c.label}: you=${JSON.stringify(c.you)} roster=${JSON.stringify(c.roster)}`,
      );
    }
    await sleep(c, 60);
  }
}

/** Title screen -> picker -> that world's title screen -> play. */
async function joinWorld(c, world) {
  await c.page.keyboard.type(c.name);
  await c.page.keyboard.press("Enter");
  await settle(c);
  await c.page.keyboard.type(world);
  await sleep(c, 400);
  await c.page.keyboard.press("Enter");
  await settle(c);
  const before = c.snapshots;
  await c.page.keyboard.press("KeyP");
  await waitFor(c, () => c.snapshots > before, `${c.label}'s join snapshot for ${world}`, 20000);
}

/**
 * The colour of one cell, read from the canvas backing store. Sampled at the
 * corners, which the ☻ never inks — a mid-cell sample would read the glyph.
 */
async function cellCorners(c, col, row) {
  return c.page.evaluate(
    ([col, row, cellW, cellH]) => {
      const canvas = document.querySelector("canvas[data-screen]");
      const ctx = canvas.getContext("2d", { willReadFrequently: true });
      const x = col * cellW;
      const y = row * cellH;
      const hex = (px, py) => {
        const d = ctx.getImageData(px, py, 1, 1).data;
        return (
          "#" +
          [d[0], d[1], d[2]].map((v) => v.toString(16).padStart(2, "0")).join("")
        );
      };
      return [hex(x, y), hex(x + cellW - 1, y), hex(x, y + cellH - 1), hex(x + cellW - 1, y + cellH - 1)];
    },
    [col, row, CELL_W, CELL_H],
  );
}

/** The board square `player` stands on, as screen columns: board x,y is screen x-1,y-1. */
const screenCellOf = (player) => ({ col: player.x - 1, row: player.y - 1 });

async function assertSquareIs(c, player, expected, describe) {
  const { col, row } = screenCellOf(player);
  const corners = await cellCorners(c, col, row);
  for (const corner of corners) {
    assert.equal(
      corner,
      expected,
      `${describe}: ${c.label}'s canvas shows ${corner} at screen (${col},${row}) — the four corners were ` +
        `${JSON.stringify(corners)}, expected ${expected}`,
    );
  }
}

async function dumpFailure(err) {
  console.error("M19.1 player-colour suite FAILED:", err);
  for (const c of clients) {
    console.error(
      `--- ${c.label}: you=${JSON.stringify(c.you)} roster=${JSON.stringify(c.roster)} ` +
        `joins=${JSON.stringify(c.joins)} pageErrors=${JSON.stringify(c.pageErrors)}`,
    );
    try {
      await c.page.screenshot({ path: path.join(resultsDir, `m19_1_${c.label.toLowerCase()}_failure.png`) });
    } catch (dumpErr) {
      console.error(`  (could not screenshot ${c.label}: ${dumpErr})`);
    }
  }
  console.error(`Saved screenshots under ${resultsDir}`);
}

let ada, bo;

try {
  console.log(`=== M19.1: two coloured players in one room, against ${baseURL} ===`);

  ada = await openClient("Ada", "Ada", ADA_COLOR);
  bo = await openClient("Bo", "Bo", BO_COLOR);

  await joinWorld(ada, WORLD);
  await joinWorld(bo, WORLD);

  // --- the wire ------------------------------------------------------------

  for (const c of clients) {
    assert.equal(c.joins.length, 1, `${c.label} must have sent exactly one join`);
    assert.ok(
      c.joins[0].includes(`"color":"${c.color}"`),
      `${c.label}'s join must carry the stored colour, sent: ${c.joins[0]}`,
    );
  }

  await waitFor(ada, () => ada.roster.length >= 2, "Ada to see both players in her roster");
  await waitFor(bo, () => bo.roster.length >= 2, "Bo to see both players in his roster");

  for (const c of clients) {
    const colors = Object.fromEntries(c.roster.map((p) => [p.id, p.color]));
    const names = Object.fromEntries(c.roster.map((p) => [p.id, p.name]));
    assert.equal(
      new Set(Object.values(colors)).size,
      2,
      `${c.label}'s roster must carry two different colours, got ${JSON.stringify(colors)}`,
    );
    assert.ok(
      Object.values(colors).includes(ADA_COLOR) && Object.values(colors).includes(BO_COLOR),
      `${c.label}'s roster must carry both picked colours, got ${JSON.stringify(colors)}`,
    );
    assert.ok(
      Object.values(names).includes("Ada") && Object.values(names).includes("Bo"),
      `${c.label}'s roster must carry both names (M19.1 puts Name on PlayerSnapshot), got ${JSON.stringify(names)}`,
    );
  }

  // --- the canvas ----------------------------------------------------------
  //
  // Nobody is moving, so the roster and the frame on screen agree. Let the room
  // tick a few times first so the assertion is about a settled screen.
  await sleep(ada, 900);

  const rosterOf = (c, label) => {
    const entry = c.roster.find((p) => p.name === label);
    assert.ok(entry, `${c.label} must see ${label} in the roster: ${JSON.stringify(c.roster)}`);
    return entry;
  };

  for (const viewer of clients) {
    const adaEntry = rosterOf(viewer, "Ada");
    const boEntry = rosterOf(viewer, "Bo");
    assert.notDeepEqual(
      screenCellOf(adaEntry),
      screenCellOf(boEntry),
      `${viewer.label}: the two players must be on different squares to be told apart`,
    );
    await assertSquareIs(viewer, adaEntry, ADA_COLOR, "Ada's smiley sits on Ada's colour");
    await assertSquareIs(viewer, boEntry, BO_COLOR, "Bo's smiley sits on Bo's colour");
  }

  console.log("  both browsers draw both players, each on their own 24-bit background");

  // Each player can point at their own ☻: the square the server says is MINE is
  // the colour I picked, in my own browser.
  await assertSquareIs(ada, ada.you, ADA_COLOR, "Ada can point at her own smiley");
  await assertSquareIs(bo, bo.you, BO_COLOR, "Bo can point at his own smiley");

  // The control: an ordinary board square is untouched by the tint, so this is
  // not a suite that would pass on a canvas painted red everywhere. The square
  // is one nobody is standing on.
  for (const viewer of clients) {
    const taken = new Set(viewer.roster.map((p) => `${p.x},${p.y}`));
    let probe = null;
    for (let x = 2; x < 20 && !probe; x += 1) {
      if (!taken.has(`${x},2`)) probe = { x, y: 2 };
    }
    assert.ok(probe, `${viewer.label}: no empty probe square found`);
    const corners = await cellCorners(viewer, probe.x - 1, probe.y - 1);
    for (const corner of corners) {
      assert.notEqual(corner, ADA_COLOR, `${viewer.label}: an empty square must not carry a player's colour`);
      assert.notEqual(corner, BO_COLOR, `${viewer.label}: an empty square must not carry a player's colour`);
    }
  }

  for (const c of clients) {
    assert.deepEqual(c.pageErrors, [], `${c.label} must reach this point with no page errors`);
  }

  console.log("=== M19.1 player colour: PASS ===");
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
