// input.ts — the play-mode key vocabulary, transcribed from the 2D client's
// keys.ts (engine/web/src/keys.ts). Movement rides the keymask; commands ride
// the key byte; the two never mix, so a command can never be read as a step.
//
// Only the vanilla bindings are here. V is this client's own: it never reaches
// the wire, it cycles the camera. WASD is this client's own too: at eye level A
// and D strafe, which the text screen has no use for, and W and S walk forward
// and back. All four ride pseudo-bits above the wire bits and are resolved
// against your facing (or dropped) before anything is sent.
//
// W and S are only bound at eye level, and S is the reason. Standing in the
// board, the hand that walks is on WASD and the hand that turns is on the
// arrows; sitting at the text screen, S is ZZT's Save and has been since 1991.
// A key cannot be both, so eye level is where the walking scheme lives and the
// text screen is where you save. Nothing is lost -- V is one press away, and
// the sidebar says so while you are standing up.

export const InputMaskUp = 1 << 0;
export const InputMaskDown = 1 << 1;
export const InputMaskLeft = 1 << 2;
export const InputMaskRight = 1 << 3;
export const InputMaskShift = 1 << 4;
export const InputMaskShoot = 1 << 5;

/**
 * Client-only bits. The server's keymask is six bits wide (input.go), so these
 * four live above it: facingMask turns them into a real direction and wireMask
 * drops them. None of them may ever be sent.
 */
export const InputMaskStrafeLeft = 1 << 6;
export const InputMaskStrafeRight = 1 << 7;
export const InputMaskWalkForward = 1 << 8;
export const InputMaskWalkBack = 1 << 9;
const WIRE_BITS =
  InputMaskUp | InputMaskDown | InputMaskLeft | InputMaskRight | InputMaskShift | InputMaskShoot;

export const KeyEnter = 13;
export const KeyEscape = 27;

export type KeyLike = {
  code: string;
  key: string;
  ctrlKey?: boolean;
  metaKey?: boolean;
  altKey?: boolean;
};

const MASKS: Record<string, number> = {
  ArrowUp: InputMaskUp,
  Numpad8: InputMaskUp,
  ArrowDown: InputMaskDown,
  Numpad2: InputMaskDown,
  ArrowLeft: InputMaskLeft,
  Numpad4: InputMaskLeft,
  ArrowRight: InputMaskRight,
  Numpad6: InputMaskRight,
  ShiftLeft: InputMaskShift,
  ShiftRight: InputMaskShift,
  Space: InputMaskShoot,
  KeyA: InputMaskStrafeLeft,
  KeyD: InputMaskStrafeRight,
  KeyW: InputMaskWalkForward,
  KeyS: InputMaskWalkBack,
};

/**
 * W and S are the two bindings that are not free: outside eye level S is Save.
 * They are filtered out at the door rather than dropped later by wireMask,
 * because a bit the wire discards is still a key that stopped commandKey from
 * seeing it -- the player would press S for Save and get nothing at all. A and
 * D need no such care: no vanilla command wants them.
 */
function walksOnlyAtEyeLevel(code: string): boolean {
  return code === "KeyW" || code === "KeyS";
}

/** Every case of ElementPlayerTick's key switch that a browser can reach. */
const COMMANDS: Record<string, number> = {
  KeyT: "T".charCodeAt(0),
  KeyP: "P".charCodeAt(0),
  KeyB: "B".charCodeAt(0),
  KeyS: "S".charCodeAt(0),
  KeyQ: "Q".charCodeAt(0),
  KeyH: "H".charCodeAt(0),
  Enter: KeyEnter,
  Escape: KeyEscape,
};

export function isMovementKey(code: string, firstPerson = false): boolean {
  if (!firstPerson && walksOnlyAtEyeLevel(code)) {
    return false;
  }
  return code in MASKS;
}

/** wireMask keeps only the bits the server understands. */
export function wireMask(mask: number): number {
  return mask & WIRE_BITS;
}

/**
 * commandKey returns the play-mode command byte for an event, or 0. At eye
 * level S walks backwards instead of saving, so it is not a command there.
 */
export function commandKey(event: KeyLike, firstPerson = false): number {
  if (event.ctrlKey || event.metaKey || event.altKey) {
    return 0;
  }
  if (firstPerson && walksOnlyAtEyeLevel(event.code)) {
    return 0;
  }
  if (event.key === "?") {
    return "?".charCodeAt(0);
  }
  return COMMANDS[event.code] ?? 0;
}

/** movementMask folds the set of held keys into the wire keymask. */
export function movementMask(pressed: ReadonlySet<string>, firstPerson = false): number {
  let mask = 0;
  for (const code of pressed) {
    if (!firstPerson && walksOnlyAtEyeLevel(code)) {
      continue;
    }
    mask |= MASKS[code] ?? 0;
  }
  return mask;
}

/** The board direction a mask is pushing, for the first-person camera to face. */
export function maskDirection(mask: number): { dx: number; dy: number } {
  let dx = 0;
  let dy = 0;
  if (mask & InputMaskLeft) dx -= 1;
  if (mask & InputMaskRight) dx += 1;
  if (mask & InputMaskUp) dy -= 1;
  if (mask & InputMaskDown) dy += 1;
  return { dx, dy };
}

/** Compass index: 0 north, 1 east, 2 south, 3 west. */
export type Facing = 0 | 1 | 2 | 3;

const FACING_MASKS = [InputMaskUp, InputMaskRight, InputMaskDown, InputMaskLeft];

/**
 * facingMask is the first-person remap: W (or up) walks the way you face, S (or
 * down) walks backwards, A and D step sideways without turning, and left/right
 * (turns, handled on the key edge) never travel. Shift and shoot pass through,
 * so Space+W shoots straight ahead and Space+A shoots to your left.
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
  return wireMask(out);
}

/**
 * ghostDrift is the direction a held mask flies a ghost, in the frame of
 * whatever the player is looking through: dz is forward, dx is right.
 *
 * A ghost is the camera, not the body, so the keys keep the meaning the view
 * already gave them. In first person left and right are still turns, so the
 * strafes are what move you sideways; everywhere else the arrows are board
 * directions and they simply fly instead of walk.
 */
export function ghostDrift(mask: number, firstPerson: boolean): { dx: number; dz: number } {
  let dx = 0;
  let dz = 0;
  if (mask & (InputMaskUp | InputMaskWalkForward)) dz += 1;
  if (mask & (InputMaskDown | InputMaskWalkBack)) dz -= 1;
  if (firstPerson) {
    if (mask & InputMaskStrafeLeft) dx -= 1;
    if (mask & InputMaskStrafeRight) dx += 1;
  } else {
    if (mask & InputMaskLeft) dx -= 1;
    if (mask & InputMaskRight) dx += 1;
  }
  return { dx, dz };
}

/** facingOfMask is the compass index a held direction points at, or null when none is held. */
export function facingOfMask(mask: number): Facing | null {
  if (mask & InputMaskUp) return 0;
  if (mask & InputMaskRight) return 1;
  if (mask & InputMaskDown) return 2;
  if (mask & InputMaskLeft) return 3;
  return null;
}
