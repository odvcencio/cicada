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
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
real_setsid=$(command -v setsid)
work=$(mktemp -d "${TMPDIR:-/tmp}/browser-runner-test.XXXXXX")
failed=0
leftover=()
finish() {
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
  # The stand-in Chrome records its PID, then becomes a long sleep.
  cat >"$dir/bin/chrome" <<EOF
#!/bin/sh
case " \$* " in *" --version "*) echo 'stand-in Chrome'; exit 0 ;; esac
echo \$\$ >"$dir/chrome.pid"
exec sleep 300
EOF
  # In the ordinary case curl answers once Chrome is running; in the early
  # case it answers at once, so the cleanup runs while setsid still waits.
  if [[ "$name" == early ]]; then
    printf '#!/bin/sh\nexit 0\n' >"$dir/bin/curl"
  else
    printf '#!/bin/sh\ntest -s "%s/chrome.pid"\n' "$dir" >"$dir/bin/curl"
  fi
  printf '#!/bin/sh\necho "ok  stand-in go test"\n' >"$dir/bin/go"
  chmod +x "$dir/bin/setsid" "$dir/bin/chrome" "$dir/bin/curl" "$dir/bin/go"

  # The new group's leader starts the bystander, then runs browser-runner.sh
  # in the same group, as a CI step's shell runs make.
  set +e
  "$real_setsid" --wait bash -c '
    sleep 300 & echo $! >"$1/bystander.pid"
    cd "$1/checkout"
    PATH="$1/bin:$PATH" CHROME_BIN="$1/bin/chrome" bash "$2/browser-runner.sh" browser "^TestStandIn$" 1m "$1/go.log" >"$1/runner.log" 2>&1
    echo $? >"$1/runner.status"
  ' _ "$dir" "$here" </dev/null
  set -e
  local bystander chrome="" status
  bystander=$(cat "$dir/bystander.pid")
  leftover+=("$bystander")
  status=$(cat "$dir/runner.status" 2>/dev/null || echo "none: the runner's own group was signaled")
  sleep "$delay"; sleep 0.3 # let a delayed setsid finish
  if [[ -s "$dir/chrome.pid" ]]; then chrome=$(cat "$dir/chrome.pid"); leftover+=("$chrome"); fi

  if ! alive "$bystander"; then
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
exit "$failed"
