import assert from "node:assert/strict";
import { build } from "esbuild";

const output = await build({
  entryPoints: ["src/title.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
const { drawTitleSidebar, titleCommand, NO_OCCUPANCY } = await import(`data:text/javascript;base64,${source}`);

function key(code, k = "", opts = {}) {
  return { code, key: k, ctrlKey: false, metaKey: false, altKey: false, ...opts };
}

assert.equal(titleCommand(key("KeyA", "a")), "about");
assert.equal(titleCommand(key("KeyW", "w")), "world");
assert.equal(titleCommand(key("KeyP", "p")), "play");
assert.equal(titleCommand(key("KeyR", "r")), "restore");
assert.equal(titleCommand(key("KeyQ", "q")), "quit");
assert.equal(titleCommand(key("Escape", "Escape")), "quit");
assert.equal(titleCommand(key("KeyH", "h")), "highScores");
assert.equal(titleCommand(key("KeyE", "e")), "editor");
assert.equal(titleCommand(key("KeyF", "f")), "feedback");
assert.equal(titleCommand(key("KeyS", "s")), "none");
assert.equal(titleCommand(key("KeyA", "a", { ctrlKey: true })), "none");

const writes = [];
drawTitleSidebar((x, y, color, text) => writes.push({ x, y, color, text }), "TOWN");
const sidebarText = writes.map((write) => write.text).join("\n");
assert.match(sidebarText, / About ZZT!/);
assert.match(sidebarText, / High Scores/);
assert.match(sidebarText, / Board editor/);
// M18.4: the beta feedback pointer is always drawn, signed in or not.
assert.match(sidebarText, / Feedback/);
assert.doesNotMatch(sidebarText, /Google sign-in/);
assert.doesNotMatch(sidebarText, /Game speed/);

const authWrites = [];
drawTitleSidebar((x, y, color, text) => authWrites.push({ x, y, color, text }), "TOWN", "", true);
const authSidebarText = authWrites.map((write) => write.text).join("\n");
assert.match(authSidebarText, / Google sign-in/);
// The feedback row must not overprint the sign-in row, and the blank separator
// between the ZZTMMO block and sign-in stays.
{
  const feedback = authWrites.find((write) => write.text === " F ");
  const signIn = authWrites.find((write) => write.text === " G ");
  assert.ok(feedback, "the feedback hotkey box is drawn");
  assert.ok(signIn.y > feedback.y + 1, "sign-in keeps a blank row above it");
  for (const write of authWrites.filter((w) => w.y === feedback.y)) {
    assert.ok(write.x >= 60 && write.x + write.text.length <= 80, `"${write.text}" leaves the sidebar`);
  }
}

// M17.11: how busy the server is, before the player opens the picker. A quiet
// server draws nothing rather than zeros, and neither count may reach into the
// sidebar's right edge (column 79) or collide with the label beside it.
{
  const draw = (occupancy) => {
    const writes = [];
    drawTitleSidebar((x, y, color, text) => writes.push({ x, y, color, text }), "TOWN", "", false, occupancy);
    return writes;
  };

  const quiet = draw(NO_OCCUPANCY).map((write) => write.text).join("\n");
  assert.doesNotMatch(quiet, /Playing:/, "an empty server shows no playing count");
  assert.doesNotMatch(quiet, /Editing:/, "an empty server shows no editing count");

  const busy = draw({ players: 12, editors: 3 });
  const rowOf = (needle) => busy.find((write) => write.text.includes(needle));
  assert.ok(rowOf(" Playing:"), "the playing total is drawn");
  assert.ok(rowOf(" Editing:"), "the editing total is drawn");
  assert.equal(rowOf(" Editing:").y, rowOf(" Playing:").y + 1, "the two totals stack");
  // sidebarClearLine also writes on these rows; the count is the numeric write.
  const valueOn = (y) => busy.filter((write) => write.y === y && /^\d+$/.test(write.text));
  assert.deepEqual(valueOn(rowOf(" Playing:").y).map((w) => w.text), ["12"]);
  assert.deepEqual(valueOn(rowOf(" Editing:").y).map((w) => w.text), ["3"]);
  // Nothing on those rows may overlap its neighbour or run off the sidebar.
  for (const write of busy.filter((w) => w.y === rowOf(" Playing:").y || w.y === rowOf(" Editing:").y)) {
    assert.ok(write.x >= 60, `sidebar row stays out of the board: ${write.x}`);
    assert.ok(write.x + write.text.length <= 80, `"${write.text}" runs past column 79`);
  }
  const label = rowOf(" Playing:");
  const value = valueOn(label.y)[0];
  assert.ok(label.x + label.text.length <= value.x, "the count never overprints its label");
  // The world name and the menu keep their vanilla rows beneath the totals.
  assert.ok(busy.find((write) => write.text === " W ").y > rowOf(" Editing:").y);

  // A server with editors but no players collapses the pair upward: a lone
  // count must not float on the second row with a gap above it.
  const editorsOnly = draw({ players: 0, editors: 2 });
  const editing = editorsOnly.find((write) => write.text.includes(" Editing:"));
  assert.ok(editing, "an editors-only server still reports them");
  assert.equal(editing.y, busy.find((write) => write.text.includes(" Playing:")).y);
}

// Occupancy is optional: every existing caller that omits it still draws.
{
  const writes = [];
  drawTitleSidebar((x, y, color, text) => writes.push({ x, y, color, text }), "TOWN");
  assert.ok(writes.some((write) => write.text === " W "));
}

console.log("title.test.mjs: title actions, sidebar menu, and occupancy passed");
