# Repo-level commands for ZZTMMO.
#
# `make parity` is the single M16 certification command: it runs the clean gates
# (go build/vet/test/-race, npm ci/test/build) and writes a deterministic
# JSON + Markdown report keyed by the parity manifest (fixtures/parity). It exits
# non-zero when a gate fails or the manifest is not yet certified, and writes
# only the gitignored report files, so a clean run leaves `git status` empty.

.PHONY: parity parity-report parity-canaries oracle-tools oracle-regen

# Full certification run: gates + report.
parity:
	cd engine && go run ./cmd/zzt-parity -out ../fixtures/parity

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
