# ZZTMMO

[![Go Test Status](https://img.shields.io/badge/go%20test-passing-brightgreen)](https://github.com/shotintoeternity/zztmmo)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**Welcome back to the Town of ZZT. Except now you can play multiplayer with your friends.**

ZZTMMO transforms Tim Sweeney's legendary 1991 shareware classic, **ZZT**, into a fully synchronized multiplayer online world. Play, chat, explore, buy torches from the Armory, dodge ruffians, cross passages, save shared room snapshots, and wander through classic ZZT worlds together in a browser-rendered DOS text mode.

## Key Features

*   **Shared co-op play:** Multiple players can share the same board, move independently, pick up items, trigger scrolls, die, respawn, and transfer between rooms without freezing everyone else.
*   **Authentic browser ZZT UI:** The web client renders the original 60x25 board plus 20x25 sidebar in CP437 cells with DOS colors, dark-room lighting, torch behavior, player blink, modal text windows, help screens, debug prompts, high scores, and title-screen flows.
*   **Dynamic worlds and room instances:** The server can discover `.ZZT` files, load worlds by name, run one room per board, freeze empty rooms, and move players through passages and board edges while keeping active rooms alive.
*   **Server-wide chat:** Browser clients can send and receive global chat, with optional JSONL persistence under the configured saves directory.
*   **Savable shared snapshots:** The browser save flow writes sanitized room snapshots to a configured `-saves` directory, and the title screen can list and restore them later.
*   **Spritesheet-based CP437 fidelity:** Rendering uses pixel-perfect PNG font sheets from Adrian Siekierka's [Zeta](https://github.com/asiekierka/zeta) emulator so smileys, borders, items, and text align like DOS ZZT.
*   **Server-authoritative simulation:** The Go backend owns all gameplay. Browser clients send keyboard state and command bytes; the server returns snapshots, dirty-cell diffs, player HUD state, modal events, sound events, and board changes.

---

## Under the Hood

Making a frame-rate-dependent DOS game from 1991 multiplayer is a technical challenge because ZZT was built around global state, blocking modal UI loops, and timing quirks.

To make it work:

1.  **The engine:** Forked from [benhoyt/zztgo](https://github.com/benhoyt/zztgo), a Go port of the reconstructed Pascal source, then converted into an importable, headless, instanced engine package.
2.  **Faithful over clean:** Classic behavior and bugs are treated as the specification unless the multiplayer server needs an explicit deviation. The Pascal source and Reconstruction of ZZT remain the behavior oracle.
3.  **Deterministic simulation:** Randomness flows through a seeded Turbo Pascal-style RNG, and replay/state-hash tests guard the engine against drift.
4.  **De-modal protocol:** Scrolls, help, save prompts, debug prompts, quit confirmation, high-score entry, pause state, sound toggles, death, respawn, and board transfers are emitted as events instead of blocking the simulation.
5.  **Room manager:** One `RoomManager` owns the world, creates one engine per active board, routes player joins/leaves/transfers, snapshots rooms for saving, and preserves shared puzzle progress across rooms.
6.  **Dumb terminal client:** The TypeScript/Vite client draws CP437 cells on canvas, synthesizes ZZT sounds with WebAudio, routes keyboard input, and renders modal UI, but it does not simulate gameplay.

---

## Running it Locally

### Prerequisites

Make sure you have [Go](https://go.dev/) and [Node.js](https://nodejs.org/) installed.

### Quick Start

All commands are run from the `engine/` directory.

1.  **Run backend tests:**
    ```bash
    go test ./...
    ```

2.  **Build the browser client:**
    ```bash
    cd web && npm install && npm run build && cd ..
    ```

3.  **Launch the MMO server:**
    ```bash
    go run ./cmd/zzt-server -world TOWN -web web/dist -help . -saves saves
    ```

4.  Open **[http://127.0.0.1:8080](http://127.0.0.1:8080)** in multiple browser tabs.

### Useful Server Flags

*   `-addr :8080` sets the HTTP/WebSocket listen address.
*   `-world TOWN` chooses the starting `.ZZT` world basename.
*   `-board 1` chooses the default starting board.
*   `-web web/dist` points at the built browser client.
*   `-help .` points at the directory containing ZZT `.HLP` files.
*   `-saves saves` enables saved-room snapshots and persistent chat logs. Use an empty value to disable saving.

---

## Directory Structure

```
engine/              Headless Go ZZT simulation engine, websocket server, and commands
engine/web/          Vite TypeScript browser client
engine/fixtures/     Replay fixtures and deterministic verification data
engine/saves/        Local saved-game snapshots and chat logs when enabled
reference/           Local reference checkouts, ignored by git
```

---

## Development Docs

For a deep dive into the architecture:

*   [TASKS.md](TASKS.md) - Active roadmap, completed milestones, and definitions of done.
*   [IMPLEMENTATION.md](IMPLEMENTATION.md) - Architecture overview and design decisions.
*   [ANALYSIS.md](ANALYSIS.md) - Low-level code maps and surgery details.
*   [NOTES.md](NOTES.md) - Running log of deviations, bugs, and decisions.

---

## Credits & Special Appreciation

This project would not exist without the dedication of the ZZT preservation community and the creators who came before us:

*   **Tim Sweeney** (Creator of ZZT / Epic Games): His game and the vibrant community surrounding it have changed my life.
*   **Adrian Siekierka** ([@asiekierka](https://github.com/asiekierka)): Immense thanks for reconstructing the original Turbo Pascal code of ZZT in [reconstruction-of-zzt](https://github.com/asiekierka/reconstruction-of-zzt), and for [Zeta](https://github.com/asiekierka/zeta), whose pixel-perfect font sheets power ZZTMMO's canvas renderer.
*   **Ben Hoyt** ([@benhoyt](https://github.com/benhoyt)): The author of [zztgo](https://github.com/benhoyt/zztgo), which made it possible to build ZZTMMO on top of a modern Go codebase.

This project is licensed under the **MIT License** (see [LICENSE](LICENSE)). Epic MegaGames' original content and help files are included for testing under fair-use/redistribution notices.

## Greetz

atom, blazer, bluemagus, bongo, chronos, cly5m, crankgod, darkmage, dex, dive, dr. dos, drac0, dragonlord, evilmario, fishfood, flatcoat_lab, flicker, funk, hercules, hm, hydra, jujubee, kkairos, knightt, lemmer, lord_igsel, madtom, masamune, mono, mooseka, myth, nadir, roastbeef, smiley, tseng, tucan, viovis, wil, xabbott, xf, yenrab, zamros, zed
