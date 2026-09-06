import assert from "node:assert/strict";
import { build } from "esbuild";

const out = await build({ entryPoints: ["src/input.ts"], bundle: true, format: "esm", platform: "node", write: false });
const {
  InputMaskUp, InputMaskDown, InputMaskLeft, InputMaskRight, InputMaskShift, InputMaskShoot,
  commandKey, isMovementKey, movementMask, facingMask, facingOfMask, KeyEnter, KeyEscape,
} = await import(`data:text/javascript;base64,${Buffer.from(out.outputFiles[0].contents).toString("base64")}`);

// The vanilla vocabulary, and nothing more: no WASD, no V on the wire.
assert.equal(movementMask(new Set(["ArrowUp", "Space"])), InputMaskUp | InputMaskShoot);
assert.equal(movementMask(new Set(["Numpad4", "ShiftLeft"])), InputMaskLeft | InputMaskShift);
assert.equal(isMovementKey("KeyW"), false);
assert.equal(isMovementKey("KeyV"), false);
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

assert.equal(facingOfMask(0), null);
assert.equal(facingOfMask(InputMaskRight), 1);
assert.equal(facingOfMask(InputMaskDown | InputMaskShoot), 2);

console.log("input ok");
