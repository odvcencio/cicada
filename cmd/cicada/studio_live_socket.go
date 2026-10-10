package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/kernel"
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
		previews := make(livePreviewSession)
		for {
			kind, data, err := connection.Read(ctx)
			if err != nil {
				return
			}
			if kind == websocket.MessageText {
				if err := s.handleLiveMessage(data, previews); err != nil {
					response, _ := json.Marshal(struct {
						Type    string `json:"type"`
						Message string `json:"message"`
					}{Type: "error", Message: err.Error()})
					writeCtx, done := context.WithTimeout(ctx, time.Second)
					err := connection.Write(writeCtx, websocket.MessageText, response)
					done()
					if err != nil {
						return
					}
				}
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

type livePreviewKey struct {
	entity string
	param  kernel.ParamID
}

type livePreview struct {
	stream  *liveplay.Player
	version uint64
}

// Each socket owns only the override versions its messages published.
type livePreviewSession map[livePreviewKey]livePreview

// handleLiveMessage queues previews through the player's control snapshot.
func (s *studio) handleLiveMessage(data []byte, previews livePreviewSession) error {
	var input struct {
		Type   string   `json:"type"`
		Entity string   `json:"entity"`
		Param  string   `json:"param"`
		Value  *float32 `json:"value"`
		Commit bool     `json:"commit"`
	}
	if err := json.Unmarshal(data, &input); err != nil {
		return fmt.Errorf("invalid live message: %w", err)
	}
	if input.Type != "preview-set" && input.Type != "preview-end" {
		return fmt.Errorf("unknown live message type %q", input.Type)
	}
	s.transport.mu.Lock()
	stream := s.transport.stream
	s.transport.mu.Unlock()
	if stream == nil {
		return fmt.Errorf("live player is unavailable")
	}
	trackID, ok := strings.CutPrefix(input.Entity, "track:")
	if !ok || trackID == "" {
		return fmt.Errorf("unknown preview entity %q", input.Entity)
	}
	track, ok := stream.TrackIndex(trackID)
	if !ok {
		return fmt.Errorf("unknown preview entity %q", input.Entity)
	}
	id, ok := kernel.FindParam(input.Param)
	if !ok {
		return fmt.Errorf("unknown preview parameter %q", input.Param)
	}
	key := livePreviewKey{entity: input.Entity, param: id}
	if input.Type == "preview-set" {
		if input.Value == nil {
			return fmt.Errorf("preview-set requires a value")
		}
		version, err := stream.SetPreview(track, id, *input.Value)
		if err != nil {
			return err
		}
		previews[key] = livePreview{stream: stream, version: version}
		return nil
	}
	if input.Commit {
		return nil // Activation clears the preview after the committed value lands.
	}
	preview, ok := previews[key]
	if !ok || preview.stream != stream {
		return nil
	}
	if err := stream.CancelPreview(preview.version); err != nil {
		return err
	}
	delete(previews, key)
	return nil
}
