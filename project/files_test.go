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

// exampleFiles reads every regular file under the given folders into a map
// keyed by absolute path, and lists their scores.
func exampleFiles(t *testing.T, folders ...string) (map[string][]byte, []string) {
	t.Helper()
	files := map[string][]byte{}
	var scores []string
	for _, folder := range folders {
		root, err := filepath.Abs(folder)
		if err != nil {
			t.Fatal(err)
		}
		if err := walkFiles(root, files, &scores); err != nil {
			t.Fatal(err)
		}
	}
	return files, scores
}

func walkFiles(root string, files map[string][]byte, scores *[]string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = data
		if strings.HasSuffix(path, ".cicada") {
			*scores = append(*scores, path)
		}
		return nil
	})
}

// TestMemoryFilesLoadExamplesAsDiskDoes loads every example score, edition 1
// and 2, once from disk and once from a map of the same files. Every score
// loads from both, and the source files, their order, the score revision and
// the diagnostics are equal.
func TestMemoryFilesLoadExamplesAsDiskDoes(t *testing.T) {
	user := t.TempDir()
	t.Setenv("CICADA_LIBRARY", user)
	files, scores := exampleFiles(t, filepath.Join("..", "examples"), filepath.Join("..", "testdata", "edition1", "examples"))
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
		heldScore, heldDiagnostics, verified, heldErr := LoadScoreIn(memory, score, nil)
		if err != nil || heldErr != nil || verified || !reflect.DeepEqual(diskDiagnostics, heldDiagnostics) || (diskScore == nil) != (heldScore == nil) {
			t.Errorf("%s: LoadScoreIn differs from LoadScore: %v, %v", name, err, heldErr)
		}
	}
	t.Logf("%d of %d example scores loaded from both", loaded, len(scores))
	if loaded != len(scores) || len(scores) < 200 {
		t.Fatalf("%d of %d example scores loaded", loaded, len(scores))
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
	if _, err := lib.ReadFile(filepath.Join("..", "b.cicada")); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("escaping name: %v", err)
	}
	if _, err := lib.ReadFile("missing.cicada"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing name: %v", err)
	}
	if _, err := memory.OpenRoot(filepath.Join(root, "nowhere")); !errors.Is(err, fs.ErrNotExist) {
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
	// The result says which: the disk checked the audio, memory did not.
	if !disk.VerifiesAssets() || held.VerifiesAssets() {
		t.Fatalf("VerifiesAssets: disk %v, memory %v", disk.VerifiesAssets(), held.VerifiesAssets())
	}
}

// Projects that fail to load fail the same way from memory as from disk: the
// same diagnostic code, position and message text.
func TestMemoryFilesFailAsDiskFails(t *testing.T) {
	user := t.TempDir()
	t.Setenv("CICADA_LIBRARY", user)
	score := "cicada 2\ntrack bass acid {}\npattern riff { a1 . }\nscene main { bass = riff }\nsong { main }\n"
	for name, project := range map[string]map[string]string{
		"listed source missing": {"cicada.mod": "project broken\ncicada 2\nentry \"main.cicada\"\nsource \"gone.cicada\"\n", "main.cicada": score},
		"score not listed":      {"cicada.mod": "project broken\ncicada 2\nentry \"other.cicada\"\n", "main.cicada": score, "other.cicada": score},
		"library missing":       {"main.cicada": "import \"demo/absent\"\n" + score},
		"bad manifest":          {"cicada.mod": "project broken\ncicada nine\n", "main.cicada": score},
		"unpinned library":      {"main.cicada": "import \"demo/tone\"\n" + score, "lib/demo/tone/cicada.mod": "library demo/tone\ncicada 2\nsource \"tone.cicada\"\nlicense \"MIT\"\nauthor \"A\"\nengine 2\ncapabilities 0\n", "lib/demo/tone/tone.cicada": "phrase hook { 1 . }\n"},
	} {
		root := t.TempDir()
		files := map[string][]byte{}
		for file, text := range project {
			files[writeSource(t, root, file, text)] = []byte(text)
		}
		entry := filepath.Join(root, "main.cicada")
		disk, diskErr := ReadSources(entry, nil)
		held, heldErr := ReadSourcesIn(MemoryFiles{Files: files, UserLibraries: user}, entry, nil)
		if (diskErr == nil) != (heldErr == nil) || diskErr != nil && diskErr.Error() != heldErr.Error() {
			t.Errorf("%s: disk %v\nmemory %v", name, diskErr, heldErr)
			continue
		}
		if diskErr != nil {
			continue
		}
		_, diskDiagnostics := disk.Parse()
		_, heldDiagnostics := held.Parse()
		if len(diskDiagnostics) == 0 || !reflect.DeepEqual(diskDiagnostics, heldDiagnostics) {
			t.Errorf("%s: diagnostics differ or are missing\ndisk:   %v\nmemory: %v", name, diskDiagnostics, heldDiagnostics)
		}
	}
	// A missing score reads the same, with the operating system's words.
	missing := filepath.Join(t.TempDir(), "missing.cicada")
	_, diskErr := ReadSources(missing, nil)
	_, heldErr := ReadSourcesIn(MemoryFiles{}, missing, nil)
	if diskErr == nil || heldErr == nil || diskErr.Error() != heldErr.Error() || !errors.Is(heldErr, fs.ErrNotExist) {
		t.Fatalf("missing score: disk %v, memory %v", diskErr, heldErr)
	}
}

// Memory has no working folder, so a score read from it is named by an
// absolute path.
func TestMemoryFilesRefuseRelativePaths(t *testing.T) {
	_, err := ReadSourcesIn(MemoryFiles{Files: map[string][]byte{"/song/main.cicada": []byte("song { }\n")}}, "main.cicada", nil)
	var source *SourceError
	if !errors.As(err, &source) || source.Diagnostic.Code != "CICADA-SOURCE-PATH" {
		t.Fatalf("relative path: %v", err)
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

// A library's hash covers the files of its audio folder, so a project held in
// memory hashes them from memory to the same pin as on disk.
func TestMemoryFilesHashLibraryAudioAsDiskDoes(t *testing.T) {
	user := t.TempDir()
	t.Setenv("CICADA_LIBRARY", user)
	root := t.TempDir()
	writeSource(t, root, "cicada.mod", "project kit\ncicada 2\nentry \"main.cicada\"\n")
	entry := writeSource(t, root, "main.cicada", "import \"demo/kit\"\ntitle \"Kit\"\ntrack lead kit.glass {}\npattern riff { 1 . }\nscene main { lead = riff }\nsong { main }\n")
	writeSource(t, root, "lib/demo/kit/cicada.mod", "library demo/kit\ncicada 2\nsource \"kit.cicada\"\nlicense \"MIT\"\nauthor \"Cicada contributors\"\nengine 2\ncapabilities 0\n")
	writeSource(t, root, "lib/demo/kit/kit.cicada", "instrument glass {\n  voice mono {\n    out = sine(pitch) * env(gate, 90ms) * velocity\n  }\n}\n")
	writeSource(t, root, "lib/demo/kit/audio/hit.wav", "RIFF not really a wave")
	writeSource(t, root, "lib/demo/kit/audio/deep/tail.wav", "another file")
	files := map[string][]byte{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		files[path], err = os.ReadFile(path)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	disk, err := ReadSources(entry, nil)
	if err != nil {
		t.Fatal(err)
	}
	held, err := ReadSourcesIn(MemoryFiles{Files: files, UserLibraries: user}, entry, nil)
	if err != nil {
		t.Fatal(err)
	}
	diskLibrary, heldLibrary := disk.Libraries["demo/kit"], held.Libraries["demo/kit"]
	if diskLibrary == nil || heldLibrary == nil || diskLibrary.SHA256 != heldLibrary.SHA256 || !reflect.DeepEqual(diskLibrary.Assets, heldLibrary.Assets) || len(heldLibrary.Assets) != 2 {
		t.Fatalf("library from disk %+v, from memory %+v", diskLibrary, heldLibrary)
	}
	// Without its audio the library hashes differently, so its pin fails.
	delete(files, filepath.Join(root, "lib", "demo", "kit", "audio", "hit.wav"))
	partial, err := ReadSourcesIn(MemoryFiles{Files: files, UserLibraries: user}, entry, nil)
	if err != nil || partial.Libraries["demo/kit"].SHA256 == diskLibrary.SHA256 {
		t.Fatalf("library without its audio: %v", err)
	}
}

// A file missing from memory reads as one missing from the service's disk, on
// every target: js/wasm's own text for ENOENT starts with a capital letter.
func TestMemoryFilesMissingFileText(t *testing.T) {
	missing := filepath.Join(string(filepath.Separator), "song", "missing.cicada")
	_, err := MemoryFiles{}.ReadFile(missing)
	if err == nil || err.Error() != "open "+missing+": no such file or directory" || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}
	root, err := MemoryFiles{Files: map[string][]byte{filepath.Join(string(filepath.Separator), "song", "a.cicada"): nil}}.OpenRoot(filepath.Dir(missing))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := root.ReadFile("missing.cicada"); err == nil || err.Error() != "openat missing.cicada: no such file or directory" || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file in a root: %v", err)
	}
}
