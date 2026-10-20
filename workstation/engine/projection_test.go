package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"m31labs.dev/gosx/client/browser"
)

func TestReadJSONProjectionPreservesGETAndExactJSONBytes(t *testing.T) {
	var state struct{ Title string }
	var requestContext context.Context
	fetch := func(ctx context.Context, request browser.FetchRequest) (browser.FetchResponse, error) {
		requestContext = ctx
		if request.URL != "http://127.0.0.1/api/state?runtime=1" || request.Method != "GET" || request.Body != nil || len(request.Headers) != 0 {
			t.Fatalf("projection request changed: %+v", request)
		}
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 2*time.Second {
			t.Fatal("request lost its timeout")
		}
		return browser.FetchResponse{Status: 200, Body: []byte("{\"title\":\"café 🎵\"}\r\n")}, nil
	}
	if err := readJSONProjection(t.Context(), fetch, "http://127.0.0.1/api/state?runtime=1", 2*time.Second, 64<<10, &state); err != nil {
		t.Fatal(err)
	}
	if state.Title != "café 🎵" {
		t.Fatalf("decoded source changed: %q", state.Title)
	}
	if !errors.Is(requestContext.Err(), context.Canceled) {
		t.Fatal("completed request retained its timeout context")
	}
}

func TestReadJSONProjectionRejectsFailureAndOversizeBeforeDecoding(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		response browser.FetchResponse
		limit    int
	}{
		{"http", browser.FetchResponse{Status: 503, Body: []byte(`{"title":"replace"}`)}, 64},
		{"oversize", browser.FetchResponse{Status: 200, Body: []byte(`{"title":"replace"}`)}, 8},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			state := struct{ Title string }{"retained"}
			fetch := func(context.Context, browser.FetchRequest) (browser.FetchResponse, error) {
				return scenario.response, nil
			}
			if err := readJSONProjection(t.Context(), fetch, "/state", time.Second, scenario.limit, &state); err == nil {
				t.Fatal("failed projection was accepted")
			}
			if state.Title != "retained" {
				t.Fatal("failed projection changed the current state")
			}
		})
	}
	broken := func(context.Context, browser.FetchRequest) (browser.FetchResponse, error) {
		return browser.FetchResponse{Status: 200, Body: []byte(`{"title":`)}, nil
	}
	var state struct{ Title string }
	if err := readJSONProjection(t.Context(), broken, "/state", time.Second, 64, &state); err == nil {
		t.Fatal("malformed JSON was accepted")
	}
}

func TestReadJSONProjectionPreservesTransportAndCancellationErrors(t *testing.T) {
	offline := errors.New("transport unavailable")
	fetch := func(context.Context, browser.FetchRequest) (browser.FetchResponse, error) {
		return browser.FetchResponse{}, offline
	}
	var state any
	if err := readJSONProjection(t.Context(), fetch, "/state", time.Second, 64, &state); !errors.Is(err, offline) {
		t.Fatalf("transport error identity lost: %v", err)
	}
	for _, timeout := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		entered := make(chan struct{})
		fetch := func(ctx context.Context, _ browser.FetchRequest) (browser.FetchResponse, error) {
			close(entered)
			<-ctx.Done()
			// A browser may report its AbortError rather than the Go context
			// error; cancellation still owns this retired projection.
			return browser.FetchResponse{}, offline
		}
		limit := time.Second
		want := context.Canceled
		if timeout {
			limit, want = 10*time.Millisecond, context.DeadlineExceeded
		}
		done := make(chan error, 1)
		go func() { done <- readJSONProjection(ctx, fetch, "/state", limit, 64, &state) }()
		<-entered
		if !timeout {
			cancel()
		}
		select {
		case err := <-done:
			if !errors.Is(err, want) {
				t.Fatalf("cancellation identity lost: %v, want %v", err, want)
			}
		case <-time.After(time.Second):
			t.Fatal("cancelled projection did not settle")
		}
	}
}

func TestReadJSONProjectionNeverStartsAnAlreadyCancelledRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	fetch := func(context.Context, browser.FetchRequest) (browser.FetchResponse, error) {
		t.Fatal("retired projection started a network request")
		return browser.FetchResponse{}, nil
	}
	var state any
	if err := readJSONProjection(ctx, fetch, "/state", time.Second, 64, &state); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := readJSONProjection(nil, fetch, "/state", time.Second, 64, &state); err == nil || !strings.Contains(err.Error(), "context") {
		t.Fatalf("missing context was accepted: %v", err)
	}
}
