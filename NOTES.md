# NOTES — escalations and decisions log (append-only)

- 2026-07-09: Project scaffolded. Baseline `engine/` (vendored from
  benhoyt/zztgo @ master, MIT) builds and passes its tests on go1.26.5,
  macOS arm64. Reference clones are gitignored; re-clone per CLAUDE.md if
  missing.

- 2026-07-09 (M0.4): The task/DoD note "SoundHasTimeElapsed still paces
  interactive mode" is inaccurate for the current code: zztgo stubs
  `SoundHasTimeElapsed` to `return true` (sounds.go), with the real timer logic
  commented out. The ONLY thing pacing interactive `GamePlayLoop` is the
  `time.Sleep(TickTimeDuration*10 ms)` at game.go:1490. So literally deleting
  that sleep would make interactive play run uncapped (DoD "interactive speed
  unchanged" would fail). DECISION: route the per-cycle pace through `Delay()`
  (removing the raw `time.Sleep` + `time` import from game.go) and make `Delay`
  a no-op when `Headless`. Interactive sleeps exactly as before; headless runs
  flat out. Fully moving the pace out to an interactive wrapper (so GameStep
  never paces) lands in M0.5. If/when `SoundHasTimeElapsed` gets real timing,
  revisit whether the Delay-based pace is still needed.

- 2026-07-09 (M0.5, [ADVISOR]): The advisor tool was unavailable this session
  (infra error on every call), so the required pre-edit advisor consult could
  NOT be done. Proceeded with user pre-authorization ("proceed with extra
  care"). Mitigation: cross-checked the extraction against GAME.PAS (1518-1590)
  before writing. Design: GameStep iterates the GLOBAL CurrentStatTicked
  (RemoveStat/DamageStat decrement it — GAME.PAS 942-943, 1233-1234), ticks the
  pending stats for the cycle, then advances CurrentTick (wrap >420->1), resets
  CurrentStatTicked, and calls InputUpdate. The wrapper is "pace (Delay), then
  if SoundHasTimeElapsed: GameStep()". The pause branch stays verbatim in the
  wrapper; the shared "all stats ticked" block folded into GameStep. The
  one-time advance the pause path used to get is reproduced by the next
  GameStep, and CurrentTick is re-randomized on unpause anyway, so the
  transition is unchanged. NOT hash-verified yet — the M0.6 replay fixture
  (next task) is what locks this pure; a manual TOWN.ZZT playtest is the interim
  DoD check. If M0.6's fixture reveals drift here, revisit GameStep.
