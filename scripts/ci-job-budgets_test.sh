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

# --- a run: matrix names match, skipped and queued jobs are not judged ---
cat >"$tmp/ok.json" <<'EOF'
{"total_count": 5, "jobs": [
  {"name": "Alpha (shard 1/2)", "status": "completed", "conclusion": "success", "started_at": "2026-11-12T10:00:00Z", "completed_at": "2026-11-12T10:08:59Z"},
  {"name": "Alpha (shard 2/2)", "status": "completed", "conclusion": "failure", "started_at": "2026-11-12T10:00:00Z", "completed_at": "2026-11-12T10:02:00Z"},
  {"name": "Beta", "status": "completed", "conclusion": "skipped", "started_at": "2026-11-12T10:00:00Z", "completed_at": "2026-11-12T10:00:00Z"},
  {"name": "Gamma", "status": "in_progress", "conclusion": null, "started_at": "2026-11-12T10:10:00Z", "completed_at": null},
  {"name": "Unlisted but queued", "status": "queued", "conclusion": null, "started_at": null, "completed_at": null}
]}
EOF
python3 "$script" check-run "$tmp/budgets.tsv" "$tmp/ok.json" 2026-11-12T10:12:00Z >"$tmp/out" || fail "check-run failed a run within budget: $(cat "$tmp/out")"
grep -Eq "^ok +8m59s of +9m  Alpha \(shard 1/2\) \(success\)$" "$tmp/out" || fail "no per-job line: $(cat "$tmp/out")"

# --- a finished job over budget, a running job over budget, a job without a budget ---
cat >"$tmp/over.json" <<'EOF'
{"total_count": 3, "jobs": [
  {"name": "Alpha (shard 2/2)", "status": "completed", "conclusion": "success", "started_at": "2026-11-12T10:00:00Z", "completed_at": "2026-11-12T10:09:01Z"},
  {"name": "Gamma", "status": "in_progress", "conclusion": null, "started_at": "2026-11-12T10:00:00Z", "completed_at": null},
  {"name": "Epsilon", "status": "completed", "conclusion": "success", "started_at": "2026-11-12T10:00:00Z", "completed_at": "2026-11-12T10:01:00Z"}
]}
EOF
if python3 "$script" check-run "$tmp/budgets.tsv" "$tmp/over.json" 2026-11-12T10:06:00Z >"$tmp/out"; then fail "check-run passed a run over budget"; fi
grep -q "::error::Alpha (shard 2/2) took 9m01s, over its budget of 9 minutes" "$tmp/out" || fail "no over-budget message: $(cat "$tmp/out")"
grep -q "::error::Gamma has run for 6m00s, over its budget of 5 minutes" "$tmp/out" || fail "no running-over message: $(cat "$tmp/out")"
grep -q "::error::job 'Epsilon' has no budget" "$tmp/out" || fail "no missing-budget message: $(cat "$tmp/out")"

# --- a truncated job list is an error, not a pass ---
printf '{"total_count": 120, "jobs": []}\n' >"$tmp/short.json"
if python3 "$script" check-run "$tmp/budgets.tsv" "$tmp/short.json" 2>/dev/null >/dev/null; then fail "check-run passed a truncated job list"; fi

echo "ci-job-budgets: all checks passed"
