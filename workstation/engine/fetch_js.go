//go:build js && wasm

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	value, abort, err := startNativeFetch(ctx, request)
	if err != nil {
		return response, err
	}
	// Cancellation must abort the request before this function returns, so
	// the pending promise rejects and awaitPromise can release its handlers.
	defer func() {
		if ctx.Err() != nil {
			abort()
		}
	}()
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

// startNativeFetch sends the request and waits for the response headers. The
// returned abort cancels the request; it is safe to call more than once.
func startNativeFetch(ctx context.Context, request browser.FetchRequest) (js.Value, func(), error) {
	if ctx == nil {
		return js.Undefined(), nil, errors.New("browser: fetch context required")
	}
	if err := ctx.Err(); err != nil {
		return js.Undefined(), nil, err
	}
	fetch := js.Global().Get("fetch")
	if fetch.Type() != js.TypeFunction {
		return js.Undefined(), nil, browser.ErrFetchUnavailable
	}
	controllerType := js.Global().Get("AbortController")
	if controllerType.Type() != js.TypeFunction {
		return js.Undefined(), nil, fmt.Errorf("browser: fetch cancellation: %w", browser.ErrFetchUnavailable)
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
	abort := func() {
		defer func() { _ = recover() }() // a custom transport can throw on abort
		controller.Call("abort")
	}
	value, err := awaitPromise(ctx, fetch.Invoke(request.URL, options))
	if err != nil {
		if ctx.Err() != nil {
			abort()
		}
		return js.Undefined(), nil, fmt.Errorf("browser: fetch transport: %w", err)
	}
	return value, abort, nil
}

// nativeFetchStream is nativeFetch for a response read as it arrives, such
// as the MIDI input stream, which stays open while a controller plays. Each
// read waits through awaitPromise, like nativeFetch. Closing the body, or
// cancelling ctx during a read, aborts the request.
func nativeFetchStream(ctx context.Context, request browser.FetchRequest) (status int, body io.ReadCloser, err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("browser: fetch: %v", value)
		}
	}()
	value, abort, err := startNativeFetch(ctx, request)
	if err != nil {
		return 0, nil, err
	}
	stream := value.Get("body")
	if stream.IsNull() || stream.IsUndefined() {
		abort()
		return 0, nil, errors.New("browser: fetch: the response has no readable body")
	}
	return value.Get("status").Int(), &nativeStreamBody{ctx: ctx, reader: stream.Call("getReader"), abort: abort}, nil
}

var errStreamClosed = errors.New("browser: response body is closed")

type nativeStreamBody struct {
	ctx     context.Context
	reader  js.Value
	abort   func()
	pending []byte
	err     error
}

func (b *nativeStreamBody) Read(p []byte) (n int, err error) {
	defer func() {
		if value := recover(); value != nil {
			b.err = fmt.Errorf("browser: reading response body: %v", value)
			b.abort()
			n, err = 0, b.err
		}
	}()
	if b.err != nil {
		return 0, b.err
	}
	if len(b.pending) == 0 {
		if err := b.ctx.Err(); err != nil {
			b.err = err
			b.abort()
			return 0, err
		}
		result, err := awaitPromise(b.ctx, b.reader.Call("read"))
		if err != nil {
			if b.ctx.Err() != nil {
				err = b.ctx.Err()
			} else {
				err = fmt.Errorf("browser: reading response body: %w", err)
			}
			b.err = err
			b.abort()
			return 0, err
		}
		if result.Get("done").Bool() {
			b.err = io.EOF
			return 0, io.EOF
		}
		chunk := result.Get("value")
		b.pending = make([]byte, chunk.Get("byteLength").Int())
		js.CopyBytesToGo(b.pending, chunk)
	}
	n = copy(p, b.pending)
	b.pending = b.pending[n:]
	return n, nil
}

func (b *nativeStreamBody) Close() error {
	if b.err == nil || b.err == io.EOF {
		b.err = errStreamClosed
	}
	b.abort()
	return nil
}

// do sends a request to this page's origin and reads the whole response.
func (r nativeRequest) do(ctx context.Context) (browser.FetchResponse, error) {
	return r.send(ctx, nativeFetch, js.Global().Get("location").Get("origin").String())
}

// stream sends a request to this page's origin and returns the status and
// the body as it arrives; the caller closes the body. It has no timeout:
// the stream lasts until ctx ends or the server closes it.
func (r nativeRequest) stream(ctx context.Context) (int, io.ReadCloser, error) {
	method, target := r.target(js.Global().Get("location").Get("origin").String())
	status, body, err := nativeFetchStream(ctx, browser.FetchRequest{URL: target, Method: method, Headers: r.Headers, Body: r.Body})
	if err != nil {
		if ctx != nil && ctx.Err() != nil {
			err = ctx.Err()
		}
		return 0, nil, requestError(method, target, err)
	}
	return status, body, nil
}
