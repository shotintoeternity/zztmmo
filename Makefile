# Repo-level commands for ZZTMMO.
#
# `make parity` is the single M16 certification command: it runs the clean gates
# — npm ci, the pinned Playwright engines, the client build, npm test, then go
# build/vet/test/-race with the real-browser suites required rather than
# skippable — and writes a deterministic JSON + Markdown report keyed by the
# parity manifest (fixtures/parity), plus a run record (run.json) carrying tool
# versions, the commit and per-gate timings. It exits non-zero when a gate
# fails, and writes only gitignored files, so a clean run leaves `git status`
# empty.
#
# `make certify` is the same run under the M16.20 gate: an uncertified manifest
# — any `unverified`/`gap` row, any `pass` naming no test, any undeclared test
# skip — is a failure rather than an expected state.

.PHONY: parity certify parity-report parity-canaries oracle-tools oracle-regen world

# Deterministic ZWD authoring gate. Example:
#   make world SOURCE=llmworld/generated/NULLSIGN.zwd OUT=engine/NULLSIGN.ZZT
SOURCE ?= llmworld/generated/NULLSIGN.zwd
OUT ?= engine/NULLSIGN.ZZT
PREVIEW ?= llmworld/previews/NULLSIGN

world:
	cd engine && go run ./cmd/zzt-build -out ../$(OUT) -preview ../$(PREVIEW) ../$(SOURCE)

# Full certification run: gates + report.
parity:
	cd engine && go run ./cmd/zzt-parity -out ../fixtures/parity

# The same run, gated: not-certified is a failure (task M16.20).
certify:
	cd engine && go run ./cmd/zzt-parity -require-certified -out ../fixtures/parity

# Re-render the report from the current manifest without running the gates.
parity-report:
	cd engine && go run ./cmd/zzt-parity -run-gates=false -out ../fixtures/parity

# Optional extended-world / corpus canaries (untracked worlds, generators).
# These are deliberately outside the certified path; run them when regenerating
# the corpus or exercising community worlds present in the engine directory.
parity-canaries:
	cd engine && go test -tags canary -count=1 ./...

# M16.2 vanilla oracle — maintainer tooling only; tests never run these.
# oracle-tools builds the pinned Zeta harness and fetches the pinned ZZT.EXE.
# oracle-regen additionally re-records fixtures/oracle/*.capture.txt.
oracle-tools:
	sh oracle/fetch_zzt.sh
	sh oracle/build.sh

oracle-regen:
	sh oracle/regen.sh
