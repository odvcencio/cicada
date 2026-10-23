#!/bin/sh
# Keep explicit tool choices and TinyGo's bundled optimizer; support Binaryen
# installed separately when the TinyGo distribution does not bundle it.
set -eu
if [ -n "${WASMOPT:-}" ]; then
	exec "$WASMOPT" "$@"
fi
if command -v tinygo >/dev/null 2>&1; then
	bundled="$(tinygo env TINYGOROOT)/bin/wasm-opt"
	if [ -x "$bundled" ]; then
		exec "$bundled" "$@"
	fi
fi
if command -v wasm-opt >/dev/null 2>&1; then
	exec wasm-opt "$@"
fi
printf '%s\n' 'wasm-opt not found: install Binaryen on PATH or set WASMOPT to its executable' >&2
exit 127
