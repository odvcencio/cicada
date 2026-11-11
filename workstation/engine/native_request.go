package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"m31labs.dev/gosx/client/browser"
)

// The engine sends its requests through the browser's fetch (nativeFetch),
// not net/http. These names replace net/http's method and status constants.
const (
	methodGet  = "GET"
	methodPost = "POST"

	statusOK                  = 200
	statusBadRequest          = 400
	statusNotFound            = 404
	statusConflict            = 409
	statusUnprocessableEntity = 422
)

// nativeRequest is one request to Studio's own origin. It replaces an
// http.Client call: Timeout bounds the whole exchange, body included, as
// http.Client.Timeout did, and zero means no timeout. Limit is the most body
// bytes the caller reads, as io.LimitReader did: the response it returns has at
// most Limit+1 body bytes, and a response that states a larger length is
// aborted before its body is read (fetch_js.go, readBody). Zero reads the
// whole body.
type nativeRequest struct {
	Method  string
	Path    string // origin-relative, for example "/__actions/live"
	Body    []byte
	Headers map[string]string
	Timeout time.Duration
	Limit   int
}

// limitedFetch sends one request and reads at most limit+1 body bytes
// (nativeFetch in the browser).
type limitedFetch func(ctx context.Context, request browser.FetchRequest, limit int) (browser.FetchResponse, error)

// send makes the request through fetch and returns the response.
//
// A failed exchange returns a *url.Error, `Post "<url>": <cause>`, the shape
// http.Client gave, because some surfaces show it in their status text.
//
// Results, in order:
//   - When ctx ends (the engine was disposed or the caller cancelled), send
//     returns ctx's error, even for a response that completed at the same
//     moment: a result is never delivered to a disposed engine.
//   - A completed response is returned even if the Timeout fired as it
//     arrived.
//   - When the Timeout fires first, the cause is context.DeadlineExceeded.
//   - Otherwise the transport's error is the cause.
func (r nativeRequest) send(ctx context.Context, fetch limitedFetch, origin string) (browser.FetchResponse, error) {
	method, target := r.target(origin)
	fail := func(err error) (browser.FetchResponse, error) {
		return browser.FetchResponse{}, requestError(method, target, err)
	}
	if ctx == nil {
		return fail(fmt.Errorf("request context is required"))
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	exchange := ctx
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		exchange, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}
	response, err := fetch(exchange, browser.FetchRequest{URL: target, Method: method, Headers: r.Headers, Body: r.Body}, r.Limit)
	switch {
	case ctx.Err() != nil:
		return fail(ctx.Err())
	case err == nil:
		return response, nil
	case exchange.Err() != nil:
		return fail(exchange.Err())
	default:
		return fail(err)
	}
}

func (r nativeRequest) target(origin string) (method, target string) {
	method = r.Method
	if method == "" {
		method = methodGet
	}
	return method, origin + r.Path
}

// requestError gives a failed exchange the shape http.Client gave it.
func requestError(method, target string, err error) error {
	return &url.Error{Op: method[:1] + strings.ToLower(method[1:]), URL: target, Err: err}
}

// limitBody returns at most n bytes of body: what io.LimitReader(body, n)
// let the old net/http call sites read.
func limitBody(body []byte, n int) []byte {
	if len(body) > n {
		return body[:n]
	}
	return body
}

// decodeLimited decodes the first JSON value in at most n bytes of body, as
// json.NewDecoder(io.LimitReader(body, n)).Decode did. A larger value is cut
// off and fails to decode.
func decodeLimited(body []byte, n int, target any) error {
	return json.NewDecoder(bytes.NewReader(limitBody(body, n))).Decode(target)
}

type liveActionFailure struct {
	Status  int
	Message string
}

func (e *liveActionFailure) Error() string { return e.Message }

// liveActionResult reads the receipt of a Live action (POST /__actions/<action>).
func liveActionResult(action string, response browser.FetchResponse) (json.RawMessage, error) {
	var result struct {
		OK      bool            `json:"ok"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := decodeLimited(response.Body, 1<<20, &result); err != nil {
		return nil, err
	}
	if response.Status < 200 || response.Status >= 300 || !result.OK {
		if result.Message == "" {
			result.Message = fmt.Sprintf("Live action failed (%d)", response.Status)
		}
		if action == "live" && response.Status == statusConflict {
			result.Message += ". Reopen Live to resume."
		}
		return nil, &liveActionFailure{Status: response.Status, Message: result.Message}
	}
	return result.Data, nil
}

// noteRecoveryResult reads GET /api/note-recovery/<id>. found is false when
// the service has no copy (404), which is not an error.
func noteRecoveryResult(response browser.FetchResponse, saved any) (found bool, err error) {
	if response.Status == statusNotFound {
		return false, nil
	}
	if response.Status != statusOK {
		return false, fmt.Errorf("recovery unavailable")
	}
	if err := decodeLimited(response.Body, 16<<20, saved); err != nil {
		return false, err
	}
	return true, nil
}

// midiInputRefusal says why the service refused the MIDI input stream, from
// at most 4 KiB of its body: the JSON error, or the text.
func midiInputRefusal(body io.Reader) string {
	data, _ := io.ReadAll(io.LimitReader(body, 4096))
	var failure struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &failure) == nil && failure.Error != "" {
		return failure.Error
	}
	return "MIDI could not connect. " + strings.TrimSpace(string(data))
}
