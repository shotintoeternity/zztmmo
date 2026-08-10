// M33.2 — the browser-suite drift lint.
//
// M33.1 found seven real-browser suites red across nine commits, and the finding
// that matters is not the seven: it is that nothing said so. Five shipped
// product changes each moved a surface an older suite drives, each added a
// browser journey for itself, and none re-ran the suites it moved — because the
// real-browser family is opt-in (CLAUDE.md rule 3, owner decision 2026-08-01).
//
// This file is the half of M33.2 that costs nothing. It runs in the everyday
// `go test ./...`, launches no browser, reads no client source, and takes
// milliseconds: it looks at the HARNESSES and forbids the two habits that let a
// product change move a suite silently. The other half is `make browser` and
// rule 3's instruction to run it — see NOTES.md 2026-08-09 for the choice and
// the alternatives it beat.
//
// What this lint deliberately does NOT do: it does not re-run browsers, it does
// not mirror the client's strings, and it does not police arrow presses (arrows
// drive the board here as well as menus, so no crisp static rule separates a
// counted menu from a walked player — that habit lives in rule 3, where
// `pickListRow` is named).
package zztgo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The one binary-launching harness allowed to inherit the server's default
// world. m16_19_test.go is the production-boundary suite: running the shipped
// defaults is part of its subject, and M29.1 kept it green by shipping a
// LOBBY.ZZT into its fixture directory rather than by naming a world. If the
// default moves again, this file is the one to update — which is the point of
// naming it here rather than skipping the whole check.
var m332DefaultWorldAllowed = map[string]string{
	"m16_19_test.go": "production-boundary harness: the shipped default is part of what it tests",
}

// The browser scripts that are deliberately about a COLD visitor and must not
// warm their profile.
var m332ColdVisitAllowed = map[string]string{
	"first_visit_journey.test.mjs": "M23.3's own suite: a fresh guest's first visit is its subject",
}

// TestM332BrowserHarnessesNameTheirWorld — check A.
//
// M29.1 changed the server's default `-world` from TOWN to LOBBY. Three
// harnesses were inheriting that default and all three changed subject
// underneath themselves; two of them (the co-op cutline and M16.11) stayed red
// until M33.1. A harness that launches the shipped binary must say which world
// it is about.
func TestM332BrowserHarnessesNameTheirWorld(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatalf("glob test files: %v", err)
	}
	seen := 0
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		text := string(src)
		if !strings.Contains(text, "getM1619ServerBinary(") {
			continue
		}
		// Allowlist first, so its staleness is checked even for the file that
		// defines the helper.
		if reason, ok := m332DefaultWorldAllowed[file]; ok {
			if strings.Contains(text, `"-world"`) {
				t.Errorf("%s names its world now, so its allowlist entry (%q) is stale — delete it from m332DefaultWorldAllowed", file, reason)
			}
			continue
		}
		// The definition site itself, not a caller.
		if strings.Contains(text, "func getM1619ServerBinary(") && strings.Count(text, "getM1619ServerBinary(") == 1 {
			continue
		}
		seen++
		if !strings.Contains(text, `"-world"`) {
			t.Errorf("%s launches the shipped server but never passes \"-world\", so it inherits whatever the server defaults to today.\n"+
				"Name the world this suite is about (M29.1 moved that default from TOWN to LOBBY and silently changed three harnesses' subject; see NOTES.md 2026-08-09).\n"+
				"If inheriting the default really is what this suite tests, add it to m332DefaultWorldAllowed with the reason.", file)
		}
	}
	if seen == 0 {
		t.Fatal("no binary-launching harness was inspected: the lint's marker (getM1619ServerBinary) has moved and this check is watching nothing")
	}
}

// TestM332BrowserScriptsDeclareTheirVisit — check B.
//
// M23.3 gave a fresh guest's root visit WELCOME's title screen instead of the
// picker (title_flow.ts shouldOpenWelcomeFirstVisit). Every browser script opens
// a brand-new context, so every script is a first visit unless it says
// otherwise, and the ones that walk the launch flow were left typing a world
// name at a title screen. Whether that fires today depends on whether the
// script's harness happens to host WELCOME — a decision made in a different
// file. A script that goes through the launch flow must say which visitor it is
// rather than inherit the answer.
func TestM332BrowserScriptsDeclareTheirVisit(t *testing.T) {
	dir := filepath.Join("web", "test")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	seen := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".test.mjs") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(src)
		launchFlow := false
		for _, arg := range m332GotoArgs(t, name, text) {
			// A page that never loads the client is not a visit at all
			// (`page.goto("about:blank")`, which the pause-clock suites use to
			// test the harness itself). A literal that does not name the server
			// is that case; anything the lint cannot read is NOT — an unreadable
			// destination is a reason to declare, not a reason to skip.
			if !strings.Contains(arg, "baseURL") && m332IsStringLiteral(arg) {
				continue
			}
			// A deep link into a world or a watch feed bypasses the launch flow
			// entirely (launchOpensPicker in web/test/lib/canvas.mjs asks the URL
			// the same way). Every other route reaches it — /replay/ and
			// /challenge included.
			if strings.Contains(arg, "/play/") || strings.Contains(arg, "/watch/") {
				continue
			}
			launchFlow = true
			break
		}
		if !launchFlow {
			continue
		}
		warm := strings.Contains(text, "markProfileWarm(")
		if reason, ok := m332ColdVisitAllowed[name]; ok {
			if warm {
				t.Errorf("%s warms its profile but is allowlisted as a cold-visit suite (%q) — the allowlist entry and the script disagree", name, reason)
			}
			continue
		}
		seen++
		if !warm {
			t.Errorf("web/test/%s navigates the launch flow without saying which visitor it is.\n"+
				"Call markProfileWarm(context) before the first page loads if the suite is about a returning player (the usual case), or add it to m332ColdVisitAllowed if a first visit is its subject.\n"+
				"M23.3 made a fresh guest's root visit open WELCOME instead of the picker; a script that does not declare this only passes while its harness happens not to host WELCOME (see NOTES.md 2026-08-09).", name)
		}
	}
	if seen == 0 {
		t.Fatal("no launch-flow browser script was inspected: this check is watching nothing")
	}
}

// m332GotoArgs returns the first argument of every `.goto(` call in a script,
// as written. A call this cannot parse fails the lint rather than passing
// silently: an unparsed navigation is an unchecked one.
func m332GotoArgs(t *testing.T, name, text string) []string {
	t.Helper()
	const marker = ".goto("
	var args []string
	for idx := 0; ; {
		hit := strings.Index(text[idx:], marker)
		if hit < 0 {
			return args
		}
		start := idx + hit + len(marker)
		arg, end, ok := m332FirstArg(text[start:])
		if !ok {
			t.Fatalf("%s: could not read the argument of the .goto( at byte %d; the lint must not skip a navigation it cannot parse", name, start)
		}
		args = append(args, arg)
		idx = start + end
	}
}

// m332IsStringLiteral reports whether an argument is one plain quoted string —
// the only shape this lint is willing to read as "not the client".
func m332IsStringLiteral(arg string) bool {
	if len(arg) < 2 {
		return false
	}
	q := arg[0]
	if q != '"' && q != '\'' && q != '`' {
		return false
	}
	if arg[len(arg)-1] != q {
		return false
	}
	// A concatenation or an interpolation is not a plain literal.
	return !strings.ContainsAny(arg[1:len(arg)-1], "`\"'+") && !strings.Contains(arg, "${")
}

// m332FirstArg reads the first call argument out of the text following an open
// paren, stopping at the comma that separates it from the options object or at
// the closing paren. Quotes, template literals and nested brackets are tracked
// so a comma or paren inside a string does not end the argument early.
func m332FirstArg(text string) (string, int, bool) {
	depth := 0
	var quote rune
	escaped := false
	for i, r := range text {
		if escaped {
			escaped = false
			continue
		}
		if quote != 0 {
			switch {
			case r == '\\':
				escaped = true
			case r == quote:
				quote = 0
			}
			continue
		}
		switch r {
		case '"', '\'', '`':
			quote = r
		case '(', '[', '{':
			depth++
		case ')':
			if depth == 0 {
				return strings.TrimSpace(text[:i]), i, true
			}
			depth--
		case ']', '}':
			depth--
		case ',':
			if depth == 0 {
				return strings.TrimSpace(text[:i]), i, true
			}
		case '\n':
			// Every .goto( in this repo writes its URL on one line; a multi-line
			// call is a shape this lint has not been taught to read.
			return "", 0, false
		}
	}
	return "", 0, false
}
