package main

import "net/http"

// meters reads the published snapshot. Formatting runs on HTTP workers, never
// on Tymbal's callback thread; no audio device enumeration or source parsing.
func (s *studio) meters(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if latest := s.transport.latestMeter.Load(); latest != nil && s.transport.snapshot().Playing {
		studioJSON(w, http.StatusOK, studioMeters(latest.frame, latest.loudness))
		return
	}
	studioJSON(w, http.StatusOK, map[string]any{"type": "meters", "tracks": map[string]any{}, "master": map[string]any{"peak": -96, "rms": -96, "comp_gr": 0, "limiter_gr": 0}})
}
