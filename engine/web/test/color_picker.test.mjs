// M19.2 — the color picker's rules, without a browser.
//
// The picker is pure (src/color_picker.ts), so everything that decides what a
// player ends up wearing is testable here: what each row means, what the arrows
// reach, what typing does, and what Enter and Escape submit. The browser suite
// (test/color_picker_journey.test.mjs) then proves the window is really on
// screen and really writes the key the join reads.

import assert from "node:assert/strict";
import { build } from "esbuild";

const output = await build({
  entryPoints: ["src/color_picker.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
const {
  colorPickerKey,
  colorPickerPreview,
  colorPickerValue,
  newColorPickerModal,
  renderColorPicker,
  CUSTOM_INDEX,
  DOS_PICKS,
  PREVIEW_CHAR,
  PREVIEW_COLOR,
  VANILLA_INDEX,
} = await import(`data:text/javascript;base64,${source}`);

function key(code, k = "", opts = {}) {
  return { code, key: k, ctrlKey: false, metaKey: false, altKey: false, ...opts };
}

function picker(current = "") {
  const m = newColorPickerModal(current, (color) => {
    m.submitted = color;
    m.submitCount = (m.submitCount ?? 0) + 1;
  });
  m.submitted = undefined;
  return m;
}

const type = (m, text) => {
  for (const ch of text) {
    colorPickerKey(m, key(`Key${ch.toUpperCase()}`, ch));
  }
};

// ---------------------------------------------------------------------------
// The 16 quick picks are the DOS palette, and they are the palette this client
// already paints with — a purist picking "Cyan" must get the cyan they have been
// looking at since 1991, not a fresh approximation of it. (main.ts's `ega`.)
// ---------------------------------------------------------------------------
const EGA = [
  "#000000", "#0000aa", "#00aa00", "#00aaaa", "#aa0000", "#aa00aa", "#aa5500", "#aaaaaa",
  "#555555", "#5555ff", "#55ff55", "#55ffff", "#ff5555", "#ff55ff", "#ffff55", "#ffffff",
];
assert.equal(DOS_PICKS.length, 16, "all sixteen DOS colors are offered");
assert.deepEqual(DOS_PICKS.map((pick) => pick.hex), EGA, "the quick picks ARE the EGA palette");
assert.equal(new Set(DOS_PICKS.map((pick) => pick.name)).size, 16, "every pick is named, and named once");
// ZZT's own names for the bright half (ColorNames, GAME.PAS:92): in ZZT "Blue"
// has always meant the bright one, so the dark half is what gets qualified.
assert.equal(DOS_PICKS[9].name, "Blue");
assert.equal(DOS_PICKS[14].name, "Yellow");
assert.equal(DOS_PICKS[15].name, "White");
assert.equal(DOS_PICKS[1].name, "Dark Blue");

// ---------------------------------------------------------------------------
// Opening: the window opens on what the player is already wearing.
// ---------------------------------------------------------------------------
{
  assert.equal(picker("").selected, VANILLA_INDEX, "an unpicked player opens on the vanilla row");

  const quick = picker("#55ffff");
  assert.equal(quick.selected, 11, "a DOS color opens on its own quick pick");
  assert.equal(quick.custom, "", "and does not pre-fill the hex field");

  const custom = picker("#7F3FBF");
  assert.equal(custom.selected, CUSTOM_INDEX, "an arbitrary color opens on the hex field");
  assert.equal(custom.custom, "7f3fbf", "with the color in it, ready to be edited");

  const junk = picker("rebeccapurple");
  assert.equal(junk.selected, VANILLA_INDEX, "a stored value that is not a color is no pick at all");
  assert.equal(junk.current, "");
}

// ---------------------------------------------------------------------------
// What a row is worth. This is what Enter submits, what the preview shows, and
// what refuses to be submitted at all.
// ---------------------------------------------------------------------------
{
  const m = picker("");
  m.selected = 0;
  assert.equal(colorPickerValue(m), "#000000");
  m.selected = VANILLA_INDEX;
  assert.equal(colorPickerValue(m), "", "the vanilla row is the empty color, not a color");
  m.selected = CUSTOM_INDEX;
  m.custom = "ff88";
  assert.equal(colorPickerValue(m), null, "four digits are not a color yet");
  m.custom = "ff8800";
  assert.equal(colorPickerValue(m), "#ff8800");
}

// ---------------------------------------------------------------------------
// The arrows reach every row, which is what makes the window work on a phone:
// M16.18a's d-pad sends exactly these codes and nothing else.
// ---------------------------------------------------------------------------
{
  // Top to bottom: the vanilla default, the grid, the hex field.
  const m = picker("");
  assert.equal(m.selected, VANILLA_INDEX, "the window opens on the default, at the top");
  colorPickerKey(m, key("ArrowUp"));
  assert.equal(m.selected, VANILLA_INDEX, "the top row is the top row");
  colorPickerKey(m, key("ArrowDown"));
  assert.equal(m.selected, 0, "down from the default is the grid's top-left");
  colorPickerKey(m, key("ArrowUp"));
  assert.equal(m.selected, VANILLA_INDEX, "and up from the grid's top row is the default again");

  colorPickerKey(m, key("ArrowDown"));
  for (let i = 0; i < 7; i += 1) colorPickerKey(m, key("ArrowDown"));
  assert.equal(m.selected, 7, "the left column is eight rows");
  colorPickerKey(m, key("ArrowDown"));
  assert.equal(m.selected, CUSTOM_INDEX, "the hex field is the row below the grid");
  colorPickerKey(m, key("ArrowDown"));
  assert.equal(m.selected, CUSTOM_INDEX, "the last row is the last row");
  colorPickerKey(m, key("ArrowUp"));
  assert.equal(m.selected, 7, "and the way back up lands on the foot of the grid");

  // Columns. The grid is two columns of eight: 0..7 dark, 8..15 bright.
  m.selected = 3;
  colorPickerKey(m, key("ArrowRight"));
  assert.equal(m.selected, 11, "right crosses to the same row of the bright column");
  colorPickerKey(m, key("ArrowRight"));
  assert.equal(m.selected, 11, "and stops at the edge");
  colorPickerKey(m, key("ArrowLeft"));
  assert.equal(m.selected, 3);
  colorPickerKey(m, key("ArrowLeft"));
  assert.equal(m.selected, 3, "the left edge holds too");
  m.selected = CUSTOM_INDEX;
  colorPickerKey(m, key("ArrowLeft"));
  colorPickerKey(m, key("ArrowRight"));
  assert.equal(m.selected, CUSTOM_INDEX, "the full-width rows have no columns to cross");

  // Every one of the 18 rows is reachable with the pad alone, from the row the
  // window opens on. Sweep it rather than trusting the moves above.
  const reachable = new Set();
  const walk = picker("");
  for (const step of ["ArrowUp", "ArrowLeft", "ArrowDown", "ArrowRight"]) {
    for (let i = 0; i < 40; i += 1) {
      colorPickerKey(walk, key(step));
      reachable.add(walk.selected);
      // Comb the other axis at each stop so the sweep covers the grid, not a line.
      for (let j = 0; j < 20; j += 1) {
        colorPickerKey(walk, key(step === "ArrowUp" || step === "ArrowDown" ? "ArrowRight" : "ArrowDown"));
        reachable.add(walk.selected);
      }
      for (let j = 0; j < 20; j += 1) {
        colorPickerKey(walk, key(step === "ArrowUp" || step === "ArrowDown" ? "ArrowLeft" : "ArrowUp"));
        reachable.add(walk.selected);
      }
    }
  }
  assert.equal(reachable.size, VANILLA_INDEX + 1, `the pad alone reaches every row, got ${[...reachable].sort((a, b) => a - b)}`);
}

// ---------------------------------------------------------------------------
// Typing a color. A hex digit means the same thing wherever the cursor is —
// the worldSearch precedent — and nothing else in this window is a shortcut.
// ---------------------------------------------------------------------------
{
  const m = picker("");
  m.selected = 4;
  type(m, "ff8800");
  assert.equal(m.selected, CUSTOM_INDEX, "typing a digit moves the cursor to the field it fills");
  assert.equal(m.custom, "ff8800");
  assert.equal(colorPickerValue(m), "#ff8800");

  // A seventh digit restarts: someone correcting a typo is retyping a color,
  // not appending to one.
  colorPickerKey(m, key("KeyA", "a"));
  assert.equal(m.custom, "a");

  // Case is not information — the wire and the storage carry lower case.
  m.custom = "";
  type(m, "FF00AA");
  assert.equal(m.custom, "ff00aa");
  assert.equal(colorPickerValue(m), "#ff00aa");

  // '#' is drawn by the window, never stored, so a pasted "#ff8800" types the
  // same as "ff8800" does.
  assert.equal(colorPickerKey(m, key("Digit3", "#")), "redraw");
  assert.equal(m.custom, "");
  type(m, "ff8800");
  assert.equal(colorPickerValue(m), "#ff8800");

  assert.equal(colorPickerKey(m, key("Backspace")), "redraw");
  assert.equal(m.custom, "ff880");
  assert.equal(colorPickerValue(m), null, "backspacing takes it back below a color");

  // Not-a-hex-digit is ignored rather than swallowed into the field.
  const before = m.custom;
  for (const [code, k] of [["KeyG", "g"], ["KeyZ", "z"], ["Slash", "/"], ["Space", " "]]) {
    assert.equal(colorPickerKey(m, key(code, k)), "ignore", `${k} is not a hex digit`);
  }
  assert.equal(m.custom, before);
  // A browser shortcut is the browser's, not the field's.
  assert.equal(colorPickerKey(m, key("KeyA", "a", { ctrlKey: true })), "ignore");
  assert.equal(m.custom, before);
}

// ---------------------------------------------------------------------------
// Enter and Escape — what actually reaches localStorage and the join.
// ---------------------------------------------------------------------------
{
  const quick = picker("");
  quick.selected = 12;
  assert.equal(colorPickerKey(quick, key("Enter")), "close");
  assert.equal(quick.submitted, "#ff5555", "a quick pick submits its own hex like any other color");

  const typed = picker("");
  type(typed, "7f3fbf");
  assert.equal(colorPickerKey(typed, key("Enter")), "close");
  assert.equal(typed.submitted, "#7f3fbf");

  // A half-typed hex is not a color: Enter refuses and the window stays open,
  // rather than closing on a color nobody chose.
  const half = picker("");
  type(half, "7f3");
  assert.equal(colorPickerKey(half, key("Enter")), "ignore");
  assert.equal(half.submitted, undefined, "nothing is submitted");
  assert.equal(half.submitCount, undefined);

  // The way back to vanilla. "" is a deliberate answer; null is Escape's.
  const off = picker("#ff0000");
  off.selected = VANILLA_INDEX;
  assert.equal(colorPickerKey(off, key("Enter")), "close");
  assert.equal(off.submitted, "", "the vanilla row submits the empty color");

  const escaped = picker("#ff0000");
  escaped.selected = 3;
  assert.equal(colorPickerKey(escaped, key("Escape")), "close");
  assert.equal(escaped.submitted, null, "Escape changes nothing, and says so with null");
}

// ---------------------------------------------------------------------------
// The preview is the real paint path: char 2 in 0x1F with a 24-bit background
// over it, which is exactly what the board draws and exactly what M19.1's tint
// gate accepts. A preview that could not be tinted would be a promise the game
// does not keep.
// ---------------------------------------------------------------------------
{
  const m = picker("");
  m.selected = 9;
  const preview = colorPickerPreview(m);
  assert.ok(preview, "a selected color previews");
  assert.equal(preview.rgb, "#5555ff");

  const cells = [];
  renderColorPicker((x, y, color, text) => cells.push({ x, y, color, text }), m);
  const at = cells.find((cell) => cell.x === preview.x && cell.y === preview.y);
  assert.ok(at, "the preview cell is drawn where colorPickerPreview says it is");
  assert.equal(at.text, String.fromCharCode(PREVIEW_CHAR), "the preview glyph is the player, char 2");
  assert.equal(at.color, PREVIEW_COLOR, "in the one attribute the M19.1 tint gate accepts");

  m.selected = VANILLA_INDEX;
  assert.equal(colorPickerPreview(m), null, "the vanilla row previews an untinted 0x1F ☻, which is vanilla");
  m.selected = CUSTOM_INDEX;
  m.custom = "abc";
  assert.equal(colorPickerPreview(m), null, "a half-typed hex previews nothing rather than something wrong");
}

// ---------------------------------------------------------------------------
// The window on screen: the M4.1 furniture, inside its own frame, naming every
// color it offers.
// ---------------------------------------------------------------------------
{
  const m = picker("#00aaaa");
  const cells = [];
  renderColorPicker((x, y, color, text) => cells.push({ x, y, color, text }), m);
  const text = cells.map((cell) => cell.text).join("\n");

  for (const pick of DOS_PICKS) {
    assert.ok(cells.some((cell) => cell.text === pick.name), `${pick.name} is on the window`);
  }
  assert.match(text, /Any color: /, "the hex field is on the window");
  assert.match(text, /Default \(white on blue\)/, "so is the way back to the vanilla player");
  assert.match(text, /Esc cancels/, "and the window says how to leave it");

  // Nothing may leave the window: the board keeps running underneath (the M1.3
  // de-modal deviation), so a stray write is a hole punched in a live screen.
  for (const cell of cells) {
    assert.ok(cell.x >= 5, `"${cell.text}" starts left of the window frame`);
    assert.ok(cell.x + cell.text.length <= 55, `"${cell.text}" runs past the window's right edge`);
    assert.ok(cell.y >= 3 && cell.y <= 21, `"${cell.text}" is drawn outside the window's rows`);
  }

  // The interior is the window's own blue, filled before anything is written on
  // it. renderTextWindow gets this from drawLine; a window that lays out its own
  // interior has to do it, and the first version of this one did not — the
  // frame's black showed between the text and the picker was the only window on
  // screen that did not match the others.
  for (let y = 6; y <= 20; y += 1) {
    const fill = cells.find((cell) => cell.y === y && cell.x === 7 && cell.text.trim() === "" && cell.text.length > 40);
    assert.ok(fill, `interior row ${y} is filled before it is written on`);
    assert.equal(fill.color, 0x1e, `interior row ${y} is filled in the window's own attribute`);
  }

  // The frame is drawn, and it is the shared one: strTop's corner piece.
  assert.ok(cells.some((cell) => cell.text.startsWith("\xc6\xd1")), "the window is the M4.1 CP437 frame");

  // Each swatch is its color as FOREGROUND (attribute i, background black), so
  // the block reads as the color and dark blue does not vanish into the window.
  for (let i = 0; i < DOS_PICKS.length; i += 1) {
    const swatch = cells.find((cell) => cell.color === i && cell.text.includes("\xfe"));
    assert.ok(swatch, `${DOS_PICKS[i].name} has a swatch in its own color`);
  }
}

console.log("color_picker.test.mjs: all assertions passed");
