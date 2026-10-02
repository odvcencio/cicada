package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExplainParameterCarriesSceneSettingToRequestedBar(t *testing.T) {
	source := []byte("track bass acid { cutoff = 700Hz }\npattern a acid steps=1 { 1 }\nscene first { bass=a bass.cutoff=800Hz }\nscene carry { bass=keep }\nscene last { bass.cutoff=900Hz }\nsong { first*2 carry last }\n")
	path := filepath.Join(t.TempDir(), "score.cicada")
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := explainParameter(path, "bass.cutoff", "@3.2.4", &output); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{"bass.cutoff at bar 3, beat 2, step 4", "registry default: 600Hz", "track block (bass): 700Hz", "scene first (entered bar 1): 800Hz", "computed: 800Hz"} {
		if !strings.Contains(text, want) {
			t.Fatalf("explain output missing %q:\n%s", want, text)
		}
	}
	output.Reset()
	if err := explainParameter(path, "bass.cutoff", "@4", &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "computed: 900Hz") || !strings.Contains(output.String(), "scene last (entered bar 4): 900Hz") {
		t.Fatalf("later scene setting did not replace the carried value:\n%s", output.String())
	}
}

func TestParseExplainLocationCountsFromOne(t *testing.T) {
	got, err := parseExplainLocation("@2.3.4")
	if err != nil || got != (explainLocation{bar: 2, beat: 3, step: 4}) {
		t.Fatalf("location parse: %+v, %v", got, err)
	}
	for _, value := range []string{"2", "@0", "@1.0", "@1.2.3.4"} {
		if _, err := parseExplainLocation(value); err == nil {
			t.Errorf("accepted invalid location %q", value)
		}
	}
}

func TestExplainPresetLayers(t *testing.T) {
	source := []byte("preset bright { instrument=acid cutoff=900Hz }\ntrack bass bright { cutoff=1100Hz }\npattern notes { 1 . }\nscene verse { bass=notes bass.cutoff=1400Hz }\nsong { verse }\n")
	path := filepath.Join(t.TempDir(), "score.cicada")
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := explainParameter(path, "bass.cutoff", "@1", &output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"registry default: 600Hz", "preset (bright): 900Hz", "track block (bass): 1100Hz", "scene verse (entered bar 1): 1400Hz", "computed: 1400Hz"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %s: %s", want, output.String())
		}
	}
}

func TestExplainAuthoredPresetLayers(t *testing.T) {
	source := []byte("instrument tone { param bite=0.4 voice mono { out=sine(pitch)*bite } }\npreset bright { instrument=tone bite=0.7 }\ntrack bass bright { bite=0.8 }\npattern notes { 1 . }\nscene verse { bass=notes }\nsong { verse }\n")
	path := filepath.Join(t.TempDir(), "score.cicada")
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := explainParameter(path, "bass.bite", "@1", &output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"registry default: none (authored parameter)", "instrument default (tone): 0.4", "preset (bright): 0.7", "track block (bass): 0.8", "computed: 0.8"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %s: %s", want, output.String())
		}
	}
}
