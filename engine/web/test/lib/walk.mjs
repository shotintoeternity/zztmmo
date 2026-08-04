// Walking a player around a board from a real browser, closed on what the
// player is OBSERVED to do rather than on a guessed hold (M16.11e).
//
// Shared by e2e_journey.test.mjs and coop_journey.test.mjs. Both drive the
// production zzt-server at its real 110ms tick; neither has a control endpoint
// to step. Four tasks — M16.11a, M16.11b, M16.11c, M16.11d — were spent fixing
// one sentence (*a step is not one tile*) one site at a time, in two files that
// carried the same helper as separate copies. This module is the end of that
// carry: the helper lives once, so the next fix lands in both journeys at once.
//
// ---------------------------------------------------------------------------
// HOW A HELD KEY BECOMES TILES. Stated exactly, because the comments this
// replaces stated it wrongly and the wrong model is what made "pick a hold and
// hope" look like the only available design.
//
//   * The client sends on the key EDGES — handleKeyDown and handleKeyUp both
//     call sendInput(currentMask()) immediately (main.ts:2871,2882) — AND
//     re-sends the current mask every 55ms from the inputTimer (main.ts:1451).
//     So while a key is held, a nonzero mask keeps arriving; on release a zero
//     mask goes out at once.
//   * The server does NOT latch it. WorldInstance.Tick takes the pending map
//     and replaces it with a fresh one (websocket_server.go:361-362), so each
//     input is consumed by exactly one tick. A tick moves the player only if a
//     nonzero mask arrived since the previous tick — which, while a key is
//     held, the 55ms re-send guarantees.
//
// Tiles moved = server ticks (ServerTickDuration = 110ms,
// websocket_server.go:24) that fell inside the hold. That is why a forced 330ms
// hold moves exactly three tiles every time, and why the count is a steady
// answer rather than a coin flip.
//
// The predecessor comments said the SERVER latched the mask "until the key-up
// sample arrives". That predicts the same three tiles, which is why it survived
// four tasks unchallenged — but it is wrong about which side holds the state,
// and it invites the belief that a stale direction can go on being applied
// after the key is up. It cannot: one input, one tick.
//
// ---------------------------------------------------------------------------
// WHAT CLOSING THE LOOP BUYS, AND WHAT IT DOES NOT.
//
// It buys a hold that ends when the tile actually lands instead of after a
// fixed 95ms, so a step tracks load instead of assuming it.
//
// It does NOT buy a guaranteed one-tile step. Between the tick that moved the
// player and the zero mask reaching the server there is a window — server
// broadcast, this driver's socket listener, its poll, the CDP keyup, the wire
// back — and when that window crosses the next tick boundary the player takes a
// second tile. Under steady load that window is steady, which is the
// residue-class trap again at k=2. So the growing hold in walkOnto STAYS: it is
// the only part that guarantees the walk terminates.
//
// ---------------------------------------------------------------------------
// WHY NOT canvas.mjs's walk(), which already does this better. Its step() posts
// to `${controlURL}/control/step` and its clock is page.clock.runFor: it needs
// M16.9's tick-locked control server and faked page clock. /control/step exists
// only in m16_9_test.go — the production server has no such endpoint, and these
// journeys run the production binary at real time on purpose, because that is
// what makes them end-to-end. What transfers is the principle (wait for the
// observable, never for the clock); the observable here is the diff stream the
// journeys already parse into their own position, not a control response.
//
// ---------------------------------------------------------------------------
// THE OBSERVER. Every helper takes the caller's observed-state bag `c`:
//
//   c.page      the Playwright page whose keyboard is driven
//   c.you       the player's latest {x, y, health}, rebuilt from server frames
//   c.boardId   the board that player is on
//   c.label     how to name the player in a failure (optional)
//   c.walkDefaults  per-driver {maxSteps, stallLimit} (optional)
//
// coop_journey's per-client object IS this shape already; e2e_journey's state is
// page-global, so it passes an adapter whose `you`/`boardId` are getters over
// that global. `you` and `boardId` are read on every poll, so they must be live
// views rather than values copied when the bag was built.
//
// The defaults differ per driver on purpose and are NOT unified here: the
// cutline walks three contending players and tolerates more steps and one more
// stall than the single-player journey does. Taking that tolerance away would
// be a behaviour change to the protected cutline driver (CUTLINE.md), not a
// refactor.

import assert from "node:assert/strict";

/** ServerTickDuration (websocket_server.go:24). */
export const SERVER_TICK_MS = 110;

/** How often the observed position is re-read while waiting on it. */
const POLL_MS = 10;

/**
 * How long a step holds a key that produces no observed movement before giving
 * up on it. Three ticks: long enough that a merely slow tile still lands inside
 * it, short enough that a genuinely blocked step (a wall, another player
 * standing there, a shot that moves nobody) is cheap. Giving up is not a
 * failure — the caller's stall detector is what decides that.
 */
const MAX_HOLD_MS = 3 * SERVER_TICK_MS;

/**
 * After the key is released, how long the position must hold still before the
 * caller re-aims. This is the same 70ms the by-hand drivers slept, spent
 * waiting for quiet instead of waiting blind: a step whose extra tile lands
 * late restarts the clock rather than letting the next re-aim read a position
 * the player has already left.
 */
const QUIET_MS = 70;

/** Cap on the settle, so a player being pushed around by something else still returns. */
const MAX_QUIET_MS = 5 * SERVER_TICK_MS;

/**
 * A plain timer rather than page.waitForTimeout: the observed state is rebuilt
 * by this process's own websocket listeners, so a poll needs to yield to the
 * Node event loop, not to make a CDP round trip. At POLL_MS granularity that
 * difference is most of the cost of a step.
 */
const nap = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

/** Everything a step can change about where the player is. */
const mark = (c) => `${c.you?.x},${c.you?.y},${c.boardId}`;

/** How a player is named in a failure. */
export const at = (c) =>
  `${c.label ? c.label + " " : ""}board=${c.boardId} pos=(${c.you?.x},${c.you?.y}) hp=${c.you?.health}`;

const who = (c) => (c.label ? `${c.label} ` : "");

const walkDefaults = (c) => ({ maxSteps: 30, stallLimit: 4, ...(c.walkDefaults || {}) });

/** Poll `pred` until it holds or `timeoutMs` runs out; true if it held. */
async function until(pred, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    if (pred()) return true;
    if (Date.now() >= deadline) return false;
    await nap(POLL_MS);
  }
}

/** Wait for the player to stop moving, so the caller re-aims from a settled position. */
async function settle(c) {
  const deadline = Date.now() + MAX_QUIET_MS;
  let last = mark(c);
  let still = Date.now();
  for (;;) {
    await nap(POLL_MS);
    const now = mark(c);
    if (now !== last) {
      last = now;
      still = Date.now();
    } else if (Date.now() - still >= QUIET_MS) {
      return;
    }
    if (Date.now() >= deadline) return;
  }
}

/**
 * Hold `code` until the player is observed to move, then release.
 *
 * `hold` forces a fixed hold in milliseconds instead, which is how a run
 * reproduces the uniform multi-tile step that this family kept being bitten by
 * (a forced 330 moves exactly three tiles) without waiting for load to supply
 * it. It is also how a non-movement key is driven: shooting moves nobody, so
 * there is no movement to close the loop on and the caller says how long to
 * hold.
 *
 * `extra` is walkOnto's residue-class escape: after the move is observed, keep
 * the key down this much longer, which buys one more tick with the key latched
 * and therefore a different step size.
 */
export async function step(c, code, { hold = null, extra = 0 } = {}) {
  const before = mark(c);
  await c.page.keyboard.down(code);
  if (hold === null) {
    await until(() => mark(c) !== before, MAX_HOLD_MS);
    if (extra > 0) await nap(extra);
  } else {
    await nap(hold + extra);
  }
  await c.page.keyboard.up(code);
  await settle(c);
}

/**
 * Walk in one direction until done(); throws if the player stops making
 * progress.
 *
 * Safe for a one-way inequality ("x >= 10") and for a board change, and NOT
 * safe for an exact tile — a step can still cover two. Use walkOnto for a tile.
 */
export async function walkUntil(c, code, done, describe, options = {}) {
  const { maxSteps, stallLimit, hold = null } = { ...walkDefaults(c), ...options };
  let stalled = 0;
  for (let i = 0; i < maxSteps; i++) {
    if (done()) return;
    const before = mark(c);
    await step(c, code, { hold });
    if (mark(c) === before) {
      if (++stalled >= stallLimit) {
        throw new Error(`${who(c)}is stuck walking ${code} toward ${describe} at ${at(c)}`);
      }
    } else {
      stalled = 0;
    }
  }
  if (!done()) throw new Error(`${who(c)}never reached ${describe}; stopped at ${at(c)}`);
}

/**
 * Walk onto one exact tile, recomputing the direction after every step.
 *
 * WHY RE-AIM. walkUntil with a one-way inequality assumes a step covers exactly
 * one tile, and it does not. Along a row that is harmless — an extra tile still
 * passes THROUGH whatever was being collected. Across rows it is not: a walk
 * that stops at "y <= 10" can stop on row 9, and everything done afterwards
 * along row 9 misses row 10 entirely. Re-aiming turns an overshoot into a
 * correction instead of a miss (M16.11a, M16.11b).
 *
 * Vertical first, then horizontal: the target is reached by clearing the
 * starting row before travelling along the target's own row.
 *
 * WHY THE HOLD STILL GROWS, even now that the release is closed on observed
 * movement. Closing the loop makes the common step one tile; it does not make
 * it one tile always, because the zero mask can still lose its race with the
 * next tick boundary (see the header). While a step size k stays constant,
 * every square this walk stands on stays in one residue class mod k, so a
 * target in another class is not merely missed, it is unreachable: k=3 from row
 * 12 visits 12, 9, 12, 9 and never row 11, and both copies of this helper were
 * watched doing exactly that until their steps ran out (M16.11c). So a step
 * that did not close the distance holds ~110ms longer next time — one more tick
 * with the key down, so a different k and a different class — cycling mod 330
 * rather than growing without bound. Nothing changes for a walk that is making
 * progress: the hold only grows after a step that failed to.
 *
 * `until` ends the walk on something other than arrival, because walking onto a
 * passage changes the board and the coordinates on the far side are not this
 * walk's coordinates.
 *
 * `hold` forces the fixed uniform step described in step(); no caller needs it
 * in the shipped form, and the suites' regression guards use it to prove this
 * helper still copes with a step that covers three tiles.
 */
export async function walkOnto(c, tx, ty, describe, options = {}) {
  const { maxSteps = 24, stallLimit, hold = null, until: done = null } = { ...walkDefaults(c), ...options };
  let stalled = 0;
  let extra = 0; // added to the hold after a step that did not close the distance
  for (let i = 0; i < maxSteps; i++) {
    if (done?.()) return;
    const { x, y } = c.you ?? {};
    if (x === tx && y === ty) return;
    const vertical = y !== ty;
    const was = vertical ? Math.abs(y - ty) : Math.abs(x - tx);
    const before = mark(c);
    const code = vertical ? (y > ty ? "ArrowUp" : "ArrowDown") : x > tx ? "ArrowLeft" : "ArrowRight";
    await step(c, code, { hold, extra });
    const now = vertical ? Math.abs((c.you?.y ?? y) - ty) : Math.abs((c.you?.x ?? x) - tx);
    extra = now > 0 && now >= was ? (extra + SERVER_TICK_MS) % (3 * SERVER_TICK_MS) : 0;
    if (mark(c) === before) {
      if (++stalled >= stallLimit) {
        throw new Error(`${who(c)}is stuck walking onto ${describe} (${tx},${ty}) at ${at(c)}`);
      }
    } else {
      stalled = 0;
    }
  }
  if (done?.()) return;
  throw new Error(`${who(c)}never stood on ${describe} (${tx},${ty}); stopped at ${at(c)}`);
}

/** The observer contract, checked once at wiring time rather than at first failure. */
export function assertObserver(c) {
  assert.ok(c?.page?.keyboard, "walk helpers need c.page (a Playwright page)");
  assert.ok("you" in c && "boardId" in c, "walk helpers need live c.you and c.boardId");
}
