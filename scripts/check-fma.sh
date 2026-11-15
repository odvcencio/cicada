#!/usr/bin/env bash
# Fail when the compiler would fuse a multiply-add on the render path.
#
# Usage, from the repository root (the target comes from the environment):
#   GOARCH=arm64 scripts/check-fma.sh
#   GOARCH=amd64 GOAMD64=v3 scripts/check-fma.sh
#
# Go may compute x*y + z with one rounding instead of two on arm64 and with
# GOAMD64=v3, so the same score would render to different bits there. This
# builds kernel/, render/ and cmd/cicada with every package of this module
# that they import, asks the compiler to report each product it would fuse
# (-d=fmahash=vy), and fails on any report outside the exemptions below. Each
# report line starts with the position of the fused product: where it was
# inlined, then the inlined code. See docs/audio/mix-chain.md.
set -euo pipefail

# Code whose results people and agents read but that never produces audio, an
# exported file or a decision about either:
#   host/modalfit/       offline modal fitting; the library stores its results
#                        and renders from there
#   host/scoreaudition/  audition measurements; it reads audio, it does not make it
#   host/transcription/  transcription of recorded audio into notes: authoring
#   language/theme.go    the colours the editor theme gives notes
exempt='^fmahash triggered (host/modalfit/|host/scoreaudition/|host/transcription/|language/theme\.go:)'

out="$(mktemp "${TMPDIR:-/tmp}/cicada-fma.XXXXXX")"
trap 'rm -f -- "$out"' EXIT
if ! go build -gcflags='m31labs.dev/cicada/...=-d=fmahash=vy' ./kernel/... ./render/... ./cmd/cicada >"$out" 2>&1; then
	cat "$out" >&2
	exit 1
fi
report="$(grep -E '^fmahash triggered' "$out" | grep -vE "$exempt" || true)"
if [[ -n "$report" ]]; then
	echo "$report"
	echo "The compiler would fuse the multiply-adds listed above (GOARCH=$(go env GOARCH) GOAMD64=$(go env GOAMD64)). Round each product with float32(...) or float64(...); see docs/audio/mix-chain.md." >&2
	exit 1
fi
echo "no fusable multiply-adds on the render path (GOARCH=$(go env GOARCH) GOAMD64=$(go env GOAMD64))"
