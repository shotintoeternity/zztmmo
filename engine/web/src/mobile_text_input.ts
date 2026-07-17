// Mobile browsers do not raise the on-screen keyboard for a focused canvas.
// This bridge owns a one-pixel native control only while an editable ZZT modal
// is open, forwarding committed input to the canvas/modal owner.

import { modalAcceptsTextInput, type Modal, type ModalTextInput } from "./modal";

// Re-export the pure mirror seam so the browser bridge and its Node regression
// exercise the exact same modal-buffer adapter.
export { handleModalTextInput, modalAcceptsTextInput } from "./modal";

export type MobileTextInputSink = (input: ModalTextInput) => void;

export class MobileTextInputBridge {
  private element: HTMLInputElement | HTMLTextAreaElement | null = null;
  private modal: Modal | null = null;
  private sink: MobileTextInputSink | null = null;
  private touchSeen = false;
  private composing = false;
  private compositionText = "";
  private skipCommittedText = "";

  constructor(
    private readonly host: Document,
    private readonly maxTouchPoints = typeof navigator === "undefined" ? 0 : navigator.maxTouchPoints,
  ) {}

  /** Covers hybrid devices whose maxTouchPoints is unreliable. This is also the
   *  sole place we raise the keyboard: iOS only shows it when focus() runs inside
   *  a real user gesture, and a modal can open outside one (launch, a server
   *  event). Focusing here — inside the canvas touchstart — is what makes the
   *  keyboard appear and, because the canvas tap no longer steals focus, stay up. */
  noteTouchStart() {
    this.touchSeen = true;
    this.sync(this.modal, this.sink ?? (() => {}));
    if (this.element) {
      this.focus(this.element);
    }
  }

  /** True while an editable overlay is (or should be) mounted. The canvas tap
   *  handler consults this to keep the keyboard up: it swallows the tap's default
   *  focus so the focusable (tabindex=0) canvas cannot steal focus and dismiss
   *  the keyboard. */
  isActive(): boolean {
    return this.shouldUseOverlay(this.modal);
  }

  /** The on-screen ⌨ button: raise the soft keyboard if it is down, dismiss it if
   *  it is up. A no-op when no editable modal is open (nothing to type into).
   *  Must run inside the button's own tap gesture — that is when iOS will show it. */
  toggleKeyboard() {
    if (!this.element) {
      return;
    }
    if (this.isFocused()) {
      this.element.blur();
    } else {
      this.focus(this.element);
    }
  }

  sync(modal: Modal | null, sink: MobileTextInputSink) {
    const unchanged = this.modal === modal && this.sink === sink;
    this.modal = modal;
    this.sink = sink;
    if (!this.shouldUseOverlay(modal)) {
      this.remove();
      return;
    }
    if (unchanged && this.element) {
      // Already mounted. Do NOT focus here — a non-gesture focus() will not raise
      // the iOS keyboard, and re-focusing an already-focused field is what makes
      // it flicker. The canvas touchstart handler focuses inside the gesture.
      return;
    }
    // If the keyboard is already up (an editable→editable modal swap), move focus
    // to the replacement field synchronously so iOS keeps it up; a cold open waits
    // for the first tap because iOS needs a gesture.
    const keepKeyboard = this.isFocused();
    this.remove();

    // Dream and chat submit on Enter, so only the source-code editor needs a
    // native textarea (where a line break is actual document content).
    const multiline = modal!.kind === "programEditor";
    const input = this.host.createElement(multiline ? "textarea" : "input") as HTMLInputElement | HTMLTextAreaElement;
    input.setAttribute("aria-hidden", "true");
    input.setAttribute("autocapitalize", "off");
    input.setAttribute("autocomplete", "off");
    input.setAttribute("spellcheck", "false");
    input.style.position = "fixed";
    input.style.left = "0";
    input.style.bottom = "0";
    input.style.width = "1px";
    input.style.height = "1px";
    input.style.opacity = "0.01";
    input.style.border = "0";
    input.style.padding = "0";
    input.style.pointerEvents = "none";
    input.style.zIndex = "1";
    // iOS zooms the whole page when focusing an input whose font is under 16px —
    // that zoom is the "layout is broken on my phone" symptom. 16px suppresses it.
    input.style.fontSize = "16px";
    input.addEventListener("compositionstart", () => {
      this.composing = true;
      this.compositionText = "";
      this.skipCommittedText = "";
    });
    input.addEventListener("compositionupdate", (event) => {
      const composition = event as CompositionEvent;
      this.compositionText = composition.data;
    });
    input.addEventListener("compositionend", (event) => {
      const composition = event as CompositionEvent;
      const text = composition.data || this.compositionText;
      this.composing = false;
      this.compositionText = "";
      this.skipCommittedText = text;
      queueMicrotask(() => {
        if (this.skipCommittedText === text) {
          this.skipCommittedText = "";
        }
      });
      input.value = "";
      if (text) {
        this.deliver({ inputType: "insertText", data: text });
      }
    });
    input.addEventListener("input", (event) => {
      const native = event as InputEvent;
      const data = native.data ?? input.value;
      if (this.composing || native.inputType === "insertCompositionText") {
        this.compositionText = data;
        return;
      }
      if (this.skipCommittedText && native.inputType === "insertText" && data === this.skipCommittedText) {
        this.skipCommittedText = "";
        input.value = "";
        return;
      }
      this.skipCommittedText = "";
      input.value = "";
      this.deliver({ inputType: native.inputType, data });
    });
    if (!multiline) {
      // Commit the soft-keyboard Return. A single-line <input> rejects newlines,
      // so on iOS the Return mutates nothing and fires no `input` event; it is
      // seen only as a `keydown` (key "Enter", a non-character key immune to the
      // 229-composing problem) and/or a `beforeinput` insertLineBreak. Route
      // either to the same commit path desktop Enter uses, so name popups, chat,
      // and single-line editor prompts submit from the on-screen keyboard. The
      // textarea (programEditor) is excluded — there Enter is real newline content.
      input.enterKeyHint = modal!.kind === "chat" ? "send" : "go";
      const submitEnter = () => {
        input.value = "";
        this.skipCommittedText = "";
        this.deliver({ inputType: "insertLineBreak", data: null });
      };
      input.addEventListener("keydown", (event) => {
        const key = event as KeyboardEvent;
        if (!this.composing && key.key === "Enter") {
          // preventDefault also cancels the `beforeinput` this keydown would
          // spawn, so Enter commits exactly once when a keyboard fires both.
          key.preventDefault();
          submitEnter();
        }
      });
      input.addEventListener("beforeinput", (event) => {
        const native = event as InputEvent;
        if (this.composing) {
          return;
        }
        if (native.inputType === "insertLineBreak" || native.inputType === "insertParagraph") {
          native.preventDefault();
          submitEnter();
        }
      });
    }
    this.element = input;
    this.host.body.appendChild(input);
    if (keepKeyboard) {
      this.focus(input);
    }
  }

  close(): boolean {
    const wasOpen = this.element !== null;
    this.modal = null;
    this.remove();
    return wasOpen;
  }

  private shouldUseOverlay(modal: Modal | null): boolean {
    return modalAcceptsTextInput(modal) && (this.maxTouchPoints > 0 || this.touchSeen);
  }

  private deliver(input: ModalTextInput) {
    if (this.modal && this.sink) {
      this.sink(input);
    }
  }

  private focus(input: HTMLInputElement | HTMLTextAreaElement) {
    try {
      input.focus({ preventScroll: true });
    } catch {
      input.focus();
    }
  }

  private isFocused(): boolean {
    return this.element !== null && this.host.activeElement === this.element;
  }

  private remove() {
    if (!this.element) {
      return;
    }
    this.element.blur();
    this.element.remove();
    this.element = null;
    this.composing = false;
    this.compositionText = "";
    this.skipCommittedText = "";
  }
}
