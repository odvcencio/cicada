package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"m31labs.dev/cicada/host/midi"
)

type studioMIDIInput struct{ connected atomic.Bool }

func (s *studio) midiInput(w http.ResponseWriter, r *http.Request) {
	s.nativeMIDI.serve(w, r, midi.Open, s.transport.midiPosition)
}

func (m *studioMIDIInput) serve(w http.ResponseWriter, r *http.Request, open func(context.Context) (midi.Stream, error), position func(midi.Event) *midi.Position) {
	if !studioSameOrigin(r) {
		studioJSON(w, http.StatusForbidden, map[string]string{"error": "cross-origin MIDI input is not allowed"})
		return
	}
	if !m.connected.CompareAndSwap(false, true) {
		studioJSON(w, http.StatusConflict, map[string]string{"error": "Native MIDI is connected in another Live panel. Disconnect it there first."})
		return
	}
	defer m.connected.Store(false)
	stream, err := open(r.Context())
	if err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, midi.ErrUnsupported) {
			status = http.StatusNotImplemented
		}
		studioJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	defer stream.Close()
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	writer := http.NewResponseController(w)
	encoder := json.NewEncoder(w)
	var inputs midi.InputState
	write := func(event midi.Event) bool {
		// A slow or vanished reader must not retain a native port indefinitely.
		_ = writer.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if err := encoder.Encode(event); err != nil {
			return false
		}
		return writer.Flush() == nil
	}
	heartbeat := time.NewTicker(time.Second)
	defer heartbeat.Stop()
	for {
		// Prefer a queued fault over stale note-ons after input overflows.
		select {
		case err := <-stream.Errors():
			write(midi.Event{Type: "error", Message: err.Error()})
			return
		default:
		}
		select {
		case <-r.Context().Done():
			return
		case err := <-stream.Errors():
			write(midi.Event{Type: "error", Message: err.Error()})
			return
		case event := <-stream.Events():
			if !inputs.Accept(event) {
				continue
			}
			if position != nil {
				event.Position = position(event)
			}
			if !write(event) {
				return
			}
		case <-heartbeat.C:
			if !write(midi.Event{Type: "heartbeat"}) {
				return
			}
		}
	}
}
