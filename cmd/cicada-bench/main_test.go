package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		{Step: "loudness:medium", MedianMS: 7800, Syntheses: 2},
	}
	outcomes, passed := checkGates(results)
	if passed || len(outcomes) != 4 {
		t.Fatalf("outcomes %+v passed %v", outcomes, passed)
	}
	for _, outcome := range outcomes {
		if want := outcome.Metric != "syntheses"; outcome.Passed != want {
			t.Fatalf("%s %s: passed %v, want %v", outcome.Step, outcome.Metric, outcome.Passed, want)
		}
	}
	results[2].Syntheses = 1
	if _, passed := checkGates(results); !passed {
		t.Fatal("one synthesis should pass the exact gate")
	}
	// A loudness render that takes more than 1.5 plain renders fails.
	results[2].MedianMS = 9100
	if _, passed := checkGates(results); passed {
		t.Fatal("a loudness render at 1.52 plain renders should fail")
	}
	// Without the plain render the ratio is unknown, which fails.
	if outcomes, _ := checkGates(results[2:]); len(outcomes) != 2 || outcomes[1].Passed {
		t.Fatalf("ratio without a plain render: %+v", outcomes)
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
