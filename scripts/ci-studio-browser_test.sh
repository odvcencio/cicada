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

# The scratch tree has the script and an empty file per real suite.
mkdir -p "$scratch/scripts" "$scratch/workstation/browser"
for suite in "$repo"/workstation/browser/*.test.cjs; do
	: > "$scratch/workstation/browser/$(basename "$suite")"
done

# run_check NAME EXPECT(pass|fail) MESSAGE_PATTERN SED_EXPR...
run_check() {
	local name="$1" expect="$2" pattern="$3" output status=0
	shift 3
	cp "$repo/scripts/ci-studio-browser.sh" "$scratch/scripts/ci-studio-browser.sh"
	local expr
	for expr in "$@"; do
		sed -i "$expr" "$scratch/scripts/ci-studio-browser.sh"
	done
	output="$(bash "$scratch/scripts/ci-studio-browser.sh" check 2>&1)" || status=$?
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

run_check "the real script passes" pass "each suite in exactly one" 

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

# The same suite declared by segments in two different shards.
run_check "suite in two shards" fail "suite shell runs in shard" \
	's|^\t# suites: mixer-strips$|\t# suites: mixer-strips shell|'

# A suite run by node --test but missing from the "# suites:" comment.
run_check "undeclared suite call" fail "runs chorus but its '# suites:' comment does not name it" \
	's|^\tmake test-studio-continuity .*|&\n\tnode --test workstation/browser/chorus.test.cjs|'

# A suite listed as not run that a segment runs.
run_check "excused suite that runs" fail "suite shell is listed in not_run_by_ci" \
	's|^\tamp-automation$|\tamp-automation\n\tshell|'

# A declared suite with no file on disk.
run_check "declared suite without a file" fail "no file workstation/browser/ghost.test.cjs" \
	's|^\t# suites: chorus$|\t# suites: chorus ghost|'

if [[ "$failures" -gt 0 ]]; then
	echo "$failures failure(s)" >&2
	exit 1
fi
echo "ci-studio-browser: shard completeness checks passed"
