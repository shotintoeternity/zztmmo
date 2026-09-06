// net.ts — the socket to zzt-server, and every message this client sends.
//
// The join is the same one the 2D client sends. Input follows its two rules:
// movement rides the keymask, commands ride the key byte, and a frame is only
// sent when something changed (or a key was pressed), because the server
// consumes each frame on one tick and the held mask is resent by a timer.

import {
  buildJoinMessage,
  MessageTypeDebugCommand,
  MessageTypeHighScoreName,
  MessageTypeInput,
  MessageTypeQuitReply,
  MessageTypeSaveFilename,
  MessageTypeScrollReply,
  type InputMessage,
  type ServerMessage,
} from "./protocol";

export type ClientHandlers = {
  onMessage: (message: ServerMessage) => void;
  onClose: (reason: string) => void;
};

export function wsURL(world: string): string {
  const url = new URL("/ws", window.location.href);
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  url.searchParams.set("world", world);
  return url.toString();
}

export class Client {
  playerId = 0;
  private ws: WebSocket | null = null;
  private seq = 0;
  private lastMask = 0;

  constructor(private readonly handlers: ClientHandlers) {}

  get open(): boolean {
    return this.ws !== null && this.ws.readyState === WebSocket.OPEN;
  }

  connect(world: string, name: string, color: string, board = -1) {
    this.close();
    this.playerId = 0;
    this.lastMask = 0;
    const socket = new WebSocket(wsURL(world));
    this.ws = socket;
    socket.addEventListener("open", () => {
      socket.send(JSON.stringify(buildJoinMessage(name, color, "", board)));
    });
    socket.addEventListener("message", (event) => {
      const message = JSON.parse(String(event.data)) as ServerMessage;
      this.handlers.onMessage(message);
    });
    socket.addEventListener("close", () => {
      if (this.ws === socket) {
        this.ws = null;
        this.handlers.onClose("Disconnected");
      }
    });
    socket.addEventListener("error", () => {
      if (this.ws === socket) {
        this.ws = null;
        this.handlers.onClose("Connection error");
      }
    });
  }

  close() {
    const socket = this.ws;
    this.ws = null;
    if (socket) {
      socket.close();
    }
  }

  private send(payload: Record<string, unknown>): boolean {
    if (!this.open || this.playerId === 0) {
      return false;
    }
    this.ws!.send(JSON.stringify(payload));
    return true;
  }

  /** sendInput sends the keymask (or a key byte), skipping a frame that says nothing new. */
  sendInput(mask: number, key = 0) {
    if (!this.open || this.playerId === 0) {
      return;
    }
    if (mask === 0 && this.lastMask === 0 && key === 0) {
      return;
    }
    this.lastMask = mask;
    const input: InputMessage = { type: MessageTypeInput, playerId: this.playerId, seq: ++this.seq };
    if (mask !== 0) {
      input.keymask = mask;
    } else if (key !== 0) {
      input.key = key;
    } else {
      input.keymask = 0;
    }
    this.ws!.send(JSON.stringify(input));
  }

  sendKey(key: number) {
    this.send({ type: MessageTypeInput, playerId: this.playerId, seq: ++this.seq, key });
  }

  sendScrollReply(statId: number, label: string) {
    this.send({ type: MessageTypeScrollReply, playerId: this.playerId, statId, label });
  }

  sendQuitReply(quit: boolean) {
    this.send({ type: MessageTypeQuitReply, playerId: this.playerId, quit });
  }

  sendSaveFilename(name: string) {
    this.send({ type: MessageTypeSaveFilename, playerId: this.playerId, name });
  }

  sendHighScoreName(name: string) {
    this.send({ type: MessageTypeHighScoreName, playerId: this.playerId, name });
  }

  sendDebugCommand(text: string) {
    this.send({ type: MessageTypeDebugCommand, playerId: this.playerId, text });
  }
}
