//go:build js && wasm

package main

import (
	"context"
	"syscall/js"
	"time"

	"m31labs.dev/cicada/workstation/control"
	"m31labs.dev/gosx/signal"
)

type playbackUpdate struct {
	State control.PlaybackState
	At    time.Time
	Ready bool
}

var nativePlayback = signal.New(playbackUpdate{})

// Arrangement and vocal cursors consume one projection, including when both
// surfaces are mounted. Hidden views do not need transport polling.
var playbackPublisher = &sharedLoop{start: func(ctx context.Context) { go publishPlayback(ctx) }}

func playbackVisible() bool {
	document := js.Global().Get("document")
	if document.Get("hidden").Bool() {
		return false
	}
	nodes := document.Call("querySelectorAll", ".placement-scroll, [data-vocal-scroll]")
	for i := 0; i < nodes.Length(); i++ {
		if nodes.Index(i).Call("getClientRects").Length() > 0 {
			return true
		}
	}
	return false
}

func publishPlayback(ctx context.Context) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if playbackVisible() {
			var state control.PlaybackState
			requested := time.Now()
			err := readNativeJSON(ctx, "/api/transport", 2*time.Second, 64<<10, &state)
			state.RoundTrip = time.Since(requested)
			if ctx.Err() != nil {
				return
			}
			nativePlayback.Set(playbackUpdate{State: state, At: time.Now(), Ready: err == nil})
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
