#!/usr/bin/env bash
# Tests for scripts/ci-changes.sh.
#
#   scripts/ci-changes_test.sh                  table tests (no Go needed)
#   scripts/ci-changes_test.sh --graph          also check the root-module import graph
#   scripts/ci-changes_test.sh --graph-workstation
#                                               check the workstation module's imports
#                                               (needs the Go version workstation/go.mod asks for)
#
# The graph checks prove the narrow rules in ci-changes.sh. For each job
# group that has a Go entry point, every root-module package it imports must
# turn that group on. If someone makes cmd/cicada import kernel/measure, the
# kernel/measure rule would skip Studio jobs for a change they need, and this
# test fails.
set -euo pipefail

here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd -- "$here/.." && pwd)"
classifier="$here/ci-changes.sh"
failures=0

fail() {
	echo "FAIL: $*" >&2
	failures=$((failures + 1))
}

all_groups="root studio_cli parity workstation studio_browser macos_desktop macos_contracts m2"

# expect_groups "name" "expected groups (space separated, or none)" paths...
expect_groups() {
	local name="$1" expected="$2"
	shift 2
	local output actual group
	output="$(printf '%s\n' "$@" | "$classifier")"
	actual=""
	for group in $all_groups; do
		if grep -qx "$group=true" <<<"$output"; then actual="$actual $group"; fi
		if ! grep -qx "$group=\(true\|false\)" <<<"$output"; then
			fail "$name: output has no line for $group"
		fi
	done
	actual="${actual# }"
	if [[ "$expected" == all ]]; then expected="$all_groups"; fi
	if [[ "$expected" == none ]]; then expected=""; fi
	if [[ "$actual" != "$expected" ]]; then
		fail "$name: got [$actual] want [$expected]"
	fi
}

# --- paths that turn everything on (the conservative fallbacks) ---
expect_groups "workflow file" all .github/workflows/ci.yml
expect_groups "local composite action" all .github/actions/setup-wasm-tools/action.yml
expect_groups "PR template (a .md under .github)" all .github/pull_request_template.md
expect_groups "Makefile" all Makefile
expect_groups "scripts dir" all scripts/check-commit-hygiene.sh
expect_groups "the classifier itself" all scripts/ci-changes.sh
expect_groups "the classifier test" all scripts/ci-changes_test.sh
expect_groups "go.mod" all go.mod
expect_groups "go.sum" all go.sum
expect_groups "unknown top-level file" all some-new-file.txt
expect_groups "unknown top-level directory" all brand-new-dir/thing.go
expect_groups "gitignore" all .gitignore
expect_groups "empty input" all
expect_groups "blank lines only" all ""
expect_groups "embedded std library text" all project/std/README.md
expect_groups "asset readme" all assets/ir/README.md
expect_groups "fixture readme" all host/recording/testdata/README.md
expect_groups "top-level testdata" all testdata/edition1/examples/first-acid.cicada
expect_groups "example score" all examples/first-acid.cicada
expect_groups "root Go package" all notation/parse.go
expect_groups "command" all cmd/cicada/studio.go
expect_groups "audio kernel" all kernel/graph/graph.go
expect_groups "audio voice" all kernel/voice/strings/strings.go
expect_groups "host package" all host/web/processor.js
expect_groups "one unknown path among docs" all docs/manual/README.md mystery.bin
expect_groups "one CI path among docs" all docs/manual/README.md .github/workflows/ci.yml
expect_groups "one kernel file among workstation files" all workstation/panels.go kernel/graph/graph.go

# --- source and module files are never docs ---
expect_groups "Go file under docs" all docs/manual/example.go
expect_groups "Go file under editors" all editors/vscode/tool.go
expect_groups "Go file under deploy" all deploy/demo/main.go
expect_groups "desktop go.mod" all desktop/go.mod
expect_groups "desktop go.sum" all desktop/go.sum
expect_groups "measurement testdata" all kernel/measure/testdata/tolerances.json
expect_groups "measurement README stays docs" none kernel/measure/README.md
expect_groups "desktop README stays docs" none desktop/README.md

# --- documentation and files no job reads ---
expect_groups "docs only" none docs/manual/README.md docs/spec/features.md
expect_groups "root README" none README.md
expect_groups "license" none LICENSE
expect_groups "nested readme" none kernel/voice/clav/README.md
expect_groups "docs json evidence" none docs/sampler/evidence/browser.json
expect_groups "editor extension" none editors/vscode/package.json
expect_groups "deploy files" none deploy/demo/README.md
expect_groups "workstation readme" none workstation/README.md

# --- the workstation module and desktop ---
expect_groups "Studio front end" "workstation studio_browser macos_desktop" workstation/panels.go
expect_groups "Studio browser suite" "workstation studio_browser macos_desktop" workstation/browser/shell.test.cjs
expect_groups "Studio ui output" "workstation studio_browser macos_desktop" workstation/ui/app.gsx
expect_groups "workstation go.mod is read by buildaudit" all workstation/go.mod
expect_groups "workstation go.sum" all workstation/go.sum
expect_groups "gosx size tool module file" all tools/gosx-size/go.mod
expect_groups "gosx size tool" "root workstation studio_browser macos_desktop" tools/gosx-size/main.go
expect_groups "desktop shell" "workstation macos_desktop macos_contracts" desktop/main.go
expect_groups "docs plus Studio front end" "workstation studio_browser macos_desktop" docs/manual/README.md workstation/panels.go
expect_groups "Studio front end plus desktop" "workstation studio_browser macos_desktop macos_contracts" workstation/panels.go desktop/main.go

# --- test-only kernel packages ---
expect_groups "measurement suite" "root" kernel/measure/model_test.go
expect_groups "measurement suite and docs" "root" kernel/measure/README.md kernel/measure/model.go

# --- --all, --classify, and bad usage ---
output="$("$classifier" --all)"
for group in $all_groups; do
	grep -qx "$group=true" <<<"$output" || fail "--all: $group is not true"
done
if [[ "$("$classifier" --classify docs/a.md)" != none ]]; then fail "--classify docs/a.md"; fi
if [[ "$("$classifier" --classify kernel/x.go)" != all ]]; then fail "--classify kernel/x.go"; fi
if "$classifier" --bogus </dev/null >/dev/null 2>&1; then fail "unknown flag was accepted"; fi
# A last line with no newline still counts.
if [[ "$(printf 'README.md' | "$classifier" | grep -c '=false')" != 8 ]]; then fail "unterminated last line"; fi

# classify_for GROUP PATH: does PATH turn GROUP on?
turns_on() {
	local group="$1" path="$2" output
	output="$(printf '%s\n' "$path" | "$classifier")"
	grep -qx "$group=true" <<<"$output"
}

# check_imports GROUP LABEL: read import paths on stdin; each root-module
# package must turn GROUP on.
check_imports() {
	local group="$1" label="$2" pkg dir checked=0
	while IFS= read -r pkg; do
		[[ "$pkg" == m31labs.dev/cicada/* ]] || continue
		dir="${pkg#m31labs.dev/cicada/}"
		# Packages of the other modules are not root-module inputs.
		case "$dir" in workstation | workstation/* | desktop | desktop/*) continue ;; esac
		checked=$((checked + 1))
		if ! turns_on "$group" "$dir/x.go"; then
			fail "$label imports $dir but a change there does not turn $group on"
		fi
	done
	if [[ "$checked" == 0 ]]; then fail "$label: found no imports; is go list working?"; fi
}

go_deps() { # go_deps GOOS TAGS PACKAGE...  (run in the root module)
	local goos="$1" tags="$2"
	shift 2
	(cd "$repo" && GOWORK=off GOOS="$goos" go list -tags "$tags" -deps -test -f '{{.ImportPath}}' "$@" |
		sed 's/ \[.*//; s/_test$//; s/\.test$//' | sort -u)
}

if [[ "${1:-}" == "--graph" ]]; then
	check_imports studio_cli "cmd/cicada" < <(go_deps linux "" ./cmd/cicada)
	check_imports parity "cmd/cicada-kernel-wasm" < <(go_deps linux "wasm_integration" ./cmd/cicada-kernel-wasm)
	check_imports m2 "M2 gates" < <(go_deps linux "wasm_integration chord_wasm browser" \
		./cmd/cicada-kernel-wasm ./cmd/cicada ./host/demobrowser ./cmd/cicada-demo ./host/wam2)
	check_imports studio_browser "Studio browser (cmd/cicada and the kernel WASM)" < <(go_deps linux "" ./cmd/cicada ./cmd/cicada-kernel-wasm)
	check_imports macos_desktop "macOS desktop build of cmd/cicada" < <(go_deps darwin "" ./cmd/cicada)
	check_imports macos_contracts "macOS platform contracts" < <(go_deps darwin "" ./host/capture ./host/midi ./internal/audiobackend ./cmd/cicada)
	check_imports workstation "workstation job (root-module tests)" < <(go_deps linux "" ./internal/buildaudit ./internal/studiohttp)
	# The workstation rules assume no root package imports the other modules.
	if (cd "$repo" && GOWORK=off go list -deps -test -f '{{.ImportPath}}' ./... | grep -E '^m31labs.dev/cicada/(workstation|desktop)(/|$)'); then
		fail "a root-module package imports workstation or desktop; the workstation rules would skip it"
	fi
fi

if [[ "${1:-}" == "--graph-workstation" ]]; then
	# Run where the toolchain that workstation/go.mod asks for is installed.
	check_imports workstation "workstation module" < <(cd "$repo/workstation" && GOWORK=off go list -deps -test -f '{{.ImportPath}}' ./... | sed 's/ \[.*//; s/_test$//; s/\.test$//' | sort -u)
	check_imports studio_browser "workstation module (Studio release build)" < <(cd "$repo/workstation" && GOWORK=off go list -deps -f '{{.ImportPath}}' ./... | sort -u)
	check_imports macos_desktop "workstation module (macOS app)" < <(cd "$repo/workstation" && GOWORK=off go list -deps -f '{{.ImportPath}}' ./... | sort -u)
fi

if [[ "$failures" -gt 0 ]]; then
	echo "$failures failure(s)" >&2
	exit 1
fi
echo "ci-changes: all checks passed"
