package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/host/recording"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// Publication and insertion share the score's ordinary revision checked edit
// transaction. Undo removes the sampler, track and scene binding together;
// immutable packs remain available for Redo and other projects.
func (s *studio) publishRecorded(w http.ResponseWriter, pack *recording.Pack, root int, edit studioEdit, extra map[string]any) {
	base, err := studioProjectRoot(s.path)
	if err != nil {
		studioJSON(w, 422, map[string]any{"error": err.Error()})
		return
	}
	relative := filepath.Join("assets", "recorded", pack.Manifest.ID+"-"+pack.Pin[:16])
	if err := pack.Write(filepath.Join(base, relative)); err != nil {
		studioJSON(w, 500, map[string]any{"error": "cannot publish recording pack"})
		return
	}
	s.mu.Lock()
	if s.recordedInstruments == nil {
		s.recordedInstruments = map[string]*recording.Pack{}
	}
	s.recordedInstruments[pack.Pin] = pack
	s.mu.Unlock()
	receipt := &studioRecordingResponse{ResponseWriter: w}
	edit.Label = ""
	s.applyPreparedIntents(receipt, edit, edits.Envelope{}, edits.ParamWriterAuto, func(source []byte, opts *edits.Options) (edits.Envelope, error) {
		p, err := opts.Compiler.Compile(source, nil)
		if err != nil {
			return edits.Envelope{}, err
		}
		scene := edit.Scene
		if scene == "" {
			scene = s.transport.snapshot().Scene
			if scene == "" && len(p.Scenes) > 0 {
				scene = p.Scenes[0].ID
			}
		}
		declaration := strings.Replace(pack.Source(filepath.Join(relative, "manifest.json"), root), "voices = 16", "voices = 4", 1)
		level := "-6dB"
		if extra["model"] != nil {
			level = "-6dB mute = on"
		}
		score, _, err := project.LoadScore(s.path, map[string][]byte{s.path: source})
		if err != nil {
			return edits.Envelope{}, err
		}
		scenePath := s.path
		for _, decl := range score.Scenes {
			if decl.Name == scene && decl.Position.File != "" {
				scenePath = decl.Position.File
				break
			}
		}
		if scenePath != s.path {
			before, err := os.ReadFile(scenePath)
			if err != nil {
				return edits.Envelope{}, err
			}
			opts.Sources = []notation.SourceFile{{Path: scenePath, Source: before}}
		}
		return edits.Envelope{Intents: []edits.Intent{&edits.PublishRecorded{Name: pack.Manifest.ID, Declaration: declaration, Scene: scene, ScenePath: scenePath, Level: level, Note: recording.NoteName(root)}}}, nil
	}, func(result *edits.Result) error {
		response := map[string]any{"sha256": pack.Pin, "manifest": pack.Manifest, "hits": pack.Hits, "kept": len(pack.Hits), "manifestPath": filepath.ToSlash(filepath.Join(relative, "manifest.json")), "scorePath": filepath.ToSlash(filepath.Join(relative, "instrument.cicada")), "license": "user recording"}
		for key, value := range response {
			result.Response[key] = value
		}
		for key, value := range extra {
			result.Response[key] = value
		}
		return nil
	}, nil)
	if receipt.status == http.StatusOK {
		s.mu.Lock()
		if extra["model"] != nil {
			s.recordedModeled = pack.Pin
		} else {
			s.recordedSampled = pack.Pin
			s.recordingPreview = ""
		}
		s.mu.Unlock()
	}

}

func (s *studio) buildRecorded(w http.ResponseWriter, r *http.Request) {
	edit, ok := studioRequest(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	draft := s.recordedInstruments[edit.TakeID]
	s.mu.Unlock()
	if draft == nil {
		studioJSON(w, 404, map[string]any{"error": "record or import hits first"})
		return
	}
	hits := draft.Hits
	if edit.Action == "remove" {
		if edit.Index < 0 || edit.Index >= len(hits) || len(hits) == 1 {
			studioJSON(w, 422, map[string]any{"error": "keep at least one hit"})
			return
		}
		kept := append([]recording.Hit(nil), hits[:edit.Index]...)
		kept = append(kept, hits[edit.Index+1:]...)
		pack, err := recording.Build(draft.Manifest.ID, kept, 3)
		if err != nil {
			studioJSON(w, 422, map[string]any{"error": err.Error()})
			return
		}
		s.mu.Lock()
		s.recordedInstruments[pack.Pin] = pack
		s.recordingPreview = pack.Pin
		s.mu.Unlock()
		studioJSON(w, 200, map[string]any{"sha256": pack.Pin, "hits": kept, "kept": len(kept)})
		return
	}
	if edit.Hits != nil {
		hits = nil
		seen := map[int]bool{}
		for _, i := range edit.Hits {
			if i < 0 || i >= len(draft.Hits) || seen[i] {
				studioJSON(w, 400, map[string]any{"error": "invalid hit selection"})
				return
			}
			seen[i] = true
			hits = append(hits, draft.Hits[i])
		}
	}
	pack, err := recording.Build(draft.Manifest.ID, hits, 3)
	if err != nil {
		studioJSON(w, 422, map[string]any{"error": err.Error()})
		return
	}
	s.publishRecorded(w, pack, hits[0].Root, edit, nil)
}

func (s *studio) recordedRevision() string {
	source, _ := os.ReadFile(s.path)
	return studioRevision(source)
}

type studioRecordingResponse struct {
	http.ResponseWriter
	status   int
	onStatus func(int)
}

func (w *studioRecordingResponse) WriteHeader(status int) {
	w.status = status
	if w.onStatus != nil {
		w.onStatus(status)
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *studioRecordingResponse) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}
