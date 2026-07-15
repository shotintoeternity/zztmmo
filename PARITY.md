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
| `mobile-touch-gap` | Mobile ships text entry only; touch movement/shoot/torch/pause is a filed gap task, not shipped | E | task M15.1; owner decision 2026-07-15 (see §5) |

Rows whose behavior is one of these divergences set `parity: "deviation"` and
`deviation: "<id>"`, and get a focused projection/boundary test in their
assigned sweep.

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
