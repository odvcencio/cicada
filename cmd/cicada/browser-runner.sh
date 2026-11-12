#!/usr/bin/env bash
set -euo pipefail

tags=${1:?Go build tags required}
pattern=${2:?Go test pattern required}
timeout=${3:?Go test timeout required}
log_path=${4:?Go test log path required}
nice_level=${NICE_LEVEL:-10}

if [[ "${CICADA_BROWSER:-}" == windows ]]; then
  chrome_path='/mnt/c/Program Files/Google/Chrome/Application/chrome.exe'
  if [[ ! -f "$chrome_path" ]]; then
    chrome_path='/mnt/c/Program Files (x86)/Google/Chrome/Application/chrome.exe'
  fi
  if [[ ! -f "$chrome_path" ]]; then
    echo "Windows Chrome is required at its standard Windows install path" >&2
    exit 1
  fi
  mkdir -p build
  export CICADA_BROWSER=windows
  export PULSE_SERVER=unix:/nonexistent
  set +e
  GOWORK=off nice -n "$nice_level" go test -tags "$tags" ./cmd/cicada \
    -run "$pattern" -count=1 -timeout="$timeout" -v 2>&1 | tee "$log_path"
  test_status=${PIPESTATUS[0]}
  set -e
  exit "$test_status"
fi

browser_path=${CHROME_BIN:-}
if [[ -z "$browser_path" ]]; then
  for candidate in google-chrome chrome-headless-shell chromium; do
    if browser_path=$(command -v "$candidate"); then
      break
    fi
  done
fi
if [[ -z "$browser_path" ]]; then
  echo "Chrome is required; set CHROME_BIN to Chrome for Testing headless shell" >&2
  exit 1
fi

mkdir -p build
profile_root=$(mktemp -d "${TMPDIR:-/tmp}/cicada-browser.XXXXXX")
profile="$profile_root/profile"
chrome_log=build/m2-browser-chrome.log
chrome_pid=
# cleanup signals only the Chrome this script started. setsid makes Chrome
# the leader of a new process group whose ID is chrome_pid. bash reaps a
# background child as soon as it exits, so after a Chrome crash chrome_pid may
# belong to another process. The cleanup therefore signals only while
# "jobs -rp" still lists chrome_pid, that is, while bash has not reaped it.
# (The job can still exit between that check and the kill; the window is
# microseconds.) Until setsid has run, the group does not exist and only the
# child is signaled. Do not read the group from ps: before setsid runs it is
# this script's own group, which a CI step's shell, make and the runner's
# worker share (browser-runner_test.sh).
cleanup() {
  trap - EXIT INT TERM
  if [[ -n "$chrome_pid" ]]; then
    if [[ " $(jobs -rp) " == *" $chrome_pid "* ]]; then
      kill -TERM -- "-$chrome_pid" 2>/dev/null || kill -TERM "$chrome_pid" 2>/dev/null || true
    fi
    wait "$chrome_pid" 2>/dev/null || true
  fi
  rm -rf "$profile_root"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

PULSE_SERVER=unix:/nonexistent "$browser_path" --mute-audio --version
setsid env PULSE_SERVER=unix:/nonexistent "$browser_path" \
  --headless=new --no-sandbox --disable-gpu --disable-dev-shm-usage \
  --no-first-run --no-default-browser-check --mute-audio \
  --remote-debugging-address=127.0.0.1 --remote-debugging-port=8165 \
  --user-data-dir="$profile" about:blank >"$chrome_log" 2>&1 &
chrome_pid=$!

ready=false
for _ in $(seq 1 200); do
  if curl --fail --silent http://127.0.0.1:8165/json/list >/dev/null; then
    ready=true
    break
  fi
  if ! kill -0 "$chrome_pid" 2>/dev/null; then
    break
  fi
  sleep 0.1
done
if [[ "$ready" != true ]]; then
  echo "Chrome DevTools did not start on 127.0.0.1:8165; see $chrome_log" >&2
  cat "$chrome_log" >&2
  exit 1
fi

export CHROME_BIN="$browser_path"
export CICADA_BROWSER_CHROME_EXTERNAL=1
export PULSE_SERVER=unix:/nonexistent
set +e
GOWORK=off nice -n "$nice_level" go test -tags "$tags" ./cmd/cicada \
  -run "$pattern" -count=1 -timeout="$timeout" -v 2>&1 | tee "$log_path"
test_status=${PIPESTATUS[0]}
set -e
exit "$test_status"
