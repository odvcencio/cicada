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
	for _, args := range [][]string{{"help", "lib"}, {"lib", "--help"}} {
		var output, stderr bytes.Buffer
		handled, status := handleCLIHelp(args, &output, &stderr)
		if !handled || status != 0 || !strings.Contains(output.String(), "cicada lib update [PATH]") || !strings.Contains(output.String(), "cicada lib list") || stderr.Len() != 0 {
			t.Fatalf("help: %v %d %s %s", args, status, &output, &stderr)
		}
	}
}

func TestLibListStdProjectAndUserWithoutScores(t *testing.T) {
	root, user := t.TempDir(), t.TempDir()
	t.Setenv("CICADA_LIBRARY", user)
	t.Chdir(root)
	for _, base := range []string{filepath.Join(root, "lib"), user} {
		dir := filepath.Join(base, "demo", "tone")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cicada.mod"), []byte("library demo/tone\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// Listing discovers manifests without loading scores or interpreting pins.
	sum := []byte("untouched pins\n")
	if err := os.WriteFile("cicada.sum", sum, 0644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := libCommand([]string{"list"}, &output); err != nil {
		t.Fatal(err)
	}
	expected := "project\tdemo/tone\nuser\tdemo/tone\nstd\tstd/drums\nstd\tstd/fx\nstd\tstd/keys\nstd\tstd/presets\nstd\tstd/synth\n"
	if output.String() != expected {
		t.Fatalf("list: %s", &output)
	}
	after, err := os.ReadFile("cicada.sum")
	if err != nil || !bytes.Equal(sum, after) {
		t.Fatalf("list changed pins: %s %v", after, err)
	}
	if err := libCommand([]string{"list", "extra"}, &output); err == nil {
		t.Fatal("list accepted an argument")
	}
}

func TestLibrarySubcommandHelp(t *testing.T) {
	for _, name := range []string{"list", "show", "new", "update", "vendor"} {
		for _, args := range [][]string{{"help", "lib", name}, {"lib", name, "--help"}} {
			var output, stderr bytes.Buffer
			handled, status := handleCLIHelp(args, &output, &stderr)
			if !handled || status != 0 || !strings.Contains(output.String(), "cicada lib "+name) || stderr.Len() != 0 {
				t.Fatalf("help: %v %d %s %s", args, status, &output, &stderr)
			}
		}
	}
}

func TestLibNewShowAndVendor(t *testing.T) {
	root, user := t.TempDir(), t.TempDir()
	t.Setenv("CICADA_LIBRARY", user)
	t.Chdir(root)
	var output bytes.Buffer
	if err := libCommand([]string{"new", "demo/tone"}, &output); err != nil {
		t.Fatal(err)
	}
	if err := libCommand([]string{"new", "demo/tone"}, &output); err == nil {
		t.Fatal("overwrote existing library")
	}
	if err := libCommand([]string{"show", "demo/tone"}, &output); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"library demo/tone", "resolved: user", "hash: sha256:", "Declarations:\n  tone", "Assets:"} {
		if !strings.Contains(output.String(), text) {
			t.Fatalf("show missing %s: %s", text, &output)
		}
	}
	for name, text := range map[string]string{"cicada.mod": "project score\ncicada 2\nentry \"main.cicada\"\n", "main.cicada": "import \"demo/tone\"\ntrack lead tone.tone {}\npattern melody { 1 . 5 . }\nscene main { lead = melody }\nsong { main }\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := libCommand([]string{"update"}, &output); err != nil {
		t.Fatal(err)
	}
	if err := libCommand([]string{"vendor"}, &output); err != nil {
		t.Fatal(err)
	}
	if _, err := loadProject(filepath.Join(root, "main.cicada")); err != nil {
		t.Fatal(err)
	}
	if err := libCommand([]string{"vendor"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "already vendored") {
		t.Fatal(&output)
	}
	explicit := filepath.Join(t.TempDir(), "nested", "library")
	if err := libCommand([]string{"new", "other/sound", "--dir", explicit}, &output); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(explicit, "cicada.mod"))
	if err != nil || !strings.Contains(string(data), "library other/sound") {
		t.Fatalf("explicit scaffold: %s %v", data, err)
	}
	if err := libCommand([]string{"new", filepath.Join(t.TempDir(), "tone")}, &output); err != nil {
		t.Fatal(err)
	}
}

func TestLibVendorVerifiesEveryLegacyScore(t *testing.T) {
	root, user := t.TempDir(), t.TempDir()
	t.Setenv("CICADA_LIBRARY", user)
	t.Chdir(root)
	if err := project.NewLibrary("demo/tone", filepath.Join(user, "demo", "tone")); err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{
		"cicada.mod": "project legacy\ncicada 2\n",
		"a.cicada":   "track lead acid {}\npattern melody acid { 1 . }\nscene main { lead = melody }\nsong { main }\n",
		"b.cicada":   "import \"demo/tone\"\ntrack lead tone.tone {}\npattern melody { 1 . }\nscene main { lead = melody }\nsong { main }\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	if err := libCommand([]string{"vendor"}, &output); err == nil || !strings.Contains(err.Error(), "CICADA-LIB-HASH") {
		t.Fatalf("vendor accepted unpinned second score: %v", err)
	}
	if err := libCommand([]string{"update"}, &output); err != nil {
		t.Fatal(err)
	}
	if err := libCommand([]string{"vendor"}, &output); err != nil {
		t.Fatal(err)
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

func TestLibVendorUsesNestedLooseScoreRoot(t *testing.T) {
	main, _ := looseImportFixture(t)
	root := filepath.Dir(main)
	user := t.TempDir()
	if err := os.Rename(filepath.Join(root, "lib", "demo"), filepath.Join(user, "demo")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CICADA_LIBRARY", user)
	t.Chdir(filepath.Dir(root))
	var output bytes.Buffer
	if err := libCommand([]string{"update"}, &output); err != nil {
		t.Fatal(err)
	}
	if err := libCommand([]string{"vendor"}, &output); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(user); err != nil {
		t.Fatal(err)
	}
	if p, err := loadProject(main); p == nil || err != nil {
		t.Fatalf("vendored nested score failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "cicada.sum")); !os.IsNotExist(err) {
		t.Fatalf("pins written outside score root: %v", err)
	}
}

func TestLibUpdateHonorsVendorLock(t *testing.T) {
	main, library := looseImportFixture(t)
	root := filepath.Dir(main)
	t.Chdir(root)
	if err := os.WriteFile(filepath.Join(root, ".cicada-vendor.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	// The lock must exclude loading too: a concurrent vendor may be moving
	// libraries while it holds the pin lock.
	if err := os.Remove(library); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := libCommand([]string{"update"}, &output); err == nil || !strings.Contains(err.Error(), "cannot lock library pins") {
		t.Fatalf("update bypassed vendor lock: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "cicada.sum")); !os.IsNotExist(err) {
		t.Fatal("locked update published pins")
	}
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

func TestProjectRootNamedLibKeepsCLIWorkflows(t *testing.T) {
	parent := t.TempDir()
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	t.Chdir(parent)
	if err := newProject("lib", os.WriteFile); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "lib")
	main := filepath.Join(root, "main.cicada")
	source := []byte("import \"demo/tone\"\ntrack lead tone.glass{}\nscene verse{lead=tone.melody}\nsong{verse}\n")
	for name, data := range map[string][]byte{
		"main.cicada":               source,
		"lib/demo/tone/cicada.mod":  []byte("library demo/tone\ncicada 2\nsource \"tone.cicada\"\nlicense \"MIT\"\nauthor \"Cicada contributors\"\n"),
		"lib/demo/tone/tone.cicada": []byte("instrument glass { voice mono { out=sine(pitch) } }\npattern melody { 1 . 5 . }\n"),
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := loadProject(main); err == nil || !strings.Contains(err.Error(), "CICADA-LIB-HASH") {
		t.Fatalf("unpinned named-lib fixture: %v", err)
	}
	t.Chdir(root)
	var output, diagnostics bytes.Buffer
	if err := libCommand([]string{"update"}, &output); err != nil {
		t.Errorf("valid project named lib was skipped by pin update: %v", err)
	}
	if p, err := loadProject(main); err != nil || p == nil {
		t.Errorf("named-lib project remains unpinned: %v", err)
	}
	if err := formatProjectCommand(true, &output, &diagnostics); err == nil {
		t.Error("project formatter skipped an unformatted score under root named lib")
	}
	if err := formatProjectCommand(false, &output, &diagnostics); err != nil {
		t.Fatal(err)
	}
	formatted, err := os.ReadFile(main)
	if err != nil || bytes.Equal(formatted, source) {
		t.Errorf("project formatter did not format root named lib: %v", err)
	}
	if p, err := loadProject(main); err != nil || p == nil {
		t.Errorf("formatted named-lib project does not load: %v", err)
	}
}
