# ZZTMMO — Implementation Overview

*Rewritten 2026-07-09 for the zztgo (Go) engine decision — see ANALYSIS.md for the
code-level surgery map and PLAN.md for the original evaluation. Task-level specs
with definitions of done live in TASKS.md; this file is the map, not the steps.*

## Approach

Fork of benhoyt/zztgo (MIT, ~7.3k lines of Go machine-converted from the
Reconstruction of ZZT Pascal) at `engine/`, converted milestone by milestone into
a headless, deterministic, instanced, multi-player simulation. Server-authoritative
netcode in Go; TypeScript dumb-terminal browser client. Zeta + Reconstruction as
correctness oracle. The Pascal source (`reference/reconstruction-of-zzt`) is the
behavior spec whenever the Go is unclear.

## Stack

Go engine+server (single binary, goroutine per room) · TS/Vite/canvas client ·
JSON WebSocket protocol first, binary later · SQLite persistence ·
plain VPS/Fly.io deploy (Durable Objects path closed by Go — accepted trade-off).

## Milestones

| # | Name | Exit criterion |
|---|---|---|
| M0 | Headless & deterministic (no behavior change) | Replay of TOWN.ZZT under scripted input yields identical state hashes across runs; interactive play unchanged |
| M1 | Instance & de-modal | Two Engine instances simulate different boards in one process; zero blocking UI calls in sim code (grep-proven); replay green |
| M2 | Multiple players | 3+ players on one board with per-player inventory/input/death; single-player replay fixture *unchanged* through M2.1–M2.3; passage transfer between live engines |
| M3 | Network | Real browsers co-roaming; 20-bot 1-hour soak with zero state drift |
| M4 | MMO layer | Accounts, persistence, chat, curated co-op worlds, instancing |
| M5 | Creation | Browser editor, ZZT-OOP multiplayer extensions, publishing |

## Sequencing rules

1. The M0.6 replay harness gates every commit from the moment it exists. Fixture
   changes require an explicit `DEVIATION:` commit-message line.
2. No M3 work before M2's exit criterion (the mistake that killed Marak/zztmmo).
3. All numeric/random behavior flows through the engine's seeded TP-LCG RNG.
4. Faithful over idiomatic: converted code stays Pascal-shaped; quirks get
   `// ZZT-QUIRK:` comments, never fixes.

## Design decisions of record

Per-player: health/ammo/gems/torches/keys/score, hint messages, scrolls/prompts,
board transitions, death→respawn (entry point, penalty, brief invulnerability).
Shared: board tiles/stats, world flags (quest gates open for everyone), stat
iteration order (vanilla numbering; player stats append at end).
Deleted: global pause, modal freezes, game-over/high-score modal, board time
limits (v1), PvP bullet damage (v1).
Client-side: dark-room masking by own torch (accepted cheat surface v1), sound
synthesis from SoundEvents, CP437 rendering.
