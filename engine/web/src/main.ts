import "./style.css";
import { drawSidebar as paintSidebar, updateSidebar as paintSidebarHud } from "./sidebar";

const COLS = 80;
// The server streams board columns 0..59 only. Columns 60..79 are the sidebar,
// which this client draws itself from HUD data — see drawSidebar/updateSidebar,
// transcribed from the engine's GameDrawSidebar/GameUpdateSidebar.
const BOARD_COLS = 60;
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

const InputMaskUp = 1 << 0;
const InputMaskDown = 1 << 1;
const InputMaskLeft = 1 << 2;
const InputMaskRight = 1 << 3;
const InputMaskShift = 1 << 4;
const InputMaskShoot = 1 << 5;

const KeyEnter = 13;
const KeyEscape = 27;

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

app.innerHTML = `
  <main class="shell">
    <section class="stage">
      <div class="topbar">
        <div class="brand">
          <h1>ZZT MMO</h1>
          <span class="status" data-status>disconnected</span>
        </div>
        <div class="actions">
          <button class="button" data-connect>Connect</button>
          <button class="button secondary" data-focus>Focus</button>
        </div>
      </div>
      <div class="canvas-wrap">
        <canvas data-screen width="${WIDTH}" height="${HEIGHT}" tabindex="0"></canvas>
        <div class="overlay" data-overlay>
          <div>
            <p class="overlay-title">ZZT MMO</p>
            <p class="overlay-text">Connect to the server, then use arrows or WASD. Hold Shift to shoot, Space to fire in the last direction.</p>
          </div>
        </div>
      </div>
      <div class="event-log" data-log></div>
    </section>
    <aside class="side">
      <section class="panel messages">
        <h2>Messages</h2>
        <div data-messages></div>
      </section>
    </aside>
  </main>
`;

const canvas = query<HTMLCanvasElement>("[data-screen]");
const ctx = canvas.getContext("2d");
if (!ctx) {
  throw new Error("canvas context unavailable");
}
const screenCtx = ctx;
const statusEl = query<HTMLElement>("[data-status]");
const overlayEl = query<HTMLElement>("[data-overlay]");
const connectButton = query<HTMLButtonElement>("[data-connect]");
const focusButton = query<HTMLButtonElement>("[data-focus]");
const logEl = query<HTMLElement>("[data-log]");
const messageEl = query<HTMLElement>("[data-messages]");

let ws: WebSocket | null = null;
let playerId = 0;
let seq = 0;
let boardId = 0;
let tick = 0;
let lastMask = 0;
let inputTimer = 0;
let connected = false;
let lastMessageKey = "";
let lastMessageAt = 0;
const pressed = new Set<string>();
const cells: ScreenCell[] = Array.from({ length: COLS * ROWS }, (_, i) => ({
  x: i % COLS,
  y: Math.floor(i / COLS),
  ch: 32,
  color: 0x1f,
}));

drawSidebar();
drawScreen();
connectButton.addEventListener("click", () => connect());
focusButton.addEventListener("click", () => canvas.focus());
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

  setStatus("connecting");
  connectButton.disabled = true;
  const socket = new WebSocket(wsURL());
  ws = socket;

  socket.addEventListener("open", () => {
    connected = true;
    setStatus("joining");
    socket.send(JSON.stringify({ type: MessageTypeJoin, name: "browser", board: 1 }));
    canvas.focus();
    inputTimer = window.setInterval(() => sendInput(currentMask()), 55);
  });

  socket.addEventListener("message", (event) => {
    const message = JSON.parse(String(event.data)) as ServerMessage;
    applyMessage(message);
  });

  socket.addEventListener("close", () => disconnect("disconnected"));
  socket.addEventListener("error", () => disconnect("connection error"));
}

function disconnect(reason: string) {
  connected = false;
  connectButton.disabled = false;
  window.clearInterval(inputTimer);
  setStatus(reason);
  overlayEl.hidden = false;
  ws = null;
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
      applySnapshot(message.snapshot);
      break;
  }
}

function applySnapshot(message: SnapshotMessage) {
  playerId = message.you.id;
  boardId = message.boardId;
  tick = message.tick;
  overlayEl.hidden = true;
  setStatus(`board ${boardId} tick ${tick}`);
  replaceCells(message.screen);
  drawSidebar();
  updateSidebar(message.hud);
  renderEvents(message.events);
  drawScreen();
}

function applyDiff(message: DiffMessage) {
  boardId = message.boardId;
  tick = message.tick;
  if (message.cells) {
    for (const cell of message.cells) {
      setBoardCell(cell);
    }
  }
  if (message.hud) {
    updateSidebar(message.hud);
  }
  renderEvents(message.events);
  setStatus(`board ${boardId} tick ${tick}`);
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
  for (const cell of cells) {
    const fg = cell.color & 0x0f;
    const bg = (cell.color >> 4) & 0x0f;
    const x = cell.x * CELL_W;
    const y = cell.y * CELL_H;
    screenCtx.fillStyle = ega[bg] ?? "#000000";
    screenCtx.fillRect(x, y, CELL_W, CELL_H);
    screenCtx.fillStyle = ega[fg] ?? "#ffffff";
    screenCtx.fillText(toGlyph(cell.ch), x, y + 1);
  }
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

function handleProtocolEvent(event: ProtocolEvent) {
  switch (event.type) {
    case "scroll":
    case "help":
      stopHeldInput();
      showMessage(event.title ?? event.filename ?? "Message", event.lines ?? []);
      appendLogOnce(`${event.type}: ${event.title ?? event.filename ?? ""}`);
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
      appendLog("quit prompt");
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

function showMessage(title: string, lines: string[]) {
  messageEl.innerHTML = "";
  const heading = document.createElement("p");
  heading.className = "message-title";
  heading.textContent = title;
  const list = document.createElement("ul");
  list.className = "message-lines";
  for (const line of lines.slice(0, 8)) {
    const item = document.createElement("li");
    item.textContent = line;
    list.appendChild(item);
  }
  messageEl.append(heading, list);
}

function appendLog(text: string) {
  const line = document.createElement("div");
  line.className = "event-line";
  line.textContent = text;
  logEl.appendChild(line);
  while (logEl.children.length > 80) {
    logEl.firstElementChild?.remove();
  }
  logEl.scrollTop = logEl.scrollHeight;
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
  if (event.repeat && !isMovementKey(event.code)) {
    return;
  }
  const handled = updatePressed(event, true);
  if (handled) {
    event.preventDefault();
    sendInput(currentMask(), rawKey(event));
  }
}

function handleKeyUp(event: KeyboardEvent) {
  const handled = updatePressed(event, false);
  if (handled) {
    event.preventDefault();
    sendInput(currentMask());
  }
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
  let mask = 0;
  if (pressed.has("ArrowUp") || pressed.has("KeyW")) {
    mask |= InputMaskUp;
  }
  if (pressed.has("ArrowDown") || pressed.has("KeyS")) {
    mask |= InputMaskDown;
  }
  if (pressed.has("ArrowLeft") || pressed.has("KeyA")) {
    mask |= InputMaskLeft;
  }
  if (pressed.has("ArrowRight") || pressed.has("KeyD")) {
    mask |= InputMaskRight;
  }
  if (pressed.has("ShiftLeft") || pressed.has("ShiftRight")) {
    mask |= InputMaskShift;
  }
  if (pressed.has("Space")) {
    mask |= InputMaskShoot;
  }
  return mask;
}

function sendInput(mask: number, key = 0) {
  if (!connected || !ws || ws.readyState !== WebSocket.OPEN || playerId === 0) {
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

function rawKey(event: KeyboardEvent): number {
  if (event.code === "Enter") {
    return KeyEnter;
  }
  if (event.code === "Escape") {
    return KeyEscape;
  }
  return 0;
}

function isMovementKey(code: string): boolean {
  return code === "ArrowUp" || code === "ArrowDown" || code === "ArrowLeft" || code === "ArrowRight" ||
    code === "KeyW" || code === "KeyA" || code === "KeyS" || code === "KeyD";
}

function isHandledKey(code: string): boolean {
  return isMovementKey(code) || code === "ShiftLeft" || code === "ShiftRight" || code === "Space" ||
    code === "Enter" || code === "Escape";
}

function setStatus(text: string) {
  statusEl.textContent = text;
}
