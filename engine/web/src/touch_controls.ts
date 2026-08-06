// On-screen touch controls for phones. ZZT is a keyboard game and the client has
// no physical keyboard on a phone, so this bar gives the essential keys as tap
// targets: a direction pad (menu navigation AND in-game movement), Enter, an
// explicit soft-keyboard toggle, the title-menu World / Play / Color commands,
// and — the
// M16.18a half — the gameplay controls a phone had no way to reach at all: Fire,
// Torch and Pause. Each button drives the SAME key handlers a physical key would
// (see main.ts), so there is one input path, not two, and no vocabulary reaches
// the simulation that a keyboard could not already produce.
//
// M21.5 adds the multiplayer half of the same gap: Chat and Players open the two
// windows that were on letter keys a phone cannot press, and Esc / Yes / No are
// what a window and its confirmation answer to once one is open.
//
// FIRE IS THE SPACE BAR, not a new idea. ElementPlayerTick shoots on
// `InputShiftPressed || InputKeyPressed == ' '` (elements.go:1424), and the
// server's keymask decode sets Shift for the shoot bit too
// (inputMessageToPlayerInput), so holding this one button covers both of
// vanilla's firing shapes: alone it repeats along the player's facing (Space),
// and held together with a direction on the pad it fires along that direction
// and suppresses the step (Shift+dir). One button, because a phone has one
// thumb free.
//
// A BUTTON IS ONLY ON SCREEN WHERE IT MEANS SOMETHING (`modes`). That is not
// tidiness: Fire is a space, and behind an open text surface a space belongs in
// the buffer, not in the game — a gameplay control that is not on screen cannot
// leak a tap into it.
//
// THE BAR RESERVES ITS OWN HEIGHT (M16.18b). It is `position: fixed`, so left to
// itself it is drawn ON the board: on a landscape phone the letterboxed screen
// fills the viewport's height and the controls land on the bottom seven text
// rows — the end of the board and the sidebar's Save/Pause/Quit block. The bar
// therefore publishes its measured height as the `--touch-bar-h` custom
// property, which style.css subtracts from the screen's box so the screen
// letterboxes ABOVE the bar. Measured rather than declared as a constant,
// because the action row wraps at narrow widths and `setMode` changes how many
// controls are in it: play mode's seven wrap to three rows on a 390px portrait
// phone (a 150px bar, against 96px for the two the title screen needs) and stay
// on one at 844px landscape.

export type TouchKeyHandler = (down: boolean, code: string, key: string) => void;

export type TouchControlHandlers = {
  // Dispatch a synthetic key. `down` distinguishes press from release so a held
  // direction keeps moving in gameplay; a tapped menu key sends down then up.
  key: TouchKeyHandler;
  // Raise the soft keyboard if it is down, dismiss it if it is up.
  toggleKeyboard: () => void;
};

// What the player is looking at. `modal` wins over the others: an open window
// owns the keys whichever screen it is drawn over. `prompt` is the one modal told
// apart from the rest (M21.5), because a yes/no prompt answers to three keys and
// none of them is Enter — see PROMPT_ONLY below.
//
// `watching` is the read-only room (M22.1). It is the one mode with no direction
// pad: a watcher's input sampler is off and the server drops anything it sends,
// so every movement control would be a button that does nothing — and a bar full
// of dead buttons is how a phone learns to distrust the whole bar.
export type TouchControlMode = "title" | "playing" | "editor" | "modal" | "prompt" | "watching";

const EVERY_MODE: readonly TouchControlMode[] = ["title", "playing", "editor", "modal", "prompt"];
const PLAYING_ONLY: readonly TouchControlMode[] = ["playing"];
const TITLE_ONLY: readonly TouchControlMode[] = ["title"];
// Every window, of either shape. Escape closes a text window and answers "no" to
// a prompt (modal.ts), so the one control means the same thing in both.
const ANY_WINDOW: readonly TouchControlMode[] = ["modal", "prompt"];
const PROMPT_ONLY: readonly TouchControlMode[] = ["prompt"];
const WATCHING_ONLY: readonly TouchControlMode[] = ["watching"];

type ButtonSpec =
  | {
      // `id` reaches the DOM as data-touch, so a test (and CSS) can name a
      // control without matching a glyph — "Play" and "Pause" are one key byte
      // apart and ▲ is not a thing a selector should have to spell.
      id: string;
      label: string;
      kind: "key";
      code: string;
      key: string;
      hold: boolean;
      group: "dpad" | "action";
      modes: readonly TouchControlMode[];
    }
  | { id: string; label: string; kind: "keyboard"; group: "action"; modes: readonly TouchControlMode[] };

// Data-driven so a test can assert the mapping without a real DOM.
//
// The action row is in DOM order and laid out flush right (style.css), so Fire
// — the one control a player holds rather than pokes — ends up nearest the
// thumb while a room is being played.
export const TOUCH_BUTTONS: ButtonSpec[] = [
  { id: "up", label: "▲", kind: "key", code: "ArrowUp", key: "ArrowUp", hold: true, group: "dpad", modes: EVERY_MODE },
  { id: "left", label: "◄", kind: "key", code: "ArrowLeft", key: "ArrowLeft", hold: true, group: "dpad", modes: EVERY_MODE },
  { id: "right", label: "►", kind: "key", code: "ArrowRight", key: "ArrowRight", hold: true, group: "dpad", modes: EVERY_MODE },
  { id: "down", label: "▼", kind: "key", code: "ArrowDown", key: "ArrowDown", hold: true, group: "dpad", modes: EVERY_MODE },
  { id: "keyboard", label: "⌨", kind: "keyboard", group: "action", modes: EVERY_MODE },
  { id: "enter", label: "⏎", kind: "key", code: "Enter", key: "Enter", hold: false, group: "action", modes: EVERY_MODE },
  // M19.2: the color picker's 'C'. TITLE_ONLY is load-bearing, not tidiness —
  // in play mode that same key byte opens chat, and the window this button is
  // for is only reachable from the title menu. Once it is open the picker is
  // driven by the d-pad and Enter, which are in EVERY_MODE.
  { id: "color", label: "Color", kind: "key", code: "KeyC", key: "c", hold: false, group: "action", modes: TITLE_ONLY },
  { id: "world", label: "World", kind: "key", code: "KeyW", key: "w", hold: false, group: "action", modes: TITLE_ONLY },
  { id: "play", label: "Play", kind: "key", code: "KeyP", key: "p", hold: false, group: "action", modes: TITLE_ONLY },
  // M21.5: the two windows a phone had no way to open. Both are letter keys
  // main.ts handles in its play-mode branch and nowhere else — 'C' is the color
  // picker on the title screen, which is why Chat is PLAYING_ONLY like Pause
  // rather than sharing the Color button above.
  { id: "chat", label: "Chat", kind: "key", code: "KeyC", key: "c", hold: false, group: "action", modes: PLAYING_ONLY },
  { id: "players", label: "Players", kind: "key", code: "KeyL", key: "l", hold: false, group: "action", modes: PLAYING_ONLY },
  // Pause and Play are the same key byte ('P'); which of them is on screen is
  // the whole difference between the title menu's "start" and play mode's
  // GamePaused, and the player should not have to know they are one key.
  { id: "pause", label: "Pause", kind: "key", code: "KeyP", key: "p", hold: false, group: "action", modes: PLAYING_ONLY },
  { id: "torch", label: "Torch", kind: "key", code: "KeyT", key: "t", hold: false, group: "action", modes: PLAYING_ONLY },
  // hold: the shoot bit must still be set when the tick that consumes it runs,
  // and holding is also how Space repeats — one shot per tick while ammo lasts.
  { id: "fire", label: "Fire", kind: "key", code: "Space", key: " ", hold: true, group: "action", modes: PLAYING_ONLY },
  // M21.5, the way back out. A window a phone can open and not close is worse
  // than one it cannot open: Enter closes a plain window but a picker with a
  // header ignores it (modal.ts requireSelection), and no window closes on the
  // pad. Escape closes every one of them, and at a prompt it is the "no" answer.
  { id: "esc", label: "Esc", kind: "key", code: "Escape", key: "Escape", hold: false, group: "action", modes: ANY_WINDOW },
  // A yes/no prompt takes Y, N or Escape and nothing else (yesNoKey mirrors
  // SidebarPromptYesNo) — so without these two a phone reaches the block
  // confirmation the Players window opens and can only decline it.
  { id: "yes", label: "Yes", kind: "key", code: "KeyY", key: "y", hold: false, group: "action", modes: PROMPT_ONLY },
  { id: "no", label: "No", kind: "key", code: "KeyN", key: "n", hold: false, group: "action", modes: PROMPT_ONLY },
  // M22.1: the only control a watcher gets, and the only one it needs. A room
  // you can enter on a phone and not leave is a dead end, and Escape is what
  // main.ts's watching branch answers to — the same key that closes a window,
  // because both mean "put me back where I was".
  { id: "leave", label: "Leave", kind: "key", code: "Escape", key: "Escape", hold: false, group: "action", modes: WATCHING_ONLY },
];

export type TouchControls = {
  /** The bar itself, so callers can toggle visibility later. */
  element: HTMLElement;
  /** Show exactly the controls that mean something on the current screen. */
  setMode(mode: TouchControlMode): void;
};

/**
 * createTouchControls builds the control bar and wires each button to `handlers`.
 * It is a no-op on non-touch devices so desktop is untouched. Returns null when
 * it is skipped.
 *
 * The gate is `navigator.maxTouchPoints` at load, deliberately and unlike
 * MobileTextInputBridge's `touchSeen` fallback: a bar that appeared partway
 * through a session would move the board out from under the player's thumb, and
 * the two gates are certified separately by M16.18's matrix (the WebKit touch
 * profile reports zero touch points and correctly gets no bar).
 */
export function createTouchControls(
  host: Document,
  handlers: TouchControlHandlers,
  maxTouchPoints = typeof navigator === "undefined" ? 0 : navigator.maxTouchPoints,
): TouchControls | null {
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

  const built: { button: HTMLElement; spec: ButtonSpec }[] = [];
  for (const spec of TOUCH_BUTTONS) {
    const button = host.createElement("button") as HTMLButtonElement;
    button.type = "button";
    button.tabIndex = -1;
    button.className = "touch-btn touch-btn-" + spec.group + labelClass(spec);
    button.setAttribute("data-touch", spec.id);
    button.textContent = spec.label;
    wireButton(button, spec, handlers);
    (spec.group === "dpad" ? dpad : action).appendChild(button);
    built.push({ button, spec });
  }

  bar.appendChild(dpad);
  bar.appendChild(action);
  host.body.appendChild(bar);

  const publishHeight = reserveBarHeight(host, bar);

  const controls: TouchControls = {
    element: bar,
    setMode(mode: TouchControlMode) {
      for (const { button, spec } of built) {
        button.hidden = spec.modes.indexOf(mode) < 0;
      }
      // Hiding a control can unwrap the action row, so the reservation is
      // republished here as well as from the observer: a mode change must not
      // leave the screen letterboxed against last mode's bar for a frame.
      publishHeight();
    },
  };
  // The client opens on the title screen (main.ts), so start there rather than
  // showing every control for the first frame.
  controls.setMode("title");
  return controls;
}

/**
 * reserveBarHeight publishes the bar's height as `--touch-bar-h` on the root
 * element and keeps it current, returning the republish function so a caller
 * that changes the bar's contents can settle the value in the same frame.
 *
 * It is a no-op against anything that is not a real DOM (the unit test's fake
 * host measures nothing), which is safe: the property's declared default is
 * `0px`, i.e. exactly today's un-reserved layout.
 */
function reserveBarHeight(host: Document, bar: HTMLElement): () => void {
  const root = host.documentElement;
  if (!root || typeof root.style?.setProperty !== "function" || typeof bar.getBoundingClientRect !== "function") {
    return () => {};
  }
  let published = "";
  const publish = () => {
    // Round up: half a CSS pixel of under-reservation is half a pixel of board
    // under a button.
    const next = Math.ceil(bar.getBoundingClientRect().height) + "px";
    if (next === published) {
      return; // also what keeps the observer below from feeding itself
    }
    published = next;
    root.style.setProperty("--touch-bar-h", next);
  };
  publish();
  // Rotation, a soft keyboard resizing the viewport, and a wrapped action row
  // all change the bar's box; the observer is what makes the reservation track
  // them rather than freeze at load.
  if (typeof ResizeObserver !== "undefined") {
    new ResizeObserver(publish).observe(bar);
  }
  return publish;
}

// A per-control class only for the ones style.css singles out; the rest share
// the group class.
function labelClass(spec: ButtonSpec): string {
  return spec.id === "fire" ? " touch-btn-fire" : "";
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
