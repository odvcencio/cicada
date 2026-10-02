package lsp

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"m31labs.dev/cicada/edition"
	"m31labs.dev/cicada/language"
	"m31labs.dev/cicada/project"
)

func libraryCompletionRoot(uri string) string {
	filename, ok := scorePathFromURI(uri)
	if !ok {
		return ""
	}
	_, manifest, _ := edition.ScoreEdition(filename)
	if manifest != "" {
		root := filepath.Dir(manifest)
		if data, err := os.ReadFile(manifest); err == nil {
			if m, err := edition.ParseProjectManifest(data); err == nil && m.Library != "" {
				for dir := filepath.Dir(root); ; dir = filepath.Dir(dir) {
					if data, err := os.ReadFile(filepath.Join(dir, "cicada.mod")); err == nil {
						if m, err := edition.ParseProjectManifest(data); err == nil && m.Project != "" {
							return dir
						}
					}
					if filepath.Dir(dir) == dir {
						break
					}
				}
			}
		}
		return root
	}
	return filepath.Dir(filename)
}

func importCompletion(uri string, source []byte, at position) ([]map[string]any, bool) {
	offset := byteOffset(source, at)
	start := bytes.LastIndexByte(source[:offset], '\n') + 1
	line := strings.TrimLeft(string(source[start:offset]), " \t")
	prefix, ok := strings.CutPrefix(line, `import "`)
	if !ok || strings.Contains(prefix, `"`) {
		return nil, false
	}
	names := project.LibraryPaths(libraryCompletionRoot(uri))
	items := matchingCompletion(names, prefix, "Cicada library")
	for _, item := range items {
		label := item["label"].(string)
		quote := "\""
		if offset < len(source) && source[offset] == '"' {
			quote = ""
		}
		item["textEdit"] = map[string]any{"range": region{Start: utf16Position(source, offset-len(prefix)), End: at}, "newText": label + quote}
	}
	return items, true
}

func libraryItems(files *project.Sources, uri string, source []byte, at position) []map[string]any {
	offset := byteOffset(source, at)
	start := offset
	for start > 0 {
		c := source[start-1]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			break
		}
		start--
	}
	prefix := string(source[start:offset])
	var items []map[string]any
	filename, _ := scorePathFromURI(uri)
	scope := ""
	for _, file := range files.Files {
		if file.Path == filename {
			scope = file.Library
		}
	}
	bindings := files.Bindings[scope]
	for alias, namespace := range bindings {
		libraryPath := strings.ReplaceAll(namespace, ".", "/")
		lib := files.Libraries[libraryPath]
		if lib == nil {
			continue
		}
		for _, file := range lib.Files {
			symbols, err := language.Symbols(file.Source)
			if err != nil {
				continue
			}
			for _, symbol := range symbols {
				if symbol.Role != "definition" || symbol.Kind == "binding" || symbol.Kind == "parameter" || strings.HasPrefix(symbol.Name, "_") {
					continue
				}
				label := alias + "." + symbol.Name
				if !strings.HasPrefix(label, prefix) {
					continue
				}
				items = append(items, map[string]any{"label": label, "detail": symbol.Kind + " from " + libraryPath, "textEdit": map[string]any{"range": region{Start: utf16Position(source, start), End: at}, "newText": label}})
			}
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i]["label"].(string) < items[j]["label"].(string) })
	return items
}

func libraryDefinition(files *project.Sources, uri string, source []byte, at position) (any, bool) {
	selected, _, ok := symbolAt(source, at)
	if !ok {
		return nil, false
	}
	alias, name, qualified := strings.Cut(selected.Name, ".")
	filename, _ := scorePathFromURI(uri)
	scope := ""
	for _, file := range files.Files {
		if file.Path == filename {
			scope = file.Library
		}
	}
	var lib *project.Library
	if !qualified {
		if scope == "" || selected.Kind == "binding" || selected.Kind == "parameter" {
			return nil, false
		}
		lib, name = files.Libraries[scope], selected.Name
	} else {
		namespace, found := files.Bindings[scope][alias]
		if !found {
			return nil, false
		}
		if strings.HasPrefix(name, "_") {
			return nil, true
		}
		lib = files.Libraries[strings.ReplaceAll(namespace, ".", "/")]
	}
	if lib == nil {
		return nil, true
	}
	for _, file := range lib.Files {
		symbols, err := language.Symbols(file.Source)
		if err != nil {
			continue
		}
		for _, symbol := range symbols {
			if symbol.Role == "definition" && symbol.Name == name && symbolFamily(symbol.Kind) == symbolFamily(selected.Kind) {
				return map[string]any{"uri": fileURI(file.Path), "range": symbolRegion(file.Source, symbol)}, true
			}
		}
	}
	return nil, true
}

func libraryParameterPath(files *project.Sources, value string) string {
	first, rest, found := strings.Cut(value, ".")
	if found {
		if namespace, ok := files.Bindings[""][first]; ok {
			return namespace + "." + rest
		}
	}
	return value
}
