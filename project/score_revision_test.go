package project

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestScoreRevisionTracksDefinitionsWithoutChangingAssetValidation(t *testing.T) {
	root, _ := libraryFixture(t)
	libraryWrite(t, root, "lib/demo/tone/audio/payload.bin", "original sample payload")
	loaded := pinLibraryFixture(t, root)
	path := filepath.Join(root, "main.cicada")
	original := loaded.ScoreRevision()
	read := func(t *testing.T) string {
		t.Helper()
		revision, err := ReadScoreRevision(path)
		if err != nil {
			t.Fatal(err)
		}
		return revision
	}
	if read(t) != original {
		t.Fatal("metadata comparison differs from the sources captured for rendering")
	}
	for _, name := range []string{"main.cicada", "cicada.mod", "lib/demo/tone/tone.cicada", "lib/demo/tone/cicada.mod"} {
		t.Run(name, func(t *testing.T) {
			full := filepath.Join(root, filepath.FromSlash(name))
			before, err := os.ReadFile(full)
			if err != nil {
				t.Fatal(err)
			}
			changed := append(append([]byte(nil), before...), []byte("\n// saved definition edit\n")...)
			if name == "cicada.mod" {
				changed = []byte(strings.Replace(string(before), "project score", "project renamed", 1))
			}
			if name == "lib/demo/tone/cicada.mod" {
				changed = []byte(strings.Replace(string(before), "MIT", "BSD-2-Clause", 1))
			}
			if err := os.WriteFile(full, changed, 0600); err != nil {
				t.Fatal(err)
			}
			if read(t) == original {
				t.Fatal("saved definition edit did not change the score revision")
			}
			if err := os.WriteFile(full, before, 0600); err != nil {
				t.Fatal(err)
			}
			if read(t) != original {
				t.Fatal("restoring the file did not restore the captured version")
			}
		})
	}
	libraryWrite(t, root, "lib/demo/tone/audio/payload.bin", "changed sample payload")
	if read(t) != original {
		t.Fatal("score-only comparison included sample payloads")
	}
	// The render loader must still detect asset tampering against the pin.
	requireLibraryDiagnostic(t, root, "CICADA-LIB-HASH")
	if err := os.Remove(filepath.Join(root, "lib/demo/tone/tone.cicada")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadScoreRevision(path); err == nil {
		t.Fatal("missing library source was treated as a comparable score")
	}
}

type scoreRevisionFS struct {
	fs.FS
	audioReads int
}

func (f *scoreRevisionFS) Open(name string) (fs.File, error) {
	if name == "audio" || strings.HasPrefix(name, "audio/") {
		f.audioReads++
		return nil, fs.ErrPermission
	}
	return f.FS.Open(name)
}

func TestRevisionOnlyLibraryReadDoesNotOpenAudio(t *testing.T) {
	files := &scoreRevisionFS{FS: fstest.MapFS{
		"cicada.mod":  {Data: []byte("library demo/tone\ncicada 2\nsource \"tone.cicada\"\nlicense \"MIT\"\nauthor \"Cicada contributors\"\n")},
		"tone.cicada": {Data: []byte(libraryVoice)},
	}}
	if _, err := readLibraryMode("demo/tone", "project", "lib/demo/tone", files, nil, true); err != nil || files.audioReads != 0 {
		t.Fatalf("score-only comparison accessed audio: %d reads, %v", files.audioReads, err)
	}
	if _, err := readLibrary("demo/tone", "project", "lib/demo/tone", files, nil); !errors.Is(err, fs.ErrPermission) || files.audioReads == 0 {
		t.Fatalf("full library loading skipped asset validation: %d reads, %v", files.audioReads, err)
	}
}

func TestScoreRevisionRetainsLibraryAmbiguityChecks(t *testing.T) {
	root, user := libraryFixture(t)
	loaded := pinLibraryFixture(t, root)
	pins, err := os.ReadFile(filepath.Join(root, "cicada.sum"))
	if err != nil {
		t.Fatal(err)
	}
	libraryWrite(t, root, "cicada.sum", strings.Replace(string(pins), " project ", " user ", 1))
	installLibrary(t, user, "demo/tone", libraryVoice)
	path := filepath.Join(root, "main.cicada")
	if _, err := ReadScoreRevision(path); err != nil {
		t.Fatal("identical project and user copies no longer resolve", err)
	}
	if loaded.Libraries["demo/tone"].SHA256 == "" {
		t.Fatal("normal loading omitted the asset digest")
	}
	libraryWrite(t, user, "demo/tone/audio/payload.bin", "a different library payload")
	if _, err := ReadScoreRevision(path); err == nil {
		t.Fatal("source equality bypassed differing library asset pins")
	}
}
