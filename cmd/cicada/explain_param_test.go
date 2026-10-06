package main

import (
	"crypto/sha256"
	"fmt"
	"m31labs.dev/cicada/internal/testwav"
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

func TestExplainDrumLanePresetLayers(t *testing.T) {
	source := []byte("preset kick { instrument=builtin.bd decay=300ms }\ntrack beat kick { decay=400ms }\npattern notes drums { bd: X... }\nscene verse { beat=notes }\nsong { verse }\n")
	path := filepath.Join(t.TempDir(), "score.cicada")
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := explainParameter(path, "beat.bd_decay", "@1", &output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"registry default:", "preset (kick): 300ms", "track block (beat): 400ms", "computed: 400ms"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %s: %s", want, output.String())
		}
	}
}

func TestExplainHostOnlyParameterLayers(t *testing.T) {
	for _, test := range []struct {
		source, path string
		wants        []string
	}{
		{"instrument tone { octave=2 voice mono { out=sine(pitch) } }\npreset upper { instrument=tone octave=4 }\ntrack lead upper { octave=5 }\npattern notes { 1 . }\nscene verse { lead=notes }\nsong { verse }", "lead.octave", []string{"registry default: 3", "instrument default (tone): 2", "preset (upper): 4", "track block (lead): 5", "computed: 5"}},
		{"cicada 2\nsampler hit { asset=wave root=c3 mode=oneshot voices=8 }\npreset looped { instrument=hit mode=loop voices=4 }\ntrack lead looped { voices=2 }\npattern notes { 1 . }\nscene verse { lead=notes }\nsong { verse }", "lead.voices", []string{"registry default: none (sampler setting)", "instrument default (hit): 8", "preset (looped): 4", "track block (lead): 2", "computed: 2"}},
	} {
		root := t.TempDir()
		path := filepath.Join(root, "score.cicada")
		source := test.source
		if strings.Contains(test.path, "voices") {
			wav := testwav.Bytes(48000, 1, 16, 480, 1)
			if err := os.WriteFile(filepath.Join(root, "wave.wav"), wav, 0600); err != nil {
				t.Fatal(err)
			}
			asset := fmt.Sprintf("asset wave \"wave.wav\" { sha256=\"%x\" format=wav frames=480 rate=48000Hz channels=1 }\n", sha256.Sum256(wav))
			source = strings.Replace(source, "cicada 2\n", "cicada 2\n"+asset, 1)
		}
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		var output strings.Builder
		if err := explainParameter(path, test.path, "@1", &output); err != nil {
			t.Fatal(err)
		}
		for _, want := range test.wants {
			if !strings.Contains(output.String(), want) {
				t.Fatalf("missing %s: %s", want, output.String())
			}
		}
	}
}

func TestExplainPresetAcidOctaveMatchesPlaybackDefault(t *testing.T) {
	source := []byte("cicada 2\npreset round { instrument=acid cutoff=900Hz }\ntrack bass round {}\npattern melody { 1 . }\nscene main { bass=melody }\nsong { main*2 }\n")
	filename := filepath.Join(t.TempDir(), "main.cicada")
	if err := os.WriteFile(filename, source, 0600); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := explainParameter(filename, "bass.octave", "@2", &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "computed: 2") {
		t.Fatalf("octave differs from playback: %s", output.String())
	}
}
