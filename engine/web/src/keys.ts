// The browser's play-mode key vocabulary (M4.2).
//
// Split out of main.ts so it can be exercised without a DOM: everything here is
// a pure function of KeyboardEvent.code / .key and the set of held keys. That
// matters because the failure mode is silent — a mistyped code string like
// "NumPad8" typechecks perfectly and simply never moves the player.
//
// Two rules govern the mapping, and they are the whole of M4.2:
//
//  1. Movement rides the KEYMASK. Arrow keys and the numeric keypad's 8/4/6/2,
//     which is exactly the original's vocabulary (INPUT.PAS:217-234, ported at
//     engine/input.go:101-110). WASD was a M3.5 invention and is gone: it made
//     'S' mean both "move down" and ZZT's save-game key, and ElementPlayerTick
//     reads both out of the same InputKeyPressed byte, so it cannot tell them
//     apart.
//
//  2. Commands ride the KEY BYTE. Every case of ElementPlayerTick's
//     `switch UpCase(InputKeyPressed)` (engine/elements.go:1374-1429).
//
// Because (1) never populates the key byte and (2) never populates the mask, a
// command can never be mistaken for a step, or the reverse.

export const InputMaskUp = 1 << 0;
export const InputMaskDown = 1 << 1;
export const InputMaskLeft = 1 << 2;
export const InputMaskRight = 1 << 3;
export const InputMaskShift = 1 << 4;
export const InputMaskShoot = 1 << 5;

export const KeyEnter = 13;
export const KeyEscape = 27;

// KeyboardEvent.code -> the byte ElementPlayerTick switches on.
// Escape is absent on purpose: it reaches the same switch through rawKey(),
// because it doubles as the text-window dismiss key.
export const COMMAND_CODES: Record<string, string> = {
  KeyT: "T", // light torch
  KeyP: "P", // pause (per-player, M3.11)
  KeyB: "B", // sound toggle (per-player, M3.11)
  KeyS: "S", // save game
  KeyQ: "Q", // quit prompt
  KeyH: "H", // help window
};

// The subset of KeyboardEvent this module reads. Keeps it driveable from a test.
export type KeyLike = {
  code: string;
  key: string;
  ctrlKey?: boolean;
  metaKey?: boolean;
  altKey?: boolean;
};

const MOVEMENT_CODES = [
  "ArrowUp",
  "ArrowDown",
  "ArrowLeft",
  "ArrowRight",
  "Numpad8",
  "Numpad2",
  "Numpad4",
  "Numpad6",
];

export function isMovementKey(code: string): boolean {
  return MOVEMENT_CODES.indexOf(code) >= 0;
}

export function isHandledKey(code: string): boolean {
  return (
    isMovementKey(code) ||
    code === "ShiftLeft" ||
    code === "ShiftRight" ||
    code === "Space" ||
    code === "Enter" ||
    code === "Escape"
  );
}

// commandKey returns the play-mode command byte for an event, or 0.
export function commandKey(event: KeyLike): number {
  if (event.ctrlKey || event.metaKey || event.altKey) {
    return 0;
  }
  // '?' is Shift+/ on US layouts and unshifted elsewhere, so match the produced
  // character rather than a physical code.
  if (event.key === "?") {
    return "?".charCodeAt(0);
  }
  const command = COMMAND_CODES[event.code];
  if (command) {
    return command.charCodeAt(0);
  }
  return 0;
}

// rawKey carries the two text-window navigation keys that also mean something
// in play mode: Escape opens the quit prompt (GamePromptEndPlay).
export function rawKey(code: string): number {
  if (code === "Enter") {
    return KeyEnter;
  }
  if (code === "Escape") {
    return KeyEscape;
  }
  return 0;
}

// movementMask folds the set of currently-held keys into the wire keymask.
export function movementMask(pressed: ReadonlySet<string>): number {
  let mask = 0;
  if (pressed.has("ArrowUp") || pressed.has("Numpad8")) {
    mask |= InputMaskUp;
  }
  if (pressed.has("ArrowDown") || pressed.has("Numpad2")) {
    mask |= InputMaskDown;
  }
  if (pressed.has("ArrowLeft") || pressed.has("Numpad4")) {
    mask |= InputMaskLeft;
  }
  if (pressed.has("ArrowRight") || pressed.has("Numpad6")) {
    mask |= InputMaskRight;
  }
  if (pressed.has("ShiftLeft") || pressed.has("ShiftRight")) {
    mask |= InputMaskShift;
  }
  if (pressed.has("Space")) {
    mask |= InputMaskShoot;
  }
  return mask;
}
