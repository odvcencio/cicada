package modalfit

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/host/recording"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/render"
)

func recordedModel(t *testing.T) Model {
	t.Helper()
	data, err := os.ReadFile("../recording/testdata/pencil-taps.wav")
	if err != nil {
		t.Fatal(err)
	}
	audio, err := recording.DecodeWAV(data)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := recording.Analyze([]recording.Audio{audio}, recording.Options{Root: 60, Layers: 3})
	if err != nil {
		t.Fatal(err)
	}
	m, err := Fit(hits[10].PCM, hits[10].Rate, hits[10].SourceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRecordedModelPinnedScoreAndFullKeyboard(t *testing.T) {
	start := time.Now()
	m := recordedModel(t)
	p, err := m.Build("pencil_model")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Manifest.Zones) != 15 || !strings.Contains(p.Manifest.Description, recording.Digest(p.Files["model.json"])) {
		t.Fatal("missing pinned model")
	}
	dir := filepath.Join(t.TempDir(), "modeled")
	if err = p.Write(dir); err != nil {
		t.Fatal(err)
	}
	prepared, err := instrumentpack.Load(dir, "manifest.json", p.Pin)
	if err != nil {
		t.Fatal(err)
	}
	v, err := prepared.New(48000)
	if err != nil {
		t.Fatal(err)
	}
	var left, right [128]float32
	for note := 0; note <= 127; note++ {
		v.Reset()
		if n, err := v.NoteOn(uint8(note), 80); err != nil || n.ID == 0 {
			t.Fatal("unmapped modeled note", note, n, err)
		}
		v.Render(left[:], right[:])
	}
	score, ds := notation.Parse(p.Files["instrument.cicada"])
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatal(d)
		}
	}
	var wav bytes.Buffer
	report, err := render.WAV(score, render.Options{AssetDir: dir, SampleRate: 48000, Bits: 32, Bars: 1, TailSec: .5}, &wav)
	if err != nil || report.OutputPeak <= 0 {
		t.Fatal("modeled score is silent", err)
	}
	p2, err := m.Build("pencil_model")
	if err != nil || p2.Pin != p.Pin {
		t.Fatal("nondeterministic model pack", err)
	}
	t.Logf("%d measured modes; root %.2f Hz; fit/bake/offline score in %s; all 128 MIDI notes mapped", len(m.Modes), m.RootHz, time.Since(start))
}
