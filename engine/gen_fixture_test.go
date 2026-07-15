//go:build canary

// Corpus/fixture generator (task M16.1): this test regenerates the committed
// fixtures/town_board1.zwd from an untracked engine-directory TOWN.ZZT. It is a
// maintainer generator, not an assertion, and writes a committed file, so it is
// kept behind the `canary` build tag and out of the required `go test ./...`
// path. The required, fail-closed comparison of that fixture lives in
// zwd_decompile_test.go's TestTOWNBoard1DecompiledFixture.
package zztgo

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestGenTownBoard1Fixture(t *testing.T) {
	data, err := os.ReadFile("TOWN.ZZT")
	if err != nil { t.Skip("no TOWN.ZZT") }
	e := NewEngine(); e.Headless = true; e.VideoInstall(); e.InitElementsGame()
	e.worldReadFrom(bytes.NewReader(data), false, nil)
	zwd := DecompileZWD(&e.World)
	// Extract board 1
	board1 := extractBoardSection(zwd, 1)
	if board1 == "" { t.Fatal("could not extract board 1") }
	_ = strings.TrimSpace(board1)
	if err := os.WriteFile("../fixtures/town_board1.zwd", []byte(board1), 0644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	t.Logf("wrote fixtures/town_board1.zwd (%d bytes)", len(board1))
}
