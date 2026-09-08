// camera.ts — two ways of looking at the board, and one continuous way of
// moving between them.
//
//   world    the board with depth. One camera on one zoom axis (zoom.ts): pull
//            all the way out for the whole board from the south, the way the
//            text screen shows it; push in to sit behind your ☻; push past the
//            last orbit step and you are standing inside your own square at eye
//            height, facing the way you last pushed. Drag to orbit, wheel to
//            zoom, F to stand up or step back out.
//   classic  the text screen itself: the regular ZZTMMO view, drawn flat.
//            This is what the client opens on. ZZTMMO is a text game and the
//            board it draws is the real one; 3D is somewhere you choose to go,
//            not somewhere you land before you have asked for anything.
//
// Overhead, chase and diorama used to be three of five modes on the V key.
// They were never three things — one orbit camera at three distances, with the
// wheel already moving between them — so they are gone as modes and kept as
// presets the ?view= parameter can still name. What is left on V is the only
// difference that was ever real: a world you look into, or a screen you read.
//
// The far end needs no special case: clampTarget's margins grow with distance
// until they meet in the middle of the board, so a camera far enough out stops
// following the player and frames the whole board on its own.
//
// World axes: x is the board column, z is the row (south is +z), y is up. A
// yaw of 0 looks north.

import * as THREE from "three";
import { TILE_DEPTH } from "./scene";
import { ORBIT_DEFAULT, ORBIT_MAX, toggleFirstPerson, zoomStep, type Zoom } from "./zoom";

export type ViewMode = "world" | "classic";
export const VIEW_MODES: readonly ViewMode[] = ["world", "classic"];

/** Facing as a compass index: 0 north, 1 east, 2 south, 3 west. */
export type Facing = 0 | 1 | 2 | 3;

/** The distances and angles the old mode names stood for, for ?view=. */
export const VIEW_PRESETS: Record<string, { dist: number; pitch: number; firstPerson: boolean }> = {
  overhead: { dist: ORBIT_DEFAULT, pitch: 1.12, firstPerson: false },
  chase: { dist: 15, pitch: 0.95, firstPerson: false },
  diorama: { dist: 60, pitch: 0.95, firstPerson: false },
  first: { dist: 15, pitch: 0.95, firstPerson: true },
};

// Eye height against 1.75-tall walls: a little over half, the Wolfenstein
// proportion, so a corridor reads as a corridor and a boulder as a boulder.
const EYE_HEIGHT = 0.95;
const FOV_ORBIT = 58;
const FOV_FIRST = 66;
// How long standing up (or stepping back out) takes. The zoom itself is
// continuous, so this is the only cut left, and it is worth spending a third
// of a second not to make it.
const BLEND_SECONDS = 0.35;
const BOARD_W = 60;
const BOARD_D = 25 * TILE_DEPTH;
// How fast a ghost drifts, in world units a second. It is a speed through the
// world rather than a number of cells a second, so crossing a row -- which is
// 1.75 deep -- honestly takes longer than crossing a column.
const GHOST_SPEED = 14;

/**
 * clampTarget keeps a following camera from looking off the board: when the
 * player nears an edge the view stops scrolling rather than showing half a
 * screen of nothing, the way a scrolling map does. The margin is what the view
 * covers, roughly, from its distance — so far enough out, the two margins meet
 * and the camera settles on the middle of the board.
 */
function clampTarget(goal: THREE.Vector3, dist: number) {
  const mx = Math.min(dist * 0.5, BOARD_W / 2);
  const mz = Math.min(dist * 0.36, BOARD_D / 2);
  goal.x = THREE.MathUtils.clamp(goal.x, mx, BOARD_W - mx);
  goal.z = THREE.MathUtils.clamp(goal.z, mz, BOARD_D - mz);
}

function lerpAngle(a: number, b: number, t: number): number {
  let d = (b - a) % (Math.PI * 2);
  if (d > Math.PI) d -= Math.PI * 2;
  if (d < -Math.PI) d += Math.PI * 2;
  return a + d * t;
}

function ease(t: number): number {
  return t * t * (3 - 2 * t);
}

export class CameraRig {
  readonly camera = new THREE.PerspectiveCamera(FOV_ORBIT, 1, 0.05, 300);
  mode: ViewMode = "classic";
  facing: Facing = 0;
  /** Where the camera is when it has left your body behind. */
  ghost = false;
  readonly ghostAt = new THREE.Vector3(30, 0, BOARD_D / 2);

  private zoomState: Zoom = { dist: ORBIT_DEFAULT, firstPerson: false };
  private yaw = 0;
  private pitch = 1.12;
  private readonly target = new THREE.Vector3(30, 0, 12.5);
  private readonly lookAt = new THREE.Vector3(30, 0, 12.5);
  private readonly orbitEye = new THREE.Vector3();
  private readonly orbitLook = new THREE.Vector3();
  private readonly firstEye = new THREE.Vector3();
  private readonly firstLook = new THREE.Vector3();
  private lookYaw = 0;
  private blend = 0;
  private snapNext = true;

  get firstPerson(): boolean {
    return this.zoomState.firstPerson;
  }

  get distance(): number {
    return this.zoomState.dist;
  }

  setMode(mode: ViewMode) {
    this.mode = mode;
    this.snapNext = true;
  }

  /**
   * V: a world you look into, or a screen you read.
   *
   * Arriving in the world puts you in it, at eye level -- choosing 3D over the
   * text screen is choosing to stand in the board rather than look down at it,
   * and an orbit camera is a third thing that is neither. That holds for the
   * first V of the session as much as the tenth: there is no establishing shot,
   * because the text screen was the establishing shot. The distance is kept, so
   * F backs you out to wherever you were watching from -- ORBIT_DEFAULT, the
   * overhead, until you have watched from somewhere else.
   */
  cycle(): ViewMode {
    const next: ViewMode = this.mode === "world" ? "classic" : "world";
    if (next === "world") {
      this.zoomState = { dist: this.zoomState.dist, firstPerson: true };
    }
    this.setMode(next);
    return this.mode;
  }

  /** applyPreset places the camera where one of the old mode names stood. */
  applyPreset(name: string): boolean {
    const preset = VIEW_PRESETS[name];
    if (!preset) {
      return false;
    }
    this.zoomState = { dist: preset.dist, firstPerson: preset.firstPerson };
    this.pitch = preset.pitch;
    this.blend = preset.firstPerson ? 1 : 0;
    this.snapNext = true;
    return true;
  }

  /**
   * setGhost leaves your body where it stands and takes the camera with you,
   * or brings it back. Coming back is deliberately not a snap: the camera
   * glides home, so you can see where your body was all along.
   */
  setGhost(on: boolean, playerX: number, playerZ: number) {
    if (on === this.ghost) {
      return;
    }
    this.ghost = on;
    if (on) {
      this.ghostAt.set(playerX, 0, playerZ);
    }
  }

  /**
   * driftGhost flies the camera itself. dx is right and dz is forward, each
   * -1..1, in the frame the player is looking through. It stops at the edges
   * of the board: there is nothing outside them to haunt.
   */
  driftGhost(dx: number, dz: number, dt: number) {
    if (!this.ghost || (dx === 0 && dz === 0)) {
      return;
    }
    const yaw = this.firstPerson ? this.lookYaw : this.yaw;
    const sin = Math.sin(yaw);
    const cos = Math.cos(yaw);
    const step = GHOST_SPEED * dt;
    this.ghostAt.x = THREE.MathUtils.clamp(this.ghostAt.x + (sin * dz + cos * dx) * step, 0, BOARD_W);
    this.ghostAt.z = THREE.MathUtils.clamp(this.ghostAt.z + (-cos * dz + sin * dx) * step, 0, BOARD_D);
  }

  /** snap skips the smoothing on the next update: a new board, not a walk. */
  snap() {
    this.snapNext = true;
  }

  turn(steps: number) {
    this.facing = ((((this.facing + steps) % 4) + 4) % 4) as Facing;
  }

  orbit(dx: number, dy: number) {
    if (this.firstPerson || this.mode === "classic") {
      return;
    }
    this.yaw -= dx * 0.005;
    this.pitch = THREE.MathUtils.clamp(this.pitch + dy * 0.005, 0.25, 1.5);
  }

  /** zoom returns true when it changed whether you are in first person. */
  zoom(delta: number): boolean {
    const before = this.zoomState.firstPerson;
    this.zoomState = zoomStep(this.zoomState, delta);
    return this.zoomState.firstPerson !== before;
  }

  /** standUp is the F key: into your own square, or back out to where you were. */
  standUp() {
    this.zoomState = toggleFirstPerson(this.zoomState);
  }

  /**
   * Fog that suits the distance, with floors: close in, a fog that scaled all
   * the way down would grey out the room you are standing in.
   */
  fog(): { near: number; far: number } {
    if (this.mode === "classic") {
      return { near: ORBIT_MAX * 2, far: ORBIT_MAX * 4 };
    }
    if (this.firstPerson) {
      return { near: 34, far: 95 };
    }
    const dist = this.zoomState.dist;
    return { near: Math.max(20, dist * 2.2), far: Math.max(55, dist * 5) };
  }

  /**
   * update moves the camera toward where it should be. Both placements are
   * computed every frame and blended, so standing up is a move rather than a
   * cut, and neither steady state pays for the other's smoothing.
   */
  update(dt: number, playerX: number, playerZ: number) {
    const k = this.snapNext ? 1 : 1 - Math.exp(-dt * 10);
    const turnK = this.snapNext ? 1 : 1 - Math.exp(-dt * 12);
    const goalBlend = this.firstPerson ? 1 : 0;
    if (this.snapNext) {
      this.blend = goalBlend;
    } else {
      const step = dt / BLEND_SECONDS;
      this.blend = goalBlend > this.blend
        ? Math.min(goalBlend, this.blend + step)
        : Math.max(goalBlend, this.blend - step);
    }
    this.snapNext = false;

    this.lookYaw = lerpAngle(this.lookYaw, (this.facing * Math.PI) / 2, turnK);
    const t = ease(this.blend);
    const fov = FOV_ORBIT + (FOV_FIRST - FOV_ORBIT) * t;
    if (Math.abs(this.camera.fov - fov) > 0.01) {
      this.camera.fov = fov;
      this.camera.updateProjectionMatrix();
    }

    // A ghost is already where it wants to be; only a body needs following.
    // The clamp is for a camera looking *at* you from a distance -- it stops
    // the view scrolling off the board. At eye level there is no distance and
    // no scrolling: the camera is you, and clamping it would stand you several
    // rows from your own body in a corner of the board.
    const goal = this.ghost
      ? this.ghostAt.clone()
      : new THREE.Vector3(playerX, 0, playerZ);
    if (!this.ghost && !this.firstPerson) {
      clampTarget(goal, this.zoomState.dist);
    }
    this.target.lerp(goal, this.ghost ? 1 : k);

    const dist = this.zoomState.dist;
    this.orbitEye.set(
      this.target.x - Math.sin(this.yaw) * Math.cos(this.pitch) * dist,
      Math.sin(this.pitch) * dist,
      this.target.z + Math.cos(this.yaw) * Math.cos(this.pitch) * dist,
    );
    this.orbitLook.set(this.target.x, 0.6, this.target.z);

    this.firstEye.set(this.target.x, EYE_HEIGHT, this.target.z);
    this.firstLook.set(
      this.firstEye.x + Math.sin(this.lookYaw),
      EYE_HEIGHT - 0.03,
      this.firstEye.z - Math.cos(this.lookYaw),
    );

    this.camera.position.copy(this.orbitEye).lerp(this.firstEye, t);
    this.lookAt.copy(this.orbitLook).lerp(this.firstLook, t);
    this.camera.lookAt(this.lookAt);
  }

  resize(aspect: number) {
    this.camera.aspect = aspect;
    this.camera.updateProjectionMatrix();
  }
}
