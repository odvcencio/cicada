//go:build js && wasm

package main

import (
	"context"
	"syscall/js"
	"time"
)

// Studio loops poll the native service several times a second. They read it
// with nativeFetch so that a poll never re-enters Go from inside a call to
// JavaScript (see nativeFetch).
func readNativeJSON(ctx context.Context, path string, timeout time.Duration, maxBytes int, target any) error {
	return readJSONProjection(ctx, nativeFetch, js.Global().Get("location").Get("origin").String()+path, timeout, maxBytes, target)
}
