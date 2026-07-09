import "./style.css";
import { drawSidebar as paintSidebar, updateSidebar as paintSidebarHud } from "./sidebar";
import { renderModal, handleModalKey, type Modal } from "./modal";
import { commandKey, isHandledKey, isMovementKey, movementMask, rawKey } from "./keys";

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
  score?: number;
  listPos?: number;
  notes?: string;
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

type ServerMessage = SnapshotMessage | DiffMessage | EventMessage | BoardChangeMessage;

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
let myX = 0;
let myY = 0;
const overlay = new Map<number, { ch: number; color: number }>();
const cells: ScreenCell[] = Array.from({ length: COLS * ROWS }, (_, i) => ({
  x: i % COLS,
  y: Math.floor(i / COLS),
  ch: 32,
  color: 0x1f,
}));

drawSidebar();
drawScreen();
// Vanilla ZZT has no "Connect" button: the game is just there when you start
// it. Connect on load, refocus on click, and retry quietly if the socket drops.
connect();
canvas.addEventListener("mousedown", () => canvas.focus());
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
    socket.send(JSON.stringify({ type: MessageTypeJoin, name: "browser" }));
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
  return url.toString();
}

function applyMessage(message: ServerMessage) {
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
  }
}

function applySnapshot(message: SnapshotMessage) {
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
  screenCtx.font = "16px 'IBM Plex Mono', 'Cascadia Mono', 'SFMono-Regular', Consolas, monospace";
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
    screenCtx.fillText(toGlyph(ch), x, y + 1);
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

// paintOverlay rebuilds the modal layer from scratch each frame, so a modal
// never has to restore what was underneath it. The pause layer goes underneath
// the modal: a paused player can still have a scroll open over the board.
function paintOverlay() {
  overlay.clear();
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
  // Repaint rather than clear: the pause layer may still be underneath.
  paintOverlay();
  drawScreen();
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
        // The server refuses saves (M3.11, NOTES.md), and there is no inbound
        // saveFilename message, so an accepted name is answered locally. M4.3a
        // adds rejoinable snapshots and the wire format they need.
        openEntry("Save game:", ".SAV", 8, "alphanum", (name) => {
          if (name) {
            openWindow("Saving", ["", "  Saving is disabled on this server.", ""], true);
          }
        });
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
      appendLogOnce(`sound priority ${event.priority ?? 0}`);
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
      // SidebarPromptYesNo("Quit ZZT? "). QuitPromptEvent carries no statId, so
      // it cannot be routed to one player and there is no reply channel back to
      // the engine — both are M4.3. Answering resolves the modal locally.
      openYesNo("Quit ZZT? ", (yes) => {
        appendLog(`quit prompt answered ${yes ? "yes" : "no"} (not wired to the server yet)`);
      });
      break;
    case "highScoreEntry":
      appendLog(`high score ${event.score ?? 0}`);
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

function handleKeyDown(event: KeyboardEvent) {
  // A modal consumes EVERY key: even one it ignores must not reach gameplay.
  if (modal) {
    event.preventDefault();
    const previous = modal;
    const result = handleModalKey(modal, event);
    if (result === "close") {
      // A callback may have chained straight into another modal (e.g. the save
      // prompt opening its "disabled" notice). Only tear down if it did not.
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
  if (modal) {
    return;
  }
  const handled = updatePressed(event, false);
  if (handled) {
    event.preventDefault();
    sendInput(currentMask());
  }
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


