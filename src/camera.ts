// camera.ts — three ways of looking at the board.
//
//   chase    behind and above your ☻, north up, so the arrow keys still mean
//            what they mean on the text screen. Drag to orbit, wheel to zoom.
//   first    at eye height inside your square, facing the way you last pushed.
//            Left and right turn; up walks the way you face.
//   diorama  the whole board from the south, the way the text screen shows it,
//            with depth.
//
// World axes: x is the board column, z is the row (south is +z), y is up. A
// yaw of 0 looks north.

import * as THREE from "three";

export type ViewMode = "chase" | "first" | "diorama";
export const VIEW_MODES: readonly ViewMode[] = ["chase", "first", "diorama"];

/** Facing as a compass index: 0 north, 1 east, 2 south, 3 west. */
export type Facing = 0 | 1 | 2 | 3;

const CHASE = { pitch: 0.95, dist: 12.5, minDist: 3, maxDist: 30 };
const DIORAMA = { pitch: 0.95, dist: 46, minDist: 20, maxDist: 90 };
const EYE_HEIGHT = 0.72;
const BOARD_CENTER = new THREE.Vector3(30, 0, 12.5);

function lerpAngle(a: number, b: number, t: number): number {
  let d = (b - a) % (Math.PI * 2);
  if (d > Math.PI) d -= Math.PI * 2;
  if (d < -Math.PI) d += Math.PI * 2;
  return a + d * t;
}

export class CameraRig {
  readonly camera = new THREE.PerspectiveCamera(58, 1, 0.05, 300);
  mode: ViewMode = "chase";
  facing: Facing = 0;

  private yaw = 0;
  private pitch = CHASE.pitch;
  private dist = CHASE.dist;
  private dioramaYaw = 0;
  private dioramaPitch = DIORAMA.pitch;
  private dioramaDist = DIORAMA.dist;
  private readonly target = new THREE.Vector3(30, 0, 12.5);
  private readonly eye = new THREE.Vector3();
  private lookYaw = 0;
  private snapNext = true;

  setMode(mode: ViewMode) {
    this.mode = mode;
    this.snapNext = true;
  }

  cycle(): ViewMode {
    const next = VIEW_MODES[(VIEW_MODES.indexOf(this.mode) + 1) % VIEW_MODES.length];
    this.setMode(next);
    return next;
  }

  /** snap skips the smoothing on the next update: a new board, not a walk. */
  snap() {
    this.snapNext = true;
  }

  turn(steps: number) {
    this.facing = ((((this.facing + steps) % 4) + 4) % 4) as Facing;
  }

  orbit(dx: number, dy: number) {
    if (this.mode === "first") {
      return;
    }
    if (this.mode === "diorama") {
      this.dioramaYaw -= dx * 0.005;
      this.dioramaPitch = THREE.MathUtils.clamp(this.dioramaPitch + dy * 0.005, 0.25, 1.5);
      return;
    }
    this.yaw -= dx * 0.005;
    this.pitch = THREE.MathUtils.clamp(this.pitch + dy * 0.005, 0.15, 1.5);
  }

  zoom(delta: number) {
    if (this.mode === "diorama") {
      this.dioramaDist = THREE.MathUtils.clamp(this.dioramaDist * (1 + delta * 0.001), DIORAMA.minDist, DIORAMA.maxDist);
      return;
    }
    if (this.mode === "chase") {
      this.dist = THREE.MathUtils.clamp(this.dist * (1 + delta * 0.001), CHASE.minDist, CHASE.maxDist);
    }
  }

  /** Fog distances that suit the view. */
  fog(): { near: number; far: number } {
    switch (this.mode) {
      case "first":
        return { near: 8, far: 26 };
      case "chase":
        return { near: 16, far: 42 };
      case "diorama":
        return { near: 120, far: 240 };
    }
  }

  /** update moves the camera toward where it should be, given the player's world position. */
  update(dt: number, playerX: number, playerZ: number) {
    const k = this.snapNext ? 1 : 1 - Math.exp(-dt * 10);
    const turnK = this.snapNext ? 1 : 1 - Math.exp(-dt * 12);
    this.snapNext = false;

    const facingYaw = (this.facing * Math.PI) / 2;
    this.lookYaw = lerpAngle(this.lookYaw, facingYaw, turnK);

    if (this.mode === "diorama") {
      this.target.lerp(BOARD_CENTER, k);
      this.place(this.dioramaYaw, this.dioramaPitch, this.dioramaDist, 0);
      return;
    }

    const goal = new THREE.Vector3(playerX, 0, playerZ);
    this.target.lerp(goal, k);

    if (this.mode === "first") {
      this.eye.set(this.target.x, EYE_HEIGHT, this.target.z);
      this.camera.position.copy(this.eye);
      const look = new THREE.Vector3(
        this.eye.x + Math.sin(this.lookYaw),
        EYE_HEIGHT - 0.05,
        this.eye.z - Math.cos(this.lookYaw),
      );
      this.camera.lookAt(look);
      return;
    }

    this.place(this.yaw, this.pitch, this.dist, 0.4);
  }

  private place(yaw: number, pitch: number, dist: number, lookHeight: number) {
    this.eye.set(
      this.target.x - Math.sin(yaw) * Math.cos(pitch) * dist,
      Math.sin(pitch) * dist,
      this.target.z + Math.cos(yaw) * Math.cos(pitch) * dist,
    );
    this.camera.position.copy(this.eye);
    this.camera.lookAt(this.target.x, lookHeight, this.target.z);
  }

  resize(aspect: number) {
    this.camera.aspect = aspect;
    this.camera.updateProjectionMatrix();
  }
}
