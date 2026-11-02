// view3d_input3d.test.mjs — the camera keys, and the frame the arrows are in.
//
// Two things live in this module and they must not be confused for each other.
//
// Pulled back, WASD is the camera: A and D swing it, W and S tilt it, and none
// of them produces a direction or a key byte. The certified row
// `input.play-wasd-removed` (M16.10) says W/A/D must reach the server as
// nothing at all on the text screen, and control_keys.test.mjs proves that with
// a real browser.
//
// At eye level W and S walk instead (owner decision 2026-11-02): they stand in
// for the up and down arrows, so facingMask reads them in the body frame like
// any arrow. eyeLevelWalkMask says which arrow each one is; A and D still only
// turn.
//
// facingMask is the other half: the arrows always walk, but at eye level they
// walk in the frame of a body rather than the frame of a map. It is a pure
// rotation of the four direction bits, and facing north is the identity, which
// is the property that keeps the text screen and the world speaking the same
// vocabulary.
//
// A NOTE ON THE NAME. An earlier scheme had a facingMask too, and it resolved
// WASD -- it put walking on WASD and turning on the arrows, which took the
// game's oldest control away and walked back into the S/Save collision that
// M4.2 removed WASD for. This is not that. Walking stays on the arrows, turning
// stays with the camera, and only the frame moves.

import assert from "node:assert/strict";
import { build } from "esbuild";

const out = await build({
  entryPoints: ["src/view3d/input3d.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const { eyeLevelWalkMask, facingMask, isCameraKey, lookStepFor } = await import(
  `data:text/javascript;base64,${Buffer.from(out.outputFiles[0].contents).toString("base64")}`
);

// ---------------------------------------------------------------------------
// Which keys the camera claims
// ---------------------------------------------------------------------------
for (const code of ["KeyW", "KeyA", "KeyS", "KeyD"]) {
  assert.equal(isCameraKey(code), true, `${code} moves the camera`);
  assert.notEqual(lookStepFor(code), null, `${code} must name a step`);
}

// The arrows are not camera keys. This is the assertion that would fail if
// somebody reached for the old scheme again and put walking back on WASD.
for (const code of ["ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", "Numpad8", "Numpad2", "Numpad4", "Numpad6"]) {
  assert.equal(isCameraKey(code), false, `${code} walks the board; it must not move the camera`);
  assert.equal(lookStepFor(code), null);
}

// Nor is anything else on the sidebar, or the keys the client added itself.
for (const code of ["KeyT", "KeyP", "KeyB", "KeyQ", "KeyH", "KeyC", "KeyL", "KeyV", "Digit3", "Space", "Enter", "Escape"]) {
  assert.equal(isCameraKey(code), false, `${code} is not a camera key`);
}

// ---------------------------------------------------------------------------
// What each one does
// ---------------------------------------------------------------------------
assert.deepEqual(lookStepFor("KeyA"), { dyaw: -1, dpitch: 0 }, "A swings left");
assert.deepEqual(lookStepFor("KeyD"), { dyaw: 1, dpitch: 0 }, "D swings right");
assert.deepEqual(lookStepFor("KeyW"), { dyaw: 0, dpitch: 1 }, "pulled back, W tilts the orbit up");
assert.deepEqual(lookStepFor("KeyS"), { dyaw: 0, dpitch: -1 }, "pulled back, S tilts the orbit down");

// A step is a look and never a move: no code may ask for both at once, and
// none may ask for a direction on the board.
for (const code of ["KeyW", "KeyA", "KeyS", "KeyD"]) {
  const step = lookStepFor(code);
  assert.equal(
    step.dyaw === 0 || step.dpitch === 0,
    true,
    `${code} must swing or tilt, not both`,
  );
  assert.equal(Math.abs(step.dyaw) <= 1 && Math.abs(step.dpitch) <= 1, true, "a press is one step");
  assert.equal("mask" in step || "keymask" in step, false, `${code} must carry nothing that could be sent`);
}

// ---------------------------------------------------------------------------
// The module's whole surface
// ---------------------------------------------------------------------------
// eyeLevelWalkMask is the one movement helper here, and it only names which
// arrow W and S stand for. Anything else new fails this and gets asked about.
const surface = Object.keys(
  await import(`data:text/javascript;base64,${Buffer.from(out.outputFiles[0].contents).toString("base64")}`),
).sort();
assert.deepEqual(surface, ["eyeLevelWalkMask", "facingMask", "isCameraKey", "lookStepFor"], `unexpected exports: ${surface.join(", ")}`);

// ---------------------------------------------------------------------------
// facingMask: the arrows, read in the frame of a body
// ---------------------------------------------------------------------------
const UP = 1 << 0, DOWN = 1 << 1, LEFT = 1 << 2, RIGHT = 1 << 3, SHIFT = 1 << 4, SHOOT = 1 << 5;
const NORTH = 0, EAST = 1, SOUTH = 2, WEST = 3;

// Facing north is the identity. This is the assertion that keeps the two
// vocabularies one vocabulary: the eye-level controls ARE the text screen's
// controls, seen from the direction the text screen is drawn from.
for (const bit of [UP, DOWN, LEFT, RIGHT, SHIFT, SHOOT, UP | SHIFT]) {
  assert.equal(facingMask(bit, NORTH), bit, "facing north must be the identity");
}

// Up walks the way you look; down walks backwards WITHOUT turning around.
assert.equal(facingMask(UP, EAST), RIGHT, "facing east, forward is east");
assert.equal(facingMask(UP, SOUTH), DOWN, "facing south, forward is south");
assert.equal(facingMask(UP, WEST), LEFT, "facing west, forward is west");
assert.equal(facingMask(DOWN, EAST), LEFT, "facing east, back is west");
assert.equal(facingMask(DOWN, WEST), RIGHT, "facing west, back is east");

// Left and right STEP SIDEWAYS. They do not turn: turning is a camera key, and
// an arrow that did not move you would be the old mistake all over again.
assert.equal(facingMask(LEFT, EAST), UP, "facing east, your left hand points north");
assert.equal(facingMask(RIGHT, EAST), DOWN, "facing east, your right hand points south");
assert.equal(facingMask(LEFT, WEST), DOWN, "facing west, your left hand points south");
assert.equal(facingMask(RIGHT, WEST), UP, "facing west, your right hand points north");
assert.equal(facingMask(LEFT, SOUTH), RIGHT, "facing south, your left hand points east");
assert.equal(facingMask(RIGHT, SOUTH), LEFT, "facing south, your right hand points west");

// Shift and shoot ride through untouched, so Shift+left fires where the step
// would have gone.
assert.equal(facingMask(LEFT | SHIFT, EAST), UP | SHIFT, "Shift+left fires to your left");
assert.equal(facingMask(UP | SHOOT, SOUTH), DOWN | SHOOT, "the shot goes where you are pointing");
assert.equal(facingMask(SHIFT | SHOOT, EAST), SHIFT | SHOOT, "no direction, nothing to rotate");
assert.equal(facingMask(0, EAST), 0, "nothing held is nothing sent");

// It is a rotation, so at every facing the four direction bits land on the four
// direction bits, one each. A mapping that collapsed two of them would quietly
// make a direction unreachable.
for (const facing of [NORTH, EAST, SOUTH, WEST]) {
  const landed = [UP, DOWN, LEFT, RIGHT].map((bit) => facingMask(bit, facing));
  assert.deepEqual([...landed].sort((a, b) => a - b), [UP, DOWN, LEFT, RIGHT], `facing ${facing} must be a bijection`);
}

// Nothing above the six the server understands can ever come out, whatever
// goes in. The pseudo-bits of the old scheme died with it, and this is the
// guard that keeps one from being reintroduced by accident.
const WIRE = UP | DOWN | LEFT | RIGHT | SHIFT | SHOOT;
for (const facing of [NORTH, EAST, SOUTH, WEST]) {
  for (let mask = 0; mask < 1024; mask += 1) {
    assert.equal(facingMask(mask, facing) & ~WIRE, 0, `facing ${facing}, mask ${mask} escaped the wire bits`);
  }
}

// ---------------------------------------------------------------------------
// eyeLevelWalkMask: W and S are the up and down arrows at eye level
// ---------------------------------------------------------------------------
assert.equal(eyeLevelWalkMask("KeyW"), UP, "W is the up arrow");
assert.equal(eyeLevelWalkMask("KeyS"), DOWN, "S is the down arrow");
for (const code of ["KeyA", "KeyD", "ArrowUp", "ArrowDown", "KeyQ", "Space"]) {
  assert.equal(eyeLevelWalkMask(code), 0, `${code} is not an eye-level walk key`);
}
// Read in the body frame like the arrows they stand for: W walks the way you
// face, S walks backwards without turning round.
assert.equal(facingMask(eyeLevelWalkMask("KeyW"), EAST), RIGHT, "facing east, W walks east");
assert.equal(facingMask(eyeLevelWalkMask("KeyS"), EAST), LEFT, "facing east, S walks west");
assert.equal(facingMask(eyeLevelWalkMask("KeyW"), SOUTH), DOWN, "facing south, W walks south");

console.log("view3d input3d ok");
