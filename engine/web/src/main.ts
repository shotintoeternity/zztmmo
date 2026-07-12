import "./style.css";
import { drawSidebar as paintSidebar, updateSidebar as paintSidebarHud } from "./sidebar";
import { renderModal, handleModalKey, POPUP_Y_CENTERED, type Modal } from "./modal";
import { commandKey, isHandledKey, isMovementKey, movementMask, rawKey } from "./keys";
import { drawTitleSidebar, titleCommand } from "./title";
import { soundNotesFromProtocol, ZztSound } from "./sound";
import { generationLines, runDreamGeneration, type GenerationProgress } from "./dream";
import { drawEditorSidebar, type EditorInspect } from "./editor";

const COLS = 80;
// The server streams board columns 0..59 only. Columns 60..79 are the sidebar,
// which this client draws itself from HUD data — see drawSidebar/updateSidebar,
// transcribed from the engine's GameDrawSidebar/GameUpdateSidebar.
const BOARD_COLS = 60;
const SIDEBAR_COLS = COLS - BOARD_COLS;
const ROWS = 25;
// One cell is one glyph of the 8x14 EGA font, blitted 1:1. The backing store is
// therefore the EGA text-mode framebuffer exactly: 640x350, square pixels, as
// Zeta renders it. Any other CELL_W/CELL_H resamples the glyph and the atlas
// bleeds neighbouring characters into the cell; CSS does the upscale instead.
const CELL_W = 8;
const CELL_H = 14;
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
const MessageTypeEditorEnter = "editorEnter";
const MessageTypeEditorExit = "editorExit";
const MessageTypeEditorInspect = "editorInspect";
const MessageTypeEditorSnapshot = "editorSnapshot";
const MessageTypeEditorEdit = "editorEdit";
const MessageTypeEditorDiff = "editorDiff";
const MessageTypeEditorProperty = "editorProperty";
const MessageTypeEditorProperties = "editorProperties";
const MessageTypeEditorStat = "editorStat";
const MessageTypeEditorStatSettings = "editorStatSettings";
const MessageTypeEditorProgram = "editorProgram";
const MessageTypeEditorProgramText = "editorProgramText";
const MessageTypeEditorProgramSave = "editorProgramSave";
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

type EditorSnapshotMessage = {
	type: typeof MessageTypeEditorSnapshot;
	boardId: number;
	screen: ScreenCell[];
	inspect: EditorInspect;
	properties: EditorProperties;
};

type EditorInspectMessage = {
  type: typeof MessageTypeEditorInspect;
  inspect: EditorInspect;
};

type EditorDiffMessage = {
  type: typeof MessageTypeEditorDiff;
  cells: ScreenCell[];
  inspect: EditorInspect;
};

type EditorBoardOption = {
  id: number;
  name: string;
};

type EditorProperties = {
  boardId: number;
  boardName: string;
  worldName: string;
  maxShots: number;
  isDark: boolean;
  neighborBoards: number[];
  reenterWhenZapped: boolean;
  timeLimitSec: number;
  boards: EditorBoardOption[];
};

type EditorPropertiesMessage = {
  type: typeof MessageTypeEditorProperties;
  properties: EditorProperties;
  screen: ScreenCell[];
};

type EditorStatSettingsMessage = {
  type: typeof MessageTypeEditorStatSettings;
  inspect: EditorInspect;
  cells: ScreenCell[];
};

type EditorProgramTextMessage = {
  type: typeof MessageTypeEditorProgramText;
  statId: number;
  prompt: string;
  lines: string[];
};

type ServerMessage = SnapshotMessage | DiffMessage | EventMessage | BoardChangeMessage | ChatMessage | EditorSnapshotMessage | EditorInspectMessage | EditorDiffMessage | EditorPropertiesMessage | EditorStatSettingsMessage | EditorProgramTextMessage;

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

// Zeta's 8x14 EGA font (fonts/pc_ega.png upstream): 256 glyphs as 32 columns by
// 8 rows, CP437 order, so glyph N sits at (N%32, N/32). Character codes go to
// the sheet directly — there is no Unicode round trip.
import pcEgaUrl from "./pc_ega.png";

const GLYPH_COLS = 32;

const fontImg = new Image();
fontImg.src = pcEgaUrl;
const fontCanvases: HTMLCanvasElement[] = [];

fontImg.onload = () => {
  const tempCanvas = document.createElement("canvas");
  tempCanvas.width = fontImg.width;
  tempCanvas.height = fontImg.height;
  const tempCtx = tempCanvas.getContext("2d");
  if (!tempCtx) return;

  tempCtx.drawImage(fontImg, 0, 0);
  const imgData = tempCtx.getImageData(0, 0, tempCanvas.width, tempCanvas.height);
  const data = imgData.data;

  // The sheet is black-on-white 1-bit. Punch the background out so the tint
  // below only lands on the glyph, and force the ink to pure white so
  // "source-in" yields the palette colour undarkened.
  for (let i = 0; i < data.length; i += 4) {
    if (data[i] + data[i + 1] + data[i + 2] < 50) {
      data[i + 3] = 0;
    } else {
      data[i] = 255;
      data[i + 1] = 255;
      data[i + 2] = 255;
      data[i + 3] = 255;
    }
  }
  tempCtx.putImageData(imgData, 0, 0);

  // One pre-tinted sheet per EGA foreground colour, so drawing a cell is a
  // single blit with no per-frame compositing.
  for (let i = 0; i < 16; i++) {
    const canvas = document.createElement("canvas");
    canvas.width = tempCanvas.width;
    canvas.height = tempCanvas.height;
    const ctx = canvas.getContext("2d");
    if (ctx) {
      ctx.imageSmoothingEnabled = false;
      ctx.drawImage(tempCanvas, 0, 0);
      ctx.globalCompositeOperation = "source-in";
      ctx.fillStyle = ega[i];
      ctx.fillRect(0, 0, canvas.width, canvas.height);
    }
    fontCanvases.push(canvas);
  }
  drawScreen();
};

const app = document.querySelector<HTMLDivElement>("#app");
if (!app) {
  throw new Error("missing app root");
}

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
// Glyphs blit 1:1, so this changes nothing today — but it is the guard that
// keeps a future scale change from silently reintroducing atlas bleed.
screenCtx.imageSmoothingEnabled = false;

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
type Mode = "title" | "playing" | "editor";
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
let editorCursor = { x: 30, y: 12 };
let editorInspect: EditorInspect = {
  x: editorCursor.x,
  y: editorCursor.y,
  elementId: 0,
  element: "",
  character: 32,
  color: 0x0f,
  hasStat: false,
};
let editorProperties: EditorProperties = {
  boardId: 0,
  boardName: "",
  worldName: "",
  maxShots: 0,
  isDark: false,
  neighborBoards: [0, 0, 0, 0],
  reenterWhenZapped: false,
  timeLimitSec: 0,
  boards: [{ id: 0, name: "None" }],
};
let editorBrush = { element: 21, character: 0xdb, color: 0x0e, copied: false };
let editorDrawing = false;
let editorPointerDrawing = false;
const overlay = new Map<number, { ch: number; color: number }>();
const cells: ScreenCell[] = Array.from({ length: COLS * ROWS }, (_, i) => ({
  x: i % COLS,
  y: Math.floor(i / COLS),
  ch: 32,
  color: 0x1f,
}));

let hasPromptedNameOnLaunch = false;

// The launch sequence: name, then world, then play. Nothing joins a room until
// a world is chosen, so the animated title board keeps running underneath.
function promptNicknameOnLaunch() {
  if (hasPromptedNameOnLaunch) {
    return;
  }
  hasPromptedNameOnLaunch = true;
  openPopupEntry(
    "Welcome to ZZTMMO!  Enter your name:",
    (name) => {
      nickname = name && name.trim() ? name.trim() : "player" + Math.floor(Math.random() * 1000);
      void showWorlds();
    },
    POPUP_Y_CENTERED,
  );
}

drawScreen();
if ("fonts" in document) {
  document.fonts.ready.then(async () => {
    await showTitle();
    promptNicknameOnLaunch();
  });
} else {
  showTitle().then(() => {
    promptNicknameOnLaunch();
  });
}
canvas.addEventListener("mousedown", handlePointerDown);
canvas.addEventListener("mousemove", handlePointerMove);
window.addEventListener("mouseup", () => { editorPointerDrawing = false; });
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

// titleStream carries the animated title board (engine/title_sim.go). It is an
// EventSource, not a WebSocket: the title screen has no socket by design, and
// the traffic is one-way. Only board columns 0..59 arrive, so it can never
// tread on the sidebar this client draws itself.
let titleStream: EventSource | null = null;

function closeTitleStream() {
  if (titleStream) {
    titleStream.close();
    titleStream = null;
  }
}

function openTitleStream(filename: string) {
  closeTitleStream();
  const source = new EventSource("/api/title/stream?world=" + encodeURIComponent(filename));
  titleStream = source;
  source.onmessage = (event) => {
    // A stream we have replaced, or one that outlived the title screen, must
    // not paint over a live board.
    if (titleStream !== source || mode !== "title") {
      return;
    }
    for (const cell of JSON.parse(event.data) as ScreenCell[]) {
      setCell(cell);
    }
    drawScreen();
  };
  // An old server has no /api/title/stream: the board simply stays static, and
  // EventSource retries on its own. Nothing to do.
  source.onerror = () => {};
}

// showTitle paints GameTitleLoop's screen: board 0 behind the monitor sidebar.
// The board comes from /api/title rather than the snapshot stream because we
// have no socket yet; the stream then animates it, as vanilla's does.
async function showTitle() {
  mode = "title";
  modal = null;
  playerId = 0;
  myStatId = -1;
  editorCursor = { x: 30, y: 12 };
  zztSound.setEnabled(false);
  setPaused(false);
  pressed.clear();
  lastMask = 0;
  clearScrolls();
  closeTitleStream();

  let friendlyName = worldName;
  let streaming = false;
  try {
    const url = worldName === "Untitled" ? "/api/title" : "/api/title?world=" + encodeURIComponent(worldName);
    const response = await fetch(url);
    const title = (await response.json()) as { world: string; filename?: string; screen: ScreenCell[] };
    worldName = title.filename || title.world;
    friendlyName = title.world;
    replaceCells(title.screen);
    streaming = true;
  } catch {
    // Offline: keep whatever board is on screen and still draw the menu, so
    // the player can retry with 'P'.
  }
  drawTitleSidebar(writeText, friendlyName);
  paintOverlay();
  drawScreen();
  canvas.focus();
  if (streaming) {
    openTitleStream(worldName);
  }
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
  closeTitleStream();
  clearScrolls();
  zztSound.setEnabled(true);
  zztSound.resume();
  leavingToTitle = false;
  drawSidebar();
  drawScreen();
  connect();
}

// startEditor deliberately opens a different kind of WebSocket. The server
// receives editorEnter instead of join, creates an isolated EditorSession, and
// never registers this browser with a RoomManager.
function startEditor() {
  closeTitleStream();
  clearScrolls();
  zztSound.setEnabled(false);
  leavingToTitle = false;
  mode = "editor";
  editorCursor = { x: 30, y: 12 };
  editorInspect = { x: 30, y: 12, elementId: 0, element: "", character: 32, color: 0x0f, hasStat: false };
  editorBrush = { element: 21, character: 0xdb, color: 0x0e, copied: false };
  editorDrawing = false;
  drawEditorSidebar(writeText, editorInspect, editorBrush, editorDrawing);
  paintOverlay();
  drawScreen();
  connectEditor();
}

function leaveEditor() {
  leavingToTitle = true;
  connected = false;
  window.clearTimeout(retryTimer);
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({ type: MessageTypeEditorExit }));
    ws.close();
  }
  ws = null;
  void showTitle();
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

// LOBBY_WORLD is the world everyone lands in by default and nobody has to
// finish: the hangout. Every other world is a game you bring people to.
const LOBBY_WORLD = "TOWN";

// The blurb sits above the list as ordinary text-window lines: only "!label;"
// lines are selectable, so none of it can be picked by accident. Keep it short.
// The text window shows 15 lines centered on the cursor, and the cursor opens on
// the first world — so a blurb longer than this scrolls its own first lines off
// the top before the player has read them.
const WORLD_SELECT_BLURB = [
  "",
  "  Each world runs on its own. Bring your",
  "  friends into one and it is yours alone.",
  "",
  `  ${LOBBY_WORLD} is the ZZTMMO lobby: no quest, just`,
  "  people. Drop in and hang out.",
  "",
  "$Pick a world",
];

/** worldLabel marks the lobby in the list, e.g. "TOWN (ZZTMMO Lobby)". */
function worldLabel(name: string): string {
  return name === LOBBY_WORLD ? `${name} (ZZTMMO Lobby)` : name;
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

  // The lobby leads, then everything else in the order the server listed it.
  const ordered = [
    ...worlds.filter((w) => w === LOBBY_WORLD),
    ...worlds.filter((w) => w !== LOBBY_WORLD),
  ];

  openSelectList(
    "Select a World",
    [...ordered.map(worldLabel), "Dream a world..."],
    (selected) => {
      if (selected === "Dream a world...") {
        openDreamPrompt();
        return;
      }
      const name = selected.split(" (")[0];
      void enterWorld(name);
    },
    WORLD_SELECT_BLURB,
  );
}

function openDreamPrompt() {
  openModal({
    kind: "multilineEntry",
    title: "Dream a world",
    lines: [""],
    line: 0,
    onSubmit: (premise) => {
      if (premise) {
        void startDreamGeneration(premise);
      }
    },
  });
}

function showGenerationProgress(progress: GenerationProgress[]) {
  openWindow("Dreaming a world", generationLines(progress), true);
}

async function startDreamGeneration(prompt: string) {
  showGenerationProgress([]);
  try {
    const world = await runDreamGeneration(
      prompt,
      fetch,
      () => new Promise((resolve) => window.setTimeout(resolve, 500)),
      showGenerationProgress,
    );
    await enterWorld(world);
  } catch (error) {
    openWindow("Dream failed", ["", String(error), "", "Try a shorter premise later."], true);
  }
}

// enterWorld joins the chosen world's own instance. It does NOT call
// /api/loadworld: that swaps the server's single default world for everybody
// and is refused while anyone is playing. Each world already has its own
// RoomManager server-side (WebSocketServer.GetOrCreateInstance), reached by the
// ?world= parameter wsURL sends — which is what makes worlds independent.
async function enterWorld(name: string) {
  worldName = name;
  await showTitle();
  startPlay();
}

// The old loadWorld POSTed /api/loadworld, swapping the server's single default
// world for everybody — which the server rightly refused while anyone was in a
// room. enterWorld replaces it: worlds are instances, so picking one is a local
// choice and needs no server-wide reload. The endpoint remains for other callers.

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

function connectEditor() {
  if (ws && ws.readyState === WebSocket.OPEN) {
    return;
  }
  const socket = new WebSocket(wsURL());
  ws = socket;
  socket.addEventListener("open", () => {
    connected = true;
    socket.send(JSON.stringify({ type: MessageTypeEditorEnter, world: worldName }));
    canvas.focus();
  });
  socket.addEventListener("message", (event) => {
    applyMessage(JSON.parse(String(event.data)) as ServerMessage);
  });
  socket.addEventListener("close", () => {
    if (leavingToTitle) {
      return;
    }
    connected = false;
    ws = null;
    void showTitle();
    drawConnectionNotice("Editor disconnected");
  });
  socket.addEventListener("error", () => {
    // close owns the recovery so an error cannot race it into a game reconnect.
  });
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
  if (message.type !== MessageTypeSnapshot && message.type !== MessageTypeEditorSnapshot && (leavingToTitle || mode === "title")) {
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
    case MessageTypeEditorSnapshot:
      applyEditorSnapshot(message);
      break;
    case MessageTypeEditorInspect:
      applyEditorInspect(message);
      break;
	case MessageTypeEditorDiff:
	  applyEditorDiff(message);
	  break;
	case MessageTypeEditorProperties:
	  applyEditorProperties(message);
	  break;
	case MessageTypeEditorStatSettings:
	  applyEditorStatSettings(message);
	  break;
	case MessageTypeEditorProgramText:
	  applyEditorProgramText(message);
	  break;
  }
}

function applyEditorSnapshot(message: EditorSnapshotMessage) {
  mode = "editor";
  editorInspect = message.inspect;
  editorCursor = { x: message.inspect.x, y: message.inspect.y };
	  editorProperties = message.properties;
  replaceCells(message.screen);
  drawEditorSidebar(writeText, editorInspect, editorBrush, editorDrawing);
  paintOverlay();
  drawScreen();
}

function applyEditorInspect(message: EditorInspectMessage) {
  editorInspect = message.inspect;
  editorCursor = { x: message.inspect.x, y: message.inspect.y };
  drawEditorSidebar(writeText, editorInspect, editorBrush, editorDrawing);
  paintOverlay();
  drawScreen();
}

function applyEditorDiff(message: EditorDiffMessage) {
  for (const cell of message.cells) setBoardCell(cell);
  editorInspect = message.inspect;
  editorCursor = { x: message.inspect.x, y: message.inspect.y };
  drawEditorSidebar(writeText, editorInspect, editorBrush, editorDrawing);
  paintOverlay();
  drawScreen();
}

function applyEditorProperties(message: EditorPropertiesMessage) {
  editorProperties = message.properties;
  replaceCells(message.screen);
  drawEditorSidebar(writeText, editorInspect, editorBrush, editorDrawing);
  paintOverlay();
  drawScreen();
}

function applyEditorStatSettings(message: EditorStatSettingsMessage) {
  for (const cell of message.cells) setBoardCell(cell);
  editorInspect = message.inspect;
  editorCursor = { x: message.inspect.x, y: message.inspect.y };
  drawEditorSidebar(writeText, editorInspect, editorBrush, editorDrawing);
  paintOverlay();
  drawScreen();
}

// applyEditorProgramText opens the M5.4 code editor once the server returns the
// requested object/scroll program. Saving is Escape (EditorEditStatText has no
// cancel), which sends the edited lines back as editorProgramSave.
function applyEditorProgramText(message: EditorProgramTextMessage) {
  const statId = message.statId;
  openModal({
    kind: "programEditor",
    title: message.prompt || "Edit Program",
    lines: message.lines.length > 0 ? [...message.lines] : [""],
    linePos: 1,
    charPos: 1,
    insertMode: true,
    onSubmit: (lines) => sendEditorProgramSave(statId, lines),
  });
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
  if (fontCanvases.length < 16) {
    return;
  }
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

    const col = ch % GLYPH_COLS;
    const row = Math.floor(ch / GLYPH_COLS);
    screenCtx.drawImage(
      fontCanvases[fg],
      col * CELL_W,
      row * CELL_H,
      CELL_W,
      CELL_H,
      x,
      y,
      CELL_W,
      CELL_H
    );
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
let currentChatMessage = "";
let currentChatTimer = 0;

function handleChatMessage(message: { from: string; text: string }) {
  chatMessages.push({ from: message.from, text: message.text });
  if (chatMessages.length > 50) {
    chatMessages.shift();
  }

  currentChatMessage = `<${message.from}> ${message.text}`;
  window.clearTimeout(currentChatTimer);
  currentChatTimer = window.setTimeout(() => {
    currentChatMessage = "";
    paintOverlay();
    drawScreen();
  }, 5000);

  if (modal && modal.kind === "text" && modal.baseTitle === "Global Chat") {
    openChatWindow();
  } else {
    paintOverlay();
    drawScreen();
  }
}

function openChatWindow() {
  const lines = [
    "  --- Global Chat ---",
    "",
    "!send;[Send Message]",
    "",
  ];
  for (let i = chatMessages.length - 1; i >= 0; i--) {
    const msg = chatMessages[i];
    const rawLine = `<${msg.from}> ${msg.text}`;
    const prefix = "  ";
    if (rawLine.length <= 44) {
      lines.push(prefix + rawLine);
    } else {
      lines.push(prefix + rawLine.slice(0, 44));
      lines.push(prefix + "  " + rawLine.slice(44, 88));
    }
  }
  openModal({
    kind: "text",
    state: { title: "Global Chat", lines, linePos: 1, viewingFile: false },
    baseTitle: "Global Chat",
    moved: false,
    selectable: true,
    onSelect: (label) => {
      if (label === "send") {
        openEntry("Chat:", "", 30, "any", (text) => {
          if (text && text.trim()) {
            if (ws && ws.readyState === WebSocket.OPEN) {
              ws.send(JSON.stringify({ type: "chat", text: text.trim() }));
            }
            setTimeout(() => openChatWindow(), 100);
          } else {
            openChatWindow();
          }
        });
      }
    },
  });
}

// paintOverlay rebuilds the modal layer from scratch each frame, so a modal
// never has to restore what was underneath it. The pause layer goes underneath
// the modal: a paused player can still have a scroll open over the board.
function paintOverlay() {
  overlay.clear();
  if (mode === "playing") {
    if (currentChatMessage) {
      writeOverlay(0, 24, 0x1e, currentChatMessage.slice(0, 60).padEnd(60, " "));
    }
  }
  if (mode === "editor") {
    writeOverlay(editorCursor.x - 1, editorCursor.y - 1, 0x1f, "\x1f");
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
function openSelectList(
  title: string,
  entries: string[],
  onPick: (entry: string) => void,
  header: string[] = [],
) {
  if (entries.length === 0) {
    return;
  }
  const lines = [...header, ...entries.map((entry) => `!${entry};${entry}`)];
  openModal({
    kind: "text",
    // The cursor starts on the first entry, not on the prose above it.
    state: { title, lines, linePos: header.length + 1, viewingFile: false },
    baseTitle: title,
    moved: false,
    selectable: true,
    // Enter on a blurb line must not dismiss a picker the player has to answer.
    requireSelection: header.length > 0,
    onSelect: onPick,
  });
}

function openEntry(
  label: string,
  suffix: string,
  width: number,
  charset: "any" | "alphanum",
  onSubmit: (text: string | null) => void,
  buffer = "",
) {
  openModal({ kind: "entry", label, suffix, width, buffer, charset, onSubmit });
}

function openPopupEntry(
  question: string,
  onSubmit: (text: string | null) => void,
  y?: number,
) {
  openModal({ kind: "popupEntry", question, buffer: "", onSubmit, y });
}

// openYesNo is SidebarPromptYesNo.
function openYesNo(message: string, onAnswer: (yes: boolean) => void) {
  openModal({ kind: "yesno", message, onAnswer });
}

// Scrolls arrive as events and must never overwrite one another: a second
// scroll opening on top of the first is text the player never got to read.
// They queue instead, and the server freezes the reader until each is answered.
type PendingScroll = { title: string; lines: string[]; statId: number };
let scrollQueue: PendingScroll[] = [];
// The object stat of the scroll currently on screen, or -1. Its reply, sent on
// dismiss, is what unfreezes this player server-side.
let openScrollStatId = -1;

function enqueueScroll(scroll: PendingScroll) {
  if (scroll.lines.length === 0) {
    return;
  }
  if (openScrollStatId >= 0) {
    scrollQueue.push(scroll);
    return;
  }
  showScroll(scroll);
}

function showScroll(scroll: PendingScroll) {
  openScrollStatId = scroll.statId;
  openWindow(scroll.title, scroll.lines, false, scroll.statId);
}

function clearScrolls() {
  scrollQueue = [];
  openScrollStatId = -1;
}

function closeModal() {
  const scrollStatId = openScrollStatId;
  openScrollStatId = -1;
  modal = null;

  if (scrollStatId >= 0) {
    // An empty label is "dismissed, no hyperlink". The engine ignores it; the
    // RoomManager reads it as this player having finished reading, and lets
    // them move again. A hyperlink pick already sent its own reply.
    sendScrollReply(scrollStatId, "");
    const next = scrollQueue.shift();
    if (next) {
      showScroll(next);
      return;
    }
  }

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
        enqueueScroll({
          title: event.title ?? "Interaction",
          lines: event.lines ?? [],
          statId: event.statId ?? -1,
        });
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
    case "dream":
      openDreamPrompt();
      break;
    case "editor":
      startEditor();
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

  if (mode === "editor") {
    handleEditorKey(event);
    return;
  }

  if (event.code === "KeyC") {
    event.preventDefault();
    stopHeldInput();
    openChatWindow();
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
  if (modal || mode === "title" || mode === "editor") {
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

  if (mode === "editor" && !modal) {
    const cell = eventCell(event);
    if (!cell || cell.x >= BOARD_COLS) return;
    event.preventDefault();
    editorCursor = { x: cell.x + 1, y: cell.y + 1 };
    editorPointerDrawing = true;
    sendEditorEdit("place");
    return;
  }
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

function handlePointerMove(event: MouseEvent) {
  if (!editorPointerDrawing || mode !== "editor" || modal || event.buttons === 0) {
    editorPointerDrawing = false;
    return;
  }
  const cell = eventCell(event);
  if (!cell || cell.x >= BOARD_COLS) return;
  const next = { x: cell.x + 1, y: cell.y + 1 };
  if (next.x === editorCursor.x && next.y === editorCursor.y) return;
  editorCursor = next;
  sendEditorEdit("place");
}

function handleEditorKey(event: KeyboardEvent) {
  let nextX = editorCursor.x;
  let nextY = editorCursor.y;
  switch (event.code) {
    case "Escape":
    case "KeyQ":
      event.preventDefault();
      leaveEditor();
      return;
    case "ArrowUp":
    case "Numpad8":
      nextY -= 1;
      break;
    case "ArrowDown":
    case "Numpad2":
      nextY += 1;
      break;
    case "ArrowLeft":
    case "Numpad4":
      nextX -= 1;
      break;
    case "ArrowRight":
    case "Numpad6":
      nextX += 1;
      break;
    case "Space":
      event.preventDefault();
      sendEditorEdit("place");
      return;
    case "Delete":
    case "Backspace":
      event.preventDefault();
      sendEditorEdit("erase");
      return;
    case "KeyX":
      event.preventDefault();
      sendEditorEdit("fill");
      return;
    case "KeyI":
      event.preventDefault();
      openEditorBoardInfo();
      return;
    case "Enter":
      event.preventDefault();
      if (editorInspect.hasStat) {
        openEditorStatSettings(editorInspect);
        return;
      }
      editorBrush = {
        element: editorInspect.elementId,
        character: editorInspect.character,
        color: editorInspect.color,
        copied: true,
      };
      drawEditorSidebar(writeText, editorInspect, editorBrush, editorDrawing);
      paintOverlay();
      drawScreen();
      return;
    case "KeyP": {
      event.preventDefault();
      const patterns = [21, 22, 23, 0, 31];
      const index = editorBrush.copied ? patterns.length - 1 : patterns.indexOf(editorBrush.element);
      const next = patterns[(index + 1) % patterns.length];
      const chars = [0xdb, 0xb2, 0xb1, 0x20, 0xce];
      editorBrush = { element: next, character: chars[patterns.indexOf(next)], color: editorBrush.color, copied: false };
      drawEditorSidebar(writeText, editorInspect, editorBrush, editorDrawing);
      paintOverlay();
      drawScreen();
      return;
    }
    case "KeyC":
      event.preventDefault();
      editorBrush = {
        ...editorBrush,
        color: (editorBrush.color & 0x0f) === 15 ? 9 : (editorBrush.color & 0x0f) + 1,
      };
      drawEditorSidebar(writeText, editorInspect, editorBrush, editorDrawing);
      paintOverlay();
      drawScreen();
      return;
    case "Tab":
      event.preventDefault();
      editorDrawing = !editorDrawing;
      drawEditorSidebar(writeText, editorInspect, editorBrush, editorDrawing);
      paintOverlay();
      drawScreen();
      return;
    default:
      return;
  }
  event.preventDefault();
  editorCursor = {
    x: Math.max(1, Math.min(BOARD_COLS, nextX)),
    y: Math.max(1, Math.min(ROWS, nextY)),
  };
  // Keep the local cursor responsive; the authoritative tile readout follows
  // in the editorInspect reply.
  editorInspect = { ...editorInspect, x: editorCursor.x, y: editorCursor.y };
  drawEditorSidebar(writeText, editorInspect, editorBrush, editorDrawing);
  paintOverlay();
  drawScreen();
  sendEditorInspect();
  if (editorDrawing) sendEditorEdit("place");
}

function editorBool(value: boolean): string {
  return value ? "Yes" : "No";
}

function editorBoardName(id: number): string {
  return editorProperties.boards.find((board) => board.id === id)?.name ?? "None";
}

// Board Information is EditorEditBoardInfo on top of the M4.1 text-window
// layer. Every selection turns into one authoritative editorProperty operation;
// the session, not this browser, validates ranges and persists BoardClose.
function openEditorBoardInfo() {
  const p = editorProperties;
  const choices = [
    { action: "boardTitle", label: `Title: ${p.boardName || "Untitled"}` },
    { action: "maxShots", label: `Can fire: ${p.maxShots} shots.` },
    { action: "dark", label: `Board is dark: ${editorBool(p.isDark)}` },
    { action: "exit0", label: `Board \u0018: ${editorBoardName(p.neighborBoards[0] ?? 0)}` },
    { action: "exit1", label: `Board \u0019: ${editorBoardName(p.neighborBoards[1] ?? 0)}` },
    { action: "exit2", label: `Board \u001b: ${editorBoardName(p.neighborBoards[2] ?? 0)}` },
    { action: "exit3", label: `Board \u001a: ${editorBoardName(p.neighborBoards[3] ?? 0)}` },
    { action: "reenter", label: `Re-enter when zapped: ${editorBool(p.reenterWhenZapped)}` },
    { action: "timeLimit", label: `Time limit, 0=None: ${p.timeLimitSec} sec.` },
    { action: "worldName", label: `World name: ${p.worldName || "Untitled"}` },
  ];
  const actions = new Map(choices.map((choice) => [choice.label, choice.action]));
  openSelectList("Board Information", [...choices.map((choice) => choice.label), "Quit!"], (choice) => {
    const action = actions.get(choice);
    switch (action) {
      case "boardTitle":
        openEntry("New title for board:", "", 20, "any", (text) => {
          if (text !== null) sendEditorProperty("boardTitle", { text });
        }, p.boardName);
        break;
      case "worldName":
        openEntry("World name:", "", 20, "any", (text) => {
          if (text !== null) sendEditorProperty("worldName", { text });
        }, p.worldName);
        break;
      case "maxShots":
        openEditorNumber("Maximum shots?", p.maxShots, 255, "maxShots");
        break;
      case "dark":
        sendEditorProperty("dark", { bool: !p.isDark });
        break;
      case "reenter":
        sendEditorProperty("reenter", { bool: !p.reenterWhenZapped });
        break;
      case "timeLimit":
        openEditorNumber("Time limit?", p.timeLimitSec, 32767, "timeLimit");
        break;
      case "exit0":
      case "exit1":
      case "exit2":
      case "exit3":
        openEditorExitPicker(Number(action.slice(-1)));
        break;
    }
  });
}

function openEditorNumber(label: string, current: number, maximum: number, field: "maxShots" | "timeLimit") {
  openEntry(label, field === "timeLimit" ? " Sec" : "", String(maximum).length, "any", (text) => {
    if (text === null || !/^\d+$/.test(text)) return;
    const value = Number(text);
    if (value <= maximum) sendEditorProperty(field, { value });
  }, String(current));
}

function openEditorExitPicker(exit: number) {
  const entries = editorProperties.boards.map((board) => `${board.id}: ${board.name}`);
  openSelectList(`Select Board ${"\u0018\u0019\u001b\u001a"[exit] ?? ""}`, entries, (entry) => {
    const target = Number(entry.slice(0, entry.indexOf(":")));
    if (Number.isInteger(target)) sendEditorProperty("exit", { exit, value: target });
  });
}

function statDirection(stepX: number, stepY: number): number {
  if (stepY === -1) return 0;
  if (stepY === 1) return 1;
  if (stepX === -1) return 2;
  return 3;
}

function openEditorStatSlider(title: string, field: "p1" | "p2") {
  const values = Array.from({ length: 9 }, (_, index) => String(index));
  openSelectList(title, values, (entry) => sendEditorStat(field, Number(entry)));
}

// EditorEditStatSettings follows ElementDefs rather than a browser-maintained
// element table. This keeps the UI in step with the engine's Param*Name
// meanings and makes the server the authority for every stat mutation.
function openEditorStatSettings(inspect: EditorInspect) {
  if (!inspect.hasStat || inspect.statId === undefined) return;
  const choices: { action: string; label: string }[] = [];
  const p1 = inspect.p1 ?? 0;
  const p2 = inspect.p2 ?? 0;
  if (inspect.param1Name) {
    const value = inspect.paramTextName ? String.fromCharCode(p1) : String(p1);
    choices.push({ action: "p1", label: `${inspect.param1Name} ${value}` });
  }
  if (inspect.paramTextName) {
    choices.push({ action: "program", label: inspect.paramTextName });
  }
  if (inspect.param2Name) choices.push({ action: "p2", label: `${inspect.param2Name} ${p2 & 0x7f}` });
  if (inspect.paramBulletTypeName) choices.push({ action: "bulletType", label: `${inspect.paramBulletTypeName} ${(p2 & 0x80) === 0 ? "Bullets" : "Stars"}` });
  if (inspect.paramDirName) {
    const direction = ["North", "South", "West", "East"][statDirection(inspect.stepX ?? 0, inspect.stepY ?? 0)] ?? "East";
    choices.push({ action: "direction", label: `${inspect.paramDirName} ${direction}` });
  }
  if (inspect.paramBoardName) choices.push({ action: "p3", label: `${inspect.paramBoardName} ${editorBoardName(inspect.p3 ?? 0)}` });
  choices.push({ action: "cycle", label: `Cycle: ${inspect.cycle ?? 0}` });
  const actions = new Map(choices.map((choice) => [choice.label, choice.action]));
  const statId = inspect.statId;
  openSelectList(`${inspect.element} settings`, [...choices.map((choice) => choice.label), "Quit!"], (entry) => {
    const action = actions.get(entry);
    switch (action) {
      case "program":
        if (statId !== undefined) sendEditorProgram(statId);
        break;
      case "p1":
        if (inspect.paramTextName) {
          openEntry(inspect.param1Name ?? "Character?", "", 1, "any", (text) => {
            if (text) sendEditorStat("p1", text.charCodeAt(0) & 0xff);
          }, String.fromCharCode(p1));
        } else {
          openEditorStatSlider(inspect.param1Name ?? "Parameter", "p1");
        }
        break;
      case "p2":
        openEditorStatSlider(inspect.param2Name ?? "Parameter", "p2");
        break;
      case "bulletType":
        openSelectList(inspect.paramBulletTypeName ?? "Firing type?", ["Bullets", "Stars"], (value) => sendEditorStat("bulletType", value === "Stars" ? 1 : 0));
        break;
      case "direction":
        openSelectList(inspect.paramDirName ?? "Direction", ["North", "South", "West", "East"], (value) => sendEditorStat("direction", ["North", "South", "West", "East"].indexOf(value)));
        break;
      case "p3":
        openEditorStatBoardPicker(inspect.paramBoardName ?? "Board");
        break;
      case "cycle":
        openEditorStatCycle(inspect.cycle ?? 0);
        break;
    }
  });
}

function openEditorStatBoardPicker(title: string) {
  const entries = editorProperties.boards.map((board) => `${board.id}: ${board.name}`);
  openSelectList(title, entries, (entry) => {
    const value = Number(entry.slice(0, entry.indexOf(":")));
    if (Number.isInteger(value)) sendEditorStat("p3", value);
  });
}

function openEditorStatCycle(current: number) {
  openEntry("Cycle:", "", 5, "any", (text) => {
    if (text === null || !/^\d+$/.test(text)) return;
    const value = Number(text);
    if (value <= 32767) sendEditorStat("cycle", value);
  }, String(current));
}

function sendEditorStat(field: string, value: number) {
  if (!connected || !ws || ws.readyState !== WebSocket.OPEN || !editorInspect.hasStat || editorInspect.statId === undefined) return;
  ws.send(JSON.stringify({ type: MessageTypeEditorStat, statId: editorInspect.statId, field, value }));
}

// sendEditorProgram asks the server for a stat's program; the reply opens the
// editor. Save travels back through sendEditorProgramSave.
function sendEditorProgram(statId: number) {
  if (!connected || !ws || ws.readyState !== WebSocket.OPEN) return;
  ws.send(JSON.stringify({ type: MessageTypeEditorProgram, statId }));
}

function sendEditorProgramSave(statId: number, lines: string[]) {
  if (!connected || !ws || ws.readyState !== WebSocket.OPEN) return;
  ws.send(JSON.stringify({ type: MessageTypeEditorProgramSave, statId, lines }));
}

function sendEditorProperty(field: string, values: { text?: string; value?: number; bool?: boolean; exit?: number }) {
  if (!connected || !ws || ws.readyState !== WebSocket.OPEN) return;
  ws.send(JSON.stringify({ type: MessageTypeEditorProperty, field, ...values }));
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

// editorInspect is a read request, not an input or a persisted cursor. The
// browser owns editorCursor; the session only returns the tile's current data.
function sendEditorInspect() {
  if (!connected || !ws || ws.readyState !== WebSocket.OPEN || mode !== "editor") {
    return;
  }
  ws.send(JSON.stringify({
    type: MessageTypeEditorInspect,
    x: editorCursor.x,
    y: editorCursor.y,
  }));
}

function sendEditorEdit(op: "place" | "erase" | "fill") {
  if (!connected || !ws || ws.readyState !== WebSocket.OPEN || mode !== "editor") {
    return;
  }
  ws.send(JSON.stringify({
    type: MessageTypeEditorEdit,
    op,
    x: editorCursor.x,
    y: editorCursor.y,
    element: editorBrush.element,
    color: editorBrush.color,
    copied: editorBrush.copied,
  }));
}
