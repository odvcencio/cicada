// Package vendortest shares a pinned user-library fixture across render gates.
package vendortest

import (
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func Setup(t testing.TB) (root, user string, sources *project.Sources) {
	t.Helper()
	root, user = t.TempDir(), t.TempDir()
	t.Setenv("CICADA_LIBRARY", user)
	Write(t, root, "cicada.mod", []byte("project vendor\ncicada 2\nentry \"main.cicada\"\n"))
	Write(t, root, "main.cicada", []byte("import \"demo/tone\"\ntrack lead tone.glass {}\nscene verse { lead = tone.melody }\nsong { verse*2 }\n"))
	Write(t, user, "demo/tone/cicada.mod", []byte("library demo/tone\ncicada 2\nsource \"parts/tone.cicada\"\nlicense \"MIT\"\nauthor \"Library authors\"\n"))
	Write(t, user, "demo/tone/parts/tone.cicada", []byte("import \"demo/phrases\"\nimport \"std/synth\"\ninstrument glass { voice mono { out = sine(pitch) * env(gate,90ms) * velocity } }\npattern melody { use phrases.hook *2 }\n"))
	Write(t, user, "demo/tone/audio/nested/tone.wav", testwav.Bytes(48000, 1, 16, 128, 1))
	Write(t, user, "demo/phrases/cicada.mod", []byte("library demo/phrases\ncicada 2\nsource \"main.cicada\"\nlicense \"MIT\"\nauthor \"Library authors\"\n"))
	Write(t, user, "demo/phrases/main.cicada", []byte("phrase hook { 1 . 3 . 5 . 3 . }\n"))
	var err error
	sources, err = project.ReadSources(filepath.Join(root, "main.cicada"), nil)
	if err != nil {
		t.Fatal(err)
	}
	sum, _, err := sources.UpdateLibraries("")
	if err != nil {
		t.Fatal(err)
	}
	Write(t, root, "cicada.sum", sum)
	return
}

func Write(t testing.TB, root, name string, data []byte) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, data, 0644); err != nil {
		t.Fatal(err)
	}
}

// BeforeAndAfter deletes the user library before loading the vendored score.
func BeforeAndAfter(t testing.TB) (*notation.Score, *notation.Score, *project.Project) {
	t.Helper()
	root, user, sources := Setup(t)
	before, ds := sources.Parse()
	if before == nil || len(ds) != 0 {
		t.Fatalf("user-library score: %+v", ds)
	}
	p, ds := project.FromScore(before)
	if p == nil || len(ds) != 0 {
		t.Fatalf("user-library compile: %+v", ds)
	}
	if _, err := sources.VendorLibraries(root); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(user); err != nil {
		t.Fatal(err)
	}
	after, ds, err := project.LoadScore(filepath.Join(root, "main.cicada"), nil)
	if err != nil || after == nil || len(ds) != 0 {
		t.Fatalf("vendored score: %+v %v", ds, err)
	}
	return before, after, p
}
