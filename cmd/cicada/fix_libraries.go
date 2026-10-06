package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"m31labs.dev/cicada/migration"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// Independent scores retain separate musical scopes, but their imports still
// need source-set lowering and pin verification during migration.
func fixLoadedScoreForProject(path string, source []byte, edition int) ([]byte, bool, error) {
	if len(notation.ReadImports(notation.SourceFile{Source: source})) == 0 {
		fixed, changed, err := fixSourceForProject(source, edition)
		if err == nil {
			err = validateSourceEdition(fixed, 2)
		}
		return fixed, changed, err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, false, err
	}
	sources, err := project.ReadSources(absolute, map[string][]byte{absolute: source})
	if err != nil {
		return nil, false, err
	}
	_, diagnostics := sources.Parse()
	for _, d := range diagnostics {
		// Legacy syntax can need fixing under an edition-2 manifest. Loading
		// failures must still stop the operation before any rewrite or write.
		if d.Severity == "error" && (strings.HasPrefix(d.Code, "CICADA-LIB-") || strings.HasPrefix(d.Code, "CICADA-ASSET-")) {
			return nil, false, fmt.Errorf("%s", d.Error())
		}
	}
	fixed, changed, err := migration.FixFiles(sources.Files, edition)
	if err != nil {
		return nil, false, err
	}
	after := *sources
	after.Files = fixed
	after.Manifest.Edition = 2
	score, diagnostics := after.Parse()
	if score == nil || hasDiagnosticErrors(diagnostics) {
		return nil, false, fmt.Errorf("migrated score does not validate as edition 2: %+v", diagnostics)
	}
	compiled, diagnostics := project.FromScore(score)
	if compiled == nil || hasDiagnosticErrors(diagnostics) {
		return nil, false, fmt.Errorf("migrated score does not compile: %+v", diagnostics)
	}
	if _, err := project.CompileEngine(compiled, 48000, 128); err != nil {
		return nil, false, err
	}
	for _, file := range fixed {
		if file.Library == "" && filepath.Clean(file.Path) == filepath.Clean(absolute) {
			return file.Source, changed, nil
		}
	}
	return nil, false, fmt.Errorf("migrated score is missing from source set: %s", path)
}
