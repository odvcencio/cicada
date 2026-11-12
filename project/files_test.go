package project

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

// exampleFiles reads every regular file under the repository's examples
// folder into a map keyed by absolute path, and lists its scores.
func exampleFiles(t *testing.T) (map[string][]byte, []string) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "examples"))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	var scores []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = data
		if strings.HasSuffix(path, ".cicada") {
			scores = append(scores, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files, scores
}

// TestMemoryFilesLoadExamplesAsDiskDoes loads every example score once from
// disk and once from a map of the same files. The source files, their order,
// the score revision, the diagnostics and any loading error must be equal.
func TestMemoryFilesLoadExamplesAsDiskDoes(t *testing.T) {
	user := t.TempDir()
	t.Setenv("CICADA_LIBRARY", user)
	files, scores := exampleFiles(t)
	memory := MemoryFiles{Files: files, UserLibraries: user}
	loaded := 0
	for _, score := range scores {
		name, _ := filepath.Rel(filepath.Join("..", "examples"), score)
		disk, diskErr := ReadSources(score, nil)
		held, heldErr := ReadSourcesIn(memory, score, nil)
		if (diskErr == nil) != (heldErr == nil) || diskErr != nil && diskErr.Error() != heldErr.Error() {
			t.Errorf("%s: disk error %v, memory error %v", name, diskErr, heldErr)
			continue
		}
		if diskErr != nil {
			continue
		}
		loaded++
		if !reflect.DeepEqual(disk.Files, held.Files) {
			t.Errorf("%s: memory sources differ from disk", name)
		}
		if disk.ScoreRevision() != held.ScoreRevision() {
			t.Errorf("%s: score revision %s from disk, %s from memory", name, disk.ScoreRevision(), held.ScoreRevision())
		}
		_, diskDiagnostics := disk.Parse()
		_, heldDiagnostics := held.Parse()
		if !reflect.DeepEqual(diskDiagnostics, heldDiagnostics) {
			t.Errorf("%s: diagnostics differ\ndisk:   %v\nmemory: %v", name, diskDiagnostics, heldDiagnostics)
		}
		diskScore, diskDiagnostics, err := LoadScore(score, nil)
		heldScore, heldDiagnostics, heldErr := LoadScoreIn(memory, score, nil)
		if err != nil || heldErr != nil || !reflect.DeepEqual(diskDiagnostics, heldDiagnostics) || (diskScore == nil) != (heldScore == nil) {
			t.Errorf("%s: LoadScoreIn differs from LoadScore: %v, %v", name, err, heldErr)
		}
	}
	// The examples include manifest projects, project libraries and std
	// imports; most of them must load for the comparison to mean anything.
	t.Logf("%d of %d example scores loaded from both", loaded, len(scores))
	if loaded < len(scores)/2 {
		t.Fatalf("only %d of %d example scores loaded", loaded, len(scores))
	}
}

// An override cannot add a project source that the files do not hold, as an
// override cannot turn an escaping symlink into a source on disk.
func TestMemoryFilesOverrideNeedsTheListedFile(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "song")
	entry := filepath.Join(root, "main.cicada")
	part := filepath.Join(root, "part.cicada")
	files := map[string][]byte{
		filepath.Join(root, "cicada.mod"): []byte("project song\ncicada 2\nentry \"main.cicada\"\nsource \"part.cicada\"\n"),
		entry:                             []byte("cicada 2\ntrack bass acid {}\npattern riff { a1 . }\nscene main { bass = riff }\nsong { main }\n"),
	}
	_, err := ReadSourcesIn(MemoryFiles{Files: files}, entry, map[string][]byte{part: []byte("// part\n")})
	var source *SourceError
	if !errors.As(err, &source) || source.Diagnostic.Code != "CICADA-SOURCE-MISSING" || source.Diagnostic.Position.File != filepath.Join(root, "cicada.mod") {
		t.Fatalf("override of a missing source: %v", err)
	}
	files[part] = []byte("// part\n")
	sources, err := ReadSourcesIn(MemoryFiles{Files: files}, entry, map[string][]byte{part: []byte("// edited\n")})
	if err != nil || len(sources.Files) != 2 || string(sources.Files[1].Source) != "// edited\n" {
		t.Fatalf("override of a held source: %+v, %v", sources, err)
	}
}

// A name that leaves its root is refused as os.Root refuses it, so the error is
// a path error and not a missing file.
func TestMemoryFilesKeepReadsInsideTheRoot(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "song")
	memory := MemoryFiles{Files: map[string][]byte{
		filepath.Join(root, "lib", "a.cicada"): []byte("a"),
		filepath.Join(root, "b.cicada"):        []byte("b"),
	}}
	lib, err := memory.OpenRoot(filepath.Join(root, "lib"))
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	if data, err := lib.ReadFile("a.cicada"); err != nil || string(data) != "a" {
		t.Fatalf("inside: %q, %v", data, err)
	}
	if _, err := lib.ReadFile(filepath.Join("..", "b.cicada")); err == nil || os.IsNotExist(err) {
		t.Fatalf("escaping name: %v", err)
	}
	if _, err := lib.ReadFile("missing.cicada"); !os.IsNotExist(err) {
		t.Fatalf("missing name: %v", err)
	}
	if _, err := memory.OpenRoot(filepath.Join(root, "nowhere")); !os.IsNotExist(err) {
		t.Fatalf("missing folder: %v", err)
	}
	if _, err := fs.Stat(lib.FS(), "a.cicada"); err != nil {
		t.Fatalf("stat through FS: %v", err)
	}
}

// The in-memory path trusts the asset declarations; only the host hashes the
// audio files (VerifyAssets).
func TestMemoryFilesTrustDeclaredAssets(t *testing.T) {
	root := t.TempDir()
	entry := writeSource(t, root, "main.cicada", "cicada 2\nasset hit \"audio/hit.wav\" {\n  sha256 = \""+strings.Repeat("a", 64)+"\"\n  format = wav\n  frames = 4800\n  rate = 48000Hz\n  channels = 1\n}\ntrack bass acid {}\npattern riff { a1 . }\nscene main { bass = riff }\nsong { main }\n")
	disk, err := ReadSources(entry, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ds := disk.Parse(); !hasDiagnostic(ds, "CICADA-ASSET-MISSING") {
		t.Fatalf("disk parse does not check the asset: %v", ds)
	}
	data, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	held, err := ReadSourcesIn(MemoryFiles{Files: map[string][]byte{entry: data}}, entry, nil)
	if err != nil {
		t.Fatal(err)
	}
	score, ds := held.Parse()
	if score == nil || hasDiagnostic(ds, "CICADA-ASSET-MISSING") || len(score.Assets) != 1 || score.Assets[0].Root != root {
		t.Fatalf("memory parse: %v", ds)
	}
}

func hasDiagnostic(diagnostics []notation.Diagnostic, code string) bool {
	for _, d := range diagnostics {
		if d.Code == code {
			return true
		}
	}
	return false
}
