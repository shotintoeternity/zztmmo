package zztgo

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The ZWD preprocessor repairs model output before the compiler sees it, and
// what it does to a given input is the contract (M40.3). This pins that
// contract, as an output hash plus the warnings, over two corpora:
//
//   - fixtures/zwd_preprocess/inputs: board sections the generation and repair
//     tests feed preprocessZWDGridWithWarnings, many of them deliberately
//     malformed.
//   - every .zwd under fixtures/ and llmworld/examples, split into board sections
//     the way the generator splits a model response.
//
// Regenerate (only for an intended behaviour change, which then needs a
// DEVIATION: line in the commit) with ZZT_PREPROCESS_GOLDEN=1.

const zwdPreprocessGoldenPath = "../fixtures/zwd_preprocess/golden.json"

type zwdPreprocessGolden struct {
	Output   string   `json:"output"`
	Warnings []string `json:"warnings,omitempty"`
}

func zwdPreprocessCases(t *testing.T) map[string]zwdPreprocessGolden {
	t.Helper()
	got := map[string]zwdPreprocessGolden{}
	record := func(name, out string, warnings []string) {
		if _, dup := got[name]; dup {
			t.Fatalf("duplicate case %s", name)
		}
		got[name] = zwdPreprocessGolden{
			Output:   fmt.Sprintf("%x", sha256.Sum256([]byte(out))),
			Warnings: warnings,
		}
	}

	inputs, err := filepath.Glob("../fixtures/zwd_preprocess/inputs/*.txt")
	if err != nil || len(inputs) == 0 {
		t.Fatalf("no preprocess inputs (%v)", err)
	}
	for _, path := range inputs {
		out, warnings := preprocessZWDGridWithWarnings(string(mustRead(t, path)))
		record("input/"+filepath.Base(path), out, warnings)
	}

	var worlds []string
	for _, root := range []string{"../fixtures", "../llmworld/examples"} {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && strings.HasSuffix(path, ".zwd") {
				worlds = append(worlds, path)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(worlds)
	for _, path := range worlds {
		sections, warnings := extractMultipleBoardsSplitWithWarnings(string(mustRead(t, path)))
		for board, out := range sections {
			record("zwd/"+filepath.ToSlash(path)+"#"+board, out, warnings[board])
		}
	}
	return got
}

func TestZWDPreprocessMatchesGolden(t *testing.T) {
	got := zwdPreprocessCases(t)
	if os.Getenv("ZZT_PREPROCESS_GOLDEN") == "1" {
		data, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(zwdPreprocessGoldenPath, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %d cases", len(got))
		return
	}

	var want map[string]zwdPreprocessGolden
	if err := json.Unmarshal([]byte(mustRead(t, zwdPreprocessGoldenPath)), &want); err != nil {
		t.Fatalf("golden: %v", err)
	}
	if len(got) != len(want) {
		t.Errorf("%d cases, golden has %d", len(got), len(want))
	}
	names := make([]string, 0, len(want))
	for name := range want {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		g, ok := got[name]
		if !ok {
			t.Errorf("%s: missing", name)
			continue
		}
		w := want[name]
		if g.Output != w.Output {
			t.Errorf("%s: output changed", name)
		}
		if strings.Join(g.Warnings, "\n") != strings.Join(w.Warnings, "\n") {
			t.Errorf("%s: warnings changed\n got: %q\nwant: %q", name, g.Warnings, w.Warnings)
		}
	}
}
