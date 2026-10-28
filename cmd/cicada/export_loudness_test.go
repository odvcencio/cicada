package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/render"
)

// noLimiterWAVSHA256 is the output of the pre-change renderer for
// testdata/export-loudness/punchy-nolimiter.cicada with export "loud". A chain
// without a limiter must keep producing exactly these bytes.
const noLimiterWAVSHA256 = "dc6bea2bcdc3a890666d1e91b75a946576762871fa24ecbb2d761042b31a0328"

func exportFixture(t *testing.T, name, export string) (inspection scoreInspection, opts render.Options, target renderTargetOptions) {
	t.Helper()
	inspection, err := inspectScore(filepath.Join("testdata", "export-loudness", name))
	if err != nil {
		t.Fatal(err)
	}
	opts = render.Options{SampleRate: 48000, Bits: 24, Bars: 4, TailSec: 3}
	target = renderTargetOptions{ExportName: export, TruePeakMaxDBTP: -1}
	if err := applyExportTarget(inspection.semantic, &opts, &target); err != nil {
		t.Fatal(err)
	}
	return inspection, opts, target
}

func TestExportLoudnessMasterLimiterReachesTarget(t *testing.T) {
	inspection, opts, target := exportFixture(t, "punchy-limiter.cicada", "loud")
	wav := filepath.Join(t.TempDir(), "limiter.wav")
	result, err := renderLoudnessFileReport(inspection.score, wav, opts, target, nil)
	if err != nil {
		t.Fatalf("target render: %v (%+v)", err, result)
	}
	report, err := render.VerifyWAV(wav, render.VerifyOptions{SampleRate: 48000, Bits: 24, Bars: 4, TailSec: 3, PeakMaxDB: -.3, DCMaxDB: -60, LUFSTarget: -9, LUFSTolerance: .5, CheckLUFS: true, TruePeakMaxDBTP: -1, CheckTruePeak: true})
	if err != nil {
		t.Fatalf("verify-wav: %v", err)
	}
	t.Logf("METRIC EXPORT-LIMITER lufs=%.3f true_peak=%.3f gain=%+.2f chain_reduction=%.2f passes=%d", report.IntegratedLUFS, report.TruePeakDBTP, result.AppliedGainDB, result.MasterLimiterReductionDB, result.Passes)
	if result.GainStage != "master_input" {
		t.Fatalf("gain stage = %q, want master_input", result.GainStage)
	}
	if result.MasterLimiterReductionDB <= 0 {
		t.Fatalf("master limiter never reduced gain: %+v", result)
	}
	if result.MasterLimiterReductionDB > maxExportLimiterReductionDB {
		t.Fatalf("limiter reduction %.2f dB exceeds the %.1f dB maximum", result.MasterLimiterReductionDB, maxExportLimiterReductionDB)
	}
	if result.LargestLimiterReductionDB < result.MasterLimiterReductionDB {
		t.Fatalf("largest limiter reduction %.2f omits the master chain limiter %.2f", result.LargestLimiterReductionDB, result.MasterLimiterReductionDB)
	}
	if report.TruePeakDBTP > -1 {
		t.Fatalf("true peak %.3f dBTP above ceiling", report.TruePeakDBTP)
	}
}

func TestExportLoudnessWithoutLimiterKeepsCrestPreservingGain(t *testing.T) {
	inspection, opts, target := exportFixture(t, "punchy-nolimiter.cicada", "loud")
	wav := filepath.Join(t.TempDir(), "nolimiter.wav")
	result, err := renderLoudnessFileReport(inspection.score, wav, opts, target, nil)
	var shortfall *loudnessShortfallError
	if !errors.As(err, &shortfall) || !strings.Contains(err.Error(), "loudness target shortfall 1.86 LU") {
		t.Fatalf("want the crest-factor shortfall, got %v", err)
	}
	if result.GainStage != "post_chain" || result.MasterLimiterReductionDB != 0 {
		t.Fatalf("no-limiter report changed: %+v", result)
	}
	data, err := os.ReadFile(wav)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != noLimiterWAVSHA256 {
		t.Fatalf("no-limiter output changed: sha256 %s", got)
	}
}

func TestExportLoudnessInputGainCapReportsShortfall(t *testing.T) {
	// Without the +6 dB master level the mix is quiet, so the +12 dB input
	// gain cap binds before the limiter reduction maximum does.
	source, err := os.ReadFile(filepath.Join("testdata", "export-loudness", "punchy-limiter.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	quiet := filepath.Join(t.TempDir(), "quiet-limiter.cicada")
	if err := os.WriteFile(quiet, []byte(strings.Replace(string(source), "level = 6dB ", "", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	inspection, err := inspectScore(quiet)
	if err != nil {
		t.Fatal(err)
	}
	opts := render.Options{SampleRate: 48000, Bits: 24, Bars: 4, TailSec: 3}
	target := renderTargetOptions{LoudnessTarget: -4, TruePeakMaxDBTP: -1, Tolerance: .5, Loudness: true}
	result, err := renderLoudnessFileReport(inspection.score, filepath.Join(t.TempDir(), "cap.wav"), opts, target, nil)
	var shortfall *loudnessShortfallError
	if !errors.As(err, &shortfall) {
		t.Fatalf("want a shortfall error, got %v (%+v)", err, result)
	}
	if result.AppliedGainDB > maxExportInputGainDB+1e-9 {
		t.Fatalf("input gain %.2f dB exceeds the %.1f dB cap", result.AppliedGainDB, maxExportInputGainDB)
	}
	if !strings.Contains(err.Error(), "input gain cap") {
		t.Fatalf("shortfall does not name the input gain cap: %v", err)
	}
	t.Logf("METRIC EXPORT-INPUT-CAP lufs=%.3f gain=%+.2f chain_reduction=%.2f err=%v", result.AchievedLUFS, result.AppliedGainDB, result.MasterLimiterReductionDB, err)
}

func TestExportLoudnessLimiterReductionMaximumReportsShortfall(t *testing.T) {
	inspection, opts, target := exportFixture(t, "punchy-limiter.cicada", "loud")
	target.LoudnessTarget = -4
	wav := filepath.Join(t.TempDir(), "reduction.wav")
	result, err := renderLoudnessFileReport(inspection.score, wav, opts, target, nil)
	var shortfall *loudnessShortfallError
	if !errors.As(err, &shortfall) {
		t.Fatalf("want a shortfall error, got %v (%+v)", err, result)
	}
	if result.MasterLimiterReductionDB > maxExportLimiterReductionDB {
		t.Fatalf("limiter reduction %.2f dB exceeds the %.1f dB maximum without a report", result.MasterLimiterReductionDB, maxExportLimiterReductionDB)
	}
	if !strings.Contains(err.Error(), "maximum master limiter gain reduction") {
		t.Fatalf("shortfall does not name the limiter reduction maximum: %v", err)
	}
	if result.Passes > 3 {
		t.Fatalf("limiter-cap shortfall used %d full renders, want at most 3", result.Passes)
	}
	t.Logf("METRIC EXPORT-REDUCTION-CAP lufs=%.3f gain=%+.2f chain_reduction=%.2f err=%v", result.AchievedLUFS, result.AppliedGainDB, result.MasterLimiterReductionDB, err)
}

func TestLimiterTargetOutOfReach(t *testing.T) {
	target := renderTargetOptions{LoudnessTarget: -9, Tolerance: .5}
	for _, test := range []struct {
		name                                           string
		bestLUFS, bestChainReduction, lowestUnsafeLUFS float64
		want                                           bool
	}{
		{"both renders below the band at the reduction maximum", -10, 6, -9.6, true},
		{"safe render 0.5 dB short of the maximum", -10, maxExportLimiterReductionDB - .5, -9.6, true},
		{"safe render has more than 0.5 dB of reduction left", -10, maxExportLimiterReductionDB - .51, -9.6, false},
		{"safe render inside the band", -9.5, 6, -9.2, false},
		{"unsafe render inside the band", -10, 6, -9.5, false},
		{"unsafe render above the band", -10, 6, -8.4, false},
		{"no unsafe render yet", -10, 6, math.Inf(1), false},
		{"unmeasurable unsafe render", -10, 6, math.NaN(), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := limiterTargetOutOfReach(target, test.bestLUFS, test.bestChainReduction, test.lowestUnsafeLUFS); got != test.want {
				t.Fatalf("limiterTargetOutOfReach(best %.2f LUFS at %.2f dB, unsafe %.2f LUFS) = %v, want %v", test.bestLUFS, test.bestChainReduction, test.lowestUnsafeLUFS, got, test.want)
			}
		})
	}
}

// loudestCompliantLUFS bisects the input gain for the loudest render that keeps
// the master limiter at or below its reduction maximum and the true peak at or
// below the ceiling. It does not use the export search, so it can check it.
func loudestCompliantLUFS(t *testing.T, inspection scoreInspection, opts render.Options, target renderTargetOptions) float64 {
	t.Helper()
	measure := func(gain float64) (lufs float64, compliant bool) {
		path := filepath.Join(t.TempDir(), "oracle.wav")
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		opts.MasterInputGainDB = gain
		report, err := render.WAV(inspection.score, opts, file)
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			t.Fatal(err)
		}
		measured, err := render.VerifyWAV(path, render.VerifyOptions{SampleRate: report.SampleRate, Bits: opts.Bits, Bars: report.Bars, From: report.From, TailSec: opts.TailSec, PeakMaxDB: 100, DCMaxDB: 100})
		if err != nil {
			t.Fatal(err)
		}
		return measured.IntegratedLUFS, report.MaxMasterChainLimiterReductionDB <= maxExportLimiterReductionDB && measured.TruePeakDBTP <= target.TruePeakMaxDBTP
	}
	low, high := 0.0, maxExportInputGainDB
	lufs, compliant := measure(low)
	if !compliant {
		t.Fatal("the fixture breaks a guard rail without any gain")
	}
	if _, ok := measure(high); ok {
		t.Fatalf("the fixture stays compliant at the +%.0f dB input gain cap", maxExportInputGainDB)
	}
	for high-low > .02 {
		mid := (low + high) / 2
		if l, ok := measure(mid); ok {
			low, lufs = mid, l
		} else {
			high = mid
		}
	}
	return lufs
}

// A target the limiter maximum rules out used to run all six passes. The search
// now stops once no safe gain can reach it, and the file it keeps must stay
// close to the loudest render that obeys the guard rails.
func TestExportLoudnessLimiterShortfallStopsEarlyAtTheLoudestSafeRender(t *testing.T) {
	inspection, opts, target := exportFixture(t, "punchy-limiter.cicada", "loud")
	ceiling := loudestCompliantLUFS(t, inspection, opts, target)
	for _, lufs := range []float64{-4, -7} {
		target.LoudnessTarget = lufs
		result, err := renderLoudnessFileReport(inspection.score, filepath.Join(t.TempDir(), "short.wav"), opts, target, nil)
		var shortfall *loudnessShortfallError
		if !errors.As(err, &shortfall) || !strings.Contains(err.Error(), "maximum master limiter gain reduction") {
			t.Fatalf("target %g LUFS: want a limiter shortfall, got %v", lufs, err)
		}
		t.Logf("METRIC EXPORT-EARLY-EXIT target=%g passes=%d lufs=%.3f loudest_compliant=%.3f chain_reduction=%.2f", lufs, result.Passes, result.AchievedLUFS, ceiling, result.MasterLimiterReductionDB)
		if result.Passes >= loudnessPassLimit {
			t.Fatalf("target %g LUFS used %d of %d passes; the search did not stop early", lufs, result.Passes, loudnessPassLimit)
		}
		if result.MasterLimiterReductionDB > maxExportLimiterReductionDB || result.TruePeakDBTP > target.TruePeakMaxDBTP {
			t.Fatalf("target %g LUFS: kept a render that breaks a guard rail: %+v", lufs, result)
		}
		if result.AchievedLUFS > ceiling+.01 || result.AchievedLUFS < ceiling-.25 {
			t.Fatalf("target %g LUFS: kept %.3f LUFS, want within 0.25 LU below the loudest compliant render at %.3f LUFS", lufs, result.AchievedLUFS, ceiling)
		}
	}
	// A reachable target near the maximum is unaffected: it still finishes
	// inside its tolerance band.
	target.LoudnessTarget = -8
	result, err := renderLoudnessFileReport(inspection.score, filepath.Join(t.TempDir(), "reach.wav"), opts, target, nil)
	if err != nil || math.Abs(result.AchievedLUFS-target.LoudnessTarget) > target.Tolerance {
		t.Fatalf("reachable target -8 LUFS: %v (%+v)", err, result)
	}
}

// Passes after the first replay its mix instead of synthesizing it again. The
// search must still find the same gain and write the same bytes as one that
// renders every pass in full.
func TestExportLoudnessMasterCacheKeepsTheSearchResultIdentical(t *testing.T) {
	defer func(previous func(*notation.Score, render.Options, io.Writer) (render.Report, error)) {
		loudnessRenderPass = previous
	}(loudnessRenderPass)
	inspection, opts, target := exportFixture(t, "punchy-limiter.cicada", "loud")
	if render.UsesScheduledEngine(inspection.score, opts) {
		t.Fatal("the fixture must use the direct renderer")
	}
	search := func(cached bool, lufs float64) (result loudnessFileReport, wav []byte, replays int, err error) {
		var cache *render.MasterCache
		loudnessRenderPass = func(score *notation.Score, opts render.Options, w io.Writer) (render.Report, error) {
			if cached {
				cache = opts.MasterCache
			} else {
				opts.MasterCache = nil
			}
			return render.WAV(score, opts, w)
		}
		path := filepath.Join(t.TempDir(), "search.wav")
		target.LoudnessTarget = lufs
		result, err = renderLoudnessFileReport(inspection.score, path, opts, target, nil)
		wav, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		return result, wav, cache.Replays(), err
	}
	for _, lufs := range []float64{-12, -9, -8, -4} {
		full, fullWAV, _, fullErr := search(false, lufs)
		cached, cachedWAV, replays, cachedErr := search(true, lufs)
		t.Logf("METRIC EXPORT-MASTER-CACHE target=%g passes=%d replays=%d lufs=%.3f gain=%+.3f", lufs, cached.Passes, replays, cached.AchievedLUFS, cached.AppliedGainDB)
		if fmt.Sprint(fullErr) != fmt.Sprint(cachedErr) || !reflect.DeepEqual(full, cached) {
			t.Fatalf("target %g LUFS: cached search differs:\n got %+v (%v)\nwant %+v (%v)", lufs, cached, cachedErr, full, fullErr)
		}
		if !bytes.Equal(fullWAV, cachedWAV) {
			t.Fatalf("target %g LUFS: cached search wrote different audio", lufs)
		}
		if replays != cached.Passes-1 {
			t.Fatalf("target %g LUFS: %d of %d passes replayed, want every pass after the first", lufs, replays, cached.Passes)
		}
	}
}

// The cache only saves time. When its file cannot be created, for example on a
// full disk, the search renders every pass in full instead of failing, and
// returns the report and the audio that it returns with a cache.
func TestExportLoudnessRunsInFullWhenTheMasterCacheCannotBeCreated(t *testing.T) {
	defer func(pass func(*notation.Score, render.Options, io.Writer) (render.Report, error), create func(string) (*render.MasterCache, error)) {
		loudnessRenderPass, newLoudnessMasterCache = pass, create
	}(loudnessRenderPass, newLoudnessMasterCache)
	inspection, opts, target := exportFixture(t, "punchy-limiter.cicada", "loud")
	var cachedPasses []bool
	loudnessRenderPass = func(score *notation.Score, opts render.Options, w io.Writer) (render.Report, error) {
		cachedPasses = append(cachedPasses, opts.MasterCache != nil)
		return render.WAV(score, opts, w)
	}
	search := func() (loudnessFileReport, []byte, error) {
		cachedPasses = nil
		path := filepath.Join(t.TempDir(), "search.wav")
		result, err := renderLoudnessFileReport(inspection.score, path, opts, target, nil)
		wav, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("the search wrote no audio: %v (search error: %v)", readErr, err)
		}
		return result, wav, err
	}
	want, wantWAV, wantErr := search()
	if len(cachedPasses) < 2 || !cachedPasses[0] {
		t.Fatalf("passes of the search with a cache, by whether they had one: %v; want several, the first with one", cachedPasses)
	}
	newLoudnessMasterCache = func(string) (*render.MasterCache, error) {
		return nil, errors.New("no space left on device")
	}
	got, gotWAV, gotErr := search()
	if len(cachedPasses) != got.Passes || got.Passes < 2 {
		t.Fatalf("the search without a cache rendered %d passes and reported %d", len(cachedPasses), got.Passes)
	}
	for pass, cached := range cachedPasses {
		if cached {
			t.Fatalf("pass %d used a cache that could not be created", pass+1)
		}
	}
	if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || !reflect.DeepEqual(got, want) || !bytes.Equal(gotWAV, wantWAV) {
		t.Fatalf("search without a cache differs:\n got %+v (%v)\nwant %+v (%v)", got, gotErr, want, wantErr)
	}
}

var errTestNoSpace = errors.New("no space left on device")

// cacheFilesIn lists the master cache files in dir.
func cacheFilesIn(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, ".cicada-master-cache-*"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// fullDiskWriter takes room bytes and then fails, as a file on a full disk does.
type fullDiskWriter struct {
	w    io.Writer
	room int
}

func (f *fullDiskWriter) Write(p []byte) (int, error) {
	if len(p) <= f.room {
		f.room -= len(p)
		return f.w.Write(p)
	}
	n, err := f.w.Write(p[:f.room])
	f.room = 0
	if err == nil {
		err = errTestNoSpace
	}
	return n, err
}

// diskFaults makes one step of the loudness search fail the way a nearly full
// disk makes it fail, and records what the search does.
//
// The step fails from its from-th call on, but only while a master cache file
// exists: the cache holds the space that the step needs, so the failure ends
// once the search has removed the cache. With always set the failure stays
// after the cache is gone, as it does on a disk that is full without a cache.
// The steps are "render" (the disk fills part way through a pass), "sync" and
// "close" (of the pass's file), "copy" (of the pass to the best-render file)
// and "read" (the stored mix is cut short before a pass replays it).
type diskFaults struct {
	t      *testing.T
	dir    string
	step   string
	from   int
	always bool

	cached     []bool // for each call of the render pass, whether it had a cache
	failures   int    // failures injected so far
	failedPass int    // the pass the first failure came in, counting from 1
	calls      map[string]int
}

func installDiskFaults(t *testing.T, dir, step string, from int, always bool) *diskFaults {
	t.Helper()
	pass, create, open, copyFile := loudnessRenderPass, newLoudnessMasterCache, openLoudnessPassFile, copyLoudnessFile
	t.Cleanup(func() {
		loudnessRenderPass, newLoudnessMasterCache, openLoudnessPassFile, copyLoudnessFile = pass, create, open, copyFile
	})
	d := &diskFaults{t: t, dir: dir, step: step, from: from, always: always, calls: map[string]int{}}
	loudnessRenderPass = func(score *notation.Score, opts render.Options, w io.Writer) (render.Report, error) {
		d.cached = append(d.cached, opts.MasterCache != nil)
		if d.failures > 0 && opts.MasterCache == nil && len(cacheFilesIn(t, dir)) > 0 {
			t.Errorf("pass %d renders without the cache, but its file is still on the disk", len(d.cached))
		}
		if step == "read" && len(d.cached) >= from {
			for _, name := range cacheFilesIn(t, dir) {
				if info, err := os.Stat(name); err == nil && info.Size() > 0 {
					if err := os.Truncate(name, info.Size()/3); err != nil {
						t.Fatal(err)
					}
					d.fail()
				}
			}
		}
		if d.fires("render") {
			w = &fullDiskWriter{w: w, room: 20_000}
		}
		return render.WAV(score, opts, w)
	}
	openLoudnessPassFile = func(path string) (loudnessPassFile, error) {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
		if err != nil {
			return nil, err
		}
		return &faultyPassFile{File: file, faults: d}, nil
	}
	copyLoudnessFile = func(source, destination string) error {
		if d.fires("copy") {
			data, err := os.ReadFile(source)
			if err != nil {
				return err
			}
			// Half of the file arrives before the disk is full.
			if err := os.WriteFile(destination, data[:len(data)/2], 0o600); err != nil {
				return err
			}
			return errTestNoSpace
		}
		return copyRenderFile(source, destination)
	}
	return d
}

// fires counts a call of step and reports whether this call fails.
func (d *diskFaults) fires(step string) bool {
	d.calls[step]++
	if step != d.step || d.calls[step] < d.from || !d.always && len(cacheFilesIn(d.t, d.dir)) == 0 {
		return false
	}
	d.fail()
	return true
}

func (d *diskFaults) fail() {
	d.failures++
	if d.failedPass == 0 {
		d.failedPass = len(d.cached)
	}
}

// faultyPassFile is a pass's candidate file whose sync or close can fail.
type faultyPassFile struct {
	*os.File
	faults *diskFaults
}

func (f *faultyPassFile) Sync() error {
	if f.faults.fires("sync") {
		return errTestNoSpace
	}
	return f.File.Sync()
}

func (f *faultyPassFile) Close() error {
	err := f.File.Close()
	if f.faults.fires("close") {
		return errTestNoSpace
	}
	return err
}

// A finished capture keeps its file for the rest of the search, so on a nearly
// full disk the cache can take the space that a later step needs. A pass, its
// sync or close, the copy to the best-render file, and the read of the stored
// mix must each give the cache up and repeat without it, and the search must
// then return the report and the audio that it returns when nothing fails.
func TestExportLoudnessFreesTheMasterCacheWhenAStepFails(t *testing.T) {
	inspection, opts, target := exportFixture(t, "punchy-limiter.cicada", "loud")
	search := func(t *testing.T, step string, from int) (faults *diskFaults, result loudnessFileReport, wav []byte, err error) {
		dir := t.TempDir()
		faults = installDiskFaults(t, dir, step, from, false)
		path := filepath.Join(dir, "search.wav")
		result, err = renderLoudnessFileReport(inspection.score, path, opts, target, nil)
		wav, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("the search wrote no audio: %v (search error: %v)", readErr, err)
		}
		return faults, result, wav, err
	}
	base, want, wantWAV, wantErr := search(t, "", 0)
	if want.Passes < 3 || len(base.cached) != want.Passes || !base.cached[0] || !base.cached[1] || base.calls["copy"] < 2 {
		t.Fatalf("the search must replay from a finished capture and copy twice: %d passes, cached %v, %d copies", want.Passes, base.cached, base.calls["copy"])
	}
	for _, test := range []struct {
		step string
		from int // the call of the step that fails first
	}{
		{"render", 1}, {"render", 2},
		{"sync", 1}, {"sync", 2},
		{"close", 1}, {"close", 2},
		{"copy", 1}, {"copy", 2},
		{"read", 2},
	} {
		t.Run(fmt.Sprintf("%s call %d", test.step, test.from), func(t *testing.T) {
			faults, got, gotWAV, gotErr := search(t, test.step, test.from)
			if faults.failures == 0 {
				t.Fatal("the test injected no failure")
			}
			if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || !reflect.DeepEqual(got, want) || !bytes.Equal(gotWAV, wantWAV) {
				t.Fatalf("search after a failed %s differs:\n got %+v (%v)\nwant %+v (%v)", test.step, got, gotErr, want, wantErr)
			}
			// Only a pass that failed is rendered twice. Passes up to the failure
			// had the cache; the repeat and every later pass run without it.
			wantPasses := want.Passes + 1
			if test.step == "copy" {
				wantPasses = want.Passes
			}
			if len(faults.cached) != wantPasses {
				t.Fatalf("rendered %d passes, want %d; by cache: %v", len(faults.cached), wantPasses, faults.cached)
			}
			for i, cached := range faults.cached {
				if cached != (i < faults.failedPass) {
					t.Fatalf("passes by cache: %v; the failure came in pass %d, so only the passes up to it can have the cache", faults.cached, faults.failedPass)
				}
			}
			if files := cacheFilesIn(t, faults.dir); len(files) != 0 {
				t.Fatalf("the search left cache files behind: %v", files)
			}
		})
	}
}

// A step that still fails without the cache is repeated once and then fails the
// search. A search that never had a cache does not repeat anything.
func TestExportLoudnessMasterCacheFallbackRepeatsAStepOnce(t *testing.T) {
	inspection, opts, target := exportFixture(t, "punchy-limiter.cicada", "loud")
	run := func(t *testing.T, faults *diskFaults) error {
		t.Helper()
		_, err := renderLoudnessFileReport(inspection.score, filepath.Join(faults.dir, "full.wav"), opts, target, nil)
		if err == nil || !strings.Contains(err.Error(), errTestNoSpace.Error()) {
			t.Fatalf("want the disk-full error, got %v", err)
		}
		if files := cacheFilesIn(t, faults.dir); len(files) != 0 {
			t.Fatalf("the search left cache files behind: %v", files)
		}
		return err
	}
	t.Run("pass", func(t *testing.T) {
		faults := installDiskFaults(t, t.TempDir(), "render", 2, true)
		run(t, faults)
		if want := []bool{true, true, false}; !reflect.DeepEqual(faults.cached, want) {
			t.Fatalf("passes by cache: %v, want %v: pass 2 with the cache, then once without it", faults.cached, want)
		}
	})
	t.Run("copy", func(t *testing.T) {
		faults := installDiskFaults(t, t.TempDir(), "copy", 1, true)
		run(t, faults)
		if faults.calls["copy"] != 2 || !reflect.DeepEqual(faults.cached, []bool{true}) {
			t.Fatalf("%d copies after passes by cache %v, want 2 after one pass with the cache", faults.calls["copy"], faults.cached)
		}
	})
	t.Run("no cache", func(t *testing.T) {
		faults := installDiskFaults(t, t.TempDir(), "render", 1, true)
		newLoudnessMasterCache = func(string) (*render.MasterCache, error) { return nil, errTestNoSpace }
		run(t, faults)
		if !reflect.DeepEqual(faults.cached, []bool{false}) {
			t.Fatalf("passes by cache: %v, want one pass without a cache and no repeat", faults.cached)
		}
	})
}

func TestExportLoudnessMasterLimiterOnScheduledEngine(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "export-loudness", "punchy-limiter.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(source), "track bass acid {", "fx cleanup eq { type = highpass frequency = 30Hz q = 0.707 }\ntrack bass acid { insert = cleanup ", 1)
	path := filepath.Join(t.TempDir(), "scheduled-limiter.cicada")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	inspection, err := inspectScore(path)
	if err != nil {
		t.Fatal(err)
	}
	opts := render.Options{SampleRate: 48000, Bits: 24, Bars: 4, TailSec: 3}
	target := renderTargetOptions{ExportName: "loud", TruePeakMaxDBTP: -1}
	if err := applyExportTarget(inspection.semantic, &opts, &target); err != nil {
		t.Fatal(err)
	}
	if !render.UsesScheduledEngine(inspection.score, opts) {
		t.Fatal("fixture does not use the scheduled engine")
	}
	wav := filepath.Join(t.TempDir(), "scheduled.wav")
	result, err := renderLoudnessFileReport(inspection.score, wav, opts, target, nil)
	if err != nil {
		t.Fatalf("target render: %v (%+v)", err, result)
	}
	if result.GainStage != "master_input" || result.MasterLimiterReductionDB <= 0 || result.MasterLimiterReductionDB > maxExportLimiterReductionDB {
		t.Fatalf("scheduled engine did not limit at the master input: %+v", result)
	}
	if result.TruePeakDBTP > -1 || result.AchievedLUFS < -9.5 || result.AchievedLUFS > -8.5 {
		t.Fatalf("scheduled engine missed the target: %+v", result)
	}
	t.Logf("METRIC EXPORT-SCHEDULED lufs=%.3f true_peak=%.3f gain=%+.2f chain_reduction=%.2f", result.AchievedLUFS, result.TruePeakDBTP, result.AppliedGainDB, result.MasterLimiterReductionDB)
}

// writeScoreVariant writes the loud-intro fixture with text replacements applied.
func writeScoreVariant(t *testing.T, replacements ...string) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("testdata", "export-loudness", "loud-intro-quiet-excerpt.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for i := 0; i+1 < len(replacements); i += 2 {
		if !strings.Contains(text, replacements[i]) {
			t.Fatalf("fixture has no %q", replacements[i])
		}
		text = strings.Replace(text, replacements[i], replacements[i+1], 1)
	}
	path := filepath.Join(t.TempDir(), "score.cicada")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func exportExcerpt(t *testing.T, path string, scheduled bool) (loudnessFileReport, error) {
	t.Helper()
	inspection, err := inspectScore(path)
	if err != nil {
		t.Fatal(err)
	}
	opts := render.Options{SampleRate: 48000, Bits: 24, From: 4, Bars: 2, TailSec: 1}
	target := renderTargetOptions{ExportName: "quiet", TruePeakMaxDBTP: -1}
	if err := applyExportTarget(inspection.semantic, &opts, &target); err != nil {
		t.Fatal(err)
	}
	if got := render.UsesScheduledEngine(inspection.score, opts); got != scheduled {
		t.Fatalf("UsesScheduledEngine = %v, want %v", got, scheduled)
	}
	return renderLoudnessFileReport(inspection.score, filepath.Join(t.TempDir(), "excerpt.wav"), opts, target, nil)
}

// The excerpt is quiet and needs about +10 dB of input gain. The intro would
// drive the limiter past the 6 dB maximum at that gain, but the excerpt does
// not, so the export must reach its target and report the excerpt's own
// limiter reduction.
func testExcerptIgnoresLoudPreroll(t *testing.T, scheduled bool) {
	variants := []string{}
	if scheduled {
		variants = []string{"track bass acid {", "fx cleanup eq { type = highpass frequency = 30Hz q = 0.707 }\ntrack bass acid { insert = cleanup "}
	}
	loud, err := exportExcerpt(t, writeScoreVariant(t, variants...), scheduled)
	t.Logf("METRIC EXPORT-EXCERPT scheduled=%v lufs=%.3f gain=%+.2f chain_reduction=%.2f err=%v", scheduled, loud.AchievedLUFS, loud.AppliedGainDB, loud.MasterLimiterReductionDB, err)
	if err != nil {
		t.Fatalf("excerpt export rejected for limiter reduction outside the excerpt: %v (%+v)", err, loud)
	}
	if loud.AchievedLUFS < -30.5 || loud.AchievedLUFS > -29.5 {
		t.Fatalf("excerpt reached %.2f LUFS, want -30 +/- 0.5", loud.AchievedLUFS)
	}
	if loud.MasterLimiterReductionDB > 1 || loud.LargestLimiterReductionDB > 1 {
		t.Fatalf("reported reduction %.2f dB includes the discarded intro", loud.MasterLimiterReductionDB)
	}

	// The same excerpt after a silent intro is the reference result.
	quiet, err := exportExcerpt(t, writeScoreVariant(t, append(variants, "song { intro*2 gap*2 excerpt*2 }", "song { gap*4 excerpt*2 }")...), scheduled)
	if err != nil {
		t.Fatalf("silent-intro reference failed: %v", err)
	}
	if d := loud.AppliedGainDB - quiet.AppliedGainDB; d < -0.1 || d > 0.1 {
		t.Fatalf("gain %.2f dB differs from the silent-intro reference %.2f dB", loud.AppliedGainDB, quiet.AppliedGainDB)
	}
}

func TestExportLoudnessRangeIgnoresPrerollLimiterReductionDirect(t *testing.T) {
	testExcerptIgnoresLoudPreroll(t, false)
}

func TestExportLoudnessRangeIgnoresPrerollLimiterReductionScheduled(t *testing.T) {
	testExcerptIgnoresLoudPreroll(t, true)
}

// Two chained limiters: the first (-20 dBTP, 1 ms lookahead) is followed by a
// second with a longer lookahead, so the first limiter's reading must be
// delayed by the second's latency before it is counted against the delivered
// frames.
const chainedLimiterMaster = "fx first limiter { ceiling = -12dBTP lookahead = 1ms release = 20ms }\nfx last limiter { ceiling = -1dBTP lookahead = 10ms release = 20ms }\nmaster { level = 6dB insert = tone -> first -> last }"

func chainedLimiterReplacements(scheduled bool, extra ...string) []string {
	r := []string{"fx ceiling limiter { ceiling = -1dBTP lookahead = 3ms release = 100ms }\nmaster { level = 6dB insert = tone -> ceiling }", chainedLimiterMaster}
	if scheduled {
		r = append(r, "track bass acid {", "fx cleanup eq { type = highpass frequency = 30Hz q = 0.707 }\ntrack bass acid { insert = cleanup ")
	}
	r = append(r, "loudness = -30LUFS", "loudness = -22LUFS", "scene excerpt { bass = bass-a drums = rest-d }", "scene excerpt { bass = bass-a drums = beat-a }")
	return append(r, extra...)
}

// chainedExcerptReduction renders the excerpt at a fixed input gain and
// returns the master chain reduction the renderer reports for it.
func chainedExcerptReduction(t *testing.T, path string, scheduled bool) float64 {
	t.Helper()
	inspection, err := inspectScore(path)
	if err != nil {
		t.Fatal(err)
	}
	opts := render.Options{SampleRate: 48000, Bits: 24, From: 4, Bars: 2, TailSec: 1, MasterInputGainDB: -3}
	if got := render.UsesScheduledEngine(inspection.score, opts); got != scheduled {
		t.Fatalf("UsesScheduledEngine = %v, want %v", got, scheduled)
	}
	report, err := render.WAV(inspection.score, opts, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return report.MaxMasterChainLimiterReductionDB
}

// The reported reduction must not depend on what was rendered before the
// range. Exact per-frame alignment of each limiter is checked on the chain
// itself in project.TestMasterChainWindowedReductionAtBothBoundaries.
func testChainedLimitersRangeIgnoresPreroll(t *testing.T, scheduled bool) {
	loud := chainedExcerptReduction(t, writeScoreVariant(t, chainedLimiterReplacements(scheduled)...), scheduled)
	quiet := chainedExcerptReduction(t, writeScoreVariant(t, chainedLimiterReplacements(scheduled, "song { intro*2 gap*2 excerpt*2 }", "song { gap*4 excerpt*2 }")...), scheduled)
	t.Logf("METRIC EXPORT-CHAINED scheduled=%v loud_intro=%.6f silent_intro=%.6f", scheduled, loud, quiet)
	if quiet <= 0 {
		t.Fatalf("excerpt never engaged the limiters (%.6f dB)", quiet)
	}
	// The intro leaves drum noise state behind, so the delivered audio is not
	// bit-identical; the reductions must still agree closely. Counting the
	// intro would raise the loud-intro figure by several dB.
	if d := loud - quiet; d < -0.5 || d > 0.5 {
		t.Fatalf("reduction %.6f dB with a loud intro differs from %.6f dB with a silent one", loud, quiet)
	}
}

func TestExportLoudnessChainedLimitersRangeDirect(t *testing.T) {
	testChainedLimitersRangeIgnoresPreroll(t, false)
}

func TestExportLoudnessChainedLimitersRangeScheduled(t *testing.T) {
	testChainedLimitersRangeIgnoresPreroll(t, true)
}

func TestExportLoudnessChainedLimitersFullSong(t *testing.T) {
	for _, scheduled := range []bool{false, true} {
		path := writeScoreVariant(t, chainedLimiterReplacements(scheduled)...)
		inspection, err := inspectScore(path)
		if err != nil {
			t.Fatal(err)
		}
		opts := render.Options{SampleRate: 48000, Bits: 24, Bars: 6, TailSec: 1}
		target := renderTargetOptions{ExportName: "quiet", TruePeakMaxDBTP: -1}
		if err := applyExportTarget(inspection.semantic, &opts, &target); err != nil {
			t.Fatal(err)
		}
		result, err := renderLoudnessFileReport(inspection.score, filepath.Join(t.TempDir(), "full.wav"), opts, target, nil)
		t.Logf("METRIC EXPORT-CHAINED-FULL scheduled=%v lufs=%.3f gain=%+.2f chain_reduction=%.3f err=%v", scheduled, result.AchievedLUFS, result.AppliedGainDB, result.MasterLimiterReductionDB, err)
		if err != nil || result.MasterLimiterReductionDB <= 0 || result.MasterLimiterReductionDB > maxExportLimiterReductionDB {
			t.Fatalf("full-song chained limiter export: %v (%+v)", err, result)
		}
	}
}

// addDC24 adds a constant offset, in full-scale units, to the 24-bit stereo
// samples of a WAV.
func addDC24(t *testing.T, wav []byte, left, right float64) {
	t.Helper()
	at := bytes.Index(wav, []byte("data"))
	if at < 0 {
		t.Fatal("no data chunk")
	}
	size := int(binary.LittleEndian.Uint32(wav[at+4:]))
	data := wav[at+8 : at+8+size]
	for i := 0; i+6 <= len(data); i += 6 {
		for c, offset := range [2]float64{left, right} {
			b := data[i+3*c : i+3*c+3]
			v := int32(b[0]) | int32(b[1])<<8 | int32(int8(b[2]))<<16
			v += int32(math.Round(offset * 8388607))
			v = max(-8388608, min(8388607, v))
			b[0], b[1], b[2] = byte(v), byte(v>>8), byte(v>>16)
		}
	}
}

// A capped pass can fail the DC check. The loop must render one more pass
// with the new corrections before it stops. DC is injected because the
// fixtures produce none: on the direct renderer it rides the signal and grows
// with the input gain; on the scheduled engine it grows with the square of the
// gain.
func testCappedPassRendersPendingDCCorrection(t *testing.T, scheduled bool) {
	defer func(previous func(*notation.Score, render.Options, io.Writer) (render.Report, error)) {
		loudnessRenderPass = previous
	}(loudnessRenderPass)
	const dc = 0.004
	var gains []float64
	loudnessRenderPass = func(score *notation.Score, opts render.Options, w io.Writer) (render.Report, error) {
		var buf bytes.Buffer
		report, err := render.WAV(score, opts, &buf)
		if err != nil {
			return report, err
		}
		gain := math.Pow(10, opts.MasterInputGainDB/20)
		// The renderer already added its correction; add only the source DC.
		// The scheduled engine applies the correction before the gain, so a
		// source whose DC grows faster than the gain (here with its square)
		// is what leaves a capped pass uncorrected.
		offset := dc * gain
		if scheduled {
			offset = dc * gain * gain
		}
		gains = append(gains, opts.MasterInputGainDB)
		addDC24(t, buf.Bytes(), offset, offset)
		_, err = w.Write(buf.Bytes())
		return report, err
	}
	var replacements []string
	if scheduled {
		// A flat master EQ lets the engine's pre-chain correction reach the
		// output, and no master level keeps that correction's scale equal to
		// the input gain the loop divides by.
		replacements = []string{"level = 6dB ", "", "type = highpass frequency = 25Hz q = 0.707", "type = peak frequency = 1000Hz q = 1 gain = 0dB", "track bass acid {", "fx cleanup eq { type = highpass frequency = 30Hz q = 0.707 }\ntrack bass acid { insert = cleanup "}
	}
	inspection, err := inspectScore(writeScoreVariant(t, replacements...))
	if err != nil {
		t.Fatal(err)
	}
	opts := render.Options{SampleRate: 48000, Bits: 24, From: 4, Bars: 2, TailSec: 1}
	target := renderTargetOptions{ExportName: "quiet", TruePeakMaxDBTP: -1}
	if err := applyExportTarget(inspection.semantic, &opts, &target); err != nil {
		t.Fatal(err)
	}
	target.LoudnessTarget = -29 // needs about +12.4 dB, so the +12 dB cap binds
	if scheduled {
		target.LoudnessTarget = -35 // the same without the 6 dB master level
	}
	result, err := renderLoudnessFileReport(inspection.score, filepath.Join(t.TempDir(), "dc.wav"), opts, target, nil)
	t.Logf("METRIC EXPORT-CAP-DC scheduled=%v gains=%v lufs=%.3f passes=%d dc=%+.6f err=%v", scheduled, gains, result.AchievedLUFS, result.Passes, result.DCCorrection.LeftFS, err)
	if err != nil {
		t.Fatalf("capped pass with DC correction was not rendered: %v (%+v)", err, result)
	}
	if result.AppliedGainDB > maxExportInputGainDB+1e-9 || math.Abs(result.AchievedLUFS-target.LoudnessTarget) > 0.5 {
		t.Fatalf("unexpected result %+v", result)
	}
}

func TestExportLoudnessCappedPassRendersPendingDCCorrectionDirect(t *testing.T) {
	testCappedPassRendersPendingDCCorrection(t, false)
}

func TestExportLoudnessCappedPassRendersPendingDCCorrectionScheduled(t *testing.T) {
	testCappedPassRendersPendingDCCorrection(t, true)
}

// Without a limiter the scheduled engine still adds the export gain ahead of
// the master chain, so the report must say master_input there and post_chain
// on the direct renderer.
func TestExportLoudnessGainStageNamesPlacementPerRenderer(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "export-loudness", "punchy-nolimiter.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	for _, scheduled := range []bool{false, true} {
		text := string(source)
		if scheduled {
			text = strings.Replace(text, "track bass acid {", "fx cleanup eq { type = highpass frequency = 30Hz q = 0.707 }\ntrack bass acid { insert = cleanup ", 1)
		}
		path := filepath.Join(t.TempDir(), "nolimiter.cicada")
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		inspection, err := inspectScore(path)
		if err != nil {
			t.Fatal(err)
		}
		opts := render.Options{SampleRate: 48000, Bits: 24, Bars: 4, TailSec: 3}
		target := renderTargetOptions{ExportName: "loud", TruePeakMaxDBTP: -1}
		if err := applyExportTarget(inspection.semantic, &opts, &target); err != nil {
			t.Fatal(err)
		}
		result, _ := renderLoudnessFileReport(inspection.score, filepath.Join(t.TempDir(), "x.wav"), opts, target, nil)
		want := "post_chain"
		if scheduled {
			want = "master_input"
		}
		if result.GainStage != want || result.limiterMode {
			t.Fatalf("scheduled=%v: gain stage %q limiterMode %v, want %q without limiter mode", scheduled, result.GainStage, result.limiterMode, want)
		}
	}
}

// The limiter ceiling (0 dBTP) sits above the export ceiling (-1 dBTP) and a
// high shelf makes the mix peaky. While the limiter holds the output at its own
// ceiling, subtracting only the true-peak excess barely moves the peak, so the
// search must step by the loudness overage and bracket the safe gain.
func testLimiterCeilingAboveExportCeiling(t *testing.T, scheduled bool) {
	source, err := os.ReadFile(filepath.Join("testdata", "export-loudness", "punchy-limiter.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, r := range [][2]string{
		{"ceiling = -1dBTP", "ceiling = 0dBTP"},
		{"fx ceiling", "fx boost eq { type = low_shelf frequency = 10000Hz q = 0.707 gain = 12dB }\nfx ceiling"},
		{"tone -> ceiling", "tone -> boost -> ceiling"},
		{"-9LUFS", "-14LUFS"},
	} {
		text = strings.Replace(text, r[0], r[1], 1)
	}
	if scheduled {
		text = strings.Replace(text, "track bass acid {", "fx cleanup eq { type = highpass frequency = 30Hz q = 0.707 }\ntrack bass acid { insert = cleanup ", 1)
	}
	path := filepath.Join(t.TempDir(), "boost.cicada")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	inspection, err := inspectScore(path)
	if err != nil {
		t.Fatal(err)
	}
	opts := render.Options{SampleRate: 48000, Bits: 24, Bars: 4, TailSec: 3}
	target := renderTargetOptions{ExportName: "loud", TruePeakMaxDBTP: -1}
	if err := applyExportTarget(inspection.semantic, &opts, &target); err != nil {
		t.Fatal(err)
	}
	if got := render.UsesScheduledEngine(inspection.score, opts); got != scheduled {
		t.Fatalf("UsesScheduledEngine = %v, want %v", got, scheduled)
	}
	result, err := renderLoudnessFileReport(inspection.score, filepath.Join(t.TempDir(), "boost.wav"), opts, target, nil)
	t.Logf("METRIC EXPORT-CEILING-MISMATCH scheduled=%v lufs=%.3f true_peak=%.3f gain=%+.2f passes=%d reduction=%.2f err=%v", scheduled, result.AchievedLUFS, result.TruePeakDBTP, result.AppliedGainDB, result.Passes, result.MasterLimiterReductionDB, err)
	if err != nil {
		t.Fatalf("reachable export failed: %v (%+v)", err, result)
	}
	if result.AchievedLUFS < -14.5 || result.AchievedLUFS > -13.5 || result.TruePeakDBTP > -1 {
		t.Fatalf("missed the target: %+v", result)
	}
	if len(result.Notes) == 0 || !strings.Contains(result.Notes[0], "above the export true-peak ceiling") {
		t.Fatalf("no ceiling mismatch note: %v", result.Notes)
	}
}

func TestExportLoudnessLimiterCeilingAboveExportCeilingDirect(t *testing.T) {
	testLimiterCeilingAboveExportCeiling(t, false)
}

func TestExportLoudnessLimiterCeilingAboveExportCeilingScheduled(t *testing.T) {
	testLimiterCeilingAboveExportCeiling(t, true)
}
