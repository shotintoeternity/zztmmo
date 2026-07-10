# ☻ ZZTMMO

[![Go Test Status](https://img.shields.io/badge/go%20test-passing-brightgreen)](https://github.com/shotintoeternity/zztmmo)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**Welcome back to the Town of ZZT! Except now you can play multiplayer with your friends!**

ZZTMMO transforms Tim Sweeney's legendary 1991 shareware classic, **ZZT**, into a fully synchronized, multiplayer online world. Play, chat, explore, and dodge ruffians cooperatively or PvP in real-time, all inside a web browser rendered in pixel-perfect classic DOS style.

##  Key Features

*   **👥 Shared Co-Op Play:** Share the board with multiple players simultaneously. Team up to solve puzzles, shoot monsters, and collect gems.
*   **💬 Server-Wide Global Chat:** Chat with anyone on the server at any time. Press **`C`** to pull up a scrollable, ZZT-style paged text window to read message history, or shout out to the lobby.
*   **🗺️ Dynamic World Instances:** Jump between different ZZT worlds (Town, Caves, Rhygar 2, Kudzu, and more) dynamically with friends, without restarting the server.
*   **📺 Spritesheet-Based CP437 Fidelity:** Rendered using the official pixel-perfect PNG font sheets from Adrian Siekierka's [Zeta](https://github.com/asiekierka/zeta) emulator. Smiley faces, border walls, and items connect and align exactly as they did on DOS machines in 1991, with zero subpixel font gaps or rendering artifacts.
*   **⚖️ Server-Authoritative Simulation:** A completely headless, deterministic backend running in Go ensures player inputs are processed synchronously with zero client-side simulation or state drift.

## 🛠️ Under the Hood (How it Works)

Making a single-player, frame-rate dependent DOS game from 1991 multiplayer is a wild technical challenge. ZZT was packed with global states, blocking modal UI loops, and timing quirks. 

To achieve multiplayer sync:
1.  **The Engine:** Forked from [benhoyt/zztgo](https://github.com/benhoyt/zztgo) (a machine-translation of the original Pascal source into Go), we unpicked global state and blocking prompts to create an isolated, tick-based `RoomManager`.
2.  **Faithful over Clean:** We prioritize **100% authentic gameplay**. Classic bugs (like actor-stat alignment offsets and physics quirks) are treated as specifications rather than issues.
3.  **Deterministic Safety Net:** A rigorous test replay harness runs `TOWN.ZZT` under pre-recorded inputs, asserting identical state hashes at every commit. No map-iteration randomness, system clock lookups, or unseeded RNG can leak into the simulation.
4.  **Dumb Terminal Client:** The web client (TypeScript/Vite) simply listens to raw grid updates from the server and transmits keyboard masks, acting as a high-frequency display terminal.

## 🎮 Running it Locally

### Prerequisites
Make sure you have [Go](https://go.dev/) (1.13+) and [Node.js](https://nodejs.org/) installed.

### Quick Start
All commands are run from the `engine/` directory.

1.  **Run backend tests** (verifies the replay harness is green):
    ```bash
    go test ./...
    ```

2.  **Build the browser client:**
    ```bash
    cd web && npm install && npm run build && cd ..
    ```

3.  **Launch the MMO server:**
    ```bash
    go run ./cmd/zzt-server
    ```

4.  Open **[http://127.0.0.1:8080](http://127.0.0.1:8080)** in multiple browser tabs to watch players interact!

## 📁 Directory Structure

```
engine/          Headless Go ZZT simulation engine & websocket server
engine/web/      Vite TypeScript browser client
fixtures/        Test worlds and replay verification hashes
saves/           Directory for saved game snapshots & chat logs
```

## 📜 Development Docs

For a deep dive into the architecture:
*   [TASKS.md](TASKS.md) — The active roadmap and milestones.
*   [IMPLEMENTATION.md](IMPLEMENTATION.md) — Protocol specifications and synchronization rules.
*   [ANALYSIS.md](ANALYSIS.md) — Low-level code maps and surgery details.
*   [NOTES.md](NOTES.md) — Running log of architectural decisions.

---

## 🤝 Credits & Special Appreciation

This project would not exist without the dedication of the ZZT preservation community and the creators who came before us:

*   **Tim Sweeney** (Creator of ZZT / Epic Games): His game and the vibrant community surrounding it have changed my life.
*   **Adrian Siekierka** ([@asiekierka](https://github.com/asiekierka)): Immense thanks for his project to reconstruct the original Turbo Pascal code of ZZT ([reconstruction-of-zzt](https://github.com/asiekierka/reconstruction-of-zzt)), and for his excellent ZZT emulator, [Zeta](https://github.com/asiekierka/zeta), whose official pixel-perfect font sheets power ZZTMMO's canvas renderer.
*   **Ben Hoyt** ([@benhoyt](https://github.com/benhoyt)): The author of the Go port ([zztgo](https://github.com/benhoyt/zztgo)) who made it possible to build on top of a modern codebase to create ZZTMMO.

This project is licensed under the **MIT License** (see [LICENSE](LICENSE)). Epic MegaGames' original content and help files are included for testing under fair-use/redistribution notices.
