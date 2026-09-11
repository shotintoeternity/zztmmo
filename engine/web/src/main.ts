// main.ts — the browser client's shell: the canvas, the socket, and the state
// machine that moves between title, play, watch, replay and editor.
//
// The client is a dumb terminal by design. It owns no game state and simulates
// nothing: it sends what the player did (a keymask, a command byte, a menu
// choice) and draws what the server says is there. Every rule about what
// happens next lives in the Go engine. When something looks wrong on screen the
// question is almost always what the server sent, not what this file decided.
//
// It is the largest file in the client, and the reason is the seam it sits on:
// this is where the DOM, the WebSocket and the render loop meet, and all three
// are awkward to test. So the parts that CAN be tested were moved out into the
// modules imported below — modal.ts, sidebar.ts, keys.ts, title.ts, sound.ts,
// museum.ts, resume.ts and the rest — each of which is pure enough to exercise
// under node, and each of which has a matching suite in ../test. What is left
// here is the impure remainder plus the wiring between them. That split is why
// the module list is long, and it is the direction to keep pushing: a new
// behavior belongs in a module with a test, not in another function here.
//
// Layout constants at the top are ZZT's, not choices: an 80x25 text screen, a
// 60-column board with a 20-column sidebar, drawn with an 8x14 CP437 font.

import "./style.css";
import { drawSidebar as paintSidebar, drawWatchSidebar as paintWatchSidebar, updateSidebar as paintSidebarHud,
  sidebarClearLine,
} from "./sidebar";
import {
  applyWorldOccupancy,
  renderModal,
  handleModalKey,
  handleModalTextInput,
  worldOccupancyTotal,
  POPUP_Y_CENTERED,
  type Modal,
  type ModalTextInput,
  type WorldSearchEntry,
  type WorldShelf,
} from "./modal";
import { MobileTextInputBridge } from "./mobile_text_input";
import { createTouchControls, type TouchControls } from "./touch_controls";
import { openHelp } from "./help";
import { commandKey, isHandledKey, isMovementKey, movementMask, rawKey } from "./keys";
import { drawTitleSidebar, titleCommand, NO_OCCUPANCY, TITLE_COLOR_SWATCH, type ServerOccupancy } from "./title";
import { colorPickerPreview, newColorPickerModal } from "./color_picker";
import { soundNotesFromProtocol, ZztSound } from "./sound";
import {
  DreamFailure,
  generationLines,
  retryDreamBoard,
  runDreamGeneration,
  salvagedBoards,
  type DreamResult,
  type GenerationProgress,
} from "./dream";
import { drawEditorSidebar, editorMessageIsForBoard, type EditorInspect, type SidebarActionMenu, type SidebarPresenceList, type SidebarStatPrompt } from "./editor";
import { editorReplyMatchesCursor, editorCursorOverlay, editorPresenceLegend, EDITOR_BLINK_PHASES } from "./editor_cursor";
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
  buildEditorEnterMessage,
  buildJoinMessage,
  buildWatchMessage,
  clearEditorToken,
  clearPlayerColor,
  clearResumeToken,
  loadEditorToken,
  loadPlayerColor,
  loadResumeToken,
  reconnectDelay,
  saveEditorToken,
  savePlayerColor,
  saveResumeToken,
} from "./resume";
import { playerTintCells, playerTintForeground } from "./player_tint";
import {
  effectivePlayerColor,
  fetchAccountPreferences,
  saveAccountHint,
  saveAccountColor,
  saveFavoriteWorld,
  saveAccountProfile,
  saveAccountComfort,
  saveShareLocationWithFollowers,
  type AccountHintKey,
  type AccountProfilePreferences,
  type AccountPreferences,
  effectiveComfort,
  EMPTY_ACCOUNT_PROFILE,
} from "./preferences";
import {
  DEFAULT_COMFORT,
  effectiveKeyBindings,
  loadGuestComfort,
  normalizeComfortPreferences,
  paletteColor,
  reducedBlinkOn,
  saveGuestComfort,
  validateComfortPreferences,
  type ComfortPreferences,
  type KeyAction,
} from "./comfort";
import { FIRST_TIME_HINTS, hintAlreadySeen, loadGuestHints, saveGuestHint } from "./first_time_hints";
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
import { blockCandidates, blockRowLabel, blockWindowHeader, mergeServerBlocks } from "./blocks";
import { moderationChoices, moderationHeader } from "./moderation";
import {
  challengeLinkID,
  challengePath,
  deepLinkPath,
  deepLinkRefusalLines,
  deepLinkWorldName,
  isChallengePath,
  replayLinkID,
  replayLinkPath,
  resolveDeepLinkWorld,
  isWatchLivePath,
  watchLinkPath,
  watchLinkWorldName,
} from "./deep_link";
import {
  challengeLandingLines,
  challengeLeaderboardLines,
  challengeResultLines,
  challengeRowActionLines,
  ghostOverlayCell,
  ghostStatusLine,
  ghostTrackMatches,
  postcardURLForRun,
  type ChallengeErrorMessage,
  type ChallengeGhostTrack,
  type ChallengeLeaderboardRow,
  type ChallengeResponse,
  type ChallengeResultMessage,
} from "./challenge";
import { WATCH_LIVE_CYCLE_MS, nextWatchLiveIndex, watchLiveEmbedMode, watchLiveEntryLabel, watchLiveEntryTarget, type WatchLiveLineupEntry } from "./watch_live";

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
const MessageTypeAnnounce = "announce";
// M21.1: "stop showing me this player" — sent by the roster window, answered
// only to the sender. The suppression itself happens on the server, at the chat
// fan-out; nothing here filters a line, because a client-side filter is
// bypassable and would still deliver the text to this machine.
const MessageTypeBlock = "block";
const MessageTypeBlockResult = "blockResult";
// M21.2: an operator acting on a person for everybody — mute, kick, refuse.
// Where a block is answered only to the sender and never mentioned to its
// target, a sanction is announced to the person it lands on: that is what
// moderationNotice carries, and a notice that ends the session is what stops the
// reconnect backoff from quietly undoing a kick half a second later.
const MessageTypeModerate = "moderate";
const MessageTypeModerateResult = "moderateResult";
const MessageTypeModerationNotice = "moderationNotice";
const MessageTypeProfileRequest = "profileRequest";
const MessageTypeProfileResult = "profileResult";
const MessageTypePrivateMessage = "privateMessage";
const MessageTypePrivateResult = "privateMessageResult";
const MessageTypeFollow = "follow";
const MessageTypeFollowResult = "followResult";
const MessageTypeReplayControl = "replayControl";
const MessageTypeReplayError = "replayError";
const MessageTypeChallengeResult = "challengeResult";
const MessageTypeChallengeError = "challengeError";

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
// SoundHasTimeElapsed(TickTimeCounter, 15) in EDITOR.PAS's cursor blink: 15
// hundredths of a second per cursorBlinker phase.
const EDITOR_BLINK_MS = 150;
const COMMAND_SOUND = "B".charCodeAt(0);

type ScreenCell = {
  x: number;
  y: number;
  ch: number;
  color: number;
  /**
   * The element the server says is SHOWING here (gamevars.go's E_* numbers), or
   * 0 when the board is holding it back. The text screen never needs it -- a
   * glyph is a glyph -- but the 3D view cannot tell a fake wall from a wall
   * without it, because ElementDefs draws the fake with the normal wall's own
   * character, and cannot tell water from a shaded wall either.
   *
   * The wire tag is `element,omitempty`, so a square going from fake back to
   * empty arrives with no field at all. Every write below therefore defaults it
   * to 0 rather than leaving whatever was there: an absent field means zero,
   * never "unchanged".
   */
  element?: number;
};

type PlayerSnapshot = {
  id: number;
  statId: number;
  x: number;
  y: number;
  health: number;
  /** Both M19.1, both `omitempty` on the wire: absent means the vanilla player. */
  name?: string;
  color?: string;
  handle?: string;
  hasProfile?: boolean;
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
  /** Raw tone frequency on a "walkClick" event — bypasses SoundQueue entirely. */
  freqHz?: number;
};

type SnapshotMessage = {
  type: typeof MessageTypeSnapshot;
  world?: string;
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
  /**
   * Which of `players` this client has already blocked (M21.4), on the
   * join/resume snapshot only. Absent from an older server and from a
   * board-change snapshot, which is why it is merged rather than assigned.
   */
  blockedPlayers?: number[];
  followedPlayers?: number[];
  /**
   * Whether this connection's account is on the server's moderator allowlist
   * (M21.2). It only decides what the Players window OFFERS: the server checks
   * the allowlist again on every action, so setting it here buys nothing.
   */
  operator?: boolean;
  /**
   * The challenge this frame belongs to and the run's server-minted key
   * (M32.1). The key is not a world name and cannot be derived by the client,
   * so it is stored and presented on a reconnect to reclaim THIS attempt.
   */
  challenge?: string;
  challengeRun?: string;
  /**
   * This frame is addressed to a WATCHER, not a player (M22.1). It carries no
   * `you`, no HUD and no events; it is what puts this client into read-only
   * mode. Said by the server rather than inferred here, because the one time a
   * guess would be wrong is a malformed frame — and the wrong guess is a
   * browser sampling input into a room it is not in.
   */
  spectator?: boolean;
  /** How many people are watching this board, counting us (M22.1). */
  watchers?: number;
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
  /**
   * How many people are watching this board (M22.1). It rides every frame, so
   * an absent value means nobody rather than "unchanged".
   */
  watchers?: number;
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
  /**
   * The sender's PlayerID (M21.1), absent from an older server's lines and from
   * anything the server says on its own behalf. It is what makes a chat line
   * addressable — `from` is a display name, and display names are neither unique
   * nor claimed.
   */
  playerId?: number;
  text: string;
};

type PrivateMessage = {
  type: typeof MessageTypePrivateMessage;
  from?: string;
  fromId?: number;
  to?: string;
  toId?: number;
  playerId?: number;
  text: string;
  outgoing?: boolean;
};

type PrivateResultMessage = {
  type: typeof MessageTypePrivateResult;
  playerId?: number;
  delivered: boolean;
  text: string;
};

type BlockResultMessage = {
  type: typeof MessageTypeBlockResult;
  playerId: number;
  name?: string;
  blocked: boolean;
  durable: boolean;
  text: string;
};

type FollowResultMessage = {
  type: typeof MessageTypeFollowResult;
  playerId?: number;
  name?: string;
  followed: boolean;
  text: string;
};

type AnnounceMessage = {
  type: typeof MessageTypeAnnounce;
  text: string;
  seconds?: number;
};

type ModerateResultMessage = {
  type: typeof MessageTypeModerateResult;
  action: string;
  playerId: number;
  name?: string;
  applied: boolean;
  durable: boolean;
  text: string;
};

// What a moderated PLAYER is told. `ended` marks the sanctions that finish this
// session — a kick, a refusal, and a refused account's rejected reconnect — and
// the client has to honour it, or the reconnect backoff walks the player it just
// removed straight back into the room.
type ModerationNoticeMessage = {
  type: typeof MessageTypeModerationNotice;
  action: string;
  text: string;
  ended?: boolean;
};

type ProfileResultMessage = {
  type: typeof MessageTypeProfileResult;
  playerId: number;
  name?: string;
  handle?: string;
  lines: string[];
};

type ReplayErrorMessage = {
  type: typeof MessageTypeReplayError;
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
  // M17.12: which board this member is editing; cursors are only drawn for
  // members on the board the viewer is looking at.
  boardId?: number;
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
	// The membership token, on the entry snapshot only (M16.14f).
	resumeToken?: string;
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
  // The board these cells belong to (M17.12). Members edit different boards of
  // one world, so a diff for another board must not paint over ours.
  boardId?: number;
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

// screen is absent for the members of the session looking at another board
// (M16.14a): the properties still reach them for their board list and the world
// name, but the frame belongs to the board the change was made on.
type EditorPropertiesMessage = {
  type: typeof MessageTypeEditorProperties;
  properties: EditorProperties;
  screen?: ScreenCell[];
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

type ServerMessage = SnapshotMessage | DiffMessage | EventMessage | BoardChangeMessage | ChatMessage | PrivateMessage | PrivateResultMessage | AnnounceMessage | BlockResultMessage | FollowResultMessage | ModerateResultMessage | ModerationNoticeMessage | ProfileResultMessage | ReplayErrorMessage | EditorSnapshotMessage | EditorInspectMessage | EditorPresenceMessage | EditorLeaseMessage | EditorDiffMessage | EditorPropertiesMessage | EditorStatSettingsMessage | EditorProgramTextMessage | EditorBoardDataMessage | EditorWorldDataMessage | EditorSaveResultMessage | EditorTestPlayMessage | ChallengeResultMessage | ChallengeErrorMessage;

type InputMessage = {
  type: typeof MessageTypeInput;
  playerId: number;
  seq: number;
  keymask?: number;
  key?: number;
};

// Zeta's 8x14 EGA font (fonts/pc_ega.png upstream): 256 glyphs as 32 columns by
// 8 rows, CP437 order, so glyph N sits at (N%32, N/32). Character codes go to
// the sheet directly — there is no Unicode round trip.
import pcEgaUrl from "./pc_ega.png";
import { isCameraKey, lookStepFor } from "./view3d/input3d";

const GLYPH_COLS = 32;

const fontImg = new Image();
fontImg.src = pcEgaUrl;
let fontCanvases: HTMLCanvasElement[] = [];
let fontSourceCanvas: HTMLCanvasElement | null = null;
const fontCanvasCache = new Map<string, HTMLCanvasElement[]>();

function buildFontCanvases(palette: string): HTMLCanvasElement[] {
  if (!fontSourceCanvas) {
    return [];
  }
  const cached = fontCanvasCache.get(palette);
  if (cached) {
    return cached;
  }
  const canvases: HTMLCanvasElement[] = [];
  for (let i = 0; i < 16; i++) {
    const canvas = document.createElement("canvas");
    canvas.width = fontSourceCanvas.width;
    canvas.height = fontSourceCanvas.height;
    const ctx = canvas.getContext("2d");
    if (ctx) {
      ctx.imageSmoothingEnabled = false;
      ctx.drawImage(fontSourceCanvas, 0, 0);
      ctx.globalCompositeOperation = "source-in";
      ctx.fillStyle = paletteColor(palette as ComfortPreferences["palette"], i);
      ctx.fillRect(0, 0, canvas.width, canvas.height);
    }
    canvases.push(canvas);
  }
  fontCanvasCache.set(palette, canvases);
  return canvases;
}

function refreshFontPalette() {
  const comfort = readEffectiveComfort();
  fontCanvases = buildFontCanvases(comfort.palette);
}

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
  fontSourceCanvas = tempCanvas;
  fontCanvasCache.set("vanilla", buildFontCanvases("vanilla"));
  refreshFontPalette();
  drawScreen();
};

const app = document.querySelector<HTMLDivElement>("#app");
if (!app) {
  throw new Error("missing app root");
}

app.innerHTML = `
  <div class="canvas-wrap">
    <canvas data-view3d hidden></canvas>
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
const pressed = new Set<string>();
const zztSound = new ZztSound();
// Expose the synth for live diagnosis (M17.7): the M17.3 fix told the operator to
// confirm the AudioContext reads "running" in the console, but the object was
// module-scoped and unreachable. `zztSound.diagnostics()` now works in DevTools.
(window as unknown as { zztSound: ZztSound }).zztSound = zztSound;

// M4.3: the client is a two-state machine, as ZZT is. "title" is GameTitleLoop
// — no socket, no player, the monitor sidebar over a static board 0. "playing"
// is GamePlayLoop: joined to a room, streaming diffs. 'P' enters, quitting
// leaves.
// M22.1 adds "watching": a room this browser renders and does not play.
type Mode = "title" | "playing" | "editor" | "watching";
let mode: Mode = "title";
// `watching` is the INTENT and `mode` is the screen. They come apart on a
// reconnect: a dropped socket puts the screen on a notice, and the intent is
// what makes the retry send another spectate join instead of quietly walking
// into the room as a player.
let watching = false;
let replaying = false;
let watcherCount = 0;
let replayID = "";
let replayTick = 0;
let replayStartTick = 0;
let watchLive = false;
let watchLiveEntries: WatchLiveLineupEntry[] = [];
let watchLiveIndex = -1;
let watchLiveTimer = 0;
let watchLiveSwitching = false;
let watchLiveLabel = "";
let watchLiveEmbed = false;
// M32.1 challenge state. `challengeID` is the catalogue id this browser is
// playing or looking at, and `challengeRun` is the server-minted run key a
// reconnect presents — the key is not a world name, so it cannot be derived and
// must be remembered. `challengeGhost` is a LOCAL overlay track and
// `challengeElapsed` is how many ticks this attempt has drawn, which is what the
// ghost is indexed by.
let challengeMode = false;
let challengeID = "";
let challengeRun = "";
let challengeLanding: ChallengeResponse | null = null;
let challengeGhost: ChallengeGhostTrack | null = null;
let challengeElapsed = 0;
let challengeFinished = false;
// The board this browser is standing on, from the last frame. Only the ghost
// reads it: a track carries board ids, and a ghost from another board must not
// be drawn onto this one.
let playBoardId = 0;
// The on-screen control bar (M15.1, M16.18a), or null on anything without touch
// points. Declared here rather than at its construction site because
// syncTouchControls() below is reached from drawScreen(), which runs before that
// site — a `const` there would be in its temporal dead zone for the first frame.
let touchControls: TouchControls | null = null;
let worldName = "Untitled";
let titleFriendlyName = "Untitled";
let nickname = "browser";
// The live roster, from the join snapshot and every diff. It is what
// playerTintCells paints from, and it is per-board: the server only ever sends
// the players on the board this client is looking at.
let roster: PlayerSnapshot[] = [];

const EMPTY_CELL_SET: ReadonlySet<number> = new Set();

// A sign taller than this is a wall of text; the first lines are the ones that
// name the place. White on blue, ZZT's own colour for a thing being told to you.
const SIGN_READOUT_LINES = 3;
const SIGN_READOUT_FG = 0x0f;
const SIGN_READOUT_BG = 0x01;

// --- the 3D view (M35) ------------------------------------------------------
//
// A second painter for the board half of `cells`. It is loaded on demand: three
// .js is most of half a megabyte and most players never ask for it, so the
// module is behind an `await import()` and the everyday bundle does not carry
// it. Until somebody presses V there is no scene, no rig and no frame loop.
let view3d: import("./view3d").View3D | null = null;
let view3dLoading = false;

/** True when the world is what the board columns are showing. */
function view3dOn(): boolean {
  return view3d !== null && view3d.rig.mode === "world" && mode === "playing";
}



const view3dCanvas = query<HTMLCanvasElement>("[data-view3d]");

/**
 * syncView3DCanvas puts the GL canvas exactly over the board columns of the
 * text screen. The screen canvas is letterboxed inside the wrap, so its client
 * rect -- not the wrap's -- is what the board is measured from, and the board
 * is the first BOARD_COLS of COLS.
 */
function syncView3DCanvas() {
  if (!view3d) {
    return;
  }
  const screenRect = canvas.getBoundingClientRect();
  const wrapRect = canvas.parentElement?.getBoundingClientRect();
  if (!wrapRect || screenRect.width === 0) {
    return;
  }
  const width = screenRect.width * (BOARD_COLS / COLS);
  view3dCanvas.style.left = `${screenRect.left - wrapRect.left}px`;
  view3dCanvas.style.top = `${screenRect.top - wrapRect.top}px`;
  view3dCanvas.style.width = `${width}px`;
  view3dCanvas.style.height = `${screenRect.height}px`;
  view3d.resize(width, screenRect.height);
}

/**
 * toggleView3D is V (and 3): the text screen, or the board with depth.
 *
 * The first press pays for the module and the font texture, so it is async and
 * the view arrives a frame or two later; every press after that is immediate.
 * Held keys are dropped on the way through, because a mask assembled under one
 * view's rules must not be delivered under the other's.
 */
async function toggleView3D() {
  if (mode !== "playing" || view3dLoading) {
    return;
  }
  stopHeldInput();
  if (!view3d) {
    view3dLoading = true;
    try {
      const module = await import("./view3d");
      view3d = await module.createView3D(view3dCanvas);
      view3d.onTextChanged = () => drawScreen();
      // The text screen is sized entirely by CSS (aspect-ratio: 640/350), so
      // there is no resize handler in this client to hang off -- the 2D canvas
      // has never needed one. A GL drawing buffer does, so the view brings its
      // own observer, watching the canvas it must stay glued to.
      new ResizeObserver(() => syncView3DCanvas()).observe(canvas);
    } finally {
      view3dLoading = false;
    }
  }
  const next = view3d.rig.mode === "world" ? "classic" : "world";
  if (next === "world") {
    // Choosing 3D is choosing to stand in the board rather than look down at
    // it, so V arrives at eye level rather than at an orbit camera.
    view3d.rig.cycle();
    view3d.snap();
    feedView3D();
    view3d.start();
    syncView3DCanvas();
  } else {
    view3d.rig.setMode("classic");
    view3d.stop();
  }
  // The rows say where 3 goes next, so they change when it is pressed. Written
  // here rather than from drawScreen: they go through writeText into `cells`,
  // and a write on every repaint would tell feedView3D the board had changed
  // and rebuild the scene's geometry every frame.
  drawView3DRows();
  drawScreen();
}

/**
 * feedView3D hands the view the model it draws, and only when that model has
 * actually moved.
 *
 * The guard is not an optimisation. The view calls back into drawScreen when it
 * has lifted new words out of the board, and drawScreen feeds the view; feeding
 * unconditionally would mark the scene dirty on every callback and rebuild the
 * whole board's geometry every frame, forever. cellsRevision is what makes the
 * pair terminate: it moves when a cell is written, and a repaint writes none.
 */
let cellsRevision = 0;
let view3dFedRevision = -1;
let view3dFedPos = "";

function feedView3D() {
  if (!view3d) {
    return;
  }
  const pos = `${myX},${myY},${roster.length}`;
  if (cellsRevision === view3dFedRevision && pos === view3dFedPos) {
    return;
  }
  view3dFedRevision = cellsRevision;
  view3dFedPos = pos;
  view3d.update({
    cells,
    roster,
    me: myX > 0 ? { x: myX, y: myY } : null,
  });
}
// Screen-cell index -> the RGB behind a player's smiley there. Rebuilt whenever
// the roster or the screen changes; consulted by drawScreen after the overlay.
const playerTints = new Map<number, string>();
let authStatus: AuthStatus = { enabled: false, authenticated: false };
// The signed-in player's account-wide preferences (M19.3), or null for a guest,
// a signed-in player who has never chosen anything, and a read that failed.
// null is what sends readStoredPlayerColor back to localStorage.
let accountPrefs: AccountPreferences | null = null;
let accountPrefsLoaded = false;
// leavingToTitle suppresses the reconnect that a dropped socket normally
// triggers: a socket we closed on purpose must not come back.
let leavingToTitle = false;
// M17.11 occupancy: how many people are playing or editing, server-wide for the
// title screen and per-world for the picker. Server-observed presentation state
// — it never enters the simulation. See refreshOccupancy.
let serverOccupancy: ServerOccupancy = NO_OCCUPANCY;
let worldPickerEntries: WorldSearchEntry[] = [];
let worldPickerShelves: WorldShelf[] = [];
let occupancyTimer = 0;

// While a modal is up, gameplay keys are swallowed (M4.1: handleModalKey is the
// only consumer). The simulation does NOT pause behind it (M1.3 deviation), so
// the board keeps updating underneath and the modal is painted as an overlay
// rather than by saving/restoring cells.
let modal: Modal | null = null;
let bindingCaptureAction: KeyAction | "" = "";
const mobileTextInput = new MobileTextInputBridge(document);
// Per-player pause (M3.11): the server tells us via PauseEvent whether OUR stat
// is paused. The room keeps running for everyone else, so this is presentation
// only — we draw what vanilla's GamePlayLoop pause branch drew.
let paused = false;
let pauseBlink = false;
let pauseTimer = 0;
// EditorLoop's idle cursor blink (editor.go:534-551). editorBlink is the 3-phase
// cursorBlinker; the timer advances it every EDITOR_BLINK_MS so the cross cursor
// fades to reveal the tile/object underneath, matching the DOS editor cadence.
let editorBlink = 0;
let editorBlinkTimer = 0;
// The high-score chain is three modals deep: the list with "-- You! --", the
// name popup, then the finished list. Each opens as the previous one closes.
let pendingHighScore = false;
let returnToTitleOnClose = false;
// M20.1: set while the "Deep link" refusal window is up, so closing it drops the
// visitor into the world picker instead of leaving them at a title screen they
// did not ask for. Same shape as returnToTitleOnClose above.
let showWorldsOnClose = false;
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
// What the browser puts back around a reconnect's fresh snapshot (M16.14f), or
// null when no reconnect is in flight. The session owns the world; these are
// the things it does not own — the board this browser was looking at and where
// its cursor was — captured when the socket closed. The brush, text mode and
// draw mode need no capture: they are client-only and nothing resets them.
let editorRestore: { boardId: number; x: number; y: number } | null = null;
// The F1/F2/F3 element category tables, delivered once on the entry snapshot.
let editorMenus: EditorElementMenu[] = [];
// The category currently open on the sidebar (F1/F2/F3), or null. While set, the
// next keystroke selects an element by its shortcut instead of driving the board
// (EDITOR.PAS:808-842).
let editorCategoryMenu: EditorElementMenu | null = null;
// M17.10: the W collaborator legend. It overlays the same sidebar rows as the
// category picker but is not modal — the cursor keeps moving and editing while
// it is up, so you can watch a coloured cursor and read its name at once.
let editorPresencePanel = false;
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
  element: 0,
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
const LAUNCH_NAME_PROMPT = "Welcome to ZZTMMO! Type your name and press Enter.";
const WORLD_SEARCH_TITLE = "Choose a World";

// The launch sequence: name, then world, then play. Nothing joins a room until
// a world is chosen, so the animated title board keeps running underneath.
//
// M20.1: the name prompt still comes first on a deep link. /play/<world> changes
// which world the visitor lands on, not who the server thinks they are, and the
// OAuth return round-trips this same path — so a visitor who signs in from a
// deep-linked title screen comes back through here and lands there again.
function promptNicknameOnLaunch() {
  if (hasPromptedNameOnLaunch) {
    return;
  }
  if (isWatchLivePath(window.location.pathname)) {
    hasPromptedNameOnLaunch = true;
    void openLaunchDestination();
    return;
  }
  hasPromptedNameOnLaunch = true;
  openPopupEntry(
    LAUNCH_NAME_PROMPT,
    (name) => {
      nickname = name && name.trim() ? name.trim() : "player" + Math.floor(Math.random() * 1000);
      void openLaunchDestination();
    },
    POPUP_Y_CENTERED,
  );
}

// openLaunchDestination is where a page load lands once the visitor has a name:
// the world their URL names, the first-visit welcome world, or the picker.
//
// It resolves the name through /api/worlds rather than trusting the URL, so the
// deep link inherits M18.13's identity — the name the join path would open —
// and a name that is not joinable is refused here, before any socket exists.
type LaunchDestination = "play" | "watch";

async function openLaunchDestination() {
  if (isWatchLivePath(window.location.pathname)) {
    await startWatchLive();
    return;
  }

  const replayRequested = replayLinkID(window.location.pathname);
  if (replayRequested) {
    startReplay(replayRequested);
    return;
  }

  // M32.1: /challenge is today's and /challenge/<id> is a named one. The route
  // is checked BEFORE the world deep links, so a challenge id can never be read
  // as a world name — and an empty id is a real answer here (the server picks
  // today's from its own clock), so it is not treated as a dead link.
  if (isChallengePath(window.location.pathname)) {
    await openChallengeLanding(challengeLinkID(window.location.pathname), { rememberPath: false });
    return;
  }

  let destination: LaunchDestination = "play";
  let requested = deepLinkWorldName(window.location.pathname);
  const watchRequested = watchLinkWorldName(window.location.pathname);
  if (watchRequested) {
    destination = "watch";
    requested = watchRequested;
  } else if (requested && watchRequestedByQuery()) {
    destination = "watch";
  }
  if (!requested) {
    await openDefaultLaunchDestination();
    return;
  }
  let entries: WorldSearchEntry[];
  try {
    entries = await fetchWorldEntries();
  } catch {
    refuseDeepLink(requested, "the server did not answer");
    return;
  }
  const world = resolveDeepLinkWorld(requested, entries);
  if (!world) {
    refuseDeepLink(requested);
    return;
  }
  // enterWorld and nothing else: a deep link must take the same seam the picker
  // takes, which is what stops it becoming the one path that skips the title
  // screen and joins straight into a room.
  await enterWorld(world, destination);
}

async function openDefaultLaunchDestination() {
  let entries: WorldSearchEntry[];
  try {
    entries = await fetchWorldEntries();
  } catch {
    openWindow("ZZT Worlds", ["", "  Not available: the server did not answer.", ""], true);
    return;
  }
  if (entries.length === 0) {
    openWindow("ZZT Worlds", ["", "  There are no ZZT worlds.", ""], true);
    return;
  }
  if (!accountPrefsLoaded) {
    await refreshAuthStatus();
  }
  showWorldEntries(entries);
}

// watchRequestedByQuery keeps M22.1's temporary door working long enough for old
// links to arrive, but M22.2's canonical address is /watch/<world>.
function watchRequestedByQuery(): boolean {
  return new URLSearchParams(window.location.search).get("spectate") === "1";
}

// A dead link leaves the visitor in a working client: the window says which name
// failed, and closing it opens the picker (showWorldsOnClose, read by closeModal).
function refuseDeepLink(requested: string, reason = "") {
  showWorldsOnClose = true;
  openWindow("Deep link", deepLinkRefusalLines(requested, reason), true);
}

// rememberWorldInPath keeps the address bar shareable: whatever title screen is
// on show, the URL is the link that reaches it. replaceState, not pushState —
// Back should leave the app, not walk a history of title screens.
function rememberWorldInPath(destination: LaunchDestination = "play") {
  if (worldName === "" || worldName === "Untitled") {
    return;
  }
  const path = destination === "watch" ? watchLinkPath(worldName) : deepLinkPath(worldName);
  if (window.location.pathname !== path) {
    const search = destination === "watch" ? "" : window.location.search;
    window.history.replaceState(null, "", path + search);
  }
}

function rememberReplayInPath(id: string) {
  const path = replayLinkPath(id);
  if (window.location.pathname !== path) {
    window.history.replaceState(null, "", path);
  }
}

// Leaving the world puts the path back to the app root, so a reload after
// quitting starts where a first-time visitor starts rather than re-entering the
// world the player just left.
function forgetWorldInPath() {
  if (window.location.pathname !== "/") {
    window.history.replaceState(null, "", "/" + window.location.search);
  }
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
canvas.addEventListener(
  "touchstart",
  (event) => {
    // While an editable modal is open on a touch device, keep the soft keyboard
    // up: swallow the tap's default so the focusable (tabindex=0) canvas cannot
    // steal focus and dismiss the keyboard, then (re)focus the hidden input inside
    // this gesture — the one moment iOS will actually raise it. Outside a text
    // modal the tap is left alone (future touch controls, M16.18a).
    if (mobileTextInput.isActive()) {
      event.preventDefault();
    }
    mobileTextInput.noteTouchStart();
  },
  { passive: false },
);
// On-screen controls for phones: ZZT is a keyboard game and a phone has none, so
// the bar's buttons drive the same key handlers a physical key would. No-op on
// desktop (createTouchControls returns null when there is no touch).
touchControls = createTouchControls(document, {
  key(down, code, key) {
    const event = new KeyboardEvent(down ? "keydown" : "keyup", { code, key, bubbles: true, cancelable: true });
    if (down) {
      handleKeyDown(event);
    } else {
      handleKeyUp(event);
    }
  },
  toggleKeyboard() {
    mobileTextInput.toggleKeyboard();
  },
});
syncTouchControls();
window.addEventListener("mouseup", () => { editorPointerDrawing = false; });
canvas.addEventListener("keydown", handleKeyDown);
canvas.addEventListener("keyup", handleKeyUp);
// Web Audio starts suspended until a user gesture resumes it. handleKeyDown /
// handlePointerDown call resume() only for gestures on the canvas, so any gesture
// that lands on the hidden mobile-input overlay (M15.1) or during the muted title
// screen never unlocked audio — the launch name popup opens on load, so on touch
// devices that is every early gesture. Unlock from a document-level, capture-phase
// listener that fires no matter what has focus and regardless of the enabled gate.
for (const type of ["pointerdown", "keydown", "touchstart"] as const) {
  document.addEventListener(type, () => zztSound.unlock(), { capture: true, passive: true });
}
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
  mobileTextInput.close();
  playerId = 0;
  myStatId = -1;
  // The room's roster does not survive leaving it (M19.1) — a stale one would
  // tint squares of a board nobody in it is standing on.
  roster = [];
  // Nor does the block mirror (M21.1). The server is the authority and this is
  // only ever a mirror of it, so leaving the title screen claims no knowledge
  // rather than a stale one; the next join's snapshot re-states which of the
  // people in the room are blocked (M21.4), which is what stops "no knowledge"
  // from reading on screen as "nobody is blocked".
  blockedPlayerIds = new Set<number>();
  followedPlayerIds = new Set<number>();
  // Nor does operator status (M21.2). It comes back on the next join's snapshot,
  // from the allowlist, which is the only thing that decides it.
  isOperator = false;
  // Nor does watching (M22.1): the title screen is not a room, and leaving one
  // must not leave the reconnect holding an intent to re-enter it.
  watching = false;
  replaying = false;
  replayID = "";
  replayTick = 0;
  replayStartTick = 0;
  stopWatchLive();
  watcherCount = 0;
  editorCursor = { x: 30, y: 12 };
  editorSidebarMenu = null;
  editorStatPrompt = null;
  zztSound.setEnabled(false);
  setPaused(false);
  setEditorBlinking(false);
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
  drawTitleSidebar(
    writeText,
    friendlyName,
    authDisplayName(),
    authStatus.enabled,
    serverOccupancy,
    readStoredPlayerColor(),
  );
  paintOverlay();
  drawScreen();
  canvas.focus();
  if (streaming) {
    openTitleStream(worldName);
  }
  void refreshAuthStatus();
  // M17.11: how busy the server is, refreshed for as long as we sit here.
  void refreshOccupancy();
  startOccupancyPolling();
}

async function refreshAuthStatus() {
  accountPrefsLoaded = false;
  try {
    const response = await fetch("/api/auth/me");
    authStatus = (await response.json()) as AuthStatus;
  } catch {
    authStatus = { enabled: false, authenticated: false };
  }
  // Sign-in state and preferences are read together because the second only
  // means anything given the first: signing out must put this browser back on
  // its own localStorage pick in the same breath (M19.3).
  accountPrefs = authStatus.authenticated ? await fetchAccountPreferences(fetch) : null;
  accountPrefsLoaded = true;
  refreshFontPalette();
  if (mode === "title") {
    drawTitleSidebar(
      writeText,
      titleFriendlyName,
      authDisplayName(),
      authStatus.enabled,
      serverOccupancy,
      readStoredPlayerColor(),
    );
    paintOverlay();
    drawScreen();
  }
  if (mode === "playing") {
    tryShowFirstTimeHint("players");
  }
}

function authDisplayName(): string {
  if (!authStatus.authenticated) {
    return "";
  }
  return authStatus.name || authStatus.email || "";
}

function redrawTitleSidebar() {
  drawTitleSidebar(
    writeText,
    titleFriendlyName,
    authDisplayName(),
    authStatus.enabled,
    serverOccupancy,
    readStoredPlayerColor(),
  );
}

// leaveToTitle ends this player's game: the room already dropped them, so all
// that remains is to close the socket without tripping the reconnect.
function leaveToTitle() {
  leavingToTitle = true;
  stopWatchLive();
  window.clearInterval(inputTimer);
  window.clearTimeout(retryTimer);
  connected = false;
  reconnectAttempt = 0;
  // An intentional exit ends the run: forget its resume token so pressing Play
  // again starts a fresh player rather than reclaiming a room we chose to leave.
  clearResumeToken(window.sessionStorage, worldName);
  chatMessages = [];
  currentHintMessage = "";
  if (ws) {
    ws.close();
    ws = null;
  }
  void showTitle();
}

// startPlay's `challenge` option is the one caller that keeps the challenge
// state: startChallengeRun. Every other route into play LEAVES a challenge
// (M32.1) — without that, the socket for the next world would still carry the
// finished run's key instead of `world=`.
function startPlay(options: { challenge?: boolean } = {}) {
  if (!options.challenge) {
    leaveChallenge();
  }
  closeTitleStream();
  stopOccupancyPolling();
  clearScrolls();
  zztSound.setEnabled(true);
  zztSound.resume();
  leavingToTitle = false;
  watching = false;
  replaying = false;
  replayID = "";
  replayTick = 0;
  replayStartTick = 0;
  stopWatchLive();
  reconnectAttempt = 0;
  drawSidebar();
  drawScreen();
  connect();
}

// startWatch is startPlay's read-only twin (M22.1): the same socket to the same
// world, joined as a spectator.
//
// Sound stays off. A watcher receives no events at all — the server shuts that
// channel rather than half-opening it for the one safe member (a room-wide
// #play) — so a watcher with sound enabled would be a client waiting for notes
// that are never sent.
function startWatch(options: { channel?: boolean } = {}) {
  leaveChallenge();
  closeTitleStream();
  stopOccupancyPolling();
  clearScrolls();
  zztSound.setEnabled(false);
  leavingToTitle = false;
  if (!options.channel) {
    stopWatchLive();
  }
  watching = true;
  replaying = false;
  replayTick = 0;
  replayStartTick = 0;
  watcherCount = 0;
  reconnectAttempt = 0;
  mode = "watching";
  drawWatchSidebar();
  drawScreen();
  connect();
}

function startReplay(id: string, options: { rememberPath?: boolean; startTick?: number; channel?: boolean } = {}) {
  leaveChallenge();
  closeTitleStream();
  stopOccupancyPolling();
  clearScrolls();
  zztSound.setEnabled(false);
  leavingToTitle = false;
  if (!options.channel) {
    stopWatchLive();
  }
  watching = true;
  replaying = true;
  watcherCount = 0;
  replayID = id;
  replayTick = 0;
  replayStartTick = options.startTick ?? 0;
  reconnectAttempt = 0;
  mode = "watching";
  if (options.rememberPath !== false) {
    rememberReplayInPath(id);
  }
  drawWatchSidebar();
  drawScreen();
  connect();
}

async function startWatchLive() {
  closeTitleStream();
  stopOccupancyPolling();
  clearScrolls();
  zztSound.setEnabled(false);
  leavingToTitle = false;
  watchLive = true;
  watchLiveEmbed = watchLiveEmbedMode(window.location.search);
  if (watchLiveEmbed) {
    document.body.dataset.zztEmbed = "watch-live";
  } else {
    delete document.body.dataset.zztEmbed;
  }
  watching = true;
  replaying = false;
  replayID = "";
  replayTick = 0;
  replayStartTick = 0;
  watcherCount = 0;
  reconnectAttempt = 0;
  mode = "watching";
  watchLiveLabel = "TV tuning";
  drawWatchSidebar();
  drawScreen();
  try {
    const response = await fetch("/api/watch/live");
    const data = (await response.json()) as { entries?: WatchLiveLineupEntry[] };
    watchLiveEntries = (data.entries ?? []).filter((entry) => entry.kind === "live" || entry.kind === "replay");
  } catch {
    watchLiveEntries = [];
  }
  if (!watchLive) {
    return;
  }
  if (watchLiveEntries.length === 0) {
    watchLiveLabel = "TV no signal";
    drawWatchSidebar();
    drawScreen();
    return;
  }
  tuneWatchLive(0);
}

function stopWatchLive() {
  window.clearTimeout(watchLiveTimer);
  watchLiveTimer = 0;
  watchLive = false;
  watchLiveEntries = [];
  watchLiveIndex = -1;
  watchLiveSwitching = false;
  watchLiveLabel = "";
  watchLiveEmbed = false;
  delete document.body.dataset.zztEmbed;
}

function tuneWatchLive(index: number) {
  if (!watchLive || watchLiveEntries.length === 0) {
    return;
  }
  const next = ((index % watchLiveEntries.length) + watchLiveEntries.length) % watchLiveEntries.length;
  const entry = watchLiveEntries[next];
  const target = watchLiveEntryTarget(entry);
  if (!target) {
    return;
  }
  watchLiveIndex = next;
  watchLiveLabel = watchLiveEntryLabel(entry, next, watchLiveEntries.length);
  window.clearTimeout(watchLiveTimer);
  watchLiveSwitching = true;
  if (ws) {
    const old = ws;
    ws = null;
    old.close();
  }
  watchLiveSwitching = false;
  if (target.kind === "replay") {
    startReplay(target.replayId, { rememberPath: false, startTick: target.startTick, channel: true });
  } else {
    worldName = target.world;
    startWatch({ channel: true });
  }
  if (watchLiveEntries.length > 1) {
    watchLiveTimer = window.setTimeout(nextWatchLive, WATCH_LIVE_CYCLE_MS);
  }
}

function nextWatchLive() {
  if (!watchLive || watchLiveEntries.length === 0) {
    return;
  }
  tuneWatchLive(nextWatchLiveIndex(watchLiveIndex, watchLiveEntries.length));
}

// startEditor deliberately opens a different kind of WebSocket. The server
// receives editorEnter instead of join, creates an isolated EditorSession, and
// never registers this browser with a RoomManager.
function startEditor() {
  closeTitleStream();
  stopOccupancyPolling();
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
  editorRestore = null;
  reconnectAttempt = 0;
  editorCategoryMenu = null;
  editorPresencePanel = false;
  editorSidebarMenu = null;
  editorStatPrompt = null;
  renderEditorSidebar();
  setEditorBlinking(true);
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
  setEditorBlinking(false);
  editorExitAfterSave = false;
  editorRestore = null;
  window.clearTimeout(retryTimer);
  // Leaving on purpose ends the membership, so its token must not be presented
  // by the next entry: that would be a browser asking to resume something it
  // chose to leave (M16.14f, and leaveToTitle's clearResumeToken).
  clearEditorToken(window.sessionStorage, worldName);
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

async function fetchWorldEntries(): Promise<WorldSearchEntry[]> {
  const response = await fetch("/api/worlds");
  const data = (await response.json()) as { worlds?: (WorldSearchEntry | string)[]; shelves?: WorldShelf[] };
  worldPickerShelves = normalizeWorldShelves(data.shelves ?? []);
  return normalizeWorldEntries(data.worlds ?? []);
}

async function showWorlds() {
  let worlds: WorldSearchEntry[] = [];
  try {
    worlds = await fetchWorldEntries();
  } catch {
    openWindow("ZZT Worlds", ["", "  Not available: the server did not answer.", ""], true);
    return;
  }

  if (worlds.length === 0) {
    openWindow("ZZT Worlds", ["", "  There are no ZZT worlds.", ""], true);
    return;
  }

  showWorldEntries(worlds);
}

function showWorldEntries(worlds: WorldSearchEntry[]) {
  serverOccupancy = worldOccupancyTotal(worlds);
  // The picker's own local entries, kept so a later refresh can update the
  // counts the museum-search closure will fall back to (M17.11).
  worldPickerEntries = worlds;

  openModal({
    kind: "worldSearch",
    title: WORLD_SEARCH_TITLE,
    query: "",
    selected: 0,
    entries: worlds,
    shelves: worldPickerShelves,
    onSelect: (entry) => void selectWorldEntry(entry),
    onQuery: (query) => scheduleMuseumSearch(query, worlds),
    onFavorite: (entry, favorite) => toggleWorldFavorite(entry, favorite),
  });
}

// M17.11: occupancy is live, not a join-time snapshot. /api/worlds is the path
// the picker already reads, so one poll feeds both the picker's per-world
// playing/editing split and the title screen's server-wide total. It runs only
// on the title screen — in play or the editor the sidebar belongs to the game.
const OCCUPANCY_POLL_MS = 5000;

function startOccupancyPolling() {
  if (occupancyTimer !== 0) {
    return;
  }
  occupancyTimer = window.setInterval(() => void refreshOccupancy(), OCCUPANCY_POLL_MS);
}

function stopOccupancyPolling() {
  window.clearInterval(occupancyTimer);
  occupancyTimer = 0;
}

async function refreshOccupancy() {
  if (mode !== "title") {
    stopOccupancyPolling();
    return;
  }
  let worlds: WorldSearchEntry[];
  try {
    worlds = await fetchWorldEntries();
  } catch {
    // A missed poll is not an error: the previous counts stand until the next.
    return;
  }
  // The player may have left the title screen while the fetch was in flight.
  if (mode !== "title") {
    return;
  }
  serverOccupancy = worldOccupancyTotal(worlds);
  applyWorldOccupancy(worldPickerEntries, worlds);
  if (modal && modal.kind === "worldSearch") {
    // Museum results are merged into a fresh array, so the entries on screen are
    // not always the ones the picker was opened with; update both.
    applyWorldOccupancy(modal.entries, worlds);
    modal.shelves = worldPickerShelves;
  }
  drawTitleSidebar(
    writeText,
    titleFriendlyName,
    authDisplayName(),
    authStatus.enabled,
    serverOccupancy,
    readStoredPlayerColor(),
  );
  paintOverlay();
  drawScreen();
}

let museumSearchTimer = 0;
let museumSearchSeq = 0;

function scheduleMuseumSearch(query: string, localEntries: WorldSearchEntry[]) {
  window.clearTimeout(museumSearchTimer);
  const trimmed = query.trim();
  if (trimmed.length < 2) {
    if (modal && modal.kind === "worldSearch") {
      modal.entries = localEntries;
      modal.shelves = worldPickerShelves;
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
    modal.shelves = [];
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
        title: entry.title || entry.world,
        author: entry.author || "Unknown",
        created: entry.created || "",
        players: entry.players || 0,
        favorite: entry.favorite === true,
        playCount: entry.playCount || 0,
        thumbnail: entry.thumbnail,
        editors: entry.editors || 0,
        friendsHere: entry.friendsHere ?? [],
        source: "local",
        // M18.9: the server's grouping. Left undefined by an older server or
        // by the bare-string list below, and the picker treats "not local" as
        // showable, so an unknown kind fails open — a world is never hidden
        // because the server did not say what it was.
        kind: entry.kind,
      };
    }
    const world = entry.split(" (")[0];
    return {
      world,
      id: world,
      title: world,
      author: "Unknown",
      created: "",
      players: 0,
      editors: 0,
      source: "local",
    };
  });
}

function normalizeWorldShelves(shelves: WorldShelf[]): WorldShelf[] {
  return shelves
    .map((shelf) => ({
      id: typeof shelf.id === "string" ? shelf.id : "",
      title: typeof shelf.title === "string" ? shelf.title : "",
      worlds: Array.isArray(shelf.worlds) ? shelf.worlds.filter((world): world is string => typeof world === "string") : [],
    }))
    .filter((shelf) => shelf.id && shelf.title && shelf.worlds.length > 0);
}

function toggleWorldFavorite(entry: WorldSearchEntry, favorite: boolean): boolean {
  if (!authStatus.authenticated) {
    openWindow("Favorites", ["", "  Sign in to keep favorite worlds.", ""], true);
    return false;
  }
  void saveFavoriteWorld(fetch, entry.world, favorite).then((stored) => {
    if (!stored) {
      entry.favorite = !favorite;
      openWindow("Favorites", ["", "  Favorite was not saved.", ""], true);
      return;
    }
    accountPrefs = stored;
    for (const world of worldPickerEntries) {
      world.favorite = stored.favoriteWorlds.includes(world.world);
    }
    if (modal && modal.kind === "worldSearch") {
      for (const world of modal.entries) {
        world.favorite = stored.favoriteWorlds.includes(world.world);
      }
      paintOverlay();
      drawScreen();
    }
  });
  return true;
}

function openDreamPrompt() {
  openModal({
    kind: "multilineEntry",
    title: "Dream a world",
    buffer: "",
    onSubmit: (premise) => {
      if (premise) {
        // Enter is the whole submission gesture: start the world immediately.
        // Grounded generation remains an explicit API capability, but never
        // inserts an extra confirmation step between this prompt and the job.
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

async function startDreamGeneration(prompt: string, ground = false) {
  showGenerationProgress([]);
  try {
    const dream = await runDreamGeneration(
      prompt,
      fetch,
      () => new Promise((resolve) => window.setTimeout(resolve, 500)),
      showGenerationProgress,
      ground,
    );
    await enterWorld(dream.world);
    offerDreamRepaint(dream);
  } catch (error) {
    handleDreamFailure(error);
  }
}

// M16.17c — the salvaged dream's repaint offer.
//
// A dream that lost a room is complete AND retryable (M17.13): the world is
// hosted and playable, the lost rooms are stub boards, and the server is still
// holding the resume state that repaints them. The world comes first — the
// offer arrives with it, not instead of it, which is what separates this from
// handleDreamFailure below.
//
// It is offered at the world's title screen rather than from inside the world,
// and that ordering is load-bearing: a repaint rewrites the world's file, and
// the server refuses to overwrite a world anybody is playing (M16.17b's
// refuseIfOccupied, which RetryBoard re-enters). A player who took the offer
// while standing in the stub room would be the one occupant blocking it.
const REPAINT_CHOICE = "Repaint the lost rooms now";
const KEEP_CHOICE = "Play the world as it is";
// The text window's inner span, matching dream.ts's own progress-line clamp:
// a longer line bleeds past the border into the sidebar.
const DREAM_OFFER_WIDTH = 42;

function offerDreamRepaint(dream: DreamResult) {
  const lost = salvagedBoards(dream);
  if (!lost) {
    return;
  }
  openSelectList(
    "Some rooms would not form",
    [REPAINT_CHOICE, KEEP_CHOICE],
    (choice) => {
      if (choice === REPAINT_CHOICE) {
        void resumeDreamGeneration(dream.jobId);
      }
    },
    [
      "",
      `The dream lost: ${lost}`.slice(0, DREAM_OFFER_WIDTH),
      "",
      "The world is yours to play now. Its lost",
      "rooms are stubs until they are repainted.",
      "",
    ],
  );
}

// When the server kept resumable state (a board exhausted its attempts but
// the plan and earlier boards survive — M12.22), offer to re-request just the
// failed board before giving up on the whole world.
function handleDreamFailure(error: unknown) {
  if (error instanceof DreamFailure && error.retryable) {
    const board = error.failedBoard || "the failed board";
    openYesNo(`Dream failed. Repaint "${board}"? `, (yes) => {
      if (yes) {
        void resumeDreamGeneration(error.jobId);
      } else {
        openWindow("Dream failed", ["", error.message, "", "Try a shorter premise later."], true);
      }
    });
    return;
  }
  openWindow("Dream failed", ["", String(error), "", "Try a shorter premise later."], true);
}

async function resumeDreamGeneration(jobId: string) {
  showGenerationProgress([]);
  try {
    const dream = await retryDreamBoard(
      jobId,
      fetch,
      () => new Promise((resolve) => window.setTimeout(resolve, 500)),
      showGenerationProgress,
    );
    await enterWorld(dream.world);
    // A repaint can lose a room of its own: the retry answers with the same
    // complete-and-retryable shape, so it is offered again rather than left
    // for the player to discover by walking into it.
    offerDreamRepaint(dream);
  } catch (error) {
    handleDreamFailure(error);
  }
}

// enterWorld selects a world's title board and stops there. Pressing P is the
// only thing that joins the chosen world's own instance. It does NOT call
// /api/loadworld: that swaps the server's single default world for everybody
// and is refused while anyone is playing. Each world already has its own
// RoomManager server-side (WebSocketServer.GetOrCreateInstance), reached by the
// ?world= parameter wsURL sends — which is what makes worlds independent.
async function enterWorld(name: string, destination: LaunchDestination = "play") {
  leaveChallenge();
  const selection = selectWorldForTitle(name);
  worldName = selection.worldName;
  await showTitle();
  // M20.1: every route into a world funnels through here — picker, Museum row,
  // dream, restore — so this one line is what makes the address bar the link
  // that works. It runs AFTER showTitle because showTitle adopts the filename
  // /api/title answers with, and the path must name what we actually landed on.
  rememberWorldInPath(destination);
  if (destination === "watch") {
    startWatch();
  } else if (selection.startPlay) {
    startPlay();
  }
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
    reconnectAttempt = 0;
    // M22.1: a watcher's join is the same message marked as a spectate, and the
    // input sampler below is never started for one. Two gates rather than one,
    // deliberately: the server drops what a watcher sends, so a sampler left
    // running would be invisible here and cost a message every 55ms forever.
    if (watching) {
      socket.send(JSON.stringify(buildWatchMessage(MessageTypeJoin)));
      canvas.focus();
      return;
    }
    // No board: the server picks its configured default (zzt-server -board). A
    // stored resume token reclaims a dropped run instead of spawning a new
    // player (M13.2); an unknown/expired token is treated as a fresh join.
    const token = loadResumeToken(window.sessionStorage, challengeMode && challengeRun ? challengeRun : worldName);
    // The "#RRGGBB" background this browser's ☻ is drawn on in everyone else's
    // client (M19.1), re-read at every join including a reconnect: it is the
    // browser's current pick, not a property of the run being reclaimed. A
    // player who has never picked one joins as the vanilla white-on-blue smiley.
    const color = readStoredPlayerColor();
    socket.send(JSON.stringify(buildJoinMessage(MessageTypeJoin, nickname, token, color)));
    canvas.focus();
    inputTimer = window.setInterval(() => sendInput(currentMask()), 55);
  });

  socket.addEventListener("message", (event) => {
    const message = JSON.parse(String(event.data)) as ServerMessage;
    applyMessage(message);
  });

  socket.addEventListener("close", () => {
    if (ws !== socket || watchLiveSwitching) {
      return;
    }
    disconnect("Disconnected");
  });
  socket.addEventListener("error", () => {
    if (ws !== socket || watchLiveSwitching) {
      return;
    }
    disconnect("Connection error");
  });
}

function connectEditor() {
  if (ws && ws.readyState === WebSocket.OPEN) {
    return;
  }
  window.clearTimeout(retryTimer);
  const socket = new WebSocket(wsURL());
  ws = socket;
  socket.addEventListener("open", () => {
    connected = true;
    reconnectAttempt = 0;
    // A stored membership token re-enters the session this browser was already
    // in (M16.14f) rather than joining it a second time; an unknown or already
    // ended one is treated as a first entry.
    const token = loadEditorToken(window.sessionStorage, worldName);
    socket.send(JSON.stringify(buildEditorEnterMessage(MessageTypeEditorEnter, worldName, token)));
    canvas.focus();
  });
  socket.addEventListener("message", (event) => {
    applyMessage(JSON.parse(String(event.data)) as ServerMessage);
  });
  socket.addEventListener("close", () => {
    // A socket that has already been superseded has nothing to recover: test
    // play and leaving the editor both put their own connection (or none) in
    // ws before this event lands.
    if (ws !== socket) {
      return;
    }
    reconnectEditor();
  });
  socket.addEventListener("error", () => {
    // close owns the recovery so an error cannot race it into a game reconnect.
  });
}

// reconnectEditor is the editor's half of what the game socket has had since
// M13.2. Before M16.14f a close of any kind — a blip, a server restart, a lid —
// dropped the author onto the title screen of the world they were editing, and
// threw away everything they had not saved; the session they were in was still
// on the server the whole time, so the recovery is a re-enter with the same
// capped backoff.
//
// It restores only what the browser owns. The world, the board contents and the
// membership come back in the entry snapshot, and anything the SERVER owns is
// let go here: the leases went with the old connection (the session releases
// them the moment it notices the drop), so a dialog holding one is closed
// rather than left looking like it can still save.
function reconnectEditor() {
  if (leavingToTitle || mode !== "editor") {
    return;
  }
  connected = false;
  ws = null;
  pendingEditorLease = null;
  activeEditorLease = null;
  retainEditorLeaseOnClose = false;
  if (modal) {
    closeModal();
  }
  editorCategoryMenu = null;
  editorSidebarMenu = null;
  editorStatPrompt = null;
  editorDrawing = false;
  // The reconnect may be handed a different membership (a fresh entry when the
  // server noticed the drop first), so the identity is dropped and re-claimed
  // from the entry snapshot — applyEditorSnapshot only adopts one it is not
  // already holding.
  editorMemberId = "";
  editorRestore = { boardId: editorProperties.boardId, x: editorCursor.x, y: editorCursor.y };
  // Nothing may repaint over the notice while we are away, and the editor's
  // idle cursor blink is the one thing that would.
  setEditorBlinking(false);
  drawConnectionNotice("Reconnecting...");
  const delay = reconnectDelay(reconnectAttempt);
  reconnectAttempt += 1;
  retryTimer = window.setTimeout(connectEditor, delay);
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
  if (replaying) {
    url.searchParams.set("replay", replayID);
    if (replayStartTick > 0) {
      url.searchParams.set("start", String(replayStartTick));
    }
  } else if (challengeMode) {
    // A run key reclaims the attempt this browser is already in; a bare id
    // starts a new one. Both go to the same socket the picker uses — a
    // challenge is play, not a second protocol.
    if (challengeRun) {
      url.searchParams.set("run", challengeRun);
    } else {
      url.searchParams.set("challenge", challengeID);
    }
  } else {
    url.searchParams.set("world", worldName);
  }
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
    case MessageTypePrivateMessage:
      handlePrivateMessage(message);
      break;
    case MessageTypePrivateResult:
      handlePrivateResultMessage(message);
      break;
    case MessageTypeAnnounce:
      handleAnnounceMessage(message);
      break;
    case MessageTypeBlockResult:
      handleBlockResultMessage(message);
      break;
    case MessageTypeFollowResult:
      handleFollowResultMessage(message);
      break;
    case MessageTypeModerateResult:
      handleModerateResultMessage(message);
      break;
    case MessageTypeModerationNotice:
      handleModerationNoticeMessage(message);
      break;
    case MessageTypeProfileResult:
      handleProfileResultMessage(message);
      break;
    case MessageTypeReplayError:
      handleReplayErrorMessage(message);
      break;
    case MessageTypeChallengeResult:
      handleChallengeResult(message);
      break;
    case MessageTypeChallengeError:
      handleChallengeError(message);
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
  // The entry snapshot carries the token this browser comes back with if its
  // socket closes (M16.14f). Only the entry snapshot has one, so a later
  // broadcast frame cannot overwrite it.
  if (message.resumeToken) {
    saveEditorToken(window.sessionStorage, worldName, message.resumeToken);
  }
  // EditorSnapshot is broadcast to every session member carrying the *acting*
  // member's id, cursor and inspect (editor_session.go:313, broadcast at
  // websocket_server.go:611). Only the board/screen half of it is shared state.
  // Adopting the rest unconditionally meant another player's edit overwrote our
  // identity and dragged our cursor onto their cell, where the local white cross
  // painted over their coloured one — the "both cursors went white" report.
  // Claim an id only when we do not already have one (the entry snapshot), and
  // take cursor-shaped state only from snapshots that are actually ours.
  const forMe = !editorMemberId || !message.memberId || message.memberId === editorMemberId;
  if (message.memberId && !editorMemberId) editorMemberId = message.memberId;
  // Read-only is per member, and a snapshot is now broadcast to everyone
  // watching the board a change was made on (M16.14a): only one addressed to us
  // may say what we are allowed to do. An invite arrives as exactly that, which
  // is how a new collaborator's browser stops refusing their own edits.
  if (forMe) {
    editorReadOnly = !!message.readOnly;
    editorInspect = message.inspect;
    editorCursor = { x: message.inspect.x, y: message.inspect.y };
  }
  if (message.presence) editorPresence = message.presence;
  adoptEditorProperties(message.properties, forMe);
  // Menus arrive only on the entry snapshot; board add/switch reuse this
  // message without them, so keep the tables already held.
  if (message.menus) editorMenus = message.menus;
  // A broadcast frame belongs to one board, exactly like a diff (M17.12): the
  // server addresses it to the members viewing that board, and this is the
  // client re-checking, so a snapshot in flight across a board switch cannot
  // land late on the wrong board.
  if (forMe || editorMessageIsForBoard(message.properties.boardId, editorProperties.boardId)) {
    replaceCells(message.screen);
  }
  if (forMe) {
    restoreEditorAfterReconnect(message);
  }
  renderEditorSidebar();
  setEditorBlinking(true);
  paintOverlay();
  drawScreen();
}

// restoreEditorAfterReconnect puts the browser's own state back around a
// reconnect's snapshot (M16.14f). A resumed membership is already on the right
// board and inspecting the right cell, and this changes nothing; a fresh entry
// opens on the SESSION's current board — whichever board somebody acted on last
// — with its cursor in the middle, so both are asked for again.
function restoreEditorAfterReconnect(message: EditorSnapshotMessage) {
  const restore = editorRestore;
  if (!restore) {
    return;
  }
  if (message.properties.boardId !== restore.boardId) {
    // Ask once: the switch answers with a snapshot of its own, and this runs
    // again for it with the board half already recorded as done. A switch the
    // session refuses (the board was deleted while we were away) therefore
    // leaves the browser on the board it was given rather than asking forever.
    editorRestore = { ...restore, boardId: message.properties.boardId };
    sendEditorBoard({ op: "switch", boardId: restore.boardId });
    return;
  }
  editorRestore = null;
  editorCursor = { x: restore.x, y: restore.y };
  // The session's presence — the cursor other collaborators see — is moved by
  // an inspect, and its reply refreshes the sidebar readout for this cell.
  sendEditorInspect();
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
  // Only when the legend is up: presence arrives on every collaborator
  // keystroke, and the rest of the sidebar has nothing to learn from it.
  if (editorPresencePanel) renderEditorSidebar();
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
  // A diff belongs to one board (M17.12). The server sends it only to members
  // viewing that board, but a diff can still be in flight while we switch, and
  // its cells would then paint the previous board's tiles onto this one.
  if (!editorMessageIsForBoard(message.boardId, editorProperties.boardId)) return;
  // EditorPrepareModifyTile's `wasModified := true` (editor.go:169), moved to
  // the reply (M16.13a). A refused edit — a read-only member, an unheld lease, a
  // placement the session declined — sends no diff or an empty one, and so
  // cannot dirty a world it never changed. A collaborator's edit counts too: the
  // world this browser would save has changed either way.
  if (message.cells.length > 0) editorModified = true;
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
  // The board-info half of vanilla's flag (editor.go:242): this message is only
  // ever sent for an ACCEPTED Board Information / world-name change — a refused
  // field returns before the reply is built (editor_session.go SetProperty).
  // A collaborator's accepted change counts too, now that it reaches us
  // (M16.14a): the world this browser would save has changed either way.
  editorModified = true;
  adoptEditorProperties(message.properties, false);
  // The frame rides only the copy sent to the members viewing that board.
  if (message.screen && message.screen.length > 0) replaceCells(message.screen);
  renderEditorSidebar();
  paintOverlay();
  drawScreen();
}

// adoptEditorProperties takes only what a properties payload is entitled to
// change (M16.14a). The board half — its name, darkness, exits, and the open
// board's id — is ours to take when the payload is about the board we are
// looking at, or when it is a snapshot addressed to us, which is what a board
// switch is. From a collaborator editing another board it is not: it would
// retitle our board, and it would move editorProperties.boardId, which is this
// client's own record of where it is (M17.12).
//
// The world half is shared by everyone regardless of where they are standing:
// the switcher's board list, which grows when anybody adds a board and is
// renamed when anybody retitles one, and the world name.
function adoptEditorProperties(properties: EditorProperties, forMe: boolean) {
  if (forMe || properties.boardId === editorProperties.boardId) {
    editorProperties = properties;
    return;
  }
  editorProperties = { ...editorProperties, boards: properties.boards, worldName: properties.worldName };
}

function applyEditorStatSettings(message: EditorStatSettingsMessage) {
  // The stat half of vanilla's flag (editor.go:401), covering both an edited
  // parameter and a saved ZZT-OOP program. As with a diff, a change always
  // redraws the stat's tile, so cells are the proof one landed: a refused stat
  // edit never reaches a reply, and a program the session declined to store
  // redraws nothing.
  if (message.cells.length > 0) editorModified = true;
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
  // M22.1: a frame the server marked as a watcher's is a different screen, not
  // a player's screen with the parts we happen to have missing.
  if (message.spectator) {
    applyWatchSnapshot(message);
    return;
  }
  mode = "playing";
  setEditorBlinking(false);
  if (message.world && message.world !== worldName) {
    worldName = message.world;
    rememberWorldInPath("play");
  }
  // The join/resume snapshot carries our resume token; keep it so a later drop
  // can reclaim this run (M13.2).
  if (message.resumeToken) {
    saveResumeToken(window.sessionStorage, challengeMode && challengeRun ? challengeRun : worldName, message.resumeToken);
  }
  // M32.1: a challenge frame names its run. The elapsed counter restarts here
  // and nowhere else — it is what the ghost is indexed by, so it must count the
  // ticks of THIS attempt, and a reconnect inside the grace resumes the count
  // rather than restarting the race.
  playBoardId = message.boardId;
  if (message.challenge) {
    challengeMode = true;
    challengeID = message.challenge;
    if (message.challengeRun !== challengeRun) {
      challengeRun = message.challengeRun ?? "";
      challengeElapsed = 0;
      challengeFinished = false;
    }
  }
  playerId = message.you.id;
  myStatId = message.you.statId;
  myX = message.you.x;
  myY = message.you.y;
  // The roster arrives with the snapshot too, and it is the only one a client
  // gets before the first diff — without this a newcomer sees the room in
  // vanilla blue for a tick (M19.1).
  trackMyStatId(message.players);
  // And with it, which of that roster this player had already blocked (M21.4).
  // Merged, never assigned: the server's list only covers the players in this
  // snapshot, so it can add knowledge and must not be able to withdraw any.
  blockedPlayerIds = mergeServerBlocks(blockedPlayerIds, message.blockedPlayers);
  followedPlayerIds = mergeServerBlocks(followedPlayerIds, message.followedPlayers);
  // Whether this account may moderate (M21.2). The server sets it on the
  // join/resume frame only, like the resume token, so a frame that omits the
  // field leaves the answer standing rather than revoking it — a board change
  // must not quietly take an operator's actions away, and an older server that
  // never mentions it leaves the window with exactly its M21.1 shape.
  if (message.operator !== undefined) {
    isOperator = message.operator;
  }
  replaceCells(message.screen);
  drawSidebar();
  updateSidebar(message.hud);
  renderEvents(message.events);
  paintOverlay();
  drawScreen();
}

// applyWatchSnapshot renders a whole board this browser is not in (M22.1).
//
// The server sends one of these at the join and again whenever the room is
// created or replaced under us — the first player arriving on a board builds it
// and the last one leaving freezes it away — because an incremental diff stream
// has no past to build on across that boundary.
//
// Everything a player's snapshot claims is deliberately dropped: no resume token
// (a watcher has no run to reclaim), no playerId or statId (no stat), no HUD (no
// inventory), no events (nothing a watcher could answer). The roster IS kept:
// it is what the M19.1 tints are painted from, and a watcher sees the room's
// people exactly as the people in it do.
function applyWatchSnapshot(message: SnapshotMessage) {
  mode = "watching";
  watching = true;
  if (replaying) {
    replayTick = message.tick ?? replayTick;
  }
  setEditorBlinking(false);
  playerId = 0;
  myStatId = -1;
  roster = message.players ?? [];
  watcherCount = message.watchers ?? 0;
  replaceCells(message.screen);
  drawWatchSidebar();
  paintOverlay();
  drawScreen();
}

function applyWatchDiff(message: DiffMessage) {
  if (replaying) {
    replayTick = message.tick ?? replayTick;
  }
  // trackMyStatId keeps the roster the tints are drawn from; it looks for a
  // player id we do not have and simply finds none, which is what a watcher is.
  trackMyStatId(message.players);
  if (message.cells) {
    for (const cell of message.cells) {
      setBoardCell(cell);
    }
  }
  // An absent count means nobody, not "unchanged": this field rides every frame
  // (M22.1), and the one message that carries nothing else is the one that
  // exists to say the count moved on a board where nothing is running.
  watcherCount = message.watchers ?? 0;
  drawWatchSidebar();
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
  // M19.1: the same pass keeps the whole roster, not just our own row — every
  // other player's square is what the color tint is painted on.
  roster = players;
  if (mode === "playing") {
    tryShowFirstTimeHint("players");
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
  if (mode === "watching") {
    applyWatchDiff(message);
    return;
  }
  playBoardId = message.boardId;
  if (challengeMode && !challengeFinished) {
    challengeElapsed += 1;
  }
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
  cellsRevision += 1;
  for (const cell of cells) {
    cell.ch = 32;
    cell.color = 0x1f;
    cell.element = 0;
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
  // Normalised here rather than at every reader: `element,omitempty` means an
  // absent field is zero, and a cell object that carries `undefined` instead
  // would make every reader repeat the `?? 0`.
  cell.element = cell.element ?? 0;
  cells[cell.y * COLS + cell.x] = cell;
  cellsRevision += 1;
}

// The on-screen control bar mirrors the screen behind it: gameplay controls
// (Fire/Torch/Pause) only while a room is being played, the title menu's World /
// Play only on the title, and neither behind an open window — a Fire tap is a
// space, and behind a text surface a space belongs in the buffer rather than in
// the game. drawScreen is the one place every mode and modal change already
// passes through, so hanging the sync here is what keeps the bar from falling
// out of step with a screen it does not own. A no-op when there is no bar.
//
// A yes/no prompt is its own mode (M21.5): it is the one window that answers to
// neither Enter nor the pad, so it is where the bar offers Yes and No instead.
function syncTouchControls() {
  touchControls?.setMode(modal ? (modal.kind === "yesno" ? "prompt" : "modal") : mode);
}

function drawScreen() {
  syncTouchControls();
  if (fontCanvases.length < 16) {
    return;
  }
  const comfort = readEffectiveComfort();
  // In 3D the board columns are a window: cleared to transparent so the scene
  // behind this canvas shows through. Everything drawn OVER the board is still
  // painted here, on top of the world -- a text window, a notice, and the board
  // message the view deliberately lifted out of the scene so it can be read as
  // text rather than stood up as a row of letter-cards.
  const board3d = view3dOn();
  const lifted = board3d && view3d ? view3d.liftedCells() : EMPTY_CELL_SET;
  feedView3D();
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
    if (comfort.reduceFlashing && (ch === 0x01 || ch === CHAR_PLAYER)) {
      ch = CHAR_PLAYER;
      color = COLOR_PLAYER;
    }
    let fg = color & 0x0f;
    const bg = (color >> 4) & 0x0f;
    const x = base.x * CELL_W;
    const y = base.y * CELL_H;
    if (board3d && base.x < BOARD_COLS && !over && !lifted.has(i)) {
      screenCtx.clearRect(x, y, CELL_W, CELL_H);
      continue;
    }

    // M19.1: a player's 24-bit background cannot be expressed as a bg nibble,
    // so it is consulted here, after the overlay has had its say, and only
    // where the cell being painted is STILL the vanilla player — so a modal, a
    // scroll or a fade drawn over that square suppresses the tint for free.
    // The glyph stays on the existing path: always the white font canvas, which
    // already exists (playerTintForeground — a white ☻ is the player and only
    // the player, whatever it is standing on).
    const tint = playerTints.get(i);
    if (tint !== undefined && ch === CHAR_PLAYER && color === COLOR_PLAYER) {
      screenCtx.fillStyle = tint;
      fg = playerTintForeground(tint);
    } else {
      screenCtx.fillStyle = paletteColor(comfort.palette, bg);
    }
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

  drawSignReadout(board3d);
}

/**
 * A sign is a wall you can read, and it stays in the world: taking the signs
 * out of a board leaves a board that is not the board. But a sign is a row of
 * letters lying on the floor, and from eye height a row of letters is edge-on
 * and unreadable, so the one you are standing at reads itself out along the
 * bottom of the board -- and only that one, because a list of every sign in
 * sight is a menu rather than a place.
 *
 * Drawn after the cell loop, straight onto the canvas: it belongs to no square,
 * so it is not in `cells`, and it must not be in `overlay` either -- that map is
 * the client's window layer, rebuilt by paintOverlay, and a sign written there
 * would be a text window's business.
 */
function drawSignReadout(board3d: boolean) {
  if (!board3d || view3d === null || !view3d.firstPerson) {
    return;
  }
  const sign = view3d.signAtEye();
  if (!sign) {
    return;
  }
  const lines = sign.lines.slice(0, SIGN_READOUT_LINES);
  const top = ROWS - lines.length;
  for (let i = 0; i < lines.length; i += 1) {
    const text = lines[i].slice(0, BOARD_COLS - 2);
    const x0 = Math.max(0, Math.floor((BOARD_COLS - text.length) / 2));
    for (let j = 0; j < text.length; j += 1) {
      const ch = text.charCodeAt(j) & 0xff;
      const px = (x0 + j) * CELL_W;
      const py = (top + i) * CELL_H;
      screenCtx.fillStyle = paletteColor(readEffectiveComfort().palette, SIGN_READOUT_BG);
      screenCtx.fillRect(px, py, CELL_W, CELL_H);
      screenCtx.drawImage(
        fontCanvases[SIGN_READOUT_FG],
        (ch % GLYPH_COLS) * CELL_W,
        Math.floor(ch / GLYPH_COLS) * CELL_H,
        CELL_W,
        CELL_H,
        px,
        py,
        CELL_W,
        CELL_H
      );
    }
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

let chatMessages: { from: string; playerId?: number; text: string; private?: boolean; outgoing?: boolean; to?: string }[] = [];
// M21.1: the ids this player has asked not to hear, as the server has confirmed
// them. It is a mirror of the server's own set, never the authority: the roster
// window reads it to decide whether a row offers "block" or "unblock".
let blockedPlayerIds = new Set<number>();
let followedPlayerIds = new Set<number>();
// Whether the server told this connection it may moderate (M21.2). It decides
// only what the Players window offers; the allowlist behind it is checked again
// on the server for every action, so this is a display flag and nothing more.
let isOperator = false;
let currentChatMessage = "";
let currentChatTimer = 0;
let currentHintMessage = "";
let currentHintTimer = 0;

// A server announcement (currently the shutdown/save warning). Unlike chat it is
// a persistent banner across every mode — the player must not miss it — that
// clears itself a few seconds after the countdown it carries would elapse.
let serverAnnounce = "";
let serverAnnounceTimer = 0;

function handleAnnounceMessage(message: { text: string; seconds?: number }) {
  serverAnnounce = message.text;
  window.clearTimeout(serverAnnounceTimer);
  // Hold the banner past the announced deadline; a fresh announce (the 15s
  // countdown re-broadcast) resets it, and if the server dies mid-countdown the
  // banner still fades instead of sticking forever.
  const holdMs = ((message.seconds ?? 60) + 8) * 1000;
  serverAnnounceTimer = window.setTimeout(() => {
    serverAnnounce = "";
    paintOverlay();
    drawScreen();
  }, holdMs);
  paintOverlay();
  drawScreen();
}

function chatLineText(message: { from: string; text: string; private?: boolean; outgoing?: boolean; to?: string }): string {
  if (message.private) {
    return message.outgoing ? `[PM to ${message.to || "player"}] ${message.text}` : `[PM from ${message.from}] ${message.text}`;
  }
  return `<${message.from}> ${message.text}`;
}

function refreshOpenChatWindow() {
  if (modal && modal.kind === "chat") {
    modal.messages = chatMessages.map(chatLineText);
    paintOverlay();
    drawScreen();
  }
}

function handleChatMessage(message: { from: string; playerId?: number; text: string }) {
  chatMessages.push({ from: message.from, playerId: message.playerId, text: message.text });
  if (chatMessages.length > 50) {
    chatMessages.shift();
  }

  currentChatMessage = chatLineText(message);
  window.clearTimeout(currentChatTimer);
  currentChatTimer = window.setTimeout(() => {
    currentChatMessage = "";
    paintOverlay();
    drawScreen();
  }, 5000);

  if (modal && modal.kind === "chat") {
    refreshOpenChatWindow();
  } else {
    if (message.playerId !== undefined && message.playerId !== playerId) {
      tryShowFirstTimeHint("chat");
    }
    paintOverlay();
    drawScreen();
  }
}

function handlePrivateMessage(message: PrivateMessage) {
  const line = {
    from: message.from || "player",
    playerId: message.fromId,
    text: message.text,
    private: true,
    outgoing: message.outgoing === true,
    to: message.to,
  };
  chatMessages.push(line);
  if (chatMessages.length > 50) {
    chatMessages.shift();
  }
  currentChatMessage = chatLineText(line);
  window.clearTimeout(currentChatTimer);
  currentChatTimer = window.setTimeout(() => {
    currentChatMessage = "";
    paintOverlay();
    drawScreen();
  }, 5000);
  if (modal && modal.kind === "chat") {
    refreshOpenChatWindow();
  } else {
    paintOverlay();
    drawScreen();
  }
}

function seenHintsForThisBrowser() {
  if (authStatus.authenticated) {
    return accountPrefs?.hints ?? null;
  }
  return loadGuestHints(window.sessionStorage);
}

function tryShowFirstTimeHint(hint: AccountHintKey) {
  if (!accountPrefsLoaded) {
    return;
  }
  if (hint === "players" && !roster.some((player) => player.id !== playerId)) {
    return;
  }
  if (hintAlreadySeen(seenHintsForThisBrowser(), hint)) {
    return;
  }
  if (authStatus.authenticated) {
    const nextHints = { ...(accountPrefs?.hints ?? {}), [hint]: true };
    accountPrefs = {
      authenticated: true,
      stored: true,
      color: accountPrefs?.color ?? "",
      profile: accountPrefs?.profile ?? EMPTY_ACCOUNT_PROFILE,
      shareLocationWithFollowers: accountPrefs?.shareLocationWithFollowers === true,
      favoriteWorlds: accountPrefs?.favoriteWorlds ?? [],
      comfort: accountPrefs?.comfort ?? DEFAULT_COMFORT,
      hints: {
        players: nextHints.players === true,
        death: nextHints.death === true,
        chat: nextHints.chat === true,
      },
    };
    void saveAccountHint(fetch, hint).then((stored) => {
      if (stored) accountPrefs = stored;
    });
  } else {
    saveGuestHint(window.sessionStorage, hint);
  }
  currentHintMessage = FIRST_TIME_HINTS[hint];
  window.clearTimeout(currentHintTimer);
  currentHintTimer = window.setTimeout(() => {
    currentHintMessage = "";
    paintOverlay();
    drawScreen();
  }, 6000);
  paintOverlay();
  drawScreen();
}

function openChatWindow() {
  openModal({
    kind: "chat",
    title: "Global Chat",
    messages: chatMessages.map(chatLineText),
    buffer: "",
    onSubmit: (text) => {
      if (ws && ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: "chat", text }));
      }
    },
  });
}

// openBlockWindow is 'L' in play mode (M21.1): who is here, who has spoken, and
// which of them this player has stopped hearing.
//
// The candidates are the union of the board roster and the recent chat senders,
// because global chat crosses boards and worlds while the roster does not — the
// server only sends the players on the board this client is looking at, so the
// roster alone cannot offer the person who just said something from elsewhere
// (blocks.ts).
function openBlockWindow() {
  const candidates = blockCandidates({
    roster,
    chat: chatMessages,
    blocked: blockedPlayerIds,
    followed: followedPlayerIds,
    self: playerId,
  });
  const byLabel = new Map(candidates.map((candidate) => [blockRowLabel(candidate), candidate]));
  const header = blockWindowHeader(candidates);
  if (candidates.length === 0) {
    openWindow("Players", header, true);
    return;
  }
  openSelectList(
    "Players",
    [...byLabel.keys()],
    (label) => {
      const candidate = byLabel.get(label);
      if (!candidate) {
        return;
      }
      const blockVerb = candidate.blocked ? "Unblock" : "Block";
      const followVerb = candidate.followed ? "Unfollow" : "Follow";
      const choices = [
        { label: "View profile", action: "profile", confirm: "" },
        { label: "Private message", action: "pm", confirm: "" },
        ...(candidate.here ? [{ label: followVerb, action: candidate.followed ? "unfollow" : "follow", confirm: `${followVerb} ${candidate.name}? ` }] : []),
        ...(isOperator ? [] : [{ label: blockVerb, action: candidate.blocked ? "unblock" : "block", confirm: `${blockVerb} ${candidate.name}? ` }]),
        ...moderationChoices(candidate, isOperator),
      ];
      const byAction = new Map(choices.map((choice) => [choice.label, choice]));
      openSelectList(
        "Players",
        [...byAction.keys()],
        (picked) => {
          const choice = byAction.get(picked);
          if (!choice) {
            return;
          }
          if (choice.action === "profile") {
            sendProfileRequest(candidate.id);
            return;
          }
          if (choice.action === "pm") {
            openPrivateMessagePrompt(candidate.id, candidate.handle ? `@${candidate.handle}` : candidate.name);
            return;
          }
          openYesNo(choice.confirm, (yes) => {
            if (!yes) {
              return;
            }
            if (choice.action === "block" || choice.action === "unblock") {
              sendBlock(candidate.id, choice.action === "block");
              return;
            }
            if (choice.action === "follow" || choice.action === "unfollow") {
              sendFollow(candidate.id, choice.action === "follow");
              return;
            }
            sendModerate(candidate.id, choice.action);
          });
        },
        moderationHeader(candidate),
      );
    },
    header,
  );
}

function sendBlock(targetId: number, blocked: boolean) {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({ type: MessageTypeBlock, playerId: targetId, blocked }));
  }
}

function sendFollow(targetId: number, follow: boolean) {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({ type: MessageTypeFollow, playerId: targetId, follow }));
  }
}

function sendProfileRequest(targetId: number) {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({ type: MessageTypeProfileRequest, playerId: targetId }));
  }
}

function openPrivateMessagePrompt(targetId: number, label: string) {
  openPopupEntry(`PM ${label}:`, (text) => {
    if (text === null) return;
    sendPrivateMessage(targetId, text);
  });
}

function sendPrivateMessage(targetId: number, text: string) {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({ type: MessageTypePrivateMessage, playerId: targetId, text }));
  }
}

function handlePrivateResultMessage(message: PrivateResultMessage) {
  if (message.delivered) {
    return;
  }
  openWindow("Private Message", ["", `  ${message.text}`, ""], true);
}

// The block's confirmation, and the only message either party gets: the blocked
// player is told nothing at all. The server writes the line because the server is
// what knows which outcome happened — in particular whether the block is durable,
// which it is not when the target is a guest, and which the player must be told
// rather than left to discover tomorrow.
function handleBlockResultMessage(message: BlockResultMessage) {
  if (message.blocked) {
    blockedPlayerIds.add(message.playerId);
  } else {
    blockedPlayerIds.delete(message.playerId);
  }
  openWindow("Players", ["", `  ${message.text}`, ""], true);
}

function handleFollowResultMessage(message: FollowResultMessage) {
  if (message.playerId) {
    if (message.followed) {
      followedPlayerIds.add(message.playerId);
    } else {
      followedPlayerIds.delete(message.playerId);
    }
  }
  openWindow("Players", ["", `  ${message.text}`, ""], true);
}

function sendModerate(targetId: number, action: string) {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({ type: MessageTypeModerate, playerId: targetId, action }));
  }
}

// An operator's confirmation, and only the operator's: the server writes the
// line because the server is what knows which outcome happened — in particular
// whether a refusal actually bound to anything, which it does not for a guest.
function handleModerateResultMessage(message: ModerateResultMessage) {
  openWindow("Players", ["", `  ${message.text}`, ""], true);
}

// The other side of the same action, on the screen of the person it landed on. A
// sanction announces itself — that is what separates a mute from a block — and a
// notice that ended the session ends it here too, rather than letting the
// reconnect backoff walk a kicked player straight back in.
function handleModerationNoticeMessage(message: ModerationNoticeMessage) {
  // The announce banner: white-on-red at the top of every mode, so it survives
  // the trip back to the title screen below and is still there to be read.
  handleAnnounceMessage({ text: message.text, seconds: 20 });
  if (message.ended) {
    leaveToTitle();
    return;
  }
  paintOverlay();
  drawScreen();
}

function handleProfileResultMessage(message: ProfileResultMessage) {
  openWindow("Profile", message.lines.length > 0 ? message.lines : ["", "  No profile.", ""], true);
}

function handleReplayErrorMessage(message: ReplayErrorMessage) {
  handleAnnounceMessage({ text: message.text, seconds: 20 });
  leavingToTitle = true;
  window.clearTimeout(retryTimer);
  if (ws) {
    ws.close();
    ws = null;
  }
}

// ---------------------------------------------------------------------------
// M32.1 — the daily challenge
// ---------------------------------------------------------------------------

// openChallengeLanding is /challenge and /challenge/<id>: the landing, inside
// the playable client. It is a CP437 window over the title screen rather than a
// page of its own, because starting a run should feel like pressing P.
async function openChallengeLanding(id: string, options: { rememberPath?: boolean } = {}) {
  let resp: ChallengeResponse;
  try {
    const response = await fetch(id ? "/api/challenge/" + encodeURIComponent(id) : "/api/challenge");
    if (!response.ok) {
      showWorldsOnClose = true;
      // A bare /challenge with nothing scheduled is not a typo, and telling
      // somebody there is no challenge named "" reads like a broken link
      // rather than an empty calendar.
      const missing = id
        ? `  No challenge named "${id.slice(0, 20)}".`
        : "  There is no challenge running.";
      openWindow("Daily Challenge", ["", missing, "", "  Choose a world instead.", ""], true);
      return;
    }
    resp = (await response.json()) as ChallengeResponse;
  } catch {
    showWorldsOnClose = true;
    openWindow("Daily Challenge", ["", "  Not available: the server did not answer.", ""], true);
    return;
  }
  challengeLanding = resp;
  challengeID = resp.challenge.id;
  if (options.rememberPath !== false) {
    rememberChallengeInPath(resp.challenge.id);
  }
  if (!accountPrefsLoaded) {
    await refreshAuthStatus();
  }
  showChallengeLanding();
}

function showChallengeLanding() {
  const resp = challengeLanding;
  if (!resp) {
    return;
  }
  const lines = challengeLandingLines(resp, {
    signedIn: authStatus.authenticated,
    hasGhost: ghostTrackMatches(challengeGhost, resp.challenge),
  });
  openSelectListIfActions("Daily Challenge", lines, (choice) => {
    if (choice === "start") {
      startChallengeRun();
    } else if (choice === "board") {
      showChallengeLeaderboard();
    } else if (choice === "ghost") {
      startChallengeRun();
    }
  });
}

// openSelectListIfActions shows a window whose selectable rows are the "!key;"
// lines the challenge helpers build. A landing with no actions (an unavailable
// challenge) is a plain window, so Enter closes it instead of waiting for a
// selection that cannot be made.
function openSelectListIfActions(title: string, lines: string[], onPick: (choice: string) => void) {
  const firstAction = lines.findIndex((line) => line.startsWith("!"));
  if (firstAction < 0) {
    openWindow(title, lines, true);
    return;
  }
  openModal({
    kind: "text",
    // The cursor opens ON the first action, not on the prose above it: the text
    // window draws a viewport around linePos, so a cursor left at the top would
    // scroll the actions off the bottom of the frame — which is exactly what the
    // first run of the browser journey found.
    state: { title, lines, linePos: firstAction + 1, viewingFile: false },
    baseTitle: title,
    moved: false,
    selectable: true,
    requireSelection: true,
    onSelect: onPick,
  });
}

function showChallengeLeaderboard() {
  const rows = challengeLanding?.leaderboard ?? [];
  const lines = challengeLeaderboardLines(rows);
  openSelectListIfActions("Challenge Times", lines, (recordingId) => {
    const row = rows.find((entry) => entry.recordingId === recordingId);
    if (row) {
      showChallengeRow(row);
    }
  });
}

function showChallengeRow(row: ChallengeLeaderboardRow) {
  openSelectListIfActions("Run", challengeRowActionLines(row), (choice) => {
    if (!row.recordingId) {
      return;
    }
    if (choice === "watch") {
      // Leave the room first. connect() will not replace a socket that is still
      // open, so watching a replay from inside a run would show the replay
      // shell with no replay in it — and would leave the player standing in
      // their own attempt while they watched somebody else's.
      closeModal();
      disconnectSocket();
      startReplay(row.recordingId);
    } else if (choice === "postcard") {
      window.open(postcardURLForRun(row.recordingId), "_blank", "noopener");
    } else if (choice === "ghost") {
      void loadChallengeGhost(row.recordingId);
    }
  });
}

// loadChallengeGhost fetches a track and keeps it LOCAL. Nothing about it is
// told to the server, and a track the server refuses (a run from an older
// definition, or one no row cites) leaves the browser with no ghost rather than
// with a stale one.
async function loadChallengeGhost(recordingId: string) {
  try {
    const response = await fetch(
      "/api/challenge/ghost?challenge=" + encodeURIComponent(challengeID) + "&recording=" + encodeURIComponent(recordingId),
    );
    if (!response.ok) {
      challengeGhost = null;
      openWindow("Ghost", ["", "  " + ((await response.text()).trim() || "Ghost unavailable.").slice(0, 42), ""], true);
      return;
    }
    const track = (await response.json()) as ChallengeGhostTrack;
    challengeGhost = ghostTrackMatches(track, challengeLanding?.challenge ?? null) ? track : null;
  } catch {
    challengeGhost = null;
  }
  if (!challengeGhost) {
    openWindow("Ghost", ["", "  Ghost unavailable.", ""], true);
    return;
  }
  openSelectListIfActions(
    "Ghost",
    ["", "  " + ghostStatusLine(challengeGhost), "", "!start;Race it now", "!board;Back to the times", ""],
    (choice) => {
      if (choice === "start") {
        startChallengeRun();
      } else if (choice === "board") {
        showChallengeLeaderboard();
      }
    },
  );
}

// startChallengeRun joins a fresh attempt. It is deliberately the same connect()
// every other kind of play uses: the challenge is a query on the socket, not a
// second client.
function startChallengeRun() {
  if (!challengeLanding || !challengeLanding.challenge.available) {
    return;
  }
  closeModal();
  challengeMode = true;
  challengeRun = "";
  challengeElapsed = 0;
  challengeFinished = false;
  challengeID = challengeLanding.challenge.id;
  worldName = challengeLanding.challenge.world;
  disconnectSocket();
  startPlay({ challenge: true });
}

function handleChallengeResult(message: ChallengeResultMessage) {
  challengeFinished = true;
  // Refresh the landing so the leaderboard behind the result is the one this
  // run just changed, rather than the copy fetched before it started.
  void refreshChallengeLanding();
  openSelectListIfActions("Challenge Result", challengeResultLines(message), (choice) => {
    if (choice === "again") {
      startChallengeRun();
    } else if (choice === "board") {
      showChallengeLeaderboard();
    }
  });
}

async function refreshChallengeLanding() {
  if (!challengeID) {
    return;
  }
  try {
    const response = await fetch("/api/challenge/" + encodeURIComponent(challengeID));
    if (response.ok) {
      challengeLanding = (await response.json()) as ChallengeResponse;
    }
  } catch {
    // A landing we could not refresh is stale, not wrong: the result window
    // above is the server's own word on this run either way.
  }
}

function handleChallengeError(message: ChallengeErrorMessage) {
  challengeMode = false;
  challengeRun = "";
  leavingToTitle = true;
  window.clearTimeout(retryTimer);
  disconnectSocket();
  showWorldsOnClose = true;
  openWindow("Daily Challenge", ["", "  " + message.reason.slice(0, 42), ""], true);
}

// leaveChallenge forgets the run this browser was in. The ghost goes with it:
// a track is loaded for a challenge, and carrying one into ordinary play would
// draw a stranger's path over a world it was never recorded in.
//
// A full page reload orphans a run for the same reason — the run key lives only
// here — and that is the intended rule: the run idles out and is evicted, and
// nothing was scored, exactly as an abandon.
function leaveChallenge() {
  challengeMode = false;
  challengeRun = "";
  challengeGhost = null;
  challengeElapsed = 0;
  challengeFinished = false;
}

function disconnectSocket() {
  if (ws) {
    const socket = ws;
    ws = null;
    socket.close();
  }
}

function rememberChallengeInPath(id: string) {
  const path = challengePath(id);
  if (window.location.pathname !== path) {
    window.history.replaceState(null, "", path);
  }
}

function openReplayPostcard() {
  if (!replaying || !replayID) {
    return;
  }
  const ticks = 30;
  const start = Math.max(0, replayTick - Math.floor(ticks / 2));
  const url = new URL("/api/replay/postcard.gif", window.location.href);
  url.searchParams.set("id", replayID);
  url.searchParams.set("start", String(start));
  url.searchParams.set("ticks", String(ticks));
  url.searchParams.set("board", "1");
  url.searchParams.set("replay", replayLinkPath(replayID));
  window.open(url.toString(), "_blank", "noopener");
}

function openAccountMenu() {
  if (!authStatus.authenticated) {
    openSelectList("Account", ["Comfort settings", "Sign in"], (entry) => {
      if (entry === "Comfort settings") {
        openComfortMenu();
      } else if (authStatus.enabled) {
        window.location.href = "/api/auth/google/start?return=" + encodeURIComponent(window.location.pathname + window.location.search);
      }
    }, ["  Guest settings stay in this browser."]);
    return;
  }
  const sharing = accountPrefs?.shareLocationWithFollowers === true;
  const favorite = accountPrefs?.favoriteWorlds.includes(worldName) === true;
  const entries = [favorite ? "Unfavorite this world" : "Favorite this world", "Edit profile", "Comfort settings", sharing ? "Hide my location" : "Share my location", "Sign out"];
  openSelectList("Account", entries, (entry) => {
    if (entry === "Sign out") {
      void fetch("/api/auth/logout", { method: "POST" }).then(() => refreshAuthStatus());
      return;
    }
    if (entry === "Favorite this world" || entry === "Unfavorite this world") {
      const next = entry === "Favorite this world";
      void saveFavoriteWorld(fetch, worldName, next).then((stored) => {
        if (!stored) {
          openWindow("Account", ["", "  Favorite was not saved.", ""], true);
          return;
        }
        accountPrefs = stored;
        openWindow("Account", ["", `  ${next ? "Favorited" : "Unfavorited"} ${worldName}.`, ""], true);
      });
      return;
    }
    if (entry === "Edit profile") {
      openProfileEditor();
      return;
    }
    if (entry === "Comfort settings") {
      openComfortMenu();
      return;
    }
    if (entry === "Share my location" || entry === "Hide my location") {
      const next = entry === "Share my location";
      void saveShareLocationWithFollowers(fetch, next).then((stored) => {
        if (!stored) {
          openWindow("Account", ["", "  Location setting was not saved.", ""], true);
          return;
        }
        accountPrefs = stored;
        openWindow("Account", ["", `  Friend location ${next ? "shared" : "hidden"}.`, ""], true);
      });
    }
  });
}

function comfortLines(comfort: ComfortPreferences): string[] {
  return [
    `Key preset: ${comfort.keyPreset}`,
    "Bind Up key",
    "Bind Torch key",
    "Reset bindings",
    `Reduce flashing: ${comfort.reduceFlashing ? "on" : "off"}`,
    `Palette: ${comfort.palette}`,
  ];
}

function openComfortMenu() {
  const comfort = readEffectiveComfort();
  openSelectList("Comfort", comfortLines(comfort), (entry) => {
    const next = normalizeComfortPreferences(comfort);
    if (entry.startsWith("Key preset:")) {
      next.keyPreset = next.keyPreset === "vanilla" ? "one-handed" : next.keyPreset === "one-handed" ? "custom" : "vanilla";
      if (next.keyPreset !== "custom") {
        next.keyBindings = {};
      }
      writeEffectiveComfort(next);
      openComfortMenu();
      return;
    }
    if (entry === "Bind Up key") {
      openBindingPrompt("up");
      return;
    }
    if (entry === "Bind Torch key") {
      openBindingPrompt("torch");
      return;
    }
    if (entry === "Reset bindings") {
      next.keyPreset = "vanilla";
      next.keyBindings = {};
      writeEffectiveComfort(next);
      openComfortMenu();
      return;
    }
    if (entry.startsWith("Reduce flashing:")) {
      next.reduceFlashing = !next.reduceFlashing;
      writeEffectiveComfort(next);
      openComfortMenu();
      return;
    }
    if (entry.startsWith("Palette:")) {
      next.palette = next.palette === "vanilla" ? "high-contrast" : next.palette === "high-contrast" ? "colorblind-assist" : "vanilla";
      writeEffectiveComfort(next);
      openComfortMenu();
    }
  }, authStatus.authenticated ? [] : ["  Guest settings stay in this browser."]);
}

function openProfileEditor() {
  if (!authStatus.authenticated) {
    openWindow("Profile", ["", "  Sign in to keep a profile.", ""], true);
    return;
  }
  const current = accountPrefs?.profile ?? EMPTY_ACCOUNT_PROFILE;
  openPopupEntry(`Handle @${current.handle || ""}:`, (handle) => {
    if (handle === null) return;
    openPopupEntry(`Name ${current.displayName || authDisplayName()}:`, (displayName) => {
      if (displayName === null) return;
      openPopupEntry(`About ${current.about[0] || ""}:`, (about) => {
        if (about === null) return;
        const profile: AccountProfilePreferences = {
          handle: handle.trim(),
          displayName: displayName.trim(),
          about: about.trim() ? [about.trim()] : [],
        };
        void saveAccountProfile(fetch, profile).then((stored) => {
          if (!stored) {
            openWindow("Profile", ["", "  Profile was not saved.", "  That handle may be taken.", ""], true);
            return;
          }
          accountPrefs = stored;
          if (mode === "title") {
            redrawTitleSidebar();
            paintOverlay();
            drawScreen();
          }
          openWindow("Profile", ["", "  Profile saved.", ""], true);
        });
      });
    });
  });
}

// readStoredPlayerColor is this browser's answer to "what color is my ☻": the
// account's, for a signed-in player who has chosen one, and this browser's own
// localStorage pick otherwise (M19.1, then M19.3). Everything that needs the
// color reads it through here — the join, the title swatch, and the picker's
// starting row — so the three cannot drift apart. A stored value that is not a
// "#RRGGBB" triple, from either source, is read as no pick at all rather than
// sent on to other people's canvases.
function readStoredPlayerColor(): string {
  return effectivePlayerColor({ account: accountPrefs, local: loadPlayerColor(window.localStorage) });
}

function readEffectiveComfort(): ComfortPreferences {
  return effectiveComfort({ account: accountPrefs, local: loadGuestComfort(window.localStorage) });
}

function writeEffectiveComfort(next: ComfortPreferences) {
  const comfort = normalizeComfortPreferences(next);
  const invalid = validateComfortPreferences(comfort);
  if (invalid) {
    openWindow("Comfort", ["", "  Key binding conflict.", ""], true);
    return;
  }
  if (authStatus.authenticated) {
    accountPrefs = {
      authenticated: true,
      stored: true,
      color: accountPrefs?.color ?? "",
      hints: accountPrefs?.hints ?? { players: false, death: false, chat: false },
      profile: accountPrefs?.profile ?? EMPTY_ACCOUNT_PROFILE,
      shareLocationWithFollowers: accountPrefs?.shareLocationWithFollowers === true,
      favoriteWorlds: accountPrefs?.favoriteWorlds ?? [],
      comfort,
    };
    void saveAccountComfort(fetch, comfort).then((stored) => {
      if (stored) {
        accountPrefs = stored;
        refreshFontPalette();
        paintOverlay();
        drawScreen();
      }
    });
  } else {
    saveGuestComfort(window.localStorage, comfort);
  }
  refreshFontPalette();
  setPaused(paused);
  setEditorBlinking(mode === "editor");
  paintOverlay();
  drawScreen();
}

// openColorPicker is the title menu's ' C ' (M19.2). The pick is stored and
// nothing else happens: the color is read again at every join (see connect()),
// so it reaches the room the next time P is pressed, and a pick made after a
// drop reaches the reconnect — without a rejoin being anyone's problem.
//
// Where it is stored is M19.3's half. A signed-in player's pick goes to their
// account, so it is waiting for them in the next world and the next browser; a
// guest keeps localStorage, having no durable identity to key on. The two are
// deliberately not written together: a signed-in player who signs out falls
// back to whatever this browser picked as a guest, rather than to a copy of an
// account preference they may no longer want.
function openColorPicker() {
  openModal(
    newColorPickerModal(readStoredPlayerColor(), (color) => {
      if (color === null) {
        return; // Escape: the window closes and nothing has changed.
      }
      if (authStatus.authenticated) {
        // Held optimistically so the swatch and the next join reflect the pick
        // immediately; the server's answer replaces it when it lands, and a
        // failed write leaves the optimistic value rather than silently
        // reverting under the player.
        accountPrefs = {
          authenticated: true,
          stored: true,
          color,
          hints: accountPrefs?.hints ?? { players: false, death: false, chat: false },
          profile: accountPrefs?.profile ?? EMPTY_ACCOUNT_PROFILE,
          shareLocationWithFollowers: accountPrefs?.shareLocationWithFollowers === true,
          favoriteWorlds: accountPrefs?.favoriteWorlds ?? [],
          comfort: accountPrefs?.comfort ?? DEFAULT_COMFORT,
        };
        void saveAccountColor(fetch, color).then((stored) => {
          if (stored) {
            accountPrefs = stored;
          }
        });
      } else if (color) {
        savePlayerColor(window.localStorage, color);
      } else {
        clearPlayerColor(window.localStorage);
      }
      // The menu row's swatch is drawn from the stored value, so redraw it.
      if (mode === "title") {
        drawTitleSidebar(
          writeText,
          titleFriendlyName,
          authDisplayName(),
          authStatus.enabled,
          serverOccupancy,
          readStoredPlayerColor(),
        );
      }
    }),
  );
}

// repaintPlayerTints rebuilds the M19.1 color layer from the live roster and
// the cells the server drew. It is rebuilt with the overlay because the two
// have the same lifetime — one message changes both — and it is empty outside a
// room, where there is no roster and the board is a title screen.
function repaintPlayerTints() {
  playerTints.clear();
  // M19.2: the title menu's ' C ' row and the picker's preview are the same
  // paint as the board — a ☻ the server would have drawn (char 2 in 0x1F) with
  // a 24-bit background over it. Going through the one override rather than a
  // second drawing path is what stops a preview from promising a color the
  // game would not actually give you.
  if (mode === "title") {
    const stored = readStoredPlayerColor();
    if (stored) {
      playerTints.set(TITLE_COLOR_SWATCH.y * COLS + TITLE_COLOR_SWATCH.x, stored);
    }
  }
  if (modal && modal.kind === "colorPicker") {
    const preview = colorPickerPreview(modal);
    if (preview) {
      playerTints.set(preview.y * COLS + preview.x, preview.rgb);
    }
  }
  if (mode !== "playing") {
    return;
  }
  for (const tint of playerTintCells({ roster, cells, boardCols: BOARD_COLS })) {
    playerTints.set(tint.y * COLS + tint.x, tint.rgb);
  }
}

// paintOverlay rebuilds the modal layer from scratch each frame, so a modal
// never has to restore what was underneath it. The pause layer goes underneath
// the modal: a paused player can still have a scroll open over the board.
function paintOverlay() {
  overlay.clear();
  repaintPlayerTints();
  // Server announcements (the shutdown/save warning) sit at the very top in
  // white-on-red across every mode — losing this one is losing your game.
  if (serverAnnounce) {
    writeOverlay(0, 0, 0x4f, serverAnnounce.slice(0, 60).padEnd(60, " "));
  }
  if (mode === "playing") {
    if (currentHintMessage) {
      writeOverlay(0, 24, 0x1e, currentHintMessage.slice(0, 60).padEnd(60, " "));
    } else if (currentChatMessage) {
      writeOverlay(0, 24, 0x1e, currentChatMessage.slice(0, 60).padEnd(60, " "));
    }
    // M32.1: the ghost. It is one overlay cell drawn over this browser's canvas
    // at the position the recorded run held after the same number of ticks —
    // local, read-only, and invisible to everyone else in the room, because it
    // is painted here rather than sent anywhere.
    if (challengeMode && challengeGhost) {
      const ghost = ghostOverlayCell(challengeGhost, challengeElapsed, playBoardId);
      // Never over this browser's own ☻: a ghost standing where you are (the
      // shared spawn tile, every run) would hide you rather than race you.
      const onMe = ghost !== null && ghost.x + 1 === myX && ghost.y + 1 === myY;
      if (ghost && !onMe && ghost.x >= 0 && ghost.x < BOARD_COLS) {
        overlay.set(ghost.y * COLS + ghost.x, { ch: ghost.ch, color: ghost.color });
      }
    }
  }
  if (mode === "editor") {
    // The cross cursor and collaborator markers blink off on phase 0 so the
    // board tile (and any object beneath) shows through — see editor_cursor.ts.
    const cells = editorCursorOverlay({
      blink: editorBlink,
      cursor: editorCursor,
      presence: editorPresence,
      selfId: editorMemberId,
      boardCols: BOARD_COLS,
      rows: ROWS,
      // Our own board: editorProperties only tracks snapshots that are ours,
      // so this is the board we are actually viewing (M17.12).
      boardId: editorProperties.boardId,
    });
    for (const cell of cells) {
      writeOverlay(cell.x, cell.y, cell.color, cell.text);
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
    if (!reducedBlinkOn(readEffectiveComfort())) {
      pauseTimer = window.setInterval(() => {
        pauseBlink = !pauseBlink;
        paintOverlay();
        drawScreen();
      }, PAUSE_BLINK_MS);
    }
  }
  paintOverlay();
  drawScreen();
}

// setEditorBlinking mirrors setPaused: an interval drives the editor cursor's
// 3-phase blink while in editor mode and is torn down on the way out. Idempotent
// so the several editor-entry paths (startEditor, editor snapshots) can all call
// it without stacking timers.
function setEditorBlinking(on: boolean) {
  if (on) {
    if (editorBlinkTimer) return;
    editorBlink = EDITOR_BLINK_PHASES - 1; // start on a cursor-shown phase
    if (reducedBlinkOn(readEffectiveComfort())) return;
    editorBlinkTimer = window.setInterval(() => {
      editorBlink = (editorBlink + 1) % EDITOR_BLINK_PHASES;
      paintOverlay();
      drawScreen();
    }, EDITOR_BLINK_MS);
    return;
  }
  window.clearInterval(editorBlinkTimer);
  editorBlinkTimer = 0;
  editorBlink = EDITOR_BLINK_PHASES - 1;
}

function openModal(next: Modal) {
  stopHeldInput();
  modal = next;
  mobileTextInput.sync(modal, routeMobileModalInput);
  paintOverlay();
  drawScreen();
}

function openBindingPrompt(action: KeyAction) {
  bindingCaptureAction = action;
  openWindow("Comfort", ["", `  Press a key for ${action}.`, "", "  Esc cancels.", ""], true);
}

function routeBindingCapture(event: KeyboardEvent): boolean {
  if (!bindingCaptureAction) {
    return false;
  }
  event.preventDefault();
  if (event.code === "Escape") {
    bindingCaptureAction = "";
    closeModal();
    openComfortMenu();
    return true;
  }
  if (!event.code || event.ctrlKey || event.metaKey || event.altKey) {
    return true;
  }
  const comfort = normalizeComfortPreferences(readEffectiveComfort());
  comfort.keyPreset = "custom";
  comfort.keyBindings = { ...comfort.keyBindings, [bindingCaptureAction]: [event.code] };
  const invalid = validateComfortPreferences(comfort);
  const action = bindingCaptureAction;
  bindingCaptureAction = "";
  closeModal();
  if (invalid) {
    openWindow("Comfort", ["", `  ${event.code} conflicts.`, ""], true);
    return true;
  }
  writeEffectiveComfort(comfort);
  openWindow("Comfort", ["", `  ${action} bound to ${event.code}.`, ""], true);
  return true;
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
    onSelect: (label) => selectScrollReply(replyStatId, label),
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
// A selected hyperlink already sent its label. closeModal still owns queue
// advancement, but must not follow a real choice with an empty dismiss reply.
let scrollReplySent = false;

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
  scrollReplySent = false;
  openWindow(scroll.title, scroll.lines, false, scroll.statId);
}

function clearScrolls() {
  scrollQueue = [];
  openScrollStatId = -1;
  scrollReplySent = false;
}

function closeModal() {
  bindingCaptureAction = "";
  const scrollStatId = openScrollStatId;
  const replySent = scrollReplySent;
  openScrollStatId = -1;
  scrollReplySent = false;
  modal = null;
  if (mobileTextInput.close()) {
    canvas.focus();
  }
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
    if (!replySent) {
      sendScrollReply(scrollStatId, "");
    }
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
  if (showWorldsOnClose) {
    showWorldsOnClose = false;
    void showWorlds();
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

function selectScrollReply(statId: number, label: string) {
  // The immediate close after Enter is still responsible for opening the next
  // queued scroll. Mark this reply so that closeModal does not send a second,
  // empty reply to the same object.
  scrollReplySent = true;
  sendScrollReply(statId, label);
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
  drawView3DRows();
}

/**
 * The 3D view's own sidebar rows.
 *
 * Rows 13 and 24 are the two vanilla leaves blank (GAME.PAS:1441-1455 writes
 * 14-19 and 21-23), and row 20 is already spent on Players. Row 13 carries the
 * toggle, always, because a feature nobody can see does not exist -- this view
 * shipped without a row and nobody could have found it. Row 24 carries the
 * camera keys, and only while you are in the world, where they mean something.
 *
 * The label names where 3 GOES, not where you are: the row reads as an
 * instruction, like every other row in this sidebar.
 */
function drawView3DRows() {
  if (mode !== "playing") {
    return;
  }
  const inWorld = view3dOn();
  sidebarClearLine(writeText, 13);
  writeText(62, 13, 0x30, " 3 ");
  // "Standard view", not "classic" or "text": the name a player should see for
  // the view they already know. The mode is still called `classic` in the code
  // and in the ?view= parameter, where it names a camera rather than a product.
  writeText(65, 13, 0x1f, (inWorld ? " Standard view" : " 3D view").padEnd(14, " "));
  sidebarClearLine(writeText, 24);
  if (inWorld) {
    writeText(61, 24, 0x30, " WASD ");
    writeText(67, 24, 0x1f, " Look");
  }
  // Row 21 is vanilla's " S  Save game", and in the world S looks down instead,
  // so the row has to stop promising something the key no longer does. It is
  // the one binding the 3D view takes away, and the sidebar is where a player
  // finds out -- not by pressing S and watching the ceiling move.
  writeText(62, 21, 0x70, " S ");
  writeText(65, 21, 0x1f, (inWorld ? " Save: press 3" : " Save game").padEnd(14, " "));
}

function updateSidebar(hud: HudSnapshot) {
  zztSound.setEnabled(hud.soundEnabled);
  paintSidebarHud(writeText, hud);
}

function drawWatchSidebar() {
  paintWatchSidebar(writeText, watcherCount, replaying, watchLiveLabel);
}

// M17.7: the first time an in-game sound arrives that the browser cannot voice,
// say why in the console instead of failing silently. "not running" means the
// AudioContext never unlocked (autoplay policy / no gesture reached unlock);
// "disabled" means hud.soundEnabled muted the synth (the 'B' toggle).
let soundUnplayableWarned = false;
function warnIfSoundUnplayable() {
  if (soundUnplayableWarned) {
    return;
  }
  const d = zztSound.diagnostics();
  if (d.contextState !== "running" || !d.enabled) {
    soundUnplayableWarned = true;
    console.warn("ZZTMMO: a sound event arrived but audio is not playable:", d);
  }
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
      break;
    case "pause":
      // Pause is per-player: a PauseEvent for somebody else's stat must not
      // draw "Pausing..." on our screen.
      if (isMine(event)) {
        setPaused(event.paused ?? false);
      }
      break;
    case "sound":
      warnIfSoundUnplayable();
      zztSound.queue(event.priority ?? 0, soundNotesFromProtocol(event.notes));
      break;
    case "walkClick":
      // Bypasses queue() entirely: a currently-playing melody preempts it, the
      // way vanilla's SoundIsPlaying gated the original direct Sound(110) poke.
      warnIfSoundUnplayable();
      zztSound.click(event.freqHz ?? 0);
      break;
    case "transfer":
      appendLog(`transfer to board ${event.toBoard ?? "?"}`);
      break;
    case "death":
      if (isMine(event)) {
        tryShowFirstTimeHint("death");
      }
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

// The page is the ZZT screen only — there is no on-page event log. Events with
// no vanilla presentation (transfer, death, respawn) go to the devtools console
// instead.
function appendLog(text: string) {
  console.debug("[zzt]", text);
}

// routeModalKey consumes a key on behalf of the open modal. A modal swallows
// EVERY key: even one it ignores must not reach gameplay or the title menu.
function routeModalKey(event: KeyboardEvent) {
  event.preventDefault();
  const previous = modal;
  const result = handleModalKey(previous!, event);
  finishModalInput(previous, result);
}

// routeMobileModalInput receives committed `input`/composition text from the
// hidden native control. It deliberately bypasses keydown — Android reports
// keyCode 229 while composing — then returns through the exact same close and
// redraw path as desktop keyboard input.
function routeMobileModalInput(input: ModalTextInput) {
  if (!modal) {
    return;
  }
  const previous = modal;
  const result = handleModalTextInput(previous, input);
  finishModalInput(previous, result);
}

function finishModalInput(previous: Modal | null, result: "close" | "redraw" | "ignore") {
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
      if (authStatus.authenticated || authStatus.enabled) {
        openAccountMenu();
      }
      break;
    case "world":
      void showWorlds();
      break;
    case "color":
      openColorPicker();
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
    case "feedback":
      showHelp("BETA.HLP", "ZZTMMO Beta");
      break;
    case "restore":
      void showSavedGames();
      break;
    case "quit":
      openYesNo("Quit ZZT? ", (yes) => {
        if (yes) {
          forgetWorldInPath();
          drawConnectionNotice("Thanks for playing ZZT!");
        }
      });
      break;
  }
}

function handleKeyDown(event: KeyboardEvent) {
  zztSound.resume();

  if (routeBindingCapture(event)) {
    return;
  }

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

  // M22.1: a watcher answers to one key. Everything else returns here rather
  // than falling through, so no command byte, no movement mask and no letter
  // window can be reached from a screen whose whole claim is that it changes
  // nothing — the server would drop them, and a control that is silently
  // ignored is how a watcher learns the client is broken.
  if (mode === "watching") {
    if (event.code === "Escape" || event.code === "KeyQ") {
      event.preventDefault();
      leaveToTitle();
    } else if (watchLive && event.code === "KeyN") {
      event.preventDefault();
      nextWatchLive();
    } else if (replaying && event.code === "KeyP") {
      event.preventDefault();
      ws?.send(JSON.stringify({ type: MessageTypeReplayControl, op: "pause" }));
    } else if (replaying && event.code === "KeyR") {
      event.preventDefault();
      ws?.send(JSON.stringify({ type: MessageTypeReplayControl, op: "restart" }));
    } else if (replaying && event.code === "KeyS") {
      event.preventDefault();
      openReplayPostcard();
    }
    return;
  }

  // M35: the board with depth, or the text screen. 3 is the shortcut and the
  // one the sidebar advertises; V still works for the hands that learned it
  // first. Both are free here, where C is chat and L is the players list, and
  // neither reaches the engine's key switch.
  if (event.code === "Digit3" || event.code === "Numpad3" || event.code === "KeyV") {
    event.preventDefault();
    view3d?.leaveGhost();
    void toggleView3D();
    return;
  }
  if (view3dOn() && (event.code === "KeyF" || event.code === "KeyG")) {
    event.preventDefault();
    stopHeldInput();
    if (event.code === "KeyF") {
      view3d?.standUp();
    } else {
      view3d?.toggleGhost();
    }
    drawScreen();
    return;
  }

  if (event.code === "KeyC") {
    event.preventDefault();
    stopHeldInput();
    openChatWindow();
    return;
  }

  // M21.1: 'L' lists the people you could stop hearing. Handled here, like 'C',
  // so it never reaches the engine's key switch — and 'L' specifically because
  // it is the one letter this decision could take: W/A/D are inert by an
  // explicit decision the M16.10 vocabulary asserts (input.play-wasd-removed),
  // and every other letter on the sidebar is vanilla's.
  if (event.code === "KeyL") {
    event.preventDefault();
    stopHeldInput();
    openBlockWindow();
    return;
  }

  // WASD swings and tilts the camera in the 3D view, and does nothing at all
  // outside it. Taken before the command lookup, because S is ZZT's save key
  // and in the world it has to look down instead; on the text screen this
  // branch never runs and S saves exactly as it always has.
  if (view3dOn() && view3d !== null && isCameraKey(event.code) && !event.ctrlKey && !event.metaKey && !event.altKey) {
    const step = lookStepFor(event.code);
    if (step) {
      event.preventDefault();
      view3d.look(step.dyaw, step.dpitch);
      return;
    }
  }

  const bindings = effectiveKeyBindings(readEffectiveComfort());
  if (event.repeat && !isMovementKey(event.code, bindings)) {
    return;
  }

  // Command keys travel as a raw key byte, not a movement mask.
  const command = commandKey(event, bindings);
  if (command !== 0) {
    event.preventDefault();
    stopHeldInput();
    sendKey(command);
    return;
  }

  const handled = updatePressed(event, true);
  if (handled) {
    event.preventDefault();
    sendInput(currentMask(), rawKey(event.code, bindings));
  }
}

function handleKeyUp(event: KeyboardEvent) {
  if (modal || mode === "title" || mode === "editor" || mode === "watching") {
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
  // The legend is rebuilt on every sidebar draw so it tracks members joining,
  // leaving, and switching boards while it is open (M17.10).
  const presenceList: SidebarPresenceList | null = editorPresencePanel
    ? editorPresenceLegend({
        presence: editorPresence,
        selfId: editorMemberId,
        boardId: editorProperties.boardId,
      })
    : null;
  drawEditorSidebar(writeText, editorInspect, editorBrush, editorDrawing, editorTextMode, editorCategoryMenu, actionMenu, statPrompt, presenceList);
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
  // Escape closes the collaborator legend before it means "leave the editor" —
  // the panel covers the command block, so dismissing it is what Escape reads as
  // while it is up.
  if (editorPresencePanel && event.code === "Escape") {
    event.preventDefault();
    editorPresencePanel = false;
    redrawEditor();
    return;
  }
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
    case "KeyW":
      event.preventDefault();
      editorPresencePanel = !editorPresencePanel;
      redrawEditor();
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

// editorBoardName is EditorGetBoardName with titleScreenIsNone TRUE (editor.go:
// 854): the board-edge rows and a passage's "Room:" readout call the title board
// "None". The wire list carries board 0 under its real name so the board
// SWITCHER — vanilla's one titleScreenIsNone-false caller — can still offer it.
function editorBoardName(id: number): string {
  if (id === 0) return "None";
  return editorProperties.boards.find((board) => board.id === id)?.name ?? "None";
}

// editorBoardEntries is EditorSelectBoard's list body (editor.go:875-877): every
// board 0..BoardCount, named by EditorGetBoardName under the caller's own
// titleScreenIsNone. The "id: " prefix is this client's, so a picked entry can
// be resolved back to a board number.
function editorBoardEntries(titleScreenIsNone: boolean): string[] {
  return editorProperties.boards.map(
    (board) => `${board.id}: ${titleScreenIsNone && board.id === 0 ? "None" : board.name}`,
  );
}

function openEditorSidebarMenu(menu: Omit<EditorSidebarMenu, "selected"> & { selected?: number }) {
  editorCategoryMenu = null;
  editorPresencePanel = false;
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
  // Both overlay sidebar rows 3-20; the picker is the one waiting on a key.
  editorPresencePanel = false;
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
  const entries = editorBoardEntries(true);
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
  const entries = editorBoardEntries(true);
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
  // titleScreenIsNone is FALSE here (editor.go:668-669), so board 0 is listed
  // under its own name and switches like any other. It used to be filtered out,
  // which left an author who moved off a world's first board with no way back
  // (M16.13a).
  const entries = editorBoardEntries(false);
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
  editorRestore = null;
  window.clearTimeout(retryTimer);
  // Test play exits the session on purpose; its membership token goes with it
  // (M16.14f), so returning to the editor afterwards enters fresh.
  clearEditorToken(window.sessionStorage, worldName);
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
  if (!isHandledKey(event.code, effectiveKeyBindings(readEffectiveComfort()))) {
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
  // The 3D view changes nothing here. The arrows are board directions in every
  // view -- north is north whichever way the camera happens to be pointing --
  // and WASD moves the camera without ever reaching this mask.
  return movementMask(pressed, effectiveKeyBindings(readEffectiveComfort()));
}

function sendInput(mask: number, key = 0) {
  // M22.1: said out loud rather than left to `playerId === 0` below, which is
  // true of a watcher only by accident. A watcher's input is dropped by the
  // server; this is the client agreeing not to send it in the first place.
  if (watching) {
    return;
  }
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
