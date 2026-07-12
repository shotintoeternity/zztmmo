package zztgo

import (
	"bytes"
	"fmt"
	"strings"
)

// Error-driven procedural repair layer (M12.16). The compiler self-heals the
// dominant "bucket 1" bookkeeping/syntactic failures before a board is ever
// resent to the LLM: LLM repair rounds are slow, cost tokens, and do not always
// converge. A fixpoint loop parses the source, dispatches a fixer on the error's
// typed code, applies it to the source, and retries — until success, an
// unfixable error (bucket 2 / unknown, which has no fixer and so is handed back
// to the LLM path unchanged), or no progress. See NOTES.md 2026-07-11 for the
// bucket boundary and 2026-07-12 for this implementation's decisions.

// zwdFixer repairs one class of ZWD error. It returns the repaired source, a
// human diagnostic naming what it changed (for auditability), and whether it
// applied anything. Fixers are deterministic and never guess semantic intent.
type zwdFixer func(src string, e *zwdError) (fixed string, diag string, applied bool)

// zwdBucket1Fixers maps a bookkeeping/syntactic error code to its procedural
// fixer. Only bucket-1 codes appear here; bucket-2 (semantic/intent) errors have
// no entry, so the fixpoint loop returns them to the caller for the LLM.
var zwdBucket1Fixers = map[zwdErrCode]zwdFixer{
	zwdErrMissingEnd:         fixZWDMissingEnd,
	zwdErrDuplicateLegendKey: fixZWDDuplicateLegendKey,
	zwdErrUnknownStatField:   fixZWDUnknownStatField,
	zwdErrRowTooWide:         fixZWDRowTooWide,
	zwdErrOffBoardCoord:      fixZWDOffBoardStat,
	zwdErrColorRange:         fixZWDColorRange,
	zwdErrDoorNibble:         fixZWDDoorNibble,
	zwdErrUndefinedGridChar:  fixZWDBoardSection,
	zwdErrOrphanStatGlyph:    fixZWDOrphanGlyphs,
}

// zwdRepairMaxIterations backstops the fixpoint loop. Real repairs converge in a
// handful of passes; the cap only guards against a fixer that reports progress
// forever. The seen-set below is the primary anti-spin guard.
const zwdRepairMaxIterations = 64

// CompileZWDWithRepair compiles ZWD to vanilla .ZZT bytes, running the
// procedural repair fixpoint loop first. diags records every procedural fix. A
// non-nil error is the LLM's job (a bucket-2 or otherwise unfixable failure),
// exactly as a plain CompileZWD error would be.
func CompileZWDWithRepair(src string) (data []byte, diags []string, err error) {
	data, _, diags, err = compileZWDBytesWithRepair(src)
	return data, diags, err
}

func compileZWDBytesWithRepair(src string) (data []byte, repaired string, diags []string, err error) {
	world, repaired, diags, err := compileZWDWorldWithRepair(src)
	if err != nil {
		return nil, repaired, diags, err
	}
	e := NewEngine()
	e.Headless = true
	e.World = world
	var out bytes.Buffer
	if err := e.worldWriteTo(&out); err != nil {
		return nil, repaired, diags, err
	}
	return out.Bytes(), repaired, diags, nil
}

// compileZWDWorldWithRepair is the fixpoint loop. It returns the compiled world,
// the (possibly repaired) source, the ordered repair diagnostics, and any error
// the procedural layer could not fix.
func compileZWDWorldWithRepair(src string) (TWorld, string, []string, error) {
	var diags []string
	seen := map[string]bool{src: true}
	for iter := 0; iter < zwdRepairMaxIterations; iter++ {
		world, err := CompileZWDWorld(src)
		if err == nil {
			return world, src, diags, nil
		}
		ze, ok := err.(*zwdError)
		if !ok {
			return TWorld{}, src, diags, err // untyped → LLM
		}
		fixer := zwdBucket1Fixers[ze.code]
		if fixer == nil {
			return TWorld{}, src, diags, err // bucket 2 / unfixable → LLM
		}
		next, diag, applied := fixer(src, ze)
		if !applied || next == src || seen[next] {
			return TWorld{}, src, diags, err // no progress → LLM
		}
		seen[next] = true
		src = next
		if diag != "" {
			diags = append(diags, diag)
		}
	}
	_, err := CompileZWDWorld(src)
	return TWorld{}, src, diags, err
}

// fixZWDMissingEnd structurally closes an unterminated board/section, reusing
// the M12.14 auto-closer.
func fixZWDMissingEnd(src string, _ *zwdError) (string, string, bool) {
	fixed, warnings := autoCloseZWDSections(src, nil)
	if fixed == src {
		return src, "", false
	}
	return fixed, "auto-closed unterminated section" + diagDetail(warnings), true
}

// fixZWDDuplicateLegendKey keeps the first legend entry for a key and drops the
// rest, reusing the M12.14 deduplicator.
func fixZWDDuplicateLegendKey(src string, _ *zwdError) (string, string, bool) {
	var warnings []string
	fixed := strings.Join(deduplicateZWDLegendEntries(strings.Split(src, "\n"), &warnings), "\n")
	if fixed == src {
		return src, "", false
	}
	return fixed, "dropped duplicate legend key" + diagDetail(warnings), true
}

// fixZWDUnknownStatField drops an unrecognized stat field, reusing the M12.14
// field dropper.
func fixZWDUnknownStatField(src string, _ *zwdError) (string, string, bool) {
	var warnings []string
	fixed := strings.Join(dropUnknownZWDStatFields(strings.Split(src, "\n"), &warnings), "\n")
	if fixed == src {
		return src, "", false
	}
	return fixed, "dropped unknown stat field" + diagDetail(warnings), true
}

// fixZWDRowTooWide truncates an over-wide grid row to the board width, preserving
// the optional two-space grid indent the compiler already tolerates.
func fixZWDRowTooWide(src string, e *zwdError) (string, string, bool) {
	lines := strings.Split(src, "\n")
	if e.line < 1 || e.line > len(lines) {
		return src, "", false
	}
	idx := e.line - 1
	raw := lines[idx]
	indent := leadingZWDIndent(raw)
	content := raw[len(indent):]
	if len(content) <= BOARD_WIDTH {
		return src, "", false
	}
	lines[idx] = indent + content[:BOARD_WIDTH]
	return strings.Join(lines, "\n"), fmt.Sprintf("truncated over-wide grid row %d to %d columns", e.line, BOARD_WIDTH), true
}

// fixZWDOffBoardStat drops a stat whose coordinate is outside the board. If a
// matching grid glyph exists on-board, the orphan fixer re-synthesizes it at the
// glyph's real coordinate; an off-board stat with no glyph is simply removed.
func fixZWDOffBoardStat(src string, e *zwdError) (string, string, bool) {
	lines := strings.Split(src, "\n")
	if e.line < 1 || e.line > len(lines) {
		return src, "", false
	}
	idx := e.line - 1
	if !strings.HasPrefix(strings.TrimSpace(lines[idx]), "stat") {
		return src, "", false
	}
	end := idx + 1
	// Absorb an attached oop block (oop ... end) so no dangling body remains.
	for end < len(lines) && strings.TrimSpace(lines[end]) == "" {
		end++
	}
	if end < len(lines) && strings.TrimSpace(lines[end]) == "oop" {
		end++
		for end < len(lines) {
			if strings.TrimSpace(lines[end]) == "end" {
				end++
				break
			}
			end++
		}
	}
	fixed := append([]string{}, lines[:idx]...)
	fixed = append(fixed, lines[end:]...)
	return strings.Join(fixed, "\n"), fmt.Sprintf("dropped off-board stat at source line %d", e.line), true
}

// fixZWDColorRange rewrites an out-of-range color token to a safe white default.
func fixZWDColorRange(src string, e *zwdError) (string, string, bool) {
	return rewriteColorToken(src, e, func(byte) byte { return 0x0F }, "replaced out-of-range color")
}

// fixZWDDoorNibble gives a Door whose color has no valid key background nibble
// (0 or 8) a valid one, preserving the foreground nibble. Which specific key is
// intent, but a door needs *some* valid key to compile and to be touchable
// without the M12.12 crash; blue (nibble 1) is the deterministic default.
func fixZWDDoorNibble(src string, e *zwdError) (string, string, bool) {
	return rewriteColorToken(src, e, func(old byte) byte { return 0x10 | (old & 0x0F) }, "gave door a valid key color")
}

// rewriteColorToken replaces the value after "color" on the error's source line
// using remap, preserving indentation.
func rewriteColorToken(src string, e *zwdError, remap func(byte) byte, what string) (string, string, bool) {
	lines := strings.Split(src, "\n")
	if e.line < 1 || e.line > len(lines) {
		return src, "", false
	}
	idx := e.line - 1
	indent := leadingZWDIndent(lines[idx])
	toks := strings.Fields(lines[idx])
	changed := false
	for i, t := range toks {
		if t == "color" && i+1 < len(toks) {
			old, _ := parseColor(toks[i+1]) // 0 when unparseable; remap still yields a valid byte
			toks[i+1] = fmt.Sprintf("0x%02X", remap(old))
			changed = true
		}
	}
	if !changed {
		return src, "", false
	}
	lines[idx] = indent + strings.Join(toks, " ")
	return strings.Join(lines, "\n"), fmt.Sprintf("%s at source line %d", what, e.line), true
}

// fixZWDBoardSection reprocesses the board containing the error through the
// pass-1 preprocessor (M12.11 undefined-char injection, M12.13 stat synthesis),
// then splices it back. Used for undefined grid keys.
func fixZWDBoardSection(src string, e *zwdError) (string, string, bool) {
	fixed, diag, applied := reprocessEnclosingBoard(src, e.line)
	if !applied {
		return src, "", false
	}
	return fixed, "injected legend entries for undefined grid keys" + diagDetail(strings.Split(diag, "; ")), true
}

// fixZWDOrphanGlyphs synthesizes a stat for every stat-backed glyph with no
// declaration (aggregate — one pass repairs them all, M12.16 folded bullet 2),
// then derives passage targets from the legend's `to` clause (folded bullet 1).
func fixZWDOrphanGlyphs(src string, e *zwdError) (string, string, bool) {
	fixed, _, applied := reprocessEnclosingBoard(src, e.line)
	if !applied {
		return src, "", false
	}
	fixed = refineSynthesizedPassageTargets(fixed)
	return fixed, "synthesized stats for orphan grid glyphs", true
}

// reprocessEnclosingBoard runs preprocessZWDGridWithWarnings on the single board
// section that contains errLine (found by the same board-header split the
// generator uses) and splices the repaired section back into the full source.
func reprocessEnclosingBoard(src string, errLine int) (string, string, bool) {
	lines := strings.Split(src, "\n")
	start, end, ok := zwdBoardSectionByLine(lines, errLine)
	if !ok {
		return src, "", false
	}
	section := strings.TrimSpace(strings.Join(lines[start:end], "\n"))
	fixed, warnings := preprocessZWDGridWithWarnings(section)
	if strings.TrimSpace(fixed) == section {
		return src, "", false
	}
	out := append([]string{}, lines[:start]...)
	out = append(out, strings.Split(fixed, "\n")...)
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n"), strings.Join(warnings, "; "), true
}

// zwdBoardSectionByLine returns the [start, end) line indices of the board
// section containing errLine, where a section runs from its `board "..."` header
// to just before the next header (or end of source) — identical to the split the
// generator applies before preprocessing.
func zwdBoardSectionByLine(lines []string, errLine int) (start, end int, ok bool) {
	idx := errLine - 1
	if idx < 0 || idx >= len(lines) {
		return 0, 0, false
	}
	start = -1
	for i := idx; i >= 0; i-- {
		if boardHeaderRe.MatchString(lines[i]) {
			start = i
			break
		}
	}
	if start < 0 {
		return 0, 0, false
	}
	end = len(lines)
	for i := start + 1; i < len(lines); i++ {
		if boardHeaderRe.MatchString(lines[i]) {
			end = i
			break
		}
	}
	return start, end, true
}

// refineSynthesizedPassageTargets rewrites synthesized passage stats that carry
// no board target (`p3 0`) to point at the destination named in the glyph's
// legend `to` clause — both coordinate and target derived, never guessed. A
// passage whose legend names no destination keeps board 0, and if the legend
// names a board that does not exist the compiler rejects it (bucket 2 → LLM).
func refineSynthesizedPassageTargets(src string) string {
	doc, err := newZWDParser(src).parse()
	if err != nil {
		return src
	}
	lines := strings.Split(src, "\n")
	changed := false
	for _, b := range doc.boards {
		for _, st := range b.stats {
			if st.element != E_PASSAGE || st.p3Board != "" {
				continue
			}
			gx, gy := int(st.x), int(st.y)
			if gy < 1 || gy > len(b.grid) || gx < 1 || gx > BOARD_WIDTH {
				continue
			}
			ch := b.grid[gy-1].text[gx-1]
			le, ok := b.legend[ch]
			if !ok || le.element != E_PASSAGE || le.toBoard == "" {
				continue
			}
			idx := st.line - 1
			if idx < 0 || idx >= len(lines) {
				continue
			}
			target := fmt.Sprintf(" p3 board %q", le.toBoard)
			if strings.Contains(lines[idx], " p3 0") {
				lines[idx] = strings.Replace(lines[idx], " p3 0", target, 1)
			} else {
				lines[idx] += target
			}
			changed = true
		}
	}
	if !changed {
		return src
	}
	return strings.Join(lines, "\n")
}

// diagDetail formats fixer sub-warnings for a diagnostic line.
func diagDetail(warnings []string) string {
	var kept []string
	for _, w := range warnings {
		if strings.TrimSpace(w) != "" {
			kept = append(kept, w)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	return ": " + strings.Join(kept, "; ")
}

// CheckZWDPassageReciprocity detects (never fixes) passages whose destination
// board has no matching-color return passage (M12.16 folded bullet 3, bucket 2).
// Vanilla BoardPassageTeleport lands the traveller on the first color-matched
// passage in the destination, else the start square; a one-way color leaves the
// player somewhere the author likely did not intend. The warnings are routed to
// the LLM / plan repair — the color is never changed procedurally.
func CheckZWDPassageReciprocity(world TWorld) []string {
	var warnings []string
	for bid := int16(0); bid <= world.BoardCount; bid++ {
		board := zwdDecodeBoardForCheck(&world, bid)
		if board == nil {
			continue
		}
		for i := int16(0); i <= board.StatCount; i++ {
			st := board.Stats[i]
			if board.Tiles[st.X][st.Y].Element != E_PASSAGE {
				continue
			}
			dest := int16(st.P3)
			if dest < 0 || dest > world.BoardCount || dest == bid {
				continue
			}
			color := board.Tiles[st.X][st.Y].Color
			if !boardHasPassageOfColor(&world, dest, color) {
				warnings = append(warnings, fmt.Sprintf(
					"passage at (%d,%d) on board %q (color 0x%02X) has no matching-color return passage on destination board %q",
					st.X, st.Y, board.Name, color, zwdBoardName(&world, dest)))
			}
		}
	}
	return warnings
}

func boardHasPassageOfColor(world *TWorld, boardID int16, color byte) bool {
	board := zwdDecodeBoardForCheck(world, boardID)
	if board == nil {
		return false
	}
	for i := int16(0); i <= board.StatCount; i++ {
		st := board.Stats[i]
		if board.Tiles[st.X][st.Y].Element == E_PASSAGE && board.Tiles[st.X][st.Y].Color == color {
			return true
		}
	}
	return false
}

// zwdDecodeBoardForCheck opens a board out of the compiled world so its tiles and
// stats can be inspected without disturbing the caller's engine.
func zwdDecodeBoardForCheck(world *TWorld, boardID int16) *TBoard {
	if boardID < 0 || boardID > world.BoardCount {
		return nil
	}
	e := NewEngine()
	e.Headless = true
	e.World = *world
	e.BoardOpen(boardID)
	b := e.Board
	return &b
}

func zwdBoardName(world *TWorld, boardID int16) string {
	board := zwdDecodeBoardForCheck(world, boardID)
	if board == nil {
		return fmt.Sprintf("#%d", boardID)
	}
	return board.Name
}
