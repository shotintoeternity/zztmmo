//go:build canary

// Extended-world canary tests (task M16.1).
//
// These round-trip large community worlds (CAVES.ZZT, CITY.ZZT) that are NOT
// committed fixtures — they live in the engine working directory when present
// and are absent in a clean clone. Because they depend on untracked worlds they
// are kept out of the required, certified `go test ./...` path and only run
// under `go test -tags canary` (see `make parity-canaries`). The committed-TOWN
// round-trip in zwd_decompile_test.go stays in the required path.
package zztgo

import "testing"

func TestZWDRoundTripCAVES(t *testing.T) {
	testZWDRoundTrip(t, "CAVES.ZZT", false)
}

func TestZWDRoundTripCITY(t *testing.T) {
	testZWDRoundTrip(t, "CITY.ZZT", false)
}
