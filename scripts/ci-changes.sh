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
#   m2               M2 AudioWorklet browser tests and the WASM parity jobs
#                    (hosted runners)
#   m2_gates         M2 AudioWorklet CPU gate (the self-hosted reference machine)
#   bench            agent loop speed (cmd/cicada-bench on the built cicada)
#   wav_sweep        WAV parity sweep of every example and the song pins
#                    (amd64, arm64); nightly, and on these pull requests
#
# m2_gates and wav_sweep are decided the other way round: each is on only when
# a changed path is one of its inputs (gate_input and sweep_input below).
# m2_gates runs on a single shared machine, where every queued run waits for
# the one before it; wav_sweep renders every example four ways on two runners
# (about half an hour each). An empty list, --all (push, schedule, manual runs)
# and any change to their inputs still turn them on.
# Always-on jobs (changes, commit hygiene, repo checks, the M0 aggregate) do
# not appear here.
set -euo pipefail

groups=(root studio_cli parity workstation studio_browser macos_desktop macos_contracts m2 m2_gates bench wav_sweep)

# classify_path prints the groups a path turns on, or "all" or "none".
#
# Order matters: the first matching pattern wins.
classify_path() {
	case "$1" in
	# Anything that changes how CI builds or runs: everything runs.
	.github/* | Makefile | scripts/* | go.mod | go.sum)
		echo all ;;
	# Text that a Go //go:embed or a test fixture may read: do not treat as docs.
	# internal/agentguide embeds the quick start that cicada agents prints.
	project/std/* | assets/* | testdata/* | */testdata/* | internal/agentguide/*)
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

# gate_input PATH succeeds when the M2 CPU gate (make build-worklets
# build-kernel-wasm budget-browser, TestBrowserCPUReport) can read PATH or a
# program built from it. On Linux the gate times the kernel WASM in Node on the
# kernel image that Studio compiles from examples/first-acid.cicada, so its
# inputs are: the kernel WASM's Go packages; the image path (project.ReadSources
# and Parse, host/schedule.CompileResident, host/kernelimage.Encode) and its
# packages; the worklet and page in host/web; the Studio handlers and test files
# that serve and time them; the WASM toolchain; and this workflow.
# scripts/ci-changes_test.sh --graph checks the Go part against the import graph.
#
# edit/ is left out on purpose. The image path reaches it in two places, and
# neither can change the image the gate times: project imports it only for
# project/editplan.go, a view of an already compiled project, and the Studio
# handler calls edit.Revision (studioRevision, cmd/cicada/studio.go) for the
# content hash in a response header. The graph test fails if the compile path
# (project outside editplan.go, host/schedule, host/kernelimage) calls EditPlan.
#
# The gate's setup path is in too: studio_files.go (studioProjectRoot feeds
# CompileResident), and isolate_test.go with internal/isolatetest (the gate
# test's TestMain). newStudio also builds takes, history and transport
# (studio_takes.go, studio_history.go, studio_transport.go); the gate's two
# requests read none of them, so a change there can only stop Studio starting,
# which the Studio CLI tests catch on the pull request and the gate itself on
# the push to main.
gate_input() {
	case "$1" in
	.github/workflows/ci.yml | .github/actions/setup-wasm-tools/* | Makefile | go.mod | go.sum) return 0 ;;
	scripts/ci-changes.sh | scripts/wasm-opt.sh | scripts/run-with-timeout.py) return 0 ;;
	# Embedded text is an input even when it is Markdown; other Markdown is docs.
	project/std/* | assets/*) return 0 ;;
	*.md) return 1 ;;
	cmd/cicada-kernel-wasm/* | cmd/cicada-wasm-size/*) return 0 ;;
	cmd/cicada/browser_test.go | cmd/cicada/browser_soak_helpers_test.go | cmd/cicada/browser_test_helpers_test.go | cmd/cicada/browser_cpu_node.js) return 0 ;;
	cmd/cicada/studio.go | cmd/cicada/studio_audio.go | cmd/cicada/studio_files.go | cmd/cicada/isolate_test.go) return 0 ;;
	internal/soaktiming/* | internal/isolatetest/* | internal/paramdefs/* | internal/filetarget/*) return 0 ;;
	kernel/measure/*) return 1 ;; # test-only; nothing the gate builds imports it
	kernel/* | host/web/* | host/schedule/* | host/kernelimage/* | host/sampleasset/* | host/irasset/*) return 0 ;;
	host/keyboard/* | host/instrumentpack/* | host/audioencoding/*) return 0 ;;
	project/* | notation/* | musictime/* | patternmachine/* | instrument/* | edition/* | audioasset/*) return 0 ;;
	sdk/director/* | experimental/expressive/* | assets/* | examples/first-acid.cicada) return 0 ;;
	esac
	return 1
}

# sweep_input PATH succeeds when a change to PATH can move a render's last bits
# on one target only, which the parity subset may not reach: the deterministic
# math every voice and processor calls (and its source guard, in the same
# directory), and the WAV parity test with its pins.
sweep_input() {
	case "$1" in
	kernel/dsp/detmath/* | cmd/cicada/example_wav_parity_test.go) return 0 ;;
	esac
	return 1
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
	result="$(classify_path "${2:?path}")"
	# m2_gates and wav_sweep come from gate_input and sweep_input, not from the
	# rules above.
	if gate_input "$2"; then result="$result m2_gates"; fi
	if sweep_input "$2"; then result="$result wav_sweep"; fi
	echo "$result"
	exit 0
fi
if [[ $# -gt 0 ]]; then
	echo "usage: $0 [--all | --classify PATH] < paths" >&2
	exit 2
fi

selected=()
seen_path=false
everything=false
gates=false
sweep=false
while IFS= read -r path || [[ -n "$path" ]]; do
	if [[ -z "$path" ]]; then continue; fi
	seen_path=true
	if gate_input "$path"; then gates=true; fi
	if sweep_input "$path"; then sweep=true; fi
	result="$(classify_path "$path")"
	if [[ "$result" == all ]]; then
		everything=true
	elif [[ "$result" != none ]]; then
		# shellcheck disable=SC2206
		selected+=($result)
	fi
done

if [[ "$seen_path" == false ]]; then
	# No changed file at all is not evidence that nothing needs testing.
	emit "${groups[@]}"
	exit 0
fi
if [[ "$everything" == true ]]; then
	# "all" turns on every group except m2_gates and wav_sweep, which
	# gate_input and sweep_input decide.
	selected=()
	for group in "${groups[@]}"; do
		if [[ "$group" != m2_gates && "$group" != wav_sweep ]]; then selected+=("$group"); fi
	done
fi
if [[ "$gates" == true ]]; then selected+=(m2_gates); fi
if [[ "$sweep" == true ]]; then selected+=(wav_sweep); fi
emit "${selected[@]}"
