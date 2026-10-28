//go:build js && wasm

package main

import (
	"context"
	"errors"
	"fmt"
	"syscall/js"

	"m31labs.dev/gosx/client/browser"
	"m31labs.dev/gosx/client/jsutil"
)

// nativeFetch reads one resource through the browser's fetch and returns its
// status and body, as browser.Fetch does. It differs in how it waits.
//
// browser.Fetch waits through jsutil.AwaitPromiseContext, which builds a
// Promise with a Go function as its executor. A Promise runs its executor
// before the constructor returns, so each fetch ran Go from inside the call
// that Go made to JavaScript. Go's js/wasm runtime keeps one global stack of
// those nested events and assumes that only one goroutine at a time pushes to
// it. The Studio loops poll together, so several goroutines did. An
// allocation inside the push can pause the goroutine while a garbage
// collection runs. If another goroutine pushed its own event meanwhile, the
// first goroutine's write-back dropped it. Go then resumed the wrong goroutine
// and the engine stopped with "traceback did not unwind completely", which
// left the workspace marked busy ("Saving…") for good.
//
// Here awaitPromise registers ordinary then and catch handlers. The browser
// runs them as separate tasks, never inside the call that registers them, so a
// poll cannot nest. Cancellation aborts the request through its
// AbortController, which rejects the pending promise.
func nativeFetch(ctx context.Context, request browser.FetchRequest) (response browser.FetchResponse, err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("browser: fetch: %v", value)
		}
	}()
	if ctx == nil {
		return response, errors.New("browser: fetch context required")
	}
	if err = ctx.Err(); err != nil {
		return response, err
	}
	fetch := js.Global().Get("fetch")
	if fetch.Type() != js.TypeFunction {
		return response, browser.ErrFetchUnavailable
	}
	controllerType := js.Global().Get("AbortController")
	if controllerType.Type() != js.TypeFunction {
		return response, fmt.Errorf("browser: fetch cancellation: %w", browser.ErrFetchUnavailable)
	}
	method := request.Method
	if method == "" {
		method = "GET"
	}
	headers := map[string]any{}
	if request.ContentType != "" {
		headers["Content-Type"] = request.ContentType
	}
	for name, value := range request.Headers {
		headers[name] = value
	}
	controller := controllerType.New()
	options := map[string]any{"method": method, "signal": controller.Get("signal")}
	if len(headers) > 0 {
		options["headers"] = headers
	}
	if request.Body != nil {
		options["body"] = jsutil.NewUint8ArrayFromBytes(request.Body)
	}
	// Cancellation must abort the request before this function returns, so
	// the pending promise rejects and awaitPromise can release its handlers.
	defer func() {
		if ctx.Err() != nil {
			defer func() { _ = recover() }() // a custom transport can throw on abort
			controller.Call("abort")
		}
	}()
	value, err := awaitPromise(ctx, fetch.Invoke(request.URL, options))
	if err != nil {
		return response, fmt.Errorf("browser: fetch transport: %w", err)
	}
	response.Status = value.Get("status").Int()
	response.StatusText = value.Get("statusText").String()
	body, err := awaitPromise(ctx, value.Call("arrayBuffer"))
	if err != nil {
		return response, fmt.Errorf("browser: reading response body: %w", err)
	}
	array := js.Global().Get("Uint8Array").New(body)
	response.Body = make([]byte, array.Get("byteLength").Int())
	js.CopyBytesToGo(response.Body, array)
	return response, nil
}
