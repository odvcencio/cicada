package main

import (
	"fmt"
	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type recordWriterCompiler struct{}

func (recordWriterCompiler) Compile(source []byte, _ map[string][]byte) (*edits.Plan, error) {
	score, ds := notation.Parse(source)
	if score == nil || hasDiagnosticErrors(ds) {
		return nil, fmt.Errorf("score must validate before recording a take")
	}
	p, ds := project.FromScore(score)
	if p == nil || hasDiagnosticErrors(ds) {
		return nil, fmt.Errorf("score must compile before recording a take")
	}
	return project.EditPlan(p, source), nil
}
func recordedTakeSource(source []byte, track, pattern string, notes []studioTakeNote) ([]byte, error) {
	result, err := edits.Apply(source, edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{&edits.RecordTake{Recordings: editRecordings([]studioTakeRecording{{Track: track, Pattern: pattern, Notes: notes}})}}}, edits.Options{Compiler: recordWriterCompiler{}})
	if err != nil {
		return nil, err
	}
	return result.Source, nil
}
