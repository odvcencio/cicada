package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
)

func TestLibUpdateCheckAndExplain(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	for name, text := range map[string]string{
		"cicada.mod":                "project score\ncicada 2\nentry \"main.cicada\"\n",
		"main.cicada":               "import \"demo/tone\"\ntrack lead tone.glass {}\nscene verse { lead = tone.melody }\nsong { verse }\n",
		"lib/demo/tone/cicada.mod":  "library demo/tone\ncicada 2\nsource \"tone.cicada\"\nlicense \"MIT\"\nauthor \"Cicada contributors\"\n",
		"lib/demo/tone/tone.cicada": "instrument glass { voice mono { out = sine(pitch) * env(gate,90ms) } }\npattern melody { 1 . 5 . }\n",
	} {
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)
	main := filepath.Join(root, "main.cicada")
	// Both playback's loadProject gate and render's inspectScore gate reject
	// unpinned imports. Check prints the positioned hash diagnostic.
	if _, err := loadProject(main); err == nil || !strings.Contains(err.Error(), "CICADA-LIB-HASH") {
		t.Fatalf("play gate: %v", err)
	}
	var output, diagnostics bytes.Buffer
	if err := checkCommand([]string{main}, &output, &diagnostics); err == nil || !strings.Contains(diagnostics.String(), "CICADA-LIB-HASH") {
		t.Fatalf("check gate: %v %s", err, &diagnostics)
	}
	if err := libCommand([]string{"update"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "unpinned -> project sha256:") {
		t.Fatalf("update report: %s", &output)
	}
	if p, err := loadProject(main); err != nil || p == nil {
		t.Fatalf("pinned play gate: %v", err)
	}
	output.Reset()
	if err := explainParameter(main, "tone.glass", "", &output); err != nil || !strings.Contains(output.String(), "library demo/tone") {
		t.Fatalf("explain declaration: %v %s", err, &output)
	}
	output.Reset()
	if err := explainParameter(main, "lead.level", "", &output); err != nil || !strings.Contains(output.String(), "library demo/tone") {
		t.Fatalf("explain value: %v %s", err, &output)
	}
	source := filepath.Join(root, "lib/demo/tone/tone.cicada")
	data, _ := os.ReadFile(source)
	if err := os.WriteFile(source, append(data, []byte("// intended change\n")...), 0644); err != nil {
		t.Fatal(err)
	}
	inspection, err := inspectScore(main)
	if err != nil || !hasDiagnosticErrors(inspection.diagnostics) {
		t.Fatalf("render gate: %v %+v", err, inspection.diagnostics)
	}
	output.Reset()
	if err := libCommand([]string{"update", "demo/tone"}, &output); err != nil || !strings.Contains(output.String(), " -> project sha256:") {
		t.Fatalf("re-pin: %v %s", err, &output)
	}
	output.Reset()
	if err := libCommand([]string{"update", "demo/tone"}, &output); err != nil || !strings.Contains(output.String(), "unchanged") {
		t.Fatalf("idempotent re-pin: %v %s", err, &output)
	}
	sources, err := project.ReadSources(main, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ds := sources.Parse(); len(ds) != 0 {
		t.Fatal(ds)
	}
}

func TestLibraryCommandHelp(t *testing.T) {
	for _, args := range [][]string{{"help", "lib"}, {"lib", "--help"}, {"lib", "update", "--help"}} {
		var output, stderr bytes.Buffer
		handled, status := handleCLIHelp(args, &output, &stderr)
		if !handled || status != 0 || !strings.Contains(output.String(), "cicada lib update [PATH]") || stderr.Len() != 0 {
			t.Fatalf("help: %v %d %s %s", args, status, &output, &stderr)
		}
	}
}
