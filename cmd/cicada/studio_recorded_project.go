package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"m31labs.dev/cicada/host/recording"
	"m31labs.dev/cicada/project"
)

func recordedName(p *project.Project, base string) string {
	used := map[string]bool{}
	for _, v := range p.Samplers {
		used[v.Name] = true
	}
	for _, v := range p.Instruments {
		used[v.ID] = true
	}
	for _, v := range p.Tracks {
		used[v.ID] = true
	}
	for _, v := range p.Patterns {
		used[v.ID] = true
	}
	for n := 1; ; n++ {
		name := base
		if n > 1 {
			name = fmt.Sprintf("%s_%d", base, n)
		}
		if !used[name] && !used[name+"_track"] && !used[name+"_taps"] {
			return name
		}
	}
}

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
	s.applyWithResult(receipt, edit, func(source []byte) (studioMutation, error) {
		p, err := compileStudioSource(s.path, source)
		if err != nil {
			return studioMutation{}, err
		}
		name := recordedName(p, pack.Manifest.ID)
		scene := edit.Scene
		if scene == "" {
			scene = s.transport.snapshot().Scene
			if scene == "" && len(p.Scenes) > 0 {
				scene = p.Scenes[0].ID
			}
		}
		declaration := strings.Replace(pack.Source(filepath.Join(relative, "manifest.json"), root), "sampler "+pack.Manifest.ID+" {", "sampler "+name+" {", 1)
		// Small recorded instruments leave room in the existing 32 voice budget.
		declaration = strings.Replace(declaration, "voices = 16", "voices = 4", 1)
		track, pattern := name+"_track", name+"_taps"
		note := recording.NoteName(root)
		updated := append([]byte(nil), source...)
		if p.Edition == 1 {
			updated = append([]byte("cicada 2\n"), updated...)
		}
		level := "-6dB"
		if extra["model"] != nil {
			level = "-6dB mute = on"
		}
		updated = append(updated, []byte(fmt.Sprintf("\n%s\ntrack %s %s { level = %s }\npattern %s { %s . . . %s . . . }\n", declaration, track, name, level, pattern, note, note))...)

		var files []studioAuxiliaryFile
		score, _, err := project.LoadScore(s.path, map[string][]byte{s.path: source})
		if err != nil {
			return studioMutation{}, err
		}
		scenePath := s.path
		for _, decl := range score.Scenes {
			if decl.Name == scene && decl.Position.File != "" {
				scenePath = decl.Position.File
				break
			}
		}
		if scenePath == s.path {
			updated, err = bindPatternSource(updated, scene, track, pattern)
		} else {
			before, readErr := os.ReadFile(scenePath)
			if readErr != nil {
				return studioMutation{}, readErr
			}
			after, bindErr := bindRecordedScene(before, scene, track, pattern)
			if bindErr != nil {
				return studioMutation{}, bindErr
			}
			info, statErr := os.Stat(scenePath)
			if statErr != nil {
				return studioMutation{}, statErr
			}
			files = append(files, studioAuxiliaryFile{Path: scenePath, Before: before, After: after, Mode: info.Mode().Perm()})
		}
		if err != nil {
			return studioMutation{}, err
		}

		response := map[string]any{"sha256": pack.Pin, "manifest": pack.Manifest, "hits": pack.Hits, "kept": len(pack.Hits), "instrument": name, "track": track, "declaration": declaration, "manifestPath": filepath.ToSlash(filepath.Join(relative, "manifest.json")), "scorePath": filepath.ToSlash(filepath.Join(relative, "instrument.cicada")), "license": "user recording"}
		for key, value := range extra {
			response[key] = value
		}
		return studioMutation{Source: updated, Files: files, Response: response, HistoryDetail: "Recorded instrument added · " + name}, nil
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
	status int
}

func (w *studioRecordingResponse) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *studioRecordingResponse) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func bindRecordedScene(source []byte, scene, track, pattern string) ([]byte, error) {
	node, _, err := studioDeclaration(source, []string{"scene_decl"}, scene)
	if err != nil {
		return nil, err
	}
	newline := "\n"
	if strings.Contains(string(source), "\r\n") {
		newline = "\r\n"
	}
	at := int(node.EndByte()) - 1
	return replaceSongSpan(source, at, at, []byte(newline+"  "+track+" = "+pattern+newline))
}
