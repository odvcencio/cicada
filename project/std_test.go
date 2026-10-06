package project

import (
	"bytes"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestStdCatalogAndMetadata(t *testing.T) {
	root, user := t.TempDir(), t.TempDir()
	t.Setenv("CICADA_LIBRARY", user)
	expected := []string{"std/drums", "std/fx", "std/keys", "std/presets", "std/synth"}
	if got := LibraryPaths(root); !reflect.DeepEqual(got, expected) {
		t.Fatalf("std paths: %v", got)
	}
	for _, name := range expected {
		lib, err := (&Sources{Root: root}).resolveLibrary(name, nil)
		if err != nil {
			t.Fatal(err)
		}
		if lib.Kind != "std" || lib.Manifest.Library != name || lib.Manifest.License != "MIT" || lib.Manifest.Author != "Cicada project" || lib.Manifest.Edition != 2 {
			t.Fatalf("std metadata: %+v", lib)
		}
		// The embedded library must hash exactly like the same bytes vendored
		// on disk. Resolution kind stays separate from the content digest.
		copyRoot := t.TempDir()
		err = fs.WalkDir(standardLibraries, name, func(file string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			data, err := fs.ReadFile(standardLibraries, file)
			if err == nil {
				libraryWrite(t, copyRoot, "lib/demo/copy/"+strings.TrimPrefix(file, name+"/"), string(data))
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		copy, err := (&Sources{Root: copyRoot}).resolveLibrary("demo/copy", nil)
		if err != nil || copy.SHA256 != lib.SHA256 || copy.Kind != "project" {
			t.Fatalf("embedded/disk hash mismatch: %+v %v", copy, err)
		}
	}
	// Listing retains every origin, even when resolution must reject shadowing.
	installLibrary(t, filepath.Join(root, "lib"), "std/synth", libraryVoice)
	installLibrary(t, user, "std/synth", libraryVoice)
	locations, err := ListLibraries(root)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, location := range locations {
		if location.Path == "std/synth" {
			kinds = append(kinds, location.Kind)
		}
	}
	if !reflect.DeepEqual(kinds, []string{"std", "project", "user"}) {
		t.Fatalf("shadowed catalog: %v", kinds)
	}
	if _, err := (&Sources{Root: root}).resolveLibrary("std/synth", nil); err == nil || !strings.Contains(err.Error(), "CICADA-LIB-SHADOW") {
		t.Fatalf("std shadowing: %v", err)
	}
}

func TestStdUpgradeRequiresRepinning(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	libraryWrite(t, root, "cicada.mod", "project std-upgrade\ncicada 2\nentry \"main.cicada\"\n")
	libraryWrite(t, root, "main.cicada", "import \"std/presets\"\ntrack bass presets.acid-squelch {}\npattern riff { 1 . 5 . }\nscene main { bass = riff }\nsong { main }\n")
	requireLibraryDiagnostic(t, root, "CICADA-LIB-HASH")
	original := pinLibraryFixture(t, root)
	oldPin := original.Libraries["std/presets"].LibraryPin
	if oldPin.Kind != "std" {
		t.Fatal(oldPin)
	}
	file := original.Libraries["std/presets"].Files[0]
	// An unsaved source override simulates a newer binary's embedded bytes.
	changed := bytes.Replace(file.Source, []byte("cutoff = 620Hz"), []byte("cutoff = 700Hz"), 1)
	if bytes.Equal(changed, file.Source) {
		t.Fatal("upgrade fixture did not change source bytes")
	}
	upgraded, err := ReadSources(filepath.Join(root, "main.cicada"), map[string][]byte{file.Path: changed})
	if err != nil {
		t.Fatal(err)
	}
	_, ds := upgraded.Parse()
	if len(ds) != 1 || ds[0].Code != "CICADA-LIB-HASH" || !strings.Contains(ds[0].Message, "-> std sha256:") {
		t.Fatalf("upgrade silently accepted: %+v", ds)
	}
	data, changes, err := upgraded.UpdateLibraries("std/presets")
	if err != nil || len(changes) != 1 || upgraded.Libraries["std/presets"].SHA256 == oldPin.SHA256 {
		t.Fatalf("re-pin: %v %v", changes, err)
	}
	libraryWrite(t, root, "cicada.sum", string(data))
	score, ds := upgraded.Parse()
	if score == nil || len(ds) != 0 {
		t.Fatalf("re-pinned std: %+v", ds)
	}
	if p, ds := FromScore(score); p == nil || len(ds) != 0 {
		t.Fatalf("re-pinned compile: %+v", ds)
	}
}

func TestStdFixturesCoverEveryExport(t *testing.T) {
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	for _, name := range []string{"synth", "drums", "fx", "presets"} {
		sources, err := ReadSources(filepath.Join("..", "examples", "std-"+name, "main.cicada"), nil)
		if err != nil {
			t.Fatal(err)
		}
		score, ds := sources.Parse()
		if score == nil || len(ds) != 0 {
			t.Fatal(ds)
		}
		used := map[string]bool{}
		for _, track := range score.Tracks {
			used[track.Kind] = true
			for _, param := range track.Params {
				if param.Name == "insert" {
					used[param.Value] = true
				}
				if param.Name == "send" {
					used[param.Target] = true
				}
			}
		}
		for _, bus := range score.Buses {
			for _, param := range bus.Params {
				if param.Name == "insert" {
					used[param.Value] = true
				}
			}
		}
		lib := sources.Libraries["std/"+name]
		for export := range notation.DeclarationNames(lib.Files) {
			if !strings.HasPrefix(export, "_") && !used["std."+name+"."+export] {
				t.Errorf("std/%s export %s has no render fixture", name, export)
			}
		}
	}
}

func TestStdEffectsCompileIndependently(t *testing.T) {
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	for _, test := range []struct{ effect, routing string }{
		{"drive", "insert = fx.drive"},
		{"delay", "send fx.delay = 0.3"},
		{"reverb", "send fx.reverb = 0.3"},
		{"", ""},
	} {
		t.Run(test.effect, func(t *testing.T) {
			root := t.TempDir()
			libraryWrite(t, root, "cicada.mod", "project effects\ncicada 2\nentry \"main.cicada\"\n")
			libraryWrite(t, root, "main.cicada", "import \"std/fx\"\ntrack bass acid { "+test.routing+" }\npattern riff { 1 . 5 . }\nscene main { bass = riff }\nsong { main }\n")
			sources := pinLibraryFixture(t, root)
			score, ds := sources.Parse()
			if score == nil || len(ds) != 0 {
				t.Fatal(ds)
			}
			compiled, ds := FromScore(score)
			if compiled == nil || len(ds) != 0 {
				t.Fatal(ds)
			}
			want := 1
			if test.effect == "" {
				want = 0
			}
			if len(compiled.Effects) != want {
				t.Fatalf("materialized effects: %+v", compiled.Effects)
			}
			if want == 1 && compiled.Effects[0].ID != "std.fx."+test.effect {
				t.Fatal(compiled.Effects)
			}
			if len(score.Effects) != 4 {
				t.Fatal("compilation changed imported definitions")
			}
		})
	}
}

func TestImportedEffectPresetKeepsTemplateUntilResolution(t *testing.T) {
	root, _ := libraryFixture(t)
	libraryWrite(t, root, "lib/demo/tone/tone.cicada", libraryVoice+"fx _template delay { feedback=0.2 }\npreset echo { instrument=_template feedback=0.3 }\n")
	libraryWrite(t, root, "main.cicada", "import \"demo/tone\"\nfx echo tone.echo {}\ntrack lead acid { send echo=0.3 }\npattern melody { 1 . 5 . }\nscene main { lead=melody }\nsong { main }\n")
	sources := pinLibraryFixture(t, root)
	score, ds := sources.Parse()
	if score == nil || hasErrors(ds) {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if p == nil || hasErrors(ds) {
		t.Fatal(ds)
	}
	if len(p.Effects) != 1 || p.Effects[0].ID != "echo" || p.Effects[0].Kind != "delay" || *p.Effects[0].Params["feedback"].Number != .3 {
		t.Fatalf("lost preset template: %+v", p.Effects)
	}
}
