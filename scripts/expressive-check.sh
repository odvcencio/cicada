#!/usr/bin/env bash
# Run actual native and Node-backed WASM DSP tests. Browser AudioWorklet and
# listening acceptance are separate and are not certified by these checks.
set -euo pipefail
cd "$(dirname "$0")/.."
GO_BIN="${GO_BIN:-go}"
"$GO_BIN" test ./experimental/expressive -count=1 -v
"$GO_BIN" test ./experimental/expressive -run '^$' -bench BenchmarkVoice -benchmem -benchtime=1s
GOROOT_DIR="$("$GO_BIN" env GOROOT)"
WASM_EXEC="$GOROOT_DIR/lib/wasm/go_js_wasm_exec"
if [[ ! -x "$WASM_EXEC" ]]; then
  WASM_EXEC="$GOROOT_DIR/misc/wasm/go_js_wasm_exec"
fi
command -v node >/dev/null
GOOS=js GOARCH=wasm "$GO_BIN" test ./experimental/expressive -exec "$WASM_EXEC" -count=1 -v
