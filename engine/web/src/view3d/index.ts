// view3d/index.ts — the 3D view as one object the client can switch on.
//
// The client already has a model and a view: `cells` is the 80x25 text screen,
// and drawScreen paints it. This is a second painter for the board half of that
// same model. It reads cells and never writes them, so the sidebar, the text
// windows, the notices, the board fade and every mode the client has go on
// working exactly as they did -- they are drawn by drawScreen on the canvas
// above this one, which clears the board columns to transparent and lets the
// world show through.
//
// Everything here is loaded on demand. three.js is 500KB and most players will
// never press V, so main.ts imports this module with `await import()` the first
// time somebody asks for it, and the everyday bundle does not carry it.

import { CameraRig, VIEW_PRESETS, type ViewMode } from "./camera";
import { BOARD_COLS, COLS, ROWS, type ViewCell, type ViewPlayer } from "./dims";
import { loadFont, type Font } from "./font";
import { BoardScene, TILE_DEPTH } from "./scene";
import { boardText, groupSigns, signInRange, type BoardText, type SignGroup } from "./text_runs";

/** What the client hands over on every frame it wants drawn. */
export type View3DState = {
  cells: readonly ViewCell[];
  roster: readonly ViewPlayer[];
  /** Cell index -> "#rrggbb", the 24-bit player colours drawScreen already resolves. */
  tints: ReadonlyMap<number, string>;
  /** The viewer's own square, 1-based as the protocol sends it, or null. */
  me: { x: number; y: number } | null;
};

// How close you must stand to read a sign, in the weighted cells signDistance
// counts: eight columns to the side of one, or four rows off it.
const SIGN_RANGE = 8;

export class View3D {
  readonly rig = new CameraRig();
  private readonly scene: BoardScene;
  private readonly canvas: HTMLCanvasElement;
  private state: View3DState = { cells: [], roster: [], tints: new Map(), me: null };
  private text: BoardText = { signs: [], message: null };
  private textCells: ReadonlySet<number> = new Set();
  private signGroups: SignGroup[] = [];
  private dirty = true;
  private raf = 0;
  private last = 0;
  private lastHide = "";
  /** Called after a rebuild, so the client can repaint the words on top. */
  onTextChanged: (() => void) | null = null;

  constructor(canvas: HTMLCanvasElement, font: Font) {
    this.canvas = canvas;
    // The font sheet belongs to the scene, which uploads it as a texture; this
    // object never draws a glyph itself.
    this.scene = new BoardScene(canvas, font);
  }

  /** The board message, for the client to write where ZZT puts it. */
  get message(): BoardText["message"] {
    return this.text.message;
  }

  /** The sign the viewer is standing at, read out at the bottom, or null. */
  signAtEye(): SignGroup | null {
    const eye = this.eyeCell();
    return eye ? signInRange(this.signGroups, eye.x, eye.y, COLS, SIGN_RANGE) : null;
  }

  /** Cells the scene is deliberately not standing up, so drawScreen can. */
  liftedCells(): ReadonlySet<number> {
    return this.textCells;
  }

  update(state: View3DState) {
    this.state = state;
    this.dirty = true;
  }

  /** snap skips the camera's smoothing once: a new board, not a walk. */
  snap() {
    this.rig.snap();
    this.dirty = true;
  }

  setMode(mode: ViewMode) {
    this.rig.setMode(mode);
    this.dirty = true;
  }

  applyPreset(name: string): boolean {
    return this.rig.applyPreset(name);
  }

  start() {
    if (this.raf !== 0) {
      return;
    }
    this.canvas.hidden = false;
    this.last = performance.now();
    this.raf = window.requestAnimationFrame((now) => this.frame(now));
  }

  stop() {
    if (this.raf !== 0) {
      window.cancelAnimationFrame(this.raf);
      this.raf = 0;
    }
    this.canvas.hidden = true;
  }

  resize(boardWidth: number, height: number) {
    const w = Math.max(1, boardWidth);
    const h = Math.max(1, height);
    this.scene.resize(w, h, Math.min(window.devicePixelRatio || 1, 2));
    this.rig.resize(w / h);
  }

  /**
   * eyeCell is the square the viewer reads from: where they stand, or where
   * they have drifted to, because reading is something eyes do and a ghost took
   * them with it. 0-based, as the scene and text runs count.
   */
  private eyeCell(): { x: number; y: number } | null {
    if (this.rig.ghost) {
      return { x: Math.floor(this.rig.ghostAt.x), y: Math.floor(this.rig.ghostAt.z / TILE_DEPTH) };
    }
    const me = this.state.me;
    return me && me.x > 0 ? { x: me.x - 1, y: me.y - 1 } : null;
  }

  private bodyX(): number {
    return this.state.me ? this.state.me.x - 0.5 : BOARD_COLS / 2;
  }

  private bodyZ(): number {
    return (this.state.me ? this.state.me.y - 0.5 : ROWS / 2) * TILE_DEPTH;
  }

  private frame(now: number) {
    const dt = Math.min(0.1, (now - this.last) / 1000);
    this.last = now;

    // Your own card is only in the way when you are behind your own eyes; a
    // ghost wants to see the body it left standing.
    const me = this.state.me;
    const hide = this.rig.mode === "world" && this.rig.firstPerson && !this.rig.ghost && me && me.x > 0
      ? { x: me.x - 1, y: me.y - 1 }
      : null;
    const hideKey = hide ? `${hide.x},${hide.y}` : "";

    if (this.dirty || hideKey !== this.lastHide) {
      // Signs and the board message are lifted out of the world here: a sign is
      // a row of letters lying on the floor, and from eye height a row of
      // letters is edge-on and unreadable, so the one you are standing at reads
      // itself out at the bottom instead.
      this.text = boardText(this.state.cells, COLS, BOARD_COLS, ROWS);
      this.textCells = new Set(this.text.message ? this.text.message.cells : []);
      this.signGroups = groupSigns(this.text.signs, COLS);
      this.scene.build(this.state.cells, {
        roster: this.state.roster,
        hide,
        textCells: this.textCells,
      });
      this.dirty = false;
      this.lastHide = hideKey;
      this.onTextChanged?.();
    }

    this.rig.update(dt, this.bodyX(), this.bodyZ());
    const fog = this.rig.fog();
    this.scene.setFog(fog.near, fog.far);
    this.scene.render(this.rig.camera);
    this.raf = window.requestAnimationFrame((next) => this.frame(next));
  }
}

export async function createView3D(canvas: HTMLCanvasElement): Promise<View3D> {
  const font = await loadFont();
  return new View3D(canvas, font);
}

export { VIEW_PRESETS };
export type { ViewMode };
