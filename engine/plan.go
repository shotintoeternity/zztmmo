package zztgo

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// World plan validator (M12.3a, plan-then-paint phase 1).
//
// A "world plan" is the compact Markdown artifact the generator LLM emits before
// any board is painted: a premise, palette rules, a board table (id, name,
// concept, dark, exits/links), and a progression spine (which keys/doors/flags
// gate what, in order). `llmworld/plans/LASTLITE.md` is the reference exemplar
// and defines the format this parser accepts.
//
// This file does NO LLM call and touches NO simulation state. It parses the
// board table and the spine, then checks the plan mechanically so bad plans
// fail with precise, repair-loop-friendly errors (same philosophy as the ZWD
// compiler in zwd.go): board count within limits, exit reciprocity, passage
// targets exist, the graph is connected from the start board, and the spine is
// solvable (every key/flag is acquired before the door/gate it opens, and the
// finale is reachable).
//
// Design decisions (advisor was unavailable 2026-07-10; recorded in NOTES.md):
//   - Reciprocity is enforced for directional edge exits only. A one-way edge
//     A --E--> B is satisfied if B links back to A by the opposite edge OR by
//     any passage/bidirectional link. LASTLITE returns village->cellar through a
//     passage, not an edge, so a strict "opposite edge only" rule would reject
//     the exemplar. Passages carry no reciprocity requirement; a `passage<->X`
//     declared on either endpoint is bidirectional for both.
//   - Spine solvability is checked as an ORDERING over spine steps, not by
//     re-deriving which board each key sits on. A key is "acquired" at its bold
//     `**COLOR KEY**` step; a door is "required" at its bold `**COLOR DOOR**`
//     step; a flag is "set" at its bold `**FLAG**` step and "checked" at any
//     later `#if FLAG`. The key/set must appear in a strictly earlier step than
//     the matching door/check ("a key behind its own door" is exactly the
//     reversed ordering). Finale reachability is covered by full connectivity.

// PlanLink is one exit or passage declared in a board's "exits/links" cell.
type PlanLink struct {
	Kind   string // "edge" or "passage"
	Dir    string // "N","S","E","W" for edges; "" for passages
	Bidir  bool   // declared with <-> (bidirectional)
	Target string // destination board id
}

// PlanBoard is one row of the plan's board table.
type PlanBoard struct {
	Index   int
	ID      string
	Name    string
	Concept string
	Dark    bool
	IsStart bool // concept contains "START"
	IsTitle bool // board index 0 (title screen, excluded from connectivity)
	Links   []PlanLink
	Line    int
}

// SpineStep is one numbered item of the progression spine.
type SpineStep struct {
	Index      int      // 1-based step number as written
	Keys       []string // colors of **... KEY** acquired here
	Doors      []string // colors of **... DOOR** required here
	FlagSets   []string // **FLAG** tokens introduced here
	FlagChecks []string // `#if FLAG` references here
	Endgame    bool     // step mentions #endgame
	Text       string
}

// Plan is a parsed world plan.
type Plan struct {
	WorldName string
	Boards    []PlanBoard
	Spine     []SpineStep
}

// planMaxBoards is the ZWD/vanilla limit: MAX_BOARD = 100 non-title boards
// (ZWD.md §Limits). The title screen is board 0 and does not count.
const planMaxBoards = 100

var (
	planTokenRe  = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	planIfRe     = regexp.MustCompile(`#if\s+(?:not\s+)?([A-Za-z_][A-Za-z0-9_]*)`)
	planParenRe  = regexp.MustCompile(`\([^)]*\)`)
	planStepRe   = regexp.MustCompile(`^\s*(\d+)\.\s+(.*)$`)
	planHeadingRe = regexp.MustCompile(`^\s*#{1,6}\s+(.*)$`)
)

// ValidatePlan parses a world plan and returns nil if it is coherent, or an
// error naming every problem found (one per line). It is the M12.4 planner's
// repair food, so messages are specific.
func ValidatePlan(src string) error {
	plan, err := ParsePlan(src)
	if err != nil {
		return err
	}
	return plan.Validate()
}

// ParsePlan reads the board table and spine out of a plan document.
func ParsePlan(src string) (Plan, error) {
	lines := strings.Split(src, "\n")
	var plan Plan
	plan.WorldName = parsePlanWorldName(lines)

	boards, err := parsePlanBoards(lines)
	if err != nil {
		return Plan{}, err
	}
	plan.Boards = boards
	plan.Spine = parsePlanSpine(lines)
	return plan, nil
}

func parsePlanWorldName(lines []string) string {
	// The document title heading, e.g. "# World Plan: THE LAST LIGHTHOUSE".
	for _, ln := range lines {
		m := planHeadingRe.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		title := strings.TrimSpace(m[1])
		if i := strings.Index(title, ":"); i >= 0 {
			title = strings.TrimSpace(title[i+1:])
		}
		return title
	}
	return ""
}

// sectionLines returns the lines under the first heading whose text contains
// needle (case-insensitive), up to the next heading of the same or shallower
// depth, with the 1-based file line number of each.
func sectionLines(lines []string, needle string) ([]string, []int) {
	needle = strings.ToLower(needle)
	start := -1
	depth := 0
	for i, ln := range lines {
		m := planHeadingRe.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		if start < 0 {
			if strings.Contains(strings.ToLower(m[1]), needle) {
				start = i + 1
				depth = headingDepth(ln)
			}
			continue
		}
		// We are inside the section; stop at the next heading of depth <= ours.
		if headingDepth(ln) <= depth {
			return lines[start:i], rangeInts(start+1, i+1)
		}
	}
	if start < 0 {
		return nil, nil
	}
	return lines[start:], rangeInts(start+1, len(lines)+1)
}

func headingDepth(ln string) int {
	t := strings.TrimSpace(ln)
	n := 0
	for n < len(t) && t[n] == '#' {
		n++
	}
	return n
}

func rangeInts(lo, hi int) []int {
	out := make([]int, 0, hi-lo)
	for i := lo; i < hi; i++ {
		out = append(out, i)
	}
	return out
}

func parsePlanBoards(lines []string) ([]PlanBoard, error) {
	sec, nums := sectionLines(lines, "board graph")
	if sec == nil {
		return nil, fmt.Errorf("plan: no '## Board graph' section found")
	}
	var boards []PlanBoard
	seenHeader := false
	for i, raw := range sec {
		ln := strings.TrimSpace(raw)
		if !strings.HasPrefix(ln, "|") {
			continue
		}
		cells := splitTableRow(ln)
		if len(cells) < 6 {
			continue
		}
		// Skip the header row and the |---|---| separator row.
		if !seenHeader {
			seenHeader = true
			continue
		}
		if isTableSeparator(cells) {
			continue
		}
		idxStr := strings.TrimSpace(cells[0])
		idx, err := strconv.Atoi(idxStr)
		if err != nil {
			return nil, fmt.Errorf("plan line %d: board row index %q is not a number", nums[i], idxStr)
		}
		b := PlanBoard{
			Index:   idx,
			ID:      strings.TrimSpace(cells[1]),
			Name:    strings.TrimSpace(cells[2]),
			Concept: strings.TrimSpace(cells[3]),
			Line:    nums[i],
		}
		darkCell := strings.ToLower(strings.TrimSpace(cells[4]))
		b.Dark = darkCell == "yes" || darkCell == "dark" || darkCell == "true"
		b.IsTitle = idx == 0
		b.IsStart = strings.Contains(strings.ToUpper(b.Concept), "START")
		links, err := parsePlanLinks(cells[5], nums[i])
		if err != nil {
			return nil, err
		}
		b.Links = links
		if b.ID == "" {
			return nil, fmt.Errorf("plan line %d: board %d has an empty id", nums[i], idx)
		}
		boards = append(boards, b)
	}
	if len(boards) == 0 {
		return nil, fmt.Errorf("plan: board graph table has no rows")
	}
	return boards, nil
}

func splitTableRow(ln string) []string {
	ln = strings.TrimSpace(ln)
	ln = strings.TrimPrefix(ln, "|")
	ln = strings.TrimSuffix(ln, "|")
	return strings.Split(ln, "|")
}

func isTableSeparator(cells []string) bool {
	for _, c := range cells {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if strings.Trim(c, "-: ") != "" {
			return false
		}
	}
	return true
}

func parsePlanLinks(cell string, line int) ([]PlanLink, error) {
	// Drop parenthetical annotations: "(passage)", "(via fence gap)", etc.
	cell = planParenRe.ReplaceAllString(cell, " ")
	cell = strings.TrimSpace(cell)
	if cell == "" || cell == "—" || cell == "-" {
		return nil, nil
	}
	// Split on commas and whitespace; keep only tokens that carry an arrow.
	fields := strings.FieldsFunc(cell, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
	var links []PlanLink
	for _, f := range fields {
		bidir, arrow := planArrowKind(f)
		if arrow < 0 {
			continue // annotation word without an arrow (e.g. leftover "via")
		}
		left := strings.TrimSpace(f[:arrow])
		right := strings.TrimSpace(f[arrow+arrowRuneLen(f, arrow):])
		if right == "" {
			return nil, fmt.Errorf("plan line %d: exit %q has no target board", line, f)
		}
		lk := PlanLink{Bidir: bidir, Target: right}
		switch strings.ToUpper(left) {
		case "PASSAGE":
			lk.Kind = "passage"
		case "N", "NORTH":
			lk.Kind, lk.Dir = "edge", "N"
		case "S", "SOUTH":
			lk.Kind, lk.Dir = "edge", "S"
		case "E", "EAST":
			lk.Kind, lk.Dir = "edge", "E"
		case "W", "WEST":
			lk.Kind, lk.Dir = "edge", "W"
		default:
			return nil, fmt.Errorf("plan line %d: unknown exit direction %q (want N/S/E/W or passage)", line, left)
		}
		links = append(links, lk)
	}
	return links, nil
}

// planArrowKind reports whether token f contains a link arrow and where. It
// returns (bidir, byteIndexOfArrow) or (_, -1) when there is no arrow. The
// bidirectional forms are "↔" and "<->"; the one-way forms are "→" and "->".
func planArrowKind(f string) (bool, int) {
	if i := strings.Index(f, "↔"); i >= 0 {
		return true, i
	}
	if i := strings.Index(f, "<->"); i >= 0 {
		return true, i
	}
	if i := strings.Index(f, "→"); i >= 0 {
		return false, i
	}
	if i := strings.Index(f, "->"); i >= 0 {
		return false, i
	}
	return false, -1
}

func arrowRuneLen(f string, at int) int {
	switch {
	case strings.HasPrefix(f[at:], "↔"):
		return len("↔")
	case strings.HasPrefix(f[at:], "<->"):
		return 3
	case strings.HasPrefix(f[at:], "→"):
		return len("→")
	case strings.HasPrefix(f[at:], "->"):
		return 2
	}
	return 1
}

func parsePlanSpine(lines []string) []SpineStep {
	sec, _ := sectionLines(lines, "progression spine")
	if sec == nil {
		return nil
	}
	var steps []SpineStep
	cur := -1
	for _, raw := range sec {
		if m := planStepRe.FindStringSubmatch(raw); m != nil {
			n, _ := strconv.Atoi(m[1])
			steps = append(steps, SpineStep{Index: n, Text: m[2]})
			cur = len(steps) - 1
			continue
		}
		if cur >= 0 && strings.TrimSpace(raw) != "" {
			// Continuation line of the current step.
			steps[cur].Text += " " + strings.TrimSpace(raw)
		}
	}
	for i := range steps {
		classifySpineStep(&steps[i])
	}
	return steps
}

func classifySpineStep(s *SpineStep) {
	for _, m := range planTokenRe.FindAllStringSubmatch(s.Text, -1) {
		tok := strings.TrimSpace(m[1])
		up := strings.ToUpper(tok)
		switch {
		case strings.HasSuffix(up, "KEY"):
			s.Keys = append(s.Keys, planColor(up, "KEY"))
		case strings.HasSuffix(up, "DOOR"):
			s.Doors = append(s.Doors, planColor(up, "DOOR"))
		default:
			// A bare bold token is a flag being introduced (set).
			s.FlagSets = append(s.FlagSets, up)
		}
	}
	for _, m := range planIfRe.FindAllStringSubmatch(s.Text, -1) {
		s.FlagChecks = append(s.FlagChecks, strings.ToUpper(m[1]))
	}
	if strings.Contains(strings.ToLower(s.Text), "#endgame") {
		s.Endgame = true
	}
}

func planColor(upperToken, suffix string) string {
	c := strings.TrimSpace(strings.TrimSuffix(upperToken, suffix))
	if c == "" {
		return upperToken
	}
	return c
}

// Validate runs every mechanical check and joins all failures into one error.
func (p Plan) Validate() error {
	var probs []string
	add := func(format string, args ...interface{}) {
		probs = append(probs, fmt.Sprintf(format, args...))
	}

	byID := map[string]*PlanBoard{}
	byIdx := map[int]*PlanBoard{}
	nonTitle := 0
	var start *PlanBoard
	for i := range p.Boards {
		b := &p.Boards[i]
		if prev, ok := byID[b.ID]; ok {
			add("board id %q is used twice (boards %d and %d)", b.ID, prev.Index, b.Index)
		}
		byID[b.ID] = b
		if prev, ok := byIdx[b.Index]; ok {
			add("board index %d is used twice (%q and %q)", b.Index, prev.ID, b.ID)
		}
		byIdx[b.Index] = b
		if !b.IsTitle {
			nonTitle++
		}
		if b.IsStart {
			if start != nil {
				add("more than one board is marked START (%q and %q)", start.ID, b.ID)
			} else {
				start = b
			}
		}
	}
	if nonTitle > planMaxBoards {
		add("plan has %d non-title boards, over the limit of %d", nonTitle, planMaxBoards)
	}

	// Choose the start board: the one marked START, else the lowest non-title
	// index. Board 0 (title) is never the start.
	if start == nil {
		for i := range p.Boards {
			b := &p.Boards[i]
			if !b.IsTitle && (start == nil || b.Index < start.Index) {
				start = b
			}
		}
	}

	// Passage/exit targets must exist.
	for i := range p.Boards {
		b := &p.Boards[i]
		for _, lk := range b.Links {
			if _, ok := byID[lk.Target]; !ok {
				kind := "exit"
				if lk.Kind == "passage" {
					kind = "passage"
				}
				add("board %q (line %d): %s target %q is not a board id in the plan", b.ID, b.Line, kind, lk.Target)
			}
		}
	}

	// Directed adjacency for connectivity: edges and one-way passages add a
	// forward link; bidirectional (<->) links add both directions.
	adj := map[string][]string{}
	for i := range p.Boards {
		b := &p.Boards[i]
		for _, lk := range b.Links {
			if _, ok := byID[lk.Target]; !ok {
				continue // already reported
			}
			adj[b.ID] = append(adj[b.ID], lk.Target)
			if lk.Bidir {
				adj[lk.Target] = append(adj[lk.Target], b.ID)
			}
		}
	}

	// Exit reciprocity for directional edges: a one-way edge A --dir--> B must
	// have a return link from B to A (opposite edge, or any passage/bidir).
	for i := range p.Boards {
		b := &p.Boards[i]
		for _, lk := range b.Links {
			if lk.Kind != "edge" || lk.Bidir {
				continue
			}
			tgt, ok := byID[lk.Target]
			if !ok {
				continue
			}
			if !hasReturnLink(tgt, b.ID, oppositeDir(lk.Dir)) {
				add("board %q has a %s exit to %q, but %q has no return exit to %q",
					b.ID, dirName(lk.Dir), tgt.ID, tgt.ID, b.ID)
			}
		}
	}

	// Connectivity: every non-title board reachable from the start board.
	if start != nil {
		seen := map[string]bool{start.ID: true}
		stack := []string{start.ID}
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, m := range adj[n] {
				if !seen[m] {
					seen[m] = true
					stack = append(stack, m)
				}
			}
		}
		for i := range p.Boards {
			b := &p.Boards[i]
			if b.IsTitle {
				continue
			}
			if !seen[b.ID] {
				add("board %q is not reachable from the start board %q", b.ID, start.ID)
			}
		}
	} else {
		add("plan has no start board (mark one board's concept with START)")
	}

	// Spine solvability: a key/flag must be acquired strictly before the door /
	// gate it opens.
	keyStep := map[string]int{}  // color -> spine step index it is acquired at
	flagStep := map[string]int{} // flag -> spine step index it is set at
	finaleSeen := false
	for _, s := range p.Spine {
		for _, c := range s.Keys {
			if _, dup := keyStep[c]; !dup {
				keyStep[c] = s.Index
			}
		}
		for _, f := range s.FlagSets {
			if _, dup := flagStep[f]; !dup {
				flagStep[f] = s.Index
			}
		}
		if s.Endgame {
			finaleSeen = true
		}
	}
	for _, s := range p.Spine {
		for _, c := range s.Doors {
			ks, ok := keyStep[c]
			if !ok {
				add("spine step %d needs the %s door, but no %s key is acquired anywhere in the spine", s.Index, strings.ToLower(c), strings.ToLower(c))
			} else if ks >= s.Index {
				add("spine step %d needs the %s door, but the %s key is not acquired until step %d (a key behind its own door)", s.Index, strings.ToLower(c), strings.ToLower(c), ks)
			}
		}
		for _, f := range s.FlagChecks {
			fs, ok := flagStep[f]
			if !ok {
				add("spine step %d checks flag %s, but it is never set in the spine", s.Index, f)
			} else if fs >= s.Index {
				add("spine step %d checks flag %s, but it is not set until step %d", s.Index, f, fs)
			}
		}
	}
	if len(p.Spine) > 0 && !finaleSeen {
		add("spine has no finale step (expected a step with #endgame)")
	}

	if len(probs) == 0 {
		return nil
	}
	return fmt.Errorf("invalid world plan:\n  - %s", strings.Join(probs, "\n  - "))
}

func hasReturnLink(from *PlanBoard, to, oppDir string) bool {
	for _, lk := range from.Links {
		if lk.Target != to {
			continue
		}
		// A passage back, a bidirectional link back, or the opposite edge back.
		if lk.Kind == "passage" || lk.Bidir {
			return true
		}
		if lk.Kind == "edge" && lk.Dir == oppDir {
			return true
		}
	}
	return false
}

func oppositeDir(d string) string {
	switch d {
	case "N":
		return "S"
	case "S":
		return "N"
	case "E":
		return "W"
	case "W":
		return "E"
	}
	return ""
}

func dirName(d string) string {
	switch d {
	case "N":
		return "north"
	case "S":
		return "south"
	case "E":
		return "east"
	case "W":
		return "west"
	}
	return d
}
