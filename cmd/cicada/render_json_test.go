package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
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

// TestRenderJSONLoudnessShortfallRemedyWorks renders the score without a
// master limiter to targets it misses at the true-peak ceiling, for two
// ceilings. The error carries the structured remedy for that ceiling; adding
// its score text exactly as it says makes the same render meet the target.
func TestRenderJSONLoudnessShortfallRemedyWorks(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "export-loudness", "punchy-nolimiter.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	opts := render.Options{SampleRate: 48000, Bits: 24, Bars: 4, TailSec: 3}
	for _, ceiling := range []float64{-1, -2} {
		t.Run(fmt.Sprint(ceiling), func(t *testing.T) {
			dir := t.TempDir()
			score := filepath.Join(dir, "loud.cicada")
			if err := os.WriteFile(score, source, 0o644); err != nil {
				t.Fatal(err)
			}
			target := renderTargetOptions{LoudnessTarget: -9, TruePeakMaxDBTP: ceiling, Tolerance: 0.5, Loudness: true}
			result, code, stderr := runRenderJSON(t, "render", score, filepath.Join(dir, "short.wav"), opts, &target)
			failure := result.Error
			if code != 1 || failure == nil || failure.Code != "loudness_shortfall_at_true_peak_ceiling" || failure.Remedy == nil {
				t.Fatalf("shortfall: code %d %+v", code, failure)
			}
			remedy := failure.Remedy
			// The remedy's intents, applied with cicada apply at its revision,
			// make a fresh copy of the score meet the target too.
			viaIntents := filepath.Join(dir, "intents.cicada")
			if err := os.WriteFile(viaIntents, source, 0o644); err != nil {
				t.Fatal(err)
			}
			envelope := filepath.Join(dir, "remedy.json")
			intents := make([]string, len(remedy.Intents))
			for i, intent := range remedy.Intents {
				intents[i] = string(intent)
			}
			if err := os.WriteFile(envelope, []byte(`{"version":1,"revision":"`+remedy.Revision+`","intents":[`+strings.Join(intents, ",")+`]}`), 0o644); err != nil {
				t.Fatal(err)
			}
			if len(remedy.Intents) != 1 {
				t.Fatalf("remedy intents: %s", intents)
			}
			if err := applyCommandWithHooks([]string{viaIntents, envelope, "--write"}, &bytes.Buffer{}, nil); err != nil {
				t.Fatalf("the remedy's intents do not apply: %v", err)
			}
			byIntents, code, _ := runRenderJSON(t, "render", viaIntents, filepath.Join(dir, "intents.wav"), opts, &target)
			if code != 0 || byIntents.Loudness == nil || !byIntents.Loudness.TargetMet {
				t.Fatalf("after the remedy's intents: code %d %+v %+v", code, byIntents.Error, byIntents.Loudness)
			}
			if !strings.Contains(remedy.ScoreText, fmt.Sprintf("ceiling = %gdBTP", ceiling)) || remedy.Summary != limiterRemedySummary(ceiling) || !strings.Contains(stderr, remedy.Summary) {
				t.Fatalf("remedy: %+v\n%s", remedy, stderr)
			}
			if remedy.File != filepath.ToSlash(score) || remedy.Replaces != "master { level = 6dB insert = tone }" || remedy.Revision != edits.Revision(source) || strings.Count(string(source), remedy.Replaces) != 1 {
				t.Fatalf("remedy target: %+v", remedy)
			}
			loudness := result.Loudness
			if loudness == nil || loudness.TargetMet || loudness.IntegratedLUFS == nil || len(result.Outputs) != 1 {
				t.Fatalf("loudness: %+v outputs %+v", loudness, result.Outputs)
			}
			fixed := strings.Replace(string(source), remedy.Replaces, remedy.ScoreText, 1)
			if err := os.WriteFile(score, []byte(fixed), 0o644); err != nil {
				t.Fatal(err)
			}
			// An agent that compares revisions sees that the file has changed
			// since the remedy was computed, so the remedy no longer holds.
			if now, err := os.ReadFile(score); err != nil || edits.Revision(now) == remedy.Revision {
				t.Fatalf("the remedy's revision does not tell a changed file: %v", err)
			}
			result, code, _ = runRenderJSON(t, "render", score, filepath.Join(dir, "fixed.wav"), opts, &target)
			if code != 0 || !result.OK || result.Loudness == nil || !result.Loudness.TargetMet || *result.Loudness.TruePeakDBTP > ceiling {
				t.Fatalf("after the remedy: code %d %+v %+v", code, result.Error, result.Loudness)
			}
			t.Logf("ceiling %g dBTP: %.2f LUFS after the remedy", ceiling, *result.Loudness.IntegratedLUFS)
		})
	}
}

// TestLimiterRemedyWithoutAMasterDeclaration adds the limiter and a master
// declaration when the score has none.
func TestLimiterRemedyWithoutAMasterDeclaration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain.cicada")
	source := []byte("cicada 2\ntempo 120\nfx ceiling limiter { ceiling = -1dBTP }\ntrack bass acid {}\npattern p { a1 . }\nscene main { bass = p }\nsong { main }\n")
	remedy := limiterRemedy(path, scoreInspection{source: source}, -1.5)
	if remedy.Replaces != "" || remedy.ScoreText != "fx ceiling-2 limiter { ceiling = -1.5dBTP }\nmaster { insert = ceiling-2 }" || len(remedy.Intents) != 1 || !strings.Contains(string(remedy.Intents[0]), `"name":"ceiling-2"`) {
		t.Fatalf("remedy: %+v", remedy)
	}
}

func TestRenderJSONReportsInvalidScores(t *testing.T) {
	dir := t.TempDir()
	score := filepath.Join(dir, "bad.cicada")
	if err := os.WriteFile(score, []byte("cicada 2\ntempo 120\ntrack bass acid { cutoff = 9 }\npattern riff { a1 . }\nscene main { bass = riff }\nsong { main }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, code, _ := runRenderJSON(t, "render", score, filepath.Join(dir, "out.wav"), render.Options{SampleRate: 48000, Bits: 24}, nil)
	if code != 1 || result.OK || result.Error == nil || result.Error.Code != "invalid_score" || len(result.Diagnostics) == 0 || len(result.Outputs) != 0 {
		t.Fatalf("invalid score: code %d %+v", code, result)
	}
}
