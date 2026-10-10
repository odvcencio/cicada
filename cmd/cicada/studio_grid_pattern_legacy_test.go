package main

import (
	"fmt"
	"net/http"
)

// The pre-M3 adapters remain test-only oracles for the captured route contract.
func legacyGridPatternHandler(path string) (http.Handler, error) {
	s, err := newStudio(path)
	if err != nil {
		return nil, err
	}
	routes := s.routes()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/toggle":
			s.legacyToggleStep(w, r)
		case "/api/pattern":
			s.legacyEditPattern(w, r)
		default:
			routes.ServeHTTP(w, r)
		}
	}), nil
}

func (s *studio) legacyToggleStep(w http.ResponseWriter, r *http.Request) {
	edit, ok := studioRequest(w, r)
	if !ok {
		return
	}
	if edit.Pitch != nil && edit.Modifier != "" {
		studioJSON(w, http.StatusBadRequest, map[string]any{"error": "choose one grid edit per request"})
		return
	}
	if edit.Pitch != nil {
		s.apply(w, edit, func(source []byte) ([]byte, error) {
			return pitchedSource(source, edit.Pattern, edit.Lane, edit.Step, *edit.Pitch)
		})
		return
	}
	if edit.Modifier != "" {
		if edit.Modifier == "ratchet" || edit.Modifier == "chance" {
			s.apply(w, edit, func(source []byte) ([]byte, error) {
				return cycledStepSource(source, edit.Pattern, edit.Lane, edit.Step, edit.Modifier)
			})
			return
		}
		s.apply(w, edit, func(source []byte) ([]byte, error) {
			return toggledModifierSource(source, edit.Pattern, edit.Lane, edit.Step, edit.Modifier)
		})
		return
	}
	s.apply(w, edit, func(source []byte) ([]byte, error) { return toggledSource(source, edit.Pattern, edit.Lane, edit.Step) })
}

func (s *studio) legacyEditPattern(w http.ResponseWriter, r *http.Request) {
	edit, ok := studioRequest(w, r)
	if !ok {
		return
	}
	switch edit.Action {
	case "settings", "step", "duplicate", "bind", "toggle", "pitch", "range", "resize":
	default:
		studioJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown pattern action"})
		return
	}
	s.apply(w, edit, s.sourceTransform(func(source []byte) ([]byte, error) {
		switch edit.Action {
		case "settings":
			return patternSettingsSource(source, edit.Pattern, edit.Settings)
		case "step":
			return patternStepSource(source, edit.Pattern, edit.Lane, edit.Step, edit.NoteEdit)
		case "duplicate":
			return duplicatePatternSource(source, edit.Pattern, edit.NewName)
		case "range":
			return patternRangeSource(source, edit.Pattern, edit.Lane, edit.Range)
		case "resize":
			return resizePatternSource(source, edit.Pattern, edit.Length)
		case "toggle", "pitch":
			local, err := independentPatternSource(source, edit.Pattern)
			if err != nil {
				return nil, err
			}
			if edit.Action == "pitch" {
				if edit.Pitch == nil {
					return nil, fmt.Errorf("choose a pitch")
				}
				return pitchedSource(local, edit.Pattern, edit.Lane, edit.Step, *edit.Pitch)
			}
			return toggledSource(local, edit.Pattern, edit.Lane, edit.Step)
		default:
			return bindPatternSource(source, edit.Scene, edit.Track, edit.Pattern)
		}
	}))
}
