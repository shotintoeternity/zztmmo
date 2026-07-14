// M5.12 — navigable .HLP help windows.
//
// The editor's help (EDITOR.HLP, opened by the editor's H key) and the title
// About window are graphs of cross-referenced .HLP files. Vanilla
// TextWindowSelect (engine/txtwind.go:163, TXTWIND.PAS) resolves two link kinds
// while viewing a file: a "!-FILE" link replaces the window with that file, and a
// bare "!label" link jumps to the ":label" line in the current file. The label
// jump lives in the modal (modal.ts jumpToLabel); this module owns the file jump,
// which needs a fetch, plus a browser-only back path so a sub-topic can be left
// without closing the whole window.
//
// Help windows are read-only (selectable === false), so they never send
// sendScrollReply — object scrolls (M3.10) stay on that path untouched.

import type { TextModal } from "./modal";

/** A loaded help file: the window title (constant across navigation, as vanilla
 *  never resets state.Title) and its text-window lines. */
export type HelpFrame = { title: string; lines: string[] };

export type HelpDeps = {
  /** Fetches a .HLP file's lines by name, e.g. "CREATURE.HLP". Throws on error. */
  fetchLines: (file: string) => Promise<string[]>;
  /** Installs the built modal (main.ts openModal, or a capture in tests). */
  openModal: (modal: TextModal) => void;
};

// helpFileFor maps a "-FILE" hyperlink pointer to its help filename. Vanilla
// TextWindowOpenFile appends ".HLP" (txtwind.go:391-396); we also uppercase so a
// case-sensitive server (Linux) finds CREATURE.HLP from "!-creature".
export function helpFileFor(pointer: string): string {
  return pointer.toUpperCase() + ".HLP";
}

function showHelpFrame(frame: HelpFrame, stack: HelpFrame[], deps: HelpDeps) {
  deps.openModal({
    kind: "text",
    state: { title: frame.title, lines: frame.lines, linePos: 1, viewingFile: true },
    baseTitle: frame.title,
    moved: false,
    selectable: false,
    onSelect: () => {},
    onOpenFile: (pointer) => {
      void showHelpFile(helpFileFor(pointer), frame.title, [...stack, frame], deps);
    },
    onBack:
      stack.length > 0
        ? () => showHelpFrame(stack[stack.length - 1], stack.slice(0, -1), deps)
        : undefined,
  });
}

async function showHelpFile(file: string, title: string, stack: HelpFrame[], deps: HelpDeps) {
  let lines: string[];
  try {
    lines = await deps.fetchLines(file);
  } catch {
    lines = ["", "  Not available: the server did not answer.", ""];
  }
  if (lines.length === 0) {
    // handleHelp returns 404 for a missing file, which fetchLines turns into an
    // error above; an empty body still gets a window rather than a dead end.
    lines = ["", "  This help topic is not available.", ""];
  }
  showHelpFrame({ title, lines }, stack, deps);
}

// openHelp opens a .HLP file as the root of a navigable help window.
export function openHelp(file: string, title: string, deps: HelpDeps) {
  void showHelpFile(file, title, [], deps);
}
