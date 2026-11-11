package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"m31labs.dev/gosx/client/browser"
)

const testOrigin = "http://127.0.0.1:8191"

// blockingFetch waits for its context like nativeFetch: cancellation aborts
// the request, which rejects the fetch promise.
func blockingFetch(entered chan<- context.Context) limitedFetch {
	return func(ctx context.Context, _ browser.FetchRequest, _ int) (browser.FetchResponse, error) {
		entered <- ctx
		<-ctx.Done()
		return browser.FetchResponse{}, fmt.Errorf("browser: fetch transport: %w", ctx.Err())
	}
}

func liveRequest(timeout time.Duration) nativeRequest {
	return nativeRequest{Method: methodPost, Path: "/__actions/live", Body: []byte(`{"type":"heartbeat"}`), Timeout: timeout, Headers: map[string]string{
		"Content-Type": "application/json", "Accept": "application/json", "X-CSRF-Token": "token",
	}}
}

func TestNativeRequestSendsMethodHeadersBodyAndTimeout(t *testing.T) {
	var seen browser.FetchRequest
	var during context.Context
	fetch := func(ctx context.Context, request browser.FetchRequest, limit int) (browser.FetchResponse, error) {
		seen, during = request, ctx
		if limit != 1<<20 {
			t.Fatalf("limit = %d, want the caller's 1 MiB", limit)
		}
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 2*time.Second {
			t.Fatal("the request lost its timeout")
		}
		return browser.FetchResponse{Status: 200, Body: []byte(`{"ok":true,"data":{"lease":"a"}}`)}, nil
	}
	request := liveRequest(2 * time.Second)
	request.Limit = 1 << 20
	response, err := request.send(t.Context(), fetch, testOrigin)
	if err != nil {
		t.Fatal(err)
	}
	if seen.URL != testOrigin+"/__actions/live" || seen.Method != "POST" || string(seen.Body) != `{"type":"heartbeat"}` || seen.Headers["X-CSRF-Token"] != "token" || seen.Headers["Content-Type"] != "application/json" {
		t.Fatalf("request changed: %+v", seen)
	}
	if !errors.Is(during.Err(), context.Canceled) {
		t.Fatal("a finished request kept its timeout context")
	}
	data, err := liveActionResult("live", response)
	if err != nil || string(data) != `{"lease":"a"}` {
		t.Fatalf("receipt = %s, %v", data, err)
	}
	// No Timeout means no deadline, as http.Client{} without Timeout.
	_, _ = nativeRequest{Path: "/api/midi/ports"}.send(t.Context(), func(ctx context.Context, request browser.FetchRequest, _ int) (browser.FetchResponse, error) {
		if _, ok := ctx.Deadline(); ok || request.Method != "GET" {
			t.Fatalf("untimed GET changed: deadline %v, method %q", ok, request.Method)
		}
		return browser.FetchResponse{Status: 200}, nil
	}, testOrigin)
}

func TestNativeRequestTimeoutKeepsClientErrorShape(t *testing.T) {
	entered := make(chan context.Context, 1)
	_, err := liveRequest(20*time.Millisecond).send(t.Context(), blockingFetch(entered), testOrigin)
	var failure *url.Error
	if !errors.As(err, &failure) || failure.Op != "Post" || failure.URL != testOrigin+"/__actions/live" {
		t.Fatalf("timeout error lost its shape: %#v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) || !failure.Timeout() {
		t.Fatalf("timeout is not reported as a timeout: %v", err)
	}
	if want := `Post "` + testOrigin + `/__actions/live": context deadline exceeded`; err.Error() != want {
		t.Fatalf("error text = %q, want %q", err, want)
	}
	if (<-entered).Err() == nil {
		t.Fatal("the timed-out request was not cancelled, so fetch would not abort")
	}
}

func TestNativeRequestNetworkErrorIsWrapped(t *testing.T) {
	offline := errors.New("browser: fetch transport: Failed to fetch")
	_, err := nativeRequest{Method: methodGet, Path: "/api/note-recovery/take", Timeout: 2 * time.Second}.send(t.Context(), func(context.Context, browser.FetchRequest, int) (browser.FetchResponse, error) {
		return browser.FetchResponse{}, offline
	}, testOrigin)
	var failure *url.Error
	if !errors.As(err, &failure) || failure.Op != "Get" || !errors.Is(err, offline) {
		t.Fatalf("network error = %#v", err)
	}
	if want := `Get "` + testOrigin + `/api/note-recovery/take": browser: fetch transport: Failed to fetch`; err.Error() != want {
		t.Fatalf("error text = %q", err)
	}
}

func TestNativeRequestDisposeDuringRequestCancelsIt(t *testing.T) {
	ctx, dispose := context.WithCancel(t.Context())
	entered := make(chan context.Context, 1)
	result := make(chan error, 1)
	go func() {
		_, err := liveRequest(90*time.Second).send(ctx, blockingFetch(entered), testOrigin)
		result <- err
	}()
	during := <-entered
	dispose()
	err := <-result
	if !errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("dispose error = %v", err)
	}
	if during.Err() == nil {
		t.Fatal("dispose did not cancel the request's context")
	}
	// A request after dispose is not sent.
	sent := false
	if _, err := liveRequest(time.Second).send(ctx, func(context.Context, browser.FetchRequest, int) (browser.FetchResponse, error) {
		sent = true
		return browser.FetchResponse{}, nil
	}, testOrigin); !errors.Is(err, context.Canceled) || sent {
		t.Fatalf("request after dispose = %v, sent %v", err, sent)
	}
}

func TestNativeRequestPrefersACompletedResponseOverItsDeadline(t *testing.T) {
	// The response completes as the deadline fires: it is delivered.
	fetch := func(ctx context.Context, _ browser.FetchRequest, _ int) (browser.FetchResponse, error) {
		<-ctx.Done()
		return browser.FetchResponse{Status: 200, Body: []byte(`{"ok":true}`)}, nil
	}
	response, err := liveRequest(10*time.Millisecond).send(t.Context(), fetch, testOrigin)
	if err != nil || response.Status != 200 {
		t.Fatalf("completed response at the deadline = %+v, %v", response, err)
	}
}

func TestNativeRequestNeverDeliversAfterDispose(t *testing.T) {
	// The response completes as the engine is disposed: it is not delivered.
	ctx, dispose := context.WithCancel(t.Context())
	fetch := func(context.Context, browser.FetchRequest, int) (browser.FetchResponse, error) {
		dispose()
		return browser.FetchResponse{Status: 200, Body: []byte(`{"ok":true}`)}, nil
	}
	response, err := liveRequest(time.Second).send(ctx, fetch, testOrigin)
	if !errors.Is(err, context.Canceled) || response.Status != 0 {
		t.Fatalf("response after dispose = %+v, %v", response, err)
	}
}

func TestLiveActionResultReadsErrorBodies(t *testing.T) {
	for _, c := range []struct {
		name, action string
		status       int
		body, want   string
	}{
		{"conflict with message", "live", 409, `{"ok":false,"message":"Live lease expired"}`, "Live lease expired. Reopen Live to resume."},
		{"server error without message", "note-preview", 500, `{"ok":false}`, "Live action failed (500)"},
		{"refused with 200", "midi-bind", 200, `{"ok":false,"message":"Port is busy"}`, "Port is busy"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := liveActionResult(c.action, browser.FetchResponse{Status: c.status, Body: []byte(c.body)})
			var failure *liveActionFailure
			if !errors.As(err, &failure) || failure.Status != c.status || failure.Message != c.want {
				t.Fatalf("failure = %#v", err)
			}
		})
	}
	// A non-JSON error page is a decode error, as before.
	if _, err := liveActionResult("live", browser.FetchResponse{Status: 502, Body: []byte("Bad Gateway")}); err == nil {
		t.Fatal("an unreadable error page was accepted")
	}
}

func TestLimitsCutOversizedBodiesBeforeDecoding(t *testing.T) {
	large := `{"ok":true,"message":"` + strings.Repeat("x", 1<<20) + `"}`
	if _, err := liveActionResult("live", browser.FetchResponse{Status: 200, Body: []byte(large)}); err == nil {
		t.Fatal("a receipt over 1 MiB was decoded")
	}
	var saved struct{ State string }
	if _, err := noteRecoveryResult(browser.FetchResponse{Status: 200, Body: []byte(`{"state":"` + strings.Repeat("y", 16<<20) + `"}`)}, &saved); err == nil {
		t.Fatal("a recovery copy over 16 MiB was decoded")
	}
	// The limit counts bytes as io.LimitReader did: a value that fits is
	// read, and data after it is ignored.
	exact := `{"ok":true}`
	var receipt struct{ OK bool }
	if err := decodeLimited([]byte(exact+"trailing"), len(exact), &receipt); err != nil || !receipt.OK {
		t.Fatalf("value at the limit = %v, %v", receipt, err)
	}
	if got := limitBody([]byte("abcdef"), 4); string(got) != "abcd" {
		t.Fatalf("limitBody = %q", got)
	}
}

func TestNoteRecoveryResultStatuses(t *testing.T) {
	var saved struct {
		State string `json:"state"`
	}
	if found, err := noteRecoveryResult(browser.FetchResponse{Status: 404, Body: []byte("missing")}, &saved); found || err != nil {
		t.Fatalf("404 = %v, %v", found, err)
	}
	if _, err := noteRecoveryResult(browser.FetchResponse{Status: 503, Body: []byte(`{"state":"ready"}`)}, &saved); err == nil || err.Error() != "recovery unavailable" || saved.State != "" {
		t.Fatalf("503 = %v, state %q", err, saved.State)
	}
	if found, err := noteRecoveryResult(browser.FetchResponse{Status: 200, Body: []byte(`{"state":"ready"}`)}, &saved); !found || err != nil || saved.State != "ready" {
		t.Fatalf("200 = %v, %v, %q", found, err, saved.State)
	}
}

func TestMIDIInputRefusalReadsTheErrorBody(t *testing.T) {
	failure, _ := json.Marshal(map[string]string{"error": "MIDI input is busy in another window."})
	if got := midiInputRefusal(strings.NewReader(string(failure))); got != "MIDI input is busy in another window." {
		t.Fatalf("JSON refusal = %q", got)
	}
	if got := midiInputRefusal(strings.NewReader(" service starting \n")); got != "MIDI could not connect. service starting" {
		t.Fatalf("text refusal = %q", got)
	}
	long := strings.Repeat("z", 5000)
	if got := midiInputRefusal(strings.NewReader(long)); got != "MIDI could not connect. "+long[:4096] {
		t.Fatalf("refusal read %d bytes, want 4096", len(got)-len("MIDI could not connect. "))
	}
}
