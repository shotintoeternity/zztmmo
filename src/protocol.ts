// protocol.ts — the slice of the ZZTMMO wire protocol a player client needs.
//
// These shapes are transcribed from the 2D client (engine/web/src/main.ts in
// shotintoeternity/zztmmo) and from engine/protocol.go. The server is the
// authority on all of them; this file only names what arrives.

export const MessageTypeJoin = "join";
export const MessageTypeInput = "input";
export const MessageTypeSnapshot = "snapshot";
export const MessageTypeDiff = "diff";
export const MessageTypeEvent = "event";
export const MessageTypeBoardChange = "boardChange";
export const MessageTypeDebugCommand = "debugCommand";
export const MessageTypeScrollReply = "scrollReply";
export const MessageTypeQuitReply = "quitReply";
export const MessageTypeHighScoreName = "highScoreName";
export const MessageTypeSaveFilename = "saveFilename";
export const MessageTypeChat = "chat";
export const MessageTypeAnnounce = "announce";

/** One screen cell as the server draws it: a CP437 byte under a DOS attribute. */
export type ScreenCell = {
  x: number;
  y: number;
  ch: number;
  color: number;
  /**
   * The element the server says is showing here (gamevars.go's E_* numbers),
   * absent when it is empty or when the board is hiding it -- a dark room
   * discloses nothing. It exists because a glyph does not always name its
   * element: a fake wall is drawn with the normal wall's own character.
   */
  element?: number;
};

/** A player on the board. x and y are 1-based board coordinates. */
export type PlayerSnapshot = {
  id: number;
  statId: number;
  x: number;
  y: number;
  health: number;
  name?: string;
  color?: string;
  handle?: string;
};

export type HudSnapshot = {
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

export type ProtocolEvent = {
  type: string;
  statId?: number;
  playerStatId?: number;
  title?: string;
  lines?: string[];
  filename?: string;
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
  freqHz?: number;
};

export type SnapshotMessage = {
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
  spectator?: boolean;
  watchers?: number;
};

export type DiffMessage = {
  type: typeof MessageTypeDiff;
  boardId: number;
  tick: number;
  hash: number;
  cells?: ScreenCell[];
  players?: PlayerSnapshot[];
  hud?: HudSnapshot;
  events?: ProtocolEvent[];
  watchers?: number;
};

export type EventMessage = {
  type: typeof MessageTypeEvent;
  boardId?: number;
  tick?: number;
  event: ProtocolEvent;
};

export type BoardChangeMessage = {
  type: typeof MessageTypeBoardChange;
  snapshot: SnapshotMessage;
};

export type ChatMessage = {
  type: typeof MessageTypeChat;
  from: string;
  playerId?: number;
  text: string;
};

export type AnnounceMessage = {
  type: typeof MessageTypeAnnounce;
  text: string;
  seconds?: number;
};

export type ServerMessage =
  | SnapshotMessage
  | DiffMessage
  | EventMessage
  | BoardChangeMessage
  | ChatMessage
  | AnnounceMessage
  | { type: string };

export type InputMessage = {
  type: typeof MessageTypeInput;
  playerId: number;
  seq: number;
  keymask?: number;
  key?: number;
};

/** buildJoinMessage omits empty fields: an absent color is the vanilla player. */
export function buildJoinMessage(name: string, color = "", resumeToken = "", board = -1): Record<string, unknown> {
  const message: Record<string, unknown> = { type: MessageTypeJoin, name };
  // A board of -1 leaves the choice to the server's configured default.
  if (board >= 0) {
    message.board = board;
  }
  if (resumeToken) {
    message.resumeToken = resumeToken;
  }
  if (color) {
    message.color = color;
  }
  return message;
}
