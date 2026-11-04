package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/gosx/action"
)

func (s *studioApp) learnPortableMIDI(ctx *action.Context) error {
	if err := actionValues(ctx); err != nil {
		return err
	}
	f := ctx.FormData
	channel, err := strconv.Atoi(f["channel"])
	if err != nil {
		return action.Validation("Choose a MIDI channel.", nil, f)
	}
	number, err := strconv.Atoi(f["number"])
	if err != nil {
		return action.Validation("Choose a MIDI input.", nil, f)
	}
	if f["revision"] == "" || f["source_revision"] == "" {
		return action.Error(http.StatusConflict, "Refresh Studio before saving a MIDI mapping.")
	}
	env := edits.Envelope{Version: edits.EnvelopeVersion, Revision: f["revision"], Intents: []edits.Intent{&edits.SetMIDIMapping{Port: f["port"], Message: f["message"], Channel: channel, Number: number, Target: f["target"]}}}
	var result map[string]any
	if err := s.backend.call(context.WithValue(ctx.Request.Context(), expectedProjectSourcesKey{}, f["source_revision"]), http.MethodPost, "/api/intents", env, &result); err != nil {
		var failure *backendError
		if errors.As(err, &failure) {
			return action.Error(failure.Status, failure.Message)
		}
		return action.Error(http.StatusServiceUnavailable, fmt.Sprintf("MIDI mapping was not saved: %v", err))
	}
	data, err := json.Marshal(result)
	if err != nil {
		return action.Error(http.StatusInternalServerError, "MIDI mapping was saved but its response could not be read.")
	}
	ctx.SetResult(action.Result{OK: true, Message: "MIDI mapping saved in the score.", Data: data})
	return nil
}

// Device names are local configuration and are only exposed to workspace
// editors, never to read-only collaborators or the public demo.
func (s *studioApp) localMIDIPorts(w http.ResponseWriter, r *http.Request) {
	_, role := s.collaboration.member(r, false)
	if role != "owner" && role != "editor" {
		http.Error(w, "Viewers cannot access local MIDI devices.", http.StatusForbidden)
		return
	}
	response, err := s.backend.request(r.Context(), http.MethodGet, "/api/midi/ports", nil)
	if err != nil {
		http.Error(w, "MIDI configuration is unavailable.", http.StatusServiceUnavailable)
		return
	}
	defer response.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(response.Body, 65537))
}

// Browser sessions and CSRF are checked by the existing GoSX middleware.
// Keep the private audio-service credential entirely on this server.
func (s *studioApp) nativeMIDIInput(w http.ResponseWriter, r *http.Request) {
	_, role := s.collaboration.member(r, false)
	if role != "owner" && role != "editor" {
		http.Error(w, "Viewers cannot connect MIDI input.", http.StatusForbidden)
		return
	}
	response, err := s.backend.request(r.Context(), http.MethodPost, "/api/midi/input", nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		http.Error(w, string(data), response.StatusCode)
		return
	}
	slog.Info("Native MIDI input connected")
	defer slog.Info("Native MIDI input disconnected")
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	controller := http.NewResponseController(w)
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 1024), 1<<20)
	for scanner.Scan() {
		_ = controller.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if _, err := w.Write(append(scanner.Bytes(), '\n')); err != nil {
			return
		}
		if err := controller.Flush(); err != nil {
			return
		}
	}
}
