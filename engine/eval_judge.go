// M12.17 tier-2 LLM judge: scores a generated world against the written
// rubric in EVAL.md (llmworld/EVAL.md; embedded copy promptkit_assets/EVAL.md)
// from board screenshots, the world plan, and an OOP sample. Owner-run via
// cmd/zzt-eval only — nothing in the server or CI calls this. The judge's
// output is a report, never world content: no model output from here is ever
// compiled, hosted, or executed.

package zztgo

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

//go:embed promptkit_assets/EVAL.md
var evalDocFS embed.FS

// EvalDimensions are the rubric dimensions, in report order.
var EvalDimensions = []string{
	"title-legibility", "visual-composition", "oop-voice", "grounding-accuracy",
}

// EvalJudge scores generated worlds against the EVAL.md rubric.
type EvalJudge struct {
	apiURL     string
	apiKey     string
	model      string
	maxTokens  int
	httpClient *http.Client
}

// EvalJudgeImage is one labeled screenshot given to the judge.
type EvalJudgeImage struct {
	Label string
	PNG   []byte
}

// EvalJudgeRequest is everything the judge sees for one world.
type EvalJudgeRequest struct {
	Premise   string
	Grounded  bool
	WorldName string
	PlanText  string
	OOPSample string
	Images    []EvalJudgeImage
}

// EvalScore is one scored rubric dimension. Score -1 means n/a
// (grounding-accuracy on an ungrounded run).
type EvalScore struct {
	Dimension     string `json:"dimension"`
	Score         int    `json:"score"`
	Justification string `json:"justification"`
}

// EvalVerdict is the judge's parsed answer.
type EvalVerdict struct {
	Scores  []EvalScore `json:"scores"`
	Overall string      `json:"overall"`
}

// EvalJudgeFromEnv builds a judge from the generation env vars.
// ZZT_EVAL_JUDGE_MODEL overrides ANTHROPIC_MODEL for the judge only.
func EvalJudgeFromEnv() (*EvalJudge, error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	model := os.Getenv("ZZT_EVAL_JUDGE_MODEL")
	if model == "" {
		model = os.Getenv("ANTHROPIC_MODEL")
	}
	if key == "" || model == "" {
		return nil, fmt.Errorf("%w: set ANTHROPIC_API_KEY and ANTHROPIC_MODEL (or ZZT_EVAL_JUDGE_MODEL)", ErrGenerationUnavailable)
	}
	apiURL := os.Getenv("ANTHROPIC_API_URL")
	if apiURL == "" {
		apiURL = "https://api.anthropic.com/v1/messages"
	}
	maxTokens := 2048
	if n, err := strconv.Atoi(os.Getenv("ZZT_EVAL_JUDGE_MAX_TOKENS")); err == nil && n > 0 {
		maxTokens = n
	}
	return &EvalJudge{
		apiURL: apiURL, apiKey: key, model: model, maxTokens: maxTokens,
		httpClient: &http.Client{Timeout: 2 * time.Minute},
	}, nil
}

// evalJudgeRubric returns the rubric section of the embedded EVAL.md, so the
// judge scores against the same text a human reads. A test guards presence.
func evalJudgeRubric() (string, error) {
	doc, err := evalDocFS.ReadFile("promptkit_assets/EVAL.md")
	if err != nil {
		return "", fmt.Errorf("embedded EVAL.md: %w", err)
	}
	_, rubric, found := strings.Cut(string(doc), "## Rubric")
	if !found {
		return "", fmt.Errorf("embedded EVAL.md has no '## Rubric' section")
	}
	return "## Rubric" + rubric, nil
}

const evalJudgeInstructions = `You are a strict quality judge for generated ZZT worlds (the 1991 DOS game).
You are given board screenshots, the world plan, and a sample of the world's
ZZT-OOP object code. Score the world against the rubric below.

Rules:
- Score every dimension 0-5 using the rubric's anchors. Judge only what you
  can see in the provided evidence; do not assume unshown boards are good.
- grounding-accuracy: when the run is marked as NOT grounded, set its score
  to -1 with justification "n/a (ungrounded run)".
- Answer with ONLY the JSON object in the rubric's schema. No prose before or
  after, no code fences.`

// Judge scores one world. It makes a single API call.
func (j *EvalJudge) Judge(ctx context.Context, req EvalJudgeRequest) (EvalVerdict, error) {
	rubric, err := evalJudgeRubric()
	if err != nil {
		return EvalVerdict{}, err
	}
	system := evalJudgeInstructions + "\n\n" + rubric

	type imageSource struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	}
	type block struct {
		Type   string       `json:"type"`
		Text   string       `json:"text,omitempty"`
		Source *imageSource `json:"source,omitempty"`
	}
	grounded := "NOT grounded (score grounding-accuracy -1)"
	if req.Grounded {
		grounded = "grounded (web research was enabled; score grounding-accuracy 0-5)"
	}
	header := fmt.Sprintf(
		"World name: %s\nPremise: %s\nRun: %s\n\n# World plan\n%s\n\n# OOP sample\n%s",
		req.WorldName, req.Premise, grounded, req.PlanText, req.OOPSample)
	blocks := []block{{Type: "text", Text: header}}
	for _, img := range req.Images {
		blocks = append(blocks,
			block{Type: "text", Text: "Screenshot: " + img.Label},
			block{Type: "image", Source: &imageSource{
				Type:      "base64",
				MediaType: "image/png",
				Data:      base64.StdEncoding.EncodeToString(img.PNG),
			}})
	}

	body, err := json.Marshal(struct {
		Model     string        `json:"model"`
		MaxTokens int           `json:"max_tokens"`
		System    []systemBlock `json:"system"`
		Messages  []struct {
			Role    string  `json:"role"`
			Content []block `json:"content"`
		} `json:"messages"`
	}{
		Model: j.model, MaxTokens: j.maxTokens,
		System: []systemBlock{{Type: "text", Text: system}},
		Messages: []struct {
			Role    string  `json:"role"`
			Content []block `json:"content"`
		}{{Role: "user", Content: blocks}},
	})
	if err != nil {
		return EvalVerdict{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, j.apiURL, strings.NewReader(string(body)))
	if err != nil {
		return EvalVerdict{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", j.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	resp, err := j.httpClient.Do(httpReq)
	if err != nil {
		return EvalVerdict{}, fmt.Errorf("judge API request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return EvalVerdict{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return EvalVerdict{}, fmt.Errorf("judge API returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var decoded struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return EvalVerdict{}, fmt.Errorf("decode judge API response: %w", err)
	}
	var text strings.Builder
	for _, b := range decoded.Content {
		if b.Type == "text" {
			text.WriteString(b.Text)
		}
	}
	return parseEvalVerdict(text.String())
}

// parseEvalVerdict extracts and validates the judge's JSON answer, tolerating
// stray prose or code fences around the object.
func parseEvalVerdict(text string) (EvalVerdict, error) {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return EvalVerdict{}, fmt.Errorf("judge answer contains no JSON object: %q", strings.TrimSpace(text))
	}
	var v EvalVerdict
	if err := json.Unmarshal([]byte(text[start:end+1]), &v); err != nil {
		return EvalVerdict{}, fmt.Errorf("judge answer is not valid JSON: %w", err)
	}
	byDim := map[string]bool{}
	for _, s := range v.Scores {
		if s.Score < -1 || s.Score > 5 {
			return EvalVerdict{}, fmt.Errorf("judge score %d for %q outside -1..5", s.Score, s.Dimension)
		}
		byDim[s.Dimension] = true
	}
	for _, dim := range EvalDimensions {
		if !byDim[dim] {
			return EvalVerdict{}, fmt.Errorf("judge answer missing dimension %q", dim)
		}
	}
	return v, nil
}
