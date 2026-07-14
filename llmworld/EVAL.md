# EVAL — generation prompting-quality evaluation (M12.17)

The generation prompts (planner, board painter, `ZWD.md` spec, `STYLE.md`
idiom, retrieval few-shots, title-screen brief, opt-in web grounding) are
judged with a two-tier harness, so prompt edits can be measured instead of
eyeballed.

**Tier 1 — offline structural gate.** `EvalGeneratedZWD` (engine/eval.go)
runs deterministic checks over generated ZWD text; `TestEvalGateFixtures`
applies it in CI to recorded generation outputs committed under
`fixtures/gen/*.zwd` (ZWD text only — never `.ZZT` binaries). A sibling
`NAME.title.txt` holds the display name the title wordmark must spell when it
differs from the sanitized `world` directive. The checks:

- `compiles` — the ZWD compiles; the compiler enforces the ZWD.md Limits table.
- `headless-validates` — 200 GameSteps with no panic and no exit request.
- `title-wordmark` — board 0 has exactly one horizontal text band spelling the
  world name (ZZT text elements carry the glyph in the tile's Color byte, so
  this is a string comparison), at most one subtitle text row strictly below
  it, and no stray text rows.
- `title-no-creatures-or-items` — board 0 has no creatures, projectiles, or
  collectible items (Bear, Ruffian, Lion, Tiger, Shark, SpinningGun, Slime,
  Centipedes, Bullet, Star, Gem, Ammo, Torch, Energizer, Key, Bomb). Objects,
  scrolls, and passages remain legal — vanilla titles use objects for motion.
- `title-one-player-start` — exactly one player tile on board 0.
- `reachable-endgame` — walking edge exits and passage targets from board 1
  (where the server lands a joiner on a generated world), some reachable
  stat's OOP contains `#endgame`.
- `no-orphan-stat-tiles` — on every board, every stat-backed tile has a stat
  at its coordinate (the LEMWILLK draw-proc crash class).

**Tier 2 — live quality pass.** `go run ./cmd/zzt-eval` (owner-run; spends
API; never in CI) generates a world per premise below, runs the tier-1 gate,
renders board 0 plus the first two gameplay boards to PNG with the engine's
CP437 renderer, scores the world against the rubric with an LLM judge, and
writes a scored Markdown report embedding the screenshots. Configuration
comes from the same env vars as generation (`ANTHROPIC_API_KEY`,
`ANTHROPIC_MODEL`, `ANTHROPIC_MAX_TOKENS`); `ZZT_EVAL_JUDGE_MODEL` overrides
the judge's model. The baseline report for comparing future prompt edits is
recorded in NOTES.md (M12.17 entry).

## Premise set

Each premise runs twice — once with the web-grounding opt-in, once without —
six generations per full run. The set spans the three registers the prompts
must handle:

1. **Real-world grounded topic** — `the 1969 Apollo 11 moon landing, from
   launch to splashdown` (grounding accuracy is checkable against history).
2. **Abstract theme** — `a dream about slowly forgetting someone you loved`
   (no facts to lean on; composition and voice carry it).
3. **Genre pastiche** — `a classic haunted castle adventure with locked
   doors, a dark dungeon, and a vampire lord` (the ZZT house style; tests
   idiom fluency and progression).

## Rubric

Each dimension is scored 0–5 by the judge from the screenshots, the world
plan, and an OOP sample. Score anchors: 0 = absent/broken, 1 = poor, 2 =
below the corpus norm, 3 = solid ZZT-corpus quality, 4 = good, 5 = excellent
(would be featured in a Museum of ZZT showcase).

- **title-legibility** — Is the title screen a clean, legible wordmark of the
  world's name? One monumental band, consistent letter heights and spacing, a
  small coherent palette, generous empty space; no stray or half-formed
  letters, no scattered creatures or furniture. (5: reads instantly at a
  glance and looks deliberate. 2: name readable but cluttered or uneven.
  0: name misspelled, duplicated, or unreadable.)
- **visual-composition** — Are the gameplay boards composed scenes in the
  corpus idiom: framed structures, gray-family shading or coherent palette
  families, walkable floors, a focal point — rather than noise, emptiness, or
  a swatch sheet? (5: every shown board reads as a place. 2: recognizable
  rooms but flat or repetitive. 0: random tiles or blank rooms.)
- **oop-voice** — Does the object code have voice and purpose: named
  characters, varied lines, dialogue or flavor that fits the premise,
  interactions that do something? (5: memorable writing plus working
  mechanics. 2: functional but generic boilerplate. 0: empty or broken
  scripts.)
- **grounding-accuracy** — Scored only for grounded runs; ungrounded runs
  record `n/a`. Are the real-world names, facts, sequence of events, and tone
  accurate to the premise's subject? Penalize invented facts presented as
  real. (5: specifics are correct and well-chosen. 2: right subject, several
  errors. 0: fabricated throughout.)

The judge must answer with only a JSON object:

```json
{
  "scores": [
    {"dimension": "title-legibility", "score": 0, "justification": "..."},
    {"dimension": "visual-composition", "score": 0, "justification": "..."},
    {"dimension": "oop-voice", "score": 0, "justification": "..."},
    {"dimension": "grounding-accuracy", "score": 0, "justification": "..."}
  ],
  "overall": "one-paragraph summary"
}
```

For ungrounded runs the judge sets the grounding-accuracy score to -1
(rendered as `n/a` in the report).
