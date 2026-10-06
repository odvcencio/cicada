package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/host/takejournal"
)

// The private domain service stores opaque replicated draft state beside the
// score. The production workstation calls this private route; no source path
// crosses it.
func (s *studio) collaborationStore(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.path + ".collaboration"
	if r.Method == http.MethodGet {
		file, err := os.Open(path)
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "Cannot read the shared score draft.", 500)
			return
		}
		defer file.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.Copy(w, io.LimitReader(file, 8<<20+1))
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 8<<20+1))
	if err != nil || len(data) > 8<<20 || !json.Valid(data) {
		http.Error(w, "Invalid shared score draft.", 400)
		return
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".cicada-collaboration-*")
	if err != nil {
		http.Error(w, "Cannot store the shared score draft.", 500)
		return
	}
	defer os.Remove(file.Name())
	err = file.Chmod(0600)
	if err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(file.Name(), path)
	}
	if err == nil {
		err = takejournal.SyncDirectory(filepath.Dir(path))
	}
	if err != nil {
		http.Error(w, "Cannot store the shared score draft.", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"ok":true}`)
}
