package lsp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"m31labs.dev/cicada/edition"
	"m31labs.dev/cicada/migration"
)

type codeActionParams struct {
	Range   region `json:"range"`
	Context struct {
		Only        []string `json:"only"`
		Diagnostics []struct {
			Range region `json:"range"`
			// LSP allows an integer or a string; only string codes can be Cicada's.
			Code    json.RawMessage `json:"code"`
			Message string          `json:"message"`
		} `json:"diagnostics"`
	} `json:"context"`
}

// wants reports whether the client asked for actions of this kind. An empty
// filter asks for all kinds, and a requested kind also covers its sub-kinds.
func (p codeActionParams) wants(kind string) bool {
	if len(p.Context.Only) == 0 {
		return true
	}
	for _, only := range p.Context.Only {
		if only == kind || strings.HasPrefix(kind, only+".") {
			return true
		}
	}
	return false
}

func (s *server) codeActions(raw json.RawMessage) ([]any, error) {
	var params codeActionParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, err
	}
	if !params.wants("quickfix") && !params.wants("refactor") {
		return []any{}, nil
	}
	uri, err := documentURI(raw)
	if err != nil {
		return nil, err
	}
	source, ok := s.documents[uri]
	if !ok || !s.canEditDocuments {
		return []any{}, nil
	}
	path, ok := scorePathFromURI(uri)
	if !ok {
		return []any{}, nil
	}
	actions := []any{}
	if params.wants("quickfix") {
		actions = append(actions, s.fixActions(uri, path, source)...)
	}
	return append(actions, s.intentActions(uri, path, source, params)...), nil
}

// fixActions offers the notation fix: header, manifest and syntax migration.
func (s *server) fixActions(uri, path string, source []byte) []any {
	projectEdition, manifest, err := edition.ScoreEdition(path)
	if err != nil || manifest == "" && !s.canCreateFiles {
		return nil
	}
	if files, err := s.projectSources(uri); err == nil && usesSourceSet(files) {
		return s.projectFixAction(files)
	}
	fixed, changed, err := migration.FixSource(source)
	if err != nil || !changed {
		return nil
	}
	changes := make([]any, 0, 3)
	if manifest == "" {
		name := strings.TrimSuffix(filepath.Base(path), ".cicada")
		if !edition.ValidProjectName(name) {
			return nil
		}
		manifestURI := fileURI(filepath.Join(filepath.Dir(path), "cicada.mod"))
		changes = append(changes,
			map[string]any{"kind": "create", "uri": manifestURI},
			map[string]any{"textDocument": map[string]any{"uri": manifestURI, "version": nil}, "edits": []any{map[string]any{"range": region{}, "newText": "project " + name + "\ncicada 2\n"}}},
		)
	} else if projectEdition == 1 {
		manifestData, readErr := os.ReadFile(manifest)
		if readErr != nil {
			return nil
		}
		upgraded, changed, upgradeErr := edition.UpgradeManifestEdition(manifestData)
		if upgradeErr != nil || !changed {
			return nil
		}
		manifestURI := fileURI(manifest)
		changes = append(changes, map[string]any{
			"textDocument": map[string]any{"uri": manifestURI, "version": nil},
			"edits":        []any{map[string]any{"range": region{Start: position{}, End: utf16Position(manifestData, len(manifestData))}, "newText": string(upgraded)}},
		})
	}
	changes = append(changes, map[string]any{
		"textDocument": map[string]any{"uri": uri, "version": s.versions[uri]},
		"edits":        []any{map[string]any{"range": region{Start: position{}, End: utf16Position(source, len(source))}, "newText": string(fixed)}},
	})
	action := map[string]any{
		"title": "Apply Cicada notation fixes", "kind": "quickfix",
		"edit": map[string]any{"documentChanges": changes},
	}
	return []any{action}
}
