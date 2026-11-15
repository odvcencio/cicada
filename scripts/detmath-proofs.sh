#!/usr/bin/env bash
# Runs the exhaustive fast-path proofs of kernel/dsp/detmath
# (Test*FastPathExhaustive: every float32 input of each fast path against the
# float64 reference) and fails unless every proof in the source passed.
#
# Usage: scripts/detmath-proofs.sh TOOLCHAIN...
#   TOOLCHAIN is "local" (the go on PATH, which go.mod picks in CI) or a Go
#   toolchain name such as go1.27.3, which the go command fetches (GOTOOLCHAIN).
#   On amd64 each toolchain runs at GOAMD64=v1 and v3, the two levels the
#   project builds for; elsewhere once.
#
# The proofs are listed from the source, so a new proof must pass too.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"
if [[ $# -eq 0 ]]; then
	echo "usage: $0 TOOLCHAIN... (local, go1.27.3, ...)" >&2
	exit 2
fi

mapfile -t proofs < <(grep -ho '^func Test[A-Za-z0-9]*FastPathExhaustive' kernel/dsp/detmath/*_test.go | sed 's/^func //' | sort)
if ((${#proofs[@]} < 4)); then
	echo "found ${#proofs[@]} proofs (Test*FastPathExhaustive) in kernel/dsp/detmath, expected at least 4" >&2
	exit 1
fi

arch="$(go env GOARCH)"
levels=(native)
if [[ "$arch" == amd64 ]]; then levels=(v1 v3); fi
logs="${RUNNER_TEMP:-${TMPDIR:-/tmp}}"
status=0
for toolchain in "$@"; do
	for level in "${levels[@]}"; do
		run=(env CICADA_DETMATH_EXHAUSTIVE=1 GOTOOLCHAIN="$toolchain")
		if [[ "$level" != native ]]; then run+=(GOAMD64="$level"); fi
		log="$logs/detmath-proofs-$toolchain-$arch-$level.log"
		echo "== $("${run[@]}" go version), GOARCH=$arch, level $level: ${proofs[*]}"
		if ! "${run[@]}" go test ./kernel/dsp/detmath -run 'FastPathExhaustive$' -count=1 -v | tee "$log"; then
			status=1
		fi
		# go test exits 0 when -run matches nothing, for example after a rename.
		for name in "${proofs[@]}"; do
			if ! grep -q -- "^--- PASS: $name " "$log"; then
				echo "No PASS line for $name ($toolchain, $arch, $level); did it fail, skip or get renamed?" >&2
				status=1
			fi
		done
	done
done
exit "$status"
