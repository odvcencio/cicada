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
// http.Client.Timeout did, and zero means no timeout.
type nativeRequest struct {
	Method  string
	Path    string // origin-relative, for example "/__actions/live"
	Body    []byte
	Headers map[string]string
	Timeout time.Duration
}

// send makes the request through fetch and returns the whole response. A
// failed exchange returns a *url.Error, `Post "<url>": <cause>`, the shape
// http.Client gave, because some surfaces show it in their status text. A
// cancelled or timed-out exchange has the context's error as its cause.
func (r nativeRequest) send(ctx context.Context, fetch projectionFetch, origin string) (browser.FetchResponse, error) {
	method, target := r.target(origin)
	fail := func(err error) error { return requestError(method, target, err) }
	if ctx == nil {
		return browser.FetchResponse{}, fail(fmt.Errorf("request context is required"))
	}
	if err := ctx.Err(); err != nil {
		return browser.FetchResponse{}, fail(err)
	}
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}
	response, err := fetch(ctx, browser.FetchRequest{URL: target, Method: method, Headers: r.Headers, Body: r.Body})
	if ctx.Err() != nil {
		return browser.FetchResponse{}, fail(ctx.Err())
	}
	if err != nil {
		return browser.FetchResponse{}, fail(err)
	}
	return response, nil
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
