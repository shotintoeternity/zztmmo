// main.ts — the shell: the two canvases, the socket, the keyboard, the loop.
//
// Like the 2D client this is a dumb terminal. It owns no game state and
// simulates nothing: it sends what the player did and draws what the server
// says is there, only with depth. When something looks wrong the question is
// what the server sent, not what this file decided.
//
// Query parameters: ?world=TOWN&name=You&color=%23ff8800&view=overhead&board=1

import "./style.css";
import { CameraRig } from "./camera";
import { loadFont, type Font } from "./font";
import { commandKey, facingMask, facingOfMask, ghostDrift, isMovementKey, movementMask, wireMask } from "./input";
import { modalKey, newTextModal, renderModal, type Modal } from "./modals";
import { Client } from "./net";
import { BOARD_COLS, COLS, Overlay, ROWS } from "./overlay";
import {
  MessageTypeAnnounce,
  MessageTypeBoardChange,
  MessageTypeChat,
  MessageTypeDiff,
  MessageTypeEvent,
  MessageTypeSnapshot,
  type AnnounceMessage,
  type BoardChangeMessage,
  type ChatMessage,
  type DiffMessage,
  type EventMessage,
  type HudSnapshot,
  type PlayerSnapshot,
  type ProtocolEvent,
  type ScreenCell,
  type ServerMessage,
  type SnapshotMessage,
} from "./protocol";
import { BoardScene, TILE_DEPTH } from "./scene";
import { drawSidebar, sidebarClearLine, updateSidebar } from "./sidebar";
import { boardText, groupSigns, signInRange, type BoardText, type SignGroup } from "./text_runs";

const params = new URLSearchParams(window.location.search);
const worldName = params.get("world") || "TOWN";
const playerName = params.get("name") || "3D";
const playerColor = params.get("color") || "";
const startBoard = Number(params.get("board") ?? "-1");
const startView = params.get("view");

const app = document.querySelector<HTMLDivElement>("#app");
if (!app) {
  throw new Error("missing app root");
}
app.innerHTML = `
  <div class="screen">
    <canvas class="gl" tabindex="0"></canvas>
    <canvas class="overlay"></canvas>
  </div>
`;
const screenEl = app.querySelector<HTMLDivElement>(".screen")!;
const glCanvas = app.querySelector<HTMLCanvasElement>(".gl")!;
const overlayCanvas = app.querySelector<HTMLCanvasElement>(".overlay")!;

// --- state -------------------------------------------------------------------

const cells: ScreenCell[] = Array.from({ length: COLS * ROWS }, (_, i) => ({
  x: i % COLS,
  y: Math.floor(i / COLS),
  ch: 0x20,
  // An empty square is black (0x0F, ' '), which is what the server sends for
  // one; a blue default here would paint the client's guess, not the board.
  color: 0x0f,
  element: 0,
}));
let roster: PlayerSnapshot[] = [];
let myStatId = -1;
let myX = 0;
let myY = 0;
let hud: HudSnapshot | null = null;
let modal: Modal | null = null;
let paused = false;
let notice = "";
let chatLine = "";
let chatTimer = 0;
let announce = "";
let announceTimer = 0;
let sceneDirty = true;
let gameOver = false;
// The board message, drawn at the bottom of the screen in the 3D views, and
// the board's signs, grouped so the one you are standing at can read itself
// out at the bottom too.
let text: BoardText = { signs: [], message: null };
let textCells = new Set<number>();
let signGroups: SignGroup[] = [];

type PendingScroll = { title: string; lines: string[]; statId: number };
let scrollQueue: PendingScroll[] = [];
let openScrollStatId = -1;
let scrollReplySent = false;
let pendingHighScore = false;
let leaveOnClose = false;

const pressed = new Set<string>();
let inputTimer = 0;
let retryTimer = 0;
let retryAttempt = 0;

const overlay = new Overlay(overlayCanvas);
const rig = new CameraRig();
// ?view= still names the old modes. They are camera positions now, not modes:
// V toggles the world and the text screen, and the rest is the wheel.
if (startView === "classic") {
  rig.setMode("classic");
} else if (startView) {
  rig.applyPreset(startView);
}

// First-person controls belong to the 3D view. In the classic view the arrows
// are the text screen's, whatever the camera was doing when you left it.
function inFirstPerson(): boolean {
  return rig.mode === "world" && rig.firstPerson;
}

/** The world point your body stands on. */
function bodyX(): number {
  return myX - 0.5;
}
function bodyZ(): number {
  return (myY - 0.5) * TILE_DEPTH;
}

// leaveGhost brings the camera home. A board change and the V key both do it:
// a ghost is a place on this board, and neither survives leaving it.
function leaveGhost() {
  rig.setGhost(false, bodyX(), bodyZ());
}

let font: Font | null = null;
let scene: BoardScene | null = null;

// --- the socket ----------------------------------------------------------------

const client = new Client({
  onMessage: applyMessage,
  onClose: (reason) => {
    if (gameOver) {
      return;
    }
    stopHeldInput();
    setNotice(`${reason}. Reconnecting...`);
    scheduleReconnect();
  },
});

function connect() {
  window.clearTimeout(retryTimer);
  gameOver = false;
  client.connect(worldName, playerName, playerColor, startBoard);
  window.clearInterval(inputTimer);
  inputTimer = window.setInterval(() => client.sendInput(currentMask()), 55);
}

function scheduleReconnect() {
  window.clearTimeout(retryTimer);
  const delay = Math.min(8000, 500 * 2 ** retryAttempt);
  retryAttempt += 1;
  retryTimer = window.setTimeout(connect, delay);
}

function applyMessage(message: ServerMessage) {
  switch (message.type) {
    case MessageTypeSnapshot:
      applySnapshot(message as SnapshotMessage);
      break;
    case MessageTypeDiff:
      applyDiff(message as DiffMessage);
      break;
    case MessageTypeEvent:
      handleEvent((message as EventMessage).event);
      break;
    case MessageTypeBoardChange:
      stopHeldInput();
      leaveGhost();
      closeModal(true);
      applySnapshot((message as BoardChangeMessage).snapshot);
      rig.snap();
      break;
    case MessageTypeChat:
      handleChat(message as ChatMessage);
      break;
    case MessageTypeAnnounce:
      handleAnnounce(message as AnnounceMessage);
      break;
  }
}

function applySnapshot(message: SnapshotMessage) {
  if (message.spectator) {
    return;
  }
  retryAttempt = 0;
  setNotice("");
  client.playerId = message.you.id;
  myStatId = message.you.statId;
  myX = message.you.x;
  myY = message.you.y;
  trackMe(message.players);
  for (const cell of cells) {
    cell.ch = 0x20;
    cell.color = 0x0f;
    cell.element = 0;
  }
  for (const cell of message.screen) {
    setBoardCell(cell);
  }
  hud = message.hud;
  drawSidebar(overlay.writeBase);
  updateSidebar(overlay.writeBase, hud);
  writeViewLabel();
  sceneDirty = true;
  renderEvents(message.events);
  redrawTop();
}

function applyDiff(message: DiffMessage) {
  trackMe(message.players);
  if (message.cells) {
    for (const cell of message.cells) {
      setBoardCell(cell);
    }
    sceneDirty = true;
  }
  if (message.hud) {
    hud = message.hud;
    updateSidebar(overlay.writeBase, hud);
  }
  renderEvents(message.events);
}

function trackMe(players: PlayerSnapshot[] | undefined) {
  if (!players) {
    return;
  }
  roster = players;
  sceneDirty = true;
  for (const player of players) {
    if (player.id === client.playerId) {
      myStatId = player.statId;
      myX = player.x;
      myY = player.y;
      return;
    }
  }
}

function setBoardCell(cell: ScreenCell) {
  if (cell.x < 0 || cell.x >= BOARD_COLS || cell.y < 0 || cell.y >= ROWS) {
    return;
  }
  const mine = cells[cell.y * COLS + cell.x];
  mine.ch = cell.ch;
  mine.color = cell.color;
  // Absent means empty, or a square a dark room is keeping to itself.
  mine.element = cell.element ?? 0;
}

// --- events --------------------------------------------------------------------

function renderEvents(events: ProtocolEvent[] | undefined) {
  if (!events) {
    return;
  }
  for (const event of events) {
    handleEvent(event);
  }
}

function isMine(event: ProtocolEvent): boolean {
  return myStatId < 0 || (event.statId ?? 0) === myStatId;
}

function isMyScroll(event: ProtocolEvent): boolean {
  const owner = event.playerStatId ?? -1;
  return owner < 0 || myStatId < 0 || owner === myStatId;
}

function handleEvent(event: ProtocolEvent) {
  switch (event.type) {
    case "help":
      if (isMine(event)) {
        openModal(newTextModal(event.title ?? event.filename ?? "Help", event.lines ?? [], true));
      }
      break;
    case "debugPrompt":
      if (isMine(event)) {
        openModal({ kind: "entry", prompt: "", buffer: "", width: 11, onSubmit: (text) => client.sendDebugCommand(text ?? "") });
      }
      break;
    case "savePrompt":
      if (isMine(event)) {
        openModal({
          kind: "entry",
          prompt: "Save game:",
          buffer: "",
          width: 8,
          onSubmit: (name) => {
            if (name) {
              client.sendSaveFilename(name);
            }
          },
        });
      }
      break;
    case "saveResult":
      openModal(newTextModal("Saving", ["", event.error ? `  Not saved: ${event.error}` : `  Saved as ${event.filename ?? ""}.SAV`, ""], true));
      break;
    case "scroll":
      if (isMyScroll(event)) {
        enqueueScroll({ title: event.title ?? "Interaction", lines: event.lines ?? [], statId: event.statId ?? -1 });
      }
      break;
    case "pause":
      if (isMine(event)) {
        paused = event.paused ?? false;
        redrawTop();
      }
      break;
    case "quitPrompt":
      if (isMine(event)) {
        openModal({ kind: "yesno", question: "End this game? ", onAnswer: (yes) => client.sendQuitReply(yes) });
      }
      break;
    case "quit":
      endGame("Game over. Press Enter to play again.");
      break;
    case "highScoreEntry":
      openModal(newTextModal(event.title ?? "New high score", event.lines ?? [], true));
      pendingHighScore = true;
      break;
    case "highScores":
      pendingHighScore = false;
      openModal(newTextModal(event.title ?? "High scores", event.lines ?? [], true));
      leaveOnClose = true;
      break;
    default:
      break;
  }
}

function endGame(message: string) {
  gameOver = true;
  window.clearInterval(inputTimer);
  inputTimer = 0;
  client.close();
  stopHeldInput();
  modal = null;
  setNotice(message);
}

function handleChat(message: ChatMessage) {
  chatLine = `<${message.from}> ${message.text}`;
  window.clearTimeout(chatTimer);
  chatTimer = window.setTimeout(() => {
    chatLine = "";
    redrawTop();
  }, 5000);
  redrawTop();
}

function handleAnnounce(message: AnnounceMessage) {
  announce = message.text;
  window.clearTimeout(announceTimer);
  announceTimer = window.setTimeout(() => {
    announce = "";
    redrawTop();
  }, ((message.seconds ?? 60) + 8) * 1000);
  redrawTop();
}

// --- scrolls and modals ------------------------------------------------------------

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
  openModal(
    newTextModal(scroll.title, scroll.lines, false, (label) => {
      scrollReplySent = true;
      client.sendScrollReply(scroll.statId, label);
    }),
  );
}

function openModal(next: Modal) {
  stopHeldInput();
  modal = next;
  redrawTop();
}

// closeModal answers an unanswered scroll (an empty label is "finished
// reading", which is what lets the player move again), then opens whatever was
// waiting behind it.
function closeModal(silent = false) {
  const scrollStatId = openScrollStatId;
  const replySent = scrollReplySent;
  openScrollStatId = -1;
  scrollReplySent = false;
  modal = null;
  if (scrollStatId >= 0 && !silent) {
    if (!replySent) {
      client.sendScrollReply(scrollStatId, "");
    }
    const next = scrollQueue.shift();
    if (next) {
      showScroll(next);
      return;
    }
  }
  if (silent) {
    scrollQueue = [];
  }
  if (pendingHighScore) {
    pendingHighScore = false;
    openModal({
      kind: "entry",
      prompt: "Your name:",
      buffer: "",
      width: 15,
      onSubmit: (name) => client.sendHighScoreName(name ?? ""),
    });
    return;
  }
  if (leaveOnClose) {
    leaveOnClose = false;
    endGame("Game over. Press Enter to play again.");
    return;
  }
  redrawTop();
}

// --- the overlay's top layer ------------------------------------------------------

function setNotice(text: string) {
  notice = text;
  redrawTop();
}

function writeViewLabel() {
  const label = rig.mode === "classic" ? "classic" : rig.ghost ? "3D ghost" : "3D";
  overlay.writeBase(71, 17, 0x1e, label.padEnd(8, " "));
  // Row 13 is blank in vanilla. F is the way to eye level and back, and it has
  // to be said somewhere: V used to arrive at first person after a couple of
  // taps, and now nothing does.
  if (rig.mode === "world") {
    overlay.writeBase(62, 13, 0x30, " F ");
    overlay.writeBase(65, 13, 0x1f, (rig.firstPerson ? " Back out" : " Eye level").padEnd(10, " "));
  } else {
    sidebarClearLine(overlay.writeBase, 13);
  }
  // Row 20 is blank in vanilla's sidebar, so the one binding that exists only
  // inside the first-person view is announced there, and only there.
  if (inFirstPerson()) {
    overlay.writeBase(63, 20, 0x30, " A D ");
    overlay.writeBase(68, 20, 0x1f, " Strafe");
  } else {
    sidebarClearLine(overlay.writeBase, 20);
  }
  // Row 24 is blank in vanilla too. In first person the sidebar is the only
  // thing that can tell you whether you are your body or not, so it says so
  // in the colour as well as the word.
  if (rig.mode === "world") {
    overlay.writeBase(62, 24, rig.ghost ? 0x2f : 0x30, " G ");
    // Padded to a common width for the reason sidebar.ts gives: a shorter word
    // written over a longer one leaves the longer one's tail behind, and
    // " Body" over " Ghost" reads "Bodyt".
    overlay.writeBase(65, 24, 0x1f, (rig.ghost ? " Body" : " Ghost").padEnd(6, " "));
  } else {
    sidebarClearLine(overlay.writeBase, 24);
  }
}

// applyView settles everything that depends on the camera mode. The classic
// view is the text screen: the board is drawn by the overlay and the 3D
// canvas is hidden underneath it.
function applyView() {
  writeViewLabel();
  glCanvas.style.visibility = rig.mode === "classic" ? "hidden" : "";
  if (rig.mode !== "classic") {
    overlay.setBoard(null, new Map());
  }
  sceneDirty = true;
}

function playerTints(): Map<number, string> {
  const tints = new Map<number, string>();
  for (const player of roster) {
    if (typeof player.color === "string" && /^#[0-9a-fA-F]{6}$/.test(player.color)) {
      tints.set((player.y - 1) * COLS + (player.x - 1), player.color);
    }
  }
  return tints;
}

// refreshText re-reads the words on the board. Two kinds, and in a 3D view
// they are read two different ways.
//
// The message (a touch, a warning, an object's #say) is a line the engine
// writes over the bottom row of the board. It leaves the scene and is written
// on row 24 of the overlay, where ZZT puts it, so it reads as text rather than
// as a row of standing cards.
//
// Signs (text elements) are part of the board and stay in the world -- they
// are walls you can read, and a board with its signs taken out is not the
// board. But a sign is a row of letters lying on the floor, and from inside
// the world, at eye height, a row of letters is edge-on and unreadable. So the
// sign you are standing at reads itself out at the bottom of the screen, and
// only that one: see signInRange.
function refreshText() {
  if (rig.mode === "classic") {
    text = { signs: [], message: null };
    textCells = new Set();
    signGroups = [];
    return;
  }
  text = boardText(cells, COLS, BOARD_COLS, ROWS);
  textCells = new Set(text.message ? text.message.cells : []);
  signGroups = groupSigns(text.signs, COLS);
}

// eyeCell is the board square you are reading from: where you are standing,
// or where you have drifted to, because reading is something eyes do and a
// ghost took them with it.
function eyeCell(): { x: number; y: number } | null {
  if (rig.ghost) {
    return { x: Math.floor(rig.ghostAt.x), y: Math.floor(rig.ghostAt.z / TILE_DEPTH) };
  }
  return myX > 0 ? { x: myX - 1, y: myY - 1 } : null;
}

// How close you must stand to read a sign, in the weighted cells signDistance
// counts: eight columns to the side of one, or four rows off it.
const SIGN_RANGE = 8;
// A sign taller than this is a wall of text; the first lines are the ones that
// name the place.
const SIGN_MAX_LINES = 3;

function writeBoardText() {
  if (rig.mode === "classic") {
    return;
  }
  if (text.message && !chatLine) {
    overlay.writeTop(text.message.x, text.message.y, text.message.color, text.message.text);
  }
  writeNearbySign();
}

// writeNearbySign writes the sign you are at just above the message line, in
// the sign's own colors, so it reads as that sign speaking rather than as
// chrome. It is centered on the board the way the message line is.
function writeNearbySign() {
  const eye = eyeCell();
  if (modal || notice || !eye) {
    return;
  }
  const group = signInRange(signGroups, eye.x, eye.y, COLS, SIGN_RANGE);
  if (!group) {
    return;
  }
  const lines = group.lines.slice(0, SIGN_MAX_LINES);
  let y = 24 - lines.length;
  for (const line of lines) {
    const label = ` ${line.slice(0, BOARD_COLS - 2)} `;
    const x = Math.max(0, Math.floor((BOARD_COLS - label.length) / 2));
    overlay.writeTop(x, y, group.color, label);
    y += 1;
  }
}

function redrawTop() {
  overlay.clearTop();
  if (announce) {
    overlay.writeTop(0, 0, 0x4f, announce.slice(0, 60).padEnd(60, " "));
  }
  writeBoardText();
  if (chatLine) {
    overlay.writeTop(0, 24, 0x1e, chatLine.slice(0, 60).padEnd(60, " "));
  }
  if (paused) {
    sidebarClearLine(overlay.writeTop, 5);
    overlay.writeTop(64, 5, 0x1f, "Pausing...");
  }
  if (notice) {
    const text = ` ${notice.slice(0, 56)} `;
    const x = Math.max(0, Math.floor((BOARD_COLS - text.length) / 2));
    overlay.writeTop(x, 11, 0x1f, " ".repeat(text.length));
    overlay.writeTop(x, 12, 0x1f, text);
    overlay.writeTop(x, 13, 0x1f, " ".repeat(text.length));
  }
  if (modal) {
    renderModal(overlay.writeTop, modal);
  }
}

// --- keyboard --------------------------------------------------------------------

function currentMask(): number {
  // A ghost's keys fly the camera, so the body is holding nothing down.
  if (modal || rig.ghost) {
    return 0;
  }
  const raw = movementMask(pressed);
  // Outside first person a strafe has no meaning -- the arrows are already
  // absolute board directions -- so the pseudo-bits are dropped rather than
  // sent. facingMask does its own dropping.
  return inFirstPerson() ? facingMask(raw, rig.facing) : wireMask(raw);
}

function stopHeldInput() {
  pressed.clear();
  client.sendInput(0);
}

function handleKeyDown(event: KeyboardEvent) {
  if (gameOver) {
    if (event.code === "Enter") {
      event.preventDefault();
      connect();
    }
    return;
  }
  if (modal) {
    const result = modalKey(modal, event);
    if (result !== "ignore") {
      event.preventDefault();
    }
    if (result === "close") {
      closeModal();
    } else if (result === "redraw") {
      redrawTop();
    }
    return;
  }
  if (event.code === "KeyV" && !event.ctrlKey && !event.metaKey && !event.altKey) {
    event.preventDefault();
    stopHeldInput();
    leaveGhost();
    rig.cycle();
    applyView();
    return;
  }
  // G steps out of your body. Your ☻ stays where it is -- it is still on the
  // board, and the board is still ticking -- while the camera drifts off
  // through the walls. Nothing is sent while you are out there, so a ghost is
  // a way of looking and never a way of reaching.
  if (event.code === "KeyG" && !event.ctrlKey && !event.metaKey && !event.altKey) {
    event.preventDefault();
    if (rig.mode !== "world") {
      return;
    }
    stopHeldInput();
    rig.setGhost(!rig.ghost, bodyX(), bodyZ());
    applyView();
    return;
  }
  // F stands you up inside your own square, or steps back out to the distance
  // you were watching from. The wheel does the same thing continuously; this is
  // the way there without one.
  if (event.code === "KeyF" && !event.ctrlKey && !event.metaKey && !event.altKey) {
    event.preventDefault();
    stopHeldInput();
    rig.standUp();
    applyView();
    return;
  }
  if (event.repeat && !isMovementKey(event.code)) {
    return;
  }
  const command = commandKey(event);
  if (command !== 0) {
    event.preventDefault();
    stopHeldInput();
    client.sendKey(command);
    return;
  }
  if (!isMovementKey(event.code)) {
    return;
  }
  event.preventDefault();
  // First person: left and right are turns on the key edge and never travel.
  if (inFirstPerson() && (event.code === "ArrowLeft" || event.code === "Numpad4")) {
    if (!event.repeat) rig.turn(-1);
    return;
  }
  if (inFirstPerson() && (event.code === "ArrowRight" || event.code === "Numpad6")) {
    if (!event.repeat) rig.turn(1);
    return;
  }
  pressed.add(event.code);
  const facing = facingOfMask(movementMask(pressed));
  if (!inFirstPerson() && facing !== null) {
    rig.facing = facing;
  }
  client.sendInput(currentMask());
}

function handleKeyUp(event: KeyboardEvent) {
  if (!pressed.has(event.code)) {
    return;
  }
  event.preventDefault();
  pressed.delete(event.code);
  client.sendInput(currentMask());
}

// --- pointer ---------------------------------------------------------------------

let dragging = false;
let dragX = 0;
let dragY = 0;

glCanvas.addEventListener("pointerdown", (event) => {
  glCanvas.focus();
  dragging = true;
  dragX = event.clientX;
  dragY = event.clientY;
  glCanvas.setPointerCapture(event.pointerId);
});
glCanvas.addEventListener("pointermove", (event) => {
  if (!dragging) {
    return;
  }
  rig.orbit(event.clientX - dragX, event.clientY - dragY);
  dragX = event.clientX;
  dragY = event.clientY;
});
glCanvas.addEventListener("pointerup", () => {
  dragging = false;
});
glCanvas.addEventListener("wheel", (event) => {
  event.preventDefault();
  // Pushing past the last orbit step stands you up, which changes what the
  // arrows mean: a key held across that moment has to be let go of, or it
  // would walk west one frame and turn the next.
  if (rig.zoom(event.deltaY)) {
    stopHeldInput();
    applyView();
  }
}, { passive: false });

window.addEventListener("keydown", handleKeyDown);
window.addEventListener("keyup", handleKeyUp);
window.addEventListener("blur", () => {
  if (pressed.size > 0) {
    stopHeldInput();
  }
});

// --- layout and the loop -------------------------------------------------------------

function resize() {
  const rect = screenEl.getBoundingClientRect();
  const width = Math.max(1, rect.width * (BOARD_COLS / COLS));
  const height = Math.max(1, rect.height);
  scene?.resize(width, height, Math.min(window.devicePixelRatio || 1, 2));
  rig.resize(width / height);
}

let lastFrame = performance.now();
let lastHide = "";
let lastEye = "";

function frame(now: number) {
  const dt = Math.min(0.1, (now - lastFrame) / 1000);
  lastFrame = now;
  if (scene && font) {
    // Your own card is only in the way when you are behind your own eyes; a
    // ghost wants to see the body it left.
    const hide = inFirstPerson() && !rig.ghost && myX > 0 ? { x: myX - 1, y: myY - 1 } : null;
    const hideKey = hide ? `${hide.x},${hide.y}` : "";
    if (rig.mode === "classic") {
      if (sceneDirty) {
        overlay.setBoard(cells, playerTints());
        sceneDirty = false;
      }
      overlay.draw(font);
      window.requestAnimationFrame(frame);
      return;
    }
    if (rig.ghost) {
      const drift = ghostDrift(movementMask(pressed), rig.firstPerson);
      rig.driftGhost(drift.dx, drift.dz, dt);
    }
    const eye = eyeCell();
    const eyeKey = eye ? `${eye.x},${eye.y}` : "";
    if (sceneDirty || hideKey !== lastHide) {
      refreshText();
      scene.build(cells, { roster, hide, textCells });
      sceneDirty = false;
      lastHide = hideKey;
      redrawTop();
    } else if (eyeKey !== lastEye) {
      // The board has not changed, so the geometry stands: only the words at
      // the bottom need to catch up with where the ghost has got to.
      redrawTop();
    }
    lastEye = eyeKey;
    rig.update(dt, bodyX(), bodyZ());
    const fog = rig.fog();
    scene.setFog(fog.near, fog.far);
    scene.render(rig.camera);
    overlay.draw(font);
  }
  window.requestAnimationFrame(frame);
}

async function start() {
  font = await loadFont();
  scene = new BoardScene(glCanvas, font);
  drawSidebar(overlay.writeBase);
  applyView();
  new ResizeObserver(resize).observe(screenEl);
  resize();
  setNotice(`Connecting to ${worldName}...`);
  glCanvas.focus();
  window.requestAnimationFrame(frame);
  connect();
}

void start();
