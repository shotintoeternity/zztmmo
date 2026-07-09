# ZZTMMO

Multiplayer browser version of the 1991 DOS game ZZT. Engine: a fork of
benhoyt/zztgo (Go, machine-converted from the original Pascal) being converted
into a headless, deterministic, multi-player, server-authoritative simulation.
Client (later milestones): TypeScript dumb terminal — renders server state,
sends keymasks.

## Doc map (read in this order when context is needed)

- `TASKS.md` — **start here every session**: executor protocol + current task list
- `ANALYSIS.md` — code analysis with file:line surgery map; cited by task specs
- `IMPLEMENTATION.md` — milestone overview and exit criteria
- `PLAN.md` — background/rationale only; its Part 2 language choice is superseded (we use Go, see ANALYSIS.md)
- `NOTES.md` — running log of escalations and decisions (append, never rewrite)

## Layout

- `engine/` — the zztgo fork we modify (Go, `package main`, builds with `go build`)
- `reference/zztgo/` — pristine upstream (gitignored; re-clone from github.com/benhoyt/zztgo)
- `reference/reconstruction-of-zzt/` — the Pascal source zztgo was converted from
  (gitignored; re-clone from github.com/asiekierka/reconstruction-of-zzt)
- `fixtures/` — test worlds and recorded replay hashes

## Hard rules

1. **Never guess ZZT behavior.** The Go was machine-converted from the Pascal with
   identical names. When Go intent is unclear, read the same function in
   `reference/reconstruction-of-zzt/SRC/*.PAS`. Port quirks and bugs faithfully;
   mark them `// ZZT-QUIRK:`.
2. **Determinism is sacred.** No `math/rand` global, no `time.Now()`, no
   `time.Sleep`, no map iteration affecting game state order — anywhere in
   simulation code. All randomness via the engine's seeded RNG.
3. **Replay fixtures are the safety net.** `go test ./...` must pass before every
   commit. Never edit a fixture hash or delete a replay test to make it pass; if a
   behavior change is intentional, the task spec will say so explicitly, and the
   commit message must include `DEVIATION:` with one line of justification.
4. **Stay inside the task.** One task per session unless TASKS.md says otherwise.
   No drive-by refactors, no idiomatic-Go cleanups of converted code, no renames
   beyond what the task lists. Ugly-but-faithful beats clean-but-drifted.
5. **Blocked? Escalate, don't improvise.** If tests won't go green after two honest
   attempts, or the Pascal and Go disagree, or the task spec seems wrong: append
   what you found to `NOTES.md`, leave the working tree clean (stash or revert),
   and stop. Consult the advisor before starting tasks marked `[ADVISOR]` and
   before any escalation stop.
6. **Input globals are also scratch vars.** `InputDeltaX/Y` are reused as dummy
   `var` params in touch-proc calls (`engine/elements.go:866,1083`) — never
   blind-rename them (ANALYSIS.md §3d).
7. Commit after each completed task: `M<milestone>.<task>: <summary>`, then check
   the box in TASKS.md in the same commit.
