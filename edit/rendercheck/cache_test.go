package rendercheck

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// projectCompiler compiles with the whole project, as the hosts do.
type projectCompiler struct{ path string }

func (c projectCompiler) Compile(source []byte, files map[string][]byte) (*edits.Plan, error) {
	overrides := map[string][]byte{c.path: source}
	for name, data := range files {
		overrides[name] = data
	}
	score, ds, err := project.LoadScore(c.path, overrides)
	if err != nil {
		return nil, err
	}
	for _, d := range ds {
		if d.Severity == "error" {
			return nil, fmt.Errorf("%s: %s", d.Code, d.Message)
		}
	}
	p, ds := project.FromScore(score)
	for _, d := range ds {
		if d.Severity == "error" {
			return nil, fmt.Errorf("%s: %s", d.Code, d.Message)
		}
	}
	sources, err := project.ReadSources(c.path, overrides)
	if err != nil {
		return nil, err
	}
	return project.EditPlan(p, source, sources.Files...), nil
}

type countedCheck struct {
	inner edits.RenderCheck
	calls atomic.Int32
}

func (c *countedCheck) Hash(source []byte) (string, error) {
	c.calls.Add(1)
	return c.inner.Hash(source)
}

func writeProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "main.cicada")
}

const peerManifest = "project peers\ncicada 2\nentry \"main.cicada\"\nsource \"patterns.cicada\"\n"
const peerMain = "scene a { drums = beat }\nscene b { drums = fill }\nsong { a*2 b*2 }\n"

func peerPatterns(lane string) string {
	return "track drums drums {}\npattern beat drums {\n  bd: " + lane + "\n}\npattern fill drums {\n  sd: " + lane + "\n}\n"
}

func convert(t *testing.T, path string, check edits.RenderCheck) error {
	t.Helper()
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var env edits.Envelope
	env.Version = edits.EnvelopeVersion
	env.Intents = []edits.Intent{&edits.ConvertSongToArrange{}}
	compiler := projectCompiler{path}
	_, err = edits.Apply(source, env, edits.Options{Compiler: compiler, RenderCheck: check, Path: path, Edition: 2,
		ParseProject: func(src []byte, files map[string][]byte) (*notation.Score, []notation.Diagnostic, error) {
			overrides := map[string][]byte{path: src}
			for name, data := range files {
				overrides[name] = data
			}
			return project.LoadScore(path, overrides)
		}})
	return err
}

// A conversion approved against silent peer patterns must not be reused after
// only the peer file changes: the entry bytes (and its revision) stay the same.
func TestCachedApprovalDoesNotSurvivePeerFileEdit(t *testing.T) {
	path := writeProject(t, map[string]string{"cicada.mod": peerManifest, "main.cicada": peerMain, "patterns.cicada": peerPatterns(".... .... .... ....")})
	counted := &countedCheck{inner: New(path)}
	check := Cached(path, counted)
	if err := convert(t, path, check); err != nil {
		t.Fatalf("silent patterns: %v", err)
	}
	renders := counted.calls.Load()
	if renders != 2 {
		t.Fatalf("first conversion rendered %d times, want 2", renders)
	}
	// Unchanged inputs are served from the cache.
	if err := convert(t, path, check); err != nil || counted.calls.Load() != renders {
		t.Fatalf("unchanged inputs: err=%v renders=%d, want %d", err, counted.calls.Load(), renders)
	}
	// Only the peer file changes: both patterns become audible.
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "patterns.cicada"), []byte(peerPatterns("X... .... X... ....")), 0o600); err != nil {
		t.Fatal(err)
	}
	err := convert(t, path, check)
	if err == nil {
		t.Fatal("conversion allowed from a stale approval")
	}
	if counted.calls.Load() == renders {
		t.Fatal("peer-file edit was served from the cache")
	}
}

func TestIdentityTracksEveryRenderInput(t *testing.T) {
	const asset = "asset wave \"hit.wav\" { sha256=\"" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + "\" format=wav frames=480 rate=48000Hz channels=1 }\n"
	files := map[string]string{"cicada.mod": peerManifest, "main.cicada": peerMain, "patterns.cicada": peerPatterns("X... .... .... ....") + asset, "hit.wav": "one"}
	path := writeProject(t, files)
	dir := filepath.Dir(path)
	entry := []byte(peerMain)
	base, ok := Identity(path, entry, nil)
	if !ok {
		t.Fatal("identity unavailable for a plain project")
	}
	if again, _ := Identity(path, entry, nil); again != base {
		t.Fatal("identity is not stable for unchanged inputs")
	}
	change := func(name, data string, mtime time.Time) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if !mtime.IsZero() {
			if err := os.Chtimes(filepath.Join(dir, name), mtime, mtime); err != nil {
				t.Fatal(err)
			}
		}
	}
	current := base
	expectNew := func(what string) {
		t.Helper()
		got, ok := Identity(path, entry, nil)
		if !ok || got == current {
			t.Fatalf("%s did not change the identity (ok=%v)", what, ok)
		}
		current = got
	}
	if got, _ := Identity(path, []byte(peerMain+"\n"), nil); got == base {
		t.Fatal("entry source did not change the identity")
	}
	change("patterns.cicada", peerPatterns("X... .... .... ....")+asset+"// edited\n", time.Time{})
	expectNew("peer source")
	change("hit.wav", "two!", time.Time{})
	expectNew("asset size")
	change("hit.wav", "twoo", time.Now().Add(time.Hour))
	expectNew("asset modification time")
	change("cicada.mod", peerManifest+"\n", time.Time{})
	expectNew("manifest")
	change("cicada.sum", "sum\n", time.Time{})
	expectNew("cicada.sum")
	// A declared asset that cannot be read makes the inputs unidentifiable, so nothing is cached.
	if err := os.Remove(filepath.Join(dir, "hit.wav")); err != nil {
		t.Fatal(err)
	}
	if _, ok := Identity(path, entry, nil); ok {
		t.Fatal("identity reported for a missing asset")
	}
}

func TestCachedSkipsTheCacheWhenInputsCannotBeIdentified(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.cicada") // no such score and an invalid source
	counted := &countingCheck{}
	check := Cached(path, counted)
	for range 3 {
		if _, err := check.Hash([]byte("not a score")); err != nil {
			t.Fatal(err)
		}
	}
	if counted.calls != 3 {
		t.Fatalf("inner called %d times, want 3 uncached calls", counted.calls)
	}
}

// An unsaved editor buffer replaces the file on disk for both the key and the render.
func TestIdentityAndRenderSeeUnsavedBuffers(t *testing.T) {
	path := writeProject(t, map[string]string{"cicada.mod": peerManifest, "main.cicada": peerMain, "patterns.cicada": peerPatterns(".... .... .... ....")})
	part := filepath.Join(filepath.Dir(path), "patterns.cicada")
	entry := []byte(peerMain)
	disk, ok := Identity(path, entry, nil)
	buffer, ok2 := Identity(path, entry, map[string][]byte{part: []byte(peerPatterns("X... .... .... ...."))})
	same, ok3 := Identity(path, entry, map[string][]byte{part: []byte(peerPatterns(".... .... .... ...."))})
	if !ok || !ok2 || !ok3 || disk == buffer || disk != same {
		t.Fatalf("identity: disk=%s buffer=%s same=%s", disk, buffer, same)
	}
	silent, err := New(path).Hash(entry)
	if err != nil {
		t.Fatal(err)
	}
	audible, err := NewWith(path, map[string][]byte{part: []byte(peerPatterns("X... .... .... ...."))}).Hash(entry)
	if err != nil || audible == silent {
		t.Fatalf("buffer not rendered: %v", err)
	}
}
