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

  /** Covers hybrid devices whose maxTouchPoints is unreliable. */
  noteTouchStart() {
    this.touchSeen = true;
    this.sync(this.modal, this.sink ?? (() => {}));
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
      this.focus(this.element);
      return;
    }
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
      // A single-line <input> rejects newlines, so on iOS the soft-keyboard
      // Return mutates nothing and fires no `input` event — only `beforeinput`
      // reports the insertLineBreak. Route it to the same commit path desktop
      // Enter uses, so name popups, chat, and single-line editor prompts submit
      // from the on-screen keyboard. preventDefault() cancels the no-op default
      // action and the `input` event some keyboards would otherwise chase it
      // with, so Enter is delivered exactly once. The textarea (programEditor)
      // is excluded: there Enter is real newline content, committed via `input`.
      input.enterKeyHint = modal!.kind === "chat" ? "send" : "go";
      input.addEventListener("beforeinput", (event) => {
        const native = event as InputEvent;
        if (this.composing) {
          return;
        }
        if (native.inputType === "insertLineBreak" || native.inputType === "insertParagraph") {
          native.preventDefault();
          input.value = "";
          this.skipCommittedText = "";
          this.deliver({ inputType: native.inputType, data: null });
        }
      });
    }
    this.element = input;
    this.host.body.appendChild(input);
    this.focus(input);
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
