//go:build js && wasm

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
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
// Here fetchCall.await registers ordinary then and catch handlers. The
// browser runs them as separate tasks, never inside the call that registers
// them, so a poll cannot nest. Cancellation aborts the request through its
// AbortController, which rejects the pending promise.
//
// The caller sees at most limit+1 body bytes; a body longer than limit means
// the response was larger (see readBody). A limit of zero or less reads the
// whole body.
func nativeFetch(ctx context.Context, request browser.FetchRequest, limit int) (response browser.FetchResponse, err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("browser: fetch: %v", value)
		}
	}()
	call, err := startNativeFetch(ctx, request)
	if err != nil {
		return response, err
	}
	// Cancellation must abort the request before this function returns, so
	// a pending promise rejects and its handlers are released.
	defer func() {
		if ctx.Err() != nil {
			call.abort()
		}
	}()
	response.Status = call.response.Get("status").Int()
	response.StatusText = call.response.Get("statusText").String()
	response.Body, err = call.readBody(limit)
	if err != nil {
		return response, fmt.Errorf("browser: reading response body: %w", err)
	}
	return response, nil
}

// fetchCall is one request in flight.
type fetchCall struct {
	ctx        context.Context
	controller js.Value
	response   js.Value // set once the headers arrive
	aborted    bool
}

// startNativeFetch sends the request and waits for the response headers.
func startNativeFetch(ctx context.Context, request browser.FetchRequest) (*fetchCall, error) {
	if ctx == nil {
		return nil, errors.New("browser: fetch context required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fetch := js.Global().Get("fetch")
	if fetch.Type() != js.TypeFunction {
		return nil, browser.ErrFetchUnavailable
	}
	controllerType := js.Global().Get("AbortController")
	if controllerType.Type() != js.TypeFunction {
		return nil, fmt.Errorf("browser: fetch cancellation: %w", browser.ErrFetchUnavailable)
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
	call := &fetchCall{ctx: ctx, controller: controllerType.New()}
	options := map[string]any{"method": method, "signal": call.controller.Get("signal")}
	if len(headers) > 0 {
		options["headers"] = headers
	}
	if request.Body != nil {
		options["body"] = jsutil.NewUint8ArrayFromBytes(request.Body)
	}
	value, err := call.await(fetch.Invoke(request.URL, options))
	if err != nil {
		return nil, fmt.Errorf("browser: fetch transport: %w", err)
	}
	call.response = value
	return call, nil
}

// abort cancels the request and reports whether the browser accepted the
// abort. A custom fetch implementation can throw instead.
func (c *fetchCall) abort() (accepted bool) {
	if c.aborted {
		return true
	}
	defer func() {
		if recover() != nil {
			accepted = false
		}
	}()
	c.controller.Call("abort")
	c.aborted = true
	return true
}

// await waits for one promise of this request. If ctx ends first, it aborts
// the request so the promise rejects, and releases the handlers when it
// settles. If the abort throws, the promise may never settle, so the handlers
// are released at once; a late settle then only logs "call to released
// function" in the console.
func (c *fetchCall) await(promise js.Value) (js.Value, error) {
	type result struct {
		value js.Value
		err   error
	}
	settled := make(chan result, 1)
	resolve := js.FuncOf(func(_ js.Value, args []js.Value) any {
		value := js.Undefined()
		if len(args) > 0 {
			value = args[0]
		}
		settled <- result{value: value}
		return nil
	})
	reject := js.FuncOf(func(_ js.Value, args []js.Value) any {
		message := "the request failed"
		if len(args) > 0 {
			message = jsErrorMessage(args[0])
		}
		settled <- result{err: errors.New(message)}
		return nil
	})
	release := func() {
		resolve.Release()
		reject.Release()
	}
	promise.Call("then", resolve).Call("catch", reject)
	select {
	case r := <-settled:
		release()
		return r.value, r.err
	case <-c.ctx.Done():
		if c.abort() {
			go func() { <-settled; release() }()
		} else {
			release()
		}
		return js.Undefined(), c.ctx.Err()
	}
}

func jsErrorMessage(value js.Value) string {
	if value.Type() == js.TypeObject {
		if message := value.Get("message"); message.Type() == js.TypeString {
			return message.String()
		}
	}
	return value.String()
}

// readBody returns at most limit+1 bytes of the body (all of it when limit is
// zero or less). A response that states an uncompressed length over the limit
// is aborted before any of its body is read, and its oversize is reported as a
// body of limit+1 bytes. Any other body is read whole, as before, and at most
// limit+1 bytes are copied into Go.
//
// Known limit: Studio's responses are compressed (GoSX compresses text of
// 1 KiB or more), so they state no usable length, and the browser buffers an
// oversized one whole before it is refused. A chunk-by-chunk read through the
// stream reader would stop at the limit; it was tried and withdrawn because
// Chrome sometimes reported a fully read response as net::ERR_ABORTED at the
// moment the reader saw the end (2 of 151 streamed reads in one benchmark
// run, none of 400 in a plain JavaScript loop), which hung the latency
// benchmark's response checks. The cause is not known.
func (c *fetchCall) readBody(limit int) ([]byte, error) {
	if limit > 0 && c.contentLength() > limit {
		c.abort()
		return make([]byte, limit+1), nil
	}
	buffer, err := c.await(c.response.Call("arrayBuffer"))
	if err != nil {
		return nil, err
	}
	array := js.Global().Get("Uint8Array").New(buffer)
	if n := array.Get("byteLength").Int(); limit > 0 && n > limit+1 {
		array = array.Call("subarray", 0, limit+1)
	}
	body := make([]byte, array.Get("byteLength").Int())
	js.CopyBytesToGo(body, array)
	return body, nil
}

// contentLength is the body's length when the response states it and is not
// compressed (a compressed body decodes to more bytes than it states), or -1.
func (c *fetchCall) contentLength() int {
	headers := c.response.Get("headers")
	if encoding := headers.Call("get", "content-encoding"); encoding.Type() == js.TypeString && encoding.String() != "" && encoding.String() != "identity" {
		return -1
	}
	value := headers.Call("get", "content-length")
	if value.Type() != js.TypeString {
		return -1
	}
	n, err := strconv.Atoi(value.String())
	if err != nil || n < 0 {
		return -1
	}
	return n
}

// nativeFetchStream is nativeFetch for a response read as it arrives, such
// as the MIDI input stream, which stays open while a controller plays. Each
// read waits like nativeFetch. Closing the body, or cancelling ctx (as an
// engine's dispose does), aborts the request at once, even between events.
func nativeFetchStream(ctx context.Context, request browser.FetchRequest) (status int, body io.ReadCloser, err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("browser: fetch: %v", value)
		}
	}()
	call, err := startNativeFetch(ctx, request)
	if err != nil {
		return 0, nil, err
	}
	stream := call.response.Get("body")
	if stream.IsNull() || stream.IsUndefined() {
		call.abort()
		return 0, nil, errors.New("browser: fetch: the response has no readable body")
	}
	return call.response.Get("status").Int(), &nativeStreamBody{call: call, reader: stream.Call("getReader")}, nil
}

var errStreamClosed = errors.New("browser: response body is closed")

type nativeStreamBody struct {
	call    *fetchCall
	reader  js.Value
	pending []byte
	err     error
}

func (b *nativeStreamBody) Read(p []byte) (n int, err error) {
	defer func() {
		if value := recover(); value != nil {
			b.err = fmt.Errorf("browser: reading response body: %v", value)
			b.call.abort()
			n, err = 0, b.err
		}
	}()
	if b.err != nil {
		return 0, b.err
	}
	if len(b.pending) == 0 {
		if err := b.call.ctx.Err(); err != nil {
			b.err = err
			b.call.abort()
			return 0, err
		}
		result, err := b.call.await(b.reader.Call("read"))
		if err != nil {
			if b.call.ctx.Err() != nil {
				err = b.call.ctx.Err()
			} else {
				err = fmt.Errorf("browser: reading response body: %w", err)
			}
			b.err = err
			b.call.abort()
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
	b.call.abort()
	return nil
}

// consoleWarn keeps a request's technical cause in the browser console, where
// the status line shows a plain sentence.
func consoleWarn(what string, err error) {
	defer func() { _ = recover() }()
	js.Global().Get("console").Call("warn", "Cicada: "+what+": "+err.Error())
}

// do sends a request to this page's origin and reads its response, at most
// r.Limit+1 bytes of the body.
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
