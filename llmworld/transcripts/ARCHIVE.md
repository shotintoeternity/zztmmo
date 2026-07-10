# M12.3 manual generation run — ARCHIVE

The M12.3 DoD's "documented manual run against a real LLM produced at least one
compiling world." This is that record.

## Setup

- **Model (the real LLM):** Claude Opus 4.8 (`claude-opus-4-8`) — the assistant
  running this coding session. The automated server that calls the Anthropic
  API programmatically is M12.4; here the same model was driven by hand against
  the assembled prompt, which is what M12.3 asks for.
- **System prompt:** `system_prompt.txt` in this directory — the exact,
  byte-for-byte output of `PromptKit.SystemPrompt()` (`engine/promptkit.go`),
  72,781 bytes: role preamble + the full ZWD spec (`ZWD.md`, including the
  limits table verbatim) + `STYLE.md` + four archetype few-shots
  (CUTLASS_board27 action arena, SEWERS_board17 texture showcase,
  DUNGEONS_board20 interior scene, RAEKUUL_board1 story board) + the output
  contract.
- **User message (the premise):**

  > Paint a single-board ZZT world. Premise: a flooded library — the sea took
  > the east wing, and the archive's treasured Codex is locked behind a violet
  > door whose key a lion now guards. Interior scene: framed stone hall,
  > fake-wall marble floor, bookshelves, one monumental title in lettering,
  > water shading for the flood, both OOP rituals (a wry Archivist that shushes
  > and a pickup on the Codex). Emit a complete world.

## Output

The model produced the ZWD in `../generated/ARCHIVE.zwd` ("The Drowned
Archive"). It applies the house-style idioms the prompt teaches: a Solid-frame
playfield, gray-family stone (`0x07`) with Fake-wall marble floor (`0x70`),
Breakable bookshelf blocks (`0x78`), a Water `0x1F` flood shading the east wing,
"ARCHIVE" spelled once in Text-Yellow lettering (each cell's color byte is its
letter's ASCII, as STYLE.md §3 describes), and the two OOP rituals from
STYLE.md §5 — the Archivist's `:touch`/`#zap touch` escalating dialogue and the
Codex's `#lock` → `#play` → `#give score` → `#die` pickup.

The grid was laid out with a small deterministic builder
(`transcripts/build_archive.py`) purely to guarantee every row is exactly 60
columns; the board design, legend, and OOP are the model's authored content.

## Result — compiles and validates, first attempt

```
$ cd engine && go test -run TestGeneratedZWDWorldsCompileAndValidate -v .
    gen_generated_test.go:39: ../llmworld/generated/ARCHIVE.zwd → ARCHIVE.ZZT (1880 bytes)
--- PASS: TestGeneratedZWDWorldsCompileAndValidate/ARCHIVE
```

`gen_generated_test.go` runs the M7.5 gate: `CompileZWD` → serialize → headless
`WorldLoad` → 200 `GameStep`s with no panic and a non-empty board render. No
repair rounds were needed — the second world (after MOSSGATE) to compile and
validate first try from the corpus idioms, now via the assembled kit.

## Reproduce

```
cd engine
go test -run 'TestLoadPromptKit|TestPromptKit'          # kit loads + assembles
go test -run TestGeneratedZWDWorldsCompileAndValidate    # ARCHIVE + MOSSGATE compile+validate
```

## Scope note

ARCHIVE is a *style/compile* proof, like MOSSGATE. It is composed and in-voice
but not solvability-tuned: the flooded east wing walls off the key and Codex
behind Water (a blocking tile), so a player cannot currently reach them on foot.
Full playability tuning (a causeway, or draining via a flag) is the kind of
coherence the M12.3a plan validator and the M12.4 per-board orchestration are
built to enforce; it is out of scope for "produced at least one compiling
world."
