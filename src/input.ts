// input.ts — the play-mode key vocabulary, transcribed from the 2D client's
// keys.ts (engine/web/src/keys.ts). Movement rides the keymask; commands ride
// the key byte; the two never mix, so a command can never be read as a step.
//
// Only the vanilla bindings are here. V is this client's own: it never reaches
// the wire, it cycles the camera.

export const InputMaskUp = 1 << 0;
export const InputMaskDown = 1 << 1;
export const InputMaskLeft = 1 << 2;
export const InputMaskRight = 1 << 3;
export const InputMaskShift = 1 << 4;
export const InputMaskShoot = 1 << 5;

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
};

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

export function isMovementKey(code: string): boolean {
  return code in MASKS;
}

/** commandKey returns the play-mode command byte for an event, or 0. */
export function commandKey(event: KeyLike): number {
  if (event.ctrlKey || event.metaKey || event.altKey) {
    return 0;
  }
  if (event.key === "?") {
    return "?".charCodeAt(0);
  }
  return COMMANDS[event.code] ?? 0;
}

/** movementMask folds the set of held keys into the wire keymask. */
export function movementMask(pressed: ReadonlySet<string>): number {
  let mask = 0;
  for (const code of pressed) {
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
 * facingMask is the first-person remap: up walks the way you face, down walks
 * backwards, and left/right (turns, handled on the key edge) never travel.
 * Shift and shoot pass through, so Space+Up shoots straight ahead.
 */
export function facingMask(mask: number, facing: Facing): number {
  let out = mask & (InputMaskShift | InputMaskShoot);
  if (mask & InputMaskUp) {
    out |= FACING_MASKS[facing];
  }
  if (mask & InputMaskDown) {
    out |= FACING_MASKS[(facing + 2) % 4];
  }
  return out;
}

/** facingOfMask is the compass index a held direction points at, or null when none is held. */
export function facingOfMask(mask: number): Facing | null {
  if (mask & InputMaskUp) return 0;
  if (mask & InputMaskRight) return 1;
  if (mask & InputMaskDown) return 2;
  if (mask & InputMaskLeft) return 3;
  return null;
}
