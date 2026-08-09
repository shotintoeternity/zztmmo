import assert from "node:assert/strict";
import { build } from "esbuild";

const comfortOutput = await build({
  entryPoints: ["src/comfort.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const keysOutput = await build({
  entryPoints: ["src/keys.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const comfort = await import(`data:text/javascript;base64,${Buffer.from(comfortOutput.outputFiles[0].contents).toString("base64")}`);
const keys = await import(`data:text/javascript;base64,${Buffer.from(keysOutput.outputFiles[0].contents).toString("base64")}`);

const {
  DEFAULT_COMFORT,
  effectiveKeyBindings,
  loadGuestComfort,
  normalizeComfortPreferences,
  paletteColor,
  reducedBlinkOn,
  saveGuestComfort,
  validateComfortPreferences,
} = comfort;
const { InputMaskUp, InputMaskShoot, commandKey, isHandledKey, isMovementKey, movementMask, rawKey } = keys;

{
  assert.equal(commandKey({ code: "KeyT", key: "t" }), "T".charCodeAt(0));
  assert.equal(rawKey("Escape"), 27);
  assert.equal(movementMask(new Set(["ArrowUp", "Space"])), InputMaskUp | InputMaskShoot);
  assert.equal(isMovementKey("KeyW"), false, "vanilla does not grow WASD by accident");
}

{
  const prefs = normalizeComfortPreferences({ keyPreset: "one-handed", reduceFlashing: true, palette: "high-contrast" });
  const bindings = effectiveKeyBindings(prefs);
  assert.equal(isMovementKey("KeyW", bindings), true);
  assert.equal(movementMask(new Set(["KeyW", "KeyF"]), bindings), InputMaskUp | InputMaskShoot);
  assert.equal(commandKey({ code: "KeyR", key: "r" }, bindings), "T".charCodeAt(0));
}

{
  const prefs = normalizeComfortPreferences({
    keyPreset: "custom",
    keyBindings: { up: ["KeyI"], torch: ["KeyO"], dance: ["KeyD"], right: ["bad code"] },
    reduceFlashing: true,
    palette: "colorblind-assist",
  });
  assert.deepEqual(prefs.keyBindings, { up: ["KeyI"], torch: ["KeyO"] });
  assert.equal(validateComfortPreferences(prefs), "");
  assert.equal(commandKey({ code: "KeyO", key: "o" }, effectiveKeyBindings(prefs)), "T".charCodeAt(0));
}

{
  const conflict = normalizeComfortPreferences({ keyBindings: { up: ["KeyT"], torch: ["KeyT"] } });
  assert.equal(validateComfortPreferences(conflict), "key conflict");
}

{
  assert.equal(paletteColor("vanilla", 1), "#0000aa");
  assert.equal(paletteColor("high-contrast", 1), "#0037ff");
  assert.notEqual(paletteColor("colorblind-assist", 4), paletteColor("vanilla", 4));
  assert.equal(reducedBlinkOn(DEFAULT_COMFORT), false);
  assert.equal(reducedBlinkOn({ ...DEFAULT_COMFORT, reduceFlashing: true }), true);
}

{
  const storage = new Map();
  const fakeStorage = {
    getItem: (key) => storage.get(key) ?? null,
    setItem: (key, value) => storage.set(key, value),
  };
  const prefs = normalizeComfortPreferences({ keyPreset: "one-handed", reduceFlashing: true, palette: "high-contrast" });
  saveGuestComfort(fakeStorage, prefs);
  assert.deepEqual(loadGuestComfort(fakeStorage), prefs);
}

console.log("comfort.test.mjs: ok");
