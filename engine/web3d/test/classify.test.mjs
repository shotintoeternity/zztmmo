import assert from "node:assert/strict";
import { build } from "esbuild";

const out = await build({ entryPoints: ["src/classify.ts"], bundle: true, format: "esm", platform: "node", write: false });
const { classify, isBlock, LINE_GLYPHS, WALL_HEIGHT, LOW_HEIGHT, FOREST_HEIGHT, FOG_COLOR, FOG_GLYPH } = await import(
  `data:text/javascript;base64,${Buffer.from(out.outputFiles[0].contents).toString("base64")}`
);

// An empty square is what the server sends for one: 0x0F, ' '.
assert.equal(classify(0x20, 0x0f).kind, "empty");
assert.equal(classify(0x20, 0x1f).kind, "floor", "a blank with a background is a colored tile");

// Walls, by glyph: solid, normal, breakable, and the Line element's whole table.
for (const ch of [0xdb, 0xb2, 0xb1, ...LINE_GLYPHS]) {
  const shape = classify(ch, 0x0e);
  assert.equal(shape.kind, "wall", `glyph ${ch} is a wall`);
  assert.equal(shape.height, WALL_HEIGHT);
  assert.ok(isBlock(shape));
}
// A fake wall is drawn with the normal wall's glyph, so it stands as one.
assert.equal(classify(0xb2, 0x0e).kind, "wall");

// The shaded glyph is four things depending on its color.
assert.equal(classify(FOG_GLYPH, FOG_COLOR).kind, "fog", "a dark room's unseen square");
assert.equal(classify(0xb0, 0x20).kind, "forest");
assert.equal(classify(0xb0, 0x20).height, FOREST_HEIGHT);
assert.equal(classify(0xb0, 0xf9).kind, "water");
assert.equal(classify(0xb0, 0x0e).kind, "wall", "a revealed invisible wall");

// Short blocks.
for (const ch of [0xfe, 0x12, 0x1d, 0x2a]) {
  const shape = classify(ch, 0x0f);
  assert.equal(shape.kind, "low", `glyph ${ch} is a short block`);
  assert.equal(shape.height, LOW_HEIGHT);
}

// Doors and passages are gates with their color on the face.
assert.equal(classify(0xf0, 0x2f).kind, "gate");
assert.equal(classify(0x0a, 0x1f).kind, "gate");
assert.equal(classify(0x0a, 0x1f).opaqueBg, true);

// Everything else stands up. The player is a white ☻ on a blue card.
const player = classify(0x02, 0x1f);
assert.equal(player.kind, "sprite");
assert.equal(player.fg, 0x0f);
assert.equal(player.bg, 0x01);
assert.equal(player.opaqueBg, true);
assert.ok(!isBlock(player));
// A lion is a red Ω on nothing.
const lion = classify(0xea, 0x0c);
assert.equal(lion.kind, "sprite");
assert.equal(lion.opaqueBg, false);
// A text element is a letter on a colored card.
assert.equal(classify("A".charCodeAt(0), 0x1f).opaqueBg, true);


// The fake wall: the one shape the glyph cannot name. ElementDefs draws E_FAKE
// with the normal wall's own 0xB2, so without the server saying so this is a
// wall -- and with it, it is the floor it behaves like.
assert.equal(classify(0xb2, 0x0e).kind, "wall", "no element named: the glyph is all there is");
assert.equal(classify(0xb2, 0x0e, 22).kind, "wall", "a normal wall stays a wall");
assert.equal(classify(0xb2, 0x0e, 27).kind, "fake", "a fake lies down");
assert.equal(classify(0xb2, 0x0e, 27).height, 0, "and nothing stands up on it");
assert.equal(classify(0xb2, 0x0e, 27).glyph, 0xb2, "keeping the pattern it was drawn with");
assert.equal(classify(0xb2, 0x0e, 27).fg, 0x0e, "and its colour");
// A fake can be any colour, and can hold any glyph an editor put there.
assert.equal(classify(0xdb, 0x0c, 27).kind, "fake", "including one drawn as a solid");
// Everything else is still read off the screen, element or no element.
assert.equal(classify(0xdb, 0x0c, 21).kind, "wall");
assert.equal(classify(0x20, 0x0f, 0).kind, "empty");

console.log("classify ok");
