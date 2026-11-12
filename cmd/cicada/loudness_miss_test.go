package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/render"
)

// A missed loudness target names the limit that stopped the search. Before,
// a search that ran out of passes, or whose louder pass broke a limit while
// still too quiet, or whose gain reached +24 dB without a limiter, ended with
// the generic loudness_target_missed (mahogany's finding: "A missed loudness
// target names the limit only sometimes").
func TestMissedLoudnessTargetNamesTheLimit(t *testing.T) {
	target := renderTargetOptions{LoudnessTarget: -14, Tolerance: .5, TruePeakMaxDBTP: -1.5, Loudness: true}
	for _, tc := range []struct {
		name   string
		miss   loudnessMiss
		reason string
		says   []string
		omits  []string
	}{
		{
			name:   "quiet mix without a limiter at the gain maximum",
			miss:   loudnessMiss{target: target, passes: 6, bestLUFS: -23.94, bestGain: maxExportGainDB},
			reason: "loudness_shortfall_at_gain_maximum",
			says:   []string{"+24 dB maximum export gain", "raise the master level or the track levels by at least 9.9 dB"},
		},
		{
			// mahogany's first render of Turn: passes at +9.37 and +9.00 dB
			// broke the limiter maximum, the best safe pass was +7.88 dB.
			name:   "louder pass over the limiter maximum and still too quiet",
			miss:   loudnessMiss{target: target, limiterMode: true, passes: 6, bestLUFS: -16.38, bestGain: 7.88, bestReduction: 4.9, nearestUnsafe: &unsafeLoudnessPass{gain: 9, lufs: -15.4, truePeak: -2.3, reduction: 6.4, overLimiter: true}},
			reason: "loudness_shortfall_at_limiter_maximum",
			says:   []string{"6.0 dB maximum master limiter gain reduction", "the next louder pass, at +9.00 dB, reached -15.40 LUFS but reduced 6.40 dB at the master limiter"},
		},
		{
			name:   "safe and unsafe gains met under the true-peak ceiling",
			miss:   loudnessMiss{target: target, limiterMode: true, passes: 5, bestLUFS: -14.6, bestGain: 8.1, bestReduction: 3, nearestUnsafe: &unsafeLoudnessPass{gain: 8.11, lufs: -14.49, truePeak: -1.4, reduction: 3, overPeak: true}, bracketClosed: true},
			reason: "loudness_shortfall_at_true_peak_ceiling",
			says:   []string{"true-peak ceiling -1.50 dBTP", "peaked at -1.40 dBTP, over the true-peak ceiling"},
		},
		{
			name:   "out of passes with a louder pass that reached the target",
			miss:   loudnessMiss{target: target, limiterMode: true, passes: 6, bestLUFS: -14.9, bestGain: 7, bestReduction: 4, nearestUnsafe: &unsafeLoudnessPass{gain: 8, lufs: -14, truePeak: -1.6, reduction: 6.2, overLimiter: true}},
			reason: "loudness_target_missed_at_pass_limit",
			says:   []string{"missed by 0.90 LU", "6-pass search limit", "the next louder pass, at +8.00 dB", "--loudness-tolerance of at least 0.90 LU"},
		},
		{
			name:   "out of passes above the target",
			miss:   loudnessMiss{target: target, passes: 6, bestLUFS: -12.8, bestGain: 3},
			reason: "loudness_target_missed_at_pass_limit",
			says:   []string{"missed by 1.20 LU", "achieved -12.80 LUFS at +3.00 dB"},
			omits:  []string{"next louder pass"},
		},
		{
			name:   "a quieter unsafe pass is not the limit",
			miss:   loudnessMiss{target: target, limiterMode: true, passes: 6, bestLUFS: -15.2, bestGain: 9, bestReduction: 2, nearestUnsafe: &unsafeLoudnessPass{gain: 8, lufs: -16, reduction: 6.1, overLimiter: true}},
			reason: "loudness_target_missed_at_pass_limit",
			omits:  []string{"next louder pass"},
		},
		{
			name:   "no limit and passes left",
			miss:   loudnessMiss{target: target, passes: 3, bestLUFS: -15.2, bestGain: 4},
			reason: "loudness_target_missed",
			says:   []string{"missed by 1.20 LU", "at +4.00 dB"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := missedLoudnessTarget(tc.miss)
			if err.reason != tc.reason {
				t.Fatalf("reason %q, want %q: %s", err.reason, tc.reason, err.message)
			}
			for _, want := range tc.says {
				if !strings.Contains(err.message, want) {
					t.Errorf("message lacks %q: %s", want, err.message)
				}
			}
			for _, unwanted := range tc.omits {
				if strings.Contains(err.message, unwanted) {
					t.Errorf("message has %q: %s", unwanted, err.message)
				}
			}
			if !containsName(renderErrorCodes, err.reason) {
				t.Errorf("%s is not in renderErrorCodes", err.reason)
			}
		})
	}
}

// A mix 40 dB under its level reaches the +24 dB maximum export gain without
// coming near the true-peak ceiling; the error says so instead of only that
// the target was missed.
func TestExportLoudnessGainMaximumReportsShortfall(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "export-loudness", "punchy-nolimiter.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	quiet := filepath.Join(t.TempDir(), "quiet-nolimiter.cicada")
	if err := os.WriteFile(quiet, []byte(strings.Replace(string(source), "level = 6dB ", "level = -40dB ", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	inspection, err := inspectScore(quiet)
	if err != nil {
		t.Fatal(err)
	}
	opts := render.Options{SampleRate: 48000, Bits: 24, Bars: 4, TailSec: 3}
	target := renderTargetOptions{LoudnessTarget: -14, TruePeakMaxDBTP: -1, Tolerance: .5, Loudness: true}
	result, err := renderLoudnessFileReport(inspection.score, filepath.Join(t.TempDir(), "quiet.wav"), opts, target, nil)
	var shortfall *loudnessShortfallError
	if !errors.As(err, &shortfall) {
		t.Fatalf("want a shortfall error, got %v (%+v)", err, result)
	}
	if shortfall.reason != "loudness_shortfall_at_gain_maximum" || shortfall.limiterCeiling != nil {
		t.Fatalf("shortfall %q (%v), want loudness_shortfall_at_gain_maximum without a limiter remedy", shortfall.reason, err)
	}
	if result.AppliedGainDB != maxExportGainDB || result.TruePeakDBTP > -6 {
		t.Fatalf("gain %+.2f dB, true peak %.2f dBTP: want the +24 dB maximum well under the ceiling", result.AppliedGainDB, result.TruePeakDBTP)
	}
	t.Logf("METRIC EXPORT-GAIN-MAXIMUM lufs=%.3f gain=%+.2f passes=%d err=%v", result.AchievedLUFS, result.AppliedGainDB, result.Passes, err)
}
