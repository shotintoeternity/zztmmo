# ZZTMMO

[![Go Test Status](https://img.shields.io/badge/go%20test-passing-brightgreen)](https://github.com/shotintoeternity/zztmmo)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**Welcome back to the Town of ZZT. This time, bring friends.**

ZZTMMO turns Tim Sweeney's 1991 shareware classic **ZZT** into a shared browser world: same blue text windows, same blinking player, same ruffians making awful decisions, but now multiple people can wander the boards together.

Explore classic `.ZZT` worlds in synchronized rooms, chat while you play, read scrolls, buy supplies, get hurt, respawn, cross passages, save shared snapshots, and watch someone else discover that yes, that fake wall was fake the whole time.

## Play Together Today

*   **Explore classic ZZT worlds together:** Load local `.ZZT` files or search Museum of ZZT titles from the world picker, then join each world as its own live instance.
*   **Share rooms without sharing a keyboard:** Multiple players can stand on the same board, move independently, trigger scrolls, buy and use items, take damage, die, and respawn.
*   **Keep the world moving:** Board transfers, passages, active-room ticks, frozen empty rooms, dark rooms, torches, high scores, help screens, pause, quit, and title-screen flows are all handled server-side.
*   **Chat like it is 1995 with better sockets:** Browser clients get global chat, with optional JSONL persistence when saves are enabled.
*   **Save the shared mess:** Room snapshots can be saved to disk and restored later, so a party can preserve puzzle progress instead of starting from a pristine world every session.
*   **Play co-op first, PvP later:** The current game is honest multiplayer ZZT co-op. Combat, damage, bullets, and hazards are server-authoritative where implemented, but there are no PvP arenas, rankings, ownership rules, or duel systems yet.

## The ZZT Feel

*   **Canvas CP437 renderer:** The browser draws the original 60x25 board plus 20x25 sidebar in DOS colors using pixel-perfect PNG font sheets from Adrian Siekierka's [Zeta](https://github.com/asiekierka/zeta).
*   **ZZT-style windows:** Scrolls, files, prompts, help, saves, generated-world progress, world search, and failure messages render as text-mode modal windows.
*   **Server-authoritative simulation:** Clients send keyboard state and command bytes; the Go server owns gameplay and streams snapshots, dirty-cell diffs, HUD updates, sounds, modal events, and board changes.
*   **Faithful where it matters:** Classic behavior and bugs are treated as the spec unless multiplayer needs an explicit deviation.

## Moonshot Roadmap

ZZTMMO is already playable as a shared ZZT server, but the long game is stranger and bigger:

*   **Dream worlds:** Generate compact, playable `.ZZT` worlds from prompts, validate them, and host them as multiplayer instances.
*   **Museum search-and-play:** Keep turning the Museum of ZZT archive into a walkable universe where old community worlds are a few keystrokes away.
*   **Player identity:** Add account-backed names, persistent player state, invites, parties, and cleaner ownership for worlds and saves.
*   **Party instances:** Make it easy for a group to spin up a private run, continue later, and invite more players into the same adventure.
*   **Replays and daily challenges:** Record runs, replay them deterministically, publish daily seeds, and let players race the same strange little board.
*   **Ghost racing:** Show prior runs as ghosts so players can speedrun ZZT boards against friends without needing everyone online at once.
*   **Live-DM tools:** Explore possession, moderation, and “dungeon master” style control for running events inside classic ZZT worlds.
*   **Community publishing:** Grow the browser editor into a collaborative way to build, test, publish, and share worlds without leaving the page.

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
    > The server serves the **built** bundle in `web/dist`, which is gitignored.
    > Any change under `web/src` requires re-running `npm run build` (and a
    > browser hard-refresh) before it is visible — editing source alone ships a
    > stale UI. The server logs a `STALE build` warning at startup if `web/dist`
    > is older than `web/src`.

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

## Directory Structure

```
engine/              Headless Go ZZT simulation engine, websocket server, and commands
engine/web/          Vite TypeScript browser client
engine/fixtures/     Replay fixtures and deterministic verification data
engine/saves/        Local saved-game snapshots and chat logs when enabled
reference/           Local reference checkouts, ignored by git
```

## Credits & Special Appreciation

This project would not exist without the dedication of the ZZT preservation community and the creators who came before us:

*   **Tim Sweeney** (Creator of ZZT / Epic Games): His game and the vibrant community surrounding it have changed my life.
*   **Adrian Siekierka** ([@asiekierka](https://github.com/asiekierka)): Immense thanks for reconstructing the original Turbo Pascal code of ZZT in [reconstruction-of-zzt](https://github.com/asiekierka/reconstruction-of-zzt), and for [Zeta](https://github.com/asiekierka/zeta), whose pixel-perfect font sheets power ZZTMMO's canvas renderer.
*   **Ben Hoyt** ([@benhoyt](https://github.com/benhoyt)): The author of [zztgo](https://github.com/benhoyt/zztgo), which made it possible to build ZZTMMO on top of a modern Go codebase.

This project is licensed under the **MIT License** (see [LICENSE](LICENSE)). Epic MegaGames' original content and help files are included for testing under fair-use/redistribution notices.

## Greetz

atom, blazer, bluemagus, bongo, capnkev, chronos, cly5m, crankgod, darkmage, dex, dive, dr. dos, drac0, dragonlord, evilmario, fishfood, flatcoat_lab, flicker, funk, hercules, hm, hydra, jujubee, kkairos, knightt, lemmer, lord_igsel, madtom, masamune, mono, mooseka, myth, nadir, roastbeef, smiley, tseng, tucan, viovis, wil, xabbott, xf, yenrab, zamros, zed
