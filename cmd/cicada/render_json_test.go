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

// TestRenderJSONLoudnessShortfallRemedyWorks renders scores without a master
// limiter to a target they miss at the true-peak ceiling: with a master
// chain, without a master, in edition 1, and with a full master chain. Where
// the error offers a remedy, applying its intents with cicada apply and,
// separately, its score text each give a score that passes check and meets
// the target. A full chain is offered neither, and the summary says what to
// remove first.
func TestRenderJSONLoudnessShortfallRemedyWorks(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "export-loudness", "punchy-nolimiter.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	base := string(fixture)
	// Without the master's 6 dB the tracks carry it, so the input gain cap
	// is not what stops the render.
	noMaster := strings.NewReplacer("fx tone eq { type = highpass frequency = 25Hz q = 0.707 }\n", "", "master { level = 6dB insert = tone }\n", "", "drive = 0.25 filter = diode }", "drive = 0.25 filter = diode level = 6dB }", "ch_decay = 60ms }", "ch_decay = 60ms level = 6dB }").Replace(base)
	editionOne := strings.Replace(strings.Replace(noMaster, "cicada 2\n", "", 1), "export loud { loudness = -9LUFS true_peak = -1dBTP normalize = off }\n", "", 1)
	// Without the levels the tracks carry, the material needs more gain than
	// the export adds ahead of a limiter: the remedy says so.
	quiet := strings.NewReplacer(" level = 6dB }", " }").Replace(editionOne)
	var full strings.Builder
	chain := []string{"tone"}
	for i := 1; i < 16; i++ {
		fmt.Fprintf(&full, "fx flat%d eq {}\n", i)
		chain = append(chain, fmt.Sprintf("flat%d", i))
	}
	fullChain := strings.Replace(base, "master { level = 6dB insert = tone }\n", full.String()+"master { level = 6dB insert = "+strings.Join(chain, " -> ")+" }\n", 1)
	opts := render.Options{SampleRate: 48000, Bits: 24, Bars: 4, TailSec: 3}
	for _, tc := range []struct {
		name, source, replaces string
		ceilings               []float64
		offered, upgrade       bool
		capBinds               bool
	}{
		{"master chain", base, "master { level = 6dB insert = tone }", []float64{-1, -2}, true, false, false},
		{"no master", noMaster, "", []float64{-1}, true, false, false},
		{"edition 1", editionOne, "", []float64{-1}, true, true, false},
		{"quiet edition 1", quiet, "", []float64{-1}, true, true, true},
		{"full master chain", fullChain, "", []float64{-1}, false, false, false},
	} {
		for _, ceiling := range tc.ceilings {
			t.Run(fmt.Sprintf("%s/%g", tc.name, ceiling), func(t *testing.T) {
				source := []byte(tc.source)
				dir := t.TempDir()
				score := filepath.Join(dir, "loud.cicada")
				if err := os.WriteFile(score, source, 0o644); err != nil {
					t.Fatal(err)
				}
				if document, err := runCheckJSON(t, score); err != nil || !document.Valid {
					t.Fatalf("the fixture does not pass check: %v", err)
				}
				target := renderTargetOptions{LoudnessTarget: -9, TruePeakMaxDBTP: ceiling, Tolerance: 0.5, Loudness: true}
				result, code, stderr := runRenderJSON(t, "render", score, filepath.Join(dir, "short.wav"), opts, &target)
				failure := result.Error
				if code != 1 || failure == nil || failure.Code != "loudness_shortfall_at_true_peak_ceiling" || failure.Remedy == nil {
					t.Fatalf("shortfall: code %d %+v", code, failure)
				}
				remedy := failure.Remedy
				loudness := result.Loudness
				if loudness == nil || loudness.TargetMet || loudness.IntegratedLUFS == nil || len(result.Outputs) != 1 {
					t.Fatalf("loudness: %+v outputs %+v", loudness, result.Outputs)
				}
				if !strings.Contains(stderr, remedy.Summary) || remedy.File != filepath.ToSlash(score) || remedy.Revision != edits.Revision(source) || remedy.Replaces != tc.replaces {
					t.Fatalf("remedy: %+v\n%s", remedy, stderr)
				}
				if !tc.offered {
					if len(remedy.Intents) != 0 || remedy.ScoreText != "" || !strings.Contains(remedy.Summary, "remove a master insert first") {
						t.Fatalf("a full chain was offered a change: %+v", remedy)
					}
					return
				}
				if len(remedy.Intents) != 1 || !strings.Contains(remedy.ScoreText, fmt.Sprintf("ceiling = %gdBTP", ceiling)) {
					t.Fatalf("remedy: %+v", remedy)
				}
				var intent struct {
					ConfirmUpgrade bool `json:"confirmupgrade"`
				}
				if err := json.Unmarshal(remedy.Intents[0], &intent); err != nil || intent.ConfirmUpgrade != tc.upgrade || tc.upgrade != strings.Contains(remedy.Summary, "upgrades it to edition 2") {
					t.Fatalf("upgrade %v: %s; %s", tc.upgrade, remedy.Intents[0], remedy.Summary)
				}
				// The intents, applied with cicada apply at the remedy's revision.
				viaIntents := filepath.Join(dir, "intents.cicada")
				if err := os.WriteFile(viaIntents, source, 0o644); err != nil {
					t.Fatal(err)
				}
				envelope := filepath.Join(dir, "remedy.json")
				if err := os.WriteFile(envelope, []byte(`{"version":1,"revision":"`+remedy.Revision+`","intents":[`+string(remedy.Intents[0])+`]}`), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := applyCommandWithHooks([]string{viaIntents, envelope, "--write"}, &bytes.Buffer{}, nil); err != nil {
					t.Fatalf("the remedy's intents do not apply: %v", err)
				}
				// The score text, as the remedy says: in place of replaces, or added.
				viaText := filepath.Join(dir, "text.cicada")
				fixed := string(source) + remedy.ScoreText + "\n"
				if remedy.Replaces != "" {
					fixed = strings.Replace(string(source), remedy.Replaces, remedy.ScoreText, 1)
				}
				if err := os.WriteFile(viaText, []byte(fixed), 0o644); err != nil {
					t.Fatal(err)
				}
				for _, path := range []string{viaIntents, viaText} {
					if document, err := runCheckJSON(t, path); err != nil || !document.Valid {
						t.Fatalf("%s does not pass check: %v", filepath.Base(path), err)
					}
					after, code, _ := runRenderJSON(t, "render", path, path+".wav", opts, &target)
					if tc.capBinds {
						// The summary said the limiter alone would not do; the
						// render after it stops at the input gain cap.
						if !strings.Contains(remedy.Summary, "the limiter alone will not reach the target") || after.Error == nil || after.Error.Code != "loudness_shortfall_at_input_gain_cap" {
							t.Fatalf("%s: summary %q, after %+v", filepath.Base(path), remedy.Summary, after.Error)
						}
						continue
					}
					if strings.Contains(remedy.Summary, "will not reach") {
						t.Fatalf("the summary says the cap binds: %s", remedy.Summary)
					}
					if code != 0 || !after.OK || after.Loudness == nil || !after.Loudness.TargetMet || *after.Loudness.TruePeakDBTP > ceiling {
						t.Fatalf("%s misses the target: code %d %+v %+v", filepath.Base(path), code, after.Error, after.Loudness)
					}
					t.Logf("%s: %.2f LUFS after the remedy", filepath.Base(path), *after.Loudness.IntegratedLUFS)
				}
			})
		}
	}
}

// TestLimiterRemedyWithoutAMasterDeclaration adds the limiter and a master
// declaration when the score has none.
func TestLimiterRemedyWithoutAMasterDeclaration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain.cicada")
	source := []byte("cicada 2\ntempo 120\nfx ceiling limiter { ceiling = -1dBTP }\ntrack bass acid {}\npattern p { a1 . }\nscene main { bass = p }\nsong { main }\n")
	remedy := limiterRemedy(path, scoreInspection{source: source}, -1.5, 0)
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
