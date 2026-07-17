// On-screen touch controls for phones. ZZT is a keyboard game and the client has
// no physical keyboard on a phone, so this bar gives the essential keys as tap
// targets: a direction pad (menu navigation AND in-game movement), Enter, an
// explicit soft-keyboard toggle, and the title-menu World / Play commands. Each
// button drives the SAME key handlers a physical key would (see main.ts), so
// there is one input path, not two.

export type TouchKeyHandler = (down: boolean, code: string, key: string) => void;

export type TouchControlHandlers = {
  // Dispatch a synthetic key. `down` distinguishes press from release so a held
  // direction keeps moving in gameplay; a tapped menu key sends down then up.
  key: TouchKeyHandler;
  // Raise the soft keyboard if it is down, dismiss it if it is up.
  toggleKeyboard: () => void;
};

type ButtonSpec =
  | { label: string; kind: "key"; code: string; key: string; hold: boolean; group: "dpad" | "action" }
  | { label: string; kind: "keyboard"; group: "action" };

// Data-driven so a test can assert the mapping without a real DOM.
export const TOUCH_BUTTONS: ButtonSpec[] = [
  { label: "▲", kind: "key", code: "ArrowUp", key: "ArrowUp", hold: true, group: "dpad" },
  { label: "◄", kind: "key", code: "ArrowLeft", key: "ArrowLeft", hold: true, group: "dpad" },
  { label: "►", kind: "key", code: "ArrowRight", key: "ArrowRight", hold: true, group: "dpad" },
  { label: "▼", kind: "key", code: "ArrowDown", key: "ArrowDown", hold: true, group: "dpad" },
  { label: "⏎", kind: "key", code: "Enter", key: "Enter", hold: false, group: "action" },
  { label: "⌨", kind: "keyboard", group: "action" },
  { label: "World", kind: "key", code: "KeyW", key: "w", hold: false, group: "action" },
  { label: "Play", kind: "key", code: "KeyP", key: "p", hold: false, group: "action" },
];

/**
 * createTouchControls builds the control bar and wires each button to `handlers`.
 * It is a no-op on non-touch devices so desktop is untouched. Returns the bar
 * element (or null when skipped) so callers can toggle visibility later.
 */
export function createTouchControls(
  host: Document,
  handlers: TouchControlHandlers,
  maxTouchPoints = typeof navigator === "undefined" ? 0 : navigator.maxTouchPoints,
): HTMLElement | null {
  if (maxTouchPoints <= 0) {
    return null;
  }
  const bar = host.createElement("div");
  bar.className = "touch-controls";
  bar.setAttribute("aria-hidden", "true");

  const dpad = host.createElement("div");
  dpad.className = "touch-dpad";
  const action = host.createElement("div");
  action.className = "touch-actions";

  for (const spec of TOUCH_BUTTONS) {
    const button = host.createElement("button") as HTMLButtonElement;
    button.type = "button";
    button.tabIndex = -1;
    button.className = "touch-btn touch-btn-" + spec.group;
    button.textContent = spec.label;
    wireButton(button, spec, handlers);
    (spec.group === "dpad" ? dpad : action).appendChild(button);
  }

  bar.appendChild(dpad);
  bar.appendChild(action);
  host.body.appendChild(bar);
  return bar;
}

function wireButton(button: HTMLElement, spec: ButtonSpec, handlers: TouchControlHandlers) {
  // preventDefault on the press keeps focus on the hidden text input (so the soft
  // keyboard does not drop when a control is tapped) and suppresses the synthetic
  // mouse/click a touch would otherwise generate.
  const press = (event: Event) => {
    event.preventDefault();
    if (spec.kind === "keyboard") {
      handlers.toggleKeyboard();
      return;
    }
    handlers.key(true, spec.code, spec.key);
    if (!spec.hold) {
      // A tap: release immediately so it reads as one discrete press.
      handlers.key(false, spec.code, spec.key);
    }
  };
  const release = (event: Event) => {
    event.preventDefault();
    if (spec.kind === "key" && spec.hold) {
      handlers.key(false, spec.code, spec.key);
    }
  };
  button.addEventListener("pointerdown", press);
  button.addEventListener("pointerup", release);
  button.addEventListener("pointercancel", release);
  button.addEventListener("pointerleave", release);
}
