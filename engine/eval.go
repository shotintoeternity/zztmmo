// M12.17 tier-1 structural gate: objective, deterministic checks over
// generated ZWD text — the properties the generation prompting is supposed to
// guarantee. Used two ways: the CI fixture test (eval_test.go) runs it over
// recorded generation outputs, and cmd/zzt-eval runs it as the objective half
// of the live quality pass. No LLM call, no network, no simulation change.
//
// The checks measure the COMPILED world (post-preprocess, post-compile), not
// the raw ZWD text, so they see exactly what a player gets. Title-screen
// checks are mechanical because ZZT text elements (E_TEXT_BLUE..E_TEXT_WHITE)
// carry the glyph in the tile's Color byte: "spells the world name" is a
// string comparison, not OCR.

package zztgo

import (
	"fmt"
	"sort"
	"strings"
)

// EvalCheck is one named tier-1 check outcome.
type EvalCheck struct {
	Name   string
	Passed bool
	Detail string
}

// EvalReport is the tier-1 structural gate result for one generated world.
type EvalReport struct {
	WorldName string
	Checks    []EvalCheck
}

// Passed reports whether every check passed.
func (r EvalReport) Passed() bool {
	for _, c := range r.Checks {
		if !c.Passed {
			return false
		}
	}
	return true
}

// Failures returns only the failed checks.
func (r EvalReport) Failures() []EvalCheck {
	var out []EvalCheck
	for _, c := range r.Checks {
		if !c.Passed {
			out = append(out, c)
		}
	}
	return out
}

func (r EvalReport) String() string {
	var b strings.Builder
	for _, c := range r.Checks {
		mark := "PASS"
		if !c.Passed {
			mark = "FAIL"
		}
		fmt.Fprintf(&b, "%s %s", mark, c.Name)
		if c.Detail != "" {
			fmt.Fprintf(&b, ": %s", c.Detail)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// evalTitleBanned are the elements the title-screen brief forbids on board 0:
// creatures, projectiles, and collectible items. Objects, scrolls, and
// passages stay legal — vanilla titles use objects for decoration and motion.
var evalTitleBanned = map[byte]string{
	E_BEAR: "Bear", E_RUFFIAN: "Ruffian", E_LION: "Lion", E_TIGER: "Tiger",
	E_SHARK: "Shark", E_SPINNING_GUN: "SpinningGun", E_SLIME: "Slime",
	E_CENTIPEDE_HEAD: "CentipedeHead", E_CENTIPEDE_SEGMENT: "CentipedeSegment",
	E_BULLET: "Bullet", E_STAR: "Star",
	E_GEM: "Gem", E_AMMO: "Ammo", E_TORCH: "Torch", E_ENERGIZER: "Energizer",
	E_KEY: "Key", E_BOMB: "Bomb",
}

// EvalGeneratedZWD runs the tier-1 structural gate over generated ZWD text.
// displayName is the name the title wordmark must spell (the plan's world
// name, which may differ from the sanitized `world` directive); "" falls back
// to the compiled world's name.
func EvalGeneratedZWD(src, displayName string) EvalReport {
	var r EvalReport

	data, err := CompileZWD(src)
	if err != nil {
		r.Checks = append(r.Checks, EvalCheck{Name: "compiles", Detail: err.Error()})
		return r
	}
	r.Checks = append(r.Checks, EvalCheck{Name: "compiles", Passed: true,
		Detail: "compiler enforces the ZWD.md Limits table"})

	if err := validateGeneratedZWD(data); err != nil {
		r.Checks = append(r.Checks, EvalCheck{Name: "headless-validates", Detail: err.Error()})
		return r
	}
	r.Checks = append(r.Checks, EvalCheck{Name: "headless-validates", Passed: true,
		Detail: "200 GameSteps, no panic, no exit request"})

	e := NewEngine()
	e.Headless = true
	e.VideoInstall()
	if err := e.worldReadFrom(strings.NewReader(string(data)), false, nil); err != nil {
		r.Checks = append(r.Checks, EvalCheck{Name: "reloads", Detail: err.Error()})
		return r
	}
	r.WorldName = e.World.Info.Name
	if displayName == "" {
		displayName = e.World.Info.Name
	}

	e.BoardOpen(0)
	r.Checks = append(r.Checks, evalTitleWordmark(e, displayName))
	r.Checks = append(r.Checks, evalTitleNoCreaturesOrItems(e))
	r.Checks = append(r.Checks, evalTitleOnePlayerStart(e))
	r.Checks = append(r.Checks, evalReachableEndgame(e))
	r.Checks = append(r.Checks, evalNoOrphanStatTiles(e))
	return r
}

// evalTextRow renders the text-element content of one board row as a string:
// text glyphs verbatim, everything else as a space, then whitespace collapsed.
// Returns "" when the row holds no text elements.
func evalTextRow(e *Engine, y int16) string {
	var raw []byte
	sawText := false
	for x := int16(1); x <= BOARD_WIDTH; x++ {
		tile := e.Board.Tiles[x][y]
		if tile.Element >= E_TEXT_MIN && tile.Element <= E_TEXT_WHITE {
			raw = append(raw, tile.Color)
			sawText = true
		} else {
			raw = append(raw, ' ')
		}
	}
	if !sawText {
		return ""
	}
	return strings.Join(strings.Fields(string(raw)), " ")
}

func evalNormalizeName(s string) string {
	return strings.ToUpper(strings.Join(strings.Fields(s), " "))
}

// foldWordmark reduces a display name to the printable CP437 bytes a title
// wordmark can actually store one glyph per cell: ASCII passes through, common
// typographic punctuation folds to its ASCII equivalent, and any other rune is
// dropped. Both the deterministic title stamp (stampTitleWordmark) and the
// title-wordmark check fold the name the same way, so a stamped row of bytes and
// the expected name are compared in one byte space instead of one being UTF-8
// and the other CP437. It is the identity on pure-ASCII names, so existing
// fixtures and unit tests are unaffected.
func foldWordmark(s string) string {
	var b []byte
	for _, r := range s {
		switch {
		case r == '—' || r == '–' || r == '―' || r == '−':
			b = append(b, '-') // em/en/horizontal-bar dash, minus sign
		case r == '‘' || r == '’' || r == '‚':
			b = append(b, '\'') // curly single quotes
		case r == '“' || r == '”' || r == '„':
			b = append(b, '"') // curly double quotes
		case r == '…':
			b = append(b, '.', '.', '.') // ellipsis
		case r == '×':
			b = append(b, 'x') // multiplication sign
		case r >= 0x20 && r < 0x7F:
			b = append(b, byte(r))
		default:
			// Unrepresentable rune: drop it rather than emit a mystery byte.
		}
	}
	return string(b)
}

// evalTitleWordmark checks board 0 for exactly one horizontal text band
// spelling the world name, with at most one subtitle text row beneath it and
// no stray text anywhere else (the "GAFFHA + stray G" failure class).
func evalTitleWordmark(e *Engine, displayName string) EvalCheck {
	check := EvalCheck{Name: "title-wordmark"}
	want := evalNormalizeName(foldWordmark(displayName))
	var wordmarkRows, otherRows []int16
	var samples []string
	for y := int16(1); y <= BOARD_HEIGHT; y++ {
		row := evalTextRow(e, y)
		if row == "" {
			continue
		}
		if evalNormalizeName(row) == want {
			wordmarkRows = append(wordmarkRows, y)
		} else {
			otherRows = append(otherRows, y)
			if len(samples) < 3 {
				samples = append(samples, fmt.Sprintf("row %d: %q", y, row))
			}
		}
	}
	switch {
	case len(wordmarkRows) == 0:
		check.Detail = fmt.Sprintf("no text row spells %q", displayName)
		if len(samples) > 0 {
			check.Detail += " (text rows found: " + strings.Join(samples, "; ") + ")"
		}
	case len(wordmarkRows) > 1:
		check.Detail = fmt.Sprintf("world name appears on %d rows %v — duplicate wordmarks", len(wordmarkRows), wordmarkRows)
	case len(otherRows) > 1:
		check.Detail = fmt.Sprintf("stray text beyond the wordmark and one subtitle line: %s", strings.Join(samples, "; "))
	case len(otherRows) == 1 && otherRows[0] < wordmarkRows[0]:
		check.Detail = fmt.Sprintf("subtitle text at row %d sits above the wordmark (row %d)", otherRows[0], wordmarkRows[0])
	default:
		check.Passed = true
		check.Detail = fmt.Sprintf("wordmark %q on row %d", displayName, wordmarkRows[0])
	}
	return check
}

// evalTitleNoCreaturesOrItems checks board 0 carries none of the elements the
// title brief forbids, as tiles or as stats.
func evalTitleNoCreaturesOrItems(e *Engine) EvalCheck {
	check := EvalCheck{Name: "title-no-creatures-or-items"}
	var found []string
	for y := int16(1); y <= BOARD_HEIGHT && len(found) < 5; y++ {
		for x := int16(1); x <= BOARD_WIDTH && len(found) < 5; x++ {
			if name, banned := evalTitleBanned[e.Board.Tiles[x][y].Element]; banned {
				found = append(found, fmt.Sprintf("%s at (%d,%d)", name, x, y))
			}
		}
	}
	for i := int16(0); i <= e.Board.StatCount && len(found) < 5; i++ {
		stat := e.Board.Stats[i]
		if name, banned := evalTitleBanned[e.Board.Tiles[stat.X][stat.Y].Element]; banned {
			found = append(found, fmt.Sprintf("%s stat at (%d,%d)", name, stat.X, stat.Y))
		}
	}
	if len(found) > 0 {
		check.Detail = strings.Join(found, "; ")
		return check
	}
	check.Passed = true
	return check
}

// evalTitleOnePlayerStart checks board 0 holds exactly one player.
func evalTitleOnePlayerStart(e *Engine) EvalCheck {
	check := EvalCheck{Name: "title-one-player-start"}
	count := 0
	for y := int16(1); y <= BOARD_HEIGHT; y++ {
		for x := int16(1); x <= BOARD_WIDTH; x++ {
			if e.Board.Tiles[x][y].Element == E_PLAYER {
				count++
			}
		}
	}
	if count != 1 {
		check.Detail = fmt.Sprintf("board 0 has %d player tiles, want exactly 1", count)
		return check
	}
	check.Passed = true
	return check
}

// evalReachableEndgame walks the exit/passage graph from board 1 (where the
// server lands a joiner on a generated world: CurrentBoard is 0, so the
// WebSocket join path falls through to board 1) and requires a reachable
// stat's OOP to contain #endgame.
func evalReachableEndgame(e *Engine) EvalCheck {
	check := EvalCheck{Name: "reachable-endgame"}
	if e.World.BoardCount < 1 {
		check.Detail = "world has no gameplay boards"
		return check
	}
	passed, detail := reachableEndgame(e)
	check.Passed = passed
	check.Detail = detail
	return check
}

// reachableEndgame is the shared compiled-world walk used by the evaluation
// gate and generation's cross-board validation. It follows exactly the routes
// a joiner can take from board 1: edge exits plus Passage stat P3 targets.
func reachableEndgame(e *Engine) (bool, string) {
	visited := map[int16]bool{}
	queue := []int16{1}
	var endgameBoards []string
	for len(queue) > 0 {
		b := queue[0]
		queue = queue[1:]
		if b < 1 || b > e.World.BoardCount || visited[b] {
			continue
		}
		visited[b] = true
		e.BoardOpen(b)
		for _, n := range e.Board.Info.NeighborBoards {
			if n != 0 {
				queue = append(queue, int16(n))
			}
		}
		hasEndgame := false
		for i := int16(0); i <= e.Board.StatCount; i++ {
			stat := e.Board.Stats[i]
			if e.Board.Tiles[stat.X][stat.Y].Element == E_PASSAGE && stat.P3 != 0 {
				queue = append(queue, int16(stat.P3))
			}
			if strings.Contains(strings.ToUpper(stat.Data), "#ENDGAME") {
				hasEndgame = true
			}
		}
		if hasEndgame {
			endgameBoards = append(endgameBoards, e.Board.Name)
		}
	}
	if len(endgameBoards) == 0 {
		var reached []int
		for b := range visited {
			reached = append(reached, int(b))
		}
		sort.Ints(reached)
		return false, fmt.Sprintf("no #endgame reachable from board 1 (reached boards %v of %d)", reached, e.World.BoardCount)
	}
	return true, "#endgame on " + strings.Join(endgameBoards, ", ")
}

// EvalOOPSample gathers the world's stat OOP for the tier-2 judge, labeled by
// board, truncated to maxBytes. Empty and bind-only stats are skipped.
func EvalOOPSample(src string, maxBytes int) (string, error) {
	data, err := CompileZWD(src)
	if err != nil {
		return "", err
	}
	e := NewEngine()
	e.Headless = true
	e.VideoInstall()
	if err := e.worldReadFrom(strings.NewReader(string(data)), false, nil); err != nil {
		return "", err
	}
	var b strings.Builder
	for board := int16(0); board <= e.World.BoardCount && b.Len() < maxBytes; board++ {
		e.BoardOpen(board)
		for i := int16(0); i <= e.Board.StatCount && b.Len() < maxBytes; i++ {
			oop := strings.TrimSpace(strings.ReplaceAll(e.Board.Stats[i].Data, "\r", "\n"))
			if oop == "" {
				continue
			}
			fmt.Fprintf(&b, "-- board %q stat at (%d,%d) --\n%s\n\n", e.Board.Name, e.Board.Stats[i].X, e.Board.Stats[i].Y, oop)
		}
	}
	sample := b.String()
	if len(sample) > maxBytes {
		sample = sample[:maxBytes] + "\n[truncated]"
	}
	return sample, nil
}

// evalNoOrphanStatTiles checks every stat-backed tile on every board has a
// stat at its coordinate — the class of defect that stops an element ticking
// or crashes draw procs (NOTES.md 2026-07-13, LEMWILLK). The ZWD compiler
// rejects orphans at the source level; this guards the assembled binary.
func evalNoOrphanStatTiles(e *Engine) EvalCheck {
	check := EvalCheck{Name: "no-orphan-stat-tiles"}
	var found []string
	for b := int16(0); b <= e.World.BoardCount && len(found) < 5; b++ {
		e.BoardOpen(b)
		for y := int16(1); y <= BOARD_HEIGHT && len(found) < 5; y++ {
			for x := int16(1); x <= BOARD_WIDTH && len(found) < 5; x++ {
				el := e.Board.Tiles[x][y].Element
				if !elementNeedsStat(el) {
					continue
				}
				if e.GetStatIdAt(x, y) == -1 {
					found = append(found, fmt.Sprintf("board %d %q: %s at (%d,%d) has no stat", b, e.Board.Name, ElementDefs[el].Name, x, y))
				}
			}
		}
	}
	if len(found) > 0 {
		check.Detail = strings.Join(found, "; ")
		return check
	}
	check.Passed = true
	return check
}
