package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"m31labs.dev/cicada/host/recording"
)

//go:embed studio-instrument-record.js
var studioInstrumentRecordJS []byte

func (s *studio) instrumentRecordScript(w http.ResponseWriter, r *http.Request) {
	serveCaptureScript(w, studioInstrumentRecordJS)
}

func instrumentSameOrigin(w http.ResponseWriter, r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host != r.Host || u.Scheme != "http" {
			studioJSON(w, 403, map[string]string{"error": "cross-origin recording requests are not allowed"})
			return false
		}
	}
	return true
}

// instrumentRecord admits bounded WAV files and publishes an immutable pack.
// Analyze allows review before publication; build adds an instrument and track.
func (s *studio) instrumentRecord(w http.ResponseWriter, r *http.Request) {
	if !instrumentSameOrigin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, recording.MaxInputBytes+1<<20)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		studioJSON(w, 400, map[string]string{"error": "choose WAV files totalling at most 64 MiB"})
		return
	}
	defer r.MultipartForm.RemoveAll()
	revision := r.FormValue("revision")
	if revision == "" {
		revision = s.recordedRevision()
	}
	options := recording.DefaultOptions()
	var err error
	if value := r.FormValue("root"); value != "" {
		options.Root, err = strconv.Atoi(value)
	}
	if err != nil {
		studioJSON(w, 400, map[string]string{"error": "invalid root MIDI note"})
		return
	}
	if value := r.FormValue("layers"); value != "" {
		options.Layers, err = strconv.Atoi(value)
	}
	if err != nil {
		studioJSON(w, 400, map[string]string{"error": "invalid layer count"})
		return
	}
	options.AutoPitch = r.FormValue("autoPitch") == "true"
	name := r.FormValue("name")
	if name == "" {
		name = "recorded"
	}
	if !recording.ValidName(name) {
		studioJSON(w, 400, map[string]string{"error": "use a lowercase instrument name with letters, digits or underscores"})
		return
	}
	files := r.MultipartForm.File["wav"]
	if len(files) == 0 || len(files) > 32 {
		studioJSON(w, 400, map[string]string{"error": "choose 1–32 WAV files"})
		return
	}
	inputs := make([]recording.Audio, 0, len(files))
	total := 0
	for _, file := range files {
		f, err := file.Open()
		if err != nil {
			studioJSON(w, 400, map[string]string{"error": "cannot open recording"})
			return
		}
		data, err := io.ReadAll(io.LimitReader(f, int64(recording.MaxInputBytes-total+1)))
		f.Close()
		total += len(data)
		if err != nil || total > recording.MaxInputBytes {
			studioJSON(w, 400, map[string]string{"error": "recordings exceed 64 MiB"})
			return
		}
		a, err := recording.DecodeWAV(data)
		if err != nil {
			studioJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		inputs = append(inputs, a)
	}
	hits, err := recording.Analyze(inputs, options)
	if err != nil {
		studioJSON(w, 422, map[string]string{"error": err.Error()})
		return
	}
	pack, err := recording.Build(name, hits, options.Layers)
	if err != nil {
		studioJSON(w, 422, map[string]string{"error": err.Error()})
		return
	}

	s.mu.Lock()
	if s.recordedInstruments == nil {
		s.recordedInstruments = map[string]*recording.Pack{}
	}
	s.recordedInstruments[pack.Pin] = pack
	s.recordingPreview = pack.Pin
	s.mu.Unlock()
	if r.FormValue("action") == "analyze" {
		studioJSON(w, 200, map[string]any{"sha256": pack.Pin, "manifest": pack.Manifest, "hits": hits, "kept": len(hits)})
		return
	}
	s.publishRecorded(w, pack, options.Root, studioEdit{Revision: revision, Scene: r.FormValue("scene")}, nil)

}

func (s *studio) instrumentAudition(w http.ResponseWriter, r *http.Request) {
	if !instrumentSameOrigin(w, r) {
		return
	}
	var request struct {
		Pin      string `json:"sha256"`
		Note     int    `json:"note"`
		Velocity int    `json:"velocity"`
		Cycle    int    `json:"cycle"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if err := d.Decode(&request); err != nil {
		studioJSON(w, 400, map[string]string{"error": "invalid instrument audition"})
		return
	}
	if err := d.Decode(new(any)); err != io.EOF {
		studioJSON(w, 400, map[string]string{"error": "expected one audition request"})
		return
	}
	s.mu.Lock()
	pack := s.recordedInstruments[request.Pin]
	model, modeled := s.recordedModels[request.Pin]
	s.mu.Unlock()
	if pack == nil {
		studioJSON(w, 404, map[string]string{"error": "record or import this instrument before auditioning"})
		return
	}
	var data []byte
	index := 0
	var err error
	if modeled {
		var pcm []float32
		pcm, err = model.Render(request.Note, request.Velocity, request.Cycle, 48000)
		if err == nil {
			data = recording.EncodeWAV(pcm, 48000)
			index = request.Cycle % 4
		}
	} else {
		data, index, err = pack.Audition(request.Note, request.Velocity, request.Cycle)
	}
	if err != nil {
		studioJSON(w, 422, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Cicada-Take", strconv.Itoa(index+1))
	_, _ = w.Write(data)
}

// Asset serving stays confined to generated immutable recording directories.
func (s *studio) instrumentPackAsset(w http.ResponseWriter, r *http.Request) {
	name, file := r.PathValue("pack"), r.PathValue("file")
	if !strings.Contains(name, "-") || strings.ContainsAny(name, "/\\.\x00:") || filepath.Base(file) != file || !(file == "manifest.json" || file == "model.json" || file == "instrument.cicada" || strings.HasSuffix(file, ".wav.gz")) {
		http.NotFound(w, r)
		return
	}
	root, err := os.OpenRoot(filepath.Join(filepath.Dir(s.path), "assets", "recorded"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	f, err := root.Open(filepath.Join(name, file))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if file == "manifest.json" {
		w.Header().Set("Content-Type", "application/json")
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	http.ServeContent(w, r, fmt.Sprint(file), info.ModTime(), f)
}
