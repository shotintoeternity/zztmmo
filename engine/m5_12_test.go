package zztgo

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// helpFileForTest mirrors the client's helpFileFor (web/src/help.ts): a "!-FILE"
// hyperlink pointer resolves to its uppercased .HLP filename.
func helpFileForTest(pointer string) string {
	return strings.ToUpper(pointer) + ".HLP"
}

// crossFileLinks extracts the "!-FILE" cross-file targets from a help file's
// lines, matching vanilla TextWindowSelect's `-` branch (txtwind.go:183).
func crossFileLinks(lines []string) []string {
	var out []string
	for _, line := range lines {
		if !strings.HasPrefix(line, "!-") {
			continue
		}
		pointer := line[1:]
		if semi := strings.IndexByte(pointer, ';'); semi >= 0 {
			pointer = pointer[:semi]
		}
		pointer = strings.TrimPrefix(pointer, "-")
		if pointer != "" {
			out = append(out, helpFileForTest(pointer))
		}
	}
	return out
}

// M5.12 — every editor/title/game help file reachable by a "!-FILE" link must be
// served. The browser now follows these links (web/src/help.ts), so a target that
// fails validHelpFile or is missing from HelpDir would be a dead link (a 404 error
// window) in the editor's help graph.
func TestM512EditorHelpGraphResolves(t *testing.T) {
	api := &WebAPI{}

	// EDITOR.HLP is the editor's H key; ABOUT.HLP the title About window; GAME.HLP
	// the in-game help. Together they root the whole help graph.
	roots := []string{"EDITOR.HLP", "ABOUT.HLP", "GAME.HLP"}

	fetch := func(file string) []string {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/help?file="+file+"&title="+file, nil)
		api.Handler().ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("/api/help?file=%s status=%d body=%q (dead help link)", file, rec.Code, rec.Body.String())
		}
		var msg struct {
			Lines []string `json:"lines"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &msg); err != nil {
			t.Fatalf("decode %s: %v", file, err)
		}
		if len(msg.Lines) == 0 {
			t.Fatalf("%s served no lines", file)
		}
		return msg.Lines
	}

	visited := map[string]bool{}
	queue := append([]string(nil), roots...)
	for len(queue) > 0 {
		file := queue[0]
		queue = queue[1:]
		if visited[file] {
			continue
		}
		visited[file] = true
		if !validHelpFile(file) {
			t.Errorf("validHelpFile(%q) = false: help link would 400 in the browser", file)
			continue
		}
		lines := fetch(file)
		queue = append(queue, crossFileLinks(lines)...)
	}

	// The task's named graph must all be reachable and resolvable.
	want := []string{
		"EDITOR.HLP", "CREATURE.HLP", "TERRAIN.HLP", "ITEM.HLP",
		"LANG.HLP", "LANGTUT.HLP", "LANGREF.HLP", "INFO.HLP",
		"ABOUT.HLP", "LICENSE.HLP", "GAME.HLP",
	}
	for _, file := range want {
		if !visited[file] {
			t.Errorf("%s was never reached/resolved from the help graph", file)
		}
	}
}

// A broken "!-FILE" target must surface as a 404 (an error window in the browser),
// never a panic or a silent empty window.
func TestM512MissingHelpFileIs404(t *testing.T) {
	api := &WebAPI{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/help?file=NOSUCH.HLP&title=x", nil)
	api.Handler().ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("missing help file status=%d, want 404", rec.Code)
	}
}
