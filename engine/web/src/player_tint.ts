// player_tint.ts — M19.1: which screen cells get a player's 24-bit background.
//
// Deliberately free of the DOM, the canvas and the socket (the editor_cursor.ts
// pattern), so the rule that decides what gets tinted can be unit-tested under
// Node without a browser.
//
// THE RULE, and why it is written this way. A cell is tinted only where the
// roster places a player AND the cell the server actually drew there is still
// the vanilla player: char 2 in attribute 0x1F. Deriving the tint from what was
// DRAWN rather than from the roster alone is what makes the three visibility
// rules fall out instead of needing three special cases:
//
//   * a dark room hides the player, because the server sent a different cell
//     there — nothing to tint, and darkness is not reimplemented here;
//   * an energizer blink wins, because ElementPlayerTick writes 0x0F and then
//     the cycling attribute (elements.go), so the attribute test fails on the
//     frames where the player is flashing;
//   * a dead player mid-respawn is not drawn at all, so again there is nothing
//     to tint.
//
// The colour never touches Board.Tiles, StateHash or a recording; it is a paint
// pass over a screen the server already decided.

export const PLAYER_TINT_CHAR = 0x02;
export const PLAYER_TINT_COLOR = 0x1f;

/** The fields of a PlayerSnapshot this module needs. Board coordinates are 1-based. */
export type TintRosterEntry = {
  id: number;
  x: number;
  y: number;
  color?: string;
};

/** The fields of a ScreenCell this module needs. Screen coordinates are 0-based. */
export type TintScreenCell = {
  x: number;
  y: number;
  ch: number;
  color: number;
};

export type TintCell = { x: number; y: number; rgb: string };

const HEX_COLOR = /^#[0-9a-fA-F]{6}$/;

/**
 * True for the one wire format a colour may take. The server validates the same
 * shape at the join (SanitizePlayerColor), so this is the second of two gates
 * rather than the only one — but it is the gate closest to the fillStyle, and a
 * roster is other people's input.
 */
export function isPlayerColor(color: string | undefined): color is string {
  return typeof color === "string" && HEX_COLOR.test(color);
}

/**
 * The cells to paint, one per player whose ☻ is on screen where the roster says
 * it is. Board column x is drawn at screen column x-1 (the same mapping the
 * pause blink uses, GAME.PAS:1518-1533).
 *
 * boardCols bounds the viewport: the sidebar is never tinted, whatever a roster
 * claims.
 */
export function playerTintCells({
  roster,
  cells,
  boardCols,
}: {
  roster: readonly TintRosterEntry[];
  cells: readonly TintScreenCell[];
  boardCols: number;
}): TintCell[] {
  const wanted = new Map<string, string>();
  for (const player of roster) {
    if (!isPlayerColor(player.color)) {
      continue;
    }
    const sx = player.x - 1;
    const sy = player.y - 1;
    if (sx < 0 || sy < 0 || sx >= boardCols) {
      continue;
    }
    wanted.set(`${sx},${sy}`, player.color);
  }
  if (wanted.size === 0) {
    return [];
  }

  const out: TintCell[] = [];
  for (const cell of cells) {
    if (cell.ch !== PLAYER_TINT_CHAR || cell.color !== PLAYER_TINT_COLOR) {
      continue;
    }
    const rgb = wanted.get(`${cell.x},${cell.y}`);
    if (rgb !== undefined) {
      out.push({ x: cell.x, y: cell.y, rgb });
    }
  }
  return out;
}

/**
 * The DOS attribute to draw the glyph in over a tinted cell: white (0x0F) on a
 * dark background, black (0x00) on a light one. Both are already pre-tinted
 * font canvases in main.ts, so no new glyph tinting is needed.
 *
 * Rec. 601 luma, which is what the eye reads as brightness; the 0.55 threshold
 * keeps a saturated blue white-on and a saturated yellow black-on, which is the
 * pair that matters (the vanilla player is white on blue).
 */
export function playerTintForeground(rgb: string): number {
  if (!isPlayerColor(rgb)) {
    return 0x0f;
  }
  const r = parseInt(rgb.slice(1, 3), 16) / 255;
  const g = parseInt(rgb.slice(3, 5), 16) / 255;
  const b = parseInt(rgb.slice(5, 7), 16) / 255;
  const luma = 0.299 * r + 0.587 * g + 0.114 * b;
  return luma > 0.55 ? 0x00 : 0x0f;
}
