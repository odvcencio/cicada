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

func looseImportFixture(t *testing.T) (string, string) {
	t.Helper()
	main, library := revisionImportFixture(t, 2, false)
	root := filepath.Dir(main)
	for _, name := range []string{"cicada.mod", "cicada.sum"} {
		if err := os.Remove(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	source := []byte("cicada 2\nimport \"demo/tone\"\ntrack lead tone.glass {}\nscene verse { lead=tone.melody }\nsong { verse }\n")
	if err := os.WriteFile(main, source, 0600); err != nil {
		t.Fatal(err)
	}
	sources, err := project.ReadSources(main, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _, err := sources.UpdateLibraries("")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cicada.sum"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if p, err := loadProject(main); err != nil || p == nil {
		t.Fatalf("loose fixture does not load with its correct pin: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "cicada.sum")); err != nil {
		t.Fatal(err)
	}
	return main, library
}

func TestLooseLibUpdateWritesScoreRoot(t *testing.T) {
	for _, name := range []string{"", "demo/tone"} {
		t.Run("library="+name, func(t *testing.T) {
			main, _ := looseImportFixture(t)
			root := filepath.Dir(main)
			cwd := filepath.Dir(root)
			t.Chdir(cwd)
			args := []string{"update"}
			if name != "" {
				args = append(args, name)
			}
			var output bytes.Buffer
			if err := libCommand(args, &output); err != nil {
				t.Fatal(err)
			}
			if p, err := loadProject(main); err != nil || p == nil {
				t.Errorf("successful update left nested loose score unpinned: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "cicada.sum")); err != nil {
				t.Errorf("score-root pins missing: %v", err)
			}
			if _, err := os.Stat(filepath.Join(cwd, "cicada.sum")); !os.IsNotExist(err) {
				t.Errorf("update wrote a sum outside score root: %v", err)
			}
			output.Reset()
			if err := libCommand(args, &output); err != nil || !strings.Contains(output.String(), "unchanged") {
				t.Errorf("nested loose re-pin is not idempotent: %v %s", err, &output)
			}
		})
	}
}

func TestLooseLibUpdateRejectsMixedRootsBeforeWriting(t *testing.T) {
	first, _ := looseImportFixture(t)
	second, _ := looseImportFixture(t)
	cwd := filepath.Dir(filepath.Dir(first))
	if cwd != filepath.Dir(filepath.Dir(second)) {
		t.Fatal("fixture roots do not share parent")
	}
	t.Chdir(cwd)
	var output bytes.Buffer
	if err := libCommand([]string{"update"}, &output); err == nil || !strings.Contains(err.Error(), "CICADA-LIB-ROOT") {
		t.Errorf("mixed loose roots must refuse before pin publication: %v %s", err, &output)
	}
	for _, root := range []string{cwd, filepath.Dir(first), filepath.Dir(second)} {
		if _, err := os.Stat(filepath.Join(root, "cicada.sum")); !os.IsNotExist(err) {
			t.Errorf("refused update changed pin root %s: %v", root, err)
		}
	}
}

func TestLibUpdateCombinesLegacyManifestNestedScores(t *testing.T) {
	main, _ := revisionImportFixture(t, 2, false)
	root := filepath.Dir(main)
	nested := filepath.Join(root, "scores", "other.cicada")
	if err := os.MkdirAll(filepath.Dir(nested), 0755); err != nil {
		t.Fatal(err)
	}
	source := []byte("import \"demo/tone\"\ntrack lead tone.glass {}\nscene verse { lead=tone.melody }\nsong { verse }\n")
	if err := os.WriteFile(main, source, 0600); err != nil {
		t.Fatal(err)
	}
	if p, err := loadProject(main); err != nil || p == nil {
		t.Fatalf("legacy fixture does not load with its correct pin: %v", err)
	}
	if err := os.WriteFile(nested, source, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "cicada.sum")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	var output bytes.Buffer
	if err := libCommand([]string{"update"}, &output); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{main, nested} {
		if p, err := loadProject(path); err != nil || p == nil {
			t.Errorf("shared legacy manifest score not pinned: %s %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(nested), "cicada.sum")); !os.IsNotExist(err) {
		t.Errorf("shared manifest must use its root sum: %v", err)
	}
}
