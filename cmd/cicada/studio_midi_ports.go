package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/edition"
	"m31labs.dev/cicada/host/midi"
)

func midiPortsRoot(path string) (*os.Root, error) {
	_, manifest, err := edition.ScoreEdition(path)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(path)
	if manifest != "" {
		dir = filepath.Dir(manifest)
	}
	return os.OpenRoot(dir)
}

func readMIDIPorts(root *os.Root) ([]byte, map[string]string, error) {
	info, err := root.Lstat("cicada.local")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, map[string]string{}, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return nil, nil, errors.New("cicada.local must be a regular file")
	}
	file, err := root.Open("cicada.local")
	if err != nil {
		return nil, nil, errors.New("cannot read cicada.local")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(data) > 65536 {
		return nil, nil, errors.New("cannot read cicada.local or file exceeds 64 KiB")
	}
	ports, err := midi.ReadLocalPorts(bytes.NewReader(data))
	return data, ports, err
}

func midiPortsRevision(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func (s *studio) midiPorts(w http.ResponseWriter, r *http.Request) {
	if !studioSameOrigin(r) {
		studioJSON(w, http.StatusForbidden, map[string]string{"error": "cross-origin MIDI access is not allowed"})
		return
	}
	root, err := midiPortsRoot(s.path)
	if err != nil {
		studioJSON(w, http.StatusInternalServerError, map[string]string{"error": "Cannot open project directory."})
		return
	}
	defer root.Close()
	data, ports, err := readMIDIPorts(root)
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	studioJSON(w, http.StatusOK, map[string]any{"ports": ports, "configRevision": midiPortsRevision(data)})
}

func (s *studio) setMIDIPort(w http.ResponseWriter, r *http.Request) {
	if !studioSameOrigin(r) {
		studioJSON(w, http.StatusForbidden, map[string]string{"error": "cross-origin MIDI access is not allowed"})
		return
	}
	var input struct{ Port, Device, Revision string }
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(&input) != nil || input.Revision == "" {
		studioJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid MIDI port binding."})
		return
	}
	s.midiPortsMu.Lock()
	defer s.midiPortsMu.Unlock()
	inspection, err := inspectScore(s.path)
	declared := false
	if err == nil && inspection.semantic != nil && inspection.semantic.MIDI != nil {
		for _, port := range inspection.semantic.MIDI.Ports {
			declared = declared || port == input.Port
		}
	}
	if !declared {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "Declare this MIDI port in a valid score first."})
		return
	}
	root, err := midiPortsRoot(s.path)
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "Cannot open project directory."})
		return
	}
	defer root.Close()
	data, _, err := readMIDIPorts(root)
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	if midiPortsRevision(data) != input.Revision {
		studioJSON(w, http.StatusConflict, map[string]string{"error": "cicada.local changed. Reload its bindings before saving."})
		return
	}
	updated, err := midi.SetLocalPort(data, input.Port, input.Device)
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		studioJSON(w, http.StatusInternalServerError, map[string]string{"error": "Cannot create local MIDI settings."})
		return
	}
	name := ".cicada-local-" + hex.EncodeToString(nonce[:])
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		defer root.Remove(name)
		_, err = file.Write(updated)
		if err == nil {
			err = file.Sync()
		}
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
	}
	if err == nil {
		var current []byte
		current, _, err = readMIDIPorts(root)
		if err == nil && midiPortsRevision(current) != input.Revision {
			studioJSON(w, http.StatusConflict, map[string]string{"error": "cicada.local changed. Reload its bindings before saving."})
			return
		}
	}
	if err == nil {
		err = root.Rename(name, "cicada.local")
	}
	if err != nil {
		studioJSON(w, http.StatusInternalServerError, map[string]string{"error": "Could not save local MIDI settings."})
		return
	}
	ports, _ := midi.ReadLocalPorts(bytes.NewReader(updated))
	w.Header().Set("Cache-Control", "no-store")
	studioJSON(w, http.StatusOK, map[string]any{"ports": ports, "configRevision": midiPortsRevision(updated)})
}
