package main

import (
	"bufio"
	"io"
	"log/slog"
	"net/http"
	"time"
)

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
