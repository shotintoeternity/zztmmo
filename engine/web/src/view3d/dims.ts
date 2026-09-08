// dims.ts — the screen's shape, and the structural types the 3D view reads.
//
// These mirror main.ts's own COLS/ROWS/BOARD_COLS rather than importing them:
// main.ts is the entry point, not a module anyone imports, and a view that
// reaches into it would be a cycle. They are ZZT's screen and have not changed
// since 1991.
//
// The types are structural on purpose. The view asks for the least it can read
// -- a cell's glyph, colour and element; a player's square and colour -- so
// main.ts's richer ScreenCell and PlayerSnapshot satisfy them without the view
// knowing anything about the client they came from.

export const COLS = 80;
export const ROWS = 25;
export const BOARD_COLS = 60;

/** A square of the text screen, as the 3D view needs to read it. */
export type ViewCell = {
  x: number;
  y: number;
  ch: number;
  color: number;
  /** gamevars.go's E_* number the square is SHOWING, or 0. See main.ts. */
  element?: number;
};

/** A player on the board, as the 3D view needs to read them. */
export type ViewPlayer = {
  x: number;
  y: number;
  color?: string;
};
