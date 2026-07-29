import assert from "node:assert/strict";
import { build } from "esbuild";

// Bundle editor.ts under Node so M5.8's sidebar parity (the F4 "Enter text" and
// "Help" command rows, and the "Text entry" mode indicator) is exercised as
// pure rendering, the same way modal.test.mjs covers the code editor.
const output = await build({
  entryPoints: ["src/editor.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
const { drawEditorSidebar, editorMessageIsForBoard } = await import(`data:text/javascript;base64,${source}`);

// A fake sidebar surface that records the text written at each row, so a test
// can assert the strings the sidebar paints without a canvas. Colours are
// recorded per cell too, for the assertions that are about colour itself.
function surface() {
  const rows = new Map();
  const colors = new Map();
  const write = (x, y, color, text) => {
    const line = rows.get(y) ?? "";
    // Pad to x then overlay text, mirroring absolute-column writes.
    const padded = line.padEnd(x, " ");
    rows.set(y, padded.slice(0, x) + text + padded.slice(x + text.length));
    for (let i = 0; i < text.length; i += 1) {
      colors.set(`${x + i},${y}`, color);
    }
  };
  return {
    write,
    row: (y) => rows.get(y) ?? "",
    text: () => [...rows.values()].join("\n"),
    colorAt: (x, y) => colors.get(`${x},${y}`),
    // The screen row containing text, with its colour at each column, for
    // asserting that a name is painted in one specific attribute.
    colorOfText: (needle) => {
      for (const [y, line] of rows) {
        const x = line.indexOf(needle);
        if (x >= 0) return colors.get(`${x},${y}`);
      }
      return undefined;
    },
    rowOfText: (needle) => {
      for (const [y, line] of rows) {
        if (line.includes(needle)) return y;
      }
      return -1;
    },
  };
}

const inspect = { x: 5, y: 6, elementId: 0, element: "Empty", character: 32, color: 0x0f, hasStat: false };
const brush = { element: 21, character: 0xdb, color: 0x0e, copied: false };

// The command block lists the F1-F4 element/text keys and the Help key.
{
  const s = surface();
  drawEditorSidebar(s.write, inspect, brush, false, false);
  const all = s.text();
  assert.ok(all.includes("Item"), "F1 Item row present");
  assert.ok(all.includes("Creature"), "F2 Creature row present");
  assert.ok(all.includes("Terrain"), "F3 Terrain row present");
  assert.ok(all.includes("Enter text"), "F4 Enter text row present");
  assert.ok(all.includes("Help"), "H Help row present");
  assert.ok(all.includes("Quit"), "Q Quit row present");
}

// The mode line reflects text entry, drawing, and the resting state.
{
  const off = surface();
  drawEditorSidebar(off.write, inspect, brush, false, false);
  assert.ok(off.text().includes("Drawing off"), "resting mode is Drawing off");

  const drawing = surface();
  drawEditorSidebar(drawing.write, inspect, brush, true, false);
  assert.ok(drawing.text().includes("Drawing on"), "Tab draw mode shows Drawing on");

  const textMode = surface();
  drawEditorSidebar(textMode.write, inspect, brush, false, true);
  assert.ok(textMode.text().includes("Text entry"), "F4 shows Text entry");
  assert.ok(!textMode.text().includes("Drawing off"), "text entry suppresses the draw label");
}

// F1/F2/F3 open the element picker on the sidebar itself (EDITOR.PAS:808-842),
// not a modal: passing a category menu lists its elements over rows 3-20, each
// with a shortcut badge, name, and glyph, while the title and mode rows remain.
{
  const menu = {
    title: "Creature",
    items: [
      { shortcut: "L", name: "Lion", character: 0xea, color: 0x0c, categoryName: "Beasts" },
      { shortcut: "T", name: "Tiger", character: 0xe3, color: 0x0b },
      { shortcut: "O", name: "Object", character: 0x02, color: 0x0f },
    ],
  };
  const s = surface();
  drawEditorSidebar(s.write, inspect, brush, false, false, menu);
  const all = s.text();
  assert.ok(all.includes("Lion"), "creature name Lion listed on the sidebar");
  assert.ok(all.includes("Tiger"), "creature name Tiger listed on the sidebar");
  assert.ok(all.includes("Object"), "creature name Object listed on the sidebar");
  assert.ok(all.includes(" L "), "Lion shortcut badge rendered");
  assert.ok(all.includes("Beasts"), "category header rendered");
  assert.ok(all.includes("ZZT Editor"), "title row survives the picker overlay");
  // The command block that normally occupies rows 3-20 is overlaid: the "Board
  // Info" command line is gone while the picker is open.
  assert.ok(!all.includes("Board Info"), "command block hidden behind the picker");
}

// Transfer board is SidebarPromptChoice(true, 3, ...): it overlays only rows
// 3-5 with a horizontal choice prompt, not a scroll/text-window picker.
{
  const actionMenu = {
    title: "Transfer board:",
    selected: 1,
    items: [
      { shortcut: "I", label: "Import board" },
      { shortcut: "E", label: "Export board" },
    ],
  };
  const s = surface();
  drawEditorSidebar(s.write, inspect, brush, false, false, null, actionMenu);
  const all = s.text();
  assert.ok(all.includes("Transfer board:"), "sidebar action title rendered");
  assert.ok(all.includes("Import"), "first sidebar action rendered");
  assert.ok(all.includes("Export"), "selected sidebar action rendered");
  assert.ok(all.includes("Switch boards"), "lower command block survives transfer choice");
  assert.ok(all.includes("Drawing off"), "mode row survives the action menu overlay");
}

// Stat editing is EditorEditStat: the normal editor chrome is cleared, category
// and element name are written at rows 6-7, and parameter prompts are painted
// directly into the sidebar. There is no "Object settings" select-list.
{
  const statPrompt = {
    categoryName: "Creatures:",
    elementName: "Spinning Gun",
    items: [
      { kind: "slider", label: "Intelligence?", value: 4, active: true },
      { kind: "choice", label: "Firing type?", choices: ["Bullets", "Stars"], selected: 0, active: false },
    ],
  };
  const s = surface();
  drawEditorSidebar(s.write, inspect, brush, false, false, null, null, statPrompt);
  const all = s.text();
  assert.ok(all.includes("Creatures:"), "stat category rendered in sidebar");
  assert.ok(all.includes("Spinning Gun"), "stat element name rendered in sidebar");
  assert.ok(all.includes("Intelligence?"), "slider prompt rendered directly");
  assert.ok(all.includes("1....:....9"), "slider scale rendered directly");
  assert.ok(all.includes("Firing type?"), "choice prompt rendered directly");
  assert.ok(all.includes("Bullets Stars"), "choice labels rendered horizontally");
  assert.ok(!all.includes("Object settings"), "no fake object settings menu");
  assert.ok(!all.includes("Cycle"), "cycle is not a vanilla stat prompt");
  assert.ok(!all.includes("ZZT Editor"), "normal editor title is cleared during stat edit");
  assert.ok(!all.includes("Drawing off"), "mode row is cleared during stat edit");
}

// The sidebar readouts are chrome, not popups: EditorDrawSidebar paints the
// brush color name, cursor position, and hovered element on fixed rows, and over
// a stat swaps the Pos row for "x,y Stat N: P1/P2/P3" and relabels Space to
// "Edit stat" (editor.ts, EDITOR.PAS:158-186). These transitions are asserted so
// a regression that reroutes any of them into a modal is caught.
{
  const yellow = surface();
  drawEditorSidebar(yellow.write, inspect, brush, false, false);
  assert.ok(yellow.text().includes("Yellow"), "Color readout names the brush color");
  assert.ok(yellow.text().includes("Pos: 5,6"), "Pos readout tracks the cursor");
  assert.ok(yellow.text().includes("Empty"), "element readout names the hovered tile");
  assert.ok(yellow.text().includes("Plot"), "Space command reads Plot over open ground");

  const cyan = surface();
  drawEditorSidebar(cyan.write, inspect, { ...brush, color: 0x0b }, false, false);
  assert.ok(cyan.text().includes("Lt Cyan"), "Color readout follows a color change");

  const stat = surface();
  const statInspect = { ...inspect, element: "Object", elementId: 36, hasStat: true, statId: 3, p1: 7, p2: 1, p3: 4 };
  drawEditorSidebar(stat.write, statInspect, brush, false, false);
  const all = stat.text();
  assert.ok(all.includes("Stat 3"), "stat readout shows the stat index over an object");
  assert.ok(all.includes("7/1/4"), "stat readout shows P1/P2/P3");
  assert.ok(all.includes("Object"), "element readout names the object");
  assert.ok(all.includes("Edit stat"), "Space command relabels to Edit stat over a stat");
  assert.ok(!all.includes("Plot"), "Plot label suppressed over a stat");
}

// M17.12 — the sidebar paints the board this member is viewing, never a
// collaborator's. Two people in one editor session can be on different boards;
// an edit diff carries the board its cells and inspected tile belong to, and a
// diff for another board is dropped before it can reach the sidebar. Without
// this, a member on board 1 saw the element row name tiles that only exist on
// somebody else's board ("Element 53", keys and bombs that are not there).
{
  assert.equal(editorMessageIsForBoard(1, 1), true, "a diff for the viewed board applies");
  assert.equal(editorMessageIsForBoard(2, 1), false, "a diff for another board is dropped");
  assert.equal(editorMessageIsForBoard(0, 1), false, "the title board is a board like any other");
  // An older peer that sends no board is accepted, the pre-M17.12 behaviour.
  assert.equal(editorMessageIsForBoard(undefined, 1), true, "a diff with no board applies");
  assert.equal(editorMessageIsForBoard(1, undefined), true, "no viewing board means no filtering");

  // The sidebar renders from the viewer's own inspected tile. A collaborator's
  // diff on another board is filtered out, so the element row keeps naming what
  // is under this member's cursor.
  const mine = { ...inspect, element: "Empty", elementId: 0 };
  const theirs = { ...inspect, element: "Bomb", elementId: 37 };
  const applyDiff = (viewerBoardId, diff, current) =>
    editorMessageIsForBoard(diff.boardId, viewerBoardId) ? diff.inspect : current;

  const foreign = applyDiff(1, { boardId: 2, inspect: theirs }, mine);
  const s1 = surface();
  drawEditorSidebar(s1.write, foreign, brush, false, false);
  assert.ok(s1.text().includes("Empty"), "sidebar keeps this member's own board tile");
  assert.ok(!s1.text().includes("Bomb"), "another board's tile never reaches the sidebar");

  const own = applyDiff(1, { boardId: 1, inspect: theirs }, mine);
  const s2 = surface();
  drawEditorSidebar(s2.write, own, brush, false, false);
  assert.ok(s2.text().includes("Bomb"), "a same-board diff still updates the sidebar");
}

// M17.10 — the collaborator legend. M17.9 took names off the board, so the only
// thing identifying a collaborator is their cursor colour; the W panel is what
// maps that colour back to a name. The board itself must stay free of names.
{
  // The command block advertises the key, otherwise the panel does not exist as
  // far as anyone in the session is concerned.
  const chrome = surface();
  drawEditorSidebar(chrome.write, inspect, brush, false, false);
  assert.ok(chrome.text().includes("Who's here"), "W command row advertises the legend");
  assert.ok(chrome.text().includes(" W "), "W shortcut badge rendered");

  const presence = {
    here: [
      { name: "Alice", color: 0x0f, self: true },
      { name: "Bob", color: 0x0d, self: false },
    ],
    elsewhere: [{ name: "Carol", color: 0x0a, self: false }],
  };
  const s = surface();
  drawEditorSidebar(s.write, inspect, brush, false, false, null, null, null, presence);
  const all = s.text();
  assert.ok(all.includes("Alice (you)"), "your own entry names you and says so");
  assert.ok(all.includes("Bob"), "a collaborator on this board is named");
  assert.ok(all.includes("Carol"), "a collaborator on another board is named");
  assert.ok(all.includes("On this board:"), "visible cursors are sectioned as such");
  assert.ok(all.includes("On other boards:"), "cursors you cannot see are sectioned apart");
  assert.ok(!all.includes("Board Info"), "the panel overlays the command block, as the picker does");
  assert.ok(all.includes("ZZT Editor"), "title row survives the legend overlay");
  assert.ok(all.includes("Drawing off"), "mode row survives the legend overlay");

  // The point of the whole feature: the name carries the colour of the cursor it
  // belongs to. The palette is foreground-on-black so the cursor overlays the
  // board tile; on the blue sidebar the same hue moves onto the blue background,
  // exactly as the element picker does for dark glyphs.
  assert.equal(s.colorOfText("Bob"), 0x1d, "Bob's name is drawn in Bob's cursor hue");
  assert.equal(s.colorOfText("Carol"), 0x1a, "Carol's name is drawn in Carol's cursor hue");
  assert.equal(s.colorOfText("Alice (you)"), 0x1f, "your own entry is the white local cursor");
  // Each row is prefixed with the cursor glyph itself, in the same colour, so the
  // legend shows the mark being matched rather than just describing it.
  const bobRow = s.rowOfText("Bob");
  assert.equal(s.row(bobRow)[62], "\xc5", "the legend row carries the editor cursor glyph");
  assert.equal(s.colorAt(62, bobRow), 0x1d, "the glyph is in the collaborator's colour");

  // Alone in a session: one line, no empty "other boards" heading.
  const solo = surface();
  drawEditorSidebar(solo.write, inspect, brush, false, false, null, null, null, {
    here: [{ name: "Alice", color: 0x0f, self: true }],
    elsewhere: [],
  });
  assert.ok(solo.text().includes("Alice (you)"), "solo editor still sees their own entry");
  assert.ok(!solo.text().includes("On other boards:"), "no empty elsewhere section");

  // A session bigger than the panel must not read as a smaller one: the
  // overflow is counted rather than silently dropped.
  const crowd = surface();
  const many = Array.from({ length: 24 }, (_, i) => ({
    name: `Member${i}`,
    color: 0x0e,
    self: false,
  }));
  drawEditorSidebar(crowd.write, inspect, brush, false, false, null, null, null, { here: many, elsewhere: [] });
  const crowdText = crowd.text();
  assert.ok(/\+\d+ more/.test(crowdText), "members past the panel's rows are counted, not dropped");
  const listed = many.filter((member) => crowdText.includes(member.name)).length;
  const missing = Number(/\+(\d+) more/.exec(crowdText)[1]);
  assert.equal(listed + missing, many.length, "the overflow count accounts for exactly the unlisted members");
  assert.ok(crowdText.includes("Drawing off"), "the panel never spills past its rows into the mode row");

  // The element picker and the legend overlay the same rows; the picker wins, so
  // a stale legend cannot bleed through the list you are choosing from.
  const both = surface();
  const menu = { title: "Item", items: [{ shortcut: "K", name: "Key", character: 0x0c, color: 0x09 }] };
  drawEditorSidebar(both.write, inspect, brush, false, false, menu, null, null, presence);
  assert.ok(both.text().includes("Key"), "the picker renders");
  assert.ok(!both.text().includes("Bob"), "the legend does not overlap the open picker");
}

console.log("editor.test.mjs: all assertions passed");
