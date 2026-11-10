package main

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/render"
)

type renderJSONResult struct {
	Schema  string `json:"schema"`
	Command string `json:"command"`
	OK      bool   `json:"ok"`
	FromBar int    `json:"from_bar"`
	Bars    int    `json:"bars"`
	Outputs []struct {
		Path            string  `json:"path"`
		Role            string  `json:"role"`
		SampleRate      int     `json:"sample_rate"`
		BitDepth        int     `json:"bit_depth"`
		Encoding        string  `json:"encoding"`
		Channels        int     `json:"channels"`
		Frames          int64   `json:"frames"`
		DurationSeconds float64 `json:"duration_seconds"`
	} `json:"outputs"`
	Levels      *renderLevelsJSON   `json:"levels"`
	Loudness    *renderLoudnessJSON `json:"loudness"`
	Diagnostics []struct {
		Code string `json:"code"`
	} `json:"diagnostics"`
	Error *renderErrorJSON `json:"error"`
}

func runRenderJSON(t *testing.T, command, score, output string, opts render.Options, target *renderTargetOptions) (renderJSONResult, int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := renderJSONCommand(command, score, output, opts, target, &stdout, &stderr)
	var result renderJSONResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("%s --json stdout is not one JSON document: %v\n%s", command, err, stdout.String())
	}
	if result.Schema != renderSchema || result.Command != command {
		t.Fatalf("header: %+v", result)
	}
	return result, code, stderr.String()
}

func TestRenderJSONDescribesTheWrittenFile(t *testing.T) {
	output := filepath.Join(t.TempDir(), "mix.wav")
	opts := render.Options{SampleRate: 44100, Bits: 16, Bars: 2, TailSec: 0.5, Block: 4096}
	result, code, _ := runRenderJSON(t, "render", filepath.Join("..", "..", "examples", "first-acid.cicada"), output, opts, nil)
	if code != 0 || !result.OK || result.Error != nil || result.FromBar != 1 || result.Bars != 2 || result.Levels == nil || result.Loudness != nil {
		t.Fatalf("render --json: code %d %+v", code, result)
	}
	if len(result.Outputs) != 1 {
		t.Fatalf("outputs: %+v", result.Outputs)
	}
	out := result.Outputs[0]
	verified, err := render.VerifyWAV(output, render.VerifyOptions{SampleRate: 44100, Bits: 16, Bars: 2, TailSec: 0.5, PeakMaxDB: 100, DCMaxDB: 100})
	if err != nil {
		t.Fatal(err)
	}
	if out.Path != output || out.Role != "mix" || out.SampleRate != 44100 || out.BitDepth != 16 || out.Encoding != "pcm" || out.Channels != 2 || out.Frames != verified.Frames {
		t.Fatalf("output %+v, verify-wav frames %d", out, verified.Frames)
	}
	if math.Abs(out.DurationSeconds-float64(out.Frames)/44100) > 1e-6 {
		t.Fatalf("duration %.6f for %d frames", out.DurationSeconds, out.Frames)
	}
}

func TestStemsJSONListsEveryStem(t *testing.T) {
	output := filepath.Join(t.TempDir(), "stems")
	opts := render.Options{SampleRate: 48000, Bits: 32, Bars: 1, TailSec: 0.25, Block: 4096}
	result, code, _ := runRenderJSON(t, "stems", filepath.Join("..", "..", "examples", "first-acid.cicada"), output, opts, nil)
	if code != 0 || !result.OK {
		t.Fatalf("stems --json: code %d %+v", code, result)
	}
	entries, err := os.ReadDir(output)
	if err != nil {
		t.Fatal(err)
	}
	wavs := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".wav") {
			wavs++
		}
	}
	if len(result.Outputs) != wavs || wavs == 0 {
		t.Fatalf("%d outputs for %d stem files", len(result.Outputs), wavs)
	}
	for _, out := range result.Outputs {
		if !strings.HasPrefix(out.Role, "stem:") || out.Encoding != "float" || out.BitDepth != 32 || out.Frames != result.Outputs[0].Frames {
			t.Fatalf("stem %+v", out)
		}
	}
}

// TestRenderJSONLoudnessShortfallNamesTheRemedy renders the score without a
// master limiter to a target it misses at the true-peak ceiling. The JSON and
// the message name the master limiter, which punchy-limiter.cicada (the same
// score with that limiter) shows reaches the target.
func TestRenderJSONLoudnessShortfallNamesTheRemedy(t *testing.T) {
	_, opts, target := exportFixture(t, "punchy-nolimiter.cicada", "loud")
	output := filepath.Join(t.TempDir(), "short.wav")
	result, code, stderr := runRenderJSON(t, "render", filepath.Join("testdata", "export-loudness", "punchy-nolimiter.cicada"), output, opts, &target)
	if code != 1 || result.OK || result.Error == nil || result.Error.Reason != "loudness_shortfall_at_true_peak_ceiling" || result.Error.Remedy != masterLimiterRemedy {
		t.Fatalf("shortfall: code %d %+v", code, result.Error)
	}
	if !strings.Contains(stderr, masterLimiterRemedy) {
		t.Fatalf("the message lacks the remedy:\n%s", stderr)
	}
	loudness := result.Loudness
	if loudness == nil || loudness.TargetMet || loudness.IntegratedLUFS == nil || loudness.TruePeakDBTP == nil || loudness.AppliedGainDB == nil || loudness.TargetLUFS != -9 {
		t.Fatalf("loudness: %+v", loudness)
	}
	if len(result.Outputs) != 1 || result.Outputs[0].Path != output {
		t.Fatalf("the best pass is written but not listed: %+v", result.Outputs)
	}
}

func TestRenderJSONReportsInvalidScores(t *testing.T) {
	dir := t.TempDir()
	score := filepath.Join(dir, "bad.cicada")
	if err := os.WriteFile(score, []byte("cicada 2\ntempo 120\ntrack bass acid { cutoff = 9 }\npattern riff { a1 . }\nscene main { bass = riff }\nsong { main }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, code, _ := runRenderJSON(t, "render", score, filepath.Join(dir, "out.wav"), render.Options{SampleRate: 48000, Bits: 24}, nil)
	if code != 1 || result.OK || result.Error == nil || result.Error.Reason != "invalid_score" || len(result.Diagnostics) == 0 || len(result.Outputs) != 0 {
		t.Fatalf("invalid score: code %d %+v", code, result)
	}
}
