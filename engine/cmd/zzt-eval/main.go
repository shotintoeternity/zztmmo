// Command zzt-eval is the M12.17 tier-2 live quality pass: for each premise
// in the documented set (llmworld/EVAL.md), with and without web grounding,
// it generates a world, runs the tier-1 structural gate, renders the title and
// the first two gameplay boards to PNG, scores the world against the EVAL.md
// rubric with an LLM judge, and writes a scored Markdown report embedding the
// screenshots.
//
// Owner-run only — it spends API on every generation and judge call; it is
// never wired into CI. Generated worlds and their prompt/plan/ZWD sidecars are
// persisted under -out; nothing is hosted.
//
// Usage:
//
//	go run ./cmd/zzt-eval -out ../llmworld/eval/baseline
//	go run ./cmd/zzt-eval -premise "a tiny lighthouse mystery" -ground off
//	go run ./cmd/zzt-eval -skip-judge   # objective tier-1 gate only
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/benhoyt/zztgo"
)

// defaultPremises mirrors the documented premise set in llmworld/EVAL.md:
// a real-world grounded topic, an abstract theme, and a genre pastiche.
var defaultPremises = []string{
	"the 1969 Apollo 11 moon landing, from launch to splashdown",
	"a dream about slowly forgetting someone you loved",
	"a classic haunted castle adventure with locked doors, a dark dungeon, and a vampire lord",
}

type premiseList []string

func (p *premiseList) String() string     { return strings.Join(*p, "; ") }
func (p *premiseList) Set(v string) error { *p = append(*p, v); return nil }

func loadEnv() {
	for _, path := range []string{"../../../.env", "../../.env", "../.env", ".env"} {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(content), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if key, val, ok := strings.Cut(line, "="); ok {
				os.Setenv(strings.TrimSpace(key), strings.TrimSpace(val))
			}
		}
		return
	}
	log.Printf("Warning: .env not found, using existing environment variables")
}

func main() {
	var premises premiseList
	outDir := flag.String("out", "eval-out", "output directory for the report, screenshots, and generated worlds")
	ground := flag.String("ground", "both", "grounding runs: both, on, or off")
	skipJudge := flag.Bool("skip-judge", false, "run only the objective tier-1 gate (no judge API calls)")
	attempts := flag.Int("attempts", 5, "per-board repair attempts")
	flag.Var(&premises, "premise", "premise to evaluate (repeatable; default is the EVAL.md set)")
	flag.Parse()
	loadEnv()

	if len(premises) == 0 {
		premises = defaultPremises
	}
	var grounds []bool
	switch *ground {
	case "both":
		grounds = []bool{false, true}
	case "on":
		grounds = []bool{true}
	case "off":
		grounds = []bool{false}
	default:
		log.Fatalf("-ground must be both, on, or off (got %q)", *ground)
	}

	if err := run(premises, grounds, *outDir, *skipJudge, *attempts); err != nil {
		log.Fatal(err)
	}
}

func run(premises []string, grounds []bool, outDir string, skipJudge bool, attempts int) error {
	worldsDir := filepath.Join(outDir, "worlds")
	shotsDir := filepath.Join(outDir, "shots")
	for _, dir := range []string{worldsDir, shotsDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	service, err := generationService(worldsDir, attempts)
	if err != nil {
		return err
	}
	var judge *zztgo.EvalJudge
	if !skipJudge {
		if judge, err = zztgo.EvalJudgeFromEnv(); err != nil {
			return err
		}
	}

	var results []runResult
	writeOut := func() error {
		report := filepath.Join(outDir, "report.md")
		f, err := os.Create(report)
		if err != nil {
			return err
		}
		defer f.Close()
		return writeReport(f, results)
	}

	for _, premise := range premises {
		for _, grounded := range grounds {
			label := runLabel(premise, grounded)
			fmt.Printf("=== %s\n", label)
			res := evaluateOne(service, judge, premise, grounded, label, shotsDir)
			results = append(results, res)
			// Write after every run so a mid-run failure keeps partial results.
			if err := writeOut(); err != nil {
				return err
			}
		}
	}
	fmt.Printf("Report: %s\n", filepath.Join(outDir, "report.md"))
	return nil
}

// generationService builds the production generation pipeline from the same
// env vars the server uses, minus the per-client rate limit (each eval run is
// sequential and deliberate).
func generationService(worldsDir string, attempts int) (*zztgo.GenerationService, error) {
	maxTokens, err := strconv.Atoi(os.Getenv("ANTHROPIC_MAX_TOKENS"))
	if err != nil || maxTokens <= 0 {
		return nil, fmt.Errorf("set ANTHROPIC_MAX_TOKENS to a positive integer")
	}
	batch := 1
	if n, err := strconv.Atoi(os.Getenv("ZZT_GENERATION_BATCH_SIZE")); err == nil && n > 0 {
		batch = n
	}
	return zztgo.NewGenerationService(zztgo.GenerationConfig{
		APIURL:      os.Getenv("ANTHROPIC_API_URL"),
		APIKey:      os.Getenv("ANTHROPIC_API_KEY"),
		Model:       os.Getenv("ANTHROPIC_MODEL"),
		MaxTokens:   maxTokens,
		MaxAttempts: attempts,
		RateLimit:   time.Nanosecond, // sequential runs pace themselves
		OutputDir:   worldsDir,
		BatchSize:   batch,
		// The service default (2 minutes) is too tight for a grounded planner
		// call: server-side web_search runs inside the request and can hold
		// the response past it. Eval runs are patient.
		HTTPClient: &http.Client{Timeout: 10 * time.Minute},
		Progress: func(p zztgo.GenerationProgress) {
			if p.Board != "" {
				fmt.Printf("  [%s] %s (attempt %d/%d) %s\n", p.Stage, p.Board, p.Attempt, p.MaxAttempts, p.Detail)
			} else {
				fmt.Printf("  [%s] %s\n", p.Stage, p.Detail)
			}
		},
	})
}

func evaluateOne(service *zztgo.GenerationService, judge *zztgo.EvalJudge, premise string, grounded bool, label, shotsDir string) runResult {
	res := runResult{Premise: premise, Grounded: grounded, Label: label}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()

	result, err := service.Generate(ctx, "zzt-eval:"+label, premise, "", nil, grounded)
	if err != nil {
		res.GenError = err.Error()
		return res
	}
	res.WorldName = result.Name

	displayName := result.Name
	if plan, err := zztgo.ParsePlan(result.Plan); err == nil && plan.WorldName != "" {
		displayName = plan.WorldName
	}
	res.DisplayName = displayName
	res.Gate = zztgo.EvalGeneratedZWD(result.ZWD, displayName)

	res.Shots = captureShots(result.ZWD, label, shotsDir)

	if judge != nil {
		verdict, err := judgeWorld(ctx, judge, premise, grounded, displayName, result, res.Shots, shotsDir)
		if err != nil {
			res.JudgeError = err.Error()
		} else {
			res.Verdict = &verdict
		}
	}
	return res
}

// captureShots renders board 0 and the first two gameplay boards. A render
// failure becomes a labeled note instead of aborting the run.
func captureShots(zwd, label, shotsDir string) []shotRef {
	world, err := zztgo.CompileZWDWorld(zwd)
	if err != nil {
		return []shotRef{{Label: "render failed", Err: err.Error()}}
	}
	boards := []int16{0}
	for b := int16(1); b <= world.BoardCount && len(boards) < 3; b++ {
		boards = append(boards, b)
	}
	var shots []shotRef
	for _, b := range boards {
		name := fmt.Sprintf("%s-board%d.png", label, b)
		shotLabel := fmt.Sprintf("board %d", b)
		if b == 0 {
			shotLabel = "title (board 0)"
		}
		png, err := zztgo.RenderZWDBoardPNG(zwd, b)
		if err != nil {
			shots = append(shots, shotRef{Label: shotLabel, Err: err.Error()})
			continue
		}
		if err := os.WriteFile(filepath.Join(shotsDir, name), png, 0644); err != nil {
			shots = append(shots, shotRef{Label: shotLabel, Err: err.Error()})
			continue
		}
		shots = append(shots, shotRef{Label: shotLabel, Path: filepath.Join("shots", name)})
	}
	return shots
}

func judgeWorld(ctx context.Context, judge *zztgo.EvalJudge, premise string, grounded bool, displayName string, result zztgo.GenerationResult, shots []shotRef, shotsDir string) (zztgo.EvalVerdict, error) {
	oop, err := zztgo.EvalOOPSample(result.ZWD, 6000)
	if err != nil {
		oop = "(unavailable: " + err.Error() + ")"
	}
	req := zztgo.EvalJudgeRequest{
		Premise:   premise,
		Grounded:  grounded,
		WorldName: displayName,
		PlanText:  result.Plan,
		OOPSample: oop,
	}
	for _, shot := range shots {
		if shot.Path == "" {
			continue
		}
		png, err := os.ReadFile(filepath.Join(filepath.Dir(shotsDir), shot.Path))
		if err != nil {
			continue
		}
		req.Images = append(req.Images, zztgo.EvalJudgeImage{Label: shot.Label, PNG: png})
	}
	return judge.Judge(ctx, req)
}

func runLabel(premise string, grounded bool) string {
	words := strings.Fields(strings.ToLower(premise))
	var keep []string
	for _, w := range words {
		w = strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, w)
		if len(w) >= 4 {
			keep = append(keep, w)
		}
		if len(keep) == 3 {
			break
		}
	}
	if len(keep) == 0 {
		keep = []string{"premise"}
	}
	suffix := "plain"
	if grounded {
		suffix = "grounded"
	}
	return strings.Join(keep, "-") + "-" + suffix
}
