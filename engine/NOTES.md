
## 2026-07-11 — Dream-a-world generation failure taxonomy (planning for M12.13/M12.14)

Pulled from one crashed-server session log (2 worlds, ~16 board paints). EVERY
repair round was mechanical bookkeeping, never a creative failure. The LLM is
asked to keep two representations in byte-for-byte agreement — the ASCII grid
AND a separate `stats` list with exact `at X,Y` coordinates — which it cannot do
reliably. Counts from that one session:

- **`grid contains stat-backed element X but no matching stat is defined at (x,y)`
  — 5+, and Saga Archive burned all 3 repair attempts on it (never converged).**
  DOMINANT failure. The model draws an Object/Passage glyph in the grid but does
  not declare a matching stat (or declares it at the wrong coord). Compiler check
  is the reverse-direction loop at `zwd.go:786-805` (error at `:801`);
  stat-backed set is `elementNeedsStat` (`zwd.go:817`).
- `duplicate legend key` (`zwd.go:348`) — 1.
- `unknown stat field <name>` (`zwd.go:612`) — 1.
- `board <name> missing end` (`zwd.go:204`) — 1 (truncated/malformed section).
- prose-in-grid undefined chars — fixed in M12.11.
- plan-level exit reciprocity — already auto-repaired by the plan validator.

Strategy (owner direction 2026-07-11): move more mechanical burden onto the
compiler / `preprocessZWDGrid` tolerance layer so LLM slips are absorbed
deterministically instead of bouncing through the slow, token-costly, sometimes
non-converging repair loop. `preprocessZWDGrid` already pads rows, expands RLE,
positions the player, snaps DECLARED stats to their nearest glyph, and (M12.11)
absorbs undefined grid chars. The gaps that remain are the orphan-glyph direction
(M12.13) and the other recurring compiler rejections (M12.14). Principle: the
compiler owns everything deterministic (coordinates, bookkeeping, RLE, padding,
legend completeness, structural closing); the LLM owns only semantic/creative
choices (layout, palette, which entities, their words). Derive, don't require.
## 2026-07-11 — M12.12 malformed doors and room panic containment

ZWD is the generated-world security boundary: the compiler now rejects raw
`Door` colors whose key/background nibble is `0` or `8`, since neither names a
vanilla key. The documented named shorthand remains valid (`Door color blue`
becomes `0x1F`; the other six key names map similarly). The simulation also
guards `key == 0` and treats a malformed door as locked, because imported DOS
worlds can still carry one.

`RoomManager.StepDiffs` recovers separately around each room's simulation step,
logs the panic, and drops the affected room and its players rather than keeping
a partially-mutated engine. `WorldInstance.Tick` (and the legacy tick path) has
an outer recovery guard as a final boundary. These safeguards are presentation /
server control flow only: neither changes `StateHash` nor the replay fixture.
