// M5.0 — browser editor chrome.
//
// This is intentionally not the play sidebar. Its title and command layout are
// transcribed from EditorDrawSidebar (editor.go); the lower rows are the
// read-only inspection panel until M5.1 introduces the pattern and color UI.

import { sidebarClearLine, type WriteText } from "./sidebar";

export type EditorInspect = {
  x: number;
  y: number;
  element: string;
  color: number;
  hasStat: boolean;
  statId?: number;
  p1?: number;
  p2?: number;
  p3?: number;
};

const DOS_COLORS = [
  "Black", "Blue", "Green", "Cyan", "Red", "Purple", "Brown", "Gray",
  "Dk Gray", "Lt Blue", "Lt Green", "Lt Cyan", "Lt Red", "Lt Purple", "Yellow", "White",
];

function trim(text: string, width: number): string {
  return text.slice(0, width).padEnd(width, " ");
}

export function drawEditorSidebar(write: WriteText, inspect: EditorInspect) {
  for (let y = 0; y < 25; y += 1) {
    sidebarClearLine(write, y);
  }
  write(61, 0, 0x1f, "     - - - -       ");
  write(62, 1, 0x70, "  ZZT Editor   ");
  write(61, 2, 0x1f, "     - - - -       ");
  write(61, 4, 0x70, " Q ");
  write(65, 4, 0x1f, " Exit");
  write(61, 5, 0x30, " \x18\x19\x1a\x1b ");
  write(68, 5, 0x1f, " Move");
  write(61, 7, 0x70, "Read-only v1");
  write(61, 9, 0x1e, ` Pos: ${inspect.x},${inspect.y}`.padEnd(19, " "));
  write(61, 10, 0x1e, " Tile:");
  write(61, 11, 0x1f, " " + trim(inspect.element, 17));
  const colorName = DOS_COLORS[inspect.color & 0x0f] ?? "Unknown";
  write(61, 13, 0x1e, " Color:");
  write(68, 13, inspect.color, trim(colorName, 11));
  if (inspect.hasStat) {
    write(61, 15, 0x1e, ` Stat: ${inspect.statId ?? 0}`.padEnd(19, " "));
    write(61, 16, 0x1f, ` P1: ${inspect.p1 ?? 0}`.padEnd(19, " "));
    write(61, 17, 0x1e, ` P2: ${inspect.p2 ?? 0}`.padEnd(19, " "));
    write(61, 18, 0x1f, ` P3: ${inspect.p3 ?? 0}`.padEnd(19, " "));
  } else {
    write(61, 16, 0x1f, " No stat here");
  }
  write(61, 22, 0x70, " E ");
  write(65, 22, 0x1f, " Inspect");
  write(61, 24, 0x1e, " Browser editor");
}
