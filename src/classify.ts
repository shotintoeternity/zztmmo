// classify.ts — what a screen cell becomes in three dimensions.
//
// The server never tells this client which element sits on a square; it sends
// the CP437 byte and DOS attribute it drew there, exactly as the 2D client
// receives them. So the 3D shape of a cell is inferred from its glyph and
// color, the same way a player's eye infers it from the text screen. A fake
// wall looks like a wall here for the same reason it does in ZZT: it is drawn
// with the wall's glyph. That is the joke working, not a limitation.
//
// The glyph table comes from ElementDefs in engine/elements.go (the Pascal's
// ELEMENTS.PAS): solid 219, normal and fake 178, breakable 177, water and forest
// 176, boulder 254, sliders 18 and 29, passage 240, door 10, and the Line
// element's sixteen-entry draw table. Everything that is not a block stands up
// as a sprite: creatures, items, the player, scrolls, objects, and text.
//
// This module is pure so it can be tested under node without a browser.

export type CellKind =
  | "empty" // black floor
  | "floor" // a flat tile in a color (a blank with a background)
  | "wall" // full-height block textured with its glyph
  | "low" // a short block: boulders, sliders, ricochets
  | "forest" // knee-high green
  | "water" // a recessed blue plane
  | "fog" // a dark room's unseen square
  | "gate" // door or passage: full height, glyph on a colored face
  | "fake" // a wall's pattern lying down: floor you walk through
  | "sprite"; // a standing card

export type CellShape = {
  kind: CellKind;
  /** Block height in tile units; 0 for flat kinds. */
  height: number;
  /** The glyph textured onto the shape. */
  glyph: number;
  /** DOS foreground nibble. */
  fg: number;
  /** DOS background nibble. */
  bg: number;
  /** Whether the background color is painted behind the glyph. */
  opaqueBg: boolean;
};

/**
 * E_FAKE, from the engine's gamevars.go. The fake is one of ZZT's two floor
 * materials -- it and the empty are the only elements every creature moves
 * through freely -- but ElementDefs draws it with the normal wall's own
 * character, so it is the one shape no terminal can read off the screen. The
 * server names it in ScreenCell.element; without that this is a wall, which is
 * what it looked like here before.
 */
export const E_FAKE = 27;

export const WALL_HEIGHT = 1;
export const LOW_HEIGHT = 0.6;
export const FOREST_HEIGHT = 0.45;

/** The Line element's draw table (elements.go ElementLineDraw): every glyph a line wall can show. */
export const LINE_GLYPHS: ReadonlySet<number> = new Set([
  0xf9, 0xd0, 0xd2, 0xba, 0xb5, 0xbc, 0xbb, 0xb9, 0xc6, 0xc8, 0xc9, 0xcc, 0xcd, 0xcf, 0xcb, 0xce,
]);

const CH_SOLID = 0xdb;
const CH_NORMAL = 0xb2;
const CH_BREAKABLE = 0xb1;
const CH_SHADE = 0xb0;
const CH_BOULDER = 0xfe;
const CH_SLIDER_NS = 0x12;
const CH_SLIDER_EW = 0x1d;
const CH_STAR_OR_RICOCHET = 0x2a; // '*': ricochet, and slime
const CH_PASSAGE = 0xf0;
const CH_DOOR = 0x0a;

/** The attribute a dark room draws over every square a torch does not reach (game.go TileToColorAndChar). */
export const FOG_COLOR = 0x07;
export const FOG_GLYPH = CH_SHADE;

export function classify(ch: number, color: number, element = 0): CellShape {
  const fg = color & 0x0f;
  const bg = (color >> 4) & 0x0f;
  const shape = (kind: CellKind, height: number, opaqueBg: boolean): CellShape => ({ kind, height, glyph: ch, fg, bg, opaqueBg });

  // Asked before the glyph is read, because the glyph would lie. A fake keeps
  // the exact pattern it was drawn with -- it is simply lying down, which is
  // what a floor is.
  if (element === E_FAKE) {
    return shape("fake", 0, bg !== 0);
  }

  if (ch === 0x20 || ch === 0x00 || ch === 0xff) {
    return bg !== 0 ? shape("floor", 0, true) : shape("empty", 0, false);
  }
  if (ch === CH_SHADE) {
    if (color === FOG_COLOR) {
      return shape("fog", 0, false);
    }
    if (bg === 0x02) {
      // Forest is black on green (attribute 0x20).
      return shape("forest", FOREST_HEIGHT, true);
    }
    if (fg === 0x09) {
      // Water is light blue on a blinking white (0xF9); the blink bit is what
      // makes the background nibble 15 rather than 7.
      return shape("water", 0, true);
    }
    // A revealed invisible wall, or shading used as a wall.
    return shape("wall", WALL_HEIGHT, bg !== 0);
  }
  if (ch === CH_SOLID || ch === CH_NORMAL || ch === CH_BREAKABLE || LINE_GLYPHS.has(ch)) {
    return shape("wall", WALL_HEIGHT, bg !== 0);
  }
  if (ch === CH_BOULDER || ch === CH_SLIDER_NS || ch === CH_SLIDER_EW || ch === CH_STAR_OR_RICOCHET) {
    return shape("low", LOW_HEIGHT, bg !== 0);
  }
  if (ch === CH_PASSAGE || ch === CH_DOOR) {
    return shape("gate", WALL_HEIGHT, true);
  }
  return shape("sprite", 0, bg !== 0);
}

/** True for the kinds that stand as a block and hide the faces of a neighbor at or below their height. */
export function isBlock(shape: CellShape): boolean {
  return shape.height > 0;
}
