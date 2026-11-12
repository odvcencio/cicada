package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
// most Limit+1 body bytes, so a caller never decodes more than its limit. Zero
// reads the whole body.
//
// Known limit: io.LimitReader also stopped the download at Limit. Here a
// response is aborted unread only when it states an uncompressed length over
// Limit, and Studio's own responses never do: GoSX compresses every text
// response of 1 KiB or more (gosx server/server.go, CompressionMiddleware;
// internal/httpcompress/policy.go, MinimumSize and Compressible), and Studio
// does not turn that off. So the browser buffers an oversized Studio response
// whole before the engine refuses it. The cost is memory, from a trusted
// server on loopback (fetch_js.go, readBody).
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

// liveFailureText is what the Live panel says when an action fails: the
// service's own message when it answered, otherwise a plain sentence. The
// technical cause goes to the console (live_js.go).
func liveFailureText(err error) string {
	var failure *liveActionFailure
	if errors.As(err, &failure) {
		return failure.Message
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "Studio took too long to answer, so the last Live action may not have applied. Check that Studio is still running."
	}
	return "The last Live action did not reach Studio or its answer could not be read. Check that Studio is still running."
}

// resampleFailureText is the Sampler's message when its resample request
// fails before Studio answers.
func resampleFailureText(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "Resampling failed: Studio took too long to answer. Your performance is retained; try again."
	}
	return "Resampling failed: the request did not reach Studio. Your performance is retained; check that Studio is still running and try again."
}

// nativeMIDISilence is how long the native MIDI stream may stay silent.
// Studio writes a heartbeat line every second (cmd/cicada/studio_midi_input.go),
// so five seconds without a line means the stream has stalled.
const nativeMIDISilence = 5 * time.Second

const nativeMIDIStalled = "MIDI input stopped responding: nothing arrived for 5 seconds, not even Studio's heartbeat. Reconnecting…"

// midiStreamEnd says how the native MIDI stream ended, from the decoder's
// error: Studio closing the stream cleanly, or the connection breaking.
func midiStreamEnd(err error) string {
	if errors.Is(err, io.EOF) {
		return "MIDI input ended: Studio closed the stream. Connect again to resume."
	}
	return "MIDI input failed: the connection to Studio broke. Connect again to resume."
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
