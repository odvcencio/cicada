package main

import (
	"context"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"m31labs.dev/cicada/host/liveplay"
)

// liveSocket streams binary telemetry frames. It sends the latest frame of
// each kind first, then each newer frame as it is published. The watcher
// publishes transport frames at 25 Hz. The route sits
// behind the per-launch bearer token that privateStudioService enforces.
func (s *studio) liveSocket(w http.ResponseWriter, r *http.Request) {
	if !studioSameOrigin(r) {
		http.Error(w, "cross-origin live stream is not allowed", http.StatusForbidden)
		return
	}
	if s.transport.telemetry == nil {
		http.Error(w, "telemetry is unavailable", http.StatusServiceUnavailable)
		return
	}
	connection, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer connection.CloseNow()
	publisher := s.transport.telemetry
	// Subscribe before reading Latest so no frame published in between is lost.
	subscriber := publisher.Subscribe(8)
	defer publisher.Unsubscribe(subscriber)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		defer cancel()
		for {
			kind, data, err := connection.Read(ctx)
			if err != nil {
				return
			}
			if kind == websocket.MessageText {
				s.handleLiveMessage(data)
			}
		}
	}()
	write := func(data []byte) bool {
		writeCtx, done := context.WithTimeout(ctx, time.Second)
		defer done()
		return connection.Write(writeCtx, websocket.MessageBinary, data) == nil
	}
	latest := publisher.Latest()
	for kind := liveplay.FrameTransport; kind <= liveplay.FrameHealth; kind++ {
		if latest[kind] != nil && !write(latest[kind]) {
			return
		}
	}
	// The producer publishes transport frames every 40 ms, which is already
	// below 30 Hz, so each notification is written at once.
	for {
		select {
		case <-ctx.Done():
			return
		case kind := <-subscriber.C:
			if data := publisher.Latest()[kind]; data != nil && !write(data) {
				return
			}
		}
	}
}

// handleLiveMessage receives text messages from a live client. Preview
// overrides arrive here in a later change; for now they are ignored.
func (s *studio) handleLiveMessage([]byte) {}
