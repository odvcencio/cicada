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
# belong to another process. What the cleanup guarantees:
# - While "jobs -rp" lists chrome_pid (bash has not reaped it), chrome_pid is
#   still this script's child: the cleanup signals its group, or the child
#   itself if setsid has not run yet (then the group does not exist).
# - Once bash has reaped Chrome, the bare PID is never signaled. Only the group
#   is, so helpers a crashed Chrome left in its group still get TERM. Linux
#   does not hand out a group's ID as a new PID while the group has members,
#   so while any of them lives, -chrome_pid is Chrome's group.
# What it does not guarantee: if every process of Chrome's group has exited,
# and the PID counter has wrapped around to chrome_pid, and that new process
# leads its own group, the group signal reaches it. And if Chrome exits
# between the "jobs -rp" check and the kill (about 0.35 ms, measured from the
# fork of the check's subshell), the group or the bare PID is signaled after
# all; that too reaches another process only if the PID counter wrapped around
# to chrome_pid within that window. Do not read the group from ps:
# before setsid runs it is this script's own group, which a CI step's shell,
# make and the runner's worker share (browser-runner_test.sh).
# chrome_listed says whether bash still lists Chrome as a running job. jobs -rp
# prints one PID per line, and lists the go test pipeline too when a signal
# bash does not trap (HUP) ends the script during the tests.
chrome_listed() {
  local running
  running=$'\n'$(jobs -rp)$'\n'
  [[ "$running" == *$'\n'"$chrome_pid"$'\n'* ]]
}
# signal_chrome SIGNAL sends SIGNAL as the comment above allows.
signal_chrome() {
  if chrome_listed; then
    kill "-$1" -- "-$chrome_pid" 2>/dev/null || kill "-$1" "$chrome_pid" 2>/dev/null || true
  else
    kill "-$1" -- "-$chrome_pid" 2>/dev/null || true
  fi
}
# Chrome, and any helper left in its group, gets 5 s to exit after TERM, then
# KILL: a Chrome that ignores TERM would otherwise keep the wait below, and the
# runner, blocked forever. kill -0 on the group asks only whether it still has
# members; the same limits as above apply to it.
chrome_grace=5
cleanup() {
  trap - EXIT INT TERM
  if [[ -n "$chrome_pid" ]]; then
    signal_chrome TERM
    local tick
    for ((tick = 0; tick < chrome_grace * 10; tick++)); do
      if ! chrome_listed && ! kill -0 -- "-$chrome_pid" 2>/dev/null; then break; fi
      sleep 0.1
    done
    if chrome_listed || kill -0 -- "-$chrome_pid" 2>/dev/null; then
      echo "browser-runner: Chrome (PID $chrome_pid) did not exit within ${chrome_grace} s of TERM; sending KILL" >&2
      signal_chrome KILL
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
