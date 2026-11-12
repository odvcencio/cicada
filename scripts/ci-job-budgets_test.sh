#!/usr/bin/env bash
# Tests scripts/ci-job-budgets.py: the real workflow and budgets agree, and a
# run's jobs are judged against their budgets. Needs only bash and python3.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script="$root/scripts/ci-job-budgets.py"
budgets="$root/scripts/ci-job-budgets.tsv"
workflow="$root/.github/workflows/ci.yml"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

fail() {
	echo "ci-job-budgets_test: $*" >&2
	exit 1
}

# --- the real workflow: every job has exactly one budget ---
python3 "$script" check-workflow "$workflow" "$budgets" >/dev/null || fail "the workflow and $budgets disagree (run: python3 scripts/ci-job-budgets.py check-workflow $workflow $budgets)"

# --- a job without a budget, a stale budget, a budget over the ceiling and over the timeout ---
cat >"$tmp/ci.yml" <<'EOF'
name: CI
jobs:
  a:
    name: Alpha (shard ${{ matrix.shard }}/2)
    timeout-minutes: 10
  b:
    name: Beta
    timeout-minutes: 30
  c:
    name: Gamma
    timeout-minutes: 5
EOF
printf '9\tpr\tAlpha (shard */2)\n20\tpr\tBeta\n6\tpr\tGamma\n3\tpr\tDelta\n' >"$tmp/budgets.tsv"
if python3 "$script" check-workflow "$tmp/ci.yml" "$tmp/budgets.tsv" 2>"$tmp/err"; then fail "check-workflow passed a bad budget file"; fi
grep -q "'Beta' runs on pull requests, so its budget may be at most 17 minutes, not 20" "$tmp/err" || fail "no ceiling message: $(cat "$tmp/err")"
grep -q "job 'Gamma' has a budget of 6 minutes, longer than its timeout-minutes of 5" "$tmp/err" || fail "no timeout message: $(cat "$tmp/err")"
grep -q "has a budget for 'Delta', but .* has no such job" "$tmp/err" || fail "no stale-budget message: $(cat "$tmp/err")"
printf '9\tpr\tAlpha (shard */2)\n17\tpr\tBeta\n' >"$tmp/budgets.tsv"
if python3 "$script" check-workflow "$tmp/ci.yml" "$tmp/budgets.tsv" 2>"$tmp/err"; then fail "check-workflow passed a missing budget"; fi
grep -q "job 'Gamma' has no budget" "$tmp/err" || fail "no missing-budget message: $(cat "$tmp/err")"
printf '9\tpr\tAlpha (shard */2)\n17\tpr\tBeta\n5\tpr\tGamma\n' >"$tmp/budgets.tsv"
python3 "$script" check-workflow "$tmp/ci.yml" "$tmp/budgets.tsv" >/dev/null || fail "check-workflow failed a good budget file"

# --- a run within its limits: matrix names match, a job over its budget only warns ---
printf '9\tpr\tAlpha (shard */2)\n17\tpr\tBeta\n5\tpr\tGamma\n30\tnightly\tNightly\n' >"$tmp/budgets.tsv"
cat >"$tmp/ok.json" <<'JSON'
{"total_count": 6, "jobs": [
  {"name": "Alpha (shard 1/2)", "status": "completed", "conclusion": "success", "started_at": "2026-11-12T10:00:00Z", "completed_at": "2026-11-12T10:08:59Z"},
  {"name": "Alpha (shard 2/2)", "status": "completed", "conclusion": "failure", "created_at": "2026-11-12T09:15:00Z", "started_at": "2026-11-12T10:00:00Z", "completed_at": "2026-11-12T10:12:00Z"},
  {"name": "Beta", "status": "completed", "conclusion": "skipped", "started_at": "2026-11-12T10:00:00Z", "completed_at": "2026-11-12T10:00:00Z"},
  {"name": "Gamma", "status": "in_progress", "conclusion": null, "started_at": "2026-11-12T10:10:00Z", "completed_at": null},
  {"name": "Nightly", "status": "completed", "conclusion": "success", "started_at": "2026-11-12T10:00:00Z", "completed_at": "2026-11-12T10:29:00Z"},
  {"name": "Beta", "status": "queued", "conclusion": null, "started_at": null, "completed_at": null}
]}
JSON
python3 "$script" check-run "$tmp/budgets.tsv" "$tmp/ok.json" 2026-11-12T10:12:00Z >"$tmp/out" || fail "check-run failed a run within its limits: $(cat "$tmp/out")"
grep -Eq "^ok +8m59s \(warn above 9m, fail above 20m\)  Alpha \(shard 1/2\) \(success\)$" "$tmp/out" || fail "no per-job line with both limits: $(cat "$tmp/out")"
grep -q "^::warning::Alpha (shard 2/2) took 12m00s, over its budget of 9 minutes" "$tmp/out" || fail "no warning above the budget: $(cat "$tmp/out")"
grep -Eq "^warn +12m00s \(warn above 9m, fail above 20m; queued 45m00s\)  Alpha \(shard 2/2\)" "$tmp/out" || fail "no queue time on the job line: $(cat "$tmp/out")"
grep -q "^Longest wait in the queue: 45m00s (Alpha (shard 2/2)). Queue time is not judged." "$tmp/out" || fail "no longest-queue line: $(cat "$tmp/out")"
grep -q "not judged (skipped, no time)  Beta" "$tmp/out" || fail "a skipped job is not reported as not judged: $(cat "$tmp/out")"
grep -q "not judged (queued, no time)  Beta" "$tmp/out" || fail "a queued job is not reported as not judged: $(cat "$tmp/out")"
grep -Eq "^ok +29m00s \(warn above 30m, fail above 30m\)  Nightly" "$tmp/out" || fail "a nightly job's limit is not its budget: $(cat "$tmp/out")"

# --- failures: a pull-request job over 20 minutes, running or cancelled (a
# timeout ends as cancelled), a nightly job over its budget, a job without one ---
cat >"$tmp/over.json" <<'JSON'
{"total_count": 5, "jobs": [
  {"name": "Alpha (shard 2/2)", "status": "completed", "conclusion": "success", "started_at": "2026-11-12T10:00:00Z", "completed_at": "2026-11-12T10:20:01Z"},
  {"name": "Beta", "status": "completed", "conclusion": "cancelled", "started_at": "2026-11-12T10:00:00Z", "completed_at": "2026-11-12T10:45:00Z"},
  {"name": "Gamma", "status": "in_progress", "conclusion": null, "started_at": "2026-11-12T10:00:00Z", "completed_at": null},
  {"name": "Nightly", "status": "completed", "conclusion": "success", "started_at": "2026-11-12T10:00:00Z", "completed_at": "2026-11-12T10:31:00Z"},
  {"name": "Epsilon", "status": "completed", "conclusion": "success", "started_at": "2026-11-12T10:00:00Z", "completed_at": "2026-11-12T10:01:00Z"}
]}
JSON
if python3 "$script" check-run "$tmp/budgets.tsv" "$tmp/over.json" 2026-11-12T10:21:00Z >"$tmp/out"; then fail "check-run passed a run over its limits"; fi
grep -q "::error::Alpha (shard 2/2) took 20m01s, over 20 minutes (the project's 20-minute rule)" "$tmp/out" || fail "no rule failure: $(cat "$tmp/out")"
grep -q "::error::Beta took 45m00s, over 20 minutes" "$tmp/out" || fail "a cancelled (timed-out) job was not judged: $(cat "$tmp/out")"
grep -q "::error::Gamma has run for 21m00s, over 20 minutes" "$tmp/out" || fail "no failure for a running job past the rule: $(cat "$tmp/out")"
grep -q "::error::Nightly took 31m00s, over 30 minutes (its budget" "$tmp/out" || fail "no failure for a nightly job over its budget: $(cat "$tmp/out")"
grep -q "::error::job 'Epsilon' has no budget" "$tmp/out" || fail "no missing-budget failure: $(cat "$tmp/out")"

# --- a truncated job list is an error, not a pass ---
printf '{"total_count": 120, "jobs": []}\n' >"$tmp/short.json"
if python3 "$script" check-run "$tmp/budgets.tsv" "$tmp/short.json" 2>/dev/null >/dev/null; then fail "check-run passed a truncated job list"; fi

echo "ci-job-budgets: all checks passed"
