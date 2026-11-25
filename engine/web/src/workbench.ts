// workbench.ts — the TAS workbench screen (M38.6).
//
// The workbench is the server's simulation, shown (TASKS.md M38, boundary 4).
// The server holds the script and re-simulates it; this browser draws the
// frames it is sent (as a watcher's), paints a timeline from the status that
// follows every frame, and turns keys into ops. Nothing here simulates,
// predicts or measures anything.
//
// Everything in this file is pure, in the challenge.ts shape: main.ts owns the
// socket, the windows and the canvas.

import type { WriteText } from "./sidebar";
import { sidebarClearLine } from "./sidebar";

export const MessageTypeWorkbench = "workbench";
export const MessageTypeWorkbenchStatus = "workbenchStatus";
export const MessageTypeWorkbenchScript = "workbenchScript";
export const MessageTypeWorkbenchSubmit = "workbenchSubmitResult";
export const MessageTypeWorkbenchError = "workbenchError";

export type WorkbenchStatusMessage = {
  type: typeof MessageTypeWorkbenchStatus;
  challenge: string;
  version: number;
  goal: string;
  tick: number;
  length: number;
  ceiling: number;
  playing: boolean;
  met: boolean;
  ticks: number;
  score?: number;
  gems?: number;
};

export type WorkbenchTick = {
  tick: number;
  dx?: number;
  dy?: number;
  shift?: boolean;
  key?: number;
  replies?: { stat: number; label: string }[];
};

export type WorkbenchScript = { challenge: string; version: number; ticks: WorkbenchTick[] };

export type WorkbenchScriptMessage = { type: typeof MessageTypeWorkbenchScript; script: WorkbenchScript };

export type AssistedSubmitResponse = {
  met: boolean;
  ticks?: number;
  score?: number;
  gems?: number;
  durable: boolean;
  rank?: number;
  recordingId?: string;
  replay?: string;
  stoppedAt?: number;
  refusal?: string;
  reason?: string;
};

export type WorkbenchSubmitMessage = { type: typeof MessageTypeWorkbenchSubmit; status: number; result: AssistedSubmitResponse };

export type WorkbenchErrorMessage = { type: typeof MessageTypeWorkbenchError; op?: string; refusal: string; reason: string };

export type WorkbenchOp = {
  type: typeof MessageTypeWorkbench;
  op: string;
  tick?: number;
  keymask?: number;
  key?: number;
  recording?: string;
};

/** The workbench's own keys, taken before the play keys. */
export type WorkbenchKey = "step" | "back" | "wait" | "menu";

export function workbenchKeyFor(code: string): WorkbenchKey | "" {
  switch (code) {
    case "Period":
      return "step";
    case "Comma":
      return "back";
    case "KeyW":
      return "wait";
    case "Escape":
      return "menu";
  }
  return "";
}

function op(name: string, fields: Omit<WorkbenchOp, "type" | "op"> = {}): WorkbenchOp {
  return { type: MessageTypeWorkbench, op: name, ...fields };
}

/**
 * workbenchInputOps is a play key on the workbench: this tick's input is set to
 * it (appending, at the script's end), and the simulation steps over it. The
 * key arrives as live play sends it, a keymask or a command byte, so the
 * server decodes it exactly as it would a live tick's.
 */
export function workbenchInputOps(status: WorkbenchStatusMessage | null, input: { keymask?: number; key?: number }): WorkbenchOp[] {
  if (!status) {
    return [];
  }
  const fields: Omit<WorkbenchOp, "type" | "op"> = { tick: status.tick };
  if (input.keymask) {
    fields.keymask = input.keymask;
  } else if (input.key) {
    fields.key = input.key;
  }
  return [op("setInput", fields), op("step")];
}

/** workbenchKeyOps is one of the workbench's own keys, as ops. */
export function workbenchKeyOps(status: WorkbenchStatusMessage | null, key: WorkbenchKey): WorkbenchOp[] {
  if (!status) {
    return [];
  }
  switch (key) {
    case "step":
      // At the end there is no tick to step over, so a step there is a wait.
      return status.tick < status.length ? [op("step")] : workbenchInputOps(status, {});
    case "back":
      return status.tick > 0 ? [op("seek", { tick: status.tick - 1 })] : [];
    case "wait":
      return workbenchInputOps(status, {});
  }
  return [];
}

/** workbenchMenuOps is a menu row's choice, as ops (none for the local ones). */
export function workbenchMenuOps(status: WorkbenchStatusMessage | null, choice: string, importRecording = ""): WorkbenchOp[] {
  switch (choice) {
    case "play":
      return [op("play")];
    case "pause":
      return [op("pause")];
    case "truncate":
      return status ? [op("truncate", { tick: status.tick })] : [];
    case "import":
      return importRecording ? [op("import", { recording: importRecording })] : [];
    case "export":
      return [op("export")];
    case "submit":
      return [op("submit")];
  }
  return [];
}

/** workbenchMenuLines is the Escape menu. Rows are walked to by name. */
export function workbenchMenuLines(status: WorkbenchStatusMessage | null, options: { canImport: boolean }): string[] {
  const lines = [""];
  if (status) {
    lines.push(`  Tick ${status.tick} of ${status.length}`, "");
  }
  lines.push(status?.playing ? "!pause;Pause" : "!play;Play");
  lines.push("!truncate;Truncate here");
  if (options.canImport) {
    lines.push("!import;Import my run");
  }
  lines.push("!export;Export the script", "!submit;Submit", "!leave;Leave the workbench", "");
  return lines;
}

/** workbenchSubmitLines is what a submission says, from the server's answer. */
export function workbenchSubmitLines(message: WorkbenchSubmitMessage): string[] {
  const result = message.result;
  if (message.status === 200 && result.met) {
    const lines = ["", `  Judged: ${result.ticks} ticks.`, `  Score ${result.score ?? 0}, gems ${result.gems ?? 0}.`, ""];
    lines.push(result.durable && result.rank ? `  Assisted rank ${result.rank}.` : "  Guest runs are not posted.");
    lines.push("");
    if (result.recordingId) {
      lines.push("!replay;Watch the replay");
    }
    lines.push("!back;Back to the workbench", "");
    return lines;
  }
  return ["", "  Not posted.", ...wrap(result.reason || "The run was not judged.", 40).map((line) => `  ${line}`), ""];
}

/** workbenchExportFileName names the download. */
export function workbenchExportFileName(script: WorkbenchScript): string {
  return `${script.challenge.replace(/[^A-Za-z0-9_-]/g, "_") || "script"}-v${script.version}-tas.json`;
}

/** workbenchTimeline is a 16-cell bar: how far through the script the cursor is. */
export function workbenchTimeline(status: WorkbenchStatusMessage, width = 16): string {
  const filled = status.length === 0 ? 0 : Math.min(width, Math.round((status.tick / status.length) * width));
  return "\xdb".repeat(filled) + "\xb0".repeat(width - filled);
}

function wrap(text: string, width: number): string[] {
  const out: string[] = [];
  let line = "";
  for (const word of text.split(/\s+/)) {
    if (line && line.length + 1 + word.length > width) {
      out.push(line);
      line = "";
    }
    line = line ? `${line} ${word}` : word.slice(0, width);
  }
  if (line) {
    out.push(line);
  }
  return out;
}

/**
 * paintWorkbenchSidebar is the workbench's sidebar: the timeline row with the
 * current tick, the goal and the measured tick count, then the keys. It is
 * painted from the last status on every frame too, because a frame repaints
 * the sidebar and its status arrives just after it.
 */
export function paintWorkbenchSidebar(write: WriteText, status: WorkbenchStatusMessage | null, notice = "") {
  for (let y = 0; y <= 24; y += 1) {
    sidebarClearLine(write, y);
  }
  write(61, 0, 0x1f, "    -  -  -  -     ");
  write(63, 1, 0x70, "  TAS Workbench ".slice(0, 15));
  write(61, 2, 0x1f, "    -  -  -  -     ");
  if (status) {
    write(62, 4, 0x1e, `Tick ${status.tick}/${status.length}`.slice(0, 17));
    write(62, 5, 0x1b, workbenchTimeline(status));
    const goal = wrap(status.goal.replace(/^Goal:\s*/, ""), 17);
    write(62, 7, 0x1f, "Goal:");
    goal.slice(0, 2).forEach((line, i) => write(62, 8 + i, 0x1e, line));
    write(62, 11, status.met ? 0x1a : 0x1e, (status.met ? `Met: ${status.ticks} ticks` : `Ticks: ${status.ticks}`).slice(0, 17));
    if (status.met) {
      write(62, 12, 0x1e, `Score ${status.score ?? 0} Gems ${status.gems ?? 0}`.slice(0, 17));
    }
    if (status.playing) {
      write(62, 13, 0x1e, "Playing");
    }
  }
  if (notice) {
    wrap(notice, 17).slice(0, 2).forEach((line, i) => write(62, 14 + i, 0x1c, line));
  }
  write(62, 17, 0x70, "\x18\x19\x1b\x1a");
  write(67, 17, 0x1f, "Set & step");
  write(62, 18, 0x70, " . ");
  write(66, 18, 0x1f, "Step");
  write(62, 19, 0x70, " , ");
  write(66, 19, 0x1f, "Back");
  write(62, 20, 0x70, " W ");
  write(66, 20, 0x1f, "Wait");
  write(62, 22, 0x70, "Esc");
  write(66, 22, 0x1f, "Menu");
}
