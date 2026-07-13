// M5.0 — browser editor chrome.
//
// This is intentionally not the play sidebar. Its title and command layout are
// transcribed from EditorDrawSidebar (editor.go); the lower rows are the
// browser controls below are transcribed from EditorDrawSidebar (editor.go).

import { sidebarClearLine, type WriteText } from "./sidebar";

export type EditorInspect = {
  x: number;
  y: number;
  elementId: number;
  element: string;
  character: number;
  color: number;
  hasStat: boolean;
  statId?: number;
  p1?: number;
  p2?: number;
  p3?: number;
  stepX?: number;
  stepY?: number;
  cycle?: number;
  param1Name?: string;
  param2Name?: string;
  paramBulletTypeName?: string;
  paramBoardName?: string;
  paramDirName?: string;
  paramTextName?: string;
};

export type EditorBrush = {
  element: number;
  character: number;
  color: number;
  copied: boolean;
};

// SidebarCategoryMenu is the F1/F2/F3 in-sidebar element picker (M5.8 parity gap):
// when a category is open, EditorLoop clears sidebar rows 3-20 and lists that
// category's elements there, waiting for the element's shortcut key
// (EDITOR.PAS:808-842). This is the structural subset drawEditorSidebar needs to
// paint it; main.ts's EditorElementMenu is a superset and passes structurally.
export type SidebarCategoryItem = {
  shortcut: string;
  name: string;
  character: number;
  color: number;
  categoryName?: string;
};

export type SidebarCategoryMenu = {
  title: string;
  items: SidebarCategoryItem[];
};

export type SidebarActionMenuItem = {
  label: string;
  value?: string;
  shortcut?: string;
};

export type SidebarActionMenu = {
  title: string;
  items: SidebarActionMenuItem[];
  selected: number;
  hint?: string;
};

export type SidebarStatPromptItem =
  | { kind: "slider"; label: string; value: number; active: boolean; startChar?: string; endChar?: string }
  | { kind: "character"; label: string; value: number; active: boolean }
  | { kind: "choice"; label: string; choices: string[]; selected: number; active: boolean }
  | { kind: "board"; label: string; value: string; active: boolean };

export type SidebarStatPrompt = {
  categoryName: string;
  elementName: string;
  items: SidebarStatPromptItem[];
};

// menuGlyphColor is EDITOR.PAS:834-837: an element whose colour has a black
// (dark) background nibble is shown on blue in the menu so its glyph is legible,
// otherwise its own colour is used. CHOICE-coloured elements reach us as 0x0F
// (neutralised server-side) and so fall into the dark-background case.
function menuGlyphColor(color: number): number {
  if ((color & 0x70) === 0) return (color & 0x0f) + 0x10;
  return color;
}

const DOS_COLORS = [
  "Black", "Blue", "Green", "Cyan", "Red", "Purple", "Brown", "Gray",
  "Dk Gray", "Lt Blue", "Lt Green", "Lt Cyan", "Lt Red", "Lt Purple", "Yellow", "White",
];

function trim(text: string, width: number): string {
  return text.slice(0, width).padEnd(width, " ");
}

function choiceOffset(choices: string[], selected: number): number {
  let offset = 0;
  for (let i = 0; i < selected; i += 1) {
    offset += choices[i].length + 1;
  }
  return offset;
}

function renderSidebarChoice(write: WriteText, y: number, prompt: string, choices: string[], selected: number, active: boolean) {
  const choiceStr = choices.join(" ");
  sidebarClearLine(write, y);
  sidebarClearLine(write, y + 1);
  sidebarClearLine(write, y + 2);
  write(63, y, active ? 0x1f : 0x1e, prompt);
  write(63, y + 2, 0x1e, choiceStr);
  write(63 + choiceOffset(choices, selected), y + 1, active ? 0x9f : 0x1f, "\x1f");
}

function renderSidebarSlider(
  write: WriteText,
  y: number,
  prompt: string,
  value: number,
  active: boolean,
  startChar = "1",
  endChar = "9",
) {
  sidebarClearLine(write, y);
  sidebarClearLine(write, y + 1);
  sidebarClearLine(write, y + 2);
  write(63, y, active ? 0x1f : 0x1e, prompt);
  write(63, y + 2, 0x1e, `${startChar}....:....${endChar}`);
  write(64 + Math.max(0, Math.min(8, value)), y + 1, active ? 0x9f : 0x1f, "\x1f");
}

function renderSidebarCharacter(write: WriteText, y: number, prompt: string, value: number, active: boolean) {
  sidebarClearLine(write, y);
  sidebarClearLine(write, y + 1);
  sidebarClearLine(write, y + 2);
  write(63, y, active ? 0x1f : 0x1e, prompt);
  write(68, y + 1, active ? 0x9f : 0x1f, "\x1f");
  let text = "";
  for (let i = value - 4; i <= value + 4; i += 1) {
    text += String.fromCharCode((i + 0x100) % 0x100);
  }
  write(63, y + 2, 0x1e, text);
}

function renderSidebarStatPrompt(write: WriteText, prompt: SidebarStatPrompt) {
  for (let y = 0; y < 25; y += 1) {
    sidebarClearLine(write, y);
  }
  write(64, 6, 0x1e, prompt.categoryName);
  write(64, 7, 0x1f, prompt.elementName);
  let y = 9;
  for (const item of prompt.items) {
    switch (item.kind) {
      case "slider":
        renderSidebarSlider(write, y, item.label, item.value, item.active, item.startChar, item.endChar);
        y += 4;
        break;
      case "character":
        renderSidebarCharacter(write, y, item.label, item.value, item.active);
        y += 4;
        break;
      case "choice":
        renderSidebarChoice(write, y, item.label, item.choices, item.selected, item.active);
        y += 4;
        break;
      case "board":
        sidebarClearLine(write, y);
        write(63, y, item.active ? 0x1f : 0x1f, `${item.label}: ${trim(item.value, 10)}`.slice(0, 17));
        y += 4;
        break;
    }
  }
}

export function drawEditorSidebar(
  write: WriteText,
  inspect: EditorInspect,
  brush: EditorBrush,
  drawing: boolean,
  textMode = false,
  categoryMenu: SidebarCategoryMenu | null = null,
  actionMenu: SidebarActionMenu | null = null,
  statPrompt: SidebarStatPrompt | null = null,
) {
  for (let y = 0; y < 25; y += 1) {
    sidebarClearLine(write, y);
  }
  if (statPrompt) {
    renderSidebarStatPrompt(write, statPrompt);
    return;
  }
  // Header and the command block are transcribed from EditorDrawSidebar
  // (editor.go:56-107 / EDITOR.PAS:89-186), row for row. Two rows differ from
  // vanilla by necessity: the browser folds "L Load" into the "S" world menu
  // (M5.6) and adds "T Transfer board" (M5.5); both are recorded in NOTES.md as
  // documented deviations rather than DOS-editor keys.
  write(61, 0, 0x1f, "     - - - -       ");
  write(62, 1, 0x70, "  ZZT Editor   ");
  write(61, 2, 0x1f, "     - - - -       ");
  write(61, 4, 0x70, " S ");
  write(64, 4, 0x1f, " World");
  write(70, 4, 0x70, " H ");
  write(73, 4, 0x1e, " Help");
  write(61, 5, 0x30, " T ");
  write(64, 5, 0x1f, " Transf");
  write(70, 5, 0x30, " Q ");
  write(73, 5, 0x1f, " Quit");
  write(61, 7, 0x70, " B ");
  write(65, 7, 0x1f, " Switch boards");
  write(61, 8, 0x30, " I ");
  write(65, 8, 0x1f, " Board Info");
  write(61, 10, 0x70, "  f1   ");
  write(68, 10, 0x1f, " Item");
  write(61, 11, 0x30, "  f2   ");
  write(68, 11, 0x1f, " Creature");
  write(61, 12, 0x70, "  f3   ");
  write(68, 12, 0x1f, " Terrain");
  write(61, 13, 0x30, "  f4   ");
  write(68, 13, 0x1f, " Enter text");
  write(61, 15, 0x70, " Space ");
  write(68, 15, 0x1f, inspect.hasStat ? " Edit stat" : " Plot");
  write(61, 16, 0x30, "  Tab  ");
  write(68, 16, 0x1f, " Draw mode");
  write(61, 18, 0x70, " P ");
  write(64, 18, 0x1f, " Pattern");
  write(61, 19, 0x30, " C ");
  write(64, 19, 0x1f, " Color:");
  const colorName = DOS_COLORS[brush.color & 0x0f] ?? "Unknown";
  write(72, 19, 0x1e, trim(colorName, 8));

  write(61, 20, 0x1e, ` Pos: ${inspect.x},${inspect.y}`.padEnd(19, " "));
  write(61, 23, 0x1f, " " + trim(inspect.element, 17));
  if (inspect.hasStat) {
    write(61, 20, 0x1e, ` ${inspect.x},${inspect.y} Stat ${inspect.statId ?? 0}: ${inspect.p1 ?? 0}/${inspect.p2 ?? 0}/${inspect.p3 ?? 0}`.slice(0, 19).padEnd(19, " "));
  }

  // The selector row is EditorDrawSidebar's colour swatches (9..15) then the
  // five terrain patterns and a copied-tile slot; the two selector markers live
  // on the row above it (EDITOR.PAS:169-186 / editor.go:94-105,131-132).
  for (let color = 9; color <= 15; color += 1) {
    write(61 + color, 22, color, "\xdb");
  }
  const patternChars = [0xdb, 0xb2, 0xb1, 0x20, 0xce];
  for (let i = 0; i < patternChars.length; i += 1) {
    write(62 + i, 22, 0x0f, String.fromCharCode(patternChars[i]));
  }
  if (brush.copied) {
    write(67, 22, brush.color, String.fromCharCode(brush.character));
    write(67, 21, 0x1f, "\x1f");
  } else {
    const patterns = [21, 22, 23, 0, 31];
    const index = patterns.indexOf(brush.element);
    if (index >= 0) write(62 + index, 21, 0x1f, "\x1f");
  }
  write(61 + (brush.color & 0x0f), 21, 0x1f, "\x1f");

  // Mode line (EDITOR.PAS:174-181): "Text entry" while F4 is on, else the draw
  // state. Text entry and Drawing on both blink (0x9E), as in vanilla.
  const modeText = textMode ? "Text entry " : drawing ? "Drawing on " : "Drawing off";
  const modeColor = textMode || drawing ? 0x9e : 0x1e;
  write(61, 24, 0x1f, " Mode:");
  write(68, 24, modeColor, modeText);

  // F1/F2/F3 category picker (EDITOR.PAS:808-842): clear rows 3-20 and list the
  // category's elements — a shortcut badge (alternating shade by row parity), the
  // name, and the glyph — over the command block, leaving the title and the
  // selector/mode rows below untouched, exactly as EditorLoop overlays them.
  if (categoryMenu) {
    for (let y = 3; y <= 20; y += 1) {
      sidebarClearLine(write, y);
    }
    let i = 3;
    for (const item of categoryMenu.items) {
      if (i > 24) break;
      if (item.categoryName) {
        i += 1;
        write(65, i, 0x1e, item.categoryName);
        i += 1;
      }
      const badgeColor = ((i % 2) << 6) + 0x30;
      write(61, i, badgeColor, ` ${item.shortcut} `);
      write(65, i, 0x1f, item.name);
      write(78, i, menuGlyphColor(item.color), String.fromCharCode(item.character));
      i += 1;
    }
  }

  // SidebarPromptChoice(true, 3, ...), used by EditorTransferBoard: a horizontal
  // choice row in editor chrome, not a text window.
  if (actionMenu) {
    renderSidebarChoice(write, 3, actionMenu.title, actionMenu.items.map((item) => item.label), actionMenu.selected, true);
  }
}
