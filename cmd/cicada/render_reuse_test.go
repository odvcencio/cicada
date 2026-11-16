package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/render"
)

func loadTupletGrid(t *testing.T) scoreInspection {
	t.Helper()
	inspection, err := inspectScore(filepath.Join(repositoryRoot(), "examples", "tuplet-grid.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	return inspection
}

// CICADA_RENDER_NO_REUSE=1 renders every loudness pass in full, and both
// renders of peak normalization, and writes what the default writes.
func TestRenderNoReuseEnvironmentRendersEveryPass(t *testing.T) {
	inspection := loadTupletGrid(t)
	opts := render.Options{SampleRate: 48000, Bits: 24, TailSec: 1}
	// tuplet-grid at -14 LUFS reaches the +24 dB gain limit in its second pass
	// and repeats it, so by default it renders 2 of its 6 passes.
	target := renderTargetOptions{LoudnessTarget: -14, TruePeakMaxDBTP: -1, Tolerance: .5, Loudness: true}
	search := func() (loudnessFileReport, []byte) {
		path := filepath.Join(t.TempDir(), "out.wav")
		result, _ := renderLoudnessFileReport(inspection.score, path, opts, target, nil)
		wav, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return result, wav
	}
	reused, reusedWAV := search()
	if reused.Syntheses != 1 || reused.Repeats == 0 {
		t.Fatalf("the default search should synthesize once and repeat passes: %+v", reused)
	}
	t.Setenv(renderNoReuseEnv, "1")
	full, fullWAV := search()
	if full.Syntheses != full.Passes || full.Replays != 0 || full.Repeats != 0 || full.Passes != reused.Passes {
		t.Fatalf("with %s=1 every pass must synthesize: %d passes, %d syntheses, %d replays, %d repeats", renderNoReuseEnv, full.Passes, full.Syntheses, full.Replays, full.Repeats)
	}
	if !bytes.Equal(fullWAV, reusedWAV) || !sameSearchResult(full, reused) {
		t.Fatal("rendering every pass changed the result")
	}
	defer func(create func(string) (*render.MasterCache, error)) { newNormalizeMasterCache = create }(newNormalizeMasterCache)
	newNormalizeMasterCache = func(string) (*render.MasterCache, error) {
		t.Fatalf("peak normalization created a cache with %s=1", renderNoReuseEnv)
		return nil, nil
	}
	normalized := opts
	normalized.Normalize = true
	if _, err := renderPlainFile(inspection.score, filepath.Join(t.TempDir(), "normalized.wav"), normalized); err != nil {
		t.Fatal(err)
	}
}

// Peak normalization renders again without its cache only when the cache was
// the cause: the stored mix could not be read back. Any other failure returns
// at once instead of synthesizing the song twice more.
func TestRenderPlainFileNormalizeRetriesOnlyACacheFault(t *testing.T) {
	defer func(create func(string) (*render.MasterCache, error), attempt func(*notation.Score, string, render.Options) (render.Report, error)) {
		newNormalizeMasterCache, renderPlainFileAttempt = create, attempt
	}(newNormalizeMasterCache, renderPlainFileAttempt)
	inspection := loadTupletGrid(t)
	opts := render.Options{SampleRate: 48000, Bits: 24, TailSec: 1, Normalize: true}
	var want bytes.Buffer
	if _, err := render.WAV(inspection.score, opts, &want); err != nil {
		t.Fatal(err)
	}
	var attempts []bool // whether each attempt had a cache
	renderPlainFileAttempt = func(score *notation.Score, path string, opts render.Options) (render.Report, error) {
		attempts = append(attempts, opts.MasterCache != nil)
		return renderPlainFileOnce(score, path, opts)
	}

	t.Run("the stored mix cannot be read back", func(t *testing.T) {
		attempts = nil
		// A cache file open for writing only: the capture succeeds and the
		// replay cannot read it.
		newNormalizeMasterCache = func(dir string) (*render.MasterCache, error) {
			file, err := os.OpenFile(filepath.Join(dir, ".cicada-master-cache-write-only"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return nil, err
			}
			return render.NewMasterCacheFile(file), nil
		}
		dir := t.TempDir()
		path := filepath.Join(dir, "out.wav")
		if _, err := renderPlainFile(inspection.score, path, opts); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(path); !bytes.Equal(got, want.Bytes()) {
			t.Fatal("the render after the cache fault differs from a render without a cache")
		}
		if !reflect.DeepEqual(attempts, []bool{true, false}) {
			t.Fatalf("attempts by cache: %v, want one with the cache and one without", attempts)
		}
		if files, _ := filepath.Glob(filepath.Join(dir, ".cicada-*")); len(files) != 0 {
			t.Fatalf("left files behind: %v", files)
		}
	})

	t.Run("another failure", func(t *testing.T) {
		attempts = nil
		newNormalizeMasterCache = func(string) (*render.MasterCache, error) { return render.NewMasterCache(t.TempDir()) }
		// The output folder does not exist, so the render fails while the
		// cache exists, for a reason that is not the cache.
		_, err := renderPlainFile(inspection.score, filepath.Join(t.TempDir(), "missing", "out.wav"), opts)
		if err == nil || masterCacheFault(err) {
			t.Fatalf("want a failure that is not a cache fault, got %v", err)
		}
		if !reflect.DeepEqual(attempts, []bool{true}) {
			t.Fatalf("attempts by cache: %v, want one attempt", attempts)
		}
	})
}

// On Unix the stored mix has no name, so a render that is killed leaves none
// of it behind.
func TestLoudnessSearchCacheFileHasNoNameOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows keeps the name of an open file")
	}
	defer func(pass func(*notation.Score, render.Options, io.Writer) (render.Report, error)) {
		loudnessRenderPass = pass
	}(loudnessRenderPass)
	dir := t.TempDir()
	named := 0
	loudnessRenderPass = func(score *notation.Score, opts render.Options, w io.Writer) (render.Report, error) {
		if opts.MasterCache == nil {
			t.Error("the search ran without its cache")
		}
		named += len(cacheFilesIn(t, dir))
		return render.WAV(score, opts, w)
	}
	inspection := loadTupletGrid(t)
	target := renderTargetOptions{LoudnessTarget: -14, TruePeakMaxDBTP: -1, Tolerance: .5, Loudness: true}
	// The quiet score stops short of the target (at the +24 dB maximum export
	// gain); only the cache file matters here.
	var shortfall *loudnessShortfallError
	if _, err := renderLoudnessFileReport(inspection.score, filepath.Join(dir, "out.wav"), render.Options{SampleRate: 48000, Bits: 24, TailSec: 1}, target, nil); err != nil && !errors.As(err, &shortfall) {
		t.Fatal(err)
	}
	if named != 0 {
		t.Fatalf("the cache file had a name in %d passes", named)
	}
}

// A render's temporary files that are more than a day old are removed by the
// next render in the folder; anything else stays.
func TestRenderRemovesStaleTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-25 * time.Hour)
	stale := []string{".cicada-render-1", ".cicada-loudness-candidate-2", ".cicada-loudness-best-3", ".cicada-master-cache-4"}
	kept := []string{".cicada-render-new", "song.wav", ".cicada-format-5"}
	for _, name := range append(append([]string{}, stale...), kept...) {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if name != ".cicada-render-new" && name != "song.wav" {
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	inspection := loadTupletGrid(t)
	if _, err := renderPlainFile(inspection.score, filepath.Join(dir, "out.wav"), render.Options{SampleRate: 48000, Bits: 24, Bars: 1, TailSec: 0}); err != nil {
		t.Fatal(err)
	}
	for _, name := range stale {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s was not removed: %v", name, err)
		}
	}
	for _, name := range kept {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s was removed: %v", name, err)
		}
	}
}

// An interrupt removes the temporary files registered at that moment.
func TestInterruptRemovesRegisteredFiles(t *testing.T) {
	dir := t.TempDir()
	first, second := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	doneFirst := removeOnInterrupt(first)
	doneSecond := removeOnInterrupt(second)
	doneFirst()
	removeInterruptFiles()
	doneSecond()
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("a file taken off the list was removed: %v", err)
	}
	if _, err := os.Stat(second); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a registered file was not removed: %v", err)
	}
	interruptFiles.Lock()
	_, leftFirst := interruptFiles.paths[first]
	_, leftSecond := interruptFiles.paths[second]
	interruptFiles.Unlock()
	if leftFirst || leftSecond {
		t.Fatal("a registration was left after its done function ran")
	}
}

// --cpuprofile writes a profile even when a command fails before it does any
// work.
func TestCPUProfileIsWrittenOnEarlyFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.cicada"), []byte("cicada 2\ntrack x nosuchvoice {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "intents.json"), []byte(`{"version": 1, "intents": []}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"render", "missing.cicada", "-o", "out.wav"},
		{"render", "bad.cicada", "-o", "out.wav"},
		{"render", "bad.cicada", "-o", "out.wav", "--bits", "7"},
		{"stems", "bad.cicada", "-o", "stems"},
		{"apply", "missing.cicada", "intents.json"},
		{"check", "bad.cicada"},
		{"audition", "missing.cicada"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			profile := filepath.Join(dir, strings.ReplaceAll(strings.Join(args, "_"), "/", "_")+".prof")
			output, status := runMain(t, dir, append(args, "--cpuprofile", profile)...)
			if status == 0 {
				t.Fatalf("the command succeeded: %s", output)
			}
			if info, err := os.Stat(profile); err != nil || info.Size() == 0 {
				t.Fatalf("no profile after exit %d (%v): %s", status, err, output)
			}
		})
	}
}
