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
#
# `make browser` is the everyday half of that: the opt-in real-browser family and
# nothing else around it. CLAUDE.md rule 3 sends client work here (M33.2).
#
# `make web3d` is the 3D client's own gate: typecheck, node suite, build. It is
# separate from `browser` because engine/web3d is a separate npm project with a
# separate lockfile, and because it needs no browser at all -- its suites are
# pure functions over the protocol and the glyph tables.

.PHONY: parity certify browser web3d parity-report parity-canaries oracle-tools oracle-regen world

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

# The opt-in real-browser family, run on purpose (task M33.2). This is what
# CLAUDE.md rule 3 means by "run the browser suites": it builds the client the
# suites load — a stale web/dist is the M18.0a failure, and the suites cannot
# tell it from a working one — runs the client's own node unit suite, and then
# runs the Go tests with the browser family requested rather than skipped. Ten
# minutes, and the price of moving a menu three journeys walk. `make certify`
# still runs everything else.
#
# `npm test` is in here because rule 3 sends client work to this target and to
# nothing else, and 918c9da shipped with it unrun: that commit deleted exports
# and filters under web/src, ran this family green, and left two node suites
# importing names that no longer existed — red in `make parity` and `make
# certify`, which is where it was found. It sits before the Go tests on
# purpose: it fails in seconds, and the family it guards costs ten minutes.
#
# -timeout is not decoration: with the family running, the engine package takes
# most of ten minutes, and `go test`'s default IS ten minutes — M33.1's run
# finished with eight seconds to spare and M33.2's first run died on the wall
# clock with a suite three seconds in. A timeout panic reads exactly like a
# hung suite, so the margin is spent here rather than in the next executor's
# afternoon (M33.3 carries the same fix to the certification run).
browser:
	cd engine/web && npm run build
	cd engine/web && npm test
	cd engine && ZZT_BROWSER=1 go test -timeout 30m -count=1 ./...

# The 3D client. `npm run build` is `tsc && vite build`, so this typechecks as
# well as bundles. The deploy passes --base=/3d/ on top (see AWS.md); the plain
# build here is what proves the tree compiles.
web3d:
	cd engine/web3d && npm test
	cd engine/web3d && npm run build

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
