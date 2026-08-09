export type ComfortPalette = "vanilla" | "high-contrast" | "colorblind-assist";
export type ComfortKeyPreset = "vanilla" | "one-handed" | "custom";
export type KeyAction =
  | "up" | "down" | "left" | "right" | "shoot" | "shift"
  | "enter" | "escape" | "torch" | "pause" | "sound" | "save" | "quit" | "help";
export type KeyBindings = Partial<Record<KeyAction, string[]>>;

export type ComfortPreferences = {
  keyPreset: ComfortKeyPreset;
  keyBindings: KeyBindings;
  reduceFlashing: boolean;
  palette: ComfortPalette;
};

export const DEFAULT_COMFORT: ComfortPreferences = {
  keyPreset: "vanilla",
  keyBindings: {},
  reduceFlashing: false,
  palette: "vanilla",
};

export const COMFORT_STORAGE_KEY = "zztmmo.comfort";

const PALETTES: Record<ComfortPalette, string[]> = {
  vanilla: [
    "#000000", "#0000aa", "#00aa00", "#00aaaa", "#aa0000", "#aa00aa", "#aa5500", "#aaaaaa",
    "#555555", "#5555ff", "#55ff55", "#55ffff", "#ff5555", "#ff55ff", "#ffff55", "#ffffff",
  ],
  "high-contrast": [
    "#000000", "#0037ff", "#00c853", "#00e5ff", "#ff1744", "#d500f9", "#ff9100", "#e0e0e0",
    "#757575", "#7c4dff", "#69f0ae", "#84ffff", "#ff8a80", "#ea80fc", "#ffff00", "#ffffff",
  ],
  "colorblind-assist": [
    "#000000", "#005ab5", "#009e73", "#56b4e9", "#d55e00", "#cc79a7", "#e69f00", "#d0d0d0",
    "#6b6b6b", "#7aa6ff", "#66d9a8", "#9adcf5", "#ff8c42", "#f0a0d5", "#f0e442", "#ffffff",
  ],
};

const PRESETS: Record<ComfortKeyPreset, KeyBindings> = {
  vanilla: {},
  "one-handed": {
    up: ["KeyW"],
    down: ["KeyS"],
    left: ["KeyA"],
    right: ["KeyD"],
    shoot: ["KeyF"],
    torch: ["KeyR"],
    pause: ["KeyE"],
  },
  custom: {},
};

const ACTIONS = new Set<KeyAction>([
  "up", "down", "left", "right", "shoot", "shift", "enter", "escape",
  "torch", "pause", "sound", "save", "quit", "help",
]);
const MOVEMENT = new Set<KeyAction>(["up", "down", "left", "right", "shoot", "shift"]);
const CODE_RE = /^[A-Za-z0-9][A-Za-z0-9_-]{1,31}$/;

export function normalizeComfortPreferences(value: unknown): ComfortPreferences {
  const doc = (value ?? {}) as Partial<ComfortPreferences>;
  const keyPreset: ComfortKeyPreset =
    doc.keyPreset === "one-handed" || doc.keyPreset === "custom" ? doc.keyPreset : "vanilla";
  const palette: ComfortPalette =
    doc.palette === "high-contrast" || doc.palette === "colorblind-assist" ? doc.palette : "vanilla";
  const keyBindings = normalizeKeyBindings(doc.keyBindings);
  return {
    keyPreset,
    keyBindings,
    reduceFlashing: doc.reduceFlashing === true,
    palette,
  };
}

export function normalizeKeyBindings(value: unknown): KeyBindings {
  if (!value || typeof value !== "object") {
    return {};
  }
  const out: KeyBindings = {};
  for (const [action, rawCodes] of Object.entries(value as Record<string, unknown>)) {
    if (!ACTIONS.has(action as KeyAction) || !Array.isArray(rawCodes)) {
      continue;
    }
    const codes = rawCodes.filter((code): code is string => typeof code === "string" && CODE_RE.test(code)).slice(0, 4);
    if (codes.length > 0) {
      out[action as KeyAction] = [...new Set(codes)];
    }
  }
  return out;
}

export function validateComfortPreferences(value: ComfortPreferences): string {
  const seen = new Map<string, boolean>();
  for (const [action, codes] of Object.entries(value.keyBindings) as [KeyAction, string[]][]) {
    if (!ACTIONS.has(action) || !Array.isArray(codes) || codes.length === 0 || codes.length > 4) {
      return "invalid binding";
    }
    const isMovement = MOVEMENT.has(action);
    for (const code of codes) {
      if (!CODE_RE.test(code)) {
        return "invalid key";
      }
      const prior = seen.get(code);
      if (prior !== undefined && prior !== isMovement) {
        return "key conflict";
      }
      seen.set(code, isMovement);
    }
  }
  return "";
}

export function effectiveKeyBindings(comfort: ComfortPreferences): KeyBindings {
  return {
    ...PRESETS.vanilla,
    ...PRESETS[comfort.keyPreset],
    ...comfort.keyBindings,
  };
}

export function paletteColor(palette: ComfortPalette, index: number): string {
  return PALETTES[palette]?.[index & 0x0f] ?? PALETTES.vanilla[index & 0x0f] ?? "#000000";
}

export function reducedBlinkOn(comfort: ComfortPreferences): boolean {
  return comfort.reduceFlashing;
}

export function loadGuestComfort(storage: Storage): ComfortPreferences {
  try {
    return normalizeComfortPreferences(JSON.parse(storage.getItem(COMFORT_STORAGE_KEY) ?? "{}"));
  } catch {
    return DEFAULT_COMFORT;
  }
}

export function saveGuestComfort(storage: Storage, prefs: ComfortPreferences) {
  storage.setItem(COMFORT_STORAGE_KEY, JSON.stringify(normalizeComfortPreferences(prefs)));
}
