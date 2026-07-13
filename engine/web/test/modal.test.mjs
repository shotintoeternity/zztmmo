import assert from "node:assert/strict";
import { build } from "esbuild";

// Bundle modal.ts under Node so M5.4's object code editor (a faithful
// TextWindowEdit port) can be exercised as pure key-routing logic, the same way
// dream.test.mjs covers the generation flow.
const output = await build({
  entryPoints: ["src/modal.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
const { handleModalKey } = await import(`data:text/javascript;base64,${source}`);

// modal.ts reads only event.code / event.key / the modifier flags at runtime.
function key(code, k = "", opts = {}) {
  return { code, key: k, ctrlKey: false, metaKey: false, altKey: false, ...opts };
}

function editor(lines) {
  return {
    kind: "programEditor",
    title: "Edit Program",
    lines: [...lines],
    linePos: 1,
    charPos: 1,
    insertMode: true,
    labels: [],
    warnings: [],
    submitted: null,
    onSubmit(result) {
      this.submitted = result;
    },
  };
}

function worldSearch() {
  return {
    kind: "worldSearch",
    title: "Select a World",
    query: "",
    selected: 0,
    entries: [
      { world: "TEEN", id: "teen", title: "Teen Priest", author: "Draco", created: "1998" },
      { world: "CUTLASS", id: "cutlass", title: "Tales of Adventure: The Treasure of Captain Cutlass", author: "Dr. Dos", created: "2001" },
    ],
    picked: null,
    onSelect(entry) {
      this.picked = entry;
    },
  };
}

// Insert mode types a character at the caret and advances it.
{
  const m = editor(["@Vendor"]);
  assert.equal(handleModalKey(m, key("KeyX", "X")), "redraw");
  assert.equal(m.lines[0], "X@Vendor");
  assert.equal(m.charPos, 2);
}

// Overwrite mode replaces the character under the caret.
{
  const m = editor(["@Vendor"]);
  m.insertMode = false;
  handleModalKey(m, key("Digit1", "N"));
  assert.equal(m.lines[0], "NVendor");
  assert.equal(m.charPos, 2);
}

// Enter splits the current line at the caret.
{
  const m = editor(["@Vendorplus"]);
  m.charPos = 8; // just after "@Vendor"
  handleModalKey(m, key("Enter", "Enter"));
  assert.deepEqual(m.lines, ["@Vendor", "plus"]);
  assert.equal(m.linePos, 2);
  assert.equal(m.charPos, 1);
}

// Backspace on an empty line deletes it and joins upward.
{
  const m = editor(["@Vendor", "", "#end"]);
  m.linePos = 2;
  m.charPos = 1;
  handleModalKey(m, key("Backspace", "Backspace"));
  assert.deepEqual(m.lines, ["@Vendor", "#end"]);
  assert.equal(m.linePos, 1);
}

// Ctrl-Y deletes the current line.
{
  const m = editor(["@Vendor", "#end", ":shop"]);
  m.linePos = 2;
  handleModalKey(m, key("KeyY", "y", { ctrlKey: true }));
  assert.deepEqual(m.lines, ["@Vendor", ":shop"]);
}

// A modifier chord that is not Ctrl-Y is not text input.
{
  const m = editor(["@Vendor"]);
  assert.equal(handleModalKey(m, key("KeyC", "c", { ctrlKey: true })), "ignore");
  assert.equal(m.lines[0], "@Vendor");
}

// A full lines never exceeds TextWindowWidth-8 (42) characters in insert mode.
{
  const m = editor(["x".repeat(42)]);
  m.charPos = 43;
  assert.equal(handleModalKey(m, key("KeyA", "a")), "ignore");
  assert.equal(m.lines[0].length, 42);
}

// Escape submits the accumulated lines and closes; there is no cancel.
{
  const m = editor(["@NewVendor", "#end"]);
  assert.equal(handleModalKey(m, key("Escape", "Escape")), "close");
  assert.deepEqual(m.submitted, ["@NewVendor", "#end"]);
}

// Arrow navigation clamps to the line range.
{
  const m = editor(["a", "b", "c"]);
  handleModalKey(m, key("ArrowDown", "ArrowDown"));
  assert.equal(m.linePos, 2);
  handleModalKey(m, key("PageUp", "PageUp"));
  assert.equal(m.linePos, 1);
  handleModalKey(m, key("ArrowUp", "ArrowUp"));
  assert.equal(m.linePos, 1);
}

// World search filters by Museum author/title metadata and selects the match.
{
  const m = worldSearch();
  assert.equal(handleModalKey(m, key("KeyD", "D")), "redraw");
  assert.equal(handleModalKey(m, key("KeyR", "r")), "redraw");
  assert.equal(m.query, "Dr");
  assert.equal(handleModalKey(m, key("Enter", "Enter")), "close");
  assert.equal(m.picked.world, "TEEN");
}

// Backspace updates the search query and resets to the first result.
{
  const m = worldSearch();
  handleModalKey(m, key("KeyC", "c"));
  handleModalKey(m, key("ArrowDown", "ArrowDown"));
  assert.equal(handleModalKey(m, key("Backspace", "Backspace")), "redraw");
  assert.equal(m.query, "");
  assert.equal(m.selected, 0);
}

console.log("modal.test.mjs: all assertions passed");
