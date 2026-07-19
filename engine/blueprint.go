package zztgo

// This file is the deterministic half of API world painting. The model emits
// semantic drawing operations and actors as JSON; this renderer owns the
// byte-exact 60x25 grid, legend, stats, and ZWD serialization.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// BoardBlueprint is the versioned, API-facing representation of one board.
// Coordinates are the native ZZT 1-based coordinates: x=1..60, y=1..25.
type BoardBlueprint struct {
	Version    int                  `json:"version"`
	Board      string               `json:"board"`
	Start      BlueprintPoint       `json:"start"`
	Dark       bool                 `json:"dark"`
	MaxShots   *int                 `json:"max_shots,omitempty"`
	Reenter    bool                 `json:"reenter,omitempty"`
	TimeLimit  int                  `json:"time_limit,omitempty"`
	Message    string               `json:"message,omitempty"`
	Exits      BlueprintExits       `json:"exits"`
	Ports      BlueprintPorts       `json:"ports,omitempty"`
	Background BlueprintTile        `json:"background"`
	Floor      BlueprintTile        `json:"floor"`
	Operations []BlueprintOperation `json:"operations"`
	Actors     []BlueprintActor     `json:"actors,omitempty"`
}

type BlueprintPoint struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type BlueprintTile struct {
	Element string `json:"element"`
	Color   string `json:"color"`
}

type BlueprintExits struct {
	North string `json:"north,omitempty"`
	South string `json:"south,omitempty"`
	West  string `json:"west,omitempty"`
	East  string `json:"east,omitempty"`
}

// A north/south port is an x coordinate; a west/east port is a y coordinate.
// Pointers distinguish an omitted port from an invalid zero coordinate.
type BlueprintPorts struct {
	North *int `json:"north,omitempty"`
	South *int `json:"south,omitempty"`
	West  *int `json:"west,omitempty"`
	East  *int `json:"east,omitempty"`
}

// BlueprintOperation is deliberately small. Sequential operations compose a
// board without requiring the model to enumerate cells. Supported kinds are
// fill, border, line, path, tile, and text.
type BlueprintOperation struct {
	Kind  string         `json:"kind"`
	X     int            `json:"x"`
	Y     int            `json:"y"`
	X2    int            `json:"x2,omitempty"`
	Y2    int            `json:"y2,omitempty"`
	Width int            `json:"width,omitempty"`
	Bend  string         `json:"bend,omitempty"`
	Tile  *BlueprintTile `json:"tile,omitempty"`
	Text  string         `json:"text,omitempty"`
	Color string         `json:"color,omitempty"`
}

type BlueprintActor struct {
	Element   string         `json:"element"`
	X         int            `json:"x"`
	Y         int            `json:"y"`
	Color     string         `json:"color"`
	Cycle     *int           `json:"cycle,omitempty"`
	P1        *int           `json:"p1,omitempty"`
	P2        *int           `json:"p2,omitempty"`
	P3        *int           `json:"p3,omitempty"`
	Character string         `json:"character,omitempty"`
	Target    string         `json:"target,omitempty"`
	Step      string         `json:"step,omitempty"`
	Under     *BlueprintTile `json:"under,omitempty"`
	OOP       string         `json:"oop,omitempty"`
}

type blueprintCell struct {
	tile    TTile
	toBoard string
	under   TTile // only meaningful for Player legend entries
}

type renderedBlueprintActor struct {
	actor BlueprintActor
	elem  byte
	color byte
	under TTile
}

// ParseBoardBlueprint accepts either one raw JSON object or exactly one
// fenced json block. Unknown fields and trailing JSON are rejected so schema
// drift becomes repair feedback rather than silently changing the board.
func ParseBoardBlueprint(text string) (BoardBlueprint, error) {
	raw := strings.TrimSpace(text)
	if strings.HasPrefix(raw, "```") {
		lines := strings.Split(raw, "\n")
		if len(lines) < 3 || strings.TrimSpace(lines[0]) != "```json" || strings.TrimSpace(lines[len(lines)-1]) != "```" {
			return BoardBlueprint{}, fmt.Errorf("model response must be one raw JSON object or exactly one fenced json block")
		}
		raw = strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var bp BoardBlueprint
	if err := dec.Decode(&bp); err != nil {
		return BoardBlueprint{}, fmt.Errorf("decode board blueprint: %w", err)
	}
	var extra interface{}
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return BoardBlueprint{}, fmt.Errorf("decode board blueprint: trailing JSON value")
		}
		return BoardBlueprint{}, fmt.Errorf("decode board blueprint: %w", err)
	}
	return bp, nil
}

// RenderBoardBlueprint validates and lowers a blueprint to one complete ZWD
// board section. The returned source is parsed again by the normal generation
// pipeline before it can enter a world.
func RenderBoardBlueprint(bp BoardBlueprint, wantName string) (string, error) {
	init := NewEngine()
	init.InitElementsGame()

	if bp.Version != 1 {
		return "", fmt.Errorf("blueprint version must be 1")
	}
	if bp.Board != wantName {
		return "", fmt.Errorf("blueprint board is %q; expected %q", bp.Board, wantName)
	}
	if len(bp.Board) == 0 || len(bp.Board) > 50 {
		return "", fmt.Errorf("board name must be 1..50 bytes")
	}
	if err := blueprintCoord(bp.Start.X, bp.Start.Y, "start"); err != nil {
		return "", err
	}
	if bp.TimeLimit < 0 || bp.TimeLimit > 32767 {
		return "", fmt.Errorf("time_limit must be 0..32767")
	}
	if len(bp.Message) > SizeOfBoardInfoMessage-1 {
		return "", fmt.Errorf("message must be at most %d bytes", SizeOfBoardInfoMessage-1)
	}
	if err := blueprintPrintableASCII(bp.Message, false); err != nil {
		return "", fmt.Errorf("message: %w", err)
	}
	maxShots := 255
	if bp.MaxShots != nil {
		maxShots = *bp.MaxShots
	}
	if maxShots < 0 || maxShots > 255 {
		return "", fmt.Errorf("max_shots must be 0..255")
	}
	background, err := blueprintResolveTile(bp.Background, false)
	if err != nil {
		return "", fmt.Errorf("background: %w", err)
	}
	floor, err := blueprintResolveTile(bp.Floor, false)
	if err != nil {
		return "", fmt.Errorf("floor: %w", err)
	}
	if !blueprintOptimisticallyWalkable(floor.Element) {
		return "", fmt.Errorf("floor element %s is not traversable", zwdElementName(floor.Element))
	}

	var cells [BOARD_WIDTH][BOARD_HEIGHT]blueprintCell
	for x := range cells {
		for y := range cells[x] {
			cells[x][y].tile = background
		}
	}
	for i, op := range bp.Operations {
		if err := blueprintApplyOperation(&cells, op); err != nil {
			return "", fmt.Errorf("operations[%d]: %w", i, err)
		}
	}
	if err := blueprintCarvePorts(&cells, bp.Exits, bp.Ports, floor); err != nil {
		return "", err
	}

	actors := make([]renderedBlueprintActor, 0, len(bp.Actors))
	occupied := map[[2]int]bool{{bp.Start.X, bp.Start.Y}: true}
	for i, actor := range bp.Actors {
		if err := blueprintCoord(actor.X, actor.Y, "actor"); err != nil {
			return "", fmt.Errorf("actors[%d]: %w", i, err)
		}
		rendered, err := blueprintResolveActor(actor, cells[actor.X-1][actor.Y-1].tile)
		if err != nil {
			return "", fmt.Errorf("actors[%d]: %w", i, err)
		}
		pos := [2]int{actor.X, actor.Y}
		if occupied[pos] {
			return "", fmt.Errorf("actors[%d]: coordinate (%d,%d) is already occupied by player or actor", i, actor.X, actor.Y)
		}
		occupied[pos] = true
		actors = append(actors, rendered)
		cells[actor.X-1][actor.Y-1] = blueprintCell{tile: TTile{Element: rendered.elem, Color: rendered.color}, toBoard: actor.Target}
	}
	if len(actors) > MAX_STAT {
		return "", fmt.Errorf("blueprint has %d actors; limit is %d", len(actors), MAX_STAT)
	}
	playerUnder := cells[bp.Start.X-1][bp.Start.Y-1].tile
	cells[bp.Start.X-1][bp.Start.Y-1] = blueprintCell{tile: TTile{Element: E_PLAYER, Color: 0x1F}, under: playerUnder}

	section, err := blueprintWriteZWD(bp, maxShots, cells, actors)
	if err != nil {
		return "", err
	}
	// Parse the exact source here as an immediate invariant check. Unknown
	// cross-board names are resolved later when all sections are assembled.
	if _, err := newZWDParser("zwd 1\nworld \"CHECK\"\n" + section).parse(); err != nil {
		return "", fmt.Errorf("internal blueprint lowering produced invalid ZWD: %w", err)
	}
	return section, nil
}

func blueprintCoord(x, y int, label string) error {
	if x < 1 || x > BOARD_WIDTH || y < 1 || y > BOARD_HEIGHT {
		return fmt.Errorf("%s coordinate must be within x=1..60, y=1..25; got (%d,%d)", label, x, y)
	}
	return nil
}

func blueprintResolveTile(spec BlueprintTile, allowStat bool) (TTile, error) {
	if spec.Element == "" || spec.Color == "" {
		return TTile{}, fmt.Errorf("tile requires element and color")
	}
	elem, ok := elementByZWDName(spec.Element)
	if !ok {
		return TTile{}, fmt.Errorf("unknown ZZT element %q", spec.Element)
	}
	if elem == E_PLAYER || (!allowStat && elementNeedsStat(elem)) {
		return TTile{}, fmt.Errorf("element %s must be placed as an actor, not a drawing tile", zwdElementName(elem))
	}
	if elem >= E_TEXT_MIN && elem <= E_TEXT_WHITE {
		return TTile{}, fmt.Errorf("text elements must be placed with a text operation")
	}
	color, err := parseLegendColor(elem, strings.ToLower(spec.Color))
	if err != nil {
		return TTile{}, err
	}
	return TTile{Element: elem, Color: color}, nil
}

func blueprintOptimisticallyWalkable(elem byte) bool {
	return ElementDefs[elem].Walkable || elem == E_FAKE || elem == E_FOREST || elem == E_BREAKABLE
}

func blueprintApplyOperation(cells *[BOARD_WIDTH][BOARD_HEIGHT]blueprintCell, op BlueprintOperation) error {
	kind := strings.ToLower(op.Kind)
	if kind == "text" {
		return blueprintApplyText(cells, op)
	}
	if op.Tile == nil {
		return fmt.Errorf("%s operation requires tile", kind)
	}
	tile, err := blueprintResolveTile(*op.Tile, false)
	if err != nil {
		return err
	}
	paint := func(x, y int) error {
		if err := blueprintCoord(x, y, kind); err != nil {
			return err
		}
		cells[x-1][y-1] = blueprintCell{tile: tile}
		return nil
	}
	line := func(x1, y1, x2, y2, width int) error {
		if x1 != x2 && y1 != y2 {
			return fmt.Errorf("line segment must be horizontal or vertical")
		}
		if width == 0 {
			width = 1
		}
		if width < 1 || width > 5 {
			return fmt.Errorf("width must be 1..5")
		}
		if x1 > x2 {
			x1, x2 = x2, x1
		}
		if y1 > y2 {
			y1, y2 = y2, y1
		}
		lo := -(width - 1) / 2
		hi := width / 2
		for y := y1; y <= y2; y++ {
			for x := x1; x <= x2; x++ {
				for d := lo; d <= hi; d++ {
					px, py := x, y
					if x1 == x2 {
						px += d
					} else {
						py += d
					}
					if err := paint(px, py); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	switch kind {
	case "tile":
		return paint(op.X, op.Y)
	case "fill", "border":
		if err := blueprintCoord(op.X, op.Y, kind); err != nil {
			return err
		}
		if err := blueprintCoord(op.X2, op.Y2, kind); err != nil {
			return err
		}
		if op.X > op.X2 || op.Y > op.Y2 {
			return fmt.Errorf("rectangle coordinates must be top-left then bottom-right")
		}
		for y := op.Y; y <= op.Y2; y++ {
			for x := op.X; x <= op.X2; x++ {
				if kind == "fill" || x == op.X || x == op.X2 || y == op.Y || y == op.Y2 {
					_ = paint(x, y)
				}
			}
		}
		return nil
	case "line":
		return line(op.X, op.Y, op.X2, op.Y2, op.Width)
	case "path":
		if err := blueprintCoord(op.X, op.Y, kind); err != nil {
			return err
		}
		if err := blueprintCoord(op.X2, op.Y2, kind); err != nil {
			return err
		}
		switch op.Bend {
		case "", "horizontal-first":
			if err := line(op.X, op.Y, op.X2, op.Y, op.Width); err != nil {
				return err
			}
			return line(op.X2, op.Y, op.X2, op.Y2, op.Width)
		case "vertical-first":
			if err := line(op.X, op.Y, op.X, op.Y2, op.Width); err != nil {
				return err
			}
			return line(op.X, op.Y2, op.X2, op.Y2, op.Width)
		default:
			return fmt.Errorf("path bend must be horizontal-first or vertical-first")
		}
	default:
		return fmt.Errorf("unknown operation kind %q", op.Kind)
	}
}

func blueprintApplyText(cells *[BOARD_WIDTH][BOARD_HEIGHT]blueprintCell, op BlueprintOperation) error {
	if op.Text == "" {
		return fmt.Errorf("text operation requires non-empty text")
	}
	elemName := op.Color
	if elemName == "" {
		elemName = "Text-White"
	}
	elem, ok := elementByZWDName(elemName)
	if !ok || elem < E_TEXT_MIN || elem > E_TEXT_WHITE {
		return fmt.Errorf("text color must be Text-Blue, Text-Green, Text-Cyan, Text-Red, Text-Purple, Text-Yellow, or Text-White")
	}
	if op.Y < 1 || op.Y > BOARD_HEIGHT || op.X < 1 || op.X+len(op.Text)-1 > BOARD_WIDTH {
		return fmt.Errorf("text must fit on one board row")
	}
	for i := 0; i < len(op.Text); i++ {
		glyph := op.Text[i]
		if glyph < 0x20 || glyph > 0x7E {
			return fmt.Errorf("text must use printable ASCII; byte %d is 0x%02X", i, glyph)
		}
		if glyph == ' ' {
			continue
		}
		cells[op.X+i-1][op.Y-1] = blueprintCell{tile: TTile{Element: elem, Color: glyph}}
	}
	return nil
}

func blueprintCarvePorts(cells *[BOARD_WIDTH][BOARD_HEIGHT]blueprintCell, exits BlueprintExits, ports BlueprintPorts, floor TTile) error {
	type port struct {
		dir, target string
		coord       *int
	}
	for _, p := range []port{{"north", exits.North, ports.North}, {"south", exits.South, ports.South}, {"west", exits.West, ports.West}, {"east", exits.East, ports.East}} {
		if p.target != "" && p.coord == nil {
			return fmt.Errorf("exit %s targets %q but ports.%s is omitted", p.dir, p.target, p.dir)
		}
		if p.coord == nil {
			continue
		}
		if p.target == "" {
			return fmt.Errorf("ports.%s is set but exits.%s is empty", p.dir, p.dir)
		}
		var coords [][2]int
		switch p.dir {
		case "north":
			coords = [][2]int{{*p.coord, 1}, {*p.coord, 2}}
		case "south":
			coords = [][2]int{{*p.coord, BOARD_HEIGHT}, {*p.coord, BOARD_HEIGHT - 1}}
		case "west":
			coords = [][2]int{{1, *p.coord}, {2, *p.coord}}
		case "east":
			coords = [][2]int{{BOARD_WIDTH, *p.coord}, {BOARD_WIDTH - 1, *p.coord}}
		}
		for _, pos := range coords {
			if err := blueprintCoord(pos[0], pos[1], "port "+p.dir); err != nil {
				return err
			}
			cells[pos[0]-1][pos[1]-1] = blueprintCell{tile: floor}
		}
	}
	return nil
}

func blueprintResolveActor(actor BlueprintActor, paintedUnder TTile) (renderedBlueprintActor, error) {
	if err := blueprintCoord(actor.X, actor.Y, "actor"); err != nil {
		return renderedBlueprintActor{}, err
	}
	elem, ok := elementByZWDName(actor.Element)
	if !ok || elem == E_PLAYER || !elementNeedsStat(elem) {
		return renderedBlueprintActor{}, fmt.Errorf("element %q is not a supported non-player actor", actor.Element)
	}
	color, err := parseLegendColor(elem, strings.ToLower(actor.Color))
	if err != nil {
		return renderedBlueprintActor{}, fmt.Errorf("color: %w", err)
	}
	if elem == E_PASSAGE && actor.Target == "" {
		return renderedBlueprintActor{}, fmt.Errorf("Passage requires target board name")
	}
	if elem != E_PASSAGE && actor.Target != "" {
		return renderedBlueprintActor{}, fmt.Errorf("target is only valid for Passage")
	}
	if actor.Character != "" {
		if elem != E_OBJECT || len(actor.Character) != 1 || actor.Character[0] < 0x20 || actor.Character[0] > 0x7E {
			return renderedBlueprintActor{}, fmt.Errorf("character must be one printable ASCII byte and is only valid for Object")
		}
		if actor.P1 != nil {
			return renderedBlueprintActor{}, fmt.Errorf("use character or p1, not both")
		}
	}
	for _, field := range []struct {
		name  string
		value *int
	}{{"cycle", actor.Cycle}, {"p1", actor.P1}, {"p2", actor.P2}, {"p3", actor.P3}} {
		name, value := field.name, field.value
		if value == nil {
			continue
		}
		if name == "cycle" && (*value < 0 || *value > 32767) {
			return renderedBlueprintActor{}, fmt.Errorf("cycle must be 0..32767")
		}
		if name != "cycle" && (*value < 0 || *value > 255) {
			return renderedBlueprintActor{}, fmt.Errorf("%s is out of range", name)
		}
	}
	if actor.Cycle == nil && ElementDefs[elem].Cycle < 0 {
		return renderedBlueprintActor{}, fmt.Errorf("cycle is required for %s", zwdElementName(elem))
	}
	if actor.Step != "" {
		if _, _, err := parseStep(actor.Step); err != nil {
			return renderedBlueprintActor{}, err
		}
	}
	if elem == E_PASSAGE && actor.P3 != nil {
		return renderedBlueprintActor{}, fmt.Errorf("use target instead of p3 for Passage")
	}
	under := paintedUnder
	if actor.Under != nil {
		under, err = blueprintResolveTile(*actor.Under, false)
		if err != nil {
			return renderedBlueprintActor{}, fmt.Errorf("under: %w", err)
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(actor.OOP, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "end" {
			return renderedBlueprintActor{}, fmt.Errorf("OOP may not contain a bare line 'end'")
		}
	}
	if err := blueprintPrintableASCII(actor.OOP, true); err != nil {
		return renderedBlueprintActor{}, fmt.Errorf("OOP: %w", err)
	}
	return renderedBlueprintActor{actor: actor, elem: elem, color: color, under: under}, nil
}

func blueprintPrintableASCII(s string, allowNewlines bool) error {
	for i := 0; i < len(s); i++ {
		if allowNewlines && (s[i] == '\n' || s[i] == '\r') {
			continue
		}
		if s[i] < 0x20 || s[i] > 0x7E {
			return fmt.Errorf("must use printable ASCII; byte %d is 0x%02X", i, s[i])
		}
	}
	return nil
}

func blueprintWriteZWD(bp BoardBlueprint, maxShots int, cells [BOARD_WIDTH][BOARD_HEIGHT]blueprintCell, actors []renderedBlueprintActor) (string, error) {
	type signature struct {
		elem, color, underElem, underColor byte
		to                                 string
	}
	order := make([]signature, 0)
	seen := make(map[signature]bool)
	var signatures [BOARD_WIDTH][BOARD_HEIGHT]signature
	for y := 0; y < BOARD_HEIGHT; y++ {
		for x := 0; x < BOARD_WIDTH; x++ {
			cell := cells[x][y]
			sig := signature{elem: cell.tile.Element, color: cell.tile.Color, to: cell.toBoard}
			if cell.tile.Element == E_PLAYER {
				sig.underElem, sig.underColor = cell.under.Element, cell.under.Color
			}
			signatures[x][y] = sig
			if !seen[sig] {
				seen[sig] = true
				order = append(order, sig)
			}
		}
	}
	keys := make(map[signature]byte, len(order))
	keyPool := make([]byte, 0, 90)
	for c := byte(0x21); c <= 0x7E; c++ {
		if c == '"' || c == '\\' || c == '=' {
			continue
		}
		keyPool = append(keyPool, c)
	}
	if len(order) > len(keyPool) {
		return "", fmt.Errorf("board needs %d unique legend entries; blueprint limit is %d", len(order), len(keyPool))
	}
	for i, sig := range order {
		keys[sig] = keyPool[i]
	}

	var b strings.Builder
	fmt.Fprintf(&b, "board %s\n", quoteZWD(bp.Board))
	fmt.Fprintf(&b, "  start player at %d,%d\n", bp.Start.X, bp.Start.Y)
	fmt.Fprintf(&b, "  max-shots %d\n", maxShots)
	fmt.Fprintf(&b, "  dark %s\n", boolStr(bp.Dark))
	fmt.Fprintf(&b, "  reenter %s\n", boolStr(bp.Reenter))
	fmt.Fprintf(&b, "  time-limit %d\n", bp.TimeLimit)
	fmt.Fprintf(&b, "  exits north %s south %s west %s east %s\n", blueprintExit(bp.Exits.North), blueprintExit(bp.Exits.South), blueprintExit(bp.Exits.West), blueprintExit(bp.Exits.East))
	if bp.Message != "" {
		fmt.Fprintf(&b, "  message %s\n", quoteZWD(bp.Message))
	}
	b.WriteString("\n  grid\n")
	for y := 0; y < BOARD_HEIGHT; y++ {
		for x := 0; x < BOARD_WIDTH; x++ {
			b.WriteByte(keys[signatures[x][y]])
		}
		b.WriteByte('\n')
	}
	b.WriteString("  end\n\n  legend\n")
	for _, sig := range order {
		fmt.Fprintf(&b, "    %c = %s color %s", keys[sig], zwdElementName(sig.elem), zwdColorStr(sig.color))
		if sig.elem == E_PLAYER {
			fmt.Fprintf(&b, " under %s color %s", zwdElementName(sig.underElem), zwdColorStr(sig.underColor))
		}
		if sig.to != "" {
			fmt.Fprintf(&b, " to %s", quoteZWD(sig.to))
		}
		b.WriteByte('\n')
	}
	b.WriteString("  end\n")
	if len(actors) > 0 {
		b.WriteString("\n  stats\n")
		for _, rendered := range actors {
			a := rendered.actor
			fmt.Fprintf(&b, "    stat at %d,%d element %s", a.X, a.Y, zwdElementName(rendered.elem))
			if a.Cycle != nil {
				fmt.Fprintf(&b, " cycle %d", *a.Cycle)
			}
			if a.Character != "" {
				fmt.Fprintf(&b, " p1 %s", zwdByteStr(a.Character[0]))
			} else if a.P1 != nil {
				fmt.Fprintf(&b, " p1 %d", *a.P1)
			}
			if a.P2 != nil {
				fmt.Fprintf(&b, " p2 %d", *a.P2)
			}
			if rendered.elem == E_PASSAGE {
				fmt.Fprintf(&b, " p3 board %s", quoteZWD(a.Target))
			} else if a.P3 != nil {
				fmt.Fprintf(&b, " p3 %d", *a.P3)
			}
			if a.Step != "" && a.Step != "idle" {
				fmt.Fprintf(&b, " step %s", a.Step)
			}
			if rendered.under.Element != E_EMPTY || rendered.under.Color != 0x00 {
				fmt.Fprintf(&b, " under %s color %s", zwdElementName(rendered.under.Element), zwdColorStr(rendered.under.Color))
			}
			b.WriteByte('\n')
			if a.OOP != "" {
				b.WriteString("    oop\n")
				oop := strings.ReplaceAll(strings.TrimRight(a.OOP, "\r\n"), "\r\n", "\n")
				for _, line := range strings.Split(oop, "\n") {
					b.WriteString("    ")
					b.WriteString(line)
					b.WriteByte('\n')
				}
				b.WriteString("    end\n")
			}
		}
		b.WriteString("  end\n")
	}
	b.WriteString("end\n")
	return b.String(), nil
}

func blueprintExit(name string) string {
	if name == "" {
		return "none"
	}
	return quoteZWD(name)
}

func blueprintJSONCandidate(text string) string {
	raw := strings.TrimSpace(text)
	if strings.HasPrefix(raw, "{") {
		return raw
	}
	if strings.HasPrefix(raw, "```json") && strings.HasSuffix(raw, "```") {
		return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(raw, "```json"), "```"))
	}
	return ""
}

// compactBlueprintJSON stabilizes the prior candidate included in repair
// prompts and avoids re-sending arbitrary whitespace from a model response.
func compactBlueprintJSON(raw string) string {
	var out bytes.Buffer
	if json.Compact(&out, []byte(raw)) == nil {
		return out.String()
	}
	return strings.TrimSpace(raw)
}
