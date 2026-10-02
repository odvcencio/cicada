package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestMultiFileToolsAndStudioRefusal(t *testing.T) {
	entry := filepath.Join("..", "..", "examples", "multifile", "main.cicada")
	inspection, err := inspectScore(entry)
	if err != nil || hasDiagnosticErrors(inspection.diagnostics) || inspection.semantic == nil {
		t.Fatalf("check: %v %+v", err, inspection.diagnostics)
	}
	root, _ := filepath.Abs(filepath.Dir(entry))
	paths, err := projectScorePaths(root)
	if err != nil || len(paths) != 3 {
		t.Fatalf("source list: %v %v", paths, err)
	}
	var stdout, stderr bytes.Buffer
	if err := checkPaths(paths[:1], &stdout, &stderr); err != nil {
		t.Fatalf("check: %v %s", err, stderr.String())
	}
	if err := formatProject(root, true, &stdout, &stderr); err != nil {
		t.Fatalf("fmt --check: %v %s", err, stderr.String())
	}
	for _, path := range paths {
		data, _ := os.ReadFile(path)
		doc, err := notation.ParseDocument(data)
		if err != nil || !bytes.Equal(notation.Print(doc), data) {
			t.Fatalf("lossless parse/print: %s %v", path, err)
		}
		formatted, err := notation.Format(doc)
		if err != nil || !bytes.Equal(formatted, data) {
			t.Fatalf("fmt byte round-trip: %s %v", path, err)
		}
	}
	var output bytes.Buffer
	if err := explainParameter(entry, "bass.cutoff", "@1.1.1", &output); err != nil || !strings.Contains(output.String(), "computed:") {
		t.Fatalf("explain: %v %s", err, output.String())
	}
	for _, rate := range []int{44100, 48000} {
		if _, err := compileLiveScoreAtRate(entry, rate); err != nil {
			t.Fatalf("play at %d Hz: %v", rate, err)
		}
	}
	for _, path := range paths {
		if _, err := newStudioWithInvalid(path, true); err == nil || !strings.Contains(err.Error(), "Studio cannot edit multi-file projects") {
			t.Fatalf("Studio should refuse before recovery or history writes: %v", err)
		}
	}
}

func TestMultiFileFixIsAtomicAcrossSources(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"cicada.mod":   "project score\ncicada 1\nentry \"main.cicada\"\nsource \"parts.cicada\"\nlicense \"MIT\"\nauthor \"Cicada contributors\"\n",
		"main.cicada":  "scene verse { bass = pulse }\nsong { verse }\n",
		"parts.cicada": "// keep this comment\ntrack bass acid { send_a = 0.2 }\nfx room delay {}\npattern pulse acid steps=4 { 1 . 5 . }\n",
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	entry := filepath.Join(root, "main.cicada")
	if err := fixCommand([]string{entry, "--all", "--check"}); err == nil || !strings.Contains(err.Error(), "fix needed") {
		t.Fatalf("fix check: %v", err)
	}
	before, _ := loadProject(entry)
	if err := fixCommand([]string{entry, "--all"}); err != nil {
		t.Fatal(err)
	}
	after, err := loadProject(entry)
	if err != nil || before == nil || after == nil {
		t.Fatalf("fixed project: %v", err)
	}
	main, _ := os.ReadFile(entry)
	if string(main) != files["main.cicada"] {
		t.Fatalf("fix moved declarations into entry: %s", main)
	}
	part, _ := os.ReadFile(filepath.Join(root, "parts.cicada"))
	if !bytes.Contains(part, []byte("send room = 0.2")) || !bytes.Contains(part, []byte("// keep this comment")) {
		t.Fatalf("fix lost peer source: %s", part)
	}
	manifest, _ := os.ReadFile(filepath.Join(root, "cicada.mod"))
	if !bytes.Contains(manifest, []byte("cicada 2")) || !bytes.Contains(manifest, []byte("license \"MIT\"")) {
		t.Fatalf("fix lost metadata: %s", manifest)
	}
	if err := fixCommand([]string{entry, "--all", "--check"}); err != nil {
		t.Fatalf("fixed package should be idempotent: %v", err)
	}
}

func TestPlayWatchHashesEverySource(t *testing.T) {
	root := t.TempDir()
	for name, text := range map[string]string{"cicada.mod": "project score\ncicada 2\nentry \"main.cicada\"\nsource \"parts.cicada\"\n", "main.cicada": "scene verse { bass = pulse }\nsong { verse }\n", "parts.cicada": "track bass acid {}\npattern pulse { 1 . }\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	entry := filepath.Join(root, "main.cicada")
	before, err := playSourceHash(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "parts.cicada"), []byte("track bass acid {}\npattern pulse { 5 . }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	after, err := playSourceHash(entry)
	if err != nil || before == after {
		t.Fatalf("play watch missed peer source change: %v", err)
	}
}
