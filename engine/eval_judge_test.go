package zztgo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEvalDocEmbeddedMatchesSource(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "llmworld", "EVAL.md"))
	if err != nil {
		t.Fatalf("required source llmworld/EVAL.md is missing: %v (it is committed; do not skip past it)", err)
	}
	embedded, err := evalDocFS.ReadFile("promptkit_assets/EVAL.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(src) != string(embedded) {
		t.Error("embedded EVAL.md has drifted from llmworld/EVAL.md; re-copy the source into promptkit_assets/")
	}
}

func TestEvalJudgeRubricContainsEveryDimension(t *testing.T) {
	rubric, err := evalJudgeRubric()
	if err != nil {
		t.Fatal(err)
	}
	for _, dim := range EvalDimensions {
		if !strings.Contains(rubric, dim) {
			t.Errorf("rubric section missing dimension %q", dim)
		}
	}
	if !strings.Contains(rubric, `"scores"`) {
		t.Error("rubric section missing the JSON answer schema")
	}
}

func evalTestVerdictJSON() string {
	return `{"scores":[
		{"dimension":"title-legibility","score":4,"justification":"clean band"},
		{"dimension":"visual-composition","score":3,"justification":"solid rooms"},
		{"dimension":"oop-voice","score":2,"justification":"generic"},
		{"dimension":"grounding-accuracy","score":-1,"justification":"n/a (ungrounded run)"}],
		"overall":"decent"}`
}

func TestParseEvalVerdict(t *testing.T) {
	v, err := parseEvalVerdict("Here you go:\n```json\n" + evalTestVerdictJSON() + "\n```\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Scores) != 4 || v.Scores[0].Score != 4 || v.Overall != "decent" {
		t.Fatalf("unexpected verdict: %+v", v)
	}

	if _, err := parseEvalVerdict(`{"scores":[{"dimension":"title-legibility","score":9,"justification":""}],"overall":""}`); err == nil || !strings.Contains(err.Error(), "outside -1..5") {
		t.Fatalf("out-of-range score must fail, got %v", err)
	}
	if _, err := parseEvalVerdict(`{"scores":[{"dimension":"title-legibility","score":3,"justification":""}],"overall":""}`); err == nil || !strings.Contains(err.Error(), "missing dimension") {
		t.Fatalf("missing dimension must fail, got %v", err)
	}
	if _, err := parseEvalVerdict("no json here"); err == nil {
		t.Fatal("prose-only answer must fail")
	}
}

func TestEvalJudgeSendsImagesAndParsesVerdict(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody = make([]byte, r.ContentLength)
		_, _ = r.Body.Read(gotBody)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"content": []map[string]string{{"type": "text", "text": evalTestVerdictJSON()}},
		})
	}))
	defer srv.Close()

	judge := &EvalJudge{
		apiURL: srv.URL, apiKey: "test", model: "test-model", maxTokens: 512,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
	verdict, err := judge.Judge(context.Background(), EvalJudgeRequest{
		Premise:   "a test premise",
		WorldName: "GEMCAVE",
		PlanText:  "# World Plan: GEMCAVE",
		OOPSample: "@exit",
		Images:    []EvalJudgeImage{{Label: "title (board 0)", PNG: []byte("fakepng")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Scores[3].Score != -1 {
		t.Fatalf("expected n/a grounding score, got %+v", verdict.Scores[3])
	}
	body := string(gotBody)
	if !strings.Contains(body, `"media_type":"image/png"`) {
		t.Error("request body carries no PNG image block")
	}
	if !strings.Contains(body, "ZmFrZXBuZw==") { // base64("fakepng")
		t.Error("request body carries no base64 image data")
	}
	if !strings.Contains(body, "title-legibility") {
		t.Error("request body carries no rubric")
	}
}
