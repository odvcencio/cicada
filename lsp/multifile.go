package lsp

import (
	"bytes"
	"sort"
	"strings"

	"m31labs.dev/cicada/edition"
	"m31labs.dev/cicada/language"
	"m31labs.dev/cicada/migration"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"os"
	"path/filepath"
)

func (s *server) projectSources(uri string) (*project.Sources, error) {
	path, ok := scorePathFromURI(uri)
	if !ok {
		return nil, nil
	}
	overrides := map[string][]byte{}
	for uri, data := range s.documents {
		if path, ok := scorePathFromURI(uri); ok {
			if absolute, err := filepath.Abs(path); err == nil {
				overrides[absolute] = data
			}
		}
	}
	return project.ReadSources(path, overrides)
}

func symbolFamily(kind string) string {
	switch kind {
	case "instrument", "kit", "sampler":
		return "voice"
	case "pattern", "clip":
		return "pattern"
	case "effect", "bus":
		return "mixer"
	}
	return kind
}

func (s *server) projectDefinition(uri string, at position) any {
	source := s.documents[uri]
	files, err := s.projectSources(uri)
	if err != nil {
		return nil
	}
	if files != nil {
		if target, found := libraryDefinition(files, uri, source, at); found {
			return target
		}
	}
	if files == nil || !files.Manifest.ExplicitSources() {
		return definition(uri, source, at)
	}
	selected, _, ok := symbolAt(source, at)
	if match, found := parameterPathAt(source, byteOffset(source, at)); found {
		score, ds := files.Parse()
		if score == nil || hasErrors(ds) {
			return nil
		}
		compiled, ds := project.FromScore(score)
		if compiled == nil || hasErrors(ds) {
			return nil
		}
		resolved, err := project.ResolveParameterPath(compiled, libraryParameterPath(files, match.path))
		if err != nil {
			return nil
		}
		if origin, found := score.Origins[resolved.Owner]; found {
			for _, file := range files.Files {
				if file.Path == origin.Position.File {
					start := scalarOffset(file.Source, origin.Position)
					return map[string]any{"uri": fileURI(file.Path), "range": region{Start: utf16Position(file.Source, start), End: utf16Position(file.Source, start+len(strings.TrimPrefix(resolved.Owner, strings.ReplaceAll(origin.Library, "/", ".")+".")))}}
				}
			}
		}
		selected = language.Symbol{Name: resolved.Owner, Kind: resolved.OwnerKind}
		ok = true
	}
	if !ok {
		return nil
	}
	if selected.Kind == "binding" || selected.Kind == "parameter" {
		return definition(uri, source, at)
	}
	for _, file := range files.Files {
		symbols, err := language.Symbols(file.Source)
		if err != nil {
			continue
		}
		for _, symbol := range symbols {
			if symbol.Role == "definition" && symbol.Name == selected.Name && symbolFamily(symbol.Kind) == symbolFamily(selected.Kind) {
				return map[string]any{"uri": fileURI(file.Path), "range": symbolRegion(file.Source, symbol)}
			}
		}
	}
	return nil
}

func (s *server) projectRename(uri string, at position, newName string) any {
	files, err := s.projectSources(uri)
	if err != nil {
		return nil
	}
	source := s.documents[uri]
	if files != nil {
		if files.Manifest.Library != "" {
			return nil
		}
		for _, file := range files.Files {
			if fileURI(file.Path) == uri && file.Library != "" {
				return nil
			}
		}
		if _, collision := files.Bindings[""][newName]; collision {
			return nil
		}
	}
	if files == nil || !files.Manifest.ExplicitSources() {
		return rename(uri, source, at, newName)
	}
	if !identifier.MatchString(newName) {
		return nil
	}
	selected, _, ok := symbolAt(source, at)
	if !ok {
		if match, found := parameterPathAt(source, byteOffset(source, at)); found {
			score, ds := files.Parse()
			if score == nil || hasErrors(ds) {
				return nil
			}
			compiled, ds := project.FromScore(score)
			if compiled == nil || hasErrors(ds) {
				return nil
			}
			resolved, err := project.ResolveParameterPath(compiled, match.path)
			if err != nil {
				return nil
			}
			selected = language.Symbol{Name: resolved.Owner, Kind: resolved.OwnerKind}
			ok = true
		}
	}
	if !ok {
		return nil
	}
	local := selected.Kind == "binding" || selected.Kind == "parameter"
	scope := instrumentScope(source, scalarOffset(source, selected.Position))
	changes := map[string]any{}
	updated := append([]notation.SourceFile(nil), files.Files...)
	if strings.Contains(selected.Name, ".") {
		return nil
	}
	for i, file := range files.Files {
		if file.Library != "" {
			continue
		}
		fileURI := fileURI(file.Path)
		if local && fileURI != uri {
			continue
		}
		symbols, err := language.Symbols(file.Source)
		if err != nil {
			return nil
		}
		var edits []map[string]any
		var replacements []replacement
		for _, symbol := range symbols {
			matches := symbol.Name == selected.Name && symbolFamily(symbol.Kind) == symbolFamily(selected.Kind)
			if local {
				matches = sameSymbol(file.Source, selected, symbol, scope)
			}
			if !matches {
				continue
			}
			start := scalarOffset(file.Source, symbol.Position)
			edits = append(edits, map[string]any{"range": symbolRegion(file.Source, symbol), "newText": newName})
			replacements = append(replacements, replacement{start, start + len(symbol.Name), newName})
		}
		if selected.Kind == "track" || selected.Kind == "effect" || selected.Kind == "bus" {
			root, w, err := notation.ParseTree(file.Source)
			if err != nil {
				return nil
			}
			for j := 0; j < root.NamedChildCount(); j++ {
				node := root.NamedChild(j)
				if w.Type(node) != "scene_decl" {
					continue
				}
				for k := 0; k < node.NamedChildCount(); k++ {
					target := w.Field(node.NamedChild(k), "target")
					if target == nil {
						continue
					}
					text := w.Text(target)
					if !strings.HasPrefix(text, selected.Name+".") {
						continue
					}
					start := int(target.StartByte())
					end := start + len(selected.Name)
					edits = append(edits, map[string]any{"range": region{Start: utf16Position(file.Source, start), End: utf16Position(file.Source, end)}, "newText": newName})
					replacements = append(replacements, replacement{start, end, newName})
				}
			}
		}
		if len(edits) == 0 {
			continue
		}
		sort.Slice(replacements, func(i, j int) bool { return replacements[i].start > replacements[j].start })
		data := bytes.Clone(file.Source)
		for _, edit := range replacements {
			data = append(append(bytes.Clone(data[:edit.start]), edit.text...), data[edit.end:]...)
		}
		updated[i].Source = data
		changes[fileURI] = edits
	}
	score, ds := notation.ParseFiles(updated, files.Manifest.Edition)
	if score == nil || hasErrors(ds) {
		return nil
	}
	if p, ds := project.FromScore(score); p == nil || hasErrors(ds) {
		return nil
	}
	return map[string]any{"changes": changes}
}

func (s *server) projectHover(uri string, at position) any {
	source := s.documents[uri]
	match, ok := parameterPathAt(source, byteOffset(source, at))
	files, err := s.projectSources(uri)
	if err != nil || files == nil || !files.Manifest.ExplicitSources() {
		return hover(source, at)
	}
	score, ds := files.Parse()
	if score == nil || hasErrors(ds) {
		return nil
	}
	if !ok {
		return hoverWithScore(source, at, score)
	}
	compiled, ds := project.FromScore(score)
	if compiled == nil || hasErrors(ds) {
		return nil
	}
	resolved, err := project.ResolveParameterPath(compiled, libraryParameterPath(files, match.path))
	if err != nil {
		return nil
	}
	return map[string]any{"contents": map[string]any{"kind": "markdown", "value": resolvedParameterHover(match.path, resolved)}, "range": region{Start: utf16Position(source, match.start), End: utf16Position(source, match.end)}}
}

func (s *server) projectCompletion(uri string, at position) any {
	source := bytes.Clone(s.documents[uri])
	if items, ok := importCompletion(uri, source, at); ok {
		return items
	}
	files, err := s.projectSources(uri)
	if files != nil {
		if items := libraryItems(files, uri, source, at); len(items) > 0 {
			return items
		}
	}
	if err != nil || files == nil || !files.Manifest.ExplicitSources() {
		return parameterPathCompletion(source, at)
	}
	for _, file := range files.Files {
		if file.Library != "" || fileURI(file.Path) == uri {
			continue
		}
		source = append(source, '\n')
		source = append(source, file.Source...)
	}
	return parameterPathCompletion(source, at)
}

func (s *server) projectFixAction(files *project.Sources) []any {
	fixed, changed, err := migration.FixFiles(files.Files, files.Manifest.Edition)
	if err != nil || !changed && files.Manifest.Edition == 2 {
		return []any{}
	}
	var changes []any
	if files.Manifest.Edition == 1 {
		before, err := os.ReadFile(files.ManifestPath)
		if err != nil {
			return []any{}
		}
		after, _, err := edition.UpgradeManifestEdition(before)
		if err != nil {
			return []any{}
		}
		uri := fileURI(files.ManifestPath)
		changes = append(changes, map[string]any{"textDocument": map[string]any{"uri": uri, "version": s.versions[uri]}, "edits": []any{map[string]any{"range": region{End: utf16Position(before, len(before))}, "newText": string(after)}}})
	}
	for i, file := range fixed {
		if bytes.Equal(file.Source, files.Files[i].Source) {
			continue
		}
		uri := fileURI(file.Path)
		before := files.Files[i].Source
		changes = append(changes, map[string]any{"textDocument": map[string]any{"uri": uri, "version": s.versions[uri]}, "edits": []any{map[string]any{"range": region{End: utf16Position(before, len(before))}, "newText": string(file.Source)}}})
	}
	return []any{map[string]any{"title": "Apply Cicada notation fixes", "kind": "quickfix", "edit": map[string]any{"documentChanges": changes}}}
}
