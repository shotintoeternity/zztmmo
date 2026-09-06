// modals.ts — the windows a player answers: scrolls and help (a text window),
// the yes/no sidebar prompt, and the sidebar string prompt.
//
// Transcribed from the 2D client's modal.ts, reduced to what play needs. The
// text window draws through textwindow.ts; the two prompts draw where ZZT
// draws them, in the sidebar (GAME.PAS SidebarPromptYesNo and
// SidebarPromptString).

import { clampLinePos, renderTextWindow, TEXT_WINDOW_PAGE, type TextWindowState } from "./textwindow";
import type { WriteText } from "./sidebar";
import { sidebarClearLine } from "./sidebar";

export type TextModal = {
  kind: "text";
  state: TextWindowState;
  /** Enter on a "!label;text" line picks the label; plain Enter closes. */
  onSelect?: (label: string) => void;
  onClose?: () => void;
};

export type YesNoModal = {
  kind: "yesno";
  question: string;
  onAnswer: (yes: boolean) => void;
};

export type EntryModal = {
  kind: "entry";
  prompt: string;
  buffer: string;
  width: number;
  /** null when the prompt was cancelled. */
  onSubmit: (text: string | null) => void;
};

export type Modal = TextModal | YesNoModal | EntryModal;

export type KeyResult = "close" | "redraw" | "ignore";

/** hyperlinkOf is the label a "!label;text" line points at, or "". A "!-FILE" link is a file, not a label. */
export function hyperlinkOf(line: string): string {
  if (!line.startsWith("!")) {
    return "";
  }
  let pointer = line.slice(1);
  const semi = pointer.indexOf(";");
  if (semi >= 0) {
    pointer = pointer.slice(0, semi);
  }
  if (pointer.startsWith("-")) {
    return "";
  }
  return pointer;
}

export function newTextModal(title: string, lines: string[], viewingFile: boolean, onSelect?: (label: string) => void, onClose?: () => void): TextModal {
  return {
    kind: "text",
    state: { title, lines, linePos: 1, viewingFile },
    onSelect,
    onClose,
  };
}

export function renderModal(write: WriteText, modal: Modal) {
  switch (modal.kind) {
    case "text":
      renderTextWindow(write, modal.state);
      break;
    case "yesno":
      // SidebarPromptYesNo: the question at 63,5 with a cursor cell after it.
      sidebarClearLine(write, 3);
      sidebarClearLine(write, 4);
      sidebarClearLine(write, 5);
      write(63, 5, 0x1f, modal.question);
      write(63 + modal.question.length, 5, 0x9e, "_");
      break;
    case "entry": {
      // SidebarPromptString: the prompt on row 3, the field on row 4.
      sidebarClearLine(write, 3);
      sidebarClearLine(write, 4);
      sidebarClearLine(write, 5);
      write(63, 3, 0x1f, modal.prompt);
      write(63, 4, 0x1e, modal.buffer.padEnd(modal.width, " "));
      const cursor = Math.min(modal.buffer.length, modal.width - 1);
      write(63 + cursor, 4, 0x9e, modal.buffer.length < modal.width ? "_" : modal.buffer[cursor]);
      break;
    }
  }
}

export function modalKey(modal: Modal, event: { code: string; key: string; ctrlKey?: boolean; metaKey?: boolean; altKey?: boolean }): KeyResult {
  switch (modal.kind) {
    case "text":
      return textKey(modal, event.code);
    case "yesno":
      if (event.code === "KeyY") {
        modal.onAnswer(true);
        return "close";
      }
      if (event.code === "KeyN" || event.code === "Escape") {
        modal.onAnswer(false);
        return "close";
      }
      return "ignore";
    case "entry":
      return entryKey(modal, event);
  }
}

function textKey(modal: TextModal, code: string): KeyResult {
  const state = modal.state;
  let next = state.linePos;
  switch (code) {
    case "ArrowUp":
      next -= 1;
      break;
    case "ArrowDown":
      next += 1;
      break;
    case "PageUp":
      next -= TEXT_WINDOW_PAGE;
      break;
    case "PageDown":
      next += TEXT_WINDOW_PAGE;
      break;
    case "Escape":
      modal.onClose?.();
      return "close";
    case "Enter": {
      const current = state.lines[state.linePos - 1] ?? "";
      const label = hyperlinkOf(current);
      if (label && modal.onSelect) {
        modal.onSelect(label);
        return "close";
      }
      modal.onClose?.();
      return "close";
    }
    default:
      return "ignore";
  }
  state.linePos = clampLinePos(next, state.lines.length);
  return "redraw";
}

function entryKey(modal: EntryModal, event: { code: string; key: string; ctrlKey?: boolean; metaKey?: boolean; altKey?: boolean }): KeyResult {
  if (event.code === "Escape") {
    modal.onSubmit(null);
    return "close";
  }
  if (event.code === "Enter") {
    modal.onSubmit(modal.buffer);
    return "close";
  }
  if (event.code === "Backspace") {
    if (modal.buffer.length === 0) {
      return "ignore";
    }
    modal.buffer = modal.buffer.slice(0, -1);
    return "redraw";
  }
  if (event.ctrlKey || event.metaKey || event.altKey || event.key.length !== 1) {
    return "ignore";
  }
  if (modal.buffer.length >= modal.width) {
    return "ignore";
  }
  const ch = event.key.charCodeAt(0);
  if (ch < 0x20 || ch > 0x7e) {
    return "ignore";
  }
  modal.buffer += event.key;
  return "redraw";
}
