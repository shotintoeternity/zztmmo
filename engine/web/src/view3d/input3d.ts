// input3d.ts — the camera keys, and the frame the arrows are read in.
//
// THE ARROWS ALWAYS MOVE YOU. That is the oldest promise this game makes and
// nothing here breaks it: an arrow has meant "walk" since 1991, and a view is
// not a reason for it to mean "turn". An earlier cut of this file put turning
// on the arrows and walking on WASD, and that was wrong twice over -- it took
// the game's oldest control away, and it walked straight back into the S
// collision that M4.2 removed WASD for in the first place.
//
// What DOES change with the view is the frame those arrows are read in, and it
// changes because the camera is what the player is reasoning with:
//
//   Pulled back -- orbit, chase, overhead, diorama -- you are reading a map.
//   North is north, the arrows are absolute board directions, and the text
//   screen and the world agree about what up means.
//
//   At eye level you are a body. Up walks the way you are facing, down walks
//   backwards, and left and right STEP SIDEWAYS without turning. The client
//   resolves those against your facing and sends an ordinary board direction,
//   which is all ZZT's six-bit keymask can carry (facingMask, below).
//
// Turning is looking, so it lives with the camera: A and D quarter-turn you at
// eye level and swing the orbit when you are pulled back, and W and S tilt the
// gaze. None of the four ever reaches the server -- they set no mask bit and
// send no key byte. That is a stronger version of what the certified row
// `input.play-wasd-removed` (M16.10) asks for: it wants W/A/D inert on the
// wire, and these are inert by construction rather than by being filtered out
// on the way past.
//
// S is the one that costs something. It is ZZT's save key, and in the 3D view
// it looks down instead, so saving is done from the text screen -- one press of
// V away, and the sidebar says so while you are in the world. Note that walking
// stays on the arrows precisely so this stays the ONLY collision: the moment
// walking moves to WASD, S has to mean "back" as well, and it cannot.

import { InputMaskDown, InputMaskLeft, InputMaskRight, InputMaskShift, InputMaskShoot, InputMaskUp } from "../keys";

/** Facing as a compass index: 0 north, 1 east, 2 south, 3 west. */
export type Facing = 0 | 1 | 2 | 3;

/** The wire bit for each compass index, in the same order as Facing. */
const FACING_MASKS = [InputMaskUp, InputMaskRight, InputMaskDown, InputMaskLeft];

/** Everything the server understands. Nothing else may leave this module. */
const WIRE_BITS = InputMaskUp | InputMaskDown | InputMaskLeft | InputMaskRight | InputMaskShift | InputMaskShoot;

/**
 * facingMask reads an arrow mask in the frame of a body facing `facing`.
 *
 * Up walks the way you look; down walks backwards without turning around; left
 * and right step sideways, also without turning. Shift and shoot pass through
 * unchanged, so Shift+left fires to your left -- the shot goes where the step
 * would have gone, which is the only reading that keeps "shoot the way you are
 * pointing" true in a view where pointing is a thing you can do.
 *
 * It is a rotation, so facing north is the identity and the text screen's
 * vocabulary is exactly the eye-level vocabulary looking north. The result is
 * masked back down to the wire bits: a caller that hands in a stray high bit
 * cannot get one out.
 */
export function facingMask(mask: number, facing: Facing): number {
  let out = mask & (InputMaskShift | InputMaskShoot);
  if (mask & InputMaskUp) {
    out |= FACING_MASKS[facing];
  }
  if (mask & InputMaskDown) {
    out |= FACING_MASKS[(facing + 2) % 4];
  }
  if (mask & InputMaskLeft) {
    out |= FACING_MASKS[(facing + 3) % 4];
  }
  if (mask & InputMaskRight) {
    out |= FACING_MASKS[(facing + 1) % 4];
  }
  return out & WIRE_BITS;
}

/** One press: which way the camera swings, and which way it tilts. */
export type LookStep = {
  /** -1 swings left, +1 right. */
  dyaw: number;
  /** +1 raises the gaze toward the ceiling, -1 lowers it toward the floor. */
  dpitch: number;
};

const CAMERA_KEYS: Record<string, LookStep> = {
  KeyW: { dyaw: 0, dpitch: 1 },
  KeyS: { dyaw: 0, dpitch: -1 },
  KeyA: { dyaw: -1, dpitch: 0 },
  KeyD: { dyaw: 1, dpitch: 0 },
};

/** isCameraKey reports whether this code moves the camera in the 3D view. */
export function isCameraKey(code: string): boolean {
  return code in CAMERA_KEYS;
}

/**
 * lookStepFor returns the step a key asks for, or null if it is not one of the
 * four. Held keys repeat, so leaning on W walks the gaze up rather than making
 * you tap it -- the step is sized so a few presses cover the whole range either
 * way.
 */
export function lookStepFor(code: string): LookStep | null {
  return CAMERA_KEYS[code] ?? null;
}
