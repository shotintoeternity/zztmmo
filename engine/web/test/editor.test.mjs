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

console.log("editor.test.mjs: all assertions passed");
