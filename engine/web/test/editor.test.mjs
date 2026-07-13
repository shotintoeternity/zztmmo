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
const { drawEditorSidebar } = await import(`data:text/javascript;base64,${source}`);

// A fake sidebar surface that records the text written at each row, so a test
// can assert the strings the sidebar paints without a canvas.
function surface() {
  const rows = new Map();
  const write = (x, y, _color, text) => {
    const line = rows.get(y) ?? "";
    // Pad to x then overlay text, mirroring absolute-column writes.
    const padded = line.padEnd(x, " ");
    rows.set(y, padded.slice(0, x) + text + padded.slice(x + text.length));
  };
  return {
    write,
    row: (y) => rows.get(y) ?? "",
    text: () => [...rows.values()].join("\n"),
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

console.log("editor.test.mjs: all assertions passed");
