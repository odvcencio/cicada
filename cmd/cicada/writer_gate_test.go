package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// deletedWriters are the source writers that the edit service replaced. Every
// Studio route that used one now runs an intent through edit.Apply.
var deletedWriters = []string{
	"toggledSource", "studioSourceOffset", "pitchedSource", "toggledChordPitch", "sourcePitch",
	"toggledModifierSource", "cycledStepSource", "drumVelocitySource", "intAbs", "capturedVelocity",
	"drumLaneName", "studioPatternStep", "studioNoteToken", "studioDrumToken",
	"editedSongSource", "editedSongBlockSource", "songEntries", "songGapsAreWhitespace",
	"sceneAutomationSource", "projectSettingsSource", "clipSettingsSource",
	"instrumentParameterSource", "addPresetSource", "addPresetSourceParsed",
	"studioLibrarySetting", "studioSavedPresetSource",
	"studioDeclaration", "noteSuffix", "patternSettingsSource", "independentPatternSource",
	"patternStepSource", "duplicatePatternSource", "patternRangeSource", "resizePatternSource", "bindPatternSource",
}

func TestDeletedWritersStayDeleted(t *testing.T) {
	deleted := map[string]bool{}
	for _, name := range deletedWriters {
		deleted[name] = true
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && deleted[id.Name] {
				t.Errorf("%s: %s was replaced by an edit intent; use edit.Apply instead of reintroducing the writer", fset.Position(id.Pos()), id.Name)
			}
			return true
		})
	}
}

func TestTakeSourceDelegatesToEditService(t *testing.T) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, filepath.Join("..", "..", "host", "takejournal", "source.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Name.Name != "SelectSource" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "edit" && sel.Sel.Name == "SelectTakeSource" {
					found = true
				}
			}
			return true
		})
	}
	if !found {
		t.Fatal("takejournal.SelectSource must delegate to edit.SelectTakeSource")
	}
}
