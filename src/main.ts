// main.ts — the shell: the two canvases, the socket, the keyboard, the loop.
//
// Like the 2D client this is a dumb terminal. It owns no game state and
// simulates nothing: it sends what the player did and draws what the server
// says is there, only with depth. When something looks wrong the question is
// what the server sent, not what this file decided.
//
// Query parameters: ?world=TOWN&name=You&color=%23ff8800&view=chase&board=1

import "./style.css";
import { CameraRig, VIEW_MODES, type ViewMode } from "./camera";
import { loadFont, type Font } from "./font";
import { commandKey, facingMask, facingOfMask, isMovementKey, movementMask } from "./input";
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
import { BoardScene } from "./scene";
import { drawSidebar, sidebarClearLine, updateSidebar } from "./sidebar";

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
if (startView && (VIEW_MODES as readonly string[]).includes(startView)) {
  rig.setMode(startView as ViewMode);
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
  overlay.writeBase(71, 17, 0x1e, rig.mode.padEnd(8, " "));
}

function redrawTop() {
  overlay.clearTop();
  if (announce) {
    overlay.writeTop(0, 0, 0x4f, announce.slice(0, 60).padEnd(60, " "));
  }
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
  if (modal) {
    return 0;
  }
  const raw = movementMask(pressed);
  return rig.mode === "first" ? facingMask(raw, rig.facing) : raw;
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
    rig.cycle();
    writeViewLabel();
    sceneDirty = true;
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
  if (rig.mode === "first" && (event.code === "ArrowLeft" || event.code === "Numpad4")) {
    if (!event.repeat) rig.turn(-1);
    return;
  }
  if (rig.mode === "first" && (event.code === "ArrowRight" || event.code === "Numpad6")) {
    if (!event.repeat) rig.turn(1);
    return;
  }
  pressed.add(event.code);
  const facing = facingOfMask(movementMask(pressed));
  if (rig.mode !== "first" && facing !== null) {
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
  rig.zoom(event.deltaY);
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

function frame(now: number) {
  const dt = Math.min(0.1, (now - lastFrame) / 1000);
  lastFrame = now;
  if (scene && font) {
    const hide = rig.mode === "first" && myX > 0 ? { x: myX - 1, y: myY - 1 } : null;
    const hideKey = hide ? `${hide.x},${hide.y}` : "";
    if (sceneDirty || hideKey !== lastHide) {
      scene.build(cells, { roster, hide });
      sceneDirty = false;
      lastHide = hideKey;
    }
    rig.update(dt, myX - 0.5, myY - 0.5);
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
  writeViewLabel();
  new ResizeObserver(resize).observe(screenEl);
  resize();
  setNotice(`Connecting to ${worldName}...`);
  glCanvas.focus();
  window.requestAnimationFrame(frame);
  connect();
}

void start();
