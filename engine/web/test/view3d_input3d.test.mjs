// view3d_input3d.test.mjs — the four camera keys.
//
// The arrows walk, in every view, north/south/east/west, which is what they
// have meant in ZZT since 1991. WASD moves the camera and only the camera: A
// and D swing it, W and S raise and lower it toward the ceiling and the floor.
//
// That makes this suite's job small and worth stating plainly. The certified
// row `input.play-wasd-removed` (M16.10) says W/A/D must reach the server as
// nothing at all, and control_keys.test.mjs proves that on the text screen with
// a real browser. Here we prove the stronger thing the 3D view relies on: these
// keys produce no direction and no key byte anywhere, because they are a look
// control and a look control has nothing to say to a simulation.
//
// An earlier version of this file tested a facingMask that resolved WASD into
// board directions. That scheme is gone -- it made the arrows turn you at eye
// level, which took the game's oldest control away in the view where a player
// is least sure where they are.

import assert from "node:assert/strict";
import { build } from "esbuild";

const out = await build({
  entryPoints: ["src/view3d/input3d.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const { isCameraKey, lookStepFor } = await import(
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
assert.deepEqual(lookStepFor("KeyW"), { dyaw: 0, dpitch: 1 }, "W looks up toward the ceiling");
assert.deepEqual(lookStepFor("KeyS"), { dyaw: 0, dpitch: -1 }, "S looks down toward the floor");

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
// If a movement helper ever reappears here, this fails and the reviewer gets to
// ask why the camera module is producing directions again.
const surface = Object.keys(
  await import(`data:text/javascript;base64,${Buffer.from(out.outputFiles[0].contents).toString("base64")}`),
).sort();
assert.deepEqual(surface, ["isCameraKey", "lookStepFor"], `unexpected exports: ${surface.join(", ")}`);

console.log("view3d input3d ok");
