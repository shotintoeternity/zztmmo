package zztgo

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var rleRe = regexp.MustCompile(`(.)\*([0-9]+)`)

func expandRLE(line string) string {
	for {
		loc := rleRe.FindStringSubmatchIndex(line)
		if loc == nil {
			break
		}
		char := line[loc[2]:loc[3]]
		countStr := line[loc[4]:loc[5]]
		count, _ := strconv.Atoi(countStr)
		expanded := strings.Repeat(char, count)
		line = line[:loc[0]] + expanded + line[loc[1]:]
	}
	return line
}

func getUnusedLegendKey(legendMap map[byte]string) byte {
	candidates := []byte("os*?xzyptkdgcaOSXZYPTKDGCA")
	for _, ch := range candidates {
		if _, exists := legendMap[ch]; !exists {
			return ch
		}
	}
	for ch := byte('A'); ch <= 'Z'; ch++ {
		if _, exists := legendMap[ch]; !exists {
			return ch
		}
	}
	for ch := byte('a'); ch <= 'z'; ch++ {
		if _, exists := legendMap[ch]; !exists {
			return ch
		}
	}
	return '?'
}

// sortedLegendKeys is legendMap's keys in byte order. Ranging over the map
// directly made a legend with two keys for one element repair differently from
// run to run.
func sortedLegendKeys(legendMap map[byte]string) []byte {
	keys := make([]byte, 0, len(legendMap))
	for ch := range legendMap {
		keys = append(keys, ch)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func parseLegendElemName(valStr string) string {
	valStr = strings.TrimSpace(valStr)
	if strings.HasPrefix(strings.ToLower(valStr), "element ") {
		words := strings.Fields(valStr)
		if len(words) >= 2 {
			return words[0] + " " + words[1]
		}
	}
	words := strings.Fields(valStr)
	if len(words) > 0 {
		return words[0]
	}
	return ""
}

func preprocessZWDGrid(zwdText string) string {
	preprocessed, _ := preprocessZWDGridWithWarnings(zwdText)
	return preprocessed
}

// preprocessZWDGridWithWarnings repairs mechanical omissions in model output
// before the strict compiler sees it. Its warnings are presentation context:
// the repaired ZWD remains ordinary source and the compiler still guards every
// semantic value that cannot be safely derived.
func preprocessZWDGridWithWarnings(zwdText string) (string, []string) {
	var warnings []string
	zwdText, warnings = autoCloseZWDSections(zwdText, warnings)
	// This preprocessor needs the same element metadata as the ZWD compiler to
	// recognize a legend's stat-backed elements and to derive their default
	// cycles.  Generation can call us before any world has been compiled.
	ensureElementDefs()

	lines := strings.Split(zwdText, "\n")
	lines = deduplicateZWDLegendEntries(lines, &warnings)
	lines = dropUnknownZWDStatFields(lines, &warnings)

	playerChar := byte('@')
	emptyChar := byte('.')
	hasPlayerLegend := false

	startPlayerRe := regexp.MustCompile(`(?i)^\s*start\s+player\s+at\s+([0-9]+)\s*,\s*([0-9]+)`)

	var originalStartX, originalStartY int
	hasOriginalStart := false

	legendMap := make(map[byte]string)
	type zwdStatSpec struct {
		lineIdx int
		startX  int
		startY  int
		elem    string
		rest    string
	}
	var statSpecs []zwdStatSpec

	statRe := regexp.MustCompile(`(?i)^(\s*stat\s+at\s+)([0-9]+)\s*,\s*([0-9]+)(\s+element\s+)(\S+)(.*)`)

	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "=") {
			parts := strings.SplitN(trimmed, "=", 2)
			if len(parts) == 2 {
				k := strings.TrimSpace(parts[0])
				if len(k) == 1 {
					ch := k[0]
					elemName := parseLegendElemName(parts[1])
					if elemName != "" {
						legendMap[ch] = elemName
					}
				}
			}
		}
		if m := startPlayerRe.FindStringSubmatch(trimmed); m != nil {
			x, _ := strconv.Atoi(m[1])
			y, _ := strconv.Atoi(m[2])
			if x >= 1 && x <= 60 && y >= 1 && y <= 25 {
				originalStartX = x
				originalStartY = y
				hasOriginalStart = true
			}
		}
		if m := statRe.FindStringSubmatch(line); m != nil {
			x, _ := strconv.Atoi(m[2])
			y, _ := strconv.Atoi(m[3])
			statSpecs = append(statSpecs, zwdStatSpec{
				lineIdx: idx,
				startX:  x,
				startY:  y,
				elem:    m[5],
				rest:    m[6],
			})
		}
	}

	for _, ch := range sortedLegendKeys(legendMap) {
		upper := strings.ToUpper(legendMap[ch])
		if upper == "PLAYER" || upper == "ELEMENT 1" || upper == "ELEMENT 01" {
			playerChar = ch
			hasPlayerLegend = true
		}
		if upper == "EMPTY" || upper == "ELEMENT 0" || upper == "ELEMENT 00" {
			emptyChar = ch
		}
	}

	modifiedLines := make(map[int]string)
	inGrid := false
	var out []string

	type gridRow struct {
		indent  string
		content string
	}
	var gridRows []gridRow
	var gridIndent string
	var firstRowIndent string
	var hasFirstRowIndent bool

	for lineIdx, line := range lines {
		if modLine, ok := modifiedLines[lineIdx]; ok {
			line = modLine
		}
		trimmed := strings.TrimSpace(line)
		if hasPlayerLegend && startPlayerRe.MatchString(trimmed) {
			continue
		}

		if trimmed == "grid" {
			inGrid = true
			out = append(out, line)
			gridRows = nil
			gridIndent = ""
			firstRowIndent = ""
			hasFirstRowIndent = false
			for _, r := range line {
				if r == ' ' || r == '\t' {
					gridIndent += string(r)
				} else {
					break
				}
			}
			continue
		}
		if inGrid {
			if trimmed == "end" {
				inGrid = false
				// Normalize gridRows to exactly 25 rows
				if len(gridRows) > 25 {
					gridRows = gridRows[:25]
				} else {
					padIndent := firstRowIndent
					if !hasFirstRowIndent {
						if gridIndent == "" {
							padIndent = "  "
						} else {
							padIndent = gridIndent + "  "
						}
					}
					for len(gridRows) < 25 {
						gridRows = append(gridRows, gridRow{
							indent:  padIndent,
							content: strings.Repeat(string(emptyChar), 60),
						})
					}
				}

				if hasPlayerLegend {
					// Find player positions in normalized gridRows
					type coord struct {
						r int // 0-indexed row
						c int // 0-indexed col
					}
					var playerCoords []coord
					for r, row := range gridRows {
						for c := 0; c < len(row.content); c++ {
							if row.content[c] == playerChar {
								playerCoords = append(playerCoords, coord{r: r, c: c})
							}
						}
					}

					var startX, startY int
					if len(playerCoords) == 1 {
						startX = playerCoords[0].c + 1
						startY = playerCoords[0].r + 1
					} else if len(playerCoords) > 1 {
						startX = playerCoords[0].c + 1
						startY = playerCoords[0].r + 1
						for idx, p := range playerCoords {
							if idx == 0 {
								continue
							}
							rowBytes := []byte(gridRows[p.r].content)
							rowBytes[p.c] = emptyChar
							gridRows[p.r].content = string(rowBytes)
						}
					} else {
						// len(playerCoords) == 0
						if hasOriginalStart {
							startX = originalStartX
							startY = originalStartY
						} else {
							startX = 30
							startY = 12
						}
						pRow := startY - 1
						pCol := startX - 1
						rowBytes := []byte(gridRows[pRow].content)
						rowBytes[pCol] = playerChar
						gridRows[pRow].content = string(rowBytes)
					}

					// Insert start player line before grid line
					gridIndex := -1
					for i := len(out) - 1; i >= 0; i-- {
						if strings.TrimSpace(out[i]) == "grid" {
							gridIndex = i
							break
						}
					}
					if gridIndex != -1 {
						indent := ""
						for _, r := range out[gridIndex] {
							if r == ' ' || r == '\t' {
								indent += string(r)
							} else {
								break
							}
						}
						startPlayerLine := fmt.Sprintf("%sstart player at %d,%d", indent, startX, startY)
						out = append(out[:gridIndex], append([]string{startPlayerLine}, out[gridIndex:]...)...)
					}
				}

				// Aligned stats block
				type coord struct {
					r int // 0-indexed row
					c int // 0-indexed col
				}
				gridElements := make(map[string][]coord)
				for r, row := range gridRows {
					for c := 0; c < len(row.content); c++ {
						ch := row.content[c]
						if elemName, ok := legendMap[ch]; ok {
							key := strings.ToUpper(elemName)
							gridElements[key] = append(gridElements[key], coord{r: r, c: c})
						}
					}
				}
				claimed := make(map[coord]bool)

				for _, spec := range statSpecs {
					key := strings.ToUpper(spec.elem)
					coords := gridElements[key]

					var bestCoord coord
					var found bool
					bestDist := 999999

					for _, co := range coords {
						if claimed[co] {
							continue
						}
						dist := abs(co.c+1-spec.startX) + abs(co.r+1-spec.startY)
						if dist < bestDist {
							bestDist = dist
							bestCoord = co
							found = true
						}
					}

					var alignedX, alignedY int
					if found {
						alignedX = bestCoord.c + 1
						alignedY = bestCoord.r + 1
						claimed[bestCoord] = true
					} else {
						var repChar byte
						hasRep := false
						for _, ch := range sortedLegendKeys(legendMap) {
							if strings.ToUpper(legendMap[ch]) == key {
								repChar = ch
								hasRep = true
								break
							}
						}
						if !hasRep {
							ch := getUnusedLegendKey(legendMap)
							legendMap[ch] = spec.elem
							repChar = ch
							hasRep = true

							// Insert the new legend definition before the legend's "end" line
							legendEndIdx := -1
							inLegend := false
							for idx, line := range lines {
								trimmed := strings.TrimSpace(line)
								if trimmed == "legend" {
									inLegend = true
								}
								if inLegend && trimmed == "end" {
									legendEndIdx = idx
									break
								}
							}
							if legendEndIdx != -1 {
								indent := ""
								for _, r := range lines[legendEndIdx] {
									if r == ' ' || r == '\t' {
										indent += string(r)
									} else {
										break
									}
								}
								color := "0x0F"
								if key == "PLAYER" {
									color = "0x1F"
								} else if key == "OBJECT" {
									color = "0x0F"
								}
								newLegendEntry := fmt.Sprintf("%s  %c = %s color %s", indent, ch, spec.elem, color)
								lines[legendEndIdx] = newLegendEntry + "\n" + lines[legendEndIdx]
							}
						}
						if hasRep {
							alignedX = spec.startX
							alignedY = spec.startY

							pRow := alignedY - 1
							pCol := alignedX - 1
							if pRow >= 0 && pRow < 25 && pCol >= 0 && pCol < 60 {
								rowBytes := []byte(gridRows[pRow].content)
								rowBytes[pCol] = repChar
								gridRows[pRow].content = string(rowBytes)
								claimed[coord{r: pRow, c: pCol}] = true
							}
						} else {
							alignedX = spec.startX
							alignedY = spec.startY
						}
					}

					indent := ""
					for _, r := range lines[spec.lineIdx] {
						if r == ' ' || r == '\t' {
							indent += string(r)
						} else {
							break
						}
					}
					modifiedLines[spec.lineIdx] = fmt.Sprintf("%sstat at %d,%d element %s%s", indent, alignedX, alignedY, spec.elem, spec.rest)
				}

				// The other half of stat reconciliation: a generated grid can
				// contain a stat-backed glyph with no matching declaration.  Do
				// not make the model repeat its coordinate in a separate block;
				// derive it from this final, normalized grid instead.  claimed is
				// deliberately shared with the alignment pass above, so a declared
				// stat is never emitted twice.  Scanning rows first, then columns,
				// keeps synthesized-stat ordering deterministic.
				var synthesized []string
				for r, row := range gridRows {
					for c := 0; c < len(row.content); c++ {
						co := coord{r: r, c: c}
						if claimed[co] {
							continue
						}
						elemName, ok := legendMap[row.content[c]]
						if !ok {
							continue
						}
						el, ok := elementByZWDName(elemName)
						if !ok || el == E_PLAYER || !elementNeedsStat(el) {
							continue
						}
						claimed[co] = true
						synthesized = append(synthesized, synthesizeZWDStat(el, elemName, c+1, r+1)...)
					}
				}

				var newStatsBlock []string
				if len(synthesized) > 0 {
					// A normal generated board already has a stats block.  Add to
					// that block rather than placing a second one earlier in the
					// board: parseBoard intentionally treats a later stats block as
					// authoritative.  If the model omitted it entirely, create one
					// immediately after the grid.
					statsEndIdx := zwdStatsEnd(lines, lineIdx)
					if statsEndIdx >= 0 {
						indent := leadingZWDIndent(lines[statsEndIdx])
						lines[statsEndIdx] = formatSynthesizedZWDStats(synthesized, indent+"  ") + lines[statsEndIdx]
					} else {
						indent := gridIndent
						newStatsBlock = append(newStatsBlock, indent+"stats")
						newStatsBlock = append(newStatsBlock, strings.Split(strings.TrimSuffix(formatSynthesizedZWDStats(synthesized, indent+"  "), "\n"), "\n")...)
						newStatsBlock = append(newStatsBlock, indent+"end")
					}
				}

				// Any grid char that still lacks a legend entry (and is not the
				// player or empty char) is prose the LLM drew straight into the
				// board — the top cause of dream failures. The compiler reports
				// one undefined char per compile, so with 31 distinct undefined
				// chars the K=3 repair budget can never converge. Give each one a
				// legend entry here instead: space -> Empty (walkable blank);
				// every other byte -> white on-board Text, whose legend color IS
				// the CP437 char code (the ZZT lettering idiom, the likely intent).
				// Build the true set of legend keys the way the compiler tokenizes
				// them (first whitespace token, parsed by parseByteToken). We can't
				// reuse the legendMap above: it mis-parses keys it cannot split —
				// notably '=', whose key equals the "=" separator, and any
				// pre-existing cp437:0xNN entry — which would make this scan treat
				// them as undefined and inject a duplicate legend key. Scan the
				// current lines (which already carry the stat block's injected
				// entries) so stat rep chars are excluded too.
				legendKeys := make(map[byte]bool)
				inLeg := false
				for _, l := range lines {
					for _, p := range strings.Split(l, "\n") {
						t := strings.TrimSpace(p)
						if t == "legend" {
							inLeg = true
							continue
						}
						if !inLeg {
							continue
						}
						if t == "end" {
							inLeg = false
							continue
						}
						toks := strings.Fields(t)
						if len(toks) >= 2 && toks[1] == "=" {
							if b, err := parseByteToken(toks[0]); err == nil {
								legendKeys[b] = true
							}
						}
					}
				}

				var undefinedChars []byte
				seenUndefined := make(map[byte]bool)
				for _, row := range gridRows {
					for i := 0; i < len(row.content); i++ {
						ch := row.content[i]
						if legendKeys[ch] {
							continue
						}
						if seenUndefined[ch] {
							continue
						}
						seenUndefined[ch] = true
						undefinedChars = append(undefinedChars, ch)
					}
				}
				if len(undefinedChars) > 0 {
					// Find the legend's closing "end" line. The stat-alignment
					// block above may have already turned that element into a
					// multiline "entry\n...\nend" string, so match on its LAST
					// physical line.
					legendEndIdx := -1
					inLegend := false
					for idx, l := range lines {
						physical := strings.Split(l, "\n")
						for _, p := range physical {
							if strings.TrimSpace(p) == "legend" {
								inLegend = true
							}
						}
						if inLegend && strings.TrimSpace(physical[len(physical)-1]) == "end" {
							legendEndIdx = idx
							break
						}
					}
					if legendEndIdx != -1 {
						indent := ""
						for _, r := range lines[legendEndIdx] {
							if r == ' ' || r == '\t' {
								indent += string(r)
							} else {
								break
							}
						}
						var inject strings.Builder
						for _, ch := range undefinedChars {
							if ch == ' ' {
								fmt.Fprintf(&inject, "%s  cp437:0x20 = Empty color 0x00\n", indent)
								legendMap[ch] = "Empty"
							} else if ch == emptyChar {
								// The conventional empty key is '.', but a model can
								// omit its legend entry. It must stay walkable rather
								// than becoming an on-board period of Text.
								fmt.Fprintf(&inject, "%s  cp437:0x%02X = Empty color 0x00\n", indent, ch)
								legendMap[ch] = "Empty"
							} else if ch == playerChar {
								fmt.Fprintf(&inject, "%s  cp437:0x%02X = Player color 0x1F under Empty color 0x00\n", indent, ch)
								legendMap[ch] = "Player"
							} else {
								fmt.Fprintf(&inject, "%s  cp437:0x%02X = Text-White color 0x%02X\n", indent, ch, ch)
								legendMap[ch] = "Text-White"
							}
						}
						lines[legendEndIdx] = inject.String() + lines[legendEndIdx]
					}
				}

				// Append the normalized rows to out using appropriate indentation
				for _, row := range gridRows {
					out = append(out, row.indent+row.content)
				}
				out = append(out, line)
				out = append(out, newStatsBlock...)
				continue
			}
			if strings.Contains(trimmed, "1234567890") {
				continue
			}
			indent := ""
			for _, r := range line {
				if r == ' ' || r == '\t' {
					indent += string(r)
				} else {
					break
				}
			}
			if !hasFirstRowIndent {
				firstRowIndent = indent
				hasFirstRowIndent = true
			}
			content := strings.TrimSpace(line)
			if strings.HasPrefix(content, "|") && strings.HasSuffix(content, "|") && len(content) >= 2 {
				content = content[1 : len(content)-1]
			}
			content = expandRLE(content)
			if len(content) > 60 {
				content = content[:60]
			} else if len(content) < 60 {
				content = content + strings.Repeat(string(emptyChar), 60-len(content))
			}
			gridRows = append(gridRows, gridRow{indent: indent, content: content})
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n"), warnings
}

func autoCloseZWDSections(src string, warnings []string) (string, []string) {
	lines := strings.Split(src, "\n")
	boardOpen := false
	section := ""
	inOOP := false
	sectionIndent := ""
	boardIndent := ""
	for i, line := range lines {
		text := strings.TrimSpace(line)
		if text == "" {
			continue
		}
		if !boardOpen {
			if strings.HasPrefix(text, "board ") {
				boardOpen = true
				boardIndent = leadingZWDIndent(line)
			}
			continue
		}
		if section != "" {
			switch section {
			case "stats":
				if inOOP {
					// A recurring model omission is the structural `end` after an
					// OOP program. The next stat declaration is unambiguous proof
					// that the OOP ended; without this repair every following stat
					// is parsed as OOP text and its grid glyph becomes an orphan.
					if strings.HasPrefix(strings.ToLower(text), "stat at ") {
						closeLine := leadingZWDIndent(line) + "end"
						lines = append(lines[:i], append([]string{closeLine}, lines[i:]...)...)
						warnings = append(warnings, "auto-closed oop block before stat declaration")
						return autoCloseZWDSections(strings.Join(lines, "\n"), warnings)
					}
					if text == "end" {
						inOOP = false
					}
				} else if text == "oop" {
					inOOP = true
				} else if text == "end" {
					section = ""
				}
			default:
				if text == "end" {
					section = ""
				}
			}
			continue
		}
		switch text {
		case "grid", "legend", "stats":
			section = text
			sectionIndent = leadingZWDIndent(line)
		case "end":
			boardOpen = false
		}
	}
	if !boardOpen {
		return src, warnings
	}
	if inOOP {
		lines = append(lines, sectionIndent+"  end")
		warnings = append(warnings, "auto-closed oop block")
	}
	if section != "" {
		lines = append(lines, sectionIndent+"end")
		warnings = append(warnings, "auto-closed "+section+" section")
	}
	lines = append(lines, boardIndent+"end")
	warnings = append(warnings, "auto-closed board section")
	return strings.Join(lines, "\n"), warnings
}

func deduplicateZWDLegendEntries(lines []string, warnings *[]string) []string {
	out := make([]string, 0, len(lines))
	inLegend := false
	seen := make(map[byte]bool)
	for _, line := range lines {
		text := strings.TrimSpace(line)
		if text == "legend" {
			inLegend = true
			seen = make(map[byte]bool)
			out = append(out, line)
			continue
		}
		if inLegend && text == "end" {
			inLegend = false
			out = append(out, line)
			continue
		}
		if inLegend {
			toks, err := tokenizeZWD(text, 0)
			if err == nil && len(toks) >= 2 && toks[1] == "=" {
				if key, err := parseByteToken(toks[0]); err == nil {
					if seen[key] {
						*warnings = append(*warnings, fmt.Sprintf("dropped duplicate legend key %q", string([]byte{key})))
						continue
					}
					seen[key] = true
				}
			}
		}
		out = append(out, line)
	}
	return out
}

func dropUnknownZWDStatFields(lines []string, warnings *[]string) []string {
	inStats := false
	inOOP := false
	for i, line := range lines {
		text := strings.TrimSpace(line)
		if text == "stats" {
			inStats = true
			continue
		}
		if !inStats {
			continue
		}
		if inOOP {
			if text == "end" {
				inOOP = false
			}
			continue
		}
		if text == "oop" {
			inOOP = true
			continue
		}
		if text == "end" {
			inStats = false
			continue
		}
		toks, err := tokenizeZWD(text, i+1)
		if err != nil || len(toks) == 0 || toks[0] != "stat" {
			continue
		}
		_, next, err := parseElementName(toks, 4)
		if err != nil || len(toks) < 5 || toks[1] != "at" || toks[3] != "element" {
			continue
		}
		kept := append([]string(nil), toks[:next]...)
		for next < len(toks) {
			field := toks[next]
			switch field {
			case "cycle", "p1", "p2", "step", "follower", "leader", "data-pos", "bind":
				if next+1 >= len(toks) {
					kept = append(kept, toks[next:]...)
					next = len(toks)
				} else {
					kept = append(kept, toks[next], toks[next+1])
					next += 2
				}
			case "p3":
				if next+2 < len(toks) && toks[next+1] == "board" && isQuotedToken(toks[next+2]) {
					kept = append(kept, toks[next], toks[next+1], toks[next+2])
					next += 3
				} else if next+1 < len(toks) {
					kept = append(kept, toks[next], toks[next+1])
					next += 2
				} else {
					kept = append(kept, toks[next:]...)
					next = len(toks)
				}
			case "under":
				_, underNext, underErr := parseElementName(toks, next+1)
				if underErr != nil || underNext+1 >= len(toks) || toks[underNext] != "color" {
					kept = append(kept, toks[next:]...)
					next = len(toks)
				} else {
					kept = append(kept, toks[next:underNext+2]...)
					next = underNext + 2
				}
			default:
				*warnings = append(*warnings, "dropped unknown stat field "+field)
				next++
				if next < len(toks) && !isKnownZWDStatField(toks[next]) {
					next++
				}
			}
		}
		lines[i] = leadingZWDIndent(line) + strings.Join(kept, " ")
	}
	return lines
}

func isKnownZWDStatField(field string) bool {
	switch field {
	case "cycle", "p1", "p2", "p3", "step", "under", "follower", "leader", "data-pos", "bind":
		return true
	}
	return false
}

// synthesizeZWDStat returns the shortest safe stat declaration for a glyph that
// survived grid normalization without a declared stat.  Cycles are explicit so
// the generated source records the ElementDefs-derived runtime value instead of
// depending on a parser default.  Parameter values mirror
// InitEditorStatSettings: ordinary intelligence/rate defaults are 4, Bear and
// Object retain their element-specific defaults, and direction-bearing editor
// elements point north.  A passage uses board 0, which is always representable
// even when preprocessing a single board section with no world-name context.
func synthesizeZWDStat(el byte, elemName string, x, y int) []string {
	line := fmt.Sprintf("stat at %d,%d element %s cycle %d", x, y, elemName, ElementDefs[el].Cycle)
	switch el {
	case E_PASSAGE:
		line += " p3 0"
	case E_TRANSPORTER, E_PUSHER, E_DUPLICATOR, E_BLINK_WALL:
		line += " step north"
	}

	// TStat's P1/P2 fields are element parameters.  Only supply values for
	// parameters the element actually defines; doing otherwise can, for
	// example, arm a synthesized bomb or turn an inert projectile into an
	// invented behavior.
	switch el {
	case E_LION, E_SHARK:
		line += " p1 4"
	case E_TIGER, E_RUFFIAN, E_SPINNING_GUN, E_CENTIPEDE_HEAD:
		line += " p1 4 p2 4"
	case E_SLIME:
		line += " p2 4"
	case E_BLINK_WALL:
		line += " p1 4 p2 4"
	case E_DUPLICATOR:
		line += " p2 4"
	}

	if el != E_OBJECT {
		return []string{line}
	}
	// Objects are the exception: the compiler requires an OOP body so they
	// remain a real, inert object rather than a stat-shaped invalid tile.
	return []string{line, "oop", "#end", "end"}
}

func formatSynthesizedZWDStats(stats []string, indent string) string {
	var b strings.Builder
	for _, statLine := range stats {
		fmt.Fprintf(&b, "%s%s\n", indent, statLine)
	}
	return b.String()
}

func leadingZWDIndent(line string) string {
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}

// zwdStatsEnd finds the closing end of the first stats block following a grid.
// OOP blocks contain their own end, so they must be skipped rather than being
// mistaken for the stats terminator.
func zwdStatsEnd(lines []string, gridEnd int) int {
	inSection := false
	for i := gridEnd + 1; i < len(lines); i++ {
		text := strings.TrimSpace(lines[i])
		if text == "stats" {
			inOOP := false
			for j := i + 1; j < len(lines); j++ {
				body := strings.TrimSpace(lines[j])
				if body == "oop" {
					inOOP = true
					continue
				}
				if body == "end" {
					if inOOP {
						inOOP = false
						continue
					}
					return j
				}
			}
			return -1
		}
		if text == "grid" || text == "legend" {
			inSection = true
			continue
		}
		if text == "end" {
			if inSection {
				inSection = false
				continue
			}
			// This is the enclosing board end; there is no stats block.
			return -1
		}
	}
	return -1
}
