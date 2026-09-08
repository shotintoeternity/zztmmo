// view3d_input3d.test.mjs — the eye-level key vocabulary.
//
// This is the boundary the certified row `input.play-wasd-removed` (M16.10)
// polices, seen from the other side. That row says W/A/D reach the server as
// nothing and S opens the save prompt, and control_keys.test.mjs proves it on
// the text screen with a real browser. What it cannot see is the view that did
// not exist when it was written: standing inside your own square, where WASD
// walks and the arrows turn.
//
// So the two suites divide the same decision. control_keys asserts the classic
// side is untouched. This asserts the eye-level side is correct AND that it
// cannot leak: the four pseudo-bits live above the six the server understands
// (input.go), and facingMask is the only thing that turns them into a
// direction. A mask that carried one onto the wire would be a client sending a
// keymask the server has no bit for.

import assert from "node:assert/strict";
import { build } from "esbuild";

const out = await build({
  entryPoints: ["src/view3d/input3d.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const {
  InputMaskStrafeLeft,
  InputMaskStrafeRight,
  InputMaskWalkForward,
  InputMaskWalkBack,
  eyeLevelBits,
  facingMask,
  isEyeLevelKey,
} = await import(`data:text/javascript;base64,${Buffer.from(out.outputFiles[0].contents).toString("base64")}`);

// The six the server understands (keys.ts / input.go), restated here so this
// suite fails if either side renumbers them behind the other's back.
const Up = 1 << 0;
const Down = 1 << 1;
const Left = 1 << 2;
const Right = 1 << 3;
const Shift = 1 << 4;
const Shoot = 1 << 5;
const WIRE = Up | Down | Left | Right | Shift | Shoot;
const PSEUDO = InputMaskStrafeLeft | InputMaskStrafeRight | InputMaskWalkForward | InputMaskWalkBack;

// The pseudo-bits must sit ABOVE the wire bits. If they ever overlapped, a
// strafe would arrive at the server as a real direction without ever being
// resolved against a facing.
assert.equal(PSEUDO & WIRE, 0, "the client's own bits must not collide with the wire's");

// ---------------------------------------------------------------------------
// Which keys the eye-level view claims
// ---------------------------------------------------------------------------
for (const code of ["KeyW", "KeyA", "KeyS", "KeyD"]) {
  assert.equal(isEyeLevelKey(code), true, `${code} walks at eye level`);
}
for (const code of ["ArrowUp", "ArrowLeft", "KeyV", "KeyT", "KeyQ", "Space", "Digit3"]) {
  assert.equal(isEyeLevelKey(code), false, `${code} is not a walking key`);
}

assert.equal(eyeLevelBits(new Set(["KeyW"])), InputMaskWalkForward);
assert.equal(eyeLevelBits(new Set(["KeyS"])), InputMaskWalkBack);
assert.equal(eyeLevelBits(new Set(["KeyA"])), InputMaskStrafeLeft);
assert.equal(eyeLevelBits(new Set(["KeyD"])), InputMaskStrafeRight);
assert.equal(
  eyeLevelBits(new Set(["KeyW", "KeyD", "ArrowUp", "KeyT"])),
  InputMaskWalkForward | InputMaskStrafeRight,
  "keys that are not the four contribute nothing",
);
assert.equal(eyeLevelBits(new Set()), 0);

// ---------------------------------------------------------------------------
// The remap: everything is relative to the way you face
// ---------------------------------------------------------------------------
const NORTH = 0;
const EAST = 1;
const SOUTH = 2;
const WEST = 3;

// W walks the way you are facing, whichever way that is.
assert.equal(facingMask(InputMaskWalkForward, NORTH), Up);
assert.equal(facingMask(InputMaskWalkForward, EAST), Right);
assert.equal(facingMask(InputMaskWalkForward, SOUTH), Down);
assert.equal(facingMask(InputMaskWalkForward, WEST), Left);

// S walks backwards, which is the opposite of that and never a turn.
assert.equal(facingMask(InputMaskWalkBack, NORTH), Down);
assert.equal(facingMask(InputMaskWalkBack, EAST), Left);

// The arrows' own up/down bits mean the same two things, so a player who never
// learns WASD can still walk at eye level.
assert.equal(facingMask(Up, EAST), Right, "the up arrow walks forward too");
assert.equal(facingMask(Down, EAST), Left, "the down arrow walks back too");

// A and D step sideways WITHOUT turning: facing east, A goes north.
assert.equal(facingMask(InputMaskStrafeLeft, EAST), Up);
assert.equal(facingMask(InputMaskStrafeRight, EAST), Down);
assert.equal(facingMask(InputMaskStrafeLeft, NORTH), Left);
assert.equal(facingMask(InputMaskStrafeRight, NORTH), Right);

// Left and right are turns, handled on the key edge, and they never travel.
assert.equal(facingMask(Left, NORTH), 0, "the left arrow turns, it does not walk");
assert.equal(facingMask(Right, NORTH), 0, "the right arrow turns, it does not walk");
assert.equal(facingMask(Left | Right, SOUTH), 0);

// Shift and shoot pass straight through, so Shift+W fires the way you face.
assert.equal(facingMask(InputMaskWalkForward | Shift, WEST), Left | Shift, "Shift+W fires straight ahead");
assert.equal(facingMask(InputMaskStrafeLeft | Shoot, NORTH), Left | Shoot, "Space+A fires to your left");
assert.equal(facingMask(Shift, SOUTH), Shift);

// W and D together are the diagonal ZZT resolves for you: two bits, one frame.
assert.equal(facingMask(InputMaskWalkForward | InputMaskStrafeRight, NORTH), Up | Right);

// ---------------------------------------------------------------------------
// The discipline: nothing the client invented may reach the wire
// ---------------------------------------------------------------------------
// Every combination of every bit, at every facing. The output must be wire bits
// and nothing else -- this is the assertion that would catch a new pseudo-bit
// added to the mask and forgotten in the mask-down at the end of facingMask.
const ALL = WIRE | PSEUDO;
for (let mask = 0; mask <= ALL; mask += 1) {
  for (const facing of [NORTH, EAST, SOUTH, WEST]) {
    const out = facingMask(mask, facing);
    assert.equal(out & PSEUDO, 0, `facingMask(${mask}, ${facing}) leaked a client-only bit`);
    assert.equal(out & ~WIRE, 0, `facingMask(${mask}, ${facing}) produced a bit the server has no name for`);
  }
}

console.log("view3d input3d ok");
