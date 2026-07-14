import "./style.css";
import { drawSidebar as paintSidebar, updateSidebar as paintSidebarHud } from "./sidebar";
import { renderModal, handleModalKey, POPUP_Y_CENTERED, type Modal, type WorldSearchEntry } from "./modal";
import { openHelp } from "./help";
import { commandKey, isHandledKey, isMovementKey, movementMask, rawKey } from "./keys";
import { drawTitleSidebar, titleCommand } from "./title";
import { soundNotesFromProtocol, ZztSound } from "./sound";
import { generationLines, runDreamGeneration, type GenerationProgress } from "./dream";
import { drawEditorSidebar, type EditorInspect, type SidebarActionMenu, type SidebarStatPrompt } from "./editor";
import { editorReplyMatchesCursor } from "./editor_cursor";
import { optimisticEditorEraseCell, optimisticEditorTextCell } from "./editor_input";
import {
  mergeWorldEntries,
  museumNetworkFailureLines,
  museumPlayFailureLines,
  museumResultsToEntries,
  type MuseumPlayResponse,
  type MuseumSearchResult,
} from "./museum";
import {
  buildJoinMessage,
  clearResumeToken,
  loadResumeToken,
  reconnectDelay,
  saveResumeToken,
} from "./resume";
import {
  boardCellIndices,
  cellSource,
  createTransition,
  shuffle,
  transitionSteps,
  TRANSITION_FILL_CH,
  TRANSITION_FILL_COLOR,
  type TransitionState,
} from "./transition";
import { selectWorldForTitle } from "./title_flow";

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
const MessageTypeEditorPresence = "editorPresence";
const MessageTypeEditorLease = "editorLease";
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
const MessageTypeEditorBoard = "editorBoard";
const MessageTypeEditorBoardData = "editorBoardData";
const MessageTypeEditorWorld = "editorWorld";
const MessageTypeEditorWorldData = "editorWorldData";
const MessageTypeEditorSaveResult = "editorSaveResult";
const MessageTypeEditorTestPlay = "editorTestPlay";
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
  resumeToken?: string;
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

type AuthStatus = {
  enabled: boolean;
  authenticated: boolean;
  id?: string;
  name?: string;
  email?: string;
};

// EditorElementItem / EditorElementMenu are the F1/F2/F3 category tables the
// server derives from ElementDefs (M5.8). They ride the entry snapshot once.
type EditorElementItem = {
  elementId: number;
  name: string;
  shortcut: string;
  character: number;
  color: number;
  categoryName?: string;
};

type EditorElementMenu = {
  category: number;
  key: string;
  title: string;
  items: EditorElementItem[];
};

type EditorSidebarMenuItem = {
  label: string;
  value?: string;
  shortcut?: string;
  onPick: () => void;
};

type EditorSidebarMenu = {
  title: string;
  items: EditorSidebarMenuItem[];
  selected: number;
  hint?: string;
  releaseLeaseOnClose?: boolean;
};

type EditorStatPromptItem =
  | { kind: "slider"; field: "p1" | "p2"; label: string; value: number; startChar?: string; endChar?: string }
  | { kind: "character"; field: "p1"; label: string; value: number }
  | { kind: "choice"; field: "bulletType" | "direction"; label: string; choices: string[]; selected: number; values: number[] }
  | { kind: "board"; label: string; value: number }
  | { kind: "program"; statId: number };

type EditorStatPrompt = {
  categoryName: string;
  elementName: string;
  items: EditorStatPromptItem[];
  active: number;
};

type EditorPresence = {
  id: string;
  name: string;
  color: number;
  x: number;
  y: number;
};

type EditorSnapshotMessage = {
	type: typeof MessageTypeEditorSnapshot;
	memberId?: string;
	readOnly?: boolean;
	boardId: number;
	screen: ScreenCell[];
	inspect: EditorInspect;
	properties: EditorProperties;
	menus?: EditorElementMenu[];
	presence?: EditorPresence[];
};

type EditorInspectMessage = {
  type: typeof MessageTypeEditorInspect;
  inspect: EditorInspect;
};

type EditorPresenceMessage = {
  type: typeof MessageTypeEditorPresence;
  members: EditorPresence[];
};

type EditorLeaseKind = "board" | "stat";

type EditorLeaseMessage = {
  type: typeof MessageTypeEditorLease;
  op: "request" | "release" | "granted" | "refused";
  kind: EditorLeaseKind;
  boardId?: number;
  statId?: number;
  holderId?: string;
  holderName?: string;
  error?: string;
};

type EditorDiffMessage = {
  type: typeof MessageTypeEditorDiff;
  memberId?: string;
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

// OopLabel / OopWarning are the M5.7 authoring aids the server computes with the
// real ZZT-OOP tokenizer: the object's :labels and advisory diagnostics.
type OopLabel = { name: string; line: number };
type OopWarning = { line: number; message: string };

type EditorProgramTextMessage = {
  type: typeof MessageTypeEditorProgramText;
  statId: number;
  prompt: string;
  lines: string[];
  labels?: OopLabel[];
  warnings?: OopWarning[];
};

// EditorBoardDataMessage carries an exported board as base64 .BRD bytes; the
// client turns it into a browser download (EditorTransferBoard's export half).
type EditorBoardDataMessage = {
  type: typeof MessageTypeEditorBoardData;
  name: string;
  data: string;
};

// EditorWorldDataMessage carries a downloaded world as base64 .ZZT bytes; the
// client turns it into a browser download the creator owns as a portable file.
type EditorWorldDataMessage = {
  type: typeof MessageTypeEditorWorldData;
  name: string;
  data: string;
};

// EditorSaveResultMessage reports the outcome of publishing or a refused upload.
type EditorSaveResultMessage = {
  type: typeof MessageTypeEditorSaveResult;
  world?: string;
  error?: string;
};

type EditorTestPlayMessage = {
  type: typeof MessageTypeEditorTestPlay;
  world?: string;
  error?: string;
};

type ServerMessage = SnapshotMessage | DiffMessage | EventMessage | BoardChangeMessage | ChatMessage | EditorSnapshotMessage | EditorInspectMessage | EditorPresenceMessage | EditorLeaseMessage | EditorDiffMessage | EditorPropertiesMessage | EditorStatSettingsMessage | EditorProgramTextMessage | EditorBoardDataMessage | EditorWorldDataMessage | EditorSaveResultMessage | EditorTestPlayMessage;

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
// reconnectAttempt drives the capped backoff (M13.2). It resets to zero the
// moment a connection is established, so a brief blip retries fast and only a
// prolonged outage backs off.
let reconnectAttempt = 0;
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
let titleFriendlyName = "Untitled";
let nickname = "browser";
let authStatus: AuthStatus = { enabled: false, authenticated: false };
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
// F4 text-entry mode (M5.8): while on, printable keys paint text tiles and the
// cursor advances, exactly as EditorLoop's TextEntry draw mode does.
let editorTextMode = false;
// Tracks whether the session world has unedited changes since the last save, so
// leaving the editor can offer EditorAskSaveChanged's "Save first?" prompt.
let editorModified = false;
// Set when a save was requested as part of leaving; the saveResult handler then
// completes the exit (or, on error, keeps the editor open).
let editorExitAfterSave = false;
let editorMemberId = "";
let editorPresence: EditorPresence[] = [];
let editorReadOnly = false;
let pendingEditorLease: { lease: EditorLeaseMessage; onGranted: () => void } | null = null;
let activeEditorLease: EditorLeaseMessage | null = null;
let retainEditorLeaseOnClose = false;
// The F1/F2/F3 element category tables, delivered once on the entry snapshot.
let editorMenus: EditorElementMenu[] = [];
// The category currently open on the sidebar (F1/F2/F3), or null. While set, the
// next keystroke selects an element by its shortcut instead of driving the board
// (EDITOR.PAS:808-842).
let editorCategoryMenu: EditorElementMenu | null = null;
let editorSidebarMenu: EditorSidebarMenu | null = null;
let editorStatPrompt: EditorStatPrompt | null = null;
// Set when a menu selection placed a stat-backed element, so the diff reply
// carrying the new stat can open its editor — EditorLoop calls EditorEditStat
// right after AddStat (EDITOR.PAS:766-772).
let editorStatEditAfterPlace = false;
const overlay = new Map<number, { ch: number; color: number }>();
const cells: ScreenCell[] = Array.from({ length: COLS * ROWS }, (_, i) => ({
  x: i % COLS,
  y: Math.floor(i / COLS),
  ch: 32,
  color: 0x1f,
}));

// M9.1 board-change fade. While `boardTransition` is set, drawScreen renders the
// viewport through it: the outgoing board (`transitionOld`) dissolves into purple
// blocks, then the incoming board (the live `cells`) is revealed in the same
// order. The incoming board is applied to `cells` up front, so diffs arriving
// mid-fade land normally and the final frame is always the true board.
let boardTransition: TransitionState | null = null;
let transitionOld = new Map<number, { ch: number; color: number }>();
let transitionRaf = 0;
// Fill + reveal together, ~vanilla's brief dissolve.
const TRANSITION_DURATION_MS = 420;

let hasPromptedNameOnLaunch = false;
const LAUNCH_NAME_PROMPT = "Traveler!  Type your name:";
const WORLD_SEARCH_TITLE = "Choose a World";

// The launch sequence: name, then world, then play. Nothing joins a room until
// a world is chosen, so the animated title board keeps running underneath.
function promptNicknameOnLaunch() {
  if (hasPromptedNameOnLaunch) {
    return;
  }
  hasPromptedNameOnLaunch = true;
  openPopupEntry(
    LAUNCH_NAME_PROMPT,
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
  editorSidebarMenu = null;
  editorStatPrompt = null;
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
    titleFriendlyName = friendlyName;
    replaceCells(title.screen);
    streaming = true;
  } catch {
    // Offline: keep whatever board is on screen and still draw the menu, so
    // the player can retry with 'P'.
  }
  drawTitleSidebar(writeText, friendlyName, authDisplayName(), authStatus.enabled);
  paintOverlay();
  drawScreen();
  canvas.focus();
  if (streaming) {
    openTitleStream(worldName);
  }
  void refreshAuthStatus();
}

async function refreshAuthStatus() {
  try {
    const response = await fetch("/api/auth/me");
    authStatus = (await response.json()) as AuthStatus;
  } catch {
    authStatus = { enabled: false, authenticated: false };
  }
  if (mode === "title") {
    drawTitleSidebar(writeText, titleFriendlyName, authDisplayName(), authStatus.enabled);
    paintOverlay();
    drawScreen();
  }
}

function authDisplayName(): string {
  if (!authStatus.authenticated) {
    return "";
  }
  return authStatus.name || authStatus.email || "";
}

// leaveToTitle ends this player's game: the room already dropped them, so all
// that remains is to close the socket without tripping the reconnect.
function leaveToTitle() {
  leavingToTitle = true;
  window.clearInterval(inputTimer);
  window.clearTimeout(retryTimer);
  connected = false;
  reconnectAttempt = 0;
  // An intentional exit ends the run: forget its resume token so pressing Play
  // again starts a fresh player rather than reclaiming a room we chose to leave.
  clearResumeToken(window.sessionStorage, worldName);
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
  reconnectAttempt = 0;
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
  editorTextMode = false;
  editorModified = false;
  editorExitAfterSave = false;
  editorMemberId = "";
  editorPresence = [];
  editorReadOnly = false;
  pendingEditorLease = null;
  activeEditorLease = null;
  retainEditorLeaseOnClose = false;
  editorCategoryMenu = null;
  editorSidebarMenu = null;
  editorStatPrompt = null;
  renderEditorSidebar();
  paintOverlay();
  drawScreen();
  connectEditor();
}

// leaveEditor is EditorAskSaveChanged on the way out (EDITOR.PAS:801-810): an
// edited world offers "Save first?" before exiting. Answering yes runs the world
// save flow and defers the exit until the save succeeds; no exits immediately.
function leaveEditor() {
  if (editorModified) {
    openYesNo("Save first? ", (yes) => {
      if (!yes) {
        closeEditor();
        return;
      }
      openEntry("Save world as:", "", 8, "any", (name) => {
        // An escaped/blank name cancels the exit, keeping the editor open, as
        // vanilla does when the save prompt is escaped.
        if (name) {
          editorExitAfterSave = true;
          sendEditorWorld({ op: "save", name });
        }
      }, editorProperties.worldName);
    });
    return;
  }
  closeEditor();
}

function closeEditor() {
  leavingToTitle = true;
  connected = false;
  editorExitAfterSave = false;
  window.clearTimeout(retryTimer);
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({ type: MessageTypeEditorExit }));
    ws.close();
  }
  ws = null;
  void showTitle();
}

// fetchLines backs the title screen's plain read-only windows (High Scores and
// the world/restore lists). A failure shows the reason rather than nothing at
// all. Help files (About, editor H) go through showHelp instead, which resolves
// their cross-file "!-FILE" links.
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

// showHelp opens a .HLP file as a navigable help window (M5.12): "!-FILE" links
// (e.g. EDITOR.HLP's Creatures/Terrains/ZZT-OOP) load that file with a back path,
// and bare "!label" links jump within the file. fetchHelpLines throws on a 404 so
// the help module can show a "not available" window instead of a dead link.
async function fetchHelpLines(file: string): Promise<string[]> {
  const response = await fetch("/api/help?file=" + encodeURIComponent(file) + "&title=" + encodeURIComponent(file));
  if (!response.ok) {
    throw new Error(String(response.status));
  }
  const data = (await response.json()) as { lines?: string[] };
  return data.lines ?? [];
}

function showHelp(file: string, title: string) {
  openHelp(file, title, { fetchLines: fetchHelpLines, openModal });
}

// LOBBY_WORLD is the world everyone lands in by default and nobody has to
// finish: the hangout. Every other world is a game you bring people to.
const LOBBY_WORLD = "TOWN";

async function showWorlds() {
  let worlds: WorldSearchEntry[] = [];
  try {
    const response = await fetch("/api/worlds");
    const data = (await response.json()) as { worlds?: (WorldSearchEntry | string)[] };
    worlds = normalizeWorldEntries(data.worlds ?? []);
  } catch {
    openWindow("ZZT Worlds", ["", "  Not available: the server did not answer.", ""], true);
    return;
  }

  if (worlds.length === 0) {
    openWindow("ZZT Worlds", ["", "  There are no ZZT worlds.", ""], true);
    return;
  }

  openModal({
    kind: "worldSearch",
    title: WORLD_SEARCH_TITLE,
    query: "",
    selected: 0,
    entries: worlds,
    onSelect: (entry) => void selectWorldEntry(entry),
    onQuery: (query) => scheduleMuseumSearch(query, worlds),
  });
}

let museumSearchTimer = 0;
let museumSearchSeq = 0;

function scheduleMuseumSearch(query: string, localEntries: WorldSearchEntry[]) {
  window.clearTimeout(museumSearchTimer);
  const trimmed = query.trim();
  if (trimmed.length < 2) {
    if (modal && modal.kind === "worldSearch") {
      modal.entries = localEntries;
    }
    return;
  }
  const seq = ++museumSearchSeq;
  museumSearchTimer = window.setTimeout(() => {
    void updateMuseumSearch(trimmed, localEntries, seq);
  }, 350);
}

async function updateMuseumSearch(query: string, localEntries: WorldSearchEntry[], seq: number) {
  try {
    const response = await fetch("/api/museum/search?q=" + encodeURIComponent(query));
    if (!response.ok) {
      return;
    }
    const data = (await response.json()) as { results?: MuseumSearchResult[] };
    if (seq !== museumSearchSeq || !modal || modal.kind !== "worldSearch") {
      return;
    }
    modal.entries = mergeWorldEntries(localEntries, museumResultsToEntries(data.results ?? []));
    modal.selected = 0;
    paintOverlay();
    drawScreen();
  } catch {
    // Local filtering still works; Museum search is opportunistic.
  }
}

function selectWorldEntry(entry: WorldSearchEntry) {
  if (entry.source === "museum") {
    void playMuseumWorld(entry);
    return;
  }
  void enterWorld(entry.world);
}

async function playMuseumWorld(entry: WorldSearchEntry, zztFile = "") {
  if (!entry.letter || !entry.filename) {
    openWindow("Museum of ZZT", ["", "  Missing Museum download information.", ""], true);
    return;
  }
  try {
    const response = await fetch("/api/museum/play", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ letter: entry.letter, filename: entry.filename, zztFile }),
    });
    if (!response.ok) {
      const reason = (await response.text()).trim() || `error ${response.status}`;
      openWindow("Museum of ZZT", museumPlayFailureLines(reason), true);
      return;
    }
    const result = (await response.json()) as MuseumPlayResponse;
    if (result.choices && result.choices.length > 0) {
      openSelectList("Choose World", result.choices.map((choice) => choice.name), (choice) => {
        void playMuseumWorld(entry, choice);
      });
      return;
    }
    if (result.world) {
      await enterWorld(result.world);
    }
  } catch {
    openWindow("Museum of ZZT", museumNetworkFailureLines(), true);
  }
}

function normalizeWorldEntries(entries: (WorldSearchEntry | string)[]): WorldSearchEntry[] {
  return entries.map((entry) => {
    if (typeof entry !== "string") {
      return {
        world: entry.world,
        id: entry.id || entry.world,
        title: entry.world === LOBBY_WORLD && entry.title === entry.world ? `${entry.title} (ZZTMMO Lobby)` : entry.title || entry.world,
        author: entry.author || "Unknown",
        created: entry.created || "",
        players: entry.players || 0,
        source: "local",
      };
    }
    const world = entry.split(" (")[0];
    return {
      world,
      id: world,
      title: world === LOBBY_WORLD ? `${world} (ZZTMMO Lobby)` : world,
      author: "Unknown",
      created: "",
      players: 0,
      source: "local",
    };
  });
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
  const lines = generationLines(progress);
  // Poll updates arrive every 500ms. Re-opening the window each time reset
  // linePos to 1, snapping the scroll back to the top so later lines could not
  // be read. If the progress window is already open, update its lines in place
  // and auto-follow the newest line instead.
  if (modal && modal.kind === "text" && modal.baseTitle === "Dreaming a world" && lines.length > 0) {
    modal.state.lines = lines;
    modal.state.linePos = lines.length;
    paintOverlay();
    drawScreen();
    return;
  }
  openWindow("Dreaming a world", lines, true);
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

// enterWorld selects a world's title board and stops there. Pressing P is the
// only thing that joins the chosen world's own instance. It does NOT call
// /api/loadworld: that swaps the server's single default world for everybody
// and is refused while anyone is playing. Each world already has its own
// RoomManager server-side (WebSocketServer.GetOrCreateInstance), reached by the
// ?world= parameter wsURL sends — which is what makes worlds independent.
async function enterWorld(name: string) {
  const selection = selectWorldForTitle(name);
  worldName = selection.worldName;
  await showTitle();
  if (selection.startPlay) {
    startPlay();
  }
}

// The old loadWorld POSTed /api/loadworld, swapping the server's single default
// world for everybody — which the server rightly refused while anyone was in a
// room. enterWorld replaces it: worlds are title-screen selections, so picking
// one is a local choice and needs no server-wide reload or automatic join. The
// endpoint remains for other callers.

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
    reconnectAttempt = 0;
    // No board: the server picks its configured default (zzt-server -board). A
    // stored resume token reclaims a dropped run instead of spawning a new
    // player (M13.2); an unknown/expired token is treated as a fresh join.
    const token = loadResumeToken(window.sessionStorage, worldName);
    socket.send(JSON.stringify(buildJoinMessage(MessageTypeJoin, nickname, token)));
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
  // Capped backoff (M13.2): a Wi-Fi blip reconnects quickly and presents the
  // stored resume token; a prolonged outage backs off toward the cap.
  const delay = reconnectDelay(reconnectAttempt);
  reconnectAttempt += 1;
  retryTimer = window.setTimeout(connect, delay);
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
      startBoardTransition(message.snapshot);
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
    case MessageTypeEditorPresence:
      applyEditorPresence(message);
      break;
    case MessageTypeEditorLease:
      applyEditorLease(message);
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
	case MessageTypeEditorBoardData:
	  applyEditorBoardData(message);
	  break;
	case MessageTypeEditorWorldData:
	  applyEditorWorldData(message);
	  break;
	case MessageTypeEditorSaveResult:
	  applyEditorSaveResult(message);
	  break;
	case MessageTypeEditorTestPlay:
	  applyEditorTestPlay(message);
	  break;
  }
}

function applyEditorSnapshot(message: EditorSnapshotMessage) {
  mode = "editor";
  if (message.memberId) editorMemberId = message.memberId;
  editorReadOnly = !!message.readOnly;
  if (message.presence) editorPresence = message.presence;
  editorInspect = message.inspect;
  editorCursor = { x: message.inspect.x, y: message.inspect.y };
	  editorProperties = message.properties;
  // Menus arrive only on the entry snapshot; board add/switch reuse this
  // message without them, so keep the tables already held.
  if (message.menus) editorMenus = message.menus;
  replaceCells(message.screen);
  renderEditorSidebar();
  paintOverlay();
  drawScreen();
}

function applyEditorInspect(message: EditorInspectMessage) {
  if (!editorReplyMatchesCursor(editorCursor, message.inspect)) {
    return;
  }
  editorInspect = message.inspect;
  renderEditorSidebar();
  paintOverlay();
  drawScreen();
}

function applyEditorPresence(message: EditorPresenceMessage) {
  editorPresence = message.members;
  paintOverlay();
  drawScreen();
}

function editorLeaseMatches(a: EditorLeaseMessage | null, b: EditorLeaseMessage): boolean {
  return !!a && a.kind === b.kind && (a.boardId ?? 0) === (b.boardId ?? 0) && (a.statId ?? -1) === (b.statId ?? -1);
}

function requestEditorLease(lease: Omit<EditorLeaseMessage, "type" | "op">, onGranted: () => void) {
  if (!connected || !ws || ws.readyState !== WebSocket.OPEN) return;
  if (editorReadOnly) {
    showEditorReadOnly();
    return;
  }
  const request: EditorLeaseMessage = { type: MessageTypeEditorLease, op: "request", ...lease };
  if (editorLeaseMatches(activeEditorLease, request)) {
    onGranted();
    return;
  }
  releaseActiveEditorLease();
  pendingEditorLease = { lease: request, onGranted };
  ws.send(JSON.stringify(request));
}

function releaseActiveEditorLease() {
  const lease = activeEditorLease;
  activeEditorLease = null;
  retainEditorLeaseOnClose = false;
  if (!lease || !connected || !ws || ws.readyState !== WebSocket.OPEN) return;
  ws.send(JSON.stringify({ ...lease, type: MessageTypeEditorLease, op: "release" }));
}

function applyEditorLease(message: EditorLeaseMessage) {
  if (message.op === "refused") {
    pendingEditorLease = null;
    if (message.error === "read-only") {
      showEditorReadOnly();
      return;
    }
    const name = message.holderName || "Another editor";
    openSelectList("Already editing", ["Ok"], () => {}, [`${name} is editing this ${message.kind}.`]);
    return;
  }
  if (message.op !== "granted" || !pendingEditorLease || !editorLeaseMatches(message, pendingEditorLease.lease)) {
    return;
  }
  const onGranted = pendingEditorLease.onGranted;
  pendingEditorLease = null;
  activeEditorLease = message;
  onGranted();
}

function showEditorReadOnly() {
  openSelectList("Read-only", ["Ok"], () => {}, ["You can look around, but this world is read-only for this account."]);
}

function applyEditorDiff(message: EditorDiffMessage) {
  for (const cell of message.cells) setBoardCell(cell);
  if (!message.memberId || message.memberId === editorMemberId) {
    const inspectIsCurrent = editorReplyMatchesCursor(editorCursor, message.inspect);
    if (inspectIsCurrent) {
      editorInspect = message.inspect;
      renderEditorSidebar();
    }
    // A category-menu placement of a stat-backed element opens its editor now
    // that the diff carries the new stat, mirroring EditorEditStat after AddStat.
    if (editorStatEditAfterPlace) {
      editorStatEditAfterPlace = false;
      if (message.inspect.hasStat && message.inspect.statId !== undefined) {
        const inspect = message.inspect;
        requestEditorLease({ kind: "stat", boardId: editorProperties.boardId, statId: inspect.statId }, () => openEditorStatSettings(inspect));
      }
    }
  }
  paintOverlay();
  drawScreen();
}

function applyEditorProperties(message: EditorPropertiesMessage) {
  editorProperties = message.properties;
  replaceCells(message.screen);
  renderEditorSidebar();
  paintOverlay();
  drawScreen();
}

function applyEditorStatSettings(message: EditorStatSettingsMessage) {
  for (const cell of message.cells) setBoardCell(cell);
  editorInspect = message.inspect;
  editorCursor = { x: message.inspect.x, y: message.inspect.y };
  renderEditorSidebar();
  paintOverlay();
  drawScreen();
}

// applyEditorProgramText opens the M5.4 code editor once the server returns the
// requested object/scroll program. Saving is Escape (EditorEditStatText has no
// cancel), which sends the edited lines back as editorProgramSave.
function applyEditorProgramText(message: EditorProgramTextMessage) {
  const statId = message.statId;
  retainEditorLeaseOnClose = false;
  openModal({
    kind: "programEditor",
    title: message.prompt || "Edit Program",
    lines: message.lines.length > 0 ? [...message.lines] : [""],
    linePos: 1,
    charPos: 1,
    insertMode: true,
    labels: message.labels ?? [],
    warnings: message.warnings ?? [],
    onSubmit: (lines) => sendEditorProgramSave(statId, lines),
  });
}

function applySnapshot(message: SnapshotMessage) {
  mode = "playing";
  // The join/resume snapshot carries our resume token; keep it so a later drop
  // can reclaim this run (M13.2).
  if (message.resumeToken) {
    saveResumeToken(window.sessionStorage, worldName, message.resumeToken);
  }
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

// startBoardTransition plays the M9.1 fill-then-reveal fade over a board change.
// The outgoing board is captured first; the incoming snapshot is then applied
// authoritatively (so mid-fade diffs land on `cells` and are never lost), and
// the animation only changes what drawScreen paints, never the model.
function startBoardTransition(snapshot: SnapshotMessage) {
  const old = new Map<number, { ch: number; color: number }>();
  for (let i = 0; i < cells.length; i += 1) {
    if (cells[i].x < BOARD_COLS) {
      old.set(i, { ch: cells[i].ch, color: cells[i].color });
    }
  }
  // Local shuffle — presentation only, so Math.random is fine (CLAUDE.md rule 2
  // governs the simulation, not the client).
  const order = shuffle(boardCellIndices(COLS, BOARD_COLS, ROWS), Math.random);
  boardTransition = createTransition(order);
  transitionOld = old;
  // Apply the new board before the timer starts. applySnapshot's own drawScreen
  // now renders through boardTransition at step 0 (all "old"), so the incoming
  // board never flashes before the fade begins.
  applySnapshot(snapshot);
  startTransitionTimer();
}

function endBoardTransition() {
  if (transitionRaf) {
    window.cancelAnimationFrame(transitionRaf);
    transitionRaf = 0;
  }
  boardTransition = null;
  transitionOld = new Map();
}

function startTransitionTimer() {
  if (transitionRaf) {
    window.cancelAnimationFrame(transitionRaf);
    transitionRaf = 0;
  }
  const state = boardTransition;
  if (!state) {
    return;
  }
  const total2 = transitionSteps(state.total);
  const start = performance.now();
  const tick = (now: number) => {
    transitionRaf = 0;
    if (boardTransition !== state) {
      return; // a newer transition superseded this one
    }
    if (mode !== "playing") {
      endBoardTransition(); // left the room mid-fade (quit / editor)
      return;
    }
    const elapsed = now - start;
    state.step = Math.min(total2, Math.floor((elapsed / TRANSITION_DURATION_MS) * total2));
    if (state.step >= total2) {
      endBoardTransition();
      drawScreen();
      return;
    }
    drawScreen();
    transitionRaf = window.requestAnimationFrame(tick);
  };
  transitionRaf = window.requestAnimationFrame(tick);
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
    let ch: number;
    let color: number;
    if (over) {
      ch = over.ch;
      color = over.color;
    } else if (boardTransition && mode === "playing" && base.x < BOARD_COLS) {
      const src = cellSource(boardTransition.orderPos.get(i), boardTransition.step, boardTransition.total);
      if (src === "purple") {
        ch = TRANSITION_FILL_CH;
        color = TRANSITION_FILL_COLOR;
      } else if (src === "old") {
        const o = transitionOld.get(i);
        ch = o ? o.ch : base.ch;
        color = o ? o.color : base.color;
      } else {
        ch = base.ch;
        color = base.color;
      }
    } else {
      ch = base.ch;
      color = base.color;
    }
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
    for (const member of editorPresence) {
      if (member.id === editorMemberId) continue;
      const x = member.x - 1;
      const y = member.y - 1;
      if (x < 0 || x >= BOARD_COLS || y < 0 || y >= ROWS) continue;
      writeOverlay(x, y, member.color, "\x1f");
      writeOverlay(x + 1, y, member.color, member.name.slice(0, 10));
    }
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
  if (activeEditorLease) {
    if (retainEditorLeaseOnClose) {
      retainEditorLeaseOnClose = false;
    } else {
      releaseActiveEditorLease();
    }
  }

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
    case "login":
      if (authStatus.authenticated) {
        void fetch("/api/auth/logout", { method: "POST" }).then(() => refreshAuthStatus());
      } else if (authStatus.enabled) {
        window.location.href = "/api/auth/google/start?return=" + encodeURIComponent(window.location.pathname + window.location.search);
      }
      break;
    case "world":
      void showWorlds();
      break;
    case "about":
      showHelp("ABOUT.HLP", "About ZZT...");
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
    if (editorReadOnly) {
      showEditorReadOnly();
      return;
    }
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
  if (!editorPointerDrawing || mode !== "editor" || modal || editorReadOnly || event.buttons === 0) {
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

// renderEditorSidebar is the single sidebar-draw seam: every editor redraw path
// runs through it so an open F1/F2/F3 category picker survives async collaborator
// diffs/inspects that would otherwise repaint the plain command block over it.
function renderEditorSidebar() {
  const actionMenu: SidebarActionMenu | null = editorSidebarMenu ? {
    title: editorSidebarMenu.title,
    items: editorSidebarMenu.items.map((item) => ({
      label: item.label,
      value: item.value,
      shortcut: item.shortcut,
    })),
    selected: editorSidebarMenu.selected,
    hint: editorSidebarMenu.hint,
  } : null;
  const statPrompt: SidebarStatPrompt | null = editorStatPrompt ? {
    categoryName: editorStatPrompt.categoryName,
    elementName: editorStatPrompt.elementName,
    items: editorStatPrompt.items
      .filter((item) => item.kind !== "program")
      .map((item, visibleIndex) => {
        const promptIndex = visiblePromptIndex(editorStatPrompt!, visibleIndex);
        const active = promptIndex === editorStatPrompt!.active;
        switch (item.kind) {
          case "slider":
            return { kind: "slider", label: item.label, value: item.value, active, startChar: item.startChar, endChar: item.endChar };
          case "character":
            return { kind: "character", label: item.label, value: item.value, active };
          case "choice":
            return { kind: "choice", label: item.label, choices: item.choices, selected: item.selected, active };
          case "board":
            return { kind: "board", label: "Room", value: editorBoardName(item.value), active };
        }
      }),
  } : null;
  drawEditorSidebar(writeText, editorInspect, editorBrush, editorDrawing, editorTextMode, editorCategoryMenu, actionMenu, statPrompt);
}

// redrawEditor repaints the editor sidebar and board from local state, the
// common tail of every editor key that changes brush/mode/cursor.
function redrawEditor() {
  renderEditorSidebar();
  paintOverlay();
  drawScreen();
}

// handleEditorTextKey is F4 text-entry mode (EDITOR.PAS:459-475): a printable
// key paints a text tile at the cursor and advances right; Backspace erases the
// tile to the left and moves onto it; Enter or Escape leaves the mode. Returns
// true when it consumed the key. Anything else (arrows, F-keys) returns false so
// the normal handler still runs while text entry stays on.
function handleEditorTextKey(event: KeyboardEvent): boolean {
  if (editorReadOnly) {
    if (event.code === "Enter" || event.code === "Escape") {
      event.preventDefault();
      editorTextMode = false;
      redrawEditor();
      return true;
    }
    if (event.code === "Backspace" || event.code === "Delete" || event.key.length === 1) {
      event.preventDefault();
      showEditorReadOnly();
      return true;
    }
  }
  if (event.code === "Enter" || event.code === "Escape") {
    event.preventDefault();
    editorTextMode = false;
    redrawEditor();
    return true;
  }
  if (event.code === "Backspace" || event.code === "Delete") {
    event.preventDefault();
    if (editorCursor.x > 1) {
      const erased = optimisticEditorEraseCell(editorCursor);
      editorCursor = { x: editorCursor.x - 1, y: editorCursor.y };
      editorInspect = { ...editorInspect, x: editorCursor.x, y: editorCursor.y };
      sendEditorEdit("erase");
      if (erased) setBoardCell(erased);
      redrawEditor();
    }
    return true;
  }
  if (event.key.length === 1) {
    const code = event.key.charCodeAt(0);
    if (code >= 0x20 && code < 0x80) {
      event.preventDefault();
      const typed = optimisticEditorTextCell(editorCursor, code, editorBrush.color);
      sendEditorEdit("text", code);
      if (typed) setBoardCell(typed);
      if (editorCursor.x < BOARD_COLS) {
        editorCursor = { x: editorCursor.x + 1, y: editorCursor.y };
        editorInspect = { ...editorInspect, x: editorCursor.x, y: editorCursor.y };
      }
      redrawEditor();
      return true;
    }
  }
  return false;
}

function handleEditorKey(event: KeyboardEvent) {
  if (editorStatPrompt) {
    handleEditorStatPromptKey(event);
    return;
  }
  if (editorSidebarMenu) {
    handleEditorSidebarMenuKey(event);
    return;
  }
  if (editorCategoryMenu) {
    handleEditorCategoryKey(event);
    return;
  }
  if (editorTextMode && handleEditorTextKey(event)) return;
  let nextX = editorCursor.x;
  let nextY = editorCursor.y;
  switch (event.code) {
    case "Escape":
    case "KeyQ":
      event.preventDefault();
      leaveEditor();
      return;
    case "F4":
      event.preventDefault();
      editorTextMode = !editorTextMode;
      redrawEditor();
      return;
    case "KeyZ":
      event.preventDefault();
      requestEditorLease({ kind: "board", boardId: editorProperties.boardId }, () => {
        openYesNo("Clear board? ", (yes) => {
          if (yes) sendEditorBoard({ op: "clear" });
        });
      });
      return;
    case "KeyN":
      event.preventDefault();
      requestEditorLease({ kind: "board", boardId: editorProperties.boardId }, () => {
        openYesNo("Make new world? ", (yes) => {
          if (yes) sendEditorBoard({ op: "new" });
        });
      });
      return;
    case "KeyH":
      event.preventDefault();
      showHelp("EDITOR.HLP", "World editor help");
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
      if (editorReadOnly) {
        showEditorReadOnly();
        return;
      }
      sendEditorEdit("place");
      return;
    case "Delete":
    case "Backspace":
      event.preventDefault();
      if (editorReadOnly) {
        showEditorReadOnly();
        return;
      }
      sendEditorEdit("erase");
      return;
    case "KeyX":
      event.preventDefault();
      if (editorReadOnly) {
        showEditorReadOnly();
        return;
      }
      sendEditorEdit("fill");
      return;
    case "F1":
      event.preventDefault();
      openEditorCategoryMenu("f1");
      return;
    case "F2":
      event.preventDefault();
      openEditorCategoryMenu("f2");
      return;
    case "F3":
      event.preventDefault();
      openEditorCategoryMenu("f3");
      return;
    case "KeyI":
      event.preventDefault();
      requestEditorLease({ kind: "board", boardId: editorProperties.boardId }, openEditorBoardInfo);
      return;
    case "KeyB":
      event.preventDefault();
      openEditorBoardList();
      return;
    case "KeyT":
      event.preventDefault();
      openEditorTransfer();
      return;
    case "KeyS":
      event.preventDefault();
      openEditorWorldMenu();
      return;
    case "Enter":
      event.preventDefault();
      if (editorInspect.hasStat) {
        requestEditorLease({ kind: "stat", boardId: editorProperties.boardId, statId: editorInspect.statId }, () => openEditorStatSettings(editorInspect));
        return;
      }
      editorBrush = {
        element: editorInspect.elementId,
        character: editorInspect.character,
        color: editorInspect.color,
        copied: true,
      };
      renderEditorSidebar();
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
      renderEditorSidebar();
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
      renderEditorSidebar();
      paintOverlay();
      drawScreen();
      return;
    case "Tab":
      event.preventDefault();
      editorDrawing = !editorDrawing;
      renderEditorSidebar();
      paintOverlay();
      drawScreen();
      return;
    default:
      return;
  }
  event.preventDefault();
  // Shift+arrow paints the pattern along the path: EditorLoop places at the
  // current cursor before moving (EDITOR.PAS:397-411), so a Shift-drag lays a
  // line of the selected pattern. Placing happens at the old cursor position.
  if (event.shiftKey) {
    if (editorReadOnly) {
      showEditorReadOnly();
      return;
    }
    sendEditorEdit("place");
  }
  editorCursor = {
    x: Math.max(1, Math.min(BOARD_COLS, nextX)),
    y: Math.max(1, Math.min(ROWS, nextY)),
  };
  // Keep the local cursor responsive; the authoritative tile readout follows
  // in the editorInspect reply.
  editorInspect = { ...editorInspect, x: editorCursor.x, y: editorCursor.y };
  renderEditorSidebar();
  paintOverlay();
  drawScreen();
  sendEditorInspect();
  if (editorDrawing) {
    if (editorReadOnly) {
      showEditorReadOnly();
      return;
    }
    sendEditorEdit("place");
  }
}

function editorBool(value: boolean): string {
  return value ? "Yes" : "No";
}

function editorBoardName(id: number): string {
  return editorProperties.boards.find((board) => board.id === id)?.name ?? "None";
}

function openEditorSidebarMenu(menu: Omit<EditorSidebarMenu, "selected"> & { selected?: number }) {
  editorCategoryMenu = null;
  editorSidebarMenu = { ...menu, selected: menu.selected ?? 0 };
  redrawEditor();
}

function closeEditorSidebarMenu(releaseLease = true) {
  const releaseLeaseOnClose = releaseLease && !!editorSidebarMenu?.releaseLeaseOnClose;
  editorSidebarMenu = null;
  redrawEditor();
  if (releaseLeaseOnClose) {
    releaseActiveEditorLease();
  }
}

function pickEditorSidebarItem(menu: EditorSidebarMenu, item: EditorSidebarMenuItem) {
  item.onPick();
  if (editorSidebarMenu === menu) {
    closeEditorSidebarMenu();
  }
}

function handleEditorSidebarMenuKey(event: KeyboardEvent) {
  event.preventDefault();
  const menu = editorSidebarMenu;
  if (!menu) return;
  switch (event.code) {
    case "Escape":
      closeEditorSidebarMenu();
      return;
    case "ArrowUp":
    case "Numpad8":
      menu.selected = Math.max(0, menu.selected - 1);
      redrawEditor();
      return;
    case "ArrowDown":
    case "Numpad2":
      menu.selected = Math.min(menu.items.length - 1, menu.selected + 1);
      redrawEditor();
      return;
    case "Home":
      menu.selected = 0;
      redrawEditor();
      return;
    case "End":
      menu.selected = menu.items.length - 1;
      redrawEditor();
      return;
    case "Enter":
    case "Space":
      pickEditorSidebarItem(menu, menu.items[menu.selected]);
      return;
  }
  const pressed = event.key.length === 1 ? event.key.toUpperCase() : "";
  if (!pressed) return;
  const shortcutIndex = menu.items.findIndex((item) => (item.shortcut ?? item.label.slice(0, 1)).toUpperCase() === pressed);
  if (shortcutIndex >= 0) {
    menu.selected = shortcutIndex;
    pickEditorSidebarItem(menu, menu.items[shortcutIndex]);
  }
}

// openEditorCategoryMenu opens the F1/F2/F3 element picker on the sidebar
// (EDITOR.PAS:808-816). Unlike a modal, it leaves the board visible and arms
// handleEditorCategoryKey to read the next keystroke as an element shortcut.
function openEditorCategoryMenu(key: string) {
  const menu = editorMenus.find((candidate) => candidate.key.toLowerCase() === key.toLowerCase());
  if (!menu || menu.items.length === 0) return;
  editorCategoryMenu = menu;
  redrawEditor();
}

// handleEditorCategoryKey is the single-key wait after F1/F2/F3
// (EDITOR.PAS:843-887): a matching element shortcut places that element; Escape
// or any non-matching key just closes the picker (vanilla reads one key and the
// no-match loop simply falls through to EditorDrawSidebar).
function handleEditorCategoryKey(event: KeyboardEvent) {
  event.preventDefault();
  const menu = editorCategoryMenu;
  editorCategoryMenu = null;
  if (!menu || event.code === "Escape") {
    redrawEditor();
    return;
  }
  const pressed = event.key.length === 1 ? event.key.toUpperCase() : "";
  const item = pressed ? menu.items.find((candidate) => candidate.shortcut.toUpperCase() === pressed) : undefined;
  if (!item) {
    redrawEditor();
    return;
  }
  selectEditorMenuItem(item);
}

// selectEditorMenuItem places the chosen category element at the cursor. The
// server op "element" ports EditorLoop's AddStat/seed path (editorPlaceElement);
// for a stat-backed element the diff reply then triggers its stat editor, so the
// coordinate and stat parameters are authored the way vanilla's EditorEditStat
// does after AddStat.
function selectEditorMenuItem(item: EditorElementItem) {
  editorBrush = {
    element: item.elementId,
    character: item.character,
    color: item.color,
    copied: false,
  };
  redrawEditor();
  if (editorReadOnly) {
    showEditorReadOnly();
    return;
  }
  editorStatEditAfterPlace = true;
  sendEditorEdit("element");
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

function splitSliderPrompt(prompt: string): { label: string; startChar?: string; endChar?: string } {
  if (prompt.length >= 3 && prompt[prompt.length - 3] === ";") {
    return {
      label: prompt.slice(0, -3),
      startChar: prompt[prompt.length - 2],
      endChar: prompt[prompt.length - 1],
    };
  }
  return { label: prompt };
}

function editorStatCategoryName(inspect: EditorInspect): string {
  for (const menu of editorMenus) {
    let categoryName = "";
    for (const item of menu.items) {
      if (item.categoryName) categoryName = item.categoryName;
      if (item.elementId === inspect.elementId) return categoryName;
    }
  }
  return "";
}

function visiblePromptIndex(prompt: EditorStatPrompt, visibleIndex: number): number {
  let seen = -1;
  for (let i = 0; i < prompt.items.length; i += 1) {
    if (prompt.items[i].kind === "program") continue;
    seen += 1;
    if (seen === visibleIndex) return i;
  }
  return -1;
}

function closeEditorStatPrompt(releaseLease = true) {
  editorStatPrompt = null;
  redrawEditor();
  if (releaseLease) releaseActiveEditorLease();
}

function sendEditorStatPromptValue(item: EditorStatPromptItem) {
  switch (item.kind) {
    case "slider":
    case "character":
      sendEditorStat(item.field, item.value);
      return;
    case "choice":
      sendEditorStat(item.field, item.values[item.selected] ?? 0);
      return;
  }
}

function startActiveEditorStatPrompt() {
  const prompt = editorStatPrompt;
  if (!prompt) return;
  while (prompt.active < prompt.items.length) {
    const item = prompt.items[prompt.active];
    if (item.kind === "program") {
      closeEditorStatPrompt(false);
      sendEditorProgram(item.statId);
      return;
    }
    if (item.kind === "board") {
      closeEditorStatPrompt(false);
      openEditorStatBoardPicker(item.label);
      return;
    }
    redrawEditor();
    return;
  }
  closeEditorStatPrompt();
}

function advanceEditorStatPrompt() {
  const prompt = editorStatPrompt;
  if (!prompt) return;
  const item = prompt.items[prompt.active];
  if (item) sendEditorStatPromptValue(item);
  prompt.active += 1;
  startActiveEditorStatPrompt();
}

function updateEditorStatPromptValue(item: EditorStatPromptItem, next: number) {
  switch (item.kind) {
    case "slider": {
      const value = Math.max(0, Math.min(8, next));
      if (value !== item.value) {
        item.value = value;
        sendEditorStat(item.field, value);
        redrawEditor();
      }
      return;
    }
    case "character": {
      const value = (next + 0x100) % 0x100;
      if (value !== item.value) {
        item.value = value;
        sendEditorStat("p1", value);
        redrawEditor();
      }
      return;
    }
    case "choice": {
      const selected = Math.max(0, Math.min(item.choices.length - 1, next));
      if (selected !== item.selected) {
        item.selected = selected;
        sendEditorStat(item.field, item.values[selected] ?? 0);
        redrawEditor();
      }
      return;
    }
  }
}

function handleEditorStatPromptKey(event: KeyboardEvent) {
  event.preventDefault();
  const prompt = editorStatPrompt;
  if (!prompt) return;
  const item = prompt.items[prompt.active];
  if (!item) {
    closeEditorStatPrompt();
    return;
  }
  switch (event.code) {
    case "Escape":
      closeEditorStatPrompt();
      return;
    case "Enter":
      advanceEditorStatPrompt();
      return;
    case "Tab":
      if (item.kind === "character") updateEditorStatPromptValue(item, item.value + 9);
      return;
    case "ArrowLeft":
    case "Numpad4":
      if (item.kind === "slider") updateEditorStatPromptValue(item, item.value - 1);
      if (item.kind === "character") updateEditorStatPromptValue(item, item.value - 1);
      if (item.kind === "choice") updateEditorStatPromptValue(item, item.selected - 1);
      return;
    case "ArrowRight":
    case "Numpad6":
      if (item.kind === "slider") updateEditorStatPromptValue(item, item.value + 1);
      if (item.kind === "character") updateEditorStatPromptValue(item, item.value + 1);
      if (item.kind === "choice") updateEditorStatPromptValue(item, item.selected + 1);
      return;
  }
  if (item.kind === "slider" && event.key >= "1" && event.key <= "9") {
    updateEditorStatPromptValue(item, event.key.charCodeAt(0) - "1".charCodeAt(0));
  }
}

// EditorEditStatSettings is not a selectable "settings" menu. Vanilla clears
// the sidebar, writes the element category/name, draws every parameter prompt,
// then edits them sequentially in place.
function openEditorStatSettings(inspect: EditorInspect) {
  if (!inspect.hasStat || inspect.statId === undefined) return;
  const items: EditorStatPromptItem[] = [];
  const p1 = inspect.p1 ?? 0;
  const p2 = inspect.p2 ?? 0;
  if (inspect.param1Name) {
    const slider = splitSliderPrompt(inspect.param1Name);
    if (inspect.paramTextName) {
      items.push({ kind: "character", field: "p1", label: slider.label, value: p1 });
    } else {
      items.push({ kind: "slider", field: "p1", label: slider.label, value: p1, startChar: slider.startChar, endChar: slider.endChar });
    }
  }
  if (inspect.paramTextName) {
    items.push({ kind: "program", statId: inspect.statId });
  }
  if (inspect.param2Name) {
    const slider = splitSliderPrompt(inspect.param2Name);
    items.push({ kind: "slider", field: "p2", label: slider.label, value: p2 & 0x7f, startChar: slider.startChar, endChar: slider.endChar });
  }
  if (inspect.paramBulletTypeName) {
    items.push({
      kind: "choice",
      field: "bulletType",
      label: inspect.paramBulletTypeName,
      choices: ["Bullets", "Stars"],
      selected: (p2 & 0x80) === 0 ? 0 : 1,
      values: [0, 1],
    });
  }
  if (inspect.paramDirName) {
    items.push({
      kind: "choice",
      field: "direction",
      label: inspect.paramDirName,
      choices: ["\x18", "\x19", "\x1b", "\x1a"],
      selected: statDirection(inspect.stepX ?? 0, inspect.stepY ?? 0),
      values: [0, 1, 2, 3],
    });
  }
  if (inspect.paramBoardName) {
    items.push({ kind: "board", label: inspect.paramBoardName, value: inspect.p3 ?? 0 });
  }
  editorCategoryMenu = null;
  editorSidebarMenu = null;
  editorStatPrompt = {
    categoryName: editorStatCategoryName(inspect),
    elementName: inspect.element,
    items,
    active: 0,
  };
  startActiveEditorStatPrompt();
}

function openEditorStatBoardPicker(title: string) {
  const entries = editorProperties.boards.map((board) => `${board.id}: ${board.name}`);
  openSelectList(title, entries, (entry) => {
    const value = Number(entry.slice(0, entry.indexOf(":")));
    if (Number.isInteger(value)) sendEditorStat("p3", value);
  });
}

function sendEditorStat(field: string, value: number) {
  if (!connected || !ws || ws.readyState !== WebSocket.OPEN || !editorInspect.hasStat || editorInspect.statId === undefined) return;
  ws.send(JSON.stringify({ type: MessageTypeEditorStat, statId: editorInspect.statId, field, value }));
}

// sendEditorProgram asks the server for a stat's program; the reply opens the
// editor. Save travels back through sendEditorProgramSave.
function sendEditorProgram(statId: number) {
  if (!connected || !ws || ws.readyState !== WebSocket.OPEN) return;
  requestEditorLease({ kind: "stat", boardId: editorProperties.boardId, statId }, () => {
    retainEditorLeaseOnClose = true;
    ws?.send(JSON.stringify({ type: MessageTypeEditorProgram, statId }));
  });
}

function sendEditorProgramSave(statId: number, lines: string[]) {
  if (!connected || !ws || ws.readyState !== WebSocket.OPEN) return;
  ws.send(JSON.stringify({ type: MessageTypeEditorProgramSave, statId, lines }));
}

function sendEditorProperty(field: string, values: { text?: string; value?: number; bool?: boolean; exit?: number }) {
  if (!connected || !ws || ws.readyState !== WebSocket.OPEN) return;
  ws.send(JSON.stringify({ type: MessageTypeEditorProperty, field, ...values }));
}

function sendEditorBoard(values: { op: string; name?: string; boardId?: number; data?: string }) {
  if (!connected || !ws || ws.readyState !== WebSocket.OPEN) return;
  if (editorReadOnly && ["add", "import", "clear", "new"].includes(values.op)) {
    showEditorReadOnly();
    return;
  }
  ws.send(JSON.stringify({ type: MessageTypeEditorBoard, ...values }));
}

// openEditorBoardList is EditorSelectBoard on the 'B' key: switch among the
// world's boards or append a new one. The reply to either is a full editor
// snapshot, so applyEditorSnapshot repaints the new board.
function openEditorBoardList() {
  const entries = editorProperties.boards
    .filter((board) => board.id !== 0)
    .map((board) => `${board.id}: ${board.name}`);
  entries.push("Add new board");
  openSelectList("Switch boards", entries, (entry) => {
    if (entry === "Add new board") {
      openPopupEntry("Room's Title:", (text) => {
        if (text) sendEditorBoard({ op: "add", name: text });
      });
      return;
    }
    const boardId = Number.parseInt(entry, 10);
    if (Number.isInteger(boardId)) sendEditorBoard({ op: "switch", boardId });
  });
}

// openEditorTransfer is EditorTransferBoard: import a .BRD file into the current
// board, or export the current board as a .BRD download.
function openEditorTransfer() {
  openEditorSidebarMenu({
    title: "Transfer board:",
    items: [
      {
        label: "Import board",
        shortcut: "I",
        onPick: () => {
          closeEditorSidebarMenu(false);
          requestEditorLease({ kind: "board", boardId: editorProperties.boardId }, () => {
            retainEditorLeaseOnClose = true;
            importEditorBoardFile();
          });
        },
      },
      {
        label: "Export board",
        shortcut: "E",
        onPick: () => sendEditorBoard({ op: "export" }),
      },
    ],
    hint: "Enter/Esc",
  });
}

// importEditorBoardFile reads a local .BRD file and hands its bytes to the
// server as base64. The server validates and rejects a malformed board.
function importEditorBoardFile() {
  const input = document.createElement("input");
  input.type = "file";
  input.accept = ".brd,.BRD";
  let handled = false;
  input.addEventListener("change", () => {
    handled = true;
    const file = input.files?.[0];
    if (!file) {
      releaseActiveEditorLease();
      return;
    }
    file.arrayBuffer().then((buffer) => {
      const bytes = new Uint8Array(buffer);
      let binary = "";
      for (let i = 0; i < bytes.length; i += 1) binary += String.fromCharCode(bytes[i]);
      sendEditorBoard({ op: "import", data: btoa(binary) });
      releaseActiveEditorLease();
    });
  });
  window.addEventListener("focus", () => {
    window.setTimeout(() => {
      if (!handled) releaseActiveEditorLease();
    }, 1000);
  }, { once: true });
  input.click();
}

function sendEditorWorld(values: { op: string; name?: string; data?: string; accountId?: string }) {
  if (!connected || !ws || ws.readyState !== WebSocket.OPEN) return;
  if (editorReadOnly && values.op !== "download") {
    showEditorReadOnly();
    return;
  }
  ws.send(JSON.stringify({ type: MessageTypeEditorWorld, ...values }));
}

function sendEditorTestPlay() {
  if (!connected || !ws || ws.readyState !== WebSocket.OPEN) return;
  ws.send(JSON.stringify({ type: MessageTypeEditorTestPlay }));
}

// openEditorWorldMenu is the 'S' key: save/publish the world so others can play
// it, download it as a portable .ZZT, or upload a .ZZT to edit.
function openEditorWorldMenu() {
  const entries = ["Test play together", "Save and publish", "Download .ZZT", "Upload .ZZT"];
  if (!editorReadOnly) entries.push("Invite collaborator");
  openSelectList("World:", entries, (entry) => {
    if (entry === "Test play together") {
      sendEditorTestPlay();
    } else if (entry === "Save and publish") {
      openEntry("Save world as:", "", 8, "any", (text) => {
        if (text) sendEditorWorld({ op: "save", name: text });
      }, editorProperties.worldName);
    } else if (entry === "Download .ZZT") {
      sendEditorWorld({ op: "download" });
    } else if (entry === "Upload .ZZT") {
      uploadEditorWorldFile();
    } else if (entry === "Invite collaborator") {
      openEntry("Account id:", "", 30, "any", (text) => {
        if (text) sendEditorWorld({ op: "invite", accountId: text });
      });
    }
  });
}

// uploadEditorWorldFile reads a local .ZZT file and hands its bytes to the server
// as base64. The server validates it (headless load + 200 steps) and rejects a
// world that fails, leaving the session untouched.
function uploadEditorWorldFile() {
  const input = document.createElement("input");
  input.type = "file";
  input.accept = ".zzt,.ZZT";
  input.addEventListener("change", () => {
    const file = input.files?.[0];
    if (!file) return;
    file.arrayBuffer().then((buffer) => {
      const bytes = new Uint8Array(buffer);
      let binary = "";
      for (let i = 0; i < bytes.length; i += 1) binary += String.fromCharCode(bytes[i]);
      sendEditorWorld({ op: "upload", data: btoa(binary) });
    });
  });
  input.click();
}

// applyEditorWorldData turns a download reply into a browser download of vanilla
// .ZZT bytes, which reload in DOS ZZT/zeta and through this engine alike.
function applyEditorWorldData(message: EditorWorldDataMessage) {
  const binary = atob(message.data);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i);
  const blob = new Blob([bytes], { type: "application/octet-stream" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = `${message.name}.ZZT`;
  link.click();
  URL.revokeObjectURL(url);
}

// applyEditorSaveResult reports whether a publish (or upload) succeeded. A refused
// upload arrives here too, carrying the validation gate's message.
function applyEditorSaveResult(message: EditorSaveResultMessage) {
  if (message.error) {
    // A failed save-on-exit keeps the editor open so the work is not lost.
    editorExitAfterSave = false;
    openSelectList("Cannot save", ["Ok"], () => {}, [message.error]);
    return;
  }
  editorModified = false;
  if (editorExitAfterSave) {
    editorExitAfterSave = false;
    closeEditor();
    return;
  }
  openSelectList("Saved", ["Ok"], () => {}, [
    `World published as ${message.world}.`,
    "It now appears in the world picker.",
  ]);
}

function applyEditorTestPlay(message: EditorTestPlayMessage) {
  if (message.error || !message.world) {
    openSelectList("Cannot test", ["Ok"], () => {}, [message.error || "Could not start test play."]);
    return;
  }
  connected = false;
  leavingToTitle = true;
  window.clearTimeout(retryTimer);
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({ type: MessageTypeEditorExit }));
    ws.close();
  }
  ws = null;
  worldName = message.world;
  startPlay();
}

// applyEditorBoardData turns an export reply into a browser download of vanilla
// .BRD bytes, which reload in DOS ZZT/zeta and re-import here alike.
function applyEditorBoardData(message: EditorBoardDataMessage) {
  const binary = atob(message.data);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i);
  const blob = new Blob([bytes], { type: "application/octet-stream" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = `${message.name}.BRD`;
  link.click();
  URL.revokeObjectURL(url);
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

function sendEditorEdit(op: "place" | "erase" | "fill" | "element" | "text", char = 0) {
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
    char,
  }));
}
