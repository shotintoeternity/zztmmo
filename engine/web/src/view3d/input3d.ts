// input3d.ts — the eye-level key vocabulary, and nothing else.
//
// Standing in the board, the arrows cannot say everything a body can do: there
// is a difference between turning to face a wall and stepping sideways along
// it, and the text screen has never needed one. So at eye level the left hand
// walks on WASD and the right hand turns on the arrows, and W/A/S/D are
// resolved against the way you are facing before anything is sent.
//
// This is scoped to eye level on purpose, and the reason is S.
//
// The certified row `input.play-wasd-removed` (M16.10) says W/A/D reach the
// server as nothing at all, and that S opens the save prompt. That row is not
// an accident: M3.5 invented WASD in this client and M4.2 removed it, because
// S meant both "move down" and ZZT's save key while ElementPlayerTick reads
// both out of one byte. Nothing here disturbs that. On the text screen, and in
// the orbit camera, W/A/S/D are as inert as they were yesterday and S still
// saves. They mean something only when you are standing inside your own square,
// a place that did not exist when the row was written.
//
// The four bits below live above the six the server understands (input.go), so
// facingMask is the only thing that can turn them into a direction, and a mask
// that never went through it carries none of them onto the wire.

import { InputMaskDown, InputMaskLeft, InputMaskRight, InputMaskShift, InputMaskShoot, InputMaskUp } from "../keys";

export const InputMaskStrafeLeft = 1 << 6;
export const InputMaskStrafeRight = 1 << 7;
export const InputMaskWalkForward = 1 << 8;
export const InputMaskWalkBack = 1 << 9;

const WIRE_BITS =
  InputMaskUp | InputMaskDown | InputMaskLeft | InputMaskRight | InputMaskShift | InputMaskShoot;

/** The keys the eye-level view claims, and the pseudo-bit each one sets. */
const EYE_LEVEL_KEYS: Record<string, number> = {
  KeyW: InputMaskWalkForward,
  KeyS: InputMaskWalkBack,
  KeyA: InputMaskStrafeLeft,
  KeyD: InputMaskStrafeRight,
};

/** Facing as a compass index: 0 north, 1 east, 2 south, 3 west. */
export type Facing = 0 | 1 | 2 | 3;

const FACING_MASKS = [InputMaskUp, InputMaskRight, InputMaskDown, InputMaskLeft];

/** isEyeLevelKey reports whether this code walks a body at eye level. */
export function isEyeLevelKey(code: string): boolean {
  return code in EYE_LEVEL_KEYS;
}

/** eyeLevelBits folds the held WASD keys into their pseudo-bits. */
export function eyeLevelBits(pressed: ReadonlySet<string>): number {
  let bits = 0;
  for (const code of pressed) {
    bits |= EYE_LEVEL_KEYS[code] ?? 0;
  }
  return bits;
}

/**
 * facingMask is the eye-level remap: W (or up) walks the way you face, S (or
 * down) walks backwards, A and D step sideways without turning, and left/right
 * -- turns, handled on the key edge -- never travel. Shift and shoot pass
 * through, so Shift+W fires straight ahead and Shift+A fires to your left.
 *
 * The result is masked back down to the wire bits, so no pseudo-bit can escape
 * even if a caller hands us one we did not expect.
 */
export function facingMask(mask: number, facing: Facing): number {
  let out = mask & (InputMaskShift | InputMaskShoot);
  if (mask & (InputMaskUp | InputMaskWalkForward)) {
    out |= FACING_MASKS[facing];
  }
  if (mask & (InputMaskDown | InputMaskWalkBack)) {
    out |= FACING_MASKS[(facing + 2) % 4];
  }
  if (mask & InputMaskStrafeLeft) {
    out |= FACING_MASKS[(facing + 3) % 4];
  }
  if (mask & InputMaskStrafeRight) {
    out |= FACING_MASKS[(facing + 1) % 4];
  }
  return out & WIRE_BITS;
}
