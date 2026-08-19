# Contributing to ZZTMMO

This is the developer's half of the [README](README.md): how to build the thing,
run it locally, and find your way around the tree.

## Prerequisites

[Go](https://go.dev/) and [Node.js](https://nodejs.org/).

## Quick Start

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

3.  **Put a world where the server can load it:**
    ```bash
    cp ../fixtures/TOWN.ZZT .
    ```
    > `.ZZT` files in `engine/` are gitignored, so a fresh clone has none. The
    > server loads its startup world from the directory it runs in, and classic
    > TOWN ships as a committed fixture. Add any other worlds the same way; the
    > picker lists everything in the `-worlds` directory.

4.  **Launch the MMO server:**
    ```bash
    go run ./cmd/zzt-server -world TOWN -web web/dist -help . -saves saves
    ```

5.  Open **[http://127.0.0.1:8080](http://127.0.0.1:8080)** in multiple browser tabs.

## Server Flags

*   `-addr :8080` sets the HTTP/WebSocket listen address.
*   `-world TOWN` chooses the starting `.ZZT` world basename (this is also the default).
*   `-board 1` chooses the default starting board.
*   `-web web/dist` points at the built browser client.
*   `-help .` points at the directory containing ZZT `.HLP` files.
*   `-saves saves` enables saved-room snapshots and persistent chat logs. Use an empty value to disable saving.
*   `-worlds .` is the directory of hosted `.ZZT` worlds, and where the browser editor publishes.
*   `-autosave 60` sets seconds between autosaves of occupied rooms; `0` disables.
*   `-fresh` skips restoring autosaves at boot, for a deliberately clean start.
*   `-record <dir>` writes deterministic session recordings; empty disables recording.
*   `-replay <dir>` is where `/replay/<id>` loads recordings from; empty uses `-record` when set.
*   `-friendly-fire` lets player bullets damage other players in every world the server hosts. On by default; pass `-friendly-fire=false` for a co-op server. This is the only control over PvP, and no world file can override it.
*   `-shutdown-grace 60s` warns connected players on SIGINT/SIGTERM and waits this long before stopping so they can save; `0` stops immediately.

## Testing

`go test ./...` must pass before every commit.

The real-browser (Playwright) suites are **opt-in**: they declare-skip unless
`ZZT_BROWSER=1`, so a one-line engine change does not pay ten minutes of
browsers. A change under `engine/web/src` runs `make browser` before its commit
— that target builds the client, runs its node unit suite, and runs the whole
family. Touching the protocol, or anything else a browser reads, earns the same
run.

`make certify` sets `ZZT_PARITY_REQUIRE_BROWSER=1`, under which a missing
browser suite is a hard failure and any undeclared skip blocks certification.

## Repository Layout

```
engine/              Headless Go ZZT simulation engine, websocket server, and commands
engine/web/          Vite TypeScript browser client
engine/saves/        Local saved-game snapshots and chat logs when enabled
fixtures/            Replay, oracle, golden and parity fixtures (repository root)
llmworld/            ZWD corpus, prompt kit assets, and generated worlds
oracle/              Pinned vanilla ZZT oracle harness (maintainer tooling)
deploy/              systemd units and scripts for the hosted server
docs/                Design documentation (see docs/generation.md)
reference/           Local reference checkouts, ignored by git
```

## House Rules

The engine is a fork of [benhoyt/zztgo](https://github.com/benhoyt/zztgo),
machine-converted from Adrian Siekierka's Pascal reconstruction. Two rules
follow from that and are not negotiable:

*   **Never guess ZZT behavior.** When the Go is unclear, read the same function
    in the Pascal. Port quirks and bugs faithfully and mark them `// ZZT-QUIRK:`
    rather than fixing them.
*   **Determinism is sacred.** No `math/rand` global, no `time.Now()`, no
    `time.Sleep`, and no map iteration that affects game state order, anywhere in
    simulation code. All randomness goes through the engine's seeded RNG.

Replay fixtures are the safety net for both. Never edit a fixture hash or delete
a replay test to make a build pass; if a behavior change is intentional, say so
in the commit message with a `DEVIATION:` line.
