package main

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestReadOutputTakesTheRenderSummary(t *testing.T) {
	var plain Result
	readOutput(&plain, "render", "out.wav: 40 bars from bar 1, 4574770 frames at 48000 Hz, pre-limiter peak 0.4\n")
	if plain.AudioSeconds < 95.3 || plain.AudioSeconds > 95.31 || plain.Passes != 0 {
		t.Fatalf("plain render: %+v", plain)
	}
	var loud Result
	readOutput(&loud, "loudness", "out.wav: 40 bars from bar 1, 4574770 frames at 48000 Hz, achieved -15.05 LUFS, passes 6 (full syntheses 1, cached replays 1, repeats 4), largest\nout.wav: pass 1: gain +0.00 dB, synthesized\n")
	if loud.Passes != 6 || loud.Syntheses != 1 || loud.Replays != 1 || loud.Repeats != 4 {
		t.Fatalf("loudness render: %+v", loud)
	}
	// A binary from before the synthesis count reports only passes.
	var old Result
	readOutput(&old, "loudness", "out.wav: 2 bars from bar 1, 336000 frames at 48000 Hz, passes 6, largest limiter gain reduction 0.00 dB\n")
	if old.Passes != 6 || old.Syntheses != -1 {
		t.Fatalf("older binary: %+v", old)
	}
}

func TestMedian(t *testing.T) {
	if got := median([]float64{1, 2, 9}); got != 2 {
		t.Fatalf("odd: %g", got)
	}
	if got := median([]float64{1, 2, 4, 9}); got != 3 {
		t.Fatalf("even: %g", got)
	}
}

func TestGates(t *testing.T) {
	results := []Result{
		{Step: "help", MedianMS: 5},
		{Step: "render:medium", MedianMS: 6000, Realtime: 15},
		{Step: "loudness:medium", MedianMS: 9100, Syntheses: 2},
	}
	outcomes, passed := checkGates(results)
	if passed || len(outcomes) != 3 {
		t.Fatalf("outcomes %+v passed %v", outcomes, passed)
	}
	for _, outcome := range outcomes {
		if want := outcome.Metric != "syntheses"; outcome.Passed != want {
			t.Fatalf("%s %s: passed %v, want %v", outcome.Step, outcome.Metric, outcome.Passed, want)
		}
	}
	results[2].Syntheses = 1
	if _, passed := checkGates(results); !passed {
		t.Fatal("one synthesis should pass the exact gate; the time ratio is not a gate")
	}
	if ratio := loudnessRatio(results[2], results); ratio < 1.51 || ratio > 1.52 {
		t.Fatalf("loudness ratio %g, want 9100/6000", ratio)
	}
	if ratio := loudnessRatio(results[2], results[2:]); !math.IsNaN(ratio) {
		t.Fatalf("ratio without a plain render: %g", ratio)
	}
}

func TestParseOptions(t *testing.T) {
	o, err := parseOptions([]string{"--steps", "help, render:small", "--runs", "3", "--medium", "x.cicada"})
	if err != nil || len(o.steps) != 2 || o.runs != 3 || o.scores["medium"] != "x.cicada" || o.scores["large"] != defaultScores["large"] {
		t.Fatalf("options %+v, %v", o, err)
	}
	for _, args := range [][]string{{"--steps", "render:huge"}, {"--steps", "fly"}, {"--runs", "0"}, {"extra"}} {
		if _, err := parseOptions(args); err == nil {
			t.Fatalf("%v: want an error", args)
		}
	}
}

func TestWriteIntentsSetsTrackLevels(t *testing.T) {
	dir := t.TempDir()
	score := filepath.Join(dir, "song.cicada")
	if err := os.WriteFile(score, []byte("cicada 2\ntrack bass acid { }\ntrack drums drums {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "intents.json")
	if err := writeIntents(score, path, 3); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Count(text, `"kind":"setparam"`) != 3 || !strings.Contains(text, `"param:bass.level"`) || !strings.Contains(text, `"param:drums.level"`) {
		t.Fatalf("intents: %s", text)
	}
}

func TestDecayStepScoresParse(t *testing.T) {
	for _, step := range []string{"render:held", "render:silent"} {
		if err := validStep(step); err != nil {
			t.Fatal(err)
		}
	}
	if err := validStep("check:held"); err == nil {
		t.Fatal("check:held: want an error, the generated scores are for renders")
	}
	large, err := os.ReadFile(filepath.Join("..", "..", defaultScores["large"]))
	if err != nil {
		t.Fatal(err)
	}
	silent, err := silentScore(large, silentBars)
	if err != nil {
		t.Fatal(err)
	}
	text := string(silent)
	if strings.Contains(text, "\nautomate ") || strings.Count(text, "\nsong {") != 1 || !strings.Contains(text, "  bench_silent*16\n") {
		t.Fatalf("silent score keeps automation or the old song:\n%s", text[max(0, len(text)-600):])
	}
	_, scene, _ := strings.Cut(text, "\nscene bench_silent {\n")
	scene, _, _ = strings.Cut(scene, "}")
	tracks := trackPattern.FindAllStringSubmatch(string(large), -1)
	if len(tracks) < 2 || strings.Count(scene, " = off\n") != len(tracks) {
		t.Fatalf("%d tracks, %d turned off", len(tracks), strings.Count(scene, " = off\n"))
	}
	for name, source := range map[string]string{"held": heldScore, "silent": text} {
		_, diagnostics := notation.Parse([]byte(source))
		for _, d := range diagnostics {
			if d.Severity == "error" {
				t.Errorf("%s score: %v", name, d)
			}
		}
	}
	if _, err := silentScore([]byte("cicada 2\ntrack a acid {}\n"), 4); err == nil {
		t.Fatal("a score without a song block: want an error")
	}
}

func TestRenderJobsStepsAndGates(t *testing.T) {
	if _, err := parseOptions([]string{"--steps", "render-jobs1:large,render-jobs4:medium,render-auto:medium"}); err != nil {
		t.Fatal(err)
	}
	for _, step := range []string{"render-jobs0:large", "render-jobs:large", "render-jobs04:large", "render-jobsx:large"} {
		if _, err := parseOptions([]string{"--steps", step}); err == nil {
			t.Errorf("%s accepted", step)
		}
	}
	serial := Result{Step: "render-jobs1:large", Bars: 8, MedianMS: 6000, SHA256: "aa", Jobs: 1}
	parallel := Result{Step: "render-jobs4:large", Bars: 8, MedianMS: 3000, SHA256: "aa", Jobs: 4}
	if got := speedup(parallel, []Result{serial, parallel}); got != 2 {
		t.Fatalf("speedup %g", got)
	}
	if got := sameAudio(parallel, []Result{serial, parallel}); got != 1 {
		t.Fatalf("same audio %g", got)
	}
	if _, passed := checkGates([]Result{serial, parallel}); !passed {
		t.Fatal("matching hashes and a 2x speed-up failed a gate")
	}
	different := parallel
	different.SHA256 = "bb"
	unstable := parallel
	unstable.SHA256, unstable.UnstableHash = "", true
	slow := parallel
	slow.MedianMS = 5500
	for name, r := range map[string]Result{"different bytes": different, "bytes that change between runs": unstable, "no speed-up": slow} {
		if _, passed := checkGates([]Result{serial, r}); passed {
			t.Errorf("%s passed the gates", name)
		}
	}
	if !math.IsNaN(speedup(parallel, []Result{parallel})) {
		t.Fatal("a speed-up without its serial step is not NaN")
	}
}
