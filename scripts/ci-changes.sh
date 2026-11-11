#!/usr/bin/env bash
# Decide which CI job groups a pull request can affect.
#
# Usage:
#   git diff --name-only --no-renames BASE...HEAD | scripts/ci-changes.sh
#   scripts/ci-changes.sh --all        # push, schedule, manual runs
#   scripts/ci-changes.sh --classify PATH   # print the groups for one path
#
# Standard input is one repository path per line. The output is one
# "group=true|false" line per group, ready to append to $GITHUB_OUTPUT.
#
# The rule is conservative: a path that no rule below names, an empty list,
# and any change to CI itself (.github, Makefile, scripts, go.mod, go.sum)
# turn every group on. Add a narrow rule only after checking the Go import
# graph (scripts/ci-changes_test.sh --graph does this) and the files the jobs
# read at test time.
#
# Groups and the jobs they gate (see .github/workflows/ci.yml):
#   root             native-go127, the four M0 parts
#   studio_cli       Studio CLI tests (cmd/cicada shards)
#   parity           PCM24 and WAV parity (amd64, arm64)
#   workstation      GoSX workstation and Tymbal boundary
#   studio_browser   Studio build and the Studio browser shards
#   macos_desktop    macOS desktop (arm64, amd64)
#   macos_contracts  macOS platform contracts (arm64, amd64)
#   m2               M2 AudioWorklet gates and the WASM parity jobs
#   bench            agent loop speed (cmd/cicada-bench on the built cicada)
# Always-on jobs (changes, commit hygiene, repo checks, the M0 aggregate) do
# not appear here.
set -euo pipefail

groups=(root studio_cli parity workstation studio_browser macos_desktop macos_contracts m2 bench)

# classify_path prints the groups a path turns on, or "all" or "none".
#
# Order matters: the first matching pattern wins.
classify_path() {
	case "$1" in
	# Anything that changes how CI builds or runs: everything runs.
	.github/* | Makefile | scripts/* | go.mod | go.sum)
		echo all ;;
	# Text that a Go //go:embed or a test fixture may read: do not treat as docs.
	project/std/* | assets/* | testdata/* | */testdata/*)
		echo all ;;
	# Every nested go.mod and go.sum turns everything on: desktop/go.mod, for
	# one, changes the module graph that other jobs resolve.
	*/go.mod | */go.sum)
		echo all ;;
	# Module-specific rules come before the documentation rule, so a README
	# inside them is still documentation (first rule below) and a Go file is not.
	workstation/*.md | desktop/*.md | kernel/measure/*.md | tools/gosx-size/*.md)
		echo none ;;
	# internal/buildaudit (root module) reads tools/gosx-size/go.mod; that file
	# and workstation/go.mod are covered by the go.mod rule above.
	tools/gosx-size/*)
		echo root workstation studio_browser macos_desktop ;;
	# The workstation module imports the root module; the root module never
	# imports it (checked by scripts/ci-changes_test.sh --graph).
	workstation/*)
		echo workstation studio_browser macos_desktop ;;
	desktop/*)
		echo workstation macos_desktop macos_contracts ;;
	# No package imports kernel/measure except cmd/cicada-engine-metrics and its
	# own tests, so only the root group needs it. Its testdata (for example
	# kernel/measure/testdata/tolerances.json) turns everything on through the
	# testdata rule above; that is deliberate, because the fixtures are inputs.
	kernel/measure/*)
		echo root ;;
	# A Go file is never documentation, even under docs/, editors/ or deploy/.
	*.go)
		echo all ;;
	# Documentation and files no build reads. Tests that read docs run in the
	# "repo-checks" job, which the required M0 check includes.
	docs/* | *.md | LICENSE | editors/* | deploy/*)
		echo none ;;
	*)
		echo all ;;
	esac
}

emit() { # emit GROUP... : print every group, true when listed
	local group value
	for group in "${groups[@]}"; do
		value=false
		for want in "$@"; do
			if [[ "$want" == "$group" ]]; then value=true; fi
		done
		echo "$group=$value"
	done
}

if [[ "${1:-}" == "--all" ]]; then
	emit "${groups[@]}"
	exit 0
fi
if [[ "${1:-}" == "--classify" ]]; then
	classify_path "${2:?path}"
	exit 0
fi
if [[ $# -gt 0 ]]; then
	echo "usage: $0 [--all | --classify PATH] < paths" >&2
	exit 2
fi

selected=()
seen_path=false
while IFS= read -r path || [[ -n "$path" ]]; do
	if [[ -z "$path" ]]; then continue; fi
	seen_path=true
	result="$(classify_path "$path")"
	if [[ "$result" == all ]]; then
		emit "${groups[@]}"
		exit 0
	fi
	if [[ "$result" != none ]]; then
		# shellcheck disable=SC2206
		selected+=($result)
	fi
done

if [[ "$seen_path" == false ]]; then
	# No changed file at all is not evidence that nothing needs testing.
	emit "${groups[@]}"
	exit 0
fi
emit "${selected[@]}"
