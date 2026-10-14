package main

import (
	"net/http"
	"os"

	edits "m31labs.dev/cicada/edit"
)

// applyText commits change(source) through the intent commit tail as a
// ReplaceText intent. Tests use it to exercise the tail itself (revision
// checks, activation, the file exchange) with an arbitrary edit; beforeSwap
// runs after the candidate is staged and before it replaces the score.
func (s *studio) applyText(w http.ResponseWriter, edit studioEdit, change func([]byte) ([]byte, error), beforeSwap func()) {
	current, err := os.ReadFile(s.path)
	if err != nil {
		studioJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	updated, err := change(current)
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	s.applyIntents(w, edit, edits.Envelope{Intents: []edits.Intent{&edits.ReplaceText{Source: string(updated)}}}, edits.ParamWriterAuto, beforeSwap)
}
