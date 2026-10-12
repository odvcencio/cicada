package main

import (
	"encoding/json"
	"net/http"
	"net/url"
)

func (s *studioApp) vocalWaveform(w http.ResponseWriter, r *http.Request) {
	query := url.Values{}
	if bins := r.URL.Query().Get("bins"); bins != "" {
		query.Set("bins", bins)
	}
	path := "/api/takes/" + url.PathEscape(r.PathValue("id")) + "/waveform?" + query.Encode()
	var data json.RawMessage
	if err := s.backend.call(r.Context(), http.MethodGet, path, nil, &data); err != nil {
		if failure, ok := err.(*backendError); ok {
			http.Error(w, failure.Message, failure.Status)
		} else {
			http.Error(w, "Waveform is unavailable. Your take is retained.", http.StatusBadGateway)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}
