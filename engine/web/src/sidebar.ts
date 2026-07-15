// The authentic ZZT sidebar: screen columns 60..79, rows 0..24.
//
// This is a literal transcription of the engine's GameDrawSidebar and
// GameUpdateSidebar (engine/game.go, itself converted from GAME.PAS). The
// server cannot send us these columns: it draws one shared sidebar from stat
// 0's PlayerState, which in a multiplayer room belongs to nobody. So the client
// redraws it from its own HUDSnapshot.
//
// Keep this in step with the engine, coordinate for coordinate. In particular
// the trailing spaces after each number are load-bearing — they are how ZZT
// erases the tail of a previous, longer value, and dropping them would silently
// change what the player sees.

export const TORCH_DURATION = 200;

// ElementDefs[...].Character for the elements the sidebar draws.
const CHAR_PLAYER = 0x02;
const CHAR_AMMO = 0x84;
const CHAR_TORCH = 0x9d;
const CHAR_GEM = 0x04;
const CHAR_KEY = 0x0c;

export type SidebarHud = {
  health: number;
  ammo: number;
  gems: number;
  torches: number;
  torchTicks: number;
  score: number;
  keys: boolean[];
  boardTimeSec: number;
  timeLimitSec: number;
  soundEnabled: boolean;
};

// WriteText matches the engine's VideoWriteText: each code unit of `text` is a
// CP437 byte, laid out left to right under one DOS attribute byte.
export type WriteText = (x: number, y: number, color: number, text: string) => void;

export function sidebarClearLine(write: WriteText, y: number) {
  write(60, y, 0x11, "\xb3                   ");
}

// drawSidebar is GameDrawSidebar for GameStateElement = E_PLAYER: the static
// chrome. Drawn once per board, exactly as GamePlayLoop does.
export function drawSidebar(write: WriteText) {
  for (let y = 3; y <= 24; y += 1) {
    sidebarClearLine(write, y);
  }
  sidebarClearLine(write, 0);
  sidebarClearLine(write, 1);
  sidebarClearLine(write, 2);
  // DEVIATION: vanilla's banner reads "ZZT"; ours is "ZZTMMO". The 6-letter word
  // is centered on the sidebar interior (cols 61-79, centre 70): the 15-cell box
  // sits at 63-77 (equal 2-cell margins) with "ZZTMMO" at 67-72. The dashes are
  // an even 4-dash row (was 5) so they line up symmetrically under that word — a
  // 5-dash row centres on a cell, the 6-letter word on a boundary, so they could
  // never share a centre.
  write(61, 0, 0x1f, "    -  -  -  -     ");
  write(63, 1, 0x70, "    ZZTMMO     ");
  write(61, 2, 0x1f, "    -  -  -  -     ");
  write(64, 7, 0x1e, " Health:");
  write(64, 8, 0x1e, "   Ammo:");
  write(64, 9, 0x1e, "Torches:");
  write(64, 10, 0x1e, "   Gems:");
  write(64, 11, 0x1e, "  Score:");
  write(64, 12, 0x1e, "   Keys:");
  write(62, 7, 0x1f, String.fromCharCode(CHAR_PLAYER));
  write(62, 8, 0x1b, String.fromCharCode(CHAR_AMMO));
  write(62, 9, 0x16, String.fromCharCode(CHAR_TORCH));
  write(62, 10, 0x1b, String.fromCharCode(CHAR_GEM));
  write(62, 12, 0x1f, String.fromCharCode(CHAR_KEY));
  write(62, 14, 0x70, " T ");
  write(65, 14, 0x1f, " Torch");
  write(62, 15, 0x30, " B ");
  write(62, 16, 0x70, " H ");
  write(65, 16, 0x1f, " Help");
  // Row 17 is blank in vanilla; the multiplayer chat window (main.ts, 'C')
  // claims it. Handled client-side, so it never reaches the engine's key switch.
  write(62, 17, 0x30, " C ");
  write(65, 17, 0x1f, " Chat");
  write(67, 18, 0x30, " \x18\x19\x1a\x1b ");
  write(72, 18, 0x1f, " Move");
  write(61, 19, 0x70, " Shift \x18\x19\x1a\x1b ");
  write(72, 19, 0x1f, " Shoot");
  write(62, 21, 0x70, " S ");
  write(65, 21, 0x1f, " Save game");
  write(62, 22, 0x30, " P ");
  write(65, 22, 0x1f, " Pause");
  write(62, 23, 0x70, " Q ");
  write(65, 23, 0x1f, " Quit");
}

// updateSidebar is GameUpdateSidebar: the live counters, redrawn whenever the
// server sends a HUD.
export function updateSidebar(write: WriteText, hud: SidebarHud) {
  if (hud.timeLimitSec > 0) {
    write(64, 6, 0x1e, "   Time:");
    write(72, 6, 0x1e, `${hud.timeLimitSec - hud.boardTimeSec} `);
  } else {
    sidebarClearLine(write, 6);
  }

  const health = hud.health < 0 ? 0 : hud.health;
  write(72, 7, 0x1e, `${health} `);
  write(72, 8, 0x1e, `${hud.ammo}  `);
  write(72, 9, 0x1e, `${hud.torches} `);
  write(72, 10, 0x1e, `${hud.gems} `);
  write(72, 11, 0x1e, `${hud.score} `);

  if (hud.torchTicks === 0) {
    write(75, 9, 0x16, "    ");
  } else {
    for (let i = 2; i <= 5; i += 1) {
      const filled = i <= Math.floor((hud.torchTicks * 5) / TORCH_DURATION);
      write(73 + i, 9, 0x16, filled ? "\xb1" : "\xb0");
    }
  }

  for (let i = 1; i <= 7; i += 1) {
    if (hud.keys[i - 1]) {
      write(71 + i, 12, 0x18 + i, String.fromCharCode(CHAR_KEY));
    } else {
      write(71 + i, 12, 0x1f, " ");
    }
  }

  write(65, 15, 0x1f, hud.soundEnabled ? " Be quiet" : " Be noisy");
}
