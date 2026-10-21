//go:build js && wasm

package main

import (
	"context"
	"syscall/js"
	"time"

	"m31labs.dev/gosx/client/browser"
)

func readNativeJSON(ctx context.Context, path string, timeout time.Duration, maxBytes int, target any) error {
	return readJSONProjection(ctx, browser.Fetch, js.Global().Get("location").Get("origin").String()+path, timeout, maxBytes, target)
}
