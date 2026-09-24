package project

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

// These complete source files exercise the M0 language boundary. They must be
// grammar-valid so each failure comes from semantic or compilation validation.
func TestInvalidSourceFixtures(t *testing.T) {
	want := map[string]struct {
		code string
		line int
	}{
		"unknown-symbol.cicada":             {"CICADA-REFERENCE", 2},
		"invalid-unit.cicada":               {"CICADA-PARAM", 2},
		"unsupported-effect.cicada":         {"CICADA-UNSUPPORTED", 2},
		"seed-64-bit.cicada":                {"CICADA-SEED", 2},
		"duplicate-id.cicada":               {"CICADA-DUPLICATE", 3},
		"slot-conflict.cicada":              {"CICADA-DUPLICATE", 4},
		"expansion-65.cicada":               {"CICADA-EXPANSION", 4},
		"unknown-scene.cicada":              {"CICADA-REFERENCE", 5},
		"incompatible-kind.cicada":          {"CICADA-PARAM", 4},
		"unsupported-poly.cicada":           {"CICADA-UNSUPPORTED", 2},
		"reserved-drum-lanes.cicada":        {"CICADA-UNSUPPORTED", 10},
		"over-32-voices.cicada":             {"CICADA-LIMIT", 10},
		"unsupported-drum-transpose.cicada": {"CICADA-UNSUPPORTED", 3},
		"invalid-scale-degree.cicada":       {"CICADA-SCALE-DEGREE", 4},
	}
	paths, err := filepath.Glob("../testdata/invalid/*.cicada")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != len(want) {
		t.Fatalf("fixture count %d differs from declared cases %d", len(paths), len(want))
	}
	for _, path := range paths {
		name := filepath.Base(path)
		expected, known := want[name]
		if !known {
			t.Fatalf("fixture %s has no expected diagnostic", name)
		}
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			document, err := notation.ParseDocument(source)
			if err != nil {
				t.Fatalf("fixture has a grammar error: %v", err)
			}
			if !bytes.Equal(notation.Print(document), source) {
				t.Fatal("source printer changed fixture bytes")
			}
			score, diagnostics := notation.Parse(source)
			if score == nil {
				t.Fatalf("fixture did not produce a score: %+v", diagnostics)
			}
			if !hasErrors(diagnostics) {
				_, compiledDiagnostics := Check(score)
				diagnostics = append(diagnostics, compiledDiagnostics...)
			}
			if name == "reserved-drum-lanes.cicada" {
				for line, lane := range []string{"lt", "mt", "ht", "cb", "cy"} {
					found := false
					for _, diagnostic := range diagnostics {
						if diagnostic.Code == "CICADA-UNSUPPORTED" && diagnostic.Position.Line == line+10 && strings.Contains(diagnostic.Message, "lane "+lane) {
							found = true
						}
					}
					if !found {
						t.Fatalf("M1 lane %s was not rejected at line %d: %+v", lane, line+10, diagnostics)
					}
				}
			}
			for _, diagnostic := range diagnostics {
				if diagnostic.Code == expected.code && diagnostic.Severity == "error" && diagnostic.Position.Line == expected.line && diagnostic.Position.Column > 0 {
					return
				}
			}
			t.Fatalf("want %s at line %d, got %+v", expected.code, expected.line, diagnostics)
		})
	}
}

func TestSlideIntoRestWarningFixture(t *testing.T) {
	path := "../testdata/warnings/slide-into-rest.cicada"
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	document, err := notation.ParseDocument(source)
	if err != nil || !bytes.Equal(notation.Print(document), source) {
		t.Fatalf("warning fixture must parse and print unchanged: %v", err)
	}
	score, diagnostics := notation.Parse(source)
	if score == nil || len(diagnostics) != 1 || diagnostics[0].Code != "CICADA-SLIDE-REST" || diagnostics[0].Severity != "warning" || diagnostics[0].Position.Line != 3 {
		t.Fatalf("expected one positioned slide warning, got %+v", diagnostics)
	}
	if compiled, extra := FromScore(score); compiled == nil || hasErrors(extra) {
		t.Fatalf("warning fixture cannot compile: %+v", extra)
	}
}

func hasErrors(diagnostics []notation.Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return true
		}
	}
	return false
}
