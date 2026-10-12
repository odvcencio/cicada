package rendercheck

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/internal/testwav"
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

func (c *countedCheck) HashWith(source []byte, files map[string][]byte) (string, error) {
	c.calls.Add(1)
	return c.inner.(edits.StagedRenderCheck).HashWith(source, files)
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
const peerMain = "scene a { bass = beat }\nscene b { bass = fill }\nsong { a*2 b*2 }\n"

// plainSteps converts cleanly; chanceSteps restart their passes at each placement origin, so splitting them changes the render.
const plainSteps = "1 . 5 . 1 . 5 . 1 . 5 . 1 . 5 ."
const chanceSteps = "1?50 . 5?50 1?50 1?50 . 5?50 1?50 1?50 . 5?50 1?50 1?50 . 5?50 1?50"

func peerPatterns(steps string) string {
	return "track bass acid {}\npattern beat acid {\n  " + steps + "\n}\npattern fill acid {\n  " + steps + "\n}\n"
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
	path := writeProject(t, map[string]string{"cicada.mod": peerManifest, "main.cicada": peerMain, "patterns.cicada": peerPatterns(plainSteps)})
	counted := &countedCheck{inner: New(path)}
	check := Cached(path, counted)
	if err := convert(t, path, check); err != nil {
		t.Fatalf("plain patterns: %v", err)
	}
	renders := counted.calls.Load()
	if renders != 2 {
		t.Fatalf("first conversion rendered %d times, want 2", renders)
	}
	// Unchanged inputs are served from the cache.
	if err := convert(t, path, check); err != nil || counted.calls.Load() != renders {
		t.Fatalf("unchanged inputs: err=%v renders=%d, want %d", err, counted.calls.Load(), renders)
	}
	// Only the peer file changes: both patterns gain chance steps.
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "patterns.cicada"), []byte(peerPatterns(chanceSteps)), 0o600); err != nil {
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
	files := map[string]string{"cicada.mod": peerManifest, "main.cicada": peerMain, "patterns.cicada": peerPatterns(chanceSteps) + asset, "hit.wav": "one"}
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
	change("patterns.cicada", peerPatterns(chanceSteps)+asset+"// edited\n", time.Time{})
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

func TestCachedRefusesWhenInputsCannotBeIdentified(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.cicada") // no such score and an invalid source
	counted := &countingCheck{}
	check := Cached(path, counted)
	for range 3 {
		if _, err := check.Hash([]byte("not a score")); !errors.Is(err, edits.ErrProjectChanged) {
			t.Fatalf("got %v", err)
		}
	}
	if counted.calls != 0 {
		t.Fatalf("rendered %d times without a usable key", counted.calls)
	}
}

// mutatingInner changes the project right before its first render reads it.
type mutatingInner struct {
	edits.StagedRenderCheck
	calls  int
	before func()
}

func (m *mutatingInner) HashWith(source []byte, files map[string][]byte) (string, error) {
	if m.calls++; m.calls == 1 && m.before != nil {
		m.before()
	}
	return m.StagedRenderCheck.HashWith(source, files)
}

// A render that read different inputs than its key describes must not be remembered.
func TestCachedDiscardsAResultWhoseInputsChangedDuringTheRender(t *testing.T) {
	path := writeProject(t, map[string]string{"cicada.mod": peerManifest, "main.cicada": peerMain, "patterns.cicada": peerPatterns(chanceSteps)})
	peer := filepath.Join(filepath.Dir(path), "patterns.cicada")
	entry := []byte(peerMain)
	inner := &mutatingInner{StagedRenderCheck: New(path).(edits.StagedRenderCheck)}
	inner.before = func() { _ = os.WriteFile(peer, []byte(peerPatterns(plainSteps)), 0o600) }
	check := Cached(path, inner)
	if _, err := check.Hash(entry); !errors.Is(err, edits.ErrProjectChanged) {
		t.Fatalf("changed during the render: %v", err)
	}
	// The project is restored. The retry must render again instead of finding
	// an approval left by the rejected attempt.
	if err := os.WriteFile(peer, []byte(peerPatterns(chanceSteps)), 0o600); err != nil {
		t.Fatal(err)
	}
	rendersBefore := inner.calls
	if _, err := check.Hash(entry); err != nil || inner.calls != rendersBefore+1 {
		t.Fatalf("retry: err=%v renders=%d, want %d", err, inner.calls, rendersBefore+1)
	}
}

func comparisonSources(t *testing.T, path string) (before, after []byte) {
	t.Helper()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The conversion the hosts would write for peerMain: one placement per run.
	after = []byte("cicada 2\n" + strings.Replace(peerMain, "song { a*2 b*2 }", "arrange {\n  place a-1-bass bass beat {\n    at = @1.1.1 length = 2bars\n  }\n  place b-3-bass bass fill {\n    at = @3.1.1 length = 2bars\n  }\n}", 1))
	return before, after
}

// The renders read the snapshot, not the disk: a peer rewritten after the
// snapshot (and one rewritten and restored, which leaves the disk identical
// afterwards) cannot change what was rendered.
func TestComparisonRendersFromOneSnapshotOfTheSources(t *testing.T) {
	path := writeProject(t, map[string]string{"cicada.mod": peerManifest, "main.cicada": peerMain, "patterns.cicada": peerPatterns(chanceSteps)})
	peer := filepath.Join(filepath.Dir(path), "patterns.cicada")
	before, after := comparisonSources(t, path)
	snap := New(path).(edits.SnapshotRenderCheck)
	fresh, freshIdentity, err := snap.Compare(before, after, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fresh {
		t.Fatal("fixture: chance steps should render differently once split")
	}
	t.Cleanup(func() { AfterSnapshot, AfterRender = nil, nil })
	for _, restore := range []bool{false, true} {
		AfterSnapshot = func() {
			// Plain steps would make the conversion render the same.
			_ = os.WriteFile(peer, []byte(peerPatterns(plainSteps)), 0o600)
		}
		if restore {
			AfterRender = func() { _ = os.WriteFile(peer, []byte(peerPatterns(chanceSteps)), 0o600) }
		}
		same, identity, err := snap.Compare(before, after, nil)
		if err != nil || same != fresh || identity != freshIdentity {
			t.Fatalf("restore=%v: same=%v identity match=%v err=%v", restore, same, identity == freshIdentity, err)
		}
		current, ok := snap.PairIdentity(before, after, nil)
		if restore && (!ok || current != identity) {
			t.Fatal("restored project should match the recorded identity")
		}
		if !restore && current == identity {
			t.Fatal("a changed project must not match the recorded identity")
		}
		AfterSnapshot, AfterRender = nil, nil
		_ = os.WriteFile(peer, []byte(peerPatterns(chanceSteps)), 0o600)
	}
}

// An asset still loads from disk, so a rewrite during the render is caught by
// its modification time even when the bytes are put back.
func TestComparisonRefusesWhenAnAssetIsRewrittenDuringTheRender(t *testing.T) {
	wav := testwav.Bytes(48000, 1, 16, 480, 1)
	main := fmt.Sprintf("cicada 2\nasset wave \"wave.wav\" { sha256=\"%x\" format=wav frames=480 rate=48000Hz channels=1 }\nsampler hit { asset=wave root=c3 mode=oneshot voices=8 }\ntrack lead hit {}\npattern melody { c3 . c3 . }\nscene a { lead = melody }\nsong { a*2 }\n", sha256.Sum256(wav))
	path := writeProject(t, map[string]string{"main.cicada": main, "wave.wav": string(wav)})
	sample := filepath.Join(filepath.Dir(path), "wave.wav")
	snap := New(path).(edits.SnapshotRenderCheck)
	before := []byte(main)
	after := []byte(strings.Replace(main, "song { a*2 }", "arrange {\n  place a-1-lead lead melody {\n    at = @1.1.1 length = 2bars\n  }\n}", 1))
	if _, _, err := snap.Compare(before, after, nil); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	t.Cleanup(func() { AfterSnapshot = nil })
	AfterSnapshot = func() {
		_ = os.WriteFile(sample, wav, 0o600)
		later := time.Now().Add(time.Hour)
		_ = os.Chtimes(sample, later, later)
	}
	if _, _, err := snap.Compare(before, after, nil); !errors.Is(err, edits.ErrProjectChanged) {
		t.Fatalf("asset rewritten during the render: %v", err)
	}
}

// The snapshot pins the resolved project. A manifest that appears on disk
// during or before a comparison cannot change which project the renders load.
func TestComparisonPinsTheResolvedProject(t *testing.T) {
	looseMain := peerPatterns(plainSteps) + peerMain
	path := writeProject(t, map[string]string{"main.cicada": looseMain})
	dir := filepath.Dir(path)
	manifest := filepath.Join(dir, "cicada.mod")
	before := []byte(looseMain)
	after := []byte("cicada 2\n" + peerPatterns(plainSteps) + "scene a { bass = beat }\nscene b { bass = fill }\narrange {\n  place a-1-bass bass beat {\n    at = @1.1.1 length = 2bars\n  }\n  place b-3-bass bass fill {\n    at = @3.1.1 length = 2bars\n  }\n}\n")
	counted := &countedSnapshot{SnapshotRenderCheck: New(path).(edits.SnapshotRenderCheck)}
	check := Cached(path, counted)
	same, identity, err := check.Compare(before, after, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { AfterSnapshot, AfterRender = nil, nil })
	// Introduced during the comparison and removed before it ends.
	AfterSnapshot = func() { _ = os.WriteFile(manifest, []byte("project nested\ncicada 1\n"), 0o600) }
	AfterRender = func() { _ = os.Remove(manifest) }
	counted.calls = 0
	again, againIdentity, err := New(path).(edits.SnapshotRenderCheck).Compare(before, after, nil)
	AfterSnapshot, AfterRender = nil, nil
	if err != nil || again != same || againIdentity != identity {
		t.Fatalf("a manifest introduced mid-comparison changed the result: same=%v/%v identity match=%v err=%v", again, same, againIdentity == identity, err)
	}
	// Present before the next comparison: the resolved project differs, so the
	// recorded identity no longer matches and the cache cannot approve it.
	if err := os.WriteFile(manifest, []byte("project nested\ncicada 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if current, ok := check.PairIdentity(before, after, nil); ok && current == identity {
		t.Fatal("a newly discovered manifest left the pair identity unchanged")
	}
	cachedBefore := counted.calls
	_, _, _ = check.Compare(before, after, nil)
	if counted.calls != cachedBefore+1 {
		t.Fatalf("the cache answered for a different resolved project (%d renders)", counted.calls-cachedBefore)
	}
	// Removed again: the original approval is valid and served from the cache.
	if err := os.Remove(manifest); err != nil {
		t.Fatal(err)
	}
	cachedBefore = counted.calls
	if got, gotIdentity, err := check.Compare(before, after, nil); err != nil || got != same || gotIdentity != identity || counted.calls != cachedBefore {
		t.Fatalf("restored project: same=%v err=%v renders=%d", got, err, counted.calls-cachedBefore)
	}
}

type countedSnapshot struct {
	edits.SnapshotRenderCheck
	calls int
}

func (c *countedSnapshot) Compare(before, after []byte, files map[string][]byte) (bool, string, error) {
	c.calls++
	return c.SnapshotRenderCheck.Compare(before, after, files)
}
