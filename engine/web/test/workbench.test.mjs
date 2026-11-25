// M38.6 — the workbench client's pure half: keys to ops, the menu, the submit
// window, the export name and the sidebar.
//
// The claim worth naming: a key on the workbench is an op the SERVER applies.
// A play key sets this tick's input as live play would send it (a keymask or a
// command byte, decoded by the server) and steps over it; nothing here
// simulates, predicts or measures.

import assert from "node:assert/strict";
import { build } from "esbuild";

async function load(entry) {
  const output = await build({ entryPoints: [entry], bundle: true, format: "esm", platform: "node", write: false });
  const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
  return import(`data:text/javascript;base64,${source}`);
}

const wb = await load("src/workbench.ts");
const challenge = await load("src/challenge.ts");

const status = (tick, length, extra = {}) => ({
  type: "workbenchStatus", challenge: "gem-dash", version: 1, goal: "Goal: collect 3 gems",
  tick, length, ceiling: 5454, playing: false, met: false, ticks: tick, ...extra,
});

// --- keys -----------------------------------------------------------------

assert.equal(wb.workbenchKeyFor("Period"), "step");
assert.equal(wb.workbenchKeyFor("Comma"), "back");
assert.equal(wb.workbenchKeyFor("KeyW"), "wait");
assert.equal(wb.workbenchKeyFor("Escape"), "menu");
assert.equal(wb.workbenchKeyFor("ArrowUp"), "", "a play key is not a workbench key");

assert.deepEqual(wb.workbenchInputOps(status(3, 10), { keymask: 8 }), [
  { type: "workbench", op: "setInput", tick: 3, keymask: 8 },
  { type: "workbench", op: "step" },
], "a play key sets this tick and steps over it");
assert.deepEqual(wb.workbenchInputOps(status(10, 10), { key: 84 })[0], { type: "workbench", op: "setInput", tick: 10, key: 84 },
  "at the end it appends");
assert.deepEqual(wb.workbenchInputOps(null, { keymask: 8 }), [], "nothing before the first status");

assert.deepEqual(wb.workbenchKeyOps(status(3, 10), "step"), [{ type: "workbench", op: "step" }]);
assert.deepEqual(wb.workbenchKeyOps(status(10, 10), "step").map((o) => o.op), ["setInput", "step"], "a step at the end is a wait");
assert.deepEqual(wb.workbenchKeyOps(status(3, 10), "back"), [{ type: "workbench", op: "seek", tick: 2 }]);
assert.deepEqual(wb.workbenchKeyOps(status(0, 10), "back"), [], "no tick before 0");
assert.deepEqual(wb.workbenchKeyOps(status(3, 10), "wait"), [
  { type: "workbench", op: "setInput", tick: 3 },
  { type: "workbench", op: "step" },
], "a wait is an empty input");

// --- the menu -------------------------------------------------------------

const menu = wb.workbenchMenuLines(status(3, 10), { canImport: true });
for (const row of ["!play;Play", "!truncate;Truncate here", "!import;Import my run", "!export;Export the script", "!submit;Submit", "!leave;Leave the workbench"]) {
  assert.ok(menu.includes(row), `the menu offers ${row}`);
}
assert.ok(wb.workbenchMenuLines(status(3, 10, { playing: true }), { canImport: false }).includes("!pause;Pause"));
assert.ok(!wb.workbenchMenuLines(status(3, 10), { canImport: false }).some((l) => l.startsWith("!import;")), "no run, no import");
assert.deepEqual(wb.workbenchMenuOps(status(3, 10), "truncate"), [{ type: "workbench", op: "truncate", tick: 3 }]);
assert.deepEqual(wb.workbenchMenuOps(status(3, 10), "import", "chal-1"), [{ type: "workbench", op: "import", recording: "chal-1" }]);
assert.deepEqual(wb.workbenchMenuOps(status(3, 10), "import", ""), [], "no recording, no import");
assert.deepEqual(wb.workbenchMenuOps(status(3, 10), "leave"), [], "leaving is the client's");

// --- submit ---------------------------------------------------------------

const posted = wb.workbenchSubmitLines({ type: "workbenchSubmitResult", status: 200, result: { met: true, ticks: 31, score: 3, gems: 3, durable: true, rank: 1, recordingId: "r" } });
assert.ok(posted.includes("  Judged: 31 ticks."));
assert.ok(posted.includes("  Assisted rank 1."));
assert.ok(posted.includes("!replay;Watch the replay"));
const guest = wb.workbenchSubmitLines({ type: "workbenchSubmitResult", status: 200, result: { met: true, ticks: 31, durable: false, recordingId: "r" } });
assert.ok(guest.includes("  Guest runs are not posted."));
const short = wb.workbenchSubmitLines({ type: "workbenchSubmitResult", status: 422, result: { met: false, durable: false, refusal: "goal-not-met", reason: "the goal was not met by tick 2, where the script ends" } });
assert.ok(short.includes("  Not posted."));
assert.ok(short.join(" ").includes("tick 2"));
for (const lines of [posted, guest, short, menu]) {
  assert.ok(lines.every((line) => line.length <= 44), "lines fit the CP437 window");
}

// --- export and sidebar ---------------------------------------------------

assert.equal(wb.workbenchExportFileName({ challenge: "gem-dash", version: 1, ticks: [] }), "gem-dash-v1-tas.json");
assert.equal(wb.workbenchTimeline(status(5, 10)), "\xdb".repeat(8) + "\xb0".repeat(8));
assert.equal(wb.workbenchTimeline(status(0, 0)), "\xb0".repeat(16));

const writes = [];
wb.paintWorkbenchSidebar((x, y, color, text) => writes.push({ x, y, text }), status(4, 9, { met: true, ticks: 9, score: 3, gems: 3 }), "tick 9 presses a refused key");
const text = writes.map((w) => w.text).join("|");
assert.ok(text.includes("Tick 4/9"), "the current tick");
assert.ok(text.includes("collect 3 gems"), "the goal");
assert.ok(text.includes("Met: 9 ticks"), "the measured count");
assert.ok(text.includes("refused key"), "a refusal is said");
assert.ok(writes.every((w) => w.x >= 60 && w.x + w.text.length <= 80), "the sidebar stays in its columns");

// --- the landing ----------------------------------------------------------

const summary = { id: "gem-dash", title: "Gem Dash", world: "GEMDASH", version: 1, goalLine: "Goal: collect 3 gems", path: "/challenge/gem-dash", available: true };
const landing = challenge.challengeLandingLines({ challenge: summary, leaderboard: [{ rank: 1, name: "ada", ticks: 9, score: 3, recordingId: "r", you: true }], canSubmit: true }, { signedIn: true, hasGhost: true });
const actions = landing.filter((line) => line.startsWith("!"));
assert.equal(actions[actions.length - 1], "!bench;TAS workbench", "the workbench is the landing's last action, so no row above it moves");

console.log("workbench.test.mjs: keys, menu, submit, export and sidebar passed");
