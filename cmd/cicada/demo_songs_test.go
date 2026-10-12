package main

import (
	"bytes"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/smf"
)

func TestDemoSongsValidateAndRoundTrip(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(repositoryRoot(), "examples", "songs", "*.cicada"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("demo songs: %v (%d scores)", err, len(paths))
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			inspection, err := inspectScore(path)
			if err != nil || inspection.semantic == nil || hasDiagnosticErrors(inspection.diagnostics) {
				t.Fatalf("demo cannot play: %v; %v", err, inspection.diagnostics)
			}
			before, err := project.CanonicalJSON(inspection.semantic)
			if err != nil {
				t.Fatal(err)
			}
			source, err := project.ToSource(inspection.semantic)
			if err != nil {
				t.Fatal(err)
			}
			score, ds := notation.Parse(source)
			if score == nil || hasDiagnosticErrors(ds) {
				t.Fatalf("normalized demo cannot parse: %v", ds)
			}
			restored, ds := project.FromScore(score)
			if restored == nil || hasDiagnosticErrors(ds) {
				t.Fatalf("normalized demo cannot compile: %v", ds)
			}
			after, err := project.CanonicalJSON(restored)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("source conversion changed the demo's musical meaning")
			}
			midi, err := smf.FromProject(inspection.semantic, 0)
			if err != nil {
				t.Fatalf("full-song MIDI export: %v", err)
			}
			var encoded bytes.Buffer
			if err := smf.Encode(midi, &encoded); err != nil {
				t.Fatal(err)
			}
			decoded, err := smf.Decode(&encoded)
			if err != nil {
				t.Fatal(err)
			}
			if err := smf.MusicalDiff(midi, decoded); err != nil {
				t.Fatalf("MIDI round trip changed the song: %v", err)
			}
			if _, err := smf.FromPattern(inspection.semantic, inspection.semantic.Patterns[0].ID); err != nil {
				t.Fatalf("independent pattern MIDI export: %v", err)
			}
		})
	}
}
