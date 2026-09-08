import assert from "node:assert/strict";
import { build } from "esbuild";

const out = await build({ entryPoints: ["src/input.ts"], bundle: true, format: "esm", platform: "node", write: false });
const {
  InputMaskUp, InputMaskDown, InputMaskLeft, InputMaskRight, InputMaskShift, InputMaskShoot,
  InputMaskStrafeLeft, InputMaskStrafeRight, InputMaskWalkForward, InputMaskWalkBack,
  commandKey, isMovementKey, movementMask, facingMask, facingOfMask, wireMask, ghostDrift, KeyEnter, KeyEscape,
} = await import(`data:text/javascript;base64,${Buffer.from(out.outputFiles[0].contents).toString("base64")}`);

// The vanilla vocabulary on the wire, and nothing more: no V, and -- away from
// eye level -- no W or S walking either. A and D exist, but only as pseudo-bits
// the client resolves itself.
assert.equal(movementMask(new Set(["ArrowUp", "Space"])), InputMaskUp | InputMaskShoot);
assert.equal(movementMask(new Set(["Numpad4", "ShiftLeft"])), InputMaskLeft | InputMaskShift);
assert.equal(isMovementKey("KeyW"), false);
assert.equal(isMovementKey("KeyV"), false);
assert.equal(isMovementKey("KeyA"), true, "a strafe is a movement key");

// WASD is the eye-level scheme, and S is the whole reason it has to be one.
// On the text screen S is ZZT's Save and W is nothing; standing in the board
// they walk, and Save is not reachable until you sit back down.
assert.equal(commandKey({ code: "KeyS", key: "s" }), "S".charCodeAt(0), "the text screen saves");
assert.equal(commandKey({ code: "KeyS", key: "s" }, true), 0, "at eye level S walks back, it does not save");
assert.equal(isMovementKey("KeyS"), false, "S is Save on the text screen, not a step");
assert.equal(isMovementKey("KeyS", true), true);
assert.equal(isMovementKey("KeyW", true), true);
assert.equal(movementMask(new Set(["KeyW", "KeyS"])), 0, "neither walks away from eye level");
assert.equal(
  movementMask(new Set(["KeyW", "KeyS"]), true),
  InputMaskWalkForward | InputMaskWalkBack,
);
// The walk bits are the client's own and must never reach the server, exactly
// as the strafes must not.
assert.equal(wireMask(InputMaskWalkForward | InputMaskWalkBack), 0);
// Save keeps working at eye level for every OTHER command key: only S moved.
assert.equal(commandKey({ code: "KeyT", key: "t" }, true), "T".charCodeAt(0));
assert.equal(commandKey({ code: "KeyQ", key: "q" }, true), "Q".charCodeAt(0));
// F (stand up) and G (ghost) are the client's own, like V: neither is a step
// nor a command, so neither can reach the server.
for (const code of ["KeyF", "KeyG"]) {
  assert.equal(isMovementKey(code), false, `${code} is not a step`);
  assert.equal(commandKey({ code, key: code.slice(3).toLowerCase() }), 0, `${code} is not a command`);
}
assert.equal(commandKey({ code: "KeyV", key: "v" }), 0);
assert.equal(commandKey({ code: "KeyT", key: "t" }), "T".charCodeAt(0));
assert.equal(commandKey({ code: "Enter", key: "Enter" }), KeyEnter);
assert.equal(commandKey({ code: "Escape", key: "Escape" }), KeyEscape);
assert.equal(commandKey({ code: "Slash", key: "?" }), "?".charCodeAt(0));
assert.equal(commandKey({ code: "KeyT", key: "t", metaKey: true }), 0, "a browser shortcut is not a command");

// First person: up walks the way you face, down walks back, turns never travel.
assert.equal(facingMask(InputMaskUp, 0), InputMaskUp);
assert.equal(facingMask(InputMaskUp, 1), InputMaskRight);
assert.equal(facingMask(InputMaskUp, 2), InputMaskDown);
assert.equal(facingMask(InputMaskUp, 3), InputMaskLeft);
assert.equal(facingMask(InputMaskDown, 1), InputMaskLeft);
assert.equal(facingMask(InputMaskLeft | InputMaskRight, 0), 0);
assert.equal(facingMask(InputMaskUp | InputMaskShoot, 3), InputMaskLeft | InputMaskShoot, "Space+Up shoots straight ahead");
assert.equal(facingMask(InputMaskShift, 2), InputMaskShift);

// W and S are the same two directions as up and down, resolved the same way.
assert.equal(facingMask(InputMaskWalkForward, 0), InputMaskUp);
assert.equal(facingMask(InputMaskWalkForward, 1), InputMaskRight);
assert.equal(facingMask(InputMaskWalkBack, 1), InputMaskLeft);
assert.equal(facingMask(InputMaskWalkBack, 0), InputMaskDown);
assert.equal(
  facingMask(InputMaskWalkForward | InputMaskShoot, 3),
  InputMaskLeft | InputMaskShoot,
  "Space+W shoots straight ahead",
);
assert.equal(
  facingMask(InputMaskWalkForward | InputMaskStrafeRight, 0),
  InputMaskUp | InputMaskRight,
  "W and D together walk the diagonal ZZT resolves for you",
);
// A ghost at eye level flies on the same four keys.
assert.equal(ghostDrift(InputMaskWalkForward, true).dz, 1);
assert.equal(ghostDrift(InputMaskWalkBack, true).dz, -1);

// Strafes: A and D step sideways from where you face, and never turn you.
assert.equal(facingMask(InputMaskStrafeLeft, 0), InputMaskLeft);
assert.equal(facingMask(InputMaskStrafeRight, 0), InputMaskRight);
assert.equal(facingMask(InputMaskStrafeLeft, 1), InputMaskUp, "facing east, left is north");
assert.equal(facingMask(InputMaskStrafeRight, 1), InputMaskDown);
assert.equal(facingMask(InputMaskStrafeLeft, 3), InputMaskDown, "facing west, left is south");
assert.equal(facingMask(InputMaskStrafeRight, 2), InputMaskLeft);
assert.equal(
  facingMask(InputMaskUp | InputMaskStrafeRight, 0),
  InputMaskUp | InputMaskRight,
  "walking and strafing at once is a diagonal, which the mask can say",
);
assert.equal(
  facingMask(InputMaskStrafeLeft | InputMaskShoot, 0),
  InputMaskLeft | InputMaskShoot,
  "Space+A shoots to your left",
);

// The pseudo-bits are the client's own and must never reach the server, in
// either view: facingMask resolves them, wireMask drops them.
const SIX_BITS = InputMaskUp | InputMaskDown | InputMaskLeft | InputMaskRight | InputMaskShift | InputMaskShoot;
for (const facing of [0, 1, 2, 3]) {
  for (let mask = 0; mask < 256; mask += 1) {
    assert.equal(facingMask(mask, facing) & ~SIX_BITS, 0, `facingMask leaked a pseudo-bit from ${mask}`);
    assert.equal(wireMask(mask) & ~SIX_BITS, 0, `wireMask leaked a pseudo-bit from ${mask}`);
  }
}
assert.equal(
  wireMask(movementMask(new Set(["KeyA", "ArrowUp"]))),
  InputMaskUp,
  "outside first person a strafe is dropped, not walked",
);

assert.equal(facingOfMask(0), null);
assert.equal(facingOfMask(InputMaskRight), 1);
assert.equal(facingOfMask(InputMaskDown | InputMaskShoot), 2);
assert.equal(facingOfMask(InputMaskStrafeLeft), null, "a strafe never turns you");

// A ghost's keys fly the camera, keeping the meaning the view already gave
// them: arrows are board directions in orbit, and in first person left and
// right are still turns, so the strafes are what move you sideways.
assert.deepEqual(ghostDrift(InputMaskUp, false), { dx: 0, dz: 1 });
assert.deepEqual(ghostDrift(InputMaskDown, false), { dx: 0, dz: -1 });
assert.deepEqual(ghostDrift(InputMaskLeft, false), { dx: -1, dz: 0 });
assert.deepEqual(ghostDrift(InputMaskUp | InputMaskRight, false), { dx: 1, dz: 1 }, "diagonals fly");
assert.deepEqual(ghostDrift(InputMaskLeft | InputMaskRight, false), { dx: 0, dz: 0 }, "opposites cancel");
assert.deepEqual(ghostDrift(InputMaskStrafeLeft, false), { dx: 0, dz: 0 }, "orbit steers with the arrows");
assert.deepEqual(ghostDrift(InputMaskStrafeLeft, true), { dx: -1, dz: 0 });
assert.deepEqual(ghostDrift(InputMaskStrafeRight, true), { dx: 1, dz: 0 });
assert.deepEqual(ghostDrift(InputMaskLeft, true), { dx: 0, dz: 0 }, "in first person left is a turn, not a drift");
assert.deepEqual(ghostDrift(InputMaskUp | InputMaskStrafeRight, true), { dx: 1, dz: 1 });
assert.deepEqual(ghostDrift(InputMaskShoot | InputMaskShift, true), { dx: 0, dz: 0 }, "shooting is not flying");

console.log("input ok");
