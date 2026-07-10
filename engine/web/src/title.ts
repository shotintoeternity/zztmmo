// M4.3 — the ZZT title screen.
//
// In vanilla the title screen is GamePlayLoop running board 0 with
// GameStateElement = E_MONITOR (GAME.PAS:1610-1622), so it shares the sidebar
// routine: GameDrawSidebar draws a different menu when the "player" is a
// monitor (GAME.PAS:1456-1481). This module is that branch, transcribed the
// same way sidebar.ts transcribed the E_PLAYER branch.
//
// It is pure — a function of the world name and a KeyboardEvent — because the
// title screen runs before any WebSocket exists and there is nothing else to
// test it against.
//
// Two rows of vanilla's menu are deliberately absent:
//
//   * ' S ' Game speed (SidebarPromptSlider at 66,21). Pacing is the server's:
//     it ticks every ServerTickDuration for every player in the room. A slider
//     that moved nothing would be a lie.
//   * ' E ' Board Editor, which vanilla draws only when EditorEnabled. The
//     browser editor is M5.

import { sidebarClearLine, type WriteText } from "./sidebar";

export type TitleAction =
  | "world"
  | "play"
  | "restore"
  | "quit"
  | "about"
  | "highScores"
  | "none";

/** The subset of KeyboardEvent this module reads, so a test can drive it. */
export type KeyLike = {
  code: string;
  key: string;
  ctrlKey?: boolean;
  metaKey?: boolean;
  altKey?: boolean;
};

const TITLE_CODES: Record<string, TitleAction> = {
  KeyW: "world",
  KeyP: "play",
  KeyR: "restore",
  KeyQ: "quit",
  KeyA: "about",
  KeyH: "highScores",
};

/** titleCommand maps a key to a title-menu action, or "none". */
export function titleCommand(event: KeyLike): TitleAction {
  if (event.ctrlKey || event.metaKey || event.altKey) {
    return "none";
  }
  // KEY_ESCAPE and 'Q' share the quit prompt, exactly as in play mode.
  if (event.code === "Escape") {
    return "quit";
  }
  return TITLE_CODES[event.code] ?? "none";
}

/** drawTitleSidebar is GameDrawSidebar's GameStateElement = E_MONITOR branch. */
export function drawTitleSidebar(write: WriteText, worldName: string) {
  for (let y = 3; y <= 24; y += 1) {
    sidebarClearLine(write, y);
  }
  sidebarClearLine(write, 0);
  sidebarClearLine(write, 1);
  sidebarClearLine(write, 2);
  write(61, 0, 0x1f, "    - - - - -      ");
  // DEVIATION: vanilla's banner reads "ZZT". Same 15 cells, see sidebar.ts.
  write(62, 1, 0x70, "    ZZTMMO     ");
  write(61, 2, 0x1f, "    - - - - -      ");

  write(62, 7, 0x30, " W ");
  write(65, 7, 0x1e, " World:");
  write(69, 8, 0x1f, worldName.length > 0 ? worldName : "Untitled");

  write(62, 11, 0x70, " P ");
  write(65, 11, 0x1f, " Play");
  write(62, 12, 0x30, " R ");
  write(65, 12, 0x1e, " Restore game");
  write(62, 13, 0x70, " Q ");
  write(65, 13, 0x1e, " Quit");
  write(62, 16, 0x30, " A ");
  write(65, 16, 0x1f, " About ZZT!");
  write(62, 17, 0x70, " H ");
  write(65, 17, 0x1e, " High Scores");
}
