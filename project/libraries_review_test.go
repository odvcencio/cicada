package project

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestImportedEditionOneEffectShorthand(t *testing.T) {
	root, _ := libraryFixture(t)
	libraryWrite(t, root, "cicada.mod", "project score\ncicada 1\nentry \"main.cicada\"\n")
	libraryWrite(t, root, "lib/demo/tone/cicada.mod", "library demo/tone\ncicada 1\nsource \"tone.cicada\"\nlicense \"MIT\"\nauthor \"Cicada contributors\"\n")
	libraryWrite(t, root, "lib/demo/tone/tone.cicada", libraryVoice+"fx delay { feedback=0.2 }\n")
	libraryWrite(t, root, "main.cicada", strings.Replace(libraryScore, "tone.glass {}", "tone.glass { send tone.delay=0.4 }", 1))
	pinLibraryFixture(t, root)
	score, ds, err := LoadScore(filepath.Join(root, "main.cicada"), nil)
	if err != nil || score == nil || hasErrors(ds) {
		t.Fatalf("imported shorthand: %v %+v", err, ds)
	}
	if score.Effects[0].Name != "demo.tone.delay" || score.Effects[0].Kind != "delay" {
		t.Fatalf("effect: %+v", score.Effects[0])
	}
}

func TestLibrarySceneSettingsCannotBypassImportScope(t *testing.T) {
	root, _ := libraryFixture(t)
	libraryWrite(t, root, "lib/demo/tone/tone.cicada", libraryVoice+"fx _echo delay { feedback=0.2 }\n")
	libraryWrite(t, root, "main.cicada", strings.Replace(libraryScore, "lead = tone.melody", "lead = tone.melody demo.tone._echo.feedback=0.3", 1))
	pinLibraryFixture(t, root)
	requireLibraryDiagnostic(t, root, "CICADA-LIB-REFERENCE")
}

func TestLibraryReferencesCannotExposeTransitiveDeclarations(t *testing.T) {
	for _, name := range []string{"_private", "public"} {
		t.Run(name, func(t *testing.T) {
			root, _ := libraryFixture(t)
			installLibrary(t, filepath.Join(root, "lib"), "demo/tone/phrases", "pattern "+name+" { 1 . }\n")
			libraryWrite(t, root, "lib/demo/tone/tone.cicada", "import \"demo/tone/phrases\"\n"+libraryVoice)
			libraryWrite(t, root, "main.cicada", strings.Replace(libraryScore, "tone.melody", "tone.phrases."+name, 1))
			pinLibraryFixture(t, root)
			requireLibraryDiagnostic(t, root, "CICADA-LIB-REFERENCE")
		})
	}
}

func TestCachedLibraryEditionValidatedOnEveryImportEdge(t *testing.T) {
	for _, imports := range []string{"import \"demo/new\"\nimport \"demo/old\"\n", "import \"demo/old\"\nimport \"demo/new\"\n"} {
		t.Run(strings.ReplaceAll(imports, "\n", " "), func(t *testing.T) {
			root, _ := libraryFixture(t)
			installLibrary(t, filepath.Join(root, "lib"), "demo/new", libraryVoice)
			installLibrary(t, filepath.Join(root, "lib"), "demo/old", "import \"demo/new\"\n")
			libraryWrite(t, root, "lib/demo/old/cicada.mod", "library demo/old\ncicada 1\nsource \"tone.cicada\"\nlicense \"MIT\"\nauthor \"Cicada contributors\"\n")
			libraryWrite(t, root, "main.cicada", imports+"track lead new.glass {}\nscene verse { lead=new.melody }\nsong { verse }\n")
			requireLibraryDiagnostic(t, root, "CICADA-VERSION")
		})
	}
}

func TestLibraryHashStreamsAudioFiles(t *testing.T) {
	root, _ := libraryFixture(t)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if _, err := ReadSources(filepath.Join(root, "main.cicada"), nil); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	sourceAllocations := after.TotalAlloc - before.TotalAlloc
	for _, name := range []string{"first.bin", "second.bin"} {
		filename := filepath.Join(root, "lib/demo/tone/audio", name)
		if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(filename)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Truncate(16 << 20); err != nil {
			file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	runtime.ReadMemStats(&before)
	sources, err := ReadSources(filepath.Join(root, "main.cicada"), nil)
	if err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if got := sources.Libraries["demo/tone"].SHA256; got != "93c14272a8c2908ad29bbcd6b2a4fa9747231b8b2fed0853ea9c76c8186e32c0" {
		t.Fatalf("library hash framing changed: %s", got)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated >= sourceAllocations+16<<20 {
		t.Fatalf("hashing retained audio contents: allocated %d bytes", allocated)
	}
}
