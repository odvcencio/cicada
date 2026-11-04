package main

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/edition"
	"m31labs.dev/cicada/host/midi"
)

func (s *studio) midiPorts(w http.ResponseWriter, r *http.Request) {
	if !studioSameOrigin(r) {
		studioJSON(w, http.StatusForbidden, map[string]string{"error": "cross-origin MIDI access is not allowed"})
		return
	}
	_, manifest, err := edition.ScoreEdition(s.path)
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	dir := filepath.Dir(s.path)
	if manifest != "" {
		dir = filepath.Dir(manifest)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		studioJSON(w, http.StatusInternalServerError, map[string]string{"error": "Cannot open project directory."})
		return
	}
	defer root.Close()
	file, err := root.Open("cicada.local")
	if errors.Is(err, fs.ErrNotExist) {
		w.Header().Set("Cache-Control", "no-store")
		studioJSON(w, http.StatusOK, map[string]any{"ports": map[string]string{}})
		return
	}
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "Cannot read cicada.local."})
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "cicada.local must be a regular file."})
		return
	}
	ports, err := midi.ReadLocalPorts(file)
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	studioJSON(w, http.StatusOK, map[string]any{"ports": ports})
}
