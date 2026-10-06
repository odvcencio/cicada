package project

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

func writeSource(t *testing.T, root, path, source string) string {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	return full
}

func TestMultiFileLoadResolvesBeforeExpansion(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "cicada.mod", "project score\ncicada 2\nentry \"main.cicada\"\nsource \"parts/z.cicada\"\nsource \"parts/a.cicada\"\n")
	entry := writeSource(t, root, "main.cicada", "cicada 2\ntitle \"Shared\"\nscene verse { lead = riff }\nsong { verse }\n")
	writeSource(t, root, "parts/z.cicada", "cicada 2\ninstrument glass { voice mono { out = sine(pitch) * env(gate, 90ms) } }\ntrack lead glass {}\nphrase motif { 1 . 5 . }\n")
	writeSource(t, root, "parts/a.cicada", "pattern riff { use motif*2 }\n")
	set, err := ReadSources(entry, nil)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(set.Files[1].Path) != "a.cicada" {
		t.Fatalf("source order: %+v", set.Files)
	}
	score, ds := set.Parse()
	if score == nil || hasErrors(ds) {
		t.Fatalf("parse: %+v", ds)
	}
	if len(score.Patterns[0].Steps) != 8 || score.Patterns[0].Steps[0].Position.File != filepath.Join(root, "parts/z.cicada") {
		t.Fatalf("phrase expansion lost origin: %+v", score.Patterns)
	}
	p, ds := FromScore(score)
	if p == nil || hasErrors(ds) {
		t.Fatalf("compile: %+v", ds)
	}
	for _, file := range set.Files {
		doc, err := notation.ParseDocument(file.Source)
		if err != nil || !bytes.Equal(notation.Print(doc), file.Source) {
			t.Fatalf("lossless file round-trip: %s %v", file.Path, err)
		}
	}
	// An open buffer replaces its own file without changing other sources.
	override := map[string][]byte{set.Files[1].Path: []byte("pattern riff { use motif }\n")}
	changed, ds, err := LoadScore(entry, override)
	if err != nil || hasErrors(ds) || len(changed.Patterns[0].Steps) != 4 {
		t.Fatalf("override: %v %+v", err, ds)
	}
}

func TestMultiFileDiagnosticsRetainBothLocations(t *testing.T) {
	for _, declaration := range []string{
		"pattern pulse { 1 . 5 . }", "track bass acid {}", "phrase motif { 1 . }", "scene verse { bass = pulse }", "fx room delay {}", "instrument glass { voice mono { out = sine(pitch) } }", "title \"Shared\"", "tempo 130", "key a minor", "seed 3", "song { verse }", "master {}", "bus music {}", "export delivery {}",
	} {
		t.Run(declaration, func(t *testing.T) {
			files := []notation.SourceFile{{Path: "main.cicada", Source: []byte(declaration + "\n")}, {Path: "parts/other.cicada", Source: []byte("// another file\n" + declaration + "\n")}}
			_, ds := notation.ParseFiles(files, 2)
			for _, d := range ds {
				if d.Code == "CICADA-DUPLICATE" && d.Position.File == "parts/other.cicada" && d.Position.Line == 2 && d.Related.File == "main.cicada" && d.Related.Line == 1 {
					return
				}
			}
			t.Fatalf("missing both declaration locations: %+v", ds)
		})
	}
	files := []notation.SourceFile{{Path: "main.cicada", Source: []byte("track bass acid {}\npattern pulse { 1 . }\nsong { verse }\n")}, {Path: "parts/scene.cicada", Source: []byte("// Unicode title\nscene verse { bass = missing }\n")}}
	_, ds := notation.ParseFiles(files, 2)
	for _, d := range ds {
		if d.Code == "CICADA-REFERENCE" && d.Position.File == "parts/scene.cicada" && d.Position.Line == 2 && d.Position.Column > 1 {
			return
		}
	}
	t.Fatalf("missing positioned unresolved reference: %+v", ds)
}

func TestSourceLoaderRejectsMissingAndEscapingFiles(t *testing.T) {
	root := t.TempDir()
	entry := writeSource(t, root, "main.cicada", "track bass acid {}\npattern pulse { 1 . }\nscene verse { bass = pulse }\nsong { verse }\n")
	manifest := writeSource(t, root, "cicada.mod", "project score\ncicada 2\nentry \"main.cicada\"\nsource \"missing.cicada\"\n")
	_, err := ReadSources(entry, nil)
	var d *SourceError
	if !errors.As(err, &d) || d.Diagnostic.Code != "CICADA-SOURCE-MISSING" || d.Diagnostic.Position.File != manifest || d.Diagnostic.Position.Line != 4 {
		t.Fatalf("missing source: %v", err)
	}
	outside := writeSource(t, t.TempDir(), "outside.cicada", "pattern other { 1 . }\n")
	if err := os.Symlink(outside, filepath.Join(root, "missing.cicada")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err = ReadSources(entry, nil)
	if !errors.As(err, &d) || d.Diagnostic.Code != "CICADA-SOURCE-PATH" {
		t.Fatalf("escaping symlink: %v", err)
	}
}

func TestMultiFileSyntaxAndHeaderPositions(t *testing.T) {
	for _, body := range []string{"cicada 1\npattern pulse { 1 . }\n", "pattern pulse { @@@ }\n"} {
		_, ds := notation.ParseFiles([]notation.SourceFile{{Path: "main.cicada", Source: []byte("track bass acid {}\n")}, {Path: "parts/other.cicada", Source: []byte(body)}}, 2)
		found := false
		for _, d := range ds {
			if (d.Code == "CICADA-SYNTAX" || d.Code == "CICADA-VERSION") && d.Position.File == "parts/other.cicada" && d.Position.Line > 0 && d.Position.Column > 0 {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing file location: %+v", ds)
		}
	}
}

func TestMultiFileAudioReferencesUseProjectRoot(t *testing.T) {
	root, source, _ := assetFixture(t)
	lines := strings.Split(string(source), "\n")
	writeSource(t, root, "cicada.mod", "project audio-score\ncicada 2\nentry \"main.cicada\"\nsource \"parts/asset.cicada\"\nsource \"parts/regions.cicada\"\n")
	entry := writeSource(t, root, "main.cicada", strings.Join(lines[3:], "\n"))
	asset := writeSource(t, root, "parts/asset.cicada", lines[0]+"\n")
	writeSource(t, root, "parts/regions.cicada", strings.Join(lines[1:3], "\n")+"\n")
	score, ds, err := LoadScore(entry, nil)
	if err != nil || hasErrors(ds) {
		t.Fatalf("cross-file asset references: %v %+v", err, ds)
	}
	if p, ds := FromScore(score); p == nil || hasErrors(ds) {
		t.Fatalf("audio project: %+v", ds)
	}
	if err := os.Remove(filepath.Join(root, "vocal.wav")); err != nil {
		t.Fatal(err)
	}
	_, ds, err = LoadScore(entry, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ds {
		if d.Code == "CICADA-ASSET-MISSING" && d.Position.File == asset && d.Position.Line == 1 {
			return
		}
	}
	t.Fatalf("asset diagnostic lost source location: %+v", ds)
}
