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
	Stage   string
	Board   string
	Index   int
	Total   int
	Attempt int
	Detail  string
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

func (g *GenerationService) Generate(ctx context.Context, client, premise, requestedName string, server *WebSocketServer) (GenerationResult, error) {
	return g.generate(ctx, client, premise, requestedName, server, nil)
}

// GenerateWithProgress runs the same production pipeline but adds a caller-
// scoped observer. It is used by asynchronous HTTP jobs without mixing events
// between concurrent clients.
func (g *GenerationService) GenerateWithProgress(ctx context.Context, client, premise, requestedName string, server *WebSocketServer, progress func(GenerationProgress)) (GenerationResult, error) {
	return g.generate(ctx, client, premise, requestedName, server, progress)
}

func (g *GenerationService) generate(ctx context.Context, client, premise, requestedName string, server *WebSocketServer, progress func(GenerationProgress)) (GenerationResult, error) {
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
	planText, plan, err := g.makePlan(ctx, premise)
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

func (g *GenerationService) makePlan(ctx context.Context, premise string) (string, Plan, error) {
	request := planRequest(premise, "")
	var lastErr error
	for attempt := 1; attempt <= g.maxAttempts; attempt++ {
		g.report(ctx, GenerationProgress{Stage: "planning", Attempt: attempt, Detail: "asking Claude for a world plan"})
		text, err := g.call(ctx, plannerSystemPrompt, request)
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
		g.report(ctx, GenerationProgress{Stage: "repairing-plan", Attempt: attempt, Detail: err.Error()})
		request = planRequest(premise, fmt.Sprintf("Your previous plan failed mechanical validation:\n%s\nReturn a corrected complete plan.", err))
	}
	return "", Plan{}, fmt.Errorf("plan generation exhausted repairs: %w", lastErr)
}

func (g *GenerationService) paintBoard(ctx context.Context, planText string, plan Plan, board PlanBoard, sections map[string]string, attempts *int, feedback string) (string, error) {
	lastFeedback := feedback
	lastCandidate := ""
	for *attempts < g.maxAttempts {
		*attempts++
		g.report(ctx, GenerationProgress{Stage: "painting", Board: board.Name, Attempt: *attempts, Detail: "asking Claude for board ZWD"})
		text, err := g.call(ctx, g.promptKit.SystemPrompt(), boardRequest(planText, plan, board, sections, lastFeedback, lastCandidate, *attempts, g.maxAttempts))
		if err != nil {
			return "", err
		}
		lastCandidate = fencedGeneratedCandidate(text)
		section, parsed, err := extractGeneratedBoard(text, board.Name)
		if err == nil {
			candidate := cloneGeneratedSections(sections)
			candidate[board.Name] = section
			data, compileErr := CompileZWD(assembleGeneratedZWD("CHECK", plan, candidate))
			if compileErr == nil {
				compileErr = validateGeneratedZWD(data)
			}
			if compileErr == nil && parsed.name == board.Name {
				return section, nil
			}
			if compileErr != nil {
				err = translateZWDError(compileErr, plan, candidate)
			}
		}
		lastFeedback = fmt.Sprintf("Attempt %d failed: %v%s. Repair only board %q and return only its fenced ZWD board section.", *attempts, err, generatedGridDiagnostics(section), board.Name)
		g.report(ctx, GenerationProgress{Stage: "repairing", Board: board.Name, Attempt: *attempts, Detail: err.Error()})
	}
	return "", fmt.Errorf("board %q exhausted %d generation attempts: %s", board.Name, g.maxAttempts, lastFeedback)
}

func (g *GenerationService) call(ctx context.Context, system, user string) (string, error) {
	body, err := json.Marshal(struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		System    string `json:"system"`
		Messages  []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}{
		Model: g.model, MaxTokens: g.maxTokens, System: system,
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
	m := fencedGeneratedBoardRe.FindStringSubmatch(text)
	if m == nil {
		return "", zwdBoard{}, fmt.Errorf("model response must be exactly one fenced zwd block")
	}
	section := strings.TrimSpace(m[1]) + "\n"
	section = preprocessZWDGrid(section)
	src := "zwd 1\nworld \"CHECK\"\n" + section
	if strings.HasPrefix(strings.TrimSpace(section), "zwd 1") {
		src = section
	}
	doc, err := newZWDParser(src).parse()
	if err != nil {
		return "", zwdBoard{}, err
	}
	if len(doc.boards) != 1 {
		return "", zwdBoard{}, fmt.Errorf("model response must contain exactly one board")
	}
	if doc.boards[0].name != wantName {
		return "", zwdBoard{}, fmt.Errorf("board is named %q; expected %q", doc.boards[0].name, wantName)
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
			return "", zwdBoard{}, fmt.Errorf("model response has no board section")
		}
		section = strings.Join(lines[start:], "\n")
	}
	return section, doc.boards[0], nil
}

func generatedGridDiagnostics(section string) string {
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
	for _, sec := range sections {
		sec = preprocessZWDGrid(sec)
		lines := strings.Split(sec, "\n")
		var currentBoardName string
		var currentBoardLines []string
		
		for _, line := range lines {
			headerMatch := boardHeaderRe.FindStringSubmatch(line)
			if len(headerMatch) > 0 {
				if currentBoardName != "" && len(currentBoardLines) > 0 {
					result[currentBoardName] = strings.TrimSpace(strings.Join(currentBoardLines, "\n"))
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
			result[currentBoardName] = strings.TrimSpace(strings.Join(currentBoardLines, "\n"))
		}
	}
	return result
}

func extractMultipleBoards(text string) (map[string]string, error) {
	sections := extractMultipleBoardsSplit(text)
	if len(sections) == 0 {
		return nil, fmt.Errorf("model response must contain at least one fenced zwd block")
	}
	return sections, nil
}

func batchBoardRequest(planText string, plan Plan, boards []PlanBoard, sections map[string]string, feedback string, previous map[string]string, attempt, maxAttempts int) string {
	var b strings.Builder
	b.WriteString("Paint the following boards for this authoritative world plan:\n")
	for _, board := range boards {
		fmt.Fprintf(&b, "- Board id=%q, required board name=%q, concept=%q, dark=%t\n", board.ID, board.Name, board.Concept, board.Dark)
	}
	b.WriteString("\nOutput a fenced zwd block for EACH board. Each board section must be complete, contain its own start player (if applicable), and use exact board names in exits and passages. Grid rows are byte-oriented: use only one-byte ASCII legend keys in every raw grid row, never literal Unicode or CP437 artwork. Use the Grid Alignment Protocol (wrapping grid rows in '|' characters and using 60-character numbered rulers above and below the grid) to ensure every grid row is exactly 60 bytes.\n")
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
		extracted, err := extractMultipleBoards(text)
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
					diag := generatedGridDiagnostics(section)
					var localErr error = parseErr
					if zErr, ok := parseErr.(*zwdError); ok {
						localLine := zErr.line
						if !strings.HasPrefix(strings.TrimSpace(section), "zwd 1") {
							localLine -= 3
						}
						localErr = fmt.Errorf("in board %q, line %d, col %d: %s", board.Name, localLine, zErr.col, zErr.msg)
					}
					batchErrors = append(batchErrors, fmt.Sprintf("board %q: %v%s", board.Name, localErr, diag))
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
				data, compileErr := CompileZWD(assembleGeneratedZWD("CHECK", plan, candidate))
				if compileErr == nil {
					compileErr = validateGeneratedZWD(data)
				}
				if compileErr != nil {
					allOk = false
					diag := generatedGridDiagnostics(section)
					translatedErr := translateZWDError(compileErr, plan, candidate)
					batchErrors = append(batchErrors, fmt.Sprintf("board %q failed validation: %v%s", board.Name, translatedErr, diag))
				}
			}
			if allOk {
				for _, board := range boards {
					sections[board.Name] = extracted[board.Name]
				}
				return nil
			}
			err = fmt.Errorf("batch validation failures: %s", strings.Join(batchErrors, "; "))
		}
		lastFeedback = err.Error()
		g.report(ctx, GenerationProgress{
			Stage:   "repairing",
			Board:   boards[0].Name,
			Attempt: batchAttempt,
			Detail:  lastFeedback,
		})
	}
	return fmt.Errorf("batch %v exhausted %d generation attempts: %s", boards, g.maxAttempts, lastFeedback)
}

func preprocessZWDGrid(zwdText string) string {
	lines := strings.Split(zwdText, "\n")
	inGrid := false
	var out []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "grid" {
			inGrid = true
			out = append(out, line)
			continue
		}
		if inGrid {
			if trimmed == "end" {
				inGrid = false
				out = append(out, line)
				continue
			}
			if strings.Contains(trimmed, "1234567890") {
				continue
			}
			row := line
			indent := ""
			for _, r := range row {
				if r == ' ' || r == '\t' {
					indent += string(r)
				} else {
					break
				}
			}
			content := strings.TrimSpace(row)
			if strings.HasPrefix(content, "|") && strings.HasSuffix(content, "|") && len(content) >= 2 {
				gridContent := content[1 : len(content)-1]
				row = indent + gridContent
			}
			out = append(out, row)
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
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
		b.WriteString(strings.TrimSpace(section))
		b.WriteString("\n\n")
	}
	return b.String()
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
