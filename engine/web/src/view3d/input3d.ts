// input3d.ts — the four keys that move the camera, and nothing else.
//
// The arrows walk. They walk north, south, east and west, in every view, which
// is what they have meant in ZZT since 1991 and what every world was built
// around: a board is a compass, not a corridor. An earlier version of this file
// made them turn you at eye level and put walking on WASD, and that was wrong
// in the way that matters -- it took the game's oldest control away from the
// one view where a player is least sure where they are.
//
// So WASD is the camera instead. A and D swing it, W and S raise and lower it,
// and none of the four ever reaches the server: they set no mask bit, they send
// no key byte, they only decide where you are looking from. That is a stronger
// version of what the certified row `input.play-wasd-removed` (M16.10) asks
// for -- it wants W/A/D inert on the wire, and these are inert by construction
// rather than by being filtered out on the way past.
//
// S is the one that costs something. It is ZZT's save key, and in the 3D view
// it looks down instead, so saving is done from the text screen -- one press of
// V away, and the sidebar says so while you are in the world.

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
