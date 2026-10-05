package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func (s *studio) editAutomation(w http.ResponseWriter, r *http.Request) {
	edit, ok := studioRequest(w, r)
	if !ok {
		return
	}
	if edit.Action != "automation-set" && edit.Action != "automation-remove" {
		studioJSON(w, http.StatusBadRequest, map[string]string{"error": "choose an automation operation"})
		return
	}
	edition, err := mixerSourceEdition(s.path)
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	s.apply(w, edit, func(source []byte) ([]byte, error) {
		return sceneAutomationSource(source, edit.Scene, edit.Path, edit.Value, edit.Action == "automation-remove", edition)
	})
}

// Scene points use the existing parameter catalog and playback/render glides.
// Only the assignment's value or syntax span changes; surrounding comments,
// bindings, authored whitespace, and newline convention remain intact.
func sceneAutomationSource(source []byte, sceneID, path string, raw json.RawMessage, remove bool, edition int) ([]byte, error) {
	score, ds := notation.ParseEdition(source, edition)
	if score == nil || hasDiagnosticErrors(ds) {
		return nil, fmt.Errorf("score must validate before editing automation")
	}
	p, ds := project.FromScore(score)
	if p == nil || hasDiagnosticErrors(ds) {
		return nil, fmt.Errorf("score parameters must validate before editing automation")
	}
	resolved, err := project.ResolveParameterPath(p, path)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(path, ".") || !resolved.Descriptor.Live || !remove && !resolved.Descriptor.Automatable {
		return nil, fmt.Errorf("%s cannot be automated at a scene boundary", path)
	}
	node, walker, err := studioDeclaration(source, []string{"scene_decl"}, sceneID)
	if err != nil {
		return nil, err
	}
	literal := ""
	if !remove {
		descriptor := resolved.Descriptor
		// Display steps guide controls, but an authored automation value keeps
		// its full precision within the shared parameter range.
		descriptor.DisplayStep = 0
		literal, err = canonicalMixerLiteral(descriptor, raw)
		if err != nil {
			return nil, err
		}
	}
	for i := 0; i < node.NamedChildCount(); i++ {
		assignment := node.NamedChild(i)
		if walker.Type(assignment) != "scene_assignment" || walker.Text(walker.Field(assignment, "target")) != path {
			continue
		}
		if remove {
			return replaceSongSpan(source, int(assignment.StartByte()), int(assignment.EndByte()), nil)
		}
		value := walker.Field(assignment, "value")
		return replaceSongSpan(source, int(value.StartByte()), int(value.EndByte()), []byte(literal))
	}
	if remove {
		return nil, fmt.Errorf("scene %s has no point for %s", sceneID, path)
	}
	newline := "\n"
	if bytes.Contains(source, []byte("\r\n")) {
		newline = "\r\n"
	}
	at := int(node.StartByte()) + bytes.IndexByte(source[node.StartByte():node.EndByte()], '{') + 1
	return replaceSongSpan(source, at, at, []byte(newline+"  "+path+" = "+literal+newline))
}
