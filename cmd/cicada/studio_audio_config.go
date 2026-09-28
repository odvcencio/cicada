package main

import (
	_ "embed"
	"encoding/json"
	"net/http"
)

//go:embed studio-audio-devices.js
var studioAudioDevicesScript []byte

func (s *studio) audioDeviceScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(studioAudioDevicesScript)
}

func (s *studio) audioConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Cache-Control", "no-store")
		studioJSON(w, http.StatusOK, s.transport.audioState())
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !studioSameOrigin(r) {
		studioJSON(w, http.StatusForbidden, map[string]string{"error": "cross-origin audio configuration is not allowed"})
		return
	}
	var options studioAudioOptions
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&options); err != nil {
		studioJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := validateStudioAudioOptions(normalizeStudioAudioOptions(options)); err != nil {
		studioJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.transport.configureAudio(options); err != nil {
		status := http.StatusBadRequest
		if err.Error() == "stop playback before changing audio devices" {
			status = http.StatusConflict
		}
		studioJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	studioJSON(w, http.StatusOK, s.transport.audioState())
}
