#!/usr/bin/env bash
# browser-runner_test.sh: browser-runner.sh signals only the Chrome it started.
#
# A CI step's shell, make and the runner's own worker can share
# browser-runner.sh's process group, so a signal to that group stops the job
# ("The runner has received a shutdown signal"). Each case runs
# browser-runner.sh in a new process group next to a bystander process, with
# stand-ins for Chrome, curl and go, and fails unless the bystander survives
# and the stand-in Chrome is gone afterwards.
#
#   ordinary   Chrome is in its own process group before the cleanup runs.
#   early      setsid waits 0.5 s before it moves Chrome to its own group, so
#              the cleanup runs while Chrome still shares the runner's group:
#              the order a loaded CI runner can hit by chance.
#   crashed    Chrome exits during the tests and bash reaps it, so its PID may
#              belong to another process by the time the cleanup runs. A
#              stand-in kill records every signal; the cleanup must send none.
#
# Each case runs under timeout(1), and finish() kills each case's process
# group, so a runner that blocks cannot hang the test or leave processes behind.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
real_setsid=$(command -v setsid)
work=$(mktemp -d "${TMPDIR:-/tmp}/browser-runner-test.XXXXXX")
failed=0
leftover=()
groups=()
finish() {
  trap - EXIT
  for group in "${groups[@]}"; do kill -KILL -- "-$group" 2>/dev/null || true; done
  for pid in "${leftover[@]}"; do kill -KILL "$pid" 2>/dev/null || true; done
  rm -rf "$work"
}
trap finish EXIT

alive() { kill -0 "$1" 2>/dev/null; }

run_case() {
  local name=$1 delay=$2 dir="$work/$1"
  mkdir -p "$dir/bin" "$dir/checkout/build"
  cat >"$dir/bin/setsid" <<EOF
#!/bin/sh
sleep $delay
exec "$real_setsid" "\$@"
EOF
  # The stand-in Chrome records its PID, then becomes a long sleep. In the
  # crashed case it exits after 0.5 s instead.
  local chrome_end='exec sleep 300'
  if [[ "$name" == crashed ]]; then chrome_end='exec sleep 0.5'; fi
  cat >"$dir/bin/chrome" <<EOF
#!/bin/sh
case " \$* " in *" --version "*) echo 'stand-in Chrome'; exit 0 ;; esac
echo \$\$ >"$dir/chrome.pid"
$chrome_end
EOF
  # In the ordinary case curl answers once Chrome is running; in the early
  # case it answers at once, so the cleanup runs while setsid still waits.
  if [[ "$name" == early ]]; then
    printf '#!/bin/sh\nexit 0\n' >"$dir/bin/curl"
  else
    printf '#!/bin/sh\ntest -s "%s/chrome.pid"\n' "$dir" >"$dir/bin/curl"
  fi
  # In the crashed case the stand-in go test outlasts Chrome, so bash reaps
  # Chrome before the cleanup runs.
  if [[ "$name" == crashed ]]; then
    printf '#!/bin/sh\nsleep 1\necho "ok  stand-in go test"\n' >"$dir/bin/go"
  else
    printf '#!/bin/sh\necho "ok  stand-in go test"\n' >"$dir/bin/go"
  fi
  chmod +x "$dir/bin/setsid" "$dir/bin/chrome" "$dir/bin/curl" "$dir/bin/go"

  # The new group's leader starts the bystander, then runs browser-runner.sh
  # in the same group, as a CI step's shell runs make. The group ID is the
  # background job's PID ($!): without job control setsid does not fork.
  # kill is a function that records each signal before it sends it (kill -0
  # is only a liveness check and is not recorded). The runner gets its own
  # TMPDIR so a failing run leaves no profile folder behind.
  "$real_setsid" --wait timeout --foreground 30 bash -c '
    export KILL_LOG="$1/kill.log"
    kill() { case "$1" in -0) ;; *) echo "kill $*" >>"$KILL_LOG" ;; esac; builtin kill "$@"; }
    export -f kill
    sleep 300 & echo $! >"$1/bystander.pid"
    cd "$1/checkout"
    PATH="$1/bin:$PATH" CHROME_BIN="$1/bin/chrome" TMPDIR="$1" bash "$2/browser-runner.sh" browser "^TestStandIn$" 1m "$1/go.log" >"$1/runner.log" 2>&1
    echo $? >"$1/runner.status"
  ' _ "$dir" "$here" </dev/null &
  local group=$! limit=0
  groups+=("$group")
  wait "$group" || limit=$?
  local bystander chrome="" status
  bystander=$(cat "$dir/bystander.pid" 2>/dev/null || echo 0)
  if [[ "$bystander" != 0 ]]; then leftover+=("$bystander"); fi
  status=$(cat "$dir/runner.status" 2>/dev/null || echo "none: the runner's own group was signaled")
  sleep "$delay"; sleep 0.3 # let a delayed setsid finish
  if [[ -s "$dir/chrome.pid" ]]; then chrome=$(cat "$dir/chrome.pid"); leftover+=("$chrome"); fi

  if [[ "$limit" == 124 ]]; then
    echo "FAIL $name: browser-runner.sh did not finish within 30 s" >&2
    sed 's/^/  /' "$dir/runner.log" >&2 || true
    failed=1
  elif [[ "$name" == crashed && -s "$dir/kill.log" ]]; then
    echo "FAIL $name: browser-runner.sh signaled after Chrome had exited and bash had reaped it; that PID may belong to another process:" >&2
    sed 's/^/  /' "$dir/kill.log" >&2
    failed=1
  elif ! alive "$bystander"; then
    echo "FAIL $name: browser-runner.sh killed a process it did not start (runner status: $status)" >&2
    sed 's/^/  /' "$dir/runner.log" >&2
    failed=1
  elif [[ -n "$chrome" ]] && alive "$chrome"; then
    echo "FAIL $name: the stand-in Chrome (PID $chrome) outlived browser-runner.sh" >&2
    failed=1
  elif [[ "$status" != 0 ]]; then
    echo "FAIL $name: browser-runner.sh exited with $status" >&2
    sed 's/^/  /' "$dir/runner.log" >&2
    failed=1
  else
    echo "ok $name"
  fi
}

run_case ordinary 0
run_case early 0.5
run_case crashed 0
exit "$failed"
