# ZZTMMO

A multiplayer browser version of [ZZT](https://en.wikipedia.org/wiki/ZZT), Tim
Sweeney's 1991 DOS game.

The engine is a fork of [benhoyt/zztgo](https://github.com/benhoyt/zztgo) — Go,
machine-converted from the original Pascal — being turned into a headless,
deterministic, server-authoritative simulation that many players can share. The
browser client is a dumb terminal: it renders server state and sends keymasks.
Nothing about the game is simulated client-side.

**Status: work in progress.** The engine is multiplayer and networked; real
browsers can co-roam a board today. It is not yet a complete, playable ZZT.

## Why this is harder than it looks

Original ZZT is a single-player DOS program with globals everywhere, blocking
modal UI in the middle of the simulation loop, and behavior that depends on
frame timing. Making it multiplayer means unpicking all three without changing
what the game *does* — because ZZT's charm lives in its bugs as much as its
features.

So the project has one governing rule: **faithful beats clean.** Converted code
stays Pascal-shaped. Quirks get a `// ZZT-QUIRK:` comment, never a fix. When the
Go is ambiguous, the Pascal is the spec.

The safety net is a deterministic replay harness. It runs `TOWN.ZZT` under
scripted input and asserts identical state hashes across runs, and it gates every
commit. That means no `math/rand`, no `time.Now()`, no `time.Sleep`, and no
map-iteration order affecting game state anywhere in simulation code — all
randomness flows through the engine's seeded RNG.

## Progress

| Milestone | | Exit criterion |
|---|---|---|
| M0 — Headless & deterministic | done | Replay of TOWN.ZZT yields identical state hashes across runs; interactive play unchanged |
| M1 — Instance & de-modal | done | Two engines simulate different boards in one process; zero blocking UI calls in sim code |
| M2 — Multiple players | done | 3+ players on one board with per-player inventory, input, and death; passage transfer between live engines |
| M3 — Network | in progress | Real browsers co-roaming; 20-bot soak with zero state drift |
| M4 — Browser UI parity | not started | Full keyboard, modal windows, sound, CP437 fidelity |
| M5 — Creation | not started | Browser editor, ZZT-OOP multiplayer extensions, publishing |
| M6 — Chat & identity | not started | Accounts, persistence, chat |

## Running it

Everything below runs from `engine/`.

```sh
go test ./...          # replay harness — must be green before any commit
go run ./cmd/zztgo     # the original single-player game, in your terminal
```

For the multiplayer server, build the browser client first:

```sh
cd web && npm install && npm run build && cd ..
go run ./cmd/zzt-server
```

Then open <http://127.0.0.1:8080>. Open it twice to see two players share a
board. The server takes `-addr`, `-world`, `-board`, `-web`, and `-help` flags;
defaults serve `TOWN` on `:8080`.

## Layout

```
engine/          the zztgo fork (Go)
engine/web/      the TypeScript browser client (Vite)
fixtures/        test worlds and recorded replay hashes
reference/       pristine upstream sources — gitignored, re-clone to populate
```

`reference/` is not checked in. To populate it:

```sh
git clone https://github.com/benhoyt/zztgo reference/zztgo
git clone https://github.com/asiekierka/reconstruction-of-zzt reference/reconstruction-of-zzt
```

## Docs

- `IMPLEMENTATION.md` — milestone map and exit criteria
- `TASKS.md` — the task list and the protocol for working through it
- `ANALYSIS.md` — code analysis with a file:line surgery map
- `NOTES.md` — running log of decisions and escalations
- `PLAN.md` — original background and rationale (its language choice is superseded)

## Credits and license

The code is MIT licensed — see [LICENSE](LICENSE). ZZT itself is Tim Sweeney's,
and the help files and `TOWN.ZZT` shipped here are Epic MegaGames' original
content, redistributed for testing. See [NOTICE.md](NOTICE.md).

Thanks to Ben Hoyt for zztgo and to Adrian Siekierka for the Pascal
reconstruction that made it possible.
