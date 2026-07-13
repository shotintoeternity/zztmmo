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
const { drawTitleSidebar, titleCommand } = await import(`data:text/javascript;base64,${source}`);

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
assert.equal(titleCommand(key("KeyS", "s")), "none");
assert.equal(titleCommand(key("KeyA", "a", { ctrlKey: true })), "none");

const writes = [];
drawTitleSidebar((x, y, color, text) => writes.push({ x, y, color, text }), "TOWN");
const sidebarText = writes.map((write) => write.text).join("\n");
assert.match(sidebarText, / About ZZT!/);
assert.match(sidebarText, / High Scores/);
assert.match(sidebarText, / Board editor/);
assert.doesNotMatch(sidebarText, /Game speed/);

console.log("title.test.mjs: title actions and sidebar menu passed");
