package zztgo

import (
	"fmt"
	"strings"
)

// DecompileZWD converts a loaded TWorld (with all boards serialized as
// BoardData) into ZWD text. The caller must have called BoardClose on the
// current board before calling this, so that BoardData is up to date for every
// board.
func DecompileZWD(world *TWorld) string {
	init := NewEngine()
	init.InitElementsGame()

	var b strings.Builder
	b.WriteString("zwd 1\n")
	b.WriteString(fmt.Sprintf("world %s\n", quoteZWD(world.Info.Name)))

	// Build board-id → name map for exit and passage references.
	boardNames := make([]string, world.BoardCount+1)
	e := NewEngine()
	e.Headless = true
	e.World = *world
	for i := int16(0); i <= world.BoardCount; i++ {
		e.BoardOpen(i)
		boardNames[i] = e.Board.Name
	}

	for i := int16(0); i <= world.BoardCount; i++ {
		e.BoardOpen(i)
		b.WriteByte('\n')
		decompileBoard(&b, e, i, boardNames)
	}

	return b.String()
}

// decompileBoard writes one board section to the builder.
func decompileBoard(b *strings.Builder, e *Engine, boardID int16, boardNames []string) {
	board := &e.Board

	// The player tile in the grid is always at Stats[0].X/Y — the compiler
	// requires the "start player at" coordinate to match the Player legend
	// symbol's position in the grid. We use Stats[0] as the canonical player
	// position. Board.Info.StartPlayerX/Y is a respawn point that may differ
	// from the active player position; we use it only when Stats[0] is invalid.
	startX := board.Stats[0].X
	startY := board.Stats[0].Y
	if startX == 0 || startY == 0 || int(startX) > BOARD_WIDTH || int(startY) > BOARD_HEIGHT {
		// Fallback: try board.Info's respawn point.
		startX = board.Info.StartPlayerX
		startY = board.Info.StartPlayerY
	}

	b.WriteString(fmt.Sprintf("board %s\n", quoteZWD(board.Name)))
	b.WriteString(fmt.Sprintf("  start player at %d,%d\n", startX, startY))
	// Emit respawn at when Info.StartPlayerX/Y differs from the player's
	// actual tile position (Stats[0]).  ZZT uses this as the re-enter point
	// when the player dies or walks through a board edge.  0,0 means "use
	// the start-player position" so we don't emit it in that case.
	respawnX := board.Info.StartPlayerX
	respawnY := board.Info.StartPlayerY
	if (respawnX != startX || respawnY != startY) && respawnX != 0 && respawnY != 0 {
		b.WriteString(fmt.Sprintf("  respawn at %d,%d\n", respawnX, respawnY))
	}
	b.WriteString(fmt.Sprintf("  max-shots %d\n", board.Info.MaxShots))
	b.WriteString(fmt.Sprintf("  dark %s\n", boolStr(board.Info.IsDark)))
	b.WriteString(fmt.Sprintf("  reenter %s\n", boolStr(board.Info.ReenterWhenZapped)))
	b.WriteString(fmt.Sprintf("  time-limit %d\n", board.Info.TimeLimitSec))

	// Exits.
	exitNames := [4]string{"none", "none", "none", "none"}
	exitDirs := [4]string{"north", "south", "west", "east"}
	for i := 0; i < 4; i++ {
		nb := board.Info.NeighborBoards[i]
		if nb != 0 && int16(nb) <= e.World.BoardCount {
			exitNames[i] = quoteZWD(boardNames[nb])
		}
	}
	b.WriteString(fmt.Sprintf("  exits %s %s %s %s %s %s %s %s\n",
		exitDirs[0], exitNames[0],
		exitDirs[1], exitNames[1],
		exitDirs[2], exitNames[2],
		exitDirs[3], exitNames[3]))

	if board.Info.Message != "" {
		b.WriteString(fmt.Sprintf("  message %s\n", quoteZWD(board.Info.Message)))
	}

	// -- Legend construction --
	//
	// A legend maps a single-byte grid character → (element, color, optional
	// under, optional toBoard).  Two tiles that differ only in under or
	// toBoard need separate legend entries.  The compound key captures that.

	type compoundKey struct {
		element    byte
		color      byte
		underElem  byte
		underColor byte
		toBoard    string
	}

	type legendEntry struct {
		element    byte
		color      byte
		hasUnder   bool
		underElem  byte
		underColor byte
		toBoard    string
	}

	// Map position → stat index for quick lookup.
	statAt := make(map[[2]byte]int16)
	for si := int16(0); si <= board.StatCount; si++ {
		stat := &board.Stats[si]
		key := [2]byte{stat.X, stat.Y}
		statAt[key] = si
	}

	legendEntries := make(map[compoundKey]legendEntry)
	var legendOrder []compoundKey
	gridKeys := [BOARD_WIDTH][BOARD_HEIGHT]compoundKey{}

	for y := int16(1); y <= BOARD_HEIGHT; y++ {
		for x := int16(1); x <= BOARD_WIDTH; x++ {
			tile := board.Tiles[x][y]
			ck := compoundKey{element: tile.Element, color: tile.Color}

			// Attach stat-specific data for elements that carry it.
			if si, ok := statAt[[2]byte{byte(x), byte(y)}]; ok {
				stat := &board.Stats[si]
				if tile.Element == E_PLAYER {
					ck.underElem = stat.Under.Element
					ck.underColor = stat.Under.Color
				}
				if tile.Element == E_PASSAGE && stat.P3 != 0 && int16(stat.P3) <= e.World.BoardCount {
					ck.toBoard = boardNames[stat.P3]
				}
			}

			if _, exists := legendEntries[ck]; !exists {
				le := legendEntry{
					element: tile.Element,
					color:   tile.Color,
				}
				if tile.Element == E_PLAYER {
					le.hasUnder = true
					le.underElem = ck.underElem
					le.underColor = ck.underColor
				}
				if ck.toBoard != "" {
					le.toBoard = ck.toBoard
				}
				legendEntries[ck] = le
				legendOrder = append(legendOrder, ck)
			}
			gridKeys[x-1][y-1] = ck
		}
	}

	// -- Assign legend characters --
	//
	// Every legend key must be a printable ASCII byte (0x21..0x7E) so that
	// it survives as a single byte in the grid without breaking line
	// structure.  The strategy:
	//   Phase 1 — try a preferred char for each element type (first come)
	//   Phase 2 — try EditorShortcut
	//   Phase 3 — allocate from the printable pool

	usedChars := make(map[byte]bool)
	charForKey := make(map[compoundKey]byte)

	// Preferred chars per element — hand-picked to match ZWD examples and
	// maximise readability.
	preferredChars := map[byte]byte{
		E_EMPTY:              '.',
		E_SOLID:              '#',
		E_NORMAL:             'N',
		E_BREAKABLE:          'B',
		E_PLAYER:             '@',
		E_OBJECT:             'o',
		E_SCROLL:             '!',
		E_PASSAGE:            'p',
		E_GEM:                'g',
		E_KEY:                'k',
		E_DOOR:               '+',
		E_AMMO:               'a',
		E_TORCH:              't',
		E_ENERGIZER:          'e',
		E_BOMB:               'b',
		E_BEAR:               'R',
		E_RUFFIAN:            'r',
		E_LION:               'L',
		E_TIGER:              'T',
		E_SHARK:              'S',
		E_SLIME:              's',
		E_BOULDER:            'O',
		E_PUSHER:             'P',
		E_DUPLICATOR:         'd',
		E_WATER:              '~',
		E_FOREST:             'f',
		E_FAKE:               'F',
		E_INVISIBLE:          'i',
		E_LINE:               'l',
		E_RICOCHET:           '*',
		E_MONITOR:            'M',
		E_CONVEYOR_CW:        '/',
		E_CONVEYOR_CCW:       '\\',
		E_BLINK_WALL:         'W',
		E_TRANSPORTER:        'X',
		E_SPINNING_GUN:       'G',
		E_SLIDER_NS:          'n',
		E_SLIDER_EW:          'w',
		E_CENTIPEDE_HEAD:     'H',
		E_CENTIPEDE_SEGMENT:  'c',
		E_BULLET:             'u',
		E_STAR:               'x',
	}

	isPrintableASCII := func(ch byte) bool { return ch >= 0x21 && ch <= 0x7E }

	// Phase 1: preferred chars.
	for _, ck := range legendOrder {
		le := legendEntries[ck]

		// For text elements, if the displayed character is printable and
		// available, use it so the grid reads naturally.
		if le.element >= E_TEXT_MIN && le.element <= E_TEXT_WHITE {
			ch := le.color // displayed char stored in color
			if isPrintableASCII(ch) && !usedChars[ch] {
				charForKey[ck] = ch
				usedChars[ch] = true
				continue
			}
		}

		if pref, ok := preferredChars[le.element]; ok && !usedChars[pref] {
			charForKey[ck] = pref
			usedChars[pref] = true
		}
	}

	// Phase 2: EditorShortcut.
	for _, ck := range legendOrder {
		if _, ok := charForKey[ck]; ok {
			continue
		}
		le := legendEntries[ck]
		if int(le.element) <= MAX_ELEMENT {
			sc := ElementDefs[le.element].EditorShortcut
			if isPrintableASCII(sc) && !usedChars[sc] {
				charForKey[ck] = sc
				usedChars[sc] = true
			}
		}
	}

	// Phase 3: fallback pool — printable ASCII chars not yet used.
	pool := buildFallbackPool()
	poolIdx := 0
	for _, ck := range legendOrder {
		if _, ok := charForKey[ck]; ok {
			continue
		}
		for poolIdx < len(pool) && usedChars[pool[poolIdx]] {
			poolIdx++
		}
		if poolIdx < len(pool) {
			ch := pool[poolIdx]
			charForKey[ck] = ch
			usedChars[ch] = true
			poolIdx++
		} else {
			// Exhausted printable ASCII — this should be extremely rare
			// (94 distinct printable chars, typical boards use far fewer
			// unique tiles).  Fall back to high bytes (0x80..0xFE).
			for v := byte(0x80); v < 0xFF; v++ {
				if !usedChars[v] {
					charForKey[ck] = v
					usedChars[v] = true
					break
				}
			}
		}
	}

	// -- Write grid --
	b.WriteString("\n  grid\n")
	for y := 0; y < BOARD_HEIGHT; y++ {
		for x := 0; x < BOARD_WIDTH; x++ {
			ck := gridKeys[x][y]
			b.WriteByte(charForKey[ck])
		}
		b.WriteByte('\n')
	}
	b.WriteString("  end\n")

	// -- Write legend --
	b.WriteString("\n  legend\n")
	for _, ck := range legendOrder {
		le := legendEntries[ck]
		ch := charForKey[ck]
		elemName := zwdElementName(le.element)
		colorStr := zwdColorStr(le.color)

		var line strings.Builder
		line.WriteString("    ")
		// Legend key.
		if isPrintableASCII(ch) {
			line.WriteByte(ch)
		} else {
			line.WriteString(fmt.Sprintf("cp437:0x%02X", ch))
		}
		line.WriteString(" = ")
		line.WriteString(elemName)
		line.WriteString(" color ")
		line.WriteString(colorStr)

		// Under tile (for Player).
		if le.hasUnder {
			line.WriteString(" under ")
			line.WriteString(zwdElementName(le.underElem))
			line.WriteString(" color ")
			line.WriteString(zwdColorStr(le.underColor))
		}

		// Passage destination.
		if le.toBoard != "" {
			line.WriteString(" to ")
			line.WriteString(quoteZWD(le.toBoard))
		}

		line.WriteByte('\n')
		b.WriteString(line.String())
	}
	b.WriteString("  end\n")

	// -- Write stats --
	b.WriteString("\n  stats\n")
	for si := int16(1); si <= board.StatCount; si++ {
		stat := &board.Stats[si]
		// Skip off-board stats (X=0 or Y=0 or out-of-bounds). ZZT uses these
		// as sentinel/null targets for centipede chains; they carry no tile.
		if stat.X == 0 || stat.Y == 0 || int(stat.X) > BOARD_WIDTH || int(stat.Y) > BOARD_HEIGHT {
			continue
		}
		elem := board.Tiles[stat.X][stat.Y].Element
		elemName := zwdElementName(elem)
		var line strings.Builder
		line.WriteString(fmt.Sprintf("    stat at %d,%d element %s", stat.X, stat.Y, elemName))

		// Cycle: omit if it matches ElementDefs default (and default >= 0).
		defCycle := ElementDefs[elem].Cycle
		if defCycle < 0 || stat.Cycle != defCycle {
			line.WriteString(fmt.Sprintf(" cycle %d", stat.Cycle))
		}

		// p1: omit if matches default.
		defP1 := byte(4)
		if elem == E_OBJECT {
			defP1 = 1
		} else if elem == E_BEAR {
			defP1 = 8
		}
		if stat.P1 != defP1 {
			if elem == E_OBJECT {
				line.WriteString(fmt.Sprintf(" p1 %s", zwdByteStr(stat.P1)))
			} else {
				line.WriteString(fmt.Sprintf(" p1 %d", stat.P1))
			}
		}

		// p2: omit if 4 (default).
		if stat.P2 != 4 {
			line.WriteString(fmt.Sprintf(" p2 %d", stat.P2))
		}

		// p3: use board name shorthand for passages.
		if stat.P3 != 0 {
			if ElementDefs[elem].ParamBoardName != "" && int16(stat.P3) <= e.World.BoardCount {
				line.WriteString(fmt.Sprintf(" p3 board %s", quoteZWD(boardNames[stat.P3])))
			} else {
				line.WriteString(fmt.Sprintf(" p3 %d", stat.P3))
			}
		}

		// Step direction.
		stepStr := zwdStepStr(stat.StepX, stat.StepY)
		if stepStr != "0,-1" {
			line.WriteString(fmt.Sprintf(" step %s", stepStr))
		}

		// Under tile: omit if Empty color 0x00.
		if stat.Under.Element != E_EMPTY || stat.Under.Color != 0x00 {
			line.WriteString(fmt.Sprintf(" under %s color %s",
				zwdElementName(stat.Under.Element),
				zwdColorStr(stat.Under.Color)))
		}

		// Follower/Leader: omit if -1.
		if stat.Follower != -1 {
			line.WriteString(fmt.Sprintf(" follower %d", stat.Follower))
		}
		if stat.Leader != -1 {
			line.WriteString(fmt.Sprintf(" leader %d", stat.Leader))
		}

		// DataPos: omit if 0.
		if stat.DataPos != 0 {
			line.WriteString(fmt.Sprintf(" data-pos %d", stat.DataPos))
		}

		// Bind: emit when DataLen is negative.
		if stat.DataLen < 0 {
			line.WriteString(fmt.Sprintf(" bind %d", -stat.DataLen))
		}

		line.WriteByte('\n')
		b.WriteString(line.String())

		// OOP block.
		if stat.DataLen > 0 && stat.Data != "" {
			b.WriteString("    oop\n")
			oopLines := strings.Split(stat.Data, string([]byte{KEY_ENTER}))
			for _, ol := range oopLines {
				b.WriteString(ol)
				b.WriteByte('\n')
			}
			b.WriteString("    end\n")
		}
	}
	b.WriteString("  end\n")

	b.WriteString("end\n")
}

// textElementNames maps E_TEXT_BLUE..E_TEXT_WHITE to their canonical ZWD names.
var textElementNames = [7]string{
	"Text-Blue",   // 47 = E_TEXT_BLUE
	"Text-Green",  // 48 = E_TEXT_GREEN
	"Text-Cyan",   // 49 = E_TEXT_CYAN
	"Text-Red",    // 50 = E_TEXT_RED
	"Text-Purple", // 51 = E_TEXT_PURPLE
	"Text-Yellow", // 52 = E_TEXT_YELLOW
	"Text-White",  // 53 = E_TEXT_WHITE
}

// zwdElementName returns the ZWD-format name for an element index.
func zwdElementName(elem byte) string {
	// Text elements have no Name in ElementDefs — use canonical ZWD names.
	if elem >= E_TEXT_MIN && elem <= E_TEXT_WHITE {
		return textElementNames[elem-E_TEXT_MIN]
	}
	if int(elem) <= MAX_ELEMENT && ElementDefs[elem].Name != "" {
		return ElementDefs[elem].Name
	}
	return fmt.Sprintf("element %d", elem)
}

// zwdColorStr returns a color value as a hex string.
func zwdColorStr(color byte) string {
	return fmt.Sprintf("0x%02X", color)
}

// zwdByteStr returns a byte in cp437:0xNN notation.
func zwdByteStr(v byte) string {
	return fmt.Sprintf("cp437:0x%02X", v)
}

// zwdStepStr returns a step direction as a named string or dx,dy.
func zwdStepStr(dx, dy int16) string {
	switch {
	case dx == 0 && dy == 0:
		return "idle"
	case dx == 0 && dy == -1:
		return "north"
	case dx == 0 && dy == 1:
		return "south"
	case dx == -1 && dy == 0:
		return "west"
	case dx == 1 && dy == 0:
		return "east"
	default:
		return fmt.Sprintf("%d,%d", dx, dy)
	}
}

// quoteZWD wraps a string in double quotes, escaping internal quotes and
// backslashes.
func quoteZWD(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteByte(s[i])
		}
	}
	b.WriteByte('"')
	return b.String()
}

func boolStr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// buildFallbackPool returns printable ASCII bytes (0x21..0x7E) prioritized
// for legend readability: lowercase, uppercase, digits, then symbols.
func buildFallbackPool() []byte {
	var pool []byte
	for c := byte('a'); c <= 'z'; c++ {
		pool = append(pool, c)
	}
	for c := byte('A'); c <= 'Z'; c++ {
		pool = append(pool, c)
	}
	for c := byte('0'); c <= '9'; c++ {
		pool = append(pool, c)
	}
	for _, c := range []byte{
		'!', '@', '#', '$', '%', '&', '*', '+', '-', '=', '~',
		'^', '_', ':', ';', ',', '.', '/', '\\', '|', '<', '>',
		'?', '`', '\'', '(', ')', '[', ']', '{', '}',
	} {
		pool = append(pool, c)
	}
	return pool
}
