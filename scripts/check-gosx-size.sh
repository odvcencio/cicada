#!/usr/bin/env bash
# Canonical GoSX sizes require its exact compiler and Brotli implementation.
# Keep this tool outside the app module graph; never downgrade app dependencies.
# Arguments and relative paths have the same meaning as `gosx size` from here.
set -euo pipefail

if [[ $# == 0 ]]; then
	echo "usage: scripts/check-gosx-size.sh [gosx size flags] <dist|build.json>" >&2
	exit 2
fi

repo="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
audit_tmp="$(mktemp -d "${TMPDIR:-/tmp}/cicada-gosx-size.XXXXXX")"
trap 'rm -rf -- "$audit_tmp"' EXIT

# Build for the host even when the app build exports a target GOOS/GOARCH.
# An explicit modfile also prevents a caller's GOFLAGS from selecting app deps.
GOWORK=off GOTOOLCHAIN=go1.26.0 GOOS= GOARCH= CGO_ENABLED=0 \
	go -C "$repo/tools/gosx-size" build -mod=readonly \
	-modfile "$repo/tools/gosx-size/go.mod" -trimpath \
	-o "$audit_tmp/gosx" m31labs.dev/gosx/cmd/gosx

# Keep the caller's directory so every SDK flag and relative target still works.
"$audit_tmp/gosx" size "$@"
