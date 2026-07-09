import "./style.css";
import { drawSidebar as paintSidebar, updateSidebar as paintSidebarHud } from "./sidebar";
import { renderModal, handleModalKey, type Modal } from "./modal";
import { commandKey, isHandledKey, isMovementKey, movementMask, rawKey } from "./keys";
import { drawTitleSidebar, titleCommand } from "./title";
import { soundNotesFromProtocol, ZztSound } from "./sound";

const COLS = 80;
// The server streams board columns 0..59 only. Columns 60..79 are the sidebar,
// which this client draws itself from HUD data — see drawSidebar/updateSidebar,
// transcribed from the engine's GameDrawSidebar/GameUpdateSidebar.
const BOARD_COLS = 60;
const SIDEBAR_COLS = COLS - BOARD_COLS;
const ROWS = 25;
const CELL_W = 10;
const CELL_H = 18;
const WIDTH = COLS * CELL_W;
const HEIGHT = ROWS * CELL_H;

const MessageTypeJoin = "join";
const MessageTypeInput = "input";
const MessageTypeSnapshot = "snapshot";
const MessageTypeDiff = "diff";
const MessageTypeEvent = "event";
const MessageTypeBoardChange = "boardChange";
const MessageTypeDebugCommand = "debugCommand";
const MessageTypeScrollReply = "scrollReply";
const MessageTypeQuitReply = "quitReply";
const MessageTypeHighScoreName = "highScoreName";
const MessageTypeSaveFilename = "saveFilename";
const MessageTypeChat = "chat";

// GameDebugPrompt's PromptString(63, 5, 0x1E, 0x0F, 11, PROMPT_ANY, ...).
// The rest of that geometry lives in modal.ts, which owns every prompt's layout.
const DEBUG_PROMPT_WIDTH = 11;

// ElementDefs[E_PLAYER].Character / .Color (elements.go:1268-1269), used by the
// pause blink.
const CHAR_PLAYER = 0x02;
const COLOR_PLAYER = 0x1f;
// SoundHasTimeElapsed(TickTimeCounter, 25) in GAME.PAS:1520. A TimerTick is 6
// hundredths of a second (SOUNDS.PAS:172), so the blink toggles every 250ms.
const PAUSE_BLINK_MS = 250;
const COMMAND_SOUND = "B".charCodeAt(0);

type ScreenCell = {
  x: number;
  y: number;
  ch: number;
  color: number;
};

type PlayerSnapshot = {
  id: number;
  statId: number;
  x: number;
  y: number;
  health: number;
};

type HudSnapshot = {
  health: number;
  ammo: number;
  gems: number;
  torches: number;
  torchTicks: number;
  energizerTicks: number;
  score: number;
  keys: boolean[];
  boardTimeSec: number;
  boardTimeHsec: number;
  timeLimitSec: number;
  soundEnabled: boolean;
};

type ProtocolEvent = {
  type: string;
  statId?: number;
  playerStatId?: number;
  title?: string;
  lines?: string[];
  filename?: string;
  /** Set on "saveResult" when the save was refused; absent means it worked. */
  error?: string;
  score?: number;
  listPos?: number;
  notes?: number[];
  priority?: number;
  x?: number;
  y?: number;
  toBoard?: number;
  entryX?: number;
  entryY?: number;
  paused?: boolean;
};

type SnapshotMessage = {
  type: typeof MessageTypeSnapshot;
  boardId: number;
  tick: number;
  seed: number;
  hash: number;
  you: PlayerSnapshot;
  players: PlayerSnapshot[];
  hud: HudSnapshot;
  screen: ScreenCell[];
  events?: ProtocolEvent[];
};

type DiffMessage = {
  type: typeof MessageTypeDiff;
  boardId: number;
  tick: number;
  hash: number;
  cells?: ScreenCell[];
  players?: PlayerSnapshot[];
  hud?: HudSnapshot;
  events?: ProtocolEvent[];
};

type EventMessage = {
  type: typeof MessageTypeEvent;
  boardId?: number;
  tick?: number;
  event: ProtocolEvent;
};

type BoardChangeMessage = {
  type: typeof MessageTypeBoardChange;
  snapshot: SnapshotMessage;
};

type ChatMessage = {
  type: typeof MessageTypeChat;
  from: string;
  text: string;
};

type ServerMessage = SnapshotMessage | DiffMessage | EventMessage | BoardChangeMessage | ChatMessage;

type InputMessage = {
  type: typeof MessageTypeInput;
  playerId: number;
  seq: number;
  keymask?: number;
  key?: number;
};

const ega = [
  "#000000",
  "#0000aa",
  "#00aa00",
  "#00aaaa",
  "#aa0000",
  "#aa00aa",
  "#aa5500",
  "#aaaaaa",
  "#555555",
  "#5555ff",
  "#55ff55",
  "#55ffff",
  "#ff5555",
  "#ff55ff",
  "#ffff55",
  "#ffffff",
];

const cp437: Record<number, string> = {
  0: " ",
  1: "☺",
  2: "☻",
  3: "♥",
  4: "♦",
  5: "♣",
  6: "♠",
  7: "•",
  8: "◘",
  9: "○",
  10: "◙",
  11: "♂",
  12: "♀",
  13: "♪",
  14: "♫",
  15: "☼",
  16: "►",
  17: "◄",
  18: "↕",
  19: "‼",
  20: "¶",
  21: "§",
  22: "▬",
  23: "↨",
  24: "↑",
  25: "↓",
  26: "→",
  27: "←",
  28: "∟",
  29: "↔",
  30: "▲",
  31: "▼",
  127: "⌂",
  128: "Ç",
  129: "ü",
  130: "é",
  131: "â",
  132: "ä",
  133: "à",
  134: "å",
  135: "ç",
  136: "ê",
  137: "ë",
  138: "è",
  139: "ï",
  140: "î",
  141: "ì",
  142: "Ä",
  143: "Å",
  144: "É",
  145: "æ",
  146: "Æ",
  147: "ô",
  148: "ö",
  149: "ò",
  150: "û",
  151: "ù",
  152: "ÿ",
  153: "Ö",
  154: "Ü",
  155: "¢",
  156: "£",
  157: "¥",
  158: "₧",
  159: "ƒ",
  160: "á",
  161: "í",
  162: "ó",
  163: "ú",
  164: "ñ",
  165: "Ñ",
  166: "ª",
  167: "º",
  168: "¿",
  169: "⌐",
  170: "¬",
  171: "½",
  172: "¼",
  173: "¡",
  174: "«",
  175: "»",
  176: "░",
  177: "▒",
  178: "▓",
  179: "│",
  180: "┤",
  181: "╡",
  182: "╢",
  183: "╖",
  184: "╕",
  185: "╣",
  186: "║",
  187: "╗",
  188: "╝",
  189: "╜",
  190: "╛",
  191: "┐",
  192: "└",
  193: "┴",
  194: "┬",
  195: "├",
  196: "─",
  197: "┼",
  198: "╞",
  199: "╟",
  200: "╚",
  201: "╔",
  202: "╩",
  203: "╦",
  204: "╠",
  205: "═",
  206: "╬",
  207: "╧",
  208: "╨",
  209: "╤",
  210: "╥",
  211: "╙",
  212: "╘",
  213: "╒",
  214: "╓",
  215: "╫",
  216: "╪",
  217: "┘",
  218: "┌",
  219: "█",
  220: "▄",
  221: "▌",
  222: "▐",
  223: "▀",
  224: "α",
  225: "ß",
  226: "Γ",
  227: "π",
  228: "Σ",
  229: "σ",
  230: "µ",
  231: "τ",
  232: "Φ",
  233: "Θ",
  234: "Ω",
  235: "δ",
  236: "∞",
  237: "φ",
  238: "ε",
  239: "∩",
  240: "≡",
  241: "±",
  242: "≥",
  243: "≤",
  244: "⌠",
  245: "⌡",
  246: "÷",
  247: "≈",
  248: "°",
  249: "∙",
  250: "·",
  251: "√",
  252: "ⁿ",
  253: "²",
  254: "■",
  255: " ",
};

const app = document.querySelector<HTMLDivElement>("#app");
if (!app) {
  throw new Error("missing app root");
}

// M4.0: the page is the ZZT screen and nothing else — no topbar, no overlay, no
// event log. Anything the player needs to see must be drawn as CP437 cells on
// the 80x25 text-mode screen, the way the original does it.
app.innerHTML = `
  <div class="canvas-wrap">
    <canvas data-screen width="${WIDTH}" height="${HEIGHT}" tabindex="0"></canvas>
  </div>
`;

const canvas = query<HTMLCanvasElement>("[data-screen]");
const ctx = canvas.getContext("2d");
if (!ctx) {
  throw new Error("canvas context unavailable");
}
const screenCtx = ctx;

let ws: WebSocket | null = null;
let playerId = 0;
let myStatId = -1;
let seq = 0;
let lastMask = 0;
let inputTimer = 0;
let retryTimer = 0;
let connected = false;
let lastMessageKey = "";
let lastMessageAt = 0;
const pressed = new Set<string>();
const zztSound = new ZztSound();

// M4.3: the client is a two-state machine, as ZZT is. "title" is GameTitleLoop
// — no socket, no player, the monitor sidebar over a static board 0. "playing"
// is GamePlayLoop: joined to a room, streaming diffs. 'P' enters, quitting
// leaves.
type Mode = "title" | "playing";
let mode: Mode = "title";
let worldName = "Untitled";
let nickname = "browser";
// leavingToTitle suppresses the reconnect that a dropped socket normally
// triggers: a socket we closed on purpose must not come back.
let leavingToTitle = false;

// While a modal is up, gameplay keys are swallowed (M4.1: handleModalKey is the
// only consumer). The simulation does NOT pause behind it (M1.3 deviation), so
// the board keeps updating underneath and the modal is painted as an overlay
// rather than by saving/restoring cells.
let modal: Modal | null = null;
// Per-player pause (M3.11): the server tells us via PauseEvent whether OUR stat
// is paused. The room keeps running for everyone else, so this is presentation
// only — we draw what vanilla's GamePlayLoop pause branch drew.
let paused = false;
let pauseBlink = false;
let pauseTimer = 0;
// The high-score chain is three modals deep: the list with "-- You! --", the
// name popup, then the finished list. Each opens as the previous one closes.
let pendingHighScore = false;
let returnToTitleOnClose = false;
let highScoreTimer = 0;
let myX = 0;
let myY = 0;
const overlay = new Map<number, { ch: number; color: number }>();
const cells: ScreenCell[] = Array.from({ length: COLS * ROWS }, (_, i) => ({
  x: i % COLS,
  y: Math.floor(i / COLS),
  ch: 32,
  color: 0x1f,
}));

drawScreen();
// ZZT opens on its title screen, not in a room. Joining costs the server a
// player stat on a shared board, so it waits for 'P' — which is also what
// vanilla waits for (GAME.PAS:1644).
void showTitle();
canvas.addEventListener("mousedown", handlePointerDown);
canvas.addEventListener("keydown", handleKeyDown);
canvas.addEventListener("keyup", handleKeyUp);
window.addEventListener("blur", () => {
  pressed.clear();
  sendInput(0);
});

function query<T extends Element>(selector: string): T {
  const element = document.querySelector<T>(selector);
  if (!element) {
    throw new Error(`missing ${selector}`);
  }
  return element;
}

// showTitle paints GameTitleLoop's screen: board 0 behind the monitor sidebar.
// The board comes from /api/title rather than the snapshot stream because we
// have no socket yet — and, unlike vanilla's, it does not animate. See the
// DEVIATION note in engine/web_api.go.
async function showTitle() {
  mode = "title";
  modal = null;
  playerId = 0;
  myStatId = -1;
  zztSound.setEnabled(false);
  setPaused(false);
  pressed.clear();
  lastMask = 0;

  let friendlyName = worldName;
  try {
    const url = worldName === "Untitled" ? "/api/title" : "/api/title?world=" + encodeURIComponent(worldName);
    const response = await fetch(url);
    const title = (await response.json()) as { world: string; filename?: string; screen: ScreenCell[] };
    worldName = title.filename || title.world;
    friendlyName = title.world;
    replaceCells(title.screen);
  } catch {
    // Offline: keep whatever board is on screen and still draw the menu, so
    // the player can retry with 'P'.
  }
  drawTitleSidebar(writeText, friendlyName);
  paintOverlay();
  drawScreen();
  canvas.focus();
}

// leaveToTitle ends this player's game: the room already dropped them, so all
// that remains is to close the socket without tripping the reconnect.
function leaveToTitle() {
  leavingToTitle = true;
  window.clearInterval(inputTimer);
  window.clearTimeout(retryTimer);
  connected = false;
  chatMessages = [];
  if (ws) {
    ws.close();
    ws = null;
  }
  void showTitle();
}

function startPlay() {
  openEntry("Enter name:", "", 15, "alphanum", (name) => {
    if (name && name.trim()) {
      nickname = name.trim();
    } else {
      nickname = "player" + Math.floor(Math.random() * 1000);
    }
    zztSound.setEnabled(true);
    zztSound.resume();
    leavingToTitle = false;
    drawSidebar();
    drawScreen();
    connect();
  });
}

// fetchLines backs the title screen's read-only windows (About, High Scores,
// World). A failure shows the reason rather than nothing at all.
async function fetchLines(url: string, fallbackTitle: string) {
  try {
    const response = await fetch(url);
    if (!response.ok) {
      throw new Error(String(response.status));
    }
    const data = (await response.json()) as { title?: string; lines?: string[] };
    openWindow(data.title || fallbackTitle, data.lines ?? [], true);
  } catch {
    openWindow(fallbackTitle, ["", "  Not available: the server did not answer.", ""], true);
  }
}

async function showWorlds() {
  let worlds: string[] = [];
  try {
    const response = await fetch("/api/worlds");
    const data = (await response.json()) as { worlds?: string[] };
    worlds = data.worlds ?? [];
  } catch {
    openWindow("ZZT Worlds", ["", "  Not available: the server did not answer.", ""], true);
    return;
  }

  if (worlds.length === 0) {
    openWindow("ZZT Worlds", ["", "  There are no ZZT worlds.", ""], true);
    return;
  }

  openSelectList("ZZT Worlds", worlds, (selected) => {
    const name = selected.split(" (")[0];
    void loadWorld(name);
  });
}

async function loadWorld(name: string) {
  try {
    const response = await fetch("/api/loadworld", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name }),
    });
    if (!response.ok) {
      const reason = (await response.text()).trim() || `error ${response.status}`;
      openWindow("Select World", ["", `  Not loaded: ${reason}`, ""], true);
      return;
    }
  } catch {
    openWindow("Select World", ["", "  Not loaded: the server did not answer.", ""], true);
    return;
  }

  // The world changed under the title screen: repaint board 0 and the name.
  await showTitle();
  openWindow("Select World", ["", `  Loaded ${name}.ZZT. Press P to play.`, ""], true);
}

// showSavedGames is GameWorldLoad(".SAV"): the selectable "Saved Games" window.
// Picking one restores it server-side, which is refused while anybody is still
// in a room — a restore rewrites every board. See NOTES.md M4.3a.
async function showSavedGames() {
  let saves: string[] = [];
  try {
    const response = await fetch("/api/saves?world=" + encodeURIComponent(worldName));
    const data = (await response.json()) as { saves?: string[] };
    saves = data.saves ?? [];
  } catch {
    openWindow("Saved Games", ["", "  Not available: the server did not answer.", ""], true);
    return;
  }

  if (saves.length === 0) {
    openWindow("Saved Games", ["", "  There are no saved games.", ""], true);
    return;
  }

  // "!NAME;NAME" is the text window's hyperlink form, so Enter yields the name.
  openSelectList("Saved Games", saves, (name) => void restoreSavedGame(name));
}

async function restoreSavedGame(name: string) {
  try {
    const response = await fetch("/api/restore", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ world: worldName, name }),
    });
    if (!response.ok) {
      const reason = (await response.text()).trim() || `error ${response.status}`;
      openWindow("Restore game", ["", `  Not restored: ${reason}`, ""], true);
      return;
    }
  } catch {
    openWindow("Restore game", ["", "  Not restored: the server did not answer.", ""], true);
    return;
  }

  // The world changed under the title screen: repaint board 0 and the name.
  await showTitle();
  openWindow("Restore game", ["", `  Restored ${name}.SAV. Press P to play.`, ""], true);
}

function connect() {
  if (ws && ws.readyState === WebSocket.OPEN) {
    return;
  }

  window.clearTimeout(retryTimer);
  const socket = new WebSocket(wsURL());
  ws = socket;

  socket.addEventListener("open", () => {
    connected = true;
    // No board: the server picks its configured default (zzt-server -board).
    socket.send(JSON.stringify({ type: MessageTypeJoin, name: nickname }));
    canvas.focus();
    inputTimer = window.setInterval(() => sendInput(currentMask()), 55);
  });

  socket.addEventListener("message", (event) => {
    const message = JSON.parse(String(event.data)) as ServerMessage;
    applyMessage(message);
  });

  socket.addEventListener("close", () => disconnect("Disconnected"));
  socket.addEventListener("error", () => disconnect("Connection error"));
}

function disconnect(reason: string) {
  // A socket we closed ourselves (quit) is not a lost connection: showTitle has
  // already taken the screen, and reconnecting would silently rejoin the room.
  if (leavingToTitle) {
    return;
  }
  connected = false;
  window.clearInterval(inputTimer);
  // Otherwise the blink timer keeps repainting the board over the notice below.
  setPaused(false);
  ws = null;
  drawConnectionNotice(reason);
  retryTimer = window.setTimeout(connect, 2000);
}

// drawConnectionNotice writes the only non-vanilla text this client shows, and
// it writes it as CP437 cells on the sidebar's bottom row rather than as an
// HTML panel. Nothing repaints while we are disconnected, so it persists.
function drawConnectionNotice(reason: string) {
  const text = ` ${reason}`.slice(0, SIDEBAR_COLS).padEnd(SIDEBAR_COLS, " ");
  writeText(BOARD_COLS, ROWS - 1, 0x1e, text);
  drawScreen();
}

function wsURL(): string {
  const url = new URL("/ws", window.location.href);
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  url.searchParams.set("world", worldName);
  return url.toString();
}

function applyMessage(message: ServerMessage) {
  // A snapshot is what takes us out of title mode; anything else arriving while
  // we are on (or on our way to) the title screen is in flight from a room we
  // have already left, and must not repaint over the menu.
  if (message.type !== MessageTypeSnapshot && (leavingToTitle || mode === "title")) {
    return;
  }

  switch (message.type) {
    case MessageTypeSnapshot:
      applySnapshot(message);
      break;
    case MessageTypeDiff:
      applyDiff(message);
      break;
    case MessageTypeEvent:
      handleProtocolEvent(message.event);
      break;
    case MessageTypeBoardChange:
      stopHeldInput();
      closeModal();
      applySnapshot(message.snapshot);
      break;
    case MessageTypeChat:
      handleChatMessage(message);
      break;
  }
}

function applySnapshot(message: SnapshotMessage) {
  mode = "playing";
  playerId = message.you.id;
  myStatId = message.you.statId;
  myX = message.you.x;
  myY = message.you.y;
  replaceCells(message.screen);
  drawSidebar();
  updateSidebar(message.hud);
  renderEvents(message.events);
  paintOverlay();
  drawScreen();
}

// Stat ids shift when other players leave the board, so track ours — and our
// position, which the pause blink draws over — from every message that carries
// the roster.
function trackMyStatId(players: PlayerSnapshot[] | undefined) {
  if (!players) {
    return;
  }
  for (const player of players) {
    if (player.id === playerId) {
      myStatId = player.statId;
      myX = player.x;
      myY = player.y;
      return;
    }
  }
}

function applyDiff(message: DiffMessage) {
  trackMyStatId(message.players);
  if (message.cells) {
    for (const cell of message.cells) {
      setBoardCell(cell);
    }
  }
  if (message.hud) {
    updateSidebar(message.hud);
  }
  renderEvents(message.events);
  paintOverlay();
  drawScreen();
}

function replaceCells(nextCells: ScreenCell[]) {
  for (const cell of cells) {
    cell.ch = 32;
    cell.color = 0x1f;
  }
  for (const cell of nextCells) {
    setBoardCell(cell);
  }
}

// setBoardCell accepts cells from the server. The sidebar columns are drawn
// locally from HUD data, so a stray legacy sidebar write can never land there.
function setBoardCell(cell: ScreenCell) {
  if (cell.x >= BOARD_COLS) {
    return;
  }
  setCell(cell);
}

function setCell(cell: ScreenCell) {
  if (cell.x < 0 || cell.x >= COLS || cell.y < 0 || cell.y >= ROWS) {
    return;
  }
  cells[cell.y * COLS + cell.x] = cell;
}

function drawScreen() {
  screenCtx.textBaseline = "top";
  screenCtx.font = "18px 'Perfect DOS VGA 437', monospace";
  for (let i = 0; i < cells.length; i += 1) {
    const base = cells[i];
    const over = overlay.get(i);
    const ch = over ? over.ch : base.ch;
    const color = over ? over.color : base.color;
    const fg = color & 0x0f;
    const bg = (color >> 4) & 0x0f;
    const x = base.x * CELL_W;
    const y = base.y * CELL_H;
    screenCtx.fillStyle = ega[bg] ?? "#000000";
    screenCtx.fillRect(x, y, CELL_W, CELL_H);
    screenCtx.fillStyle = ega[fg] ?? "#ffffff";
    screenCtx.fillText(toGlyph(ch), x, y);
  }
}

function writeOverlay(x: number, y: number, color: number, text: string) {
  for (let i = 0; i < text.length; i += 1) {
    const cx = x + i;
    if (cx < 0 || cx >= COLS || y < 0 || y >= ROWS) {
      continue;
    }
    overlay.set(y * COLS + cx, { ch: text.charCodeAt(i) & 0xff, color });
  }
}

let chatMessages: { from: string; text: string }[] = [];

function handleChatMessage(message: { from: string; text: string }) {
  chatMessages.push({ from: message.from, text: message.text });
  if (chatMessages.length > 3) {
    chatMessages.shift();
  }
  paintOverlay();
  drawScreen();
}

function paintChat() {
  for (let i = 0; i < 3; i++) {
    const y = 3 + i;
    writeOverlay(61, y, 0x1f, "                  ");
    if (i < chatMessages.length) {
      const msg = chatMessages[i];
      const nick = `<${msg.from}>`;
      const text = msg.text;
      
      const nickLen = Math.min(nick.length, 18);
      writeOverlay(61, y, 0x1b, nick.slice(0, nickLen));
      
      if (nickLen < 18) {
        const textLen = 18 - nickLen - 1;
        if (textLen > 0) {
          writeOverlay(61 + nickLen + 1, y, 0x1f, text.slice(0, textLen));
        }
      }
    }
  }
}

// paintOverlay rebuilds the modal layer from scratch each frame, so a modal
// never has to restore what was underneath it. The pause layer goes underneath
// the modal: a paused player can still have a scroll open over the board.
function paintOverlay() {
  overlay.clear();
  if (mode === "playing") {
    paintChat();
  }
  if (paused) {
    paintPause();
  }
  if (modal) {
    renderModal(writeOverlay, modal);
  }
}

// paintPause is GAME.PAS:1518-1533. Note what actually blinks: the "Pausing..."
// label is written unconditionally every frame, and it is the PLAYER GLYPH that
// alternates with a blank. Board column x maps to screen column x-1.
function paintPause() {
  writeOverlay(64, 5, 0x1f, "Pausing...");
  if (myX <= 0 || myY <= 0) {
    return;
  }
  if (pauseBlink) {
    writeOverlay(myX - 1, myY - 1, COLOR_PLAYER, String.fromCharCode(CHAR_PLAYER));
  } else {
    writeOverlay(myX - 1, myY - 1, 0x0f, " ");
  }
}

function setPaused(next: boolean) {
  if (paused === next) {
    return;
  }
  paused = next;
  window.clearInterval(pauseTimer);
  pauseTimer = 0;
  if (paused) {
    pauseBlink = true;
    pauseTimer = window.setInterval(() => {
      pauseBlink = !pauseBlink;
      paintOverlay();
      drawScreen();
    }, PAUSE_BLINK_MS);
  }
  paintOverlay();
  drawScreen();
}

function openModal(next: Modal) {
  stopHeldInput();
  modal = next;
  paintOverlay();
  drawScreen();
}

function openWindow(title: string, lines: string[], viewingFile: boolean, replyStatId = -1) {
  if (lines.length === 0) {
    return;
  }
  openModal({
    kind: "text",
    state: { title, lines, linePos: 1, viewingFile },
    baseTitle: title,
    moved: false,
    selectable: replyStatId >= 0,
    onSelect: (label) => sendScrollReply(replyStatId, label),
  });
}

// openSelectList is vanilla's selectable file window (GameWorldLoad's "Saved
// Games"). The lines are rendered as text-window hyperlinks so that Enter yields
// the entry itself; openWindow's own selectable path is bound to scroll replies.
function openSelectList(title: string, entries: string[], onPick: (entry: string) => void) {
  if (entries.length === 0) {
    return;
  }
  openModal({
    kind: "text",
    state: { title, lines: entries.map((entry) => `!${entry};${entry}`), linePos: 1, viewingFile: false },
    baseTitle: title,
    moved: false,
    selectable: true,
    onSelect: onPick,
  });
}

// openEntry is SidebarPromptString / GameDebugPrompt's field.
function openEntry(
  label: string,
  suffix: string,
  width: number,
  charset: "any" | "alphanum",
  onSubmit: (text: string | null) => void,
) {
  openModal({ kind: "entry", label, suffix, width, buffer: "", charset, onSubmit });
}

// openYesNo is SidebarPromptYesNo.
function openYesNo(message: string, onAnswer: (yes: boolean) => void) {
  openModal({ kind: "yesno", message, onAnswer });
}

function closeModal() {
  modal = null;
  if (pendingHighScore) {
    pendingHighScore = false;
    openHighScoreName();
    return;
  }
  if (returnToTitleOnClose) {
    returnToTitleOnClose = false;
    leaveToTitle();
    return;
  }
  // Repaint rather than clear: the pause layer may still be underneath.
  paintOverlay();
  drawScreen();
}

// openHighScoreName is PopupPromptString("Congratulations!  Enter your name:").
function openHighScoreName() {
  openModal({
    kind: "popupEntry",
    question: "Congratulations!  Enter your name:",
    buffer: "",
    // ZZT-QUIRK: Escape leaves the name empty and the entry is still recorded,
    // occupying a slot that HighScoresInitTextWindow then skips because its
    // name is blank (GAME.PAS PopupPromptString clears the buffer up front).
    onSubmit: (name) => sendHighScoreName(name ?? ""),
  });
}

function sendScrollReply(statId: number, label: string) {
  if (!connected || !ws || ws.readyState !== WebSocket.OPEN || playerId === 0) {
    return;
  }
  ws.send(JSON.stringify({ type: MessageTypeScrollReply, playerId, statId, label }));
}

function toGlyph(ch: number): string {
  if (ch >= 32 && ch <= 126) {
    return String.fromCharCode(ch);
  }
  return cp437[ch] ?? " ";
}

// writeText mirrors the engine's VideoWriteText: each code unit of `text` is a
// CP437 byte, written left to right with a single DOS attribute byte.
function writeText(x: number, y: number, color: number, text: string) {
  for (let i = 0; i < text.length; i += 1) {
    setCell({ x: x + i, y, ch: text.charCodeAt(i) & 0xff, color });
  }
}

function drawSidebar() {
  paintSidebar(writeText);
}

function updateSidebar(hud: HudSnapshot) {
  zztSound.setEnabled(hud.soundEnabled);
  paintSidebarHud(writeText, hud);
}

function renderEvents(events: ProtocolEvent[] | undefined) {
  if (!events) {
    return;
  }
  for (const event of events) {
    handleProtocolEvent(event);
  }
}

// isMine filters room-wide event broadcasts down to this player's own modals.
function isMine(event: ProtocolEvent): boolean {
  return myStatId < 0 || (event.statId ?? 0) === myStatId;
}

// A scroll's statId is the OBJECT; playerStatId is who touched it. -1 means the
// scroll has no owner and everyone sees it.
function isMyScroll(event: ProtocolEvent): boolean {
  const owner = event.playerStatId ?? -1;
  return owner < 0 || myStatId < 0 || owner === myStatId;
}

function handleProtocolEvent(event: ProtocolEvent) {
  switch (event.type) {
    case "help":
      if (isMine(event)) {
        openWindow(event.title ?? event.filename ?? "Help", event.lines ?? [], true);
      }
      appendLogOnce(`help: ${event.title ?? event.filename ?? ""}`);
      break;
    case "debugPrompt":
      if (isMine(event)) {
        // GameDebugPrompt: bare 11-wide field, any characters. Escape submits
        // the empty command, because vanilla still runs the tail of the routine.
        openEntry("", "", DEBUG_PROMPT_WIDTH, "any", (text) => sendDebugCommand(text ?? ""));
      }
      break;
    case "savePrompt":
      if (isMine(event)) {
        // SidebarPromptString("Save game:", ".SAV", ..., PROMPT_ALPHANUM).
        // Escape or an empty name cancels, exactly as vanilla's prompt does.
        openEntry("Save game:", ".SAV", 8, "alphanum", (name) => {
          if (name) {
            sendSaveFilename(name);
          }
        });
      }
      break;
    case "saveResult":
      // Addressed to one client rather than one stat, so it is not isMine-gated:
      // the server only sends it to the player who asked to save.
      if (event.error) {
        openWindow("Saving", ["", `  Not saved: ${event.error}`, ""], true);
      } else {
        openWindow("Saving", ["", `  Saved as ${event.filename ?? ""}.SAV`, ""], true);
      }
      break;
    case "scroll":
      // A scroll with no owner (an object opening one from its own code rather
      // than a touch) is shown to everybody on the board, as in vanilla.
      if (isMyScroll(event)) {
        openWindow(event.title ?? "Interaction", event.lines ?? [], false, event.statId ?? -1);
      }
      appendLogOnce(`scroll: ${event.title ?? ""}`);
      break;
    case "pause":
      // Pause is per-player: a PauseEvent for somebody else's stat must not
      // draw "Pausing..." on our screen.
      if (isMine(event)) {
        setPaused(event.paused ?? false);
      }
      break;
    case "sound":
      zztSound.queue(event.priority ?? 0, soundNotesFromProtocol(event.notes));
      break;
    case "transfer":
      appendLog(`transfer to board ${event.toBoard ?? "?"}`);
      break;
    case "death":
      appendLog("death");
      break;
    case "respawn":
      appendLog(`respawn at ${event.x ?? "?"},${event.y ?? "?"}`);
      break;
    case "quitPrompt":
      // GamePromptEndPlay's SidebarPromptYesNo("End this game? ")
      // (ELEMENTS.PAS:1308) — not the title screen's "Quit ZZT? ". The event
      // now names the player who asked, so it opens on their screen only.
      if (isMine(event)) {
        openYesNo("End this game? ", (yes) => sendQuitReply(yes));
      }
      break;
    case "quit":
      // The room has already dropped us. Score did not place: straight back to
      // the title screen, as GameTitleLoop does when GamePlayLoop returns.
      leaveToTitle();
      break;
    case "highScoreEntry":
      // The score placed. Vanilla shows the list with "-- You! --" in the new
      // slot, then PopupPromptString asks for a name (game.go:1892-1908).
      // openWindow no-ops on an empty list, so ask for the name directly rather
      // than leaving the player on a board they have already left.
      openWindow(event.title ?? "New high score", event.lines ?? [], true);
      if (modal) {
        pendingHighScore = true;
      } else {
        openHighScoreName();
      }
      break;
    case "highScores":
      // The finished list, after the name was recorded. Closing it ends the game.
      window.clearTimeout(highScoreTimer);
      pendingHighScore = false;
      openWindow(event.title ?? "High scores", event.lines ?? [], true);
      if (modal) {
        returnToTitleOnClose = true;
      } else {
        leaveToTitle();
      }
      break;
    default:
      appendLog(event.type);
      break;
  }
}

function stopHeldInput() {
  if (pressed.size === 0) {
    return;
  }
  pressed.clear();
  sendInput(0);
}

// M4.0 removed the on-page event log: the page is the ZZT screen only. Events
// that have no vanilla presentation yet (transfer, death, high score) go to the
// devtools console until M4.1's text-window system gives them a real home.
function appendLog(text: string) {
  console.debug("[zzt]", text);
}

function appendLogOnce(text: string) {
  const now = Date.now();
  if (text === lastMessageKey && now - lastMessageAt < 1000) {
    return;
  }
  lastMessageKey = text;
  lastMessageAt = now;
  appendLog(text);
}

// routeModalKey consumes a key on behalf of the open modal. A modal swallows
// EVERY key: even one it ignores must not reach gameplay or the title menu.
function routeModalKey(event: KeyboardEvent) {
  event.preventDefault();
  const previous = modal;
  const result = handleModalKey(previous!, event);
  if (result === "close") {
    // A callback may have chained straight into another modal (e.g. the score
    // list opening the name popup). Only tear down if it did not.
    if (modal === previous) {
      closeModal();
    } else {
      paintOverlay();
      drawScreen();
    }
  } else if (result === "redraw") {
    paintOverlay();
    drawScreen();
  }
}

// handleTitleKey is GameTitleLoop's `case UpCase(InputKeyPressed)` menu.
function handleTitleKey(event: KeyboardEvent) {
  const action = titleCommand(event);
  if (action === "none") {
    return;
  }
  event.preventDefault();

  switch (action) {
    case "play":
      startPlay();
      break;
    case "world":
      void showWorlds();
      break;
    case "about":
      void fetchLines("/api/help?file=ABOUT.HLP&title=About+ZZT...", "About ZZT...");
      break;
    case "highScores":
      void fetchLines("/api/highscores?world=" + encodeURIComponent(worldName), `High scores for ${worldName}`);
      break;
    case "restore":
      void showSavedGames();
      break;
    case "quit":
      openYesNo("Quit ZZT? ", (yes) => {
        if (yes) {
          drawConnectionNotice("Thanks for playing ZZT!");
        }
      });
      break;
  }
}

function handleKeyDown(event: KeyboardEvent) {
  zztSound.resume();

  if (modal) {
    routeModalKey(event);
    return;
  }

  if (mode === "title") {
    handleTitleKey(event);
    return;
  }

  if (event.code === "KeyC") {
    event.preventDefault();
    stopHeldInput();
    openEntry("Chat:", "", 30, "any", (text) => {
      if (text && text.trim() && ws && ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: "chat", text: text.trim() }));
      }
    });
    return;
  }

  if (event.repeat && !isMovementKey(event.code)) {
    return;
  }

  // Command keys travel as a raw key byte, not a movement mask.
  const command = commandKey(event);
  if (command !== 0) {
    event.preventDefault();
    stopHeldInput();
    sendKey(command);
    return;
  }

  const handled = updatePressed(event, true);
  if (handled) {
    event.preventDefault();
    sendInput(currentMask(), rawKey(event.code));
  }
}

function handleKeyUp(event: KeyboardEvent) {
  if (modal || mode === "title") {
    return;
  }
  const handled = updatePressed(event, false);
  if (handled) {
    event.preventDefault();
    sendInput(currentMask());
  }
}

function handlePointerDown(event: MouseEvent) {
  canvas.focus();
  zztSound.resume();

  if (mode !== "playing" || modal) {
    return;
  }
  const cell = eventCell(event);
  if (!cell) {
    return;
  }
  if (cell.y === 15 && cell.x >= 62 && cell.x <= 73) {
    event.preventDefault();
    stopHeldInput();
    sendKey(COMMAND_SOUND);
  }
}

function eventCell(event: MouseEvent): { x: number; y: number } | null {
  const rect = canvas.getBoundingClientRect();
  if (rect.width <= 0 || rect.height <= 0) {
    return null;
  }
  const x = Math.floor(((event.clientX - rect.left) / rect.width) * COLS);
  const y = Math.floor(((event.clientY - rect.top) / rect.height) * ROWS);
  if (x < 0 || x >= COLS || y < 0 || y >= ROWS) {
    return null;
  }
  return { x, y };
}

function sendKey(key: number) {
  if (!connected || !ws || ws.readyState !== WebSocket.OPEN || playerId === 0) {
    return;
  }
  const input: InputMessage = {
    type: MessageTypeInput,
    playerId,
    seq: ++seq,
    key,
  };
  ws.send(JSON.stringify(input));
}

function sendDebugCommand(text: string) {
  if (!connected || !ws || ws.readyState !== WebSocket.OPEN || playerId === 0) {
    return;
  }
  ws.send(JSON.stringify({ type: MessageTypeDebugCommand, playerId, text }));
}

function sendQuitReply(quit: boolean) {
  if (!connected || !ws || ws.readyState !== WebSocket.OPEN || playerId === 0) {
    return;
  }
  ws.send(JSON.stringify({ type: MessageTypeQuitReply, playerId, quit }));
}

// sendSaveFilename answers a savePrompt. The server sanitizes the name before it
// reaches a path, and answers with a saveResult event either way.
function sendSaveFilename(name: string) {
  if (!connected || !ws || ws.readyState !== WebSocket.OPEN || playerId === 0) {
    return;
  }
  ws.send(JSON.stringify({ type: MessageTypeSaveFilename, playerId, name }));
}

function sendHighScoreName(name: string) {
  if (!connected || !ws || ws.readyState !== WebSocket.OPEN || playerId === 0) {
    leaveToTitle();
    return;
  }
  ws.send(JSON.stringify({ type: MessageTypeHighScoreName, playerId, name }));
  // We have already left the room, so no diff will ever repaint this screen.
  // If the server does not send the finished list, do not strand the player on
  // a frozen board with no way out.
  window.clearTimeout(highScoreTimer);
  highScoreTimer = window.setTimeout(leaveToTitle, 3000);
}

function updatePressed(event: KeyboardEvent, down: boolean): boolean {
  if (!isHandledKey(event.code)) {
    return false;
  }
  if (down) {
    pressed.add(event.code);
  } else {
    pressed.delete(event.code);
  }
  return true;
}

function currentMask(): number {
  return movementMask(pressed);
}

function sendInput(mask: number, key = 0) {
  if (!connected || !ws || ws.readyState !== WebSocket.OPEN || playerId === 0) {
    return;
  }
  // A modal is open: never let held movement keys through.
  if (modal && mask !== 0) {
    return;
  }
  if (mask === 0 && lastMask === 0 && key === 0) {
    return;
  }
  lastMask = mask;
  const input: InputMessage = {
    type: MessageTypeInput,
    playerId,
    seq: ++seq,
  };
  if (mask !== 0) {
    input.keymask = mask;
  } else if (key !== 0) {
    input.key = key;
  } else {
    input.keymask = 0;
  }
  ws.send(JSON.stringify(input));
}
