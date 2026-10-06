package main

import (
	"encoding/json"
	"io"
	"net/http"

	"m31labs.dev/cicada/host/modalfit"
	"m31labs.dev/cicada/host/recording"
)

func (s *studio) instrumentFit(w http.ResponseWriter, r *http.Request) {
	if !instrumentSameOrigin(w, r) {
		return
	}
	var request struct {
		Pin      string `json:"sha256"`
		Hit      int    `json:"hit"`
		Revision string `json:"revision"`
		Scene    string `json:"scene"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(&request) != nil || d.Decode(new(any)) != io.EOF {
		studioJSON(w, 400, map[string]string{"error": "choose one recorded hit to fit"})
		return
	}
	if request.Revision == "" {
		request.Revision = s.recordedRevision()
	}
	s.mu.Lock()
	source := s.recordedInstruments[request.Pin]
	s.mu.Unlock()
	if source == nil || request.Hit < 1 || request.Hit > len(source.Hits) {
		studioJSON(w, 422, map[string]string{"error": "choose a hit from an imported or recorded instrument"})
		return
	}
	hit := source.Hits[request.Hit-1]
	model, err := modalfit.Fit(hit.PCM, hit.Rate, hit.SourceSHA256)
	if err != nil {
		studioJSON(w, 422, map[string]string{"error": err.Error()})
		return
	}
	name := source.Manifest.ID
	if len(name) > 42 {
		name = name[:42]
	}
	name += "_model"
	pack, err := model.Build(name)
	if err != nil {
		studioJSON(w, 422, map[string]string{"error": err.Error()})
		return
	}

	s.mu.Lock()
	s.recordedInstruments[pack.Pin] = pack
	if s.recordedModels == nil {
		s.recordedModels = map[string]modalfit.Model{}
	}
	s.recordedModels[pack.Pin] = model
	s.mu.Unlock()
	s.publishRecorded(w, pack, model.RootMIDI, studioEdit{Revision: request.Revision, Scene: request.Scene}, map[string]any{"model": model, "modelSHA256": recording.Digest(pack.Files["model.json"]), "approximation": true})
}
