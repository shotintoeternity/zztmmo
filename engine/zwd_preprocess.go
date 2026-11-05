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
var (
	zwdStartPlayerRe = regexp.MustCompile(`(?i)^\s*start\s+player\s+at\s+([0-9]+)\s*,\s*([0-9]+)`)
	zwdStatAtRe      = regexp.MustCompile(`(?i)^(\s*stat\s+at\s+)([0-9]+)\s*,\s*([0-9]+)(\s+element\s+)(\S+)(.*)`)
)

type zwdStatSpec struct {
	lineIdx int
	startX  int
	startY  int
	elem    string
	rest    string
}

type zwdGridRow struct {
	indent  string
	content string
}

type zwdGridCoord struct {
	r int // 0-indexed row
	c int // 0-indexed col
}

// zwdGridState is one board's preprocessing. lines is shared with the pass
// that walks it: finishing a grid writes legend and stat entries into later
// lines (and modifiedLines), and the walk then emits them as rewritten.
type zwdGridState struct {
	lines         []string
	legendMap     map[byte]string
	modifiedLines map[int]string
	statSpecs     []zwdStatSpec
	out           []string

	playerChar      byte
	emptyChar       byte
	hasPlayerLegend bool

	originalStartX, originalStartY int
	hasOriginalStart               bool

	gridRows          []zwdGridRow
	gridIndent        string
	firstRowIndent    string
	hasFirstRowIndent bool
}

// autoCloseZWDLines is autoCloseZWDSections in the line-based shape the other
// passes share.
func autoCloseZWDLines(lines []string, warnings *[]string) []string {
	src, w := autoCloseZWDSections(strings.Join(lines, "\n"), *warnings)
	*warnings = w
	return strings.Split(src, "\n")
}

func preprocessZWDGridWithWarnings(zwdText string) (string, []string) {
	var warnings []string
	lines := autoCloseZWDLines(strings.Split(zwdText, "\n"), &warnings)
	// This preprocessor needs the same element metadata as the ZWD compiler to
	// recognize a legend's stat-backed elements and to derive their default
	// cycles.  Generation can call us before any world has been compiled.
	ensureElementDefs()
	lines = deduplicateZWDLegendEntries(lines, &warnings)
	lines = dropUnknownZWDStatFields(lines, &warnings)

	st := &zwdGridState{
		lines:         lines,
		legendMap:     make(map[byte]string),
		modifiedLines: make(map[int]string),
		playerChar:    byte('@'),
		emptyChar:     byte('.'),
	}
	st.scanLegendAndStats()

	inGrid := false
	for lineIdx, line := range st.lines {
		if modLine, ok := st.modifiedLines[lineIdx]; ok {
			line = modLine
		}
		trimmed := strings.TrimSpace(line)
		if st.hasPlayerLegend && zwdStartPlayerRe.MatchString(trimmed) {
			continue
		}

		if trimmed == "grid" {
			inGrid = true
			st.out = append(st.out, line)
			st.gridRows = nil
			st.gridIndent = ""
			st.firstRowIndent = ""
			st.hasFirstRowIndent = false
			for _, r := range line {
				if r == ' ' || r == '\t' {
					st.gridIndent += string(r)
				} else {
					break
				}
			}
			continue
		}
		if inGrid {
			if trimmed == "end" {
				inGrid = false
				st.finishGrid(lineIdx, line)
				continue
			}
			if strings.Contains(trimmed, "1234567890") {
				continue
			}
			st.addGridRow(line)
			continue
		}
		st.out = append(st.out, line)
	}
	return strings.Join(st.out, "\n"), warnings
}

// scanLegendAndStats reads the legend's glyphs, any declared start position
// and the stat declarations, and picks the player and empty glyphs.
func (st *zwdGridState) scanLegendAndStats() {
	for idx, line := range st.lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "=") {
			parts := strings.SplitN(trimmed, "=", 2)
			if len(parts) == 2 {
				k := strings.TrimSpace(parts[0])
				if len(k) == 1 {
					ch := k[0]
					elemName := parseLegendElemName(parts[1])
					if elemName != "" {
						st.legendMap[ch] = elemName
					}
				}
			}
		}
		if m := zwdStartPlayerRe.FindStringSubmatch(trimmed); m != nil {
			x, _ := strconv.Atoi(m[1])
			y, _ := strconv.Atoi(m[2])
			if x >= 1 && x <= 60 && y >= 1 && y <= 25 {
				st.originalStartX = x
				st.originalStartY = y
				st.hasOriginalStart = true
			}
		}
		if m := zwdStatAtRe.FindStringSubmatch(line); m != nil {
			x, _ := strconv.Atoi(m[2])
			y, _ := strconv.Atoi(m[3])
			st.statSpecs = append(st.statSpecs, zwdStatSpec{
				lineIdx: idx,
				startX:  x,
				startY:  y,
				elem:    m[5],
				rest:    m[6],
			})
		}
	}

	for _, ch := range sortedLegendKeys(st.legendMap) {
		upper := strings.ToUpper(st.legendMap[ch])
		if upper == "PLAYER" || upper == "ELEMENT 1" || upper == "ELEMENT 01" {
			st.playerChar = ch
			st.hasPlayerLegend = true
		}
		if upper == "EMPTY" || upper == "ELEMENT 0" || upper == "ELEMENT 00" {
			st.emptyChar = ch
		}
	}
}

// addGridRow takes one raw grid row: indent kept, |…| frame and ruler
// stripped, RLE expanded, and the row cut or padded to 60.
func (st *zwdGridState) addGridRow(line string) {
	indent := ""
	for _, r := range line {
		if r == ' ' || r == '\t' {
			indent += string(r)
		} else {
			break
		}
	}
	if !st.hasFirstRowIndent {
		st.firstRowIndent = indent
		st.hasFirstRowIndent = true
	}
	content := strings.TrimSpace(line)
	if strings.HasPrefix(content, "|") && strings.HasSuffix(content, "|") && len(content) >= 2 {
		content = content[1 : len(content)-1]
	}
	content = expandRLE(content)
	if len(content) > 60 {
		content = content[:60]
	} else if len(content) < 60 {
		content = content + strings.Repeat(string(st.emptyChar), 60-len(content))
	}
	st.gridRows = append(st.gridRows, zwdGridRow{indent: indent, content: content})
}

// finishGrid runs at a grid's "end" line (lineIdx): it repairs the rows it
// collected and the declarations that refer to them, then emits the grid.
func (st *zwdGridState) finishGrid(lineIdx int, line string) {
	st.normalizeGridRows()
	if st.hasPlayerLegend {
		st.placeStartPlayer()
	}
	claimed := st.alignStats()
	newStatsBlock := st.synthesizeUnclaimedStats(claimed, lineIdx)
	st.defineUndefinedGlyphs()

	// Append the normalized rows to out using appropriate indentation
	for _, row := range st.gridRows {
		st.out = append(st.out, row.indent+row.content)
	}
	st.out = append(st.out, line)
	st.out = append(st.out, newStatsBlock...)
}

// normalizeGridRows makes the grid exactly 25 rows.
func (st *zwdGridState) normalizeGridRows() {
	// Normalize gridRows to exactly 25 rows
	if len(st.gridRows) > 25 {
		st.gridRows = st.gridRows[:25]
	} else {
		padIndent := st.firstRowIndent
		if !st.hasFirstRowIndent {
			if st.gridIndent == "" {
				padIndent = "  "
			} else {
				padIndent = st.gridIndent + "  "
			}
		}
		for len(st.gridRows) < 25 {
			st.gridRows = append(st.gridRows, zwdGridRow{
				indent:  padIndent,
				content: strings.Repeat(string(st.emptyChar), 60),
			})
		}
	}
}

// placeStartPlayer leaves exactly one player glyph on the grid and writes the
// "start player at" line for it before the grid.
func (st *zwdGridState) placeStartPlayer() {
	// Find player positions in normalized gridRows
	var playerCoords []zwdGridCoord
	for r, row := range st.gridRows {
		for c := 0; c < len(row.content); c++ {
			if row.content[c] == st.playerChar {
				playerCoords = append(playerCoords, zwdGridCoord{r: r, c: c})
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
			rowBytes := []byte(st.gridRows[p.r].content)
			rowBytes[p.c] = st.emptyChar
			st.gridRows[p.r].content = string(rowBytes)
		}
	} else {
		// len(playerCoords) == 0
		if st.hasOriginalStart {
			startX = st.originalStartX
			startY = st.originalStartY
		} else {
			startX = 30
			startY = 12
		}
		pRow := startY - 1
		pCol := startX - 1
		rowBytes := []byte(st.gridRows[pRow].content)
		rowBytes[pCol] = st.playerChar
		st.gridRows[pRow].content = string(rowBytes)
	}

	// Insert start player line before grid line
	gridIndex := -1
	for i := len(st.out) - 1; i >= 0; i-- {
		if strings.TrimSpace(st.out[i]) == "grid" {
			gridIndex = i
			break
		}
	}
	if gridIndex != -1 {
		indent := ""
		for _, r := range st.out[gridIndex] {
			if r == ' ' || r == '\t' {
				indent += string(r)
			} else {
				break
			}
		}
		startPlayerLine := fmt.Sprintf("%sstart player at %d,%d", indent, startX, startY)
		st.out = append(st.out[:gridIndex], append([]string{startPlayerLine}, st.out[gridIndex:]...)...)
	}
}

// alignStats moves each declared stat onto the nearest unclaimed glyph of its
// element, or draws one at the declared square, and returns the squares now
// claimed by a stat.
func (st *zwdGridState) alignStats() map[zwdGridCoord]bool {
	// Aligned stats block
	gridElements := make(map[string][]zwdGridCoord)
	for r, row := range st.gridRows {
		for c := 0; c < len(row.content); c++ {
			ch := row.content[c]
			if elemName, ok := st.legendMap[ch]; ok {
				key := strings.ToUpper(elemName)
				gridElements[key] = append(gridElements[key], zwdGridCoord{r: r, c: c})
			}
		}
	}
	claimed := make(map[zwdGridCoord]bool)

	for _, spec := range st.statSpecs {
		key := strings.ToUpper(spec.elem)
		coords := gridElements[key]

		var bestCoord zwdGridCoord
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
			repChar, hasRep := st.statRepresentative(spec, key)
			if hasRep {
				alignedX = spec.startX
				alignedY = spec.startY

				pRow := alignedY - 1
				pCol := alignedX - 1
				if pRow >= 0 && pRow < 25 && pCol >= 0 && pCol < 60 {
					rowBytes := []byte(st.gridRows[pRow].content)
					rowBytes[pCol] = repChar
					st.gridRows[pRow].content = string(rowBytes)
					claimed[zwdGridCoord{r: pRow, c: pCol}] = true
				}
			} else {
				alignedX = spec.startX
				alignedY = spec.startY
			}
		}

		indent := ""
		for _, r := range st.lines[spec.lineIdx] {
			if r == ' ' || r == '\t' {
				indent += string(r)
			} else {
				break
			}
		}
		st.modifiedLines[spec.lineIdx] = fmt.Sprintf("%sstat at %d,%d element %s%s", indent, alignedX, alignedY, spec.elem, spec.rest)
	}
	return claimed
}

// statRepresentative is the glyph a stat with no glyph on the grid is drawn
// with: the legend's key for its element, or a new key added to the legend.
func (st *zwdGridState) statRepresentative(spec zwdStatSpec, key string) (byte, bool) {
	var repChar byte
	hasRep := false
	for _, ch := range sortedLegendKeys(st.legendMap) {
		if strings.ToUpper(st.legendMap[ch]) == key {
			repChar = ch
			hasRep = true
			break
		}
	}
	if !hasRep {
		ch := getUnusedLegendKey(st.legendMap)
		st.legendMap[ch] = spec.elem
		repChar = ch
		hasRep = true

		// Insert the new legend definition before the legend's "end" line
		legendEndIdx := -1
		inLegend := false
		for idx, line := range st.lines {
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
			for _, r := range st.lines[legendEndIdx] {
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
			st.lines[legendEndIdx] = newLegendEntry + "\n" + st.lines[legendEndIdx]
		}
	}
	return repChar, hasRep
}

// synthesizeUnclaimedStats declares a stat for every stat-backed glyph no
// declaration claimed. It adds them to the board's stats block, or returns a
// new block to emit after the grid when the board has none.
func (st *zwdGridState) synthesizeUnclaimedStats(claimed map[zwdGridCoord]bool, lineIdx int) []string {
	// The other half of stat reconciliation: a generated grid can
	// contain a stat-backed glyph with no matching declaration.  Do
	// not make the model repeat its coordinate in a separate block;
	// derive it from this final, normalized grid instead.  claimed is
	// deliberately shared with the alignment pass above, so a declared
	// stat is never emitted twice.  Scanning rows first, then columns,
	// keeps synthesized-stat ordering deterministic.
	var synthesized []string
	for r, row := range st.gridRows {
		for c := 0; c < len(row.content); c++ {
			co := zwdGridCoord{r: r, c: c}
			if claimed[co] {
				continue
			}
			elemName, ok := st.legendMap[row.content[c]]
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
		statsEndIdx := zwdStatsEnd(st.lines, lineIdx)
		if statsEndIdx >= 0 {
			indent := leadingZWDIndent(st.lines[statsEndIdx])
			st.lines[statsEndIdx] = formatSynthesizedZWDStats(synthesized, indent+"  ") + st.lines[statsEndIdx]
		} else {
			indent := st.gridIndent
			newStatsBlock = append(newStatsBlock, indent+"stats")
			newStatsBlock = append(newStatsBlock, strings.Split(strings.TrimSuffix(formatSynthesizedZWDStats(synthesized, indent+"  "), "\n"), "\n")...)
			newStatsBlock = append(newStatsBlock, indent+"end")
		}
	}
	return newStatsBlock
}

// defineUndefinedGlyphs gives every grid glyph without a legend entry one.
func (st *zwdGridState) defineUndefinedGlyphs() {
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
	for _, l := range st.lines {
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
	for _, row := range st.gridRows {
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
	if len(undefinedChars) == 0 {
		return
	}
	// Find the legend's closing "end" line. The stat-alignment
	// block above may have already turned that element into a
	// multiline "entry\n...\nend" string, so match on its LAST
	// physical line.
	legendEndIdx := -1
	inLegend := false
	for idx, l := range st.lines {
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
		for _, r := range st.lines[legendEndIdx] {
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
				st.legendMap[ch] = "Empty"
			} else if ch == st.emptyChar {
				// The conventional empty key is '.', but a model can
				// omit its legend entry. It must stay walkable rather
				// than becoming an on-board period of Text.
				fmt.Fprintf(&inject, "%s  cp437:0x%02X = Empty color 0x00\n", indent, ch)
				st.legendMap[ch] = "Empty"
			} else if ch == st.playerChar {
				fmt.Fprintf(&inject, "%s  cp437:0x%02X = Player color 0x1F under Empty color 0x00\n", indent, ch)
				st.legendMap[ch] = "Player"
			} else {
				fmt.Fprintf(&inject, "%s  cp437:0x%02X = Text-White color 0x%02X\n", indent, ch, ch)
				st.legendMap[ch] = "Text-White"
			}
		}
		st.lines[legendEndIdx] = inject.String() + st.lines[legendEndIdx]
	}
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
