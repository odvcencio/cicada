package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// Audition renders the existing bounded sample voice in the native service.
// GoSX serves the returned WAV privately; no asset path enters public/.
func (s *studioApp) takeAudio(w http.ResponseWriter, r *http.Request) {
	note, e := strconv.Atoi(r.URL.Query().Get("note"))
	if e != nil || note < 0 || note > 127 {
		http.Error(w, "note must be 0 to 127", 400)
		return
	}
	root, e := strconv.Atoi(r.URL.Query().Get("root"))
	if e != nil || root < 0 || root > 127 {
		http.Error(w, "root note must be 0 to 127", 400)
		return
	}
	id := r.PathValue("id")
	if id == "" || r.URL.Query().Get("revision") == "" {
		http.Error(w, "take and revision are required", 400)
		return
	}
	response, err := s.backend.request(r.Context(), http.MethodPost, "/api/takes", map[string]any{"action": "audition", "takeId": id, "revision": r.URL.Query().Get("revision"), "sample": map[string]any{"root": root, "note": note, "loop": r.URL.Query().Get("loop") == "on"}})
	if err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		var result struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&result)
		http.Error(w, result.Error, response.StatusCode)
		return
	}
	if response.Header.Get("Content-Type") != "audio/wav" {
		http.Error(w, "audio service did not return WAV", 502)
		return
	}
	if response.ContentLength > (64<<20)+44 {
		http.Error(w, "sample preview exceeds the output frame limit", 502)
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.Copy(w, io.LimitReader(response.Body, (64<<20)+44))
}

func takePreviewURL(id, revision string, note, root int, loop bool) string {
	q := url.Values{"revision": {revision}, "note": {strconv.Itoa(note)}, "root": {strconv.Itoa(root)}}
	if loop {
		q.Set("loop", "on")
	}
	return fmt.Sprintf("/media/takes/%s.wav?%s", url.PathEscape(id), q.Encode())
}
