#!/usr/bin/env bash
# Tests the download step of .github/actions/setup-wasm-tools/action.yml with
# stubbed curl and sleep: a wrong checksum must fail on the first attempt, a
# network failure must retry until the retry window closes, and a good
# download must pass.
set -euo pipefail

here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd -- "$here/.." && pwd)"
scratch="$(mktemp -d "${TMPDIR:-/tmp}/ci-setup-wasm-tools-test.XXXXXX")"
trap 'rm -rf -- "$scratch"' EXIT
failures=0

# Extract the step's script from the action.
python3 - "$repo/.github/actions/setup-wasm-tools/action.yml" "$scratch/step.sh" <<'PY'
import sys, yaml
steps = yaml.safe_load(open(sys.argv[1]))["runs"]["steps"]
step = next(s for s in steps if s.get("name", "").startswith("Download"))
open(sys.argv[2], "w").write(step["run"])
PY

mkdir -p "$scratch/bin"
# sleep is an exported function that moves bash's SECONDS clock instead of waiting.
sleep() { SECONDS=$((SECONDS + $1)); }
export -f sleep
# The stub curl writes "payload" (or fails when STUB_CURL=fail) and counts calls.
cat > "$scratch/bin/curl" <<STUB
#!/bin/sh
echo x >> "$scratch/curls"
if [ "\$(wc -l < "$scratch/curls")" -gt 50 ]; then echo "stub: runaway retries" >&2; exit 99; fi
if [ "\${STUB_CURL:-}" = fail ]; then echo "curl: (22) HTTP 504" >&2; exit 22; fi
while [ \$# -gt 0 ]; do if [ "\$1" = --output ]; then printf payload > "\$2"; fi; shift; done
STUB
chmod +x "$scratch/bin/"*
good="$(printf payload | sha256sum | cut -d' ' -f1)"

# run NAME STUB_CURL TINYGO_SHA256 WINDOW EXPECT(pass|fail) EXPECTED_CURLS PATTERN
run() {
	local name="$1" stub="$2" sha="$3" window="$4" expect="$5" curls="$6" pattern="$7" status=0 output got
	rm -f "$scratch/curls"
	output="$(PATH="$scratch/bin:$PATH" STUB_CURL="$stub" DIR="$scratch/dir" RETRY_WINDOW="$window" \
		TINYGO_URL=u1 TINYGO_SHA256="$sha" BINARYEN_URL=u2 BINARYEN_SHA256="$good" \
		bash "$scratch/step.sh" 2>&1)" || status=$?
	got="$(wc -l < "$scratch/curls" 2>/dev/null || echo 0)"
	# The good download has no archive to extract; tar failing is expected there.
	if [[ "$expect" == fail && "$status" == 0 ]]; then
		echo "FAIL: $name: expected $expect, status $status" >&2; echo "$output" >&2; failures=$((failures + 1))
	elif [[ -n "$curls" && "$got" != "$curls" ]]; then
		echo "FAIL: $name: $got curl calls, want $curls" >&2; echo "$output" >&2; failures=$((failures + 1))
	elif ! grep -q -- "$pattern" <<<"$output"; then
		echo "FAIL: $name: output lacks '$pattern'" >&2; echo "$output" >&2; failures=$((failures + 1))
	fi
}

run "wrong checksum fails on the first attempt" ok deadbeef 480 fail 1 "not retrying"
run "network failure retries until the window closes" fail "$good" 150 fail 3 "could not download u1 after 3 attempts"
run "network failure with a short window tries once" fail "$good" 60 fail 1 "after 1 attempts"
# Good checksums get past the downloads (tar then fails on the fake archive).
run "good checksums are accepted" ok "$good" 480 fail 2 "tar:\|not a gzip\|gzip"

if [[ "$failures" -gt 0 ]]; then echo "$failures failure(s)" >&2; exit 1; fi
echo "ci-setup-wasm-tools: download step checks passed"
