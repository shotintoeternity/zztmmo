package zztgo

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// CompileZWD parses a ZZT World Description document and returns vanilla .ZZT
// bytes. The parser is intentionally strict: repair-loop errors are more useful
// when bad fields are rejected instead of guessed.
func CompileZWD(src string) ([]byte, error) {
	world, err := CompileZWDWorld(src)
	if err != nil {
		return nil, err
	}
	e := NewEngine()
	e.Headless = true
	e.World = world
	var out bytes.Buffer
	if err := e.worldWriteTo(&out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// CompileZWDWorld parses ZWD into the in-memory world structs used by the
// serializer. It is exposed separately so tests and later services can inspect
// the compiled world without round-tripping through bytes.
func CompileZWDWorld(src string) (TWorld, error) {
	init := NewEngine()
	init.InitElementsGame()
	p := newZWDParser(src)
	doc, err := p.parse()
	if err != nil {
		return TWorld{}, err
	}
	return compileZWDDocument(doc)
}

type zwdError struct {
	line, col int
	msg       string
}

func (e *zwdError) Error() string {
	if e.line <= 0 {
		return e.msg
	}
	return fmt.Sprintf("line %d, col %d: %s", e.line, e.col, e.msg)
}

type zwdParser struct {
	lines []string
	pos   int
}

type zwdDocument struct {
	worldName string
	boards    []zwdBoard
}

type zwdBoard struct {
	name       string
	line       int
	startX     int16
	startY     int16
	hasRespawn bool
	respawnX   int16
	respawnY   int16
	maxShots   byte
	dark       bool
	reenter    bool
	timeLimit  int16
	message    string
	exits      [4]string
	grid       []zwdGridLine
	legend     map[byte]zwdLegendEntry
	stats      []zwdStat
}

type zwdGridLine struct {
	line int
	text string
}

type zwdLegendEntry struct {
	line    int
	element byte
	color   byte
	under   *TTile
	toBoard string
}

type zwdStat struct {
	line     int
	x, y     int16
	element  byte
	cycle    int16
	p1, p2   byte
	p3       byte
	p3Board  string
	stepX    int16
	stepY    int16
	under    TTile
	follower int16
	leader   int16
	dataPos  int16
	data     string
	bind     int16
	hasBind  bool
}

func newZWDParser(src string) *zwdParser {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\r", "\n")
	return &zwdParser{lines: strings.Split(src, "\n")}
}

func (p *zwdParser) parse() (zwdDocument, error) {
	var doc zwdDocument
	if err := p.expectDirective("zwd", "1"); err != nil {
		return doc, err
	}
	name, err := p.expectQuotedDirective("world")
	if err != nil {
		return doc, err
	}
	doc.worldName = name

	for {
		line, text, ok := p.nextContentLine()
		if !ok {
			break
		}
		toks, err := tokenizeZWD(text, line)
		if err != nil {
			return doc, err
		}
		if len(toks) == 0 {
			continue
		}
		if toks[0] != "board" {
			return doc, zerr(line, 1, "expected board section")
		}
		if len(toks) != 2 || !isQuotedToken(toks[1]) {
			return doc, zerr(line, 1, "board requires one quoted name")
		}
		board, err := p.parseBoard(unquoteToken(toks[1]), line)
		if err != nil {
			return doc, err
		}
		doc.boards = append(doc.boards, board)
	}
	if len(doc.boards) == 0 {
		return doc, zerr(0, 0, "world must contain at least one board")
	}
	return doc, nil
}

func (p *zwdParser) expectDirective(name, value string) error {
	line, text, ok := p.nextContentLine()
	if !ok {
		return zerr(1, 1, "expected "+name+" "+value)
	}
	toks, err := tokenizeZWD(text, line)
	if err != nil {
		return err
	}
	if len(toks) != 2 || toks[0] != name || toks[1] != value {
		return zerr(line, 1, "expected "+name+" "+value)
	}
	return nil
}

func (p *zwdParser) expectQuotedDirective(name string) (string, error) {
	line, text, ok := p.nextContentLine()
	if !ok {
		return "", zerr(1, 1, "expected "+name+" \"...\"")
	}
	toks, err := tokenizeZWD(text, line)
	if err != nil {
		return "", err
	}
	if len(toks) != 2 || toks[0] != name || !isQuotedToken(toks[1]) {
		return "", zerr(line, 1, "expected "+name+" \"...\"")
	}
	return unquoteToken(toks[1]), nil
}

func (p *zwdParser) parseBoard(name string, line int) (zwdBoard, error) {
	board := zwdBoard{
		name:     name,
		line:     line,
		maxShots: 255,
		legend:   make(map[byte]zwdLegendEntry),
	}
	for {
		line, text, ok := p.nextContentLine()
		if !ok {
			return board, zerr(line, 1, "board "+name+" missing end")
		}
		toks, err := tokenizeZWD(text, line)
		if err != nil {
			return board, err
		}
		if len(toks) == 0 {
			continue
		}
		switch toks[0] {
		case "end":
			if len(toks) != 1 {
				return board, zerr(line, 1, "end takes no arguments")
			}
			if len(board.grid) == 0 {
				return board, zerr(line, 1, "board "+name+" missing grid")
			}
			return board, nil
		case "start":
			if len(toks) != 4 || toks[1] != "player" || toks[2] != "at" {
				return board, zerr(line, 1, "expected start player at X,Y")
			}
			x, y, err := parseCoordToken(toks[3])
			if err != nil {
				return board, zerr(line, 18, "start coordinate must be X,Y within 1..60 and 1..25")
			}
			board.startX, board.startY = x, y
		case "respawn":
			if len(toks) != 3 || toks[1] != "at" {
				return board, zerr(line, 1, "expected respawn at X,Y")
			}
			x, y, err := parseCoordToken(toks[2])
			if err != nil {
				return board, zerr(line, 12, "respawn coordinate must be X,Y within 1..60 and 1..25")
			}
			board.respawnX, board.respawnY = x, y
			board.hasRespawn = true
		case "max-shots":
			n, err := parseIntField(toks, line, 0, 255)
			if err != nil {
				return board, err
			}
			board.maxShots = byte(n)
		case "dark":
			v, err := parseBoolField(toks, line)
			if err != nil {
				return board, err
			}
			board.dark = v
		case "reenter":
			v, err := parseBoolField(toks, line)
			if err != nil {
				return board, err
			}
			board.reenter = v
		case "time-limit":
			n, err := parseIntField(toks, line, 0, 32767)
			if err != nil {
				return board, err
			}
			board.timeLimit = int16(n)
		case "message":
			if len(toks) != 2 || !isQuotedToken(toks[1]) {
				return board, zerr(line, 1, "message requires one quoted string")
			}
			board.message = unquoteToken(toks[1])
		case "exits":
			if err := parseExits(toks, line, &board); err != nil {
				return board, err
			}
		case "grid":
			grid, err := p.parseGrid()
			if err != nil {
				return board, err
			}
			board.grid = grid
		case "legend":
			legend, err := p.parseLegend()
			if err != nil {
				return board, err
			}
			board.legend = legend
		case "stats":
			stats, err := p.parseStats()
			if err != nil {
				return board, err
			}
			board.stats = stats
		default:
			return board, zerr(line, 1, "unknown board field "+toks[0])
		}
	}
}

func (p *zwdParser) parseGrid() ([]zwdGridLine, error) {
	var grid []zwdGridLine
	for p.pos < len(p.lines) {
		line := p.pos + 1
		raw := strings.TrimSuffix(p.lines[p.pos], "\n")
		p.pos++
		if strings.TrimSpace(raw) == "end" {
			if len(grid) != BOARD_HEIGHT {
				return nil, zerr(line, 1, fmt.Sprintf("grid has %d rows; expected 25", len(grid)))
			}
			return grid, nil
		}
		if len(raw) != BOARD_WIDTH && strings.HasPrefix(raw, "  ") && len(raw[2:]) == BOARD_WIDTH {
			raw = raw[2:]
		}
		if len(raw) != BOARD_WIDTH {
			if len(raw) > BOARD_WIDTH {
				return nil, zerr(line, BOARD_WIDTH+1, "grid row wider than 60")
			}
			return nil, zerr(line, len(raw)+1, "grid row shorter than 60")
		}
		grid = append(grid, zwdGridLine{line: line, text: raw})
	}
	return nil, zerr(0, 0, "grid missing end")
}

func (p *zwdParser) parseLegend() (map[byte]zwdLegendEntry, error) {
	legend := make(map[byte]zwdLegendEntry)
	for {
		if p.pos >= len(p.lines) {
			return nil, zerr(0, 0, "legend missing end")
		}
		line := p.pos + 1
		text := strings.TrimSpace(p.lines[p.pos])
		p.pos++
		if text == "" {
			continue
		}
		toks, err := tokenizeZWD(text, line)
		if err != nil {
			return nil, err
		}
		if len(toks) == 1 && toks[0] == "end" {
			return legend, nil
		}
		entryKey, entry, err := parseLegendEntry(toks, line)
		if err != nil {
			return nil, err
		}
		if _, exists := legend[entryKey]; exists {
			return nil, zerr(line, 1, "duplicate legend key")
		}
		entry.line = line
		legend[entryKey] = entry
	}
}

func (p *zwdParser) parseStats() ([]zwdStat, error) {
	var stats []zwdStat
	for {
		line, text, ok := p.nextContentLine()
		if !ok {
			return nil, zerr(0, 0, "stats missing end")
		}
		toks, err := tokenizeZWD(text, line)
		if err != nil {
			return nil, err
		}
		if len(toks) == 1 && toks[0] == "end" {
			return stats, nil
		}
		if len(toks) == 0 {
			continue
		}
		if toks[0] != "stat" {
			return nil, zerr(line, 1, "expected stat or end")
		}
		stat, err := parseStatLine(toks, line)
		if err != nil {
			return nil, err
		}
		nextLine, nextText, ok := p.peekContentLine()
		if ok && strings.TrimSpace(nextText) == "oop" {
			p.pos = nextLine
			data, err := p.parseOOP()
			if err != nil {
				return nil, err
			}
			stat.data = data
		}
		stats = append(stats, stat)
	}
}

func (p *zwdParser) parseOOP() (string, error) {
	var lines []string
	for p.pos < len(p.lines) {
		line := p.pos + 1
		raw := p.lines[p.pos]
		p.pos++
		if strings.TrimSpace(raw) == "end" {
			return strings.Join(lines, string([]byte{KEY_ENTER})), nil
		}
		lines = append(lines, raw)
		_ = line
	}
	return "", zerr(0, 0, "oop block missing end")
}

func (p *zwdParser) nextContentLine() (int, string, bool) {
	for p.pos < len(p.lines) {
		line := p.pos + 1
		text := strings.TrimSpace(p.lines[p.pos])
		p.pos++
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		return line, text, true
	}
	return 0, "", false
}

func (p *zwdParser) peekContentLine() (int, string, bool) {
	old := p.pos
	line, text, ok := p.nextContentLine()
	p.pos = old
	if !ok {
		return 0, "", false
	}
	return line, text, true
}

func parseLegendEntry(toks []string, line int) (byte, zwdLegendEntry, error) {
	var entry zwdLegendEntry
	if len(toks) < 5 || toks[1] != "=" {
		return 0, entry, zerr(line, 1, "legend entry must be KEY = Element color C")
	}
	key, err := parseByteToken(toks[0])
	if err != nil {
		return 0, entry, zerr(line, 1, "legend key must be one byte or cp437:0xNN")
	}
	elem, next, err := parseElementName(toks, 2)
	if err != nil {
		return 0, entry, zerr(line, 5, err.Error())
	}
	entry.element = elem
	i := next
	for i < len(toks) {
		switch toks[i] {
		case "color":
			if i+1 >= len(toks) {
				return 0, entry, zerr(line, 1, "color requires a value")
			}
			color, err := parseColor(toks[i+1])
			if err != nil {
				return 0, entry, zerr(line, 1, err.Error())
			}
			entry.color = color
			i += 2
		case "under":
			tile, next, err := parseTile(toks, i+1, line)
			if err != nil {
				return 0, entry, err
			}
			entry.under = &tile
			i = next
		case "to":
			if i+1 >= len(toks) || !isQuotedToken(toks[i+1]) {
				return 0, entry, zerr(line, 1, "to requires a quoted board name")
			}
			entry.toBoard = unquoteToken(toks[i+1])
			i += 2
		default:
			return 0, entry, zerr(line, 1, "unknown legend field "+toks[i])
		}
	}
	return key, entry, nil
}

func parseStatLine(toks []string, line int) (zwdStat, error) {
	stat := zwdStat{
		line:     line,
		cycle:    -1,
		p1:       4,
		p2:       4,
		p3:       0,
		stepY:    -1,
		under:    TTile{Element: E_EMPTY, Color: 0x00},
		follower: -1,
		leader:   -1,
		bind:     -1,
	}
	if len(toks) < 6 || toks[1] != "at" {
		return stat, zerr(line, 1, "stat requires at X,Y element NAME")
	}
	x, y, err := parseCoordToken(toks[2])
	if err != nil {
		return stat, zerr(line, 9, "stat coordinate must be X,Y within 1..60 and 1..25")
	}
	stat.x, stat.y = x, y
	if toks[3] != "element" {
		return stat, zerr(line, 1, "stat requires element NAME")
	}
	elem, next, err := parseElementName(toks, 4)
	if err != nil {
		return stat, zerr(line, 1, err.Error())
	}
	stat.element = elem
	if elem == E_OBJECT {
		stat.p1 = 1
	} else if elem == E_BEAR {
		stat.p1 = 8
	}
	i := next
	for i < len(toks) {
		switch toks[i] {
		case "cycle":
			n, err := parseOneInt(toks, i+1, line, 0, 32767, "cycle")
			if err != nil {
				return stat, err
			}
			stat.cycle = int16(n)
			i += 2
		case "p1", "p2", "p3":
			if i+1 >= len(toks) {
				return stat, zerr(line, 1, toks[i]+" requires a value")
			}
			if toks[i] == "p3" && i+2 < len(toks) && toks[i+1] == "board" && isQuotedToken(toks[i+2]) {
				stat.p3Board = unquoteToken(toks[i+2])
				i += 3
				continue
			}
			n, err := parseByteValue(toks[i+1])
			if err != nil {
				return stat, zerr(line, 1, toks[i]+" must be 0..255 or cp437:0xNN")
			}
			switch toks[i] {
			case "p1":
				stat.p1 = n
			case "p2":
				stat.p2 = n
			case "p3":
				stat.p3 = n
			}
			i += 2
		case "step":
			if i+1 >= len(toks) {
				return stat, zerr(line, 1, "step requires a direction")
			}
			dx, dy, err := parseStep(toks[i+1])
			if err != nil {
				return stat, zerr(line, 1, err.Error())
			}
			stat.stepX, stat.stepY = dx, dy
			i += 2
		case "under":
			tile, next, err := parseTile(toks, i+1, line)
			if err != nil {
				return stat, err
			}
			stat.under = tile
			i = next
		case "follower":
			n, err := parseOneInt(toks, i+1, line, -1, MAX_STAT, "follower")
			if err != nil {
				return stat, err
			}
			stat.follower = int16(n)
			i += 2
		case "leader":
			n, err := parseOneInt(toks, i+1, line, -1, MAX_STAT, "leader")
			if err != nil {
				return stat, err
			}
			stat.leader = int16(n)
			i += 2
		case "data-pos":
			n, err := parseOneInt(toks, i+1, line, -32768, 32767, "data-pos")
			if err != nil {
				return stat, err
			}
			stat.dataPos = int16(n)
			i += 2
		case "bind":
			n, err := parseOneInt(toks, i+1, line, 1, MAX_STAT, "bind")
			if err != nil {
				return stat, err
			}
			stat.bind = int16(n)
			stat.hasBind = true
			i += 2
		default:
			return stat, zerr(line, 1, "unknown stat field "+toks[i])
		}
	}
	if stat.cycle < 0 {
		if ElementDefs[stat.element].Cycle < 0 {
			return stat, zerr(line, 1, "cycle required for element "+ElementDefs[stat.element].Name)
		}
		stat.cycle = ElementDefs[stat.element].Cycle
	}
	return stat, nil
}

func compileZWDDocument(doc zwdDocument) (TWorld, error) {
	if len(doc.worldName) == 0 || len(doc.worldName) > 20 {
		return TWorld{}, zerr(2, 1, "world name must be 1..20 bytes")
	}
	if len(doc.boards) > MAX_BOARD+1 {
		return TWorld{}, zerr(0, 0, "world has more than 101 boards")
	}
	boardIDs := make(map[string]int16)
	for i, b := range doc.boards {
		if _, exists := boardIDs[b.name]; !exists {
			boardIDs[b.name] = int16(i)
		}
	}

	e := NewEngine()
	e.Headless = true
	e.VideoInstall()
	e.WorldCreate()
	e.World.Info.Name = doc.worldName
	e.World.Info.IsSave = false
	e.World.Info.CurrentBoard = 0
	e.World.BoardCount = int16(len(doc.boards) - 1)

	for boardID, srcBoard := range doc.boards {
		if err := compileZWDBoard(e, int16(boardID), srcBoard, boardIDs); err != nil {
			return TWorld{}, err
		}
	}
	e.World.Info.CurrentBoard = 0
	e.BoardOpen(0)
	return e.World, nil
}

func compileZWDBoard(e *Engine, boardID int16, src zwdBoard, boardIDs map[string]int16) error {
	if len(src.name) > 50 {
		return zerr(src.line, 1, "board name must be 50 bytes or fewer")
	}
	if src.startX < 1 || src.startY < 1 {
		return zerr(src.line, 1, "board requires start player at X,Y")
	}
	if len(src.grid) != BOARD_HEIGHT {
		return zerr(src.line, 1, "board grid must have 25 rows")
	}

	e.BoardCreate()
	e.World.Info.CurrentBoard = boardID
	e.Board.Name = src.name
	e.Board.Info.StartPlayerX = byte(src.startX)
	e.Board.Info.StartPlayerY = byte(src.startY)
	// respawn at overrides the default (which equals start player at).
	if src.hasRespawn {
		e.Board.Info.StartPlayerX = byte(src.respawnX)
		e.Board.Info.StartPlayerY = byte(src.respawnY)
	}
	e.Board.Info.MaxShots = src.maxShots
	e.Board.Info.IsDark = src.dark
	e.Board.Info.ReenterWhenZapped = src.reenter
	e.Board.Info.TimeLimitSec = src.timeLimit
	e.Board.Info.Message = src.message
	for i, name := range src.exits {
		if name == "" {
			e.Board.Info.NeighborBoards[i] = 0
			continue
		}
		id, ok := boardIDs[name]
		if !ok {
			return zerr(src.line, 1, "exit references unknown board "+name)
		}
		if id == 0 {
			return zerr(src.line, 1, "board edge exits cannot reference board 0")
		}
		e.Board.Info.NeighborBoards[i] = byte(id)
	}

	playerCount := 0
	for y, row := range src.grid {
		for x := 0; x < BOARD_WIDTH; x++ {
			ch := row.text[x]
			entry, ok := src.legend[ch]
			if !ok {
				return zerr(row.line, x+1, fmt.Sprintf("grid uses legend key %q with no legend entry", string([]byte{ch})))
			}
			e.Board.Tiles[x+1][y+1] = TTile{Element: entry.element, Color: entry.color}
			if entry.element == E_PLAYER {
				playerCount++
				if int16(x+1) != src.startX || int16(y+1) != src.startY {
					return zerr(row.line, x+1, "player tile must be at start player coordinate")
				}
				e.Board.Stats[0] = TStat{
					X:        byte(x + 1),
					Y:        byte(y + 1),
					Cycle:    1,
					Follower: -1,
					Leader:   -1,
					Under:    TTile{Element: E_EMPTY, Color: 0x00},
				}
				if entry.under != nil {
					e.Board.Stats[0].Under = *entry.under
				}
			}
		}
	}
	if playerCount != 1 {
		return zerr(src.line, 1, fmt.Sprintf("board must contain exactly one player tile, found %d", playerCount))
	}

	e.Board.StatCount = 0
	for _, srcStat := range src.stats {
		if e.Board.StatCount >= MAX_STAT {
			return zerr(srcStat.line, 1, "board has more than 150 non-player stats")
		}
		if e.Board.Tiles[srcStat.x][srcStat.y].Element != srcStat.element {
			return zerr(srcStat.line, 1, "stat element must match grid tile at its coordinate")
		}
		stat := TStat{
			X:        byte(srcStat.x),
			Y:        byte(srcStat.y),
			StepX:    srcStat.stepX,
			StepY:    srcStat.stepY,
			Cycle:    srcStat.cycle,
			P1:       srcStat.p1,
			P2:       srcStat.p2,
			P3:       srcStat.p3,
			Follower: srcStat.follower,
			Leader:   srcStat.leader,
			Under:    srcStat.under,
			DataPos:  srcStat.dataPos,
		}
		if srcStat.p3Board != "" {
			id, ok := boardIDs[srcStat.p3Board]
			if !ok {
				return zerr(srcStat.line, 1, "stat references unknown board "+srcStat.p3Board)
			}
			stat.P3 = byte(id)
		}
		if srcStat.data != "" {
			if len(srcStat.data) > 32767 {
				return zerr(srcStat.line, 1, "oop block exceeds 32767 bytes")
			}
			stat.Data = srcStat.data
			stat.DataLen = int16(len(srcStat.data))
		}
		if srcStat.hasBind {
			if srcStat.data != "" {
				return zerr(srcStat.line, 1, "bind cannot be used with an oop block")
			}
			if srcStat.bind > e.Board.StatCount {
				return zerr(srcStat.line, 1, "bind target must be a previous stat")
			}
			stat.DataLen = -srcStat.bind
		}
		e.Board.StatCount++
		e.Board.Stats[e.Board.StatCount] = stat
	}

	if size := estimateZWDSerializedBoardSize(e); size > len(e.IoTmpBuf) {
		return zerr(src.line, 1, fmt.Sprintf("serialized board is about %d bytes; maximum is 20000", size))
	}
	e.BoardClose()
	if int(e.World.BoardLen[boardID]) > len(e.IoTmpBuf) {
		return zerr(src.line, 1, "serialized board exceeds 20000 bytes")
	}
	return nil
}

func estimateZWDSerializedBoardSize(e *Engine) int {
	size := SizeOfBoardName + SizeOfBoardInfo + 2
	rleCount := 0
	var last TTile
	run := 0
	for y := int16(1); y <= BOARD_HEIGHT; y++ {
		for x := int16(1); x <= BOARD_WIDTH; x++ {
			tile := e.Board.Tiles[x][y]
			if run > 0 && tile == last && run < 255 {
				run++
				continue
			}
			if run > 0 {
				rleCount++
			}
			last = tile
			run = 1
		}
	}
	if run > 0 {
		rleCount++
	}
	size += rleCount * SizeOfRleTile
	for i := int16(0); i <= e.Board.StatCount; i++ {
		stat := &e.Board.Stats[i]
		size += SizeOfStat
		if stat.DataLen > 0 {
			size += int(stat.DataLen)
		}
	}
	return size
}

func tokenizeZWD(text string, line int) ([]string, error) {
	var toks []string
	for i := 0; i < len(text); {
		for i < len(text) && (text[i] == ' ' || text[i] == '\t') {
			i++
		}
		if i >= len(text) {
			break
		}
		if text[i] == '"' {
			start := i
			i++
			var b strings.Builder
			for i < len(text) && text[i] != '"' {
				if text[i] == '\\' && i+1 < len(text) {
					i++
					switch text[i] {
					case '"', '\\':
						b.WriteByte(text[i])
					case 'n':
						b.WriteByte('\n')
					default:
						return nil, zerr(line, i+1, "unsupported escape")
					}
					i++
					continue
				}
				b.WriteByte(text[i])
				i++
			}
			if i >= len(text) {
				return nil, zerr(line, start+1, "unterminated quoted string")
			}
			i++
			toks = append(toks, "\""+b.String()+"\"")
			continue
		}
		start := i
		for i < len(text) && text[i] != ' ' && text[i] != '\t' {
			i++
		}
		toks = append(toks, text[start:i])
	}
	return toks, nil
}

func parseElementName(toks []string, start int) (byte, int, error) {
	var parts []string
	for i := start; i < len(toks); i++ {
		if isZWDKeyword(toks[i]) {
			break
		}
		parts = append(parts, toks[i])
		name := strings.Join(parts, " ")
		if elem, ok := elementByZWDName(name); ok {
			return elem, i + 1, nil
		}
	}
	return 0, start, fmt.Errorf("unknown element name")
}

// textZWDNames maps normalized ZWD name → element id for Text-Blue..Text-White.
// Text elements have no Name in ElementDefs, so they need special handling.
var textZWDNames = map[string]byte{
	"TEXTBLUE":   E_TEXT_BLUE,
	"TEXTGREEN":  E_TEXT_GREEN,
	"TEXTCYAN":   E_TEXT_CYAN,
	"TEXTRED":    E_TEXT_RED,
	"TEXTPURPLE": E_TEXT_PURPLE,
	"TEXTYELLOW": E_TEXT_YELLOW,
	"TEXTWHITE":  E_TEXT_WHITE,
}

func elementByZWDName(name string) (byte, bool) {
	if strings.HasPrefix(name, "element ") {
		n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(name, "element ")))
		if err == nil && n >= 0 && n <= MAX_ELEMENT {
			// element N form is valid for any defined element OR for text elements.
			if ElementDefs[n].Name != "" || (n >= E_TEXT_MIN && n <= E_TEXT_WHITE) {
				return byte(n), true
			}
		}
	}
	normalized := normalizeZWDName(name)
	// Check text elements first (they have no Name in ElementDefs).
	if elem, ok := textZWDNames[normalized]; ok {
		return elem, true
	}
	for i := 0; i <= MAX_ELEMENT; i++ {
		if ElementDefs[i].Name == "" {
			continue
		}
		if normalizeZWDName(ElementDefs[i].Name) == normalized {
			return byte(i), true
		}
	}
	return 0, false
}

func normalizeZWDName(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '-' || c == '_' {
			continue
		}
		if c >= 'a' && c <= 'z' {
			c = c - ('a' - 'A')
		}
		b.WriteByte(c)
	}
	return b.String()
}

func parseTile(toks []string, start int, line int) (TTile, int, error) {
	elem, next, err := parseElementName(toks, start)
	if err != nil {
		return TTile{}, start, zerr(line, 1, "unknown under element")
	}
	if next >= len(toks) || toks[next] != "color" || next+1 >= len(toks) {
		return TTile{}, start, zerr(line, 1, "tile requires color C")
	}
	color, err := parseColor(toks[next+1])
	if err != nil {
		return TTile{}, start, zerr(line, 1, err.Error())
	}
	return TTile{Element: elem, Color: color}, next + 2, nil
}

func parseExits(toks []string, line int, board *zwdBoard) error {
	if len(toks) != 9 {
		return zerr(line, 1, "expected exits north X south X west X east X")
	}
	for i := 1; i < len(toks); i += 2 {
		dir := toks[i]
		name := toks[i+1]
		var idx int
		switch dir {
		case "north":
			idx = 0
		case "south":
			idx = 1
		case "west":
			idx = 2
		case "east":
			idx = 3
		default:
			return zerr(line, 1, "unknown exit direction "+dir)
		}
		if name == "none" {
			board.exits[idx] = ""
		} else if isQuotedToken(name) {
			board.exits[idx] = unquoteToken(name)
		} else {
			return zerr(line, 1, "exit target must be none or quoted board name")
		}
	}
	return nil
}

func parseBoolField(toks []string, line int) (bool, error) {
	if len(toks) != 2 {
		return false, zerr(line, 1, toks[0]+" requires true or false")
	}
	switch toks[1] {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, zerr(line, 1, toks[0]+" requires true or false")
	}
}

func parseIntField(toks []string, line int, min, max int) (int, error) {
	if len(toks) != 2 {
		return 0, zerr(line, 1, toks[0]+" requires one numeric value")
	}
	return parseOneInt(toks, 1, line, min, max, toks[0])
}

func parseOneInt(toks []string, idx int, line int, min, max int, name string) (int, error) {
	if idx >= len(toks) {
		return 0, zerr(line, 1, name+" requires a value")
	}
	n, err := strconv.Atoi(toks[idx])
	if err != nil || n < min || n > max {
		return 0, zerr(line, 1, fmt.Sprintf("%s must be %d..%d", name, min, max))
	}
	return n, nil
}

func parseCoordToken(tok string) (int16, int16, error) {
	parts := strings.Split(tok, ",")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("bad coordinate")
	}
	x, err1 := strconv.Atoi(parts[0])
	y, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || x < 1 || x > BOARD_WIDTH || y < 1 || y > BOARD_HEIGHT {
		return 0, 0, fmt.Errorf("bad coordinate")
	}
	return int16(x), int16(y), nil
}

func parseColor(tok string) (byte, error) {
	switch tok {
	case "black":
		return 0, nil
	case "blue":
		return 1, nil
	case "green":
		return 2, nil
	case "cyan":
		return 3, nil
	case "red":
		return 4, nil
	case "purple":
		return 5, nil
	case "brown":
		return 6, nil
	case "white":
		return 7, nil
	case "gray":
		return 8, nil
	case "bright-blue":
		return 9, nil
	case "bright-green":
		return 10, nil
	case "bright-cyan":
		return 11, nil
	case "bright-red":
		return 12, nil
	case "bright-purple":
		return 13, nil
	case "yellow":
		return 14, nil
	case "bright-white":
		return 15, nil
	}
	n, err := strconv.ParseUint(strings.TrimPrefix(tok, "0x"), 16, 8)
	if err == nil && (strings.HasPrefix(tok, "0x") || strings.HasPrefix(tok, "0X")) {
		return byte(n), nil
	}
	n, err = strconv.ParseUint(tok, 10, 8)
	if err == nil {
		return byte(n), nil
	}
	return 0, fmt.Errorf("color must be 0x00..0xFF or a DOS color name")
}

func parseByteToken(tok string) (byte, error) {
	if strings.HasPrefix(tok, "cp437:0x") || strings.HasPrefix(tok, "cp437:0X") {
		n, err := strconv.ParseUint(tok[len("cp437:0x"):], 16, 8)
		return byte(n), err
	}
	if len(tok) == 1 {
		return tok[0], nil
	}
	return 0, fmt.Errorf("not a byte")
}

func parseByteValue(tok string) (byte, error) {
	if b, err := parseByteToken(tok); err == nil && strings.HasPrefix(tok, "cp437:") {
		return b, nil
	}
	n, err := strconv.Atoi(tok)
	if err == nil && n >= 0 && n <= 255 {
		return byte(n), nil
	}
	return 0, fmt.Errorf("not a byte")
}

func parseStep(tok string) (int16, int16, error) {
	switch tok {
	case "idle":
		return 0, 0, nil
	case "north":
		return 0, -1, nil
	case "south":
		return 0, 1, nil
	case "west":
		return -1, 0, nil
	case "east":
		return 1, 0, nil
	}
	parts := strings.Split(tok, ",")
	if len(parts) == 2 {
		x, err1 := strconv.Atoi(parts[0])
		y, err2 := strconv.Atoi(parts[1])
		if err1 == nil && err2 == nil && x >= -32768 && x <= 32767 && y >= -32768 && y <= 32767 {
			return int16(x), int16(y), nil
		}
	}
	return 0, 0, fmt.Errorf("step must be idle, north, south, west, east, or dx,dy")
}

func isZWDKeyword(s string) bool {
	switch s {
	case "cycle", "p1", "p2", "p3", "step", "under", "follower", "leader", "data-pos", "bind", "color", "to":
		return true
	}
	return false
}

func isQuotedToken(s string) bool {
	return len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"'
}

func unquoteToken(s string) string {
	if isQuotedToken(s) {
		return s[1 : len(s)-1]
	}
	return s
}

func zerr(line, col int, msg string) error {
	return &zwdError{line: line, col: col, msg: msg}
}
