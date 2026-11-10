#!/usr/bin/env bash
# Tests that "ci-studio-browser.sh check" catches a suite that is in no shard
# or in two shards. It mutates a copy of the script in a scratch directory and
# expects the check to fail (or, for the real script, to pass).
set -euo pipefail

here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd -- "$here/.." && pwd)"
scratch="$(mktemp -d "${TMPDIR:-/tmp}/ci-studio-browser-test.XXXXXX")"
trap 'rm -rf -- "$scratch"' EXIT
failures=0

# The scratch tree has the script, an empty file per real suite, and what a
# dry run needs (a fake build/cicada and an example project).
mkdir -p "$scratch/scripts" "$scratch/workstation/browser" "$scratch/build" "$scratch/examples/studio-project"
printf '#!/bin/sh\nexit 0\n' > "$scratch/build/cicada"
chmod +x "$scratch/build/cicada"
for suite in "$repo"/workstation/browser/*.test.cjs; do
	: > "$scratch/workstation/browser/$(basename "$suite")"
done

# run_check NAME EXPECT(pass|fail) MESSAGE_PATTERN SED_EXPR...
# Runs "check" and a dry run of every shard on a mutated copy of the script.
run_check() {
	local name="$1" expect="$2" pattern="$3" output="" status=0 shard
	shift 3
	cp "$repo/scripts/ci-studio-browser.sh" "$scratch/scripts/ci-studio-browser.sh"
	local expr
	for expr in "$@"; do
		sed -i "$expr" "$scratch/scripts/ci-studio-browser.sh"
	done
	output="$(bash "$scratch/scripts/ci-studio-browser.sh" check 2>&1)" || status=$?
	for shard in 1 2 3 4; do
		output="$output
$(cd "$scratch" && CICADA_STUDIO_DRY_RUN=1 CICADA_STUDIO_DRY_ROOT="$scratch/dry" bash scripts/ci-studio-browser.sh run "$shard" 2>&1 | grep -v '^::')" || status=$?
		# pipefail is off in this subshell; look for the failure text instead
		if grep -q '^FAIL' <<<"$output"; then status=1; fi
		rm -rf "$scratch/dry"
	done
	if [[ "$expect" == pass && "$status" != 0 ]] || [[ "$expect" == fail && "$status" == 0 ]]; then
		echo "FAIL: $name: expected $expect, exit status $status" >&2
		echo "$output" >&2
		failures=$((failures + 1))
	elif ! grep -q -- "$pattern" <<<"$output"; then
		echo "FAIL: $name: output lacks '$pattern'" >&2
		echo "$output" >&2
		failures=$((failures + 1))
	fi
}

run_check "the real script passes" pass "ran all"

# A segment dropped from every shard leaves its suite in no shard.
run_check "segment in no shard" fail "segment shell is in no shard" 's/ shell / /'

# The same segment in two shards.
run_check "segment in two shards" fail "segment chorus is in shard" \
	's/^\t1) echo "print_sources /\t1) echo "chorus print_sources /'

# A shard that names a segment that does not exist.
run_check "shard names a missing segment" fail "which has no function" \
	's/^\t2) echo "pattern_song /\t2) echo "no_such_segment pattern_song /'

# A new suite file that no segment runs and no list excuses.
: > "$scratch/workstation/browser/brand-new.test.cjs"
run_check "new suite in no shard" fail "suite brand-new is in no shard"
rm "$scratch/workstation/browser/brand-new.test.cjs"

# The same suite listed by segments in two different shards.
run_check "suite in two shards" fail "suite shell runs in shard" \
	's|^\tmixer_strips) echo mixer-strips ;;|\tmixer_strips) echo mixer-strips shell ;;|'

# The reviewer's mutation 1: delete a suite's command. The comment-only
# declaration is gone, so check and the dry run both notice.
run_check "deleted suite command" fail "shell" '/run_suite shell make test-studio-shell/d'

# The reviewer's mutation 2: drop a phase. The sampler phase leaves no record.
run_check "dropped phase" fail "did not run sampler@primary-sampler" \
	's/print_phases=(sampler producer roundtrip reopen)/print_phases=(producer roundtrip reopen)/'

# A run_suite call for a suite that segment_runs does not list.
run_check "unlisted suite call" fail "calls run_suite for chorus but segment_runs does not list it" \
	's|^\trun_suite continuity make test-studio-continuity .*|&\n\trun_suite chorus true|'

# A suite listed as not run that a segment runs.
run_check "excused suite that runs" fail "suite shell is listed in not_run_by_ci" \
	's|^\tamp-automation$|\tamp-automation\n\tshell|'

# A listed suite with no file on disk.
run_check "listed suite without a file" fail "no file workstation/browser/ghost.test.cjs" \
	's|^\tchorus) echo chorus ;;|\tchorus) echo chorus ghost ;;|'

# A suite that outlives the time limit must fail with its name in the output,
# and one that finishes in time must pass with its output intact.
cp "$repo/scripts/ci-studio-browser.sh" "$scratch/scripts/ci-studio-browser.sh"
status=0
output="$(cd "$scratch" && CICADA_STUDIO_SUITE_TIMEOUT=2 bash scripts/ci-studio-browser.sh run-suite hung-suite sh -c 'echo started; sleep 30' 2>&1)" || status=$?
if [[ "$status" == 0 ]] || ! grep -q 'suite hung-suite timed out after 2s' <<<"$output" || ! grep -q '^started$' <<<"$output"; then
	echo "FAIL: suite time limit: exit status $status" >&2
	echo "$output" >&2
	failures=$((failures + 1))
fi
status=0
output="$(cd "$scratch" && CICADA_STUDIO_SUITE_TIMEOUT=2 bash scripts/ci-studio-browser.sh run-suite quick-suite sh -c 'echo finished' 2>&1)" || status=$?
if [[ "$status" != 0 ]] || ! grep -q '^finished$' <<<"$output"; then
	echo "FAIL: suite inside the time limit: exit status $status" >&2
	echo "$output" >&2
	failures=$((failures + 1))
fi

if [[ "$failures" -gt 0 ]]; then
	echo "$failures failure(s)" >&2
	exit 1
fi
echo "ci-studio-browser: shard completeness checks passed"
