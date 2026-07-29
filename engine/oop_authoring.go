package zztgo

import (
	"fmt"
	"strings"
)

// M5.7 — ZZT-OOP authoring aids. OopAnalyze is an advisory static pass over one
// object/scroll program for the browser code editor: it lists the object's
// :labels and warns about #sends to labels that do not exist and about #words
// that are neither a known command nor a local label. It reuses the engine's own
// tokenizer primitives (OopReadChar/OopReadWord/OopSkipLine/OopReadLineToEnd and
// OopFindLabel — the same calls OopExecute makes) rather than a second parser, so
// what it reports is exactly what the runtime sees. It never executes anything,
// never mutates board state, and never blocks a save: vanilla ZZT accepts any
// text and worlds rely on that.

// OopLabelInfo is a :label an object defines, with the 0-based program line it
// sits on so the editor can navigate to it.
type OopLabelInfo struct {
	Name string `json:"name"`
	Line int16  `json:"line"`
}

// OopWarning is one advisory diagnostic, tagged with the 0-based program line.
type OopWarning struct {
	Line    int16  `json:"line"`
	Message string `json:"message"`
}

// oopCommands is exactly the #command vocabulary OopExecute dispatches (see
// oop.go). A #word outside this set is, in ZZT, an implicit #send to a label of
// that name (OopExecute's final else falls through to OopSend), so OopAnalyze
// validates it as one.
var oopCommands = map[string]bool{
	"GO": true, "TRY": true, "WALK": true, "SET": true, "CLEAR": true,
	"IF": true, "SHOOT": true, "THROWSTAR": true, "GIVE": true, "TAKE": true,
	"END": true, "ENDGAME": true, "IDLE": true, "RESTART": true, "ZAP": true,
	"RESTORE": true, "LOCK": true, "UNLOCK": true, "SEND": true, "BECOME": true,
	"PUT": true, "CHANGE": true, "PLAY": true, "CYCLE": true, "CHAR": true,
	"DIE": true, "BIND": true,
}

// OopAnalyze walks statId's program with the runtime tokenizer and returns its
// labels and advisory warnings.
func (e *Engine) OopAnalyze(statId int16) ([]OopLabelInfo, []OopWarning) {
	if statId < 0 || statId > e.Board.StatCount {
		return nil, nil
	}
	// Resolve a shared program (negative DataLen) to its source, the way BoardOpen
	// does, so a bound object still lists and validates against the real text.
	src := statId
	if e.Board.Stats[statId].DataLen < 0 {
		src = -e.Board.Stats[statId].DataLen
	}
	if src < 0 || src > e.Board.StatCount || e.Board.Stats[src].DataLen <= 0 {
		return nil, nil
	}
	dataLen := e.Board.Stats[src].DataLen

	// labelExists resolves a send target exactly as OopSend does: OopFindLabel
	// from stat 0 with the "\r:" prefix. Unqualified labels resolve against src
	// only; a Name:label form iterates named objects — both faithful to runtime.
	labelExists := func(target string) bool {
		if target == "" {
			return false
		}
		var iStat, iDataPos int16
		return e.OopFindLabel(src, target, &iStat, &iDataPos, "\r:")
	}

	var labels []OopLabelInfo
	var warnings []OopWarning
	position := int16(0)
	line := int16(0)

	for position >= 0 && position < dataLen {
		startLine := line
		e.OopReadChar(src, &position)
		switch {
		case e.OopChar == ':':
			e.OopReadWord(src, &position)
			labels = append(labels, OopLabelInfo{Name: e.OopWord, Line: startLine})
			e.OopSkipLine(src, &position)
			line++
		case e.OopChar == '#':
			e.OopReadWord(src, &position)
			word := e.OopWord
			// OopExecute skips a leading THEN before the real command.
			if word == "THEN" {
				e.OopReadWord(src, &position)
				word = e.OopWord
			}
			switch {
			case word == "":
				// A bare '#' with nothing after it; OopExecute ignores it too.
			case word == "SEND" || word == "ZAP" || word == "RESTORE":
				e.OopReadWord(src, &position)
				target := e.OopWord
				if target == "" {
					warnings = append(warnings, OopWarning{Line: startLine, Message: "#" + strings.ToLower(word) + " has no label"})
				} else if !labelExists(target) {
					warnings = append(warnings, OopWarning{Line: startLine, Message: "#" + strings.ToLower(word) + " " + target + ": no such label"})
				}
			case word == "CHANGE":
				// The one #command whose arguments are checked, because getting
				// them wrong deletes the running object out from under itself
				// (M12.23). Parsed with the runtime's own OopParseTile so the
				// color/element forms understood here are exactly the ones
				// OopExecute accepts.
				var from, to TTile
				if e.OopParseTile(&src, &position, &from) && e.OopParseTile(&src, &position, &to) {
					if message := oopSelfErasingChange(e, statId, from, to); message != "" {
						warnings = append(warnings, OopWarning{Line: startLine, Message: message})
					}
				}
			case oopCommands[word]:
				// A known command; its arguments are not validated here.
			default:
				// An unknown #word is an implicit self-send in ZZT: fine only if
				// this object defines the label, otherwise a typo or dead send.
				if !labelExists(word) {
					warnings = append(warnings, OopWarning{Line: startLine, Message: "#" + word + ": unknown command or missing label"})
				}
			}
			e.OopSkipLine(src, &position)
			line++
		case e.OopChar == '\r':
			line++
		case e.OopChar == '\x00':
			position = -1
		default:
			// A message/text line. Its only reference to a label is a "!label;text"
			// hyperlink, which selecting sends to this object.
			first := e.OopChar
			text := string([]byte{first}) + e.OopReadLineToEnd(src, &position)
			if label := oopHyperlinkLabel(text); label != "" && !labelExists(label) {
				// Uppercase to match the label list and #send warnings; ZZT matches
				// labels case-insensitively.
				warnings = append(warnings, OopWarning{Line: startLine, Message: "!" + strings.ToUpper(label) + ": no such label"})
			}
			line++
		}
	}
	return labels, warnings
}

// oopSelfErasingChange reports a #change that would erase the executing object's
// own tile while leaving its stat behind, or "" if the command is safe.
//
// #change is a board-wide tile substitution: OopExecute walks FindTileOnBoard and
// calls OopPlaceTile on every match (oop.go:714-728), with no stat bookkeeping at
// all. So `#change Object Empty` inside an Object rewrites the very tile the
// running stat points at — the stat survives with nothing under it, and the next
// tick draws or ticks an element that is no longer there. #die is the command
// that removes both, and it is what the message names.
//
// The check is deliberately narrow: it fires only when the executing tile itself
// matches the search tile the way FindTileOnBoard would match it (element, plus
// color when the command names one), and only when the replacement is an element
// that carries no stat. A generated world reached the browser with exactly this
// line behind a :touch label and panicked on a later room tick, which is why this
// lives here — in the analyzer both the editor and the generation gate run —
// rather than in a prompt instruction.
func oopSelfErasingChange(e *Engine, statId int16, from, to TTile) string {
	if statId < 0 || statId > e.Board.StatCount {
		return ""
	}
	stat := e.Board.Stats[statId]
	self := e.Board.Tiles[stat.X][stat.Y]
	if self.Element != from.Element || !elementNeedsStat(self.Element) {
		return ""
	}
	if from.Color != 0 && GetColorForTileMatch(&self) != from.Color {
		return ""
	}
	if elementNeedsStat(to.Element) {
		return ""
	}
	return fmt.Sprintf("#change %s %s erases this %s but leaves its stat behind; use #die to remove the object",
		ElementDefs[from.Element].Name, ElementDefs[to.Element].Name, ElementDefs[self.Element].Name)
}

// oopHyperlinkLabel extracts the label a "!label;text" message line sends when
// selected, or "" if the line is not a label hyperlink. It mirrors
// TextWindowSelect (txtwind.go): the label is the text between '!' and ';', and a
// leading '-' means a file jump ("!-FILE;text"), not a label.
func oopHyperlinkLabel(line string) string {
	if len(line) == 0 || line[0] != '!' {
		return ""
	}
	s := line[1:]
	if i := strings.IndexByte(s, ';'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 0 && s[0] == '-' {
		return ""
	}
	return s
}
