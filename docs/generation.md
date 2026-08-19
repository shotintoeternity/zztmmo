# World Generation

World generation runs entirely in the server process; it does not depend on a
Codex session or local visual inspection. Configure the existing Anthropic
provider with `ANTHROPIC_API_KEY`, `ANTHROPIC_MODEL`, and
`ANTHROPIC_MAX_TOKENS`. `ANTHROPIC_API_URL` is optional. Generated `.ZZT`, ZWD,
plan, and prompt sidecars go to `ZZT_GENERATED_DIR`.

## The single-board pipeline

This is the default path:

1. The API creates and mechanically validates a world plan.
2. For each board, the API returns a semantic JSON blueprint: drawing
   operations, actors, passages, and ZZT-OOP.
3. The Go server deterministically rasterizes the blueprint into the exact
   60x25 grid, legend, and stat records required by ZWD.
4. The normal compiler, headless simulation, route checks, progression checks,
   and OOP analysis accept the board or return structured repair feedback to
   the API for that board only.
5. Only a fully accepted world is persisted and hosted.

This keeps renderer-owned details — row lengths, legend bytes, stat bookkeeping,
and passage encoding — out of the model's prompt. The prompt still includes the
ZZT mechanics, OOP rules, corpus-derived composition guidance, and compact
semantic notes from relevant real boards.

## The legacy path

Full ZWD responses remain accepted as a migration fallback. Batch sizes above
one currently use that legacy path; leave `ZZT_GENERATION_BATCH_SIZE` unset or
set it to `1` for blueprints.

## Rendering

PNG rendering is not part of the correctness loop and no screenshots are sent to
the API. The local renderer remains available for optional human evaluation or
future opt-in visual critique after deterministic validation.
