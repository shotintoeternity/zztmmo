// scene.ts — the 60x25 board as a three.js scene.
//
// Every square is classified from its glyph and attribute (classify.ts) and
// becomes one of three things: a floor tile, a block textured with its own
// glyph on every face, or a standing sprite card that always faces the camera.
// All of them draw through one pair of shaders that read the CP437 atlas: a
// texel is foreground where the glyph has ink and background elsewhere, so a
// yellow "▓" wall is a yellow-on-black wall and a white "☻" on blue is the
// player's card, exactly the colors the text screen would show.
//
// Geometry is rebuilt whenever the cells change (a few times a second, from
// server diffs), never per frame: the billboard math runs in the vertex
// shader, so a static buffer stays correct as the camera moves.

import * as THREE from "three";
import { classify, isBlock, type CellShape } from "./classify";
import { CELL_H, CELL_W, GLYPH_COLS, GLYPH_ROWS, type Font } from "./font";
import { hexRGB, paletteRGB } from "./palette";
import { BOARD_COLS, COLS, ROWS } from "./overlay";
import type { PlayerSnapshot, ScreenCell } from "./protocol";

/**
 * A text cell is 8 pixels wide and 14 tall, and the world keeps that ratio
 * everywhere so a glyph is never stretched: a tile is 1 wide and 1.75 deep, a
 * wall is 1.75 tall, and a sprite card is 1 wide and 1.75 tall. Seen from
 * straight above, the board has exactly the proportions of the text screen.
 */
export const TILE_DEPTH = CELL_H / CELL_W;
export const SPRITE_HEIGHT = TILE_DEPTH;
const FLOOR_DOT = 0xfa;
const WATER_DEPTH = -0.08;

const CHAR_PLAYER = 0x02;
const COLOR_PLAYER = 0x1f;

type RGB = [number, number, number];

const VERTEX_COMMON = `
  attribute vec3 fg;
  attribute vec3 bg;
  attribute float bgAlpha;
  attribute float shade;
  varying vec2 vUv;
  varying vec3 vFg;
  varying vec3 vBg;
  varying float vBgAlpha;
  varying float vShade;
  varying float vDist;
`;

const STATIC_VERTEX = `
  ${VERTEX_COMMON}
  void main() {
    vUv = uv;
    vFg = fg;
    vBg = bg;
    vBgAlpha = bgAlpha;
    vShade = shade;
    vec4 mv = modelViewMatrix * vec4(position, 1.0);
    vDist = -mv.z;
    gl_Position = projectionMatrix * mv;
  }
`;

// A card is anchored at its base and spans the camera's right and up vectors,
// so it faces the viewer from any angle and its feet stay on the floor.
const BILLBOARD_VERTEX = `
  ${VERTEX_COMMON}
  attribute vec2 corner;
  void main() {
    vUv = uv;
    vFg = fg;
    vBg = bg;
    vBgAlpha = bgAlpha;
    vShade = shade;
    vec3 camRight = vec3(viewMatrix[0][0], viewMatrix[1][0], viewMatrix[2][0]);
    vec3 camUp = vec3(viewMatrix[0][1], viewMatrix[1][1], viewMatrix[2][1]);
    vec3 world = position + camRight * corner.x + camUp * corner.y;
    vec4 mv = viewMatrix * vec4(world, 1.0);
    vDist = -mv.z;
    gl_Position = projectionMatrix * mv;
  }
`;

const FRAGMENT = `
  uniform sampler2D atlas;
  uniform vec3 fogColor;
  uniform float fogNear;
  uniform float fogFar;
  varying vec2 vUv;
  varying vec3 vFg;
  varying vec3 vBg;
  varying float vBgAlpha;
  varying float vShade;
  varying float vDist;
  void main() {
    float ink = texture2D(atlas, vUv).a;
    if (ink < 0.5 && vBgAlpha < 0.5) {
      discard;
    }
    vec3 color = ink >= 0.5 ? vFg : vBg;
    color *= vShade;
    float fog = smoothstep(fogNear, fogFar, vDist);
    gl_FragColor = vec4(mix(color, fogColor, fog), 1.0);
  }
`;

class QuadBuffer {
  positions: number[] = [];
  uvs: number[] = [];
  fg: number[] = [];
  bg: number[] = [];
  bgAlpha: number[] = [];
  shade: number[] = [];
  corners: number[] = [];
  indices: number[] = [];
  private count = 0;

  /**
   * quad appends four vertices in top-left, top-right, bottom-right,
   * bottom-left order, textured with one glyph.
   */
  quad(points: [number, number, number][], glyph: number, fg: RGB, bg: RGB, opaque: boolean, shade: number, corners?: [number, number][], span: [number, number] = [0, 1]) {
    // span clips the glyph horizontally (fractions of its width), for a face
    // wider than one glyph that shows a second, partial one.
    const g0 = (glyph % GLYPH_COLS) / GLYPH_COLS;
    const v0 = Math.floor(glyph / GLYPH_COLS) / GLYPH_ROWS;
    const u0 = g0 + span[0] / GLYPH_COLS;
    const u1 = g0 + span[1] / GLYPH_COLS;
    const v1 = v0 + 1 / GLYPH_ROWS;
    const uv = [u0, v0, u1, v0, u1, v1, u0, v1];
    for (let i = 0; i < 4; i += 1) {
      this.positions.push(points[i][0], points[i][1], points[i][2]);
      this.uvs.push(uv[i * 2], uv[i * 2 + 1]);
      this.fg.push(fg[0], fg[1], fg[2]);
      this.bg.push(bg[0], bg[1], bg[2]);
      this.bgAlpha.push(opaque ? 1 : 0);
      this.shade.push(shade);
      if (corners) {
        this.corners.push(corners[i][0], corners[i][1]);
      }
    }
    const b = this.count;
    this.indices.push(b, b + 2, b + 1, b, b + 3, b + 2);
    this.count += 4;
  }

  toGeometry(withCorners: boolean): THREE.BufferGeometry {
    const geometry = new THREE.BufferGeometry();
    geometry.setAttribute("position", new THREE.Float32BufferAttribute(this.positions, 3));
    geometry.setAttribute("uv", new THREE.Float32BufferAttribute(this.uvs, 2));
    geometry.setAttribute("fg", new THREE.Float32BufferAttribute(this.fg, 3));
    geometry.setAttribute("bg", new THREE.Float32BufferAttribute(this.bg, 3));
    geometry.setAttribute("bgAlpha", new THREE.Float32BufferAttribute(this.bgAlpha, 1));
    geometry.setAttribute("shade", new THREE.Float32BufferAttribute(this.shade, 1));
    if (withCorners) {
      geometry.setAttribute("corner", new THREE.Float32BufferAttribute(this.corners, 2));
    }
    geometry.setIndex(this.indices);
    return geometry;
  }
}

const SHADE_TOP = 1.0;
const SHADE_SOUTH = 0.88;
const SHADE_EAST = 0.74;
const SHADE_WEST = 0.74;
const SHADE_NORTH = 0.58;

// An empty ZZT square is black, and on the text screen that is all it needs to
// be: the grid is implied by the characters sitting in it. In three dimensions
// a black floor is not a floor, it is a hole -- there is nothing to judge
// distance, speed or scale against, and in first person you walk over an
// absence. So the floor keeps ZZT's near-black but carries a lit dot at the
// centre of every square, which is the board's own grid and the only thing in
// the world that says how big a step is.
const FLOOR_BG: RGB = [0.075, 0.075, 0.095];
const FLOOR_DOT_FG: RGB = [0.42, 0.42, 0.5];
// A dark room's unseen squares: ZZT fills them with a grey ▒, and so does the
// floor here, dimly, so a dark board is a floor you cannot see across rather
// than nothing at all.
const FOG_BG: RGB = [0, 0, 0];
const FOG_FG: RGB = [0.13, 0.13, 0.15];
const GROUND_BG: RGB = [0.012, 0.012, 0.018];
const RIM_TOP: RGB = [0.22, 0.22, 0.26];
const RIM_SIDE: RGB = [0.14, 0.14, 0.17];
const BLACK: RGB = [0, 0, 0];

function dim(color: RGB, k: number): RGB {
  return [color[0] * k, color[1] * k, color[2] * k];
}

export type SceneBuildOptions = {
  roster: readonly PlayerSnapshot[];
  /** A 0-based screen cell whose sprite is not drawn: the viewer's own, in first person. */
  hide: { x: number; y: number } | null;
  /**
   * Cell indices (y * COLS + x) of the board message, which is written at the
   * bottom of the screen instead and would otherwise stand in the world twice.
   * They are drawn as plain floor.
   */
  textCells: ReadonlySet<number>;
};

export class BoardScene {
  readonly scene = new THREE.Scene();
  private readonly renderer: THREE.WebGLRenderer;
  private readonly atlas: THREE.CanvasTexture;
  private readonly staticMaterial: THREE.ShaderMaterial;
  private readonly billboardMaterial: THREE.ShaderMaterial;
  private staticMesh: THREE.Mesh | null = null;
  private billboardMesh: THREE.Mesh | null = null;
  private readonly uniforms: {
    atlas: { value: THREE.Texture };
    fogColor: { value: THREE.Color };
    fogNear: { value: number };
    fogFar: { value: number };
  };

  constructor(canvas: HTMLCanvasElement, font: Font) {
    this.renderer = new THREE.WebGLRenderer({ canvas, antialias: false, powerPreference: "high-performance" });
    this.renderer.setClearColor(0x000000, 1);
    this.atlas = new THREE.CanvasTexture(font.sheet);
    this.atlas.flipY = false;
    this.atlas.magFilter = THREE.NearestFilter;
    this.atlas.minFilter = THREE.NearestFilter;
    this.atlas.generateMipmaps = false;
    this.uniforms = {
      atlas: { value: this.atlas },
      fogColor: { value: new THREE.Color(0x000000) },
      fogNear: { value: 14 },
      fogFar: { value: 40 },
    };
    this.staticMaterial = new THREE.ShaderMaterial({
      uniforms: this.uniforms,
      vertexShader: STATIC_VERTEX,
      fragmentShader: FRAGMENT,
      side: THREE.DoubleSide,
    });
    this.billboardMaterial = new THREE.ShaderMaterial({
      uniforms: this.uniforms,
      vertexShader: BILLBOARD_VERTEX,
      fragmentShader: FRAGMENT,
      side: THREE.DoubleSide,
    });
  }

  setFog(near: number, far: number) {
    this.uniforms.fogNear.value = near;
    this.uniforms.fogFar.value = far;
  }

  resize(width: number, height: number, pixelRatio: number) {
    this.renderer.setPixelRatio(pixelRatio);
    this.renderer.setSize(width, height, false);
  }

  render(camera: THREE.Camera) {
    this.renderer.render(this.scene, camera);
  }

  /** build replaces the whole board geometry from the 80x25 cell grid's board columns. */
  build(cells: readonly ScreenCell[], options: SceneBuildOptions) {
    const shapes: CellShape[] = new Array(BOARD_COLS * ROWS);
    for (let y = 0; y < ROWS; y += 1) {
      for (let x = 0; x < BOARD_COLS; x += 1) {
        const cell = cells[y * COLS + x];
        const shape = classify(cell.ch, cell.color);
        if (shape.kind === "sprite" && options.textCells.has(y * COLS + x)) {
          shapes[y * BOARD_COLS + x] = { ...shape, kind: "empty", opaqueBg: false };
        } else {
          shapes[y * BOARD_COLS + x] = shape;
        }
      }
    }
    const tints = new Map<number, RGB>();
    for (const player of options.roster) {
      const rgb = hexRGB(player.color);
      if (rgb) {
        tints.set((player.y - 1) * BOARD_COLS + (player.x - 1), rgb);
      }
    }
    const blockAt = (x: number, y: number): number => {
      if (x < 0 || y < 0 || x >= BOARD_COLS || y >= ROWS) {
        return 0;
      }
      const shape = shapes[y * BOARD_COLS + x];
      return isBlock(shape) ? shape.height : 0;
    };

    const solid = new QuadBuffer();
    const cards = new QuadBuffer();
    this.surroundings(solid);

    for (let y = 0; y < ROWS; y += 1) {
      for (let x = 0; x < BOARD_COLS; x += 1) {
        const shape = shapes[y * BOARD_COLS + x];
        const cell = cells[y * COLS + x];
        const fg = paletteRGB(shape.fg);
        const bg = paletteRGB(shape.bg);
        const x0 = x;
        const x1 = x + 1;
        const z0 = y * TILE_DEPTH;
        const z1 = (y + 1) * TILE_DEPTH;
        const zm = z0 + 1; // one glyph's width along a side face

        switch (shape.kind) {
          case "empty":
            solid.quad([[x0, 0, z0], [x1, 0, z0], [x1, 0, z1], [x0, 0, z1]], FLOOR_DOT, FLOOR_DOT_FG, FLOOR_BG, true, SHADE_TOP);
            break;
          case "fog":
            solid.quad([[x0, 0, z0], [x1, 0, z0], [x1, 0, z1], [x0, 0, z1]], shape.glyph, FOG_FG, FOG_BG, true, SHADE_TOP);
            break;
          case "floor":
            // A coloured floor is ground you are standing on, so it is lit
            // like ground rather than dimmed towards the background.
            solid.quad([[x0, 0, z0], [x1, 0, z0], [x1, 0, z1], [x0, 0, z1]], FLOOR_DOT, dim(bg, 1.15), dim(bg, 0.8), true, SHADE_TOP);
            break;
          case "water":
            // Water is ground you can see across and not walk on, so it is
            // drawn at full strength: dimming it put a dark band in front of
            // you that read as nothing at all.
            solid.quad(
              [[x0, WATER_DEPTH, z0], [x1, WATER_DEPTH, z0], [x1, WATER_DEPTH, z1], [x0, WATER_DEPTH, z1]],
              shape.glyph,
              paletteRGB(0x09),
              paletteRGB(0x01),
              true,
              SHADE_TOP,
            );
            break;
          case "wall":
          case "low":
          case "forest":
          case "gate": {
            const h = shape.height * TILE_DEPTH;
            // A block is always opaque: a see-through wall is a hole, and a
            // black background is what the text screen shows there too.
            solid.quad([[x0, h, z0], [x1, h, z0], [x1, h, z1], [x0, h, z1]], shape.glyph, fg, bg, true, SHADE_TOP);
            if (blockAt(x, y + 1) < shape.height) {
              solid.quad([[x0, h, z1], [x1, h, z1], [x1, 0, z1], [x0, 0, z1]], shape.glyph, fg, bg, true, SHADE_SOUTH);
            }
            if (blockAt(x, y - 1) < shape.height) {
              solid.quad([[x1, h, z0], [x0, h, z0], [x0, 0, z0], [x1, 0, z0]], shape.glyph, fg, bg, true, SHADE_NORTH);
            }
            // The east and west faces are a tile deep, 1.75 glyphs wide: a
            // whole glyph and then three quarters of another, so the pattern
            // keeps its true proportions instead of stretching to fit.
            if (blockAt(x + 1, y) < shape.height) {
              solid.quad([[x1, h, z1], [x1, h, zm], [x1, 0, zm], [x1, 0, z1]], shape.glyph, fg, bg, true, SHADE_EAST);
              solid.quad([[x1, h, zm], [x1, h, z0], [x1, 0, z0], [x1, 0, zm]], shape.glyph, fg, bg, true, SHADE_EAST, undefined, [0, TILE_DEPTH - 1]);
            }
            if (blockAt(x - 1, y) < shape.height) {
              solid.quad([[x0, h, z0], [x0, h, zm], [x0, 0, zm], [x0, 0, z0]], shape.glyph, fg, bg, true, SHADE_WEST);
              solid.quad([[x0, h, zm], [x0, h, z1], [x0, 0, z1], [x0, 0, zm]], shape.glyph, fg, bg, true, SHADE_WEST, undefined, [0, TILE_DEPTH - 1]);
            }
            if (shape.height < 1) {
              // A short block stands on a visible floor.
              solid.quad([[x0, 0, z0], [x1, 0, z0], [x1, 0, z1], [x0, 0, z1]], FLOOR_DOT, FLOOR_DOT_FG, FLOOR_BG, true, SHADE_TOP);
            }
            break;
          }
          case "sprite": {
            solid.quad([[x0, 0, z0], [x1, 0, z0], [x1, 0, z1], [x0, 0, z1]], FLOOR_DOT, FLOOR_DOT_FG, FLOOR_BG, true, SHADE_TOP);
            if (options.hide && options.hide.x === x && options.hide.y === y) {
              break;
            }
            let cardBg = bg;
            let opaque = shape.opaqueBg;
            const tint = tints.get(y * BOARD_COLS + x);
            if (tint && cell.ch === CHAR_PLAYER && cell.color === COLOR_PLAYER) {
              cardBg = tint;
              opaque = true;
            }
            const cx = x + 0.5;
            const cz = (y + 0.5) * TILE_DEPTH;
            const center: [number, number, number] = [cx, 0, cz];
            cards.quad(
              [center, center, center, center],
              shape.glyph,
              fg,
              cardBg,
              opaque,
              SHADE_TOP,
              [[-0.5, SPRITE_HEIGHT], [0.5, SPRITE_HEIGHT], [0.5, 0], [-0.5, 0]],
            );
            break;
          }
        }
      }
    }

    this.replace(solid, cards);
  }

  // surroundings is the world beyond the board's edge: ZZT stops you at row 25
  // and column 60, and a horizon there reads better than a void. A dark ground
  // plane far past the edge, and a low rim marking the edge itself.
  private surroundings(solid: QuadBuffer) {
    const G = 120;
    const D = ROWS * TILE_DEPTH;
    const groundY = -0.02;
    solid.quad([[-G, groundY, -G], [BOARD_COLS + G, groundY, -G], [BOARD_COLS + G, groundY, D + G], [-G, groundY, D + G]], 0x20, BLACK, GROUND_BG, true, SHADE_TOP);
    const rim = 0.2;
    const rimW = 0.5;
    const box = (x0: number, x1: number, z0: number, z1: number) => {
      solid.quad([[x0, rim, z0], [x1, rim, z0], [x1, rim, z1], [x0, rim, z1]], 0x20, BLACK, RIM_TOP, true, SHADE_TOP);
      solid.quad([[x0, rim, z1], [x1, rim, z1], [x1, 0, z1], [x0, 0, z1]], 0x20, BLACK, RIM_SIDE, true, SHADE_SOUTH);
      solid.quad([[x1, rim, z0], [x0, rim, z0], [x0, 0, z0], [x1, 0, z0]], 0x20, BLACK, RIM_SIDE, true, SHADE_NORTH);
      solid.quad([[x1, rim, z1], [x1, rim, z0], [x1, 0, z0], [x1, 0, z1]], 0x20, BLACK, RIM_SIDE, true, SHADE_EAST);
      solid.quad([[x0, rim, z0], [x0, rim, z1], [x0, 0, z1], [x0, 0, z0]], 0x20, BLACK, RIM_SIDE, true, SHADE_WEST);
    };
    box(-rimW, BOARD_COLS + rimW, -rimW, 0);
    box(-rimW, BOARD_COLS + rimW, D, D + rimW);
    box(-rimW, 0, 0, D);
    box(BOARD_COLS, BOARD_COLS + rimW, 0, D);
  }

  private replace(solid: QuadBuffer, cards: QuadBuffer) {
    if (this.staticMesh) {
      this.scene.remove(this.staticMesh);
      this.staticMesh.geometry.dispose();
    }
    if (this.billboardMesh) {
      this.scene.remove(this.billboardMesh);
      this.billboardMesh.geometry.dispose();
    }
    this.staticMesh = new THREE.Mesh(solid.toGeometry(false), this.staticMaterial);
    this.staticMesh.frustumCulled = false;
    this.billboardMesh = new THREE.Mesh(cards.toGeometry(true), this.billboardMaterial);
    this.billboardMesh.frustumCulled = false;
    this.scene.add(this.staticMesh);
    this.scene.add(this.billboardMesh);
  }
}
