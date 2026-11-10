package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
)

// The stored mix (render.MasterCache) only saves time: a render that replays
// it, a loudness search whose later passes replay it, and a peak-normalized
// render whose second render replays it must write the same bytes, and report
// the same numbers, as renders that synthesize the song every time.
//
// TestRenderCacheIdentity checks one short score for each master path on every
// test run. With CICADA_RENDER_IDENTITY_SWEEP=1 it checks every example score
// the repository can render, in full; the nightly CI job runs that sweep on
// amd64 and arm64 (make test-render-identity).

// identityTargets are the loudness targets checked: one that most example
// scores reach and one that most cannot reach below their true-peak ceiling.
var identityTargets = []struct{ lufs, truePeak float64 }{{-14, -1}, {-9, -1.5}}

const scheduledInsert = "fx cleanup eq { type = highpass frequency = 30Hz q = 0.707 }\ntrack bass acid { insert = cleanup "

// identityScore is a score to check and the bars to render, 0 for the whole
// song. Replacements rewrite the source first, in pairs of old and new text.
type identityScore struct {
	name         string
	path         string
	bars         int
	replacements []string
}

func identitySubset() []identityScore {
	root := repositoryRoot()
	loudness := filepath.Join("testdata", "export-loudness")
	return []identityScore{
		{name: "direct-no-chain", path: filepath.Join(root, "examples", "tuplet-grid.cicada")},
		{name: "direct-limiter-chain", path: filepath.Join(root, "examples", "songs", "anywhere-you-are.cicada"), bars: 4},
		{name: "direct-chain", path: filepath.Join(loudness, "punchy-nolimiter.cicada"), bars: 2},
		{name: "scheduled-no-chain", path: filepath.Join(root, "examples", "arrangement-tempo.cicada"), bars: 4},
		{name: "scheduled-chain", path: filepath.Join(loudness, "punchy-nolimiter.cicada"), bars: 2, replacements: []string{"track bass acid {", scheduledInsert}},
		{name: "scheduled-limiter-chain", path: filepath.Join(loudness, "punchy-limiter.cicada"), bars: 2, replacements: []string{"track bass acid {", scheduledInsert}},
	}
}

// identitySweep lists every .cicada file under examples/.
func identitySweep(t *testing.T) []identityScore {
	t.Helper()
	root := repositoryRoot()
	var scores []identityScore
	err := filepath.WalkDir(filepath.Join(root, "examples"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".cicada") {
			name, _ := filepath.Rel(root, path)
			scores = append(scores, identityScore{name: filepath.ToSlash(name), path: path})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return scores
}

func TestRenderCacheIdentity(t *testing.T) {
	scores := identitySubset()
	sweep := os.Getenv("CICADA_RENDER_IDENTITY_SWEEP") == "1"
	if sweep {
		scores = identitySweep(t)
	}
	checked, skipped := 0, 0
	for _, score := range scores {
		t.Run(score.name, func(t *testing.T) {
			inspection, why := loadIdentityScore(t, score)
			if inspection.score == nil {
				if !sweep {
					t.Fatalf("subset score does not load: %s", why)
				}
				skipped++
				t.Skipf("not renderable without more files: %s", why)
			}
			opts := render.Options{SampleRate: 48000, Bits: 24, Bars: score.bars, TailSec: 3, AssetRoot: inspection.root(score.path), AssetDir: inspection.root(score.path)}
			plain, plainReport, err := renderWithReport(inspection.score, opts)
			if err != nil {
				if !sweep {
					t.Fatalf("subset score does not render: %v", err)
				}
				skipped++
				t.Skipf("does not render: %v", err)
			}
			sum := sha256.Sum256(plain)
			t.Logf("path: %s; plain render sha256 %x", describeMasterPath(inspection, opts), sum)
			checkPlainRenderIdentity(t, inspection.score, opts, plain, plainReport)
			checkNormalizeIdentity(t, inspection.score, opts)
			for _, target := range identityTargets {
				checkLoudnessSearchIdentity(t, inspection.score, opts, renderTargetOptions{LoudnessTarget: target.lufs, TruePeakMaxDBTP: target.truePeak, Tolerance: 0.5, Loudness: true})
			}
			checked++
		})
	}
	t.Logf("METRIC RENDER-CACHE-IDENTITY scores=%d checked=%d skipped=%d targets=%d", len(scores), checked, skipped, len(identityTargets))
}

// loadIdentityScore parses a score, after its replacements, or explains why it
// cannot be rendered on its own.
func loadIdentityScore(t *testing.T, score identityScore) (scoreInspection, string) {
	t.Helper()
	path := score.path
	if len(score.replacements) > 0 {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(source)
		for i := 0; i+1 < len(score.replacements); i += 2 {
			if !strings.Contains(text, score.replacements[i]) {
				t.Fatalf("%s has no %q", path, score.replacements[i])
			}
			text = strings.Replace(text, score.replacements[i], score.replacements[i+1], 1)
		}
		path = filepath.Join(t.TempDir(), filepath.Base(path))
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	inspection, err := inspectScore(path)
	if err != nil {
		return scoreInspection{}, err.Error()
	}
	if inspection.score == nil || inspection.semantic == nil || hasDiagnosticErrors(inspection.diagnostics) {
		return scoreInspection{}, fmt.Sprint(inspection.diagnostics)
	}
	return inspection, ""
}

func describeMasterPath(inspection scoreInspection, opts render.Options) string {
	renderer := "direct"
	if render.UsesScheduledEngine(inspection.score, opts) {
		renderer = "scheduled"
	}
	chain := "no master chain"
	if p := inspection.semantic; p.Master != nil && len(p.Master.Mixer.Inserts) > 0 {
		chain = "master chain"
		if project.MasterHasLimiter(p) {
			chain = "master chain with a limiter"
		}
	}
	return renderer + ", " + chain
}

func renderWithReport(score *notation.Score, opts render.Options) ([]byte, render.Report, error) {
	var out bytes.Buffer
	report, err := render.WAV(score, opts, &out)
	return out.Bytes(), report, err
}

func mustRender(t *testing.T, score *notation.Score, opts render.Options) ([]byte, render.Report) {
	t.Helper()
	wav, report, err := renderWithReport(score, opts)
	if err != nil {
		t.Fatal(err)
	}
	return wav, report
}

// checkPlainRenderIdentity captures and then replays the render that gave want
// without a cache.
func checkPlainRenderIdentity(t *testing.T, score *notation.Score, opts render.Options, want []byte, wantReport render.Report) {
	t.Helper()
	cache, err := render.NewMasterCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	opts.MasterCache = cache
	for _, step := range []string{"capture", "replay"} {
		got, gotReport := mustRender(t, score, opts)
		if !bytes.Equal(got, want) {
			t.Fatalf("plain render, %s: WAV differs from a render without the cache", step)
		}
		if !reflect.DeepEqual(gotReport, wantReport) {
			t.Fatalf("plain render, %s: report differs:\n got %+v\nwant %+v", step, gotReport, wantReport)
		}
	}
	if cache.Replays() != 1 {
		t.Fatalf("plain render: %d replays, want 1", cache.Replays())
	}
}

// checkNormalizeIdentity renders with peak normalization, whose second render
// replays the first one's mix when a cache is set.
func checkNormalizeIdentity(t *testing.T, score *notation.Score, opts render.Options) {
	t.Helper()
	opts.Normalize = true
	want, wantReport := mustRender(t, score, opts)
	cache, err := render.NewMasterCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	opts.MasterCache = cache
	got, gotReport := mustRender(t, score, opts)
	if !bytes.Equal(got, want) || !reflect.DeepEqual(gotReport, wantReport) {
		t.Fatalf("normalized render with the cache differs:\n got %+v\nwant %+v", gotReport, wantReport)
	}
	if cache.Replays() != 1 {
		t.Fatalf("normalized render: %d replays, want 1", cache.Replays())
	}
}

// checkLoudnessSearchIdentity runs the loudness search with every pass in full
// (the cache cannot be created) and with the cache, and compares the files and
// the reports. Only the counts of syntheses and replays may differ.
func checkLoudnessSearchIdentity(t *testing.T, score *notation.Score, opts render.Options, target renderTargetOptions) {
	t.Helper()
	defer func(create func(string) (*render.MasterCache, error)) { newLoudnessMasterCache = create }(newLoudnessMasterCache)
	search := func(cached bool) (loudnessFileReport, []byte, error) {
		newLoudnessMasterCache = render.NewMasterCache
		if !cached {
			newLoudnessMasterCache = func(string) (*render.MasterCache, error) { return nil, errors.New("cache disabled by the test") }
		}
		path := filepath.Join(t.TempDir(), "search.wav")
		result, err := renderLoudnessFileReport(score, path, opts, target, nil)
		wav, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("target %g LUFS: the search wrote no audio: %v (search error: %v)", target.LoudnessTarget, readErr, err)
		}
		return result, wav, err
	}
	full, fullWAV, fullErr := search(false)
	cached, cachedWAV, cachedErr := search(true)
	sum := sha256.Sum256(cachedWAV)
	t.Logf("METRIC RENDER-CACHE-SEARCH target=%g passes=%d syntheses=%d->%d replays=%d repeats=%d lufs=%.3f gain=%+.3f sha256=%x err=%v", target.LoudnessTarget, cached.Passes, full.Syntheses, cached.Syntheses, cached.Replays, cached.Repeats, cached.AchievedLUFS, cached.AppliedGainDB, sum, cachedErr)
	if fmt.Sprint(fullErr) != fmt.Sprint(cachedErr) || !sameSearchResult(full, cached) {
		t.Fatalf("target %g LUFS: search with the cache differs:\n got %+v (%v)\nwant %+v (%v)", target.LoudnessTarget, cached, cachedErr, full, fullErr)
	}
	if !bytes.Equal(fullWAV, cachedWAV) {
		t.Fatalf("target %g LUFS: search with the cache wrote different audio", target.LoudnessTarget)
	}
	if full.Syntheses+full.Repeats != full.Passes || full.Replays != 0 {
		t.Fatalf("target %g LUFS: search without a cache reports %d syntheses, %d replays and %d repeats in %d passes", target.LoudnessTarget, full.Syntheses, full.Replays, full.Repeats, full.Passes)
	}
	if cached.Syntheses != 1 || cached.Replays+cached.Repeats != cached.Passes-1 {
		t.Fatalf("target %g LUFS: search with the cache synthesized %d times, replayed %d times and repeated %d passes of %d, want 1 synthesis", target.LoudnessTarget, cached.Syntheses, cached.Replays, cached.Repeats, cached.Passes)
	}
}

// sameSearchResult reports whether two loudness searches found the same result:
// every field except whether a rendered pass synthesized the song or replayed
// the stored mix.
func sameSearchResult(a, b loudnessFileReport) bool {
	for _, r := range []*loudnessFileReport{&a, &b} {
		r.Syntheses, r.Replays = 0, 0
		r.PassLog = slices.Clone(r.PassLog)
		for i := range r.PassLog {
			if r.PassLog[i].Render == passReplayed {
				r.PassLog[i].Render = passSynthesized
			}
		}
	}
	return reflect.DeepEqual(a, b)
}
