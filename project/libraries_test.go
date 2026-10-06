package project

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/notation"
)

const libraryVoice = `instrument glass { voice mono { out = sine(pitch) * env(gate, 90ms) * velocity } }
phrase _hook { 1 . 5 . }
pattern melody { use _hook }
`
const libraryScore = "import \"demo/tone\"\ntrack lead tone.glass {}\nscene verse { lead = tone.melody }\nsong { verse }\n"

func libraryWrite(t testing.TB, root, name, text string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}
func libraryFixture(t *testing.T) (string, string) {
	t.Helper()
	root, user := t.TempDir(), t.TempDir()
	t.Setenv("CICADA_LIBRARY", user)
	libraryWrite(t, root, "cicada.mod", "project score\ncicada 2\nentry \"main.cicada\"\n")
	libraryWrite(t, root, "main.cicada", libraryScore)
	installLibrary(t, filepath.Join(root, "lib"), "demo/tone", libraryVoice)
	return root, user
}
func installLibrary(t testing.TB, base, name, source string) {
	t.Helper()
	libraryWrite(t, base, name+"/cicada.mod", fmt.Sprintf("library %s\ncicada 2\nsource \"tone.cicada\"\nlicense \"MIT\"\nauthor \"Cicada contributors\"\nengine 2\ncapabilities 0\n", name))
	libraryWrite(t, base, name+"/tone.cicada", source)
}
func pinLibraryFixture(t *testing.T, root string) *Sources {
	t.Helper()
	sources, err := ReadSources(filepath.Join(root, "main.cicada"), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _, err := sources.UpdateLibraries("")
	if err != nil {
		t.Fatal(err)
	}
	libraryWrite(t, root, "cicada.sum", string(data))
	return sources
}
func requireLibraryDiagnostic(t *testing.T, root, code string) notation.Diagnostic {
	t.Helper()
	_, ds, err := LoadScore(filepath.Join(root, "main.cicada"), nil)
	if err != nil {
		var d *SourceError
		if errors.As(err, &d) {
			ds = append(ds, d.Diagnostic)
		} else {
			t.Fatal(err)
		}
	}
	for _, d := range ds {
		if d.Code == code {
			return d
		}
	}
	t.Fatalf("missing %s: %+v err=%v", code, ds, err)
	return notation.Diagnostic{}
}

func TestLibraryPinningAndContentChanges(t *testing.T) {
	root, _ := libraryFixture(t)
	d := requireLibraryDiagnostic(t, root, "CICADA-LIB-HASH")
	if d.Position.Line != 1 || d.Position.Column != 1 || d.Position.File != filepath.Join(root, "main.cicada") {
		t.Fatalf("import position: %+v", d)
	}
	sources := pinLibraryFixture(t, root)
	score, ds := sources.Parse()
	if score == nil || len(ds) != 0 {
		t.Fatalf("pinned parse: %+v", ds)
	}
	if p, ds := FromScore(score); p == nil || len(ds) != 0 {
		t.Fatalf("compile: %+v", ds)
	}
	if score.Tracks[0].Kind != "demo.tone.glass" || score.Patterns[0].Name != "demo.tone.melody" || score.Origins[score.Tracks[0].Kind].Library != "demo/tone" {
		t.Fatalf("names/provenance: %+v", score)
	}
	original := sources.Libraries["demo/tone"].SHA256
	for _, change := range []struct{ name, text string }{{"tone.cicada", libraryVoice + "// intended source edit\n"}, {"cicada.mod", "library demo/tone\ncicada 2\nsource \"tone.cicada\"\nlicense \"BSD-2-Clause\"\nauthor \"Cicada contributors\"\n"}, {"audio/unused.bin", "asset bytes"}} {
		t.Run(change.name, func(t *testing.T) {
			libraryWrite(t, root, "lib/demo/tone/"+change.name, change.text)
			d := requireLibraryDiagnostic(t, root, "CICADA-LIB-HASH")
			if d.Position.Line != 1 || !strings.Contains(d.Message, "changed:") {
				t.Fatalf("hash mismatch: %+v", d)
			}
			current, err := ReadSources(filepath.Join(root, "main.cicada"), nil)
			if err != nil {
				t.Fatal(err)
			}
			data, changes, err := current.UpdateLibraries("demo/tone")
			if err != nil || len(changes) != 1 || !strings.Contains(changes[0], " -> project sha256:") {
				t.Fatalf("update: %s %v %v", data, changes, err)
			}
			if current.Libraries["demo/tone"].SHA256 == original {
				t.Fatal("content change did not change hash")
			}
			libraryWrite(t, root, "cicada.sum", string(data))
		})
	}
	data, changes, err := pinLibraryFixture(t, root).UpdateLibraries("")
	before, _ := os.ReadFile(filepath.Join(root, "cicada.sum"))
	if err != nil || len(changes) != 0 || !bytes.Equal(data, before) {
		t.Fatalf("idempotent update: %v %v", changes, err)
	}
}

func TestLibraryUserResolutionAndRelocation(t *testing.T) {
	root, user := libraryFixture(t)
	if err := os.Rename(filepath.Join(root, "lib/demo"), filepath.Join(user, "demo")); err != nil {
		t.Fatal(err)
	}
	sources := pinLibraryFixture(t, root)
	if sources.Libraries["demo/tone"].Kind != "user" {
		t.Fatal("user library did not resolve")
	}
	if _, ds := sources.Parse(); len(ds) != 0 {
		t.Fatal(ds)
	}
	// Content hashes do not include absolute roots, but moving between kinds
	// requires a deliberate re-pin even when every byte is unchanged.
	if err := os.Rename(filepath.Join(user, "demo"), filepath.Join(root, "lib/demo")); err != nil {
		t.Fatal(err)
	}
	d := requireLibraryDiagnostic(t, root, "CICADA-LIB-HASH")
	if !strings.Contains(d.Message, "user sha256:") || !strings.Contains(d.Message, "-> project sha256:") {
		t.Fatal(d)
	}
	current, err := ReadSources(filepath.Join(root, "main.cicada"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if sources.Libraries["demo/tone"].SHA256 != current.Libraries["demo/tone"].SHA256 {
		t.Fatal("location affected content hash")
	}
}

func TestLibraryShadowCyclesAndPrivateNames(t *testing.T) {
	t.Run("shadow", func(t *testing.T) {
		root, user := libraryFixture(t)
		installLibrary(t, user, "demo/tone", libraryVoice)
		requireLibraryDiagnostic(t, root, "CICADA-LIB-SHADOW")
	})
	t.Run("alias collision", func(t *testing.T) {
		root, _ := libraryFixture(t)
		installLibrary(t, filepath.Join(root, "lib"), "other/tone", libraryVoice)
		libraryWrite(t, root, "main.cicada", "import \"other/tone\"\n"+libraryScore)
		requireLibraryDiagnostic(t, root, "CICADA-LIB-SHADOW")
	})
	t.Run("cycle", func(t *testing.T) {
		root, _ := libraryFixture(t)
		installLibrary(t, filepath.Join(root, "lib"), "demo/other", "import \"demo/tone\"\n"+libraryVoice)
		libraryWrite(t, root, "lib/demo/tone/tone.cicada", "import \"demo/other\"\n"+libraryVoice)
		requireLibraryDiagnostic(t, root, "CICADA-LIB-CYCLE")
	})
	for _, ref := range []string{"pattern line { use tone._hook }", "track secret tone._hidden {}", "track secret demo.tone.glass {}"} {
		t.Run(ref, func(t *testing.T) {
			root, _ := libraryFixture(t)
			libraryWrite(t, root, "main.cicada", libraryScore+ref+"\n")
			pinLibraryFixture(t, root)
			code := "CICADA-LIB-PRIVATE"
			if strings.Contains(ref, "demo.tone") {
				code = "CICADA-LIB-REFERENCE"
			}
			requireLibraryDiagnostic(t, root, code)
		})
	}
}

func TestLibraryTransitiveImportsAndScope(t *testing.T) {
	root, _ := libraryFixture(t)
	installLibrary(t, filepath.Join(root, "lib"), "demo/phrases", "phrase _private { 1 . }\nphrase hook { 5 . 1 . }\n")
	libraryWrite(t, root, "lib/demo/tone/tone.cicada", "import \"demo/phrases\"\ninstrument glass { voice mono { out = sine(pitch) } }\npattern melody { use phrases.hook }\n")
	sources := pinLibraryFixture(t, root)
	score, ds := sources.Parse()
	if score == nil || len(ds) != 0 {
		t.Fatalf("transitive parse: %+v", ds)
	}
	if len(sources.Libraries) != 2 || len(score.Patterns[0].Steps) != 4 {
		t.Fatal("dependency did not expand")
	}
	sum, _ := os.ReadFile(filepath.Join(root, "cicada.sum"))
	if bytes.Count(sum, []byte("sha256:")) != 2 {
		t.Fatalf("sum: %s", sum)
	}
	libraryWrite(t, root, "main.cicada", libraryScore+"pattern hidden { use phrases.hook }\n")
	requireLibraryDiagnostic(t, root, "CICADA-LIB-REFERENCE")
	libraryWrite(t, root, "main.cicada", libraryScore)
	libraryWrite(t, root, "lib/demo/phrases/tone.cicada", "phrase hook { 3 . }\n")
	d := requireLibraryDiagnostic(t, root, "CICADA-LIB-HASH")
	if !strings.Contains(d.Position.File, "tone.cicada") || !strings.Contains(d.Message, "demo/phrases") {
		t.Fatalf("dependency import position: %+v", d)
	}
}

func TestLibraryRequirementsAndDeclarations(t *testing.T) {
	for _, declaration := range []string{"track illegal acid {}", "scene illegal {}", "song { illegal }", "tempo 120", "clip illegal wave {}"} {
		t.Run(declaration, func(t *testing.T) {
			root, _ := libraryFixture(t)
			libraryWrite(t, root, "lib/demo/tone/tone.cicada", declaration)
			requireLibraryDiagnostic(t, root, "CICADA-LIB-DECL")
		})
	}
	for _, requirements := range []string{"engine 3\ncapabilities 0\n", "engine 2\ncapabilities 1\n"} {
		t.Run(requirements, func(t *testing.T) {
			root, _ := libraryFixture(t)
			libraryWrite(t, root, "lib/demo/tone/cicada.mod", "library demo/tone\ncicada 2\nsource \"tone.cicada\"\nlicense \"MIT\"\nauthor \"Cicada contributors\"\n"+requirements)
			requireLibraryDiagnostic(t, root, "CICADA-LIB-CAPABILITY")
		})
	}
	t.Run("source edition", func(t *testing.T) {
		root, _ := libraryFixture(t)
		libraryWrite(t, root, "cicada.mod", "project score\ncicada 1\nentry \"main.cicada\"\n")
		requireLibraryDiagnostic(t, root, "CICADA-VERSION")
	})
	t.Run("traversal", func(t *testing.T) {
		root, _ := libraryFixture(t)
		libraryWrite(t, root, "main.cicada", "import \"../tone\"\n")
		requireLibraryDiagnostic(t, root, "CICADA-LIB-PATH")
	})
	t.Run("symlink", func(t *testing.T) {
		root, _ := libraryFixture(t)
		outside := t.TempDir()
		installLibrary(t, outside, "escape", libraryVoice)
		if err := os.Symlink(filepath.Join(outside, "escape"), filepath.Join(root, "lib", "escape")); err != nil {
			t.Fatal(err)
		}
		libraryWrite(t, root, "main.cicada", "import \"escape\"\n")
		requireLibraryDiagnostic(t, root, "CICADA-LIB-PATH")
	})
}

func TestLibrarySumValidation(t *testing.T) {
	for _, data := range []string{"demo/tone project abc", "demo/tone hidden sha256:" + strings.Repeat("0", 64), "../tone project sha256:" + strings.Repeat("0", 64), "demo/tone user sha256:" + strings.Repeat("A", 64), strings.Repeat("demo/tone project sha256:"+strings.Repeat("0", 64)+"\n", 2)} {
		if _, err := ParseLibrarySum([]byte(data)); err == nil {
			t.Fatalf("accepted invalid sum %q", data)
		}
	}
}

func TestLibraryAssetRootsAndHashes(t *testing.T) {
	root, _ := libraryFixture(t)
	data := testwav.Bytes(48000, 1, 16, 4800, 1)
	libraryWrite(t, root, "lib/demo/tone/audio/tone.wav", string(data))
	source := fmt.Sprintf("asset sample \"audio/tone.wav\" { sha256 = \"%x\" format = wav frames = 4800 rate = 48000Hz channels = 1 }\nsampler hit { asset = sample root = c3 mode = oneshot voices = 8 }\npattern melody { c3 . c3 . }\n", sha256.Sum256(data))
	libraryWrite(t, root, "lib/demo/tone/tone.cicada", source)
	libraryWrite(t, root, "main.cicada", strings.ReplaceAll(libraryScore, "tone.glass", "tone.hit"))
	sources := pinLibraryFixture(t, root)
	score, ds := sources.Parse()
	if score == nil || len(ds) != 0 {
		t.Fatalf("asset parse: %+v", ds)
	}
	p, ds := FromScore(score)
	if p == nil || len(ds) != 0 {
		t.Fatalf("asset compile: %+v", ds)
	}
	if p.Assets[0].Directory(root) != filepath.Join(root, "lib/demo/tone") || p.Samplers[0].Asset != "demo.tone.sample" {
		t.Fatal("library asset root/reference lost")
	}
	// The project has no audio/ file; verification must use the library root.
	libraryWrite(t, root, "lib/demo/tone/audio/tone.wav", string(testwav.Bytes(48000, 1, 16, 4800, 2)))
	requireLibraryDiagnostic(t, root, "CICADA-LIB-HASH")
	libraryWrite(t, root, "lib/demo/tone/tone.cicada", strings.ReplaceAll(source, "audio/tone.wav", "../tone.wav"))
	requireLibraryDiagnostic(t, root, "CICADA-ASSET-PATH")
}

func TestLibraryQualifiedEffectsAndKits(t *testing.T) {
	root, _ := libraryFixture(t)
	source := libraryVoice + "\nfx dirt drive { gain = 3dB mix = 0.5 }\nfx echo delay { time = 1/8 feedback = 0.2 }\nkit percussion { bd = glass ch = builtin.ch }\npattern beat drums { bd: x... ch: x.x. }\n"
	libraryWrite(t, root, "lib/demo/tone/tone.cicada", source)
	libraryWrite(t, root, "main.cicada", "import \"demo/tone\"\ntrack lead tone.glass { insert = tone.dirt send tone.echo = -9dB }\ntrack drums tone.percussion {}\nscene verse { lead = tone.melody drums = tone.beat tone.echo.feedback = 0.3 }\nsong { verse }\n")
	sources := pinLibraryFixture(t, root)
	score, ds := sources.Parse()
	if score == nil || len(ds) != 0 {
		t.Fatalf("effect/kit parse: %+v", ds)
	}
	p, ds := FromScore(score)
	if p == nil || len(ds) != 0 {
		t.Fatalf("effect/kit compile: %+v", ds)
	}
	if _, err := CompileEngine(p, 48000, 128); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveParameterPath(p, "demo.tone.echo.feedback"); err != nil {
		t.Fatal(err)
	}
	// Scope-wide local declarations win over built-in spellings within a library.
	libraryWrite(t, root, "lib/demo/tone/tone.cicada", "instrument music { voice mono { out = sine(pitch) } }\nkit percussion { bd = music }\npattern beat drums { bd: x... }\n")
	libraryWrite(t, root, "main.cicada", "import \"demo/tone\"\ntrack drums tone.percussion {}\nscene verse { drums = tone.beat }\nsong { verse }\n")
	sources = pinLibraryFixture(t, root)
	score, ds = sources.Parse()
	if score == nil || len(ds) != 0 || score.Kits[0].Bindings[0].Target != "demo.tone.music" {
		t.Fatalf("local declaration: %+v %+v", score, ds)
	}
}

func TestLibrarySemanticAndFlatSourceRoundTrip(t *testing.T) {
	root, _ := libraryFixture(t)
	score, ds := pinLibraryFixture(t, root).Parse()
	if score == nil || len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if p == nil || len(ds) != 0 {
		t.Fatal(ds)
	}
	data, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	q, err := DecodeJSON(data)
	if err != nil || !SemanticEqual(p, q) {
		t.Fatalf("semantic round trip: %v", err)
	}
	source, err := ToSource(q)
	if err != nil {
		t.Fatal(err)
	}
	flat, ds := notation.Parse(source)
	if flat == nil || len(ds) != 0 {
		t.Fatalf("flat source: %+v\n%s", ds, source)
	}
	r, ds := FromScore(flat)
	if r == nil || len(ds) != 0 || !SemanticEqual(p, r) {
		t.Fatalf("flat project: %+v", ds)
	}
}

func TestLibraryPathsContinuePastUnreadableSubtrees(t *testing.T) {
	root, user := libraryFixture(t)
	installLibrary(t, filepath.Join(root, "lib"), "a/tone", libraryVoice)
	installLibrary(t, filepath.Join(root, "lib"), "z/tone", libraryVoice)
	installLibrary(t, user, "u/tone", libraryVoice)
	blocked := filepath.Join(root, "lib/a")
	if err := os.Chmod(blocked, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(blocked, 0755) })
	if dir, err := os.Open(blocked); err == nil {
		dir.Close()
		t.Skip("filesystem does not enforce directory permissions")
	}
	names := LibraryPaths(root)
	for _, want := range []string{"z/tone", "u/tone"} {
		found := false
		for _, name := range names {
			found = found || name == want
		}
		if !found {
			t.Fatalf("unreadable subtree hid %s: %v", want, names)
		}
	}
}
