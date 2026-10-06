package recording

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/render"
)

func TestRecordedPackPlaysInScore(t *testing.T) {
	hits, err := Analyze([]Audio{fixture(t)}, Options{Root: 60, Layers: 3})
	if err != nil {
		t.Fatal(err)
	}
	p, err := Build("pencil", hits, 3)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "pack")
	if err := p.Write(dir); err != nil {
		t.Fatal(err)
	}
	prepared, err := instrumentpack.Load(dir, "manifest.json", p.Pin)
	if err != nil {
		t.Fatal(err)
	}
	voice, err := prepared.New(48000)
	if err != nil {
		t.Fatal(err)
	}
	var left, right [128]float32
	if _, err := voice.NoteOn(60, 80); err != nil {
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(50, func() { voice.Render(left[:], right[:]) })
	if allocs != 0 {
		t.Fatalf("recorded instrument render allocations: %f", allocs)
	}
	score, ds := notation.Parse(p.Files["instrument.cicada"])
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatal(d)
		}
	}
	var output bytes.Buffer
	report, err := render.WAV(score, render.Options{AssetDir: dir, SampleRate: 48000, Bits: 32, Bars: 1, TailSec: .5}, &output)
	if err != nil {
		t.Fatal(err)
	}
	if report.OutputPeak <= 0 {
		t.Fatal("silent recorded score")
	}
	bad := p.Manifest
	bad.Assets = append([]Asset(nil), bad.Assets...)
	bad.Assets[0].License = "owner recording"
	bad.Assets[0].SourceURL = "https://example.com/foreign.wav"
	data, _ := json.Marshal(bad)
	if _, err := instrumentpack.DecodeManifest(data); err == nil {
		t.Fatal("ambiguous owner provenance accepted")
	}
	t.Logf("pinned recorded score renders %d frames; render allocations %.0f", report.Frames, allocs)
}
