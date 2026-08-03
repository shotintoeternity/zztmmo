# PARITY.md — the ZZTMMO feature-parity contract

This document is the human-readable half of the M16 whole-product
feature-parity proof. Its machine-readable half is
`fixtures/parity/manifest.json`, validated by `TestParityManifest*` in
`engine/parity_manifest_test.go`. Neither may drift from the other: the
validator fails the build if an inventoried surface has no manifest row, or a
row points at a test/fixture that does not exist.

**Status of this milestone (M16.0):** this task defines the contract, the
manifest, the validator, and the seeded deviation catalog. It records *no*
oracle evidence — every `V`/`P` behavioral row is `unverified` and assigned to
the later M16 task that will certify it. M16.0 is complete when the owner (and,
when available, the advisor) approve the contract and deviation list below; the
independent oracle (M16.2) records no fixtures until then.

---

## 1. What "perfect feature parity" means here

Parity is not one relationship. Every inventoried surface carries exactly one
**contract** naming which of three it must satisfy:

- **`V` — Vanilla.** Single-player simulation and file behavior match ZZT 3.2.
  The reconstructed Pascal in `reference/reconstruction-of-zzt/SRC` is the
  semantic authority. Committed captures that prove a `V` row must come from an
  **independent** executable implementation of that authority (M16.2's oracle),
  never from this Go engine comparing against itself.
- **`P` — Projection.** Given the same authoritative engine state, the
  RoomManager, JSON protocol, real browser client, renderer, controls, modals,
  and sound expose the same playable result as the terminal path. A `P` row is
  proven by showing the projected artifact (protocol snapshot, browser canvas,
  audio graph) reconstructs the authoritative state.
- **`E` — Extension.** Multiplayer, persistence, accounts/chat, editor
  collaboration, Museum, and Dream have no vanilla counterpart. An `E` row is
  proven against its completed task's own contract (DoD), and it must preserve
  the `V` projection for each player except where an owner-approved deviation
  (§4) says otherwise.

A surface with no parity claim at all carries the contract `out-of-scope` and
must justify itself in the row's `notes` (e.g. a terminal-only convenience, or
a feature deliberately not built yet).

**No percentage or coverage number is a parity claim.** Certification (M16.20)
means every manifest row is `pass` or an owner-approved `deviation` — with no
`unverified`, `unknown`, `gap`, skipped required fixture, or open surface.

---

## 2. The inventory dimensions

The manifest inventories nine dimensions. Five are **mechanically derived** from
code at validation time, so a newly added surface *cannot* be silently
unlisted — the validator fails until a row is added. Four are **curated** lists
whose completeness a human maintains, but whose internal consistency the
validator still enforces.

| Dimension | Derivation | Authority for derivation |
|---|---|---|
| `task` | derived | checked `- [x] **M…` boxes in `TASKS.md` (M0–M15 non-hygiene, plus M17 shipped live fixes) |
| `element` | derived | `ElementDefs[i]` draw/tick/touch procs that differ from the defaults, via reflection after `InitElementDefs()` |
| `oop` | derived | `e.OopWord == "…"` command/condition/direction/counter literals scanned from `oop.go` |
| `protocol` | derived | `MessageType… = "…"` consts and `ProtocolEvent{Type: "…"}` literals scanned from `protocol.go` |
| `route` | derived | `mux.HandleFunc("/…"` registrations scanned from `web_api.go`, plus the `/ws` upgrade |
| `oop-structural` | curated | ZZT-OOP forms not reducible to one dispatch word: `:label`, `@name`, `#`, text lines, `!link;text` hyperlinks, `#play`/`;` sound, comments |
| `input` | curated | play/title/editor key vocabulary (`ElementPlayerTick`, `GameTitleLoop`, `editor.go`), cross-checked against the client `keys.ts` |
| `browser-mode` | curated | `web/src` modes and modal surfaces (title, playing, editor, and each CP437 window family) |
| `service` | curated | shipped end-to-end workflows (save/restore, high scores, museum, auth, chat, dream, session record/replay, editor collaboration, world picker) |

The derived dimensions are the fail-closed backbone: add a ZZT-OOP command, a
protocol event, an element proc, an HTTP route, or check a task box, and
`go test ./...` goes red until the manifest gains a matching row. This is the
mechanism M16.6 asks for ("a newly added command cannot be unlisted"),
generalized to every mechanical surface.

---

## 3. Manifest schema

`fixtures/parity/manifest.json`:

```jsonc
{
  "schemaVersion": 1,
  "rows": [
    {
      "id":           "elem.player.tick",   // stable, unique, kebab/dotted
      "dimension":    "element",            // one of the nine above
      "subject":      "E_PLAYER TickProc (ElementPlayerTick)",
      "contract":     "V",                  // V | P | E | out-of-scope
      "authority":    "ELEMENTS.PAS ElementPlayerTick; tasks M2.2–M2.4",
      "parity":       "deviation",          // exact | deviation
      "deviation":    "mp-respawn",         // catalog id; required iff parity=deviation
      "test":         "",                   // Go/TS test name(s) that certify it; empty until assigned
      "fixture":      "",                   // required fixture path, if any
      "status":       "unverified",         // pass | unverified | deviation | gap | out-of-scope
      "assignedTask": "M16.5",              // the M16 task that will certify/close it
      "notes":        ""
    }
  ],
  "deviations": [ /* §4 catalog */ ]
}
```

### Status vocabulary and the rules the validator enforces

- **`pass`** — certified now by an existing named test. Requires a non-empty
  `test` naming a test that exists. (At M16.0 almost nothing is `pass`; the
  oracle is not recorded yet.)
- **`unverified`** — will be certified by `assignedTask`. **Permitted only when
  `assignedTask` is a later M16 task** (`M16.1`–`M16.20` or a filed M16 gap
  task). This is the M16.0 DoD rule.
- **`deviation`** — an owner-approved intentional divergence. `parity` must be
  `deviation` and reference a `deviations` catalog entry. Still carries an
  `assignedTask` (the projection/boundary test that pins the deviation).
- **`gap`** — a known product defect or unbuilt claim with a filed gap task.
  `assignedTask` points at that gap task, which must land before M16.20.
- **`out-of-scope`** — no parity claim; `contract` is `out-of-scope` and `notes`
  justifies it.

Additional validator invariants: unique `id`s; every derived inventory item has
exactly one row and every `element`/`oop`/`protocol`/`route`/`task` row maps to
a real derived item (no orphans/stale rows); every `test` names an existing test
and every `fixture` names an existing file (no stale references); every
`deviation` reference resolves to the catalog; every `assignedTask` is a real
M16 task id.

### Regenerating the manifest (task M16.20a)

`PARITY_SCAFFOLD=1 go test -run TestParityManifestScaffold ./` rewrites the file
from code. Regeneration is **additive**: it adds rows for new code surfaces and
changes nothing else.

- The deriver owns `id`, `dimension` and `subject` — the mechanical description
  of a surface. Everything else is curator-owned: an on-disk value always wins,
  **including an empty one** (a sweep that cleared `assignedTask` on a passing
  row meant it). Derived values only populate rows new to the manifest.
- Rows the deriver cannot re-derive — a curated row a later task hand-added — are
  carried forward. Deleting one requires naming its id in `PARITY_SCAFFOLD_DROP`,
  and the scaffold refuses ids that are still derived or not in the manifest.
- The scaffold logs what it added, preserved, dropped, and where an on-disk value
  overrode a differing derived one, so no merge decision is silent.
- `TestParityManifestIsCanonical` asserts the committed file is byte-for-byte
  what a regeneration writes. If it goes red, regenerate and commit — never
  weaken the merge.

---

## 4. Seeded deviation catalog

These are the intentional, already-landed divergences from vanilla, seeded here
rather than rediscovered during the sweeps. Each is grounded in a landed
`DEVIATION:` note (TASKS.md/NOTES.md) or the M16 contract text. **This list is
the owner-approval surface for M16.0.**

| id | title | contract | authority |
|---|---|---|---|
| `mp-respawn` | Death is a respawn (score penalty + brief invulnerability), not game-over | E | tasks M2.4, M4.3; NOTES 2026-07-09 |
| `collision-pushout` | Two players on one square: the arriving player is pushed to a free adjacent square, never overlapping | E | task M4.3b; `placement.go` |
| `friendly-fire-policy` | Multiplayer projectile/contact damage between players follows an explicit friendly-fire policy, not vanilla's single-player assumption | E | task M2.4; `Engine.FriendlyFire` |
| `per-player-modal-freeze` | Scroll/pause/save/quit/help/debug modals freeze only the acting player; the room keeps ticking for everyone else | E, P | tasks M1.3, M3.11 |
| `shared-world-flags` | `World.Info.Flags` are shared across all players in a world (co-op puzzle progress) | E | task M2.1; NOTES 2026-07-09 |
| `snapshot-player-drop` | A restored room snapshot drops other players; a joiner arrives fresh at the start square (World.Info holds one player's stats) | E | task M4.3a |
| `account-sidecar-restore` | Persistent per-player state (keys/inventory) lives in an account sidecar, not the world snapshot | E | tasks M4.3a, M15/persistence |
| `omitted-game-speed` | The monitor/title menu omits vanilla's `S` game-speed control — the server owns the tick | E, P | task M4.3 |
| `score-on-quit` | A high score is entered on quit, not on death (death is a respawn) | E | task M4.3 |
| `wasd-removed` | Movement is arrows + numpad `8/4/6/2` only; WASD (a client invention) was removed because `S` collides with the save key | P | task M4.2; NOTES |
| `per-player-sound` | Pickup/shot/damage sounds are attributed to the acting player; only `#play` from an object's own tick stays room-wide | E, P | task M7.4 |
| `scroll-removal-timing` | A windowed scroll is consumed when its reply arrives, not the instant it is touched (de-modal design) | P | task M17.4 |
| `presentation-additions` | Presentation-only additions with no vanilla counterpart: launch name popup, world picker, player-identity overlay, chat panel, sound-toggle UI, Dream flow, help/debug windows | P, E | tasks M3.8–M3.10, M4.x, M6.x, M12.5 |
| `mobile-touch-gap` | Mobile ships text entry only; touch movement/shoot/torch/pause is a filed gap task, not shipped | E | task M15.1; owner decision 2026-07-15 (see §5) — **resolved 2026-08-01: M16.18a built the controls, `mode.mobile-touchplay` is `pass` with `parity: exact`, and no row references this id any more. The catalog entry stays because the catalog is the owner-approval record of what was once approved, not a list of what is still true.** |

Rows whose behavior is one of these divergences set `parity: "deviation"` and
`deviation: "<id>"`, and get a focused projection/boundary test in their
assigned sweep.

**Deviations a row cannot name (M16.20 reconciliation, 2026-08-01).** A row
carries at most one `deviation` id, so a surface that diverges twice can only
name one, and three approved deviations end up referenced by no row at all.
They are live, and each is pinned by name — listed here so the catalog is not
read as "three approvals nobody uses":

| deviation | where it actually lives | pinned by |
|---|---|---|
| `collision-pushout` | `elem.player` (which names `mp-respawn`), `task.M4.3b` | `TestM43bTwoPlayersReenterSameSquare`, `TestM43bTwoPlayersRespawnSameSquare`, `TestM43bNoOpenSquareStaysPut` |
| `shared-world-flags` | `task.M2.1`, `task.M14.0`, the world-scope seam | `TestRoomManagerSharesWorldFlagsAcrossLiveRooms`, `TestRoomManagerFlagVisibleToLaterRoomSameTick`, `TestRoomManagerFlagSurvivesFreezeThaw` |
| `scroll-removal-timing` | `task.M17.4`, the scroll/modal surface | `TestScrollHyperlinkReplyGrantsRewardThenConsumes`, `TestScrollDismissConsumesWithoutReward`, oracle normalization `oracle-modal-hyperlink` |

`mobile-touch-gap` is referenced by no row for the other reason: M16.18a built
the controls, so it is approved history rather than current behaviour (§5).

---

## 5. Resolved scope claims (owner decisions, 2026-07-15)

The M16 contract requires broader-than-landed claims to be narrowed with owner
approval or turned into gap tasks. Resolved:

- **Mobile playability → gap task.** M15.1 shipped mobile text entry (the
  on-screen keyboard for prompts/chat) but no touch movement/shoot/torch/pause.
  Rather than narrow the claim, the owner chose to **file a touch-controls gap
  task** (M16.18a, blocks M16.20). Until it lands, the `browser-mode` /`service`
  row for touch gameplay is `gap` (assigned to M16.18a) under deviation
  `mobile-touch-gap`; mobile **text entry** is a normal `E` row.

  **Settled again on 2026-07-30 and certified by M16.18 (2026-08-01).** The owner
  deferred M16.18a past the beta and scoped the product copy to desktop browsers
  with a keyboard, so M16.18 certified mobile **text entry** —
  `mode.mobile-textentry` is now `pass` — and left `mode.mobile-touchplay` at
  `gap`. `TestM1618ProductCopyMakesNoTouchGameplayClaim` holds the two together:
  while that row is `gap` the README must carry the desktop scope and must not
  claim phone play, and when M16.18a lands the requirement lifts itself.
  M16.18's own artifact is `fixtures/parity/device-matrix.json` (§7a).

  **Closed 2026-08-01 by M16.18a**, the way the 2026-07-15 decision said it
  would be: by building the controls, not by narrowing the claim.
  `touch_controls.ts` now offers Fire, Torch and Pause beside the direction pad,
  each one a key the keyboard already sends — Fire *is* the space bar, which is
  why one button covers both of vanilla's firing shapes (alone it repeats along
  the facing; held with a pad direction it fires along that direction, because
  the shoot bit sets `Shift` in `inputMessageToPlayerInput`). A control is only
  on screen where it means something, so a Fire tap cannot put a space in an
  open text buffer. `mode.mobile-touchplay` is now `pass` with `parity: exact`,
  like its `mode.mobile-textentry` sibling, and the deviation `mobile-touch-gap`
  is referenced by no row (§4). The evidence is the two Chromium touch profiles
  in the device matrix, which declare `touchplay` and play the CONTROL world
  with **no keyboard at all** — walk, shoot both ways, carry a torch to the dark
  board and light it, pause and un-pause — every act tick-locked on the input
  frame the server received, plus a focus/leak check behind an open chat
  composer. `TestM1618DeviceMatrixIsWellFormed` fails if the matrix stops
  declaring a `touchplay` profile, so the claim cannot outlive its run. The
  README's scope paragraph moved with it. **M16.18b closed the same day**: the
  bar publishes its measured height and the screen letterboxes above it rather
  than under it, so the rows it used to cover on a landscape phone (18-24, the
  end of the board and the sidebar's Save/Pause/Quit block) are declared empty
  on every profile and the matrix asserts the reservation itself.

- **M17 live fixes are in scope.** M17.1–M17.4 (name-popup centering,
  world-picker metadata, audio regression, scroll-hyperlink consume) are checked
  shipped fixes the player relies on. Each gets a `task` row and a regression
  fixture in the sweep that owns its feature area, so a shipped fix cannot
  silently regress.

---

## 6. How the later M16 tasks consume this

- M16.1 turns the manifest into a runnable, immutable evidence report.
- M16.2 stands up the independent oracle; only then may `V` rows begin flipping
  to `pass`.
- M16.3–M16.7 certify the `V` sweeps (player/terrain, movers/devices,
  creatures/combat, OOP/scroll/sound, world/title/file).
- M16.8–M16.12 certify `P` (engine↔room↔protocol, browser visual/control/audio,
  end-to-end journeys, multiplayer invariants).
- M16.13–M16.19 certify the `E` services (editor solo/collab, persistence,
  auth/chat/museum, ZWD/Dream, production boundary/load).
- M16.20 reconciles every `task` row and README/TASKS claim to the evidence and
  performs the clean-clone certification. Only M16.20 may state the product has
  full parity within this contract.

When a sweep certifies a row it flips `status` to `pass` (or `deviation`) and
fills `test`/`fixture`. When a sweep discovers a defect it files a small M16 gap
task and sets the row to `gap`. The validator keeps everyone honest in between.

---

## 6a. The device/browser matrix (M16.18)

`fixtures/parity/device-matrix.json` is the platform half of the contract: which
browser engines and screens the client is certified on, which text surfaces each
one exercises, and the reason for anything it does not. It is authored by hand
and reviewed, not written by a run.

Two gates hold it to reality, from opposite sides. `TestM1618PlatformMatrix`
runs every profile the file marks `covered` — a real Chromium, Firefox or WebKit
at that viewport and deviceScaleFactor, driving the built client on M16.9's
tick-locked harness — and fails if a run covered less, or more, than the profile
declares. `cmd/zzt-parity` renders the file into `report.json`/`report.md` and
refuses to certify while any profile is skipped with no reason, is covered with
no evidence, or carries an unknown status. So "the matrix contains no unexplained
skip" is a property of the artifact, and "the matrix describes what actually
ran" is a property of the test.

The inventory of text surfaces is derived from the client itself — the modal
kinds `modalAcceptsTextInput` (`web/src/modal.ts`) accepts — so a seventh
editable modal cannot be added without the matrix going red.

A profile may also declare `touchplay` (M16.18a), which asks that run to play
the world with no keyboard at all through the on-screen controls. Only a profile
whose engine reports touch points can: the control bar is decided once at load
from `navigator.maxTouchPoints`, so an engine that merely delivers touch events
gets no bar and nothing to play with. The declaration is checked from both sides
like the surface list — a run that skipped an act fails, a run that performed
acts its profile does not declare fails, and a matrix with no `touchplay`
profile at all fails because `mode.mobile-touchplay` would then be `pass` with
nothing behind it.

## 7. The vanilla oracle (M16.2)

The independent ground truth every `V` sweep compares against is the **real
ZZT.EXE v3.2** (Museum of ZZT `zzt.zip`, Epic's freeware release) running under
a pinned build of the Zeta 8086 emulator with a custom headless frontend
(`oracle/frontend_oracle.c`): virtual clock, scripted keyboard schedule,
checkpoints read from emulated text VRAM, PC-speaker transitions logged. Every
input is sha256-pinned in `fixtures/oracle/provenance.json`. Captures are
committed; `make oracle-regen` is the only way to regenerate them, and tests
never run the oracle (`oracle/README.md`).

**Compared surface.** The 80x25 text page is what a vanilla player observes and
what the oracle exposes: board cells are compared raw; player/world counters
(including a timed board's remaining `Time:`) are parsed from the sidebar and
compared against engine state; title-screen checkpoints (taken before `play`)
compare board cells only, since the sidebar shows the menu; modal text windows
are compared by content; speaker tone onsets are compared as frequency
sequences through an ISR-faithful matcher — vanilla plays one melody at a time
and a newly accepted `SoundQueue` replaces whatever is still sounding, so each
engine-queued melody may match only a prefix, a mid-play melody's remaining
onsets may land after the checkpoint, and drum bursts match by onset count
(several drum tables draw frequencies from `Random()` seeded at the oracle's
boot). That sound leniency is one-sided: every tone the oracle did play must
appear, in order, at the engine's queue positions. Board tiles/stats/RNG are
covered via their screen projection — memory-level capture out of the emulated
data segment is a possible later extension, not part of this seam.

**Scenario-design exclusions.** No checkpoint depends on a message that crossed
the initial pause boundary, where vanilla's global freeze and the fork's
per-player freeze (deviation `per-player-modal-freeze`) tick board stats
differently (M16.3). Sweep scenarios avoid vanilla's self-shot ricochet damage,
which the friendly-fire policy suppresses (deviation `friendly-fire-policy`,
pinned by the M16.5 bullet row). M16.5 adds one more: no scenario drives the
player to 0 health, because death is the `mp-respawn` deviation rather than
vanilla's game over (`TestSinglePlayerDeathIsRespawnDeviation` carries that
branch instead). M16.5's second exclusion — no scenario fires or receives a
point-blank shot, because `BoardShoot`'s ownership test was inverted against
GAME.PAS:1246 — is **lifted**: gap task **M16.5a** restored vanilla's sense and
added ORCLFIRE's Blank Bay, which fires point-blank in both ownership directions
and stands the player next to a spinning gun in its own row.
M16.3's exclusion of energized checkpoints is
**lifted**: it existed only because the adapter pinned CurrentTick to 0, and the
M16.4 phase solver recovers it instead, so ORCLHUNT compares the energizer flash
like any other cell.
M16.6 adds two of its own. **No scenario runs `#endgame`**, which sets health to
0 and therefore lands in the same `mp-respawn` territory death does
(`TestOopEndgameLeavesThePlayerInLimbo` carries that branch; task **M16.6a**
routed it through the same death/respawn path `DamageStat` uses). And **the
four random OOP directions are
compared by outcome SET, not draw by draw**: vanilla's `RandSeed` is seeded from
its own boot clock and is not this engine's, so no exact-cell comparison can
follow an individual `RND`/`RNDNS`/`RNDNE`/`RNDP` draw across the seam. What
ORCLWALK compares instead is draw-invariant and holds for thousands of draws on
both sides — a chamber whose every legal outcome is walled must report `blocked`
every time, and an object in open ground must never report `blocked`, so a draw
outside the legal set or one that came back as the object's own square turns the
object's glyph and the checkpoint names the cell.

**Documented representation normalizations** (the complete list — anything else
that differs is a defect):

| id | normalization | grounded in |
|---|---|---|
| `oracle-pause-blink` | Vanilla's interactive loop draws a paused player blinking (`02`/`1F` alternating with the square's content); the headless engine emits `PauseEvent` and leaves drawing to the client. The paused player's square accepts either blink phase. | deviation `per-player-modal-freeze` |
| `oracle-modal-scroll` | Vanilla freezes the sim inside a modal text window drawn over the board; the engine emits `ScrollEvent`. Checkpoints with an open window compare window text against the event's lines/title. | deviation `per-player-modal-freeze` (M1.3) |
| `oracle-modal-hyperlink` | Vanilla draws a `!label;text` line as its caption alone (TXTWIND.PAS `TextWindowDrawLine` copies from the `;`) and runs the chosen label inside the same modal `OopExecute`; the engine emits the raw line in a `ScrollEvent` and re-enters on the reply. A window checkpoint compares captions, and the adapter plays the client the fork expects — cursor keys move a line cursor, ENTER/ESCAPE answer through `SubmitScrollReply`. | deviations `per-player-modal-freeze`, `scroll-removal-timing` |
| `oracle-sidebar-prompt-line` | `GamePromptEndPlay`'s `SidebarPromptYesNo` ("End this game? ") and `GameDebugPrompt`'s `PromptString` (an 11-wide field) draw into the sidebar at (63,5), which the board-cell comparison (columns 0-59 only) never inspects; the engine emits `QuitPromptEvent`/`DebugPromptEvent` and keeps ticking. A checkpoint taken while one is open is asserted against the oracle's own sidebar text alone — there is no engine-drawn pixel to compare it to. | deviation `per-player-modal-freeze` (M3.9/M3.11) |

---

## 8. The certification run (M16.20)

`make certify` is the whole gate in one command: `cmd/zzt-parity` with
`-require-certified`, which turns "not yet certified" from an expected state
into a failure. `make parity` is the same run without that gate.

**The gate list, in order.** Order is part of the contract, not a convenience:

| # | gate | dir | why here |
|---|---|---|---|
| 1 | `npm ci` | `engine/web` | the client's pinned dependencies |
| 2 | `npx playwright install chromium firefox webkit` | `engine/web` | the three engines the device matrix (§6a) covers |
| 3 | `npm run build` | `engine/web` | the built bundle the server serves and every browser suite drives |
| 4 | `npm test` | `engine/web` | the TypeScript unit suites |
| 5 | `go build ./...` | `engine` | |
| 6 | `go vet ./...` | `engine` | |
| 7 | `go test -count=1 ./...` | `engine` | includes the real-browser, service, security and bounded-load tracks |
| 8 | `go test -race -count=1 ./...` | `engine` | the required race job |

The browser track runs **first** because the real-browser suites live inside
`go test ./...` and skip themselves when the harness is absent. Installing it
afterwards — which is what the pre-M16.20 list did — let a clean clone certify
itself with every browser suite silently skipped.

**The browser suites are opt-in everywhere but here** (owner decision
2026-08-01). They declare-skip unless `ZZT_BROWSER=1`, so an everyday
`go test ./...` is under a minute instead of ten; the `go test` gate above sets
`ZZT_PARITY_REQUIRE_BROWSER=1`, which makes them mandatory AND makes an absent
harness a failure. The `go test -race` gate deliberately does not: racing twelve
Playwright suites doubled the certification run for a class of finding the
wire-level concurrency tests already cover, and the report records — gate by
gate — that they sat that one out.

**No silent skips.** The two go gates run under `go test -json`, and every
skipped test is recorded by name with the reason it printed. A skip blocks
certification unless the test declares itself by beginning its skip message with
`declared skip:`. The run also exports `ZZT_PARITY_REQUIRE_BROWSER=1`, which
turns the browser harness's own "not installed" skips into failures: by that
point the engines have been installed, so an absent browser is a broken
certification rather than an environment fact. The report lists every skip,
declared or not, so a reader sees what did not run without reading a log.

**What the run publishes** (all gitignored, all uploaded by the CI `parity` job):

| file | contents | deterministic? |
|---|---|---|
| `fixtures/parity/manifest.json` | the claim itself (committed) | yes |
| `fixtures/parity/device-matrix.json` | the platform claim (committed) | yes |
| `fixtures/parity/report.json` / `.md` | rows, gates, skips, device matrix, blockers | **yes — byte-identical for one tree** |
| `fixtures/parity/run.json` | commit, tree-dirty flag, OS/arch, go/node/npm/playwright versions, per-gate wall clock | no (that is the point) |
| `fixtures/parity/load-metrics.txt` | M16.19's measured 30-client numbers, captured from the run | no |
| `engine/web/test-results/**` | browser diffs, screens and traces | no |

Timings and tool versions are deliberately kept **out** of the report: two runs
of one tree must render the same report bytes, and a duration is a fact about
the machine rather than about the tree.

**Fail-closed.** The manifest gate is proven closed rather than assumed: point a
row's `test` at a name that does not exist, or its `fixture` at a path that does
not, and `TestParityManifest` fails; set a row to `pass` with no test, leave one
`unverified`, or let a suite skip undeclared, and `certificationBlockers` refuses
to certify. `cmd/zzt-parity/report_test.go` holds each of those as its own test,
and the M16.20 session performed the perturbation live (NOTES.md 2026-08-01),
as did the certification session below (NOTES.md 2026-08-02).

**The certification runs (2026-08-02, commit `95982bd`).** Two independent clean
`git clone`s, each run through `make certify` sequentially — a clone carries no
untracked world, no `node_modules` and no build cache, which is the point.
Both: 8/8 gates PASS, `371 rows | verdict: CERTIFIED`, 357 `pass` /
5 `deviation` / 9 `out-of-scope` / 0 `unverified` / 0 `gap`, 23 skips all
declared, no blockers, `git status` empty afterwards. The reports are
byte-identical between the two runs (`report.json` sha256 `27e95781…`,
`report.md` sha256 `e88d57be…`); the two run records differ only in wall clock
(384s and 413s).

**Certified 2026-08-02.** The owner read both reports, approved the five
`deviation` rows and the nine `out-of-scope` rows, and ticked M16.20. The
product therefore has full feature parity **within this contract** — meaning
this document's 371-row manifest, with those five approved deviations and nine
out-of-scope rows, on the platforms §6a covers. It does not mean bug-for-bug
identity with vanilla ZZT in the abstract, and it is a claim about the tree at
`95982bd`: it survives only as long as `make certify` keeps passing, which is
why CI runs it on every push. One half of the gate was **waived rather than
met** — the advisor's independent review of the oracle chain, for want of the
tool since M16.0; `TestM1620OracleInputsMatchTheirPinnedHashes` and
`TestM1620EveryOracleCaptureHasAPinnedScenario` are what mechanically stands in
its place.
