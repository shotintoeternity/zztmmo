import assert from "node:assert/strict";
import { build } from "esbuild";

const out = await build({ entryPoints: ["src/view3d/zoom.ts"], bundle: true, format: "esm", platform: "node", write: false });
const { zoomStep, toggleFirstPerson, ORBIT_MIN, ORBIT_MAX, FIRST_EXIT, ORBIT_DEFAULT } =
  await import(`data:text/javascript;base64,${Buffer.from(out.outputFiles[0].contents).toString("base64")}`);

const NOTCH = 100; // one wheel click, near enough
const orbit = (dist) => ({ dist, firstPerson: false });

// Pulling out walks to the whole board and stops there.
{
  let state = orbit(ORBIT_DEFAULT);
  for (let i = 0; i < 100; i += 1) state = zoomStep(state, NOTCH);
  assert.equal(state.firstPerson, false);
  assert.equal(state.dist, ORBIT_MAX, "the far end is the whole board");
}

// Pushing in walks to your own square and stops there.
{
  let state = orbit(ORBIT_DEFAULT);
  let entered = -1;
  for (let i = 0; i < 100 && entered < 0; i += 1) {
    state = zoomStep(state, -NOTCH);
    if (state.firstPerson) entered = i;
  }
  assert.ok(entered > 3, `standing up should take a few notches, took ${entered + 1}`);
  const inside = zoomStep(state, -NOTCH);
  assert.deepEqual(inside, state, "pushing in from inside your own square goes nowhere");
}

// The hysteresis: leaving lands clear of the threshold that got you in, so the
// notch after leaving cannot put you straight back.
{
  const inside = { dist: ORBIT_MIN, firstPerson: true };
  const out1 = zoomStep(inside, NOTCH);
  assert.equal(out1.firstPerson, false);
  assert.ok(out1.dist >= FIRST_EXIT, "leaving lands at the exit distance");
  assert.ok(out1.dist > ORBIT_MIN, "which is clear of the entry threshold");
  // A trackpad's small deltas must not cross straight back over.
  let state = out1;
  for (let i = 0; i < 3; i += 1) {
    state = zoomStep(state, -8);
    assert.equal(state.firstPerson, false, "a nudge inward right after leaving must not re-enter");
  }
}

// F keeps the distance, so F out of a wide shot and F back is a peek.
{
  const wide = orbit(60);
  const up = toggleFirstPerson(wide);
  assert.equal(up.firstPerson, true);
  assert.equal(up.dist, 60);
  assert.deepEqual(toggleFirstPerson(up), wide, "and back to the wide shot");
  // Wheeling out of an F-entered first person keeps that distance too.
  assert.equal(zoomStep(up, NOTCH).dist, 60);
}

// A trackpad fling delivers a delta of thousands in one event. Whatever it is,
// the distance stays a sane number in range.
{
  for (const delta of [-100000, -5000, -1000, -1, 0, 1, 1000, 5000, 100000]) {
    for (const dist of [ORBIT_MIN, 3, 4, 15, ORBIT_DEFAULT, 60, ORBIT_MAX]) {
      for (const firstPerson of [false, true]) {
        const next = zoomStep({ dist, firstPerson }, delta);
        assert.ok(Number.isFinite(next.dist), `dist went to ${next.dist} from ${dist} on ${delta}`);
        assert.ok(next.dist >= ORBIT_MIN, `dist fell to ${next.dist} from ${dist} on ${delta}`);
        assert.ok(next.dist <= ORBIT_MAX, `dist rose to ${next.dist} from ${dist} on ${delta}`);
      }
    }
  }
}

console.log("zoom ok");
