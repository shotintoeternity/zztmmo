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
const { handleModalKey, renderModal, applyWorldOccupancy, worldOccupancyTotal, modalAcceptsTextInput } = await import(`data:text/javascript;base64,${source}`);

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
      { world: "LOBBY", id: "lobby", title: "ZZTMMO Lobby", author: "ZZTMMO", created: "2026", kind: "classic" },
      { world: "TOWN", id: "TOWN", title: "Town of ZZT", author: "Tim Sweeney", created: "1991", kind: "classic" },
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

// Empty world search lists every hosted world (scrollable), lobby first, with a
// match count that reflects all of them. The count is separate from the centered
// instruction. Museum search is reached by typing.
{
  const m = worldSearch();
  const writes = [];
  renderModal((x, y, color, text) => writes.push({ x, y, color, text }), m);
  const rendered = writes.map((write) => write.text).join(" ");
  // M27.1 reworded this line again — the first screen is shelves now, while
  // typing remains the way to reach worlds the first screen does not show.
  assert.match(rendered, /Type to search all\/Museum; shelves/);
  // The count sits on the blank line below the instruction (y=12), not on the
  // instruction row (y=11) where it used to overprint the header.
  const instructionWrite = writes.find((write) => write.text === "Type to search all/Museum; shelves");
  assert.ok(instructionWrite && instructionWrite.y === 11);
  // All seven fixture worlds are matched, not a featured subset.
  assert.ok(writes.some((write) => write.text === "7 matches" && write.x === 42 && write.y === 12));
  assert.ok(writes.some((write) => write.color === 0x70 && write.text.startsWith("Type to search: ")));
  assert.match(rendered, /ZZTMMO Lobby/);
  assert.match(rendered, /Rhygar/);
  assert.match(rendered, /by Saxxon Pike/);
  assert.doesNotMatch(rendered, /id:/);
  const searchWrites = writes.filter((write) => write.text.startsWith("Type to search: "));
  assert.equal(searchWrites.length, 1);
  assert.equal(searchWrites[0].y, 19, "search prompt stays below the result rows");
}

// A world with players online shows its live player count in the list.
{
  const m = worldSearch();
  m.selected = 2; // RHYGAR1, the first occupied world after the lobby and TOWN
  const writes = [];
  renderModal((x, y, color, text) => writes.push({ x, y, color, text }), m);
  assert.match(writes.map((write) => write.text).join(" "), /\(1 player currently online\)/);
}

// The empty-query view lists the whole library, not a capped handful: the old
// build showed only ~6 default worlds, hiding most of a large hosted catalog.
{
  const entries = [{ world: "TOWN", id: "TOWN", title: "TOWN", author: "Unknown", created: "" }];
  for (let i = 1; i <= 12; i += 1) {
    entries.push({ world: `WORLD${i}`, id: `world${i}`, title: `World ${i}`, author: "Nobody", created: "2000" });
  }
  const m = { kind: "worldSearch", title: "Select a World", query: "", selected: 0, entries, onSelect() {}, onQuery() {} };
  const writes = [];
  renderModal((x, y, color, text) => writes.push({ x, y, color, text }), m);
  assert.ok(writes.some((write) => write.text === "13 matches" && write.y === 12), "all 13 worlds are matched, not a featured subset");
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

// M17.11: editing occupancy renders the same way playing occupancy does, and an
// entry with only editors still gets its occupancy line — including the line
// accounting behind the selection highlight, which previously keyed off
// `players` alone and would have drifted for an editors-only entry.
{
  const entries = [
    { world: "AAA", id: "aaa", title: "Aaa", author: "Nobody", created: "2000", editors: 1 },
    { world: "BBB", id: "bbb", title: "Bbb", author: "Nobody", created: "2000", players: 2, editors: 3 },
    { world: "CCC", id: "ccc", title: "Ccc", author: "Nobody", created: "2000" },
  ];
  const render = (selected) => {
    const m = { kind: "worldSearch", title: "Select a World", query: "", selected, entries, onSelect() {}, onQuery() {} };
    const writes = [];
    renderModal((x, y, color, text) => writes.push({ x, y, color, text }), m);
    return writes;
  };

  const writes = render(0);
  const text = writes.map((w) => w.text).join("\n");
  assert.ok(text.includes("1 editor)"), `editors-only world shows its count: ${text}`);
  assert.ok(text.includes("2 players currently online, 3 editors)"), `both counts read together: ${text}`);
  assert.ok(!/\(0 (players|editors)/.test(text), "zero counts are never printed");

  // Each occupied entry occupies three lines (title, byline, occupancy), so the
  // two occupied entries above CCC push it down by six. The list scrolls with
  // the selection, so assert the spacing rather than absolute rows — that is
  // what worldSearchLinePos has to agree with, and it keyed off `players` alone
  // before M17.11, which would have mis-measured the editors-only entry.
  for (const selected of [0, 2]) {
    const rows = render(selected);
    const rowOf = (needle) => {
      const w = rows.find((write) => write.text.includes(needle));
      return w ? w.y : -1;
    };
    assert.ok(rowOf("Aaa") > 0 && rowOf("Ccc") > 0, "entries are rendered");
    assert.equal(rowOf("Ccc") - rowOf("Aaa"), 6, "two occupied entries take three lines each");
    assert.equal(rowOf("Bbb") - rowOf("Aaa"), 3, "the editors-only entry still gets its occupancy line");
  }
}

// M17.11: the counts are live. A picker left open tracks people arriving and
// leaving, and the same listing sums into the title screen's server-wide total.
{
  const local = [
    { world: "TOWN", id: "town", title: "Town", author: "Tim", created: "1991", players: 2, editors: 1, source: "local" },
    { world: "QUIET", id: "quiet", title: "Quiet", author: "Nobody", created: "2000", source: "local" },
  ];
  const museum = [
    { world: "FARAWAY", id: "faraway", title: "Faraway", author: "Someone", created: "1994", source: "museum" },
  ];
  const onScreen = [...local, ...museum];

  assert.deepEqual(worldOccupancyTotal(onScreen), { players: 2, editors: 1 }, "the total spans every world");

  // Someone left TOWN, someone else opened the editor on QUIET.
  applyWorldOccupancy(onScreen, [
    { world: "TOWN", players: 1, editors: 1 },
    { world: "QUIET", editors: 2 },
  ]);
  assert.deepEqual(
    onScreen.map((entry) => [entry.world, entry.players ?? 0, entry.editors ?? 0]),
    [["TOWN", 1, 1], ["QUIET", 0, 2], ["FARAWAY", 0, 0]],
    "counts follow the fresh listing",
  );
  assert.deepEqual(worldOccupancyTotal(onScreen), { players: 1, editors: 3 }, "the total follows too");

  // A local world nobody is in reports nothing; a Museum entry the server does
  // not host is left alone rather than being invented as an empty world.
  applyWorldOccupancy(onScreen, []);
  assert.deepEqual(
    onScreen.map((entry) => [entry.world, entry.players ?? 0, entry.editors ?? 0]),
    [["TOWN", 0, 0], ["QUIET", 0, 0], ["FARAWAY", 0, 0]],
    "an empty listing empties the local worlds",
  );
  assert.equal(onScreen[2].players, undefined, "the Museum entry never gained counts");
  assert.deepEqual(worldOccupancyTotal(onScreen), { players: 0, editors: 0 });

  // Titles and provenance belong to whoever built the entry, not to a refresh.
  assert.equal(onScreen[0].title, "Town");
  assert.equal(onScreen[2].source, "museum");
}

console.log("modal.test.mjs: M17.11 live occupancy passed");

// M18.9 — the picker's first screen is curated. Production listed 134 worlds,
// 69 of them uncatalogued files rendering as "by Local ????". Those are still
// hosted and still joinable; they are reached by typing rather than filling the
// first click.
{
  const entries = [
    { world: "WELCOME", id: "welcome", title: "Welcome to ZZTMMO", author: "ZZTMMO", created: "2026", kind: "classic" },
    { world: "LOBBY", id: "lobby", title: "ZZTMMO Lobby", author: "ZZTMMO", created: "2026", kind: "classic" },
    { world: "TOWN", id: "town", title: "Town of ZZT", author: "Tim Sweeney", created: "1991", kind: "classic" },
    { world: "CAVES", id: "caves", title: "Caves of ZZT", author: "Tim Sweeney", created: "1991", kind: "classic" },
    { world: "MOSSGATE", id: "mossgate", title: "MOSSGATE", author: "Dreamed here", created: "", kind: "dreamed" },
    { world: "MERC", id: "merc", title: "MERC", author: "Local", created: "", kind: "local" },
    { world: "PR0N4U", id: "pr0n4u", title: "PR0N4U", author: "Local", created: "", kind: "local" },
  ];
  const render = (query, selected = 0) => {
    const m = { kind: "worldSearch", title: "Select a World", query, selected, entries, onSelect() {}, onQuery() {} };
    const writes = [];
    renderModal((x, y, color, text) => writes.push({ x, y, color, text }), m);
    return writes.map((w) => w.text).join("\n");
  };

  const firstScreen = render("");
  assert.match(firstScreen, /Welcome to ZZTMMO/, "the welcome world leads the first screen");
  assert.match(firstScreen, /Start here/, "the welcome world is marked for newcomers");
  assert.match(firstScreen, /ZZTMMO Lobby/, "the lobby leads the first screen");
  assert.match(firstScreen, /Caves of ZZT/, "catalogued classics are listed");
  assert.doesNotMatch(firstScreen, /MERC/, "uncatalogued worlds are left to search");
  assert.doesNotMatch(firstScreen, /PR0N4U/, "…including ones whose names read badly on a first screen");
  assert.match(firstScreen, /5 matches/, "the count reflects the curated list");
  assert.match(render("", 4), /MOSSGATE/, "worlds this server dreamed are listed in the curated set");

  // Hidden is not gone: typing still finds them, which is the whole bargain.
  const searched = render("merc");
  assert.match(searched, /MERC/, "an uncatalogued world is still reachable by name");
  // The lobby is appended to every search result, so a single hit reads as two.
  assert.match(searched, /2 matches/);
  assert.match(render("pr0n"), /PR0N4U/, "nothing is removed from the catalogue");

  // The instruction has to say that typing reaches more than what is shown.
  assert.match(firstScreen, /Type to search all/);
}

// M27.1 — when /api/worlds supplies shelves, the empty picker shows those shelf
// titles in server order; typing still searches the flat list.
{
  const entries = [
    { world: "WELCOME", id: "welcome", title: "Welcome to ZZTMMO", author: "ZZTMMO", created: "2026", kind: "classic" },
    { world: "LOBBY", id: "lobby", title: "ZZTMMO Lobby", author: "ZZTMMO", created: "2026", kind: "classic" },
    { world: "TOWN", id: "town", title: "Town of ZZT", author: "Tim Sweeney", created: "1991", kind: "classic" },
    { world: "ALPHA", id: "alpha", title: "Alpha Keep", author: "Ada", created: "2026", favorite: true, playCount: 3 },
    { world: "HIDDEN", id: "hidden", title: "Hidden Local", author: "Local", created: "", kind: "local" },
  ];
  const picked = [];
  const toggles = [];
  const m = {
    kind: "worldSearch",
    title: "Select a World",
    query: "",
    selected: 2,
    entries,
    shelves: [
      { id: "start", title: "Start here", worlds: ["WELCOME"] },
      { id: "lobby", title: "Lobby", worlds: ["LOBBY"] },
      { id: "favorites", title: "Favorites", worlds: ["ALPHA"] },
      { id: "classics", title: "Classics", worlds: ["TOWN"] },
    ],
    onSelect(entry) { picked.push(entry.world); },
    onFavorite(entry, favorite) {
      toggles.push([entry.world, favorite]);
      return true;
    },
  };
  const writes = [];
  renderModal((x, y, color, text) => writes.push({ x, y, color, text }), m);
  const firstScreen = writes.map((w) => w.text).join("\n");
  assert.match(firstScreen, /Start here/);
  assert.match(firstScreen, /Favorites/);
  assert.match(firstScreen, /\* Alpha Keep/);
  assert.match(firstScreen, /3 plays/);
  assert.doesNotMatch(firstScreen, /Hidden Local/);

  assert.equal(handleModalKey(m, key("Tab", "Tab")), "redraw");
  assert.deepEqual(toggles, [["ALPHA", false]]);
  assert.equal(entries[3].favorite, false);
  assert.equal(handleModalKey(m, key("Enter", "Enter")), "close");
  assert.deepEqual(picked, ["ALPHA"]);

  const guest = { ...m, selected: 0, onFavorite() { return false; } };
  assert.equal(handleModalKey(guest, key("Tab", "Tab")), "redraw");
  assert.equal(entries[0].favorite, undefined, "a refused guest toggle stays uncommitted");

  m.query = "hidden";
  m.selected = 0;
  const searched = [];
  renderModal((x, y, color, text) => searched.push({ x, y, color, text }), m);
  assert.match(searched.map((w) => w.text).join("\n"), /Hidden Local/, "search still reaches an unshelved world");
}

// Classics come before dreams on the first screen, each keeping server order.
{
  const entries = [
    { world: "LOBBY", id: "lobby", title: "ZZTMMO Lobby", author: "ZZTMMO", created: "2026", kind: "classic" },
    { world: "TOWN", id: "town", title: "Town of ZZT", author: "Tim Sweeney", created: "1991", kind: "classic" },
    { world: "ARCHIVE", id: "archive", title: "ARCHIVE", author: "Dreamed here", created: "", kind: "dreamed" },
    { world: "CAVES", id: "caves", title: "Caves of ZZT", author: "Tim Sweeney", created: "1991", kind: "classic" },
  ];
  const m = { kind: "worldSearch", title: "Select a World", query: "", selected: 0, entries, onSelect() {}, onQuery() {} };
  const writes = [];
  renderModal((x, y, color, text) => writes.push({ x, y, color, text }), m);
  const text = writes.map((w) => w.text).join("\n");
  assert.ok(
    text.indexOf("Caves of ZZT") < text.indexOf("ARCHIVE"),
    "a catalogued classic sorts above a dreamed world",
  );
}

// An entry with no kind at all — an older server, or the bare-string world
// list — must still be shown. The filter fails open: a world is never hidden
// because the server did not say what it was.
{
  const entries = [
    { world: "LOBBY", id: "lobby", title: "ZZTMMO Lobby", author: "ZZTMMO", created: "2026", kind: "classic" },
    { world: "MYSTERY", id: "mystery", title: "Mystery", author: "Unknown", created: "" },
  ];
  const m = { kind: "worldSearch", title: "Select a World", query: "", selected: 0, entries, onSelect() {}, onQuery() {} };
  const writes = [];
  renderModal((x, y, color, text) => writes.push({ x, y, color, text }), m);
  assert.match(writes.map((w) => w.text).join("\n"), /Mystery/, "an unclassified world stays visible");
}

// M19.2 — the colour picker is a modal like any other: the router renders it,
// routes its keys, and swallows every key it does not use. The picker's own
// rules live in color_picker.test.mjs; what is asserted here is that they are
// wired to the one router, so nothing reaches the title menu underneath.
{
  const m = {
    kind: "colorPicker",
    title: "Your Player Colour",
    selected: 17,
    custom: "",
    current: "",
    submitted: undefined,
    onSubmit(color) {
      this.submitted = color;
    },
  };
  const writes = [];
  renderModal((x, y, color, text) => writes.push({ x, y, color, text }), m);
  assert.match(writes.map((w) => w.text).join("\n"), /Your Player Colour/, "renderModal draws the picker");

  assert.equal(handleModalKey(m, key("ArrowUp")), "redraw", "the router routes the picker's keys");
  // 'P' would start the game if it reached the title menu behind this window.
  assert.equal(handleModalKey(m, key("KeyP", "p")), "ignore");
  assert.equal(m.submitted, undefined);
  assert.equal(handleModalKey(m, key("Escape")), "close");
  assert.equal(m.submitted, null, "Escape cancels rather than picking");

  // The hex field is text input, so the soft keyboard can be raised over it.
  assert.equal(modalAcceptsTextInput(m), true);
  assert.equal(modalAcceptsTextInput(null), false);
}

console.log("modal.test.mjs: M18.9 curated world picker passed");
