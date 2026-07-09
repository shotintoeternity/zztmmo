import "./style.css";
import { drawSidebar as paintSidebar, updateSidebar as paintSidebarHud } from "./sidebar";
import { renderTextWindow, clampLinePos, TEXT_WINDOW_PAGE, type TextWindowState } from "./textwindow";

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
const MessageTypeDebugCommand = "debugCommand";
const MessageTypeScrollReply = "scrollReply";

// TextWindowSelect's title prompts when the cursor sits on a "!label;text" line.
const SELECT_PROMPT = "\xaePress ENTER to select this\xaf";
const MORE_INFO_PROMPT = "\xaePress ENTER for more info\xaf";

// GameDebugPrompt's PromptString(63, 5, 0x1E, 0x0F, 11, PROMPT_ANY, ...).
const DEBUG_PROMPT_X = 63;
const DEBUG_PROMPT_Y = 5;
const DEBUG_PROMPT_WIDTH = 11;
const DEBUG_PROMPT_ARROW_COLOR = 0x1e;
const DEBUG_PROMPT_COLOR = 0x0f;

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

let ws: WebSocket | null = null;
let playerId = 0;
let myStatId = -1;
let seq = 0;
let boardId = 0;
let tick = 0;
let lastMask = 0;
let inputTimer = 0;
let connected = false;
let lastMessageKey = "";
let lastMessageAt = 0;
const pressed = new Set<string>();

// While a modal is up, gameplay keys are swallowed. The simulation does NOT
// pause behind it (M1.3 deviation), so the board keeps updating underneath and
// the modal is painted as an overlay rather than by saving/restoring cells.
type Mode = "play" | "debug" | "window";
let mode: Mode = "play";
let windowState: TextWindowState | null = null;
let windowBaseTitle = "";
// Set for scroll windows: the object stat a "!label" selection is sent back to.
// -1 for read-only windows such as help.
let windowReplyStatId = -1;
// TextWindowSelect only swaps the title to the ENTER prompt once the cursor has
// moved, so the first frame keeps the real title even on a "!" line.
let windowMoved = false;
let debugBuffer = "";
const overlay = new Map<number, { ch: number; color: number }>();
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
    // No board: the server picks its configured default (zzt-server -board).
    socket.send(JSON.stringify({ type: MessageTypeJoin, name: "browser" }));
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
      closeModal();
      applySnapshot(message.snapshot);
      break;
  }
}

function applySnapshot(message: SnapshotMessage) {
  playerId = message.you.id;
  myStatId = message.you.statId;
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

// Stat ids shift when other players leave the board, so track ours from every
// message that carries the roster.
function trackMyStatId(players: PlayerSnapshot[] | undefined) {
  if (!players) {
    return;
  }
  for (const player of players) {
    if (player.id === playerId) {
      myStatId = player.statId;
      return;
    }
  }
}

function applyDiff(message: DiffMessage) {
  boardId = message.boardId;
  tick = message.tick;
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
// never has to restore what was underneath it.
function paintOverlay() {
  overlay.clear();
  if (mode === "window" && windowState) {
    renderTextWindow(writeOverlay, windowState);
  } else if (mode === "debug") {
    paintDebugPrompt();
  }
}

// paintDebugPrompt is PromptString's redraw, at the sidebar coordinates
// GameDebugPrompt uses.
function paintDebugPrompt() {
  for (let i = 0; i <= DEBUG_PROMPT_WIDTH - 1; i += 1) {
    writeOverlay(DEBUG_PROMPT_X + i, DEBUG_PROMPT_Y, DEBUG_PROMPT_COLOR, " ");
    writeOverlay(DEBUG_PROMPT_X + i, DEBUG_PROMPT_Y - 1, DEBUG_PROMPT_ARROW_COLOR, " ");
  }
  writeOverlay(DEBUG_PROMPT_X + DEBUG_PROMPT_WIDTH, DEBUG_PROMPT_Y - 1, DEBUG_PROMPT_ARROW_COLOR, " ");
  const cursorColor = Math.trunc(DEBUG_PROMPT_ARROW_COLOR / 0x10) * 16 + 0x0f;
  writeOverlay(DEBUG_PROMPT_X + debugBuffer.length, DEBUG_PROMPT_Y - 1, cursorColor, "\x1f");
  writeOverlay(DEBUG_PROMPT_X, DEBUG_PROMPT_Y, DEBUG_PROMPT_COLOR, debugBuffer);
}

function openWindow(title: string, lines: string[], viewingFile: boolean, replyStatId = -1) {
  if (lines.length === 0) {
    return;
  }
  stopHeldInput();
  mode = "window";
  windowBaseTitle = title;
  windowReplyStatId = replyStatId;
  windowMoved = false;
  windowState = { title, lines, linePos: 1, viewingFile };
  paintOverlay();
  drawScreen();
}

// hyperlinkOf extracts the ZZT-OOP label from a "!label;text" line, or "" if the
// line is not a hyperlink. "!-FILE;text" jumps to another help file — not wired
// up yet, so it is reported as "" and ignored.
function hyperlinkOf(line: string): string {
  if (!line.startsWith("!")) {
    return "";
  }
  let pointer = line.slice(1);
  const semi = pointer.indexOf(";");
  if (semi >= 0) {
    pointer = pointer.slice(0, semi);
  }
  if (pointer.startsWith("-")) {
    return "";
  }
  return pointer;
}

// Mirrors TextWindowSelect's title swap.
function resolveWindowTitle() {
  if (!windowState) {
    return;
  }
  const line = windowState.lines[windowState.linePos - 1] ?? "";
  if (windowMoved && line.startsWith("!")) {
    windowState.title = windowReplyStatId >= 0 ? SELECT_PROMPT : MORE_INFO_PROMPT;
  } else {
    windowState.title = windowBaseTitle;
  }
}

function openDebugPrompt() {
  stopHeldInput();
  mode = "debug";
  debugBuffer = "";
  paintOverlay();
  drawScreen();
}

function closeModal() {
  mode = "play";
  windowState = null;
  windowReplyStatId = -1;
  windowMoved = false;
  debugBuffer = "";
  overlay.clear();
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
        openDebugPrompt();
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
  if (mode !== "play") {
    event.preventDefault();
    if (mode === "window") {
      handleWindowKey(event);
    } else {
      handleDebugKey(event);
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
    sendInput(currentMask(), rawKey(event));
  }
}

// handleWindowKey is TextWindowSelect's navigation. Enter on a "!label;text"
// line selects it; anywhere else Enter closes, and Escape always closes without
// a reply (TextWindowRejected).
function handleWindowKey(event: KeyboardEvent) {
  if (!windowState) {
    return;
  }
  const lineCount = windowState.lines.length;
  let next = windowState.linePos;
  switch (event.code) {
    case "ArrowUp":
      next -= 1;
      break;
    case "ArrowDown":
      next += 1;
      break;
    case "PageUp":
      next -= TEXT_WINDOW_PAGE;
      break;
    case "PageDown":
      next += TEXT_WINDOW_PAGE;
      break;
    case "Escape":
      closeModal();
      return;
    case "Enter": {
      const label = hyperlinkOf(windowState.lines[windowState.linePos - 1] ?? "");
      if (label && windowReplyStatId >= 0) {
        sendScrollReply(windowReplyStatId, label);
      }
      closeModal();
      return;
    }
    default:
      return;
  }
  const clamped = clampLinePos(next, lineCount);
  if (clamped !== windowState.linePos) {
    windowState.linePos = clamped;
    windowMoved = true;
  }
  resolveWindowTitle();
  paintOverlay();
  drawScreen();
}

// handleDebugKey is PromptString's editing loop for PROMPT_ANY.
function handleDebugKey(event: KeyboardEvent) {
  if (event.code === "Enter") {
    sendDebugCommand(debugBuffer);
    closeModal();
    return;
  }
  if (event.code === "Escape") {
    // Vanilla restores the old (empty) buffer and still runs the tail of
    // GameDebugPrompt, which plays a sound. Send the empty command.
    sendDebugCommand("");
    closeModal();
    return;
  }
  if (event.code === "Backspace" || event.code === "ArrowLeft") {
    debugBuffer = debugBuffer.slice(0, -1);
  } else if (event.key.length === 1 && event.key >= " " && event.key.charCodeAt(0) < 0x80) {
    if (debugBuffer.length < DEBUG_PROMPT_WIDTH) {
      debugBuffer += event.key;
    }
  } else {
    return;
  }
  paintOverlay();
  drawScreen();
}

function handleKeyUp(event: KeyboardEvent) {
  if (mode !== "play") {
    return;
  }
  const handled = updatePressed(event, false);
  if (handled) {
    event.preventDefault();
    sendInput(currentMask());
  }
}

// commandKey maps the play-mode command keys the engine reads out of
// InputKeyPressed. Movement stays on the keymask. Full parity is M4.2.
function commandKey(event: KeyboardEvent): number {
  if (event.ctrlKey || event.metaKey || event.altKey) {
    return 0;
  }
  if (event.key === "?") {
    return "?".charCodeAt(0);
  }
  if (event.code === "KeyH") {
    return "H".charCodeAt(0);
  }
  return 0;
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
  // A modal is open: never let held movement keys through.
  if (mode !== "play" && mask !== 0) {
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
