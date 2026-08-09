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

import type { KeyAction, KeyBindings } from "./comfort";

export const InputMaskUp = 1 << 0;
export const InputMaskDown = 1 << 1;
export const InputMaskLeft = 1 << 2;
export const InputMaskRight = 1 << 3;
export const InputMaskShift = 1 << 4;
export const InputMaskShoot = 1 << 5;

export const KeyEnter = 13;
export const KeyEscape = 27;

const COMMAND_ACTIONS: Record<KeyAction, number> = {
  up: 0,
  down: 0,
  left: 0,
  right: 0,
  shoot: 0,
  shift: 0,
  enter: KeyEnter,
  escape: KeyEscape,
  torch: "T".charCodeAt(0),
  pause: "P".charCodeAt(0),
  sound: "B".charCodeAt(0),
  save: "S".charCodeAt(0),
  quit: "Q".charCodeAt(0),
  help: "H".charCodeAt(0),
};

// The subset of KeyboardEvent this module reads. Keeps it driveable from a test.
export type KeyLike = {
  code: string;
  key: string;
  ctrlKey?: boolean;
  metaKey?: boolean;
  altKey?: boolean;
};

const VANILLA_BINDINGS: Required<KeyBindings> = {
  up: ["ArrowUp", "Numpad8"],
  down: ["ArrowDown", "Numpad2"],
  left: ["ArrowLeft", "Numpad4"],
  right: ["ArrowRight", "Numpad6"],
  shoot: ["Space"],
  shift: ["ShiftLeft", "ShiftRight"],
  enter: ["Enter"],
  escape: ["Escape"],
  torch: ["KeyT"],
  pause: ["KeyP"],
  sound: ["KeyB"],
  save: ["KeyS"],
  quit: ["KeyQ"],
  help: ["KeyH"],
};

export function vanillaKeyBindings(): Required<KeyBindings> {
  return Object.fromEntries(Object.entries(VANILLA_BINDINGS).map(([action, codes]) => [action, [...codes]])) as Required<KeyBindings>;
}

function bindingsWithDefaults(bindings: KeyBindings = {}): Required<KeyBindings> {
  const base = vanillaKeyBindings();
  for (const [action, codes] of Object.entries(bindings) as [KeyAction, string[]][]) {
    if (Array.isArray(codes) && codes.length > 0) {
      base[action] = [...codes];
    }
  }
  return base;
}

function hasCode(bindings: Required<KeyBindings>, action: KeyAction, code: string): boolean {
  return bindings[action].indexOf(code) >= 0;
}

export function isMovementKey(code: string, bindings: KeyBindings = {}): boolean {
  const b = bindingsWithDefaults(bindings);
  return hasCode(b, "up", code) || hasCode(b, "down", code) || hasCode(b, "left", code) || hasCode(b, "right", code);
}

export function isHandledKey(code: string, bindings: KeyBindings = {}): boolean {
  const b = bindingsWithDefaults(bindings);
  return (Object.keys(b) as KeyAction[]).some((action) => hasCode(b, action, code));
}

export function actionForKey(code: string, bindings: KeyBindings = {}): KeyAction | "" {
  const b = bindingsWithDefaults(bindings);
  for (const action of Object.keys(b) as KeyAction[]) {
    if (hasCode(b, action, code)) {
      return action;
    }
  }
  return "";
}

// commandKey returns the play-mode command byte for an event, or 0.
export function commandKey(event: KeyLike, bindings: KeyBindings = {}): number {
  if (event.ctrlKey || event.metaKey || event.altKey) {
    return 0;
  }
  // '?' is Shift+/ on US layouts and unshifted elsewhere, so match the produced
  // character rather than a physical code.
  if (event.key === "?") {
    return "?".charCodeAt(0);
  }
  const action = actionForKey(event.code, bindings);
  return action ? COMMAND_ACTIONS[action] ?? 0 : 0;
}

// rawKey carries the two text-window navigation keys that also mean something
// in play mode: Escape opens the quit prompt (GamePromptEndPlay).
export function rawKey(code: string, bindings: KeyBindings = {}): number {
  const action = actionForKey(code, bindings);
  if (action === "enter") {
    return KeyEnter;
  }
  if (action === "escape") {
    return KeyEscape;
  }
  return 0;
}

// movementMask folds the set of currently-held keys into the wire keymask.
export function movementMask(pressed: ReadonlySet<string>, bindings: KeyBindings = {}): number {
  const b = bindingsWithDefaults(bindings);
  let mask = 0;
  if (b.up.some((code) => pressed.has(code))) {
    mask |= InputMaskUp;
  }
  if (b.down.some((code) => pressed.has(code))) {
    mask |= InputMaskDown;
  }
  if (b.left.some((code) => pressed.has(code))) {
    mask |= InputMaskLeft;
  }
  if (b.right.some((code) => pressed.has(code))) {
    mask |= InputMaskRight;
  }
  if (b.shift.some((code) => pressed.has(code))) {
    mask |= InputMaskShift;
  }
  if (b.shoot.some((code) => pressed.has(code))) {
    mask |= InputMaskShoot;
  }
  return mask;
}
