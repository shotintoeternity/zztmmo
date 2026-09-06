// overlay.ts — the 80x25 CP437 layer drawn over the 3D view.
//
// The sidebar lives in columns 60..79 and is opaque; text windows, prompts and
// the pause label are written over the board columns and only their own cells
// are painted, so the 3D world shows through everywhere else. Two layers: the
// base (sidebar chrome and HUD counters, which persist) and the top (modals
// and messages, rebuilt from scratch each time something changes).

import { CELL_H, CELL_W, GLYPH_COLS, type Font } from "./font";
import { paletteColor } from "./palette";
import type { WriteText } from "./sidebar";

export const COLS = 80;
export const ROWS = 25;
export const BOARD_COLS = 60;
export const WIDTH = COLS * CELL_W;
export const HEIGHT = ROWS * CELL_H;

type Cell = { ch: number; color: number };

export class Overlay {
  private readonly base = new Map<number, Cell>();
  private readonly top = new Map<number, Cell>();
  private dirty = true;

  constructor(private readonly canvas: HTMLCanvasElement) {
    canvas.width = WIDTH;
    canvas.height = HEIGHT;
  }

  /** writeBase paints a persistent cell: the sidebar. */
  readonly writeBase: WriteText = (x, y, color, text) => {
    this.put(this.base, x, y, color, text);
  };

  /** writeTop paints a cell of the transient layer, cleared by clearTop. */
  readonly writeTop: WriteText = (x, y, color, text) => {
    this.put(this.top, x, y, color, text);
  };

  clearTop() {
    if (this.top.size > 0) {
      this.top.clear();
      this.dirty = true;
    }
  }

  private put(layer: Map<number, Cell>, x: number, y: number, color: number, text: string) {
    for (let i = 0; i < text.length; i += 1) {
      const cx = x + i;
      if (cx < 0 || cx >= COLS || y < 0 || y >= ROWS) {
        continue;
      }
      layer.set(y * COLS + cx, { ch: text.charCodeAt(i) & 0xff, color });
    }
    this.dirty = true;
  }

  /** draw repaints the canvas if anything changed since the last draw. */
  draw(font: Font) {
    if (!this.dirty) {
      return;
    }
    this.dirty = false;
    const ctx = this.canvas.getContext("2d");
    if (!ctx) {
      return;
    }
    ctx.imageSmoothingEnabled = false;
    ctx.clearRect(0, 0, WIDTH, HEIGHT);
    for (let i = 0; i < COLS * ROWS; i += 1) {
      const cell = this.top.get(i) ?? this.base.get(i);
      if (!cell) {
        continue;
      }
      const x = (i % COLS) * CELL_W;
      const y = Math.floor(i / COLS) * CELL_H;
      const fg = cell.color & 0x0f;
      const bg = (cell.color >> 4) & 0x0f;
      ctx.fillStyle = paletteColor(bg);
      ctx.fillRect(x, y, CELL_W, CELL_H);
      const col = cell.ch % GLYPH_COLS;
      const row = Math.floor(cell.ch / GLYPH_COLS);
      ctx.drawImage(font.tinted[fg], col * CELL_W, row * CELL_H, CELL_W, CELL_H, x, y, CELL_W, CELL_H);
    }
  }
}
