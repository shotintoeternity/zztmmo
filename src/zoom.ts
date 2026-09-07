// zoom.ts — the one axis the 3D view moves along, and the one state on it.
//
// Overhead, chase and diorama were never three modes: they are one orbit
// camera at three distances, and the wheel already moved between them. So the
// view is a single distance, from the whole board down to your own shoulder.
//
// First person is the exception. It is not an angle — it is a different set of
// controls, where left and right turn instead of walking — so it is a state,
// entered by pushing past the closest orbit step and left by pulling back out.
// The two thresholds are deliberately not the same number: you enter at
// ORBIT_MIN and leave at FIRST_EXIT, which is further out, so a wheel resting
// on the boundary cannot flicker your controls between two frames.
//
// Pure, and tested under node: this is the part that can go wrong, and
// camera.ts cannot be tested without a browser.

export type Zoom = {
  /** The orbit distance. Kept even in first person, which is what F returns to. */
  dist: number;
  firstPerson: boolean;
};

/** The closest the camera orbits before you are simply standing there. */
export const ORBIT_MIN = 2.5;
/** The whole board, from the south, the way the text screen shows it. */
export const ORBIT_MAX = 110;
/** Pulling out of first person lands here, clear of ORBIT_MIN: the hysteresis. */
export const FIRST_EXIT = 4;
/** The default: high above your ☻, most of the rows around you in sight. */
export const ORBIT_DEFAULT = 27;

const RATE = 0.004;

function clamp(value: number, low: number, high: number): number {
  return Math.min(high, Math.max(low, value));
}

/**
 * zoomStep applies one wheel event. A positive delta pulls out, as the wheel
 * does everywhere. The per-event factor is clamped because a trackpad fling
 * can deliver a delta of thousands in one event, and an unclamped factor would
 * either invert (a negative distance) or throw you into first person from
 * across the board.
 */
export function zoomStep(state: Zoom, delta: number): Zoom {
  if (state.firstPerson) {
    // Pushing further in from inside your own square has nowhere to go.
    if (delta <= 0) {
      return state;
    }
    return { dist: Math.max(state.dist, FIRST_EXIT), firstPerson: false };
  }
  const next = state.dist * clamp(1 + delta * RATE, 0.5, 2);
  if (next < ORBIT_MIN) {
    return { dist: ORBIT_MIN, firstPerson: true };
  }
  return { dist: Math.min(next, ORBIT_MAX), firstPerson: false };
}

/**
 * toggleFirstPerson is the F key, for anyone without a wheel. It keeps the
 * orbit distance rather than resetting it, so F out of a wide shot and F back
 * returns you to the wide shot: a peek, not a journey.
 */
export function toggleFirstPerson(state: Zoom): Zoom {
  return { dist: state.dist, firstPerson: !state.firstPerson };
}
