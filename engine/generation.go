package zztgo

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GenerationService is the M12.4 plan-then-paint orchestrator. LLM responses
// remain text until they have passed the same compiler and headless validator
// used for authored ZWD; no model output is ever interpreted as code.
type GenerationService struct {
	apiURL       string
	apiKey       string
	model        string
	maxTokens    int
	maxAttempts  int
	outputDir    string
	httpClient   *http.Client
	promptKit    *PromptKit
	rateLimit    time.Duration
	sem          chan struct{}
	mu           sync.Mutex
	lastByClient map[string]time.Time
	progress     func(GenerationProgress)
	batchSize    int
}

type GenerationConfig struct {
	APIURL        string
	APIKey        string
	Model         string
	MaxTokens     int
	MaxAttempts   int
	MaxConcurrent int
	RateLimit     time.Duration
	OutputDir     string
	HTTPClient    *http.Client
	Progress      func(GenerationProgress)
	BatchSize     int
}

// GenerationProgress is emitted at every durable boundary in the plan-then-
// paint pipeline. M12.5 can render it directly; headless servers log it.
type GenerationProgress struct {
	Stage       string `json:"stage"`
	Board       string `json:"board,omitempty"`
	Index       int    `json:"index,omitempty"`
	Total       int    `json:"total,omitempty"`
	Attempt     int    `json:"attempt,omitempty"`
	MaxAttempts int    `json:"maxAttempts"`
	Detail      string `json:"detail,omitempty"`
}

type generationProgressContextKey struct{}

type GenerationResult struct {
	Name string
	Plan string
	ZWD  string
}

// ErrGenerationUnavailable is returned before making a network call when the
// server has not been configured with its Anthropic credentials.
var ErrGenerationUnavailable = fmt.Errorf("world generation is not configured")

func NewGenerationService(c GenerationConfig) (*GenerationService, error) {
	if c.APIKey == "" || c.Model == "" || c.MaxTokens <= 0 {
		return nil, fmt.Errorf("%w: set ANTHROPIC_API_KEY, ANTHROPIC_MODEL, and ANTHROPIC_MAX_TOKENS", ErrGenerationUnavailable)
	}
	if c.APIURL == "" {
		c.APIURL = "https://api.anthropic.com/v1/messages"
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 3
	}
	if c.MaxConcurrent <= 0 {
		c.MaxConcurrent = 2
	}
	if c.RateLimit == 0 {
		c.RateLimit = time.Minute
	}
	if c.OutputDir == "" {
		c.OutputDir = "."
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: 2 * time.Minute}
	}
	if c.BatchSize <= 0 {
		c.BatchSize = 1
	}
	kit, err := LoadPromptKit()
	if err != nil {
		return nil, err
	}
	return &GenerationService{
		apiURL: c.APIURL, apiKey: c.APIKey, model: c.Model, maxTokens: c.MaxTokens,
		maxAttempts: c.MaxAttempts, outputDir: c.OutputDir, httpClient: c.HTTPClient,
		promptKit: kit, rateLimit: c.RateLimit, sem: make(chan struct{}, c.MaxConcurrent),
		lastByClient: make(map[string]time.Time),
		progress:     c.Progress,
		batchSize:    c.BatchSize,
	}, nil
}

func (g *GenerationService) SetProgressReporter(report func(GenerationProgress)) {
	g.mu.Lock()
	g.progress = report
	g.mu.Unlock()
}

func (g *GenerationService) report(ctx context.Context, progress GenerationProgress) {
	// The browser needs the ceiling to narrate repair attempts faithfully (for
	// example, "attempt 2 of 3"). Keep it presentation-only: it does not alter
	// a generation decision or enter saved world state.
	if progress.MaxAttempts == 0 {
		progress.MaxAttempts = g.maxAttempts
	}
	if report, ok := ctx.Value(generationProgressContextKey{}).(func(GenerationProgress)); ok && report != nil {
		report(progress)
	}
	g.mu.Lock()
	report := g.progress
	g.mu.Unlock()
	if report != nil {
		report(progress)
	}
}

// GenerationServiceFromEnv is deliberately strict: generation is an optional
// server capability, and a half-configured service should fail before spending
// a request or writing a partial world.
func GenerationServiceFromEnv() (*GenerationService, error) {
	maxTokens, err := strconv.Atoi(os.Getenv("ANTHROPIC_MAX_TOKENS"))
	if err != nil || maxTokens <= 0 {
		return nil, fmt.Errorf("%w: set ANTHROPIC_MAX_TOKENS to a positive integer", ErrGenerationUnavailable)
	}
	c := GenerationConfig{
		APIURL:    os.Getenv("ANTHROPIC_API_URL"),
		APIKey:    os.Getenv("ANTHROPIC_API_KEY"),
		Model:     os.Getenv("ANTHROPIC_MODEL"),
		MaxTokens: maxTokens,
		OutputDir: os.Getenv("ZZT_GENERATED_DIR"),
	}
	if n, err := strconv.Atoi(os.Getenv("ZZT_GENERATION_ATTEMPTS")); err == nil && n > 0 {
		c.MaxAttempts = n
	}
	if n, err := strconv.Atoi(os.Getenv("ZZT_GENERATION_CONCURRENCY")); err == nil && n > 0 {
		c.MaxConcurrent = n
	}
	if seconds, err := strconv.Atoi(os.Getenv("ZZT_GENERATION_RATE_SECONDS")); err == nil && seconds >= 0 {
		c.RateLimit = time.Duration(seconds) * time.Second
	}
	if n, err := strconv.Atoi(os.Getenv("ZZT_GENERATION_BATCH_SIZE")); err == nil && n > 0 {
		c.BatchSize = n
	}
	return NewGenerationService(c)
}

func (g *GenerationService) Generate(ctx context.Context, client, premise, requestedName string, server *WebSocketServer, ground bool) (GenerationResult, error) {
	return g.generate(ctx, client, premise, requestedName, server, ground, nil)
}

// GenerateWithProgress runs the same production pipeline but adds a caller-
// scoped observer. It is used by asynchronous HTTP jobs without mixing events
// between concurrent clients.
func (g *GenerationService) GenerateWithProgress(ctx context.Context, client, premise, requestedName string, server *WebSocketServer, progress func(GenerationProgress), ground bool) (GenerationResult, error) {
	return g.generate(ctx, client, premise, requestedName, server, ground, progress)
}

func (g *GenerationService) generate(ctx context.Context, client, premise, requestedName string, server *WebSocketServer, ground bool, progress func(GenerationProgress)) (GenerationResult, error) {
	if progress != nil {
		ctx = context.WithValue(ctx, generationProgressContextKey{}, progress)
	}
	if strings.TrimSpace(premise) == "" {
		return GenerationResult{}, fmt.Errorf("prompt is required")
	}
	if len(premise) > 8000 {
		return GenerationResult{}, fmt.Errorf("prompt is longer than 8000 bytes")
	}
	if err := g.admit(ctx, client); err != nil {
		return GenerationResult{}, err
	}
	defer func() { <-g.sem }()

	g.report(ctx, GenerationProgress{Stage: "planning", Attempt: 1, Detail: "imagining the world plan"})
	planText, plan, err := g.makePlan(ctx, premise, ground)
	if err != nil {
		return GenerationResult{}, err
	}
	name, err := generatedSaveName(requestedName, plan.WorldName, premise)
	if err != nil {
		return GenerationResult{}, err
	}

	byID := make(map[string]PlanBoard, len(plan.Boards))
	for _, b := range plan.Boards {
		byID[strings.ToLower(b.ID)] = b
	}
	sections := make(map[string]string, len(plan.Boards))
	attempts := make(map[string]*int, len(plan.Boards))
	for _, board := range plan.Boards {
		count := 0
		attempts[board.Name] = &count
	}
	if g.batchSize <= 1 {
		for index, id := range plan.GenerationOrder {
			board, ok := byID[strings.ToLower(id)]
			if !ok {
				return GenerationResult{}, fmt.Errorf("plan generation order references unknown board %q", id)
			}
			g.report(ctx, GenerationProgress{Stage: "painting", Board: board.Name, Index: index + 1, Total: len(plan.GenerationOrder), Attempt: *attempts[board.Name] + 1})
			section, err := g.paintBoard(ctx, planText, plan, board, sections, attempts[board.Name], "")
			if err != nil {
				return GenerationResult{}, err
			}
			sections[board.Name] = section
		}
	} else {
		for i := 0; i < len(plan.GenerationOrder); i += g.batchSize {
			end := i + g.batchSize
			if end > len(plan.GenerationOrder) {
				end = len(plan.GenerationOrder)
			}
			var batchBoards []PlanBoard
			for _, id := range plan.GenerationOrder[i:end] {
				board, ok := byID[strings.ToLower(id)]
				if !ok {
					return GenerationResult{}, fmt.Errorf("plan generation order references unknown board %q", id)
				}
				batchBoards = append(batchBoards, board)
			}
			err := g.paintBoardsBatch(ctx, planText, plan, batchBoards, sections, attempts)
			if err != nil {
				return GenerationResult{}, err
			}
		}
	}

	var full string
	var data []byte
	var world TWorld
	for repairRound := 0; repairRound < g.maxAttempts; repairRound++ {
		g.report(ctx, GenerationProgress{Stage: "validating", Detail: "compiling and validating the assembled world"})
		full = assembleGeneratedZWD(name, plan, sections)
		data, err = CompileZWD(full)
		if err != nil {
			return GenerationResult{}, fmt.Errorf("assembled world did not compile: %w", translateZWDError(err, plan, sections))
		}
		if err := validateGeneratedZWD(data); err != nil {
			return GenerationResult{}, fmt.Errorf("assembled world did not validate: %w", err)
		}
		world, err = CompileZWDWorld(full)
		if err != nil {
			return GenerationResult{}, fmt.Errorf("assembled world did not compile: %w", translateZWDError(err, plan, sections))
		}
		problems := crossBoardProblems(plan, full)
		if len(problems) == 0 {
			break
		}
		if repairRound == g.maxAttempts-1 {
			return GenerationResult{}, fmt.Errorf("cross-board validation exhausted repairs: %s", formatGenerationProblems(problems))
		}
		for _, board := range orderedProblemBoards(plan, problems) {
			g.report(ctx, GenerationProgress{Stage: "repairing", Board: board.Name, Attempt: *attempts[board.Name] + 1, Detail: strings.Join(problems[board.Name], "; ")})
			section, err := g.paintBoard(ctx, planText, plan, board, sections, attempts[board.Name], strings.Join(problems[board.Name], "; "))
			if err != nil {
				return GenerationResult{}, err
			}
			sections[board.Name] = section
		}
	}

	// Bucket-2 detection (M12.16 folded bullet 3): one-way passage colors are
	// reported for visibility, never re-colored procedurally — a deliberately
	// one-way passage is legitimate, and the authoring rule prevents accidents
	// upstream. Informational only, so it never rejects an otherwise good world.
	if notes := CheckZWDPassageReciprocity(world); len(notes) > 0 {
		g.report(ctx, GenerationProgress{Stage: "validating", Detail: "passage reciprocity notes: " + strings.Join(notes, "; ")})
	}

	g.report(ctx, GenerationProgress{Stage: "persisting", Detail: "saving accepted world and sidecars"})
	if err := persistGeneratedWorld(g.outputDir, name, premise, planText, full, data); err != nil {
		return GenerationResult{}, err
	}
	if server != nil {
		if err := server.HostGeneratedWorld(name, world); err != nil {
			return GenerationResult{}, err
		}
	}
	g.report(ctx, GenerationProgress{Stage: "complete", Detail: name})
	return GenerationResult{Name: name, Plan: planText, ZWD: full}, nil
}

func (g *GenerationService) admit(ctx context.Context, client string) error {
	if client == "" {
		client = "unknown"
	}
	g.mu.Lock()
	if last := g.lastByClient[client]; g.rateLimit > 0 && time.Since(last) < g.rateLimit {
		g.mu.Unlock()
		return fmt.Errorf("generation rate limit: try again later")
	}
	g.lastByClient[client] = time.Now()
	g.mu.Unlock()
	select {
	case g.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *GenerationService) makePlan(ctx context.Context, premise string, ground bool) (string, Plan, error) {
	// Keep the planner's system prompt stable for caching; the only per-request
	// corpus material is this deterministic retrieval block. When grounding is on
	// the planner also gets the web_search tool (via callGrounded) and is told to
	// emit a Grounding notes section, so real facts ride into every board.
	retrieval := g.promptKit.RetrievalContext(premise, "world plan")
	buildRequest := func(repair string) string {
		base := planRequest(premise, repair)
		if ground {
			base += "\n\n" + groundingInstruction
		}
		return base + "\n\n" + retrieval
	}
	request := buildRequest("")
	var lastErr error
	for attempt := 1; attempt <= g.maxAttempts; attempt++ {
		g.report(ctx, GenerationProgress{Stage: "planning", Attempt: attempt, Detail: "asking Claude for a world plan"})
		var text string
		var err error
		if ground {
			text, err = g.callGrounded(ctx, plannerSystemPrompt, request)
		} else {
			text, err = g.call(ctx, plannerSystemPrompt, request)
		}
		if err != nil {
			return "", Plan{}, err
		}
		plan, err := ParsePlan(text)
		if err == nil {
			err = plan.Validate()
		}
		if err == nil {
			return text, plan, nil
		}
		lastErr = err
		if attempt < g.maxAttempts {
			g.report(ctx, GenerationProgress{Stage: "repairing-plan", Attempt: attempt + 1, Detail: err.Error()})
		}
		request = buildRequest(fmt.Sprintf("Your previous plan failed mechanical validation:\n%s\nReturn a corrected complete plan.", err))
	}
	return "", Plan{}, fmt.Errorf("plan generation exhausted repairs: %w", lastErr)
}

func (g *GenerationService) paintBoard(ctx context.Context, planText string, plan Plan, board PlanBoard, sections map[string]string, attempts *int, feedback string) (string, error) {
	lastFeedback := feedback
	lastCandidate := ""
	for *attempts < g.maxAttempts {
		*attempts++
		g.report(ctx, GenerationProgress{Stage: "painting", Board: board.Name, Attempt: *attempts, Detail: "asking Claude for board ZWD"})
		request := boardRequest(planText, plan, board, sections, lastFeedback, lastCandidate, *attempts, g.maxAttempts)
		if board.Index == 0 {
			request += "\n\n" + g.promptKit.TitleRetrievalContext(plan.WorldName)
		} else {
			request += "\n\n" + g.promptKit.RetrievalContext(plan.WorldName, board.Concept)
		}
		text, err := g.call(ctx, g.promptKit.SystemPrompt(), request)
		if err != nil {
			return "", err
		}
		lastCandidate = fencedGeneratedCandidate(text)
		section, parsed, warnings, err := extractGeneratedBoardWithWarnings(text, board.Name)
		if err == nil {
			candidate := cloneGeneratedSections(sections)
			candidate[board.Name] = section
			// Procedural repair (M12.16) self-heals bucket-1 defects before the
			// LLM is asked again; if it applies a fix, adopt the repaired board so
			// the final assembly and the next board's edge context see it.
			data, repairedSrc, repairDiags, compileErr := compileZWDBytesWithRepair(assembleGeneratedZWD("CHECK", plan, candidate))
			if compileErr == nil {
				compileErr = validateGeneratedZWD(data)
			}
			if compileErr == nil && parsed.name == board.Name {
				if len(repairDiags) > 0 {
					if repaired := boardSectionFromSource(repairedSrc, board.Name); repaired != "" {
						section = repaired
					}
					warnings = append(warnings, repairDiags...)
				}
				if len(warnings) > 0 {
					g.report(ctx, GenerationProgress{Stage: "validating", Board: board.Name, Attempt: *attempts, Detail: "preprocessor warnings: " + strings.Join(warnings, "; ")})
				}
				return section, nil
			}
			if compileErr != nil {
				err = translateZWDError(compileErr, plan, candidate)
			}
		}
		lastFeedback = fmt.Sprintf("Attempt %d failed: %v%s. Repair only board %q and return only its fenced ZWD board section.", *attempts, err, generatedGridDiagnostics(section, warnings...), board.Name)
		if *attempts < g.maxAttempts {
			g.report(ctx, GenerationProgress{Stage: "repairing", Board: board.Name, Attempt: *attempts + 1, Detail: err.Error()})
		}
	}
	return "", fmt.Errorf("board %q exhausted %d generation attempts: %s", board.Name, g.maxAttempts, lastFeedback)
}

type systemBlock struct {
	Type         string            `json:"type"`
	Text         string            `json:"text"`
	CacheControl map[string]string `json:"cache_control,omitempty"`
}

func (g *GenerationService) call(ctx context.Context, system, user string) (string, error) {
	body, err := json.Marshal(struct {
		Model     string        `json:"model"`
		MaxTokens int           `json:"max_tokens"`
		System    []systemBlock `json:"system"`
		Messages  []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}{
		Model: g.model, MaxTokens: g.maxTokens,
		System: []systemBlock{{
			Type: "text",
			Text: system,
			CacheControl: map[string]string{
				"type": "ephemeral",
			},
		}},
		Messages: []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}{{Role: "user", Content: user}},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.apiURL, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", g.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := g.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("Claude API request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("Claude API returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var decoded struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return "", fmt.Errorf("decode Claude API response: %w", err)
	}
	var text strings.Builder
	for _, block := range decoded.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	if text.Len() == 0 {
		return "", fmt.Errorf("Claude API response contained no text")
	}
	resultText := text.String()
	if os.Getenv("ZZT_GENERATION_DEBUG") == "1" {
		log.Printf("[DEBUG CLAUDE PROMPT]\n%s\n[DEBUG CLAUDE RESPONSE]\n%s\n", user, resultText)
	}
	return resultText, nil
}

// callGrounded is call() with the server-side web_search tool enabled and a
// bounded pause_turn resume loop (M12 opt-in grounding). It is used only for the
// planner step; per-board painting stays tool-free, offline, and deterministic.
func (g *GenerationService) callGrounded(ctx context.Context, system, user string) (string, error) {
	type msg struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	userContent, err := json.Marshal(user)
	if err != nil {
		return "", err
	}
	messages := []msg{{Role: "user", Content: userContent}}
	tool := map[string]interface{}{"type": "web_search_20260209", "name": "web_search", "max_uses": 5}
	// The web_search tool runs a server-side loop; it can return pause_turn if it
	// hits the server's iteration cap. Echo the assistant turn back a few times to
	// let it resume before giving up.
	for round := 0; round < 4; round++ {
		body, err := json.Marshal(map[string]interface{}{
			"model":      g.model,
			"max_tokens": g.maxTokens,
			"system":     []systemBlock{{Type: "text", Text: system}},
			"tools":      []interface{}{tool},
			"messages":   messages,
		})
		if err != nil {
			return "", err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.apiURL, strings.NewReader(string(body)))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", g.apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		resp, err := g.httpClient.Do(req)
		if err != nil {
			return "", fmt.Errorf("Claude API request: %w", err)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if err != nil {
			return "", err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", fmt.Errorf("Claude API returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
		}
		var decoded struct {
			StopReason string            `json:"stop_reason"`
			Content    []json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(data, &decoded); err != nil {
			return "", fmt.Errorf("decode Claude API response: %w", err)
		}
		if decoded.StopReason == "pause_turn" {
			assistantContent, err := json.Marshal(decoded.Content)
			if err != nil {
				return "", err
			}
			messages = append(messages, msg{Role: "assistant", Content: assistantContent})
			continue
		}
		var text strings.Builder
		for _, raw := range decoded.Content {
			var block struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(raw, &block) == nil && block.Type == "text" {
				text.WriteString(block.Text)
			}
		}
		if text.Len() == 0 {
			return "", fmt.Errorf("Claude API grounded response contained no text")
		}
		resultText := text.String()
		if os.Getenv("ZZT_GENERATION_DEBUG") == "1" {
			log.Printf("[DEBUG CLAUDE GROUNDED PROMPT]\n%s\n[DEBUG CLAUDE GROUNDED RESPONSE]\n%s\n", user, resultText)
		}
		return resultText, nil
	}
	return "", fmt.Errorf("grounded planner did not converge (too many web-search pause rounds)")
}

const plannerSystemPrompt = `You design compact, mechanically checkable ZZT world plans.

Emit Markdown in exactly this shape:

# World Plan: NAME

## Board graph

| # | id | name | concept | dark | exits/links |
|---|----|------|---------|------|-------------|
| 0 | title | Title Screen | title art | no | — |
| 1 | shopfront | Shop Front | START. opening scene | no | E→kitchen |
| 2 | kitchen | Kitchen Floor | puzzle room | no | W→shopfront |

## Progression spine

1. shopfront → kitchen (free). #endgame

## Generation order

shopfront → kitchen → title

Rules: every table row has all six cells; ids are lowercase one-token slugs;
names are display labels only; exits target ids, never display names; link
tokens are N→id, S→id, E→id, W→id, or passage→id/passage↔id. Include one
title row (0), exactly one START concept, reciprocal paths, and #endgame.`

// titleScreenBrief is appended to the board request for board 0 only. Without it
// the model paints the title like a gameplay room with a name stamped on top
// (scattered creatures, inconsistent letters, stray glyphs); a ZZT title screen
// is a decorative splash.
const titleScreenBrief = "\n\n# Title screen brief (board 0)\n" +
	"This is the title screen: a decorative splash shown before play, NOT a gameplay room. Follow the title-lettering examples above.\n" +
	"- Center the world's exact name as ONE monumental wordmark, spelled left to right in a single horizontal band near the top. Do not leave stray, duplicate, or half-formed letters anywhere else on the board.\n" +
	"- Give every letter the SAME height and even spacing, built from `Text-<color>` legend entries (each entry's color is the CP437 code of the glyph). Keep a small, coherent palette — a few colors, not a different color per letter unless deliberate.\n" +
	"- Do NOT scatter creatures, items, keys, gems, or furniture across the title. At most a thin border, a small decorative motif, or one short subtitle line beneath the wordmark.\n" +
	"- Place `start player` unobtrusively (a corner, or just below the wordmark). The title board has no combat.\n" +
	"- Leave generous empty space; a clean, readable wordmark beats a busy board. Do not draw menu instructions — the engine already shows Play / Restore / Quit.\n"

// groundingInstruction is appended to the planner request when the caller opts
// in to open-ended web grounding (M12). Only the planner step searches; the
// facts it gathers ride into every board through the plan text's Grounding notes.
const groundingInstruction = "# Web grounding (enabled)\n" +
	"You have a web_search tool. First research the real-world subject of the premise: search for accurate names, facts, events, tone, and iconic imagery, and base the world plan on what you find rather than on assumptions.\n" +
	"After the ## Generation order section, append a ## Grounding notes section: 4-8 terse prose bullets capturing the key real facts, proper names, tone, and iconic images a board author should honor. Use plain bullets only — no tables, no pipe (`|`) characters, no code fences — so the plan still parses."

func planRequest(premise, repair string) string {
	var b strings.Builder
	b.WriteString("Create a world plan for this player premise. Include ## Board graph as the six-column table used by the reference format, ## Progression spine, and ## Generation order. The `id` column is a lowercase single-token slug (for example `shopfront`), never a display name. In exits/links, targets are ids and every link is one compact token such as `E→kitchen`, `W→shopfront`, or `passage↔cellar`; do not use display names or prose as targets. Use board ids in generation order. The plan must have a title board (0), a START board, reciprocal directional exits or passages, and a reachable #endgame.\n\nPlayer premise:\n---\n")
	b.WriteString(premise)
	b.WriteString("\n---")
	if repair != "" {
		b.WriteString("\n\n")
		b.WriteString(repair)
	}
	return b.String()
}

func boardRequest(planText string, plan Plan, board PlanBoard, sections map[string]string, feedback, previous string, attempt, maxAttempts int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Paint exactly one board for this authoritative world plan. Board id=%q, required board name=%q, concept=%q, dark=%t. Output exactly one fenced zwd block containing only that board section. It must contain its own start player and use exact board names in exits and passages. Grid rows are byte-oriented: use only one-byte ASCII legend keys in every raw grid row, never literal Unicode or CP437 artwork. Use the Grid Alignment Protocol (wrapping grid rows in '|' characters and using 60-character numbered rulers above and below the grid) to ensure every grid row is exactly 60 bytes.\n\n# World plan\n%s\n\n# Already-painted adjacent board edges\n%s", board.ID, board.Name, board.Concept, board.Dark, planText, generatedEdgeContext(plan, board, sections))
	if board.Index == 0 {
		b.WriteString(titleScreenBrief)
	}
	if feedback != "" {
		fmt.Fprintf(&b, "\n\n# Repair required (attempt %d of %d)\n%s", attempt, maxAttempts, feedback)
		if previous != "" {
			fmt.Fprintf(&b, "\n\n# Previous failed candidate\nEdit this exact candidate. Preserve valid content and fix the named defects; do not repaint from scratch.\n```zwd\n%s\n```", strings.TrimSpace(previous))
		}
	}
	return b.String()
}

var fencedGeneratedBoardRe = regexp.MustCompile("(?s)^\\s*```zwd[ \\t]*\\r?\\n(.*?)\\r?\\n?```[ \\t]*\\s*$")
var multiFencedBoardRe = regexp.MustCompile("(?s)```zwd[ \\t]*\\r?\\n(.*?)\\r?\\n?```")

func fencedGeneratedCandidate(text string) string {
	m := fencedGeneratedBoardRe.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func extractGeneratedBoard(text, wantName string) (string, zwdBoard, error) {
	section, board, _, err := extractGeneratedBoardWithWarnings(text, wantName)
	return section, board, err
}

func extractGeneratedBoardWithWarnings(text, wantName string) (string, zwdBoard, []string, error) {
	m := fencedGeneratedBoardRe.FindStringSubmatch(text)
	if m == nil {
		return "", zwdBoard{}, nil, fmt.Errorf("model response must be exactly one fenced zwd block")
	}
	section := strings.TrimSpace(m[1]) + "\n"
	section, warnings := preprocessZWDGridWithWarnings(section)
	log.Printf("[DEBUG PREPROCESSED ZWD]\n%s\n", section)
	src := "zwd 1\nworld \"CHECK\"\n" + section
	if strings.HasPrefix(strings.TrimSpace(section), "zwd 1") {
		src = section
	}
	doc, err := newZWDParser(src).parse()
	if err != nil {
		return "", zwdBoard{}, warnings, err
	}
	if len(doc.boards) != 1 {
		return "", zwdBoard{}, warnings, fmt.Errorf("model response must contain exactly one board")
	}
	if doc.boards[0].name != wantName {
		return "", zwdBoard{}, warnings, fmt.Errorf("board is named %q; expected %q", doc.boards[0].name, wantName)
	}
	if strings.HasPrefix(strings.TrimSpace(section), "zwd 1") {
		// A model occasionally wraps the requested one-board section in a
		// complete document. It is still just data; accept it only when the
		// strict parser found exactly the requested one board.
		lines := strings.Split(section, "\n")
		start := -1
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "board ") {
				start = i
				break
			}
		}
		if start < 0 {
			return "", zwdBoard{}, warnings, fmt.Errorf("model response has no board section")
		}
		section = strings.Join(lines[start:], "\n")
	}
	return section, doc.boards[0], warnings, nil
}

func generatedGridDiagnostics(section string, warnings ...string) string {
	if section == "" {
		return ""
	}
	inGrid := false
	row := 0
	var problems []string
	for _, raw := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "grid" {
			inGrid = true
			row = 0
			continue
		}
		if !inGrid {
			continue
		}
		if trimmed == "end" {
			break
		}
		row++
		line := raw
		if strings.HasPrefix(line, "  ") {
			line = line[2:]
		}
		if len(line) != BOARD_WIDTH {
			// Build visual ruler
			var ruler1, ruler2 strings.Builder
			for i := 1; i <= len(line); i++ {
				if i%10 == 0 {
					ruler1.WriteString(fmt.Sprintf("%d", (i/10)%10))
				} else {
					ruler1.WriteByte(' ')
				}
				ruler2.WriteString(fmt.Sprintf("%d", i%10))
			}
			problems = append(problems, fmt.Sprintf("\n- grid row %d is %d bytes (every raw grid row must be exactly 60 bytes):\n```\n%s\n%s\n%s\n```",
				row, len(line), line, ruler1.String(), ruler2.String()))
		}
	}
	if len(warnings) > 0 {
		problems = append(problems, "\n- preprocessor warnings: "+strings.Join(warnings, "; "))
	}
	if len(problems) == 0 {
		return ""
	}
	return ";" + strings.Join(problems, ";")
}

// translateZWDError translates a line number in the assembled ZWD document
// back to a board name and line number within that board's section.
func translateZWDError(err error, plan Plan, sections map[string]string) error {
	zErr, ok := err.(*zwdError)
	if !ok || zErr.line <= 0 {
		return err
	}
	boards := append([]PlanBoard(nil), plan.Boards...)
	sort.Slice(boards, func(i, j int) bool { return boards[i].Index < boards[j].Index })
	currentLine := 4
	for _, board := range boards {
		section := sections[board.Name]
		if section == "" {
			section = generatedPlaceholderBoard(board.Name, board.Dark)
		}
		section = strings.TrimSpace(section)
		sectionLines := strings.Split(section, "\n")
		if zErr.line >= currentLine && zErr.line < currentLine+len(sectionLines) {
			localLine := zErr.line - currentLine + 1
			return fmt.Errorf("in board %q, line %d, col %d: %s", board.Name, localLine, zErr.col, zErr.msg)
		}
		currentLine += len(sectionLines) + 2
	}
	return err
}

// extractMultipleBoards extracts all boards defined in the LLM output.
// It returns a map from board name to its raw ZWD section text.
var boardHeaderRe = regexp.MustCompile(`(?m)^[ \t]*board\s+"([^"]+)"`)

func extractMultipleBoardsSplit(text string) map[string]string {
	sections, _ := extractMultipleBoardsSplitWithWarnings(text)
	return sections
}

func extractMultipleBoardsSplitWithWarnings(text string) (map[string]string, map[string][]string) {
	matches := multiFencedBoardRe.FindAllStringSubmatch(text, -1)
	var sections []string
	if len(matches) > 0 {
		for _, m := range matches {
			sections = append(sections, m[1])
		}
	} else {
		sections = []string{text}
	}

	result := make(map[string]string)
	warnings := make(map[string][]string)
	for _, sec := range sections {
		lines := strings.Split(sec, "\n")
		var currentBoardName string
		var currentBoardLines []string

		for _, line := range lines {
			headerMatch := boardHeaderRe.FindStringSubmatch(line)
			if len(headerMatch) > 0 {
				if currentBoardName != "" && len(currentBoardLines) > 0 {
					boardContent := strings.TrimSpace(strings.Join(currentBoardLines, "\n"))
					result[currentBoardName], warnings[currentBoardName] = preprocessZWDGridWithWarnings(boardContent)
				}
				currentBoardName = headerMatch[1]
				currentBoardLines = []string{line}
			} else {
				if currentBoardName != "" {
					currentBoardLines = append(currentBoardLines, line)
				}
			}
		}
		if currentBoardName != "" && len(currentBoardLines) > 0 {
			boardContent := strings.TrimSpace(strings.Join(currentBoardLines, "\n"))
			result[currentBoardName], warnings[currentBoardName] = preprocessZWDGridWithWarnings(boardContent)
		}
	}
	return result, warnings
}

// boardSectionFromSource returns the trimmed ZWD section for the named board out
// of a full-world source, using the same board-header split the generator uses.
// Empty when the board is absent. Used to lift a procedurally repaired board back
// out of the repaired full-world source (M12.16).
func boardSectionFromSource(src, name string) string {
	lines := strings.Split(src, "\n")
	start := -1
	for i, l := range lines {
		if m := boardHeaderRe.FindStringSubmatch(l); len(m) > 0 && m[1] == name {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if boardHeaderRe.MatchString(lines[i]) {
			end = i
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines[start:end], "\n"))
}

func extractMultipleBoards(text string) (map[string]string, error) {
	sections, _, err := extractMultipleBoardsWithWarnings(text)
	return sections, err
}

func extractMultipleBoardsWithWarnings(text string) (map[string]string, map[string][]string, error) {
	sections, warnings := extractMultipleBoardsSplitWithWarnings(text)
	if len(sections) == 0 {
		return nil, nil, fmt.Errorf("model response must contain at least one fenced zwd block")
	}
	return sections, warnings, nil
}

func batchBoardRequest(planText string, plan Plan, boards []PlanBoard, sections map[string]string, feedback string, previous map[string]string, attempt, maxAttempts int) string {
	var b strings.Builder
	b.WriteString("Paint the following boards for this authoritative world plan:\n")
	for _, board := range boards {
		fmt.Fprintf(&b, "- Board id=%q, required board name=%q, concept=%q, dark=%t\n", board.ID, board.Name, board.Concept, board.Dark)
	}
	b.WriteString("\nOutput a fenced zwd block for EACH board. Each board section must be complete, contain its own start player (if applicable), and use exact board names in exits and passages. Grid rows are byte-oriented: use only one-byte ASCII legend keys in every raw grid row, never literal Unicode or CP437 artwork. Use the Grid Alignment Protocol (wrapping grid rows in '|' characters and using 60-character numbered rulers above and below the grid) to ensure every grid row is exactly 60 bytes.\n")
	for _, board := range boards {
		if board.Index == 0 {
			b.WriteString(titleScreenBrief)
			break
		}
	}
	fmt.Fprintf(&b, "\n# World plan\n%s", planText)
	b.WriteString("\n\n# Already-painted adjacent board edges\n")
	for _, board := range boards {
		edges := generatedEdgeContext(plan, board, sections)
		if edges != "" {
			fmt.Fprintf(&b, "## Edges for board %q:\n%s\n", board.Name, edges)
		}
	}
	if feedback != "" {
		fmt.Fprintf(&b, "\n\n# Repair required (attempt %d of %d)\n%s", attempt, maxAttempts, feedback)
		if len(previous) > 0 {
			b.WriteString("\n\n# Previous failed candidates\nEdit these exact candidates. Preserve valid content and fix the named defects:\n")
			for name, prev := range previous {
				fmt.Fprintf(&b, "## Candidate for board %q:\n```zwd\n%s\n```\n", name, strings.TrimSpace(prev))
			}
		}
	}
	return b.String()
}

func (g *GenerationService) paintBoardsBatch(ctx context.Context, planText string, plan Plan, boards []PlanBoard, sections map[string]string, attempts map[string]*int) error {
	batchAttempt := 0
	lastFeedback := ""
	lastCandidates := make(map[string]string)
	for batchAttempt < g.maxAttempts {
		batchAttempt++
		boardNames := make([]string, len(boards))
		for i, b := range boards {
			boardNames[i] = b.Name
			*attempts[b.Name]++
		}
		detail := fmt.Sprintf("asking Claude for board batch ZWD: %s", strings.Join(boardNames, ", "))
		g.report(ctx, GenerationProgress{
			Stage:   "painting",
			Board:   boards[0].Name,
			Attempt: batchAttempt,
			Detail:  detail,
		})
		req := batchBoardRequest(planText, plan, boards, sections, lastFeedback, lastCandidates, batchAttempt, g.maxAttempts)
		text, err := g.call(ctx, g.promptKit.SystemPrompt(), req)
		if err != nil {
			return err
		}
		extracted, extractedWarnings, err := extractMultipleBoardsWithWarnings(text)
		if err == nil {
			allOk := true
			var batchErrors []string
			for _, board := range boards {
				section, ok := extracted[board.Name]
				if !ok {
					allOk = false
					batchErrors = append(batchErrors, fmt.Sprintf("board %q was missing in model response", board.Name))
					continue
				}
				lastCandidates[board.Name] = section
				src := section
				if !strings.HasPrefix(strings.TrimSpace(section), "zwd 1") {
					src = "zwd 1\nworld \"CHECK\"\n" + section + "\n"
				}
				doc, parseErr := newZWDParser(src).parse()
				if parseErr != nil {
					allOk = false
					var localErr error = parseErr
					if zErr, ok := parseErr.(*zwdError); ok {
						localLine := zErr.line
						if !strings.HasPrefix(strings.TrimSpace(section), "zwd 1") {
							localLine -= 3
						}
						localErr = fmt.Errorf("in board %q, line %d, col %d: %s", board.Name, localLine, zErr.col, zErr.msg)
					}
					batchErrors = append(batchErrors, fmt.Sprintf("board %q: %v%s", board.Name, localErr, generatedGridDiagnostics(section, extractedWarnings[board.Name]...)))
					continue
				}

				if len(doc.boards) != 1 {
					allOk = false
					batchErrors = append(batchErrors, fmt.Sprintf("board %q: section contains %d boards; expected 1", board.Name, len(doc.boards)))
					continue
				}
				parsed := doc.boards[0]
				if parsed.name != board.Name {
					allOk = false
					batchErrors = append(batchErrors, fmt.Sprintf("board %q: extracted board named %q instead", board.Name, parsed.name))
					continue
				}
				candidate := cloneGeneratedSections(sections)
				candidate[board.Name] = section
				data, repairedSrc, repairDiags, compileErr := compileZWDBytesWithRepair(assembleGeneratedZWD("CHECK", plan, candidate))
				if compileErr == nil {
					compileErr = validateGeneratedZWD(data)
				}
				if compileErr != nil {
					allOk = false
					diag := generatedGridDiagnostics(section, extractedWarnings[board.Name]...)
					translatedErr := translateZWDError(compileErr, plan, candidate)
					batchErrors = append(batchErrors, fmt.Sprintf("board %q failed validation: %v%s", board.Name, translatedErr, diag))
				} else if len(repairDiags) > 0 {
					// Procedural repair fixed this board (M12.16); commit the
					// repaired section instead of forcing an LLM repair round.
					if repaired := boardSectionFromSource(repairedSrc, board.Name); repaired != "" {
						extracted[board.Name] = repaired
					}
					extractedWarnings[board.Name] = append(extractedWarnings[board.Name], repairDiags...)
				}
			}
			if allOk {
				for _, board := range boards {
					sections[board.Name] = extracted[board.Name]
					if warnings := extractedWarnings[board.Name]; len(warnings) > 0 {
						g.report(ctx, GenerationProgress{Stage: "validating", Board: board.Name, Attempt: batchAttempt, Detail: "preprocessor warnings: " + strings.Join(warnings, "; ")})
					}
				}
				return nil
			}
			err = fmt.Errorf("batch validation failures: %s", strings.Join(batchErrors, "; "))
		}
		lastFeedback = err.Error()
		if batchAttempt < g.maxAttempts {
			g.report(ctx, GenerationProgress{
				Stage:   "repairing",
				Board:   boards[0].Name,
				Attempt: batchAttempt + 1,
				Detail:  lastFeedback,
			})
		}
	}
	return fmt.Errorf("batch %v exhausted %d generation attempts: %s", boards, g.maxAttempts, lastFeedback)
}

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

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
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
func preprocessZWDGridWithWarnings(zwdText string) (string, []string) {
	var warnings []string
	zwdText, warnings = autoCloseZWDSections(zwdText, warnings)
	// This preprocessor needs the same element metadata as the ZWD compiler to
	// recognize a legend's stat-backed elements and to derive their default
	// cycles.  Generation can call us before any world has been compiled.
	init := NewEngine()
	init.InitElementsGame()

	lines := strings.Split(zwdText, "\n")
	lines = deduplicateZWDLegendEntries(lines, &warnings)
	lines = dropUnknownZWDStatFields(lines, &warnings)

	playerChar := byte('@')
	emptyChar := byte('.')
	hasPlayerLegend := false

	startPlayerRe := regexp.MustCompile(`(?i)^\s*start\s+player\s+at\s+([0-9]+)\s*,\s*([0-9]+)`)

	var originalStartX, originalStartY int
	hasOriginalStart := false

	legendMap := make(map[byte]string)
	type zwdStatSpec struct {
		lineIdx int
		startX  int
		startY  int
		elem    string
		rest    string
	}
	var statSpecs []zwdStatSpec

	statRe := regexp.MustCompile(`(?i)^(\s*stat\s+at\s+)([0-9]+)\s*,\s*([0-9]+)(\s+element\s+)(\S+)(.*)`)

	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "=") {
			parts := strings.SplitN(trimmed, "=", 2)
			if len(parts) == 2 {
				k := strings.TrimSpace(parts[0])
				if len(k) == 1 {
					ch := k[0]
					elemName := parseLegendElemName(parts[1])
					if elemName != "" {
						legendMap[ch] = elemName
					}
				}
			}
		}
		if m := startPlayerRe.FindStringSubmatch(trimmed); m != nil {
			x, _ := strconv.Atoi(m[1])
			y, _ := strconv.Atoi(m[2])
			if x >= 1 && x <= 60 && y >= 1 && y <= 25 {
				originalStartX = x
				originalStartY = y
				hasOriginalStart = true
			}
		}
		if m := statRe.FindStringSubmatch(line); m != nil {
			x, _ := strconv.Atoi(m[2])
			y, _ := strconv.Atoi(m[3])
			statSpecs = append(statSpecs, zwdStatSpec{
				lineIdx: idx,
				startX:  x,
				startY:  y,
				elem:    m[5],
				rest:    m[6],
			})
		}
	}

	for ch, elemName := range legendMap {
		upper := strings.ToUpper(elemName)
		if upper == "PLAYER" || upper == "ELEMENT 1" || upper == "ELEMENT 01" {
			playerChar = ch
			hasPlayerLegend = true
		}
		if upper == "EMPTY" || upper == "ELEMENT 0" || upper == "ELEMENT 00" {
			emptyChar = ch
		}
	}

	modifiedLines := make(map[int]string)
	inGrid := false
	var out []string

	type gridRow struct {
		indent  string
		content string
	}
	var gridRows []gridRow
	var gridIndent string
	var firstRowIndent string
	var hasFirstRowIndent bool

	for lineIdx, line := range lines {
		if modLine, ok := modifiedLines[lineIdx]; ok {
			line = modLine
		}
		trimmed := strings.TrimSpace(line)
		if hasPlayerLegend && startPlayerRe.MatchString(trimmed) {
			continue
		}

		if trimmed == "grid" {
			inGrid = true
			out = append(out, line)
			gridRows = nil
			gridIndent = ""
			firstRowIndent = ""
			hasFirstRowIndent = false
			for _, r := range line {
				if r == ' ' || r == '\t' {
					gridIndent += string(r)
				} else {
					break
				}
			}
			continue
		}
		if inGrid {
			if trimmed == "end" {
				inGrid = false
				// Normalize gridRows to exactly 25 rows
				if len(gridRows) > 25 {
					gridRows = gridRows[:25]
				} else {
					padIndent := firstRowIndent
					if !hasFirstRowIndent {
						if gridIndent == "" {
							padIndent = "  "
						} else {
							padIndent = gridIndent + "  "
						}
					}
					for len(gridRows) < 25 {
						gridRows = append(gridRows, gridRow{
							indent:  padIndent,
							content: strings.Repeat(string(emptyChar), 60),
						})
					}
				}

				if hasPlayerLegend {
					// Find player positions in normalized gridRows
					type coord struct {
						r int // 0-indexed row
						c int // 0-indexed col
					}
					var playerCoords []coord
					for r, row := range gridRows {
						for c := 0; c < len(row.content); c++ {
							if row.content[c] == playerChar {
								playerCoords = append(playerCoords, coord{r: r, c: c})
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
							rowBytes := []byte(gridRows[p.r].content)
							rowBytes[p.c] = emptyChar
							gridRows[p.r].content = string(rowBytes)
						}
					} else {
						// len(playerCoords) == 0
						if hasOriginalStart {
							startX = originalStartX
							startY = originalStartY
						} else {
							startX = 30
							startY = 12
						}
						pRow := startY - 1
						pCol := startX - 1
						rowBytes := []byte(gridRows[pRow].content)
						rowBytes[pCol] = playerChar
						gridRows[pRow].content = string(rowBytes)
					}

					// Insert start player line before grid line
					gridIndex := -1
					for i := len(out) - 1; i >= 0; i-- {
						if strings.TrimSpace(out[i]) == "grid" {
							gridIndex = i
							break
						}
					}
					if gridIndex != -1 {
						indent := ""
						for _, r := range out[gridIndex] {
							if r == ' ' || r == '\t' {
								indent += string(r)
							} else {
								break
							}
						}
						startPlayerLine := fmt.Sprintf("%sstart player at %d,%d", indent, startX, startY)
						out = append(out[:gridIndex], append([]string{startPlayerLine}, out[gridIndex:]...)...)
					}
				}

				// Aligned stats block
				type coord struct {
					r int // 0-indexed row
					c int // 0-indexed col
				}
				gridElements := make(map[string][]coord)
				for r, row := range gridRows {
					for c := 0; c < len(row.content); c++ {
						ch := row.content[c]
						if elemName, ok := legendMap[ch]; ok {
							key := strings.ToUpper(elemName)
							gridElements[key] = append(gridElements[key], coord{r: r, c: c})
						}
					}
				}
				claimed := make(map[coord]bool)

				for _, spec := range statSpecs {
					key := strings.ToUpper(spec.elem)
					coords := gridElements[key]

					var bestCoord coord
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
						var repChar byte
						hasRep := false
						for ch, name := range legendMap {
							if strings.ToUpper(name) == key {
								repChar = ch
								hasRep = true
								break
							}
						}
						if !hasRep {
							ch := getUnusedLegendKey(legendMap)
							legendMap[ch] = spec.elem
							repChar = ch
							hasRep = true

							// Insert the new legend definition before the legend's "end" line
							legendEndIdx := -1
							inLegend := false
							for idx, line := range lines {
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
								for _, r := range lines[legendEndIdx] {
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
								lines[legendEndIdx] = newLegendEntry + "\n" + lines[legendEndIdx]
							}
						}
						if hasRep {
							alignedX = spec.startX
							alignedY = spec.startY

							pRow := alignedY - 1
							pCol := alignedX - 1
							if pRow >= 0 && pRow < 25 && pCol >= 0 && pCol < 60 {
								rowBytes := []byte(gridRows[pRow].content)
								rowBytes[pCol] = repChar
								gridRows[pRow].content = string(rowBytes)
								claimed[coord{r: pRow, c: pCol}] = true
							}
						} else {
							alignedX = spec.startX
							alignedY = spec.startY
						}
					}

					indent := ""
					for _, r := range lines[spec.lineIdx] {
						if r == ' ' || r == '\t' {
							indent += string(r)
						} else {
							break
						}
					}
					modifiedLines[spec.lineIdx] = fmt.Sprintf("%sstat at %d,%d element %s%s", indent, alignedX, alignedY, spec.elem, spec.rest)
				}

				// The other half of stat reconciliation: a generated grid can
				// contain a stat-backed glyph with no matching declaration.  Do
				// not make the model repeat its coordinate in a separate block;
				// derive it from this final, normalized grid instead.  claimed is
				// deliberately shared with the alignment pass above, so a declared
				// stat is never emitted twice.  Scanning rows first, then columns,
				// keeps synthesized-stat ordering deterministic.
				var synthesized []string
				for r, row := range gridRows {
					for c := 0; c < len(row.content); c++ {
						co := coord{r: r, c: c}
						if claimed[co] {
							continue
						}
						elemName, ok := legendMap[row.content[c]]
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
					statsEndIdx := zwdStatsEnd(lines, lineIdx)
					if statsEndIdx >= 0 {
						indent := leadingZWDIndent(lines[statsEndIdx])
						lines[statsEndIdx] = formatSynthesizedZWDStats(synthesized, indent+"  ") + lines[statsEndIdx]
					} else {
						indent := gridIndent
						newStatsBlock = append(newStatsBlock, indent+"stats")
						newStatsBlock = append(newStatsBlock, strings.Split(strings.TrimSuffix(formatSynthesizedZWDStats(synthesized, indent+"  "), "\n"), "\n")...)
						newStatsBlock = append(newStatsBlock, indent+"end")
					}
				}

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
				for _, l := range lines {
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
				for _, row := range gridRows {
					for i := 0; i < len(row.content); i++ {
						ch := row.content[i]
						if ch == playerChar || ch == emptyChar {
							continue
						}
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
				if len(undefinedChars) > 0 {
					// Find the legend's closing "end" line. The stat-alignment
					// block above may have already turned that element into a
					// multiline "entry\n...\nend" string, so match on its LAST
					// physical line.
					legendEndIdx := -1
					inLegend := false
					for idx, l := range lines {
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
						for _, r := range lines[legendEndIdx] {
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
								legendMap[ch] = "Empty"
							} else {
								fmt.Fprintf(&inject, "%s  cp437:0x%02X = Text-White color 0x%02X\n", indent, ch, ch)
								legendMap[ch] = "Text-White"
							}
						}
						lines[legendEndIdx] = inject.String() + lines[legendEndIdx]
					}
				}

				// Append the normalized rows to out using appropriate indentation
				for _, row := range gridRows {
					out = append(out, row.indent+row.content)
				}
				out = append(out, line)
				out = append(out, newStatsBlock...)
				continue
			}
			if strings.Contains(trimmed, "1234567890") {
				continue
			}
			indent := ""
			for _, r := range line {
				if r == ' ' || r == '\t' {
					indent += string(r)
				} else {
					break
				}
			}
			if !hasFirstRowIndent {
				firstRowIndent = indent
				hasFirstRowIndent = true
			}
			content := strings.TrimSpace(line)
			if strings.HasPrefix(content, "|") && strings.HasSuffix(content, "|") && len(content) >= 2 {
				content = content[1 : len(content)-1]
			}
			content = expandRLE(content)
			if len(content) > 60 {
				content = content[:60]
			} else if len(content) < 60 {
				content = content + strings.Repeat(string(emptyChar), 60-len(content))
			}
			gridRows = append(gridRows, gridRow{indent: indent, content: content})
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n"), warnings
}

func autoCloseZWDSections(src string, warnings []string) (string, []string) {
	lines := strings.Split(src, "\n")
	boardOpen := false
	section := ""
	inOOP := false
	sectionIndent := ""
	boardIndent := ""
	for _, line := range lines {
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

func assembleGeneratedZWD(worldName string, plan Plan, sections map[string]string) string {
	boards := append([]PlanBoard(nil), plan.Boards...)
	sort.Slice(boards, func(i, j int) bool { return boards[i].Index < boards[j].Index })
	var b strings.Builder
	fmt.Fprintf(&b, "zwd 1\nworld %q\n\n", worldName)
	for _, board := range boards {
		section := sections[board.Name]
		if section == "" {
			section = generatedPlaceholderBoard(board.Name, board.Dark)
		}
		// M12.20: guarantee a legible title. The model still paints board 0 to
		// the titleScreenBrief, but it draws letter-SHAPED clusters of Text tiles
		// that never resolve into the name (the #1 M12.17 baseline finding). The
		// pipeline already knows the name, so we STAMP it as one clean centered
		// row of literal Text glyphs and strip the model's stray title text —
		// derive, don't require (M12.13).
		if board.Index == 0 {
			section, _ = stampTitleWordmark(section, plan.WorldName)
		}
		b.WriteString(strings.TrimSpace(section))
		b.WriteString("\n\n")
	}
	return b.String()
}

// stampTitleWordmark rewrites a title board's ZWD section so the world name is
// spelled as one clean, centered row of literal Text-White glyphs and every
// other Text tile is stripped, leaving the title's non-text scenery (walls,
// borders, decorative objects) and its single player untouched. The evalTextRow
// / evalTitleWordmark gate reads the glyph out of each Text tile's Color byte,
// so one contiguous row of one-glyph-per-cell Text is exactly what "spells the
// world name" means. Block-letter fonts spread a name across five rows and so
// can never satisfy that single-row check — literal one-tile-per-letter is the
// only representation that passes, and the only one the model cannot garble.
//
// It works purely at the ZWD-text level (grid + legend surgery) so the persisted
// sidecar and the hosted world stay identical, and on any structural surprise it
// returns the section unchanged — the compiler remains the security boundary.
func stampTitleWordmark(section, displayName string) (string, []string) {
	var warnings []string
	wordmark := foldWordmark(displayName)
	if strings.TrimSpace(wordmark) == "" {
		return section, warnings
	}
	if len(wordmark) > int(BOARD_WIDTH) {
		return section, append(warnings, fmt.Sprintf("title wordmark %q is wider than %d columns; left unstamped", wordmark, BOARD_WIDTH))
	}

	lines := strings.Split(section, "\n")

	// Locate the first grid block and the first legend block.
	gridStart, gridEnd, legendStart, legendEnd := -1, -1, -1, -1
	inGrid, inLegend := false, false
	for i, l := range lines {
		t := strings.TrimSpace(l)
		switch {
		case t == "grid" && gridStart == -1:
			gridStart, inGrid = i, true
		case inGrid && t == "end":
			gridEnd, inGrid = i, false
		case t == "legend" && legendStart == -1 && !inGrid:
			legendStart, inLegend = i, true
		case inLegend && t == "end":
			legendEnd, inLegend = i, false
		}
	}
	if gridStart < 0 || gridEnd < 0 || legendStart < 0 || legendEnd < 0 {
		return section, append(warnings, "title board has no grid/legend block; left unstamped")
	}

	// Parse the legend: which single-byte keys map to Text elements (strip
	// targets), which are protected (player or stat-backed, never overwritten),
	// the Empty key (the gap glyph), and the full set of used key bytes.
	init := NewEngine()
	init.InitElementsGame()
	textKeys := map[byte]bool{}
	protectedKeys := map[byte]bool{}
	usedKeys := map[byte]bool{}
	emptyKey := byte(0)
	haveEmptyKey := false
	legendIndent := "    "
	for i := legendStart + 1; i < legendEnd; i++ {
		toks := strings.Fields(lines[i])
		if len(toks) < 3 || toks[1] != "=" {
			continue
		}
		if in := leadingIndent(lines[i]); in != "" {
			legendIndent = in
		}
		key, ok := zwdLegendKeyByte(toks[0])
		if !ok {
			continue
		}
		usedKeys[key] = true
		elem, ok := elementByZWDName(parseLegendElemName(strings.Join(toks[2:], " ")))
		if !ok {
			continue
		}
		switch {
		case elem >= E_TEXT_MIN && elem <= E_TEXT_WHITE:
			textKeys[key] = true
		case elem == E_PLAYER || elementNeedsStat(elem):
			protectedKeys[key] = true
		}
		if elem == E_EMPTY && !haveEmptyKey {
			emptyKey, haveEmptyKey = key, true
		}
	}
	if !haveEmptyKey {
		emptyKey = '.'
	}

	// Extract the 25 grid rows as (indent, cells).
	type gridRow struct {
		lineIdx int
		indent  string
		cells   []byte
	}
	var rows []gridRow
	for i := gridStart + 1; i < gridEnd; i++ {
		indent := leadingIndent(lines[i])
		rows = append(rows, gridRow{lineIdx: i, indent: indent, cells: []byte(strings.TrimRight(lines[i][len(indent):], "\r"))})
	}
	if len(rows) == 0 {
		return section, append(warnings, "title board grid is empty; left unstamped")
	}

	rowHasKind := func(cells []byte, kind map[byte]bool) bool {
		for _, c := range cells {
			if kind[c] {
				return true
			}
		}
		return false
	}

	// Choose the band row: the vertical center of the model's own lettering (so
	// the clean wordmark lands where the title was intended), else near the top.
	// Never a row that holds the player or a stat.
	var textRowIdx []int
	for r := range rows {
		if rowHasKind(rows[r].cells, textKeys) {
			textRowIdx = append(textRowIdx, r)
		}
	}
	band := 3
	if band >= len(rows) {
		band = len(rows) / 2
	}
	if len(textRowIdx) > 0 {
		band = textRowIdx[len(textRowIdx)/2]
	}
	if rowHasKind(rows[band].cells, protectedKeys) {
		band = -1
		for r := range rows {
			if !rowHasKind(rows[r].cells, protectedKeys) {
				band = r
				break
			}
		}
		if band < 0 {
			return section, append(warnings, "title board has no row free of the player/stats to stamp; left unstamped")
		}
	}

	// Allocate a fresh legend key per distinct wordmark glyph. Fresh keys never
	// collide with the model's legend, so this can never change what an existing
	// grid cell means. Prefer readable keys (letters, digits) before symbols.
	glyphKey := map[byte]byte{}
	var newLegend []string
	nextKey := func() (byte, bool) {
		try := func(lo, hi byte) (byte, bool) {
			for c := lo; c <= hi; c++ {
				if c == emptyKey || c == ' ' || c == '=' || c == '"' || usedKeys[c] {
					continue
				}
				return c, true
			}
			return 0, false
		}
		if k, ok := try('A', 'Z'); ok {
			return k, true
		}
		if k, ok := try('a', 'z'); ok {
			return k, true
		}
		if k, ok := try('0', '9'); ok {
			return k, true
		}
		return try(0x21, 0x7E)
	}
	ensureGlyphKey := func(glyph byte) (byte, bool) {
		if k, ok := glyphKey[glyph]; ok {
			return k, true
		}
		k, ok := nextKey()
		if !ok {
			return 0, false
		}
		usedKeys[k] = true
		glyphKey[glyph] = k
		newLegend = append(newLegend, fmt.Sprintf("%s%c = Text-White color 0x%02X", legendIndent, k, glyph))
		return k, true
	}

	// Build the centered band row: empty everywhere, one Text key per glyph.
	width := len(rows[band].cells)
	if width < int(BOARD_WIDTH) {
		width = int(BOARD_WIDTH)
	}
	newRow := make([]byte, width)
	for i := range newRow {
		newRow[i] = emptyKey
	}
	start := (width - len(wordmark)) / 2
	if start < 0 {
		start = 0
	}
	for i := 0; i < len(wordmark); i++ {
		if wordmark[i] == ' ' {
			continue // leave the gap as the Empty key
		}
		k, ok := ensureGlyphKey(wordmark[i])
		if !ok {
			return section, append(warnings, "title board legend is full; wordmark left unstamped")
		}
		newRow[start+i] = k
	}
	rows[band].cells = newRow

	// Strip every other Text tile so the wordmark is the only text row.
	for r := range rows {
		if r == band {
			continue
		}
		for c, ch := range rows[r].cells {
			if textKeys[ch] {
				rows[r].cells[c] = emptyKey
			}
		}
	}

	// Rewrite the grid rows in place, then splice the new legend entries in
	// before the legend's closing `end`.
	for _, row := range rows {
		lines[row.lineIdx] = row.indent + string(row.cells)
	}
	if len(newLegend) > 0 {
		out := make([]string, 0, len(lines)+len(newLegend))
		out = append(out, lines[:legendEnd]...)
		out = append(out, newLegend...)
		out = append(out, lines[legendEnd:]...)
		lines = out
	}
	warnings = append(warnings, fmt.Sprintf("stamped title wordmark %q on grid row %d", wordmark, band+1))
	return strings.Join(lines, "\n"), warnings
}

// leadingIndent returns the run of spaces/tabs at the start of s.
func leadingIndent(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\t' {
			return s[:i]
		}
	}
	return s
}

// zwdLegendKeyByte resolves a legend key token to its single grid byte. Handles
// a bare one-byte key and the cp437:0xNN escape; other tokens have no single
// grid byte.
func zwdLegendKeyByte(tok string) (byte, bool) {
	if len(tok) == 1 {
		return tok[0], true
	}
	if strings.HasPrefix(tok, "cp437:0x") || strings.HasPrefix(tok, "cp437:0X") {
		n, err := strconv.ParseUint(tok[len("cp437:0x"):], 16, 8)
		if err == nil {
			return byte(n), true
		}
	}
	return 0, false
}

func generatedPlaceholderBoard(name string, dark bool) string {
	rows := make([]string, BOARD_HEIGHT)
	rows[0] = "@" + strings.Repeat(".", BOARD_WIDTH-1)
	for i := 1; i < len(rows); i++ {
		rows[i] = strings.Repeat(".", BOARD_WIDTH)
	}
	return fmt.Sprintf("board %q\n  start player at 1,1\n  dark %t\n  exits north none south none west none east none\n  grid\n%s\n  end\n  legend\n    @ = Player color 0x1F\n    . = Empty color 0x00\n  end\nend", name, dark, strings.Join(rows, "\n"))
}

func cloneGeneratedSections(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func validateGeneratedZWD(data []byte) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("headless validation panicked: %v", r)
		}
	}()
	e := NewEngine()
	e.Headless = true
	e.VideoInstall()
	if err := e.worldReadFrom(strings.NewReader(string(data)), false, nil); err != nil {
		return fmt.Errorf("load compiled bytes: %w", err)
	}
	e.BoardOpen(0)
	e.BoardEnter(0)
	e.GameStateElement = E_PLAYER
	e.PlayerFor(0).Paused = false
	e.GamePlayExitRequested = false
	e.SetInputSource(&ScriptedInput{})
	for i := 0; i < 200; i++ {
		e.GameStep(nil)
		if e.GamePlayExitRequested {
			return fmt.Errorf("world requested exit at step %d", i+1)
		}
	}
	return nil
}

func generatedSaveName(requested, planName, premise string) (string, error) {
	if requested != "" {
		return SanitizeSaveName(requested)
	}

	// Attempt to clean and format the planName into an 8-character DOS-safe string
	var clean []byte
	for i := 0; i < len(planName); i++ {
		c := UpCase(planName[i])
		if (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' {
			clean = append(clean, c)
		}
	}

	if len(clean) > SaveNameMaxLength {
		clean = clean[:SaveNameMaxLength]
	}

	if len(clean) > 0 {
		if sanitized, err := SanitizeSaveName(string(clean)); err == nil {
			return sanitized, nil
		}
	}

	// Fallback to FNV hash if the cleaned name is empty or invalid
	h := fnv.New32a()
	_, _ = h.Write([]byte(planName + "\n" + premise))
	return SanitizeSaveName(fmt.Sprintf("GEN%05X", h.Sum32()&0xFFFFF))
}

func persistGeneratedWorld(dir, name, prompt, plan, zwd string, data []byte) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create generated-world directory: %w", err)
	}
	files := []struct {
		ext  string
		data []byte
	}{
		{ext: ".ZZT", data: data},
		{ext: ".prompt.txt", data: []byte(prompt + "\n")},
		{ext: ".plan.md", data: []byte(plan)},
		{ext: ".zwd", data: []byte(zwd)},
	}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(dir, name+file.ext), file.data, 0644); err != nil {
			return fmt.Errorf("persist generated world: %w", err)
		}
	}
	return nil
}

// generatedEdgeContext gives a board painter the literal grid edge of every
// already-painted neighbour. The symbols are accompanied by that neighbour's
// own legend in its section in the world plan prompt; the rows are primarily a
// geometric constraint so roads and walls arrive at the same coordinates.
func generatedEdgeContext(plan Plan, board PlanBoard, sections map[string]string) string {
	var lines []string
	for _, other := range plan.Boards {
		section := sections[other.Name]
		if section == "" {
			continue
		}
		_, parsed, err := extractGeneratedBoard("```zwd\n"+strings.TrimSpace(section)+"\n```", other.Name)
		if err != nil {
			continue
		}
		for _, link := range board.Links {
			if !strings.EqualFold(link.Target, other.ID) || link.Kind != "edge" {
				continue
			}
			lines = append(lines, fmt.Sprintf("%s is %s of this board; its facing edge is %q", other.Name, dirName(link.Dir), gridEdge(parsed, oppositeDir(link.Dir))))
		}
		for _, link := range other.Links {
			if !strings.EqualFold(link.Target, board.ID) || link.Kind != "edge" {
				continue
			}
			lines = append(lines, fmt.Sprintf("%s is %s of this board; its facing edge is %q", other.Name, dirName(oppositeDir(link.Dir)), gridEdge(parsed, link.Dir)))
		}
	}
	if len(lines) == 0 {
		return "No adjacent board has been painted yet."
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func gridEdge(board zwdBoard, dir string) string {
	switch dir {
	case "N":
		return board.grid[0].text
	case "S":
		return board.grid[len(board.grid)-1].text
	case "W":
		var b strings.Builder
		for _, row := range board.grid {
			b.WriteByte(row.text[0])
		}
		return b.String()
	case "E":
		var b strings.Builder
		for _, row := range board.grid {
			b.WriteByte(row.text[len(row.text)-1])
		}
		return b.String()
	}
	return ""
}

// crossBoardProblems is intentionally stricter than the compiler: it checks
// that the actual generated topology realizes the approved plan, and that
// progression promises survived the board-by-board prompts.
func crossBoardProblems(plan Plan, full string) map[string][]string {
	doc, err := newZWDParser(full).parse()
	if err != nil {
		return map[string][]string{"": {err.Error()}}
	}
	problems := make(map[string][]string)
	add := func(board, format string, args ...interface{}) {
		problems[board] = append(problems[board], fmt.Sprintf(format, args...))
	}

	actual := make(map[string]zwdBoard, len(doc.boards))
	for _, board := range doc.boards {
		actual[board.name] = board
	}
	byID := make(map[string]PlanBoard, len(plan.Boards))
	for _, board := range plan.Boards {
		byID[strings.ToLower(board.ID)] = board
	}

	for _, board := range plan.Boards {
		actualBoard, ok := actual[board.Name]
		if !ok {
			add(board.Name, "planned board is missing")
			continue
		}
		for _, link := range board.Links {
			target, ok := byID[strings.ToLower(link.Target)]
			if !ok {
				continue // plan validation reports this before painting.
			}
			if link.Kind == "edge" {
				idx := zwdExitIndex(link.Dir)
				if actualBoard.exits[idx] != target.Name {
					add(board.Name, "missing promised %s exit to %q", dirName(link.Dir), target.Name)
				}
				continue
			}
			if !hasPassageTarget(actualBoard, target.Name) {
				add(board.Name, "missing promised passage to %q", target.Name)
			}
			if link.Bidir {
				if targetBoard, ok := actual[target.Name]; !ok || !hasPassageTarget(targetBoard, board.Name) {
					add(target.Name, "missing promised return passage to %q", board.Name)
				}
			}
		}
	}

	for name, board := range actual {
		for i, target := range board.exits {
			if target == "" {
				continue
			}
			targetBoard, ok := actual[target]
			if !ok {
				add(name, "exit references missing board %q", target)
				continue
			}
			if targetBoard.exits[zwdOppositeIndex(i)] != name && !hasPassageTarget(targetBoard, name) {
				add(name, "exit to %q is not reciprocal", target)
			}
		}
	}

	for _, step := range plan.Spine {
		owner := spineBoardName(plan, step)
		for _, color := range step.Keys {
			if !worldHasKey(doc, color) {
				add(owner, "missing promised %s key", color)
			}
		}
		for _, flag := range step.FlagSets {
			if !worldSetsFlag(doc, flag) {
				add(owner, "missing promised flag %s (#set %s)", flag, flag)
			}
		}
	}
	return problems
}

func zwdExitIndex(dir string) int {
	switch dir {
	case "N":
		return 0
	case "S":
		return 1
	case "W":
		return 2
	case "E":
		return 3
	}
	return 0
}

func zwdOppositeIndex(index int) int {
	switch index {
	case 0:
		return 1
	case 1:
		return 0
	case 2:
		return 3
	default:
		return 2
	}
}

func hasPassageTarget(board zwdBoard, target string) bool {
	for _, entry := range board.legend {
		if entry.element == E_PASSAGE && entry.toBoard == target {
			return true
		}
	}
	return false
}

func worldHasKey(doc zwdDocument, color string) bool {
	want := 0
	for i, name := range ColorNames {
		if strings.EqualFold(color, name) {
			want = i + 1
			break
		}
	}
	if want == 0 {
		return false
	}
	for _, board := range doc.boards {
		for _, entry := range board.legend {
			if entry.element == E_KEY && int(entry.color)%8 == want {
				return true
			}
		}
	}
	return false
}

func worldSetsFlag(doc zwdDocument, flag string) bool {
	setRe := regexp.MustCompile(`(?i)(?:^|[\r\n])\s*#set\s+` + regexp.QuoteMeta(flag) + `(?:\s|$)`)
	for _, board := range doc.boards {
		for _, stat := range board.stats {
			if setRe.MatchString(stat.data) {
				return true
			}
		}
	}
	return false
}

func spineBoardName(plan Plan, step SpineStep) string {
	words := strings.FieldsFunc(strings.ToLower(step.Text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' && r != '-'
	})
	for _, word := range words {
		for _, board := range plan.Boards {
			if word == strings.ToLower(board.ID) {
				return board.Name
			}
		}
	}
	for _, board := range plan.Boards {
		if board.IsStart {
			return board.Name
		}
	}
	return plan.Boards[0].Name
}

func orderedProblemBoards(plan Plan, problems map[string][]string) []PlanBoard {
	var boards []PlanBoard
	for _, board := range plan.Boards {
		if len(problems[board.Name]) != 0 {
			boards = append(boards, board)
		}
	}
	sort.Slice(boards, func(i, j int) bool { return boards[i].Index < boards[j].Index })
	return boards
}

func formatGenerationProblems(problems map[string][]string) string {
	keys := make([]string, 0, len(problems))
	for board := range problems {
		keys = append(keys, board)
	}
	sort.Strings(keys)
	var lines []string
	for _, board := range keys {
		lines = append(lines, strings.TrimSpace(board+": "+strings.Join(problems[board], "; ")))
	}
	return strings.Join(lines, " | ")
}
