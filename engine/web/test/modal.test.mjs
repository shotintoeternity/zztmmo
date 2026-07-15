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
const { handleModalKey, renderModal } = await import(`data:text/javascript;base64,${source}`);

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
      { world: "TOWN", id: "TOWN", title: "TOWN (ZZTMMO Lobby)", author: "Unknown", created: "" },
      { world: "RHYGAR1", id: "rhygar1", title: "Rhygar", author: "Saxxon Pike", created: "1997", players: 1 },
      { world: "CASTLE", id: "castle", title: "Castle", author: "Unknown", created: "1999", players: 2 },
      { world: "TEEN", id: "teen", title: "Teen Priest", author: "Draco", created: "1998" },
      { world: "CUTLASS", id: "cutlass", title: "Tales of Adventure: The Treasure of Captain Cutlass", author: "Dr. Dos", created: "2001" },
      { world: "CAVES", id: "caves", title: "Caves", author: "Potter", created: "1991" },
    ],
    picked: null,
    onSelect(entry) {
      this.picked = entry;
    },
    queries: [],
    onQuery(query) {
      this.queries.push(query);
    },
  };
}

function prompt() {
  return {
    kind: "multilineEntry",
    title: "Dream a world",
    buffer: "",
    submitted: null,
    onSubmit(text) { this.submitted = text; },
  };
}

function chat() {
  return {
    kind: "chat",
    title: "Global Chat",
    messages: ["<Ada> a message that should wrap on a word boundary inside the ZZT text window"],
    buffer: "",
    submitted: null,
    onSubmit(text) { this.submitted = text; },
  };
}

function scroll() {
  return {
    kind: "text",
    state: {
      title: "Vendor",
      lines: ["Hello, you must be new to town!", "!ba;Ammunition, 3 shots.........1 gem", "!bt;Torch.......................1 gem"],
      linePos: 2,
      viewingFile: false,
    },
    baseTitle: "Vendor",
    moved: false,
    selectable: true,
    selected: null,
    onSelect(label) { this.selected = label; },
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

// Dream is one logical prompt: visual wrapping does not insert a newline and
// Enter starts generation rather than creating a second editor line.
{
  const m = prompt();
  for (const char of "a moonlit castle above a very wide underground sea") {
    handleModalKey(m, key("KeyX", char));
  }
  assert.equal(handleModalKey(m, key("Enter", "Enter")), "close");
  assert.equal(m.submitted, "a moonlit castle above a very wide underground sea");
  const writes = [];
  renderModal((x, y, color, text) => writes.push({ x, y, color, text }), m);
  assert.ok(writes.some((write) => write.text.includes("Enter: dream")));
}

// Chat composes directly in its scroll window. Both history and a long active
// message wrap at words, while Enter sends the unbroken logical message.
{
  const m = chat();
  for (const char of "this is a long message that wraps instead of needing a manual line break") {
    handleModalKey(m, key("KeyX", char));
  }
  const writes = [];
  renderModal((x, y, color, text) => writes.push({ x, y, color, text }), m);
  assert.ok(writes.some((write) => write.text === "Type a message; Enter sends:"));
  assert.ok(writes.some((write) => write.text === "<Ada> a message that should wrap on a word"));
  assert.equal(handleModalKey(m, key("Enter", "Enter")), "close");
  assert.equal(m.submitted, "this is a long message that wraps instead of needing a manual line break");
}

// A scroll hyperlink returns its OOP label—not its visible caption—on Enter.
// The main client sends that one label as the scroll reply before closing.
{
  const m = scroll();
  assert.equal(handleModalKey(m, key("Enter", "Enter")), "close");
  assert.equal(m.selected, "ba");
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

// Empty world search shows its Museum instruction, featured local games, and
// occupied rooms. The count is separate from the centered instruction.
{
  const m = worldSearch();
  const writes = [];
  renderModal((x, y, color, text) => writes.push({ x, y, color, text }), m);
  const rendered = writes.map((write) => write.text).join(" ");
  assert.match(rendered, /Type below to search the museum!/);
  // The count sits on the blank line below the instruction (y=12), not on the
  // instruction row (y=11) where it used to overprint "museum!" into "muse6 matches".
  const instructionWrite = writes.find((write) => write.text === "Type below to search the museum!");
  assert.ok(instructionWrite && instructionWrite.y === 11);
  assert.ok(writes.some((write) => write.text === "6 matches" && write.x === 42 && write.y === 12));
  assert.ok(writes.some((write) => write.color === 0x70 && write.text.startsWith("Type to search: ")));
  assert.match(rendered, /TOWN \(ZZTMMO Lobby\)/);
  assert.match(rendered, /Rhygar/);
  assert.match(rendered, /Caves/);
  assert.doesNotMatch(rendered, /id:/);
  assert.match(rendered, /by Saxxon Pike/);
  const searchWrites = writes.filter((write) => write.text.startsWith("Type to search: "));
  assert.equal(searchWrites.length, 1);
  assert.equal(searchWrites[0].y, 19, "search prompt stays below the result rows");
}

// Existing players remain in the default list after the featured local games.
{
  const m = worldSearch();
  m.selected = 3;
  const writes = [];
  renderModal((x, y, color, text) => writes.push({ x, y, color, text }), m);
  assert.match(writes.map((write) => write.text).join(" "), /\(1 player currently online\)/);
}

// World search filters by Museum author/title metadata and selects the match.
{
  const m = worldSearch();
  assert.equal(handleModalKey(m, key("KeyD", "D")), "redraw");
  assert.equal(handleModalKey(m, key("KeyR", "r")), "redraw");
  assert.equal(m.query, "Dr");
  assert.deepEqual(m.queries, ["D", "Dr"]);
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
  assert.deepEqual(m.queries, ["c", ""]);
}

// Museum entries live in the same ZZT-style world selector and can be selected.
{
  const m = worldSearch();
  m.entries.push({
    world: "ZIGZAG",
    id: "zzt_zigzag",
    title: "Zigzag and the Crystal Maze",
    author: "Benco",
    created: "1997-04-01",
    source: "museum",
    letter: "z",
    filename: "zigzag.zip",
  });
  handleModalKey(m, key("KeyB", "B"));
  assert.equal(handleModalKey(m, key("Enter", "Enter")), "close");
  assert.equal(m.picked.source, "museum");
  assert.equal(m.picked.filename, "zigzag.zip");
}

console.log("modal.test.mjs: all assertions passed");
