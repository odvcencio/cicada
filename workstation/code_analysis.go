package main

import (
	"encoding/json"
	"net/http"

	"m31labs.dev/cicada/language"
)

// Analysis decorates the submitted buffer, including incomplete notation. It
// never compiles, saves or changes the active song. The editor owns request
// ordering and converts these parser byte ranges to browser UTF-16 positions.
func codeAnalysis(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		http.Error(w, "Invalid score analysis request", http.StatusBadRequest)
		return
	}
	spans, err := language.Highlight([]byte(input.Content))
	if err != nil {
		http.Error(w, "Score highlighting unavailable", http.StatusServiceUnavailable)
		return
	}
	type highlight struct {
		StartByte int    `json:"startByte"`
		EndByte   int    `json:"endByte"`
		Capture   string `json:"capture"`
	}
	result := struct {
		Highlights []highlight `json:"highlights"`
		HasErrors  bool        `json:"hasErrors"`
	}{Highlights: make([]highlight, 0, len(spans))}
	for _, span := range spans {
		result.Highlights = append(result.Highlights, highlight{span.Start, span.End, span.Capture})
		result.HasErrors = result.HasErrors || span.Capture == language.ErrorCapture
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(result)
}
