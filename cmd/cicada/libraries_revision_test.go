package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/sampleasset"
	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/project"
)

func revisionImportFixture(t *testing.T, edition int, audio bool) (string, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	main := filepath.Join(root, "main.cicada")
	library := filepath.Join(root, "lib/demo/tone/tone.cicada")
	source := "import \"demo/tone\"\nfx delay { feedback=0.2 }\ntrack lead tone.glass { send_a=0.4 }\nscene verse { lead=tone.melody }\nsong { verse }\n"
	lib := "instrument glass { voice mono { out=sine(pitch) } }\npattern melody { 1 . 5 . }\n"
	libEdition := 1
	files := map[string][]byte{}
	if audio {
		libEdition = 2
		data := testwav.Bytes(48000, 1, 16, 4800, 1)
		lib = fmt.Sprintf("asset wave \"audio/tone.wav\" { sha256=\"%x\" format=wav frames=4800 rate=48000Hz channels=1 }\nsampler hit { asset=wave root=c3 mode=oneshot voices=8 }\npattern melody { c3 . c3 . }\n", sha256.Sum256(data))
		source = "import \"demo/tone\"\ntrack lead tone.hit {}\nscene verse { lead=tone.melody }\nsong { verse }\n"
		files[filepath.Join(root, "lib/demo/tone/audio/tone.wav")] = data
	}
	files[filepath.Join(root, "cicada.mod")] = []byte(fmt.Sprintf("project score\ncicada %d\n", edition))
	files[main] = []byte(source)
	files[library] = []byte(lib)
	files[filepath.Join(root, "lib/demo/tone/cicada.mod")] = []byte(fmt.Sprintf("library demo/tone\ncicada %d\nsource \"tone.cicada\"\nlicense \"MIT\"\nauthor \"Cicada contributors\"\n", libEdition))
	for path, data := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	sources, err := project.ReadSources(main, nil)
	if err != nil {
		t.Fatal(err)
	}
	sum, _, err := sources.UpdateLibraries("")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cicada.sum"), sum, 0600); err != nil {
		t.Fatal(err)
	}
	return main, library
}

func TestLegacySingleScoreImportsMigrateWithPinnedLibraries(t *testing.T) {
	for _, all := range []bool{false, true} {
		t.Run(fmt.Sprint(all), func(t *testing.T) {
			main, library := revisionImportFixture(t, 1, false)
			root := filepath.Dir(main)
			before, err := loadProject(main)
			if err != nil {
				t.Fatalf("original pinned score: %v", err)
			}
			paths := []string{main}
			source, _ := os.ReadFile(main)
			if all {
				sibling := filepath.Join(root, "second.cicada")
				if err := os.WriteFile(sibling, bytes.ReplaceAll(source, []byte("lead"), []byte("second")), 0600); err != nil {
					t.Fatal(err)
				}
				paths = append(paths, sibling)
			}
			preserved := map[string][]byte{}
			for _, path := range append(append([]string{}, paths...), library, filepath.Join(root, "cicada.sum"), filepath.Join(root, "cicada.mod")) {
				preserved[path], err = os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			t.Chdir(root)
			args := []string{main}
			if all {
				args = []string{"--all"}
			}
			if err := fixCommand(append(append([]string{}, args...), "--check")); err == nil || !strings.Contains(err.Error(), "fix needed") {
				t.Fatalf("import-aware check did not reach migration: %v", err)
			}
			for path, data := range preserved {
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, data) {
					t.Fatalf("check changed %s: %v", path, err)
				}
			}
			if err := fixCommand(args); err != nil {
				t.Fatalf("pinned independent migration: %v", err)
			}
			for _, path := range paths {
				after, err := loadProject(path)
				if err != nil {
					t.Fatalf("migrated %s: %v", path, err)
				}
				if path == main && !project.SemanticEqual(before, after) {
					t.Fatal("migration changed musical meaning")
				}
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != 0600 {
					t.Fatalf("mode changed: %v %v", info, err)
				}
			}
			for _, path := range []string{library, filepath.Join(root, "cicada.sum")} {
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, preserved[path]) {
					t.Fatalf("migration changed library or pins: %s %v", path, err)
				}
			}
			if err := fixCommand([]string{main, "--check"}); err != nil {
				t.Fatalf("second check: %v", err)
			}
		})
	}
}

func TestConvertImportedAssetsRefusesBeforeWriting(t *testing.T) {
	main, _ := revisionImportFixture(t, 2, true)
	root := filepath.Dir(main)
	original, err := loadProject(main)
	if err != nil {
		t.Fatalf("pinned sampler score: %v", err)
	}
	if _, err := sampleasset.LoadSampler(root, original, "demo.tone.hit"); err != nil {
		t.Fatalf("original sampler: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "cicada")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	target := filepath.Join(root, "converted.json")
	out, err := exec.Command(bin, "convert", main, "-o", target).CombinedOutput()
	if err == nil {
		decoded, decodeErr := loadProject(target)
		var samplerErr error
		if decodeErr == nil {
			_, samplerErr = sampleasset.LoadSampler(root, decoded, "demo.tone.hit")
		}
		flat := filepath.Join(root, "converted.cicada")
		flatOut, flatErr := exec.Command(bin, "convert", target, "-o", flat).CombinedOutput()
		_, reloadErr := loadProject(flat)
		t.Fatalf("conversion reported success while losing library asset ownership: output=%s decoded=%v sampler=%v flat=%s flatErr=%v reload=%v", out, decodeErr, samplerErr, flatOut, flatErr, reloadErr)
	}
	if !bytes.Contains(out, []byte("CICADA-LIB-ASSET")) {
		t.Fatalf("missing actionable conversion refusal: %v %s", err, out)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("refused conversion wrote output: %v", err)
	}
}

func TestIndependentImportFixRejectsChangedPinsWithoutWriting(t *testing.T) {
	main, library := revisionImportFixture(t, 1, false)
	root := filepath.Dir(main)
	source, err := os.ReadFile(main)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(root, "cicada.mod"))
	if err != nil {
		t.Fatal(err)
	}
	pins, err := os.ReadFile(filepath.Join(root, "cicada.sum"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(library)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(library, append(data, []byte("// changed after pinning\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	if err := fixCommand([]string{main}); err == nil || !strings.Contains(err.Error(), "CICADA-LIB-HASH") {
		t.Fatalf("changed import was migrated: %v", err)
	}
	for path, want := range map[string][]byte{main: source, filepath.Join(root, "cicada.mod"): manifest, filepath.Join(root, "cicada.sum"): pins} {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("refused migration changed %s: %v", path, err)
		}
	}
}

func TestIndependentImportedReturnMigrationUsesSourceAliases(t *testing.T) {
	for _, kind := range []string{"delay", "reverb"} {
		t.Run(kind, func(t *testing.T) {
			main, library := revisionImportFixture(t, 1, false)
			root := filepath.Dir(main)
			data, err := os.ReadFile(main)
			if err != nil {
				t.Fatal(err)
			}
			data = bytes.Replace(data, []byte("fx delay { feedback=0.2 }\n"), nil, 1)
			if kind == "reverb" {
				data = bytes.ReplaceAll(data, []byte("send_a"), []byte("send_b"))
			}
			if err := os.WriteFile(main, data, 0600); err != nil {
				t.Fatal(err)
			}
			lib, err := os.ReadFile(library)
			if err != nil {
				t.Fatal(err)
			}
			lib = append(lib, []byte("fx echo "+kind+" {}\n")...)
			if err := os.WriteFile(library, lib, 0600); err != nil {
				t.Fatal(err)
			}
			sources, err := project.ReadSources(main, nil)
			if err != nil {
				t.Fatal(err)
			}
			sum, _, err := sources.UpdateLibraries("")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "cicada.sum"), sum, 0600); err != nil {
				t.Fatal(err)
			}
			before, err := loadProject(main)
			if err != nil {
				t.Fatalf("original imported return: %v", err)
			}
			if err := fixCommand([]string{main, "--check"}); err == nil || !strings.Contains(err.Error(), "fix needed") {
				t.Fatalf("imported return migration check: %v", err)
			}
			if err := fixCommand([]string{main}); err != nil {
				t.Fatalf("imported return migration: %v", err)
			}
			after, err := loadProject(main)
			if err != nil || !project.SemanticEqual(before, after) {
				t.Fatalf("migrated return changed meaning: %v", err)
			}
			fixed, err := os.ReadFile(main)
			if err != nil || !bytes.Contains(fixed, []byte("send tone.echo")) || bytes.Contains(fixed, []byte("send demo.tone.echo")) {
				t.Fatalf("migration used an internal ID as source syntax: %v %s", err, fixed)
			}
			got, err := os.ReadFile(library)
			if err != nil || !bytes.Equal(got, lib) {
				t.Fatalf("library return was rewritten: %v", err)
			}
			got, err = os.ReadFile(filepath.Join(root, "cicada.sum"))
			if err != nil || !bytes.Equal(got, sum) {
				t.Fatalf("library pin was changed: %v", err)
			}
		})
	}
}

func TestIndependentSamplerFixIsIdempotent(t *testing.T) {
	main, library := revisionImportFixture(t, 2, true)
	root := filepath.Dir(main)
	p, err := loadProject(main)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sampleasset.LoadSampler(root, p, "demo.tone.hit"); err != nil {
		t.Fatal(err)
	}
	preserved := map[string][]byte{}
	for _, path := range []string{main, library, filepath.Join(root, "cicada.mod"), filepath.Join(root, "cicada.sum")} {
		preserved[path], err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{main, "--check"}, {main}} {
		if err := fixCommand(args); err != nil {
			t.Fatalf("valid pinned sampler no-op fix: %v", err)
		}
	}
	for path, want := range preserved {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("sampler no-op changed %s: %v", path, err)
		}
	}
	// Re-pin deliberately changed library bytes without altering the asset's
	// declared hash. The independent-score fix still has to verify the asset.
	audio := filepath.Join(root, "lib/demo/tone/audio/tone.wav")
	if err := os.WriteFile(audio, testwav.Bytes(48000, 1, 16, 4800, 2), 0600); err != nil {
		t.Fatal(err)
	}
	sources, err := project.ReadSources(main, nil)
	if err != nil {
		t.Fatal(err)
	}
	sum, _, err := sources.UpdateLibraries("")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cicada.sum"), sum, 0600); err != nil {
		t.Fatal(err)
	}
	if err := fixCommand([]string{main, "--check"}); err == nil || !strings.Contains(err.Error(), "CICADA-ASSET-") {
		t.Fatalf("sampler fix skipped asset verification: %v", err)
	}
	got, err := os.ReadFile(main)
	if err != nil || !bytes.Equal(got, preserved[main]) {
		t.Fatalf("rejected sampler changed source: %v", err)
	}
}

func TestCombinedImportFixRejectsChangedPinsWithoutWriting(t *testing.T) {
	for _, check := range []bool{true, false} {
		t.Run(fmt.Sprint(check), func(t *testing.T) {
			main, library := revisionImportFixture(t, 1, false)
			root := filepath.Dir(main)
			if err := os.WriteFile(filepath.Join(root, "cicada.mod"), []byte("project score\ncicada 1\nentry \"main.cicada\"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			lib, err := os.ReadFile(library)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(library, append(lib, []byte("// changed after pinning\n")...), 0600); err != nil {
				t.Fatal(err)
			}
			preserved := map[string][]byte{}
			for _, path := range []string{main, library, filepath.Join(root, "cicada.mod"), filepath.Join(root, "cicada.sum")} {
				preserved[path], err = os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			args := []string{main, "--all"}
			if check {
				args = append(args, "--check")
			}
			if err := fixCommand(args); err == nil || !strings.Contains(err.Error(), "CICADA-LIB-HASH") {
				t.Errorf("combined migration used an unverified import: %v", err)
			}
			for path, want := range preserved {
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, want) {
					t.Errorf("unverified migration changed %s: %v", path, err)
				}
			}
		})
	}
}

func TestCombinedImportFixRejectsChangedAssetAfterRepinning(t *testing.T) {
	for _, check := range []bool{true, false} {
		t.Run(fmt.Sprint(check), func(t *testing.T) {
			main, library := revisionImportFixture(t, 2, true)
			root := filepath.Dir(main)
			if err := os.WriteFile(filepath.Join(root, "cicada.mod"), []byte("project score\ncicada 2\nentry \"main.cicada\"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			asset := filepath.Join(root, "lib/demo/tone/audio/tone.wav")
			if err := os.WriteFile(asset, testwav.Bytes(48000, 1, 16, 4800, 2), 0600); err != nil {
				t.Fatal(err)
			}
			sources, err := project.ReadSources(main, nil)
			if err != nil {
				t.Fatal(err)
			}
			sum, _, err := sources.UpdateLibraries("")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "cicada.sum"), sum, 0600); err != nil {
				t.Fatal(err)
			}
			preserved := map[string][]byte{}
			for _, path := range []string{main, library, asset, filepath.Join(root, "cicada.mod"), filepath.Join(root, "cicada.sum")} {
				preserved[path], err = os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			args := []string{main, "--all"}
			if check {
				args = append(args, "--check")
			}
			if err := fixCommand(args); err == nil || !strings.Contains(err.Error(), "CICADA-ASSET-") {
				t.Errorf("combined sampler fix skipped asset verification: %v", err)
			}
			for path, want := range preserved {
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, want) {
					t.Errorf("rejected combined sampler changed %s: %v", path, err)
				}
			}
		})
	}
}
