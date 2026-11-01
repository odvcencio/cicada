package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
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
