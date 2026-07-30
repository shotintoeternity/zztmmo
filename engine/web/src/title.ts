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
// One vanilla row is deliberately absent, and one is deliberately different:
//
//   * ' S ' Game speed (SidebarPromptSlider at 66,21). Pacing is the server's:
//     it ticks every ServerTickDuration for every player in the room. A slider
//     that moved nothing would be a lie.
//   * ' E ' Board Editor is always shown here because the browser editor is the
//     multiplayer-safe M5 path rather than vanilla's terminal editor loop.

import { sidebarClearLine, type WriteText } from "./sidebar";

export type TitleAction =
  | "world"
  | "play"
  | "login"
  | "restore"
  | "quit"
  | "about"
  | "highScores"
  | "dream"
  | "editor"
  | "feedback"
  | "none";

/**
 * ServerOccupancy is how many people are on the server right now, summed across
 * every hosted world (M17.11). It is server-observed presentation state: it
 * never enters the simulation, a replay, or the parity oracle.
 */
export type ServerOccupancy = {
  players: number;
  editors: number;
};

export const NO_OCCUPANCY: ServerOccupancy = { players: 0, editors: 0 };

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
  KeyG: "login",
  KeyR: "restore",
  KeyQ: "quit",
  KeyA: "about",
  KeyH: "highScores",
  KeyD: "dream",
  KeyE: "editor",
  KeyF: "feedback",
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
export function drawTitleSidebar(
  write: WriteText,
  worldName: string,
  accountName = "",
  authEnabled = false,
  occupancy: ServerOccupancy = NO_OCCUPANCY,
) {
  for (let y = 3; y <= 24; y += 1) {
    sidebarClearLine(write, y);
  }
  sidebarClearLine(write, 0);
  sidebarClearLine(write, 1);
  sidebarClearLine(write, 2);
  // Centered "ZZTMMO" banner with an even 4-dash row — see sidebar.ts.
  write(61, 0, 0x1f, "    -  -  -  -     ");
  write(63, 1, 0x70, "    ZZTMMO     ");
  write(61, 2, 0x1f, "    -  -  -  -     ");

  // M17.11: how busy the server is, before the player opens the picker. A zero
  // draws nothing rather than " Playing: 0" — a quiet server should read as
  // quiet, and the rows collapse upward so a lone count never floats.
  // The labels share the menu's text column (65) and the counts sit clear of
  // them at 75, so a five-figure crowd still stops at the last column.
  let occupancyRow = 4;
  if (occupancy.players > 0) {
    write(65, occupancyRow, 0x1e, " Playing:");
    write(75, occupancyRow, 0x1f, String(occupancy.players));
    occupancyRow += 1;
  }
  if (occupancy.editors > 0) {
    write(65, occupancyRow, 0x1e, " Editing:");
    write(75, occupancyRow, 0x1f, String(occupancy.editors));
  }

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
  write(62, 19, 0x30, " D ");
  write(65, 19, 0x1f, " Dream a world");
  write(62, 20, 0x70, " E ");
  write(65, 20, 0x1f, " Board editor");
  // M18.4: the beta feedback pointer. It sits with the other ZZTMMO-only rows
  // rather than beside vanilla's About/High Scores pair, and the sign-in row
  // moves down one to keep the blank separator above it.
  write(62, 21, 0x30, " F ");
  write(65, 21, 0x1e, " Feedback");
  if (authEnabled || accountName) {
    write(62, 23, 0x30, " G ");
    write(65, 23, 0x1e, accountName ? " " + accountName.slice(0, 13) : " Google sign-in");
  }
}
