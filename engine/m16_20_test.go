package zztgo

// M16.20 — clean-clone certification and claim reconciliation.
//
// The certification run itself lives in `cmd/zzt-parity` (gate list, skip
// recording, report, run record) and in the manifest validator
// (parity_manifest_test.go). What belongs here is the one part of the evidence
// chain neither of those covered: the independent oracle's INPUTS.
//
// fixtures/oracle/provenance.json pins every hash a `V` claim rests on — the
// pinned ZZT.EXE and ZZT.DAT, the Zeta commit and frontend, and the sha256 of
// each committed scenario and oracle world. It was written by `make
// oracle-regen` and, until now, believed. A required fixture nobody re-hashes
// is a mutable required fixture, which M16.20's DoD forbids by name: edit an
// ORCL world to make a sweep agree with the engine and every capture derived
// from it still reads as independent ground truth.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type m1620Provenance struct {
	Program struct {
		ZZTExeSHA256 string `json:"zzt_exe_sha256"`
		ZZTDatSHA256 string `json:"zzt_dat_sha256"`
	} `json:"program"`
	Runner struct {
		Commit      string `json:"commit"`
		Frontend    string `json:"frontend"`
		FrontendSHA string `json:"frontend_sha256"`
	} `json:"runner"`
	Worlds    map[string]string `json:"worlds"`
	Scenarios map[string]string `json:"scenarios"`
}

// TestM1620OracleInputsMatchTheirPinnedHashes re-hashes every committed oracle
// input and compares it with provenance.json. A capture is only independent
// evidence if the world and schedule it was recorded from are the ones the
// provenance names.
func TestM1620OracleInputsMatchTheirPinnedHashes(t *testing.T) {
	dir := filepath.Join("..", "fixtures", "oracle")
	data, err := os.ReadFile(filepath.Join(dir, "provenance.json"))
	if err != nil {
		t.Fatalf("required parity fixture provenance.json is missing: %v", err)
	}
	var prov m1620Provenance
	if err := json.Unmarshal(data, &prov); err != nil {
		t.Fatalf("decoding provenance.json: %v", err)
	}
	if len(prov.Worlds) == 0 || len(prov.Scenarios) == 0 {
		t.Fatal("provenance.json pins no worlds or no scenarios")
	}

	for name, want := range prov.Worlds {
		checkHash(t, filepath.Join(dir, name), want)
	}
	for name, want := range prov.Scenarios {
		checkHash(t, filepath.Join(dir, name), want)
	}

	// The frontend is the other half of the runner: the emulator commit is a
	// remote fact this repo cannot re-derive, but the C file that drove it is
	// committed here and must still be the one that recorded the captures.
	checkHash(t, filepath.Join("..", prov.Runner.Frontend), prov.Runner.FrontendSHA)
	if prov.Program.ZZTExeSHA256 == "" || prov.Program.ZZTDatSHA256 == "" || prov.Runner.Commit == "" {
		t.Error("provenance.json does not pin the vanilla program and the emulator build it ran under")
	}
}

// TestM1620EveryOracleCaptureHasAPinnedScenario closes the other direction: a
// capture committed with no scenario behind it (or with one provenance does not
// pin) is evidence with no recorded origin.
func TestM1620EveryOracleCaptureHasAPinnedScenario(t *testing.T) {
	dir := filepath.Join("..", "fixtures", "oracle")
	data, err := os.ReadFile(filepath.Join(dir, "provenance.json"))
	if err != nil {
		t.Fatalf("reading provenance.json: %v", err)
	}
	var prov m1620Provenance
	if err := json.Unmarshal(data, &prov); err != nil {
		t.Fatalf("decoding provenance.json: %v", err)
	}

	captures, err := filepath.Glob(filepath.Join(dir, "*.capture.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(captures) == 0 {
		t.Fatal("no oracle captures are committed")
	}
	for _, capture := range captures {
		scenario := strings.TrimSuffix(filepath.Base(capture), ".capture.txt") + ".scn"
		if _, err := os.Stat(filepath.Join(dir, scenario)); err != nil {
			t.Errorf("capture %s has no committed scenario %s", filepath.Base(capture), scenario)
			continue
		}
		if _, ok := prov.Scenarios[scenario]; !ok {
			t.Errorf("scenario %s is committed but provenance.json does not pin it", scenario)
		}
	}
}

func checkHash(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("pinned oracle input %s is missing: %v", path, err)
		return
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		t.Errorf("%s hashes to %s; provenance.json pins %s — a required oracle input changed without a regeneration",
			path, got, want)
	}
}
